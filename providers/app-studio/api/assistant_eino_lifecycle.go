/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package api

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/faroshq/provider-app-studio/workspace"
)

// projectEinoAssistantLifecycle records durable effects without adding hidden
// verification or commit obligations to Eino's conversational loop.
type projectEinoAssistantLifecycle struct {
	*adk.BaseChatModelAgentMiddleware

	runState         *projectEinoAssistantRunState
	server           *Server
	req              projectAssistantRunRequest
	initialBuild     bool
	repositoryRef    string
	workspace        *workspace.FileStore
	workspaceScope   workspace.Scope
	repositoryView   func(context.Context) (*ProjectRepositoryView, error)
	auditRecorder    *projectAssistantRunAuditRecorder
	steering         <-chan projectAssistantSteeringInput
	activateSteering func(context.Context, []projectAssistantSteeringInput) error
	managedToolNames map[string]struct{}
}

func projectEinoAssistantLifecycleMiddleware(
	req projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
	servers ...*Server,
) adk.ChatModelAgentMiddleware {
	lifecycle := &projectEinoAssistantLifecycle{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		runState:                     runState,
		req:                          req,
		initialBuild:                 projectAssistantInitialBuildActive(req, runState),
		repositoryRef:                projectEinoAssistantProjectRepositoryRef(req),
		workspace:                    req.Workspace,
		workspaceScope:               req.WorkspaceScope,
		auditRecorder:                req.auditRecorder,
		steering:                     req.Steering,
		activateSteering:             req.ActivateSteering,
	}
	if len(servers) > 0 {
		lifecycle.server = servers[0]
	}
	if req.Client != nil && req.Project != nil {
		lifecycle.repositoryView = func(ctx context.Context) (*ProjectRepositoryView, error) {
			runCtx, err := refreshProjectAssistantWorkflowRunContext(ctx, projectAssistantWorkflowRunContext{
				Client:     req.Client,
				Project:    req.Project,
				Repository: req.Repository,
			})
			if err != nil {
				return nil, err
			}
			return runCtx.Repository, nil
		}
	}
	return lifecycle
}

func projectEinoAssistantRepositoryCommitReady(repository *ProjectRepositoryView) bool {
	return repository != nil && strings.TrimSpace(repository.Ref) != "" &&
		(repository.Ready || repository.Status == projectRepositoryStatusReady)
}

func (m *projectEinoAssistantLifecycle) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	modelCtx *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if m.runState == nil {
		return ctx, state, nil
	}
	if state == nil {
		return ctx, state, nil
	}
	if err := m.refreshLiveRequestContext(ctx); err != nil {
		return ctx, state, err
	}
	if err := m.refreshExecutableToolContext(ctx, state, modelCtx); err != nil {
		return ctx, state, err
	}
	if !m.runState.TakeSteeringDeferral() {
		if _, err := projectEinoAssistantDrainSteeringAtBoundary(ctx, m.steering, m.runState, state, m.activateSteering); err != nil {
			return ctx, state, err
		}
	}
	budget := m.runState.RolloutBudget()
	if err := budget.ExhaustionError(); err != nil {
		return ctx, state, err
	}
	var rolloutBudgetRemaining *int64
	if reminder := budget.PendingReminder(); reminder != nil {
		state.Messages = append(state.Messages, projectEinoAssistantRolloutBudgetMessage(reminder))
		remaining := reminder.RemainingTokens
		rolloutBudgetRemaining = &remaining
		if err := budget.DeliverReminder(ctx, reminder); err != nil {
			return ctx, state, err
		}
	}
	if err := m.rewriteLiveContext(ctx, state); err != nil {
		return ctx, state, err
	}
	ordinal := m.runState.NextModelCallOrdinal()
	if m.auditRecorder != nil {
		sourceRevision, verifiedRevision := m.runState.SourceMutationRevisions()
		if err := m.auditRecorder.recordModelCall(
			ctx,
			ordinal,
			sourceRevision,
			verifiedRevision,
			rolloutBudgetRemaining,
			state.ToolInfos,
			nil,
		); err != nil {
			return ctx, state, err
		}
	}
	return ctx, state, nil
}

func (m *projectEinoAssistantLifecycle) refreshExecutableToolContext(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	modelCtx *adk.ModelContext,
) error {
	if m == nil || m.server == nil || m.runState == nil || state == nil {
		return nil
	}
	discovery, ok := m.runState.ToolDiscovery()
	if !ok {
		return nil
	}
	tools, err := projectEinoAssistantToolsForDiscovery(ctx, m.server, m.req, m.runState, discovery)
	if err != nil {
		return err
	}
	infos := make([]*schema.ToolInfo, 0, len(tools))
	currentNames := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		info, infoErr := tool.Info(ctx)
		if infoErr != nil {
			return infoErr
		}
		if info == nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		name := projectAssistantToolKey(info.Name)
		if _, exists := currentNames[name]; exists {
			continue
		}
		currentNames[name] = struct{}{}
		infos = append(infos, info)
	}
	if m.managedToolNames == nil {
		m.managedToolNames = map[string]struct{}{}
	}
	frameworkInfos := make([]*schema.ToolInfo, 0, len(state.ToolInfos))
	for _, info := range state.ToolInfos {
		if info == nil {
			continue
		}
		name := projectAssistantToolKey(info.Name)
		if _, managed := m.managedToolNames[name]; managed {
			continue
		}
		if _, current := currentNames[name]; current {
			continue
		}
		frameworkInfos = append(frameworkInfos, info)
	}
	for name := range currentNames {
		m.managedToolNames[name] = struct{}{}
	}
	state.ToolInfos = append(frameworkInfos, infos...)
	state.DeferredToolInfos = nil
	if modelCtx != nil {
		modelCtx.Tools = state.ToolInfos
	}
	return nil
}

func (m *projectEinoAssistantLifecycle) refreshLiveRequestContext(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.req.Client != nil && m.req.Project != nil && strings.TrimSpace(m.req.Project.Name) != "" {
		current, err := refreshProjectAssistantWorkflowRunContext(ctx, projectAssistantWorkflowRunContext{
			Client:     m.req.Client,
			Project:    m.req.Project,
			Repository: m.req.Repository,
		})
		// This is a fresh-view attempt, not a new availability dependency: a
		// transient project read must not discard the request view that was
		// already authorized when the run began.
		if err == nil {
			m.req.Project = current.Project
			m.req.Repository = current.Repository
		}
	}
	m.refreshRepositoryState(ctx)
	// newAgent resolves the first request's executable tool set immediately
	// before this hook runs. Reuse that just-captured view for its first sample;
	// every later sample refreshes discovery before rebuilding prompt context.
	if m.server != nil && m.runState.CurrentModelCallOrdinal() > 0 {
		m.runState.SetToolDiscovery(projectEinoAssistantDiscoverTools(ctx, m.server, m.req))
	}
	return nil
}

func (m *projectEinoAssistantLifecycle) rewriteLiveContext(ctx context.Context, state *adk.ChatModelAgentState) error {
	if m == nil || state == nil || m.req.Project == nil {
		return nil
	}
	contextMessages := []chatMessage{{
		Role:    "system",
		Content: projectEinoAssistantLiveContextPrefix + projectSystemPromptForMode(m.req.Project, m.req.Repository, m.req.CollaborationMode, projectAssistantInitialBuildActive(m.req, m.runState)),
	}}
	if snapshot, ok := projectEinoAssistantSessionContextMessage(ctx, m.req, m.runState); ok {
		snapshot.Content = projectEinoAssistantLiveContextPrefix + snapshot.Content
		contextMessages = append(contextMessages, snapshot)
	}
	if prompt := m.runState.ToolPrompt(); prompt != "" {
		contextMessages = append(contextMessages, chatMessage{Role: "system", Content: projectEinoAssistantLiveContextPrefix + prompt})
	}
	live, err := projectChatMessagesToEino(contextMessages)
	if err != nil {
		return err
	}
	conversation := projectEinoMessagesToChat(state.Messages)
	conversation = projectEinoAssistantConversationPayload(conversation)
	conversationMessages, err := projectChatMessagesToEino(conversation)
	if err != nil {
		return err
	}
	state.Messages = append(live, conversationMessages...)
	return nil
}

func projectEinoAssistantDrainSteering(
	steering <-chan projectAssistantSteeringInput,
	runState *projectEinoAssistantRunState,
	state *adk.ChatModelAgentState,
) int {
	drained, _ := projectEinoAssistantDrainSteeringAtBoundary(context.Background(), steering, runState, state, nil)
	return drained
}

func projectEinoAssistantDrainSteeringAtBoundary(
	ctx context.Context,
	steering <-chan projectAssistantSteeringInput,
	runState *projectEinoAssistantRunState,
	state *adk.ChatModelAgentState,
	activate func(context.Context, []projectAssistantSteeringInput) error,
) (int, error) {
	if steering == nil || runState == nil {
		return 0, nil
	}
	inputs := make([]projectAssistantSteeringInput, 0, 1)
	for {
		select {
		case input, ok := <-steering:
			if !ok {
				return projectEinoAssistantActivateSteeringInputs(ctx, inputs, runState, state, activate)
			}
			content := strings.TrimSpace(input.Content)
			if content == "" {
				continue
			}
			input.Content = content
			inputs = append(inputs, input)
		default:
			return projectEinoAssistantActivateSteeringInputs(ctx, inputs, runState, state, activate)
		}
	}
}

func projectEinoAssistantActivateSteeringInputs(
	ctx context.Context,
	inputs []projectAssistantSteeringInput,
	runState *projectEinoAssistantRunState,
	state *adk.ChatModelAgentState,
	activate func(context.Context, []projectAssistantSteeringInput) error,
) (int, error) {
	if len(inputs) == 0 {
		return 0, nil
	}
	if activate != nil {
		if err := activate(ctx, inputs); err != nil {
			return 0, err
		}
	}
	for _, input := range inputs {
		runState.RecordSteeringInput(input.Content)
		if state != nil {
			state.Messages = append(state.Messages, schema.UserMessage(input.Content))
		}
	}
	return len(inputs), nil
}

func (m *projectEinoAssistantLifecycle) refreshRepositoryState(ctx context.Context) {
	if m == nil || m.runState == nil || m.repositoryView == nil {
		return
	}
	repository, err := m.repositoryView(ctx)
	if err != nil || repository == nil {
		return
	}
	if ref := strings.TrimSpace(repository.Ref); ref != "" {
		m.repositoryRef = ref
		m.runState.SetProjectRepositoryRef(ref)
	}
}

func (m *projectEinoAssistantLifecycle) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	toolCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	if toolCtx == nil {
		return endpoint, nil
	}
	name := projectToolBaseName(toolCtx.Name)
	return func(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
		commitRevision := uint64(0)
		if projectEinoAssistantCommitTool(name) && m.runState != nil {
			commitRevision, _ = m.runState.SourceMutationRevisions()
		}
		result, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			if name == projectToolVerifyDevelopmentRuntime && m.runState != nil {
				m.runState.RecordDevelopmentVerification(false)
			}
			if m.runState != nil && !projectEinoAssistantFilesystemReadTool(name) {
				if _, interrupted := compose.IsInterruptRerunError(err); !interrupted {
					if projectEinoAssistantCommitTool(name) {
						m.runState.RecordSourceCommitAttempt(commitRevision)
					}
					m.runState.RecordCompletedAction(name, projectEinoAssistantCanonicalActionArguments(argumentsInJSON), false)
				}
			}
			return result, err
		}
		switch {
		case name == projectToolVerifyDevelopmentRuntime:
			if m.runState != nil {
				m.runState.RecordDevelopmentVerificationResult(result)
				if m.runState.SourceMutationVerified() {
					digest, digestErr := projectEinoAssistantWorkspaceDigest(
						ctx,
						m.workspace,
						m.workspaceScope,
						m.runState.SuccessfulMutationPaths(),
					)
					if digestErr != nil {
						m.runState.RecordVerificationBindingFailure("operational verification could not be bound to current workspace content: " + digestErr.Error())
					} else {
						m.runState.RecordVerifiedWorkspaceDigest(digest)
					}
				}
				if m.initialBuild && m.runState.SourceMutationVerified() {
					m.runState.CompleteExecutionPlan()
				}
			}
		case projectEinoAssistantCommitTool(name) &&
			projectEinoAssistantSuccessfulToolContent(result) &&
			projectToolCallResultStatus(name, result) == "succeeded":
			if m.runState != nil {
				m.runState.RecordSourceCommit()
				m.runState.CompleteExecutionPlan()
				if m.workspace != nil {
					// The external commit already succeeded. Clearing local pending
					// state is best-effort here: returning an error would encourage a
					// duplicate repository commit after a completed side effect. The
					// caller may be cancelled immediately after that side effect, so
					// give this server-owned bookkeeping a bounded detached context.
					clearCtx, cancelClear := detachedProjectPersistenceContext(ctx)
					args, _ := projectEinoToolArguments(argumentsInJSON)
					_ = m.workspace.RemoveUncommittedPaths(clearCtx, m.workspaceScope, projectToolStringList(args["paths"]))
					cancelClear()
				}
			}
		}
		if m.runState != nil && !projectEinoAssistantFilesystemReadTool(name) &&
			!projectEinoAssistantPendingPermissionResult(result) {
			successful := projectEinoAssistantSuccessfulToolContent(result)
			if projectEinoAssistantCommitTool(name) {
				successful = successful && projectToolCallResultStatus(name, result) == "succeeded"
				m.runState.RecordSourceCommitAttempt(commitRevision)
			}
			m.runState.RecordCompletedAction(name, projectEinoAssistantCanonicalActionArguments(argumentsInJSON), successful)
		}
		return result, nil
	}, nil
}

func projectEinoAssistantCanonicalActionArguments(raw string) string {
	args, err := projectEinoToolArguments(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return projectEinoToolArgumentsString(args)
}

func projectEinoAssistantCommitTool(name string) bool {
	switch projectToolBaseName(name) {
	case projectToolCommitProjectFiles, projectToolCommitFiles:
		return true
	default:
		return false
	}
}

func projectEinoAssistantPendingPermissionResult(content string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(content)), "tool call skipped: waiting for approval")
}

func projectEinoAssistantVerificationReady(content string) bool {
	return projectEinoAssistantVerificationContentReady(content)
}
