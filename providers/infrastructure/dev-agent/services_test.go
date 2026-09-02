// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkspaceRevisionRestartsNamedServiceAfterSyncAndMutation(t *testing.T) {
	workspace := t.TempDir()
	config := &agentConfig{WorkDir: workspace, ControlToken: "test-token"}
	logs := newRingLog(500)
	manager := newServiceManager(t.Context(), workspace)
	runtimeServer := httptest.NewServer(newRuntimeSupervisorServer(newSupervisor(t.Context(), config, logs), logs, nil, manager))
	t.Cleanup(runtimeServer.Close)
	t.Cleanup(manager.StopAll)
	runtimeClient := &httpRuntimeClient{baseURL: runtimeServer.URL, client: runtimeServer.Client()}
	server := newCoordinatorServer(config, runtimeClient, &sync.Mutex{})
	firstFiles := []syncFile{{Path: "version.txt", Content: "one"}}
	firstDigest, err := digestSyncFiles(firstFiles)
	if err != nil {
		t.Fatal(err)
	}
	recorder, first := doSync(t, server, syncRequest{Files: firstFiles, SourceRevision: 1, SourceDigest: firstDigest})
	if recorder.Code != http.StatusOK {
		t.Fatalf("initial sync status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if first.NamedServicesRestarted != 0 {
		t.Fatalf("initial sync restarted services = %d, want 0", first.NamedServicesRestarted)
	}

	observed := filepath.Join(workspace, "observed")
	runtime := serviceRuntimeOperations(runtimeClient)
	status, err := runtime.ConfigureService(t.Context(), serviceSpec{
		Name: "web", Argv: []string{"/bin/sh", "-c", "cat version.txt > observed; sleep 30"},
		Port: 18093, Enabled: true, RestartPolicy: "Never",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.RemoveService(context.Background(), "web") })
	if !status.Running || status.RestartCount != 0 {
		t.Fatalf("initial service status = %+v", status)
	}
	waitForFileValue(t, observed, "one")

	secondFiles := []syncFile{{Path: "version.txt", Content: "two"}}
	secondDigest, err := digestSyncFiles(secondFiles)
	if err != nil {
		t.Fatal(err)
	}
	recorder, second := doSync(t, server, syncRequest{Files: secondFiles, SourceRevision: 2, SourceDigest: secondDigest})
	if recorder.Code != http.StatusOK {
		t.Fatalf("second sync status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if second.NamedServicesRestarted != 1 {
		t.Fatalf("second sync restarted services = %d, want 1", second.NamedServicesRestarted)
	}
	waitForFileValue(t, observed, "two")
	status, err = runtime.ServiceStatus(t.Context(), "web")
	if err != nil || status.RestartCount != 1 {
		t.Fatalf("status after second sync = %+v err=%v, want one restart", status, err)
	}

	recorder, replay := doSync(t, server, syncRequest{Files: secondFiles, SourceRevision: 2, SourceDigest: secondDigest})
	if recorder.Code != http.StatusOK || replay.NamedServicesRestarted != 0 {
		t.Fatalf("replayed sync status = %d response=%+v", recorder.Code, replay)
	}
	status, err = runtime.ServiceStatus(t.Context(), "web")
	if err != nil || status.RestartCount != 1 {
		t.Fatalf("status after replay = %+v err=%v, want unchanged restart count", status, err)
	}

	recorder, raw := workspaceRequest(t, server, http.MethodPost, "/workspace/mutate", workspaceMutateRequest{
		ExpectedRevision: 2,
		ExpectedDigest:   secondDigest,
		Operations:       []workspaceMutation{{Operation: "write", Path: "version.txt", Content: "three"}},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("workspace mutation status = %d body=%s", recorder.Code, raw)
	}
	var mutated workspaceMutateResponse
	if err := json.Unmarshal(raw, &mutated); err != nil {
		t.Fatal(err)
	}
	if mutated.SourceRevision != 3 || mutated.NamedServicesRestarted != 1 {
		t.Fatalf("workspace mutation response = %+v, want revision 3 and one restart", mutated)
	}
	waitForFileValue(t, observed, "three")
	status, err = runtime.ServiceStatus(t.Context(), "web")
	if err != nil || status.RestartCount != 2 {
		t.Fatalf("status after workspace mutation = %+v err=%v, want two restarts", status, err)
	}
}

func TestServiceManagerWorkspaceRevisionRejectsStaleOrConflictingEvidence(t *testing.T) {
	manager := newServiceManager(t.Context(), t.TempDir())
	if restarted, err := manager.ApplyWorkspaceRevision(t.Context(), 4, "sha256:first"); err != nil || restarted != 0 {
		t.Fatalf("initial workspace revision = restarted %d err=%v", restarted, err)
	}
	if _, err := manager.ApplyWorkspaceRevision(t.Context(), 3, "sha256:older"); err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("stale workspace revision error = %v", err)
	}
	if _, err := manager.ApplyWorkspaceRevision(t.Context(), 4, "sha256:conflict"); err == nil || !strings.Contains(err.Error(), "different digest") {
		t.Fatalf("conflicting workspace revision error = %v", err)
	}
}

func TestServiceConnectionFilesInjectAndRevisionRestartsNeverPolicy(t *testing.T) {
	originalRoot := connectionFilesRoot
	connectionFilesRoot = t.TempDir()
	t.Cleanup(func() { connectionFilesRoot = originalRoot })
	credentialFile := filepath.Join(connectionFilesRoot, "database-url")
	if err := os.WriteFile(credentialFile, []byte("postgres://first"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	output := filepath.Join(workspace, "observed")
	manager := newServiceManager(t.Context(), workspace)
	t.Cleanup(manager.StopAll)
	spec := serviceSpec{
		Name: "api", Argv: []string{"/bin/sh", "-c", "printf %s \"$DATABASE_URL\" > observed; sleep 30"},
		Port: 18090, Enabled: true, RestartPolicy: "Never", EnvFiles: map[string]string{"DATABASE_URL": credentialFile}, ConnectionRevision: "r1",
	}
	if _, err := manager.Configure(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	waitForFileValue(t, output, "postgres://first")
	if err := os.WriteFile(credentialFile, []byte("postgres://second"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec.ConnectionRevision = "r2"
	if _, err := manager.Configure(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	waitForFileValue(t, output, "postgres://second")
}

func TestServiceRestartTokenForcesRestartWithNeverPolicy(t *testing.T) {
	workspace := t.TempDir()
	manager := newServiceManager(t.Context(), workspace)
	t.Cleanup(manager.StopAll)
	spec := serviceSpec{
		Name: "api", Argv: []string{"/bin/sh", "-c", "sleep 30"},
		Port: 18094, Enabled: true, RestartPolicy: "Never", RestartToken: "request-1",
	}
	status, err := manager.Configure(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running {
		t.Fatalf("initial status=%+v, want running service", status)
	}

	status, err = manager.Configure(t.Context(), spec)
	if err != nil || status.RestartCount != 0 {
		t.Fatalf("unchanged restart token status=%+v err=%v, want no restart", status, err)
	}
	spec.RestartToken = "request-2"
	status, err = manager.Configure(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if status.RestartCount != 1 {
		t.Fatalf("changed restart token status=%+v, want one restart", status)
	}
}

func TestServiceManagerRetriesFailedStartWhenConnectionFileAppears(t *testing.T) {
	originalRoot := connectionFilesRoot
	connectionFilesRoot = t.TempDir()
	t.Cleanup(func() { connectionFilesRoot = originalRoot })
	credentialFile := filepath.Join(connectionFilesRoot, "database-url")
	workspace := t.TempDir()
	output := filepath.Join(workspace, "observed")
	manager := newServiceManager(t.Context(), workspace)
	t.Cleanup(manager.StopAll)
	spec := serviceSpec{
		Name: "api", Argv: []string{"/bin/sh", "-c", "printf %s \"$DATABASE_URL\" > observed; sleep 30"},
		Port: 18092, Enabled: true, RestartPolicy: "Never",
		EnvFiles: map[string]string{"DATABASE_URL": credentialFile}, ConnectionRevision: "r1",
	}
	status, err := manager.Configure(t.Context(), spec)
	if err == nil || status.Phase != "Failed" || status.Running {
		t.Fatalf("initial configure = status=%+v err=%v, want failed start", status, err)
	}
	if err := os.WriteFile(credentialFile, []byte("postgres://appeared"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Configure(t.Context(), spec)
	if err != nil {
		t.Fatalf("retry configure: %v", err)
	}
	if !status.Running || status.Phase != "Running" {
		t.Fatalf("retry status = %+v, want running service", status)
	}
	waitForFileValue(t, output, "postgres://appeared")
}

func TestNormalizeServiceSpecRejectsConnectionFileEscape(t *testing.T) {
	originalRoot := connectionFilesRoot
	connectionFilesRoot = t.TempDir()
	t.Cleanup(func() { connectionFilesRoot = originalRoot })
	_, err := normalizeServiceSpec(serviceSpec{Name: "api", Argv: []string{"/bin/true"}, Port: 18091, Enabled: true, EnvFiles: map[string]string{"DATABASE_URL": filepath.Join(connectionFilesRoot, "..", "stolen")}})
	if err == nil || !strings.Contains(err.Error(), "outside the managed mount") {
		t.Fatalf("escape error = %v", err)
	}
}

func waitForFileValue(t *testing.T, name, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(name); err == nil && string(raw) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, err := os.ReadFile(name)
	t.Fatalf("file %s = %q, %v; want %q", name, raw, err, want)
}

func TestServiceManagerSupervisesIndependentProcess(t *testing.T) {
	manager := newServiceManager(t.Context(), t.TempDir())
	status, err := manager.Configure(t.Context(), serviceSpec{
		Name: "web", Argv: []string{"/bin/sh", "-c", "sleep 30"}, Port: 18080, Enabled: true, RestartPolicy: "Never",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.Name != "web" {
		t.Fatalf("configured status = %+v, want running web service", status)
	}
	status, err = manager.Configure(t.Context(), serviceSpec{
		Name: "web", Argv: []string{"/bin/sh", "-c", "sleep 30"}, Port: 18080, Enabled: true, RestartPolicy: "Never",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running {
		t.Fatalf("idempotent configure stopped service: %+v", status)
	}
	if status.RestartCount != 0 {
		t.Fatalf("idempotent configure restarted healthy service: %+v", status)
	}
	if err := manager.Remove(t.Context(), "web"); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Status(t.Context(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if status.Running || status.Phase != "Stopped" {
		t.Fatalf("removed status = %+v, want stopped", status)
	}
}

func TestServiceManagerRejectsUnsafeProcessContract(t *testing.T) {
	manager := newServiceManager(t.Context(), t.TempDir())
	for _, spec := range []serviceSpec{
		{Name: "Bad", Argv: []string{"/bin/true"}, Port: 18080},
		{Name: "bad", Argv: []string{"/bin/true"}, Port: 7070},
		{Name: "bad", Argv: []string{"/bin/true"}, Port: 18080, WorkDir: "../escape"},
		{Name: "bad", Argv: []string{"/bin/true", string([]byte{0})}, Port: 18080},
		{Name: "bad", Argv: []string{"/bin/true"}, Port: 18080, Env: map[string]string{"API_TOKEN": "secret"}},
	} {
		if _, err := manager.Configure(t.Context(), spec); err == nil {
			t.Fatalf("unsafe service spec %+v was accepted", spec)
		}
	}
}

func TestServiceManagerRestartPolicyOnFailure(t *testing.T) {
	manager := newServiceManager(t.Context(), t.TempDir())
	if _, err := manager.Configure(t.Context(), serviceSpec{
		Name: "crash", Argv: []string{"/bin/sh", "-c", "exit 1"}, Port: 18081, Enabled: true, RestartPolicy: "OnFailure",
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := manager.Status(t.Context(), "crash")
		if err != nil {
			t.Fatal(err)
		}
		if status.RestartCount > 0 {
			_ = manager.Remove(t.Context(), "crash")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	status, _ := manager.Status(t.Context(), "crash")
	_ = manager.Remove(t.Context(), "crash")
	t.Fatalf("failure policy did not restart service: %+v", status)
}

func TestServiceEndpointsStayControlTokenGated(t *testing.T) {
	server := newAgentServer(t.Context(), &agentConfig{WorkDir: t.TempDir(), ControlToken: "control"})
	request := httptest.NewRequest(http.MethodPost, "/service", strings.NewReader(`{"name":"web","argv":["/bin/sleep","30"],"port":18082,"enabled":true}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated service create status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	request = httptest.NewRequest(http.MethodPost, "/service", strings.NewReader(`{"name":"web","argv":["/bin/sleep","30"],"port":18082,"enabled":true}`))
	request.Header.Set(controlTokenHeader, "control")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated service create status = %d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodDelete, "/service?name=web", nil)
	request.Header.Set(controlTokenHeader, "control")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated service remove status = %d body=%s", response.Code, response.Body.String())
	}
	if runtime, ok := server.runtime.(serviceRuntimeOperations); ok {
		_, _ = runtime.ServiceStatus(t.Context(), "web")
	}
}
