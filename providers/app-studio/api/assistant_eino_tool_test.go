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
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
)

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

func TestEinoApprovePlanToolRejectsMissingAllowedOperations(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	tool := projectEinoAssistantTool{runState: runState}

	result := tool.invokeApprovedPlanTool(context.Background(), "call-plan", projectAssistantToolSpec{
		Name: projectToolRequestProjectPlanApproval,
		Risk: projectAssistantToolRiskPlan,
	}, map[string]any{
		"summary":            "Build dashboard",
		"steps":              []any{"Write app shell"},
		"targetPaths":        []any{"src/"},
		"acceptanceCriteria": []any{"src/App.tsx exists"},
	})

	if !strings.Contains(result, "allowedOperations is required") {
		t.Fatalf("result = %q, want allowedOperations validation error", result)
	}
	if plan := runState.ApprovedPlan(); plan != nil {
		t.Fatalf("approved plan = %#v, want nil after malformed approve_plan", plan)
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
		req:      projectAssistantRunRequest{},
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

func TestEinoToolSchedulesDevelopmentSyncAfterMutatingTool(t *testing.T) {
	runState := newProjectEinoAssistantRunState()
	server := &Server{}
	var gotName string
	var gotProjectName string
	server.developmentSyncAfterMutation = func(_ identity, p *aiv1alpha1.Project, name string) {
		gotName = name
		if p != nil {
			gotProjectName = p.Name
		}
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
	tool := projectEinoAssistantTool{
		server: server,
		tool:   localTool,
		req: projectAssistantRunRequest{
			Project:        project,
			WorkspaceScope: workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo"},
		},
		runState: runState,
	}

	if _, err := tool.invokeAllowedTool(context.Background(), "call-write", localTool.Spec(), map[string]any{"path": "src/App.tsx"}); err != nil {
		t.Fatalf("invokeAllowedTool returned error: %v", err)
	}
	if gotName != projectToolWriteFile || gotProjectName != "demo" {
		t.Fatalf("scheduled sync = (%q, %q), want (%q, demo)", gotName, gotProjectName, projectToolWriteFile)
	}
}
