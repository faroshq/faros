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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
)

const (
	projectEinoToolParametersExtraKey = "parametersJSON"
	projectEinoToolSearchableExtraKey = "appStudioSearchableMCP"
)

var errProjectAssistantInitialPlanPersistence = errors.New("persist initial project execution plan")

const projectAssistantMutationPatchMaxBytes = 16 << 10

type projectEinoAssistantToolDiscovery struct {
	IncludeCommitBridge bool
	MCPTools            []projectAssistantTool
	Prompt              string
}

type projectEinoAssistantTool struct {
	server        *Server
	tool          projectAssistantTool
	req           projectAssistantRunRequest
	runState      *projectEinoAssistantRunState
	searchableMCP bool
}

func newProjectEinoAssistantToolsFactory(server *Server) projectEinoAssistantToolsFactory {
	return func(ctx context.Context, req projectAssistantRunRequest, runState *projectEinoAssistantRunState) ([]einotool.BaseTool, error) {
		if server == nil {
			return nil, errors.New("server is not configured")
		}
		registry := server.projectAssistantToolRegistry()
		discovery := projectEinoAssistantEnsureToolDiscovery(ctx, server, req, runState)
		catalogPolicy := projectAssistantToolCatalogPolicy(req)
		localTools := projectAssistantToolsForCollaborationMode(registry.Tools(discovery.IncludeCommitBridge), req.CollaborationMode)
		mcpTools := projectAssistantToolsForCollaborationMode(discovery.MCPTools, req.CollaborationMode)
		out := make([]einotool.BaseTool, 0, len(localTools)+len(mcpTools)+1)
		if projectEinoAssistantProgressEnabled(req, runState) {
			out = append(out, newProjectEinoAssistantProgressTool(req, runState))
		}
		graphTools, err := newProjectAssistantGraphWorkflowTools(ctx, projectAssistantWorkflowRunContextForRequest(server, req, runState), catalogPolicy)
		if err != nil {
			return nil, err
		}
		out = append(out, graphTools...)
		for _, tool := range localTools {
			out = append(out, newProjectEinoAssistantServerTool(server, tool, req, runState))
		}
		for _, tool := range mcpTools {
			out = append(out, newProjectEinoAssistantSearchableMCPTool(server, tool, req, runState))
		}
		return out, nil
	}
}

func projectEinoAssistantEnsureToolDiscovery(ctx context.Context, server *Server, req projectAssistantRunRequest, runState *projectEinoAssistantRunState) projectEinoAssistantToolDiscovery {
	if discovery, ok := runState.ToolDiscovery(); ok {
		return discovery
	}
	discovery := projectEinoAssistantDiscoverTools(ctx, server, req)
	runState.SetToolDiscovery(discovery)
	return discovery
}

func projectEinoAssistantDiscoverTools(ctx context.Context, server *Server, req projectAssistantRunRequest) projectEinoAssistantToolDiscovery {
	if server == nil {
		return projectEinoAssistantToolDiscovery{}
	}
	registry := server.projectAssistantToolRegistry()
	policy := normalizeProjectAssistantTurnPolicy(req.TurnPolicy, req.TurnProfile)
	chatTools := projectAssistantChatToolsForSpecs(projectAssistantToolSpecsForTurnPolicy(projectAssistantAllToolSpecs(registry.Tools(false)), policy))
	if len(chatTools) == 0 {
		return projectEinoAssistantToolDiscovery{}
	}
	discovery := projectEinoAssistantToolDiscovery{
		Prompt: projectMCPToolsPrompt(chatTools),
	}
	if req.ToolPort == nil {
		return discovery
	}
	mcpTools, includeCommitBridge, err := req.ToolPort.DiscoverMCP(ctx, req.Identity, req.LLM)
	if err != nil {
		if projectAssistantTurnPolicyCanUseMCP(policy, req) {
			discovery.Prompt = projectMCPToolsFailurePrompt(err)
		}
		return discovery
	}
	discovery.IncludeCommitBridge = includeCommitBridge
	discovery.MCPTools = mcpTools
	allTools := append(registry.Tools(discovery.IncludeCommitBridge), discovery.MCPTools...)
	discovery.Prompt = projectMCPToolsPrompt(projectAssistantChatToolsForSpecs(projectAssistantToolSpecsForTurnPolicy(projectAssistantAllToolSpecs(allTools), policy)))
	return discovery
}

func projectAssistantToolsForCollaborationMode(tools []projectAssistantTool, mode projectAssistantCollaborationMode) []projectAssistantTool {
	out := make([]projectAssistantTool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		spec := tool.Spec()
		if mode == projectAssistantCollaborationModePlan && projectAssistantToolHasEffect(spec) {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func projectAssistantTurnPolicyCanUseMCP(policy projectAssistantTurnPolicy, _ projectAssistantRunRequest) bool {
	for _, name := range []string{
		projectToolInfrastructureListTemplates,
		projectToolInfrastructureDescribeTemplate,
		projectToolInfrastructureListInstances,
		projectToolInfrastructureGetInstance,
		projectToolInfrastructureProvision,
		projectToolDatabricksListTables,
		projectToolDatabricksDescribeTable,
	} {
		spec, ok := projectAssistantMCPToolSpec(projectMCPTool{Name: name})
		if ok && policy.AllowsTool(spec) {
			return true
		}
	}
	return false
}

func projectAssistantChatToolsForSpecs(specs []projectAssistantToolSpec) []chatTool {
	out := make([]chatTool, 0, len(specs))
	for _, spec := range specs {
		if strings.TrimSpace(spec.Name) == "" {
			continue
		}
		out = append(out, spec.chatTool())
	}
	return out
}

func newProjectEinoAssistantTool(tool projectAssistantTool, req projectAssistantRunRequest, runState *projectEinoAssistantRunState) einotool.BaseTool {
	return newProjectEinoAssistantServerTool(nil, tool, req, runState)
}

func newProjectEinoAssistantServerTool(server *Server, tool projectAssistantTool, req projectAssistantRunRequest, runState *projectEinoAssistantRunState) einotool.BaseTool {
	return projectEinoAssistantTool{
		server:   server,
		tool:     tool,
		req:      req,
		runState: runState,
	}
}

func newProjectEinoAssistantSearchableMCPTool(server *Server, tool projectAssistantTool, req projectAssistantRunRequest, runState *projectEinoAssistantRunState) einotool.BaseTool {
	return projectEinoAssistantTool{
		server:        server,
		tool:          tool,
		req:           req,
		runState:      runState,
		searchableMCP: true,
	}
}

func (t projectEinoAssistantTool) Info(context.Context) (*schema.ToolInfo, error) {
	if t.tool == nil {
		return nil, errors.New("project assistant tool is not configured")
	}
	spec := t.tool.Spec()
	info := &schema.ToolInfo{
		Name: strings.TrimSpace(spec.Name),
		Desc: strings.TrimSpace(spec.Description),
		Extra: map[string]any{
			"bundle":                          string(projectAssistantToolBundleForSpec(spec)),
			"risk":                            string(spec.Risk),
			projectEinoToolParametersExtraKey: string(spec.Parameters),
		},
	}
	if len(spec.Parameters) > 0 {
		var params jsonschema.Schema
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return nil, fmt.Errorf("decode tool %q JSON schema: %w", spec.Name, err)
		}
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&params)
	}
	if t.searchableMCP {
		info.Extra[projectEinoToolSearchableExtraKey] = true
	}
	return info, nil
}

func (t projectEinoAssistantTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	if cause := context.Cause(ctx); cause != nil {
		return "", cause
	}
	if t.tool == nil {
		return "", errors.New("project assistant tool is not configured")
	}
	spec := t.tool.Spec()
	callID := compose.GetToolCallID(ctx)
	if wasInterrupted, hasState, state := einotool.GetInterruptState[*projectEinoFollowUpInterruptState](ctx); wasInterrupted && hasState && state != nil {
		return t.resumeFollowUp(ctx, callID, spec, state)
	}
	if wasInterrupted, hasState, state := einotool.GetInterruptState[*projectEinoPermissionInterruptState](ctx); wasInterrupted && hasState && state != nil {
		return t.resumePermission(ctx, callID, spec, state)
	}
	args, err := projectEinoToolArguments(argumentsInJSON)
	if err != nil {
		return t.finishFailedToolCall(callID, spec.Name, argumentsInJSON, "invalid arguments: "+truncateProjectToolInfo(err.Error())), nil
	}
	if t.runState.PermissionBarrierActive() {
		return projectEinoPermissionBarrierToolResult(), nil
	}
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      spec.Name,
		Status:    "requested",
		Arguments: summarizeProjectToolArgumentsMap(spec.Name, args),
	})
	if projectEinoAssistantCommitTool(spec.Name) {
		args, err = t.v2CommitArguments(args)
		if err != nil {
			return t.finishFailedToolCall(callID, spec.Name, argumentsInJSON, err.Error()), nil
		}
		if err := t.validateV2CommitWorkspace(ctx, args); err != nil {
			return t.finishFailedToolCall(callID, spec.Name, argumentsInJSON, err.Error()), nil
		}
		argumentsInJSON = projectEinoToolArgumentsString(args)
	}
	if projectToolBaseName(spec.Name) == projectToolAskFollowUp {
		return t.requestFollowUp(ctx, callID, spec, args)
	}
	if t.req.CollaborationMode == projectAssistantCollaborationModePlan &&
		projectAssistantToolHasEffect(spec) {
		return t.finishFailedToolCall(callID, spec.Name, argumentsInJSON, "Plan mode is read-only; start a new Default turn to implement the plan"), nil
	}
	if err := projectAssistantValidateGrantBearingToolArguments(spec, args); err != nil {
		return t.finishFailedToolCall(
			callID,
			spec.Name,
			argumentsInJSON,
			"invalid workspace approval scope: "+err.Error(),
		), nil
	}
	decision := projectAssistantPermissionForV2(
		spec,
		t.req.ApprovalMode,
		t.runState,
		args,
		projectEinoAssistantTemplateBootstrapAllowed(t.req.Project),
	)
	switch decision {
	case projectAssistantPermissionAllow:
		if t.req.ApprovalMode == store.AssistantApprovalModeAutoApprove &&
			projectAssistantPermissionForV2(
				spec,
				store.AssistantApprovalModeAlwaysAsk,
				t.runState,
				args,
				projectEinoAssistantTemplateBootstrapAllowed(t.req.Project),
			) == projectAssistantPermissionAsk {
			if t.req.auditRecorder != nil {
				t.req.auditRecorder.recordAutomaticApproval(callID, spec.Name, t.req.Identity.user, t.req.ApprovalMode)
			}
		}
		return t.invokeAllowedTool(ctx, callID, spec, args)
	case projectAssistantPermissionAsk:
		if !t.runState.TryStartPermissionBarrier() {
			return projectEinoPermissionBarrierToolResult(), nil
		}
		return "", t.requestPermission(ctx, callID, spec, args, argumentsInJSON)
	case projectAssistantPermissionDeny:
		return t.finishFailedToolCall(callID, spec.Name, argumentsInJSON, "permission denied: unknown tool risk"), nil
	default:
		return t.finishFailedToolCall(callID, spec.Name, argumentsInJSON, "permission denied"), nil
	}
}

func (t projectEinoAssistantTool) invokeAllowedTool(ctx context.Context, callID string, spec projectAssistantToolSpec, args map[string]any) (string, error) {
	if cause := context.Cause(ctx); cause != nil {
		return "", cause
	}
	if err := t.admitMutation(ctx, spec); err != nil {
		return "", err
	}
	if t.req.eventLedger == nil {
		return "", errors.New("assistant run event ledger is not configured")
	}
	ledgerDecision, err := t.req.eventLedger.BeginToolCall(ctx, callID, spec, args)
	if err != nil {
		return "", err
	}
	if ledgerDecision.Replay != nil {
		return t.replayDurableToolCall(callID, spec, args, *ledgerDecision.Replay)
	}
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      spec.Name,
		Status:    "running",
		Arguments: summarizeProjectToolArgumentsMap(spec.Name, args),
	})
	if projectToolBaseName(spec.Name) == projectToolDefineInitialProjectPlan {
		result, err := t.invokeInitialProjectPlanTool(ctx, callID, spec, args)
		return t.finishDurableToolCall(ctx, ledgerDecision, result, err)
	}
	if t.req.ToolPort == nil {
		return t.finishDurableToolCall(ctx, ledgerDecision, "", errors.New("App Studio tool port is not configured"))
	}
	result, err := t.req.ToolPort.Invoke(ctx, t.tool, projectAssistantToolCallRequest{
		Identity:              t.req.Identity,
		Project:               t.req.Project,
		Repository:            t.req.Repository,
		WorkspaceScope:        t.req.WorkspaceScope,
		ProjectRepositoryRef:  t.runState.ProjectRepositoryRef(),
		MCPEndpoint:           mcpServerURL(t.req.MCPBaseURL, t.req.Identity.clusterID, "default"),
		SessionSnapshot:       t.runState.SessionSnapshot(),
		AssistantRunID:        projectAssistantRunID(t.req),
		InitialBuild:          projectAssistantInitialBuildActive(t.req, t.runState),
		EnforceMutationSafety: true,
		ObservedReadFiles:     t.runState.ObservedReadFilePaths(),
		Arguments:             args,
	})
	if err != nil {
		if projectEinoAssistantWorkspaceMutationResultHasChanges(spec.Name, result) {
			// A contextual patch can fail after an I/O error and still leave a
			// partial delta when rollback is incomplete. Treat the observed delta
			// as a real source mutation even though the requested patch failed.
			t.invalidateV2PartialPatchReads(spec, args)
			if recordErr := t.recordV2WorkspaceMutation(ctx, spec.Name, result); recordErr != nil {
				return t.finishDurableToolCall(ctx, ledgerDecision, result, recordErr)
			}
			modelResult := t.runState.RegisterTransientToolResult(
				spec.Name,
				projectEinoAssistantPartialMutationResult(result, err),
			)
			modelResult, durableErr := t.finishDurableToolCall(ctx, ledgerDecision, modelResult, nil)
			if durableErr != nil {
				return "", durableErr
			}
			t.emitToolCall(projectToolCallStreamEvent{
				ID:        callID,
				Name:      spec.Name,
				Status:    "failed",
				Arguments: summarizeProjectToolArgumentsMap(spec.Name, args),
				Summary:   summarizeProjectToolResult(spec.Name, modelResult),
				Mutation:  projectAssistantMutationFromResult(spec.Name, modelResult),
			})
			t.recordToolMessage(callID, spec.Name, modelResult)
			t.appendBuilderEvent(projectBuilderEventWorkspaceChanged)
			return modelResult, nil
		}
		t.reopenV2FailedContextualPatchReads(spec, args)
		if projectEinoAssistantPropagateToolError(err) {
			return t.finishDurableToolCall(ctx, ledgerDecision, "", err)
		}
		failed := t.finishFailedToolCall(callID, spec.Name, projectEinoToolArgumentsString(args), err.Error())
		return t.finishDurableToolCall(ctx, ledgerDecision, failed, nil)
	}
	modelResult := t.runState.RegisterTransientToolResult(spec.Name, result)
	if projectEinoAssistantSuccessfulWorkspaceMutationResult(spec.Name, result) {
		if recordErr := t.recordV2WorkspaceMutation(ctx, spec.Name, result); recordErr != nil {
			return t.finishDurableToolCall(ctx, ledgerDecision, result, recordErr)
		}
	} else if projectToolBaseName(spec.Name) == projectToolSelectTemplate &&
		projectEinoAssistantSuccessfulToolContent(result) && t.server != nil {
		// Selecting a replacement development target must also populate it with
		// the existing workspace, even when no source edit follows this call.
		t.server.scheduleDevelopmentSyncAfterMutation(t.req.Identity, t.req.Project, spec.Name)
	}
	modelResult, err = t.finishDurableToolCall(ctx, ledgerDecision, modelResult, nil)
	if err != nil {
		return "", err
	}
	successful := projectEinoAssistantSuccessfulToolContent(modelResult)
	status := projectToolCallResultStatus(spec.Name, result)
	if !successful {
		status = "failed"
	}
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      spec.Name,
		Status:    status,
		Arguments: summarizeProjectToolArgumentsMap(spec.Name, args),
		Summary:   summarizeProjectToolResult(spec.Name, modelResult),
		Mutation:  projectAssistantMutationFromSuccessfulResult(spec.Name, modelResult, successful),
	})
	t.recordToolMessage(callID, spec.Name, modelResult)
	if spec.Risk == projectAssistantToolRiskWrite && successful {
		t.appendBuilderEvent(projectBuilderEventWorkspaceChanged)
	}
	return modelResult, nil
}

func (t projectEinoAssistantTool) finishDurableToolCall(
	ctx context.Context,
	decision projectAssistantRunToolCallDecision,
	result string,
	invokeErr error,
) (string, error) {
	if t.req.eventLedger == nil || !decision.ShouldDispatch() {
		return "", errors.New("assistant run event ledger dispatch token is missing")
	}
	outcome, err := t.req.eventLedger.FinishToolCall(ctx, decision.Token, result, invokeErr)
	if err != nil {
		return "", err
	}
	return outcome.InvokeResult()
}

func (t projectEinoAssistantTool) replayDurableToolCall(
	callID string,
	spec projectAssistantToolSpec,
	args map[string]any,
	outcome projectAssistantRunToolCallOutcome,
) (string, error) {
	result, err := outcome.InvokeResult()
	if err != nil {
		return result, err
	}
	successful := projectEinoAssistantSuccessfulToolContent(result)
	status := projectToolCallResultStatus(spec.Name, result)
	if !successful {
		status = "failed"
	}
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      spec.Name,
		Status:    status,
		Arguments: summarizeProjectToolArgumentsMap(spec.Name, args),
		Summary:   summarizeProjectToolResult(spec.Name, result),
		Mutation:  projectAssistantMutationFromSuccessfulResult(spec.Name, result, successful),
	})
	t.recordToolMessage(callID, spec.Name, result)
	if spec.Risk == projectAssistantToolRiskWrite && successful {
		t.appendBuilderEvent(projectBuilderEventWorkspaceChanged)
	}
	return result, nil
}

func (t projectEinoAssistantTool) reopenV2FailedContextualPatchReads(spec projectAssistantToolSpec, args map[string]any) {
	if t.runState == nil || projectToolBaseName(spec.Name) != projectToolApplyPatch {
		return
	}
	patch, ok := projectToolRawString(args["patch"])
	if !ok || strings.TrimSpace(patch) == "" {
		return
	}
	paths, err := workspace.PatchReadPaths(patch)
	if err != nil {
		return
	}
	for _, path := range paths {
		t.runState.ReopenReadFile(path)
	}
}

func (t projectEinoAssistantTool) invalidateV2PartialPatchReads(spec projectAssistantToolSpec, args map[string]any) {
	if t.runState == nil || projectToolBaseName(spec.Name) != projectToolApplyPatch {
		return
	}
	patch, ok := projectToolRawString(args["patch"])
	if !ok || strings.TrimSpace(patch) == "" {
		return
	}
	paths, err := workspace.PatchReadPaths(patch)
	if err != nil {
		return
	}
	for _, path := range paths {
		t.runState.InvalidateObservedReadFile(path)
	}
}

func (t projectEinoAssistantTool) recordV2WorkspaceMutation(ctx context.Context, name, result string) error {
	if t.runState == nil {
		return nil
	}
	paths := t.recordV2SuccessfulMutationPaths(result)
	var persistErr error
	if len(paths) > 0 && t.req.Workspace != nil {
		if _, err := t.req.Workspace.AddUncommittedPaths(ctx, t.req.WorkspaceScope, paths); err != nil {
			persistErr = fmt.Errorf("persist project uncommitted source paths: %w", err)
		}
	}
	revision := t.runState.BeginDevelopmentSyncForNextMutation()
	if t.server == nil || !t.server.scheduleDevelopmentSyncAfterMutationWithCompletion(
		t.req.Identity,
		t.req.Project,
		name,
		func(syncErr error) { t.runState.CompleteDevelopmentSync(revision, syncErr) },
	) {
		t.runState.CompleteDevelopmentSync(revision, errors.New("workspace synchronization was not scheduled"))
	}
	// Record the source revision only on the first durable dispatch. Ledger
	// replay returns above, so an exactly-once replay cannot invent a second
	// mutation revision without a corresponding synchronization.
	t.runState.RecordSourceMutation()
	return persistErr
}

func (t projectEinoAssistantTool) recordV2SuccessfulMutationPaths(result string) []string {
	if t.runState == nil {
		return nil
	}
	mutation := projectAssistantMutationFromResult(projectToolApplyPatch, result)
	if mutation == nil {
		return nil
	}
	candidates := append(append([]string(nil), mutation.Paths...), mutation.Path)
	pathSet := make(map[string]struct{}, len(candidates))
	for _, path := range candidates {
		clean, err := workspace.CleanProjectPath(path)
		if err != nil {
			continue
		}
		pathSet[clean] = struct{}{}
		t.runState.RecordSuccessfulMutationPath(clean)
	}
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func projectEinoAssistantWorkspaceMutationResultHasChanges(name, result string) bool {
	mutation := projectAssistantMutationFromResult(name, result)
	if mutation == nil {
		return false
	}
	if strings.TrimSpace(mutation.Path) != "" {
		return true
	}
	for _, path := range mutation.Paths {
		if strings.TrimSpace(path) != "" {
			return true
		}
	}
	return false
}

func projectEinoAssistantPartialMutationResult(result string, invokeErr error) string {
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &decoded); err != nil {
		return result
	}
	decoded["status"] = "partial_failure"
	decoded["error"] = projectEinoAssistantSafeErrorText(invokeErr)
	decoded["message"] = "The patch failed and rollback was incomplete. The listed paths remain changed; reread their current contents before another edit. Workspace synchronization, operational verification, and commit are still required."
	raw, err := json.Marshal(decoded)
	if err != nil {
		return result
	}
	return string(raw)
}

func (t projectEinoAssistantTool) v2CommitArguments(args map[string]any) (map[string]any, error) {
	if t.runState == nil {
		return nil, errors.New("the current run has no source mutation evidence to commit")
	}
	mutationPaths := t.runState.SuccessfulMutationPaths()
	if len(mutationPaths) == 0 {
		return nil, errors.New("commit_project_files is unavailable until this run successfully mutates source files")
	}
	allowed := make(map[string]struct{}, len(mutationPaths))
	for _, path := range mutationPaths {
		allowed[path] = struct{}{}
	}
	requestedPaths := projectToolStringList(args["paths"])
	if len(requestedPaths) == 0 {
		return nil, errors.New("commit_project_files requires the complete current-run mutation path set")
	}
	requestedSet := make(map[string]struct{}, len(requestedPaths))
	for _, requested := range requestedPaths {
		clean, err := workspace.CleanProjectPath(requested)
		if err != nil {
			return nil, err
		}
		if _, ok := allowed[clean]; !ok {
			return nil, fmt.Errorf("commit path %q was not mutated by this assistant run", clean)
		}
		requestedSet[clean] = struct{}{}
	}
	for _, path := range mutationPaths {
		if _, ok := requestedSet[path]; !ok {
			return nil, fmt.Errorf("commit paths omit %q from this assistant run's mutation set", path)
		}
	}
	normalized := make(map[string]any, len(args))
	for key, value := range args {
		normalized[key] = value
	}
	normalized["paths"] = append([]string(nil), mutationPaths...)
	verifiedDigest := t.runState.VerifiedWorkspaceDigest()
	if verifiedDigest != "" {
		normalized["workspaceDigest"] = verifiedDigest
	}
	return normalized, nil
}

func (t projectEinoAssistantTool) validateV2CommitWorkspace(ctx context.Context, args map[string]any) error {
	digest, err := t.v2CommitWorkspaceDigest(ctx, args)
	if err == nil && t.runState != nil && t.runState.VerifiedWorkspaceDigestMatches(digest) {
		return nil
	}
	t.invalidateV2CommitWorkspace(ctx)
	if err != nil {
		return fmt.Errorf("workspace changed after operational verification: %w", err)
	}
	return errors.New("workspace changed after operational verification; synchronize and verify again before committing")
}

func (t projectEinoAssistantTool) invalidateV2CommitWorkspace(_ context.Context) {
	if t.runState == nil {
		return
	}
	revision := t.runState.BeginDevelopmentSyncForNextMutation()
	if t.server == nil || !t.server.scheduleDevelopmentSyncAfterMutationWithCompletion(
		t.req.Identity,
		t.req.Project,
		projectActionWorkspaceSync,
		func(syncErr error) { t.runState.CompleteDevelopmentSync(revision, syncErr) },
	) {
		t.runState.CompleteDevelopmentSync(revision, errors.New("workspace synchronization was not scheduled"))
	}
	t.runState.RecordSourceMutation()
}

func (t projectEinoAssistantTool) v2CommitWorkspaceDigest(ctx context.Context, args map[string]any) (string, error) {
	return projectEinoAssistantWorkspaceDigest(ctx, t.req.Workspace, t.req.WorkspaceScope, projectToolStringList(args["paths"]))
}

func projectEinoAssistantWorkspaceDigest(ctx context.Context, store *workspace.FileStore, scope workspace.Scope, paths []string) (string, error) {
	if store == nil {
		return "", errors.New("project workspace store is not configured")
	}
	if len(paths) == 0 {
		return "", errors.New("commit paths are required")
	}
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		clean, err := workspace.CleanProjectPath(path)
		if err != nil {
			return "", err
		}
		file, err := store.ReadFile(ctx, scope, workspace.ReadOptions{
			Path:     clean,
			MaxBytes: workspace.MaxWriteBytes,
		})
		if err != nil {
			return "", err
		}
		if file.Binary || file.Truncated {
			return "", fmt.Errorf("file %q cannot be committed as bounded UTF-8 source", clean)
		}
		_, _ = hash.Write([]byte(clean))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(file.Content))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func projectAssistantMutationFromSuccessfulResult(name, result string, successful bool) *projectAssistantMutation {
	if !successful {
		return nil
	}
	return projectAssistantMutationFromResult(name, result)
}

func projectEinoAssistantPersistentToolResult(name, result string) string {
	if projectToolBaseName(name) != projectToolGetPreviewConsoleLogs {
		return result
	}
	var decoded struct {
		Status        string `json:"status"`
		NextSequence  uint64 `json:"nextSequence,omitempty"`
		DroppedCount  int    `json:"droppedCount,omitempty"`
		ReceivedCount int    `json:"receivedCount,omitempty"`
		RedactedCount int    `json:"redactedCount,omitempty"`
		Summary       string `json:"summary,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		return `{"status":"unavailable","summary":"transient preview console result omitted from persistence"}`
	}
	persistent, err := json.Marshal(map[string]any{
		"status":         strings.TrimSpace(decoded.Status),
		"nextSequence":   decoded.NextSequence,
		"droppedCount":   decoded.DroppedCount,
		"receivedCount":  decoded.ReceivedCount,
		"redactedCount":  decoded.RedactedCount,
		"summary":        strings.TrimSpace(decoded.Summary),
		"transientEvent": true,
	})
	if err != nil {
		return `{"status":"unavailable","summary":"transient preview console result omitted from persistence"}`
	}
	return string(persistent)
}

func projectAssistantMutationFromResult(name, result string) *projectAssistantMutation {
	switch projectToolBaseName(name) {
	case projectToolApplyPatch:
	default:
		return nil
	}
	var decoded struct {
		Path         string   `json:"path"`
		Paths        []string `json:"paths"`
		Additions    int      `json:"additions"`
		Deletions    int      `json:"deletions"`
		Replacements int      `json:"replacements"`
		Patch        string   `json:"patch"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		return nil
	}
	patch, truncated := projectAssistantBoundedMutationPatch(decoded.Patch)
	return &projectAssistantMutation{
		Path:           decoded.Path,
		Paths:          append([]string(nil), decoded.Paths...),
		Additions:      decoded.Additions,
		Deletions:      decoded.Deletions,
		Replacements:   decoded.Replacements,
		Patch:          patch,
		PatchTruncated: truncated,
	}
}

func projectAssistantBoundedMutationPatch(patch string) (string, bool) {
	if len([]byte(patch)) <= projectAssistantMutationPatchMaxBytes {
		return patch, false
	}
	raw := []byte(patch)[:projectAssistantMutationPatchMaxBytes]
	for len(raw) > 0 && !utf8.Valid(raw) {
		raw = raw[:len(raw)-1]
	}
	return string(raw), true
}

func projectAssistantRunID(req projectAssistantRunRequest) string {
	if req.AssistantRun == nil {
		return ""
	}
	return strings.TrimSpace(req.AssistantRun.ID)
}

func projectAssistantInitialBuildActive(req projectAssistantRunRequest, runState *projectEinoAssistantRunState) bool {
	if req.InitialApprovedPlan != nil {
		return true
	}
	return runState != nil && runState.ApprovedPlan() != nil && runState.ApprovedPlan().RunLocal
}

func (t projectEinoAssistantTool) retireApprovedPlan(_ context.Context) error {
	if t.server == nil && t.req.executionAuthority == nil {
		return store.ErrAssistantRunConflict
	}
	t.runState.ClearApprovedPlan()
	return nil
}

func (t projectEinoAssistantTool) requestFollowUp(ctx context.Context, callID string, spec projectAssistantToolSpec, args map[string]any) (string, error) {
	questions := normalizeProjectAssistantStringList(projectToolStringList(args["questions"]))
	if len(questions) == 0 {
		return t.finishFailedToolCall(callID, spec.Name, projectEinoToolArgumentsString(args), "follow-up requires at least one question"), nil
	}
	if len(questions) > 3 {
		questions = questions[:3]
	}
	prompt := projectAssistantFollowUpPrompt(questions)
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      spec.Name,
		Status:    "input_required",
		Arguments: summarizeProjectToolArgumentsMap(spec.Name, args),
		Summary:   prompt,
	})
	return "", einotool.StatefulInterrupt(ctx, &projectEinoFollowUpInterruptInfo{
		ToolCallID: callID,
		Questions:  append([]string(nil), questions...),
		Prompt:     prompt,
	}, &projectEinoFollowUpInterruptState{
		ToolCallID: callID,
		Questions:  append([]string(nil), questions...),
	})
}

func (t projectEinoAssistantTool) resumeFollowUp(ctx context.Context, callID string, spec projectAssistantToolSpec, state *projectEinoFollowUpInterruptState) (string, error) {
	if strings.TrimSpace(callID) == "" {
		callID = strings.TrimSpace(state.ToolCallID)
	}
	questions := normalizeProjectAssistantStringList(state.Questions)
	isResumeTarget, hasData, data := einotool.GetResumeContext[*projectEinoFollowUpResumeData](ctx)
	if !isResumeTarget {
		return "", einotool.StatefulInterrupt(ctx, &projectEinoFollowUpInterruptInfo{
			ToolCallID: callID,
			Questions:  append([]string(nil), questions...),
			Prompt:     projectAssistantFollowUpPrompt(questions),
		}, state)
	}
	if !hasData || data == nil || strings.TrimSpace(data.Answer) == "" {
		return t.finishFailedToolCall(callID, spec.Name, projectEinoToolArgumentsString(map[string]any{"questions": questions}), "follow-up answer is required"), nil
	}
	result := projectEinoFollowUpToolResult(data.Answer)
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      spec.Name,
		Status:    "succeeded",
		Arguments: summarizeProjectToolArgumentsMap(spec.Name, map[string]any{"questions": questions}),
		Summary:   summarizeProjectToolResult(spec.Name, result),
	})
	t.recordToolMessage(callID, spec.Name, result)
	return result, nil
}

func (t projectEinoAssistantTool) invokeInitialProjectPlanTool(
	ctx context.Context,
	callID string,
	spec projectAssistantToolSpec,
	args map[string]any,
) (string, error) {
	authority := t.runState.ApprovedPlan()
	if authority == nil || !authority.RunLocal {
		return t.finishFailedToolCall(callID, spec.Name, projectEinoToolArgumentsString(args), "initial project planning is unavailable outside the initial build"), nil
	}
	plan, err := projectAssistantInitialExecutionPlanFromArguments(authority.Goal, args)
	if err != nil {
		return t.finishFailedToolCall(callID, spec.Name, projectEinoToolArgumentsString(args), err.Error()), nil
	}
	if existing := t.runState.ExecutionPlan(); existing != nil {
		plan = mergeProjectAssistantInitialExecutionPlans(*existing, plan)
	}

	persistCtx, cancelPersist := detachedProjectPersistenceContext(ctx)
	defer cancelPersist()
	if err := persistInitialProjectPlanMemory(
		persistCtx,
		t.req.Client,
		t.req.Project,
		plan.Goal,
		plan.AcceptanceCriteria,
	); err != nil {
		return "", fmt.Errorf("%w: persist project memory: %v", errProjectAssistantInitialPlanPersistence, err)
	}
	t.runState.ApprovePlan(plan)
	t.runState.SetExecutionPlan(plan)
	initialProgress := projectAssistantPlanSnapshot{
		Steps: make([]projectAssistantPlanStep, 0, len(plan.Steps)),
	}
	for _, step := range plan.Steps {
		initialProgress.Steps = append(initialProgress.Steps, projectAssistantPlanStep{
			Content:    step,
			ActiveForm: step,
			Status:     "pending",
		})
	}
	t.runState.SetPlanProgress(initialProgress)
	if t.req.StreamCallbacks.OnPlan != nil {
		t.req.StreamCallbacks.OnPlan(initialProgress)
	}
	if t.req.StreamCallbacks.OnStatus != nil {
		t.req.StreamCallbacks.OnStatus("Building · 0 of " + fmt.Sprintf("%d", len(plan.Steps)) + " steps")
	}

	raw, err := json.Marshal(map[string]any{
		"status":             "defined",
		"summary":            plan.Summary,
		"steps":              plan.Steps,
		"targetPaths":        plan.TargetPaths,
		"acceptanceCriteria": plan.AcceptanceCriteria,
	})
	if err != nil {
		return "", err
	}
	result := string(raw)
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      spec.Name,
		Status:    "succeeded",
		Arguments: summarizeProjectToolArgumentsMap(spec.Name, args),
		Summary:   summarizeProjectToolResult(spec.Name, result),
	})
	t.recordToolMessage(callID, spec.Name, result)
	return result, nil
}

func (t projectEinoAssistantTool) admitMutation(ctx context.Context, spec projectAssistantToolSpec) error {
	switch spec.Risk {
	case projectAssistantToolRiskPlan, projectAssistantToolRiskWrite, projectAssistantToolRiskCommit, projectAssistantToolRiskRuntime:
	default:
		return nil
	}
	if t.req.CollaborationMode != projectAssistantCollaborationModeDefault || t.req.AssistantRun == nil {
		return store.ErrAssistantRunConflict
	}
	return t.executionAuthority().AdmitMutation(ctx)
}

func (t projectEinoAssistantTool) appendBuilderEvent(eventType string) {
	emitProjectAssistantBuilderEvent(t.req.StreamCallbacks, projectAssistantBuilderEventView(eventType))
}

func (t projectEinoAssistantTool) requestPermission(ctx context.Context, callID string, spec projectAssistantToolSpec, args map[string]any, argumentsInJSON string) error {
	commitWorkspaceDigest := ""
	if projectEinoAssistantCommitTool(spec.Name) {
		var err error
		commitWorkspaceDigest, err = t.v2CommitWorkspaceDigest(ctx, args)
		if err != nil {
			return fmt.Errorf("bind commit approval to current workspace: %w", err)
		}
	}
	if spec.Risk == projectAssistantToolRiskCommit {
		if err := t.retireApprovedPlan(ctx); err != nil {
			return fmt.Errorf("%w: retire approved plan before commit approval: %v", errProjectAssistantPlanRetirement, err)
		}
	}
	reason := projectAssistantPermissionReasonForArguments(spec, args)
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      spec.Name,
		Status:    "permission_required",
		Arguments: summarizeProjectToolArgumentsMap(spec.Name, args),
		Summary:   reason,
	})
	return einotool.StatefulInterrupt(ctx, &projectEinoPermissionInterruptInfo{
		ToolCallID:      callID,
		ToolName:        spec.Name,
		ArgumentsInJSON: argumentsInJSON,
		Reason:          reason,
		Risk:            spec.Risk,
	}, &projectEinoPermissionInterruptState{
		ToolCallID:            callID,
		ToolName:              spec.Name,
		ArgumentsInJSON:       argumentsInJSON,
		CommitWorkspaceDigest: commitWorkspaceDigest,
	})
}

func (t projectEinoAssistantTool) resumePermission(ctx context.Context, callID string, spec projectAssistantToolSpec, state *projectEinoPermissionInterruptState) (string, error) {
	if strings.TrimSpace(callID) == "" {
		callID = strings.TrimSpace(state.ToolCallID)
	}
	name := strings.TrimSpace(state.ToolName)
	if name == "" {
		name = spec.Name
	}
	args, err := projectEinoToolArguments(state.ArgumentsInJSON)
	if err != nil {
		return t.finishFailedToolCall(callID, name, state.ArgumentsInJSON, "invalid interrupted arguments: "+truncateProjectToolInfo(err.Error())), nil
	}
	isResumeTarget, hasData, data := einotool.GetResumeContext[*projectEinoPermissionResumeData](ctx)
	if !isResumeTarget {
		return "", einotool.StatefulInterrupt(ctx, &projectEinoPermissionInterruptInfo{
			ToolCallID:      callID,
			ToolName:        name,
			ArgumentsInJSON: state.ArgumentsInJSON,
			Reason:          projectAssistantPermissionReason(spec),
			Risk:            spec.Risk,
		}, state)
	}
	if !hasData || data == nil {
		return "", errors.New("permission resume data is required")
	}
	switch data.Decision {
	case projectAssistantPermissionAllow:
		if data.EditedArguments != nil {
			args = cloneProjectAssistantToolArguments(data.EditedArguments)
		}
		if projectEinoAssistantCommitTool(spec.Name) {
			normalized, normalizeErr := t.v2CommitArguments(args)
			if normalizeErr != nil {
				return t.finishFailedToolCall(callID, name, projectEinoToolArgumentsString(args), normalizeErr.Error()), nil
			}
			args = normalized
			if validateErr := t.validateV2CommitWorkspace(ctx, args); validateErr != nil {
				return t.finishFailedToolCall(callID, name, projectEinoToolArgumentsString(args), validateErr.Error()), nil
			}
			currentDigest, digestErr := t.v2CommitWorkspaceDigest(ctx, args)
			if digestErr != nil {
				return t.finishFailedToolCall(callID, name, projectEinoToolArgumentsString(args), "revalidate approved commit workspace: "+digestErr.Error()), nil
			}
			if strings.TrimSpace(state.CommitWorkspaceDigest) == "" || currentDigest != state.CommitWorkspaceDigest {
				t.invalidateV2CommitWorkspace(ctx)
				return t.finishFailedToolCall(
					callID,
					name,
					projectEinoToolArgumentsString(args),
					"workspace content changed after commit approval; rerun operational verification before requesting a new commit",
				), nil
			}
		}
		return t.invokeAllowedTool(ctx, callID, spec, args)
	case projectAssistantPermissionDeny:
		return t.finishDeniedToolCall(callID, name, args, "denied by user"), nil
	default:
		return t.finishDeniedToolCall(callID, name, args, "invalid permission decision"), nil
	}
}

func (t projectEinoAssistantTool) finishDeniedToolCall(callID, name string, args map[string]any, reason string) string {
	tc := projectEinoAssistantFallbackToolCall(callID, name, projectEinoToolArgumentsString(args))
	msg := projectAssistantPermissionDeniedToolMessage(tc, reason)
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        tc.ID,
		Name:      tc.Function.Name,
		Status:    "rejected",
		Arguments: summarizeProjectToolArgumentsMap(name, args),
		Error:     msg.Content,
	})
	t.recordToolMessage(tc.ID, tc.Function.Name, msg.Content)
	return msg.Content
}

func (t projectEinoAssistantTool) finishFailedToolCall(callID, name, rawArgs, reason string) string {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(rawArgs), &args)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "tool call failed"
	}
	safeReason := projectEinoAssistantSafeErrorText(errors.New(reason))
	t.emitToolCall(projectToolCallStreamEvent{
		ID:        callID,
		Name:      name,
		Status:    "failed",
		Arguments: summarizeProjectToolArgumentsMap(name, args),
		Error:     safeReason,
	})
	result := truncateProjectToolInfo("Tool call failed: " + safeReason)
	t.recordToolMessage(callID, name, result)
	return result
}

func (t projectEinoAssistantTool) emitToolCall(event projectToolCallStreamEvent) {
	if t.req.StreamCallbacks.OnToolCall == nil {
		return
	}
	if event.ID == "" {
		event.ID = "tool-1"
	}
	t.runState.EmitToolCall(t.req.StreamCallbacks.OnToolCall, event)
}

func (t projectEinoAssistantTool) recordToolMessage(callID, name, content string) {
	if strings.TrimSpace(callID) == "" {
		callID = "tool-1"
	}
	t.runState.RecordToolMessage(chatMessage{
		Role:       "tool",
		Name:       strings.TrimSpace(name),
		ToolCallID: callID,
		Content:    content,
	})
}

func projectEinoToolArguments(argumentsInJSON string) (map[string]any, error) {
	args := map[string]any{}
	if strings.TrimSpace(argumentsInJSON) == "" {
		return args, nil
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func projectEinoFollowUpToolResult(answer string) string {
	raw, err := json.Marshal(map[string]any{
		"answer": strings.TrimSpace(answer),
	})
	if err != nil {
		return strings.TrimSpace(answer)
	}
	return string(raw)
}

func projectEinoToolArgumentsString(args map[string]any) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func projectChatToolsInclude(tools []chatTool, name string) bool {
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool.Function.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func projectEinoUnknownToolHandler(req projectAssistantRunRequest, runState *projectEinoAssistantRunState) func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, name, input string) (string, error) {
		if runState.PermissionBarrierActive() {
			return projectEinoPermissionBarrierToolResult(), nil
		}
		callID := compose.GetToolCallID(ctx)
		args := map[string]any{}
		_ = json.Unmarshal([]byte(input), &args)
		runState.EmitToolCall(req.StreamCallbacks.OnToolCall, projectToolCallStreamEvent{
			ID:        callID,
			Name:      name,
			Status:    "rejected",
			Arguments: summarizeProjectToolArgumentsMap(name, args),
			Error:     "disallowed tool name",
		})
		result := "Tool call failed: disallowed tool name"
		runState.RecordToolMessage(chatMessage{
			Role:       "tool",
			Name:       strings.TrimSpace(name),
			ToolCallID: callID,
			Content:    result,
		})
		runState.RecordCompletedAction(name, projectEinoToolArgumentsString(args), false)
		return result, nil
	}
}

func projectEinoPermissionBarrierToolResult() string {
	return "Tool call skipped: waiting for approval of a previous tool call"
}

func projectAssistantApprovedPlanFromArguments(args map[string]any) (projectAssistantApprovedPlan, error) {
	targetPaths, err := projectAssistantCanonicalGrantTargets(projectToolStringList(args["targetPaths"]))
	if err != nil {
		return projectAssistantApprovedPlan{}, err
	}
	if len(targetPaths) == 0 {
		return projectAssistantApprovedPlan{}, errors.New("targetPaths is required")
	}
	return normalizeProjectAssistantApprovedPlan(projectAssistantApprovedPlan{
		Summary:            projectToolString(args["summary"]),
		Steps:              projectToolStringList(args["steps"]),
		TargetPaths:        targetPaths,
		Version:            projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities:       []string{projectAssistantCapabilityWorkspaceMutate},
		AcceptanceCriteria: projectToolStringList(args["acceptanceCriteria"]),
		ApprovalTool:       projectToolDefineInitialProjectPlan,
	}), nil
}

func projectAssistantInitialExecutionPlanFromArguments(
	goal string,
	args map[string]any,
) (projectAssistantApprovedPlan, error) {
	plan, err := projectAssistantApprovedPlanFromArguments(args)
	if err != nil {
		return projectAssistantApprovedPlan{}, err
	}
	if len(plan.AcceptanceCriteria) == 0 {
		return projectAssistantApprovedPlan{}, errors.New("acceptanceCriteria is required")
	}
	plan.Goal = strings.TrimSpace(goal)
	plan.ApprovalTool = projectToolDefineInitialProjectPlan
	plan.RunLocal = true
	plan.AllowAllWrites = false
	return normalizeProjectAssistantApprovedPlan(plan), nil
}

func mergeProjectAssistantInitialExecutionPlans(
	current projectAssistantApprovedPlan,
	revision projectAssistantApprovedPlan,
) projectAssistantApprovedPlan {
	merged := mergeProjectAssistantApprovedPlans(current, revision)
	merged.Goal = current.Goal
	merged.Summary = revision.Summary
	merged.Steps = append([]string(nil), revision.Steps...)
	merged.AcceptanceCriteria = append([]string(nil), revision.AcceptanceCriteria...)
	merged.ApprovalTool = projectToolDefineInitialProjectPlan
	merged.RunLocal = true
	merged.AllowAllWrites = false
	return normalizeProjectAssistantApprovedPlan(merged)
}

func projectAssistantValidateGrantBearingToolArguments(spec projectAssistantToolSpec, args map[string]any) error {
	switch strings.TrimSpace(spec.Name) {
	case projectToolDefineInitialProjectPlan:
		activeGoal := ""
		// Run state validates that this internal tool is available only for a
		// run-local initial-build authority. Argument shape is validated here.
		_, err := projectAssistantInitialExecutionPlanFromArguments(activeGoal, args)
		return err
	case projectToolApplyPatch:
		_, err := projectAssistantWriteTargetPaths(spec.Name, args)
		return err
	default:
		return nil
	}
}
