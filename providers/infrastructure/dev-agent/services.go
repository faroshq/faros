// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

// Named development services are deliberately supervised separately from the
// template's legacy process. A DevelopmentService is durable intent in the
// Infrastructure provider; this manager is only the in-pod execution half.
// Processes are started with exec.Command(argv[0], argv[1:]...) and therefore
// never inherit shell parsing accidentally. The coordinator's authenticated
// /service endpoint is the only caller-facing control seam.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxManagedServices   = 8
	maxServiceArgv       = 32
	maxServiceArgBytes   = 4096
	maxServiceHealthPath = 512
	serviceStopTimeout   = 3 * time.Second
	serviceRestartDelay  = 100 * time.Millisecond
)

// connectionFilesRoot is fixed in production by the universal sandbox
// Template. It is a variable only so pure supervisor tests can use an isolated
// temporary mount without requiring host /var permissions.
var connectionFilesRoot = "/var/run/faros/connections"

// serviceSpec is the wire-compatible process contract shared by the
// Infrastructure controller and the dev-agent coordinator.
type serviceSpec struct {
	Name               string            `json:"name"`
	Argv               []string          `json:"argv"`
	WorkDir            string            `json:"workDir,omitempty"`
	Port               int32             `json:"port"`
	HealthPath         string            `json:"healthPath,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	EnvFiles           map[string]string `json:"envFiles,omitempty"`
	ConnectionRevision string            `json:"connectionRevision,omitempty"`
	RestartToken       string            `json:"restartToken,omitempty"`
	Enabled            bool              `json:"enabled"`
	RestartPolicy      string            `json:"restartPolicy,omitempty"`
}

type serviceStatus struct {
	Name          string `json:"name"`
	Phase         string `json:"phase,omitempty"`
	Running       bool   `json:"running"`
	PortListening bool   `json:"portListening"`
	Reachable     bool   `json:"reachable"`
	RestartCount  int64  `json:"restartCount,omitempty"`
	LastExitCode  *int32 `json:"lastExitCode,omitempty"`
	Message       string `json:"message,omitempty"`
}

type managedService struct {
	spec           serviceSpec
	cmd            *exec.Cmd
	done           chan struct{}
	logs           *ringLog
	phase          string
	message        string
	restartCount   int64
	lastExitCode   *int32
	started        bool
	startFailed    bool
	stopping       bool
	generation     uint64
	sourceRevision uint64
	sourceDigest   string
}

type serviceManager struct {
	ctx            context.Context
	workspace      string
	mu             sync.Mutex
	services       map[string]*managedService
	sourceRevision uint64
	sourceDigest   string
}

// serviceRuntimeOperations is intentionally additive to runtimeOperations:
// legacy template reload/env/status callers continue to work with fakes that
// only implement the original process contract.
type serviceRuntimeOperations interface {
	ConfigureService(context.Context, serviceSpec) (serviceStatus, error)
	RemoveService(context.Context, string) error
	ServiceStatus(context.Context, string) (serviceStatus, error)
	ServiceLogs(context.Context, string) (string, error)
}

// serviceWorkspaceRevisionOperations keeps named processes on the same
// authoritative workspace revision as stateless exec. It stays separate from
// serviceRuntimeOperations so legacy runtime fakes do not accidentally gain a
// mutating capability merely by implementing CRUD for named services.
type serviceWorkspaceRevisionOperations interface {
	ApplyServiceWorkspaceRevision(context.Context, uint64, string) (int, error)
}

func (r *localRuntime) ConfigureService(ctx context.Context, spec serviceSpec) (serviceStatus, error) {
	if r == nil || r.services == nil {
		return serviceStatus{}, errors.New("service manager is unavailable")
	}
	return r.services.Configure(ctx, spec)
}

func (r *localRuntime) RemoveService(ctx context.Context, name string) error {
	if r == nil || r.services == nil {
		return errors.New("service manager is unavailable")
	}
	return r.services.Remove(ctx, name)
}

func (r *localRuntime) ServiceStatus(ctx context.Context, name string) (serviceStatus, error) {
	if r == nil || r.services == nil {
		return serviceStatus{}, errors.New("service manager is unavailable")
	}
	return r.services.Status(ctx, name)
}

func (r *localRuntime) ServiceLogs(ctx context.Context, name string) (string, error) {
	if r == nil || r.services == nil {
		return "", errors.New("service manager is unavailable")
	}
	return r.services.Logs(ctx, name)
}

func (r *localRuntime) ApplyServiceWorkspaceRevision(ctx context.Context, revision uint64, digest string) (int, error) {
	if r == nil || r.services == nil {
		return 0, errors.New("service manager is unavailable")
	}
	return r.services.ApplyWorkspaceRevision(ctx, revision, digest)
}

func (c *httpRuntimeClient) ConfigureService(ctx context.Context, spec serviceSpec) (serviceStatus, error) {
	var status serviceStatus
	err := c.call(ctx, http.MethodPost, "service", spec, &status)
	return status, err
}

func (c *httpRuntimeClient) RemoveService(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodDelete, "service?name="+url.QueryEscape(name), nil, nil)
}

func (c *httpRuntimeClient) ServiceStatus(ctx context.Context, name string) (serviceStatus, error) {
	var status serviceStatus
	err := c.call(ctx, http.MethodGet, "service?name="+url.QueryEscape(name), nil, &status)
	return status, err
}

func (c *httpRuntimeClient) ServiceLogs(ctx context.Context, name string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/internal/service/logs?name="+url.QueryEscape(name), nil)
	if err != nil {
		return "", err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 512<<10))
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("runtime supervisor service logs: %s", strings.TrimSpace(string(raw)))
	}
	return string(raw), nil
}

func (c *httpRuntimeClient) ApplyServiceWorkspaceRevision(ctx context.Context, revision uint64, digest string) (int, error) {
	var response struct {
		Restarted int `json:"restarted"`
	}
	err := c.call(ctx, http.MethodPost, "service/workspace-revision", struct {
		SourceRevision uint64 `json:"sourceRevision"`
		SourceDigest   string `json:"sourceDigest"`
	}{SourceRevision: revision, SourceDigest: digest}, &response)
	return response.Restarted, err
}

func newServiceManager(ctx context.Context, workspace string) *serviceManager {
	if ctx == nil {
		ctx = context.Background()
	}
	return &serviceManager{ctx: ctx, workspace: workspace, services: map[string]*managedService{}}
}

func (m *serviceManager) Configure(ctx context.Context, spec serviceSpec) (serviceStatus, error) {
	normalized, err := normalizeServiceSpec(spec)
	if err != nil {
		return serviceStatus{}, err
	}
	if m == nil {
		return serviceStatus{}, errors.New("service manager is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.services[normalized.Name]
	if current == nil {
		if len(m.services) >= maxManagedServices {
			return serviceStatus{}, fmt.Errorf("at most %d development services may run in one sandbox", maxManagedServices)
		}
		current = &managedService{logs: newRingLog(500)}
		m.services[normalized.Name] = current
	}
	if serviceSpecsEqual(current.spec, normalized) {
		if normalized.Enabled && m.sourceRevision > 0 && !serviceWorkspaceRevisionEqual(current, m.sourceRevision, m.sourceDigest) {
			if err := m.restartForWorkspaceRevisionLocked(current, m.sourceRevision, m.sourceDigest); err != nil {
				return m.statusLocked(current), err
			}
			return m.statusLocked(current), nil
		}
		// A failed start is stateful even when the desired contract is not.
		// Retry it on the next Configure: projected connection files can appear
		// after the first reconcile without changing the service spec.
		if normalized.Enabled && current.startFailed && current.cmd == nil {
			current.stopping = false
			current.lastExitCode = nil
			if err := m.startLocked(current); err != nil {
				current.startFailed = true
				current.phase = "Failed"
				current.message = err.Error()
				return m.statusLocked(current), err
			}
			m.markServiceWorkspaceRevisionLocked(current)
		}
		return m.statusLocked(current), nil
	}
	if current.cmd != nil {
		if err := m.stopLocked(current); err != nil {
			return serviceStatus{}, err
		}
	}
	current.spec = normalized
	current.stopping = false
	current.lastExitCode = nil
	current.generation++
	if !normalized.Enabled {
		current.phase = "Stopped"
		current.message = "service is disabled"
		m.markServiceWorkspaceRevisionLocked(current)
		return m.statusLocked(current), nil
	}
	if err := m.startLocked(current); err != nil {
		current.startFailed = true
		current.phase = "Failed"
		current.message = err.Error()
		return m.statusLocked(current), err
	}
	m.markServiceWorkspaceRevisionLocked(current)
	_ = ctx
	return m.statusLocked(current), nil
}

// ApplyWorkspaceRevision restarts every enabled named process onto a newly
// synchronized authoritative workspace. Replaying the same revision is
// idempotent, while per-service revision tracking lets a later controller
// Configure retry only a service whose restart previously failed.
func (m *serviceManager) ApplyWorkspaceRevision(ctx context.Context, revision uint64, digest string) (int, error) {
	if m == nil {
		return 0, errors.New("service manager is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	digest = normalizeSourceDigest(digest)
	if revision == 0 || digest == "" {
		return 0, errors.New("service workspace sourceRevision and sourceDigest are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if revision < m.sourceRevision {
		return 0, errors.New("service workspace revision is older than the applied revision")
	}
	if revision == m.sourceRevision && m.sourceDigest != "" && digest != m.sourceDigest {
		return 0, errors.New("service workspace revision was already applied with a different digest")
	}
	m.sourceRevision = revision
	m.sourceDigest = digest

	restarted := 0
	names := make([]string, 0, len(m.services))
	for name := range m.services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return restarted, err
		}
		current := m.services[name]
		if serviceWorkspaceRevisionEqual(current, revision, digest) {
			continue
		}
		if !current.spec.Enabled {
			m.markServiceWorkspaceRevisionLocked(current)
			continue
		}
		if err := m.restartForWorkspaceRevisionLocked(current, revision, digest); err != nil {
			return restarted, fmt.Errorf("restart named service %q for workspace revision %d: %w", name, revision, err)
		}
		restarted++
	}
	return restarted, nil
}

func serviceWorkspaceRevisionEqual(current *managedService, revision uint64, digest string) bool {
	return current != nil && current.sourceRevision == revision && current.sourceDigest == digest
}

func (m *serviceManager) markServiceWorkspaceRevisionLocked(current *managedService) {
	if current == nil {
		return
	}
	current.sourceRevision = m.sourceRevision
	current.sourceDigest = m.sourceDigest
}

func (m *serviceManager) restartForWorkspaceRevisionLocked(current *managedService, revision uint64, digest string) error {
	current.stopping = true
	if err := m.stopLocked(current); err != nil {
		current.stopping = false
		return err
	}
	current.stopping = false
	current.lastExitCode = nil
	if err := m.startLocked(current); err != nil {
		current.startFailed = true
		current.phase = "Failed"
		current.message = err.Error()
		return err
	}
	current.sourceRevision = revision
	current.sourceDigest = digest
	return nil
}

func (m *serviceManager) Remove(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if !validServiceName(name) {
		return errors.New("service name is required and must be a DNS label")
	}
	if m == nil {
		return errors.New("service manager is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.services[name]
	if current == nil {
		return nil
	}
	current.stopping = true
	if err := m.stopLocked(current); err != nil {
		return err
	}
	delete(m.services, name)
	_ = ctx
	return nil
}

func (m *serviceManager) Status(ctx context.Context, name string) (serviceStatus, error) {
	name = strings.TrimSpace(name)
	if !validServiceName(name) {
		return serviceStatus{}, errors.New("service name is required and must be a DNS label")
	}
	if m == nil {
		return serviceStatus{}, errors.New("service manager is unavailable")
	}
	m.mu.Lock()
	current := m.services[name]
	if current == nil {
		m.mu.Unlock()
		return serviceStatus{Name: name, Phase: "Stopped", Message: "service is not configured"}, nil
	}
	status := m.statusLocked(current)
	m.mu.Unlock()
	_ = ctx
	return status, nil
}

func (m *serviceManager) Logs(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validServiceName(name) {
		return "", errors.New("service name is required and must be a DNS label")
	}
	if m == nil {
		return "", errors.New("service manager is unavailable")
	}
	m.mu.Lock()
	current := m.services[name]
	if current == nil {
		m.mu.Unlock()
		return "", nil
	}
	logs := current.logs
	m.mu.Unlock()
	_ = ctx
	return strings.Join(logs.lines(), "\n"), nil
}

func (m *serviceManager) StopAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.services {
		current.stopping = true
		_ = m.stopLocked(current)
	}
}

func normalizeServiceSpec(spec serviceSpec) (serviceSpec, error) {
	spec.Name = strings.TrimSpace(spec.Name)
	if !validServiceName(spec.Name) {
		return serviceSpec{}, errors.New("service name is required and must be a DNS label")
	}
	if len(spec.Argv) == 0 || len(spec.Argv) > maxServiceArgv {
		return serviceSpec{}, fmt.Errorf("service argv must contain 1-%d arguments", maxServiceArgv)
	}
	for i, arg := range spec.Argv {
		if arg == "" || len(arg) > maxServiceArgBytes || strings.IndexByte(arg, 0) >= 0 {
			return serviceSpec{}, fmt.Errorf("service argv[%d] is empty, contains NUL, or exceeds %d bytes", i, maxServiceArgBytes)
		}
	}
	var err error
	spec.WorkDir, err = cleanServiceWorkDir(spec.WorkDir)
	if err != nil {
		return serviceSpec{}, err
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return serviceSpec{}, errors.New("service port must be between 1 and 65535")
	}
	if spec.Port >= 7070 && spec.Port <= 7073 {
		return serviceSpec{}, errors.New("service port is reserved by the sandbox control plane")
	}
	if spec.HealthPath != "" && (len(spec.HealthPath) > maxServiceHealthPath || !strings.HasPrefix(spec.HealthPath, "/") || strings.IndexByte(spec.HealthPath, 0) >= 0) {
		return serviceSpec{}, fmt.Errorf("service healthPath must be an absolute path of at most %d bytes", maxServiceHealthPath)
	}
	if spec.RestartPolicy == "" {
		spec.RestartPolicy = "Always"
	}
	if spec.RestartPolicy != "Always" && spec.RestartPolicy != "OnFailure" && spec.RestartPolicy != "Never" {
		return serviceSpec{}, errors.New("service restartPolicy must be Always, OnFailure, or Never")
	}
	if len(spec.Env) > maxRuntimeEnvKeys {
		return serviceSpec{}, fmt.Errorf("at most %d service environment variables may be set", maxRuntimeEnvKeys)
	}
	for name, value := range spec.Env {
		if !isValidRuntimeEnvName(name) || hasReservedEnvPrefix(name) || isSecretLikeRuntimeEnvName(name) {
			return serviceSpec{}, fmt.Errorf("service environment variable %q is not allowed", name)
		}
		if len(value) > maxServiceArgBytes || strings.IndexByte(value, 0) >= 0 {
			return serviceSpec{}, fmt.Errorf("service environment variable %q is too large or contains NUL", name)
		}
	}
	if spec.Env != nil {
		spec.Env = mapsClone(spec.Env)
	}
	if len(spec.EnvFiles) > maxRuntimeEnvKeys {
		return serviceSpec{}, fmt.Errorf("at most %d connection environment files may be set", maxRuntimeEnvKeys)
	}
	for name, file := range spec.EnvFiles {
		if !isValidRuntimeEnvName(name) {
			return serviceSpec{}, fmt.Errorf("connection environment name %q is invalid", name)
		}
		clean := filepath.Clean(file)
		root := filepath.Clean(connectionFilesRoot)
		if !filepath.IsAbs(clean) || clean == root || !strings.HasPrefix(clean, root+string(filepath.Separator)) || strings.IndexByte(clean, 0) >= 0 {
			return serviceSpec{}, fmt.Errorf("connection environment file for %q is outside the managed mount", name)
		}
		spec.EnvFiles[name] = clean
	}
	if len(spec.ConnectionRevision) > 128 || strings.IndexByte(spec.ConnectionRevision, 0) >= 0 {
		return serviceSpec{}, errors.New("connection revision is invalid")
	}
	if len(spec.RestartToken) > 128 || strings.IndexByte(spec.RestartToken, 0) >= 0 {
		return serviceSpec{}, errors.New("service restart token is invalid")
	}
	if spec.EnvFiles != nil {
		spec.EnvFiles = mapsClone(spec.EnvFiles)
	}
	spec.Argv = append([]string(nil), spec.Argv...)
	return spec, nil
}

func validServiceName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func cleanServiceWorkDir(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return ".", nil
	}
	if len(raw) > 512 || path.IsAbs(raw) || strings.IndexByte(raw, 0) >= 0 {
		return "", errors.New("service workDir must be relative to the sandbox workspace")
	}
	clean := path.Clean(raw)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("service workDir must remain inside the sandbox workspace")
	}
	return clean, nil
}

func mapsClone(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func serviceSpecsEqual(left, right serviceSpec) bool {
	if left.Name != right.Name || left.WorkDir != right.WorkDir || left.Port != right.Port || left.HealthPath != right.HealthPath || left.Enabled != right.Enabled || left.RestartPolicy != right.RestartPolicy || left.ConnectionRevision != right.ConnectionRevision || left.RestartToken != right.RestartToken || len(left.Argv) != len(right.Argv) || len(left.Env) != len(right.Env) || len(left.EnvFiles) != len(right.EnvFiles) {
		return false
	}
	for i := range left.Argv {
		if left.Argv[i] != right.Argv[i] {
			return false
		}
	}
	for key, value := range left.Env {
		if right.Env[key] != value {
			return false
		}
	}
	for key, value := range left.EnvFiles {
		if right.EnvFiles[key] != value {
			return false
		}
	}
	return true
}

func (m *serviceManager) startLocked(current *managedService) error {
	workDir := m.workspace
	if current.spec.WorkDir != "." {
		workDir = filepath.Join(workDir, filepath.FromSlash(current.spec.WorkDir))
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create service workDir: %w", err)
	}
	connectionEnv, err := readConnectionEnvironment(current.spec.EnvFiles)
	if err != nil {
		return err
	}
	childEnv := mapsClone(current.spec.Env)
	for name, value := range connectionEnv {
		childEnv[name] = value
	}
	cmd := managedServiceCommand(m.ctx, current.spec.Argv)
	cmd.Dir = workDir
	cmd.Env = mergeChildEnv(os.Environ(), childEnv, strconv.Itoa(int(current.spec.Port)))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if current.started {
		current.restartCount++
	}
	current.started = true
	current.startFailed = false
	current.phase = "Running"
	current.message = "service process is running"
	current.cmd = cmd
	current.done = make(chan struct{})
	current.generation++
	attempt := current.logs.beginAttempt()
	done := current.done
	generation := current.generation
	go scanManagedServiceOutput(current.logs, attempt, stdout)
	go scanManagedServiceOutput(current.logs, attempt, stderr)
	go m.wait(current.spec.Name, generation, cmd, done)
	return nil
}

// managedServiceCommand lets the platform-owned universal image validate the
// project's declared runtime requirements before starting application code.
// Other development images retain the existing direct argv execution contract.
func managedServiceCommand(ctx context.Context, argv []string) *exec.Cmd {
	if resolver, err := exec.LookPath("faros-env"); err == nil {
		wrapped := append([]string{"exec", "--"}, argv...)
		return exec.CommandContext(ctx, resolver, wrapped...)
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}

func readConnectionEnvironment(files map[string]string) (map[string]string, error) {
	values := make(map[string]string, len(files))
	for name, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			return nil, fmt.Errorf("read connection environment %q: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return nil, fmt.Errorf("connection environment file for %q must be a regular file of at most 64KiB", name)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read connection environment %q: %w", name, err)
		}
		if strings.IndexByte(string(raw), 0) >= 0 {
			return nil, fmt.Errorf("connection environment %q contains NUL", name)
		}
		values[name] = string(raw)
	}
	return values, nil
}

func scanManagedServiceOutput(logs *ringLog, attempt uint64, reader io.Reader) {
	if logs == nil || reader == nil {
		return
	}
	buf := make([]byte, 32<<10)
	var pending string
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			pending += string(buf[:n])
			for {
				line, rest, ok := strings.Cut(pending, "\n")
				if !ok {
					break
				}
				logs.appendAttempt(attempt, strings.TrimSuffix(line, "\r"))
				pending = rest
			}
		}
		if err != nil {
			if pending != "" {
				logs.appendAttempt(attempt, pending)
			}
			return
		}
	}
}

func (m *serviceManager) wait(name string, generation uint64, cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	close(done)
	m.mu.Lock()
	current := m.services[name]
	if current == nil || current.cmd != cmd || current.generation != generation {
		m.mu.Unlock()
		return
	}
	current.cmd = nil
	current.done = nil
	if cmd.ProcessState != nil {
		code := int32(cmd.ProcessState.ExitCode())
		current.lastExitCode = &code
		current.message = "service process exited"
		if err != nil {
			current.message = err.Error()
		}
		current.phase = "Stopped"
		if code != 0 {
			current.phase = "Failed"
		}
	} else {
		current.phase = "Stopped"
		current.message = "service process exited"
	}
	restart := !current.stopping && current.spec.Enabled && (current.spec.RestartPolicy == "Always" || (current.spec.RestartPolicy == "OnFailure" && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 0)) && m.ctx.Err() == nil
	if restart {
		current.message = "service process exited; restarting"
		generation := current.generation
		m.mu.Unlock()
		go m.restartAfterDelay(name, generation)
		return
	}
	m.mu.Unlock()
}

func (m *serviceManager) restartAfterDelay(name string, generation uint64) {
	timer := time.NewTimer(serviceRestartDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-m.ctx.Done():
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.services[name]
	if current == nil || current.generation != generation || current.cmd != nil || current.stopping || !current.spec.Enabled {
		return
	}
	if err := m.startLocked(current); err != nil {
		current.startFailed = true
		current.phase = "Failed"
		current.message = err.Error()
	}
}

func (m *serviceManager) stopLocked(current *managedService) error {
	if current == nil || current.cmd == nil || current.cmd.Process == nil {
		return nil
	}
	pid := current.cmd.Process.Pid
	done := current.done
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(serviceStopTimeout):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
	current.cmd = nil
	current.done = nil
	current.phase = "Stopped"
	current.message = "service process stopped"
	return nil
}

func (m *serviceManager) statusLocked(current *managedService) serviceStatus {
	status := serviceStatus{Name: current.spec.Name, Phase: current.phase, Message: current.message, RestartCount: current.restartCount}
	if current.lastExitCode != nil {
		code := *current.lastExitCode
		status.LastExitCode = &code
	}
	status.Running = current.cmd != nil && current.done != nil && processRunning(current.done, current.cmd)
	if status.Running {
		status.PortListening, status.Reachable, status.Message = probeServiceEndpoint(current.spec.Port, current.spec.HealthPath)
	}
	return status
}

// probeServiceEndpoint separates transport readiness from process liveness.
// A declared health path must return a successful HTTP response; a TCP
// connect alone is not enough to report the service reachable.
func probeServiceEndpoint(port int32, healthPath string) (listening, reachable bool, message string) {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
	conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
	if err != nil {
		return false, false, "service port is not listening"
	}
	_ = conn.Close()
	if healthPath == "" {
		return true, true, "service process is accepting connections"
	}
	request, err := http.NewRequest(http.MethodGet, "http://"+address+healthPath, nil)
	if err != nil {
		return true, false, "service health check could not be created"
	}
	client := &http.Client{
		Timeout: 250 * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return true, false, "service health check failed"
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return true, false, fmt.Sprintf("service health check returned HTTP %d", response.StatusCode)
	}
	return true, true, "service health check is ready"
}

// registerServiceRuntimeEndpoints adds the loopback-only service manager API
// to the runtime supervisor. The caller-facing coordinator proxies this API
// only after its own control-token check.
func registerServiceRuntimeEndpoints(mux *http.ServeMux, manager *serviceManager) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/internal/service/logs", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "service manager is unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("name")
		logs, err := manager.Logs(r.Context(), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, logs)
	})
	mux.HandleFunc("/internal/service/workspace-revision", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "service manager is unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			SourceRevision uint64 `json:"sourceRevision"`
			SourceDigest   string `json:"sourceDigest"`
		}
		if err := decodeBoundedJSON(w, r, 16<<10, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		restarted, err := manager.ApplyWorkspaceRevision(r.Context(), request.SourceRevision, request.SourceDigest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"restarted": restarted})
	})
	mux.HandleFunc("/internal/service", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "service manager is unavailable", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodPost:
			var spec serviceSpec
			if err := decodeBoundedJSON(w, r, 128<<10, &spec); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			status, err := manager.Configure(r.Context(), spec)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, status)
		case http.MethodGet:
			status, err := manager.Status(r.Context(), r.URL.Query().Get("name"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, status)
		case http.MethodDelete:
			if err := manager.Remove(r.Context(), r.URL.Query().Get("name")); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *agentServer) applyNamedServiceWorkspaceRevision(ctx context.Context, revision uint64, digest string) (int, error) {
	runtime, ok := s.runtime.(serviceWorkspaceRevisionOperations)
	if !ok {
		return 0, nil
	}
	return runtime.ApplyServiceWorkspaceRevision(ctx, revision, digest)
}

func (s *agentServer) handleService(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeControl(w, r) {
		return
	}
	runtime, ok := s.runtime.(serviceRuntimeOperations)
	if !ok {
		http.Error(w, "named service supervision is unavailable", http.StatusNotImplemented)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var spec serviceSpec
		if err := decodeBoundedJSON(w, r, 128<<10, &spec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status, err := runtime.ConfigureService(r.Context(), spec)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodGet:
		status, err := runtime.ServiceStatus(r.Context(), r.URL.Query().Get("name"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodDelete:
		if err := runtime.RemoveService(r.Context(), r.URL.Query().Get("name")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *agentServer) handleServiceLogs(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeControl(w, r) {
		return
	}
	runtime, ok := s.runtime.(serviceRuntimeOperations)
	if !ok {
		http.Error(w, "named service supervision is unavailable", http.StatusNotImplemented)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	logs, err := runtime.ServiceLogs(r.Context(), r.URL.Query().Get("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, logs)
}
