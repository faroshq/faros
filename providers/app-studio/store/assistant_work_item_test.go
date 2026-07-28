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

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreWorkItemsAreIsolatedByProjectUID(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	first := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-old"}
	second := first
	second.ProjectUID = "project-new"

	if _, err := store.CreateWorkItemAndAssistantRun(ctx, first, testWorkItem("item-old", "user-old"), testWorkItemUser("user-old"), testWorkItemAssistant("assistant-old"), testWorkItemRun("run-old", "item-old", "user-old", "assistant-old")); err != nil {
		t.Fatalf("CreateWorkItemAndAssistantRun first: %v", err)
	}
	if _, err := store.CreateWorkItemAndAssistantRun(ctx, second, testWorkItem("item-new", "user-new"), testWorkItemUser("user-new"), testWorkItemAssistant("assistant-new"), testWorkItemRun("run-new", "item-new", "user-new", "assistant-new")); err != nil {
		t.Fatalf("CreateWorkItemAndAssistantRun second: %v", err)
	}
	if _, err := store.GetAssistantWorkItem(ctx, second, "item-old"); !errors.Is(err, ErrAssistantWorkItemNotFound) {
		t.Fatalf("old work item visible through recreated Project UID: %v", err)
	}
}

func TestMemoryStoreWorkItemLifecycleAndGrantAreAtomic(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	createdAt := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	item := testWorkItem("item-1", "user-1")
	item.CreatedAt = createdAt
	item.UpdatedAt = createdAt
	run := testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")
	run.CreatedAt = createdAt
	run.UpdatedAt = createdAt
	created, err := store.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), run)
	if err != nil {
		t.Fatalf("CreateWorkItemAndAssistantRun: %v", err)
	}
	if created.ActiveRunID != "run-1" || created.Revision != 1 {
		t.Fatalf("created work item = %#v", created)
	}

	secondItem := testWorkItem("item-2", "user-2")
	if _, err := store.CreateWorkItemAndAssistantRun(ctx, scope, secondItem, testWorkItemUser("user-2"), testWorkItemAssistant("assistant-2"), testWorkItemRun("run-2", "item-2", "user-2", "assistant-2")); !errors.Is(err, ErrAssistantWorkItemConflict) {
		t.Fatalf("second active work item error = %v, want conflict", err)
	}

	grant := json.RawMessage(`{"capabilities":["workspace_mutate"]}`)
	approved, err := store.ApproveWorkItemPlan(ctx, scope, item.ID, run.ID, created.Revision, "grant-1", grant, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("ApproveWorkItemPlan: %v", err)
	}
	if approved.GrantRevision != "grant-1" || string(approved.PlanGrant) != string(grant) {
		t.Fatalf("approved work item = %#v", approved)
	}
	persistedRun, err := store.GetAssistantRun(ctx, scope, run.ID)
	if err != nil {
		t.Fatalf("GetAssistantRun: %v", err)
	}
	if persistedRun.ExpectedGrantRevision != "grant-1" {
		t.Fatalf("run grant revision = %q, want grant-1", persistedRun.ExpectedGrantRevision)
	}

	persistedRun.Status = AssistantRunStatusCompleted
	persistedRun.Revision++
	persistedRun.Checkpoint = json.RawMessage(`{"must":"clear"}`)
	if err := store.TransitionWorkItemAndRun(ctx, scope, approved.ID, approved.Revision, persistedRun, AssistantWorkItemStatusCompleted, "completed", createdAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("TransitionWorkItemAndRun: %v", err)
	}
	terminal, err := store.GetAssistantWorkItem(ctx, scope, item.ID)
	if err != nil {
		t.Fatalf("GetAssistantWorkItem: %v", err)
	}
	if terminal.ActiveRunID != "" || terminal.GrantRevision != "" || len(terminal.PlanGrant) != 0 || terminal.Status != AssistantWorkItemStatusCompleted {
		t.Fatalf("terminal work item = %#v, want cleared run and grant", terminal)
	}
	persistedRun, err = store.GetAssistantRun(ctx, scope, run.ID)
	if err != nil {
		t.Fatalf("GetAssistantRun after transition: %v", err)
	}
	if len(persistedRun.Checkpoint) != 0 {
		t.Fatalf("terminal checkpoint = %s, want empty", persistedRun.Checkpoint)
	}
}

func TestMemoryStoreStopAndGrantRevocationAreAtomic(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	item := testWorkItem("item-1", "user-1")
	run := testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")
	created, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), run)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.ApproveWorkItemPlan(ctx, scope, item.ID, run.ID, created.Revision, "grant-1", json.RawMessage(`{"capabilities":["workspace_mutate"]}`), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.GetAssistantRun(ctx, scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	stopping, err := s.RequestAssistantRunStop(ctx, scope, item.ID, run.ID, approved.Revision, run.Revision, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if stopping.Status != AssistantRunStatusStopping || stopping.Revision != run.Revision+1 {
		t.Fatalf("stopping run = %#v", stopping)
	}
	revoked, err := s.GetAssistantWorkItem(ctx, scope, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.GrantRevision != "" || len(revoked.PlanGrant) != 0 || revoked.Revision != approved.Revision+1 {
		t.Fatalf("stopped WorkItem = %#v, want atomically revoked grant", revoked)
	}
}

func TestMemoryStoreResumeWorkItemAndCreateAssistantRunIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	item := testWorkItem("item-1", "user-1")
	first := testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")
	if _, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), first); err != nil {
		t.Fatal(err)
	}
	first.Status = AssistantRunStatusInterrupted
	first.Revision++
	if err := s.TransitionWorkItemAndRun(ctx, scope, item.ID, 1, first, AssistantWorkItemStatusSuspended, "interrupted", time.Now().UTC()); err != nil {
		t.Fatalf("suspend work item: %v", err)
	}

	nextUser := Message{ID: "user-2", Role: "user", ActorID: "actor-1", WorkItemID: item.ID, Content: "continue", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	nextAssistant := Message{ID: "assistant-2", Role: "assistant", WorkItemID: item.ID, CreatedAt: nextUser.CreatedAt, UpdatedAt: nextUser.UpdatedAt}
	nextRun := AssistantRun{ID: "run-2", WorkItemID: item.ID, Mode: AssistantRunModeContinue, Status: AssistantRunStatusRunning, ClientRequestID: "request-2", UserMessageID: nextUser.ID, ActiveMessageID: nextAssistant.ID, Revision: 1, CreatedAt: nextUser.CreatedAt, UpdatedAt: nextUser.UpdatedAt}
	resumed, err := s.ResumeWorkItemAndCreateAssistantRun(ctx, scope, item.ID, "actor-1", 2, nextUser, nextAssistant, nextRun)
	if err != nil {
		t.Fatalf("ResumeWorkItemAndCreateAssistantRun: %v", err)
	}
	if resumed.Status != AssistantWorkItemStatusActive || resumed.ActiveRunID != nextRun.ID || resumed.Revision != 3 {
		t.Fatalf("resumed work item = %#v", resumed)
	}
	if _, err := s.GetAssistantRun(ctx, scope, nextRun.ID); err != nil {
		t.Fatalf("continued run was not created: %v", err)
	}
	if _, err := s.ResumeWorkItemAndCreateAssistantRun(ctx, scope, item.ID, "other", resumed.Revision, nextUser, nextAssistant, nextRun); !errors.Is(err, ErrAssistantWorkItemConflict) {
		t.Fatalf("wrong actor error = %v, want work item conflict", err)
	}
}

func TestMemoryStoreRejectsImmutableWorkItemMembershipAndRunMode(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	item := testWorkItem("item-1", "user-1")
	run := testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")
	if _, err := store.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), run); err != nil {
		t.Fatalf("CreateWorkItemAndAssistantRun: %v", err)
	}
	if err := store.AppendMessage(ctx, scope, Message{ID: "user-1", Role: "user", ActorID: "actor-1", WorkItemID: "item-2"}); !errors.Is(err, ErrAssistantWorkItemConflict) {
		t.Fatalf("relink message error = %v, want work item conflict", err)
	}
	run.Mode = AssistantRunModeDiscussion
	run.Revision++
	if err := store.SaveAssistantRunSnapshot(ctx, scope, run, nil, 1); !errors.Is(err, ErrAssistantRunConflict) {
		t.Fatalf("change run mode error = %v, want run conflict", err)
	}
}

func TestMemoryStoreGenericMessageUpdatesPreserveActorAndWorkItem(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	item := testWorkItem("item-1", "user-1")
	if _, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")); !errors.Is(err, ErrAssistantWorkItemConflict) {
		t.Fatalf("duplicate WorkItem creation = %v, want conflict for API-level idempotency recovery", err)
	}
	if err := s.AppendMessage(ctx, scope, Message{ID: "user-1", Role: "user", Content: "changed", ActorID: "attacker", WorkItemID: item.ID}); !errors.Is(err, ErrAssistantWorkItemConflict) {
		t.Fatalf("generic actor rewrite = %v, want conflict", err)
	}
}

func TestMemoryStoreWorkItemAttachesMatchingUnassignedRootMessageOnce(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	if err := s.AppendMessage(ctx, scope, testWorkItemUser("user-1")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	item := testWorkItem("item-1", "user-1")
	if _, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")); err != nil {
		t.Fatalf("CreateWorkItemAndAssistantRun attach root: %v", err)
	}
	messages, err := s.LoadMessagesForWorkItem(ctx, scope, item.ID, 10)
	if err != nil || len(messages) != 2 || messages[0].ID != "user-1" {
		t.Fatalf("attached messages = %#v, err=%v", messages, err)
	}
	if _, err := s.CreateWorkItemAndAssistantRun(ctx, scope, testWorkItem("item-2", "user-1"), testWorkItemUser("user-1"), testWorkItemAssistant("assistant-2"), testWorkItemRun("run-2", "item-2", "user-1", "assistant-2")); !errors.Is(err, ErrAssistantWorkItemConflict) {
		t.Fatalf("second root attachment error = %v, want conflict", err)
	}
	otherStore := NewMemoryStore()
	if err := otherStore.AppendMessage(ctx, scope, testWorkItemUser("user-1")); err != nil {
		t.Fatalf("AppendMessage other store: %v", err)
	}
	otherActor := testWorkItemUser("user-1")
	otherActor.ActorID = "actor-2"
	if _, err := otherStore.CreateWorkItemAndAssistantRun(ctx, scope, testWorkItem("item-3", "user-1"), otherActor, testWorkItemAssistant("assistant-3"), testWorkItemRun("run-3", "item-3", "user-1", "assistant-3")); err == nil {
		t.Fatal("cross-actor root attachment unexpectedly succeeded")
	}
}

func TestMemoryStoreTerminalTransitionRequiresMatchingLifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	item := testWorkItem("item-1", "user-1")
	run := testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")
	created, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), run)
	if err != nil {
		t.Fatalf("CreateWorkItemAndAssistantRun: %v", err)
	}
	run.Status = AssistantRunStatusCompleted
	run.Revision++
	if err := s.TransitionWorkItemAndRun(ctx, scope, item.ID, created.Revision, run, AssistantWorkItemStatusSuspended, "wrong target", time.Now()); err == nil {
		t.Fatal("TransitionWorkItemAndRun accepted completed run as suspended work item")
	}
}

func TestMemoryStoreWorkItemCreationRejectsPreinstalledGrant(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	item := testWorkItem("item-1", "user-1")
	item.PlanGrant = json.RawMessage(`{"capabilities":["workspace_mutate"]}`)
	item.GrantRevision = "grant-1"
	run := testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")
	run.ExpectedGrantRevision = "grant-1"
	if _, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), run); err == nil {
		t.Fatal("CreateWorkItemAndAssistantRun accepted a preinstalled grant")
	}
}

func TestMemoryStoreApproveWorkItemPlanRequiresRunningRun(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	item := testWorkItem("item-1", "user-1")
	run := testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")
	created, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), run)
	if err != nil {
		t.Fatalf("CreateWorkItemAndAssistantRun: %v", err)
	}
	run.Status = AssistantRunStatusPendingPermission
	run.Revision++
	if err := s.SaveAssistantRunSnapshot(ctx, scope, run, nil, 1); err != nil {
		t.Fatalf("SaveAssistantRunSnapshot: %v", err)
	}
	if _, err := s.ApproveWorkItemPlan(ctx, scope, item.ID, run.ID, created.Revision, "grant-1", json.RawMessage(`{"capabilities":["workspace_mutate"]}`), time.Now()); !errors.Is(err, ErrAssistantRunConflict) {
		t.Fatalf("ApproveWorkItemPlan pending run error = %v, want run conflict", err)
	}
}

func TestMemoryStoreRetentionDoesNotOrphanWorkItemState(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	item := testWorkItem("item-1", "user-1")
	run := testWorkItemRun("run-1", item.ID, "user-1", "assistant-1")
	item.CreatedAt, item.UpdatedAt = now, now
	run.CreatedAt, run.UpdatedAt = now, now
	if _, err := s.CreateWorkItemAndAssistantRun(ctx, scope, item, testWorkItemUser("user-1"), testWorkItemAssistant("assistant-1"), run); err != nil {
		t.Fatalf("CreateWorkItemAndAssistantRun: %v", err)
	}
	if _, err := s.DeleteMessagesOlderThan(ctx, now.Add(time.Hour)); err != nil {
		t.Fatalf("DeleteMessagesOlderThan: %v", err)
	}
	if _, err := s.GetAssistantWorkItem(ctx, scope, item.ID); err != nil {
		t.Fatalf("active WorkItem was deleted or orphaned: %v", err)
	}
	if _, err := s.GetAssistantRun(ctx, scope, run.ID); err != nil {
		t.Fatalf("active WorkItem run was deleted: %v", err)
	}
	messages, err := s.LoadMessagesForWorkItem(ctx, scope, item.ID, 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("active WorkItem messages = %#v, err=%v", messages, err)
	}
}

func TestEncryptedWorkItemGrantBindsProjectUIDAndWorkItemID(t *testing.T) {
	wrapped, err := NewEncryptedStore(NewMemoryStore(), testEncryptionKeys(t))
	if err != nil {
		t.Fatalf("NewEncryptedStore: %v", err)
	}
	encrypted := wrapped.(*encryptedStore)
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	item := AssistantWorkItem{ID: "item-1"}
	ciphertext, err := encrypted.encryptAssistantWorkItemGrant(scope, item, json.RawMessage(`{"capabilities":["workspace_mutate"]}`))
	if err != nil {
		t.Fatalf("encryptAssistantWorkItemGrant: %v", err)
	}
	if string(ciphertext) == `{"capabilities":["workspace_mutate"]}` {
		t.Fatal("grant remained plaintext")
	}
	changedItem := AssistantWorkItem{ID: "item-2", PlanGrant: ciphertext}
	if err := encrypted.decryptAssistantWorkItemGrant(scope, &changedItem); err == nil {
		t.Fatal("grant decrypted after WorkItem ID substitution")
	}
	changedScope := scope
	changedScope.ProjectUID = "project-2"
	changedProject := AssistantWorkItem{ID: item.ID, PlanGrant: ciphertext}
	if err := encrypted.decryptAssistantWorkItemGrant(changedScope, &changedProject); err == nil {
		t.Fatal("grant decrypted after Project UID substitution")
	}
}

func testWorkItem(id, rootMessageID string) AssistantWorkItem {
	return AssistantWorkItem{ID: id, RootMessageID: rootMessageID, CreatedBy: "actor-1", Status: AssistantWorkItemStatusActive}
}

func testWorkItemUser(id string) Message {
	return Message{ID: id, Role: "user", Content: "Build it", ActorID: "actor-1"}
}

func testWorkItemAssistant(id string) Message {
	return Message{ID: id, Role: "assistant", Content: ""}
}

func testWorkItemRun(id, workItemID, userID, assistantID string) AssistantRun {
	return AssistantRun{ID: id, WorkItemID: workItemID, Mode: AssistantRunModeNew, Status: AssistantRunStatusRunning, ClientRequestID: id, UserMessageID: userID, ActiveMessageID: assistantID, Revision: 1}
}
