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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	execRequestMaxBytes    = 16 << 20
	execMaxSourceFiles     = 512
	execMaxSourceBytes     = 16 << 20
	execMaxSourceFileBytes = 8 << 20
	execDefaultTimeout     = 30 * time.Second
	execMaxTimeout         = 2 * time.Minute
	execDefaultOutputBytes = 256 << 10
	execMaxOutputBytes     = 256 << 10
	execMaxWorkDirBytes    = 256
	execSnapshotFileBytes  = 16 << 20
	execMaxSnapshotFiles   = 4096
)

var (
	errExecPathEscape    = errors.New("path escapes execution workspace")
	errExecSymlink       = errors.New("symbolic links are not allowed in execution workspace paths")
	errExecSnapshotLimit = errors.New("execution workspace snapshot limit reached")
)

// execSourceFile is source staged into the execution workspace before argv is
// started. Content is deliberately a string: the executor is source-oriented
// and rejects invalid UTF-8 and NUL bytes before touching the workspace.
type execSourceFile struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable,omitempty"`
}

type execRequest struct {
	Files       []execSourceFile  `json:"files,omitempty"`
	DeletePaths []string          `json:"deletePaths,omitempty"`
	Argv        []string          `json:"argv"`
	WorkDir     string            `json:"workDir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	TimeoutMS   int               `json:"timeoutMs,omitempty"`
	MaxOutput   int               `json:"maxOutputBytes,omitempty"`
}

type execResponse struct {
	Phase             string   `json:"phase"`
	Argv              []string `json:"argv,omitempty"`
	WorkDir           string   `json:"workDir"`
	ExitCode          int      `json:"exitCode"`
	TimedOut          bool     `json:"timedOut,omitempty"`
	Cancelled         bool     `json:"cancelled,omitempty"`
	Stdout            string   `json:"stdout,omitempty"`
	Stderr            string   `json:"stderr,omitempty"`
	StdoutTruncated   bool     `json:"stdoutTruncated,omitempty"`
	StderrTruncated   bool     `json:"stderrTruncated,omitempty"`
	Changed           []string `json:"changed,omitempty"`
	Deleted           []string `json:"deleted,omitempty"`
	SnapshotTruncated bool     `json:"snapshotTruncated,omitempty"`
	SourceRevision    uint64   `json:"sourceRevision,omitempty"`
	SourceDigest      string   `json:"sourceDigest,omitempty"`
	DurationMS        int64    `json:"durationMs"`
	Error             string   `json:"error,omitempty"`
}

// persistentExecRequest is the normal dev-agent protocol. Source files are
// deliberately absent: /sync owns the persistent workspace and this endpoint
// verifies the platform-applied revision/digest before launching argv.
type persistentExecRequest struct {
	Argv           []string `json:"argv"`
	WorkDir        string   `json:"workDir,omitempty"`
	TimeoutMS      int      `json:"timeoutMs,omitempty"`
	MaxOutputBytes int      `json:"maxOutputBytes,omitempty"`
	SourceRevision uint64   `json:"sourceRevision"`
	SourceDigest   string   `json:"sourceDigest"`
}

type execFileState struct {
	Size     int64
	Digest   [sha256.Size]byte
	Complete bool
}

type execWorkspaceSnapshot struct {
	Files     map[string]execFileState
	Truncated bool
}

// newExecAgentServer creates a deliberately smaller surface than the normal
// development agent. The executor pod does not supervise the app process and
// exposes only healthz plus the authenticated /exec endpoint.
func newExecAgentServer(ctx context.Context, cfg *agentConfig) *agentServer {
	s := newAgentServer(ctx, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/exec", s.handleExec)
	s.mux = mux
	return s
}

func (s *agentServer) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeExec(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, execRequestMaxBytes)
	var req execRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		http.Error(w, "request body must contain one JSON object", http.StatusBadRequest)
		return
	}
	s.execMu.Lock()
	defer s.execMu.Unlock()
	result, err := runExecRequest(r.Context(), s.config.WorkDir, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *agentServer) handlePersistentExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeExec(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, execRequestMaxBytes)
	var req persistentExecRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		http.Error(w, "request body must contain one JSON object", http.StatusBadRequest)
		return
	}
	s.execMu.Lock()
	defer s.execMu.Unlock()
	result, err := runPersistentExec(r.Context(), s.config.WorkDir, req)
	if err != nil {
		// Revision/digest mismatch is a conflict: the caller must wait for the
		// authoritative sync evidence rather than retrying the same command.
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "source revision") || strings.Contains(err.Error(), "source digest") || strings.Contains(err.Error(), "workspace manifest") {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func runPersistentExec(parent context.Context, workspace string, req persistentExecRequest) (execResponse, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := validatePersistentExecRequest(req); err != nil {
		return execResponse{}, err
	}
	rootPath, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil || strings.TrimSpace(workspace) == "" {
		return execResponse{}, errors.New("execution workspace is required")
	}
	if err := rejectRootSymlink(rootPath); err != nil {
		return execResponse{}, err
	}
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		return execResponse{}, fmt.Errorf("create execution workspace: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return execResponse{}, fmt.Errorf("open execution workspace: %w", err)
	}
	defer func() { _ = root.Close() }()
	workDir, err := cleanExecWorkDir(req.WorkDir)
	if err != nil {
		return execResponse{}, err
	}
	if err := ensureExecDirectory(root, workDir); err != nil {
		return execResponse{}, err
	}
	manifest, found, err := readWorkspaceManifest(root)
	if err != nil {
		return execResponse{}, fmt.Errorf("read workspace manifest: %w", err)
	}
	if !found {
		return execResponse{}, errors.New("source revision is not synchronized: workspace manifest is missing")
	}
	if manifest.SourceRevision != req.SourceRevision {
		return execResponse{}, fmt.Errorf("source revision %d is not the applied source revision %d", req.SourceRevision, manifest.SourceRevision)
	}
	if normalizeSourceDigest(manifest.SourceDigest) != normalizeSourceDigest(req.SourceDigest) {
		return execResponse{}, errors.New("source digest does not match the applied workspace manifest")
	}
	if err := verifyWorkspaceManifest(root, manifest); err != nil {
		return execResponse{}, fmt.Errorf("workspace manifest verification failed: %w", err)
	}

	workPath := filepath.Join(rootPath, filepath.FromSlash(workDir))
	env := sanitizedExecEnvironment(workPath)
	executable, err := resolveExecExecutable(req.Argv[0], env, workPath)
	if err != nil {
		return execResponse{}, err
	}
	outputLimit := boundedExecOutput(req.MaxOutputBytes)
	started := time.Now()
	stdout := newExecOutputBuffer(outputLimit)
	stderr := newExecOutputBuffer(outputLimit)
	cmd := exec.Command(executable, req.Argv[1:]...)
	cmd.Dir = workPath
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return execResponse{}, fmt.Errorf("start %q: %w", req.Argv[0], err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	timer := time.NewTimer(boundedExecTimeout(req.TimeoutMS))
	defer timer.Stop()
	response := execResponse{
		Phase: "completed", Argv: append([]string(nil), req.Argv...), WorkDir: workDir,
		ExitCode: -1, SourceRevision: req.SourceRevision, SourceDigest: req.SourceDigest,
	}
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-timer.C:
		response.Phase = "timed_out"
		response.TimedOut = true
		killExecProcessGroup(cmd.Process.Pid)
		waitErr = <-waitCh
	case <-parent.Done():
		response.Phase = "cancelled"
		response.Cancelled = true
		killExecProcessGroup(cmd.Process.Pid)
		waitErr = <-waitCh
	}
	response.DurationMS = time.Since(started).Milliseconds()
	response.Stdout = stdout.String()
	response.Stderr = stderr.String()
	response.StdoutTruncated = stdout.Truncated()
	response.StderrTruncated = stderr.Truncated()
	if exitCode, ok := execExitCode(waitErr); ok {
		response.ExitCode = exitCode
	}
	if waitErr != nil && !response.TimedOut && !response.Cancelled {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			response.Error = waitErr.Error()
		}
	}
	return response, nil
}

func validatePersistentExecRequest(req persistentExecRequest) error {
	if len(req.Argv) == 0 || strings.TrimSpace(req.Argv[0]) == "" {
		return errors.New("argv must contain an executable")
	}
	if len(req.Argv) > 128 {
		return errors.New("argv contains too many arguments")
	}
	for i, arg := range req.Argv {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("argv[%d] contains NUL", i)
		}
		if len([]byte(arg)) > 16<<10 {
			return fmt.Errorf("argv[%d] is too large", i)
		}
	}
	if req.SourceRevision == 0 {
		return errors.New("source revision is required")
	}
	digest := normalizeSourceDigest(req.SourceDigest)
	if len(digest) != sha256.Size*2 {
		return errors.New("source digest is required")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return errors.New("source digest must be a SHA-256 hex digest")
	}
	if req.TimeoutMS < 0 || (req.TimeoutMS > 0 && time.Duration(req.TimeoutMS)*time.Millisecond > execMaxTimeout) {
		return fmt.Errorf("timeout must be between 1ms and %s", execMaxTimeout)
	}
	if req.MaxOutputBytes < 0 || req.MaxOutputBytes > execMaxOutputBytes {
		return fmt.Errorf("maxOutputBytes must be between 1 and %d", execMaxOutputBytes)
	}
	return nil
}

// authorizeExec never honors AllowInsecureControl. The executor is a
// high-authority endpoint and must remain authenticated even in local agent
// development modes where the legacy control API may be intentionally open.
func (s *agentServer) authorizeExec(w http.ResponseWriter, r *http.Request) bool {
	if s == nil || s.config == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	token := strings.TrimSpace(s.config.ControlToken)
	if token == "" || !subtleConstantTimeCompare(r.Header.Get(controlTokenHeader), token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func runExecRequest(parent context.Context, workspace string, req execRequest) (execResponse, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := validateExecRequest(req); err != nil {
		return execResponse{}, err
	}
	rootPath, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil || strings.TrimSpace(workspace) == "" {
		return execResponse{}, errors.New("execution workspace is required")
	}
	if err := rejectRootSymlink(rootPath); err != nil {
		return execResponse{}, err
	}
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		return execResponse{}, fmt.Errorf("create execution workspace: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return execResponse{}, fmt.Errorf("open execution workspace: %w", err)
	}
	defer func() { _ = root.Close() }()

	workDir, err := cleanExecWorkDir(req.WorkDir)
	if err != nil {
		return execResponse{}, err
	}
	if err := stageExecSources(root, req); err != nil {
		return execResponse{}, err
	}
	// A submitted source bundle may create the requested relative workdir.
	// Validate it after staging, while still before taking the execution
	// snapshot and launching the process.
	if err := ensureExecDirectory(root, workDir); err != nil {
		return execResponse{}, err
	}
	before, err := snapshotExecWorkspace(root)
	if err != nil {
		return execResponse{}, err
	}

	env := sanitizedExecEnvironment(filepath.Join(rootPath, filepath.FromSlash(workDir)))
	executable, err := resolveExecExecutable(req.Argv[0], env, filepath.Join(rootPath, filepath.FromSlash(workDir)))
	if err != nil {
		return execResponse{}, err
	}

	started := time.Now()
	outputLimit := boundedExecOutput(req.MaxOutput)
	stdout := newExecOutputBuffer(outputLimit)
	stderr := newExecOutputBuffer(outputLimit)
	cmd := exec.Command(executable, req.Argv[1:]...)
	cmd.Dir = filepath.Join(rootPath, filepath.FromSlash(workDir))
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return execResponse{}, fmt.Errorf("start %q: %w", req.Argv[0], err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	timeout := boundedExecTimeout(req.TimeoutMS)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	response := execResponse{
		Phase:    "completed",
		Argv:     append([]string(nil), req.Argv...),
		WorkDir:  workDir,
		ExitCode: -1,
	}
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-timer.C:
		response.Phase = "timed_out"
		response.TimedOut = true
		killExecProcessGroup(cmd.Process.Pid)
		waitErr = <-waitCh
	case <-parent.Done():
		response.Phase = "cancelled"
		response.Cancelled = true
		killExecProcessGroup(cmd.Process.Pid)
		waitErr = <-waitCh
	}

	response.DurationMS = time.Since(started).Milliseconds()
	response.Stdout = stdout.String()
	response.Stderr = stderr.String()
	response.StdoutTruncated = stdout.Truncated()
	response.StderrTruncated = stderr.Truncated()
	if exitCode, ok := execExitCode(waitErr); ok {
		response.ExitCode = exitCode
	}
	if waitErr != nil && !response.TimedOut && !response.Cancelled {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			response.Error = waitErr.Error()
		}
	}

	after, snapshotErr := snapshotExecWorkspace(root)
	if snapshotErr != nil {
		return execResponse{}, snapshotErr
	}
	response.Changed, response.Deleted = diffExecSnapshots(before, after)
	response.SnapshotTruncated = before.Truncated || after.Truncated
	return response, nil
}

func validateExecRequest(req execRequest) error {
	if len(req.Argv) == 0 || strings.TrimSpace(req.Argv[0]) == "" {
		return errors.New("argv must contain an executable")
	}
	if len(req.Argv) > 128 {
		return errors.New("argv contains too many arguments")
	}
	for i, arg := range req.Argv {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("argv[%d] contains NUL", i)
		}
		if len([]byte(arg)) > 16<<10 {
			return fmt.Errorf("argv[%d] is too large", i)
		}
	}
	if len(req.Files)+len(req.DeletePaths) > execMaxSourceFiles {
		return fmt.Errorf("at most %d source paths may be staged or deleted", execMaxSourceFiles)
	}
	seen := make(map[string]struct{}, len(req.Files)+len(req.DeletePaths))
	total := 0
	for _, file := range req.Files {
		clean, err := cleanExecPath(file.Path)
		if err != nil {
			return err
		}
		if _, ok := seen[clean]; ok {
			return fmt.Errorf("duplicate source path %q", clean)
		}
		seen[clean] = struct{}{}
		if !utf8.ValidString(file.Content) || strings.ContainsRune(file.Content, '\x00') {
			return fmt.Errorf("source file %q must be UTF-8 text without NUL bytes", clean)
		}
		if len([]byte(file.Content)) > execMaxSourceFileBytes {
			return fmt.Errorf("source file %q exceeds %d bytes", clean, execMaxSourceFileBytes)
		}
		total += len([]byte(clean)) + len([]byte(file.Content))
	}
	for _, raw := range req.DeletePaths {
		clean, err := cleanExecPath(raw)
		if err != nil {
			return err
		}
		if _, ok := seen[clean]; ok {
			return fmt.Errorf("source path %q is both staged and deleted", clean)
		}
		seen[clean] = struct{}{}
		total += len([]byte(clean))
	}
	if total > execMaxSourceBytes {
		return fmt.Errorf("source request exceeds %d bytes", execMaxSourceBytes)
	}
	if len(req.Env) != 0 {
		return errors.New("execution environment is server-owned; caller environment overrides are not accepted")
	}
	if req.TimeoutMS < 0 || (req.TimeoutMS > 0 && time.Duration(req.TimeoutMS)*time.Millisecond > execMaxTimeout) {
		return fmt.Errorf("timeout must be between 1ms and %s", execMaxTimeout)
	}
	if req.MaxOutput < 0 || req.MaxOutput > execMaxOutputBytes {
		return fmt.Errorf("maxOutputBytes must be between 1 and %d", execMaxOutputBytes)
	}
	return nil
}

func cleanExecPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsRune(raw, '\x00') || strings.ContainsRune(raw, '\\') {
		return "", errors.New("workspace path must be non-empty and use slash-separated components")
	}
	if path.IsAbs(raw) {
		return "", errExecPathEscape
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", errExecPathEscape
		}
		switch strings.ToLower(part) {
		case ".git", "node_modules", ".assistant-snapshots":
			return "", fmt.Errorf("workspace path contains reserved component %q", part)
		}
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errExecPathEscape
	}
	if clean == workspaceManifestName {
		return "", fmt.Errorf("workspace path %q is reserved for the platform sync manifest", raw)
	}
	return clean, nil
}

func cleanExecWorkDir(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ".", nil
	}
	if len([]byte(raw)) > execMaxWorkDirBytes {
		return "", fmt.Errorf("workdir exceeds %d bytes", execMaxWorkDirBytes)
	}
	if path.Clean(raw) == "." {
		for _, part := range strings.Split(raw, "/") {
			if part == ".." {
				return "", errExecPathEscape
			}
		}
		return ".", nil
	}
	return cleanExecPath(raw)
}

func stageExecSources(root *os.Root, req execRequest) error {
	for _, file := range req.Files {
		clean, _ := cleanExecPath(file.Path)
		if err := ensureExecPathNoSymlink(root, clean, false); err != nil {
			return fmt.Errorf("stage %q: %w", clean, err)
		}
		parent := path.Dir(clean)
		if parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return fmt.Errorf("create source parent %q: %w", parent, err)
			}
		}
		if err := ensureExecPathNoSymlink(root, clean, true); err != nil {
			return fmt.Errorf("stage %q: %w", clean, err)
		}
		mode := os.FileMode(0o644)
		if file.Executable {
			mode = 0o755
		}
		if err := root.WriteFile(clean, []byte(file.Content), mode); err != nil {
			return fmt.Errorf("write source %q: %w", clean, err)
		}
	}
	for _, raw := range req.DeletePaths {
		clean, _ := cleanExecPath(raw)
		if err := ensureExecPathNoSymlink(root, clean, true); err != nil {
			return fmt.Errorf("delete %q: %w", clean, err)
		}
		info, err := root.Lstat(clean)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("delete %q: only regular files may be deleted", clean)
		}
		if err := root.Remove(clean); err != nil {
			return fmt.Errorf("delete source %q: %w", clean, err)
		}
	}
	return nil
}

func ensureExecDirectory(root *os.Root, clean string) error {
	if clean == "." {
		return nil
	}
	if err := ensureExecPathNoSymlink(root, clean, true); err != nil {
		return err
	}
	info, err := root.Stat(clean)
	if err != nil {
		return fmt.Errorf("workdir %q: %w", clean, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workdir %q is not a directory", clean)
	}
	return nil
}

func ensureExecPathNoSymlink(root *os.Root, clean string, includeTarget bool) error {
	parts := strings.Split(clean, "/")
	for index, part := range parts {
		current := strings.Join(parts[:index+1], "/")
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errExecSymlink
		}
		if index < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("workspace path component %q is not a directory", part)
		}
		if index == len(parts)-1 && !includeTarget {
			return nil
		}
	}
	return nil
}

func rejectRootSymlink(rootPath string) error {
	rootPath = filepath.Clean(rootPath)
	current := filepath.VolumeName(rootPath)
	if strings.HasPrefix(rootPath, string(filepath.Separator)) {
		current += string(filepath.Separator)
	}
	for _, part := range strings.Split(strings.TrimPrefix(rootPath, current), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errExecSymlink
		}
	}
	return nil
}

func sanitizedExecEnvironment(workDir string) []string {
	// Never inherit the agent's environment: it may contain the control token
	// or provider/runtime credentials. Keep this server-owned and deterministic.
	// This is not a container boundary: the child shares the component's mounts,
	// network and PID namespace, so deployment-level secret/credential exposure
	// must be handled by the dev workload itself.
	values := map[string]string{
		"HOME":   "/tmp",
		"LANG":   "C.UTF-8",
		"PATH":   "/usr/local/go/bin:/go/bin:/usr/local/bin:/usr/bin:/bin",
		"PWD":    workDir,
		"TMPDIR": "/tmp",
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func resolveExecExecutable(name string, env []string, workDir string) (string, error) {
	if strings.TrimSpace(name) == "" || strings.ContainsRune(name, '\x00') {
		return "", errors.New("argv executable is invalid")
	}
	if strings.Contains(name, "/") {
		if filepath.IsAbs(name) {
			return name, nil
		}
		clean, err := cleanExecPath(name)
		if err != nil {
			return "", err
		}
		return filepath.Join(workDir, filepath.FromSlash(clean)), nil
	}
	pathValue := ""
	for _, value := range env {
		if strings.HasPrefix(value, "PATH=") {
			pathValue = strings.TrimPrefix(value, "PATH=")
			break
		}
	}
	if pathValue == "" {
		return "", errors.New("argv executable must be absolute or PATH must be supplied explicitly")
	}
	for _, directory := range strings.Split(pathValue, string(os.PathListSeparator)) {
		if directory == "" || !filepath.IsAbs(directory) {
			return "", errors.New("execution PATH entries must be absolute")
		}
		candidate := filepath.Join(directory, name)
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("executable %q was not found in explicit PATH", name)
}

func boundedExecTimeout(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		return execDefaultTimeout
	}
	return time.Duration(timeoutMS) * time.Millisecond
}

func boundedExecOutput(max int) int {
	if max <= 0 {
		return execDefaultOutputBytes
	}
	return max
}

type execOutputBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func newExecOutputBuffer(limit int) *execOutputBuffer {
	return &execOutputBuffer{limit: limit}
}

func (b *execOutputBuffer) Write(data []byte) (int, error) {
	if b.limit <= b.Len() {
		b.truncated = b.truncated || len(data) > 0
		return len(data), nil
	}
	remaining := b.limit - b.Len()
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		b.truncated = true
		return len(data), nil
	}
	return b.Buffer.Write(data)
}

// ReadFrom shadows bytes.Buffer.ReadFrom. os/exec copies pipe output through
// io.ReaderFrom when the destination advertises it; delegating to the embedded
// bytes.Buffer would bypass Write's bound entirely.
func (b *execOutputBuffer) ReadFrom(reader io.Reader) (int64, error) {
	var total int64
	buf := make([]byte, 32<<10)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			written, _ := b.Write(buf[:n])
			total += int64(written)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
}

func (b *execOutputBuffer) Truncated() bool { return b.truncated }

func killExecProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func execExitCode(err error) (int, bool) {
	if err == nil {
		return 0, true
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return -1, false
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
		if status.Exited() {
			return status.ExitStatus(), true
		}
		if status.Signaled() {
			return 128 + int(status.Signal()), true
		}
	}
	return -1, true
}

func snapshotExecWorkspace(root *os.Root) (execWorkspaceSnapshot, error) {
	snapshot := execWorkspaceSnapshot{Files: map[string]execFileState{}}
	bytesSeen := int64(0)
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if name == workspaceManifestName {
			return nil
		}
		base := path.Base(name)
		if entry.IsDir() && (base == ".git" || base == "node_modules" || base == ".assistant-snapshots") {
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot encountered symbolic link %q", name)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if len(snapshot.Files) >= execMaxSnapshotFiles {
			snapshot.Truncated = true
			return errExecSnapshotLimit
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := execFileState{Size: info.Size(), Complete: true}
		if info.Size() > execSnapshotFileBytes || bytesSeen+info.Size() > execMaxSourceBytes {
			state.Complete = false
			snapshot.Truncated = true
			snapshot.Files[name] = state
			return nil
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		_ = file.Close()
		if copyErr != nil {
			return copyErr
		}
		copy(state.Digest[:], hash.Sum(nil))
		bytesSeen += info.Size()
		snapshot.Files[name] = state
		return nil
	})
	if errors.Is(err, errExecSnapshotLimit) {
		return snapshot, nil
	}
	if err != nil {
		return execWorkspaceSnapshot{}, fmt.Errorf("snapshot execution workspace: %w", err)
	}
	return snapshot, nil
}

func diffExecSnapshots(before, after execWorkspaceSnapshot) (changed, deleted []string) {
	paths := make(map[string]struct{}, len(before.Files)+len(after.Files))
	for name := range before.Files {
		paths[name] = struct{}{}
	}
	for name := range after.Files {
		paths[name] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		oldState, oldOK := before.Files[name]
		newState, newOK := after.Files[name]
		switch {
		case oldOK && !newOK:
			deleted = append(deleted, name)
		case !oldOK && newOK:
			changed = append(changed, name)
		case oldOK && newOK && (!oldState.Complete || !newState.Complete || oldState.Size != newState.Size || !bytes.Equal(oldState.Digest[:], newState.Digest[:])):
			changed = append(changed, name)
		}
	}
	return changed, deleted
}
