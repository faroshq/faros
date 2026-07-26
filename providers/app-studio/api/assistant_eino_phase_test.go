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

	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
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
		projectEinoAssistantPhaseToolInfo(projectToolListProjectFiles, projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead),
		projectEinoAssistantPhaseToolInfo(projectToolSearchProjectFiles, projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead),
		projectEinoAssistantPhaseToolInfo("ask_for_input", projectAssistantToolRiskInput, projectAssistantToolBundleCollaboration),
		projectEinoAssistantPhaseToolInfo(projectToolAskFollowUp, projectAssistantToolRiskInput, projectAssistantToolBundleCollaboration),
		projectEinoAssistantPhaseToolInfo(projectToolRequestProjectPlanApproval, projectAssistantToolRiskPlan, projectAssistantToolBundleCollaboration),
		projectEinoAssistantPhaseToolInfo(projectToolPlanProjectChanges, projectAssistantToolRiskRead, projectAssistantToolBundleWorkflow),
		projectEinoAssistantPhaseToolInfo(projectToolCheckProjectReadiness, projectAssistantToolRiskRead, projectAssistantToolBundleWorkflow),
		projectEinoAssistantPhaseToolInfo(projectToolWriteFile, projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
		projectEinoAssistantPhaseToolInfo(projectToolApplyPatch, projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
		projectEinoAssistantPhaseToolInfo(projectToolGetRuntimeStatus, projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
		projectEinoAssistantPhaseToolInfo(projectToolRestartRuntime, projectAssistantToolRiskRuntime, projectAssistantToolBundleRuntime),
		projectEinoAssistantPhaseToolInfo(projectToolSetRuntimeEnv, projectAssistantToolRiskRuntime, projectAssistantToolBundleRuntime),
		projectEinoAssistantPhaseToolInfo(projectToolVerifyDevelopmentRuntime, projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
		projectEinoAssistantPhaseToolInfo(projectToolCommitProjectFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
		projectEinoAssistantPhaseToolInfo(projectToolCommitFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
		projectEinoAssistantPhaseToolInfo("invalid_runtime_commit", projectAssistantToolRiskCommit, projectAssistantToolBundleRuntime),
		{Name: projectEinoAssistantWriteTodosTool},
		{Name: projectEinoAssistantToolSearchTool},
	}

	tests := []struct {
		name         string
		req          projectAssistantRunRequest
		approvedPlan *projectAssistantApprovedPlan
		messages     []*schema.Message
		want         []string
	}{
		{
			name: "approval exposes workspace reads input and plan tools",
			want: []string{
				"read_workspace", projectToolListProjectFiles, projectToolSearchProjectFiles,
				"ask_for_input", projectToolAskFollowUp, projectToolRequestProjectPlanApproval,
				projectToolPlanProjectChanges, projectToolCheckProjectReadiness, projectEinoAssistantToolSearchTool,
			},
		},
		{
			name:         "mutate exposes only edits follow-up and multi-step todos",
			approvedPlan: &projectAssistantApprovedPlan{Steps: []string{"inspect", "edit"}},
			want: []string{
				projectToolAskFollowUp, projectToolWriteFile, projectToolApplyPatch, projectEinoAssistantWriteTodosTool,
			},
		},
		{
			name:         "verify exposes only the runtime verifier",
			approvedPlan: &projectAssistantApprovedPlan{Steps: []string{"edit"}},
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
			},
			want: []string{projectToolVerifyDevelopmentRuntime},
		},
		{
			name:         "repair exposes targeted reads edits runtime tools follow-up and todos",
			approvedPlan: &projectAssistantApprovedPlan{Steps: []string{"inspect", "repair"}},
			messages: []*schema.Message{
				projectEinoAssistantPhaseToolResult(projectToolWriteFile, `{"operation":"write_file"}`),
				projectEinoAssistantPhaseToolResult(projectToolVerifyDevelopmentRuntime, `{"status":"not_ready"}`),
			},
			want: []string{
				"read_workspace", projectToolListProjectFiles, projectToolSearchProjectFiles,
				projectToolAskFollowUp, projectToolWriteFile, projectToolApplyPatch,
				projectToolGetRuntimeStatus, projectToolRestartRuntime, projectToolSetRuntimeEnv,
				projectToolVerifyDevelopmentRuntime, projectEinoAssistantWriteTodosTool,
			},
		},
		{
			name:         "commit exposes only commit project files",
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

func TestProjectEinoAssistantPhaseRealFactoryInventoryAllowsOnlyCanonicalMutationTools(t *testing.T) {
	tools := projectEinoAssistantPhaseFactoryToolInfos(t)
	inventoryNames := projectEinoAssistantPhaseToolNames(tools)
	if !projectEinoAssistantPhaseToolNamesContain(inventoryNames, projectToolSelectTemplate) {
		t.Fatalf("factory inventory = %#v, want %s represented in the real inventory", inventoryNames, projectToolSelectTemplate)
	}
	if !projectEinoAssistantPhaseToolNamesContain(inventoryNames, projectToolHydrateWorkspace) {
		t.Fatalf("factory inventory = %#v, want %s represented in the real inventory", inventoryNames, projectToolHydrateWorkspace)
	}

	approvedPlan := &projectAssistantApprovedPlan{Steps: []string{"inspect", "edit"}}
	for _, tt := range []struct {
		phase projectEinoAssistantPhase
		want  []string
	}{
		{
			phase: projectEinoAssistantPhaseMutate,
			want: []string{
				projectToolAskFollowUp, projectToolWriteFile, projectToolApplyPatch, projectToolMkdir, projectToolSelectTemplate,
			},
		},
		{
			phase: projectEinoAssistantPhaseRepair,
			want:  []string{projectToolWriteFile, projectToolApplyPatch, projectToolMkdir, projectToolSelectTemplate},
		},
	} {
		t.Run(string(tt.phase), func(t *testing.T) {
			filtered := projectEinoAssistantPhaseFilterTools(tt.phase, approvedPlan, tools)
			got := projectEinoAssistantPhaseToolNames(filtered)
			if projectEinoAssistantPhaseToolNamesContain(got, projectToolHydrateWorkspace) {
				t.Fatalf("%s tools = %#v, want %s excluded", tt.phase, got, projectToolHydrateWorkspace)
			}
			for _, want := range tt.want {
				if !projectEinoAssistantPhaseToolNamesContain(got, want) {
					t.Fatalf("%s tools = %#v, want canonical tool %s", tt.phase, got, want)
				}
			}
			for _, tool := range filtered {
				risk, bundle, ok := projectEinoAssistantPhaseToolMetadata(tool)
				if ok && bundle == projectAssistantToolBundleInfrastructure {
					t.Fatalf("%s exposed infrastructure tool %q from real factory inventory", tt.phase, tool.Name)
				}
				if ok && bundle == projectAssistantToolBundleWorkflow && tool.Name != projectToolSelectTemplate {
					t.Fatalf("%s exposed non-bootstrap workflow tool %q from real factory inventory", tt.phase, tool.Name)
				}
				if !ok || risk != projectAssistantToolRiskWrite || bundle != projectAssistantToolBundleEdit {
					continue
				}
				switch tool.Name {
				case projectToolWriteFile, projectToolApplyPatch, projectToolMkdir:
				default:
					t.Fatalf("%s exposed noncanonical edit tool %q from real factory inventory", tt.phase, tool.Name)
				}
			}
			if tt.phase == projectEinoAssistantPhaseMutate &&
				!projectEinoAssistantPhaseStringSlicesEqual(got, tt.want) {
				t.Fatalf("mutate tools = %#v, want only %#v", got, tt.want)
			}
		})
	}
}

func TestProjectEinoAssistantPhaseRequiresCanonicalExclusiveToolMetadata(t *testing.T) {
	tests := []struct {
		name  string
		phase projectEinoAssistantPhase
		tool  *schema.ToolInfo
		want  bool
	}{
		{
			name:  "mutate allows canonical write",
			phase: projectEinoAssistantPhaseMutate,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolWriteFile, projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
			want:  true,
		},
		{
			name:  "mutate rejects namespaced write",
			phase: projectEinoAssistantPhaseMutate,
			tool:  projectEinoAssistantPhaseToolInfo("provider__write_file", projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
		},
		{
			name:  "mutate rejects hydrate workspace",
			phase: projectEinoAssistantPhaseMutate,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolHydrateWorkspace, projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
		},
		{
			name:  "mutate allows canonical template bootstrap",
			phase: projectEinoAssistantPhaseMutate,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolSelectTemplate, projectAssistantToolRiskWrite, projectAssistantToolBundleWorkflow),
			want:  true,
		},
		{
			name:  "mutate rejects namespaced template bootstrap",
			phase: projectEinoAssistantPhaseMutate,
			tool:  projectEinoAssistantPhaseToolInfo("provider__select_project_template", projectAssistantToolRiskWrite, projectAssistantToolBundleWorkflow),
		},
		{
			name:  "mutate rejects template bootstrap with infrastructure bundle",
			phase: projectEinoAssistantPhaseMutate,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolSelectTemplate, projectAssistantToolRiskWrite, projectAssistantToolBundleInfrastructure),
		},
		{
			name:  "repair allows canonical template bootstrap",
			phase: projectEinoAssistantPhaseRepair,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolSelectTemplate, projectAssistantToolRiskWrite, projectAssistantToolBundleWorkflow),
			want:  true,
		},
		{
			name:  "repair rejects case template bootstrap lookalike",
			phase: projectEinoAssistantPhaseRepair,
			tool:  projectEinoAssistantPhaseToolInfo("SELECT_PROJECT_TEMPLATE", projectAssistantToolRiskWrite, projectAssistantToolBundleWorkflow),
		},
		{
			name:  "repair rejects namespaced mkdir",
			phase: projectEinoAssistantPhaseRepair,
			tool:  projectEinoAssistantPhaseToolInfo("provider__mkdir", projectAssistantToolRiskWrite, projectAssistantToolBundleEdit),
		},
		{
			name:  "verify allows canonical verifier metadata",
			phase: projectEinoAssistantPhaseVerify,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolVerifyDevelopmentRuntime, projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
			want:  true,
		},
		{
			name:  "verify rejects namespaced verifier",
			phase: projectEinoAssistantPhaseVerify,
			tool:  projectEinoAssistantPhaseToolInfo("provider__verify_development_runtime", projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
		},
		{
			name:  "verify rejects case lookalike",
			phase: projectEinoAssistantPhaseVerify,
			tool:  projectEinoAssistantPhaseToolInfo("VERIFY_DEVELOPMENT_RUNTIME", projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
		},
		{
			name:  "verify rejects wrong risk",
			phase: projectEinoAssistantPhaseVerify,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolVerifyDevelopmentRuntime, projectAssistantToolRiskRuntime, projectAssistantToolBundleRuntime),
		},
		{
			name:  "verify rejects wrong bundle",
			phase: projectEinoAssistantPhaseVerify,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolVerifyDevelopmentRuntime, projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead),
		},
		{
			name:  "commit allows canonical commit metadata",
			phase: projectEinoAssistantPhaseCommit,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolCommitProjectFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			want:  true,
		},
		{
			name:  "commit rejects namespaced commit",
			phase: projectEinoAssistantPhaseCommit,
			tool:  projectEinoAssistantPhaseToolInfo("code__commit_project_files", projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
		},
		{
			name:  "commit rejects case lookalike",
			phase: projectEinoAssistantPhaseCommit,
			tool:  projectEinoAssistantPhaseToolInfo("COMMIT_PROJECT_FILES", projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
		},
		{
			name:  "commit rejects wrong bundle",
			phase: projectEinoAssistantPhaseCommit,
			tool:  projectEinoAssistantPhaseToolInfo(projectToolCommitProjectFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRuntime),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := projectEinoAssistantPhaseAllowsTool(tt.phase, nil, tt.tool); got != tt.want {
				t.Fatalf("allows %q = %t, want %t", tt.tool.Name, got, tt.want)
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
			name:       "commit rejects transformed commit",
			phase:      projectEinoAssistantPhaseCommit,
			tool:       projectEinoAssistantPhaseToolInfo(projectToolCodeCommitFiles, projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			wantResult: "Tool call denied: commit_files is unavailable in the current assistant phase",
		},
		{
			name:       "commit rejects namespaced canonical lookalike",
			phase:      projectEinoAssistantPhaseCommit,
			tool:       projectEinoAssistantPhaseToolInfo("code__commit_project_files", projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			wantResult: "Tool call denied: commit_project_files is unavailable in the current assistant phase",
		},
		{
			name:       "commit rejects case canonical lookalike",
			phase:      projectEinoAssistantPhaseCommit,
			tool:       projectEinoAssistantPhaseToolInfo("COMMIT_PROJECT_FILES", projectAssistantToolRiskCommit, projectAssistantToolBundleRepo),
			wantResult: "Tool call denied: commit_project_files is unavailable in the current assistant phase",
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

func TestProjectEinoAssistantPhaseMiddlewareRestoresReadOnlyPolicyToolsFromLegacyCheckpoint(t *testing.T) {
	tools := []einotool.BaseTool{
		projectEinoAssistantPhaseBaseTool(projectToolReadProjectFile, projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead),
		projectEinoAssistantPhaseBaseTool(projectToolGetRuntimeStatus, projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
		projectEinoAssistantPhaseBaseTool(projectToolGetPreviewURL, projectAssistantToolRiskRead, projectAssistantToolBundleRuntime),
	}
	runCtx := &adk.ChatModelAgentContext{Tools: tools}
	persistedPrunedState := &adk.ChatModelAgentState{ToolInfos: []*schema.ToolInfo{
		projectEinoAssistantPhaseToolInfo(projectToolReadProjectFile, projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead),
	}}
	middleware := projectEinoAssistantPhaseMiddleware(projectAssistantRunRequest{
		TurnPolicy: projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileDebugging),
	}, newProjectEinoAssistantRunState())
	if _, _, err := middleware.BeforeAgent(context.Background(), runCtx); err != nil {
		t.Fatalf("BeforeAgent returned error: %v", err)
	}

	_, state, err := middleware.BeforeModelRewriteState(context.Background(), persistedPrunedState, nil)
	if err != nil {
		t.Fatalf("BeforeModelRewriteState returned error: %v", err)
	}
	want := []string{projectToolReadProjectFile, projectToolGetRuntimeStatus, projectToolGetPreviewURL}
	if got := projectEinoAssistantPhaseToolNames(state.ToolInfos); !projectEinoAssistantPhaseStringSlicesEqual(got, want) {
		t.Fatalf("restored read-only tools = %#v, want legacy checkpoint restored to %#v", got, want)
	}
}

func TestProjectEinoAssistantPhaseAllowsToolSearchOnlyWhileDiscoveryCanAdvanceWork(t *testing.T) {
	toolSearch := &schema.ToolInfo{Name: "tool_search"}
	for _, tt := range []struct {
		phase projectEinoAssistantPhase
		want  bool
	}{
		{phase: projectEinoAssistantPhaseApproval, want: true},
		{phase: projectEinoAssistantPhaseMutate, want: false},
		{phase: projectEinoAssistantPhaseVerify, want: false},
		{phase: projectEinoAssistantPhaseRepair, want: false},
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

func projectEinoAssistantPhaseFactoryToolInfos(t *testing.T) []*schema.ToolInfo {
	t.Helper()
	server := NewWithWorkspace(nil, store.NewMemoryStore(), workspace.NewFileStore(t.TempDir()), "", false)
	runState := newProjectEinoAssistantRunState()
	runState.SetToolDiscovery(projectEinoAssistantToolDiscovery{IncludeCommitBridge: true})
	req := projectAssistantRunRequest{
		TurnProfile: projectAssistantTurnProfileImplementation,
		TurnPolicy:  projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileImplementation),
	}
	tools, err := newProjectEinoAssistantToolsFactory(server)(context.Background(), req, runState)
	if err != nil {
		t.Fatalf("new factory tools returned error: %v", err)
	}
	infos := make([]*schema.ToolInfo, 0, len(tools))
	for _, tool := range tools {
		info, err := tool.Info(context.Background())
		if err != nil {
			t.Fatalf("factory tool Info returned error: %v", err)
		}
		infos = append(infos, info)
	}
	return infos
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

func projectEinoAssistantPhaseToolNamesContain(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
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
