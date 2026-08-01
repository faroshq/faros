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
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
)

func TestAssistantRegistryExposesOnlyContextualWorkspacePatchMutation(t *testing.T) {
	registry := projectAssistantLocalToolRegistry(nil)
	for _, retired := range []string{"write_file", "mkdir", "hydrate_workspace"} {
		if registry.Has(retired) {
			t.Fatalf("retired tool %s remains registered", retired)
		}
	}
	spec, ok := registry.Spec(projectToolApplyPatch)
	if !ok {
		t.Fatal("apply_patch is not model-visible")
	}
	var schema map[string]any
	if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
		t.Fatalf("decode apply_patch schema: %v", err)
	}
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) != 1 || properties["patch"] == nil {
		t.Fatalf("apply_patch properties = %#v, want only patch", properties)
	}
	if got := strings.Join(projectToolStringList(schema["required"]), ","); got != "patch" {
		t.Fatalf("required = %q, want patch", got)
	}
	for _, forbidden := range []string{"oldText", "newText", "replaceAll"} {
		if strings.Contains(string(spec.Parameters), forbidden) {
			t.Fatalf("apply_patch schema still contains %q: %s", forbidden, spec.Parameters)
		}
	}
	if strings.Contains(spec.Description, "'*** Delete File:") || strings.Contains(spec.Description, "optional '*** Move to:") {
		t.Fatalf("apply_patch still advertises repository-incompatible deletion semantics: %s", spec.Description)
	}
}

func TestAssistantV2ToolCatalogIsStableForCollaborationMode(t *testing.T) {
	server := NewWithWorkspace(nil, store.NewMemoryStore(), workspace.NewFileStore(t.TempDir()), "", false)
	tests := []struct {
		name      string
		mode      projectAssistantCollaborationMode
		profile   projectAssistantTurnProfile
		wantPatch bool
	}{
		{name: "default", mode: projectAssistantCollaborationModeDefault, profile: projectAssistantTurnProfileImplementation, wantPatch: true},
		{name: "plan", mode: projectAssistantCollaborationModePlan, profile: projectAssistantTurnProfileDebugging},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := store.AssistantRun{Mode: store.AssistantRunMode(tt.mode)}
			req := projectAssistantRunRequest{
				AssistantRun:      &run,
				CollaborationMode: tt.mode,
				TurnProfile:       tt.profile,
				TurnPolicy:        projectAssistantTurnPolicyForProfile(tt.profile),
			}
			state := newProjectEinoAssistantRunState()
			state.SetToolDiscovery(projectEinoAssistantToolDiscovery{})
			tools, err := newProjectEinoAssistantToolsFactory(server)(context.Background(), req, state)
			if err != nil {
				t.Fatal(err)
			}
			names := map[string]bool{}
			for _, tool := range tools {
				info, err := tool.Info(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				names[projectToolBaseName(info.Name)] = true
			}
			if names[projectToolApplyPatch] != tt.wantPatch {
				t.Fatalf("tools = %#v, apply_patch presence = %t, want %t", names, names[projectToolApplyPatch], tt.wantPatch)
			}
			for _, retired := range []string{"write_file", "mkdir", "hydrate_workspace", "request_project_plan_approval"} {
				if names[retired] {
					t.Fatalf("tools = %#v, retired %s remains model-visible", names, retired)
				}
			}
			if tt.mode == projectAssistantCollaborationModePlan {
				for _, effect := range []string{
					projectToolApplyPatch,
					projectToolSelectTemplate,
					projectToolRestartRuntime,
					projectToolSetRuntimeEnv,
					projectToolCommitProjectFiles,
				} {
					if names[effect] {
						t.Fatalf("Plan tools = %#v, effect %s remains visible", names, effect)
					}
				}
			}
		})
	}
}

func TestAssistantV2MutationAdmissionRejectsStoppedRun(t *testing.T) {
	ctx := context.Background()
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-1"}
	now := time.Now().UTC()
	run := store.AssistantRun{
		ID: "run-1", Mode: store.AssistantRunModeDefault, Status: store.AssistantRunStatusRunning,
		ClientRequestID: "request-1", UserMessageID: "user-1", ActiveMessageID: "assistant-1",
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	user := store.Message{ID: run.UserMessageID, Role: "user", ActorID: "actor-1", Content: "edit it", CreatedAt: now, UpdatedAt: now}
	assistant := store.Message{ID: run.ActiveMessageID, Role: "assistant", CreatedAt: now, UpdatedAt: now}
	created, err := messages.CreateAssistantRun(ctx, scope, user, assistant, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.projectAssistantSupervisor().Attach(scope, created, assistant); err != nil {
		t.Fatal(err)
	}
	tool := projectEinoAssistantTool{
		server: server,
		req: projectAssistantRunRequest{
			Identity: identity{user: user.ActorID}, MessageScope: scope,
			CollaborationMode: projectAssistantCollaborationModeDefault, AssistantRun: &created,
		},
		runState: newProjectEinoAssistantRunState(),
	}
	spec := projectAssistantToolSpec{Name: projectToolApplyPatch, Risk: projectAssistantToolRiskWrite}
	if err := tool.admitMutation(ctx, spec); err != nil {
		t.Fatalf("admit running mutation: %v", err)
	}
	if _, stopped, err := server.projectAssistantSupervisor().Stop(scope, created.ID); err != nil || !stopped {
		t.Fatalf("Stop = %v, %v", stopped, err)
	}
	if err := tool.admitMutation(ctx, spec); !errors.Is(err, store.ErrAssistantRunConflict) {
		t.Fatalf("admit stopped mutation = %v, want run conflict", err)
	}
}

func TestAssistantV2LifecycleBlocksCommitBeforeCurrentVerification(t *testing.T) {
	state := newProjectEinoAssistantRunState()
	state.RecordSourceMutation()
	lifecycle := projectEinoAssistantLifecycleMiddleware(projectAssistantRunRequest{}, state).(*projectEinoAssistantLifecycle)
	called := false
	wrapped, err := lifecycle.WrapInvokableToolCall(
		context.Background(),
		func(context.Context, string, ...einotool.Option) (string, error) {
			called = true
			return `{"status":"succeeded"}`, nil
		},
		&adk.ToolContext{Name: projectToolCommitProjectFiles},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := wrapped(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if called || !strings.Contains(result, "verify_development_runtime") {
		t.Fatalf("commit result = %q, endpoint called = %v", result, called)
	}
}

func TestAssistantContextualPatchRejectsDeleteAndMoveUntilCommitBridgeSupportsThem(t *testing.T) {
	workspaces := workspace.NewFileStore(t.TempDir())
	scope := workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo", ProjectUID: "test-project-uid"}
	if err := workspaces.ApplyFiles(context.Background(), scope, []workspace.File{{Path: "old.txt", Content: "old\n"}}); err != nil {
		t.Fatal(err)
	}
	tool, ok := projectAssistantLocalToolRegistry(&Server{workspaces: workspaces}).Get(projectToolApplyPatch)
	if !ok {
		t.Fatal("apply_patch tool was not registered")
	}
	for name, patch := range map[string]string{
		"delete": "*** Begin Patch\n*** Delete File: old.txt\n*** End Patch",
		"move":   "*** Begin Patch\n*** Update File: old.txt\n*** Move to: new.txt\n@@\n-old\n+new\n*** End Patch",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Call(context.Background(), projectAssistantToolCallRequest{
				WorkspaceScope:        scope,
				EnforceMutationSafety: true,
				ObservedReadFiles:     []string{"old.txt"},
				Arguments:             map[string]any{"patch": patch},
			})
			if err == nil || !strings.Contains(err.Error(), "repository commits support deletions") {
				t.Fatalf("error = %v, want repository deletion limitation", err)
			}
		})
	}
	read, err := workspaces.ReadFile(context.Background(), scope, workspace.ReadOptions{Path: "old.txt"})
	if err != nil || read.Content != "old\n" {
		t.Fatalf("source changed after rejected operations: content=%q err=%v", read.Content, err)
	}
}

func TestAssistantContextualPatchRequiresReadsAndDoesNotCreateLegacySnapshot(t *testing.T) {
	workspaces := workspace.NewFileStore(t.TempDir())
	scope := workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo", ProjectUID: "test-project-uid"}
	if err := workspaces.ApplyFiles(context.Background(), scope, []workspace.File{{Path: "src/app.js", Content: "export const theme = 'light'\n"}}); err != nil {
		t.Fatalf("ApplyFiles returned error: %v", err)
	}
	tool, ok := projectAssistantLocalToolRegistry(&Server{workspaces: workspaces}).Get(projectToolApplyPatch)
	if !ok {
		t.Fatal("apply_patch tool was not registered")
	}
	patch := `*** Begin Patch
*** Update File: src/app.js
@@
-export const theme = 'light'
+export const theme = 'dark'
*** Add File: src/new.js
+export const created = true
*** End Patch`
	req := projectAssistantToolCallRequest{
		WorkspaceScope:        scope,
		AssistantRunID:        "run-patch",
		EnforceMutationSafety: true,
		Arguments:             map[string]any{"patch": patch},
	}
	if _, err := tool.Call(context.Background(), req); err == nil || !strings.Contains(err.Error(), `read_file must successfully read "src/app.js"`) {
		t.Fatalf("patch without source read error = %v", err)
	}
	req.ObservedReadFiles = []string{"src/app.js"}
	resultJSON, err := tool.Call(context.Background(), req)
	if err != nil {
		t.Fatalf("patch after source read returned error: %v", err)
	}
	var result workspace.MutationResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode mutation result: %v", err)
	}
	if result.Operation != "apply_patch" || len(result.Files) != 2 || strings.Join(result.Paths, ",") != "src/app.js,src/new.js" {
		t.Fatalf("mutation result = %#v", result)
	}
	if _, err := workspaces.RestoreSnapshot(context.Background(), scope, "run-patch"); !errors.Is(err, workspace.ErrSnapshotNotFound) {
		t.Fatalf("RestoreSnapshot error = %v, want ErrSnapshotNotFound", err)
	}
	read, err := workspaces.ReadFile(context.Background(), scope, workspace.ReadOptions{Path: "src/app.js"})
	if err != nil || read.Content != "export const theme = 'dark'\n" {
		t.Fatalf("patched source = %#v, err = %v", read, err)
	}
	created, err := workspaces.ReadFile(context.Background(), scope, workspace.ReadOptions{Path: "src/new.js"})
	if err != nil || created.Content != "export const created = true\n" {
		t.Fatalf("patched Add File target = %#v, err = %v", created, err)
	}
}

func TestAssistantContextualPatchAllowsAddWithoutPriorRead(t *testing.T) {
	workspaces := workspace.NewFileStore(t.TempDir())
	scope := workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo", ProjectUID: "test-project-uid"}
	tool, ok := projectAssistantLocalToolRegistry(&Server{workspaces: workspaces}).Get(projectToolApplyPatch)
	if !ok {
		t.Fatal("apply_patch tool was not registered")
	}
	_, err := tool.Call(context.Background(), projectAssistantToolCallRequest{
		WorkspaceScope:        scope,
		AssistantRunID:        "run-add",
		EnforceMutationSafety: true,
		Arguments: map[string]any{"patch": `*** Begin Patch
*** Add File: nested/app.js
+export default true
*** End Patch`},
	})
	if err != nil {
		t.Fatalf("Add File without prior read returned error: %v", err)
	}
	if _, err := workspaces.RestoreSnapshot(context.Background(), scope, "run-add"); err != nil && !errors.Is(err, workspace.ErrSnapshotNotFound) {
		t.Fatalf("RestoreSnapshot returned error: %v", err)
	}
}
