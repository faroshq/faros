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
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
)

type projectAssistantV2DirectToolPort struct{}

func (projectAssistantV2DirectToolPort) DiscoverMCP(context.Context, identity, projectLLMSettings) ([]projectAssistantTool, bool, error) {
	return nil, false, nil
}

func (projectAssistantV2DirectToolPort) Invoke(ctx context.Context, tool projectAssistantTool, req projectAssistantToolCallRequest) (string, error) {
	return tool.Call(ctx, req)
}

type projectAssistantV2ToolHarness struct {
	server     *Server
	messages   *store.MemoryStore
	workspaces *workspace.FileStore
	project    *aiv1alpha1.Project
	scope      store.Scope
	req        projectAssistantRunRequest
}

func newProjectAssistantV2ToolHarness(t *testing.T, requestID string) projectAssistantV2ToolHarness {
	t.Helper()
	ctx := context.Background()
	messages := store.NewMemoryStore()
	workspaces := workspace.NewFileStore(t.TempDir())
	server := NewWithWorkspace(nil, messages, workspaces, "", false)
	project := &aiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "test-project-uid-demo"}}
	id := identity{orgUUID: "org-a", workspaceUUID: "ws-1", user: "alice"}
	scope := testProjectMessageScope(id.orgUUID, id.workspaceUUID, project.Name)
	started, err := server.startProjectAssistantRunDurablyWithMode(
		ctx,
		scope,
		id.user,
		"update the app",
		requestID,
		store.AssistantRunModeDefault,
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, started.Run, started.Assistant); err != nil {
		t.Fatal(err)
	}
	run := started.Run
	return projectAssistantV2ToolHarness{
		server: server, messages: messages, workspaces: workspaces, project: project, scope: scope,
		req: projectAssistantRunRequest{
			Identity: id, Project: project, Workspace: workspaces,
			WorkspaceScope: projectWorkspaceScope(id, project), MessageScope: scope,
			ToolPort: projectAssistantV2DirectToolPort{}, AssistantRun: &run,
			ApprovalMode: run.ApprovalMode, CollaborationMode: projectAssistantCollaborationModeDefault,
			eventLedger: newProjectAssistantRunEventLedger(messages, scope, run.ID),
		},
	}
}

func TestEinoV2MutationReplayDispatchesExactlyOnce(t *testing.T) {
	h := newProjectAssistantV2ToolHarness(t, "v2-replay")
	var calls int
	backend := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			calls++
			return `{"operation":"apply_patch","paths":["src/App.tsx"],"additions":1}`, nil
		},
	}
	runState := newProjectEinoAssistantRunState()
	tool := projectEinoAssistantTool{server: h.server, tool: backend, req: h.req, runState: runState}
	args := map[string]any{"patch": "*** Begin Patch\n*** Add File: src/App.tsx\n+ok\n*** End Patch"}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := tool.invokeAllowedTool(context.Background(), "call-patch", backend.Spec(), args); err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
	}
	if calls != 1 {
		t.Fatalf("backend calls = %d, want exactly one", calls)
	}
	if current, _ := runState.SourceMutationRevisions(); current != 1 {
		t.Fatalf("source mutation revision = %d, want one", current)
	}
	events, err := h.messages.ListAssistantRunEvents(context.Background(), h.scope, h.req.AssistantRun.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("durable events = %#v, want one call/result pair", events)
	}
}

type projectAssistantIncompleteThenCompleteModel struct {
	calls            int
	partialToolCall  bool
	alwaysIncomplete bool
	setupErrors      int
}

func (m *projectAssistantIncompleteThenCompleteModel) Generate(
	context.Context,
	[]*schema.Message,
	...einomodel.Option,
) (*schema.Message, error) {
	return nil, errors.New("Generate should not be called")
}

func (m *projectAssistantIncompleteThenCompleteModel) Stream(
	context.Context,
	[]*schema.Message,
	...einomodel.Option,
) (*schema.StreamReader[*schema.Message], error) {
	m.calls++
	if m.calls <= m.setupErrors {
		return nil, io.ErrUnexpectedEOF
	}
	message := schema.AssistantMessage("discarded partial", nil)
	if m.partialToolCall {
		message = schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "partial-call",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      projectToolApplyPatch,
				Arguments: `{"patch":`,
			},
		}})
	}
	if m.calls > 1 && !m.alwaysIncomplete {
		message = schema.AssistantMessage("recovered response", nil)
		message.ResponseMeta = &schema.ResponseMeta{FinishReason: "stop"}
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func TestEinoV2PublishesReconnectForPreStreamFailure(t *testing.T) {
	h := newProjectAssistantV2ToolHarness(t, "v2-pre-stream-retry")
	model := &projectAssistantIncompleteThenCompleteModel{setupErrors: 1}
	h.req.LLM = projectLLMSettings{
		Provider:          defaultProjectLLMProvider,
		MaxRetries:        5,
		RetryBackoff:      time.Millisecond,
		StreamIdleTimeout: time.Second,
	}
	var statuses []string
	h.req.StreamCallbacks.OnStatus = func(status string) { statuses = append(statuses, status) }
	engine := projectEinoAssistantEngine{
		server: h.server,
		newModel: func(context.Context, projectAssistantRunRequest, *projectEinoAssistantRunState) (einomodel.BaseChatModel, error) {
			return model, nil
		},
		newTools: func(context.Context, projectAssistantRunRequest, *projectEinoAssistantRunState) ([]einotool.BaseTool, error) {
			return nil, nil
		},
	}

	result, err := engine.StreamProjectAssistant(context.Background(), h.req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "recovered response" || model.calls != 2 {
		t.Fatalf("result = %q after %d calls, want recovered response after setup retry", result.Content, model.calls)
	}
	if len(statuses) != 1 || statuses[0] != "Model connection was interrupted; reconnecting 1/5" {
		t.Fatalf("statuses = %#v, want one reconnect warning", statuses)
	}
}

func TestEinoV2ExhaustionStopsAtConfiguredRetryCount(t *testing.T) {
	h := newProjectAssistantV2ToolHarness(t, "v2-stream-exhaustion")
	model := &projectAssistantIncompleteThenCompleteModel{alwaysIncomplete: true}
	h.req.LLM = projectLLMSettings{
		Provider:          defaultProjectLLMProvider,
		MaxRetries:        5,
		RetryBackoff:      time.Millisecond,
		StreamIdleTimeout: time.Second,
	}
	var statuses []string
	h.req.StreamCallbacks.OnStatus = func(status string) { statuses = append(statuses, status) }
	engine := projectEinoAssistantEngine{
		server: h.server,
		newModel: func(context.Context, projectAssistantRunRequest, *projectEinoAssistantRunState) (einomodel.BaseChatModel, error) {
			return model, nil
		},
		newTools: func(context.Context, projectAssistantRunRequest, *projectEinoAssistantRunState) ([]einotool.BaseTool, error) {
			return nil, nil
		},
	}

	_, err := engine.StreamProjectAssistant(context.Background(), h.req)
	var incomplete *projectEinoAssistantIncompleteStreamError
	if !errors.As(err, &incomplete) {
		t.Fatalf("terminal error = %v, want original incomplete-stream error", err)
	}
	if model.calls != 6 {
		t.Fatalf("model calls = %d, want initial call plus five retries", model.calls)
	}
	if len(statuses) != 5 || statuses[len(statuses)-1] != "Model connection was interrupted; reconnecting 5/5" {
		t.Fatalf("statuses = %#v, want exactly 1/5 through 5/5", statuses)
	}
}

func TestEinoV2DoesNotDispatchToolFromRejectedIncompleteResponse(t *testing.T) {
	h := newProjectAssistantV2ToolHarness(t, "v2-incomplete-tool-stream")
	model := &projectAssistantIncompleteThenCompleteModel{partialToolCall: true}
	h.req.LLM = projectLLMSettings{
		Provider:          defaultProjectLLMProvider,
		MaxRetries:        5,
		RetryBackoff:      time.Millisecond,
		StreamIdleTimeout: time.Second,
	}
	backendCalls := 0
	backend := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			backendCalls++
			return `{"operation":"apply_patch","paths":["src/App.tsx"]}`, nil
		},
	}
	engine := projectEinoAssistantEngine{
		server: h.server,
		newModel: func(context.Context, projectAssistantRunRequest, *projectEinoAssistantRunState) (einomodel.BaseChatModel, error) {
			return model, nil
		},
		newTools: func(_ context.Context, req projectAssistantRunRequest, state *projectEinoAssistantRunState) ([]einotool.BaseTool, error) {
			return []einotool.BaseTool{newProjectEinoAssistantServerTool(h.server, backend, req, state)}, nil
		},
	}

	result, err := engine.StreamProjectAssistant(context.Background(), h.req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "recovered response" || model.calls != 2 {
		t.Fatalf("result = %q after %d calls, want recovered response after one retry", result.Content, model.calls)
	}
	if backendCalls != 0 {
		t.Fatalf("backend calls = %d, want no dispatch from rejected incomplete response", backendCalls)
	}
	events, err := h.messages.ListAssistantRunEvents(context.Background(), h.scope, h.req.AssistantRun.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("durable tool events = %#v, want none from rejected incomplete response", events)
	}
}

func TestEinoV2RecoversIncompleteChatCompletionLikeCodex(t *testing.T) {
	h := newProjectAssistantV2ToolHarness(t, "v2-incomplete-stream")
	model := &projectAssistantIncompleteThenCompleteModel{}
	h.req.LLM = projectLLMSettings{
		Provider:          defaultProjectLLMProvider,
		MaxRetries:        5,
		RetryBackoff:      time.Millisecond,
		StreamIdleTimeout: time.Second,
	}
	var statuses []string
	var accepted []string
	h.req.StreamCallbacks = projectAssistantStreamCallbacks{
		OnStatus: func(status string) { statuses = append(statuses, status) },
		OnChunk:  func(content string) { accepted = append(accepted, content) },
	}
	engine := projectEinoAssistantEngine{
		server: h.server,
		newModel: func(context.Context, projectAssistantRunRequest, *projectEinoAssistantRunState) (einomodel.BaseChatModel, error) {
			return model, nil
		},
		newTools: func(context.Context, projectAssistantRunRequest, *projectEinoAssistantRunState) ([]einotool.BaseTool, error) {
			return nil, nil
		},
	}

	result, err := engine.StreamProjectAssistant(context.Background(), h.req)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want one retry", model.calls)
	}
	if result.Content != "recovered response" || strings.Join(accepted, "") != "recovered response" {
		t.Fatalf("accepted response = (%q, %#v), want only recovered response", result.Content, accepted)
	}
	foundReconnect := false
	for _, status := range statuses {
		if status == "Model connection was interrupted; reconnecting 1/5" {
			foundReconnect = true
		}
	}
	if !foundReconnect {
		t.Fatalf("statuses = %#v, want reconnect 1/5", statuses)
	}
}

func TestEinoV2PartialPatchRollbackTracksActualDeltaOnce(t *testing.T) {
	h := newProjectAssistantV2ToolHarness(t, "v2-partial-patch")
	actual := workspace.MutationResult{
		Operation: "apply_patch", Path: "src/App.tsx", Paths: []string{"src/App.tsx"}, Additions: 1, Deletions: 1,
	}
	var calls int
	backend := projectAssistantToolFunc{
		spec: projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite},
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			calls++
			return projectAssistantToolJSONResult(actual, &workspace.PatchError{
				Code: workspace.PatchErrorApplyFailed, Path: "src/theme.css",
				Message: "patch application failed; rollback was incomplete", ActualChanges: []workspace.MutationResult{actual},
			})
		},
	}
	runState := newProjectEinoAssistantRunState()
	runState.RecordObservedReadFile("src/App.tsx")
	tool := projectEinoAssistantTool{server: h.server, tool: backend, req: h.req, runState: runState}
	args := map[string]any{"patch": "*** Begin Patch\n*** Update File: src/App.tsx\n@@\n-old\n+new\n*** End Patch"}
	result, err := tool.invokeAllowedTool(context.Background(), "call-partial", backend.Spec(), args)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatal(err)
	}
	if projectToolString(decoded["status"]) != "partial_failure" || !strings.Contains(projectToolString(decoded["message"]), "remain changed") {
		t.Fatalf("partial result = %#v", decoded)
	}
	if current, verified := runState.SourceMutationRevisions(); current != 1 || verified != 0 {
		t.Fatalf("source revisions = (%d, %d), want (1, 0)", current, verified)
	}
	if got := strings.Join(runState.SuccessfulMutationPaths(), ","); got != "src/App.tsx" {
		t.Fatalf("changed paths = %q, want src/App.tsx", got)
	}
	if got := runState.ObservedReadFilePaths(); len(got) != 0 {
		t.Fatalf("stale read coverage = %#v, want invalidated", got)
	}
	replayed, err := tool.invokeAllowedTool(context.Background(), "call-partial", backend.Spec(), args)
	if err != nil || replayed != result || calls != 1 {
		t.Fatalf("replay = (%q, %v), calls=%d; want exact durable result and one dispatch", replayed, err, calls)
	}
}

func TestEinoV2CommitWorkspaceDigestRejectsPostApprovalChange(t *testing.T) {
	ctx := context.Background()
	workspaces := workspace.NewFileStore(t.TempDir())
	scope := workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo", ProjectUID: "project-uid"}
	if err := workspaces.ApplyFiles(ctx, scope, []workspace.File{{Path: "src/App.tsx", Content: "before\n"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaces.AddUncommittedPaths(ctx, scope, []string{"src/App.tsx"}); err != nil {
		t.Fatal(err)
	}
	runState := newProjectEinoAssistantRunState()
	runState.RecordSuccessfulMutationPath("src/App.tsx")
	runState.RecordSourceMutation()
	runState.RecordDevelopmentVerificationResult(`{"checkedMutationRevision":1,"status":"ready"}`)
	digest, err := projectEinoAssistantWorkspaceDigest(ctx, workspaces, scope, []string{"src/App.tsx"})
	if err != nil {
		t.Fatal(err)
	}
	runState.RecordVerifiedWorkspaceDigest(digest)
	tool := projectEinoAssistantTool{req: projectAssistantRunRequest{Workspace: workspaces, WorkspaceScope: scope}, runState: runState}
	args, err := tool.v2CommitArguments(ctx, map[string]any{"paths": []any{"src/App.tsx"}, "message": "Update app"})
	if err != nil {
		t.Fatal(err)
	}
	if got := projectToolString(args["workspaceDigest"]); got != digest {
		t.Fatalf("bound digest = %q, want %q", got, digest)
	}
	if err := tool.validateV2CommitWorkspace(ctx, args); err != nil {
		t.Fatalf("unchanged workspace rejected: %v", err)
	}
	if _, err := workspaces.WriteFile(ctx, scope, workspace.WriteOptions{Path: "src/App.tsx", Content: "after\n"}); err != nil {
		t.Fatal(err)
	}
	if err := tool.validateV2CommitWorkspace(ctx, args); err == nil || !strings.Contains(err.Error(), "changed after commit approval") {
		t.Fatalf("changed workspace error = %v", err)
	}
}

func TestEinoV2UsesPriorUncommittedPathsWithoutRestoringMutationRevision(t *testing.T) {
	ctx := context.Background()
	workspaces := workspace.NewFileStore(t.TempDir())
	scope := workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo", ProjectUID: "project-uid"}
	if err := workspaces.ApplyFiles(ctx, scope, []workspace.File{{Path: "package.json", Content: `{"name":"demo"}`}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaces.AddUncommittedPaths(ctx, scope, []string{"package.json"}); err != nil {
		t.Fatal(err)
	}
	runState := newProjectEinoAssistantRunState()
	req := projectAssistantRunRequest{Workspace: workspaces, WorkspaceScope: scope, CollaborationMode: projectAssistantCollaborationModeDefault}
	digest, err := projectEinoAssistantWorkspaceDigest(ctx, workspaces, scope, []string{"package.json"})
	if err != nil {
		t.Fatal(err)
	}
	args, err := (projectEinoAssistantTool{req: req, runState: runState}).v2CommitArguments(ctx, map[string]any{
		"paths": []any{"package.json"}, "message": "Commit pending source",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(projectToolStringList(args["paths"]), ","); got != "package.json" {
		t.Fatalf("commit paths = %q, want package.json", got)
	}
	if got := projectToolString(args["workspaceDigest"]); got != digest {
		t.Fatalf("workspace digest = %q, want %q", got, digest)
	}
	if revision, _ := runState.SourceMutationRevisions(); revision != 0 {
		t.Fatalf("durable dirty paths manufactured mutation revision %d", revision)
	}
}

func TestEinoV2ResumePreservesRunLocalMutationGrant(t *testing.T) {
	ctx := context.Background()
	h := newProjectAssistantV2ToolHarness(t, "v2-resume-run-local-grant")
	if err := h.workspaces.ApplyFiles(ctx, h.req.WorkspaceScope, []workspace.File{{
		Path: "src/App.tsx",
		Content: `export function App() {
  const greeting = "hello";
  const audience = "world";
  return greeting + " " + audience;
}
`,
	}}); err != nil {
		t.Fatal(err)
	}

	grant := normalizeProjectAssistantApprovedPlan(projectAssistantApprovedPlan{
		Goal:               "Update the app greeting",
		Summary:            "Change the greeting in src/App.tsx.",
		Steps:              []string{"read src/App.tsx", "update its greeting"},
		TargetPaths:        []string{"src/App.tsx"},
		Version:            projectAssistantApprovedPlanVersionWorkspaceMutation,
		Capabilities:       []string{projectAssistantCapabilityWorkspaceMutate},
		AcceptanceCriteria: []string{"The greeting says hello again."},
		ApprovalTool:       projectToolDefineInitialProjectPlan,
		RunLocal:           true,
	})
	h.req.InitialApprovedPlan = &grant
	h.req.TurnProfile = projectAssistantTurnProfileImplementation
	h.req.TurnPolicy = projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileImplementation)
	h.req.ApprovalMode = store.AssistantApprovalModeAlwaysAsk
	run := *h.req.AssistantRun
	run.ApprovalMode = store.AssistantApprovalModeAlwaysAsk
	h.req.AssistantRun = &run

	toolCall := func(id, name, arguments string) *schema.Message {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:   id,
			Type: "function",
			Function: schema.FunctionCall{
				Name:      name,
				Arguments: arguments,
			},
		}})
	}
	model := &repositoryFlowEinoChatModel{Steps: []repositoryFlowEinoModelStep{
		{Message: toolCall("call-runtime-before-resume", projectToolRestartRuntime, `{}`)},
		{Message: toolCall("call-read-after-resume", projectToolReadFile, `{"file_path":"src/App.tsx","offset":1,"limit":200}`)},
		{Message: toolCall("call-patch-after-resume", projectToolApplyPatch, `{"patch":"*** Begin Patch\n*** Update File: src/App.tsx\n@@\n export function App() {\n-  const greeting = \"hello\";\n+  const greeting = \"hello again\";\n   const audience = \"world\";\n   return greeting + \" \" + audience;\n }\n*** End Patch"}`)},
		{Message: toolCall("call-runtime-after-patch", projectToolRestartRuntime, `{}`)},
	}}
	var runtimeCalls int
	runtimeSpec, ok := projectAssistantWorkflowToolSpec(projectToolRestartRuntime)
	if !ok {
		t.Fatalf("%s workflow spec missing", projectToolRestartRuntime)
	}
	runtimeTool := projectAssistantToolFunc{
		spec: runtimeSpec,
		call: func(context.Context, projectAssistantToolCallRequest) (string, error) {
			runtimeCalls++
			return `{"status":"ready"}`, nil
		},
	}
	patchTool, ok := h.server.projectAssistantToolRegistry().Get(projectToolApplyPatch)
	if !ok {
		t.Fatalf("%s missing from registry", projectToolApplyPatch)
	}
	engine := projectEinoAssistantEngine{
		server: h.server,
		newModel: func(context.Context, projectAssistantRunRequest, *projectEinoAssistantRunState) (einomodel.BaseChatModel, error) {
			return model, nil
		},
		newTools: func(_ context.Context, req projectAssistantRunRequest, state *projectEinoAssistantRunState) ([]einotool.BaseTool, error) {
			return []einotool.BaseTool{
				newProjectEinoAssistantServerTool(h.server, runtimeTool, req, state),
				newProjectEinoAssistantServerTool(h.server, patchTool, req, state),
			}, nil
		},
	}

	_, err := engine.StreamProjectAssistant(ctx, h.req)
	var firstPermission *projectAssistantPermissionRequiredError
	if !errors.As(err, &firstPermission) {
		t.Fatalf("StreamProjectAssistant error = %v, want permission interrupt", err)
	}
	if firstPermission.ToolName != projectToolRestartRuntime {
		t.Fatalf("initial permission tool = %q, want %q", firstPermission.ToolName, projectToolRestartRuntime)
	}
	pending, err := h.messages.GetAssistantRun(ctx, h.scope, firstPermission.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint projectAssistantCheckpointState
	if err := json.Unmarshal(pending.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.ApprovedPlan == nil || !checkpoint.ApprovedPlan.RunLocal ||
		strings.Join(checkpoint.ApprovedPlan.TargetPaths, ",") != "src/App.tsx" {
		t.Fatalf("saved run-local grant = %#v", checkpoint.ApprovedPlan)
	}

	accumulator := h.server.projectAssistantSupervisor().accumulatorFor(h.scope, pending.ID)
	if accumulator == nil {
		t.Fatal("assistant run accumulator missing")
	}
	claimed, err := accumulator.ClaimPending(ctx, firstPermission.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	h.req.AssistantRun = &claimed
	h.req.eventLedger = nil
	_, err = engine.ResumeProjectAssistant(ctx, h.req, projectAssistantResumeRequest{
		RequestID: firstPermission.RequestID,
		Decision:  string(projectAssistantPermissionAllow),
	}, checkpoint)
	var secondPermission *projectAssistantPermissionRequiredError
	if !errors.As(err, &secondPermission) {
		t.Fatalf("ResumeProjectAssistant error = %v, want second permission interrupt", err)
	}
	if secondPermission.ToolName != projectToolRestartRuntime {
		t.Fatalf("resumed permission tool = %q, want %q", secondPermission.ToolName, projectToolRestartRuntime)
	}
	if runtimeCalls != 1 {
		t.Fatalf("approved runtime calls = %d, want one", runtimeCalls)
	}
	read, err := h.workspaces.ReadFile(ctx, h.req.WorkspaceScope, workspace.ReadOptions{Path: "src/App.tsx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Content, `const greeting = "hello again";`) {
		t.Fatalf("workspace content after resume = %q, want approved patch applied", read.Content)
	}
}
