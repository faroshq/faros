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
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	execAgentPort             = 7070
	execSessionRetention      = 10 * time.Minute
	execPodStartupTimeout     = 45 * time.Second
	execSessionCapacity       = 256
	execOrphanMaximumAge      = 30 * time.Minute
	execOrphanPolicyGrace     = time.Minute
	execJanitorInterval       = 5 * time.Minute
	execAgentTokenHeader      = "X-Sandbox-Control-Token"
	execAgentContainerName    = "executor"
	execInjectorContainerName = "agent-injector"
)

var executionGroupResource = schema.GroupResource{Group: "infrastructure.kedge.faros.sh", Resource: "executions"}

type execPodProxy interface {
	Execute(context.Context, string, string, string, execAgentRequest) (execAgentResponse, error)
}

type restExecPodProxy struct {
	client rest.Interface
}

func (p restExecPodProxy) Execute(ctx context.Context, namespace, pod, token string, input execAgentRequest) (execAgentResponse, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return execAgentResponse{}, fmt.Errorf("encode executor request: %w", err)
	}
	raw, err := p.client.Post().
		Namespace(namespace).
		Resource("pods").
		Name(execPodProxyName(pod)).
		SubResource("proxy").
		Suffix("exec").
		SetHeader("Content-Type", "application/json").
		SetHeader(execAgentTokenHeader, token).
		Body(payload).
		DoRaw(ctx)
	if err != nil {
		message := strings.TrimSpace(string(raw))
		if len(message) > 2048 {
			message = message[:2048] + "..."
		}
		if message != "" {
			return execAgentResponse{}, fmt.Errorf("executor pod proxy: %w: %s", err, message)
		}
		return execAgentResponse{}, fmt.Errorf("executor pod proxy: %w", err)
	}
	var response execAgentResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return execAgentResponse{}, fmt.Errorf("decode executor response: %w", err)
	}
	return response, nil
}

// KubernetesExecutor runs each command in a fresh, namespace-confined pod.
// Session state is intentionally short-lived: App Studio's durable run-event
// ledger is the cross-restart replay boundary, while this type owns only the
// active pod lifecycle and bounded polling result.
type KubernetesExecutor struct {
	client     kubernetes.Interface
	proxy      execPodProxy
	agentImage string
	now        func() time.Time

	mu       sync.Mutex
	sessions map[string]*kubeExecSession
	requests map[string]string
}

type kubeExecSession struct {
	mu sync.RWMutex

	id          string
	requestID   string
	fingerprint string
	workspace   string
	resource    string
	name        string
	component   string
	callerKey   string
	namespace   string
	podName     string
	policyName  string
	result      ExecResult
	outputLimit int
	completedAt time.Time
	cancel      context.CancelFunc
}

// NewKubernetesExecutor constructs the production executor from the runtime
// cluster config. agentImage is the platform-owned kedge-dev-agent injector
// image and should be digest-pinned in production.
func NewKubernetesExecutor(config *rest.Config, agentImage string) (*KubernetesExecutor, error) {
	if config == nil {
		return nil, fmt.Errorf("runtime config is required for exec")
	}
	agentImage = strings.TrimSpace(agentImage)
	if agentImage == "" {
		return nil, fmt.Errorf("exec agent image is required")
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("exec runtime client: %w", err)
	}
	executor := newKubernetesExecutor(client, restExecPodProxy{client: client.CoreV1().RESTClient()}, agentImage)
	go executor.runJanitor()
	return executor, nil
}

func newKubernetesExecutor(client kubernetes.Interface, proxy execPodProxy, agentImage string) *KubernetesExecutor {
	return &KubernetesExecutor{
		client: client, proxy: proxy, agentImage: agentImage, now: time.Now,
		sessions: map[string]*kubeExecSession{}, requests: map[string]string{},
	}
}

func (e *KubernetesExecutor) Start(_ context.Context, call ExecCall) (ExecResult, error) {
	if e == nil || e.client == nil || e.proxy == nil {
		return ExecResult{}, fmt.Errorf("kubernetes executor is unavailable")
	}
	if err := validateKubeExecCall(call); err != nil {
		return ExecResult{}, err
	}
	fingerprint, err := execCallFingerprint(call)
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
	if len(e.sessions) >= execSessionCapacity {
		e.mu.Unlock()
		return ExecResult{}, fmt.Errorf("executor session capacity is exhausted")
	}
	runCtx, cancel := context.WithTimeout(context.Background(), execRunDeadline(call))
	session := &kubeExecSession{
		id: sessionID, requestID: call.Request.RequestID, fingerprint: fingerprint,
		workspace: call.Workspace, resource: call.Resource, name: call.Name,
		component: call.Component, callerKey: call.CallerKey, namespace: call.RuntimeNamespace,
		podName: "kedge-exec-" + sessionID[:20], policyName: "kedge-exec-" + sessionID[:20],
		result:      ExecResult{SessionID: sessionID, RequestID: call.Request.RequestID, State: "queued"},
		outputLimit: execOutputLimit(call),
		cancel:      cancel,
	}
	e.sessions[sessionID] = session
	e.requests[requestKey] = sessionID
	e.mu.Unlock()

	go e.run(runCtx, session, call)
	return session.snapshot(), nil
}

func (e *KubernetesExecutor) Poll(_ context.Context, call ExecCall) (ExecResult, error) {
	session, err := e.sessionFor(call)
	if err != nil {
		return ExecResult{}, err
	}
	return session.snapshot(), nil
}

func (e *KubernetesExecutor) Cancel(_ context.Context, call ExecCall) (ExecResult, error) {
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
	go e.cleanup(context.Background(), session)
	return result, nil
}

func (e *KubernetesExecutor) sessionFor(call ExecCall) (*kubeExecSession, error) {
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

func (s *kubeExecSession) snapshot() ExecResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.result
}

func (e *KubernetesExecutor) run(ctx context.Context, session *kubeExecSession, call ExecCall) {
	defer e.cleanup(context.Background(), session)
	token, err := randomExecToken()
	if err != nil {
		e.finish(session, ExecResult{State: "failed", Stderr: err.Error()})
		return
	}
	pod := execPodForCall(call, session.podName, e.agentImage, token)
	// Install deny-egress before creating the selected pod. Creating the pod
	// first would leave a short but real unrestricted-network window.
	policy := execNetworkPolicy(call.RuntimeNamespace, session.policyName, session.id)
	if err := e.createExecNetworkPolicy(ctx, policy, session.podName); err != nil {
		e.finish(session, ExecResult{State: "failed", Stderr: fmt.Sprintf("create executor network policy: %v", err)})
		return
	}
	if err := e.rejectPermissiveEgressPolicies(ctx, call.RuntimeNamespace, pod.Labels, policy.Name); err != nil {
		e.finish(session, ExecResult{State: "failed", Stderr: err.Error()})
		return
	}
	if _, err := e.client.CoreV1().Pods(call.RuntimeNamespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		e.finish(session, ExecResult{State: "failed", Stderr: fmt.Sprintf("create executor pod: %v", err)})
		return
	}
	e.setState(session, "starting")
	if err := e.waitForReady(ctx, call.RuntimeNamespace, session.podName); err != nil {
		state := "failed"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			state = "canceled"
		}
		e.finish(session, ExecResult{State: state, Stderr: err.Error()})
		return
	}
	e.setState(session, "running")
	limits, _ := limitsForCapability(call.Capability)
	request := execAgentRequest{
		Files: call.Request.Files, Argv: call.Request.Argv, WorkDir: call.Request.Workdir,
		TimeoutMS: int(execTimeoutSeconds(call)) * 1000, MaxOutputBytes: limits.outputBytes,
	}
	response, err := e.proxy.Execute(ctx, call.RuntimeNamespace, session.podName, token, request)
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

func (e *KubernetesExecutor) setState(session *kubeExecSession, state string) {
	session.mu.Lock()
	if !execTerminalState(session.result.State) {
		session.result.State = state
	}
	session.mu.Unlock()
}

func (e *KubernetesExecutor) finish(session *kubeExecSession, result ExecResult) {
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

func (e *KubernetesExecutor) cleanup(parent context.Context, session *kubeExecSession) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	grace := int64(0)
	if err := e.client.CoreV1().Pods(session.namespace).Delete(ctx, session.podName, metav1.DeleteOptions{GracePeriodSeconds: &grace}); err != nil && !apierrors.IsNotFound(err) {
		log.Printf("data plane exec: delete pod %s/%s: %v", session.namespace, session.podName, err)
	}
	if err := e.client.NetworkingV1().NetworkPolicies(session.namespace).Delete(ctx, session.policyName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		log.Printf("data plane exec: delete network policy %s/%s: %v", session.namespace, session.policyName, err)
	}
}

func (e *KubernetesExecutor) runJanitor() {
	e.cleanupStale("")
	ticker := time.NewTicker(execJanitorInterval)
	defer ticker.Stop()
	for range ticker.C {
		e.cleanupStale("")
	}
}

// cleanupStale removes terminal or abandoned executor objects. Normal
// completion deletes immediately; this is the provider-crash safety net.
func (e *KubernetesExecutor) cleanupStale(namespace string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	selector := "app.kubernetes.io/name=kedge-executor"
	pods, err := e.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return
	}
	cutoff := e.now().Add(-execOrphanMaximumAge)
	live := make(map[string]struct{}, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		key := pod.Namespace + "/" + pod.Name
		stale := pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded ||
			(!pod.CreationTimestamp.IsZero() && pod.CreationTimestamp.Time.Before(cutoff))
		if stale {
			grace := int64(0)
			_ = e.client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &grace})
			continue
		}
		live[key] = struct{}{}
	}
	policies, err := e.client.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return
	}
	for i := range policies.Items {
		policy := &policies.Items[i]
		if _, ok := live[policy.Namespace+"/"+policy.Name]; ok {
			continue
		}
		policyCutoff := e.now().Add(-execOrphanPolicyGrace)
		if policy.CreationTimestamp.IsZero() || policy.CreationTimestamp.Time.Before(policyCutoff) {
			if err := e.client.NetworkingV1().NetworkPolicies(policy.Namespace).Delete(ctx, policy.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				log.Printf("data plane exec janitor: delete network policy %s/%s: %v", policy.Namespace, policy.Name, err)
			}
		}
	}
}

// createExecNetworkPolicy recovers the only safe deterministic-name collision:
// a deny policy left behind without its pod after a provider restart. A live
// pod is never adopted because its one-time control token was held only by the
// lost provider process.
func (e *KubernetesExecutor) createExecNetworkPolicy(ctx context.Context, policy *networkingv1.NetworkPolicy, podName string) error {
	policies := e.client.NetworkingV1().NetworkPolicies(policy.Namespace)
	if _, err := policies.Create(ctx, policy, metav1.CreateOptions{}); !apierrors.IsAlreadyExists(err) {
		return err
	}
	if _, err := e.client.CoreV1().Pods(policy.Namespace).Get(ctx, podName, metav1.GetOptions{}); err == nil {
		return fmt.Errorf("prior executor pod %q still exists", podName)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check prior executor pod %q: %w", podName, err)
	}
	if err := policies.Delete(ctx, policy.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete orphan executor network policy: %w", err)
	}
	_, err := policies.Create(ctx, policy, metav1.CreateOptions{})
	return err
}

// rejectPermissiveEgressPolicies makes the no-network contract fail closed.
// Kubernetes NetworkPolicies are additive, so a broad allow policy selecting
// this pod would otherwise override the executor's empty egress rule set.
func (e *KubernetesExecutor) rejectPermissiveEgressPolicies(ctx context.Context, namespace string, podLabels map[string]string, ownPolicy string) error {
	policies, err := e.client.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("verify executor network isolation: %w", err)
	}
	set := labels.Set(podLabels)
	for i := range policies.Items {
		policy := &policies.Items[i]
		if policy.Name == ownPolicy || len(policy.Spec.Egress) == 0 {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
		if err != nil {
			return fmt.Errorf("verify executor network policy %q selector: %w", policy.Name, err)
		}
		if selector.Matches(set) {
			return fmt.Errorf("executor network isolation is overridden by egress policy %q", policy.Name)
		}
	}
	return nil
}

func (e *KubernetesExecutor) waitForReady(parent context.Context, namespace, name string) error {
	ctx, cancel := context.WithTimeout(parent, execPodStartupTimeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		pod, err := e.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read executor pod: %w", err)
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return nil
			}
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return fmt.Errorf("executor pod terminated before becoming ready: %s", pod.Status.Message)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (e *KubernetesExecutor) pruneSessionsLocked() {
	cutoff := e.now().Add(-execSessionRetention)
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

type execAgentRequest struct {
	Files          []ExecFile `json:"files,omitempty"`
	Argv           []string   `json:"argv"`
	WorkDir        string     `json:"workDir,omitempty"`
	TimeoutMS      int        `json:"timeoutMs,omitempty"`
	MaxOutputBytes int        `json:"maxOutputBytes,omitempty"`
	SourceRevision uint64     `json:"sourceRevision,omitempty"`
	SourceDigest   string     `json:"sourceDigest,omitempty"`
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
	return ExecResult{State: state, ExitCode: &exitCode, Stdout: response.Stdout, Stderr: stderr, Truncated: response.StdoutTruncated || response.StderrTruncated}
}

func validateKubeExecCall(call ExecCall) error {
	if call.Request.Action != ExecActionStart || strings.TrimSpace(call.IdempotencyKey) == "" {
		return fmt.Errorf("executor start requires a start request and idempotency key")
	}
	if strings.TrimSpace(call.RuntimeNamespace) == "" || strings.TrimSpace(call.DevImage) == "" || strings.TrimSpace(call.CallerKey) == "" {
		return fmt.Errorf("executor runtime namespace, platform image, and caller binding are required")
	}
	if !path.IsAbs(call.WorkingDir) || path.Clean(call.WorkingDir) == "/" {
		return fmt.Errorf("executor working directory must be an absolute non-root path")
	}
	if call.Request.Workdir == "" {
		call.Request.Workdir = "."
	}
	for _, file := range call.Request.Files {
		// App Studio's FileStore has no executable-mode contract. Reject mode
		// elevation instead of accepting a bit that is not bound into its source
		// digest and cannot be faithfully round-tripped by the source of truth.
		if file.Executable {
			return fmt.Errorf("executable source uploads are not supported")
		}
	}
	if got, err := execSourceDigest(call); err != nil {
		return err
	} else if subtle.ConstantTimeCompare([]byte(got), []byte(strings.TrimPrefix(call.Request.SourceDigest, "sha256:"))) != 1 {
		return fmt.Errorf("source digest does not match the supplied snapshot")
	}
	return nil
}

func execSourceDigest(call ExecCall) (string, error) {
	prefix := path.Clean(strings.TrimSpace(call.WorkspacePath))
	if prefix == "" {
		prefix = "."
	}
	type entry struct{ name, content string }
	entries := make([]entry, 0, len(call.Request.Files))
	for _, file := range call.Request.Files {
		name, err := normalizeExecPath(file.Path)
		if err != nil {
			return "", err
		}
		if prefix != "." {
			name = path.Join(prefix, name)
		}
		entries = append(entries, entry{name: name, content: file.Content})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	hash := sha256.New()
	for _, item := range entries {
		_, _ = hash.Write([]byte(item.name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(item.content))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func execCallFingerprint(call ExecCall) (string, error) {
	payload, err := json.Marshal(struct {
		Namespace, Resource, Name, Component, Image, WorkingDir, WorkspacePath, Digest, RequestWorkdir string
		Revision                                                                                       uint64
		Argv                                                                                           []string
		Timeout                                                                                        int32
	}{call.RuntimeNamespace, call.Resource, call.Name, call.Component, call.DevImage, call.WorkingDir, call.WorkspacePath, call.Request.SourceDigest, call.Request.Workdir, call.Request.SourceRevision, call.Request.Argv, call.Request.TimeoutSeconds})
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

func execPodProxyName(pod string) string {
	return pod + ":" + strconv.Itoa(execAgentPort)
}

func execRunDeadline(call ExecCall) time.Duration {
	return time.Duration(execTimeoutSeconds(call))*time.Second + execPodStartupTimeout + 15*time.Second
}

func execTerminalState(state string) bool {
	switch state {
	case "succeeded", "failed", "canceled", "timed_out":
		return true
	default:
		return false
	}
}

func randomExecToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate executor token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func execPodForCall(call ExecCall, name, agentImage, token string) *corev1.Pod {
	labels := map[string]string{"app.kubernetes.io/name": "kedge-executor", "kedge.faros.sh/exec-session": execSessionLabel(strings.TrimPrefix(name, "kedge-exec-"))}
	falseValue := false
	trueValue := true
	user := int64(1000)
	group := int64(1000)
	nonRoot := true
	deadline := int64(execTimeoutSeconds(call)) + 90
	mode := corev1.MountPropagationNone
	security := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &falseValue, ReadOnlyRootFilesystem: &trueValue,
		RunAsNonRoot: &nonRoot, RunAsUser: &user, RunAsGroup: &group,
		Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: call.RuntimeNamespace, Labels: labels},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: &falseValue, EnableServiceLinks: &falseValue,
			RestartPolicy: corev1.RestartPolicyNever, ActiveDeadlineSeconds: &deadline,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &nonRoot, RunAsUser: &user, RunAsGroup: &group, FSGroup: &group,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			InitContainers: []corev1.Container{{
				Name: execInjectorContainerName, Image: agentImage, ImagePullPolicy: corev1.PullIfNotPresent, Args: []string{"--install", "/kedge/bin"},
				SecurityContext: security,
				VolumeMounts:    []corev1.VolumeMount{{Name: "agent-bin", MountPath: "/kedge/bin", MountPropagation: &mode}},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: apiresource.MustParse("10m"), corev1.ResourceMemory: apiresource.MustParse("16Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: apiresource.MustParse("100m"), corev1.ResourceMemory: apiresource.MustParse("64Mi")},
				},
			}},
			Containers: []corev1.Container{{
				Name: execAgentContainerName, Image: call.DevImage,
				Command: []string{"/kedge/bin/kedge-dev-agent"}, Args: []string{"--exec-server"},
				Env:   []corev1.EnvVar{{Name: "KEDGE_DEV_WORKDIR", Value: call.WorkingDir}, {Name: "KEDGE_DEV_CONTROL_TOKEN", Value: token}},
				Ports: []corev1.ContainerPort{{Name: "control", ContainerPort: execAgentPort, Protocol: corev1.ProtocolTCP}},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstrFromInt(execAgentPort)}},
					InitialDelaySeconds: 1, PeriodSeconds: 1, TimeoutSeconds: 1, FailureThreshold: 30,
				},
				SecurityContext: security,
				VolumeMounts: []corev1.VolumeMount{
					{Name: "agent-bin", MountPath: "/kedge/bin", ReadOnly: true},
					{Name: "workspace", MountPath: call.WorkingDir},
					{Name: "tmp", MountPath: "/tmp"},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: apiresource.MustParse("50m"), corev1.ResourceMemory: apiresource.MustParse("128Mi"), corev1.ResourceEphemeralStorage: apiresource.MustParse("128Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: apiresource.MustParse("1"), corev1.ResourceMemory: apiresource.MustParse("1Gi"), corev1.ResourceEphemeralStorage: apiresource.MustParse("1Gi")},
				},
			}},
			Volumes: []corev1.Volume{
				{Name: "agent-bin", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: quantityPtr("32Mi")}}},
				{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: quantityPtr("1Gi")}}},
				{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: quantityPtr("256Mi")}}},
			},
		},
	}
}

func execNetworkPolicy(namespace, name, sessionID string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{
			"app.kubernetes.io/name": "kedge-executor", "kedge.faros.sh/exec-session": execSessionLabel(sessionID),
		}},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"kedge.faros.sh/exec-session": execSessionLabel(sessionID)}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}, Egress: []networkingv1.NetworkPolicyEgressRule{},
		},
	}
}

func execSessionLabel(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) > 20 {
		return sessionID[:20]
	}
	return sessionID
}

func quantityPtr(value string) *apiresource.Quantity {
	quantity := apiresource.MustParse(value)
	return &quantity
}

func intstrFromInt(value int) intstr.IntOrString {
	return intstr.FromInt32(int32(value))
}
