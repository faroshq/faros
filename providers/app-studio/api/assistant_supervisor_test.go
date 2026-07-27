// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/faroshq/provider-app-studio/store"
)

func TestProjectAssistantSupervisorOwnsExecutionAfterStarterCancellation(t *testing.T) {
	supervisor := newProjectAssistantSupervisor(context.Background(), store.NewMemoryStore())
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: run.CreatedAt, UpdatedAt: run.CreatedAt}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: run.CreatedAt, UpdatedAt: run.CreatedAt}
	created, err := supervisor.store.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatalf("CreateAssistantRun: %v", err)
	}

	starter, cancelStarter := context.WithCancel(context.Background())
	started := make(chan struct{})
	finished := make(chan struct{})
	if err := supervisor.Start(starter, scope, created, assistant, func(ctx context.Context, snapshots *projectAssistantSnapshotAccumulator) {
		close(started)
		<-ctx.Done()
		close(finished)
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started
	cancelStarter()

	select {
	case <-finished:
		t.Fatal("starter cancellation canceled server-owned execution")
	case <-time.After(25 * time.Millisecond):
	}
	if !supervisor.Abort(scope, created.ID) {
		t.Fatal("Abort did not find active worker")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Abort did not cancel active worker")
	}
}

func TestProjectAssistantSupervisorCoalescesSlowSubscriberSnapshots(t *testing.T) {
	supervisor := newProjectAssistantSupervisor(context.Background(), store.NewMemoryStore())
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: run.CreatedAt, UpdatedAt: run.CreatedAt}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: run.CreatedAt, UpdatedAt: run.CreatedAt}
	created, err := supervisor.store.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatalf("CreateAssistantRun: %v", err)
	}
	accumulator, err := supervisor.Attach(scope, created, assistant)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	updates, unsubscribe, err := supervisor.Subscribe(scope, created.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()
	<-updates // initial snapshot

	for _, content := range []string{"one", "two", "three"} {
		if err := accumulator.UpdateText(context.Background(), content, true); err != nil {
			t.Fatalf("UpdateText(%q): %v", content, err)
		}
	}
	select {
	case snapshot := <-updates:
		if snapshot.Message.Content != "three" {
			t.Fatalf("coalesced content = %q, want latest", snapshot.Message.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive coalesced snapshot")
	}
}

func TestProjectAssistantSupervisorStartsOneWorkerForRun(t *testing.T) {
	supervisor := newProjectAssistantSupervisor(context.Background(), store.NewMemoryStore())
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: now, UpdatedAt: now}
	created, err := supervisor.store.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatalf("CreateAssistantRun: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	if err := supervisor.Start(context.Background(), scope, created, assistant, func(context.Context, *projectAssistantSnapshotAccumulator) { close(started); <-release }); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	<-started
	if err := supervisor.Start(context.Background(), scope, created, assistant, func(context.Context, *projectAssistantSnapshotAccumulator) { t.Fatal("duplicate worker executed") }); !errors.Is(err, store.ErrAssistantRunConflict) {
		t.Fatalf("duplicate Start error = %v, want conflict", err)
	}
	close(release)
}

func TestProjectAssistantSupervisorAbortCannotBeOverwrittenByLateCompletion(t *testing.T) {
	supervisor := newProjectAssistantSupervisor(context.Background(), store.NewMemoryStore())
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: now, UpdatedAt: now}
	created, err := supervisor.store.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatalf("CreateAssistantRun: %v", err)
	}
	acc, err := supervisor.Attach(scope, created, assistant)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !supervisor.Abort(scope, created.ID) {
		t.Fatal("Abort = false")
	}
	if err := acc.SetStatus(context.Background(), store.AssistantRunStatusCompleted); err != nil {
		t.Fatalf("late completion: %v", err)
	}
	got, err := supervisor.store.GetAssistantRun(context.Background(), scope, created.ID)
	if err != nil {
		t.Fatalf("GetAssistantRun: %v", err)
	}
	if got.Status != store.AssistantRunStatusAborted {
		t.Fatalf("status = %q, want aborted", got.Status)
	}
}

func TestProjectAssistantSupervisorShutdownInterruptsWorker(t *testing.T) {
	supervisor := newProjectAssistantSupervisor(context.Background(), store.NewMemoryStore())
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: now, UpdatedAt: now}
	created, err := supervisor.store.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	if err := supervisor.Start(context.Background(), scope, created, assistant, func(ctx context.Context, _ *projectAssistantSnapshotAccumulator) { <-ctx.Done(); close(done) }); err != nil {
		t.Fatal(err)
	}
	supervisor.Shutdown(context.Background())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker not canceled")
	}
	got, err := supervisor.store.GetAssistantRun(context.Background(), scope, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.AssistantRunStatusInterrupted {
		t.Fatalf("status = %q, want interrupted", got.Status)
	}
}

func TestProjectAssistantSupervisorReleasesPendingWorkerOwnership(t *testing.T) {
	supervisor := newProjectAssistantSupervisor(context.Background(), store.NewMemoryStore())
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusPendingPermission, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: now, UpdatedAt: now}
	created, err := supervisor.store.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan struct{})
	if err := supervisor.Start(context.Background(), scope, created, assistant, func(context.Context, *projectAssistantSnapshotAccumulator) { close(firstDone) }); err != nil {
		t.Fatal(err)
	}
	<-firstDone
	// finish runs after worker return; wait briefly for the ownership release.
	time.Sleep(10 * time.Millisecond)
	secondDone := make(chan struct{})
	if err := supervisor.Start(context.Background(), scope, created, assistant, func(context.Context, *projectAssistantSnapshotAccumulator) { close(secondDone) }); err != nil {
		t.Fatalf("resume Start: %v", err)
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("resumed worker did not start")
	}
}

func TestProjectAssistantSupervisorRestartAttachesPendingRunWithoutMutation(t *testing.T) {
	memoryStore := store.NewMemoryStore()
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusPendingInput, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: now, UpdatedAt: now}
	created, err := memoryStore.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := newProjectAssistantSupervisor(context.Background(), memoryStore)
	if _, err := supervisor.Attach(scope, created, assistant); err != nil {
		t.Fatal(err)
	}
	got, err := memoryStore.GetAssistantRun(context.Background(), scope, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.AssistantRunStatusPendingInput || got.Revision != 1 {
		t.Fatalf("restart attach mutated durable run: %#v", got)
	}
}
