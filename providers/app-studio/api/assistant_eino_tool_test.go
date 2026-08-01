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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/tenant"
	"github.com/faroshq/provider-app-studio/workspace"
)

// projectAssistantDirectToolPort is a test-only transport adapter. It keeps
// direct tool tests on the same invocation boundary as production without
// manufacturing an HTTP request.
type projectAssistantDirectToolPort struct{}

func (projectAssistantDirectToolPort) DiscoverMCP(context.Context, identity, projectLLMSettings) ([]projectAssistantTool, bool, error) {
	return nil, false, nil
}

func (projectAssistantDirectToolPort) Invoke(ctx context.Context, tool projectAssistantTool, req projectAssistantToolCallRequest) (string, error) {
	return tool.Call(ctx, req)
}

func TestProjectAssistantTurnNeedsInfrastructureMCP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    bool
	}{
		{"list instances", "list instances via mcp", true},
		{"single instance", "show me the status of my instance", true},
		{"platform vocabulary", "what platform resources do I have?", true},
		{"mcp mention", "call mcp to enumerate things", true},
		{"templates", "what templates are available?", true},
		{"databricks tables", "can you query my Databricks table metadata?", true},
		{"data prompt", "I need to inspect table data for this project", true},
		{"generic UI table", "render a table of todos in app.js", false},
		{"unrelated", "fix the button styling in app.js", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := []store.Message{{
				Role:    aiv1alpha1.ProjectMessageRoleUser,
				Content: tc.content,
			}}
			if got := projectAssistantTurnNeedsInfrastructureMCP(history); got != tc.want {
				t.Fatalf("projectAssistantTurnNeedsInfrastructureMCP(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestProjectAssistantTurnPolicyCanUseDatabricksMCP(t *testing.T) {
	req := projectAssistantRunRequest{
		History: []store.Message{{
			Role:    aiv1alpha1.ProjectMessageRoleUser,
			Content: "make me a dashboard from my Databricks table",
		}},
	}
	policy := projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileExploration)
	if !projectAssistantTurnPolicyCanUseMCP(policy, req) {
		t.Fatal("expected exploration turn with Databricks table request to use MCP")
	}

	req.History[0].Content = "fix the button styling"
	if projectAssistantTurnPolicyCanUseMCP(policy, req) {
		t.Fatal("expected unrelated turn to skip MCP discovery")
	}

	req.History[0].Content = "render a table of todos in app.js"
	if projectAssistantTurnPolicyCanUseMCP(policy, req) {
		t.Fatal("expected generic UI table request to skip MCP discovery")
	}
}

func TestProjectEinoAssistantToolRedactsFailedResult(t *testing.T) {
	const secret = "sk-super-secret"
	var failedEvent projectToolCallStreamEvent
	localTool := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{
			Name: "failing_local_tool",
			Risk: projectAssistantToolRiskRead,
		},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			return "", errors.New(
				"Authorization: Bearer " + secret + " " +
					strings.Repeat("x", projectToolInfoLimit),
			)
		},
	}
	tool := projectEinoAssistantTool{
		tool: localTool,
		req: projectAssistantRunRequest{
			ToolPort: projectAssistantDirectToolPort{},
			StreamCallbacks: projectAssistantStreamCallbacks{
				OnToolCall: func(event projectToolCallStreamEvent) {
					failedEvent = event
				},
			},
		},
		runState: newProjectEinoAssistantRunState(),
	}

	result, err := tool.invokeAllowedTool(
		context.Background(),
		"call-failing-local-tool",
		localTool.Spec(),
		map[string]any{},
	)
	if err != nil {
		t.Fatalf("invokeAllowedTool returned error: %v", err)
	}
	if strings.Contains(result, secret) || !strings.Contains(result, "[REDACTED]") {
		t.Fatalf("model-visible result = %q, want secret redacted", result)
	}
	if result != truncateProjectToolInfo(result) {
		t.Fatalf("model-visible result length = %d, want bounded by truncateProjectToolInfo", len(result))
	}
	if failedEvent.Status != "failed" {
		t.Fatalf("tool event status = %q, want failed", failedEvent.Status)
	}
	if strings.Contains(failedEvent.Error, secret) || !strings.Contains(failedEvent.Error, "[REDACTED]") {
		t.Fatalf("tool event error = %q, want secret redacted", failedEvent.Error)
	}
	if failedEvent.Error != truncateProjectToolInfo(failedEvent.Error) {
		t.Fatalf("tool event error length = %d, want bounded by truncateProjectToolInfo", len(failedEvent.Error))
	}
}

func TestProjectEinoAssistantToolPropagatesControlFlowErrors(t *testing.T) {
	interruptErr := einotool.StatefulInterrupt(
		context.Background(),
		"approval required",
		map[string]string{"status": "waiting"},
	)
	tests := []struct {
		name string
		err  error
	}{
		{name: "context canceled", err: context.Canceled},
		{name: "context deadline exceeded", err: context.DeadlineExceeded},
		{name: "stream canceled", err: adk.ErrStreamCanceled},
		{name: "forbidden", err: apierrors.NewForbidden(
			k8sschema.GroupResource{Group: "ai.kedge.faros.sh", Resource: "projects"},
			"demo",
			errors.New("denied"),
		)},
		{name: "unauthorized", err: apierrors.NewUnauthorized("denied")},
		{name: "stateful interrupt", err: interruptErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localTool := projectAssistantToolFunc{
				spec: projectAssistantToolSpec{
					Name: "control_flow_tool",
					Risk: projectAssistantToolRiskRead,
				},
				call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
					return "", tt.err
				},
			}
			tool := projectEinoAssistantTool{
				tool:     localTool,
				req:      projectAssistantRunRequest{ToolPort: projectAssistantDirectToolPort{}},
				runState: newProjectEinoAssistantRunState(),
			}

			result, gotErr := tool.invokeAllowedTool(
				context.Background(),
				"call-control-flow",
				localTool.Spec(),
				map[string]any{},
			)
			if gotErr != tt.err {
				t.Fatalf("error = %v, want original error %v", gotErr, tt.err)
			}
			if result != "" {
				t.Fatalf("result = %q, want empty result for propagated error", result)
			}
		})
	}
}

func TestEinoApprovePlanToolDerivesWorkspaceMutationCapability(t *testing.T) {
	server := NewWithWorkspace(nil, store.NewMemoryStore(), workspace.NewFileStore(t.TempDir()), "", false)
	runState := newProjectEinoAssistantRunState()
	tool := projectEinoAssistantTool{
		server: server,
		req: projectAssistantRunRequest{
			MessageScope:       store.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-a", ProjectName: "demo", ProjectUID: "test-project-uid-demo"},
			executionAuthority: &projectAssistantExplicitTestAuthority{},
		},
		runState: runState,
	}

	result, err := tool.invokeApprovedPlanTool(context.Background(), "call-plan", projectAssistantToolSpec{
		Name: projectToolRequestProjectPlanApproval,
		Risk: projectAssistantToolRiskPlan,
	}, map[string]any{
		"summary":            "Build dashboard",
		"steps":              []any{"Write app shell"},
		"targetPaths":        []any{"src/"},
		"acceptanceCriteria": []any{"src/App.tsx exists"},
	})
	if err != nil {
		t.Fatalf("invokeApprovedPlanTool returned error: %v", err)
	}

	if !strings.Contains(result, `"status":"approved"`) {
		t.Fatalf("result = %q, want approved plan", result)
	}
	plan := runState.ApprovedPlan()
	if plan == nil {
		t.Fatal("approved plan = nil")
	}
	if plan.Version != projectAssistantApprovedPlanVersionWorkspaceMutation {
		t.Fatalf("approved plan version = %d, want %d", plan.Version, projectAssistantApprovedPlanVersionWorkspaceMutation)
	}
	if !stringSliceEqual(plan.Capabilities, []string{projectAssistantCapabilityWorkspaceMutate}) {
		t.Fatalf("approved plan capabilities = %#v, want workspace mutation", plan.Capabilities)
	}
	for _, toolName := range []string{projectToolWriteFile, projectToolApplyPatch, projectToolMkdir} {
		if !projectAssistantApprovedPlanAllowsWrite(plan, toolName, map[string]any{"path": "src/App.tsx"}) {
			t.Fatalf("derived workspace grant does not authorize %s", toolName)
		}
	}
}

func TestEinoAdaptivePlanRequestPromotesBeforePermissionInterrupt(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	scope := testProjectMessageScope("org-a", "workspace-a", "demo")
	started, err := server.startProjectAssistantAdaptiveRunDurably(
		ctx,
		scope,
		"alice",
		"I just tried to use the queue custom toast but it didnt work",
		"auto-plan-promotion-1",
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	run := started.Run
	if _, err := server.projectAssistantSupervisor().Attach(scope, run, started.Assistant); err != nil {
		t.Fatal(err)
	}
	toolCalls := 0
	adapter := projectEinoAssistantTool{
		server: server,
		tool: projectAssistantToolFunc{
			spec: projectAssistantToolSpec{
				Name:       projectToolRequestProjectPlanApproval,
				Risk:       projectAssistantToolRiskPlan,
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
			call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
				toolCalls++
				return "", nil
			},
		},
		req: projectAssistantRunRequest{
			Identity: identity{user: "alice"}, MessageScope: scope, AssistantRun: &run,
		},
		runState: newProjectEinoAssistantRunState(),
	}

	result, err := adapter.InvokableRun(ctx, `{
		"summary":"Repair the toast queue",
		"steps":["inspect and repair the toast integration"],
		"targetPaths":["src/"],
		"acceptanceCriteria":["the custom toast appears"]
	}`)
	if err == nil {
		t.Fatal("InvokableRun error = nil, want permission interrupt")
	}
	if result != "" || toolCalls != 0 {
		t.Fatalf("result/tool calls = %q/%d, want promotion before invocation", result, toolCalls)
	}
	if run.WorkItemID == "" || run.Mode != store.AssistantRunModeNew {
		t.Fatalf("in-memory promoted run = %#v", run)
	}
	persisted, err := messages.GetAssistantRun(ctx, scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.WorkItemID != run.WorkItemID || persisted.Mode != store.AssistantRunModeNew {
		t.Fatalf("persisted promoted run = %#v", persisted)
	}
	item, err := messages.GetAssistantWorkItem(ctx, scope, run.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	if item.RootMessageID != run.UserMessageID || item.ActiveRunID != run.ID {
		t.Fatalf("promoted item = %#v", item)
	}
}

func TestEinoDurableMutationWithoutWorkItemFailsClosed(t *testing.T) {
	run := store.AssistantRun{ID: "run-without-item", Mode: store.AssistantRunModeAdaptive, Status: store.AssistantRunStatusRunning}
	adapter := projectEinoAssistantTool{req: projectAssistantRunRequest{AssistantRun: &run}}
	err := adapter.admitMutation(context.Background(), projectAssistantToolSpec{
		Name: projectToolWriteFile,
		Risk: projectAssistantToolRiskWrite,
	})
	if !errors.Is(err, store.ErrAssistantWorkItemConflict) {
		t.Fatalf("admitMutation error = %v, want work item conflict", err)
	}
}

func TestEinoApprovePlanReplacesExpandedScopeInsteadOfUnioning(t *testing.T) {
	server := NewWithWorkspace(nil, store.NewMemoryStore(), workspace.NewFileStore(t.TempDir()), "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-a", ProjectName: "demo", ProjectUID: "test-project-uid-demo"}
	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(projectAssistantApprovedPlan{
		Version:      projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities: []string{projectAssistantCapabilityWorkspaceMutate},
		TargetPaths:  []string{"src/"},
	})
	tool := projectEinoAssistantTool{
		server:   server,
		req:      projectAssistantRunRequest{MessageScope: scope, executionAuthority: &projectAssistantExplicitTestAuthority{}},
		runState: runState,
	}

	result, err := tool.invokeApprovedPlanTool(
		context.Background(),
		"call-plan",
		projectAssistantToolSpec{Name: projectToolRequestProjectPlanApproval, Risk: projectAssistantToolRiskPlan},
		map[string]any{
			"summary":     "Move implementation to generated output",
			"steps":       []any{"write generated files"},
			"targetPaths": []any{"generated/"},
		},
	)
	if err != nil {
		t.Fatalf("invokeApprovedPlanTool returned error: %v", err)
	}
	if !strings.Contains(result, `"status":"approved"`) {
		t.Fatalf("result = %q, want approved plan", result)
	}
	plan := runState.ApprovedPlan()
	if plan == nil {
		t.Fatal("approved plan = nil")
	}
	if !stringSliceEqual(plan.TargetPaths, []string{"generated/"}) {
		t.Fatalf("approved plan paths = %#v, want replacement scope only", plan.TargetPaths)
	}
}

func TestEinoInlineAdaptiveMCPDiscoveryFailureKeepsActivePolicyPrompt(t *testing.T) {
	server := NewWithWorkspace(nil, store.NewMemoryStore(), workspace.NewFileStore(t.TempDir()), "", false)
	run := store.AssistantRun{
		ID:           "run-inline-auto",
		Mode:         store.AssistantRunModeAdaptive,
		ApprovalMode: store.AssistantApprovalModeAutoApprove,
	}
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "test-project-uid-demo"
	req := projectAssistantRunRequest{
		ToolPort:     newProjectAssistantHTTPToolPort(server, httptest.NewRequest(http.MethodPost, "/", nil)),
		Identity:     identity{orgUUID: "org-a", workspaceUUID: "ws-a", tenantPath: "root:org-a:ws-a", user: "alice"},
		Project:      project,
		MessageScope: testProjectMessageScope("org-a", "ws-a", project.Name),
		TurnProfile:  projectAssistantTurnProfileAdaptive,
		TurnPolicy:   projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileAdaptive),
		ApprovalMode: store.AssistantApprovalModeAutoApprove,
		AssistantRun: &run,
	}
	discovery := projectEinoAssistantDiscoverTools(context.Background(), server, req)
	if strings.Contains(discovery.Prompt, "git-source tools are unavailable") {
		t.Fatalf("adaptive discovery prompt advertised hidden mutation failure: %q", discovery.Prompt)
	}
	if strings.Contains(discovery.Prompt, projectToolCommitProjectFiles) {
		t.Fatalf("adaptive discovery prompt = %q, must hide commit bridge", discovery.Prompt)
	}
}

func TestEinoPendingLegacyPlanApprovalCannotBecomeCapabilityGrant(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	tool := projectEinoAssistantTool{runState: runState}

	result, err := tool.invokeApprovedPlanTool(
		context.Background(),
		"call-legacy-plan",
		projectAssistantToolSpec{Name: projectToolRequestProjectPlanApproval, Risk: projectAssistantToolRiskPlan},
		map[string]any{
			"summary":           "Update the app",
			"steps":             []any{"edit source"},
			"targetPaths":       []any{"src/"},
			"allowedOperations": []any{projectToolWriteFile},
		},
	)
	if err != nil {
		t.Fatalf("invokeApprovedPlanTool returned error: %v", err)
	}
	if !strings.Contains(result, "must be requested again") {
		t.Fatalf("legacy plan result = %q, want a fresh-approval requirement", result)
	}
	if plan := runState.ApprovedPlan(); plan != nil {
		t.Fatalf("legacy pending approval created capability grant %#v", plan)
	}
}

func TestEinoDirectWriteGrantAuthorizesCanonicalEditsOnlyOnApprovedPath(t *testing.T) {
	server := NewWithWorkspace(nil, store.NewMemoryStore(), workspace.NewFileStore(t.TempDir()), "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-a", ProjectName: "demo", ProjectUID: "test-project-uid-demo"}
	runState := newProjectEinoAssistantRunState()
	tool := projectEinoAssistantTool{
		server:   server,
		req:      projectAssistantRunRequest{MessageScope: scope, executionAuthority: &projectAssistantExplicitTestAuthority{}},
		runState: runState,
	}

	if err := tool.grantWriteUntilCommit(context.Background(), projectToolWriteFile, map[string]any{"path": "src/App.tsx"}); err != nil {
		t.Fatalf("grantWriteUntilCommit returned error: %v", err)
	}
	plan := runState.ApprovedPlan()
	if plan == nil {
		t.Fatal("direct write grant = nil")
	}
	if !stringSliceEqual(plan.TargetPaths, []string{"src/App.tsx"}) {
		t.Fatalf("direct write grant paths = %#v, want exact approved path", plan.TargetPaths)
	}
	if !projectAssistantApprovedPlanAllowsWrite(plan, projectToolApplyPatch, map[string]any{"path": "src/App.tsx"}) {
		t.Fatal("direct write grant should authorize apply_patch on the approved path")
	}
	if projectAssistantApprovedPlanAllowsWrite(plan, projectToolWriteFile, map[string]any{"path": "src/Other.tsx"}) {
		t.Fatal("direct write grant must not authorize a different path")
	}
}

func TestEinoDirectMkdirGrantAuthorizesChildEdits(t *testing.T) {
	server := NewWithWorkspace(nil, store.NewMemoryStore(), workspace.NewFileStore(t.TempDir()), "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-a", ProjectName: "demo", ProjectUID: "test-project-uid-demo"}
	runState := newProjectEinoAssistantRunState()
	tool := projectEinoAssistantTool{
		server:   server,
		req:      projectAssistantRunRequest{MessageScope: scope, executionAuthority: &projectAssistantExplicitTestAuthority{}},
		runState: runState,
	}

	if err := tool.grantWriteUntilCommit(context.Background(), projectToolMkdir, map[string]any{"path": "src/components"}); err != nil {
		t.Fatalf("grantWriteUntilCommit returned error: %v", err)
	}
	plan := runState.ApprovedPlan()
	if plan == nil {
		t.Fatal("direct mkdir grant = nil")
	}
	if !stringSliceEqual(plan.TargetPaths, []string{"src/components/"}) {
		t.Fatalf("direct mkdir grant paths = %#v, want directory subtree", plan.TargetPaths)
	}
	if !projectAssistantApprovedPlanAllowsWrite(plan, projectToolWriteFile, map[string]any{"path": "src/components/Foo.tsx"}) {
		t.Fatal("direct mkdir grant should authorize writes below the approved directory")
	}
}

func TestEinoDirectWriteGrantDoesNotMergeObsoleteAuthority(t *testing.T) {
	server := NewWithWorkspace(nil, store.NewMemoryStore(), workspace.NewFileStore(t.TempDir()), "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-a", ProjectName: "demo", ProjectUID: "test-project-uid-demo"}
	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(projectAssistantApprovedPlan{
		TargetPaths:    []string{"secrets/"},
		AllowAllWrites: true,
	})
	tool := projectEinoAssistantTool{
		server:   server,
		req:      projectAssistantRunRequest{MessageScope: scope, executionAuthority: &projectAssistantExplicitTestAuthority{}},
		runState: runState,
	}

	if err := tool.grantWriteUntilCommit(context.Background(), projectToolWriteFile, map[string]any{"path": "src/App.tsx"}); err != nil {
		t.Fatalf("grantWriteUntilCommit returned error: %v", err)
	}
	plan := runState.ApprovedPlan()
	if plan == nil {
		t.Fatal("direct write grant = nil")
	}
	if plan.AllowAllWrites || !stringSliceEqual(plan.TargetPaths, []string{"src/App.tsx"}) {
		t.Fatalf("direct write grant = %#v, want only fresh approved path", plan)
	}
}
func TestEinoToolWorkItemCommitPermissionRetiresGrantWithoutSyntheticRun(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	id := identity{orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1", user: "alice"}
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "test-project-uid-demo"
	scope := testProjectMessageScope(id.orgUUID, id.workspaceUUID, project.Name)
	started, err := server.startProjectAssistantBuildRunDurably(ctx, scope, id.user, "Update the application", "build-commit-1", func(store.AssistantRun, store.Message, bool) error { return nil })
	if err != nil {
		t.Fatalf("start durable build: %v", err)
	}
	plan := normalizeProjectAssistantApprovedPlan(projectAssistantApprovedPlan{
		Summary:      "Update the application.",
		Steps:        []string{"update src/App.tsx"},
		TargetPaths:  []string{"src/App.tsx"},
		Version:      projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities: []string{projectAssistantCapabilityWorkspaceMutate},
	})
	grantRevision, err := server.persistProjectAssistantWorkItemApprovedPlan(ctx, scope, id.user, started.Run, &plan, "")
	if err != nil {
		t.Fatalf("persist WorkItem plan: %v", err)
	}
	approvedItem, err := messages.GetAssistantWorkItem(ctx, scope, started.Run.WorkItemID)
	if err != nil {
		t.Fatalf("GetAssistantWorkItem after approval: %v", err)
	}
	run, err := messages.GetAssistantRun(ctx, scope, started.Run.ID)
	if err != nil {
		t.Fatalf("GetAssistantRun: %v", err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, run, started.Assistant); err != nil {
		t.Fatalf("attach durable run: %v", err)
	}
	if run.ExpectedGrantRevision != grantRevision {
		t.Fatalf("run grant revision = %q, want %q", run.ExpectedGrantRevision, grantRevision)
	}
	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(plan)
	runState.SetApprovedPlanGrantRevision(grantRevision)

	commitCalls := 0
	var events []projectToolCallStreamEvent
	commit := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolCommitProjectFiles, Risk: projectAssistantToolRiskCommit, Parameters: json.RawMessage(`{"type":"object"}`)},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			commitCalls++
			return `{"status":"committed"}`, nil
		},
	}
	adapter := projectEinoAssistantTool{
		server: server,
		tool:   commit,
		req: projectAssistantRunRequest{
			Identity: id, Project: project, MessageScope: scope, AssistantRun: &run,
			StreamCallbacks: projectAssistantStreamCallbacks{OnToolCall: func(event projectToolCallStreamEvent) {
				events = append(events, event)
			}},
		},
		runState: runState,
	}
	if err := adapter.requestPermission(ctx, "call-commit", commit.Spec(), map[string]any{"message": "Update application"}, `{"message":"Update application"}`); err == nil {
		t.Fatal("requestPermission returned nil, want permission interrupt")
	}
	if commitCalls != 0 {
		t.Fatalf("commit calls = %d, want 0 before approval", commitCalls)
	}
	if len(events) != 1 || events[0].Status != "permission_required" {
		t.Fatalf("tool events = %#v, want one permission_required event", events)
	}
	if runState.ApprovedPlan() != nil {
		t.Fatalf("run-local plan = %#v, want cleared after durable retirement", runState.ApprovedPlan())
	}

	item, err := messages.GetAssistantWorkItem(ctx, scope, started.Run.WorkItemID)
	if err != nil {
		t.Fatalf("GetAssistantWorkItem: %v", err)
	}
	if len(item.PlanGrant) != 0 || item.GrantRevision == "" || item.GrantRevision == grantRevision || item.Revision != approvedItem.Revision+1 {
		t.Fatalf("retired WorkItem = %#v, want cleared plan and fresh tombstone", item)
	}
	persistedRun, err := messages.GetAssistantRun(ctx, scope, run.ID)
	if err != nil {
		t.Fatalf("GetAssistantRun after retirement: %v", err)
	}
	if persistedRun.Status != store.AssistantRunStatusRunning || persistedRun.ExpectedGrantRevision != item.GrantRevision || runState.ApprovedPlanGrantRevision() != item.GrantRevision {
		t.Fatalf("retired run state = %#v/%q, want running shared tombstone %q", persistedRun, runState.ApprovedPlanGrantRevision(), item.GrantRevision)
	}
}

func TestEinoToolWorkItemCommitPermissionFailsClosedWhenRetirementFails(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	id := identity{orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1", user: "alice"}
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "test-project-uid-demo"
	scope := testProjectMessageScope(id.orgUUID, id.workspaceUUID, project.Name)
	started, err := server.startProjectAssistantBuildRunDurably(ctx, scope, id.user, "Update the application", "build-commit-failure-1", func(store.AssistantRun, store.Message, bool) error { return nil })
	if err != nil {
		t.Fatalf("start durable build: %v", err)
	}
	plan := normalizeProjectAssistantApprovedPlan(projectAssistantApprovedPlan{
		Summary:      "Update the application.",
		TargetPaths:  []string{"src/App.tsx"},
		Version:      projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities: []string{projectAssistantCapabilityWorkspaceMutate},
	})
	grantRevision, err := server.persistProjectAssistantWorkItemApprovedPlan(ctx, scope, id.user, started.Run, &plan, "")
	if err != nil {
		t.Fatalf("persist WorkItem plan: %v", err)
	}
	run, err := messages.GetAssistantRun(ctx, scope, started.Run.ID)
	if err != nil {
		t.Fatalf("GetAssistantRun: %v", err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, run, started.Assistant); err != nil {
		t.Fatalf("attach durable run: %v", err)
	}
	failingStore := failProjectAssistantWorkItemPlanRetirementStore{Store: messages}
	server.store = failingStore
	server.assistantSupervisor = newProjectAssistantSupervisor(context.Background(), failingStore)
	if _, err := server.projectAssistantSupervisor().Attach(scope, run, started.Assistant); err != nil {
		t.Fatalf("attach durable run: %v", err)
	}
	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(plan)
	runState.SetApprovedPlanGrantRevision(grantRevision)
	commitCalls := 0
	var events []projectToolCallStreamEvent
	commit := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolCommitProjectFiles, Risk: projectAssistantToolRiskCommit, Parameters: json.RawMessage(`{"type":"object"}`)},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			commitCalls++
			return `{"status":"committed"}`, nil
		},
	}
	adapter := projectEinoAssistantTool{
		server: server,
		tool:   commit,
		req: projectAssistantRunRequest{
			Identity: id, Project: project, MessageScope: scope, AssistantRun: &run,
			StreamCallbacks: projectAssistantStreamCallbacks{OnToolCall: func(event projectToolCallStreamEvent) {
				events = append(events, event)
			}},
		},
		runState: runState,
	}
	err = adapter.requestPermission(ctx, "call-commit", commit.Spec(), map[string]any{"message": "Update application"}, `{"message":"Update application"}`)
	if err == nil || !errors.Is(err, errProjectAssistantPlanRetirement) {
		t.Fatalf("requestPermission error = %v, want plan retirement failure", err)
	}
	if commitCalls != 0 || len(events) != 0 {
		t.Fatalf("commit calls/events = %d/%#v, want neither mutation nor permission interrupt", commitCalls, events)
	}
	if runState.ApprovedPlan() == nil || runState.ApprovedPlanGrantRevision() != grantRevision {
		t.Fatalf("run state = %#v/%q, want unchanged authority after failed retirement", runState.ApprovedPlan(), runState.ApprovedPlanGrantRevision())
	}
	item, err := messages.GetAssistantWorkItem(ctx, scope, started.Run.WorkItemID)
	if err != nil {
		t.Fatalf("GetAssistantWorkItem: %v", err)
	}
	if len(item.PlanGrant) == 0 || item.GrantRevision != grantRevision {
		t.Fatalf("WorkItem after failed retirement = %#v, want original grant", item)
	}
}

func TestEinoToolWorkItemCommitRetirementYieldsToStopWithoutPermissionPrompt(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	id := identity{orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1", user: "alice"}
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "test-project-uid-demo"
	scope := testProjectMessageScope(id.orgUUID, id.workspaceUUID, project.Name)
	started, err := server.startProjectAssistantBuildRunDurably(ctx, scope, id.user, "Update the application", "build-stop-race-1", func(store.AssistantRun, store.Message, bool) error { return nil })
	if err != nil {
		t.Fatalf("start durable build: %v", err)
	}
	plan := normalizeProjectAssistantApprovedPlan(projectAssistantApprovedPlan{
		Summary:      "Update the application.",
		TargetPaths:  []string{"src/App.tsx"},
		Version:      projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities: []string{projectAssistantCapabilityWorkspaceMutate},
	})
	grantRevision, err := server.persistProjectAssistantWorkItemApprovedPlan(ctx, scope, id.user, started.Run, &plan, "")
	if err != nil {
		t.Fatalf("persist WorkItem plan: %v", err)
	}
	run, err := messages.GetAssistantRun(ctx, scope, started.Run.ID)
	if err != nil {
		t.Fatalf("GetAssistantRun: %v", err)
	}

	stopEntered := make(chan struct{})
	allowStop := make(chan struct{})
	blockingStore := blockProjectAssistantStopStore{Store: messages, entered: stopEntered, release: allowStop}
	server.store = blockingStore
	server.assistantSupervisor = newProjectAssistantSupervisor(context.Background(), blockingStore)
	supervisor := server.projectAssistantSupervisor()
	if _, err := supervisor.Attach(scope, run, started.Assistant); err != nil {
		t.Fatalf("attach durable run: %v", err)
	}

	stopDone := make(chan error, 1)
	go func() {
		_, _, stopErr := supervisor.Stop(scope, run.ID)
		stopDone <- stopErr
	}()
	<-stopEntered // Stop owns transitionMu before retirement starts.

	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(plan)
	runState.SetApprovedPlanGrantRevision(grantRevision)
	var events []projectToolCallStreamEvent
	commit := projectAssistantToolFunc{spec: projectAssistantToolSpec{
		Name: projectToolCommitProjectFiles, Risk: projectAssistantToolRiskCommit, Parameters: json.RawMessage(`{"type":"object"}`),
	}}
	adapter := projectEinoAssistantTool{
		server: server,
		tool:   commit,
		req: projectAssistantRunRequest{
			Identity: id, Project: project, MessageScope: scope, AssistantRun: &run,
			StreamCallbacks: projectAssistantStreamCallbacks{OnToolCall: func(event projectToolCallStreamEvent) {
				events = append(events, event)
			}},
		},
		runState: runState,
	}
	retirementDone := make(chan error, 1)
	go func() {
		retirementDone <- adapter.requestPermission(ctx, "call-commit", commit.Spec(), map[string]any{"message": "Update application"}, `{"message":"Update application"}`)
	}()
	close(allowStop)
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	err = <-retirementDone
	if err == nil || !errors.Is(err, errProjectAssistantPlanRetirement) {
		t.Fatalf("retirement error = %v, want plan-retirement conflict after Stop", err)
	}
	if len(events) != 0 {
		t.Fatalf("permission events = %#v, want no prompt after Stop won", events)
	}
	if runState.ApprovedPlan() == nil || runState.ApprovedPlanGrantRevision() != grantRevision {
		t.Fatalf("run state = %#v/%q, want original local authority after failed retirement", runState.ApprovedPlan(), runState.ApprovedPlanGrantRevision())
	}
	item, err := messages.GetAssistantWorkItem(ctx, scope, started.Run.WorkItemID)
	if err != nil {
		t.Fatalf("GetAssistantWorkItem: %v", err)
	}
	if len(item.PlanGrant) != 0 || item.GrantRevision != "" {
		t.Fatalf("WorkItem after Stop = %#v, want revoked authority", item)
	}
	persistedRun, err := messages.GetAssistantRun(ctx, scope, run.ID)
	if err != nil {
		t.Fatalf("GetAssistantRun after Stop: %v", err)
	}
	if persistedRun.Status != store.AssistantRunStatusStopping {
		t.Fatalf("run after Stop = %#v, want stopping without pending-permission resurrection", persistedRun)
	}
}

func TestEinoToolInitialExecutionPlanYieldsToStop(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	stopEntered := make(chan struct{})
	allowStop := make(chan struct{})
	blockingStore := blockProjectAssistantStopStore{Store: messages, entered: stopEntered, release: allowStop}
	server := NewWithWorkspace(nil, blockingStore, workspace.NewFileStore(t.TempDir()), "", false)
	server.assistantSupervisor = newProjectAssistantSupervisor(context.Background(), blockingStore)
	id := identity{orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1", user: "alice"}
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "test-project-uid-demo"
	scope := testProjectMessageScope(id.orgUUID, id.workspaceUUID, project.Name)
	started, err := server.startProjectAssistantBuildRunDurably(
		ctx,
		scope,
		id.user,
		"Build the application",
		"initial-plan-stop-race-1",
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatalf("start durable build: %v", err)
	}
	_, err = server.projectAssistantSupervisor().Attach(scope, started.Run, started.Assistant)
	if err != nil {
		t.Fatalf("attach durable run: %v", err)
	}

	stopDone := make(chan error, 1)
	go func() {
		_, _, stopErr := server.projectAssistantSupervisor().Stop(scope, started.Run.ID)
		stopDone <- stopErr
	}()
	<-stopEntered // Stop owns transitionMu before execution-plan persistence starts.

	adapter := projectEinoAssistantTool{
		server: server,
		req: projectAssistantRunRequest{
			Identity:     id,
			Project:      project,
			MessageScope: scope,
			AssistantRun: &started.Run,
		},
	}
	planDone := make(chan error, 1)
	go func() {
		_, planErr := adapter.persistInitialExecutionPlan(ctx, &projectAssistantApprovedPlan{
			Goal:         "Build the application",
			Summary:      "Build the application.",
			TargetPaths:  []string{"src/App.jsx"},
			Capabilities: []string{projectAssistantCapabilityWorkspaceMutate},
			RunLocal:     true,
		})
		planDone <- planErr
	}()

	select {
	case err := <-planDone:
		t.Fatalf("execution-plan persistence completed before Stop released transition lock: %v", err)
	default:
	}
	close(allowStop)
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if err := <-planDone; !errors.Is(err, store.ErrAssistantRunConflict) {
		t.Fatalf("execution-plan persistence error = %v, want run conflict after Stop", err)
	}
	item, err := messages.GetAssistantWorkItem(ctx, scope, started.Run.WorkItemID)
	if err != nil {
		t.Fatalf("GetAssistantWorkItem: %v", err)
	}
	if len(item.ExecutionPlan) != 0 || item.ExecutionPlanRevision != "" {
		t.Fatalf("WorkItem execution plan persisted after Stop: %#v", item)
	}
}

func TestEinoToolTodoFileMistakePreservesApprovedPlan(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	plan := normalizeProjectAssistantApprovedPlan(projectAssistantApprovedPlan{
		Summary:      "Update the timestamp.",
		Steps:        []string{"update app.js", "verify the runtime"},
		TargetPaths:  []string{"app.js"},
		Version:      projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities: []string{projectAssistantCapabilityWorkspaceMutate},
	})
	runState.ApprovePlan(plan)

	toolCalls := 0
	write := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{
			Name:       projectToolWriteFile,
			Risk:       projectAssistantToolRiskWrite,
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			toolCalls++
			return `{"status":"ok"}`, nil
		},
	}
	adapter := projectEinoAssistantTool{
		tool: write,
		req: projectAssistantRunRequest{
			ApprovalMode: store.AssistantApprovalModeAutoApprove,
		},
		runState: runState,
	}

	result, err := adapter.InvokableRun(context.Background(), `{"path":"todos.md","content":"- verify"}`)
	if err != nil {
		t.Fatalf("InvokableRun returned error: %v", err)
	}
	if !strings.Contains(result, "use write_todos") {
		t.Fatalf("result = %q, want write_todos correction", result)
	}
	if toolCalls != 0 {
		t.Fatalf("write_file calls = %d, want 0", toolCalls)
	}
	if got := runState.ApprovedPlan(); got == nil || !stringSliceEqual(got.TargetPaths, plan.TargetPaths) {
		t.Fatalf("approved plan = %#v, want original plan preserved", got)
	}
}

func TestEinoToolTodoFileMistakeAllowsExplicitlyApprovedProjectFile(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(normalizeProjectAssistantApprovedPlan(projectAssistantApprovedPlan{
		Summary:      "Create the requested project todo document.",
		Steps:        []string{"write todos.md", "verify its contents"},
		TargetPaths:  []string{"todos.md"},
		Version:      projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities: []string{projectAssistantCapabilityWorkspaceMutate},
	}))
	spec := projectAssistantToolSpec{Name: projectToolWriteFile, Risk: projectAssistantToolRiskWrite}
	if projectEinoAssistantTodoFileWriteMistake(spec, map[string]any{"path": "todos.md"}, runState) {
		t.Fatal("explicitly approved todos.md was treated as execution tracking")
	}
}

func TestEinoToolTodoFileMistakeRejectsRunLocalBlanketGrant(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	runState.ApprovePlan(normalizeProjectAssistantApprovedPlan(projectAssistantApprovedPlan{
		Summary:        "Build the application.",
		Steps:          []string{"build the application", "verify the runtime"},
		AllowAllWrites: true,
		RunLocal:       true,
		Version:        projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities:   []string{projectAssistantCapabilityWorkspaceMutate},
	}))
	spec := projectAssistantToolSpec{Name: projectToolWriteFile, Risk: projectAssistantToolRiskWrite}
	if !projectEinoAssistantTodoFileWriteMistake(spec, map[string]any{"path": "todos.md"}, runState) {
		t.Fatal("run-local blanket grant allowed an execution-tracking todos.md")
	}
}

func TestEinoToolPassesSessionSnapshotToLocalTool(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	runState.SetSessionSnapshot(projectEinoAssistantSessionSnapshot{
		LastFileSnapshot:  []string{"package.json"},
		RecommendedChecks: []string{"build"},
	})
	var got *projectEinoAssistantSessionSnapshot
	localTool := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{
			Name: "capture_session_snapshot",
			Risk: projectAssistantToolRiskRead,
		},
		call: func(_ context.Context, req projectAssistantToolCallRequest) (string, error) {
			got = req.SessionSnapshot
			return `{"status":"captured"}`, nil
		},
	}
	tool := projectEinoAssistantTool{
		tool:     localTool,
		req:      projectAssistantRunRequest{ToolPort: projectAssistantDirectToolPort{}},
		runState: runState,
	}

	if _, err := tool.invokeAllowedTool(context.Background(), "call-session", localTool.Spec(), nil); err != nil {
		t.Fatalf("invokeAllowedTool returned error: %v", err)
	}
	if got == nil || !stringSliceEqual(got.LastFileSnapshot, []string{"package.json"}) {
		t.Fatalf("session snapshot = %#v, want file snapshot", got)
	}
	if !stringSliceEqual(got.RecommendedChecks, []string{"build"}) {
		t.Fatalf("recommended checks = %#v, want build", got.RecommendedChecks)
	}
	got.LastFileSnapshot[0] = "mutated"
	if !stringSliceEqual(runState.SessionSnapshot().LastFileSnapshot, []string{"package.json"}) {
		t.Fatal("tool received mutable run-state session snapshot")
	}
}

type failProjectAssistantWorkItemPlanRetirementStore struct {
	store.Store
}

func (failProjectAssistantWorkItemPlanRetirementStore) RetireWorkItemPlan(
	context.Context,
	store.Scope,
	string,
	string,
	string,
	int64,
	string,
	string,
	time.Time,
) (store.AssistantWorkItem, error) {
	return store.AssistantWorkItem{}, errors.New("injected WorkItem plan retirement failure")
}

type blockProjectAssistantStopStore struct {
	store.Store
	entered chan<- struct{}
	release <-chan struct{}
}

func (s blockProjectAssistantStopStore) RequestAssistantRunStopWithAssistantMessage(
	ctx context.Context,
	scope store.Scope,
	workItemID string,
	runID string,
	expectedWorkItemRevision int64,
	expectedRunRevision int64,
	assistant store.Message,
	now time.Time,
) (store.AssistantRun, error) {
	s.entered <- struct{}{}
	select {
	case <-s.release:
		return s.Store.RequestAssistantRunStopWithAssistantMessage(ctx, scope, workItemID, runID, expectedWorkItemRevision, expectedRunRevision, assistant, now)
	case <-ctx.Done():
		return store.AssistantRun{}, ctx.Err()
	}
}

func TestEinoToolSchedulesDevelopmentSyncAfterMutatingTool(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	server := &Server{}
	type scheduledSync struct{ name, project string }
	scheduled := make(chan scheduledSync, 1)
	server.developmentSyncAfterMutation = func(_ identity, p *aiv1alpha1.Project, name string) error {
		projectName := ""
		if p != nil {
			projectName = p.Name
		}
		scheduled <- scheduledSync{name: name, project: projectName}
		return nil
	}
	localTool := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{
			Name: projectToolWriteFile,
			Risk: projectAssistantToolRiskWrite,
		},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			return `{"status":"ok"}`, nil
		},
	}
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "test-project-uid-demo"
	tool := projectEinoAssistantTool{
		server: server,
		tool:   localTool,
		req: projectAssistantRunRequest{
			Project:            project,
			WorkspaceScope:     workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo", ProjectUID: "test-project-uid"},
			ToolPort:           projectAssistantDirectToolPort{},
			executionAuthority: &projectAssistantExplicitTestAuthority{},
		},
		runState: runState,
	}

	if _, err := tool.invokeAllowedTool(context.Background(), "call-write", localTool.Spec(), map[string]any{"path": "src/App.tsx"}); err != nil {
		t.Fatalf("invokeAllowedTool returned error: %v", err)
	}
	select {
	case got := <-scheduled:
		if got.name != projectToolWriteFile || got.project != "demo" {
			t.Fatalf("scheduled sync = (%q, %q), want (%q, demo)", got.name, got.project, projectToolWriteFile)
		}
	case <-time.After(time.Second):
		t.Fatal("development sync was not scheduled")
	}
}

func TestEinoToolReplayDoesNotRepeatMutationOrSync(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	workspaces := workspace.NewFileStore(t.TempDir())
	server := NewWithWorkspace(nil, messages, workspaces, "", false)
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "test-project-uid-demo"
	scope := testProjectMessageScope("org-a", "ws-1", project.Name)
	started, err := server.startProjectAssistantRunDurablyWithMode(
		ctx,
		scope,
		"alice",
		"fix the app",
		"sync-replay-request",
		store.AssistantRunModeDefault,
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, started.Run, started.Assistant); err != nil {
		t.Fatal(err)
	}

	var mutationCalls int
	syncs := make(chan struct{}, 2)
	server.developmentSyncAfterMutation = func(identity, *aiv1alpha1.Project, string) error {
		syncs <- struct{}{}
		return nil
	}
	localTool := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			mutationCalls++
			return `{"operation":"apply_patch","paths":["src/App.tsx"]}`, nil
		},
	}
	run := started.Run
	req := projectAssistantRunRequest{
		Identity:          identity{orgUUID: "org-a", workspaceUUID: "ws-1", user: "alice"},
		Project:           project,
		WorkspaceScope:    projectWorkspaceScope(identity{orgUUID: "org-a", workspaceUUID: "ws-1"}, project),
		MessageScope:      scope,
		ToolPort:          projectAssistantDirectToolPort{},
		AssistantRun:      &run,
		ApprovalMode:      run.ApprovalMode,
		CollaborationMode: projectAssistantCollaborationModeDefault,
		eventLedger:       newProjectAssistantRunEventLedger(messages, scope, run.ID),
	}
	tool := projectEinoAssistantTool{server: server, tool: localTool, req: req, runState: newProjectEinoAssistantRunState()}
	args := map[string]any{"patch": "*** Begin Patch\n*** Add File: src/App.tsx\n+ok\n*** End Patch"}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := tool.invokeAllowedTool(ctx, "call-patch", localTool.Spec(), args); err != nil {
			t.Fatalf("invoke attempt %d: %v", attempt+1, err)
		}
	}
	if mutationCalls != 1 {
		t.Fatalf("mutation calls = %d, want one durable dispatch", mutationCalls)
	}
	select {
	case <-syncs:
	case <-time.After(time.Second):
		t.Fatal("original mutation did not schedule sync")
	}
	select {
	case <-syncs:
		t.Fatal("durable replay scheduled a second sync")
	case <-time.After(25 * time.Millisecond):
	}
	events, err := messages.ListAssistantRunEvents(ctx, scope, run.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("durable events = %#v, want one call/result pair", events)
	}
}

func TestEinoToolTracksPartialPatchMutationAfterIncompleteRollback(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	project := &aiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "test-project-uid-demo"}}
	scope := testProjectMessageScope("org-a", "ws-1", project.Name)
	started, err := server.startProjectAssistantRunDurablyWithMode(
		ctx, scope, "alice", "fix the app", "partial-patch-mutation", store.AssistantRunModeDefault,
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, started.Run, started.Assistant); err != nil {
		t.Fatal(err)
	}

	var mutationCalls int
	syncs := make(chan struct{}, 2)
	server.developmentSyncAfterMutation = func(identity, *aiv1alpha1.Project, string) error {
		syncs <- struct{}{}
		return nil
	}
	actual := workspace.MutationResult{
		Operation: "apply_patch",
		Path:      "src/App.tsx",
		Paths:     []string{"src/App.tsx"},
		Additions: 1,
		Deletions: 1,
	}
	localTool := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			mutationCalls++
			return projectAssistantToolJSONResult(actual, &workspace.PatchError{
				Code:          workspace.PatchErrorApplyFailed,
				Path:          "src/theme.css",
				Message:       "patch application failed; rollback was incomplete",
				ActualChanges: []workspace.MutationResult{actual},
			})
		},
	}
	run := started.Run
	runState := newProjectEinoAssistantRunState()
	runState.RecordObservedReadFile("src/App.tsx")
	tool := projectEinoAssistantTool{
		server: server,
		tool:   localTool,
		req: projectAssistantRunRequest{
			Identity:          identity{orgUUID: "org-a", workspaceUUID: "ws-1", user: "alice"},
			Project:           project,
			WorkspaceScope:    projectWorkspaceScope(identity{orgUUID: "org-a", workspaceUUID: "ws-1"}, project),
			MessageScope:      scope,
			ToolPort:          projectAssistantDirectToolPort{},
			AssistantRun:      &run,
			CollaborationMode: projectAssistantCollaborationModeDefault,
			eventLedger:       newProjectAssistantRunEventLedger(messages, scope, run.ID),
		},
		runState: runState,
	}
	args := map[string]any{"patch": "*** Begin Patch\n*** Update File: src/App.tsx\n@@\n-old\n+new\n*** End Patch"}
	result, err := tool.invokeAllowedTool(ctx, "call-partial-patch", localTool.Spec(), args)
	if err != nil {
		t.Fatalf("partial mutation result returned transport error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode partial mutation result: %v", err)
	}
	if projectToolString(decoded["status"]) != "partial_failure" ||
		!strings.Contains(projectToolString(decoded["message"]), "remain changed") {
		t.Fatalf("partial mutation result = %#v", decoded)
	}
	if projectEinoAssistantPhaseSuccessfulToolContent(result) {
		t.Fatalf("partial mutation result was classified as a successful patch: %s", result)
	}
	if !projectEinoAssistantSuccessfulWorkspaceMutationResult(projectToolApplyPatch, result) {
		t.Fatalf("partial mutation result lost its real workspace delta: %s", result)
	}
	if current, verified := runState.SourceMutationRevisions(); current != 1 || verified != 0 {
		t.Fatalf("source revisions = (%d, %d), want (1, 0)", current, verified)
	}
	if got := strings.Join(runState.SuccessfulMutationPaths(), ","); got != "src/App.tsx" {
		t.Fatalf("mutation paths = %q, want src/App.tsx", got)
	}
	if got := runState.ObservedReadFilePaths(); len(got) != 0 {
		t.Fatalf("stale observed reads = %#v, want invalidated", got)
	}
	select {
	case <-syncs:
	case <-time.After(time.Second):
		t.Fatal("partial workspace mutation did not schedule synchronization")
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, _ := runState.DevelopmentSyncEvidence(1)
		if status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("partial mutation sync status = %q, want succeeded", status)
		}
		time.Sleep(time.Millisecond)
	}

	replayed, err := tool.invokeAllowedTool(ctx, "call-partial-patch", localTool.Spec(), args)
	if err != nil || replayed != result {
		t.Fatalf("durable partial replay = (%q, %v), want original result", replayed, err)
	}
	if mutationCalls != 1 {
		t.Fatalf("partial mutation calls = %d, want one durable dispatch", mutationCalls)
	}
	select {
	case <-syncs:
		t.Fatal("durable partial replay scheduled a second sync")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestEinoToolFailedPatchReplayStaysFailedAndDoesNotSync(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	project := &aiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "test-project-uid-demo"}}
	scope := testProjectMessageScope("org-a", "ws-1", project.Name)
	started, err := server.startProjectAssistantRunDurablyWithMode(
		ctx, scope, "alice", "fix the app", "failed-patch-replay", store.AssistantRunModeDefault,
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, started.Run, started.Assistant); err != nil {
		t.Fatal(err)
	}

	var mutationCalls int
	syncs := make(chan struct{}, 1)
	server.developmentSyncAfterMutation = func(identity, *aiv1alpha1.Project, string) error {
		syncs <- struct{}{}
		return nil
	}
	localTool := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			mutationCalls++
			return "", errors.New("context did not match")
		},
	}
	run := started.Run
	var toolEvents []projectToolCallStreamEvent
	var builderEvents int
	req := projectAssistantRunRequest{
		Identity:          identity{orgUUID: "org-a", workspaceUUID: "ws-1", user: "alice"},
		Project:           project,
		WorkspaceScope:    projectWorkspaceScope(identity{orgUUID: "org-a", workspaceUUID: "ws-1"}, project),
		MessageScope:      scope,
		ToolPort:          projectAssistantDirectToolPort{},
		AssistantRun:      &run,
		ApprovalMode:      run.ApprovalMode,
		CollaborationMode: projectAssistantCollaborationModeDefault,
		eventLedger:       newProjectAssistantRunEventLedger(messages, scope, run.ID),
		StreamCallbacks: projectAssistantStreamCallbacks{
			OnToolCall: func(event projectToolCallStreamEvent) { toolEvents = append(toolEvents, event) },
			OnAssistantEvent: func(event projectAssistantEvent) {
				if event.BuilderEvent != nil && event.BuilderEvent.Type == projectBuilderEventWorkspaceChanged {
					builderEvents++
				}
			},
		},
	}
	tool := projectEinoAssistantTool{server: server, tool: localTool, req: req, runState: newProjectEinoAssistantRunState()}
	args := map[string]any{"patch": "*** Begin Patch\n*** Update File: src/App.tsx\n@@\n-old\n+new\n*** End Patch"}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := tool.invokeAllowedTool(ctx, "call-failed-patch", localTool.Spec(), args)
		if err != nil || !strings.HasPrefix(result, "Tool call failed:") {
			t.Fatalf("invoke attempt %d = (%q, %v), want shaped failure", attempt+1, result, err)
		}
	}
	if mutationCalls != 1 {
		t.Fatalf("mutation calls = %d, want one durable dispatch", mutationCalls)
	}
	select {
	case <-syncs:
		t.Fatal("failed mutation scheduled development sync")
	default:
	}
	if builderEvents != 0 {
		t.Fatalf("workspace_changed events = %d, want none", builderEvents)
	}
	for _, event := range toolEvents {
		if event.Status == "succeeded" || event.Mutation != nil {
			t.Fatalf("failed replay emitted successful mutation event: %#v", event)
		}
	}
}

func TestEinoToolFailedContextualPatchReopensSameRead(t *testing.T) {
	ctx := context.Background()
	runState := newProjectEinoAssistantRunState()
	readCalls := 0
	filesystem := projectEinoAssistantFilesystemTelemetryMiddleware(projectAssistantRunRequest{}, runState)
	read, err := filesystem.WrapInvokableToolCall(
		ctx,
		func(context.Context, string, ...einotool.Option) (string, error) {
			readCalls++
			return "     1\told\n", nil
		},
		&adk.ToolContext{Name: projectToolReadFile},
	)
	if err != nil {
		t.Fatal(err)
	}
	readArgs := `{"file_path":"src/App.tsx","offset":1,"limit":20}`
	if _, err := read(ctx, readArgs); err != nil {
		t.Fatal(err)
	}

	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	project := &aiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "test-project-uid-demo"}}
	scope := testProjectMessageScope("org-a", "ws-1", project.Name)
	started, err := server.startProjectAssistantRunDurablyWithMode(
		ctx, scope, "alice", "fix", "failed-patch-reread", store.AssistantRunModeDefault,
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, started.Run, started.Assistant); err != nil {
		t.Fatal(err)
	}
	localTool := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			return "", errors.New("context did not match")
		},
	}
	run := started.Run
	tool := projectEinoAssistantTool{
		server: server,
		tool:   localTool,
		req: projectAssistantRunRequest{
			Identity:          identity{orgUUID: "org-a", workspaceUUID: "ws-1", user: "alice"},
			Project:           project,
			WorkspaceScope:    projectWorkspaceScope(identity{orgUUID: "org-a", workspaceUUID: "ws-1"}, project),
			MessageScope:      scope,
			ToolPort:          projectAssistantDirectToolPort{},
			AssistantRun:      &run,
			CollaborationMode: projectAssistantCollaborationModeDefault,
			eventLedger:       newProjectAssistantRunEventLedger(messages, scope, run.ID),
		},
		runState: runState,
	}
	patchArgs := map[string]any{"patch": "*** Begin Patch\n*** Update File: src/App.tsx\n@@\n-old\n+new\n*** End Patch"}
	if result, err := tool.invokeAllowedTool(ctx, "call-patch", localTool.Spec(), patchArgs); err != nil || !strings.HasPrefix(result, "Tool call failed:") {
		t.Fatalf("failed patch = (%q, %v)", result, err)
	}
	if _, err := read(ctx, readArgs); err != nil {
		t.Fatal(err)
	}
	if readCalls != 2 {
		t.Fatalf("read dispatches = %d, want original read plus failed-patch recovery reread", readCalls)
	}
}

func TestEinoToolBlockedSyncCannotVerifyLatestMutation(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	project := &aiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "test-project-uid-demo"}}
	scope := testProjectMessageScope("org-a", "ws-1", project.Name)
	started, err := server.startProjectAssistantRunDurablyWithMode(
		ctx, scope, "alice", "fix", "blocked-sync", store.AssistantRunModeDefault,
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, started.Run, started.Assistant); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	server.developmentSyncAfterMutation = func(identity, *aiv1alpha1.Project, string) error {
		close(entered)
		<-release
		return nil
	}
	localTool := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			return `{"operation":"apply_patch","paths":["src/App.tsx"]}`, nil
		},
	}
	run := started.Run
	runState := newProjectEinoAssistantRunState()
	tool := projectEinoAssistantTool{
		server: server,
		tool:   localTool,
		req: projectAssistantRunRequest{
			Identity:          identity{orgUUID: "org-a", workspaceUUID: "ws-1", user: "alice"},
			Project:           project,
			WorkspaceScope:    projectWorkspaceScope(identity{orgUUID: "org-a", workspaceUUID: "ws-1"}, project),
			MessageScope:      scope,
			ToolPort:          projectAssistantDirectToolPort{},
			AssistantRun:      &run,
			CollaborationMode: projectAssistantCollaborationModeDefault,
			eventLedger:       newProjectAssistantRunEventLedger(messages, scope, run.ID),
		},
		runState: runState,
	}
	if _, err := tool.invokeAllowedTool(ctx, "call-patch", localTool.Spec(), map[string]any{"patch": "*** Begin Patch\n*** Add File: src/App.tsx\n+ok\n*** End Patch"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("development sync did not start")
	}
	status, reason := runState.DevelopmentSyncEvidence(1)
	result, err := formatProjectAssistantRuntimeVerification(ctx, &projectAssistantRuntimeVerificationContext{
		CheckedMutationRevision: 1,
		DevelopmentSyncStatus:   status,
		DevelopmentSyncFailure:  reason,
		Runtime: &projectAssistantRuntimeWorkflowResult{
			Status:     "ready",
			PreviewURL: "https://demo.example",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	runState.RecordDevelopmentVerificationResult(string(encoded))
	if result.Status == "ready" || runState.SourceMutationVerified() {
		t.Fatalf("blocked sync verification = %#v, source verified=%t", result, runState.SourceMutationVerified())
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		status, _ = runState.DevelopmentSyncEvidence(1)
		if status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sync status = %q, want succeeded after release", status)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEinoV2CommitArgumentsAreDerivedFromRunMutations(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	runState.RecordSuccessfulMutationPath("src/App.tsx")
	runState.RecordSuccessfulMutationPath("src/theme.css")
	tool := projectEinoAssistantTool{runState: runState}
	args, err := tool.v2CommitArguments(map[string]any{
		"repositoryRef": "repo",
		"paths":         []any{"src/App.tsx", "src/theme.css"},
		"message":       "fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(projectToolStringList(args["paths"]), ","); got != "src/App.tsx,src/theme.css" {
		t.Fatalf("derived paths = %q, want complete run mutation set", got)
	}
	if _, err := tool.v2CommitArguments(map[string]any{"paths": []any{"README.md"}}); err == nil || !strings.Contains(err.Error(), "not mutated") {
		t.Fatalf("unrelated commit path error = %v", err)
	}
	if _, err := tool.v2CommitArguments(map[string]any{"paths": []any{"src/App.tsx"}}); err == nil || !strings.Contains(err.Error(), "omit") {
		t.Fatalf("partial commit path error = %v", err)
	}
}

func TestEinoV2FreshRunCommitsPriorAndCurrentUncommittedPathsThenClearsState(t *testing.T) {
	ctx := context.Background()
	workspaces := workspace.NewFileStore(t.TempDir())
	scope := workspace.Scope{
		OrgUUID:       "org-a",
		WorkspaceUUID: "ws-1",
		ProjectName:   "demo",
		ProjectUID:    "project-uid",
	}
	if err := workspaces.ApplyFiles(ctx, scope, []workspace.File{
		{Path: "package.json", Content: `{"scripts":{"build":"vite build"}}`},
		{Path: "src/App.tsx", Content: "export const App = () => <main />\n"},
	}); err != nil {
		t.Fatalf("ApplyFiles: %v", err)
	}
	if _, err := workspaces.AddUncommittedPaths(ctx, scope, []string{"package.json"}); err != nil {
		t.Fatalf("seed prior uncommitted path: %v", err)
	}

	runState := newProjectEinoAssistantRunState()
	req := projectAssistantRunRequest{
		Workspace:      workspaces,
		WorkspaceScope: scope,
		AssistantRun: &store.AssistantRun{
			EngineVersion: store.AssistantEngineVersionV2,
			Mode:          store.AssistantRunModeDefault,
		},
	}
	if err := restoreProjectEinoAssistantUncommittedPaths(ctx, req, runState); err != nil {
		t.Fatalf("restoreProjectEinoAssistantUncommittedPaths: %v", err)
	}
	if got := strings.Join(runState.SuccessfulMutationPaths(), ","); got != "package.json" {
		t.Fatalf("restored paths = %q, want package.json", got)
	}

	runState.RecordSuccessfulMutationPath("src/App.tsx")
	if _, err := workspaces.AddUncommittedPaths(ctx, scope, []string{"src/App.tsx"}); err != nil {
		t.Fatalf("persist current uncommitted path: %v", err)
	}
	revision := runState.BeginDevelopmentSyncForNextMutation()
	runState.RecordSourceMutation()
	runState.CompleteDevelopmentSync(revision, nil)
	runState.RecordDevelopmentVerificationResult(`{"checkedMutationRevision":2,"status":"ready"}`)
	digest, err := projectEinoAssistantWorkspaceDigest(ctx, workspaces, scope, runState.SuccessfulMutationPaths())
	if err != nil {
		t.Fatalf("projectEinoAssistantWorkspaceDigest: %v", err)
	}
	runState.RecordVerifiedWorkspaceDigest(digest)
	runState.ConfigureCommitRequirement(true)

	tool := projectEinoAssistantTool{req: req, runState: runState}
	args, err := tool.v2CommitArguments(map[string]any{
		"repositoryRef": "demo-repo",
		"paths":         []any{"package.json", "src/App.tsx"},
		"message":       "Update application",
	})
	if err != nil {
		t.Fatalf("v2CommitArguments: %v", err)
	}
	if got := strings.Join(projectToolStringList(args["paths"]), ","); got != "package.json,src/App.tsx" {
		t.Fatalf("commit paths = %q, want prior and current paths", got)
	}
	if got := projectToolString(args["workspaceDigest"]); got == "" || got != digest {
		t.Fatalf("workspace digest = %q, want %q", got, digest)
	}

	lifecycle := &projectEinoAssistantLifecycle{
		v2:             true,
		runState:       runState,
		workspace:      workspaces,
		workspaceScope: scope,
	}
	commit := wrapProjectEinoAssistantLifecycleTool(
		t,
		lifecycle,
		projectToolCommitProjectFiles,
		func(context.Context, string, ...einotool.Option) (string, error) {
			return `{"commitSHA":"abc123"}`, nil
		},
	)
	commitCtx, cancelCommit := context.WithCancel(ctx)
	cancelCommit()
	if _, err := commit(commitCtx, projectEinoToolArgumentsString(args)); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if evidence := runState.CompletionEvidence(); !evidence.LatestMutationCommitted {
		t.Fatalf("completion evidence = %#v, want committed mutation", evidence)
	}
	paths, err := workspaces.UncommittedPaths(ctx, scope)
	if err != nil {
		t.Fatalf("UncommittedPaths after commit: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("uncommitted paths after commit = %v, want empty", paths)
	}
}

func TestEinoV2FreshRunCanVerifyAndCommitPriorUncommittedPathsWithoutAnotherEdit(t *testing.T) {
	ctx := context.Background()
	workspaces := workspace.NewFileStore(t.TempDir())
	scope := workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo", ProjectUID: "project-uid"}
	if err := workspaces.ApplyFiles(ctx, scope, []workspace.File{{Path: "package.json", Content: `{"name":"demo"}`}}); err != nil {
		t.Fatalf("ApplyFiles: %v", err)
	}
	if _, err := workspaces.AddUncommittedPaths(ctx, scope, []string{"package.json"}); err != nil {
		t.Fatalf("seed prior uncommitted path: %v", err)
	}

	runState := newProjectEinoAssistantRunState()
	req := projectAssistantRunRequest{
		Workspace: workspaces, WorkspaceScope: scope,
		AssistantRun: &store.AssistantRun{EngineVersion: store.AssistantEngineVersionV2, Mode: store.AssistantRunModeDefault},
	}
	if err := restoreProjectEinoAssistantUncommittedPaths(ctx, req, runState); err != nil {
		t.Fatalf("restoreProjectEinoAssistantUncommittedPaths: %v", err)
	}
	revision, _ := runState.SourceMutationRevisions()
	if revision != 1 {
		t.Fatalf("restored mutation revision = %d, want 1", revision)
	}
	runState.BeginDevelopmentSyncForCurrentMutation()
	runState.CompleteDevelopmentSync(revision, nil)
	runState.RecordDevelopmentVerificationResult(`{"checkedMutationRevision":1,"status":"ready"}`)
	digest, err := projectEinoAssistantWorkspaceDigest(ctx, workspaces, scope, runState.SuccessfulMutationPaths())
	if err != nil {
		t.Fatalf("projectEinoAssistantWorkspaceDigest: %v", err)
	}
	runState.RecordVerifiedWorkspaceDigest(digest)
	runState.ConfigureCommitRequirement(true)

	args, err := (projectEinoAssistantTool{req: req, runState: runState}).v2CommitArguments(map[string]any{
		"repositoryRef": "demo-repo",
		"paths":         []any{"package.json"},
		"message":       "Commit pending application source",
	})
	if err != nil {
		t.Fatalf("v2CommitArguments: %v", err)
	}
	if got := strings.Join(projectToolStringList(args["paths"]), ","); got != "package.json" {
		t.Fatalf("commit paths = %q, want package.json", got)
	}
	if got := projectToolString(args["workspaceDigest"]); got != digest {
		t.Fatalf("workspace digest = %q, want %q", got, digest)
	}
}

func TestEinoSelectTemplateRefreshesProjectUsedBySubsequentWorkspaceSync(t *testing.T) {
	var projectYAML string
	templateJSON, err := json.Marshal(applicationTemplateObject().Object)
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	graphQLServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "TemplateYaml"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"infrastructure_kedge_faros_sh": map[string]any{"v1alpha1": map[string]any{"TemplateYaml": string(templateJSON)}},
			}})
		case strings.Contains(req.Query, "ApplicationYaml"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"infrastructure_kedge_faros_sh": map[string]any{"v1alpha1": map[string]any{
					"ApplicationYaml": `{"apiVersion":"infrastructure.kedge.faros.sh/v1alpha1","kind":"Application","metadata":{"name":"demo-dev"},"status":{"phase":"Ready"}}`,
				}},
			}})
		case strings.Contains(req.Query, "ProjectYaml"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"ai_kedge_faros_sh": map[string]any{"v1alpha1": map[string]any{"ProjectYaml": projectYAML}},
			}})
		case strings.Contains(req.Query, "applyStatusYaml"):
			_, _ = w.Write([]byte(`{"data":{"applyStatusYaml":"ok"}}`))
		case strings.Contains(req.Query, "applyYaml"):
			appliedYAML, _ := req.Variables["yaml"].(string)
			if strings.HasPrefix(strings.TrimSpace(appliedYAML), "apiVersion: ai.kedge.faros.sh/") && strings.Contains(appliedYAML, "kind: Project\n") {
				projectYAML = appliedYAML
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"applyYaml": appliedYAML}})
		default:
			t.Fatalf("unexpected GraphQL query: %s", req.Query)
		}
	}))
	t.Cleanup(graphQLServer.Close)

	workspaces := workspace.NewFileStore(t.TempDir())
	server := NewWithWorkspace(
		tenant.NewGraphQLClient(graphQLServer.URL, false),
		nil,
		workspaces,
		"",
		false,
	)
	type scheduledSync struct {
		name    string
		project *aiv1alpha1.Project
	}
	scheduled := make(chan scheduledSync, 2)
	server.developmentSyncAfterMutation = func(_ identity, p *aiv1alpha1.Project, name string) error {
		scheduled <- scheduledSync{name: name, project: p.DeepCopy()}
		return nil
	}

	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "test-project-uid-demo"},
		Spec:       aiv1alpha1.ProjectSpec{DisplayName: "Demo"},
	}
	id := identity{
		clusterID:     "cluster-ws-1",
		token:         "caller-token",
		orgUUID:       "org-a",
		workspaceUUID: "ws-1",
	}
	req := projectAssistantRunRequest{
		Identity:           id,
		Project:            project,
		WorkspaceScope:     projectWorkspaceScope(id, project),
		ToolPort:           projectAssistantDirectToolPort{},
		executionAuthority: &projectAssistantExplicitTestAuthority{},
	}
	runState := newProjectEinoAssistantRunState()
	registry := server.projectAssistantToolRegistry()
	invoke := func(name string, arguments map[string]any) {
		t.Helper()
		localTool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("%s missing from registry", name)
		}
		tool := projectEinoAssistantTool{
			server:   server,
			tool:     localTool,
			req:      req,
			runState: runState,
		}
		if _, err := tool.invokeAllowedTool(context.Background(), "call-"+name, localTool.Spec(), arguments); err != nil {
			t.Fatalf("%s returned error: %v", name, err)
		}
	}

	invoke(projectToolSelectTemplate, map[string]any{"template": "application"})
	invoke(projectToolWriteFile, map[string]any{"path": "web/src/App.tsx", "content": "export default function App() {}\n"})

	syncs := make([]scheduledSync, 0, 2)
	for len(syncs) < 2 {
		select {
		case sync := <-scheduled:
			syncs = append(syncs, sync)
		case <-time.After(time.Second):
			t.Fatalf("scheduled syncs = %d, want 2", len(syncs))
		}
	}
	for _, sync := range syncs {
		if sync.project.Spec.Template == nil || sync.project.Spec.Template.Name != "application" {
			t.Fatalf("%s sync project template = %#v, want application", sync.name, sync.project.Spec.Template)
		}
	}
}

func TestProjectAssistantInitialExecutionPlanRequiresAcceptanceCriteriaAndBoundsWrites(t *testing.T) {
	_, err := projectAssistantInitialExecutionPlanFromArguments("Build Whisker Swipe.", map[string]any{
		"summary":     "Build the app",
		"steps":       []any{"Create the UI"},
		"targetPaths": []any{"src/"},
	})
	if err == nil {
		t.Fatal("initial execution plan without acceptance criteria succeeded")
	}

	plan, err := projectAssistantInitialExecutionPlanFromArguments("Build Whisker Swipe.", map[string]any{
		"summary":            "Build the app",
		"steps":              []any{"Create the UI", "Verify the preview"},
		"targetPaths":        []any{"src/", "package.json"},
		"acceptanceCriteria": []any{"The preview is ready"},
	})
	if err != nil {
		t.Fatalf("projectAssistantInitialExecutionPlanFromArguments: %v", err)
	}
	if plan.Goal != "Build Whisker Swipe." || !plan.RunLocal || plan.AllowAllWrites ||
		plan.ApprovalTool != projectToolDefineInitialProjectPlan {
		t.Fatalf("initial execution plan = %#v", plan)
	}
	if !projectAssistantApprovedPlanAllowsWrite(&plan, projectToolWriteFile, map[string]any{"path": "src/App.tsx"}) {
		t.Fatal("defined target path was not authorized")
	}
	if projectAssistantApprovedPlanAllowsWrite(&plan, projectToolWriteFile, map[string]any{"path": "api/index.js"}) {
		t.Fatal("out-of-plan path was authorized")
	}
}

func TestRefreshProjectToolSnapshotKeepsSelfAliasedProject(t *testing.T) {
	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "demo",
			Labels: map[string]string{"app": "demo"},
		},
		Spec: aiv1alpha1.ProjectSpec{
			DisplayName: "Demo",
			Template:    &aiv1alpha1.ProjectTemplateSpec{Name: "application"},
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
				Name: projectDevelopmentEnvironmentName,
				Mode: aiv1alpha1.ProjectEnvironmentModeLive,
			}},
		},
	}

	refreshProjectToolSnapshot(project, project)

	if project.Spec.Template == nil || project.Spec.Template.Name != "application" {
		t.Fatalf("template = %#v, want application", project.Spec.Template)
	}
	if len(project.Spec.Environments) != 1 || project.Spec.Environments[0].Name != projectDevelopmentEnvironmentName {
		t.Fatalf("environments = %#v, want development", project.Spec.Environments)
	}
	if project.Labels["app"] != "demo" {
		t.Fatalf("labels = %#v, want app=demo", project.Labels)
	}
}

func TestProjectEinoUnknownToolCountsAsNoProgress(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	runState.NextModelCallOrdinal()
	handler := projectEinoUnknownToolHandler(projectAssistantRunRequest{}, runState)
	result, err := handler(context.Background(), "hallucinated_tool", `{"path":"src/App.tsx"}`)
	if err != nil || !strings.Contains(result, "disallowed tool name") {
		t.Fatalf("unknown tool result = (%q, %v)", result, err)
	}
	if _, count := runState.ConsecutiveNoProgressModelCalls(); count != 1 {
		t.Fatalf("unknown tool model batch count = %d, want 1", count)
	}
}
