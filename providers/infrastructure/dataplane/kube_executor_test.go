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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

func TestExecPodForCallHasIsolatedSecurityProfile(t *testing.T) {
	call := kubeExecTestCall()
	sessionID := "12345678901234567890abcdef"
	pod := execPodForCall(call, "kedge-exec-"+execSessionLabel(sessionID), "registry.example/agent@sha256:abc", "secret-token")
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("executor pod must not automount a service-account token")
	}
	if len(pod.Spec.Containers) != 1 || len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("containers = %d init = %d", len(pod.Spec.Containers), len(pod.Spec.InitContainers))
	}
	if got := pod.Spec.InitContainers[0].ImagePullPolicy; got != corev1.PullIfNotPresent {
		t.Fatalf("injector image pull policy = %q, want %q for side-loaded local images and digest-pinned production images", got, corev1.PullIfNotPresent)
	}
	container := pod.Spec.Containers[0]
	if container.Image != call.DevImage || len(container.Command) != 1 || container.Command[0] != "/kedge/bin/kedge-dev-agent" {
		t.Fatalf("executor container = %#v", container)
	}
	if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation ||
		container.SecurityContext.ReadOnlyRootFilesystem == nil || !*container.SecurityContext.ReadOnlyRootFilesystem ||
		container.SecurityContext.RunAsNonRoot == nil || !*container.SecurityContext.RunAsNonRoot {
		t.Fatalf("executor security context = %#v", container.SecurityContext)
	}
	if container.SecurityContext.Capabilities == nil || len(container.SecurityContext.Capabilities.Drop) != 1 || container.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("executor capabilities = %#v", container.SecurityContext.Capabilities)
	}
	if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.SeccompProfile == nil || pod.Spec.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod security context = %#v", pod.Spec.SecurityContext)
	}
	for _, volume := range pod.Spec.Volumes {
		if volume.EmptyDir == nil {
			t.Fatalf("non-ephemeral volume %q = %#v", volume.Name, volume.VolumeSource)
		}
	}
	if len(container.Env) != 2 {
		t.Fatalf("executor env = %#v, want only workdir and one-time token", container.Env)
	}
	policy := execNetworkPolicy(call.RuntimeNamespace, "kedge-exec-"+execSessionLabel(sessionID), sessionID)
	if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != "Egress" || len(policy.Spec.Egress) != 0 {
		t.Fatalf("network policy = %#v, want deny-all egress", policy.Spec)
	}
	if pod.Labels["kedge.faros.sh/exec-session"] != policy.Spec.PodSelector.MatchLabels["kedge.faros.sh/exec-session"] {
		t.Fatalf("network policy selector %q does not select pod label %q", policy.Spec.PodSelector.MatchLabels["kedge.faros.sh/exec-session"], pod.Labels["kedge.faros.sh/exec-session"])
	}
}

func TestKubernetesExecutorStartPollAndIdempotency(t *testing.T) {
	client := fake.NewClientset()
	proxy := &fakeExecPodProxy{response: execAgentResponse{Phase: "completed", ExitCode: 0, Stdout: "ok\n"}}
	executor := newKubernetesExecutor(client, proxy, "registry.example/agent@sha256:abc")
	call := kubeExecTestCall()
	digest, err := execSourceDigest(call)
	if err != nil {
		t.Fatal(err)
	}
	call.Request.SourceDigest = digest

	started, err := executor.Start(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if started.SessionID == "" || started.State != "queued" {
		t.Fatalf("start = %#v", started)
	}
	duplicate, err := executor.Start(context.Background(), call)
	if err != nil || duplicate.SessionID != started.SessionID {
		t.Fatalf("duplicate start = %#v, %v", duplicate, err)
	}

	var pod *corev1.Pod
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pod, err = client.CoreV1().Pods(call.RuntimeNamespace).Get(context.Background(), "kedge-exec-"+started.SessionID[:20], metav1.GetOptions{})
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("executor pod was not created: %v", err)
	}
	pod.Status.Phase = corev1.PodRunning
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if _, err := client.CoreV1().Pods(call.RuntimeNamespace).UpdateStatus(context.Background(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	pollCall := call
	pollCall.Request = ExecRequest{Action: ExecActionPoll, SessionID: started.SessionID, RequestID: call.Request.RequestID}
	var result ExecResult
	for time.Now().Before(deadline) {
		result, err = executor.Poll(context.Background(), pollCall)
		if err != nil {
			t.Fatal(err)
		}
		if execTerminalState(result.State) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result.State != "succeeded" || result.ExitCode == nil || *result.ExitCode != 0 || result.Stdout != "ok\n" {
		t.Fatalf("result = %#v", result)
	}
	proxy.mu.Lock()
	gotRequest := proxy.request
	gotToken := proxy.token
	proxy.mu.Unlock()
	if gotToken == "" || gotRequest.WorkDir != "." || len(gotRequest.Files) != 1 {
		t.Fatalf("proxy request = %#v token=%q", gotRequest, gotToken)
	}
	policyCreate, podCreate := -1, -1
	for index, action := range client.Actions() {
		if action.GetVerb() != "create" {
			continue
		}
		switch action.GetResource().Resource {
		case "networkpolicies":
			if policyCreate < 0 {
				policyCreate = index
			}
		case "pods":
			if podCreate < 0 {
				podCreate = index
			}
		}
	}
	if policyCreate < 0 || podCreate < 0 || policyCreate > podCreate {
		t.Fatalf("create action order policy=%d pod=%d; deny policy must exist first", policyCreate, podCreate)
	}
}

func TestKubernetesExecutorSessionIsBoundToCaller(t *testing.T) {
	executor := newKubernetesExecutor(fake.NewClientset(), &fakeExecPodProxy{}, "registry.example/agent@sha256:abc")
	call := kubeExecTestCall()
	digest, err := execSourceDigest(call)
	if err != nil {
		t.Fatal(err)
	}
	call.Request.SourceDigest = digest
	started, err := executor.Start(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}

	pollCall := call
	pollCall.CallerKey = execCallerKey("different-caller-token")
	pollCall.Request = ExecRequest{Action: ExecActionPoll, SessionID: started.SessionID, RequestID: call.Request.RequestID}
	if _, err := executor.Poll(context.Background(), pollCall); err == nil {
		t.Fatal("poll by a different caller must be rejected")
	}
	secondStart := call
	secondStart.CallerKey = pollCall.CallerKey
	startedBySecondCaller, err := executor.Start(context.Background(), secondStart)
	if err != nil {
		t.Fatal(err)
	}
	if startedBySecondCaller.SessionID == started.SessionID {
		t.Fatal("idempotency scope must be isolated between callers")
	}

	// End the background execution started by this unit test.
	cancelCall := pollCall
	cancelCall.CallerKey = call.CallerKey
	if _, err := executor.Cancel(context.Background(), cancelCall); err != nil {
		t.Fatal(err)
	}
	secondCancel := cancelCall
	secondCancel.CallerKey = secondStart.CallerKey
	secondCancel.Request.SessionID = startedBySecondCaller.SessionID
	if _, err := executor.Cancel(context.Background(), secondCancel); err != nil {
		t.Fatal(err)
	}
}

func TestExecCallFingerprintIncludesRequestWorkdir(t *testing.T) {
	call := kubeExecTestCall()
	first, err := execCallFingerprint(call)
	if err != nil {
		t.Fatal(err)
	}
	call.Request.Workdir = "src"
	second, err := execCallFingerprint(call)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("request workdir must participate in the idempotency fingerprint")
	}
}

func TestExecPodProxyNameSelectsControlPort(t *testing.T) {
	if got := execPodProxyName("sandbox"); got != "sandbox:7070" {
		t.Fatalf("proxy name = %q, want sandbox:7070", got)
	}
}

func TestRestExecPodProxyUsesProxySubresourceRoute(t *testing.T) {
	token := "one-time-token"
	reject := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got, want := r.URL.Path, "/api/v1/namespaces/runtime/pods/sandbox:7070/proxy/exec"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got := r.Header.Get(execAgentTokenHeader); got != token {
			t.Errorf("control token = %q, want %q", got, token)
		}
		if reject {
			http.Error(w, "request rejected by executor", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(execAgentResponse{Phase: "completed"})
	}))
	t.Cleanup(server.Close)

	client, err := rest.RESTClientFor(&rest.Config{
		Host:    server.URL,
		APIPath: "/api",
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &schema.GroupVersion{Version: "v1"},
			NegotiatedSerializer: clientgoscheme.Codecs.WithoutConversion(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (restExecPodProxy{client: client}).Execute(context.Background(), "runtime", "sandbox", token, execAgentRequest{}); err != nil {
		t.Fatal(err)
	}
	reject = true
	if _, err := (restExecPodProxy{client: client}).Execute(context.Background(), "runtime", "sandbox", token, execAgentRequest{}); err == nil || !strings.Contains(err.Error(), "request rejected by executor") {
		t.Fatalf("proxy error = %v, want bounded executor response body", err)
	}
}

func TestCreateExecNetworkPolicyReplacesOrphan(t *testing.T) {
	call := kubeExecTestCall()
	name := "kedge-exec-orphan"
	policy := execNetworkPolicy(call.RuntimeNamespace, name, "orphan")
	client := fake.NewClientset(policy.DeepCopy())
	executor := newKubernetesExecutor(client, &fakeExecPodProxy{}, "registry.example/agent@sha256:abc")
	if err := executor.createExecNetworkPolicy(context.Background(), policy, name); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NetworkingV1().NetworkPolicies(call.RuntimeNamespace).Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("replacement policy is missing: %v", err)
	}
	deleted := false
	for _, action := range client.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "networkpolicies" {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("orphan policy was not deleted before replacement")
	}
}

func TestRejectPermissiveEgressPolicies(t *testing.T) {
	call := kubeExecTestCall()
	allow := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-all", Namespace: call.RuntimeNamespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      []networkingv1.NetworkPolicyEgressRule{{}},
		},
	}
	executor := newKubernetesExecutor(fake.NewClientset(allow), &fakeExecPodProxy{}, "registry.example/agent@sha256:abc")
	labels := map[string]string{"app.kubernetes.io/name": "kedge-executor", "kedge.faros.sh/exec-session": "session"}
	if err := executor.rejectPermissiveEgressPolicies(context.Background(), call.RuntimeNamespace, labels, "own-deny"); err == nil {
		t.Fatal("matching egress allow policy must fail the no-network contract")
	}
}

func TestValidateKubeExecCallRejectsDigestMismatch(t *testing.T) {
	call := kubeExecTestCall()
	call.Request.SourceDigest = "wrong"
	if err := validateKubeExecCall(call); err == nil {
		t.Fatal("digest mismatch must be rejected before pod creation")
	}
}

func TestValidateKubeExecCallRejectsExecutableUpload(t *testing.T) {
	call := kubeExecTestCall()
	call.Request.Files[0].Executable = true
	if err := validateKubeExecCall(call); err == nil {
		t.Fatal("executable mode must be rejected because FileStore does not preserve it")
	}
}

type fakeExecPodProxy struct {
	mu       sync.Mutex
	request  execAgentRequest
	token    string
	response execAgentResponse
	err      error
}

func (p *fakeExecPodProxy) Execute(_ context.Context, _, _ string, token string, request execAgentRequest) (execAgentResponse, error) {
	p.mu.Lock()
	p.request = request
	p.token = token
	p.mu.Unlock()
	return p.response, p.err
}

func kubeExecTestCall() ExecCall {
	return ExecCall{
		Workspace: "logical-cluster", Resource: "applications", Name: "demo", Component: "backend",
		Capability: &infrav1alpha1.TemplateDataPlaneExec{MaxTimeoutSeconds: 120, MaxOutputBytes: 256 << 10, MaxFiles: 512, MaxFileBytes: 1 << 20},
		DevImage:   "registry.example/node@sha256:def", WorkingDir: "/workspace", WorkspacePath: "api", CallerKey: execCallerKey("caller-token"), RuntimeNamespace: "tenant-demo",
		IdempotencyKey: "run-call", Request: ExecRequest{
			Action: ExecActionStart, RequestID: "run-call", Argv: []string{"node", "--version"}, Workdir: ".", TimeoutSeconds: 30,
			Files: []ExecFile{{Path: "package.json", Content: "{}\n"}},
		},
	}
}
