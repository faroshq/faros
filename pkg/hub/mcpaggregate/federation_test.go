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

package mcpaggregate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFilterBoundProviderTargets(t *testing.T) {
	targets := []ProviderTarget{
		{Name: "bound", APIExportPath: "root:faros:providers:bound", APIExportName: "bound.providers.faros.sh"},
		{Name: "unbound", APIExportPath: "root:faros:providers:unbound", APIExportName: "unbound.providers.faros.sh"},
		{Name: "absent"},
		{Name: "missing-name", APIExportPath: "root:faros:providers:missing-name"},
	}

	got := FilterBoundProviderTargets(targets, []ProviderBinding{
		{ExportPath: "root:faros:providers:bound", ExportName: "bound.providers.faros.sh", Phase: "Bound"},
		{ExportPath: "root:faros:providers:unbound", ExportName: "unbound.providers.faros.sh", Phase: "Pending"},
		{ExportPath: "root:faros:providers:other-tenant", ExportName: "bound.providers.faros.sh", Phase: "Bound"},
	})
	if len(got) != 1 || got[0].Name != "bound" {
		t.Fatalf("filtered targets = %#v, want only bound provider", got)
	}
}

func TestFilterBoundProviderTargetsIsolatesBindingSnapshot(t *testing.T) {
	target := ProviderTarget{
		Name:          "infra",
		APIExportPath: "root:faros:providers:infra",
		APIExportName: "infra.providers.faros.sh",
	}

	orgA := FilterBoundProviderTargets([]ProviderTarget{target}, []ProviderBinding{{
		ExportPath: target.APIExportPath, ExportName: target.APIExportName, Phase: "Bound",
	}})
	orgB := FilterBoundProviderTargets([]ProviderTarget{target}, nil)
	if len(orgA) != 1 {
		t.Fatalf("org A targets = %#v, want bound provider", orgA)
	}
	if len(orgB) != 0 {
		t.Fatalf("org B targets = %#v, want no provider without its binding", orgB)
	}
}

func TestProxyToolPreservesAnnotations(t *testing.T) {
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: true}
	tool := proxyTool(
		ProviderTarget{Name: "infra", DisplayName: "Infrastructure"},
		discoveredTool{Name: "inspect", Title: "Inspect", Annotations: annotations},
	)
	if tool.Name != "infra__inspect" {
		t.Fatalf("proxy tool name = %q, want infra__inspect", tool.Name)
	}
	if tool.Annotations != annotations {
		t.Fatalf("proxy annotations pointer = %#v, want %#v", tool.Annotations, annotations)
	}
}

func TestProviderMCPClientForwardsBoundedProvenanceAllowlist(t *testing.T) {
	var got http.Header
	client := newProviderMCPClientWithProvenance(
		"caller-token",
		"root:faros:orgs:org-a:ws-a",
		"cluster-a",
		map[string]string{
			provenanceUserHeader:      "alice",
			provenanceOrgHeader:       "org-a",
			provenanceWorkspaceHeader: "ws-a",
			provenanceClusterHeader:   "spoofed-cluster",
			"X-Faros-Tenant":          "spoofed-tenant",
			"X-Faros-Evil":            "should-not-forward",
		},
	)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Clone()
		return jsonResponse(`{"jsonrpc":"2.0","id":1,"result":{}}`), nil
	})

	if _, err := client.listTools(context.Background(), "http://provider.invalid/mcp"); err != nil {
		t.Fatalf("listTools error = %v", err)
	}
	if got.Get("Authorization") != "Bearer caller-token" {
		t.Fatalf("authorization = %q, want caller token", got.Get("Authorization"))
	}
	if got.Get("X-Faros-Tenant") != "root:faros:orgs:org-a:ws-a" {
		t.Fatalf("tenant = %q, want server-derived tenant", got.Get("X-Faros-Tenant"))
	}
	if got.Get("X-Faros-Cluster") != "cluster-a" {
		t.Fatalf("cluster = %q, want server-derived cluster", got.Get("X-Faros-Cluster"))
	}
	for name, want := range map[string]string{
		provenanceUserHeader: "alice", provenanceOrgHeader: "org-a", provenanceWorkspaceHeader: "ws-a",
	} {
		if got.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, got.Get(name), want)
		}
	}
	for _, name := range []string{"X-Faros-Evil", "X-Faros-AppStudio-Internal-Token"} {
		if got.Get(name) != "" {
			t.Errorf("%s was forwarded as %q", name, got.Get(name))
		}
	}
}

func TestCaptureProvenanceDropsOversizedAndArbitraryHeaders(t *testing.T) {
	oversized := strings.Repeat("x", provenanceHeaderMaxBytes+1)
	got := captureProvenance(http.Header{
		provenanceUserHeader: []string{"alice"},
		provenanceOrgHeader:  []string{oversized},
		"X-Faros-Evil":       []string{"nope"},
		"X-Not-Faros":        []string{"nope"},
	})
	if got[provenanceUserHeader] != "alice" {
		t.Fatalf("captured user = %#v, want alice", got)
	}
	if _, ok := got[provenanceOrgHeader]; ok {
		t.Fatalf("oversized org provenance was retained: %#v", got)
	}
	if _, ok := got["X-Faros-Evil"]; ok {
		t.Fatalf("arbitrary provenance was retained: %#v", got)
	}
}

func TestProviderMCPClientDefaultTimeoutPolicy(t *testing.T) {
	client := newProviderMCPClient("", "", "")

	if got, want := client.discoveryTimeout, 15*time.Second; got != want {
		t.Fatalf("discovery timeout = %s, want %s", got, want)
	}
	if got, want := client.callTimeout, 90*time.Second; got != want {
		t.Fatalf("call timeout = %s, want %s", got, want)
	}
	if client.callTimeout >= 2*time.Minute {
		t.Fatalf("call timeout = %s, want it below the App Studio 2m budget", client.callTimeout)
	}
}

func TestProviderMCPClientInitializeUsesDiscoveryTimeout(t *testing.T) {
	client := newProviderMCPClient("", "", "")
	deadline := make(chan time.Time, 1)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		d, ok := r.Context().Deadline()
		if !ok {
			t.Error("initialize request has no deadline")
		} else {
			deadline <- d
		}
		return jsonResponse(`{"jsonrpc":"2.0","id":1,"result":{"instructions":"ready"}}`), nil
	})

	if got := client.fetchInstructions(context.Background(), "http://provider.invalid/mcp"); got != "ready" {
		t.Fatalf("fetchInstructions result = %q, want ready", got)
	}
	if got := time.Until(<-deadline); got < 14*time.Second || got > 15*time.Second {
		t.Fatalf("initialize deadline is %s from observation, want approximately 15s", got)
	}
}

func TestProviderMCPClientListUsesDefaultDiscoveryTimeout(t *testing.T) {
	client := newProviderMCPClient("", "", "")
	deadline := make(chan time.Time, 1)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		d, ok := r.Context().Deadline()
		if !ok {
			t.Error("tools/list request has no deadline")
		} else {
			deadline <- d
		}
		return jsonResponse(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`), nil
	})

	if tools, err := client.listTools(context.Background(), "http://provider.invalid/mcp"); err != nil {
		t.Fatalf("listTools error = %v", err)
	} else if len(tools) != 0 {
		t.Fatalf("listTools returned %d tools, want 0", len(tools))
	}
	if got := time.Until(<-deadline); got < 14*time.Second || got > 15*time.Second {
		t.Fatalf("tools/list deadline is %s from observation, want approximately 15s", got)
	}
}

func TestProviderMCPClientCallUsesDefaultCallTimeout(t *testing.T) {
	client := newProviderMCPClient("", "", "")
	deadline := make(chan time.Time, 1)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		d, ok := r.Context().Deadline()
		if !ok {
			t.Error("tools/call request has no deadline")
		} else {
			deadline <- d
		}
		return jsonResponse(`{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`), nil
	})

	if _, err := client.callTool(context.Background(), "http://provider.invalid/mcp", "slow", nil); err != nil {
		t.Fatalf("callTool error = %v", err)
	}
	if got := time.Until(<-deadline); got < 89*time.Second || got > 90*time.Second {
		t.Fatalf("tools/call deadline is %s from observation, want approximately 90s", got)
	}
}

func TestProviderMCPClientListUsesDiscoveryTimeout(t *testing.T) {
	started := make(chan struct{})
	client := newProviderMCPClientWithTimeouts("", "", "", 20*time.Millisecond, time.Second)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	result := make(chan error, 1)
	go func() {
		_, err := client.listTools(context.Background(), "http://provider.invalid/mcp")
		result <- err
	}()

	<-started
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("listTools error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listTools did not honor its discovery timeout")
	}
}

func TestProviderMCPClientCallUsesLongerTimeout(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := newProviderMCPClientWithTimeouts("", "", "", 20*time.Millisecond, 200*time.Millisecond)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-release:
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"done"}]}}`)),
			}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})

	discoveryTimeout := client.discoveryTimeout
	result := make(chan struct {
		gotResult bool
		err       error
	}, 1)
	go func() {
		_, err := client.callTool(context.Background(), "http://provider.invalid/mcp", "slow", nil)
		if err != nil {
			result <- struct {
				gotResult bool
				err       error
			}{err: err}
			return
		}
		result <- struct {
			gotResult bool
			err       error
		}{gotResult: true}
	}()

	<-started
	select {
	case outcome := <-result:
		t.Fatalf("callTool returned before its call timeout: gotResult=%v err=%v", outcome.gotResult, outcome.err)
	case <-time.After(2 * discoveryTimeout):
	}
	close(release)

	select {
	case outcome := <-result:
		if outcome.err != nil {
			t.Fatalf("callTool error = %v", outcome.err)
		}
		if !outcome.gotResult {
			t.Fatalf("callTool result was not received")
		}
	case <-time.After(time.Second):
		t.Fatal("callTool did not complete after provider response")
	}
}

func TestProviderMCPClientCallHonorsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	client := newProviderMCPClientWithTimeouts("", "", "", time.Second, time.Second)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.callTool(ctx, "http://provider.invalid/mcp", "cancel", nil)
		result <- err
	}()

	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("callTool error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("callTool did not honor caller cancellation")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
