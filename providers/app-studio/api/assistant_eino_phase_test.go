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
	"testing"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestProjectEinoAssistantPhaseDerivation(t *testing.T) {
	tests := []struct {
		name     string
		req      projectAssistantRunRequest
		approved bool
		messages []*schema.Message
		want     projectEinoAssistantPhase
	}{
		{
			name: "no approved plan requires approval",
			want: projectEinoAssistantPhaseApproval,
		},
		{
			name:     "approved plan without workspace write mutates",
			approved: true,
			want:     projectEinoAssistantPhaseMutate,
		},
		{
			name:     "workspace write requires verification",
			approved: true,
			messages: []*schema.Message{projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`)},
			want:     projectEinoAssistantPhaseVerify,
		},
		{
			name:     "non-ready verification requires repair",
			approved: true,
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"provisioning"}`),
			},
			want: projectEinoAssistantPhaseRepair,
		},
		{
			name:     "reachable verification permits commit",
			approved: true,
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"reachable"}`),
			},
			want: projectEinoAssistantPhaseCommit,
		},
		{
			name: "reachable verification reports during initial project creation",
			req: projectAssistantRunRequest{
				InitialApprovedPlan: &projectAssistantApprovedPlan{Steps: []string{"create project"}},
			},
			approved: true,
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"available"}`),
			},
			want: projectEinoAssistantPhaseReport,
		},
		{
			name:     "successful commit reports completion",
			approved: true,
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"ready"}`),
				projectEinoAssistantPhaseToolResult(projectToolCommitProjectFiles, `{"commitSHA":"abc123"}`),
			},
			want: projectEinoAssistantPhaseReport,
		},
		{
			name:     "later write invalidates earlier verification",
			approved: true,
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"ready"}`),
				projectEinoAssistantPhaseToolResult(projectToolApplyPatch, `{"operation":"apply_patch"}`),
			},
			want: projectEinoAssistantPhaseVerify,
		},
		{
			name:     "later failed verification invalidates earlier reachable verification",
			approved: true,
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"ready"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, "Tool call failed: runtime unavailable"),
			},
			want: projectEinoAssistantPhaseRepair,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runState := newProjectEinoAssistantRunState()
			if tt.approved {
				runState.ApprovePlan(projectAssistantApprovedPlan{Steps: []string{"implement change"}})
			}
			state := &adk.ChatModelAgentState{Messages: tt.messages}
			if got := projectEinoAssistantPhaseForState(tt.req, runState, state); got != tt.want {
				t.Fatalf("phase = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProjectEinoAssistantPhasePreservesInitialCreationReportAfterResume(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(projectAssistantInitialCreationPlan())
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
		projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"reachable"}`),
	}}
	if got := projectEinoAssistantPhaseForState(projectAssistantRunRequest{}, runState, state); got != projectEinoAssistantPhaseReport {
		t.Fatalf("resumed initial-creation phase = %q, want report", got)
	}
}

func TestProjectEinoAssistantPhaseIgnoresUnsuccessfulToolResults(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(projectAssistantApprovedPlan{Steps: []string{"edit"}})
	tests := []struct {
		name     string
		messages []*schema.Message
		want     projectEinoAssistantPhase
	}{
		{
			name:     "denied write does not require verification",
			messages: []*schema.Message{projectEinoAssistantPhaseToolResult(projectToolWriteFile, "Tool call denied: user declined")},
			want:     projectEinoAssistantPhaseMutate,
		},
		{
			name: "permission barrier verification does not advance write",
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, "Tool call skipped: waiting for approval of a previous tool call"),
			},
			want: projectEinoAssistantPhaseVerify,
		},
		{
			name: "failed commit does not report completion",
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"ready"}`),
				projectEinoAssistantPhaseToolResult(projectToolCommitProjectFiles, "Tool call failed: permission denied"),
			},
			want: projectEinoAssistantPhaseCommit,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &adk.ChatModelAgentState{Messages: tt.messages}
			if got := projectEinoAssistantPhaseForState(projectAssistantRunRequest{}, runState, state); got != tt.want {
				t.Fatalf("phase = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProjectEinoAssistantPhaseMiddlewareFiltersTools(t *testing.T) {
	allTools := []*schema.ToolInfo{
		projectEinoAssistantPhaseToolInfo("read_workspace", projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead),
		projectEinoAssistantPhaseToolInfo("ask_for_input", projectAssistantToolRiskInput, projectAssistantToolBundleCollaboration),
		projectEinoAssistantPhaseToolInfo(projectToolRequestProjectPlanApproval, projectAssistantToolRiskPlan, projectAssistantToolBundleCollaboration),
		projectEinoAssistantPhaseToolInfo(projectToolPlanProjectChanges, projectAssistantToolRiskRead, projectAssistantToolBundleWorkflow),
		projectEinoAssistantPhaseToolInfo(projectToolWriteFile, projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
		projectEinoAssistantPhaseToolInfo(projectToolGetRuntimeStatus, projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
		projectEinoAssistantPhaseToolInfo(projectToolRestartRuntime, projectAssistantToolRiskRuntime, projectAssistantToolBundleRuntime),
		projectEinoAssistantPhaseToolInfo(projectToolVerifyDevelopmentRuntime, projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
		projectEinoAssistantPhaseToolInfo(projectToolCommitProjectFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
		projectEinoAssistantPhaseToolInfo("invalid_runtime_commit", projectAssistantToolRiskCommit, projectAssistantToolBundleRuntime),
		{Name: "write_todos"},
	}

	tests := []struct {
		name         string
		req          projectAssistantRunRequest
		approvedPlan *projectAssistantApprovedPlan
		messages     []*schema.Message
		want         []string
	}{
		{
			name: "approval only exposes read input and plan tools",
			want: []string{
				"read_workspace", "ask_for_input", projectToolRequestProjectPlanApproval, projectToolPlanProjectChanges,
			},
		},
		{
			name:         "mutate hides approval and commit while allowing multi-step todos",
			approvedPlan: &projectAssistantApprovedPlan{Steps: []string{"inspect", "edit"}},
			want: []string{
				"read_workspace", "ask_for_input", projectToolPlanProjectChanges, projectToolWriteFile,
				projectToolGetRuntimeStatus, projectToolRestartRuntime, projectToolVerifyDevelopmentRuntime, "write_todos",
			},
		},
		{
			name:         "verify exposes verifier and repair-capable edit runtime tools",
			approvedPlan: &projectAssistantApprovedPlan{Steps: []string{"edit"}},
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
			},
			want: []string{
				projectToolWriteFile, projectToolGetRuntimeStatus, projectToolRestartRuntime, projectToolVerifyDevelopmentRuntime,
			},
		},
		{
			name:         "repair includes diagnostic workspace and runtime reads",
			approvedPlan: &projectAssistantApprovedPlan{Steps: []string{"inspect", "repair"}},
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"not_ready"}`),
			},
			want: []string{
				"read_workspace", projectToolWriteFile, projectToolGetRuntimeStatus, projectToolRestartRuntime,
				projectToolVerifyDevelopmentRuntime, "write_todos",
			},
		},
		{
			name:         "commit exposes only the commit tool",
			approvedPlan: &projectAssistantApprovedPlan{Steps: []string{"edit"}},
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"ready"}`),
			},
			want: []string{projectToolCommitProjectFiles},
		},
		{
			name: "report exposes no tools after initial creation verification",
			req: projectAssistantRunRequest{
				InitialApprovedPlan: &projectAssistantApprovedPlan{Steps: []string{"create"}},
			},
			approvedPlan: &projectAssistantApprovedPlan{Steps: []string{"create"}},
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"ready"}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runState := newProjectEinoAssistantRunState()
			if tt.approvedPlan != nil {
				runState.ApprovePlan(*tt.approvedPlan)
			}
			state := &adk.ChatModelAgentState{
				Messages:          tt.messages,
				ToolInfos:         append([]*schema.ToolInfo(nil), allTools...),
				DeferredToolInfos: append([]*schema.ToolInfo(nil), allTools...),
			}
			middleware := projectEinoAssistantPhaseMiddleware(tt.req, runState)
			_, state, err := middleware.BeforeModelRewriteState(context.Background(), state, nil)
			if err != nil {
				t.Fatalf("BeforeModelRewriteState returned error: %v", err)
			}
			if got := projectEinoAssistantPhaseToolNames(state.ToolInfos); !projectEinoAssistantPhaseStringSlicesEqual(got, tt.want) {
				t.Fatalf("tool infos = %#v, want %#v", got, tt.want)
			}
			if got := projectEinoAssistantPhaseToolNames(state.DeferredToolInfos); !projectEinoAssistantPhaseStringSlicesEqual(got, tt.want) {
				t.Fatalf("deferred tool infos = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestProjectEinoAssistantPhaseMiddlewareGatesHiddenToolExecution(t *testing.T) {
	tests := []struct {
		name         string
		phase        projectEinoAssistantPhase
		approvedPlan *projectAssistantApprovedPlan
		tool         *schema.ToolInfo
		wantCalls    int
		wantResult   string
	}{
		{
			name:       "approval rejects hidden todo",
			phase:      projectEinoAssistantPhaseApproval,
			tool:       &schema.ToolInfo{Name: projectEinoAssistantWriteTodosTool},
			wantResult: "Tool call denied: write_todos is unavailable in the current assistant phase",
		},
		{
			name:       "verify rejects hidden commit",
			phase:      projectEinoAssistantPhaseVerify,
			tool:       projectEinoAssistantPhaseToolInfo(projectToolCommitProjectFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			wantResult: "Tool call denied: commit_project_files is unavailable in the current assistant phase",
		},
		{
			name:       "verify rejects transformed hidden commit",
			phase:      projectEinoAssistantPhaseVerify,
			tool:       projectEinoAssistantPhaseToolInfo(projectToolCodeCommitFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			wantResult: "Tool call denied: commit_files is unavailable in the current assistant phase",
		},
		{
			name:       "commit executes verified commit",
			phase:      projectEinoAssistantPhaseCommit,
			tool:       projectEinoAssistantPhaseToolInfo(projectToolCommitProjectFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			wantCalls:  1,
			wantResult: "todo recorded",
		},
		{
			name:       "commit executes transformed verified commit",
			phase:      projectEinoAssistantPhaseCommit,
			tool:       projectEinoAssistantPhaseToolInfo(projectToolCodeCommitFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			wantCalls:  1,
			wantResult: "todo recorded",
		},
		{
			name:       "initial creation report rejects hidden commit",
			phase:      projectEinoAssistantPhaseReport,
			tool:       projectEinoAssistantPhaseToolInfo(projectToolCommitProjectFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			wantResult: "Tool call denied: commit_project_files is unavailable in the current assistant phase",
		},
		{
			name:  "one-step mutate rejects hidden todo",
			phase: projectEinoAssistantPhaseMutate,
			tool:  &schema.ToolInfo{Name: projectEinoAssistantWriteTodosTool},
			approvedPlan: &projectAssistantApprovedPlan{
				Steps: []string{"make the small change"},
			},
			wantResult: "Tool call denied: write_todos is unavailable in the current assistant phase",
		},
		{
			name:  "multi-step mutate executes todo",
			phase: projectEinoAssistantPhaseMutate,
			tool:  &schema.ToolInfo{Name: projectEinoAssistantWriteTodosTool},
			approvedPlan: &projectAssistantApprovedPlan{
				Steps: []string{"inspect", "edit"},
			},
			wantCalls:  1,
			wantResult: "todo recorded",
		},
		{
			name:  "multi-step repair executes todo",
			phase: projectEinoAssistantPhaseRepair,
			tool:  &schema.ToolInfo{Name: projectEinoAssistantWriteTodosTool},
			approvedPlan: &projectAssistantApprovedPlan{
				Steps: []string{"diagnose", "repair"},
			},
			wantCalls:  1,
			wantResult: "todo recorded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware := &projectEinoAssistantPhaseFilterMiddleware{
				BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
				phase:                        tt.phase,
				approvedPlan:                 tt.approvedPlan,
			}
			calls := 0
			wrapped, err := middleware.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...einotool.Option) (string, error) {
				calls++
				return "todo recorded", nil
			}, &adk.ToolContext{Name: tt.tool.Name})
			if err != nil {
				t.Fatalf("WrapInvokableToolCall returned error: %v", err)
			}
			result, err := wrapped(context.Background(), `{"todos":[]}`)
			if err != nil {
				t.Fatalf("wrapped %s returned error: %v", tt.tool.Name, err)
			}
			if result != tt.wantResult {
				t.Fatalf("wrapped %s result = %q, want %q", tt.tool.Name, result, tt.wantResult)
			}
			if calls != tt.wantCalls {
				t.Fatalf("inner %s calls = %d, want %d", tt.tool.Name, calls, tt.wantCalls)
			}
		})
	}
}

func TestProjectEinoAssistantPhaseMiddlewareRestoresVerifiedCommitPhaseForResume(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	middleware := projectEinoAssistantPhaseMiddleware(projectAssistantRunRequest{
		Continuation: &projectAssistantCheckpointState{Messages: []chatMessage{
			{Role: string(schema.Tool), Name: projectToolWriteFile, Content: `{"operation":"write_file"}`},
			{Role: string(schema.Tool), Name: projectToolVerifyDevelopmentRuntime, Content: `{"status":"ready"}`},
		}, Eino: &projectAssistantEinoCheckpointState{ToolName: projectToolCommitProjectFiles}},
	}, runState).(*projectEinoAssistantPhaseFilterMiddleware)
	if middleware.phase != projectEinoAssistantPhaseCommit {
		t.Fatalf("resumed phase = %q, want commit", middleware.phase)
	}
	if middleware.approvedPlan != nil || runState.ApprovedPlan() != nil {
		t.Fatalf("resumed approval = %#v, want commit resume without restoring the consumed plan", middleware.approvedPlan)
	}
	calls := 0
	wrapped, err := middleware.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...einotool.Option) (string, error) {
		calls++
		return "commit requested", nil
	}, &adk.ToolContext{Name: projectToolCommitProjectFiles})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall returned error: %v", err)
	}
	result, err := wrapped(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("wrapped commit returned error: %v", err)
	}
	if result != "commit requested" || calls != 1 {
		t.Fatalf("resumed commit result = %q calls = %d, want execution", result, calls)
	}
}

func TestProjectEinoAssistantPhaseMiddlewareRejectsUnverifiedCommitResume(t *testing.T) {
	middleware := projectEinoAssistantPhaseMiddleware(projectAssistantRunRequest{
		Continuation: &projectAssistantCheckpointState{
			Messages: []chatMessage{
				{Role: string(schema.Tool), Name: projectToolWriteFile, Content: `{"operation":"write_file"}`},
			},
			Eino: &projectAssistantEinoCheckpointState{ToolName: projectToolCommitProjectFiles},
		},
	}, newProjectEinoAssistantRunState()).(*projectEinoAssistantPhaseFilterMiddleware)
	calls := 0
	wrapped, err := middleware.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...einotool.Option) (string, error) {
		calls++
		return "commit requested", nil
	}, &adk.ToolContext{Name: projectToolCommitProjectFiles})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall returned error: %v", err)
	}
	result, err := wrapped(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("wrapped commit returned error: %v", err)
	}
	if result != "Tool call denied: commit_project_files is unavailable in the current assistant phase" || calls != 0 {
		t.Fatalf("unverified resumed commit result = %q calls = %d, want denial", result, calls)
	}
}

func TestProjectEinoAssistantPhaseMiddlewareRestoresToolsAfterApproval(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	middleware := projectEinoAssistantPhaseMiddleware(projectAssistantRunRequest{}, runState)
	state := &adk.ChatModelAgentState{ToolInfos: []*schema.ToolInfo{
		projectEinoAssistantPhaseToolInfo(projectToolRequestProjectPlanApproval, projectAssistantToolRiskPlan, projectAssistantToolBundleCollaboration),
		projectEinoAssistantPhaseToolInfo(projectToolWriteFile, projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
	}}

	_, state, err := middleware.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("approval filtering returned error: %v", err)
	}
	if got := projectEinoAssistantPhaseToolNames(state.ToolInfos); !projectEinoAssistantPhaseStringSlicesEqual(got, []string{projectToolRequestProjectPlanApproval}) {
		t.Fatalf("approval tool infos = %#v, want only approval tool", got)
	}

	runState.ApprovePlan(projectAssistantApprovedPlan{Steps: []string{"edit"}})
	_, state, err = middleware.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("mutate filtering returned error: %v", err)
	}
	if got := projectEinoAssistantPhaseToolNames(state.ToolInfos); !projectEinoAssistantPhaseStringSlicesEqual(got, []string{projectToolWriteFile}) {
		t.Fatalf("mutate tool infos = %#v, want recovered workspace write", got)
	}
}

func TestProjectEinoAssistantPhaseMiddlewareRestoresCanonicalToolsAfterResume(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	tools := []einotool.BaseTool{
		projectEinoAssistantPhaseBaseTool(projectToolRequestProjectPlanApproval, projectAssistantToolRiskPlan, projectAssistantToolBundleCollaboration),
		projectEinoAssistantPhaseBaseTool(projectToolWriteFile, projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
	}
	runCtx := &adk.ChatModelAgentContext{Tools: tools}
	persistedApprovalState := &adk.ChatModelAgentState{ToolInfos: []*schema.ToolInfo{
		projectEinoAssistantPhaseToolInfo(projectToolRequestProjectPlanApproval, projectAssistantToolRiskPlan, projectAssistantToolBundleCollaboration),
	}}

	resumedMiddleware := projectEinoAssistantPhaseMiddleware(projectAssistantRunRequest{}, runState)
	if _, _, err := resumedMiddleware.BeforeAgent(context.Background(), runCtx); err != nil {
		t.Fatalf("BeforeAgent returned error: %v", err)
	}
	runState.ApprovePlan(projectAssistantApprovedPlan{Steps: []string{"edit"}})
	_, state, err := resumedMiddleware.BeforeModelRewriteState(context.Background(), persistedApprovalState, nil)
	if err != nil {
		t.Fatalf("mutate filtering returned error: %v", err)
	}
	if got := projectEinoAssistantPhaseToolNames(state.ToolInfos); !projectEinoAssistantPhaseStringSlicesEqual(got, []string{projectToolWriteFile}) {
		t.Fatalf("resumed mutate tool infos = %#v, want recovered workspace write without plan tool", got)
	}
}

func TestProjectEinoAssistantPhaseAllowsToolSearchOnlyWhileDiscoveryCanAdvanceWork(t *testing.T) {
	toolSearch := &schema.ToolInfo{Name: "tool_search"}
	for _, tt := range []struct {
		phase projectEinoAssistantPhase
		want  bool
	}{
		{phase: projectEinoAssistantPhaseApproval, want: true},
		{phase: projectEinoAssistantPhaseMutate, want: true},
		{phase: projectEinoAssistantPhaseVerify, want: true},
		{phase: projectEinoAssistantPhaseRepair, want: true},
		{phase: projectEinoAssistantPhaseCommit, want: false},
		{phase: projectEinoAssistantPhaseReport, want: false},
	} {
		t.Run(string(tt.phase), func(t *testing.T) {
			if got := projectEinoAssistantPhaseAllowsTool(tt.phase, nil, toolSearch); got != tt.want {
				t.Fatalf("tool_search allowed = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestProjectEinoAssistantPhaseVisibleToolsKeepsOnlySelectedSearchableTools(t *testing.T) {
	static := projectEinoAssistantPhaseToolInfo(projectToolReadProjectFile, projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead)
	selected := projectEinoAssistantPhaseSearchableToolInfo("provider__selected")
	unselected := projectEinoAssistantPhaseSearchableToolInfo("provider__unselected")
	visible := projectEinoAssistantPhaseVisibleTools(
		[]*schema.ToolInfo{static, selected, unselected},
		[]*schema.ToolInfo{static, selected},
	)
	if got := projectEinoAssistantPhaseToolNames(visible); !projectEinoAssistantPhaseStringSlicesEqual(got, []string{projectToolReadProjectFile, "provider__selected"}) {
		t.Fatalf("visible tools = %#v, want static tools and the selected searchable tool", got)
	}
}

func projectEinoAssistantPhaseToolResult(name, content string) *schema.Message {
	return schema.ToolMessage(content, "call-"+name, schema.WithToolName(name))
}

func projectEinoAssistantPhaseToolInfo(name string, risk projectAssistantToolRisk, bundle projectAssistantToolBundle) *schema.ToolInfo {
	return &schema.ToolInfo{Extra: map[string]any{
		"bundle": string(bundle),
		"risk":   string(risk),
	}, Name: name}
}

func projectEinoAssistantPhaseBaseTool(name string, risk projectAssistantToolRisk, bundle projectAssistantToolBundle) einotool.BaseTool {
	return projectEinoAssistantTool{tool: projectAssistantToolFunc{spec: projectAssistantToolSpec{
		Name:        name,
		Risk:        risk,
		Description: string(bundle),
	}}}
}

func projectEinoAssistantPhaseSearchableToolInfo(name string) *schema.ToolInfo {
	tool := projectEinoAssistantPhaseToolInfo(name, projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead)
	tool.Extra[projectEinoToolSearchableExtraKey] = true
	return tool
}

func projectEinoAssistantPhaseToolNames(tools []*schema.ToolInfo) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			names = append(names, tool.Name)
		}
	}
	return names
}

func projectEinoAssistantPhaseStringSlicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
