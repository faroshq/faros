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

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type projectAssistantRunStartResponse struct {
	Run       store.AssistantRun        `json:"run"`
	User      aiv1alpha1.ProjectMessage `json:"user"`
	Assistant aiv1alpha1.ProjectMessage `json:"assistant"`
}

type projectAssistantSupervisorRunContextKey struct{}

func (s *Server) startProjectAssistantRun(w http.ResponseWriter, r *http.Request) {
	c, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	var request CreateProjectMessageRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Content = strings.TrimSpace(request.Content)
	request.ClientRequestID = strings.TrimSpace(request.ClientRequestID)
	if request.Content == "" || request.ClientRequestID == "" {
		writeProjectError(w, newValidationError("content and clientRequestID are required"))
		return
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project.Name)
	if err := s.reconcileOrphanedProjectAssistantRun(r.Context(), scope); err != nil {
		writeProjectError(w, err)
		return
	}
	if prior, err := s.store.FindAssistantRunByClientRequestID(r.Context(), scope, request.ClientRequestID); err == nil {
		s.writeProjectAssistantRunStart(w, http.StatusAccepted, scope, prior)
		return
	} else if !errors.Is(err, store.ErrAssistantRunNotFound) {
		writeProjectError(w, err)
		return
	}
	now := time.Now().UTC()
	user := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleUser, Content: request.Content, CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleAssistant, Content: "", CreatedAt: now, UpdatedAt: now}
	run := store.AssistantRun{ID: "run-" + uuid.NewString(), Status: store.AssistantRunStatusRunning, ClientRequestID: request.ClientRequestID, ActiveMessageID: assistant.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	created, err := s.store.CreateAssistantRun(r.Context(), scope, user, assistant, run)
	if err != nil {
		if errors.Is(err, store.ErrAssistantRunConflict) {
			s.writeProjectAssistantRunConflict(w, scope)
			return
		}
		writeProjectError(w, err)
		return
	}
	supervisor := s.projectAssistantSupervisor()
	if err := supervisor.Start(r.Context(), scope, created, assistant, func(ctx context.Context, accumulator *projectAssistantSnapshotAccumulator) {
		content := &strings.Builder{}
		var toolCalls []projectToolCallStreamEvent
		req := r.Clone(context.WithValue(ctx, projectAssistantSupervisorRunContextKey{}, created))
		_, err := s.generateProjectAssistantStream(req, id, c, project, projectAssistantStreamCallbacks{
			OnChunk: func(chunk string) {
				content.WriteString(chunk)
				_ = accumulator.UpdateText(ctx, content.String(), false)
			},
			OnStatus: func(status string) { _ = status },
			OnToolCall: func(event projectToolCallStreamEvent) {
				toolCalls = upsertProjectToolCallStreamEvent(toolCalls, event)
				_ = accumulator.SetMessageMetadata(ctx, projectAssistantMessageMetadata("", sanitizeProjectToolCallStreamEventsForMetadata(toolCalls)))
			},
			OnAssistantEvent: func(event projectAssistantEvent) {
				if event.Permission != nil && event.Permission.ToolCallID != "" {
					toolCalls = upsertProjectToolCallStreamEvent(toolCalls, projectToolCallStreamEvent{ID: event.Permission.ToolCallID, Name: event.Permission.ToolName, Status: "permission_required", Summary: event.Permission.Reason, Permission: event.Permission})
				}
				if event.FollowUp != nil && event.FollowUp.ToolCallID != "" {
					toolCalls = upsertProjectToolCallStreamEvent(toolCalls, projectToolCallStreamEvent{ID: event.FollowUp.ToolCallID, Name: projectToolAskFollowUp, Status: "input_required", Summary: event.FollowUp.Prompt, FollowUp: event.FollowUp})
				}
				if event.Checkpoint != nil {
					for i := range toolCalls {
						if toolCalls[i].Status == "permission_required" || toolCalls[i].Status == "input_required" {
							toolCalls[i].Checkpoint = event.Checkpoint
						}
					}
				}
				_ = accumulator.SetMessageMetadata(ctx, projectAssistantMessageMetadata("", sanitizeProjectToolCallStreamEventsForMetadata(toolCalls)))
			},
		})
		_ = accumulator.UpdateText(ctx, content.String(), true)
		if err == nil {
			_ = accumulator.SetStatus(ctx, store.AssistantRunStatusCompleted)
			return
		}
		var permissionErr *projectAssistantPermissionRequiredError
		if errors.As(err, &permissionErr) {
			_ = accumulator.SetStatus(context.Background(), store.AssistantRunStatusPendingPermission)
			return
		}
		var inputErr *projectAssistantInputRequiredError
		if errors.As(err, &inputErr) {
			_ = accumulator.SetStatus(context.Background(), store.AssistantRunStatusPendingInput)
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(context.Cause(ctx), context.Canceled) {
			_ = accumulator.SetStatus(context.Background(), store.AssistantRunStatusAborted)
			return
		}
		_ = accumulator.SetStatus(context.Background(), store.AssistantRunStatusFailed)
	}); err != nil {
		if errors.Is(err, store.ErrAssistantRunConflict) {
			s.writeProjectAssistantRunStart(w, http.StatusAccepted, scope, created)
			return
		}
		writeProjectError(w, err)
		return
	}
	s.writeProjectAssistantRunStart(w, http.StatusAccepted, scope, created)
}

func (s *Server) writeProjectAssistantRunStart(w http.ResponseWriter, status int, scope store.Scope, run store.AssistantRun) {
	message, err := s.findProjectMessage(context.Background(), scope, run.ActiveMessageID)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	page, err := s.store.ListMessages(context.Background(), scope, 500, "")
	if err != nil {
		writeProjectError(w, err)
		return
	}
	var user store.Message
	for i, candidate := range page.Items {
		if candidate.ID != run.ActiveMessageID {
			continue
		}
		for preceding := i - 1; preceding >= 0; preceding-- {
			if page.Items[preceding].Role == aiv1alpha1.ProjectMessageRoleUser {
				user = page.Items[preceding]
				break
			}
		}
		break
	}
	writeJSON(w, status, projectAssistantRunStartResponse{Run: run, User: projectMessageToAPI(user), Assistant: projectMessageToAPI(message)})
}

func (s *Server) writeProjectAssistantRunConflict(w http.ResponseWriter, scope store.Scope) {
	run, err := s.store.LatestAssistantRun(context.Background(), scope)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusConflict, projectAssistantRunSnapshot{Run: run})
}

func (s *Server) latestProjectAssistantRun(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project.Name)
	if err := s.reconcileOrphanedProjectAssistantRun(r.Context(), scope); err != nil {
		writeProjectError(w, err)
		return
	}
	run, err := s.store.LatestAssistantRun(r.Context(), scope)
	if errors.Is(err, store.ErrAssistantRunNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeProjectError(w, err)
		return
	}
	message, err := s.findProjectMessage(r.Context(), scope, run.ActiveMessageID)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectAssistantRunSnapshot{Run: run, Message: message})
}

func (s *Server) streamProjectAssistantSnapshots(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project.Name)
	runID := mux.Vars(r)["run"]
	if err := s.reconcileOrphanedProjectAssistantRun(r.Context(), scope); err != nil {
		writeProjectError(w, err)
		return
	}
	after := projectAssistantAfterRevision(r)
	updates, unsubscribe, err := s.projectAssistantSupervisor().Subscribe(scope, runID, after)
	if errors.Is(err, store.ErrAssistantRunNotFound) {
		run, loadErr := s.store.GetAssistantRun(r.Context(), scope, runID)
		if loadErr != nil {
			writeProjectError(w, loadErr)
			return
		}
		message, loadErr := s.findProjectMessage(r.Context(), scope, run.ActiveMessageID)
		if loadErr != nil {
			writeProjectError(w, loadErr)
			return
		}
		flusher, streamOK := startProjectMessageStream(w)
		if !streamOK {
			return
		}
		_ = writeProjectAssistantSnapshotSSE(w, flusher, projectAssistantRunSnapshot{Run: run, Message: message})
		return
	}
	if err != nil {
		writeProjectError(w, err)
		return
	}
	defer unsubscribe()
	flusher, streamOK := startProjectMessageStream(w)
	if !streamOK {
		return
	}
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case snapshot, open := <-updates:
			if !open {
				return
			}
			if err := writeProjectAssistantSnapshotSSE(w, flusher, snapshot); err != nil {
				return
			}
			if assistantRunTerminal(snapshot.Run.Status) {
				return
			}
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

func projectAssistantAfterRevision(r *http.Request) int64 {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("afterRevision")
	}
	value, _ := strconv.ParseInt(raw, 10, 64)
	return value
}

func writeProjectAssistantSnapshotSSE(w http.ResponseWriter, flusher http.Flusher, snapshot projectAssistantRunSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: snapshot\ndata: %s\n\n", snapshot.Run.Revision, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (s *Server) reconcileOrphanedProjectAssistantRun(ctx context.Context, scope store.Scope) error {
	run, err := s.store.LatestAssistantRun(ctx, scope)
	if errors.Is(err, store.ErrAssistantRunNotFound) || run.Status != store.AssistantRunStatusRunning {
		return nil
	}
	if err != nil {
		return err
	}
	key := projectAssistantRunKey{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID, ProjectName: scope.ProjectName}
	supervisor := s.projectAssistantSupervisor()
	supervisor.mu.Lock()
	active := supervisor.runs[key]
	supervisor.mu.Unlock()
	if active != nil && active.run.ID == run.ID {
		return nil
	}
	run.Status = store.AssistantRunStatusInterrupted
	run.UpdatedAt = time.Now().UTC()
	run.Revision++
	return s.store.SaveAssistantRunSnapshot(ctx, scope, run, nil, run.Revision-1)
}
