// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	agentsv1alpha1 "github.com/faroshq/provider-agents/apis/v1alpha1"
	agentsclient "github.com/faroshq/provider-agents/client"
	"github.com/faroshq/provider-agents/engine"
	"github.com/faroshq/provider-agents/executor"
	"github.com/faroshq/provider-agents/llm"
	"github.com/faroshq/provider-agents/store"
	"github.com/faroshq/provider-agents/tenant"
	"sigs.k8s.io/yaml"
)

type recordingExecutor struct {
	jobs      []executor.Job
	reject    bool
	rejectErr error
}

// terminalRejectStore simulates a write that loses the execution fence exactly
// when the model result is ready. The underlying store still records the run's
// Running state, which lets the test assert that no stale side effects escaped.
type terminalRejectStore struct{ store.Store }

func (s terminalRejectStore) SaveRunOwned(ctx context.Context, scope store.Scope, run store.Run, owner string, epoch int64) error {
	switch run.Phase {
	case store.RunPhaseSucceeded, store.RunPhaseFailed, store.RunPhaseAborted:
		return store.ErrRunLeaseLost
	default:
		return s.Store.SaveRunOwned(ctx, scope, run, owner, epoch)
	}
}

// contextCheckingStore makes a canceled execution context visible to the
// persistence seam. Terminal bookkeeping must detach from that context or an
// explicit cancel leaves the run indistinguishable from a crashed worker.
type contextCheckingStore struct{ store.Store }

func (s contextCheckingStore) GetRun(ctx context.Context, scope store.Scope, id string) (store.Run, error) {
	if err := ctx.Err(); err != nil {
		return store.Run{}, err
	}
	return s.Store.GetRun(ctx, scope, id)
}

func (s contextCheckingStore) SaveRunOwned(ctx context.Context, scope store.Scope, run store.Run, owner string, epoch int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Store.SaveRunOwned(ctx, scope, run, owner, epoch)
}

func (e *recordingExecutor) Start(context.Context) error { return nil }

func (e *recordingExecutor) Submit(_ context.Context, job executor.Job) error {
	e.jobs = append(e.jobs, job)
	if e.reject {
		if e.rejectErr != nil {
			return e.rejectErr
		}
		return errors.New("executor queue full")
	}
	return nil
}

func (e *recordingExecutor) Stop() {}

func scheduleObjectForReliability(name, cluster string, runAt time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": agentsv1alpha1.SchemeGroupVersion.String(),
		"kind":       "Schedule",
		"metadata": map[string]any{
			"name": name, "generation": int64(1), "resourceVersion": "1",
			"annotations": map[string]any{"kcp.io/cluster": cluster},
		},
		"spec": map[string]any{
			"type": "wakeup", "agentRef": "agent", "task": "wake up",
			"runAt": runAt.UTC().Format(time.RFC3339),
		},
	}}
}

func TestScheduleDispatchIntentSurvivesQueueRejection(t *testing.T) {
	ctx := context.Background()
	cluster := "cluster"
	now := time.Date(2026, 9, 6, 15, 0, 0, 0, time.UTC)
	obj := scheduleObjectForReliability("wake", cluster, now.Add(-time.Minute))
	scheme := runtime.NewScheme()
	scoped := dynamicfake.NewSimpleDynamicClient(scheme, obj)
	st := store.NewMemoryStore()
	if err := st.SaveTenantRef(ctx, cluster, store.TenantRef{OrgUUID: "org", WorkspaceUUID: "workspace", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}
	e := &recordingExecutor{reject: true}
	b := &background{
		server: s, exec: e,
		scopedFn: func(context.Context, string) (dynamic.Interface, error) { return scoped, nil },
	}

	if err := b.process(ctx, obj, now); err == nil {
		t.Fatal("first dispatch should report queue rejection")
	}
	if len(e.jobs) != 1 {
		t.Fatalf("first submission count = %d, want 1", len(e.jobs))
	}
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", AgentName: "agent"}
	page, err := st.QueryRuns(ctx, scope, store.RunQuery{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("intent query: err=%v runs=%d", err, len(page.Items))
	}
	intent := page.Items[0]
	if intent.Phase != store.RunPhasePending || intent.IdempotencyKey == "" {
		t.Fatalf("intent = %+v", intent)
	}

	// The status claim is persisted even though the in-memory queue rejected the
	// first delivery. The next poll must use LastRunID to retry the same intent.
	stored, err := scoped.Resource(agentsclient.ScheduleGVR).Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The old occurrence must keep its durable task and destination even if the
	// schedule is edited before the queue accepts the retry.
	if err := unstructured.SetNestedField(stored.Object, "edited task", "spec", "task"); err != nil {
		t.Fatal(err)
	}
	stored.SetGeneration(2)
	e.reject = false
	if err := b.process(ctx, stored, now.Add(time.Minute)); err != nil {
		t.Fatalf("retry dispatch: %v", err)
	}
	if len(e.jobs) != 2 || e.jobs[0].ID != e.jobs[1].ID || e.jobs[0].RunID != intent.ID || e.jobs[1].RunID != intent.ID {
		t.Fatalf("jobs = %+v, want two deliveries of one durable run", e.jobs)
	}
	if e.jobs[1].Task != intent.Input {
		t.Fatalf("retry task = %q, want durable intent task %q", e.jobs[1].Task, intent.Input)
	}

	// Two queue deliveries still have one execution acceptance boundary: only the
	// first worker may transition the durable intent to Running.
	if _, err := st.ClaimRun(ctx, scope, intent.ID, "worker-1", now); err != nil {
		t.Fatalf("first worker claim: %v", err)
	}
	if _, err := st.ClaimRun(ctx, scope, intent.ID, "worker-2", now); !errors.Is(err, store.ErrRunAlreadyClaimed) {
		t.Fatalf("duplicate worker claim = %v, want ErrRunAlreadyClaimed", err)
	}
}

// TestRecoveryRunsThroughEngine proves the recovery handoff all the way from
// the stale sweep through the background VW adapter and resume preflight into
// the real engine. A recovery callback that only returns nil would miss the
// original failure mode: the sweep could report a run as resumed even though
// resumeRun failed before invoking the model.
func TestRecoveryRunsThroughEngine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newRecoverFixture(t)
	f.s.engine = engine.New()
	model := newFakeLLM(t, "recovered answer")

	agent := &agentsv1alpha1.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: agentsv1alpha1.SchemeGroupVersion.String(), Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: "scout"},
	}
	agent.Spec.DisplayName = "Scout"
	agent.Spec.Models = map[string]string{"chat": "main"}
	agentObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(agent)
	if err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: llm.SecretNamespace,
			Name:      llm.CredentialSecretName("main"),
		},
		Data: map[string][]byte{
			"provider": []byte(llm.ProviderOpenAICompatible),
			"baseURL":  []byte(model.srv.URL),
			"model":    []byte("gpt-4o"),
			"apiKey":   []byte("test-key"),
		},
	}
	secretObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
	if err != nil {
		t.Fatal(err)
	}
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		&unstructured.Unstructured{Object: agentObj},
		&unstructured.Unstructured{Object: secretObj},
	)
	b := &background{
		server: f.s,
		scopedFn: func(context.Context, string) (dynamic.Interface, error) {
			return dyn, nil
		},
	}

	at := time.Now().UTC().Add(-time.Hour)
	oldLease := at.Add(time.Minute)
	checkpoint, err := json.Marshal(runCheckpoint{Engine: engine.Checkpoint{
		Messages: []engine.CheckpointMessage{{Role: "user", Content: "resume me"}},
		Iter:     1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.SaveRun(ctx, f.scope, store.Run{
		ID: "recovered", AgentName: "scout", SessionID: "chat", Trigger: agentsv1alpha1.RunTriggerChat,
		Phase: store.RunPhaseRunning, Input: "resume me", Checkpoint: checkpoint,
		CreatedAt: at, UpdatedAt: at, ExecutionOwner: "old-owner", ExecutionEpoch: 2,
		LeaseUntil: &oldLease,
	}); err != nil {
		t.Fatal(err)
	}

	f.s.sweepStaleRuns(ctx, b.resumeRecoveredRun, nil)
	deadline := time.Now().Add(8 * time.Second)
	var got store.Run
	for {
		got, err = f.s.store.GetRun(ctx, f.scope, "recovered")
		if err != nil {
			t.Fatal(err)
		}
		if got.Phase == store.RunPhaseSucceeded || got.Phase == store.RunPhaseFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovered run remained %s: %+v", got.Phase, got)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got.Phase != store.RunPhaseSucceeded {
		t.Fatalf("recovered run phase = %s, message=%q", got.Phase, got.Message)
	}
	if got.Output != "recovered answer" {
		t.Fatalf("recovered output = %q", got.Output)
	}
	if model.calls.Load() == 0 {
		t.Fatal("recovery did not reach the model engine")
	}
	if got.ExecutionOwner == "old-owner" || got.ExecutionEpoch != 3 {
		t.Fatalf("recovery fence = %q/%d, want new owner epoch 3", got.ExecutionOwner, got.ExecutionEpoch)
	}
	messages, err := f.s.store.LoadRecentMessages(ctx, f.scope, "chat", 20)
	if err != nil {
		t.Fatal(err)
	}
	var assistant bool
	for _, message := range messages {
		if message.Role == "assistant" && message.Content == "recovered answer" {
			assistant = true
			break
		}
	}
	if !assistant {
		t.Fatal("recovery did not append the assistant result after the fenced terminal write")
	}
}

func TestExecuteTaskSuppressesUncommittedSuccess(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	model := newFakeLLM(t, "stale answer")
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", AgentName: "scout"}
	agent := &agentsv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: "scout"}}
	agent.Spec.DisplayName = "Scout"
	agent.Spec.Models = map[string]string{"chat": "main"}
	s := &Server{
		store:    terminalRejectStore{Store: mem},
		engine:   engine.New(),
		events:   newEventBus(),
		liveRuns: newRunRegistry(),
	}
	events, unsubscribe := s.events.subscribe(scope)
	defer unsubscribe()
	res, err := s.executeTask(ctx, taskRun{
		Creds: credsFor(model.srv.URL, map[string]string{"main": "gpt-4o"}),
		Scope: scope, Agent: agent, SessionID: "chat", Task: "answer", Trigger: agentsv1alpha1.RunTriggerChat,
	})
	if !errors.Is(err, store.ErrRunLeaseLost) {
		t.Fatalf("executeTask error = %v, want ErrRunLeaseLost", err)
	}
	if res.Content != "" {
		t.Fatalf("uncommitted result content = %q", res.Content)
	}
	run, err := mem.GetRun(ctx, scope, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != store.RunPhaseRunning {
		t.Fatalf("run phase = %s, want Running after rejected terminal write", run.Phase)
	}
	messages, err := mem.LoadRecentMessages(ctx, scope, "chat", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.Role == "assistant" {
			t.Fatalf("stale success appended assistant message: %+v", message)
		}
	}
	for {
		select {
		case event := <-events:
			if event.Type == "run" {
				if data, ok := event.Data.(map[string]any); ok && data["phase"] == string(store.RunPhaseSucceeded) {
					t.Fatal("stale success published a Succeeded event")
				}
			}
		default:
			return
		}
	}
}

func TestFinishRunOwnedPersistsAfterExecutionCancellation(t *testing.T) {
	ctx := context.Background()
	base := store.NewMemoryStore()
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", AgentName: "scout"}
	now := time.Now().UTC()
	if err := base.SaveRun(ctx, scope, store.Run{
		ID: "cancelled", AgentName: scope.AgentName, Trigger: agentsv1alpha1.RunTriggerChat,
		Phase: store.RunPhaseRunning, ExecutionOwner: "worker", ExecutionEpoch: 1,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	s := &Server{store: contextCheckingStore{Store: base}}
	if !s.finishRunOwned(cancelled, scope, "cancelled", "worker", 1,
		runOutcome{Phase: store.RunPhaseAborted, Message: "cancelled by user"}, now.Add(time.Second)) {
		t.Fatal("terminal persistence should survive the execution cancellation")
	}
	got, err := base.GetRun(ctx, scope, "cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != store.RunPhaseAborted || got.Message != "cancelled by user" {
		t.Fatalf("run = %+v, want persisted Aborted outcome", got)
	}
}

func TestChatHTTPStreamingPersistsAnnouncedRunAndReachesModel(t *testing.T) {
	ctx := context.Background()
	model := newFakeLLM(t, "streamed answer")
	agent := &agentsv1alpha1.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: agentsv1alpha1.SchemeGroupVersion.String(), Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: "scout"},
	}
	agent.Spec.DisplayName = "Scout"
	agent.Spec.Models = map[string]string{"chat": "main"}
	agentYAML, err := yaml.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: llm.SecretNamespace,
			Name:      llm.CredentialSecretName("main"),
		},
		Data: map[string][]byte{
			"provider": []byte(llm.ProviderOpenAICompatible),
			"baseURL":  []byte(model.srv.URL),
			"model":    []byte("gpt-4o"),
			"apiKey":   []byte("test-key"),
		},
	}
	secretYAML, err := yaml.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var data any
		switch {
		case strings.Contains(request.Query, "AgentYaml"):
			data = map[string]any{"agents_faros_sh": map[string]any{
				"v1alpha1": map[string]any{"AgentYaml": string(agentYAML)},
			}}
		case strings.Contains(request.Query, "SecretYaml"):
			data = map[string]any{"v1": map[string]any{"SecretYaml": string(secretYAML)}}
		default:
			// The chat path also probes the optional edges MCP endpoint. This
			// fixture intentionally leaves that endpoint absent so the test stays
			// focused on the tenant GraphQL/model path.
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(hub.Close)

	st := store.NewMemoryStore()
	s := &Server{
		cfg:      Config{HubURL: hub.URL},
		store:    st,
		gql:      tenant.NewGraphQLClient(hub.URL, false),
		engine:   engine.New(),
		events:   newEventBus(),
		liveRuns: newRunRegistry(),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/scout/chat", strings.NewReader(`{"message":"hello"}`))
	req.SetPathValue("name", "scout")
	req.Header.Set("X-Faros-Tenant", "root:faros:tenants:org:workspace")
	req.Header.Set("X-Faros-Cluster", "cluster")
	req.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()
	s.chat(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("chat status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "event: done") {
		t.Fatalf("chat did not reach the done event: %s", response.Body.String())
	}
	if model.calls.Load() == 0 {
		t.Fatal("streaming chat did not reach the model")
	}
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", AgentName: "scout"}
	page, err := st.QueryRuns(ctx, scope, store.RunQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Phase != store.RunPhaseSucceeded {
		t.Fatalf("runs = %+v, want one succeeded run", page.Items)
	}
	if !strings.Contains(response.Body.String(), fmt.Sprintf(`"runID":"%s"`, page.Items[0].ID)) {
		t.Fatalf("SSE start/done did not retain the persisted run ID %q: %s", page.Items[0].ID, response.Body.String())
	}
}
