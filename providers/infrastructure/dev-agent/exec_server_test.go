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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestExecServerRequiresAuthenticationEvenWhenLegacyControlIsInsecure(t *testing.T) {
	srv := newExecAgentServer(context.Background(), &agentConfig{WorkDir: t.TempDir(), AllowInsecureControl: true})
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(`{"argv":["/bin/echo","ok"]}`))
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestExecServerRejectsUnknownShellCommandField(t *testing.T) {
	srv := newExecAgentServer(context.Background(), &agentConfig{WorkDir: t.TempDir(), ControlToken: "secret"})
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(`{"command":"echo unsafe","argv":["/bin/true"]}`))
	req.Header.Set(controlTokenHeader, "secret")
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestExecServerRunsDirectArgvWithSanitizedEnvironmentAndChangedPaths(t *testing.T) {
	workdir := t.TempDir()
	t.Setenv("ONLY_EXPLICIT", "must-not-inherit")
	if err := os.WriteFile(filepath.Join(workdir, "before.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newExecAgentServer(context.Background(), &agentConfig{WorkDir: workdir, ControlToken: "secret"})
	reqBody := execRequest{
		Files: []execSourceFile{{Path: "staged.txt", Content: "staged\n"}},
		Argv:  []string{"/bin/sh", "-c", "test -z \"$ONLY_EXPLICIT\"; touch added.txt; rm before.txt"},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/exec", bytes.NewReader(raw))
	req.Header.Set(controlTokenHeader, "secret")
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var got execResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ExitCode != 0 || got.Stdout != "" || got.Stderr != "" {
		t.Fatalf("result = %+v", got)
	}
	if !slices.Equal(got.Changed, []string{"added.txt"}) {
		t.Fatalf("changed = %v, want [added.txt]", got.Changed)
	}
	if !slices.Equal(got.Deleted, []string{"before.txt"}) {
		t.Fatalf("deleted = %v, want [before.txt]", got.Deleted)
	}
	if _, err := os.Stat(filepath.Join(workdir, "staged.txt")); err != nil {
		t.Fatalf("staged file missing: %v", err)
	}
}

func TestExecServerStagesBeforeValidatingSourceCreatedWorkdir(t *testing.T) {
	result, err := runExecRequest(context.Background(), t.TempDir(), execRequest{
		Files:   []execSourceFile{{Path: "component/main.txt", Content: "source\n"}},
		WorkDir: "component",
		Argv:    []string{"/bin/cat", "main.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stdout != "source\n" {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecServerPreservesExplicitExecutableMode(t *testing.T) {
	result, err := runExecRequest(context.Background(), t.TempDir(), execRequest{
		Files: []execSourceFile{{Path: "script.sh", Content: "#!/bin/sh\nprintf 'executable\\n'\n", Executable: true}},
		Argv:  []string{"./script.sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stdout != "executable\n" {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecServerRejectsUnsafeSourceAndWorkdirPaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]execRequest{
		"source escape":   {Files: []execSourceFile{{Path: "../escape.txt", Content: "x"}}, Argv: []string{"/bin/true"}},
		"workdir escape":  {WorkDir: "../", Argv: []string{"/bin/true"}},
		"source symlink":  {Files: []execSourceFile{{Path: "link/file.txt", Content: "x"}}, Argv: []string{"/bin/true"}},
		"invalid utf8":    {Files: []execSourceFile{{Path: "bad.txt", Content: string([]byte{0xff})}}, Argv: []string{"/bin/true"}},
		"duplicate paths": {Files: []execSourceFile{{Path: "same.txt", Content: "one"}, {Path: "same.txt", Content: "two"}}, Argv: []string{"/bin/true"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runExecRequest(context.Background(), root, req); err == nil {
				t.Fatal("runExecRequest succeeded, want validation error")
			}
		})
	}
	if _, err := runExecRequest(context.Background(), root, execRequest{Argv: []string{"/bin/true"}, Env: map[string]string{"LD_PRELOAD": "evil.so"}}); err == nil {
		t.Fatal("caller environment override succeeded")
	}
	if _, err := runExecRequest(context.Background(), root, execRequest{Argv: []string{"/bin/true"}}); err == nil {
		t.Fatal("workspace symlink was accepted")
	}
}

func TestExecServerBoundsOutputAndKillsTimedOutProcess(t *testing.T) {
	result, err := runExecRequest(context.Background(), t.TempDir(), execRequest{
		Argv:      []string{"/bin/sh", "-c", "yes output & yes error >&2"},
		TimeoutMS: 30,
		MaxOutput: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut || result.ExitCode == 0 {
		t.Fatalf("result = %+v, want timeout and nonzero exit", result)
	}
	if !result.StdoutTruncated || len(result.Stdout) != 128 {
		t.Fatalf("stdout length/truncation = %d/%t, want 128/true", len(result.Stdout), result.StdoutTruncated)
	}
	if !result.StderrTruncated || len(result.Stderr) != 128 {
		t.Fatalf("stderr length/truncation = %d/%t, want 128/true", len(result.Stderr), result.StderrTruncated)
	}
}

func TestExecServerRejectsProcessTimeoutAboveMaximum(t *testing.T) {
	_, err := runExecRequest(context.Background(), t.TempDir(), execRequest{Argv: []string{"/bin/true"}, TimeoutMS: int((execMaxTimeout + time.Second) / time.Millisecond)})
	if err == nil {
		t.Fatal("timeout above maximum was accepted")
	}
}

func TestExecServerCancellationKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan execResponse, 1)
	errs := make(chan error, 1)
	go func() {
		result, err := runExecRequest(ctx, t.TempDir(), execRequest{Argv: []string{"/bin/sleep", "30"}})
		done <- result
		errs <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case result := <-done:
		if !result.Cancelled {
			t.Fatalf("result = %+v, want cancelled", result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled process did not exit")
	}
	if err := <-errs; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
}
