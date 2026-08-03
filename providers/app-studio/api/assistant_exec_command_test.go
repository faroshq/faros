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

	"github.com/faroshq/provider-app-studio/workspace"
)

func TestNormalizeProjectAssistantExecCommandInput(t *testing.T) {
	tests := []struct {
		name     string
		input    *projectAssistantExecCommandInput
		wantErr  string
		wantWork string
		wantTime int
	}{
		{name: "defaults", input: &projectAssistantExecCommandInput{Component: "backend", Argv: []string{"go", "test", "./..."}}, wantWork: "", wantTime: projectAssistantExecDefaultTimeout},
		{name: "cleans component relative workdir", input: &projectAssistantExecCommandInput{Component: "backend", Argv: []string{"go", "test"}, Workdir: "./internal"}, wantWork: "internal", wantTime: projectAssistantExecDefaultTimeout},
		{name: "rejects parent", input: &projectAssistantExecCommandInput{Component: "backend", Argv: []string{"go", "test"}, Workdir: "../other"}, wantErr: "under the selected component"},
		{name: "rejects absolute", input: &projectAssistantExecCommandInput{Component: "backend", Argv: []string{"go", "test"}, Workdir: "/workspace"}, wantErr: "relative path"},
		{name: "rejects windows separator", input: &projectAssistantExecCommandInput{Component: "backend", Argv: []string{"go", "test"}, Workdir: `internal\\test`}, wantErr: "relative path"},
		{name: "allows explicit interpreter argv", input: &projectAssistantExecCommandInput{Component: "backend", Argv: []string{"sh", "-c", "go test"}}, wantWork: "", wantTime: projectAssistantExecDefaultTimeout},
		{name: "rejects timeout", input: &projectAssistantExecCommandInput{Component: "backend", Argv: []string{"go", "test"}, TimeoutSeconds: projectAssistantExecMaxTimeout + 1}, wantErr: "timeoutSeconds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, blockers := normalizeProjectAssistantExecCommandInput(tt.input)
			if tt.wantErr == "" {
				if len(blockers) != 0 {
					t.Fatalf("blockers = %v", blockers)
				}
				if got.Workdir != tt.wantWork || got.TimeoutSeconds != tt.wantTime {
					t.Fatalf("normalized input = %#v", got)
				}
				return
			}
			if !strings.Contains(strings.Join(blockers, "; "), tt.wantErr) {
				t.Fatalf("blockers = %v, want %q", blockers, tt.wantErr)
			}
		})
	}
}

func TestProjectAssistantExecCommandContractAndPolicy(t *testing.T) {
	spec, ok := projectAssistantWorkflowToolSpec(projectToolExecCommand)
	if !ok {
		t.Fatal("exec_command workflow spec is missing")
	}
	if spec.Risk != projectAssistantToolRiskRuntime || spec.ParallelSafe {
		t.Fatalf("exec spec = %#v, want effectful exclusive runtime tool", spec)
	}
	if !projectAssistantToolHasEffect(spec) {
		t.Fatal("exec_command must be effectful")
	}
	debugging := projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileDebugging)
	if debugging.AllowsTool(spec) {
		t.Fatal("debugging tool catalog must not expose exec_command")
	}
	implementation := projectAssistantTurnPolicyForProfile(projectAssistantTurnProfileImplementation)
	if !implementation.AllowsTool(spec) {
		t.Fatal("implementation tool catalog must expose exec_command")
	}
	if projectAssistantOnRequestRequiresApproval(projectToolExecCommand) == false {
		t.Fatal("exec_command must require approval")
	}
	if got := projectAssistantToolsForCollaborationMode([]projectAssistantTool{projectAssistantToolFunc{spec: spec}}, projectAssistantCollaborationModePlan); len(got) != 0 {
		t.Fatal("plan mode must hide exec_command")
	}
}

func TestProjectAssistantExecSnapshotRoutesSelectedComponent(t *testing.T) {
	root := t.TempDir()
	files := workspace.NewFileStore(root)
	scope := workspace.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "project", ProjectUID: "uid"}
	if err := files.ApplyFiles(context.Background(), scope, []workspace.File{
		{Path: "backend/main.go", Content: "package main\n"},
		{Path: "frontend/index.html", Content: "<!doctype html>\n"},
	}); err != nil {
		t.Fatal(err)
	}
	got, digest, err := projectAssistantExecSnapshot(context.Background(), projectAssistantWorkflowRunContext{
		Workspace:      files,
		WorkspaceScope: scope,
	}, projectTemplateComponent{WorkspacePath: "backend"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || len(got) != 1 || got[0].Path != "main.go" || got[0].Content != "package main\n" {
		t.Fatalf("snapshot = %#v, digest = %q", got, digest)
	}
	wantDigest, err := files.WorkspaceDigest(context.Background(), scope, []string{"backend/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if digest != wantDigest {
		t.Fatalf("snapshot digest = %q, FileStore digest = %q", digest, wantDigest)
	}
}

func TestProjectAssistantExecSnapshotRejectsChangedMutationRevision(t *testing.T) {
	root := t.TempDir()
	files := workspace.NewFileStore(root)
	scope := workspace.Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "project", ProjectUID: "uid"}
	if err := files.ApplyFiles(context.Background(), scope, []workspace.File{{Path: "backend/main.go", Content: "package main\n"}}); err != nil {
		t.Fatal(err)
	}
	runState := newProjectEinoAssistantRunState()
	runState.RecordSourceMutation()
	_, _, err := projectAssistantExecSnapshot(context.Background(), projectAssistantWorkflowRunContext{
		Workspace:      files,
		WorkspaceScope: scope,
		RunState:       runState,
	}, projectTemplateComponent{WorkspacePath: "backend"}, 0)
	if !errors.Is(err, errProjectAssistantExecRevisionChanged) {
		t.Fatalf("snapshot error = %v, want mutation revision change", err)
	}
}

func TestProjectAssistantExecResultBoundsOutput(t *testing.T) {
	result := projectAssistantExecResult(projectSandboxExecResponse{SessionID: "session", State: "succeeded", Stdout: strings.Repeat("x", projectAssistantExecMaxOutput+1)}, "backend", 3, "sha256:digest", "succeeded", 2, "")
	if !result.OutputTruncated || len(result.Stdout) == 0 || result.Status != "succeeded" || result.SourceRevision != 3 {
		t.Fatalf("result = %#v", result)
	}
	empty := projectAssistantExecResult(projectSandboxExecResponse{State: "succeeded"}, "backend", 0, "sha256:empty", "succeeded", 0, "")
	if empty.OutputTruncated {
		t.Fatal("empty output must not be marked truncated")
	}
}

func TestProjectAssistantExecRequestIDStable(t *testing.T) {
	first := projectAssistantExecRequestID("run", "call")
	if first == "" || first != projectAssistantExecRequestID("run", "call") || first == projectAssistantExecRequestID("run", "other") {
		t.Fatalf("request IDs = %q", first)
	}
	if anonymous := projectAssistantExecRequestID("", ""); anonymous == "" || anonymous != projectAssistantExecRequestID("", "") {
		t.Fatalf("anonymous request ID = %q", anonymous)
	}
}
