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
	"sync"
	"testing"
	"time"

	"github.com/faroshq/provider-app-studio/store"
)

type failingAssistantThreadProjectionStore struct {
	store.Store
	appendFailures            int
	appendAfterCommitFailures int
	reloadFailuresAfterAppend int
	listFailures              int
	listCalls                 int
	saveFailures              int
	saveAfterCommitFailures   int
}

func (s *failingAssistantThreadProjectionStore) AppendAssistantThreadEvent(ctx context.Context, scope store.Scope, event store.AssistantThreadEvent, expectedSequence int64) (store.AssistantThreadEvent, error) {
	if s.appendFailures > 0 {
		s.appendFailures--
		return store.AssistantThreadEvent{}, errors.New("thread event append unavailable")
	}
	created, err := s.Store.AppendAssistantThreadEvent(ctx, scope, event, expectedSequence)
	if err == nil && s.appendAfterCommitFailures > 0 {
		s.appendAfterCommitFailures--
		s.listFailures += s.reloadFailuresAfterAppend
		s.reloadFailuresAfterAppend = 0
		return store.AssistantThreadEvent{}, errors.New("thread event acknowledgement unavailable")
	}
	return created, err
}

func (s *failingAssistantThreadProjectionStore) ListAssistantThreadEvents(ctx context.Context, scope store.Scope, threadID string, afterSequence int64, limit int) ([]store.AssistantThreadEvent, error) {
	s.listCalls++
	if s.listFailures > 0 {
		s.listFailures--
		return nil, errors.New("thread event reload unavailable")
	}
	return s.Store.ListAssistantThreadEvents(ctx, scope, threadID, afterSequence, limit)
}

type concurrentAssistantThreadPatchStore struct {
	store.Store
	getReady    chan struct{}
	updateReady chan struct{}
	mu          sync.Mutex
	gets        int
	updates     int
}

func (s *concurrentAssistantThreadPatchStore) GetAssistantThread(ctx context.Context, scope store.Scope, threadID string) (store.AssistantThread, error) {
	s.mu.Lock()
	s.gets++
	count := s.gets
	if count == 2 {
		close(s.getReady)
	}
	s.mu.Unlock()
	if count <= 2 {
		<-s.getReady
	}
	return s.Store.GetAssistantThread(ctx, scope, threadID)
}

func (s *concurrentAssistantThreadPatchStore) UpdateAssistantThreadWithEvent(ctx context.Context, scope store.Scope, thread store.AssistantThread, event store.AssistantThreadEvent, expectedSequence int64) (store.AssistantThread, store.AssistantThreadEvent, error) {
	s.mu.Lock()
	s.updates++
	count := s.updates
	if count == 2 {
		close(s.updateReady)
	}
	s.mu.Unlock()
	if count <= 2 {
		<-s.updateReady
	}
	return s.Store.UpdateAssistantThreadWithEvent(ctx, scope, thread, event, expectedSequence)
}

func (s *failingAssistantThreadProjectionStore) SaveAssistantTurnWithEvent(ctx context.Context, scope store.Scope, turn store.AssistantTurn, event store.AssistantThreadEvent, expectedSequence int64) error {
	if s.saveFailures > 0 {
		s.saveFailures--
		return errors.New("terminal turn save unavailable")
	}
	err := s.Store.SaveAssistantTurnWithEvent(ctx, scope, turn, event, expectedSequence)
	if err == nil && s.saveAfterCommitFailures > 0 {
		s.saveAfterCommitFailures--
		return errors.New("terminal turn acknowledgement unavailable")
	}
	return err
}

func TestProjectAssistantThreadSnapshotDoesNotAdvanceStateOnAppendFailure(t *testing.T) {
	inner := store.NewMemoryStore()
	failing := &failingAssistantThreadProjectionStore{Store: inner, appendFailures: 1}
	server := NewWithWorkspace(nil, failing, nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	now := time.Now().UTC()
	if _, err := inner.CreateAssistantThread(context.Background(), scope, store.AssistantThread{ID: "thread-mirror", ActorID: "alice", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	state := assistantThreadMirrorState{actionStatuses: map[string]string{}}
	turn := store.AssistantTurn{ID: "turn-mirror", ThreadID: "thread-mirror", ActorID: "alice", ClientUserMessageID: "client", Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, Status: store.AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
	run := store.AssistantRun{ID: turn.ID, ActiveMessageID: "assistant-mirror", Status: store.AssistantRunStatusRunning}
	snapshot := projectAssistantRunSnapshot{Run: run, Message: store.Message{ID: run.ActiveMessageID, Content: "hello"}}
	if err := server.projectAssistantThreadSnapshot(context.Background(), scope, turn.ThreadID, turn, run, &state, snapshot); err == nil {
		t.Fatal("expected append failure")
	}
	if state.lastContent != "" {
		t.Fatalf("mirror state advanced after failed append: %q", state.lastContent)
	}
	events, err := inner.ListAssistantThreadEvents(context.Background(), scope, turn.ThreadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events after failed append = %#v, want none", events)
	}
	if err := server.projectAssistantThreadSnapshot(context.Background(), scope, turn.ThreadID, turn, run, &state, snapshot); err != nil {
		t.Fatal(err)
	}
	if state.lastContent != "hello" {
		t.Fatalf("mirror state after retry = %q, want hello", state.lastContent)
	}
	events, err = inner.ListAssistantThreadEvents(context.Background(), scope, turn.ThreadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != assistantThreadEventItemDelta {
		t.Fatalf("events after successful retry = %#v", events)
	}
}

func TestProjectAssistantThreadMirrorRetriesTransientProjectionFailure(t *testing.T) {
	inner := store.NewMemoryStore()
	failing := &failingAssistantThreadProjectionStore{Store: inner, appendFailures: 1}
	server := NewWithWorkspace(nil, failing, nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	now := time.Now().UTC()
	if _, err := inner.CreateAssistantThread(context.Background(), scope, store.AssistantThread{ID: "thread-retry", ActorID: "alice", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	state := assistantThreadMirrorState{actionStatuses: map[string]string{}}
	turn := store.AssistantTurn{ID: "turn-retry", ThreadID: "thread-retry", ActorID: "alice", ClientUserMessageID: "client", Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, Status: store.AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
	run := store.AssistantRun{ID: turn.ID, ActiveMessageID: "assistant-retry", Status: store.AssistantRunStatusRunning}
	snapshot := projectAssistantRunSnapshot{Run: run, Message: store.Message{ID: run.ActiveMessageID, Content: "hello"}}
	if err := server.projectAssistantThreadSnapshotWithRetry(context.Background(), scope, turn.ThreadID, turn, run, &state, snapshot); err != nil {
		t.Fatal(err)
	}
	events, err := inner.ListAssistantThreadEvents(context.Background(), scope, turn.ThreadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != assistantThreadEventItemDelta || state.lastContent != "hello" {
		t.Fatalf("projection after retry = events %#v state %#v", events, state)
	}
}

func TestProjectAssistantThreadMirrorDoesNotReloadDurableHistoryPerSnapshot(t *testing.T) {
	inner := store.NewMemoryStore()
	failing := &failingAssistantThreadProjectionStore{Store: inner}
	server := NewWithWorkspace(nil, failing, nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	now := time.Now().UTC()
	if _, err := inner.CreateAssistantThread(context.Background(), scope, store.AssistantThread{ID: "thread-cache", ActorID: "alice", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	state := assistantThreadMirrorState{}
	turn := store.AssistantTurn{ID: "turn-cache", ThreadID: "thread-cache", ActorID: "alice", ClientUserMessageID: "client", Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, Status: store.AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
	run := store.AssistantRun{ID: turn.ID, ActiveMessageID: "assistant-cache", Status: store.AssistantRunStatusRunning}
	for _, content := range []string{"hello", "hello world"} {
		if err := server.projectAssistantThreadSnapshotWithRetry(context.Background(), scope, turn.ThreadID, turn, run, &state, projectAssistantRunSnapshot{Run: run, Message: store.Message{ID: run.ActiveMessageID, Content: content}}); err != nil {
			t.Fatal(err)
		}
	}
	if failing.listCalls != 1 {
		t.Fatalf("durable history loads = %d, want one initial reconstruction", failing.listCalls)
	}
}

func TestLoadAssistantThreadMirrorStateRetriesTransientReadFailure(t *testing.T) {
	inner := store.NewMemoryStore()
	failing := &failingAssistantThreadProjectionStore{Store: inner, listFailures: 1}
	server := NewWithWorkspace(nil, failing, nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	now := time.Now().UTC()
	if _, err := inner.CreateAssistantThread(context.Background(), scope, store.AssistantThread{ID: "thread-load-retry", ActorID: "alice", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	state, err := server.loadAssistantThreadMirrorStateWithRetry(context.Background(), scope, "thread-load-retry", "assistant-load-retry", "turn-load-retry")
	if err != nil {
		t.Fatal(err)
	}
	if state.actionStatuses == nil {
		t.Fatal("retried mirror state did not initialize action statuses")
	}
}

func TestAssistantThreadProjectionLockIsReclaimed(t *testing.T) {
	server := NewWithWorkspace(nil, store.NewMemoryStore(), nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	release := server.acquireAssistantThreadProjectionLock(scope, "thread-lock", "turn-lock")
	if len(server.assistantProjectionLocks) != 1 {
		t.Fatalf("projection lock entries = %d, want 1", len(server.assistantProjectionLocks))
	}
	release()
	if len(server.assistantProjectionLocks) != 0 {
		t.Fatalf("projection lock entries after release = %d, want 0", len(server.assistantProjectionLocks))
	}
}

func TestProjectAssistantThreadMirrorReconcilesAmbiguousAppendCommit(t *testing.T) {
	inner := store.NewMemoryStore()
	failing := &failingAssistantThreadProjectionStore{Store: inner, appendAfterCommitFailures: 1, reloadFailuresAfterAppend: 1}
	server := NewWithWorkspace(nil, failing, nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	now := time.Now().UTC()
	if _, err := inner.CreateAssistantThread(context.Background(), scope, store.AssistantThread{ID: "thread-ambiguous", ActorID: "alice", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	state := assistantThreadMirrorState{actionStatuses: map[string]string{}}
	turn := store.AssistantTurn{ID: "turn-ambiguous", ThreadID: "thread-ambiguous", ActorID: "alice", ClientUserMessageID: "client", Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, Status: store.AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
	run := store.AssistantRun{ID: turn.ID, ActiveMessageID: "assistant-ambiguous", Status: store.AssistantRunStatusRunning}
	snapshot := projectAssistantRunSnapshot{Run: run, Message: store.Message{ID: run.ActiveMessageID, Content: "hello"}}
	if err := server.projectAssistantThreadSnapshotWithRetry(context.Background(), scope, turn.ThreadID, turn, run, &state, snapshot); err != nil {
		t.Fatal(err)
	}
	events, err := inner.ListAssistantThreadEvents(context.Background(), scope, turn.ThreadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := countAssistantThreadMirrorTestEvents(events, assistantThreadEventItemDelta, run.ActiveMessageID); got != 1 {
		t.Fatalf("ambiguous append produced %d deltas, want 1: %#v", got, events)
	}
	if state.lastContent != "hello" {
		t.Fatalf("reconciled content = %q, want hello", state.lastContent)
	}
}

func TestProjectAssistantThreadMirrorReconcilesAmbiguousTerminalCommit(t *testing.T) {
	inner := store.NewMemoryStore()
	failing := &failingAssistantThreadProjectionStore{Store: inner, saveAfterCommitFailures: 1}
	server := NewWithWorkspace(nil, failing, nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	now := time.Now().UTC()
	if _, err := inner.CreateAssistantThread(context.Background(), scope, store.AssistantThread{ID: "thread-terminal-ambiguous", ActorID: "alice", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	turn := store.AssistantTurn{ID: "turn-terminal-ambiguous", ThreadID: "thread-terminal-ambiguous", ActorID: "alice", ClientUserMessageID: "client", Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, Status: store.AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
	if _, err := inner.CreateAssistantTurn(context.Background(), scope, turn, nil); err != nil {
		t.Fatal(err)
	}
	run := store.AssistantRun{ID: turn.ID, Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, ClientRequestID: "client", ActiveMessageID: "assistant-terminal-ambiguous", Status: store.AssistantRunStatusRunning, Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-terminal-ambiguous", Role: "user", ActorID: "alice", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", Content: "done", CreatedAt: now, UpdatedAt: now}
	run.UserMessageID = user.ID
	if _, err := inner.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	run.Status = store.AssistantRunStatusCompleted
	state := assistantThreadMirrorState{actionStatuses: map[string]string{}}
	snapshot := projectAssistantRunSnapshot{Run: run, Message: assistant}
	if err := server.projectAssistantThreadSnapshotWithRetry(context.Background(), scope, turn.ThreadID, turn, run, &state, snapshot); err != nil {
		t.Fatal(err)
	}
	events, err := inner.ListAssistantThreadEvents(context.Background(), scope, turn.ThreadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := countAssistantThreadMirrorTestEvents(events, assistantThreadEventItemCompleted, run.ActiveMessageID); got != 1 {
		t.Fatalf("ambiguous terminal commit produced %d terminal items, want 1: %#v", got, events)
	}
	if got := countAssistantThreadMirrorTestEvents(events, assistantThreadEventTurnCompleted, ""); got != 1 {
		t.Fatalf("ambiguous terminal commit produced %d terminal events, want 1: %#v", got, events)
	}
	if !state.terminalEvent {
		t.Fatal("ambiguous terminal commit did not reconcile terminal state")
	}
}

func TestReconcileOrphanedAssistantTurnResolvesPendingApproval(t *testing.T) {
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	now := time.Now().UTC()
	threadID := "thread-orphaned-approval"
	turnID := "turn-orphaned-approval"
	requestID := "approval-orphaned"
	if _, err := messages.CreateAssistantThread(context.Background(), scope, store.AssistantThread{ID: threadID, ActorID: "alice", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	turn := store.AssistantTurn{ID: turnID, ThreadID: threadID, ActorID: "alice", ClientUserMessageID: "client", Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, Status: store.AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
	if _, err := messages.CreateAssistantTurn(context.Background(), scope, turn, []store.AssistantThreadEvent{{
		Type: assistantThreadEventApprovalRequested, ItemID: requestID, RequestID: requestID,
		Payload: []byte(`{"requestID":"approval-orphaned","item":{"id":"approval-orphaned","type":"approval","status":"in_progress"}}`),
	}}); err != nil {
		t.Fatal(err)
	}
	run := store.AssistantRun{ID: turnID, Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, ClientRequestID: "client", ActiveMessageID: "assistant-orphaned-approval", RequestID: requestID, Status: store.AssistantRunStatusInterrupted, Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-orphaned-approval", Role: "user", ActorID: "alice", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", Content: "Waiting for approval", CreatedAt: now, UpdatedAt: now}
	run.UserMessageID = user.ID
	if _, err := messages.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}

	if err := server.reconcileProjectAssistantThreadTurn(context.Background(), scope, turn); err != nil {
		t.Fatal(err)
	}
	events, err := messages.ListAssistantThreadEvents(context.Background(), scope, threadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	resolvedSequence, terminalSequence := int64(0), int64(0)
	for _, event := range events {
		switch event.Type {
		case assistantThreadEventApprovalResolved:
			if event.RequestID == requestID && event.ItemID == requestID {
				resolvedSequence = event.Sequence
			}
		case assistantThreadEventTurnInterrupted:
			terminalSequence = event.Sequence
		}
	}
	if resolvedSequence == 0 || terminalSequence == 0 || resolvedSequence >= terminalSequence {
		t.Fatalf("orphaned approval resolution sequence=%d terminal sequence=%d events=%#v", resolvedSequence, terminalSequence, events)
	}

	if err := server.reconcileProjectAssistantThreadTurn(context.Background(), scope, turn); err != nil {
		t.Fatal(err)
	}
	reconciled, err := messages.ListAssistantThreadEvents(context.Background(), scope, threadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled) != len(events) {
		t.Fatalf("idempotent orphan reconciliation added events: before=%d after=%d", len(events), len(reconciled))
	}
}

func TestMaterializeAssistantThreadItemsRetiresLegacyTerminalApproval(t *testing.T) {
	events := []store.AssistantThreadEvent{
		{
			TurnID: "turn-legacy", Sequence: 1, Type: assistantThreadEventApprovalRequested,
			ItemID: "approval-legacy", RequestID: "approval-legacy",
			Payload: []byte(`{"item":{"id":"approval-legacy","turnID":"turn-legacy","type":"approval","status":"in_progress"}}`),
		},
		{TurnID: "turn-legacy", Sequence: 2, Type: assistantThreadEventTurnInterrupted},
	}
	items := materializeAssistantThreadItems(events)
	if len(items) != 1 || items[0].ID != "approval-legacy" || items[0].Status != "completed" {
		t.Fatalf("legacy terminal approval items = %#v, want completed approval", items)
	}
}

func TestProjectAssistantThreadSnapshotRetryDoesNotDuplicateTerminalEvents(t *testing.T) {
	inner := store.NewMemoryStore()
	failing := &failingAssistantThreadProjectionStore{Store: inner, saveFailures: 1}
	server := NewWithWorkspace(nil, failing, nil, "", false)
	scope := store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	now := time.Now().UTC()
	if _, err := inner.CreateAssistantThread(context.Background(), scope, store.AssistantThread{ID: "thread-terminal", ActorID: "alice", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	turn := store.AssistantTurn{ID: "turn-terminal", ThreadID: "thread-terminal", ActorID: "alice", ClientUserMessageID: "client", Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, Status: store.AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
	if _, err := inner.CreateAssistantTurn(context.Background(), scope, turn, nil); err != nil {
		t.Fatal(err)
	}
	run := store.AssistantRun{ID: turn.ID, Mode: store.AssistantRunModeDefault, ApprovalMode: store.AssistantApprovalModeOnRequest, ClientRequestID: "client", ActiveMessageID: "assistant-terminal", Status: store.AssistantRunStatusRunning, Revision: 1, CreatedAt: now, UpdatedAt: now}
	user := store.Message{ID: "user-terminal", Role: "user", ActorID: "alice", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", Content: "done", CreatedAt: now, UpdatedAt: now}
	run.UserMessageID = user.ID
	if _, err := inner.CreateAssistantRun(context.Background(), scope, user, assistant, run); err != nil {
		t.Fatal(err)
	}
	run.Status = store.AssistantRunStatusCompleted
	snapshot := projectAssistantRunSnapshot{Run: run, Message: assistant}
	state := assistantThreadMirrorState{actionStatuses: map[string]string{}}
	if err := server.projectAssistantThreadSnapshot(context.Background(), scope, turn.ThreadID, turn, run, &state, snapshot); err == nil {
		t.Fatal("expected terminal save failure")
	}
	if !state.terminalItem || state.terminalEvent {
		t.Fatalf("state after partial terminal projection = %#v", state)
	}
	events, err := inner.ListAssistantThreadEvents(context.Background(), scope, turn.ThreadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := countAssistantThreadMirrorTestEvents(events, assistantThreadEventItemCompleted, run.ActiveMessageID); got != 1 {
		t.Fatalf("terminal item events after failed save = %d, want 1: %#v", got, events)
	}
	if got := countAssistantThreadMirrorTestEvents(events, assistantThreadEventTurnCompleted, ""); got != 0 {
		t.Fatalf("terminal turn events after failed save = %d, want 0: %#v", got, events)
	}
	if err := server.projectAssistantThreadSnapshot(context.Background(), scope, turn.ThreadID, turn, run, &state, snapshot); err != nil {
		t.Fatal(err)
	}
	if !state.terminalEvent {
		t.Fatal("successful retry did not commit terminal event")
	}
	events, err = inner.ListAssistantThreadEvents(context.Background(), scope, turn.ThreadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := countAssistantThreadMirrorTestEvents(events, assistantThreadEventItemCompleted, run.ActiveMessageID); got != 1 {
		t.Fatalf("terminal item events after retry = %d, want 1: %#v", got, events)
	}
	if got := countAssistantThreadMirrorTestEvents(events, assistantThreadEventTurnCompleted, ""); got != 1 {
		t.Fatalf("terminal turn events after retry = %d, want 1: %#v", got, events)
	}
	eventCount := len(events)
	reconciled, err := server.loadAssistantThreadMirrorState(context.Background(), scope, turn.ThreadID, run.ActiveMessageID, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.projectAssistantThreadSnapshot(context.Background(), scope, turn.ThreadID, turn, run, &reconciled, snapshot); err != nil {
		t.Fatal(err)
	}
	events, err = inner.ListAssistantThreadEvents(context.Background(), scope, turn.ThreadID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != eventCount {
		t.Fatalf("events after idempotent reconcile = %#v, want %d", events, eventCount)
	}
}

func countAssistantThreadMirrorTestEvents(events []store.AssistantThreadEvent, eventType, itemID string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType && (itemID == "" || event.ItemID == itemID) {
			count++
		}
	}
	return count
}
