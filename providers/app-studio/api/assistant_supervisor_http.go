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
	"sync"
	"time"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type projectAssistantRunStartResponse struct {
	Run       store.AssistantRun         `json:"run"`
	User      *aiv1alpha1.ProjectMessage `json:"user,omitempty"`
	Assistant aiv1alpha1.ProjectMessage  `json:"assistant"`
}

type projectAssistantSupervisorRunContextKey struct{}

const (
	projectAssistantMetadataRunID                = "assistantRunID"
	projectAssistantMetadataRevision             = "assistantRevision"
	projectAssistantMetadataWorkingStatus        = "assistantStatus"
	projectAssistantMetadataProvisional          = "assistantProvisional"
	projectAssistantMetadataPreviewRefreshNeeded = "previewRefreshNeeded"
)

func projectAssistantDurableMetadataForTransition(run store.AssistantRun, status string, provisional, preview bool, toolCalls []projectToolCallStreamEvent) map[string]any {
	metadata := projectAssistantMessageMetadata(status, sanitizeProjectToolCallStreamEventsForMetadata(toolCalls))
	metadata[projectAssistantMetadataRunID] = run.ID
	metadata[projectAssistantMetadataRevision] = run.Revision
	metadata[projectAssistantMetadataWorkingStatus] = status
	metadata[projectAssistantMetadataProvisional] = provisional
	metadata[projectAssistantMetadataPreviewRefreshNeeded] = preview
	return metadata
}

func projectAssistantDurableMetadataFromExisting(run store.AssistantRun, status string, provisional bool, existing map[string]any) map[string]any {
	metadata := map[string]any{}
	if actions := projectAssistantUIActionsFromMetadata(existing[projectMessageMetadataAssistantActions]); len(actions) > 0 {
		metadata[projectMessageMetadataAssistantActions] = actions
	}
	if interrupt := projectAssistantUIInterruptFromMetadata(existing[projectMessageMetadataAssistantInterrupt]); interrupt != nil {
		metadata[projectMessageMetadataAssistantInterrupt] = interrupt
	}
	preview, _ := existing[projectAssistantMetadataPreviewRefreshNeeded].(bool)
	metadata[projectAssistantMetadataRunID] = run.ID
	metadata[projectAssistantMetadataRevision] = run.Revision
	metadata[projectAssistantMetadataWorkingStatus] = status
	metadata[projectAssistantMetadataProvisional] = provisional
	metadata[projectAssistantMetadataPreviewRefreshNeeded] = preview
	return metadata
}

type projectAssistantDurableMetadataState struct {
	status      string
	provisional bool
	toolCalls   []projectToolCallStreamEvent
}

func projectAssistantRunDisplayStatus(status store.AssistantRunStatus, fallback string) string {
	switch status {
	case store.AssistantRunStatusCompleted:
		return "Completed"
	case store.AssistantRunStatusAborted:
		return "Aborted"
	case store.AssistantRunStatusFailed:
		return "Failed"
	case store.AssistantRunStatusInterrupted:
		return "Interrupted"
	case store.AssistantRunStatusPendingPermission:
		return projectMessageStatusPendingPermission
	case store.AssistantRunStatusPendingInput:
		return projectMessageStatusPendingInput
	}
	return fallback
}

// persistProjectAssistantDurableMetadata is the one metadata write path for
// both a fresh run and a resumed continuation. It derives the metadata revision
// from the same transition that persists the run and message.
func (s *Server) persistProjectAssistantDurableMetadata(ctx context.Context, accumulator *projectAssistantSnapshotAccumulator, workspaceScope workspace.Scope, state *projectAssistantDurableMetadataState, runStatus *store.AssistantRunStatus) error {
	return accumulator.UpdateSnapshot(ctx, func(run *store.AssistantRun, message *store.Message) {
		if runStatus != nil {
			run.Status = *runStatus
		}
		if assistantRunTerminal(run.Status) {
			state.provisional = false
		}
		next := *run
		next.Revision++
		message.Metadata = projectAssistantDurableMetadataForTransition(
			next,
			projectAssistantRunDisplayStatus(run.Status, state.status),
			state.provisional,
			s.projectAssistantPreviewRefreshNeeded(ctx, workspaceScope, "", false, state.toolCalls),
			state.toolCalls,
		)
	})
}

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
	supervisor := s.projectAssistantSupervisor()
	releaseReservation, err := supervisor.Reserve(scope)
	if err != nil {
		if errors.Is(err, store.ErrAssistantRunConflict) {
			if _, latestErr := s.store.LatestAssistantRun(r.Context(), scope); latestErr == nil {
				s.writeProjectAssistantRunConflict(w, scope)
			} else {
				writeStatus(w, http.StatusConflict, "Conflict", "assistant run start is already in progress")
			}
			return
		}
		writeProjectError(w, err)
		return
	}
	defer releaseReservation()
	now := time.Now().UTC()
	user := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleUser, Content: request.Content, CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleAssistant, Content: "", CreatedAt: now, UpdatedAt: now}
	run := store.AssistantRun{ID: "run-" + uuid.NewString(), Status: store.AssistantRunStatusRunning, ClientRequestID: request.ClientRequestID, UserMessageID: user.ID, ActiveMessageID: assistant.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	assistant.Metadata = projectAssistantDurableMetadataForTransition(run, "Working", false, false, nil)
	created, err := s.store.CreateAssistantRun(r.Context(), scope, user, assistant, run)
	if err != nil {
		if errors.Is(err, store.ErrAssistantRunConflict) {
			s.writeProjectAssistantRunConflict(w, scope)
			return
		}
		writeProjectError(w, err)
		return
	}
	if err := supervisor.Start(r.Context(), scope, created, assistant, func(ctx context.Context, accumulator *projectAssistantSnapshotAccumulator) {
		content := &strings.Builder{}
		state := &projectAssistantDurableMetadataState{status: "Working"}
		workspaceScope := projectWorkspaceScope(id, project.Name)
		persistMetadata := func(ctx context.Context, runStatus *store.AssistantRunStatus) error {
			return s.persistProjectAssistantDurableMetadata(ctx, accumulator, workspaceScope, state, runStatus)
		}
		var snapshotErr error
		var snapshotErrMu sync.Mutex
		recordSnapshotErr := func(err error) {
			if err == nil {
				return
			}
			snapshotErrMu.Lock()
			if snapshotErr == nil {
				snapshotErr = err
			}
			snapshotErrMu.Unlock()
		}
		getSnapshotErr := func() error {
			snapshotErrMu.Lock()
			defer snapshotErrMu.Unlock()
			return snapshotErr
		}
		req := r.Clone(context.WithValue(ctx, projectAssistantSupervisorRunContextKey{}, created))
		_, err := s.generateProjectAssistantStream(req, id, c, project, projectAssistantStreamCallbacks{
			OnChunk: func(chunk string) {
				content.WriteString(chunk)
				recordSnapshotErr(accumulator.UpdateText(ctx, content.String(), false))
			},
			OnProvisionalText: func(_ string) {
				state.provisional = true
				recordSnapshotErr(persistMetadata(ctx, nil))
			},
			OnProvisionalReset: func() {
				state.provisional = false
				recordSnapshotErr(persistMetadata(ctx, nil))
			},
			OnStatus: func(nextStatus string) {
				state.status = nextStatus
				recordSnapshotErr(persistMetadata(ctx, nil))
			},
			OnToolCall: func(event projectToolCallStreamEvent) {
				state.toolCalls = upsertProjectToolCallStreamEvent(state.toolCalls, event)
				recordSnapshotErr(persistMetadata(ctx, nil))
			},
			OnAssistantEvent: func(event projectAssistantEvent) {
				if event.Permission != nil && event.Permission.ToolCallID != "" {
					state.toolCalls = upsertProjectToolCallStreamEvent(state.toolCalls, projectToolCallStreamEvent{ID: event.Permission.ToolCallID, Name: event.Permission.ToolName, Status: "permission_required", Summary: event.Permission.Reason, Permission: event.Permission})
				}
				if event.FollowUp != nil && event.FollowUp.ToolCallID != "" {
					state.toolCalls = upsertProjectToolCallStreamEvent(state.toolCalls, projectToolCallStreamEvent{ID: event.FollowUp.ToolCallID, Name: projectToolAskFollowUp, Status: "input_required", Summary: event.FollowUp.Prompt, FollowUp: event.FollowUp})
				}
				if event.Checkpoint != nil {
					for i := range state.toolCalls {
						if state.toolCalls[i].Status == "permission_required" || state.toolCalls[i].Status == "input_required" {
							state.toolCalls[i].Checkpoint = event.Checkpoint
						}
					}
				}
				recordSnapshotErr(persistMetadata(ctx, nil))
			},
		})
		recordSnapshotErr(accumulator.UpdateText(ctx, content.String(), true))
		if getSnapshotErr() != nil {
			return
		}
		if err == nil {
			state.status = "Completed"
			runStatus := store.AssistantRunStatusCompleted
			recordSnapshotErr(persistMetadata(ctx, &runStatus))
			return
		}
		var permissionErr *projectAssistantPermissionRequiredError
		if errors.As(err, &permissionErr) {
			state.status = projectMessageStatusPendingPermission
			runStatus := store.AssistantRunStatusPendingPermission
			recordSnapshotErr(persistMetadata(context.Background(), &runStatus))
			return
		}
		var inputErr *projectAssistantInputRequiredError
		if errors.As(err, &inputErr) {
			state.status = projectMessageStatusPendingInput
			runStatus := store.AssistantRunStatusPendingInput
			recordSnapshotErr(persistMetadata(context.Background(), &runStatus))
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(context.Cause(ctx), context.Canceled) {
			state.status = "Aborted"
			runStatus := store.AssistantRunStatusAborted
			recordSnapshotErr(persistMetadata(context.Background(), &runStatus))
			return
		}
		state.status = "Failed"
		runStatus := store.AssistantRunStatusFailed
		recordSnapshotErr(persistMetadata(context.Background(), &runStatus))
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
	response := projectAssistantRunStartResponse{Run: run, Assistant: projectMessageToAPI(message)}
	if strings.TrimSpace(run.UserMessageID) != "" {
		user, err := s.findProjectMessage(context.Background(), scope, run.UserMessageID)
		if err != nil {
			writeProjectError(w, err)
			return
		}
		apiUser := projectMessageToAPI(user)
		response.User = &apiUser
	}
	writeJSON(w, status, response)
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
	if err != nil {
		if errors.Is(err, store.ErrAssistantRunNotFound) {
			return nil
		}
		return err
	}
	if run.Status != store.AssistantRunStatusRunning {
		return nil
	}
	key := projectAssistantRunKey{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID, ProjectName: scope.ProjectName}
	supervisor := s.projectAssistantSupervisor()
	if supervisor.reserved(scope) {
		return nil
	}
	supervisor.mu.Lock()
	active := supervisor.runs[key]
	supervisor.mu.Unlock()
	if active != nil && active.run.ID == run.ID {
		return nil
	}
	run.Status = store.AssistantRunStatusInterrupted
	run.UpdatedAt = time.Now().UTC()
	run.Revision++
	message, err := s.findProjectMessage(ctx, scope, run.ActiveMessageID)
	if err != nil {
		return err
	}
	message.UpdatedAt = run.UpdatedAt
	message.Metadata = projectAssistantDurableMetadataFromExisting(run, "Interrupted", false, message.Metadata)
	return s.store.SaveAssistantRunSnapshot(ctx, scope, run, []store.Message{message}, run.Revision-1)
}
