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

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino-examples/adk/common/tool"
	"github.com/cloudwego/eino-examples/adk/common/tool/graphtool"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/faroshq/provider-app-studio/workspace"
)

const (
	projectAssistantExecDefaultTimeout   = 30
	projectAssistantExecMaxTimeout       = 120
	projectAssistantExecMaxArgv          = 32
	projectAssistantExecMaxArgBytes      = 256
	projectAssistantExecMaxWorkdir       = 256
	projectAssistantExecMaxSnapshot      = 8 << 20
	projectAssistantExecMaxOutput        = 1 << 20
	projectAssistantExecPollInterval     = 250 * time.Millisecond
	projectAssistantExecPollTimeout      = 2 * time.Minute
	projectAssistantExecCancelTimeout    = 5 * time.Second
	projectAssistantExecSnapshotAttempts = 3
)

var errProjectAssistantExecRevisionChanged = errors.New("workspace mutation revision changed while preparing the execution snapshot")

type projectAssistantExecCommandInput struct {
	Component      string   `json:"component"`
	Argv           []string `json:"argv"`
	Workdir        string   `json:"workdir,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

type projectSandboxExecFile struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable,omitempty"`
}

type projectAssistantExecSnapshotEntry struct {
	path string
	file projectSandboxExecFile
}

// projectSandboxExecRequest is the typed infrastructure data-plane protocol.
// The executor receives an immutable source snapshot and no App Studio
// credentials, environment, image, network, PTY, or writeback capability.
type projectSandboxExecRequest struct {
	Action         string                   `json:"action"`
	SessionID      string                   `json:"sessionID,omitempty"`
	RequestID      string                   `json:"requestID,omitempty"`
	Argv           []string                 `json:"argv,omitempty"`
	Workdir        string                   `json:"workdir,omitempty"`
	TimeoutSeconds int                      `json:"timeoutSeconds,omitempty"`
	SourceDigest   string                   `json:"sourceDigest,omitempty"`
	Files          []projectSandboxExecFile `json:"files,omitempty"`
}

type projectSandboxExecResponse struct {
	SessionID string `json:"sessionID,omitempty"`
	RequestID string `json:"requestID,omitempty"`
	State     string `json:"state"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type projectAssistantExecCommandResult struct {
	Status          string   `json:"status"`
	Summary         string   `json:"summary"`
	Component       string   `json:"component,omitempty"`
	SessionID       string   `json:"sessionID,omitempty"`
	ExitCode        *int     `json:"exitCode,omitempty"`
	Stdout          []string `json:"stdout,omitempty"`
	Stderr          []string `json:"stderr,omitempty"`
	OutputTruncated bool     `json:"outputTruncated,omitempty"`
	DurationMS      int64    `json:"durationMs,omitempty"`
	SourceRevision  uint64   `json:"sourceRevision,omitempty"`
	SourceDigest    string   `json:"sourceDigest,omitempty"`
	SyncStatus      string   `json:"syncStatus,omitempty"`
	Blockers        []string `json:"blockers,omitempty"`
}

func newProjectAssistantExecCommandGraphTool(runCtx projectAssistantWorkflowRunContext) (einotool.BaseTool, error) {
	workflow := compose.NewWorkflow[*projectAssistantExecCommandInput, *projectAssistantExecCommandResult]()
	workflow.AddLambdaNode("exec-command", compose.InvokableLambda(execProjectAssistantCommand(runCtx))).
		AddInput(compose.START)
	workflow.End().AddInput("exec-command")
	inner, err := graphtool.NewInvokableGraphTool(
		workflow,
		projectToolExecCommand,
		"Run one approved compiler, test, or lint argv in an isolated executor for one development component. The executor receives an exact synced source snapshot and cannot access project secrets, network, PTY, or write back to the workspace.",
		compose.WithGraphName("app-studio-exec-command"),
	)
	if err != nil {
		return nil, err
	}
	var approved einotool.InvokableTool = inner
	if runCtx.EventLedger != nil {
		spec, ok := projectAssistantWorkflowToolSpec(projectToolExecCommand)
		if !ok {
			return nil, fmt.Errorf("project assistant workflow spec %q is not configured", projectToolExecCommand)
		}
		durable, err := newProjectAssistantDurableGraphTool(inner, spec, runCtx.EventLedger, runCtx.AdmitMutation)
		if err != nil {
			return nil, err
		}
		approved = durable.(einotool.InvokableTool)
	}
	if !projectAssistantRuntimeGraphToolRequiresApproval(projectToolExecCommand, runCtx.ApprovalMode) {
		return approved, nil
	}
	return tool.InvokableApprovableTool{InvokableTool: approved}, nil
}

func execProjectAssistantCommand(runCtx projectAssistantWorkflowRunContext) func(context.Context, *projectAssistantExecCommandInput) (*projectAssistantExecCommandResult, error) {
	return func(ctx context.Context, input *projectAssistantExecCommandInput) (*projectAssistantExecCommandResult, error) {
		current := runCtx.current()
		args, blockers := normalizeProjectAssistantExecCommandInput(input)
		if len(blockers) > 0 {
			return &projectAssistantExecCommandResult{Status: "blocked", Summary: "Command execution was rejected.", Blockers: blockers}, nil
		}
		server, id, target, blocked := projectAssistantRuntimeCallContext(ctx, current)
		if blocked != nil {
			return &projectAssistantExecCommandResult{Status: blocked.Status, Summary: blocked.Summary, Blockers: blocked.Blockers}, nil
		}
		component, componentInfo, err := projectAssistantExecComponent(target, args.Component)
		if err != nil {
			return &projectAssistantExecCommandResult{Status: "blocked", Summary: "Command execution was rejected.", Blockers: []string{err.Error()}}, nil
		}
		var (
			revision                uint64
			syncStatus, syncFailure string
			files                   []projectSandboxExecFile
			digest                  string
		)
		for attempt := 0; attempt < projectAssistantExecSnapshotAttempts; attempt++ {
			revision, syncStatus, syncFailure = projectAssistantExecSyncEvidence(ctx, current)
			if syncStatus != "succeeded" {
				break
			}
			files, digest, err = projectAssistantExecSnapshot(ctx, current, componentInfo, revision)
			if !errors.Is(err, errProjectAssistantExecRevisionChanged) {
				break
			}
		}
		if syncStatus != "succeeded" {
			if syncFailure == "" {
				syncFailure = "the latest workspace mutation has not completed development synchronization"
			}
			return &projectAssistantExecCommandResult{Status: "blocked", Summary: "Command execution was blocked until the exact workspace revision is synchronized.", Component: component, SourceRevision: revision, SyncStatus: syncStatus, Blockers: []string{syncFailure}}, nil
		}
		if err != nil {
			return &projectAssistantExecCommandResult{Status: "blocked", Summary: "Command execution was blocked because an exact workspace snapshot could not be prepared.", Component: component, SourceRevision: revision, SyncStatus: syncStatus, Blockers: []string{err.Error()}}, nil
		}
		requestID := projectAssistantExecRequestID(current.AssistantRunID, compose.GetToolCallID(ctx))
		start := projectSandboxExecRequest{Action: "start", RequestID: requestID, Argv: args.Argv, Workdir: args.Workdir, TimeoutSeconds: args.TimeoutSeconds, SourceDigest: digest, Files: files}
		started, err := projectAssistantExecCall(ctx, server, id, target.dataPlaneRefFor(component), start)
		if err != nil {
			return &projectAssistantExecCommandResult{Status: "error", Summary: "Command execution could not start: " + err.Error(), Component: component, SourceRevision: revision, SourceDigest: digest, SyncStatus: syncStatus}, nil
		}
		if started.SessionID == "" {
			return &projectAssistantExecCommandResult{Status: "error", Summary: "Command execution returned no session ID.", Component: component, SourceRevision: revision, SourceDigest: digest, SyncStatus: syncStatus}, nil
		}
		startedAt := time.Now()
		result := started
		cancelSent := false
		cancelSession := func() {
			if cancelSent {
				return
			}
			cancelSent = true
			cancelCtx, cancel := context.WithTimeout(context.Background(), projectAssistantExecCancelTimeout)
			defer cancel()
			_, _ = projectAssistantExecCall(cancelCtx, server, id, target.dataPlaneRefFor(component), projectSandboxExecRequest{Action: "cancel", SessionID: started.SessionID, RequestID: requestID})
		}
		defer func() {
			// A request can be canceled while the HTTP poll is in flight. Keep
			// the remote process bounded even when that poll returns ctx.Err
			// before the select below gets a chance to send the cancel action.
			if !projectAssistantExecTerminal(result.State) {
				cancelSession()
			}
		}()
		deadline := time.NewTimer(projectAssistantExecPollTimeout)
		defer deadline.Stop()
		for !projectAssistantExecTerminal(result.State) {
			select {
			case <-ctx.Done():
				cancelSession()
				return projectAssistantExecResult(result, component, revision, digest, syncStatus, time.Since(startedAt), "canceled"), nil
			case <-deadline.C:
				cancelSession()
				return projectAssistantExecResult(result, component, revision, digest, syncStatus, time.Since(startedAt), "timed_out"), nil
			case <-time.After(projectAssistantExecPollInterval):
			}
			result, err = projectAssistantExecCall(ctx, server, id, target.dataPlaneRefFor(component), projectSandboxExecRequest{Action: "poll", SessionID: started.SessionID, RequestID: requestID})
			if err != nil {
				if ctx.Err() != nil {
					cancelSession()
					return projectAssistantExecResult(result, component, revision, digest, syncStatus, time.Since(startedAt), "canceled"), nil
				}
				return &projectAssistantExecCommandResult{Status: "error", Summary: "Command execution polling failed: " + err.Error(), Component: component, SessionID: started.SessionID, SourceRevision: revision, SourceDigest: digest, SyncStatus: syncStatus}, nil
			}
		}
		return projectAssistantExecResult(result, component, revision, digest, syncStatus, time.Since(startedAt), ""), nil
	}
}

func projectAssistantExecResult(raw projectSandboxExecResponse, component string, revision uint64, digest, syncStatus string, duration time.Duration, override string) *projectAssistantExecCommandResult {
	status := override
	if status == "" {
		switch raw.State {
		case "succeeded":
			status = "succeeded"
		case "failed":
			status = "failed"
		case "canceled", "cancelled":
			status = "canceled"
		case "timed_out":
			status = "timed_out"
		default:
			status = "error"
		}
	}
	stdout, stdoutTruncated := boundedProjectAssistantExecOutput(raw.Stdout)
	stderr, stderrTruncated := boundedProjectAssistantExecOutput(raw.Stderr)
	result := &projectAssistantExecCommandResult{Status: status, Component: component, SessionID: raw.SessionID, ExitCode: raw.ExitCode, Stdout: stdout, Stderr: stderr, OutputTruncated: raw.Truncated || stdoutTruncated || stderrTruncated, DurationMS: duration.Milliseconds(), SourceRevision: revision, SourceDigest: digest, SyncStatus: syncStatus}
	result.Summary = fmt.Sprintf("Command %s in component %q.", status, component)
	return result
}

func boundedProjectAssistantExecOutput(raw string) ([]string, bool) {
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return nil, false
	}
	truncated := false
	if len(raw) > projectAssistantExecMaxOutput {
		raw = raw[:projectAssistantExecMaxOutput]
		truncated = true
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
		truncated = true
	}
	for i := range lines {
		lines[i] = trimProjectAssistantWorkflowString(lines[i], 4096)
	}
	return lines, truncated
}

func projectAssistantExecCall(ctx context.Context, server *Server, id identity, ref dataPlaneRef, request projectSandboxExecRequest) (projectSandboxExecResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return projectSandboxExecResponse{}, fmt.Errorf("encode exec request: %w", err)
	}
	body, status, err := server.dataPlanePostBoundedWithHeaders(ctx, id, ref, dataPlaneVerbExec, payload, projectAssistantExecMaxOutput*2, http.Header{"Idempotency-Key": []string{request.RequestID}})
	if err != nil {
		return projectSandboxExecResponse{}, err
	}
	if status < 200 || status >= 300 {
		return projectSandboxExecResponse{}, fmt.Errorf("exec endpoint returned %d: %s", status, truncateProjectToolInfo(string(body)))
	}
	var response projectSandboxExecResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return projectSandboxExecResponse{}, fmt.Errorf("decode exec response: %w", err)
	}
	return response, nil
}

func projectAssistantExecComponent(target projectDevelopmentSyncTargetInfo, requested string) (string, projectTemplateComponent, error) {
	component := strings.TrimSpace(requested)
	if component == "" {
		return "", projectTemplateComponent{}, errors.New("component is required")
	}
	info, ok := target.Components[component]
	if !ok {
		return "", projectTemplateComponent{}, fmt.Errorf("unknown component %q; available components: %s", component, strings.Join(target.sortedComponents(), ", "))
	}
	return component, info, nil
}

func normalizeProjectAssistantExecCommandInput(input *projectAssistantExecCommandInput) (*projectAssistantExecCommandInput, []string) {
	if input == nil {
		return nil, []string{"component and argv are required"}
	}
	out := &projectAssistantExecCommandInput{Component: strings.TrimSpace(input.Component), Argv: append([]string(nil), input.Argv...), Workdir: strings.TrimSpace(input.Workdir), TimeoutSeconds: input.TimeoutSeconds}
	var blockers []string
	if out.Component == "" {
		blockers = append(blockers, "component is required")
	}
	if len(out.Argv) == 0 || len(out.Argv) > projectAssistantExecMaxArgv {
		blockers = append(blockers, fmt.Sprintf("argv must contain between 1 and %d tokens", projectAssistantExecMaxArgv))
	}
	for index, token := range out.Argv {
		if token == "" || len([]byte(token)) > projectAssistantExecMaxArgBytes || strings.IndexByte(token, 0) >= 0 {
			blockers = append(blockers, fmt.Sprintf("argv token %d is empty, too large, or contains NUL", index+1))
		}
	}
	if len([]byte(out.Workdir)) > projectAssistantExecMaxWorkdir || strings.IndexByte(out.Workdir, 0) >= 0 || strings.Contains(out.Workdir, "\\") || path.IsAbs(out.Workdir) {
		blockers = append(blockers, "workdir must be a bounded relative path")
	} else if out.Workdir != "" {
		clean := path.Clean(out.Workdir)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			blockers = append(blockers, "workdir must remain under the selected component workspace")
		} else {
			out.Workdir = clean
		}
	}
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = projectAssistantExecDefaultTimeout
	}
	if out.TimeoutSeconds < 1 || out.TimeoutSeconds > projectAssistantExecMaxTimeout {
		blockers = append(blockers, fmt.Sprintf("timeoutSeconds must be between 1 and %d", projectAssistantExecMaxTimeout))
	}
	return out, blockers
}

func projectAssistantExecSyncEvidence(ctx context.Context, runCtx projectAssistantWorkflowRunContext) (uint64, string, string) {
	if runCtx.RunState == nil {
		return 0, "unknown", "assistant run state is unavailable"
	}
	revision, _ := runCtx.RunState.SourceMutationRevisions()
	if revision == 0 {
		return 0, "succeeded", ""
	}
	status, failure := runCtx.RunState.WaitForDevelopmentSync(ctx, revision, dataPlaneCallTimeout)
	return revision, status, failure
}

func projectAssistantExecSnapshot(ctx context.Context, runCtx projectAssistantWorkflowRunContext, component projectTemplateComponent, expectedRevision uint64) ([]projectSandboxExecFile, string, error) {
	if runCtx.Workspace == nil {
		return nil, "", errors.New("project workspace store is not configured")
	}
	root := path.Clean(strings.TrimSpace(component.WorkspacePath))
	if root == "" {
		root = "."
	}
	for attempt := 0; attempt < projectAssistantExecSnapshotAttempts; attempt++ {
		list, err := runCtx.Workspace.ListFiles(ctx, runCtx.WorkspaceScope, workspace.ListOptions{Limit: workspace.MaxListLimit})
		if err != nil {
			return nil, "", err
		}
		if list.Truncated {
			return nil, "", fmt.Errorf("workspace snapshot exceeds the %d-file limit", workspace.MaxListLimit)
		}
		paths := projectAssistantExecComponentPaths(list, root)
		entries := make([]projectAssistantExecSnapshotEntry, 0, len(paths))
		total := 0
		retry := false
		for _, clean := range paths {
			relative := clean
			if root != "." {
				relative = strings.TrimPrefix(clean, root+"/")
			}
			read, readErr := runCtx.Workspace.ReadFile(ctx, runCtx.WorkspaceScope, workspace.ReadOptions{Path: clean, MaxBytes: workspace.MaxWriteBytes})
			if readErr != nil {
				if errors.Is(readErr, fs.ErrNotExist) {
					retry = true
					break
				}
				return nil, "", readErr
			}
			if read.Binary || read.Truncated {
				return nil, "", fmt.Errorf("workspace file %q is not bounded UTF-8 source", clean)
			}
			total += len([]byte(read.Content))
			if total > projectAssistantExecMaxSnapshot {
				return nil, "", fmt.Errorf("component snapshot exceeds %d bytes", projectAssistantExecMaxSnapshot)
			}
			entries = append(entries, projectAssistantExecSnapshotEntry{path: clean, file: projectSandboxExecFile{Path: relative, Content: read.Content}})
		}
		if retry {
			continue
		}
		files, digest := projectAssistantExecSnapshotDigest(entries)

		// FileStore deliberately exposes separate bounded list/read/digest
		// operations. Re-list and then compare its digest under the store lock
		// before accepting this bundle, so a concurrent mutation cannot leave
		// the executor with bytes from one revision and a digest from another.
		confirm, err := runCtx.Workspace.ListFiles(ctx, runCtx.WorkspaceScope, workspace.ListOptions{Limit: workspace.MaxListLimit})
		if err != nil {
			return nil, "", err
		}
		if confirm.Truncated {
			return nil, "", fmt.Errorf("workspace snapshot exceeds the %d-file limit", workspace.MaxListLimit)
		}
		if !projectAssistantExecStringSlicesEqual(paths, projectAssistantExecComponentPaths(confirm, root)) {
			continue
		}
		if len(paths) > 0 {
			currentDigest, err := runCtx.Workspace.WorkspaceDigest(ctx, runCtx.WorkspaceScope, paths)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, "", err
			}
			if currentDigest != digest {
				continue
			}
		}
		// Bind the accepted bytes to the exact mutation revision whose
		// development sync was verified by the caller. If another assistant
		// mutation landed during List/Read/Digest, the outer preparation loop
		// waits for that newer revision and captures again.
		if runCtx.RunState != nil {
			currentRevision, _ := runCtx.RunState.SourceMutationRevisions()
			if currentRevision != expectedRevision {
				return nil, "", errProjectAssistantExecRevisionChanged
			}
		}
		return files, digest, nil
	}
	return nil, "", errors.New("workspace changed while preparing the execution snapshot")
}

func projectAssistantExecComponentPaths(list workspace.FileList, root string) []string {
	paths := make([]string, 0, len(list.Files))
	prefix := root + "/"
	for _, info := range list.Files {
		clean := path.Clean(info.Path)
		if root != "." && !strings.HasPrefix(clean, prefix) {
			continue
		}
		paths = append(paths, clean)
	}
	sort.Strings(paths)
	return paths
}

func projectAssistantExecSnapshotDigest(entries []projectAssistantExecSnapshotEntry) ([]projectSandboxExecFile, string) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hash := sha256.New()
	files := make([]projectSandboxExecFile, 0, len(entries))
	for _, entry := range entries {
		_, _ = hash.Write([]byte(entry.path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(entry.file.Content))
		_, _ = hash.Write([]byte{0})
		files = append(files, entry.file)
	}
	return files, hex.EncodeToString(hash.Sum(nil))
}

func projectAssistantExecStringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func projectAssistantExecRequestID(runID, callID string) string {
	runID = strings.TrimSpace(runID)
	callID = strings.TrimSpace(callID)
	if runID == "" && callID == "" {
		// Model-issued tool calls normally provide both values. Keep a
		// deterministic fallback for direct/internal invocations so the
		// infrastructure contract's required idempotency key is still met.
		runID = "anonymous"
		callID = "anonymous"
	}
	sum := sha256.Sum256([]byte(runID + "\x00" + callID))
	return "appstudio-exec-" + hex.EncodeToString(sum[:16])
}

func projectAssistantExecTerminal(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "succeeded", "failed", "canceled", "timed_out", "cancelled":
		return true
	default:
		return false
	}
}
