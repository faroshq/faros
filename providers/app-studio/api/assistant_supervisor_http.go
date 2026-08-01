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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type projectAssistantRunStartResponse struct {
	Run       projectAssistantRunView    `json:"run"`
	User      *aiv1alpha1.ProjectMessage `json:"user,omitempty"`
	Assistant aiv1alpha1.ProjectMessage  `json:"assistant"`
}

// projectAssistantRunView is the public run contract. Durable execution
// records also contain checkpoint, audit, grant, and project-scoping state that
// must remain server-side.
type projectAssistantRunView struct {
	ID              string                      `json:"id"`
	EngineVersion   string                      `json:"engineVersion,omitempty"`
	WorkItemID      string                      `json:"workItemID,omitempty"`
	Mode            store.AssistantRunMode      `json:"mode,omitempty"`
	ApprovalMode    store.AssistantApprovalMode `json:"approvalMode,omitempty"`
	Status          store.AssistantRunStatus    `json:"status"`
	ClientRequestID string                      `json:"clientRequestID,omitempty"`
	UserMessageID   string                      `json:"userMessageID,omitempty"`
	ActiveMessageID string                      `json:"activeMessageID,omitempty"`
	Revision        int64                       `json:"revision,omitempty"`
	RequestID       string                      `json:"requestID,omitempty"`
	CreatedAt       time.Time                   `json:"createdAt"`
	UpdatedAt       time.Time                   `json:"updatedAt"`
}

type projectAssistantRunSnapshotResponse struct {
	Run     projectAssistantRunView   `json:"run"`
	Message aiv1alpha1.ProjectMessage `json:"message"`
}

func projectAssistantRunToAPI(run store.AssistantRun) projectAssistantRunView {
	legacyWorkItemID := run.WorkItemID
	if projectAssistantRunUsesV2(&run) {
		legacyWorkItemID = ""
	}
	return projectAssistantRunView{
		ID:              run.ID,
		EngineVersion:   run.EngineVersion,
		WorkItemID:      legacyWorkItemID,
		Mode:            run.Mode,
		ApprovalMode:    run.ApprovalMode,
		Status:          run.Status,
		ClientRequestID: run.ClientRequestID,
		UserMessageID:   run.UserMessageID,
		ActiveMessageID: run.ActiveMessageID,
		Revision:        run.Revision,
		RequestID:       run.RequestID,
		CreatedAt:       run.CreatedAt,
		UpdatedAt:       run.UpdatedAt,
	}
}

func projectAssistantRunSnapshotToAPI(snapshot projectAssistantRunSnapshot) projectAssistantRunSnapshotResponse {
	return projectAssistantRunSnapshotResponse{
		Run:     projectAssistantRunToAPI(snapshot.Run),
		Message: projectMessageToAPI(snapshot.Message),
	}
}

type projectAssistantDurableStartResult struct {
	Run       store.AssistantRun
	User      store.Message
	Assistant store.Message
	Started   bool
}

// projectAssistantDurableFinalContent makes the engine's returned response
// authoritative when present. Chunk callbacks are progressive UI snapshots;
// they can be empty or partial and must never truncate or duplicate the final
// durable assistant message.
func projectAssistantDurableFinalContent(reply, streamed string) string {
	return projectAssistantStoredContent(reply, streamed)
}

func projectAssistantDurableTerminalContent(reply, streamed string, err error) string {
	content := projectAssistantDurableFinalContent(reply, streamed)
	if projectEinoAssistantBoundedExit(err) || errors.Is(err, errProjectAssistantNoOutput) {
		if projectEinoAssistantBoundedClosingAnswerValid(content) {
			return content
		}
		if errors.Is(err, errProjectAssistantNoOutput) {
			return projectEinoAssistantBoundedClosingFallback("No usable assistant response was produced for this turn.")
		}
		return projectEinoAssistantBoundedClosingFallback("")
	}
	if projectAssistantShouldPersistTerminalFailure(err) {
		return projectAssistantContentWithTerminalFailure(content, err)
	}
	return content
}

func projectAssistantMutationAwareTerminalContent(
	reply, streamed string,
	err error,
	evidence projectAssistantCompletionEvidence,
	engineCompleted bool,
) string {
	return projectAssistantEffectAwareTerminalContent(
		reply,
		streamed,
		err,
		evidence,
		engineCompleted,
		false,
	)
}

func projectAssistantEffectAwareTerminalContent(
	reply, streamed string,
	err error,
	evidence projectAssistantCompletionEvidence,
	engineCompleted bool,
	successfulNonSourceEffect bool,
) string {
	content := projectAssistantDurableTerminalContent(reply, streamed, err)
	if evidence.SourceMutationRevision == 0 && (!engineCompleted || !successfulNonSourceEffect) {
		return content
	}
	if engineCompleted && strings.TrimSpace(content) != "" {
		content = projectAssistantTruthfulModelSummary(content)
		if content != "" || evidence.SourceMutationRevision == 0 {
			return projectAssistantContentWithVerificationScope(content, evidence)
		}
	}
	if evidence.SourceMutationRevision == 0 {
		return projectAssistantContentWithVerificationScope("", evidence)
	}
	return projectAssistantMutationTerminalContent(evidence, engineCompleted)
}

func (s *Server) projectAssistantRunTerminalContent(
	ctx context.Context,
	scope store.Scope,
	run store.AssistantRun,
	reply, streamed string,
	err error,
	evidence projectAssistantCompletionEvidence,
	engineCompleted bool,
) string {
	return projectAssistantEffectAwareTerminalContent(
		reply,
		streamed,
		err,
		evidence,
		engineCompleted,
		s.projectAssistantSuccessfulTerminalEffectWithoutSourceEvidence(
			ctx,
			scope,
			run,
			evidence,
			engineCompleted,
		),
	)
}

func projectAssistantTruthfulModelSummary(content string) string {
	content = strings.TrimSpace(content)
	lower := strings.ToLower(content)
	claimIndex := -1
	for _, claim := range []string{
		"verify", "verified", "confirmed", "passed", "satisfied", "met",
		"works", "working", "renders", "rendered", "behaves", "succeeded",
		"successful", "correct", "correctly", "functional", "functioning",
		"tested", "validated", "observed", "demonstrated", "proved", "proven",
		"exercised", "checked",
	} {
		index := projectAssistantSummaryWordIndex(lower, claim)
		if index >= 0 && (claimIndex < 0 || index < claimIndex) {
			claimIndex = index
		}
	}
	for _, scope := range []string{
		"acceptance criteria",
		"application behavior",
		"development preview",
		"rendered content",
		"user interaction",
		"data flow",
		"user flow",
	} {
		index := strings.Index(lower, scope)
		if index >= 0 && (claimIndex < 0 || index < claimIndex) {
			claimIndex = index
		}
	}
	if claimIndex < 0 {
		return content
	}
	cut := -1
	for _, separator := range []string{". ", "\n", ";", ", but ", " and "} {
		if index := strings.LastIndex(lower[:claimIndex], separator); index >= 0 {
			candidate := index
			if separator == ". " {
				candidate++
			}
			if candidate > cut {
				cut = candidate
			}
		}
	}
	if cut < 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(content[:cut], " ,;:-"))
}

func projectAssistantSummaryWordIndex(content, word string) int {
	for offset := 0; offset < len(content); {
		index := strings.Index(content[offset:], word)
		if index < 0 {
			return -1
		}
		index += offset
		beforeOK := index == 0 || content[index-1] < 'a' || content[index-1] > 'z'
		after := index + len(word)
		afterOK := after == len(content) || content[after] < 'a' || content[after] > 'z'
		if beforeOK && afterOK {
			return index
		}
		offset = index + len(word)
	}
	return -1
}

func projectAssistantContentWithVerificationScope(
	content string,
	evidence projectAssistantCompletionEvidence,
) string {
	lines := []string{"Verification scope:"}
	if evidence.SourceMutationRevision > 0 {
		operational := "not completed for the current workspace revision"
		if projectAssistantVerifiedMutationCompleted(evidence) {
			operational = "passed for the current workspace revision (synchronization, process/log health, and preview reachability only)"
		}
		lines = append(lines, "- Operational checks: "+operational+".")
	}
	lines = append(lines,
		"- Application behavior was not independently verified; rendered content, interactions, data flow, and acceptance criteria were not proven.",
	)
	block := strings.Join(lines, "\n")
	content = strings.TrimSpace(content)
	if strings.Contains(content, block) {
		return content
	}
	if content == "" {
		return block
	}
	return content + "\n\n" + block
}

type projectAssistantDurableTerminalEffectCall struct {
	toolName   string
	argsDigest string
}

func (s *Server) projectAssistantSuccessfulTerminalEffectWithoutSourceEvidence(
	ctx context.Context,
	scope store.Scope,
	run store.AssistantRun,
	evidence projectAssistantCompletionEvidence,
	engineCompleted bool,
) bool {
	if !engineCompleted || evidence.SourceMutationRevision > 0 || !projectAssistantRunUsesV2(&run) {
		return false
	}
	effect, err := s.projectAssistantSuccessfulNonSourceRunEffect(ctx, scope, run.ID)
	if err != nil {
		// If durable effect evidence cannot be read, fail closed on response
		// truthfulness: the disclosure makes no positive claim and is safer
		// than preserving a model-authored behavioral overclaim.
		return true
	}
	return effect
}

func (s *Server) projectAssistantSuccessfulNonSourceRunEffect(
	ctx context.Context,
	scope store.Scope,
	runID string,
) (bool, error) {
	if s == nil || s.store == nil || strings.TrimSpace(runID) == "" {
		return false, errors.New("assistant run event store is not configured")
	}
	registry := projectAssistantLocalToolRegistry(nil)
	calls := map[string]projectAssistantDurableTerminalEffectCall{}
	afterSequence := int64(0)
	for {
		events, err := s.store.ListAssistantRunEvents(
			ctx,
			scope,
			runID,
			afterSequence,
			projectAssistantRunEventPageSize,
		)
		if err != nil {
			return false, err
		}
		for _, event := range events {
			switch event.Type {
			case projectAssistantRunToolCallEventType:
				var payload projectAssistantRunToolCallPayload
				if json.Unmarshal(event.Payload, &payload) != nil || !payload.Effect ||
					!projectAssistantTerminalToolMutates(registry, event.ToolName) {
					continue
				}
				calls[event.CallID] = projectAssistantDurableTerminalEffectCall{
					toolName:   event.ToolName,
					argsDigest: event.ArgsDigest,
				}
			case projectAssistantRunToolResultEventType:
				call, ok := calls[event.CallID]
				if !ok || call.toolName != event.ToolName || call.argsDigest != event.ArgsDigest {
					continue
				}
				var payload projectAssistantRunToolResultPayload
				if json.Unmarshal(event.Payload, &payload) != nil {
					continue
				}
				if projectAssistantTerminalEffectResultSucceeded(call.toolName, payload) {
					return true, nil
				}
			}
		}
		if len(events) < projectAssistantRunEventPageSize {
			return false, nil
		}
		nextSequence := events[len(events)-1].Sequence
		if nextSequence <= afterSequence {
			return false, errors.New("assistant run event pagination did not advance")
		}
		afterSequence = nextSequence
	}
}

func projectAssistantTerminalToolMutates(
	registry projectAssistantToolRegistry,
	name string,
) bool {
	spec, ok := registry.Spec(name)
	if !ok {
		spec, ok = projectAssistantWorkflowToolSpec(name)
	}
	if !ok {
		spec, ok = projectAssistantMCPToolSpec(projectMCPTool{Name: name})
	}
	if !ok {
		return false
	}
	switch spec.Risk {
	case projectAssistantToolRiskWrite, projectAssistantToolRiskRuntime, projectAssistantToolRiskCommit:
		return true
	default:
		return false
	}
}

func projectAssistantTerminalEffectResultSucceeded(
	name string,
	payload projectAssistantRunToolResultPayload,
) bool {
	if payload.Failed || !projectEinoAssistantPhaseSuccessfulToolContent(payload.Result) {
		return false
	}
	if projectEinoAssistantCommitTool(name) && projectToolCallResultStatus(name, payload.Result) != "succeeded" {
		return false
	}
	decoded := map[string]any{}
	if json.Unmarshal([]byte(strings.TrimSpace(payload.Result)), &decoded) != nil {
		return true
	}
	for _, field := range []string{"status", "phase", "outcome"} {
		switch strings.ToLower(projectToolString(decoded[field])) {
		case "blocked", "cancelled", "canceled", "denied", "error", "failed", "failure", "not_configured", "not_ready", "rejected", "unavailable":
			return false
		}
	}
	return true
}

// appendProjectAssistantStreamBlock keeps complete assistant updates readable
// while a tool-driven turn is running. These are accepted assistant prose
// blocks, not token deltas or reasoning content; the final returned response
// remains authoritative for the durable terminal message.
func appendProjectAssistantStreamBlock(content *strings.Builder, block string) string {
	block = strings.TrimSpace(block)
	if block == "" {
		return content.String()
	}
	current := strings.TrimSpace(content.String())
	if current == block || strings.HasSuffix(current, "\n\n"+block) {
		return content.String()
	}
	if content.Len() > 0 {
		content.WriteString("\n\n")
	}
	content.WriteString(block)
	return content.String()
}

// startProjectAssistantRunDurably is the one start boundary for every new
// conversation turn. It validates its durable inputs, reserves the project,
// atomically creates the user message, assistant placeholder and run, then
// hands the run to a server-owned worker. It deliberately accepts no response
// writer and never derives execution from the caller's request context.
func (s *Server) startProjectAssistantRunDurably(ctx context.Context, scope store.Scope, actor, content, clientRequestID string, start func(store.AssistantRun, store.Message, bool) error) (projectAssistantDurableStartResult, error) {
	return s.startProjectAssistantRunDurablyWithMode(ctx, scope, actor, content, clientRequestID, store.AssistantRunModeDiscussion, start)
}

func (s *Server) startProjectAssistantAdaptiveRunDurably(ctx context.Context, scope store.Scope, actor, content, clientRequestID string, start func(store.AssistantRun, store.Message, bool) error) (projectAssistantDurableStartResult, error) {
	return s.startProjectAssistantRunDurablyWithMode(ctx, scope, actor, content, clientRequestID, store.AssistantRunModeAdaptive, start)
}

func (s *Server) startProjectAssistantRunDurablyWithMode(ctx context.Context, scope store.Scope, actor, content, clientRequestID string, mode store.AssistantRunMode, start func(store.AssistantRun, store.Message, bool) error) (projectAssistantDurableStartResult, error) {
	content = strings.TrimSpace(content)
	clientRequestID = strings.TrimSpace(clientRequestID)
	actor = strings.TrimSpace(actor)
	if content == "" || clientRequestID == "" || actor == "" {
		return projectAssistantDurableStartResult{}, newValidationError("content, clientRequestID, and actor are required")
	}
	if err := s.reconcileOrphanedProjectAssistantRun(ctx, scope); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	if prior, err := s.store.FindAssistantRunByClientRequestID(ctx, scope, clientRequestID); err == nil {
		if err := validateProjectAssistantStartReplay(prior, actor, content, mode, "", 0); err != nil {
			return projectAssistantDurableStartResult{}, err
		}
		return projectAssistantDurableStartResult{Run: prior}, nil
	} else if !errors.Is(err, store.ErrAssistantRunNotFound) {
		return projectAssistantDurableStartResult{}, err
	}
	supervisor := s.projectAssistantSupervisor()
	releaseReservation, err := supervisor.Reserve(scope)
	if err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	defer releaseReservation()
	messages, err := s.store.ListMessages(ctx, scope, 1, "")
	if err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	transcriptEmpty := len(messages.Items) == 0
	now := time.Now().UTC()
	assistantAt := now.Add(time.Microsecond)
	user := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleUser, ActorID: actor, Content: content, CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleAssistant, CreatedAt: assistantAt, UpdatedAt: assistantAt}
	engineVersion := ""
	if collaborationMode := projectAssistantCollaborationMode(mode); collaborationMode == projectAssistantCollaborationModeDefault || collaborationMode == projectAssistantCollaborationModePlan {
		engineVersion = store.AssistantEngineVersionV2
	}
	run := store.AssistantRun{ID: "run-" + uuid.NewString(), EngineVersion: engineVersion, Mode: mode, Status: store.AssistantRunStatusRunning, ClientRequestID: clientRequestID, UserMessageID: user.ID, ActiveMessageID: assistant.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.captureProjectAssistantApprovalMode(ctx, scope, actor, &run); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	if err := bindProjectAssistantStartRequest(&run, actor, content, "", 0); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	assistant.Metadata = projectAssistantDurableMetadataForTransition(run, "Working", false, false, nil, nil)
	created, err := s.store.CreateAssistantRun(ctx, scope, user, assistant, run)
	if err != nil {
		if prior, ok := s.recoverProjectAssistantStartReplay(ctx, scope, err, clientRequestID, actor, content, mode, "", 0); ok {
			return projectAssistantDurableStartResult{Run: prior}, nil
		}
		return projectAssistantDurableStartResult{}, err
	}
	if created.ID != run.ID {
		if err := validateProjectAssistantStartReplay(created, actor, content, mode, "", 0); err != nil {
			return projectAssistantDurableStartResult{}, err
		}
		return projectAssistantDurableStartResult{Run: created}, nil
	}
	if err := start(created, assistant, transcriptEmpty); err != nil {
		return projectAssistantDurableStartResult{Run: created, User: user, Assistant: assistant}, err
	}
	return projectAssistantDurableStartResult{Run: created, User: user, Assistant: assistant, Started: true}, nil
}

// startProjectAssistantBuildRunDurably creates the WorkItem, its root message,
// assistant placeholder, and initial mutation run in the store's single
// atomic boundary.  The actor is persisted with the root message and WorkItem;
// it is never inferred from prompt text or a client-provided field.
func (s *Server) startProjectAssistantBuildRunDurably(ctx context.Context, scope store.Scope, actor, content, clientRequestID string, start func(store.AssistantRun, store.Message, bool) error) (projectAssistantDurableStartResult, error) {
	return s.startProjectAssistantBuildRunDurablyWithInitialBootstrap(ctx, scope, actor, content, clientRequestID, false, start)
}

func (s *Server) startProjectAssistantBuildRunDurablyWithInitialBootstrap(ctx context.Context, scope store.Scope, actor, content, clientRequestID string, initialBootstrap bool, start func(store.AssistantRun, store.Message, bool) error) (projectAssistantDurableStartResult, error) {
	content, clientRequestID, actor = strings.TrimSpace(content), strings.TrimSpace(clientRequestID), strings.TrimSpace(actor)
	if content == "" || clientRequestID == "" || actor == "" {
		return projectAssistantDurableStartResult{}, newValidationError("content, clientRequestID, and actor are required")
	}
	if err := s.reconcileOrphanedProjectAssistantRun(ctx, scope); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	if prior, err := s.store.FindAssistantRunByClientRequestID(ctx, scope, clientRequestID); err == nil {
		if err := validateProjectAssistantStartReplay(prior, actor, content, store.AssistantRunModeNew, "", 0, initialBootstrap); err != nil {
			return projectAssistantDurableStartResult{}, err
		}
		return projectAssistantDurableStartResult{Run: prior}, nil
	} else if !errors.Is(err, store.ErrAssistantRunNotFound) {
		return projectAssistantDurableStartResult{}, err
	}
	supervisor := s.projectAssistantSupervisor()
	releaseReservation, err := supervisor.Reserve(scope)
	if err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	defer releaseReservation()
	messages, err := s.store.ListMessages(ctx, scope, 1, "")
	if err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	transcriptEmpty := len(messages.Items) == 0
	now := time.Now().UTC()
	assistantAt := now.Add(time.Microsecond)
	user := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleUser, ActorID: actor, Content: content, CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleAssistant, CreatedAt: assistantAt, UpdatedAt: assistantAt}
	item := store.AssistantWorkItem{ID: "work-item-" + uuid.NewString(), RootMessageID: user.ID, CreatedBy: actor, Status: store.AssistantWorkItemStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
	user.WorkItemID = item.ID
	assistant.WorkItemID = item.ID
	run := store.AssistantRun{ID: "run-" + uuid.NewString(), WorkItemID: item.ID, Mode: store.AssistantRunModeNew, Status: store.AssistantRunStatusRunning, ClientRequestID: clientRequestID, UserMessageID: user.ID, ActiveMessageID: assistant.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.captureProjectAssistantApprovalMode(ctx, scope, actor, &run); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	if err := bindProjectAssistantStartRequest(&run, actor, content, "", 0, initialBootstrap); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	assistant.Metadata = projectAssistantDurableMetadataForTransition(run, "Working", false, false, nil, nil)
	if initialBootstrap && transcriptEmpty {
		assistant.Metadata[projectAssistantMetadataInitialBuild] = true
	}
	if _, err := s.store.CreateWorkItemAndAssistantRun(ctx, scope, item, user, assistant, run); err != nil {
		if prior, ok := s.recoverProjectAssistantStartReplay(ctx, scope, err, clientRequestID, actor, content, store.AssistantRunModeNew, "", 0, initialBootstrap); ok {
			return projectAssistantDurableStartResult{Run: prior}, nil
		}
		return projectAssistantDurableStartResult{}, err
	}
	if err := start(run, assistant, transcriptEmpty); err != nil {
		return projectAssistantDurableStartResult{Run: run, User: user, Assistant: assistant}, err
	}
	return projectAssistantDurableStartResult{Run: run, User: user, Assistant: assistant, Started: true}, nil
}

// startProjectAssistantContinueRunDurably is the only HTTP-to-store boundary
// that may reactivate a suspended WorkItem. The store repeats all selection
// checks while atomically creating the next messages and run.
func (s *Server) startProjectAssistantContinueRunDurably(ctx context.Context, scope store.Scope, workItemID, actor string, expectedRevision int64, content, clientRequestID string, start func(store.AssistantRun, store.Message, bool) error) (projectAssistantDurableStartResult, error) {
	content, clientRequestID, actor, workItemID = strings.TrimSpace(content), strings.TrimSpace(clientRequestID), strings.TrimSpace(actor), strings.TrimSpace(workItemID)
	if content == "" || clientRequestID == "" || actor == "" || workItemID == "" || expectedRevision < 1 {
		return projectAssistantDurableStartResult{}, newValidationError("content, clientRequestID, actor, workItemID, and workItemRevision are required")
	}
	if err := s.reconcileOrphanedProjectAssistantRun(ctx, scope); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	if prior, err := s.store.FindAssistantRunByClientRequestID(ctx, scope, clientRequestID); err == nil {
		if err := validateProjectAssistantStartReplay(prior, actor, content, store.AssistantRunModeContinue, workItemID, expectedRevision); err != nil {
			return projectAssistantDurableStartResult{}, err
		}
		return projectAssistantDurableStartResult{Run: prior}, nil
	} else if !errors.Is(err, store.ErrAssistantRunNotFound) {
		return projectAssistantDurableStartResult{}, err
	}
	supervisor := s.projectAssistantSupervisor()
	releaseReservation, err := supervisor.Reserve(scope)
	if err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	defer releaseReservation()
	item, err := s.store.GetAssistantWorkItem(ctx, scope, workItemID)
	if err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	now := time.Now().UTC()
	assistantAt := now.Add(time.Microsecond)
	user := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleUser, ActorID: actor, WorkItemID: workItemID, Content: content, CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: newMessageID(), Role: aiv1alpha1.ProjectMessageRoleAssistant, WorkItemID: workItemID, CreatedAt: assistantAt, UpdatedAt: assistantAt}
	run := store.AssistantRun{ID: "run-" + uuid.NewString(), WorkItemID: workItemID, Mode: store.AssistantRunModeContinue, ExpectedGrantRevision: item.GrantRevision, Status: store.AssistantRunStatusRunning, ClientRequestID: clientRequestID, UserMessageID: user.ID, ActiveMessageID: assistant.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.captureProjectAssistantApprovalMode(ctx, scope, actor, &run); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	if err := bindProjectAssistantStartRequest(&run, actor, content, workItemID, expectedRevision); err != nil {
		return projectAssistantDurableStartResult{}, err
	}
	assistant.Metadata = projectAssistantDurableMetadataForTransition(run, "Working", false, false, nil, nil)
	if _, err := s.store.ResumeWorkItemAndCreateAssistantRun(ctx, scope, workItemID, actor, expectedRevision, user, assistant, run); err != nil {
		if prior, ok := s.recoverProjectAssistantStartReplay(ctx, scope, err, clientRequestID, actor, content, store.AssistantRunModeContinue, workItemID, expectedRevision); ok {
			return projectAssistantDurableStartResult{Run: prior}, nil
		}
		return projectAssistantDurableStartResult{}, err
	}
	if err := start(run, assistant, false); err != nil {
		return projectAssistantDurableStartResult{Run: run, User: user, Assistant: assistant}, err
	}
	return projectAssistantDurableStartResult{Run: run, User: user, Assistant: assistant, Started: true}, nil
}

type projectAssistantSupervisorRunContextKey struct{}

const (
	projectAssistantMetadataRunID                = "assistantRunID"
	projectAssistantMetadataRevision             = "assistantRevision"
	projectAssistantMetadataWorkingStatus        = "assistantStatus"
	projectAssistantMetadataProvisional          = "assistantProvisional"
	projectAssistantMetadataPreviewRefreshNeeded = "previewRefreshNeeded"
	projectAssistantMetadataPlan                 = "assistantPlan"
	projectAssistantMetadataInitialBuild         = "assistantInitialBuild"
	projectAssistantMetadataProgress             = "assistantProgress"
	projectAssistantProgressMaxMessages          = 32
	projectAssistantWorkedDurationMaxMS          = int64((7 * 24 * time.Hour) / time.Millisecond)
	projectAssistantTraceMaxSequence             = 10_000
)

type projectAssistantProgressSnapshot struct {
	Version          int      `json:"version"`
	Messages         []string `json:"messages"`
	MessageSequences []int    `json:"messageSequences,omitempty"`
	WorkedDurationMS int64    `json:"workedDurationMs"`
}

func projectAssistantDurableMetadataForTransition(run store.AssistantRun, status string, provisional, preview bool, toolCalls []projectToolCallStreamEvent, plan *projectAssistantPlanSnapshot) map[string]any {
	metadata := projectAssistantMessageMetadata(status, sanitizeProjectToolCallStreamEventsForMetadata(toolCalls))
	metadata[projectAssistantMetadataRunID] = run.ID
	metadata[projectAssistantMetadataRevision] = run.Revision
	metadata[projectAssistantMetadataWorkingStatus] = status
	metadata[projectAssistantMetadataProvisional] = provisional
	metadata[projectAssistantMetadataPreviewRefreshNeeded] = preview
	if plan, ok := projectAssistantPlanSnapshotFromMetadata(plan); ok {
		metadata[projectAssistantMetadataPlan] = *plan
	}
	return metadata
}

func projectAssistantDurableMetadataFromExisting(run store.AssistantRun, status string, provisional bool, existing map[string]any) map[string]any {
	metadata := map[string]any{}
	if actions := projectAssistantActionFeedFromMetadata(existing[projectMessageMetadataAssistantActionFeed]); len(actions) > 0 {
		metadata[projectMessageMetadataAssistantActionFeed] = actions
	}
	if interrupt := projectAssistantUIInterruptFromMetadata(existing[projectMessageMetadataAssistantInterrupt]); interrupt != nil {
		metadata[projectMessageMetadataAssistantInterrupt] = interrupt
	}
	if plan, ok := projectAssistantPlanSnapshotFromMetadata(existing[projectAssistantMetadataPlan]); ok {
		metadata[projectAssistantMetadataPlan] = *plan
	}
	if initialBuild, _ := existing[projectAssistantMetadataInitialBuild].(bool); initialBuild {
		metadata[projectAssistantMetadataInitialBuild] = true
	}
	if progress, ok := projectAssistantProgressSnapshotFromMetadata(existing[projectAssistantMetadataProgress]); ok {
		metadata[projectAssistantMetadataProgress] = *progress
	}
	preview, _ := existing[projectAssistantMetadataPreviewRefreshNeeded].(bool)
	metadata[projectAssistantMetadataRunID] = run.ID
	metadata[projectAssistantMetadataRevision] = run.Revision
	metadata[projectAssistantMetadataWorkingStatus] = status
	metadata[projectAssistantMetadataProvisional] = provisional
	metadata[projectAssistantMetadataPreviewRefreshNeeded] = preview
	return metadata
}

func projectAssistantProgressSnapshotFromMetadata(value any) (*projectAssistantProgressSnapshot, bool) {
	if value == nil {
		return nil, false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var progress projectAssistantProgressSnapshot
	if err := decoder.Decode(&progress); err != nil ||
		progress.Version != 1 ||
		len(progress.Messages) == 0 ||
		len(progress.Messages) > projectAssistantProgressMaxMessages ||
		progress.WorkedDurationMS < 0 ||
		progress.WorkedDurationMS > projectAssistantWorkedDurationMaxMS {
		return nil, false
	}
	for _, message := range progress.Messages {
		if message == "" ||
			message != strings.TrimSpace(message) ||
			len(message) > projectEinoAssistantProgressMaxBytes ||
			!utf8.ValidString(message) ||
			strings.IndexFunc(message, unicode.IsControl) >= 0 {
			return nil, false
		}
	}
	if len(progress.MessageSequences) > 0 {
		if len(progress.MessageSequences) != len(progress.Messages) {
			return nil, false
		}
		previous := 0
		for _, sequence := range progress.MessageSequences {
			if sequence <= 0 || sequence > projectAssistantTraceMaxSequence {
				return nil, false
			}
			if sequence <= previous {
				return nil, false
			}
			previous = sequence
		}
	}
	return &progress, true
}

// projectAssistantPlanSnapshotFromMetadata is the durable metadata boundary
// for plans. Postgres rehydrates JSON values as generic maps, so decode them
// back into the public snapshot shape and retain only values the write_todos
// producer could have emitted. Validation deliberately does not sanitize or
// redact labels again: a retained plan must preserve its already-sanitized
// user-facing wording exactly.
func projectAssistantPlanSnapshotFromMetadata(value any) (*projectAssistantPlanSnapshot, bool) {
	if value == nil {
		return nil, false
	}
	raw, err := json.Marshal(value)
	if err != nil || !projectAssistantPlanMetadataKeysValid(raw) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan projectAssistantPlanSnapshot
	if err := decoder.Decode(&plan); err != nil || !projectAssistantPlanSnapshotValid(plan) {
		return nil, false
	}
	return &plan, true
}

func projectAssistantPlanMetadataKeysValid(raw []byte) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || len(object) != 1 {
		return false
	}
	rawSteps, ok := object["steps"]
	if !ok {
		return false
	}
	var steps []map[string]json.RawMessage
	if err := json.Unmarshal(rawSteps, &steps); err != nil {
		return false
	}
	for _, step := range steps {
		if _, ok := step["content"]; !ok {
			return false
		}
		if _, ok := step["status"]; !ok {
			return false
		}
		for key := range step {
			switch key {
			case "content", "activeForm", "status":
			default:
				return false
			}
		}
	}
	return true
}

func projectAssistantPlanSnapshotValid(plan projectAssistantPlanSnapshot) bool {
	if len(plan.Steps) == 0 || len(plan.Steps) > projectEinoAssistantTodoProgressMaxItems {
		return false
	}
	inProgress := 0
	for _, step := range plan.Steps {
		if !projectAssistantPlanLabelValid(step.Content, true) || !projectAssistantPlanLabelValid(step.ActiveForm, false) {
			return false
		}
		switch step.Status {
		case "pending", "completed":
		case "in_progress":
			inProgress++
			if inProgress > 1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func projectAssistantPlanLabelValid(label string, required bool) bool {
	if !utf8.ValidString(label) || len(label) > projectEinoAssistantTodoProgressMaxLabelBytes {
		return false
	}
	if projectEinoAssistantTodoProgressLabel(label) != label {
		return false
	}
	return !required || strings.TrimSpace(label) != ""
}

type projectAssistantDurableMetadataState struct {
	status             string
	provisional        bool
	toolCalls          []projectToolCallStreamEvent
	plan               *projectAssistantPlanSnapshot
	initialBuild       bool
	progressMessages   []string
	progressSequences  []int
	actionSequences    map[string]int
	nextTraceSequence  int
	workedDuration     time.Duration
	workSegmentStarted time.Time
}

func (s *projectAssistantDurableMetadataState) appendProgress(message string) bool {
	if s == nil {
		return false
	}
	message, reason := projectEinoAssistantProgressMessage(message)
	if reason != "" {
		return false
	}
	if len(s.progressMessages) > 0 && s.progressMessages[len(s.progressMessages)-1] == message {
		return false
	}
	if len(s.progressMessages) >= projectAssistantProgressMaxMessages {
		return false
	}
	s.progressMessages = append(s.progressMessages, message)
	s.progressSequences = append(s.progressSequences, s.nextSequence())
	return true
}

func (s *projectAssistantDurableMetadataState) restoreTrace(progress *projectAssistantProgressSnapshot, actions []projectAssistantActionFeedItem) {
	if s == nil {
		return
	}
	s.actionSequences = map[string]int{}
	if progress != nil {
		s.progressMessages = append([]string(nil), progress.Messages...)
		if len(progress.MessageSequences) == len(progress.Messages) {
			s.progressSequences = append([]int(nil), progress.MessageSequences...)
		} else {
			s.progressSequences = make([]int, len(progress.Messages))
		}
		for _, sequence := range s.progressSequences {
			if sequence > s.nextTraceSequence {
				s.nextTraceSequence = sequence
			}
		}
		s.workedDuration = time.Duration(progress.WorkedDurationMS) * time.Millisecond
	}
	for _, action := range actions {
		if action.ID == "" || action.Sequence <= 0 || action.Sequence > projectAssistantTraceMaxSequence {
			continue
		}
		s.actionSequences[action.ID] = action.Sequence
		if action.Sequence > s.nextTraceSequence {
			s.nextTraceSequence = action.Sequence
		}
	}
}

func (s *projectAssistantDurableMetadataState) upsertToolCall(event projectToolCallStreamEvent) {
	if s == nil || event.ID == "" {
		return
	}
	// Ordering is owned by the durable callback boundary. Ignore any sequence
	// carried by an upstream event and recover only from server state.
	event.Sequence = 0
	publicID := projectAssistantActionPublicID(event.ID)
	if s.actionSequences != nil {
		event.Sequence = s.actionSequences[publicID]
	}
	if event.Sequence == 0 {
		for _, existing := range s.toolCalls {
			if existing.ID == event.ID && existing.Sequence > 0 {
				event.Sequence = existing.Sequence
				break
			}
		}
	}
	if event.Sequence == 0 {
		event.Sequence = s.nextSequence()
	}
	if event.Sequence > 0 {
		if s.actionSequences == nil {
			s.actionSequences = map[string]int{}
		}
		s.actionSequences[publicID] = event.Sequence
	}
	s.toolCalls = upsertProjectToolCallStreamEvent(s.toolCalls, event)
}

func (s *projectAssistantDurableMetadataState) nextSequence() int {
	if s == nil || s.nextTraceSequence >= projectAssistantTraceMaxSequence {
		return 0
	}
	s.nextTraceSequence++
	return s.nextTraceSequence
}

func (s *projectAssistantDurableMetadataState) progressSnapshot(now time.Time) *projectAssistantProgressSnapshot {
	if s == nil || len(s.progressMessages) == 0 {
		return nil
	}
	duration := s.workedDuration
	if !s.workSegmentStarted.IsZero() && now.After(s.workSegmentStarted) {
		duration += now.Sub(s.workSegmentStarted)
	}
	durationMS := duration.Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}
	if durationMS > projectAssistantWorkedDurationMaxMS {
		durationMS = projectAssistantWorkedDurationMaxMS
	}
	messageSequences := append([]int(nil), s.progressSequences...)
	if len(messageSequences) != len(s.progressMessages) {
		messageSequences = nil
	} else {
		for _, sequence := range messageSequences {
			if sequence <= 0 {
				messageSequences = nil
				break
			}
		}
	}
	return &projectAssistantProgressSnapshot{
		Version:          1,
		Messages:         append([]string(nil), s.progressMessages...),
		MessageSequences: messageSequences,
		WorkedDurationMS: durationMS,
	}
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
		metadata := projectAssistantDurableMetadataForTransition(
			next,
			projectAssistantRunDisplayStatus(run.Status, state.status),
			state.provisional,
			s.projectAssistantPreviewRefreshNeeded(ctx, workspaceScope, "", false, state.toolCalls),
			state.toolCalls,
			state.plan,
		)
		// Resumed segments begin with durable actions from the previous segment.
		// Keep that history and only upsert new action updates.
		actions := projectAssistantActionFeedFromMetadata(message.Metadata[projectMessageMetadataAssistantActionFeed])
		for _, action := range projectAssistantActionFeedUpdatesFromToolCalls(state.toolCalls) {
			actions = applyProjectAssistantActionFeedUpdate(actions, action)
		}
		if len(actions) > 0 {
			metadata[projectMessageMetadataAssistantActionFeed] = actions
		}
		if preview, _ := message.Metadata[projectAssistantMetadataPreviewRefreshNeeded].(bool); preview {
			metadata[projectAssistantMetadataPreviewRefreshNeeded] = true
		}
		if _, ok := metadata[projectAssistantMetadataPlan]; !ok {
			if plan, ok := projectAssistantPlanSnapshotFromMetadata(message.Metadata[projectAssistantMetadataPlan]); ok {
				metadata[projectAssistantMetadataPlan] = *plan
			}
		}
		if state.initialBuild {
			metadata[projectAssistantMetadataInitialBuild] = true
		} else if initialBuild, _ := message.Metadata[projectAssistantMetadataInitialBuild].(bool); initialBuild {
			metadata[projectAssistantMetadataInitialBuild] = true
		}
		if progress := state.progressSnapshot(time.Now().UTC()); progress != nil {
			metadata[projectAssistantMetadataProgress] = *progress
		} else if progress, ok := projectAssistantProgressSnapshotFromMetadata(message.Metadata[projectAssistantMetadataProgress]); ok {
			metadata[projectAssistantMetadataProgress] = *progress
		}
		message.Metadata = metadata
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
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	supervisor := s.projectAssistantSupervisor()
	mode, err := request.collaborationMode()
	if err != nil {
		writeProjectError(w, err)
		return
	}
	initialBootstrap, err := s.consumeProjectInitialBootstrap(r.Context(), scope, id.user, request.Content, request.ClientRequestID)
	if err != nil {
		if errors.Is(err, store.ErrProjectBootstrapPermitConflict) {
			writeStatus(w, http.StatusConflict, "Conflict", "initial project bootstrap is already reserved")
			return
		}
		writeProjectError(w, err)
		return
	}
	if initialBootstrap {
		mode = projectAssistantCollaborationModeDefault
	}
	startWorker := func(created store.AssistantRun, assistant store.Message, start *projectAssistantStreamStart) error {
		return supervisor.Start(r.Context(), scope, created, assistant, func(ctx context.Context, accumulator *projectAssistantSnapshotAccumulator) {
			s.runProjectAssistantWorker(ctx, accumulator, r, id, c, project, created, start)
		})
	}
	started, err := s.startProjectAssistantRunDurablyWithMode(
		r.Context(),
		scope,
		id.user,
		request.Content,
		request.ClientRequestID,
		store.AssistantRunMode(mode),
		func(created store.AssistantRun, assistant store.Message, transcriptEmpty bool) error {
			var start *projectAssistantStreamStart
			if initialBootstrap && transcriptEmpty {
				plan := projectAssistantInitialCreationPlan(request.Content)
				start = &projectAssistantStreamStart{InitialApprovedPlan: cloneProjectAssistantApprovedPlan(&plan)}
			}
			return startWorker(created, assistant, start)
		},
	)
	if err != nil {
		if errors.Is(err, store.ErrAssistantRunConflict) {
			if _, latestErr := s.store.LatestAssistantRun(r.Context(), scope); latestErr == nil {
				s.writeProjectAssistantRunConflict(w, r.Context(), scope, id.user)
			} else {
				writeStatus(w, http.StatusConflict, "Conflict", "assistant run start is already in progress")
			}
			return
		}
		writeProjectError(w, err)
		return
	}
	s.writeProjectAssistantRunStart(w, http.StatusAccepted, scope, started.Run)
}

func (s *Server) runProjectAssistantWorker(ctx context.Context, accumulator *projectAssistantSnapshotAccumulator, request *http.Request, id identity, c *asclient.Client, project *aiv1alpha1.Project, run store.AssistantRun, start *projectAssistantStreamStart) {
	content := &strings.Builder{}
	workSegmentStarted := time.Now().UTC()
	state := &projectAssistantDurableMetadataState{
		status: "Working",
		initialBuild: start != nil &&
			start.InitialApprovedPlan != nil &&
			strings.TrimSpace(start.InitialApprovedPlan.Goal) != "",
		workSegmentStarted: workSegmentStarted,
	}
	workspaceScope := projectWorkspaceScope(id, project)
	persistMetadata := func(ctx context.Context, runStatus *store.AssistantRunStatus) error {
		return s.persistProjectAssistantDurableMetadata(ctx, accumulator, workspaceScope, state, runStatus)
	}
	persistWorkItemTerminal := func(ctx context.Context, runStatus store.AssistantRunStatus, itemStatus store.AssistantWorkItemStatus, reason string) error {
		return accumulator.TransitionWorkItemTerminal(ctx, runStatus, itemStatus, reason, func(committed *store.AssistantRun, message *store.Message) {
			state.provisional = false
			initialBuild := state.initialBuild
			if persisted, _ := message.Metadata[projectAssistantMetadataInitialBuild].(bool); persisted {
				initialBuild = true
			}
			message.Metadata = projectAssistantDurableMetadataForTransition(
				*committed,
				projectAssistantRunDisplayStatus(runStatus, state.status),
				false,
				s.projectAssistantPreviewRefreshNeeded(ctx, workspaceScope, "", false, state.toolCalls),
				state.toolCalls,
				state.plan,
			)
			if initialBuild {
				message.Metadata[projectAssistantMetadataInitialBuild] = true
			}
			if progress := state.progressSnapshot(time.Now().UTC()); progress != nil {
				message.Metadata[projectAssistantMetadataProgress] = *progress
			}
		})
	}
	hasDurableWorkItem := func() bool {
		if committed, ok := accumulator.CommittedRun(); ok {
			return strings.TrimSpace(committed.WorkItemID) != ""
		}
		return strings.TrimSpace(run.WorkItemID) != ""
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
	var callbackMu sync.Mutex
	callbacksClosed := false
	req := request.Clone(context.WithValue(ctx, projectAssistantSupervisorRunContextKey{}, run))
	result, err := s.generateProjectAssistantResultWithStart(req, id, c, project, projectAssistantStreamCallbacks{
		OnChunk: func(chunk string) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if callbacksClosed {
				return
			}
			recordSnapshotErr(accumulator.UpdateText(ctx, appendProjectAssistantStreamBlock(content, chunk), false))
		},
		OnProgress: func(message string) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if callbacksClosed {
				return
			}
			if state.appendProgress(message) {
				recordSnapshotErr(persistMetadata(ctx, nil))
			}
		},
		OnProvisionalText: func(_ string) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if callbacksClosed {
				return
			}
			state.provisional = true
			recordSnapshotErr(persistMetadata(ctx, nil))
		},
		OnProvisionalReset: func() {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if callbacksClosed {
				return
			}
			state.provisional = false
			recordSnapshotErr(persistMetadata(ctx, nil))
		},
		OnStatus: func(nextStatus string) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if callbacksClosed {
				return
			}
			state.status = nextStatus
			recordSnapshotErr(persistMetadata(ctx, nil))
		},
		OnPlan: func(plan projectAssistantPlanSnapshot) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if callbacksClosed {
				return
			}
			state.plan = &plan
			recordSnapshotErr(persistMetadata(ctx, nil))
		},
		OnToolCall: func(event projectToolCallStreamEvent) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if callbacksClosed {
				return
			}
			state.upsertToolCall(event)
			recordSnapshotErr(persistMetadata(ctx, nil))
		},
		OnAssistantEvent: func(event projectAssistantEvent) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if callbacksClosed {
				return
			}
			if event.Permission != nil && event.Permission.ToolCallID != "" {
				state.upsertToolCall(projectToolCallStreamEvent{ID: event.Permission.ToolCallID, Name: event.Permission.ToolName, Status: "permission_required", Summary: event.Permission.Reason, Permission: event.Permission})
			}
			if event.FollowUp != nil && event.FollowUp.ToolCallID != "" {
				state.upsertToolCall(projectToolCallStreamEvent{ID: event.FollowUp.ToolCallID, Name: projectToolAskFollowUp, Status: "input_required", Summary: event.FollowUp.Prompt, FollowUp: event.FollowUp})
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
	}, start)
	callbackMu.Lock()
	callbacksClosed = true
	contentText := content.String()
	callbackMu.Unlock()
	state.initialBuild = state.initialBuild || result.InitialBuild
	reply := result.Content
	// Current v2 follows the Codex-style Default loop and does not require the
	// retired internal initial-build plan artifact. Explicit Plan mode remains
	// a separate read-only turn; source/verification/commit facts are the v2
	// completion boundary.
	initialBuildCompletionEnforced := state.initialBuild && !projectAssistantRunUsesV2(&run)
	completionSuspensionReason := projectAssistantCompletionSuspensionReason(result, initialBuildCompletionEnforced)
	engineCompleted := err == nil && completionSuspensionReason == ""
	finalContent := s.projectAssistantRunTerminalContent(
		ctx,
		projectMessageScope(id.orgUUID, id.workspaceUUID, project),
		run,
		reply,
		contentText,
		err,
		result.CompletionEvidence,
		engineCompleted,
	)
	recordSnapshotErr(accumulator.UpdateText(ctx, finalContent, true))
	if getSnapshotErr() != nil {
		return
	}
	// A durable Stop wins even if the engine concurrently returns success or
	// an interrupt. Do this before interpreting the engine result so a stopped
	// run cannot become completed or pending again.
	if ctx.Err() != nil {
		state.status = "Aborted"
		runStatus := store.AssistantRunStatusAborted
		var transitionErr error
		if hasDurableWorkItem() {
			transitionErr = persistWorkItemTerminal(context.Background(), runStatus, store.AssistantWorkItemStatusSuspended, "aborted")
		} else {
			_, transitionErr = accumulator.supervisor.AbortWith(projectMessageScope(id.orgUUID, id.workspaceUUID, project), run.ID, nil)
		}
		recordSnapshotErr(transitionErr)
		if transitionErr == nil {
			if committed, ok := accumulator.CommittedRun(); ok {
				accumulator.supervisor.log("aborted", projectMessageScope(id.orgUUID, id.workspaceUUID, project), committed)
			}
		}
		return
	}
	if reason := completionSuspensionReason; reason != "" &&
		(err == nil || projectEinoAssistantBoundedExit(err)) {
		state.status = "Suspended"
		runStatus := store.AssistantRunStatusInterrupted
		if hasDurableWorkItem() {
			recordSnapshotErr(persistWorkItemTerminal(ctx, runStatus, store.AssistantWorkItemStatusSuspended, reason))
		} else {
			recordSnapshotErr(persistMetadata(ctx, &runStatus))
		}
		return
	}
	if err == nil {
		if projectAssistantTerminalPlanCompleted(result.CompletionEvidence, state.toolCalls) {
			state.plan = projectAssistantCompletedPlanSnapshot(state.plan)
		}
		state.status = "Completed"
		runStatus := store.AssistantRunStatusCompleted
		var transitionErr error
		if hasDurableWorkItem() {
			transitionErr = persistWorkItemTerminal(ctx, runStatus, store.AssistantWorkItemStatusCompleted, "completed")
		} else {
			transitionErr = persistMetadata(ctx, &runStatus)
		}
		recordSnapshotErr(transitionErr)
		if transitionErr == nil {
			if committed, ok := accumulator.CommittedRun(); ok {
				accumulator.supervisor.log("completed", projectMessageScope(id.orgUUID, id.workspaceUUID, project), committed)
			}
		}
		return
	}
	if errors.Is(err, context.Canceled) {
		state.status = "Aborted"
		runStatus := store.AssistantRunStatusAborted
		var transitionErr error
		if hasDurableWorkItem() {
			transitionErr = persistWorkItemTerminal(context.Background(), runStatus, store.AssistantWorkItemStatusSuspended, "aborted")
		} else {
			transitionErr = persistMetadata(context.Background(), &runStatus)
		}
		recordSnapshotErr(transitionErr)
		if transitionErr == nil {
			if committed, ok := accumulator.CommittedRun(); ok {
				accumulator.supervisor.log("aborted", projectMessageScope(id.orgUUID, id.workspaceUUID, project), committed)
			}
		}
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
	state.status = "Failed"
	runStatus := store.AssistantRunStatusFailed
	var transitionErr error
	if hasDurableWorkItem() {
		transitionErr = persistWorkItemTerminal(
			context.Background(),
			runStatus,
			store.AssistantWorkItemStatusSuspended,
			projectAssistantWorkItemFailureReason(err),
		)
	} else {
		transitionErr = persistMetadata(context.Background(), &runStatus)
	}
	recordSnapshotErr(transitionErr)
	if transitionErr == nil {
		if committed, ok := accumulator.CommittedRun(); ok {
			accumulator.supervisor.log("failed", projectMessageScope(id.orgUUID, id.workspaceUUID, project), committed)
		}
	}
}

func projectAssistantCompletionSuspensionReason(result projectAssistantRunResult, initialBuild bool) string {
	evidence := result.CompletionEvidence
	mutating := evidence.SourceMutationRevision > 0
	planRequired := initialBuild || evidence.PlanDefined || result.InitialPlan != nil
	if !mutating && !planRequired {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(evidence.VerificationOutcome), "provisioning") {
		return "runtime provisioning"
	}
	if (planRequired && !evidence.PlanComplete) ||
		(mutating && (!evidence.LatestMutationVerified ||
			evidence.VerifiedMutationRevision != evidence.SourceMutationRevision)) ||
		(mutating && evidence.CommitRequired && !evidence.LatestMutationCommitted) ||
		strings.TrimSpace(evidence.VerificationOutcome) != "ready" {
		return "objective incomplete"
	}
	return ""
}

func projectAssistantVerifiedMutationCompleted(evidence projectAssistantCompletionEvidence) bool {
	return evidence.SourceMutationRevision > 0 &&
		evidence.LatestMutationVerified &&
		evidence.VerifiedMutationRevision == evidence.SourceMutationRevision &&
		strings.TrimSpace(evidence.VerificationOutcome) == "ready"
}

func projectAssistantTerminalPlanCompleted(
	evidence projectAssistantCompletionEvidence,
	toolCalls []projectToolCallStreamEvent,
) bool {
	if !projectAssistantVerifiedMutationCompleted(evidence) {
		return false
	}
	if evidence.PlanDefined {
		return evidence.PlanComplete
	}
	for _, call := range toolCalls {
		if projectEinoAssistantCommitTool(call.Name) &&
			strings.TrimSpace(call.Status) == "succeeded" {
			return true
		}
	}
	return false
}

func projectAssistantCompletedPlanSnapshot(
	plan *projectAssistantPlanSnapshot,
) *projectAssistantPlanSnapshot {
	if plan == nil {
		return nil
	}
	completed := cloneProjectAssistantPlanSnapshot(*plan)
	for index := range completed.Steps {
		completed.Steps[index].Status = "completed"
	}
	return &completed
}

func projectAssistantMutationTerminalContent(
	evidence projectAssistantCompletionEvidence,
	engineCompleted bool,
) string {
	runtimeVerified := evidence.SourceMutationRevision > 0 &&
		evidence.LatestMutationVerified &&
		evidence.VerifiedMutationRevision == evidence.SourceMutationRevision &&
		strings.TrimSpace(evidence.VerificationOutcome) == "ready"
	complete := engineCompleted && runtimeVerified && (!evidence.PlanDefined || evidence.PlanComplete)
	status := "Incomplete"
	if complete {
		status = "Complete"
	}
	lines := []string{
		"Status: " + status,
		"",
	}
	switch {
	case complete:
		lines = append(lines,
			"The latest app changes are synchronized and the development preview is operationally reachable.",
			"",
			"Operational checks:",
			"- The current workspace passed operational verification.",
		)
	case runtimeVerified:
		lines = append(lines,
			"The development preview is operationally reachable, but the requested project work is not finished yet.",
			"",
			"Operational checks:",
			"- The current workspace passed operational verification.",
		)
	case strings.EqualFold(strings.TrimSpace(evidence.VerificationOutcome), "provisioning"):
		lines = append(lines,
			"The latest app changes are saved, and the development environment is still starting.",
			"",
			"What is ready:",
			"- Your changes are preserved in the project workspace.",
		)
	default:
		lines = append(lines,
			"The latest app changes are saved, but I could not finish operational verification yet.",
			"",
			"What is ready:",
			"- Your changes are preserved in the project workspace.",
		)
	}
	if summary := strings.TrimSpace(evidence.VerificationSummary); summary != "" {
		lines = append(lines, "", "What I found:", "- "+strings.ReplaceAll(summary, "\n", " "))
	}
	if len(evidence.Blockers) > 0 {
		lines = append(lines, "", "What is blocking completion:")
		for _, blocker := range evidence.Blockers {
			if blocker = strings.TrimSpace(blocker); blocker != "" {
				lines = append(lines, "- "+strings.ReplaceAll(blocker, "\n", " "))
			}
		}
	}
	if evidence.PlanDefined && !evidence.PlanComplete {
		lines = append(lines, "", "Still to do:", "- Finish the remaining project steps.")
	}
	if !complete {
		next := "Continue from the saved workspace and verify the remaining work."
		switch strings.ToLower(strings.TrimSpace(evidence.VerificationOutcome)) {
		case "provisioning":
			next = "Wait for the development environment to finish starting, then verify again."
		case "not_ready":
			if len(evidence.Blockers) > 0 {
				next = "Resolve the issue above, then run operational verification again."
			}
		}
		lines = append(lines, "", "Next:", "- "+next)
	}
	return projectAssistantContentWithVerificationScope(strings.Join(lines, "\n"), evidence)
}

func projectAssistantWorkItemFailureReason(err error) string {
	if projectEinoAssistantNoProgressExceeded(err) {
		return "no_progress"
	}
	return "failed"
}

func projectAssistantTerminalFailureContent(err error) string {
	if projectEinoAssistantNoProgressExceeded(err) {
		return "I stopped because this run did not make implementation progress within the current phase. Your project changes are preserved. Send another message with what you want me to do next."
	}
	if projectEinoAssistantMaxIterationsExceeded(err) {
		return "I stopped after reaching the bounded action limit. Your project changes are preserved. Send another message with what you want me to do next."
	}
	return "The assistant run stopped before it could finish. Your project changes are preserved. Send another message with what you want me to do next."
}

func projectAssistantContentWithTerminalFailure(content string, err error) string {
	content = strings.TrimSpace(content)
	failure := projectAssistantTerminalFailureContent(err)
	if content == "" {
		return failure
	}
	return content + "\n\n" + failure
}

func projectAssistantShouldPersistTerminalFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var permissionErr *projectAssistantPermissionRequiredError
	if errors.As(err, &permissionErr) {
		return false
	}
	var inputErr *projectAssistantInputRequiredError
	return !errors.As(err, &inputErr)
}

func (s *Server) writeProjectAssistantRunStart(w http.ResponseWriter, status int, scope store.Scope, run store.AssistantRun) {
	message, err := s.findProjectMessage(context.Background(), scope, run.ActiveMessageID)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	response := projectAssistantRunStartResponse{Run: projectAssistantRunToAPI(run), Assistant: projectMessageToAPI(message)}
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

func (s *Server) writeProjectAssistantRunConflict(w http.ResponseWriter, ctx context.Context, scope store.Scope, actor string) {
	run, err := s.store.LatestAssistantRun(ctx, scope)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	if s.authorizeProjectAssistantRunActor(ctx, scope, run, actor, false) != nil {
		writeStatus(w, http.StatusConflict, "Conflict", "another assistant run is active")
		return
	}
	writeJSON(w, http.StatusConflict, projectAssistantRunSnapshotResponse{Run: projectAssistantRunToAPI(run)})
}

func (s *Server) latestProjectAssistantRun(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
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
	if s.authorizeProjectAssistantRunActor(r.Context(), scope, run, id.user, false) != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	message, err := s.findProjectMessage(r.Context(), scope, run.ActiveMessageID)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectAssistantRunSnapshotToAPI(projectAssistantRunSnapshot{Run: run, Message: message}))
}

func (s *Server) streamProjectAssistantSnapshots(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	runID := mux.Vars(r)["run"]
	if err := s.reconcileOrphanedProjectAssistantRun(r.Context(), scope); err != nil {
		writeProjectError(w, err)
		return
	}
	run, err := s.store.GetAssistantRun(r.Context(), scope, runID)
	if err != nil || s.authorizeProjectAssistantRunActor(r.Context(), scope, run, id.user, false) != nil {
		writeStatus(w, http.StatusNotFound, "NotFound", "assistant run not found")
		return
	}
	after := projectAssistantAfterRevision(r)
	updates, unsubscribe, err := s.projectAssistantSupervisor().Subscribe(scope, runID, after)
	if errors.Is(err, store.ErrAssistantRunNotFound) {
		message, loadErr := s.findProjectMessage(r.Context(), scope, run.ActiveMessageID)
		if loadErr != nil {
			writeProjectError(w, loadErr)
			return
		}
		flusher, streamOK := startProjectAssistantSnapshotStream(w)
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
	flusher, streamOK := startProjectAssistantSnapshotStream(w)
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

func startProjectAssistantSnapshotStream(w http.ResponseWriter) (http.Flusher, bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeStatus(w, http.StatusInternalServerError, "InternalError", "streaming unsupported")
		return nil, false
	}
	return flusher, true
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
	data, err := json.Marshal(projectAssistantRunSnapshotToAPI(snapshot))
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
	if assistantRunTerminal(run.Status) {
		return nil
	}
	legacy := !projectAssistantRunUsesV2(&run)
	if !legacy && run.Status != store.AssistantRunStatusRunning && run.Status != store.AssistantRunStatusStopping {
		// Current v2 permission/input checkpoints remain intentionally resumable.
		return nil
	}
	key := projectAssistantRunKey{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID, ProjectName: scope.ProjectName, ProjectUID: scope.ProjectUID}
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
	if legacy {
		projectAssistantClearPendingInterruptMetadata(&message, run.ID)
	}
	if run.WorkItemID != "" {
		item, itemErr := s.store.GetAssistantWorkItem(ctx, scope, run.WorkItemID)
		if itemErr != nil {
			return itemErr
		}
		reason := "provider restarted"
		if legacy {
			reason = "retired assistant engine"
		}
		if err := s.store.TransitionWorkItemAndRun(ctx, scope, item.ID, item.Revision, run, store.AssistantWorkItemStatusSuspended, reason, run.UpdatedAt); err != nil {
			return err
		}
		if err := s.store.AppendMessage(ctx, scope, message); err != nil {
			return err
		}
	} else if err := s.store.SaveAssistantRunSnapshot(ctx, scope, run, []store.Message{message}, run.Revision-1); err != nil {
		return err
	}
	s.projectAssistantSupervisor().log("orphan_interrupted", scope, run)
	return nil
}
