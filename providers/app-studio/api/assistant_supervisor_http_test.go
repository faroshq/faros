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
	"testing"
	"time"

	"github.com/faroshq/provider-app-studio/store"
)

func TestProjectAssistantDurableMetadataTracksEveryTransition(t *testing.T) {
	now := time.Now().UTC()
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, Revision: 2, CreatedAt: now, UpdatedAt: now}
	metadata := projectAssistantDurableMetadataForTransition(run, "Writing files", true, false, []projectToolCallStreamEvent{{
		ID: "tool-1", Name: projectToolWriteFile, Status: "running", Arguments: `{"path":"src/App.tsx"}`,
	}})
	if got := metadata[projectAssistantMetadataRevision]; got != int64(2) {
		t.Fatalf("revision = %#v, want current run revision", got)
	}
	if got := metadata[projectAssistantMetadataWorkingStatus]; got != "Writing files" {
		t.Fatalf("status = %#v, want Writing files", got)
	}
	if got := metadata[projectAssistantMetadataProvisional]; got != true {
		t.Fatalf("provisional = %#v, want true", got)
	}
	if _, ok := metadata[projectMessageMetadataAssistantActions]; !ok {
		t.Fatalf("metadata = %#v, want sanitized assistant actions", metadata)
	}

	run.Status = store.AssistantRunStatusCompleted
	run.Revision = 5
	metadata = projectAssistantDurableMetadataForTransition(run, "Completed", false, true, []projectToolCallStreamEvent{{
		ID: "tool-1", Name: projectToolWriteFile, Status: "succeeded", Arguments: `{"path":"src/App.tsx"}`,
	}})
	if got := metadata[projectAssistantMetadataRevision]; got != int64(5) {
		t.Fatalf("terminal revision = %#v, want 5", got)
	}
	if got := metadata[projectAssistantMetadataProvisional]; got != false {
		t.Fatalf("terminal provisional = %#v, want false", got)
	}
	if got := metadata[projectAssistantMetadataPreviewRefreshNeeded]; got != true {
		t.Fatalf("preview refresh = %#v, want true for successful mutation", got)
	}
}

func TestProjectAssistantDurableMetadataSurvivesStatusToolProvisionalAndTerminalTransitions(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	scope := store.Scope{OrgUUID: "org-1", WorkspaceUUID: "workspace-1", ProjectName: "project-1"}
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", Content: "make it", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", Metadata: projectAssistantDurableMetadataForTransition(run, "Working", false, false, nil), CreatedAt: now, UpdatedAt: now}
	msgStore := store.NewMemoryStore()
	if _, err := msgStore.CreateAssistantRun(ctx, scope, user, assistant, run); err != nil {
		t.Fatalf("CreateAssistantRun: %v", err)
	}
	supervisor := newProjectAssistantSupervisor(ctx, msgStore)
	accumulator, err := supervisor.Attach(scope, run, assistant)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	server := NewWithWorkspace(nil, msgStore, nil, "", false)
	status := "Preparing action"
	provisional := false
	var toolCalls []projectToolCallStreamEvent
	persist := func(runStatus *store.AssistantRunStatus) {
		t.Helper()
		if err := accumulator.UpdateSnapshot(ctx, func(current *store.AssistantRun, message *store.Message) {
			if runStatus != nil {
				current.Status = *runStatus
			}
			if assistantRunTerminal(current.Status) {
				provisional = false
			}
			next := *current
			next.Revision++
			message.Metadata = projectAssistantDurableMetadataForTransition(next, status, provisional, server.projectAssistantPreviewRefreshNeeded(ctx, projectWorkspaceScope(identity{}, scope.ProjectName), "", false, toolCalls), toolCalls)
		}); err != nil {
			t.Fatalf("UpdateSnapshot: %v", err)
		}
	}
	persist(nil)
	toolCalls = []projectToolCallStreamEvent{{ID: "tool-1", Name: projectToolWriteFile, Status: "running"}}
	persist(nil)
	provisional = true
	persist(nil)
	provisional = false
	persist(nil)
	toolCalls[0].Status = "succeeded"
	persist(nil)
	completed := store.AssistantRunStatusCompleted
	status = "Completed"
	persist(&completed)

	got, err := msgStore.ListMessages(ctx, scope, 10, "")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var message store.Message
	for _, candidate := range got.Items {
		if candidate.ID == assistant.ID {
			message = candidate
			break
		}
	}
	if got := message.Metadata[projectAssistantMetadataRunID]; got != run.ID {
		t.Fatalf("run ID = %#v, want %q", got, run.ID)
	}
	if got := message.Metadata[projectAssistantMetadataRevision]; got != int64(7) {
		t.Fatalf("revision = %#v, want latest persisted revision 7", got)
	}
	if got := message.Metadata[projectAssistantMetadataWorkingStatus]; got != "Completed" {
		t.Fatalf("status = %#v, want terminal status", got)
	}
	if got := message.Metadata[projectAssistantMetadataProvisional]; got != false {
		t.Fatalf("provisional = %#v, want reset at terminal", got)
	}
	if got := message.Metadata[projectAssistantMetadataPreviewRefreshNeeded]; got != true {
		t.Fatalf("preview refresh = %#v, want true after successful mutation", got)
	}
	if _, ok := message.Metadata[projectMessageMetadataAssistantActions]; !ok {
		t.Fatalf("metadata = %#v, tool update discarded actions", message.Metadata)
	}
}

func TestReconcileOrphanedProjectAssistantRunPersistsInterruptedMessageMetadata(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	scope := store.Scope{OrgUUID: "org-1", WorkspaceUUID: "workspace-1", ProjectName: "project-1"}
	run := store.AssistantRun{ID: "run-1", Status: store.AssistantRunStatusRunning, ClientRequestID: "request-1", ActiveMessageID: "assistant-1", Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-1", Role: "user", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", Metadata: projectAssistantDurableMetadataForTransition(run, "Working", false, false, nil), CreatedAt: now, UpdatedAt: now}
	msgStore := store.NewMemoryStore()
	if _, err := msgStore.CreateAssistantRun(ctx, scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	server := NewWithWorkspace(nil, msgStore, nil, "", false)
	if err := server.reconcileOrphanedProjectAssistantRun(ctx, scope); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	updatedRun, err := msgStore.GetAssistantRun(ctx, scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedRun.Status != store.AssistantRunStatusInterrupted || updatedRun.Revision != 2 {
		t.Fatalf("run = %#v, want interrupted revision 2", updatedRun)
	}
	page, err := msgStore.ListMessages(ctx, scope, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range page.Items {
		if message.ID != assistant.ID {
			continue
		}
		if message.Metadata[projectAssistantMetadataWorkingStatus] != "Interrupted" || message.Metadata[projectAssistantMetadataRevision] != int64(2) {
			t.Fatalf("message metadata = %#v, want interrupted revision 2", message.Metadata)
		}
		return
	}
	t.Fatal("assistant message not found")
}
