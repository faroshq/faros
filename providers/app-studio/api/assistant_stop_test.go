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
	"errors"
	"testing"
	"time"

	"github.com/faroshq/provider-app-studio/store"
)

func TestProjectAssistantStopPersistsStoppingBeforeCancellingWorker(t *testing.T) {
	messages := store.NewMemoryStore()
	supervisor := newProjectAssistantSupervisor(context.Background(), messages)
	scope := testProjectMessageScope("org-a", "workspace-a", "demo")
	now := time.Now().UTC()
	run := store.AssistantRun{
		ID: "run-1", Mode: store.AssistantRunModeDiscussion, Status: store.AssistantRunStatusRunning,
		ClientRequestID: "request-1", UserMessageID: "user-1", ActiveMessageID: "assistant-1",
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	user := store.Message{ID: run.UserMessageID, Role: "user", ActorID: "alice", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", CreatedAt: now, UpdatedAt: now}
	if _, err := messages.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	cancelled := make(chan error, 1)
	if err := supervisor.Start(context.Background(), scope, run, assistant, func(ctx context.Context, _ *projectAssistantSnapshotAccumulator) {
		<-ctx.Done()
		cancelled <- context.Cause(ctx)
	}); err != nil {
		t.Fatal(err)
	}

	stopping, found, err := supervisor.Stop(scope, run.ID)
	if err != nil || !found {
		t.Fatalf("Stop = %#v, %v, %v", stopping, found, err)
	}
	if stopping.Status != store.AssistantRunStatusStopping {
		t.Fatalf("status = %q, want stopping", stopping.Status)
	}
	persisted, err := messages.GetAssistantRun(context.Background(), scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != store.AssistantRunStatusStopping {
		t.Fatalf("persisted status = %q, want stopping", persisted.Status)
	}
	select {
	case cause := <-cancelled:
		if !errors.Is(cause, errProjectAssistantUserStop) {
			t.Fatalf("worker cause = %v, want user stop", cause)
		}
	case <-time.After(time.Second):
		t.Fatal("worker was not cancelled")
	}
	again, found, err := supervisor.Stop(scope, run.ID)
	if err != nil || !found || again.Status != store.AssistantRunStatusStopping {
		t.Fatalf("repeated Stop = %#v, %v, %v", again, found, err)
	}
}

func TestProjectAssistantToolRejectsAdmissionAfterStop(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errProjectAssistantUserStop)
	_, err := (projectEinoAssistantTool{}).InvokableRun(ctx, `{}`)
	if !errors.Is(err, errProjectAssistantUserStop) {
		t.Fatalf("InvokableRun error = %v, want user stop", err)
	}
}

func TestProjectAssistantStopClosesDurableWorkItemAdmissionAndRevokesGrant(t *testing.T) {
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, nil, "", false)
	scope := testProjectMessageScope("org-a", "workspace-a", "demo")
	started, err := server.startProjectAssistantBuildRunDurably(
		context.Background(), scope, "alice", "Implement dark mode", "build-1",
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	item, err := messages.GetAssistantWorkItem(context.Background(), scope, started.Run.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	item, err = messages.ApproveWorkItemPlan(
		context.Background(), scope, item.ID, started.Run.ID, item.Revision,
		"grant-1", []byte(`{"capabilities":["workspace_mutate"]}`), time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := messages.GetAssistantRun(context.Background(), scope, started.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := server.projectAssistantSupervisor()
	workerDone := make(chan struct{})
	if err := supervisor.Start(context.Background(), scope, run, started.Assistant, func(ctx context.Context, _ *projectAssistantSnapshotAccumulator) {
		<-ctx.Done()
		close(workerDone)
	}); err != nil {
		t.Fatal(err)
	}
	tool := projectEinoAssistantTool{
		server: server,
		req: projectAssistantRunRequest{
			Identity:     identity{user: "alice"},
			MessageScope: scope,
			AssistantRun: &run,
		},
	}
	writeSpec := projectAssistantToolSpec{Risk: projectAssistantToolRiskWrite}
	if err := tool.admitMutation(context.Background(), writeSpec); err != nil {
		t.Fatalf("admit before Stop: %v", err)
	}
	if _, found, err := supervisor.Stop(scope, run.ID); err != nil || !found {
		t.Fatalf("Stop = found %v, err %v", found, err)
	}
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("worker did not observe Stop")
	}
	if err := tool.admitMutation(context.Background(), writeSpec); !errors.Is(err, store.ErrAssistantRunConflict) {
		t.Fatalf("admit after Stop = %v, want run conflict", err)
	}
	item, err = messages.GetAssistantWorkItem(context.Background(), scope, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.GrantRevision != "" || len(item.PlanGrant) != 0 {
		t.Fatalf("grant survived Stop: %#v", item)
	}
}
