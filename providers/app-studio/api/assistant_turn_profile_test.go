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
	"strings"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	einoschema "github.com/cloudwego/eino/schema"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/store"
)

func TestProjectAssistantTurnProfileClassifier(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    projectAssistantTurnProfile
	}{
		{name: "discussion", message: "I am thinking about whether this product direction makes sense", want: projectAssistantTurnProfileDiscussion},
		{name: "guidance", message: "How should I design authentication for this app?", want: projectAssistantTurnProfileGuidance},
		{name: "exploration", message: "What files are in my current app?", want: projectAssistantTurnProfileExploration},
		{name: "debugging", message: "Why is the preview not working and showing Failed to fetch? Diagnose it only.", want: projectAssistantTurnProfileDebugging},
		{name: "exact declarative breakage report", message: "I just tried to use the queue custom toast but it didnt work", want: projectAssistantTurnProfileDebugFix},
		{name: "implicit debug fix", message: "I click the button to make it dark mode but it didnt do anything", want: projectAssistantTurnProfileDebugFix},
		{name: "debug fix", message: "Fix the failed fetch error and make it work", want: projectAssistantTurnProfileDebugFix},
		{name: "fix only fallback", message: "Please fix the login form", want: projectAssistantTurnProfileDebugFix},
		{name: "implementation", message: "Add a search field to the todo app", want: projectAssistantTurnProfileImplementation},
		{name: "git push", message: "push my changes to git", want: projectAssistantTurnProfileImplementation},
		{name: "pull request", message: "open a pull request for this branch", want: projectAssistantTurnProfileImplementation},
		{name: "merge", message: "merge the feature branch", want: projectAssistantTurnProfileImplementation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyProjectAssistantTurnProfile([]store.Message{{
				Role:    aiv1alpha1.ProjectMessageRoleUser,
				Content: tt.message,
			}})
			if got != tt.want {
				t.Fatalf("profile = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProjectAssistantSemanticTurnClassifierUsesStructuredModelDecision(t *testing.T) {
	model := &repositoryFlowEinoChatModel{Steps: []repositoryFlowEinoModelStep{{
		Message: einoschema.AssistantMessage(`{"profile":"debug_fix","requires_current_state":true,"requires_runtime_state":true,"requests_mutation":true,"confidence":"high"}`, nil),
	}}}

	decision, err := classifyProjectAssistantTurnWithModel(context.Background(), model, []store.Message{{
		Role:    aiv1alpha1.ProjectMessageRoleUser,
		Content: "Please make the sign-in flow behave again",
	}})
	if err != nil {
		t.Fatalf("classifyProjectAssistantTurnWithModel returned error: %v", err)
	}
	if decision.Profile != projectAssistantTurnProfileDebugFix {
		t.Fatalf("profile = %q, want debug_fix", decision.Profile)
	}
	if !decision.RequiresCurrentState || !decision.RequiresRuntimeState || !decision.RequestsMutation {
		t.Fatalf("decision = %#v, want structured state and mutation flags", decision)
	}
	if decision.Confidence != projectAssistantTurnConfidenceHigh {
		t.Fatalf("confidence = %q, want high", decision.Confidence)
	}
	if len(model.Inputs) != 1 {
		t.Fatalf("model inputs = %d, want 1", len(model.Inputs))
	}
	if got := model.Inputs[0].ToolChoice; got != "none" {
		t.Fatalf("tool choice = %q, want none for classifier", got)
	}
	if len(model.Inputs[0].Tools) != 0 {
		t.Fatalf("classifier tools = %#v, want none", model.Inputs[0].Tools)
	}
}

func TestProjectAssistantSemanticTurnClassifierFallsBackOnLowConfidence(t *testing.T) {
	model := &repositoryFlowEinoChatModel{Steps: []repositoryFlowEinoModelStep{{
		Message: einoschema.AssistantMessage(`{"profile":"discussion","confidence":"low"}`, nil),
	}}}

	decision, err := classifyProjectAssistantTurnWithModel(context.Background(), model, []store.Message{{
		Role:    aiv1alpha1.ProjectMessageRoleUser,
		Content: "Please fix the login form",
	}})
	if err != nil {
		t.Fatalf("classifyProjectAssistantTurnWithModel returned error: %v", err)
	}
	if decision.Profile != projectAssistantTurnProfileDebugFix {
		t.Fatalf("profile = %q, want fallback debug_fix", decision.Profile)
	}
}

func TestProjectAssistantSemanticTurnClassifierNormalizesInconsistentMutationDecision(t *testing.T) {
	model := &repositoryFlowEinoChatModel{Steps: []repositoryFlowEinoModelStep{{
		Message: einoschema.AssistantMessage(`{"profile":"discussion","requires_current_state":true,"requires_runtime_state":false,"requests_mutation":true,"confidence":"high"}`, nil),
	}}}

	decision, err := classifyProjectAssistantTurnWithModel(context.Background(), model, []store.Message{{
		Role:    aiv1alpha1.ProjectMessageRoleUser,
		Content: "Please fix the login form",
	}})
	if err != nil {
		t.Fatalf("classifyProjectAssistantTurnWithModel returned error: %v", err)
	}
	if decision.Profile != projectAssistantTurnProfileDebugFix {
		t.Fatalf("profile = %q, want debug_fix from mutation fallback", decision.Profile)
	}
	if !decision.RequestsMutation {
		t.Fatalf("decision = %#v, want mutation preserved", decision)
	}
}

func TestProjectAssistantSemanticTurnClassifierPreservesMutationFallback(t *testing.T) {
	model := &repositoryFlowEinoChatModel{Steps: []repositoryFlowEinoModelStep{{
		Message: einoschema.AssistantMessage(`{"profile":"debugging","requires_current_state":true,"requires_runtime_state":false,"requests_mutation":false,"confidence":"high"}`, nil),
	}}}

	decision, err := classifyProjectAssistantTurnWithModel(context.Background(), model, []store.Message{{
		Role:    aiv1alpha1.ProjectMessageRoleUser,
		Content: "I click the button to make it dark mode but it didnt do anything",
	}})
	if err != nil {
		t.Fatalf("classifyProjectAssistantTurnWithModel returned error: %v", err)
	}
	if decision.Profile != projectAssistantTurnProfileDebugFix {
		t.Fatalf("profile = %q, want debug_fix from mutation fallback", decision.Profile)
	}
	if !decision.RequestsMutation {
		t.Fatalf("decision = %#v, want mutation preserved", decision)
	}
}

func TestProjectAssistantSemanticTurnClassifierNormalizesRuntimeStateDecision(t *testing.T) {
	model := &repositoryFlowEinoChatModel{Steps: []repositoryFlowEinoModelStep{{
		Message: einoschema.AssistantMessage(`{"profile":"guidance","requires_current_state":true,"requires_runtime_state":true,"requests_mutation":false,"confidence":"high"}`, nil),
	}}}

	decision, err := classifyProjectAssistantTurnWithModel(context.Background(), model, []store.Message{{
		Role:    aiv1alpha1.ProjectMessageRoleUser,
		Content: "Show me the current preview URL",
	}})
	if err != nil {
		t.Fatalf("classifyProjectAssistantTurnWithModel returned error: %v", err)
	}
	if decision.Profile != projectAssistantTurnProfileExploration {
		t.Fatalf("profile = %q, want exploration", decision.Profile)
	}
	if !decision.RequiresRuntimeState {
		t.Fatalf("decision = %#v, want runtime state preserved", decision)
	}
	policy := projectAssistantTurnPolicyForDecision(decision)
	registry := projectAssistantLocalToolRegistry(nil)
	previewTool, ok := registry.Spec(projectToolGetPreviewURL)
	if !ok {
		t.Fatal("get_preview_url missing from registry")
	}
	if !policy.AllowsTool(previewTool) {
		t.Fatalf("policy %#v rejected get_preview_url", policy)
	}
	deployTool, ok := registry.Spec(projectToolRestartRuntime)
	if !ok {
		t.Fatal("restart_runtime missing from registry")
	}
	if policy.AllowsTool(deployTool) {
		t.Fatalf("policy %#v allowed restart_runtime", policy)
	}
}

func TestProjectAssistantSemanticTurnClassifierRejectsToolCalls(t *testing.T) {
	model := &repositoryFlowEinoChatModel{Steps: []repositoryFlowEinoModelStep{{
		Message: einoschema.AssistantMessage("", []einoschema.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: einoschema.FunctionCall{
				Name:      projectToolReadFile,
				Arguments: `{"file_path":"src/App.tsx","offset":1,"limit":200}`,
			},
		}}),
	}}}

	decision, err := classifyProjectAssistantTurnWithModel(context.Background(), model, []store.Message{{
		Role:    aiv1alpha1.ProjectMessageRoleUser,
		Content: "What files are in this app?",
	}})
	if err != nil {
		t.Fatalf("classifyProjectAssistantTurnWithModel returned error: %v", err)
	}
	if decision.Profile != projectAssistantTurnProfileExploration {
		t.Fatalf("profile = %q, want fallback exploration", decision.Profile)
	}
}

// A new message is routed from its own intent. Durable Continue and resume
// preserve an in-flight execution policy separately.
func TestProjectAssistantTurnProfileClassifierUsesCurrentTurnIntent(t *testing.T) {
	got := classifyProjectAssistantTurnProfile([]store.Message{
		{Role: aiv1alpha1.ProjectMessageRoleUser, Content: "Add a dashboard"},
		{Role: aiv1alpha1.ProjectMessageRoleAssistant, Content: "I can do that."},
		{Role: aiv1alpha1.ProjectMessageRoleUser, Content: "Actually, how should I think about the design?"},
	})
	if got != projectAssistantTurnProfileGuidance {
		t.Fatalf("profile = %q, want guidance from the latest user turn", got)
	}

	// A terse inspection request is classified from that request alone.
	got = classifyProjectAssistantTurnProfile([]store.Message{
		{Role: aiv1alpha1.ProjectMessageRoleUser, Content: "wire the /api proxy in the web component"},
		{Role: aiv1alpha1.ProjectMessageRoleAssistant, Content: "I need to inspect the files first."},
		{Role: aiv1alpha1.ProjectMessageRoleUser, Content: "just check in your workspace."},
	})
	if got != projectAssistantTurnProfileExploration {
		t.Fatalf("profile = %q, want exploration from the latest user turn", got)
	}

	// A conversation with no instruction anywhere stays advisory/toolless.
	got = classifyProjectAssistantTurnProfile([]store.Message{
		{Role: aiv1alpha1.ProjectMessageRoleUser, Content: "How should I think about the architecture?"},
		{Role: aiv1alpha1.ProjectMessageRoleAssistant, Content: "Here are the tradeoffs."},
		{Role: aiv1alpha1.ProjectMessageRoleUser, Content: "What about the design approach?"},
	})
	if got != projectAssistantTurnProfileGuidance {
		t.Fatalf("profile = %q, want guidance for a purely advisory conversation", got)
	}
}

func TestProjectAssistantModePromptsKeepDiscussionAndGuidanceToolFree(t *testing.T) {
	project := projectWithRepository("demo-repo", "demo", "github")
	project.Name = "demo-project"
	project.UID = "test-project-uid-demo-project"
	project.Spec.DisplayName = "Demo Project"
	repository := &ProjectRepositoryView{Ref: "demo-repo", Name: "demo", Status: projectRepositoryStatusReady, Ready: true}

	for _, profile := range []projectAssistantTurnProfile{
		projectAssistantTurnProfileDiscussion,
		projectAssistantTurnProfileGuidance,
	} {
		t.Run(string(profile), func(t *testing.T) {
			prompt := projectSystemPrompt(project, repository, profile)
			for _, unwanted := range []string{
				projectToolCheckProjectReadiness,
				projectToolPrepareProjectDeployment,
				projectToolRestartRuntime,
				projectToolGetRuntimeStatus,
				projectToolGetPreviewURL,
				projectToolReadFile,
				projectToolGlob,
				projectToolGrep,
				projectToolWriteFile,
				projectToolApplyPatch,
				projectToolMkdir,
				projectToolCommitProjectFiles,
				"tool_search",
			} {
				if strings.Contains(prompt, unwanted) {
					t.Fatalf("%s prompt unexpectedly mentions %q:\n%s", profile, unwanted, prompt)
				}
			}
		})
	}
}

func TestProjectAssistantModePromptsPutBuilderGuidanceOnlyOnWriteProfiles(t *testing.T) {
	project := projectWithRepository("demo-repo", "demo", "github")
	project.Name = "demo-project"
	project.UID = "test-project-uid-demo-project"
	project.Spec.DisplayName = "Demo Project"
	repository := &ProjectRepositoryView{Ref: "demo-repo", Name: "demo", Status: projectRepositoryStatusReady, Ready: true}

	for _, profile := range []projectAssistantTurnProfile{
		projectAssistantTurnProfileImplementation,
		projectAssistantTurnProfileDebugFix,
	} {
		t.Run(string(profile), func(t *testing.T) {
			prompt := projectSystemPrompt(project, repository, profile)
			for _, want := range []string{
				projectToolCheckProjectReadiness,
				projectToolRequestProjectPlanApproval,
				projectToolWriteFile,
				projectToolApplyPatch,
				projectToolCommitProjectFiles,
				"Do not give the user manual copy/paste file replacement instructions",
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("%s prompt missing %q:\n%s", profile, want, prompt)
				}
			}
		})
	}

	for _, profile := range []projectAssistantTurnProfile{
		projectAssistantTurnProfileDiscussion,
		projectAssistantTurnProfileGuidance,
		projectAssistantTurnProfileExploration,
		projectAssistantTurnProfileDebugging,
	} {
		t.Run(string(profile), func(t *testing.T) {
			prompt := projectSystemPrompt(project, repository, profile)
			if strings.Contains(prompt, projectToolRequestProjectPlanApproval) || strings.Contains(prompt, projectToolCommitProjectFiles) {
				t.Fatalf("%s prompt should not contain builder approval/commit guidance:\n%s", profile, prompt)
			}
		})
	}
}

func TestProjectAssistantPromptAlwaysIncludesModeContractAcrossRepositoryStates(t *testing.T) {
	profiles := map[projectAssistantTurnProfile]string{
		projectAssistantTurnProfileDiscussion:     "Answer exploratory or conceptual questions directly",
		projectAssistantTurnProfileAdaptive:       "Answer directly when project inspection is unnecessary",
		projectAssistantTurnProfileGuidance:       "Give practical guidance, recommendations, and tradeoffs",
		projectAssistantTurnProfileExploration:    "Use read-only App Studio workflow",
		projectAssistantTurnProfileDebugging:      "Diagnose in read-only mode",
		projectAssistantTurnProfileDebugFix:       "First perform the same read-only diagnosis as debugging mode",
		projectAssistantTurnProfileImplementation: "The supplied current project snapshot is the initial workspace manifest",
	}
	repositories := map[string]*ProjectRepositoryView{
		"ready":        {Ref: "demo-repo", Status: projectRepositoryStatusReady, Ready: true},
		"provisioning": {Ref: "demo-repo", Status: projectRepositoryStatusProvisioning},
		"unhealthy":    {Ref: "demo-repo", Status: projectRepositoryStatusUnavailable, Message: "connection missing"},
		"missing_view": nil,
	}
	for state, repository := range repositories {
		for profile, marker := range profiles {
			t.Run(state+"/"+string(profile), func(t *testing.T) {
				project := projectWithRepository("demo-repo", "demo", "github")
				project.Name = "demo-project"
				prompt := projectSystemPrompt(project, repository, profile)
				if !strings.Contains(prompt, marker) {
					t.Fatalf("prompt missing mode contract %q:\n%s", marker, prompt)
				}
				if state != "ready" && strings.Contains(prompt, `After workspace mutations, commit the changed source/config files`) {
					t.Fatalf("%s prompt includes commit guidance without a ready repository:\n%s", state, prompt)
				}
				if strings.Contains(prompt, `repositoryRef ""`) {
					t.Fatalf("prompt includes empty repository reference:\n%s", prompt)
				}
			})
		}
	}

	project := projectWithRepository("demo-repo", "demo", "github")
	project.Name = "demo-project"
	project.Spec.Repository = nil
	for profile, marker := range profiles {
		t.Run("absent/"+string(profile), func(t *testing.T) {
			prompt := projectSystemPrompt(project, nil, profile)
			if !strings.Contains(prompt, marker) {
				t.Fatalf("prompt missing mode contract %q without a repository:\n%s", marker, prompt)
			}
			if strings.Contains(prompt, `After workspace mutations, commit the changed source/config files`) || strings.Contains(prompt, `repositoryRef ""`) {
				t.Fatalf("prompt includes commit guidance without a repository:\n%s", prompt)
			}
		})
	}
}

func TestProjectAssistantPromptDefinesEvidenceFirstDiagnosticConstitution(t *testing.T) {
	project := projectWithRepository("demo-repo", "demo", "github")
	project.Name = "demo-project"
	prompt := projectSystemPrompt(project, &ProjectRepositoryView{Status: projectRepositoryStatusReady}, projectAssistantTurnProfileDebugFix)
	for _, want := range []string{
		"every conclusion about the current app requires current evidence",
		"reported symptom and expected behavior",
		"boundary where observed and expected behavior diverge",
		"workspace state, workspace synchronization, runtime operational health, and application behavior as separate claims",
		"rerun the original observation",
		"do not claim it or the acceptance criteria were verified",
		"plan whose summary identifies the suspected cause and the current evidence supporting it",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing diagnostic contract %q:\n%s", want, prompt)
		}
	}
}

func TestProjectAssistantV2PromptsKeepCollaborationModeAcrossRepositoryStates(t *testing.T) {
	for _, legacy := range []string{"request_project_plan_approval", "write_file", "mkdir", "durable implementation task"} {
		if strings.Contains(projectEinoAssistantV2DeepInstruction, legacy) {
			t.Fatalf("v2 deep-agent instruction leaked legacy execution guidance %q:\n%s", legacy, projectEinoAssistantV2DeepInstruction)
		}
	}
	base := projectWithRepository("demo-repo", "demo", "github")
	base.Name = "demo-project"
	states := map[string]struct {
		project    *aiv1alpha1.Project
		repository *ProjectRepositoryView
		commit     bool
	}{
		"ready": {
			project:    base.DeepCopy(),
			repository: &ProjectRepositoryView{Ref: "demo-repo", Status: projectRepositoryStatusReady, Ready: true},
			commit:     true,
		},
		"unhealthy": {
			project:    base.DeepCopy(),
			repository: &ProjectRepositoryView{Ref: "demo-repo", Status: projectRepositoryStatusUnavailable, Message: "connection missing"},
		},
		"missing": {
			project: base.DeepCopy(),
		},
		"absent": {
			project: func() *aiv1alpha1.Project {
				project := base.DeepCopy()
				project.Spec.Repository = nil
				return project
			}(),
		},
	}

	for state, fixture := range states {
		for _, mode := range []projectAssistantCollaborationMode{
			projectAssistantCollaborationModeDefault,
			projectAssistantCollaborationModePlan,
		} {
			t.Run(state+"/"+string(mode), func(t *testing.T) {
				prompt := projectSystemPromptForMode(fixture.project, fixture.repository, projectAssistantTurnProfileDiscussion, mode, false)
				for _, want := range []string{
					"Collaboration mode: " + string(mode),
					"The collaboration mode is fixed for this run",
					"every conclusion about the current app requires current evidence",
					"workspace state, workspace synchronization, runtime operational health, and application behavior as separate claims",
				} {
					if !strings.Contains(prompt, want) {
						t.Fatalf("%s/%s prompt missing %q:\n%s", state, mode, want, prompt)
					}
				}
				for _, legacy := range []string{"request_project_plan_approval", "write_file", "mkdir", "durable implementation task"} {
					if strings.Contains(prompt, legacy) {
						t.Fatalf("%s/%s v2 prompt leaked legacy execution guidance %q:\n%s", state, mode, legacy, prompt)
					}
				}
				if mode == projectAssistantCollaborationModePlan {
					if !strings.Contains(prompt, "Plan mode is read-only") || !strings.Contains(prompt, "the App Studio client owns the explicit transition to Default mode") {
						t.Fatalf("%s plan prompt lacks server-owned read-only transition contract:\n%s", state, prompt)
					}
					return
				}
				for _, want := range []string{
					"answer, explanation, review, status, and diagnosis requests authorize inspection only",
					"The only source-mutation tool is apply_patch",
					"does not prove rendered content, interactions, data flow, application behavior, or acceptance criteria",
				} {
					if !strings.Contains(prompt, want) {
						t.Fatalf("%s default prompt missing %q:\n%s", state, want, prompt)
					}
				}
				commitGuidance := strings.Contains(prompt, "commit only source/config paths actually changed in this run")
				if commitGuidance != fixture.commit {
					t.Fatalf("%s commit guidance = %t, want %t:\n%s", state, commitGuidance, fixture.commit, prompt)
				}
			})
		}
	}
}

// The bound template is the app's environment contract — the prompt must
// direct the model to describe THAT template (agent.usage) before reasoning
// about what infrastructure the app has, and must forbid concluding a
// declared backing service (e.g. the application template's Postgres +
// injected DATABASE_URL) is missing just because the code doesn't use it.
func TestProjectAssistantPromptTreatsBoundTemplateAsEnvironmentContract(t *testing.T) {
	project := projectWithRepository("demo-repo", "demo", "github")
	project.Name = "demo-project"
	project.UID = "test-project-uid-demo-project"
	project.Spec.Template = &aiv1alpha1.ProjectTemplateSpec{Name: "application"}
	repository := &ProjectRepositoryView{Ref: "demo-repo", Name: "demo", Status: projectRepositoryStatusReady, Ready: true}

	prompt := projectSystemPrompt(project, repository, projectAssistantTurnProfileImplementation)
	for _, want := range []string{
		"Development template: application",
		"ENVIRONMENT CONTRACT",
		"infrastructure__describe_template on THIS template",
		"DATABASE_URL",
		"do not conclude a declared service is missing",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestProjectAssistantPromptRequiresEvidenceForProductCapabilities(t *testing.T) {
	project := projectWithRepository("demo-repo", "demo", "github")
	project.Name = "demo-project"
	project.UID = "test-project-uid-demo-project"
	project.Spec.DisplayName = "Demo Project"
	repository := &ProjectRepositoryView{Ref: "demo-repo", Name: "demo", Status: projectRepositoryStatusReady, Ready: true}

	prompt := projectSystemPrompt(project, repository, projectAssistantTurnProfileExploration)
	for _, want := range []string{
		"Do not invent App Studio product capabilities",
		"UI tabs",
		"cloud providers",
		"infrastructure templates",
		"I don't see that capability available in this workspace",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing product capability guardrail %q:\n%s", want, prompt)
		}
	}
	for _, unsupported := range []string{
		"AWS App Runner",
		"Google Cloud Run",
		"Cloud Connections",
		"Deployments tab",
		"Environments tab",
	} {
		if strings.Contains(prompt, unsupported) {
			t.Fatalf("prompt should not contain unsupported product example %q:\n%s", unsupported, prompt)
		}
	}
}

func TestProjectAssistantPromptFramesAppStudioAsBusinessUserEasyButton(t *testing.T) {
	project := projectWithRepository("demo-repo", "demo", "github")
	project.Name = "demo-project"
	project.UID = "test-project-uid-demo-project"
	project.Spec.DisplayName = "Demo Project"
	repository := &ProjectRepositoryView{Ref: "demo-repo", Name: "demo", Status: projectRepositoryStatusReady, Ready: true}

	prompt := projectSystemPrompt(project, repository, projectAssistantTurnProfileImplementation)
	lowerPrompt := strings.ToLower(prompt)
	for _, want := range []string{
		"business users",
		"non-technical",
		"easy button",
		"live development sandbox",
		"source changes run in that sandbox",
		"translate technical choices into business outcomes",
		"do not ask the user to choose databases, networking, infrastructure templates, or deployment architecture",
		"do not recommend a full application or runtime template just to satisfy a smaller need like persistent data",
		"consult the template's agent.usage guidance",
		"separate development sandbox guidance from production launch guidance",
	} {
		if !strings.Contains(lowerPrompt, strings.ToLower(want)) {
			t.Fatalf("prompt missing business-user App Studio guidance %q:\n%s", want, prompt)
		}
	}
}

func TestProjectAssistantPromptExplainsTemplateAgentUsageFitDecision(t *testing.T) {
	project := projectWithRepository("demo-repo", "demo", "github")
	project.Name = "demo-project"
	project.UID = "test-project-uid-demo-project"
	project.Spec.DisplayName = "Demo Project"
	repository := &ProjectRepositoryView{Ref: "demo-repo", Name: "demo", Status: projectRepositoryStatusReady, Ready: true}

	prompt := projectSystemPrompt(project, repository, projectAssistantTurnProfileImplementation)
	lowerPrompt := strings.ToLower(prompt)
	for _, want := range []string{
		"agent.usage as the provider-authored operating contract",
		"do not recommend a template merely because it contains one thing the user asked for",
		"application template",
		"includes postgres",
		"full 3-tier web app",
		"frontend and backend container images",
		"production-style app deployment template",
		"not a simple add a database to my sandbox app option",
	} {
		if !strings.Contains(lowerPrompt, strings.ToLower(want)) {
			t.Fatalf("prompt missing template agent.usage fit guidance %q:\n%s", want, prompt)
		}
	}
}

func TestProjectAssistantTurnPolicyAllowsExpectedToolBundles(t *testing.T) {
	tests := []struct {
		name       string
		profile    projectAssistantTurnProfile
		wantAllow  []string
		wantReject []string
	}{
		{
			name:       "discussion",
			profile:    projectAssistantTurnProfileDiscussion,
			wantReject: []string{projectToolCheckProjectReadiness, projectToolReadFile, projectToolGetRuntimeStatus, projectToolWriteFile, projectToolCommitProjectFiles, projectToolAskFollowUp},
		},
		{
			name:       "guidance",
			profile:    projectAssistantTurnProfileGuidance,
			wantReject: []string{projectToolCheckProjectReadiness, projectToolReadFile, projectToolGetRuntimeStatus, projectToolWriteFile, projectToolCommitProjectFiles, projectToolAskFollowUp},
		},
		{
			name:       "adaptive",
			profile:    projectAssistantTurnProfileAdaptive,
			wantAllow:  []string{projectToolPlanProjectChanges, projectToolCheckProjectReadiness, projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep, projectToolGetRuntimeStatus, projectToolGetPreviewURL, projectToolVerifyDevelopmentRuntime, projectToolRequestProjectPlanApproval, projectToolAskFollowUp},
			wantReject: []string{projectToolRestartRuntime, projectToolWriteFile, projectToolCommitProjectFiles, projectToolInfrastructureListTemplates, projectToolInfrastructureProvision},
		},
		{
			name:       "exploration",
			profile:    projectAssistantTurnProfileExploration,
			wantAllow:  []string{projectToolPlanProjectChanges, projectToolCheckProjectReadiness, projectToolPrepareProjectDeployment, projectToolInspectDevelopmentTemplates, projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep, projectToolInfrastructureListTemplates, projectToolInfrastructureDescribeTemplate, projectToolInfrastructureListInstances, projectToolInfrastructureGetInstance},
			wantReject: []string{projectToolGetRuntimeStatus, projectToolGetPreviewURL, projectToolVerifyDevelopmentRuntime, projectToolRestartRuntime, projectToolWriteFile, projectToolCommitProjectFiles, projectToolAskFollowUp, projectToolInfrastructureProvision},
		},
		{
			name:       "debugging",
			profile:    projectAssistantTurnProfileDebugging,
			wantAllow:  []string{projectToolCheckProjectReadiness, projectToolInspectDevelopmentTemplates, projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep, projectToolGetRuntimeStatus, projectToolGetPreviewURL, projectToolVerifyDevelopmentRuntime, projectToolInfrastructureListTemplates, projectToolInfrastructureDescribeTemplate, projectToolInfrastructureListInstances, projectToolInfrastructureGetInstance},
			wantReject: []string{projectToolRestartRuntime, projectToolWriteFile, projectToolCommitProjectFiles, projectToolAskFollowUp, projectToolInfrastructureProvision},
		},
		{
			name:       "debug fix",
			profile:    projectAssistantTurnProfileDebugFix,
			wantAllow:  []string{projectToolCheckProjectReadiness, projectToolInspectDevelopmentTemplates, projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep, projectToolGetRuntimeStatus, projectToolVerifyDevelopmentRuntime, projectToolRestartRuntime, projectToolRequestProjectPlanApproval, projectToolWriteFile, projectToolCommitProjectFiles, projectToolAskFollowUp, projectToolInfrastructureProvision},
			wantReject: nil,
		},
		{
			name:       "implementation",
			profile:    projectAssistantTurnProfileImplementation,
			wantAllow:  []string{projectToolCheckProjectReadiness, projectToolInspectDevelopmentTemplates, projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep, projectToolGetRuntimeStatus, projectToolVerifyDevelopmentRuntime, projectToolRestartRuntime, projectToolRequestProjectPlanApproval, projectToolWriteFile, projectToolCommitProjectFiles, projectToolAskFollowUp, projectToolInfrastructureProvision},
			wantReject: nil,
		},
	}

	registry := newProjectAssistantToolRegistry(projectAssistantLocalToolRegistry(nil).Tools(true)...)
	for _, tool := range projectAssistantMCPToolsForSpecs([]projectMCPTool{
		{Name: projectToolInfrastructureListTemplates, Description: "List templates", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: projectToolInfrastructureDescribeTemplate, Description: "Describe template", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: projectToolInfrastructureListInstances, Description: "List instances", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: projectToolInfrastructureGetInstance, Description: "Get instance", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: projectToolInfrastructureProvision, Description: "Provision", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}) {
		registry = newProjectAssistantToolRegistry(append(registry.Tools(true), tool)...)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := projectAssistantTurnPolicyForProfile(tt.profile)
			for _, name := range tt.wantAllow {
				spec, ok := projectAssistantTurnPolicyTestSpec(registry, name)
				if !ok {
					t.Fatalf("tool %s missing from registry", name)
				}
				if !policy.AllowsTool(spec) {
					t.Fatalf("%s policy rejected %s", tt.profile, name)
				}
			}
			for _, name := range tt.wantReject {
				spec, ok := projectAssistantTurnPolicyTestSpec(registry, name)
				if !ok {
					t.Fatalf("tool %s missing from registry", name)
				}
				if policy.AllowsTool(spec) {
					t.Fatalf("%s policy allowed %s", tt.profile, name)
				}
			}
		})
	}
}

func TestProjectAssistantTurnPolicyAllowsRuntimeReadsForRuntimeStateExploration(t *testing.T) {
	registry := projectAssistantLocalToolRegistry(nil)
	policy := projectAssistantTurnPolicy{
		profile:              projectAssistantTurnProfileExploration,
		requiresRuntimeState: true,
	}
	for _, name := range []string{projectToolGetRuntimeStatus, projectToolGetPreviewURL, projectToolVerifyDevelopmentRuntime} {
		spec, ok := registry.Spec(name)
		if !ok {
			t.Fatalf("tool %s missing from registry", name)
		}
		if !policy.AllowsTool(spec) {
			t.Fatalf("runtime-state exploration policy rejected %s", name)
		}
	}
	for _, name := range []string{projectToolRestartRuntime, projectToolWriteFile, projectToolCommitProjectFiles} {
		spec, ok := registry.Spec(name)
		if !ok {
			t.Fatalf("tool %s missing from registry", name)
		}
		if policy.AllowsTool(spec) {
			t.Fatalf("runtime-state exploration policy allowed mutating tool %s", name)
		}
	}
}

// The regression this guards: a run that starts as a discussion question is
// checkpointed with a toolless policy; when the user's follow-up answer is an
// instruction ("go for it"), the resume must escalate to the re-routed
// profile — and an in-flight implementation run must never downgrade because
// a follow-up reads as chit-chat.
func TestEscalateProjectAssistantTurnPolicy(t *testing.T) {
	discussion := projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileDiscussion)
	implementation := projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileImplementation)

	if got := escalateProjectAssistantTurnPolicy(discussion, implementation); got.profile != projectAssistantTurnProfileImplementation {
		t.Errorf("discussion + implementation = %q, want escalation to implementation", got.profile)
	}
	if got := escalateProjectAssistantTurnPolicy(implementation, discussion); got.profile != projectAssistantTurnProfileImplementation {
		t.Errorf("implementation + discussion = %q, must not downgrade", got.profile)
	}
	// Empty next policy (resume without a re-routed decision, e.g. a plain
	// approve/deny) keeps the checkpoint policy untouched.
	if got := escalateProjectAssistantTurnPolicy(implementation, projectAssistantTurnPolicy{}); got.profile != projectAssistantTurnProfileImplementation {
		t.Errorf("implementation + empty = %q, want implementation", got.profile)
	}
	// requiresRuntimeState is sticky in both directions (OR).
	withRuntime := projectAssistantTurnPolicy{profile: projectAssistantTurnProfileExploration, requiresRuntimeState: true}
	if got := escalateProjectAssistantTurnPolicy(withRuntime, discussion); !got.requiresRuntimeState {
		t.Error("runtime-state requirement lost on merge with discussion")
	}
	if got := escalateProjectAssistantTurnPolicy(discussion, withRuntime); !got.requiresRuntimeState {
		t.Error("runtime-state requirement not gained from next policy")
	}
	// The escalated policy actually grants tools a discussion policy denies.
	escalated := escalateProjectAssistantTurnPolicy(discussion, implementation)
	spec := projectAssistantToolSpec{Name: projectToolReadFile, Risk: projectAssistantToolRiskRead}
	if discussion.AllowsTool(spec) {
		t.Fatal("discussion policy unexpectedly allows workspace reads")
	}
	if !escalated.AllowsTool(spec) {
		t.Fatal("escalated policy still denies workspace reads")
	}
}

func projectAssistantTurnPolicyTestSpec(registry projectAssistantToolRegistry, name string) (projectAssistantToolSpec, bool) {
	if projectEinoAssistantFilesystemReadTool(name) {
		return projectAssistantToolSpec{Name: name, Risk: projectAssistantToolRiskRead}, true
	}
	return registry.Spec(name)
}

var _ einomodel.BaseChatModel = (*repositoryFlowEinoChatModel)(nil)
