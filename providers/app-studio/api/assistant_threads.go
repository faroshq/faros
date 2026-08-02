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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/store"
)

const (
	assistantThreadEventThreadCreated         = "thread.created"
	assistantThreadEventThreadUpdated         = "thread.updated"
	assistantThreadEventTurnStarted           = "turn.started"
	assistantThreadEventTurnCompleted         = "turn.completed"
	assistantThreadEventTurnFailed            = "turn.failed"
	assistantThreadEventTurnInterrupted       = "turn.interrupted"
	assistantThreadEventItemStarted           = "item.started"
	assistantThreadEventItemDelta             = "item.delta"
	assistantThreadEventItemCompleted         = "item.completed"
	assistantThreadEventApprovalRequested     = "approval.requested"
	assistantThreadEventApprovalResolved      = "approval.resolved"
	assistantThreadEventUserInputRequested    = "input.requested"
	assistantThreadEventUserInputResolved     = "input.resolved"
	assistantThreadEventAssistantMessage      = "agentMessage"
	assistantThreadEventUserMessage           = "userMessage"
	assistantThreadEventAssistantMessageDelta = "agentMessageDelta"
	assistantThreadEventDynamicToolCall       = "dynamicToolCall"
	assistantThreadEventPlan                  = "plan"
)

type assistantThreadCreateRequest struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
}

type assistantThreadPatchRequest struct {
	Title    *string `json:"title,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}

type assistantThreadTurnCreateRequest struct {
	Content             string                 `json:"content"`
	ClientUserMessageID string                 `json:"clientUserMessageID"`
	CollaborationMode   store.AssistantRunMode `json:"collaborationMode,omitempty"`
}

type assistantThreadTurnStartResponse struct {
	Thread store.AssistantThread `json:"thread"`
	Turn   store.AssistantTurn   `json:"turn"`
}

type assistantThreadSteerRequest struct {
	Content             string `json:"content"`
	ClientUserMessageID string `json:"clientUserMessageID"`
}

type assistantThreadInterruptRequest struct {
	ClientRequestID string `json:"clientRequestID"`
}

type assistantThreadItem struct {
	ID        string          `json:"id"`
	TurnID    string          `json:"turnID,omitempty"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	Content   string          `json:"content,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Sequence  int64           `json:"sequence"`
	CreatedAt time.Time       `json:"createdAt"`
}

func (s *Server) createProjectAssistantThread(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	var request assistantThreadCreateRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	threadID := strings.TrimSpace(request.ID)
	if threadID == "" {
		threadID = "thread-" + uuid.NewString()
	}
	now := time.Now().UTC()
	thread := store.AssistantThread{ID: threadID, Title: request.Title, Status: store.AssistantThreadStatusIdle, ActorID: id.user, CreatedAt: now, UpdatedAt: now}
	payload, _ := json.Marshal(map[string]any{"thread": thread})
	created, err := s.store.CreateAssistantThread(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), thread, []store.AssistantThreadEvent{{Type: assistantThreadEventThreadCreated, Payload: payload, CreatedAt: now}})
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listProjectAssistantThreads(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	includeArchived, _ := strconv.ParseBool(r.URL.Query().Get("includeArchived"))
	page, err := s.store.ListAssistantThreads(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), id.user, includeArchived, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) patchProjectAssistantThread(w http.ResponseWriter, r *http.Request) {
	_, id, project, thread, ok := s.requireOwnedAssistantThread(w, r)
	if !ok {
		return
	}
	var request assistantThreadPatchRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	if request.Title != nil {
		thread.Title = strings.TrimSpace(*request.Title)
	}
	if request.Archived != nil {
		if *request.Archived {
			thread.Status = store.AssistantThreadStatusArchived
		} else {
			thread.Status = store.AssistantThreadStatusIdle
		}
	}
	thread.UpdatedAt = time.Now().UTC()
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	updated, err := s.store.UpdateAssistantThread(r.Context(), scope, thread)
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	payload, _ := json.Marshal(map[string]any{"thread": updated})
	_, _ = s.appendAssistantThreadEvent(r.Context(), scope, store.AssistantThreadEvent{ThreadID: thread.ID, Type: assistantThreadEventThreadUpdated, Payload: payload})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) listProjectAssistantThreadItems(w http.ResponseWriter, r *http.Request) {
	_, id, project, thread, ok := s.requireOwnedAssistantThread(w, r)
	if !ok {
		return
	}
	events, err := s.loadAllAssistantThreadEvents(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), thread.ID)
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": materializeAssistantThreadItems(events)})
}

func (s *Server) startProjectAssistantThreadTurn(w http.ResponseWriter, r *http.Request) {
	c, id, project, thread, ok := s.requireOwnedAssistantThread(w, r)
	if !ok {
		return
	}
	if thread.Status == store.AssistantThreadStatusArchived {
		writeStatus(w, http.StatusConflict, "Conflict", "assistant thread is archived")
		return
	}
	var request assistantThreadTurnCreateRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	request.Content = strings.TrimSpace(request.Content)
	request.ClientUserMessageID = strings.TrimSpace(request.ClientUserMessageID)
	if request.Content == "" || request.ClientUserMessageID == "" {
		writeProjectError(w, newValidationError("content and clientUserMessageID are required"))
		return
	}
	if request.CollaborationMode == "" {
		request.CollaborationMode = store.AssistantRunModeDefault
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	var canonicalTurn store.AssistantTurn
	started, err := s.startProjectAssistantRunDurablyWithMode(r.Context(), scope, id.user, request.Content, request.ClientUserMessageID, request.CollaborationMode,
		func(created store.AssistantRun, assistant store.Message, _ bool) error {
			now := time.Now().UTC()
			canonicalTurn = store.AssistantTurn{ID: created.ID, ThreadID: thread.ID, ActorID: id.user, ClientUserMessageID: request.ClientUserMessageID,
				Mode: created.Mode, ApprovalMode: created.ApprovalMode, Status: store.AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
			turnPayload, _ := json.Marshal(map[string]any{"turn": canonicalTurn})
			userItem := assistantThreadItem{ID: created.UserMessageID, TurnID: created.ID, Type: assistantThreadEventUserMessage, Status: "completed", Content: request.Content, CreatedAt: now}
			userPayload, _ := json.Marshal(map[string]any{"item": userItem})
			assistantItem := assistantThreadItem{ID: created.ActiveMessageID, TurnID: created.ID, Type: assistantThreadEventAssistantMessage, Status: "in_progress", CreatedAt: now}
			assistantPayload, _ := json.Marshal(map[string]any{"item": assistantItem})
			createdTurn, createErr := s.store.CreateAssistantTurn(r.Context(), scope, canonicalTurn, []store.AssistantThreadEvent{
				{Type: assistantThreadEventTurnStarted, Payload: turnPayload, CreatedAt: now},
				{Type: assistantThreadEventItemCompleted, ItemID: userItem.ID, Payload: userPayload, CreatedAt: now},
				{Type: assistantThreadEventItemStarted, ItemID: assistantItem.ID, Payload: assistantPayload, CreatedAt: now},
			})
			if createErr != nil {
				return createErr
			}
			canonicalTurn = createdTurn
			if err := s.projectAssistantSupervisor().Start(r.Context(), scope, created, assistant, func(ctx context.Context, accumulator *projectAssistantSnapshotAccumulator) {
				s.runProjectAssistantWorker(ctx, accumulator, r, id, c, project, created, nil)
			}); err != nil {
				return err
			}
			go s.mirrorAssistantRunIntoThread(scope, thread.ID, canonicalTurn, created)
			return nil
		})
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	if !started.Started {
		canonicalTurn, err = s.store.FindAssistantTurnByClientUserMessageID(r.Context(), scope, thread.ID, request.ClientUserMessageID)
		if err != nil {
			s.writeAssistantThreadError(w, err)
			return
		}
	}
	thread, _ = s.store.GetAssistantThread(r.Context(), scope, thread.ID)
	writeJSON(w, http.StatusAccepted, assistantThreadTurnStartResponse{Thread: thread, Turn: canonicalTurn})
}

func (s *Server) activeProjectAssistantThreadTurn(w http.ResponseWriter, r *http.Request) {
	_, id, project, thread, ok := s.requireOwnedAssistantThread(w, r)
	if !ok {
		return
	}
	turn, err := s.store.ActiveAssistantTurn(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), thread.ID)
	if errors.Is(err, store.ErrAssistantTurnNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	if err := s.reconcileProjectAssistantThreadTurn(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), turn); err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	turn, err = s.store.GetAssistantTurn(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), thread.ID, turn.ID)
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	if turn.Status != store.AssistantTurnStatusInProgress {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, turn)
}

func (s *Server) steerProjectAssistantThreadTurn(w http.ResponseWriter, r *http.Request) {
	_, id, project, thread, ok := s.requireOwnedAssistantThread(w, r)
	if !ok {
		return
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	turn, err := s.store.GetAssistantTurn(r.Context(), scope, thread.ID, mux.Vars(r)["turn"])
	if err != nil || turn.ActorID != id.user || turn.Status != store.AssistantTurnStatusInProgress {
		writeStatus(w, http.StatusNotFound, "NotFound", "active assistant turn not found")
		return
	}
	var request assistantThreadSteerRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	request.Content = strings.TrimSpace(request.Content)
	request.ClientUserMessageID = strings.TrimSpace(request.ClientUserMessageID)
	if request.Content == "" || request.ClientUserMessageID == "" {
		writeProjectError(w, newValidationError("content and clientUserMessageID are required"))
		return
	}
	_, user, _, handled, err := s.projectAssistantSupervisor().EnqueueSteering(r.Context(), scope, turn.ID, id.user, request.Content, request.ClientUserMessageID, turn.Mode)
	if err != nil || !handled {
		if err == nil {
			err = store.ErrAssistantTurnConflict
		}
		s.writeAssistantThreadError(w, err)
		return
	}
	item := assistantThreadItem{ID: user.ID, TurnID: turn.ID, Type: assistantThreadEventUserMessage, Status: "completed", Content: user.Content, CreatedAt: user.CreatedAt}
	payload, _ := json.Marshal(map[string]any{"item": item})
	_, err = s.appendAssistantThreadEvent(r.Context(), scope, store.AssistantThreadEvent{ThreadID: thread.ID, TurnID: turn.ID, Type: assistantThreadEventItemCompleted, ItemID: item.ID, Payload: payload})
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, turn)
}

func (s *Server) interruptProjectAssistantThreadTurn(w http.ResponseWriter, r *http.Request) {
	_, id, project, thread, ok := s.requireOwnedAssistantThread(w, r)
	if !ok {
		return
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	turn, err := s.store.GetAssistantTurn(r.Context(), scope, thread.ID, mux.Vars(r)["turn"])
	if err != nil || turn.ActorID != id.user {
		writeStatus(w, http.StatusNotFound, "NotFound", "assistant turn not found")
		return
	}
	var request assistantThreadInterruptRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	request.ClientRequestID = strings.TrimSpace(request.ClientRequestID)
	if request.ClientRequestID == "" {
		writeProjectError(w, newValidationError("clientRequestID is required"))
		return
	}
	run, runErr := s.store.GetAssistantRun(r.Context(), scope, turn.ID)
	if runErr != nil || s.authorizeProjectAssistantRunActor(r.Context(), scope, run, id.user, false) != nil {
		writeStatus(w, http.StatusNotFound, "NotFound", "assistant turn not found")
		return
	}
	if found, bindErr := s.projectAssistantSupervisor().BindStopRequest(r.Context(), scope, turn.ID, id.user, request.ClientRequestID); found {
		if bindErr != nil {
			s.writeAssistantThreadError(w, bindErr)
			return
		}
	} else {
		if bindErr := bindProjectAssistantStopRequest(&run, id.user, request.ClientRequestID); bindErr != nil {
			s.writeAssistantThreadError(w, bindErr)
			return
		}
		run.UpdatedAt = time.Now().UTC()
		if saveErr := s.store.SaveAssistantRun(r.Context(), scope, run); saveErr != nil {
			s.writeAssistantThreadError(w, saveErr)
			return
		}
	}
	stopped, found, err := s.projectAssistantSupervisor().Stop(scope, turn.ID)
	if err != nil {
		s.writeAssistantThreadError(w, err)
		return
	}
	if !found && !assistantRunTerminal(run.Status) {
		writeStatus(w, http.StatusConflict, "Conflict", "assistant turn is not active on this provider")
		return
	}
	if found {
		run = stopped
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"turnID": turn.ID, "status": run.Status})
}

// Approval and structured-input decisions use the same durable Eino checkpoint
// implementation during the cutover. Their public identity is the Turn ID.
func (s *Server) respondProjectAssistantThreadTurn(w http.ResponseWriter, r *http.Request) {
	_, id, project, thread, ok := s.requireOwnedAssistantThread(w, r)
	if !ok {
		return
	}
	turnID := mux.Vars(r)["turn"]
	turn, err := s.store.GetAssistantTurn(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), thread.ID, turnID)
	if err != nil || turn.ActorID != id.user || turn.Status != store.AssistantTurnStatusInProgress {
		writeStatus(w, http.StatusNotFound, "NotFound", "active assistant turn not found")
		return
	}
	vars := mux.Vars(r)
	vars["run"] = turnID
	s.resumeProjectAssistant(w, mux.SetURLVars(r, vars))
}

func (s *Server) streamProjectAssistantThreadEvents(w http.ResponseWriter, r *http.Request) {
	_, id, project, thread, ok := s.requireOwnedAssistantThread(w, r)
	if !ok {
		return
	}
	after := assistantThreadAfterSequence(r)
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	if active, err := s.store.ActiveAssistantTurn(r.Context(), scope, thread.ID); err == nil {
		if err := s.reconcileProjectAssistantThreadTurn(r.Context(), scope, active); err != nil {
			s.writeAssistantThreadError(w, err)
			return
		}
	} else if !errors.Is(err, store.ErrAssistantTurnNotFound) {
		s.writeAssistantThreadError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeStatus(w, http.StatusInternalServerError, "InternalError", "streaming is not supported")
		return
	}
	poll := time.NewTicker(250 * time.Millisecond)
	keepalive := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer keepalive.Stop()
	for {
		events, err := s.store.ListAssistantThreadEvents(r.Context(), scope, thread.ID, after, 500)
		if err != nil {
			return
		}
		for _, event := range events {
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data); err != nil {
				return
			}
			after = event.Sequence
			if event.Type == assistantThreadEventTurnCompleted || event.Type == assistantThreadEventTurnFailed || event.Type == assistantThreadEventTurnInterrupted {
				flusher.Flush()
				return
			}
		}
		if len(events) > 0 {
			flusher.Flush()
			continue
		}
		select {
		case <-poll.C:
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// reconcileProjectAssistantThreadTurn closes the canonical projection when a
// provider restart orphaned the internal Eino run before its mirror goroutine
// could publish the terminal item and event.
func (s *Server) reconcileProjectAssistantThreadTurn(ctx context.Context, scope store.Scope, turn store.AssistantTurn) error {
	if err := s.reconcileOrphanedProjectAssistantRun(ctx, scope); err != nil {
		return err
	}
	run, err := s.store.GetAssistantRun(ctx, scope, turn.ID)
	if err != nil || !assistantRunTerminal(run.Status) {
		return err
	}
	current, err := s.store.GetAssistantTurn(ctx, scope, turn.ThreadID, turn.ID)
	if err != nil || current.Status != store.AssistantTurnStatusInProgress {
		return err
	}
	message, err := s.findProjectMessage(ctx, scope, run.ActiveMessageID)
	if err != nil {
		return err
	}
	item := assistantThreadItem{ID: run.ActiveMessageID, TurnID: turn.ID, Type: assistantThreadEventAssistantMessage, Status: "completed", Content: message.Content, CreatedAt: message.CreatedAt}
	payload, _ := json.Marshal(map[string]any{"item": item})
	if _, err := s.appendAssistantThreadEvent(ctx, scope, store.AssistantThreadEvent{ThreadID: turn.ThreadID, TurnID: turn.ID, Type: assistantThreadEventItemCompleted, ItemID: item.ID, Payload: payload}); err != nil {
		return err
	}
	current.UpdatedAt = time.Now().UTC()
	terminalType := assistantThreadEventTurnCompleted
	switch run.Status {
	case store.AssistantRunStatusCompleted:
		current.Status = store.AssistantTurnStatusCompleted
	case store.AssistantRunStatusInterrupted, store.AssistantRunStatusAborted:
		current.Status = store.AssistantTurnStatusInterrupted
		terminalType = assistantThreadEventTurnInterrupted
	default:
		current.Status = store.AssistantTurnStatusFailed
		current.Error = run.Error
		terminalType = assistantThreadEventTurnFailed
	}
	turnPayload, _ := json.Marshal(map[string]any{"turn": current})
	return s.saveAssistantTurnWithEvent(ctx, scope, current, store.AssistantThreadEvent{ThreadID: turn.ThreadID, TurnID: turn.ID, Type: terminalType, Payload: turnPayload})
}

func (s *Server) requireOwnedAssistantThread(w http.ResponseWriter, r *http.Request) (*asclient.Client, identity, *aiv1alpha1.Project, store.AssistantThread, bool) {
	c, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return nil, identity{}, nil, store.AssistantThread{}, false
	}
	thread, err := s.store.GetAssistantThread(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), mux.Vars(r)["thread"])
	if err != nil || thread.ActorID != id.user {
		writeStatus(w, http.StatusNotFound, "NotFound", "assistant thread not found")
		return nil, identity{}, nil, store.AssistantThread{}, false
	}
	return c, id, project, thread, true
}

func (s *Server) writeAssistantThreadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrAssistantThreadNotFound), errors.Is(err, store.ErrAssistantTurnNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", err.Error())
	case errors.Is(err, store.ErrAssistantThreadConflict), errors.Is(err, store.ErrAssistantTurnConflict), errors.Is(err, store.ErrAssistantRunConflict):
		writeStatus(w, http.StatusConflict, "Conflict", err.Error())
	default:
		writeProjectError(w, err)
	}
}

func assistantThreadAfterSequence(r *http.Request) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get("afterSequence"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	after, _ := strconv.ParseInt(raw, 10, 64)
	if after < 0 {
		return 0
	}
	return after
}

func (s *Server) appendAssistantThreadEvent(ctx context.Context, scope store.Scope, event store.AssistantThreadEvent) (store.AssistantThreadEvent, error) {
	for attempts := 0; attempts < 8; attempts++ {
		events, err := s.loadAllAssistantThreadEvents(ctx, scope, event.ThreadID)
		if err != nil {
			return store.AssistantThreadEvent{}, err
		}
		expected := int64(0)
		if len(events) > 0 {
			expected = events[len(events)-1].Sequence
		}
		created, err := s.store.AppendAssistantThreadEvent(ctx, scope, event, expected)
		if !errors.Is(err, store.ErrAssistantThreadEventConflict) {
			return created, err
		}
	}
	return store.AssistantThreadEvent{}, store.ErrAssistantThreadEventConflict
}

func (s *Server) saveAssistantTurnWithEvent(ctx context.Context, scope store.Scope, turn store.AssistantTurn, event store.AssistantThreadEvent) error {
	for attempts := 0; attempts < 8; attempts++ {
		events, err := s.loadAllAssistantThreadEvents(ctx, scope, turn.ThreadID)
		if err != nil {
			return err
		}
		expected := int64(0)
		if len(events) > 0 {
			expected = events[len(events)-1].Sequence
		}
		err = s.store.SaveAssistantTurnWithEvent(ctx, scope, turn, event, expected)
		if !errors.Is(err, store.ErrAssistantThreadEventConflict) {
			return err
		}
	}
	return store.ErrAssistantThreadEventConflict
}

func (s *Server) loadAllAssistantThreadEvents(ctx context.Context, scope store.Scope, threadID string) ([]store.AssistantThreadEvent, error) {
	all := make([]store.AssistantThreadEvent, 0)
	after := int64(0)
	for {
		page, err := s.store.ListAssistantThreadEvents(ctx, scope, threadID, after, 500)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 500 {
			return all, nil
		}
		after = page[len(page)-1].Sequence
	}
}

func materializeAssistantThreadItems(events []store.AssistantThreadEvent) []assistantThreadItem {
	items := make([]assistantThreadItem, 0)
	indexes := map[string]int{}
	for _, event := range events {
		if event.ItemID == "" {
			continue
		}
		index, exists := indexes[event.ItemID]
		if !exists {
			index = len(items)
			indexes[event.ItemID] = index
			items = append(items, assistantThreadItem{ID: event.ItemID, TurnID: event.TurnID, Status: "in_progress", Sequence: event.Sequence, CreatedAt: event.CreatedAt})
		}
		var envelope struct {
			Item  assistantThreadItem `json:"item"`
			Delta string              `json:"delta"`
		}
		_ = json.Unmarshal(event.Payload, &envelope)
		if envelope.Item.ID != "" {
			// Item creation time is stable across subsequent delta/completion
			// events. Event creation time remains available on the event itself.
			if !items[index].CreatedAt.IsZero() {
				envelope.Item.CreatedAt = items[index].CreatedAt
			} else if envelope.Item.CreatedAt.IsZero() {
				envelope.Item.CreatedAt = event.CreatedAt
			}
			envelope.Item.Sequence = event.Sequence
			items[index] = envelope.Item
		}
		if event.Type == assistantThreadEventItemDelta {
			items[index].Content += envelope.Delta
			items[index].Sequence = event.Sequence
		}
	}
	return items
}

func (s *Server) mirrorAssistantRunIntoThread(scope store.Scope, threadID string, turn store.AssistantTurn, run store.AssistantRun) {
	updates, unsubscribe, err := s.projectAssistantSupervisor().Subscribe(scope, run.ID, 0)
	if err != nil {
		return
	}
	defer unsubscribe()
	lastContent := ""
	lastRequestID := ""
	lastRequestType := ""
	actionStatuses := map[string]string{}
	lastPlan := ""
	for snapshot := range updates {
		content := snapshot.Message.Content
		if strings.HasPrefix(content, lastContent) && len(content) > len(lastContent) {
			delta := content[len(lastContent):]
			payload, _ := json.Marshal(map[string]any{"delta": delta})
			_, _ = s.appendAssistantThreadEvent(context.Background(), scope, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: assistantThreadEventItemDelta, ItemID: run.ActiveMessageID, Payload: payload})
		}
		lastContent = content
		for _, action := range projectAssistantActionFeedFromMetadata(snapshot.Message.Metadata[projectMessageMetadataAssistantActionFeed]) {
			if actionStatuses[action.ID] == action.Status {
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
			data, _ := json.Marshal(action)
			item := assistantThreadItem{ID: action.ID, TurnID: turn.ID, Type: assistantThreadEventDynamicToolCall, Status: status, Content: action.Title, Data: data, CreatedAt: time.Now().UTC()}
			payload, _ := json.Marshal(map[string]any{"item": item})
			_, _ = s.appendAssistantThreadEvent(context.Background(), scope, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: eventType, ItemID: action.ID, Payload: payload})
			actionStatuses[action.ID] = action.Status
		}
		if planValue, exists := snapshot.Message.Metadata[projectAssistantMetadataPlan]; exists {
			planData, _ := json.Marshal(planValue)
			if string(planData) != lastPlan {
				item := assistantThreadItem{ID: "plan-" + turn.ID, TurnID: turn.ID, Type: assistantThreadEventPlan, Status: "in_progress", Data: planData, CreatedAt: time.Now().UTC()}
				payload, _ := json.Marshal(map[string]any{"item": item})
				_, _ = s.appendAssistantThreadEvent(context.Background(), scope, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: assistantThreadEventItemCompleted, ItemID: item.ID, Payload: payload})
				lastPlan = string(planData)
			}
		}
		if lastRequestID != "" && snapshot.Run.RequestID != lastRequestID {
			resolvedType := assistantThreadEventApprovalResolved
			itemType := "approval"
			if lastRequestType == assistantThreadEventUserInputRequested {
				resolvedType = assistantThreadEventUserInputResolved
				itemType = "input"
			}
			item := assistantThreadItem{ID: lastRequestID, TurnID: turn.ID, Type: itemType, Status: "completed", CreatedAt: time.Now().UTC()}
			payload, _ := json.Marshal(map[string]any{"requestID": lastRequestID, "item": item})
			_, _ = s.appendAssistantThreadEvent(context.Background(), scope, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: resolvedType, ItemID: lastRequestID, RequestID: lastRequestID, Payload: payload})
			lastRequestID, lastRequestType = "", ""
		}
		if snapshot.Run.RequestID != "" && snapshot.Run.RequestID != lastRequestID {
			eventType := ""
			switch snapshot.Run.Status {
			case store.AssistantRunStatusPendingPermission:
				eventType = assistantThreadEventApprovalRequested
			case store.AssistantRunStatusPendingInput:
				eventType = assistantThreadEventUserInputRequested
			}
			if eventType != "" {
				interrupt := projectAssistantUIInterruptFromMetadata(snapshot.Message.Metadata[projectMessageMetadataAssistantInterrupt])
				interruptData, _ := json.Marshal(interrupt)
				itemType := "approval"
				if eventType == assistantThreadEventUserInputRequested {
					itemType = "input"
				}
				item := assistantThreadItem{ID: snapshot.Run.RequestID, TurnID: turn.ID, Type: itemType, Status: "in_progress", Data: interruptData, CreatedAt: time.Now().UTC()}
				payload, _ := json.Marshal(map[string]any{
					"requestID": snapshot.Run.RequestID,
					"interrupt": interrupt,
					"item":      item,
				})
				_, _ = s.appendAssistantThreadEvent(context.Background(), scope, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: eventType, ItemID: snapshot.Run.RequestID, RequestID: snapshot.Run.RequestID, Payload: payload})
				lastRequestID = snapshot.Run.RequestID
				lastRequestType = eventType
			}
		}
		if !assistantRunTerminal(snapshot.Run.Status) {
			continue
		}
		item := assistantThreadItem{ID: run.ActiveMessageID, TurnID: turn.ID, Type: assistantThreadEventAssistantMessage, Status: "completed", Content: content, CreatedAt: snapshot.Message.CreatedAt}
		payload, _ := json.Marshal(map[string]any{"item": item})
		_, _ = s.appendAssistantThreadEvent(context.Background(), scope, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: assistantThreadEventItemCompleted, ItemID: item.ID, Payload: payload})
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
		turnPayload, _ := json.Marshal(map[string]any{"turn": turn})
		_ = s.saveAssistantTurnWithEvent(context.Background(), scope, turn, store.AssistantThreadEvent{ThreadID: threadID, TurnID: turn.ID, Type: terminalType, Payload: turnPayload})
		return
	}
}
