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
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const dependencyResolverTimeout = 2 * time.Minute

type workspaceResolveDependenciesRequest struct {
	Ecosystem        string `json:"ecosystem"`
	WorkDir          string `json:"workDir,omitempty"`
	ExpectedRevision uint64 `json:"expectedRevision"`
	ExpectedDigest   string `json:"expectedDigest"`
}

type workspaceResolveDependenciesResponse struct {
	Status                 string   `json:"status"`
	Ecosystem              string   `json:"ecosystem"`
	Changed                bool     `json:"changed"`
	Paths                  []string `json:"paths,omitempty"`
	ExitCode               int      `json:"exitCode"`
	Stdout                 string   `json:"stdout,omitempty"`
	Stderr                 string   `json:"stderr,omitempty"`
	SourceRevision         uint64   `json:"sourceRevision"`
	SourceDigest           string   `json:"sourceDigest"`
	NamedServicesRestarted int      `json:"namedServicesRestarted,omitempty"`
}

type dependencyResolverResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Writes   map[string][]byte
	Deletes  []string
}

type dependencyResolverFunc func(context.Context, string, string, []syncFile) (dependencyResolverResult, error)

// handleWorkspaceResolveDependencies is a source transaction, not an open
// command endpoint. The resolver runs against an isolated copy and only the
// ecosystem-owned manifest files are imported into the managed workspace.
// A failed resolver therefore cannot leave half-written source behind.
func (s *agentServer) handleWorkspaceResolveDependencies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req workspaceResolveDependenciesRequest
	if err := decodeBoundedJSON(w, r, workspaceRequestMaxBytes, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ecosystem := strings.ToLower(strings.TrimSpace(req.Ecosystem))
	if ecosystem != "go" {
		http.Error(w, "ecosystem must be go", http.StatusBadRequest)
		return
	}
	workDir, err := cleanWorkspaceDirectory(req.WorkDir)
	if err != nil {
		http.Error(w, "invalid workDir: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.ExpectedRevision == 0 || normalizeSourceDigest(req.ExpectedDigest) == "" {
		http.Error(w, "expectedRevision and expectedDigest are required", http.StatusBadRequest)
		return
	}
	root, err := openWorkspaceRoot(s.config.WorkDir)
	if err != nil {
		http.Error(w, "open workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = root.Close() }()
	manifest, found, err := readWorkspaceManifest(root)
	if err != nil || !found {
		http.Error(w, "workspace is not seeded", http.StatusConflict)
		return
	}
	if err := verifyWorkspaceManifest(root, manifest); err != nil {
		http.Error(w, "workspace manifest verification failed: "+err.Error(), http.StatusConflict)
		return
	}
	if manifest.SourceRevision != req.ExpectedRevision || normalizeSourceDigest(manifest.SourceDigest) != normalizeSourceDigest(req.ExpectedDigest) {
		http.Error(w, "workspace revision or digest no longer matches expected evidence", http.StatusConflict)
		return
	}
	files, err := readManagedFiles(root, manifest.Files)
	if err != nil {
		http.Error(w, "read managed workspace: "+err.Error(), http.StatusConflict)
		return
	}
	goModPath := path.Join(workDir, "go.mod")
	if workDir == "." {
		goModPath = "go.mod"
	}
	if !syncFilesContainPath(files, goModPath) {
		http.Error(w, fmt.Sprintf("%s does not contain a managed go.mod", workDir), http.StatusBadRequest)
		return
	}
	resolveCtx, cancel := context.WithTimeout(r.Context(), dependencyResolverTimeout)
	defer cancel()
	var resolved dependencyResolverResult
	if s.resolveDependencies != nil {
		resolved, err = s.resolveDependencies(resolveCtx, ecosystem, workDir, files)
	} else {
		resolved, err = runDependencyResolver(resolveCtx, s.config.WorkDir, ecosystem, workDir, files, manifest.SourceRevision, manifest.SourceDigest, s.dependencyExecutor)
	}
	if err != nil {
		http.Error(w, "resolve dependencies: "+err.Error(), http.StatusBadGateway)
		return
	}
	response := workspaceResolveDependenciesResponse{
		Status:         "succeeded",
		Ecosystem:      ecosystem,
		ExitCode:       resolved.ExitCode,
		Stdout:         resolved.Stdout,
		Stderr:         resolved.Stderr,
		SourceRevision: manifest.SourceRevision,
		SourceDigest:   manifest.SourceDigest,
	}
	if resolved.ExitCode != 0 {
		response.Status = "failed"
		writeJSON(w, http.StatusOK, response)
		return
	}
	contentByPath := make(map[string][]byte, len(files)+len(resolved.Writes))
	for _, file := range files {
		contentByPath[file.Path] = []byte(file.Content)
	}
	for raw, content := range resolved.Writes {
		clean, err := cleanWorkspacePath(raw)
		if err != nil || !dependencyResolverOwnsPath(ecosystem, workDir, clean) {
			http.Error(w, fmt.Sprintf("resolver returned unauthorized path %q", raw), http.StatusBadGateway)
			return
		}
		if len(content) > workspaceMaxFileBytes || !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 {
			http.Error(w, fmt.Sprintf("resolver returned invalid content for %q", clean), http.StatusBadGateway)
			return
		}
		contentByPath[clean] = content
	}
	for _, raw := range resolved.Deletes {
		clean, err := cleanWorkspacePath(raw)
		if err != nil || !dependencyResolverOwnsPath(ecosystem, workDir, clean) {
			http.Error(w, fmt.Sprintf("resolver returned unauthorized deletion %q", raw), http.StatusBadGateway)
			return
		}
		delete(contentByPath, clean)
	}
	updated := syncFilesFromContent(contentByPath)
	newDigest, err := digestSyncFiles(updated)
	if err != nil {
		http.Error(w, "digest resolved workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if newDigest == normalizeSourceDigest(manifest.SourceDigest) {
		writeJSON(w, http.StatusOK, response)
		return
	}
	changed, deleted, err := applyManagedContent(root, contentByPath, manifest.Files)
	if err != nil {
		http.Error(w, "apply resolved dependencies: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if manifest.SourceRevision == ^uint64(0) {
		http.Error(w, "workspace revision exhausted", http.StatusConflict)
		return
	}
	manifest.SourceRevision++
	manifest.SourceDigest = newDigest
	manifest.Files = make([]string, 0, len(contentByPath))
	for clean := range contentByPath {
		manifest.Files = append(manifest.Files, clean)
	}
	slicesSortStrings(manifest.Files)
	manifest.PendingReloadCommands = nil
	if err := writeWorkspaceManifest(root, manifest); err != nil {
		http.Error(w, "write workspace manifest: "+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Changed = true
	response.Paths = append(changed, deleted...)
	slicesSortStrings(response.Paths)
	response.SourceRevision = manifest.SourceRevision
	response.SourceDigest = manifest.SourceDigest
	restarted, err := s.applyNamedServiceWorkspaceRevision(r.Context(), manifest.SourceRevision, manifest.SourceDigest)
	if err != nil {
		http.Error(w, "restart named services: "+err.Error(), http.StatusBadGateway)
		return
	}
	response.NamedServicesRestarted = restarted
	writeJSON(w, http.StatusOK, response)
}

func syncFilesContainPath(files []syncFile, target string) bool {
	for _, file := range files {
		if file.Path == target {
			return true
		}
	}
	return false
}

func dependencyResolverOwnsPath(ecosystem, workDir, candidate string) bool {
	if ecosystem != "go" {
		return false
	}
	prefix := ""
	if workDir != "." {
		prefix = workDir + "/"
	}
	return candidate == prefix+"go.mod" || candidate == prefix+"go.sum"
}

// runDependencyResolver materializes an isolated copy so dependency managers
// may write their normal lock/checksum artifacts without bypassing the live
// workspace manifest. Only the generated manifest files are returned.
func runDependencyResolver(ctx context.Context, workspaceRoot, ecosystem, workDir string, files []syncFile, sourceRevision uint64, sourceDigest string, executor execDispatcher) (dependencyResolverResult, error) {
	if ecosystem != "go" {
		return dependencyResolverResult{}, fmt.Errorf("unsupported ecosystem %q", ecosystem)
	}
	if executor == nil {
		return dependencyResolverResult{}, errors.New("dependency executor is not configured")
	}
	workspaceRoot, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return dependencyResolverResult{}, errors.New("dependency workspace is required")
	}
	temporary, err := os.MkdirTemp(workspaceRoot, ".faros-dependency-resolver-")
	if err != nil {
		return dependencyResolverResult{}, err
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	for _, file := range files {
		clean, err := cleanWorkspacePath(file.Path)
		if err != nil {
			return dependencyResolverResult{}, err
		}
		target := filepath.Join(temporary, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return dependencyResolverResult{}, err
		}
		if err := os.WriteFile(target, []byte(file.Content), 0o644); err != nil {
			return dependencyResolverResult{}, err
		}
	}
	stageRelative, err := filepath.Rel(workspaceRoot, temporary)
	if err != nil {
		return dependencyResolverResult{}, err
	}
	stageWorkDir := filepath.ToSlash(stageRelative)
	if workDir != "." {
		stageWorkDir = path.Join(stageWorkDir, workDir)
	}
	run, err := executor.Execute(ctx, persistentExecRequest{
		Argv:           []string{"faros-env", "exec", "--", "go", "mod", "tidy"},
		WorkDir:        stageWorkDir,
		TimeoutMS:      int(dependencyResolverTimeout / time.Millisecond),
		MaxOutputBytes: execMaxOutputBytes,
		SourceRevision: sourceRevision,
		SourceDigest:   sourceDigest,
	})
	if err != nil {
		return dependencyResolverResult{}, err
	}
	if run.Error != "" {
		return dependencyResolverResult{}, errors.New(run.Error)
	}
	result := dependencyResolverResult{ExitCode: run.ExitCode, Stdout: run.Stdout, Stderr: run.Stderr, Writes: map[string][]byte{}}
	if run.ExitCode != 0 {
		return result, nil
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		clean := name
		if workDir != "." {
			clean = path.Join(workDir, name)
		}
		content, err := os.ReadFile(filepath.Join(temporary, filepath.FromSlash(clean)))
		switch {
		case err == nil:
			result.Writes[clean] = content
		case errors.Is(err, fs.ErrNotExist) && name == "go.sum":
			result.Deletes = append(result.Deletes, clean)
		case err != nil:
			return dependencyResolverResult{}, err
		}
	}
	return result, nil
}
