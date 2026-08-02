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
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/faroshq/provider-app-studio/store"
)

const (
	assistantThreadMirrorPersistMaxAttempts = 8
	assistantThreadMirrorRetryBaseDelay     = 25 * time.Millisecond
)

// assistantThreadMirrorState is reconstructed from the canonical thread
// stream before subscribing to the in-process supervisor. A mirror can then
// be restarted after a process or persistence failure without emitting an
// already committed delta, item, request, plan, or terminal event.
type assistantThreadMirrorState struct {
	lastContent     string
	actionStatuses  map[string]string
	lastPlan        string
	lastRequestID   string
	lastRequestType string
	lastSequence    int64
	reconstructed   bool
	needsReload     bool
	terminalItem    bool
	terminalEvent   bool
}

func (s *Server) loadAssistantThreadMirrorState(ctx context.Context, scope store.Scope, threadID, activeMessageID, turnID string) (assistantThreadMirrorState, error) {
	events, err := s.loadAllAssistantThreadEvents(ctx, scope, threadID)
	if err != nil {
		return assistantThreadMirrorState{}, err
	}
	state := assistantThreadMirrorState{actionStatuses: map[string]string{}, reconstructed: true}
	for _, event := range events {
		if event.Sequence > state.lastSequence {
			state.lastSequence = event.Sequence
		}
		if event.TurnID != "" && event.TurnID != turnID {
			continue
		}
		if event.Type == assistantThreadEventTurnCompleted || event.Type == assistantThreadEventTurnFailed || event.Type == assistantThreadEventTurnInterrupted {
			state.terminalEvent = true
		}
		if event.ItemID == activeMessageID {
			var envelope struct {
				Item  assistantThreadItem `json:"item"`
				Delta string              `json:"delta"`
			}
			if json.Unmarshal(event.Payload, &envelope) == nil {
				switch event.Type {
				case assistantThreadEventItemDelta:
					state.lastContent += envelope.Delta
				case assistantThreadEventItemCompleted:
					if envelope.Item.ID != "" {
						state.lastContent = envelope.Item.Content
						state.terminalItem = true
					}
				}
			}
		}
		if event.Type == assistantThreadEventItemCompleted && event.ItemID == "plan-"+turnID {
			var envelope struct {
				Item assistantThreadItem `json:"item"`
			}
			if json.Unmarshal(event.Payload, &envelope) == nil {
				state.lastPlan = string(envelope.Item.Data)
			}
		}
		if event.ItemID != "" && (event.Type == assistantThreadEventItemStarted || event.Type == assistantThreadEventItemCompleted) {
			var envelope struct {
				Item assistantThreadItem `json:"item"`
			}
			if json.Unmarshal(event.Payload, &envelope) == nil && envelope.Item.Type == assistantThreadEventDynamicToolCall {
				var action projectAssistantActionFeedItem
				if json.Unmarshal(envelope.Item.Data, &action) == nil && action.ID != "" {
					state.actionStatuses[action.ID] = action.Status
				}
			}
		}
		switch event.Type {
		case assistantThreadEventApprovalRequested, assistantThreadEventUserInputRequested:
			state.lastRequestID = event.RequestID
			state.lastRequestType = event.Type
		case assistantThreadEventApprovalResolved, assistantThreadEventUserInputResolved:
			if event.RequestID == state.lastRequestID {
				state.lastRequestID, state.lastRequestType = "", ""
			}
		}
	}
	return state, nil
}

func (s *Server) mirrorAssistantRunIntoThread(scope store.Scope, threadID string, turn store.AssistantTurn, run store.AssistantRun) {
	ctx := context.Background()
	state, err := s.loadAssistantThreadMirrorStateWithRetry(ctx, scope, threadID, run.ActiveMessageID, turn.ID)
	if err != nil {
		s.reportAssistantThreadMirrorFailure(scope, turn, err)
		return
	}
	if state.terminalEvent {
		return
	}
	updates, unsubscribe, err := s.projectAssistantSupervisor().Subscribe(scope, run.ID, 0)
	if err != nil {
		s.reportAssistantThreadMirrorFailure(scope, turn, err)
		return
	}
	defer unsubscribe()
	for snapshot := range updates {
		if err := s.projectAssistantThreadSnapshotWithRetry(ctx, scope, threadID, turn, run, &state, snapshot); err != nil {
			s.reportAssistantThreadMirrorFailure(scope, turn, err)
			return
		}
		if state.terminalEvent {
			return
		}
	}
}

func (s *Server) loadAssistantThreadMirrorStateWithRetry(ctx context.Context, scope store.Scope, threadID, activeMessageID, turnID string) (assistantThreadMirrorState, error) {
	var lastErr error
	for attempt := 0; attempt < assistantThreadMirrorPersistMaxAttempts; attempt++ {
		state, err := s.loadAssistantThreadMirrorState(ctx, scope, threadID, activeMessageID, turnID)
		if err == nil {
			return state, nil
		}
		lastErr = err
		if attempt+1 == assistantThreadMirrorPersistMaxAttempts {
			break
		}
		if err := waitForAssistantThreadMirrorRetry(ctx, attempt); err != nil {
			return assistantThreadMirrorState{}, err
		}
	}
	return assistantThreadMirrorState{}, fmt.Errorf("load assistant thread mirror state after %d attempts: %w", assistantThreadMirrorPersistMaxAttempts, lastErr)
}

// projectAssistantThreadSnapshotWithRetry is the mirror's persistence barrier.
// After every failed attempt, reload state from the durable stream before
// retrying. This resolves ambiguous commit errors: if the write committed but
// its acknowledgement was lost, the retry observes it instead of appending a
// duplicate semantic event.
// The bound prevents a permanently failing store from leaking a goroutine.
func (s *Server) projectAssistantThreadSnapshotWithRetry(ctx context.Context, scope store.Scope, threadID string, turn store.AssistantTurn, run store.AssistantRun, state *assistantThreadMirrorState, snapshot projectAssistantRunSnapshot) error {
	if state == nil {
		return errors.New("assistant thread mirror state is required")
	}
	release := s.acquireAssistantThreadProjectionLock(scope, threadID, turn.ID)
	defer release()

	var lastErr error
	for attempt := 0; attempt < assistantThreadMirrorPersistMaxAttempts; attempt++ {
		if !state.reconstructed || state.needsReload {
			durableState, err := s.loadAssistantThreadMirrorState(ctx, scope, threadID, run.ActiveMessageID, turn.ID)
			if err != nil {
				lastErr = errors.Join(lastErr, fmt.Errorf("reload assistant thread projection state: %w", err))
				if attempt+1 == assistantThreadMirrorPersistMaxAttempts {
					break
				}
				if err := waitForAssistantThreadMirrorRetry(ctx, attempt); err != nil {
					return err
				}
				continue
			}
			*state = durableState
		}
		if err := s.projectAssistantThreadSnapshot(ctx, scope, threadID, turn, run, state, snapshot); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt+1 == assistantThreadMirrorPersistMaxAttempts {
			break
		}
		if err := waitForAssistantThreadMirrorRetry(ctx, attempt); err != nil {
			return err
		}
	}
	return fmt.Errorf("assistant thread projection failed after %d attempts: %w", assistantThreadMirrorPersistMaxAttempts, lastErr)
}

func waitForAssistantThreadMirrorRetry(ctx context.Context, attempt int) error {
	delay := assistantThreadMirrorRetryBaseDelay << attempt
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

func (s *Server) reportAssistantThreadMirrorFailure(scope store.Scope, turn store.AssistantTurn, err error) {
	if err == nil {
		return
	}
	klog.Background().Error(err, "assistant thread mirror stopped before durable projection", "org", scope.OrgUUID, "workspace", scope.WorkspaceUUID, "project", scope.ProjectName, "thread", turn.ThreadID, "turn", turn.ID)
}

func (s *Server) projectAssistantThreadSnapshot(ctx context.Context, scope store.Scope, threadID string, turn store.AssistantTurn, run store.AssistantRun, state *assistantThreadMirrorState, snapshot projectAssistantRunSnapshot) error {
	if state == nil {
		return errors.New("assistant thread mirror state is required")
	}
	if !state.reconstructed || state.needsReload {
		durableState, err := s.loadAssistantThreadMirrorState(ctx, scope, threadID, run.ActiveMessageID, turn.ID)
		if err != nil {
			return fmt.Errorf("reconstruct assistant thread mirror state: %w", err)
		}
		*state = durableState
	}
	if state.terminalEvent {
		return nil
	}
	content := snapshot.Message.Content
	if strings.HasPrefix(content, state.lastContent) && len(content) > len(state.lastContent) {
		delta := content[len(state.lastContent):]
		payload, err := json.Marshal(map[string]any{"delta": delta})
		if err != nil {
			return fmt.Errorf("encode assistant thread message delta: %w", err)
		}
		if _, err := s.appendAssistantThreadMirrorEvent(ctx, scope, state, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: assistantThreadEventItemDelta, ItemID: run.ActiveMessageID, Payload: payload}); err != nil {
			return fmt.Errorf("persist assistant thread message delta: %w", err)
		}
		state.lastContent = content
	} else if content == state.lastContent {
		// The snapshot did not add durable content. Keep the state unchanged.
	} else if state.lastContent == "" && content == "" {
		state.lastContent = content
	}

	for _, action := range projectAssistantActionFeedFromMetadata(snapshot.Message.Metadata[projectMessageMetadataAssistantActionFeed]) {
		if state.actionStatuses[action.ID] == action.Status {
			continue
		}
		status := "in_progress"
		eventType := assistantThreadEventItemStarted
		switch action.Status {
		case projectAssistantActionFeedStatusSucceeded, projectAssistantActionFeedStatusSkipped:
			status, eventType = "completed", assistantThreadEventItemCompleted
		case projectAssistantActionFeedStatusFailed, projectAssistantActionFeedStatusRejected:
			status, eventType = "failed", assistantThreadEventItemCompleted
		}
		data, err := json.Marshal(action)
		if err != nil {
			return fmt.Errorf("encode assistant thread action: %w", err)
		}
		item := assistantThreadItem{ID: action.ID, TurnID: turn.ID, Type: assistantThreadEventDynamicToolCall, Status: status, Content: action.Title, Data: data, CreatedAt: time.Now().UTC()}
		payload, err := json.Marshal(map[string]any{"item": item})
		if err != nil {
			return fmt.Errorf("encode assistant thread action item: %w", err)
		}
		if _, err := s.appendAssistantThreadMirrorEvent(ctx, scope, state, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: eventType, ItemID: action.ID, Payload: payload}); err != nil {
			return fmt.Errorf("persist assistant thread action %q: %w", action.ID, err)
		}
		state.actionStatuses[action.ID] = action.Status
	}

	if planValue, exists := snapshot.Message.Metadata[projectAssistantMetadataPlan]; exists {
		planData, err := json.Marshal(planValue)
		if err != nil {
			return fmt.Errorf("encode assistant thread plan: %w", err)
		}
		if string(planData) != state.lastPlan {
			item := assistantThreadItem{ID: "plan-" + turn.ID, TurnID: turn.ID, Type: assistantThreadEventPlan, Status: "in_progress", Data: planData, CreatedAt: time.Now().UTC()}
			payload, err := json.Marshal(map[string]any{"item": item})
			if err != nil {
				return fmt.Errorf("encode assistant thread plan item: %w", err)
			}
			if _, err := s.appendAssistantThreadMirrorEvent(ctx, scope, state, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: assistantThreadEventItemCompleted, ItemID: item.ID, Payload: payload}); err != nil {
				return fmt.Errorf("persist assistant thread plan: %w", err)
			}
			state.lastPlan = string(planData)
		}
	}

	requestStillPending := snapshot.Run.RequestID == state.lastRequestID &&
		((state.lastRequestType == assistantThreadEventApprovalRequested && snapshot.Run.Status == store.AssistantRunStatusPendingPermission) ||
			(state.lastRequestType == assistantThreadEventUserInputRequested && snapshot.Run.Status == store.AssistantRunStatusPendingInput))
	if state.lastRequestID != "" && !requestStillPending {
		resolution, err := assistantThreadPendingRequestResolution(turn.ID, *state)
		if err != nil {
			return fmt.Errorf("encode assistant thread request resolution: %w", err)
		}
		resolution.ThreadID = threadID
		if _, err := s.appendAssistantThreadMirrorEvent(ctx, scope, state, resolution); err != nil {
			return fmt.Errorf("persist assistant thread request resolution: %w", err)
		}
		state.lastRequestID, state.lastRequestType = "", ""
	}

	if snapshot.Run.RequestID != "" && snapshot.Run.RequestID != state.lastRequestID {
		eventType := ""
		switch snapshot.Run.Status {
		case store.AssistantRunStatusPendingPermission:
			eventType = assistantThreadEventApprovalRequested
		case store.AssistantRunStatusPendingInput:
			eventType = assistantThreadEventUserInputRequested
		}
		if eventType != "" {
			interrupt := projectAssistantUIInterruptFromMetadata(snapshot.Message.Metadata[projectMessageMetadataAssistantInterrupt])
			if !assistantThreadInterruptMatchesPendingRequest(interrupt, eventType, snapshot.Run.ID, snapshot.Run.RequestID) {
				return nil
			}
			interruptData, err := json.Marshal(interrupt)
			if err != nil {
				return fmt.Errorf("encode assistant thread pending request: %w", err)
			}
			itemType := "approval"
			if eventType == assistantThreadEventUserInputRequested {
				itemType = "input"
			}
			item := assistantThreadItem{ID: snapshot.Run.RequestID, TurnID: turn.ID, Type: itemType, Status: "in_progress", Data: interruptData, CreatedAt: time.Now().UTC()}
			payload, err := json.Marshal(map[string]any{"requestID": snapshot.Run.RequestID, "interrupt": interrupt, "item": item})
			if err != nil {
				return fmt.Errorf("encode assistant thread pending request item: %w", err)
			}
			if _, err := s.appendAssistantThreadMirrorEvent(ctx, scope, state, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: eventType, ItemID: snapshot.Run.RequestID, RequestID: snapshot.Run.RequestID, Payload: payload}); err != nil {
				return fmt.Errorf("persist assistant thread pending request: %w", err)
			}
			state.lastRequestID = snapshot.Run.RequestID
			state.lastRequestType = eventType
		}
	}

	if !assistantRunTerminal(snapshot.Run.Status) {
		return nil
	}
	if !state.terminalItem {
		item := assistantThreadItemWithMessagePresentation(assistantThreadItem{ID: run.ActiveMessageID, TurnID: turn.ID, Type: assistantThreadEventAssistantMessage, Status: "completed", Content: content, CreatedAt: snapshot.Message.CreatedAt}, snapshot.Message.Metadata)
		payload, err := json.Marshal(map[string]any{"item": item})
		if err != nil {
			return fmt.Errorf("encode assistant thread terminal item: %w", err)
		}
		if _, err := s.appendAssistantThreadMirrorEvent(ctx, scope, state, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: assistantThreadEventItemCompleted, ItemID: item.ID, Payload: payload}); err != nil {
			return fmt.Errorf("persist assistant thread terminal item: %w", err)
		}
		state.terminalItem = true
	}
	if state.terminalEvent {
		return nil
	}
	turn.UpdatedAt = time.Now().UTC()
	terminalType := assistantThreadEventTurnCompleted
	switch snapshot.Run.Status {
	case store.AssistantRunStatusCompleted:
		turn.Status = store.AssistantTurnStatusCompleted
	case store.AssistantRunStatusInterrupted, store.AssistantRunStatusAborted:
		turn.Status = store.AssistantTurnStatusInterrupted
		terminalType = assistantThreadEventTurnInterrupted
	default:
		turn.Status = store.AssistantTurnStatusFailed
		turn.Error = snapshot.Run.Error
		terminalType = assistantThreadEventTurnFailed
	}
	turnPayload, err := json.Marshal(map[string]any{"turn": turn})
	if err != nil {
		return fmt.Errorf("encode assistant thread terminal turn: %w", err)
	}
	if err := s.saveAssistantThreadTurnWithEvent(ctx, scope, state, turn, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: terminalType, Payload: turnPayload}); err != nil {
		return fmt.Errorf("persist assistant thread terminal turn: %w", err)
	}
	state.terminalEvent = true
	return nil
}

func assistantThreadPendingRequestResolution(turnID string, state assistantThreadMirrorState) (store.AssistantThreadEvent, error) {
	resolvedType := assistantThreadEventApprovalResolved
	itemType := "approval"
	if state.lastRequestType == assistantThreadEventUserInputRequested {
		resolvedType = assistantThreadEventUserInputResolved
		itemType = "input"
	}
	item := assistantThreadItem{ID: state.lastRequestID, TurnID: turnID, Type: itemType, Status: "completed", CreatedAt: time.Now().UTC()}
	payload, err := json.Marshal(map[string]any{"requestID": state.lastRequestID, "item": item})
	if err != nil {
		return store.AssistantThreadEvent{}, err
	}
	return store.AssistantThreadEvent{
		TurnID:    turnID,
		Type:      resolvedType,
		ItemID:    state.lastRequestID,
		RequestID: state.lastRequestID,
		Payload:   payload,
	}, nil
}

// appendAssistantThreadMirrorEvent uses the sequence reconstructed at the
// start of the mirror turn. A successful append advances it locally; failures
// mark the state for a durable reload before retrying, covering both ordinary
// errors and acknowledgements lost after a commit.
func (s *Server) appendAssistantThreadMirrorEvent(ctx context.Context, scope store.Scope, state *assistantThreadMirrorState, event store.AssistantThreadEvent) (store.AssistantThreadEvent, error) {
	if state == nil {
		return store.AssistantThreadEvent{}, errors.New("assistant thread mirror state is required")
	}
	created, err := s.store.AppendAssistantThreadEvent(ctx, scope, event, state.lastSequence)
	if err != nil {
		state.needsReload = true
		return store.AssistantThreadEvent{}, err
	}
	state.lastSequence = created.Sequence
	return created, nil
}

func (s *Server) saveAssistantThreadTurnWithEvent(ctx context.Context, scope store.Scope, state *assistantThreadMirrorState, turn store.AssistantTurn, event store.AssistantThreadEvent) error {
	if state == nil {
		return errors.New("assistant thread mirror state is required")
	}
	if err := s.store.SaveAssistantTurnWithEvent(ctx, scope, turn, event, state.lastSequence); err != nil {
		state.needsReload = true
		return err
	}
	state.lastSequence++
	return nil
}
