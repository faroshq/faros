/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dataplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	persistentExecSessionRetention = 10 * time.Minute
	persistentExecSessionCapacity  = 256
	persistentExecStartupGrace     = 15 * time.Second
	persistentExecAgentBodyLimit   = 2 << 20
)

var executionGroupResource = schema.GroupResource{Group: "infrastructure.kedge.faros.sh", Resource: "executions"}

type execAgentRequest struct {
	Argv           []string `json:"argv"`
	WorkDir        string   `json:"workDir,omitempty"`
	TimeoutMS      int      `json:"timeoutMs,omitempty"`
	MaxOutputBytes int      `json:"maxOutputBytes,omitempty"`
	SourceRevision uint64   `json:"sourceRevision,omitempty"`
	SourceDigest   string   `json:"sourceDigest,omitempty"`
}

type execAgentResponse struct {
	Phase           string `json:"phase"`
	ExitCode        int32  `json:"exitCode"`
	TimedOut        bool   `json:"timedOut,omitempty"`
	Cancelled       bool   `json:"cancelled,omitempty"`
	Stdout          string `json:"stdout,omitempty"`
	Stderr          string `json:"stderr,omitempty"`
	StdoutTruncated bool   `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool   `json:"stderrTruncated,omitempty"`
	Error           string `json:"error,omitempty"`
}

// PersistentExecutor sends direct-argv requests to the live component's
// kedge-dev-agent. Sync owns the persistent PVC and the agent verifies the
// synchronized revision/digest before launching a child in that workspace.
type PersistentExecutor struct {
	runtime Runtime
	now     func() time.Time

	mu       sync.Mutex
	sessions map[string]*persistentExecSession
	requests map[string]string
}

type persistentExecSession struct {
	mu sync.RWMutex

	id          string
	requestID   string
	fingerprint string
	workspace   string
	resource    string
	name        string
	component   string
	callerKey   string
	result      ExecResult
	outputLimit int
	completedAt time.Time
	cancel      context.CancelFunc
}

// NewPersistentExecutor constructs an executor using the provider-owned
// runtime transport. The runtime credential never crosses into App Studio or
// the dev-agent; only the per-instance control token is sent upstream.
func NewPersistentExecutor(runtime Runtime) (*PersistentExecutor, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime is required for persistent exec")
	}
	return &PersistentExecutor{
		runtime:  runtime,
		now:      time.Now,
		sessions: map[string]*persistentExecSession{},
		requests: map[string]string{},
	}, nil
}

func (e *PersistentExecutor) Start(_ context.Context, call ExecCall) (ExecResult, error) {
	if e == nil || e.runtime == nil {
		return ExecResult{}, fmt.Errorf("persistent executor is unavailable")
	}
	if err := validatePersistentExecCall(call); err != nil {
		return ExecResult{}, err
	}
	fingerprint, err := persistentExecCallFingerprint(call)
	if err != nil {
		return ExecResult{}, err
	}
	sessionID := execSessionID(call)
	requestKey := execRequestKey(call)

	e.mu.Lock()
	e.pruneSessionsLocked()
	if existingID := e.requests[requestKey]; existingID != "" {
		existing := e.sessions[existingID]
		if existing == nil {
			delete(e.requests, requestKey)
		} else if existing.fingerprint != fingerprint {
			e.mu.Unlock()
			return ExecResult{}, fmt.Errorf("idempotency key was already used for a different execution request")
		} else {
			e.mu.Unlock()
			return existing.snapshot(), nil
		}
	}
	if len(e.sessions) >= persistentExecSessionCapacity {
		e.mu.Unlock()
		return ExecResult{}, fmt.Errorf("persistent executor session capacity is exhausted")
	}
	runCtx, cancel := context.WithTimeout(context.Background(), persistentExecRunDeadline(call))
	session := &persistentExecSession{
		id: sessionID, requestID: call.Request.RequestID, fingerprint: fingerprint,
		workspace: call.Workspace, resource: call.Resource, name: call.Name,
		component: call.Component, callerKey: call.CallerKey,
		result:      ExecResult{SessionID: sessionID, RequestID: call.Request.RequestID, State: "queued"},
		outputLimit: execOutputLimit(call), cancel: cancel,
	}
	e.sessions[sessionID] = session
	e.requests[requestKey] = sessionID
	e.mu.Unlock()

	go e.run(runCtx, session, call)
	return session.snapshot(), nil
}

func (e *PersistentExecutor) Poll(_ context.Context, call ExecCall) (ExecResult, error) {
	session, err := e.sessionFor(call)
	if err != nil {
		return ExecResult{}, err
	}
	return session.snapshot(), nil
}

func (e *PersistentExecutor) Cancel(_ context.Context, call ExecCall) (ExecResult, error) {
	session, err := e.sessionFor(call)
	if err != nil {
		return ExecResult{}, err
	}
	session.mu.Lock()
	if !execTerminalState(session.result.State) {
		session.result.State = "canceled"
		session.completedAt = e.now()
	}
	cancel := session.cancel
	result := session.result
	session.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return result, nil
}

func (e *PersistentExecutor) sessionFor(call ExecCall) (*persistentExecSession, error) {
	id := strings.TrimSpace(call.Request.SessionID)
	e.mu.Lock()
	e.pruneSessionsLocked()
	session := e.sessions[id]
	e.mu.Unlock()
	if session == nil {
		return nil, apierrors.NewNotFound(executionGroupResource, id)
	}
	if session.workspace != call.Workspace || session.resource != call.Resource || session.name != call.Name || session.component != call.Component ||
		session.callerKey == "" || session.callerKey != call.CallerKey {
		return nil, apierrors.NewForbidden(executionGroupResource, id, fmt.Errorf("execution session does not belong to this component"))
	}
	return session, nil
}

func (s *persistentExecSession) snapshot() ExecResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.result
}

func (e *PersistentExecutor) run(ctx context.Context, session *persistentExecSession, call ExecCall) {
	e.setState(session, "running")
	limits, _ := limitsForCapability(call.Capability)
	request := execAgentRequest{
		Argv: call.Request.Argv, WorkDir: call.Request.Workdir,
		TimeoutMS: int(execTimeoutSeconds(call)) * 1000, MaxOutputBytes: limits.outputBytes,
		SourceRevision: call.Request.SourceRevision, SourceDigest: call.Request.SourceDigest,
	}
	response, err := e.execute(ctx, call.ControlTarget, request)
	if err != nil {
		state := "failed"
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			state = "canceled"
		}
		e.finish(session, ExecResult{State: state, Stderr: err.Error()})
		return
	}
	e.finish(session, execResultFromAgent(response))
}

func (e *PersistentExecutor) setState(session *persistentExecSession, state string) {
	session.mu.Lock()
	if !execTerminalState(session.result.State) {
		session.result.State = state
	}
	session.mu.Unlock()
}

func (e *PersistentExecutor) finish(session *persistentExecSession, result ExecResult) {
	session.mu.Lock()
	if execTerminalState(session.result.State) && session.result.State == "canceled" {
		session.mu.Unlock()
		return
	}
	result.SessionID = session.id
	result.RequestID = session.requestID
	result = boundExecResult(result, session.outputLimit)
	session.result = result
	session.completedAt = e.now()
	session.mu.Unlock()
}

func (e *PersistentExecutor) pruneSessionsLocked() {
	cutoff := e.now().Add(-persistentExecSessionRetention)
	for id, session := range e.sessions {
		session.mu.RLock()
		completedAt := session.completedAt
		requestID := session.requestID
		session.mu.RUnlock()
		if !completedAt.IsZero() && completedAt.Before(cutoff) {
			delete(e.sessions, id)
			for key, mapped := range e.requests {
				if mapped == id || strings.HasSuffix(key, "\x00"+requestID) {
					delete(e.requests, key)
				}
			}
		}
	}
}

func (e *PersistentExecutor) execute(ctx context.Context, target ResolvedTarget, input execAgentRequest) (execAgentResponse, error) {
	if strings.TrimSpace(target.ServiceName) == "" || strings.TrimSpace(target.ServiceNamespace) == "" || strings.TrimSpace(target.ServicePort) == "" {
		return execAgentResponse{}, fmt.Errorf("persistent exec control Service is unavailable")
	}
	transport, err := e.runtime.Transport()
	if err != nil {
		return execAgentResponse{}, fmt.Errorf("runtime transport unavailable: %w", err)
	}
	base, err := url.Parse(e.runtime.Host())
	if err != nil || base.Scheme == "" || base.Host == "" {
		return execAgentResponse{}, fmt.Errorf("invalid runtime host: %v", err)
	}
	token := ""
	if target.TokenSecretName != "" {
		token, err = e.runtime.ControlToken(ctx, target.TokenSecretNamespace, target.TokenSecretName)
		if err != nil {
			return execAgentResponse{}, fmt.Errorf("control token unavailable: %w", err)
		}
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return execAgentResponse{}, fmt.Errorf("encode persistent exec request: %w", err)
	}
	requestURL := *base
	requestURL.Path = serviceProxyPath(target, "")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), strings.NewReader(string(payload)))
	if err != nil {
		return execAgentResponse{}, fmt.Errorf("build persistent exec request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Del("Authorization")
	if token != "" {
		req.Header.Set(controlTokenHeader, token)
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return execAgentResponse{}, fmt.Errorf("persistent dev-agent request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, persistentExecAgentBodyLimit))
	if err != nil {
		return execAgentResponse{}, fmt.Errorf("read persistent dev-agent response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if len(message) > 2048 {
			message = message[:2048] + "..."
		}
		return execAgentResponse{}, fmt.Errorf("persistent dev-agent returned %s: %s", resp.Status, message)
	}
	var response execAgentResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return execAgentResponse{}, fmt.Errorf("decode persistent dev-agent response: %w", err)
	}
	return response, nil
}

func validatePersistentExecCall(call ExecCall) error {
	if call.Request.Action != ExecActionStart || strings.TrimSpace(call.IdempotencyKey) == "" {
		return fmt.Errorf("persistent executor start requires a start request and idempotency key")
	}
	if strings.TrimSpace(call.RuntimeNamespace) == "" || strings.TrimSpace(call.CallerKey) == "" {
		return fmt.Errorf("persistent executor runtime namespace and caller binding are required")
	}
	if !path.IsAbs(call.WorkingDir) || path.Clean(call.WorkingDir) == "/" {
		return fmt.Errorf("persistent executor working directory must be an absolute non-root path")
	}
	if call.Request.SourceRevision == 0 {
		return fmt.Errorf("sourceRevision is required for persistent execution")
	}
	if strings.TrimSpace(call.Request.SourceDigest) == "" {
		return fmt.Errorf("sourceDigest is required for persistent execution")
	}
	digest := strings.TrimPrefix(strings.TrimSpace(call.Request.SourceDigest), "sha256:")
	if len(digest) != sha256.Size*2 {
		return fmt.Errorf("sourceDigest must be a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("sourceDigest must be a SHA-256 hex digest")
	}
	if call.ControlTarget.ServiceName == "" || call.ControlTarget.ServicePort == "" {
		return fmt.Errorf("persistent executor control Service is unavailable")
	}
	if call.ControlTarget.TokenSecretNamespace == "" || call.ControlTarget.TokenSecretName == "" {
		return fmt.Errorf("persistent executor control token Secret is unavailable")
	}
	return nil
}

func persistentExecCallFingerprint(call ExecCall) (string, error) {
	payload, err := json.Marshal(struct {
		Namespace, Resource, Name, Component, WorkingDir, WorkspacePath, Digest string
		Revision                                                                uint64
		Argv                                                                    []string
		Workdir                                                                 string
		Timeout                                                                 int32
		Target                                                                  ResolvedTarget
	}{call.RuntimeNamespace, call.Resource, call.Name, call.Component, call.WorkingDir, call.WorkspacePath, call.Request.SourceDigest, call.Request.SourceRevision, call.Request.Argv, call.Request.Workdir, call.Request.TimeoutSeconds, call.ControlTarget})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func execSessionID(call ExecCall) string {
	sum := sha256.Sum256([]byte(execRequestKey(call)))
	return hex.EncodeToString(sum[:16])
}

func execRequestKey(call ExecCall) string {
	return strings.Join([]string{call.CallerKey, call.Workspace, call.Resource, call.Name, call.Component, call.IdempotencyKey}, "\x00")
}

func execTimeoutSeconds(call ExecCall) int32 {
	if call.Request.TimeoutSeconds > 0 {
		return call.Request.TimeoutSeconds
	}
	limits, _ := limitsForCapability(call.Capability)
	return limits.timeoutSeconds
}

func execOutputLimit(call ExecCall) int {
	limits, _ := limitsForCapability(call.Capability)
	return limits.outputBytes
}

func execTerminalState(state string) bool {
	switch state {
	case "succeeded", "failed", "canceled", "timed_out":
		return true
	default:
		return false
	}
}

func execResultFromAgent(response execAgentResponse) ExecResult {
	state := "failed"
	switch {
	case response.Cancelled:
		state = "canceled"
	case response.TimedOut:
		state = "timed_out"
	case response.Phase == "completed" && response.ExitCode == 0:
		state = "succeeded"
	case response.Phase == "completed":
		state = "failed"
	case response.Phase == "cancelled" || response.Phase == "canceled":
		state = "canceled"
	case response.Phase == "timed_out":
		state = "timed_out"
	}
	stderr := response.Stderr
	if response.Error != "" {
		if stderr != "" {
			stderr += "\n"
		}
		stderr += response.Error
	}
	exitCode := response.ExitCode
	return ExecResult{
		State: state, ExitCode: &exitCode, Stdout: response.Stdout, Stderr: stderr,
		Truncated: response.StdoutTruncated || response.StderrTruncated,
	}
}

func persistentExecRunDeadline(call ExecCall) time.Duration {
	return time.Duration(execTimeoutSeconds(call))*time.Second + persistentExecStartupGrace
}
