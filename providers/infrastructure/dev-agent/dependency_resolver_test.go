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

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type dependencyExecDispatcherFunc func(context.Context, persistentExecRequest) (execResponse, error)

func (f dependencyExecDispatcherFunc) Execute(ctx context.Context, req persistentExecRequest) (execResponse, error) {
	return f(ctx, req)
}

func TestRunDependencyResolverExecutesInsideSharedIsolatedStage(t *testing.T) {
	workspaceRoot := t.TempDir()
	files := []syncFile{
		{Path: "go.mod", Content: "module example.test/app\n\ngo 1.22\n"},
		{Path: "main.go", Content: "package main\n"},
	}
	dispatcher := dependencyExecDispatcherFunc(func(_ context.Context, req persistentExecRequest) (execResponse, error) {
		if req.SourceRevision != 7 || req.SourceDigest != "source-digest" {
			t.Fatalf("source fence = %d %q", req.SourceRevision, req.SourceDigest)
		}
		if !strings.HasPrefix(req.WorkDir, ".faros-dependency-resolver-") {
			t.Fatalf("resolver workdir=%q", req.WorkDir)
		}
		goMod, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(req.WorkDir), "go.mod"))
		if err != nil || !strings.Contains(string(goMod), "example.test/app") {
			t.Fatalf("staged go.mod=%q err=%v", goMod, err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, filepath.FromSlash(req.WorkDir), "go.sum"), []byte("resolved checksum\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return execResponse{ExitCode: 0}, nil
	})

	resolved, err := runDependencyResolver(t.Context(), workspaceRoot, "go", ".", files, 7, "source-digest", dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(resolved.Writes["go.sum"]); got != "resolved checksum\n" {
		t.Fatalf("resolved go.sum=%q", got)
	}
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("resolver stage was not cleaned up: %v", entries)
	}
}

func TestWorkspaceResolveDependenciesCommitsOwnedFiles(t *testing.T) {
	server := newTestAgent(t, &agentConfig{WorkDir: t.TempDir()})
	seeded := seedDependencyResolverWorkspace(t, server)
	server.resolveDependencies = func(_ context.Context, ecosystem, workDir string, files []syncFile) (dependencyResolverResult, error) {
		if ecosystem != "go" || workDir != "." || !syncFilesContainPath(files, "go.mod") {
			t.Fatalf("resolver input ecosystem=%q workDir=%q files=%+v", ecosystem, workDir, files)
		}
		return dependencyResolverResult{
			Writes: map[string][]byte{
				"go.mod": []byte("module example.test/app\n\ngo 1.22\n\nrequire example.test/dependency v1.0.0\n"),
				"go.sum": []byte("example.test/dependency v1.0.0 h1:checksum\n"),
			},
		}, nil
	}

	rec, raw := workspaceRequest(t, server, http.MethodPost, "/workspace/resolve-dependencies", workspaceResolveDependenciesRequest{
		Ecosystem:        "go",
		ExpectedRevision: seeded.SourceRevision,
		ExpectedDigest:   seeded.SourceDigest,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", rec.Code, raw)
	}
	var resolved workspaceResolveDependenciesResponse
	if err := json.Unmarshal(raw, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "succeeded" || !resolved.Changed || resolved.SourceRevision != seeded.SourceRevision+1 || resolved.SourceDigest == seeded.SourceDigest {
		t.Fatalf("resolve response=%+v", resolved)
	}
	if !slices.Equal(resolved.Paths, []string{"go.mod", "go.sum"}) {
		t.Fatalf("resolved paths=%v", resolved.Paths)
	}
	if got, err := os.ReadFile(filepath.Join(server.config.WorkDir, "go.sum")); err != nil || !strings.Contains(string(got), "h1:checksum") {
		t.Fatalf("go.sum=%q err=%v", got, err)
	}
	assertDependencyResolverManifest(t, server, resolved.SourceRevision, resolved.SourceDigest)
}

func TestWorkspaceResolveDependenciesFailureLeavesSourceUntouched(t *testing.T) {
	server := newTestAgent(t, &agentConfig{WorkDir: t.TempDir()})
	seeded := seedDependencyResolverWorkspace(t, server)
	server.resolveDependencies = func(context.Context, string, string, []syncFile) (dependencyResolverResult, error) {
		return dependencyResolverResult{ExitCode: 1, Stderr: "module download failed", Writes: map[string][]byte{"go.sum": []byte("partial")}}, nil
	}

	rec, raw := workspaceRequest(t, server, http.MethodPost, "/workspace/resolve-dependencies", workspaceResolveDependenciesRequest{
		Ecosystem:        "go",
		ExpectedRevision: seeded.SourceRevision,
		ExpectedDigest:   seeded.SourceDigest,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", rec.Code, raw)
	}
	var resolved workspaceResolveDependenciesResponse
	if err := json.Unmarshal(raw, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "failed" || resolved.Changed || resolved.ExitCode != 1 || resolved.SourceRevision != seeded.SourceRevision || resolved.SourceDigest != seeded.SourceDigest {
		t.Fatalf("resolve response=%+v", resolved)
	}
	if _, err := os.Stat(filepath.Join(server.config.WorkDir, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("failed resolution wrote go.sum: %v", err)
	}
	assertDependencyResolverManifest(t, server, seeded.SourceRevision, seeded.SourceDigest)
}

func TestWorkspaceResolveDependenciesRejectsUnownedOutput(t *testing.T) {
	server := newTestAgent(t, &agentConfig{WorkDir: t.TempDir()})
	seeded := seedDependencyResolverWorkspace(t, server)
	server.resolveDependencies = func(context.Context, string, string, []syncFile) (dependencyResolverResult, error) {
		return dependencyResolverResult{Writes: map[string][]byte{"main.go": []byte("package compromised\n")}}, nil
	}

	rec, _ := workspaceRequest(t, server, http.MethodPost, "/workspace/resolve-dependencies", workspaceResolveDependenciesRequest{
		Ecosystem:        "go",
		ExpectedRevision: seeded.SourceRevision,
		ExpectedDigest:   seeded.SourceDigest,
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("resolve status=%d, want %d", rec.Code, http.StatusBadGateway)
	}
	got, err := os.ReadFile(filepath.Join(server.config.WorkDir, "main.go"))
	if err != nil || string(got) != "package main\n" {
		t.Fatalf("main.go=%q err=%v", got, err)
	}
	assertDependencyResolverManifest(t, server, seeded.SourceRevision, seeded.SourceDigest)
}

func seedDependencyResolverWorkspace(t *testing.T, server *agentServer) syncResponse {
	t.Helper()
	rec, raw := workspaceRequest(t, server, http.MethodPost, "/workspace/seed", workspaceSeedRequest{Files: []syncFile{
		{Path: "go.mod", Content: "module example.test/app\n\ngo 1.22\n"},
		{Path: "main.go", Content: "package main\n"},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status=%d body=%s", rec.Code, raw)
	}
	var seeded syncResponse
	if err := json.Unmarshal(raw, &seeded); err != nil {
		t.Fatal(err)
	}
	return seeded
}

func assertDependencyResolverManifest(t *testing.T, server *agentServer, revision uint64, digest string) {
	t.Helper()
	root := mustOpenWorkspaceRoot(t, server.config.WorkDir)
	defer func() { _ = root.Close() }()
	manifest, found, err := readWorkspaceManifest(root)
	if err != nil || !found {
		t.Fatalf("manifest found=%t err=%v", found, err)
	}
	if manifest.SourceRevision != revision || manifest.SourceDigest != digest {
		t.Fatalf("manifest=%+v want revision=%d digest=%s", manifest, revision, digest)
	}
	if err := verifyWorkspaceManifest(root, manifest); err != nil {
		t.Fatalf("verify manifest: %v", err)
	}
}
