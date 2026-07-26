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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type projectEinoAssistantPhase string

const (
	projectEinoAssistantPhaseApproval projectEinoAssistantPhase = "approval"
	projectEinoAssistantPhaseMutate   projectEinoAssistantPhase = "mutate"
	projectEinoAssistantPhaseVerify   projectEinoAssistantPhase = "verify"
	projectEinoAssistantPhaseRepair   projectEinoAssistantPhase = "repair"
	projectEinoAssistantPhaseCommit   projectEinoAssistantPhase = "commit"
	projectEinoAssistantPhaseReport   projectEinoAssistantPhase = "report"
)

const (
	projectEinoAssistantWriteTodosTool = "write_todos"
	projectEinoAssistantToolSearchTool = "tool_search"
)

type projectEinoAssistantPhaseFilterMiddleware struct {
	*adk.BaseChatModelAgentMiddleware

	req               projectAssistantRunRequest
	runState          *projectEinoAssistantRunState
	toolInfos         []*schema.ToolInfo
	deferredToolInfos []*schema.ToolInfo
	phase             projectEinoAssistantPhase
	approvedPlan      *projectAssistantApprovedPlan
}

func projectEinoAssistantPhaseMiddleware(
	req projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
) adk.ChatModelAgentMiddleware {
	phase := projectEinoAssistantPhaseApproval
	if req.Continuation != nil {
		if messages, err := projectChatMessagesToEino(req.Continuation.Messages); err == nil {
			state := &adk.ChatModelAgentState{Messages: messages}
			phase = projectEinoAssistantPhaseForState(req, runState, state)
			// Asking permission for a commit consumes the plan grant before
			// checkpointing. Restore only the execution phase for that exact
			// interrupted commit after re-validating write -> verification
			// ordering from the persisted message history.
			if phase == projectEinoAssistantPhaseApproval &&
				req.Continuation.Eino != nil &&
				projectEinoAssistantCommitTool(req.Continuation.Eino.ToolName) {
				phase = projectEinoAssistantPhaseForStateWithApproval(req, state, true)
			}
		}
	}
	return &projectEinoAssistantPhaseFilterMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		req:                          req,
		runState:                     runState,
		phase:                        phase,
		approvedPlan:                 cloneProjectAssistantApprovedPlan(projectEinoAssistantPhaseApprovedPlan(req, runState)),
	}
}

// BeforeAgent captures the full tool inventory after earlier middleware has
// augmented the agent. Eino persists rewritten ToolInfos across interrupts, so
// the next agent instance must not treat that prior phase-filtered slice as the
// canonical catalog.
func (m *projectEinoAssistantPhaseFilterMiddleware) BeforeAgent(
	ctx context.Context,
	runCtx *adk.ChatModelAgentContext,
) (context.Context, *adk.ChatModelAgentContext, error) {
	if runCtx == nil {
		return ctx, runCtx, nil
	}
	infos := make([]*schema.ToolInfo, 0, len(runCtx.Tools))
	for _, tool := range runCtx.Tools {
		if tool == nil {
			continue
		}
		info, err := tool.Info(ctx)
		if err != nil {
			return ctx, nil, fmt.Errorf("read phase tool info: %w", err)
		}
		infos = append(infos, info)
	}
	m.toolInfos = projectEinoAssistantPhaseMergeTools(m.toolInfos, infos)
	return ctx, runCtx, nil
}

func (m *projectEinoAssistantPhaseFilterMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	_ *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil {
		return ctx, state, nil
	}
	m.toolInfos = projectEinoAssistantPhaseMergeTools(m.toolInfos, state.ToolInfos)
	m.deferredToolInfos = projectEinoAssistantPhaseMergeTools(m.deferredToolInfos, state.DeferredToolInfos)
	// Read-only profiles already receive their exact tool set from turn-policy
	// filtering. They do not participate in the mutation lifecycle, but still
	// need the canonical static inventory after resuming a legacy checkpoint
	// whose persisted ToolInfos were phase-filtered.
	if !projectEinoAssistantPhaseLifecycleApplies(m.req) {
		state.ToolInfos = projectEinoAssistantPhaseVisibleTools(m.toolInfos, state.ToolInfos)
		state.DeferredToolInfos = projectEinoAssistantPhaseMergeTools(m.deferredToolInfos, state.DeferredToolInfos)
		return ctx, state, nil
	}
	phase := projectEinoAssistantPhaseForState(m.req, m.runState, state)
	approvedPlan := projectEinoAssistantPhaseApprovedPlan(m.req, m.runState)
	m.phase = phase
	m.approvedPlan = cloneProjectAssistantApprovedPlan(approvedPlan)
	state.ToolInfos = projectEinoAssistantPhaseFilterTools(
		phase,
		approvedPlan,
		projectEinoAssistantPhaseVisibleTools(m.toolInfos, state.ToolInfos),
	)
	state.DeferredToolInfos = projectEinoAssistantPhaseFilterTools(phase, approvedPlan, m.deferredToolInfos)
	return ctx, state, nil
}

func (m *projectEinoAssistantPhaseFilterMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	toolCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	if toolCtx == nil || !projectEinoAssistantPhaseLifecycleApplies(m.req) {
		return endpoint, nil
	}
	rawName := toolCtx.Name
	name := projectToolBaseName(rawName)
	if name != projectEinoAssistantWriteTodosTool &&
		name != projectToolRequestProjectPlanApproval &&
		name != projectToolCommitProjectFiles &&
		name != projectToolCommitFiles {
		return endpoint, nil
	}
	return func(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
		tool := &schema.ToolInfo{Name: rawName}
		switch name {
		case projectToolRequestProjectPlanApproval:
			tool.Extra = map[string]any{
				"bundle": string(projectAssistantToolBundleCollaboration),
				"risk":   string(projectAssistantToolRiskPlan),
			}
		case projectToolCommitProjectFiles, projectToolCommitFiles:
			tool.Extra = map[string]any{
				"bundle": string(projectAssistantToolBundleRepo),
				"risk":   string(projectAssistantToolRiskCommit),
			}
		}
		if !projectEinoAssistantPhaseAllowsTool(m.phase, m.approvedPlan, tool) {
			return fmt.Sprintf("Tool call denied: %s is unavailable in the current assistant phase", name), nil
		}
		return endpoint(ctx, argumentsInJSON, opts...)
	}, nil
}

func projectEinoAssistantPhaseLifecycleApplies(req projectAssistantRunRequest) bool {
	profile := req.TurnPolicy.profile
	if strings.TrimSpace(string(profile)) == "" {
		profile = req.TurnProfile
	}
	if strings.TrimSpace(string(profile)) == "" {
		// Phase-helper tests and callers outside the engine historically omit
		// turn policy. Keep lifecycle control enabled for that legacy shape;
		// the engine always supplies a normalized profile.
		return true
	}
	return projectAssistantTurnProfileAllowsMutation(profile)
}

func projectEinoAssistantPhaseVisibleTools(canonical, current []*schema.ToolInfo) []*schema.ToolInfo {
	visibleNames := make(map[string]struct{}, len(current))
	for _, tool := range current {
		if tool != nil {
			visibleNames[projectAssistantToolKey(tool.Name)] = struct{}{}
		}
	}
	visible := make([]*schema.ToolInfo, 0, len(canonical))
	for _, tool := range canonical {
		if tool == nil {
			continue
		}
		if !projectEinoAssistantPhaseSearchableTool(tool) {
			visible = append(visible, tool)
			continue
		}
		if _, selected := visibleNames[projectAssistantToolKey(tool.Name)]; selected {
			visible = append(visible, tool)
		}
	}
	return visible
}

func projectEinoAssistantPhaseSearchableTool(tool *schema.ToolInfo) bool {
	if tool == nil || tool.Extra == nil {
		return false
	}
	searchable, _ := tool.Extra[projectEinoToolSearchableExtraKey].(bool)
	return searchable
}

func projectEinoAssistantPhaseForState(
	req projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
	state *adk.ChatModelAgentState,
) projectEinoAssistantPhase {
	approvedPlan := projectEinoAssistantPhaseApprovedPlan(req, runState)
	return projectEinoAssistantPhaseForHistory(
		projectEinoAssistantPhaseHistoryForState(state),
		approvedPlan != nil,
		req.InitialApprovedPlan != nil || (approvedPlan != nil && approvedPlan.RunLocal),
	)
}

func projectEinoAssistantPhaseForStateWithApproval(
	req projectAssistantRunRequest,
	state *adk.ChatModelAgentState,
	approved bool,
) projectEinoAssistantPhase {
	return projectEinoAssistantPhaseForHistory(
		projectEinoAssistantPhaseHistoryForState(state),
		approved,
		req.InitialApprovedPlan != nil,
	)
}

type projectEinoAssistantPhaseHistory struct {
	latestWrite        int
	latestVerification int
	latestCommit       int
	verificationReady  bool
}

func projectEinoAssistantPhaseHistoryForState(state *adk.ChatModelAgentState) projectEinoAssistantPhaseHistory {
	history := projectEinoAssistantPhaseHistory{
		latestWrite:        -1,
		latestVerification: -1,
		latestCommit:       -1,
	}
	if state != nil {
		for index, message := range state.Messages {
			if message == nil {
				continue
			}
			name := projectToolBaseName(message.ToolName)
			if name == projectToolVerifyDevelopmentRuntime && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(message.Content)), "tool call skipped: waiting for approval") {
				history.latestVerification = index
				history.verificationReady = projectEinoAssistantPhaseSuccessfulToolResult(message) &&
					projectEinoAssistantPhaseVerificationReady(message.Content)
				continue
			}
			if !projectEinoAssistantPhaseSuccessfulToolResult(message) {
				continue
			}
			switch name {
			case projectToolWriteFile, projectToolApplyPatch, projectToolMkdir:
				history.latestWrite = index
			case projectToolCommitProjectFiles, projectToolCommitFiles:
				history.latestCommit = index
			}
		}
	}
	return history
}

func projectEinoAssistantPhaseForHistory(
	history projectEinoAssistantPhaseHistory,
	approved bool,
	initialCreation bool,
) projectEinoAssistantPhase {
	// A completed commit is terminal even though the tool execution clears the
	// run-local approval grant before the next model call.
	if history.latestCommit > history.latestWrite {
		return projectEinoAssistantPhaseReport
	}
	if !approved {
		return projectEinoAssistantPhaseApproval
	}
	if history.latestWrite < 0 {
		return projectEinoAssistantPhaseMutate
	}
	if history.latestVerification < history.latestWrite {
		return projectEinoAssistantPhaseVerify
	}
	if !history.verificationReady {
		return projectEinoAssistantPhaseRepair
	}
	if initialCreation {
		return projectEinoAssistantPhaseReport
	}
	return projectEinoAssistantPhaseCommit
}

func projectEinoAssistantCommitTool(name string) bool {
	switch projectToolBaseName(name) {
	case projectToolCommitProjectFiles, projectToolCommitFiles:
		return true
	default:
		return false
	}
}

func projectEinoAssistantPhaseApprovedPlan(
	req projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
) *projectAssistantApprovedPlan {
	if approvedPlan := runState.ApprovedPlan(); approvedPlan != nil {
		return approvedPlan
	}
	return req.InitialApprovedPlan
}

func projectEinoAssistantPhaseSuccessfulToolResult(message *schema.Message) bool {
	if message == nil || message.Role != schema.Tool || strings.TrimSpace(message.ToolName) == "" {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(message.Content))
	for _, prefix := range []string{
		"tool call failed:",
		"tool call denied:",
		"tool call skipped: waiting for approval",
		"permission denied:",
	} {
		if strings.HasPrefix(content, prefix) {
			return false
		}
	}
	return true
}

func projectEinoAssistantPhaseVerificationReady(content string) bool {
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &result); err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(result.Status)) {
	case "reachable", "ready", "available":
		return true
	default:
		return false
	}
}

func projectEinoAssistantPhaseFilterTools(
	phase projectEinoAssistantPhase,
	approvedPlan *projectAssistantApprovedPlan,
	tools []*schema.ToolInfo,
) []*schema.ToolInfo {
	if tools == nil {
		return nil
	}
	filtered := make([]*schema.ToolInfo, 0, len(tools))
	for _, tool := range tools {
		if projectEinoAssistantPhaseAllowsTool(phase, approvedPlan, tool) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func projectEinoAssistantPhaseMergeTools(existing, current []*schema.ToolInfo) []*schema.ToolInfo {
	if len(current) == 0 {
		return existing
	}
	merged := append([]*schema.ToolInfo(nil), existing...)
	for _, tool := range current {
		if tool == nil {
			continue
		}
		found := false
		for _, known := range merged {
			if known != nil && strings.EqualFold(strings.TrimSpace(known.Name), strings.TrimSpace(tool.Name)) {
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, tool)
		}
	}
	return merged
}

func projectEinoAssistantPhaseAllowsTool(
	phase projectEinoAssistantPhase,
	approvedPlan *projectAssistantApprovedPlan,
	tool *schema.ToolInfo,
) bool {
	if tool == nil {
		return false
	}
	name := projectToolBaseName(tool.Name)
	if name == projectEinoAssistantToolSearchTool {
		return phase == projectEinoAssistantPhaseApproval
	}
	if name == projectEinoAssistantWriteTodosTool {
		return (phase == projectEinoAssistantPhaseMutate || phase == projectEinoAssistantPhaseRepair) &&
			approvedPlan != nil && len(approvedPlan.Steps) > 1
	}
	risk, bundle, ok := projectEinoAssistantPhaseToolMetadata(tool)
	if !ok {
		return false
	}

	switch phase {
	case projectEinoAssistantPhaseApproval:
		if bundle == projectAssistantToolBundleRuntime {
			return false
		}
		return risk == projectAssistantToolRiskRead ||
			risk == projectAssistantToolRiskInput ||
			risk == projectAssistantToolRiskPlan
	case projectEinoAssistantPhaseMutate:
		return (projectEinoAssistantPhaseCanonicalEditTool(tool.Name) &&
			bundle == projectAssistantToolBundleEdit && risk == projectAssistantToolRiskWrite) ||
			(name == projectToolAskFollowUp && risk == projectAssistantToolRiskInput)
	case projectEinoAssistantPhaseVerify:
		return tool.Name == projectToolVerifyDevelopmentRuntime &&
			bundle == projectAssistantToolBundleRuntime &&
			risk == projectAssistantToolRiskRead
	case projectEinoAssistantPhaseRepair:
		return (bundle == projectAssistantToolBundleWorkspaceRead && risk == projectAssistantToolRiskRead) ||
			(projectEinoAssistantPhaseCanonicalEditTool(tool.Name) &&
				bundle == projectAssistantToolBundleEdit && risk == projectAssistantToolRiskWrite) ||
			(bundle == projectAssistantToolBundleRuntime &&
				(risk == projectAssistantToolRiskRead || risk == projectAssistantToolRiskRuntime)) ||
			(name == projectToolAskFollowUp && risk == projectAssistantToolRiskInput)
	case projectEinoAssistantPhaseCommit:
		return tool.Name == projectToolCommitProjectFiles &&
			bundle == projectAssistantToolBundleRepo &&
			risk == projectAssistantToolRiskCommit
	case projectEinoAssistantPhaseReport:
		return false
	default:
		return false
	}
}

func projectEinoAssistantPhaseCanonicalEditTool(name string) bool {
	switch name {
	case projectToolWriteFile, projectToolApplyPatch, projectToolMkdir:
		return true
	default:
		return false
	}
}

func projectEinoAssistantPhaseToolMetadata(tool *schema.ToolInfo) (projectAssistantToolRisk, projectAssistantToolBundle, bool) {
	if tool == nil || tool.Extra == nil {
		return "", "", false
	}
	risk, riskOK := tool.Extra["risk"].(string)
	bundle, bundleOK := tool.Extra["bundle"].(string)
	if !riskOK || !bundleOK {
		return "", "", false
	}
	return projectAssistantToolRisk(strings.TrimSpace(risk)), projectAssistantToolBundle(strings.TrimSpace(bundle)), true
}
