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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/tenant"
	"github.com/gorilla/mux"
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

func TestProjectAssistantSupervisorReservationProtectsFreshDurableRunUntilAttach(t *testing.T) {
	memoryStore := store.NewMemoryStore()
	supervisor := newProjectAssistantSupervisor(context.Background(), memoryStore)
	server := NewWithWorkspace(nil, memoryStore, nil, "", false)
	server.assistantSupervisor = supervisor
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	release, err := supervisor.Reserve(scope)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	defer release()
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", UserMessageID: "user-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", Metadata: map[string]any{
		projectMessageMetadataAssistantActions:       []projectAssistantUIAction{{ID: "prior", Status: "succeeded", Label: "Wrote file"}},
		projectAssistantMetadataPreviewRefreshNeeded: true,
		projectMessageMetadataAssistantInterrupt:     &projectAssistantUIInterruptRequest{InterruptID: "resolved"},
	}, CreatedAt: now, UpdatedAt: now}
	if _, err := memoryStore.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatalf("CreateAssistantRun: %v", err)
	}
	if err := server.reconcileOrphanedProjectAssistantRun(context.Background(), scope); err != nil {
		t.Fatalf("reconcile while reserved: %v", err)
	}
	persisted, err := memoryStore.GetAssistantRun(context.Background(), scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != store.AssistantRunStatusRunning {
		t.Fatalf("reserved fresh run was orphaned as %q", persisted.Status)
	}
	if _, err := supervisor.Attach(scope, persisted, assistant); err != nil {
		t.Fatalf("Attach: %v", err)
	}
}

func TestProjectAssistantSupervisorReservationReleaseAllowsRetryAfterStartFailure(t *testing.T) {
	supervisor := newProjectAssistantSupervisor(context.Background(), store.NewMemoryStore())
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	release, err := supervisor.Reserve(scope)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	// The HTTP start handler defers this release when durable creation or
	// attachment fails, so a subsequent caller is not wedged behind a stale
	// in-memory reservation.
	release()
	retryRelease, err := supervisor.Reserve(scope)
	if err != nil {
		t.Fatalf("Reserve after failed start release: %v", err)
	}
	retryRelease()
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

func TestProjectAssistantSupervisorTrailingFlushKeepsNewerText(t *testing.T) {
	memoryStore := store.NewMemoryStore()
	supervisor := newProjectAssistantSupervisor(context.Background(), memoryStore)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", Metadata: map[string]any{
		projectMessageMetadataAssistantActions:       []projectAssistantUIAction{{ID: "prior", Status: "succeeded", Label: "Wrote file"}},
		projectAssistantMetadataPreviewRefreshNeeded: true,
		projectMessageMetadataAssistantInterrupt:     &projectAssistantUIInterruptRequest{InterruptID: "resolved"},
	}, CreatedAt: now, UpdatedAt: now}
	created, err := memoryStore.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatal(err)
	}
	accumulator, err := supervisor.Attach(scope, created, assistant)
	if err != nil {
		t.Fatal(err)
	}
	if err := accumulator.UpdateText(context.Background(), "old", false); err != nil {
		t.Fatal(err)
	}
	supervisor.mu.Lock()
	active := supervisor.runs[projectAssistantRunKey{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID, ProjectName: scope.ProjectName}]
	active.beforeTextFlushPersist = func() {
		if updateErr := accumulator.UpdateText(context.Background(), "new", false); updateErr != nil {
			t.Errorf("UpdateText(new): %v", updateErr)
		}
	}
	supervisor.mu.Unlock()
	if err := accumulator.UpdateText(context.Background(), "old", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(projectAssistantTextSnapshotInterval + 50*time.Millisecond)
	page, err := memoryStore.ListMessages(context.Background(), scope, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range page.Items {
		if message.ID == assistant.ID {
			if message.Content != "new" {
				t.Fatalf("durable trailing text = %q, want newer chunk", message.Content)
			}
			return
		}
	}
	t.Fatal("assistant message not found")
}

func TestProjectAssistantSupervisorCursorAtTerminalRevisionCloses(t *testing.T) {
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
	accumulator, err := supervisor.Attach(scope, created, assistant)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := accumulator.SetStatus(context.Background(), store.AssistantRunStatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	updates, unsubscribe, err := supervisor.Subscribe(scope, created.ID, created.Revision+1)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()
	if updates == nil {
		t.Fatal("Subscribe returned a nil channel, which leaves an SSE handler open forever")
	}
	select {
	case _, open := <-updates:
		if open {
			t.Fatal("terminal cursor subscription stayed open")
		}
	case <-time.After(time.Second):
		t.Fatal("terminal cursor subscription did not close")
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

func TestProjectAssistantSupervisorAbortPersistsAuditAndClearsPendingInterrupt(t *testing.T) {
	memoryStore := store.NewMemoryStore()
	supervisor := newProjectAssistantSupervisor(context.Background(), memoryStore)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusPendingPermission, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", RequestID: "permission-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", Metadata: map[string]any{
		projectMessageMetadataStatus:             projectMessageStatusPendingPermission,
		projectMessageMetadataAssistantInterrupt: projectAssistantUIInterruptRequest{Action: &projectAssistantUIInterruptAction{RunID: run.ID, RequestID: run.RequestID}},
	}, CreatedAt: now, UpdatedAt: now}
	created, err := memoryStore.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Attach(scope, created, assistant); err != nil {
		t.Fatal(err)
	}
	aborted, err := supervisor.AbortWith(scope, created.ID, func(current *store.AssistantRun, message *store.Message) error {
		updated, auditErr := finalizeProjectAssistantRunAudit(*current, projectAssistantAuditOutcomeAborted, time.Now().UTC())
		if auditErr != nil {
			return auditErr
		}
		*current = updated
		projectAssistantClearPendingInterruptMetadata(message, current.ID)
		return nil
	})
	if err != nil || !aborted {
		t.Fatalf("AbortWith = (%v, %v), want (true, nil)", aborted, err)
	}
	got, err := memoryStore.GetAssistantRun(context.Background(), scope, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.AssistantRunStatusAborted {
		t.Fatalf("status = %q, want aborted", got.Status)
	}
	audit := decodeProjectAssistantRunAudit(t, got.Audit)
	if audit.Outcome != projectAssistantAuditOutcomeAborted {
		t.Fatalf("audit outcome = %q, want aborted", audit.Outcome)
	}
	page, err := memoryStore.ListMessages(context.Background(), scope, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range page.Items {
		if message.ID != assistant.ID {
			continue
		}
		if _, found := message.Metadata[projectMessageMetadataAssistantInterrupt]; found {
			t.Fatalf("assistant metadata still has pending interrupt: %#v", message.Metadata)
		}
		return
	}
	t.Fatal("assistant message not found")
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
	page, err := supervisor.store.ListMessages(context.Background(), scope, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("message count = %d, want 2", len(page.Items))
	}
	for _, message := range page.Items {
		if message.ID != assistant.ID {
			continue
		}
		if message.Metadata[projectAssistantMetadataWorkingStatus] != "Interrupted" || message.Metadata[projectAssistantMetadataRevision] != int64(2) {
			t.Fatalf("interrupted message metadata = %#v", message.Metadata)
		}
		break
	}
}

func TestProjectAssistantSupervisorShutdownLeavesPendingCheckpointResumable(t *testing.T) {
	msgStore := store.NewMemoryStore()
	supervisor := newProjectAssistantSupervisor(context.Background(), msgStore)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusPendingInput, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", Metadata: projectAssistantDurableMetadataForTransition(run, projectMessageStatusPendingInput, false, false, nil), CreatedAt: now, UpdatedAt: now}
	if _, err := msgStore.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Attach(scope, run, assistant); err != nil {
		t.Fatal(err)
	}
	supervisor.Shutdown(context.Background())
	got, err := msgStore.GetAssistantRun(context.Background(), scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.AssistantRunStatusPendingInput || got.Revision != 1 {
		t.Fatalf("pending run changed during shutdown: %#v", got)
	}
}

func TestProjectAssistantSupervisorParentCancellationPersistsInterrupted(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	supervisor := newProjectAssistantSupervisor(parent, store.NewMemoryStore())
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
	cancelParent()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not stop worker")
	}
	deadline := time.After(time.Second)
	for {
		got, getErr := supervisor.store.GetAssistantRun(context.Background(), scope, created.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if got.Status == store.AssistantRunStatusInterrupted {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("status = %q, want interrupted", got.Status)
		case <-time.After(time.Millisecond):
		}
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

func TestProjectAssistantSupervisorClaimPublishesRunningRevision(t *testing.T) {
	memoryStore := store.NewMemoryStore()
	supervisor := newProjectAssistantSupervisor(context.Background(), memoryStore)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusPendingPermission, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", RequestID: "permission-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", Metadata: map[string]any{
		projectMessageMetadataAssistantActions:       []projectAssistantUIAction{{ID: "prior", Status: "succeeded", Label: "Wrote file"}},
		projectAssistantMetadataPreviewRefreshNeeded: true,
		projectMessageMetadataAssistantInterrupt:     &projectAssistantUIInterruptRequest{InterruptID: "resolved"},
	}, CreatedAt: now, UpdatedAt: now}
	created, err := memoryStore.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatal(err)
	}
	accumulator, err := supervisor.Attach(scope, created, assistant)
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe, err := supervisor.Subscribe(scope, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	<-updates

	claimed, err := accumulator.ClaimPending(context.Background(), created.RequestID)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if claimed.Status != store.AssistantRunStatusRunning || claimed.Revision != created.Revision+1 {
		t.Fatalf("claimed run = %#v, want durable running revision", claimed)
	}
	page, err := memoryStore.ListMessages(context.Background(), scope, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range page.Items {
		if message.ID != assistant.ID {
			continue
		}
		if message.Metadata[projectAssistantMetadataWorkingStatus] != "Working" || message.Metadata[projectAssistantMetadataRevision] != claimed.Revision || message.Metadata[projectAssistantMetadataPreviewRefreshNeeded] != true {
			t.Fatalf("claimed message metadata = %#v, want durable running revision", message.Metadata)
		}
		if _, found := message.Metadata[projectMessageMetadataAssistantInterrupt]; found {
			t.Fatalf("claimed metadata retained resolved interrupt: %#v", message.Metadata)
		}
		if len(projectAssistantUIActionsFromMetadata(message.Metadata[projectMessageMetadataAssistantActions])) != 1 {
			t.Fatalf("claimed metadata lost prior action: %#v", message.Metadata)
		}
		break
	}
	if err := accumulator.UpdateSnapshot(context.Background(), func(current *store.AssistantRun, message *store.Message) {
		next := *current
		next.Revision++
		message.Metadata = projectAssistantDurableMetadataForTransition(next, "Writing files", false, true, []projectToolCallStreamEvent{{ID: "tool-1", Name: projectToolWriteFile, Status: "succeeded"}})
	}); err != nil {
		t.Fatalf("persist resumed tool metadata: %v", err)
	}
	if err := accumulator.SetStatus(context.Background(), store.AssistantRunStatusPendingInput); err != nil {
		t.Fatalf("persist resumed pending status: %v", err)
	}
	if err := accumulator.SetStatus(context.Background(), store.AssistantRunStatusCompleted); err != nil {
		t.Fatalf("persist resumed terminal status: %v", err)
	}
	page, err = memoryStore.ListMessages(context.Background(), scope, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range page.Items {
		if message.ID != assistant.ID {
			continue
		}
		if message.Metadata[projectAssistantMetadataWorkingStatus] != "Completed" || message.Metadata[projectAssistantMetadataPreviewRefreshNeeded] != true {
			t.Fatalf("resumed terminal metadata = %#v", message.Metadata)
		}
		if _, ok := message.Metadata[projectMessageMetadataAssistantActions]; !ok {
			t.Fatalf("resumed terminal metadata lost actions: %#v", message.Metadata)
		}
		break
	}
	select {
	case snapshot := <-updates:
		if snapshot.Run.Status != store.AssistantRunStatusCompleted || snapshot.Run.Revision <= claimed.Revision {
			t.Fatalf("snapshot = %#v, want later completed metadata revision", snapshot.Run)
		}
	case <-time.After(time.Second):
		t.Fatal("resumed metadata transitions did not publish a terminal snapshot")
	}
}

func TestResumedAssistantSegmentPublishesTerminalMessageAndRunAtomically(t *testing.T) {
	msgStore := store.NewMemoryStore()
	supervisor := newProjectAssistantSupervisor(context.Background(), msgStore)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", Metadata: projectAssistantDurableMetadataForTransition(run, "Working", false, false, nil), CreatedAt: now, UpdatedAt: now}
	if _, err := msgStore.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	accumulator, err := supervisor.Attach(scope, run, assistant)
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe, err := supervisor.Subscribe(scope, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	<-updates
	state := &projectAssistantDurableMetadataState{status: "Writing files", toolCalls: []projectToolCallStreamEvent{{ID: "tool-1", Name: projectToolWriteFile, Status: "succeeded"}}}
	server := NewWithWorkspace(nil, msgStore, nil, "", false)
	if err := server.persistProjectAssistantDurableMetadata(context.Background(), accumulator, projectWorkspaceScope(identity{}, scope.ProjectName), state, nil); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.UpdateText(context.Background(), "done", true); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.SetStatus(context.Background(), store.AssistantRunStatusCompleted); err != nil {
		t.Fatal(err)
	}
	var terminal projectAssistantRunSnapshot
	select {
	case terminal = <-updates:
	case <-time.After(time.Second):
		t.Fatal("missing resumed terminal snapshot")
	}
	if terminal.Run.Status != store.AssistantRunStatusCompleted || terminal.Message.Content != "done" || terminal.Message.Metadata[projectAssistantMetadataPreviewRefreshNeeded] != true {
		t.Fatalf("terminal snapshot = %#v", terminal)
	}
	if _, ok := terminal.Message.Metadata[projectMessageMetadataAssistantActions]; !ok {
		t.Fatalf("terminal metadata lost actions: %#v", terminal.Message.Metadata)
	}
}

type failingResumeSnapshotStore struct {
	store.Store
	err error
}

func (s failingResumeSnapshotStore) SaveAssistantRunSnapshot(context.Context, store.Scope, store.AssistantRun, []store.Message, int64) error {
	return s.err
}

func TestResumeSnapshotPersistenceFailurePreventsSuccessfulTerminalTransition(t *testing.T) {
	inner := store.NewMemoryStore()
	failing := failingResumeSnapshotStore{Store: inner, err: errors.New("snapshot unavailable")}
	supervisor := newProjectAssistantSupervisor(context.Background(), failing)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", CreatedAt: now, UpdatedAt: now}
	if _, err := inner.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	accumulator, err := supervisor.Attach(scope, run, assistant)
	if err != nil {
		t.Fatal(err)
	}
	if err := accumulator.UpdateText(context.Background(), "partial", true); err == nil {
		t.Fatal("expected snapshot persistence error")
	}
	if err := accumulator.SetStatus(context.Background(), store.AssistantRunStatusCompleted); err != nil {
		t.Fatal(err)
	}
	persisted, err := inner.GetAssistantRun(context.Background(), scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status == store.AssistantRunStatusCompleted {
		t.Fatalf("persistence failure allowed successful completion: %#v", persisted)
	}
}

func TestWriteProjectAssistantRunStartReturnsRunUserMessage(t *testing.T) {
	memoryStore := store.NewMemoryStore()
	server := NewWithWorkspace(nil, memoryStore, nil, "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	user := store.Message{ID: "user-z", Role: "user", Content: "build a todo app", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: now, UpdatedAt: now}
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", UserMessageID: user.ID, ActiveMessageID: assistant.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	created, err := memoryStore.CreateAssistantRun(context.Background(), scope, user, assistant, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := memoryStore.AppendMessage(context.Background(), scope, store.Message{ID: "user-a", Role: "user", Content: "an unrelated earlier-looking message", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.writeProjectAssistantRunStart(recorder, http.StatusAccepted, scope, created)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	var response projectAssistantRunStartResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.User == nil || response.User.ID != user.ID || response.User.Content != user.Content {
		t.Fatalf("response user = %#v, want %#v", response.User, projectMessageToAPI(user))
	}
}

func TestWriteProjectAssistantRunStartFindsOriginatingUserBeyondFirstFiveHundredMessages(t *testing.T) {
	memoryStore := store.NewMemoryStore()
	server := NewWithWorkspace(nil, memoryStore, nil, "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	user := store.Message{ID: "user-target", Role: "user", Content: "the intended prompt", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-target", Role: "assistant", CreatedAt: now, UpdatedAt: now}
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusCompleted, ClientRequestID: "request-1", UserMessageID: user.ID, ActiveMessageID: assistant.ID, Revision: 2, CreatedAt: now, UpdatedAt: now}
	if err := memoryStore.SaveAssistantRun(context.Background(), scope, run); err != nil {
		t.Fatal(err)
	}
	if err := memoryStore.AppendMessage(context.Background(), scope, user); err != nil {
		t.Fatal(err)
	}
	if err := memoryStore.AppendMessage(context.Background(), scope, assistant); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 550; i++ {
		if err := memoryStore.AppendMessage(context.Background(), scope, store.Message{ID: fmt.Sprintf("noise-%03d", i), Role: "user", Content: "unrelated", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	server.writeProjectAssistantRunStart(recorder, http.StatusAccepted, scope, run)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	var response projectAssistantRunStartResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.User == nil || response.User.ID != user.ID || response.Assistant.ID != assistant.ID {
		t.Fatalf("response did not load exact long-history messages: %#v", response)
	}
}

func TestProjectAssistantRunStartIdempotentLegacyRunOmitsUnknownUser(t *testing.T) {
	graphQL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ai_kedge_faros_sh": map[string]any{"v1alpha1": map[string]any{"ProjectYaml": "apiVersion: ai.kedge.faros.sh/v1alpha1\nkind: Project\nmetadata:\n  name: demo\nspec: {}\n"}}}})
	}))
	defer graphQL.Close()
	memoryStore := store.NewMemoryStore()
	server := NewWithWorkspace(tenant.NewGraphQLClient(graphQL.URL, false), memoryStore, nil, "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	assistant := store.Message{ID: "assistant-1", Role: "assistant", Content: "still readable", CreatedAt: now, UpdatedAt: now}
	if err := memoryStore.AppendMessage(context.Background(), scope, assistant); err != nil {
		t.Fatal(err)
	}
	legacy := store.AssistantRun{ID: "run-legacy", Status: store.AssistantRunStatusCompleted, ClientRequestID: "request-legacy", ActiveMessageID: assistant.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := memoryStore.SaveAssistantRun(context.Background(), scope, legacy); err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	server.Register(router)
	request := httptest.NewRequest(http.MethodPost, "/api/projects/demo/messages", strings.NewReader(`{"content":"retry","clientRequestID":"request-legacy"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org-a:workspace-a")
	request.Header.Set("X-Kedge-Cluster", "cluster-a")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("legacy retry status = %d, want %d: %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	var response projectAssistantRunStartResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Run.ID != legacy.ID || response.Assistant.ID != assistant.ID || response.User != nil {
		t.Fatalf("legacy retry response = %#v, want readable run/assistant and no fabricated user", response)
	}
}

func TestProjectAssistantSnapshotStreamReconcilesRestartedRunningRun(t *testing.T) {
	graphQL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(request.Query, "ProjectYaml") {
			t.Fatalf("unexpected GraphQL query: %s", request.Query)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"ai_kedge_faros_sh": map[string]any{"v1alpha1": map[string]any{"ProjectYaml": "apiVersion: ai.kedge.faros.sh/v1alpha1\nkind: Project\nmetadata:\n  name: demo\nspec: {}\n"}},
		}})
	}))
	defer graphQL.Close()
	memoryStore := store.NewMemoryStore()
	server := NewWithWorkspace(tenant.NewGraphQLClient(graphQL.URL, false), memoryStore, nil, "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", Content: "working", CreatedAt: now, UpdatedAt: now}
	if _, err := memoryStore.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	server.Register(router)
	request := httptest.NewRequest(http.MethodGet, "/api/projects/demo/assistant/run-1/stream", nil)
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org-a:workspace-a")
	request.Header.Set("X-Kedge-Cluster", "cluster-a")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	raw, found := firstSSELine(recorder.Body.Bytes())
	if !found {
		t.Fatalf("response did not contain an SSE snapshot: %s", recorder.Body.String())
	}
	var snapshot projectAssistantRunSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Run.Status != store.AssistantRunStatusInterrupted {
		t.Fatalf("streamed status = %q, want interrupted", snapshot.Run.Status)
	}
}

func TestProjectAssistantRunRoutesStartLatestAbortAndIsolateTenantStreams(t *testing.T) {
	settings := projectLLMSettings{Provider: defaultProjectLLMProvider, BaseURL: defaultProjectLLMBaseURL, Model: "test-model", APIKey: "test-key"}
	secret, err := json.Marshal(projectLLMSettingsSecret(settings).Object)
	if err != nil {
		t.Fatal(err)
	}
	graphQL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		switch {
		case strings.Contains(request.Query, "ProjectYaml"):
			response = map[string]any{"data": map[string]any{"ai_kedge_faros_sh": map[string]any{"v1alpha1": map[string]any{"ProjectYaml": "apiVersion: ai.kedge.faros.sh/v1alpha1\nkind: Project\nmetadata:\n  name: demo\nspec: {}\n"}}}}
		case strings.Contains(request.Query, "SecretYaml"):
			response = map[string]any{"data": map[string]any{"v1": map[string]any{"SecretYaml": string(secret)}}}
		default:
			t.Fatalf("unexpected GraphQL query: %s", request.Query)
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer graphQL.Close()
	memoryStore := store.NewMemoryStore()
	server := NewWithWorkspace(tenant.NewGraphQLClient(graphQL.URL, false), memoryStore, nil, "", false)
	engine := &blockingStartRouteEngine{entered: make(chan struct{}), finished: make(chan struct{})}
	server.assistantEngine = engine
	router := mux.NewRouter()
	server.Register(router)
	request := httptest.NewRequest(http.MethodPost, "/api/projects/demo/messages", strings.NewReader(`{"content":"build a todo app","clientRequestID":"request-1"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org-a:workspace-a")
	request.Header.Set("X-Kedge-Cluster", "cluster-a")
	started := httptest.NewRecorder()
	router.ServeHTTP(started, request)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want %d: %s", started.Code, http.StatusAccepted, started.Body.String())
	}
	var start projectAssistantRunStartResponse
	if err := json.NewDecoder(started.Body).Decode(&start); err != nil {
		t.Fatal(err)
	}
	if start.User == nil || start.Run.UserMessageID != start.User.ID || start.Assistant.ID != start.Run.ActiveMessageID {
		t.Fatalf("start response has unstable message identity: %#v", start)
	}
	select {
	case <-engine.entered:
	case <-time.After(time.Second):
		t.Fatal("start worker did not enter")
	}
	latest := httptest.NewRecorder()
	latestRequest := httptest.NewRequest(http.MethodGet, "/api/projects/demo/assistant/runs/latest", nil)
	latestRequest.Header = request.Header.Clone()
	router.ServeHTTP(latest, latestRequest)
	if latest.Code != http.StatusOK {
		t.Fatalf("latest status = %d, want %d: %s", latest.Code, http.StatusOK, latest.Body.String())
	}
	var latestSnapshot projectAssistantRunSnapshot
	if err := json.NewDecoder(latest.Body).Decode(&latestSnapshot); err != nil {
		t.Fatal(err)
	}
	if latestSnapshot.Run.ID != start.Run.ID || latestSnapshot.Run.Status != start.Run.Status || latestSnapshot.Run.Revision != start.Run.Revision || latestSnapshot.Message.ID != start.Run.ActiveMessageID {
		t.Fatalf("latest snapshot = %#v, want started run and active assistant message", latestSnapshot)
	}
	otherWorkspace := httptest.NewRecorder()
	otherWorkspaceRequest := httptest.NewRequest(http.MethodGet, "/api/projects/demo/assistant/"+start.Run.ID+"/stream", nil)
	otherWorkspaceRequest.Header = request.Header.Clone()
	otherWorkspaceRequest.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org-a:workspace-b")
	otherWorkspaceRequest.Header.Set("X-Kedge-Cluster", "cluster-a")
	router.ServeHTTP(otherWorkspace, otherWorkspaceRequest)
	if otherWorkspace.Code == http.StatusOK {
		t.Fatalf("cross-workspace stream unexpectedly succeeded: %s", otherWorkspace.Body.String())
	}
	abort := httptest.NewRecorder()
	abortRequest := httptest.NewRequest(http.MethodPost, "/api/projects/demo/assistant/"+start.Run.ID+"/abort", nil)
	abortRequest.Header = request.Header.Clone()
	router.ServeHTTP(abort, abortRequest)
	if abort.Code != http.StatusAccepted {
		t.Fatalf("abort status = %d, want %d: %s", abort.Code, http.StatusAccepted, abort.Body.String())
	}
	select {
	case <-engine.finished:
	case <-time.After(time.Second):
		t.Fatal("explicit abort did not stop started worker")
	}
}

func TestResumeProjectAssistantRouteDetachesRequestAndPublishesRunningSnapshot(t *testing.T) {
	settings := projectLLMSettings{Provider: defaultProjectLLMProvider, BaseURL: defaultProjectLLMBaseURL, Model: "test-model", APIKey: "test-key"}
	secret, err := json.Marshal(projectLLMSettingsSecret(settings).Object)
	if err != nil {
		t.Fatal(err)
	}
	graphQL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		switch {
		case strings.Contains(request.Query, "ProjectYaml"):
			response = map[string]any{"data": map[string]any{
				"ai_kedge_faros_sh": map[string]any{"v1alpha1": map[string]any{"ProjectYaml": "apiVersion: ai.kedge.faros.sh/v1alpha1\nkind: Project\nmetadata:\n  name: demo\nspec: {}\n"}},
			}}
		case strings.Contains(request.Query, "SecretYaml"):
			response = map[string]any{"data": map[string]any{"v1": map[string]any{"SecretYaml": string(secret)}}}
		default:
			t.Fatalf("unexpected GraphQL query: %s", request.Query)
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer graphQL.Close()
	memoryStore := store.NewMemoryStore()
	server := NewWithWorkspace(tenant.NewGraphQLClient(graphQL.URL, false), memoryStore, nil, "", false)
	engine := &blockingResumeRouteEngine{entered: make(chan struct{}), finished: make(chan struct{})}
	server.assistantEngine = engine
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo"}
	now := time.Now().UTC()
	checkpoint, err := json.Marshal(projectAssistantCheckpointState{Eino: &projectAssistantEinoCheckpointState{
		CheckpointID: "run-1", Checkpoint: []byte("checkpoint"), InterruptID: "interrupt-1", InterruptType: projectAssistantInterruptTypePermission, ToolCallID: "tool-1", ToolName: projectToolWriteFile,
	}})
	if err != nil {
		t.Fatal(err)
	}
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusPendingPermission, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", RequestID: "permission-1", Checkpoint: checkpoint, Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "hello", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: "assistant-1", Role: "assistant", CreatedAt: now, UpdatedAt: now}
	if _, err := memoryStore.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	server.Register(router)
	starter, cancelStarter := context.WithCancel(context.Background())
	defer cancelStarter()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/demo/assistant/run-1/resume", strings.NewReader(`{"requestID":"permission-1","decision":"allow"}`)).WithContext(starter)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org-a:workspace-a")
	request.Header.Set("X-Kedge-Cluster", "cluster-a")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("resume status = %d, want %d: %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	select {
	case <-engine.entered:
	case <-time.After(time.Second):
		got, getErr := memoryStore.GetAssistantRun(context.Background(), scope, run.ID)
		t.Fatalf("resume engine did not start; run = %#v, err = %v", got, getErr)
	}
	updates, unsubscribe, err := server.projectAssistantSupervisor().Subscribe(scope, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	select {
	case snapshot := <-updates:
		if snapshot.Run.Status != store.AssistantRunStatusRunning || snapshot.Run.Revision != 2 {
			t.Fatalf("resume snapshot = %#v, want running revision 2", snapshot.Run)
		}
	case <-time.After(time.Second):
		t.Fatal("resume did not publish running snapshot")
	}
	cancelStarter()
	select {
	case <-engine.finished:
		t.Fatal("canceling initiating HTTP request canceled resumed worker")
	case <-time.After(25 * time.Millisecond):
	}
	if !server.projectAssistantSupervisor().Abort(scope, run.ID) {
		t.Fatal("Abort did not find resumed worker")
	}
	select {
	case <-engine.finished:
	case <-time.After(time.Second):
		t.Fatal("Abort did not cancel resumed worker")
	}
}

type blockingResumeRouteEngine struct {
	entered  chan struct{}
	finished chan struct{}
}

type blockingStartRouteEngine struct {
	entered  chan struct{}
	finished chan struct{}
}

func (e *blockingStartRouteEngine) StreamProjectAssistant(ctx context.Context, _ projectAssistantRunRequest) (projectAssistantRunResult, error) {
	close(e.entered)
	<-ctx.Done()
	close(e.finished)
	return projectAssistantRunResult{}, context.Cause(ctx)
}

func (*blockingStartRouteEngine) ResumeProjectAssistant(context.Context, projectAssistantRunRequest, projectAssistantResumeRequest, projectAssistantCheckpointState) (projectAssistantRunResult, error) {
	return projectAssistantRunResult{}, errors.New("unexpected resume")
}

func (*blockingResumeRouteEngine) StreamProjectAssistant(context.Context, projectAssistantRunRequest) (projectAssistantRunResult, error) {
	return projectAssistantRunResult{}, errors.New("unexpected stream")
}

func (e *blockingResumeRouteEngine) ResumeProjectAssistant(ctx context.Context, _ projectAssistantRunRequest, _ projectAssistantResumeRequest, _ projectAssistantCheckpointState) (projectAssistantRunResult, error) {
	close(e.entered)
	<-ctx.Done()
	close(e.finished)
	return projectAssistantRunResult{}, context.Cause(ctx)
}
