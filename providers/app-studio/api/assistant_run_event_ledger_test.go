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
	"sync"
	"testing"
	"time"

	"github.com/faroshq/provider-app-studio/store"
)

func TestAssistantRunEventLedgerPersistsCallBeforeResultInOrder(t *testing.T) {
	ctx := context.Background()
	messageStore, scope := newAssistantRunEventLedgerTestStore(t, "run-order")
	ledger := newProjectAssistantRunEventLedger(messageStore, scope, "run-order")
	spec := projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite}

	decision, err := ledger.BeginToolCall(ctx, "call-patch", spec, map[string]any{
		"patch": "*** Begin Patch\n*** End Patch",
	})
	if err != nil {
		t.Fatalf("BeginToolCall: %v", err)
	}
	if !decision.ShouldDispatch() {
		t.Fatalf("first call decision = %#v, want dispatch", decision)
	}
	events := listAssistantRunEventLedgerEvents(t, messageStore, scope, "run-order")
	if len(events) != 1 || events[0].Type != projectAssistantRunToolCallEventType || events[0].Sequence != 1 {
		t.Fatalf("events before dispatch = %#v, want one call event", events)
	}

	wantResult := `{"status":"applied","path":"src/App.vue"}`
	outcome, err := ledger.FinishToolCall(ctx, decision.Token, wantResult, nil)
	if err != nil {
		t.Fatalf("FinishToolCall: %v", err)
	}
	if outcome.Result != wantResult || outcome.Failed {
		t.Fatalf("outcome = %#v, want exact successful result", outcome)
	}
	events = listAssistantRunEventLedgerEvents(t, messageStore, scope, "run-order")
	if len(events) != 2 || events[1].Type != projectAssistantRunToolResultEventType || events[1].Sequence != 2 {
		t.Fatalf("events after dispatch = %#v, want ordered call then result", events)
	}
	if events[0].CallID != events[1].CallID || events[0].ArgsDigest != events[1].ArgsDigest {
		t.Fatalf("call/result identity mismatch: %#v", events)
	}
}

func TestAssistantRunEventLedgerReplaysExactCompletedCall(t *testing.T) {
	ctx := context.Background()
	messageStore, scope := newAssistantRunEventLedgerTestStore(t, "run-replay")
	spec := projectAssistantToolSpec{Name: projectToolReadFile, Risk: projectAssistantToolRiskRead}
	ledger := newProjectAssistantRunEventLedger(messageStore, scope, "run-replay")

	decision, err := ledger.BeginToolCall(ctx, "call-read", spec, map[string]any{"path": "src/App.vue", "limit": 200})
	if err != nil {
		t.Fatalf("BeginToolCall: %v", err)
	}
	wantResult := "Tool call failed: exact model-visible failure"
	wantError := errors.New("exact model-visible failure")
	if _, err := ledger.FinishToolCall(ctx, decision.Token, wantResult, wantError); err != nil {
		t.Fatalf("FinishToolCall: %v", err)
	}

	// Reconstruct from durable state and provide the same arguments in a
	// different map insertion order. Canonical JSON makes this the same call.
	restarted := newProjectAssistantRunEventLedger(messageStore, scope, "run-replay")
	replay, err := restarted.BeginToolCall(ctx, "call-read", spec, map[string]any{"limit": 200, "path": "src/App.vue"})
	if err != nil {
		t.Fatalf("BeginToolCall replay: %v", err)
	}
	if replay.ShouldDispatch() || replay.Replay == nil {
		t.Fatalf("replay decision = %#v, want durable replay", replay)
	}
	if replay.Replay.Result != wantResult || replay.Replay.Error != wantError.Error() || !replay.Replay.Failed {
		t.Fatalf("replayed outcome = %#v, want exact result and error", replay.Replay)
	}
	result, invokeErr := replay.Replay.InvokeResult()
	if result != wantResult || invokeErr == nil || invokeErr.Error() != wantError.Error() {
		t.Fatalf("InvokeResult = (%q, %v), want exact persisted values", result, invokeErr)
	}
	if events := listAssistantRunEventLedgerEvents(t, messageStore, scope, "run-replay"); len(events) != 2 {
		t.Fatalf("replay appended events: %#v", events)
	}
}

func TestAssistantRunEventLedgerRejectsConflictingCallID(t *testing.T) {
	ctx := context.Background()
	messageStore, scope := newAssistantRunEventLedgerTestStore(t, "run-conflict")
	ledger := newProjectAssistantRunEventLedger(messageStore, scope, "run-conflict")
	spec := projectAssistantToolSpec{Name: projectToolReadFile, Risk: projectAssistantToolRiskRead}
	if _, err := ledger.BeginToolCall(ctx, "call-1", spec, map[string]any{"path": "src/App.vue"}); err != nil {
		t.Fatalf("BeginToolCall: %v", err)
	}

	_, err := ledger.BeginToolCall(ctx, "call-1", spec, map[string]any{"path": "src/main.ts"})
	if !errors.Is(err, errProjectAssistantRunToolCallIDConflict) {
		t.Fatalf("conflicting call error = %v, want errProjectAssistantRunToolCallIDConflict", err)
	}
	if events := listAssistantRunEventLedgerEvents(t, messageStore, scope, "run-conflict"); len(events) != 1 {
		t.Fatalf("conflict appended events: %#v", events)
	}
}

func TestAssistantRunEventLedgerRetriesIncompleteReadButFailsClosedForEffect(t *testing.T) {
	ctx := context.Background()
	messageStore, scope := newAssistantRunEventLedgerTestStore(t, "run-incomplete")
	readSpec := projectAssistantToolSpec{Name: projectToolReadFile, Risk: projectAssistantToolRiskRead}
	effectSpec := projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite}

	firstRead := newProjectAssistantRunEventLedger(messageStore, scope, "run-incomplete")
	if _, err := firstRead.BeginToolCall(ctx, "call-read", readSpec, map[string]any{"path": "README.md"}); err != nil {
		t.Fatalf("begin first read: %v", err)
	}
	restartedRead := newProjectAssistantRunEventLedger(messageStore, scope, "run-incomplete")
	retry, err := restartedRead.BeginToolCall(ctx, "call-read", readSpec, map[string]any{"path": "README.md"})
	if err != nil {
		t.Fatalf("retry incomplete read: %v", err)
	}
	if !retry.ShouldDispatch() {
		t.Fatalf("incomplete read decision = %#v, want dispatch retry", retry)
	}

	firstEffect := newProjectAssistantRunEventLedger(messageStore, scope, "run-incomplete")
	if _, err := firstEffect.BeginToolCall(ctx, "call-effect", effectSpec, map[string]any{"patch": "patch"}); err != nil {
		t.Fatalf("begin effect: %v", err)
	}
	restartedEffect := newProjectAssistantRunEventLedger(messageStore, scope, "run-incomplete")
	_, err = restartedEffect.BeginToolCall(ctx, "call-effect", effectSpec, map[string]any{"patch": "patch"})
	if !errors.Is(err, errProjectAssistantRunIncompleteEffect) {
		t.Fatalf("incomplete effect error = %v, want errProjectAssistantRunIncompleteEffect", err)
	}

	events := listAssistantRunEventLedgerEvents(t, messageStore, scope, "run-incomplete")
	if len(events) != 3 {
		t.Fatalf("events = %#v, want two read attempts and one effect call", events)
	}
	var attempts []int
	for _, event := range events {
		if event.CallID != "call-read" || event.Type != projectAssistantRunToolCallEventType {
			continue
		}
		var payload projectAssistantRunToolCallPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode call payload: %v", err)
		}
		attempts = append(attempts, payload.Attempt)
	}
	if fmt.Sprint(attempts) != "[1 2]" {
		t.Fatalf("read attempts = %v, want [1 2]", attempts)
	}
}

func TestAssistantRunEventLedgerCorruptionRemainsSticky(t *testing.T) {
	ctx := context.Background()
	messageStore, scope := newAssistantRunEventLedgerTestStore(t, "run-corrupt")
	arguments, digest, err := projectAssistantRunToolCallDigest(projectToolReadFile, map[string]any{"path": "README.md"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(projectAssistantRunToolCallPayload{
		Arguments: arguments,
		Read:      true,
		Attempt:   0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messageStore.AppendAssistantRunEvent(ctx, scope, store.AssistantRunEvent{
		RunID:      "run-corrupt",
		Type:       projectAssistantRunToolCallEventType,
		CallID:     "call-corrupt",
		ToolName:   projectToolReadFile,
		ArgsDigest: digest,
		Payload:    payload,
	}, 0); err != nil {
		t.Fatal(err)
	}
	ledger := newProjectAssistantRunEventLedger(messageStore, scope, "run-corrupt")
	for attempt := 0; attempt < 2; attempt++ {
		_, err := ledger.BeginToolCall(ctx, "call-new", projectAssistantToolSpec{Name: projectToolReadFile, Risk: projectAssistantToolRiskRead}, map[string]any{"path": "src/App.tsx"})
		if !errors.Is(err, errProjectAssistantRunToolLedgerCorrupt) {
			t.Fatalf("attempt %d error = %v, want sticky ledger corruption", attempt+1, err)
		}
	}
	if events := listAssistantRunEventLedgerEvents(t, messageStore, scope, "run-corrupt"); len(events) != 1 {
		t.Fatalf("corrupt ledger appended later events: %#v", events)
	}
}

func TestAssistantRunEventLedgerFinishesAfterCallerCancellation(t *testing.T) {
	messageStore, scope := newAssistantRunEventLedgerTestStore(t, "run-cancelled-finish")
	ledger := newProjectAssistantRunEventLedger(messageStore, scope, "run-cancelled-finish")
	spec := projectAssistantToolSpec{Name: projectToolReadFile, Risk: projectAssistantToolRiskRead}

	success, err := ledger.BeginToolCall(context.Background(), "call-success", spec, map[string]any{"path": "README.md"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ledger.FinishToolCall(cancelled, success.Token, "read result", nil); err != nil {
		t.Fatalf("persist successful result after cancellation: %v", err)
	}

	failure, err := ledger.BeginToolCall(context.Background(), "call-failure", spec, map[string]any{"path": "missing.txt"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel = context.WithCancel(context.Background())
	cancel()
	backendErr := errors.New("backend read failed")
	if _, err := ledger.FinishToolCall(cancelled, failure.Token, "model-visible failure", backendErr); err != nil {
		t.Fatalf("persist failed result after cancellation: %v", err)
	}

	restarted := newProjectAssistantRunEventLedger(messageStore, scope, "run-cancelled-finish")
	replay, err := restarted.BeginToolCall(context.Background(), "call-failure", spec, map[string]any{"path": "missing.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Replay == nil || replay.Replay.Error != backendErr.Error() || replay.Replay.Result != "model-visible failure" {
		t.Fatalf("cancelled failure replay = %#v, want exact durable outcome", replay)
	}
}

func TestAssistantRunEventLedgerSerializesConcurrentCASAppends(t *testing.T) {
	ctx := context.Background()
	messageStore, scope := newAssistantRunEventLedgerTestStore(t, "run-concurrent")
	const callCount = 24

	var wg sync.WaitGroup
	errs := make(chan error, callCount)
	for index := 0; index < callCount; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Separate instances exercise durable CAS refresh as well as each
			// recorder's own mutex.
			ledger := newProjectAssistantRunEventLedger(messageStore, scope, "run-concurrent")
			decision, err := ledger.BeginToolCall(
				ctx,
				fmt.Sprintf("call-%02d", index),
				projectAssistantToolSpec{Name: projectToolReadFile, Risk: projectAssistantToolRiskRead},
				map[string]any{"path": fmt.Sprintf("src/%02d.ts", index)},
			)
			if err != nil {
				errs <- err
				return
			}
			if _, err := ledger.FinishToolCall(ctx, decision.Token, fmt.Sprintf("result-%02d", index), nil); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ledger call: %v", err)
	}

	events := listAssistantRunEventLedgerEvents(t, messageStore, scope, "run-concurrent")
	if len(events) != callCount*2 {
		t.Fatalf("event count = %d, want %d", len(events), callCount*2)
	}
	callSequence := map[string]int64{}
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("event %d sequence = %d, want %d", index, event.Sequence, index+1)
		}
		switch event.Type {
		case projectAssistantRunToolCallEventType:
			callSequence[event.CallID] = event.Sequence
		case projectAssistantRunToolResultEventType:
			if callSequence[event.CallID] == 0 || callSequence[event.CallID] >= event.Sequence {
				t.Fatalf("result event %#v did not follow its call", event)
			}
		default:
			t.Fatalf("unexpected event type %q", event.Type)
		}
	}
}

func newAssistantRunEventLedgerTestStore(t *testing.T, runID string) (*store.MemoryStore, store.Scope) {
	t.Helper()
	messageStore := store.NewMemoryStore()
	scope := store.Scope{
		OrgUUID:       "org-a",
		WorkspaceUUID: "workspace-a",
		ProjectName:   "demo",
		ProjectUID:    "project-a",
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	if err := messageStore.SaveAssistantRun(context.Background(), scope, store.AssistantRun{
		ID:        runID,
		Mode:      store.AssistantRunModeDefault,
		Status:    store.AssistantRunStatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAssistantRun: %v", err)
	}
	return messageStore, scope
}

func listAssistantRunEventLedgerEvents(
	t *testing.T,
	messageStore store.Store,
	scope store.Scope,
	runID string,
) []store.AssistantRunEvent {
	t.Helper()
	events, err := messageStore.ListAssistantRunEvents(context.Background(), scope, runID, 0, 500)
	if err != nil {
		t.Fatalf("ListAssistantRunEvents: %v", err)
	}
	return events
}
