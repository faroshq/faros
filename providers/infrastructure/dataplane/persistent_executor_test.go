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

package dataplane

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

type persistentRuntimeRoundTripper struct {
	request *http.Request
}

func (t *persistentRuntimeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	t.request = r.Clone(r.Context())
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"phase":"completed","exitCode":0,"stdout":"ok\n"}`)),
		Request:    r,
	}, nil
}

type persistentTestRuntime struct {
	transport *persistentRuntimeRoundTripper
}

func (r *persistentTestRuntime) Host() string                          { return "https://runtime.example" }
func (r *persistentTestRuntime) Transport() (http.RoundTripper, error) { return r.transport, nil }
func (r *persistentTestRuntime) ControlToken(context.Context, string, string) (string, error) {
	return "control-token", nil
}

func TestPersistentExecutorRoutesLiveAgentAndDeduplicatesStart(t *testing.T) {
	transport := &persistentRuntimeRoundTripper{}
	executor, err := NewPersistentExecutor(&persistentTestRuntime{transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	call := ExecCall{
		Workspace: "ws", Resource: "applications", Name: "app", Component: "backend",
		Capability: &infrav1alpha1.TemplateDataPlaneExec{MaxTimeoutSeconds: 5, MaxOutputBytes: 128},
		WorkingDir: "/workspace", RuntimeNamespace: "runtime", CallerKey: "caller",
		ControlTarget: ResolvedTarget{
			ServiceNamespace: "runtime", ServiceName: "app-dev-backend-control", ServicePort: "control",
			TokenSecretNamespace: "runtime", TokenSecretName: "app-control", UpstreamPath: "/exec",
		},
		Request: ExecRequest{
			Action: ExecActionStart, RequestID: "request-1", Argv: []string{"go", "test", "./..."},
			SourceRevision: 4, SourceDigest: strings.Repeat("a", 64),
		},
		IdempotencyKey: "request-1",
	}
	started, err := executor.Start(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := executor.Start(context.Background(), call)
	if err != nil || duplicate.SessionID != started.SessionID {
		t.Fatalf("duplicate start = %#v, %v", duplicate, err)
	}
	poll := call
	poll.Request = ExecRequest{Action: ExecActionPoll, SessionID: started.SessionID, RequestID: "request-1"}
	var result ExecResult
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		result, err = executor.Poll(context.Background(), poll)
		if err != nil {
			t.Fatal(err)
		}
		if execTerminalState(result.State) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if result.State != "succeeded" || result.ExitCode == nil || *result.ExitCode != 0 || result.Stdout != "ok\n" {
		t.Fatalf("result = %#v", result)
	}
	if transport.request == nil {
		t.Fatal("persistent executor did not call runtime transport")
	}
	if got, want := transport.request.URL.Path, "/api/v1/namespaces/runtime/services/app-dev-backend-control:control/proxy/exec"; got != want {
		t.Fatalf("agent proxy path = %q, want %q", got, want)
	}
	if got := transport.request.Header.Get(controlTokenHeader); got != "control-token" {
		t.Fatalf("agent control token = %q", got)
	}
}
