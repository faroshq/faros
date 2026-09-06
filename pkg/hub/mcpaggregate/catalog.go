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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// catalogScope identifies the security context in which provider metadata was
// discovered. Provider MCP servers can return different tools or instructions
// for different tenants and credentials, so neither may be omitted.
type catalogScope struct {
	tenant           string
	credentialDigest string
}

type catalogRefreshKey struct {
	scope   catalogScope
	version string
}

// providerCatalog contains only immutable results from provider discovery. It
// deliberately has no request context, HTTP client, bearer token, or server
// callbacks. A fresh request server supplies those pieces while registering
// proxy handlers.
type providerCatalog struct {
	version   string
	providers map[string]catalogProvider
}

type catalogProvider struct {
	instructions string
	tools        []discoveredTool
	noMCP        bool
	discoveryErr string
}

type catalogCacheEntry struct {
	catalog   *providerCatalog
	expiresAt time.Time
	lastUsed  uint64
}

type catalogRefresh struct {
	done       chan struct{}
	snapshot   *providerCatalog
	generation uint64
}

// catalogCache is a bounded, scoped cache for provider MCP metadata. A cache
// miss starts one detached refresh for a (scope, target-version) pair. Other
// callers wait for that refresh, while a canceled waiter returns its previous
// snapshot without canceling work needed by the remaining waiters.
type catalogCache struct {
	mu          sync.Mutex
	ttl         time.Duration
	maxEntries  int
	now         func() time.Time
	entries     map[catalogScope]*catalogCacheEntry
	inflight    map[catalogRefreshKey]*catalogRefresh
	latest      map[catalogScope]uint64
	generation  uint64
	accessClock uint64
}

func newCatalogCache(ttl time.Duration, maxEntries int) *catalogCache {
	if ttl <= 0 {
		ttl = DefaultCatalogCacheTTL
	}
	if maxEntries <= 0 {
		maxEntries = DefaultCatalogCacheMaxEntries
	}
	return &catalogCache{
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        time.Now,
		entries:    make(map[catalogScope]*catalogCacheEntry),
		inflight:   make(map[catalogRefreshKey]*catalogRefresh),
		latest:     make(map[catalogScope]uint64),
	}
}

// get returns metadata for the current target set. Provider enumeration stays
// outside this type, so every request observes the hub's current Ready set.
// The target fingerprint is the version component: adding, removing, or
// changing a target starts a fresh discovery refresh immediately.
func (c *catalogCache) get(ctx context.Context, targets []ProviderTarget, bearerToken, cluster string) *providerCatalog {
	if c == nil {
		return discoverCatalog(ctx, targets, bearerToken, cluster)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	version := providerTargetVersion(targets)
	scope := catalogScope{
		tenant:           cluster,
		credentialDigest: credentialDigest(bearerToken),
	}
	refreshKey := catalogRefreshKey{scope: scope, version: version}

	c.mu.Lock()
	if c.now == nil {
		c.now = time.Now
	}
	if c.entries == nil {
		c.entries = make(map[catalogScope]*catalogCacheEntry)
	}
	if c.inflight == nil {
		c.inflight = make(map[catalogRefreshKey]*catalogRefresh)
	}
	if c.latest == nil {
		c.latest = make(map[catalogScope]uint64)
	}
	now := c.now()
	var fallback *providerCatalog
	if entry, ok := c.entries[scope]; ok {
		fallback = entry.catalog
		if entry.catalog.version == version && now.Before(entry.expiresAt) {
			c.pruneExpiredLocked(now, scope)
			c.accessClock++
			entry.lastUsed = c.accessClock
			c.mu.Unlock()
			return entry.catalog
		}
		// Expired and version-mismatched snapshots are invalidated eagerly for
		// this scope. The local fallback above is still available if this
		// request is canceled while its replacement refreshes.
		delete(c.entries, scope)
	}
	// Opportunistically remove expired entries for scopes that are otherwise
	// idle. The hard maxEntries bound remains the protection when no requests
	// arrive to trigger this sweep.
	c.pruneExpiredLocked(now, scope)

	refresh, ok := c.inflight[refreshKey]
	if !ok {
		c.generation++
		refresh = &catalogRefresh{
			done:       make(chan struct{}),
			generation: c.generation,
		}
		c.inflight[refreshKey] = refresh
		c.latest[scope] = refresh.generation
		// Copy the target strings before handing them to a detached goroutine.
		// Enumerators return snapshots today, but keeping the cache independent
		// of caller-owned slice storage avoids an accidental data race later.
		targets = cloneProviderTargets(targets)
		go c.refresh(refreshKey, refresh, targets, bearerToken, cluster)
	}
	// A request can move from version A to B and back to A while both
	// refreshes are in flight. The latest waiter wins, even when it joins an
	// existing refresh, so the requested version is the one retained.
	c.latest[scope] = refresh.generation
	c.mu.Unlock()

	select {
	case <-refresh.done:
		if refresh.snapshot != nil {
			return refresh.snapshot
		}
		if fallback != nil {
			return fallback
		}
		return emptyCatalog(version)
	case <-ctx.Done():
		// A request cancellation belongs to this waiter only. Reusing the old
		// immutable snapshot gives an in-progress request a useful server while
		// the detached refresh continues for other callers. Target lookup during
		// registration still uses the current enumeration, so changed targets
		// cannot route through stale URLs.
		if fallback != nil {
			return fallback
		}
		return emptyCatalog(version)
	}
}

func (c *catalogCache) pruneExpiredLocked(now time.Time, keep catalogScope) {
	for scope, entry := range c.entries {
		if scope == keep {
			continue
		}
		if !now.Before(entry.expiresAt) {
			delete(c.entries, scope)
		}
	}
}

func (c *catalogCache) refresh(key catalogRefreshKey, refresh *catalogRefresh, targets []ProviderTarget, bearerToken, cluster string) {
	var snapshot *providerCatalog
	func() {
		defer func() {
			if recover() != nil {
				snapshot = emptyCatalog(key.version)
			}
		}()
		// Do not retain a caller's request context in the refresh. Each provider
		// operation has the same bounded discovery timeout as the uncached path.
		snapshot = discoverCatalog(context.Background(), targets, bearerToken, cluster)
	}()

	c.mu.Lock()
	refresh.snapshot = snapshot
	if current, ok := c.inflight[key]; ok && current == refresh {
		delete(c.inflight, key)
	}
	if c.latest[key.scope] == refresh.generation {
		delete(c.latest, key.scope)
		c.storeLocked(key.scope, snapshot)
	}
	close(refresh.done)
	c.mu.Unlock()
}

func (c *catalogCache) storeLocked(scope catalogScope, snapshot *providerCatalog) {
	if snapshot == nil || c.maxEntries <= 0 {
		return
	}
	if existing, ok := c.entries[scope]; ok {
		c.accessClock++
		existing.catalog = snapshot
		existing.expiresAt = c.nowTime().Add(c.ttl)
		existing.lastUsed = c.accessClock
		return
	}
	if len(c.entries) >= c.maxEntries {
		c.evictOneLocked()
	}
	c.accessClock++
	c.entries[scope] = &catalogCacheEntry{
		catalog:   snapshot,
		expiresAt: c.nowTime().Add(c.ttl),
		lastUsed:  c.accessClock,
	}
}

func (c *catalogCache) nowTime() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

func (c *catalogCache) evictOneLocked() {
	var oldest catalogScope
	var oldestUse uint64
	first := true
	for scope, entry := range c.entries {
		if first || entry.lastUsed < oldestUse {
			oldest, oldestUse, first = scope, entry.lastUsed, false
		}
	}
	if !first {
		delete(c.entries, oldest)
		// If a refresh is active for an evicted scope, allowing it to finish
		// without storing is preferable to letting the retained-entry bound be
		// exceeded. A later request will coalesce or start a new refresh.
		delete(c.latest, oldest)
	}
}

func emptyCatalog(version string) *providerCatalog {
	return &providerCatalog{version: version, providers: make(map[string]catalogProvider)}
}

// providerTargetVersion is a stable digest over target identity and display
// metadata. Registry.List is map-backed and can enumerate in a different order
// on each call, so sorting first avoids false invalidations from ordering alone.
func providerTargetVersion(targets []ProviderTarget) string {
	sorted := cloneProviderTargets(targets)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		if sorted[i].MCPURL != sorted[j].MCPURL {
			return sorted[i].MCPURL < sorted[j].MCPURL
		}
		return sorted[i].DisplayName < sorted[j].DisplayName
	})

	h := sha256.New()
	for _, target := range sorted {
		writeVersionField(h, target.Name)
		writeVersionField(h, target.DisplayName)
		writeVersionField(h, target.MCPURL)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeVersionField(w byteWriter, value string) {
	_, _ = w.Write([]byte(strconv.Itoa(len(value))))
	_, _ = w.Write([]byte{':'})
	_, _ = w.Write([]byte(value))
	_, _ = w.Write([]byte{'|'})
}

func cloneProviderTargets(targets []ProviderTarget) []ProviderTarget {
	return append([]ProviderTarget(nil), targets...)
}

func targetIdentity(target ProviderTarget) string {
	return target.Name + "\x00" + target.MCPURL
}

func (c *providerCatalog) metadataFor(target ProviderTarget) (catalogProvider, bool) {
	if c == nil {
		return catalogProvider{}, false
	}
	metadata, ok := c.providers[targetIdentity(target)]
	if !ok {
		return catalogProvider{}, false
	}
	metadata.tools = cloneDiscoveredTools(metadata.tools)
	return metadata, true
}

func (c *providerCatalog) instructions(targets []ProviderTarget) string {
	if c == nil {
		return ""
	}
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		metadata, ok := c.providers[targetIdentity(target)]
		if !ok || strings.TrimSpace(metadata.instructions) == "" {
			continue
		}
		label := target.DisplayName
		if label == "" {
			label = target.Name
		}
		parts = append(parts, fmt.Sprintf("## %s\n%s", label, strings.TrimSpace(metadata.instructions)))
	}
	return strings.Join(parts, "\n\n")
}

// discoverCatalog performs the two provider metadata RPCs once for a cache
// miss. Instructions and tool lists remain separate waves to preserve the
// existing timeout and error behavior; callers never repeat either wave while
// an entry is fresh.
func discoverCatalog(ctx context.Context, targets []ProviderTarget, bearerToken, cluster string) *providerCatalog {
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot := emptyCatalog(providerTargetVersion(targets))
	if len(targets) == 0 {
		return snapshot
	}

	metadata := make([]catalogProvider, len(targets))
	cli := newProviderMCPClient(bearerToken, cluster, cluster)

	var wg sync.WaitGroup
	for i, target := range targets {
		if target.MCPURL == "" {
			continue
		}
		wg.Add(1)
		go func(i int, target ProviderTarget) {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					metadata[i].discoveryErr = fmt.Sprintf("discovery panic: %v", recovered)
				}
			}()
			dctx, cancel := context.WithTimeout(ctx, providerDiscoveryTimeout)
			defer cancel()
			metadata[i].instructions = strings.TrimSpace(cli.fetchInstructions(dctx, target.MCPURL))
		}(i, target)
	}
	wg.Wait()

	for i, target := range targets {
		if target.MCPURL == "" {
			continue
		}
		wg.Add(1)
		go func(i int, target ProviderTarget) {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					metadata[i].discoveryErr = fmt.Sprintf("discovery panic: %v", recovered)
				}
			}()
			dctx, cancel := context.WithTimeout(ctx, providerDiscoveryTimeout)
			defer cancel()
			tools, err := cli.listTools(dctx, target.MCPURL)
			if err != nil {
				if errors.Is(err, errNoMCPEndpoint) {
					metadata[i].noMCP = true
					return
				}
				metadata[i].discoveryErr = err.Error()
				return
			}
			metadata[i].tools = cloneDiscoveredTools(tools)
		}(i, target)
	}
	wg.Wait()

	for i, target := range targets {
		if target.MCPURL == "" {
			continue
		}
		snapshot.providers[targetIdentity(target)] = metadata[i]
	}
	return snapshot
}

// registerCatalogTools registers immutable metadata on a new server while
// taking routing identity from the current ProviderTarget values. A changed
// URL therefore cannot be accidentally captured by a stale discovery result.
func registerCatalogTools(srv *mcp.Server, log logr.Logger, catalog *providerCatalog, targets []ProviderTarget, bearerToken, cluster string) {
	log.Info("provider federation: enumerated", "count", len(targets))
	cli := newProviderMCPClient(bearerToken, cluster, cluster)
	for _, target := range targets {
		metadata, ok := catalog.metadataFor(target)
		if !ok || metadata.noMCP {
			continue
		}
		if metadata.discoveryErr != "" {
			log.Info("provider federation: tools/list failed (skipping)", "provider", target.Name, "mcpURL", target.MCPURL, "err", metadata.discoveryErr)
			continue
		}
		log.Info("provider federation: registering tools", "provider", target.Name, "count", len(metadata.tools))
		for _, tool := range metadata.tools {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						log.Info("provider federation: AddTool panic recovered", "provider", target.Name, "tool", tool.Name, "panic", fmt.Sprint(recovered))
					}
				}()
				registerOneProxyTool(srv, cli, target, tool)
			}()
		}
	}
}

func cloneDiscoveredTools(tools []discoveredTool) []discoveredTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]discoveredTool, len(tools))
	for i, tool := range tools {
		out[i] = tool
		out[i].InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
		if tool.Annotations != nil {
			annotations := *tool.Annotations
			annotations.DestructiveHint = cloneBoolPointer(tool.Annotations.DestructiveHint)
			annotations.OpenWorldHint = cloneBoolPointer(tool.Annotations.OpenWorldHint)
			out[i].Annotations = &annotations
		}
	}
	return out
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
