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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestCatalogWarmBuildAvoidsDiscovery(t *testing.T) {
	transport := newCatalogTestTransport()
	installCatalogTransport(t, transport)

	cache := newCatalogCache(time.Minute, 8)
	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://infra.invalid/mcp"}}
	params := buildParams{
		cluster:   "tenant-a",
		name:      "default",
		token:     "credential-a",
		enumerate: func(context.Context) []ProviderTarget { return targets },
		catalog:   cache,
	}

	buildServer(context.Background(), params)
	buildServer(context.Background(), params)

	if got := transport.count("infra.invalid", "initialize"); got != 1 {
		t.Fatalf("initialize calls = %d, want 1", got)
	}
	if got := transport.count("infra.invalid", "tools/list"); got != 1 {
		t.Fatalf("tools/list calls = %d, want 1", got)
	}
}

func TestCatalogConcurrentMissesCoalesce(t *testing.T) {
	transport := newCatalogTestTransport()
	started := make(chan struct{})
	release := make(chan struct{})
	transport.beforeResponse = func(host, method string) {
		if host == "infra.invalid" && method == "initialize" {
			transport.onceStart.Do(func() { close(started) })
			<-release
		}
	}
	installCatalogTransport(t, transport)

	cache := newCatalogCache(time.Minute, 8)
	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://infra.invalid/mcp"}}
	const waiters = 12
	results := make(chan *providerCatalog, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			results <- cache.get(context.Background(), targets, "credential-a", "tenant-a")
		}()
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("catalog refresh did not start")
	}
	if got := transport.count("infra.invalid", "initialize"); got != 1 {
		t.Fatalf("initialize calls while refresh was blocked = %d, want 1", got)
	}
	close(release)
	for i := 0; i < waiters; i++ {
		select {
		case catalog := <-results:
			if _, ok := catalog.metadataFor(targets[0]); !ok {
				t.Fatal("coalesced waiter received no provider metadata")
			}
		case <-time.After(time.Second):
			t.Fatal("coalesced waiter did not receive catalog")
		}
	}
	if got := transport.count("infra.invalid", "tools/list"); got != 1 {
		t.Fatalf("tools/list calls = %d, want 1", got)
	}
}

func TestCatalogSlowUnrelatedProviderDoesNotBlockWarmBuild(t *testing.T) {
	transport := newCatalogTestTransport()
	installCatalogTransport(t, transport)

	cache := newCatalogCache(time.Minute, 8)
	targets := []ProviderTarget{
		{Name: "healthy", MCPURL: "http://healthy.invalid/mcp"},
		{Name: "slow", MCPURL: "http://slow.invalid/mcp"},
	}
	params := buildParams{
		cluster:   "tenant-a",
		name:      "default",
		token:     "credential-a",
		enumerate: func(context.Context) []ProviderTarget { return targets },
		catalog:   cache,
	}
	buildServer(context.Background(), params)

	transport.beforeResponse = func(host, _ string) {
		if host == "slow.invalid" {
			select {
			case <-transport.slowRelease:
			case <-time.After(time.Second):
			}
		}
	}
	start := time.Now()
	buildServer(context.Background(), params)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("warm build took %s with a blocked unrelated provider", elapsed)
	}
	if got := transport.count("slow.invalid", "initialize") + transport.count("slow.invalid", "tools/list"); got != 2 {
		t.Fatalf("warm build rediscovered slow provider %d times, want 2 total", got)
	}
}

func TestCatalogChangedTargetsRefresh(t *testing.T) {
	transport := newCatalogTestTransport()
	installCatalogTransport(t, transport)

	cache := newCatalogCache(time.Minute, 8)
	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://one.invalid/mcp"}}
	cache.get(context.Background(), targets, "credential-a", "tenant-a")
	targets[0].MCPURL = "http://two.invalid/mcp"
	catalog := cache.get(context.Background(), targets, "credential-a", "tenant-a")
	if _, ok := catalog.metadataFor(targets[0]); !ok {
		t.Fatal("changed target did not produce current metadata")
	}
	if got := transport.count("one.invalid", "initialize"); got != 1 {
		t.Fatalf("old target initialize calls = %d, want 1", got)
	}
	if got := transport.count("two.invalid", "initialize"); got != 1 {
		t.Fatalf("new target initialize calls = %d, want 1", got)
	}
	if got := transport.count("two.invalid", "tools/list"); got != 1 {
		t.Fatalf("new target tools/list calls = %d, want 1", got)
	}
}

func TestCatalogAddedAndRemovedTargetsRefresh(t *testing.T) {
	transport := newCatalogTestTransport()
	installCatalogTransport(t, transport)

	cache := newCatalogCache(time.Minute, 8)
	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://infra.invalid/mcp"}}
	cache.get(context.Background(), targets, "credential-a", "tenant-a")
	targets = append(targets, ProviderTarget{Name: "code", MCPURL: "http://code.invalid/mcp"})
	cache.get(context.Background(), targets, "credential-a", "tenant-a")
	targets = targets[:1]
	catalog := cache.get(context.Background(), targets, "credential-a", "tenant-a")
	if _, ok := catalog.metadataFor(ProviderTarget{Name: "code", MCPURL: "http://code.invalid/mcp"}); ok {
		t.Fatal("removed target metadata remained in the current catalog")
	}
	if got := transport.count("infra.invalid", "initialize"); got != 3 {
		t.Fatalf("infra initialize calls across target-set changes = %d, want 3", got)
	}
	if got := transport.count("code.invalid", "initialize"); got != 1 {
		t.Fatalf("code initialize calls across target-set changes = %d, want 1", got)
	}
}

func TestCatalogProxyUsesCurrentTargetURL(t *testing.T) {
	transport := newCatalogTestTransport()
	installCatalogTransport(t, transport)

	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://old.invalid/mcp"}}
	h := New(Options{
		Providers: func(context.Context) []ProviderTarget { return targets },
		Verifier:  allowAll,
	})
	if _, code := jsonrpc(t, h, "tools/list", `{}`); code != http.StatusOK {
		t.Fatalf("initial tools/list status = %d, want 200", code)
	}
	targets[0].MCPURL = "http://new.invalid/mcp"
	if _, code := jsonrpc(t, h, "tools/list", `{}`); code != http.StatusOK {
		t.Fatalf("changed tools/list status = %d, want 200", code)
	}
	if _, code := jsonrpc(t, h, "tools/call", `{"name":"infra__ping","arguments":{}}`); code != http.StatusOK {
		t.Fatalf("tools/call status = %d, want 200", code)
	}
	if got := transport.count("old.invalid", "tools/call"); got != 0 {
		t.Fatalf("old target tools/call calls = %d, want 0", got)
	}
	if got := transport.count("new.invalid", "tools/call"); got != 1 {
		t.Fatalf("new target tools/call calls = %d, want 1", got)
	}
}

func TestCatalogExpiryRefreshesMetadata(t *testing.T) {
	transport := newCatalogTestTransport()
	installCatalogTransport(t, transport)

	cache := newCatalogCache(20*time.Millisecond, 8)
	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://infra.invalid/mcp"}}
	cache.get(context.Background(), targets, "credential-a", "tenant-a")
	time.Sleep(40 * time.Millisecond)
	cache.get(context.Background(), targets, "credential-a", "tenant-a")

	if got := transport.count("infra.invalid", "initialize"); got != 2 {
		t.Fatalf("initialize calls after expiry = %d, want 2", got)
	}
}

func TestCatalogCacheEvictsOldestScope(t *testing.T) {
	transport := newCatalogTestTransport()
	installCatalogTransport(t, transport)

	cache := newCatalogCache(time.Minute, 2)
	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://infra.invalid/mcp"}}
	for _, tenant := range []string{"tenant-a", "tenant-b", "tenant-c"} {
		cache.get(context.Background(), targets, "credential-a", tenant)
	}
	cache.mu.Lock()
	retained := len(cache.entries)
	cache.mu.Unlock()
	if retained != 2 {
		t.Fatalf("retained catalogs = %d, want 2", retained)
	}
	cache.get(context.Background(), targets, "credential-a", "tenant-a")
	if got := transport.count("infra.invalid", "initialize"); got != 4 {
		t.Fatalf("initialize calls after oldest scope revisit = %d, want 4", got)
	}
}

func TestCatalogCanceledWaiterDoesNotCancelRefresh(t *testing.T) {
	transport := newCatalogTestTransport()
	started := make(chan struct{})
	release := make(chan struct{})
	transport.beforeResponse = func(host, method string) {
		if host == "infra.invalid" && method == "tools/list" {
			transport.onceStart.Do(func() { close(started) })
			<-release
		}
	}
	installCatalogTransport(t, transport)

	cache := newCatalogCache(time.Minute, 8)
	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://infra.invalid/mcp"}}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan *providerCatalog, 1)
	go func() { first <- cache.get(ctx, targets, "credential-a", "tenant-a") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("catalog refresh did not reach tools/list")
	}

	second := make(chan *providerCatalog, 1)
	go func() { second <- cache.get(context.Background(), targets, "credential-a", "tenant-a") }()
	cancel()
	select {
	case <-first:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("canceled waiter remained blocked on shared refresh")
	}
	close(release)
	select {
	case catalog := <-second:
		if _, ok := catalog.metadataFor(targets[0]); !ok {
			t.Fatal("remaining waiter did not receive refreshed metadata")
		}
	case <-time.After(time.Second):
		t.Fatal("remaining waiter did not receive refreshed catalog")
	}
	if got := transport.count("infra.invalid", "initialize"); got != 1 {
		t.Fatalf("refresh initialize calls = %d, want 1", got)
	}
}

func TestCatalogSeparatesCredentialAndTenantScopes(t *testing.T) {
	transport := newCatalogTestTransport()
	installCatalogTransport(t, transport)

	cache := newCatalogCache(time.Minute, 8)
	targets := []ProviderTarget{{Name: "infra", MCPURL: "http://infra.invalid/mcp"}}
	cache.get(context.Background(), targets, "credential-a", "tenant-a")
	cache.get(context.Background(), targets, "credential-a", "tenant-b")
	cache.get(context.Background(), targets, "credential-b", "tenant-a")

	if got := transport.count("infra.invalid", "initialize"); got != 3 {
		t.Fatalf("initialize calls across scopes = %d, want 3", got)
	}
	if got := transport.count("infra.invalid", "tools/list"); got != 3 {
		t.Fatalf("tools/list calls across scopes = %d, want 3", got)
	}
	cache.mu.Lock()
	for scope := range cache.entries {
		if scope.credentialDigest == "credential-a" || scope.credentialDigest == "credential-b" {
			t.Fatalf("cache retained raw credential key %q", scope.credentialDigest)
		}
	}
	cache.mu.Unlock()
}

type catalogTestTransport struct {
	mu             sync.Mutex
	calls          map[string]int
	beforeResponse func(host, method string)
	onceStart      sync.Once
	slowRelease    chan struct{}
}

func newCatalogTestTransport() *catalogTestTransport {
	return &catalogTestTransport{
		calls:       make(map[string]int),
		slowRelease: make(chan struct{}),
	}
}

func installCatalogTransport(t *testing.T, transport *catalogTestTransport) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func (t *catalogTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var request struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	host := r.URL.Host
	t.mu.Lock()
	t.calls[host+"|"+request.Method]++
	beforeResponse := t.beforeResponse
	t.mu.Unlock()
	if beforeResponse != nil {
		beforeResponse(host, request.Method)
	}
	switch request.Method {
	case "initialize":
		return jsonResponse(`{"jsonrpc":"2.0","id":1,"result":{"instructions":"guidance"}}`), nil
	case "tools/list":
		return jsonResponse(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"ping","inputSchema":{"type":"object"}}]}}`), nil
	case "tools/call":
		return jsonResponse(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"called"}]}}`), nil
	default:
		return nil, errors.New("unexpected catalog test method " + request.Method)
	}
}

func (t *catalogTestTransport) count(host, method string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls[host+"|"+method]
}
