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
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/faroshq/provider-app-studio/workspace"
)

const projectEinoAssistantProgressCorrectionPrefix = "[App Studio progress correction] "

// projectEinoAssistantLifecycle observes the two facts App Studio must enforce
// around source effects: a successful source mutation invalidates prior
// verification, and commit requires successful verification of the latest
// mutation. It deliberately does not infer phases or rewrite the model's tool
// catalog; Eino remains the owner of the conversational execution loop.
type projectEinoAssistantLifecycle struct {
	*adk.BaseChatModelAgentMiddleware

	runState       *projectEinoAssistantRunState
	initialBuild   bool
	v2             bool
	repositoryRef  string
	workspace      *workspace.FileStore
	workspaceScope workspace.Scope
	repositoryView func(context.Context) (*ProjectRepositoryView, error)
}

func projectEinoAssistantLifecycleMiddleware(
	req projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
) adk.ChatModelAgentMiddleware {
	lifecycle := &projectEinoAssistantLifecycle{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		runState:                     runState,
		initialBuild:                 projectAssistantInitialBuildActive(req, runState),
		v2:                           projectAssistantRequestUsesV2(req),
		repositoryRef:                projectEinoAssistantProjectRepositoryRef(req),
		workspace:                    req.Workspace,
		workspaceScope:               req.WorkspaceScope,
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
	_ *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if !m.v2 || m.runState == nil {
		return ctx, state, nil
	}
	m.refreshRepositoryState(ctx)
	m.runState.NextModelCallOrdinal()
	if state == nil {
		return ctx, state, nil
	}
	toolName, repeated := m.runState.RepeatedCompletedAction()
	_, stalled := m.runState.ConsecutiveNoProgressModelCalls()
	if repeated < projectEinoAssistantRepeatedActionWarnAt && stalled < projectEinoAssistantRepeatedActionWarnAt {
		return ctx, state, nil
	}
	messages := state.Messages[:0]
	for _, message := range state.Messages {
		if message != nil && message.Role == schema.System &&
			strings.HasPrefix(message.Content, projectEinoAssistantProgressCorrectionPrefix) {
			continue
		}
		messages = append(messages, message)
	}
	instruction := "The last model turn produced no new evidence or action progress. Do not repeat a tool call whose result is already in context. Inspect different evidence, take the next authorized action, or stop and report the exact limitation."
	if toolName != "" && repeated >= projectEinoAssistantRepeatedActionWarnAt {
		instruction = "Do not call " + projectToolBaseName(toolName) + " again with the same arguments; its result is already in context. Inspect different evidence, take the next authorized action, or stop and report the exact limitation."
	}
	state.Messages = append(messages, schema.SystemMessage(projectEinoAssistantProgressCorrectionPrefix+instruction))
	return ctx, state, nil
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
	// Readiness is monotonic for this run: a transient later read failure must
	// never waive a commit that became required while the assistant was active.
	if projectEinoAssistantRepositoryCommitReady(repository) {
		m.runState.ConfigureCommitRequirement(true)
	}
}

func (m *projectEinoAssistantLifecycle) WrapModel(
	_ context.Context,
	base einomodel.BaseChatModel,
	modelCtx *adk.ModelContext,
) (einomodel.BaseChatModel, error) {
	if base == nil || !m.v2 || m.runState == nil {
		return base, nil
	}
	if m.runState.NeedsCompletionVerification() {
		if name := projectEinoAssistantLifecycleToolName(modelCtx, projectToolVerifyDevelopmentRuntime); name != "" {
			return &projectEinoAssistantCompletionBarrierModel{
				BaseChatModel:        base,
				verificationToolName: name,
				toolArguments:        `{}`,
				runState:             m.runState,
			}, nil
		}
		return base, nil
	}
	evidence := m.runState.CompletionEvidence()
	if evidence.SourceMutationRevision > 0 && !evidence.LatestMutationVerified && m.runState.ClaimRepairOpportunity() {
		// A current-revision not-ready result requires another diagnostic or
		// repair action before prose can terminate the run.
		return &projectEinoAssistantForcedToolModel{BaseChatModel: base}, nil
	}
	if m.runState.ShouldRequestSourceCommit() {
		if name := projectEinoAssistantLifecycleToolName(modelCtx, projectToolCommitProjectFiles); name != "" {
			arguments := projectEinoToolArgumentsString(map[string]any{
				"repositoryRef": m.repositoryRef,
				"paths":         m.runState.SuccessfulMutationPaths(),
				"message":       "Apply App Studio changes",
			})
			return &projectEinoAssistantCompletionBarrierModel{
				BaseChatModel:        base,
				verificationToolName: name,
				toolArguments:        arguments,
				runState:             m.runState,
			}, nil
		}
	}
	return base, nil
}

func projectEinoAssistantLifecycleToolName(modelCtx *adk.ModelContext, baseName string) string {
	if modelCtx == nil {
		return ""
	}
	for _, tool := range modelCtx.Tools {
		if tool != nil && projectToolBaseName(tool.Name) == baseName {
			return strings.TrimSpace(tool.Name)
		}
	}
	return ""
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
		if projectEinoAssistantCommitTool(name) &&
			(m.runState == nil || !m.runState.SourceMutationVerified()) {
			return "Tool call failed: verify_development_runtime must successfully verify the latest workspace mutation before commit approval can be requested.", nil
		}

		result, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			if name == projectToolVerifyDevelopmentRuntime && m.runState != nil {
				m.runState.RecordDevelopmentVerification(false)
			}
			if m.v2 && m.runState != nil && !projectEinoAssistantFilesystemReadTool(name) {
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
		case !m.v2 && projectEinoAssistantWorkspaceMutationTool(name) &&
			projectEinoAssistantSuccessfulWorkspaceMutationResult(name, result):
			if m.runState != nil {
				m.runState.RecordSourceMutation()
			}
		case name == projectToolVerifyDevelopmentRuntime:
			if m.runState != nil {
				m.runState.RecordDevelopmentVerificationResult(result)
				if m.v2 && m.runState.SourceMutationVerified() {
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
			projectEinoAssistantPhaseSuccessfulToolContent(result) &&
			projectToolCallResultStatus(name, result) == "succeeded":
			if m.runState != nil {
				m.runState.RecordSourceCommit()
				m.runState.CompleteExecutionPlan()
				if m.v2 && m.workspace != nil {
					// The external commit already succeeded. Clearing local pending
					// state is best-effort here: returning an error would encourage a
					// duplicate repository commit after a completed side effect. The
					// caller may be cancelled immediately after that side effect, so
					// give this server-owned bookkeeping a bounded detached context.
					clearCtx, cancelClear := detachedProjectPersistenceContext(ctx)
					_ = m.workspace.ClearUncommittedPaths(clearCtx, m.workspaceScope)
					cancelClear()
				}
			}
		}
		if m.v2 && m.runState != nil && !projectEinoAssistantFilesystemReadTool(name) &&
			!projectEinoAssistantPendingPermissionResult(result) {
			successful := projectEinoAssistantPhaseSuccessfulToolContent(result)
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

func projectEinoAssistantVerificationReady(content string) bool {
	return projectEinoAssistantPhaseVerificationReady(content)
}
