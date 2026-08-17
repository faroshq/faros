# Kuery provider: fleet-wide object query for MCP, search, and impact analysis

Status: **Implemented architecture; phasing retained as design history.**
Author: 2026-06-11
Related: [kuery](https://github.com/faroshq/kuery) (the query engine this wraps),
`providers/infrastructure/` (the standalone-provider pattern this is modeled on),
`pkg/hub/providers/` (CatalogEntry verification),
`providers/edges/internal/tunnel/edges_proxy_builder.go` (edge data path),
`docs/providers.md`, `docs/code-provider-architecture.md`.

## Summary

The **`kuery`** provider gives tenants — and, most importantly, **AI agents via MCP** — a
single query surface over every object across their connected edge clusters. One structured
query against a local SQL-backed index replaces N kubectl round-trips through N reverse
tunnels: "which of my 50 edges run image X / still have the old ConfigMap / are missing CRD
Y" becomes one cheap call instead of a fan-out over slow edge links. On top of that index it
offers relationship traversal for impact analysis ("who consumes this Secret — safe to
rotate?") and a portal UI.

Realistic value ranking, which drives the phasing below:

1. **Fleet-wide search/inventory** — the differentiated piece; nothing in faros answers
   cross-edge questions today without per-edge fan-out.
2. **MCP query tools** — a far better agent surface than the per-edge `kubernetes_*` tools:
   one query instead of dozens of tunneled kubectl calls (latency *and* token cost).
3. **Config-rotation impact** — reliable for declared coupling (ownerRefs, spec references,
   selectors); NOT a full dependency map (network calls, DNS-name references, and dynamic
   operator reads are invisible to it). Cross-edge relations additionally require
   `kuery.io/relates-to` / `kuery.io/group` instrumentation, which manifests won't have by
   default.
4. **Graph visualization** — demo layer on top; single-cluster object graphs are commodity
   (Lens, Headlamp, ArgoCD tree).

It wraps [kuery](https://github.com/faroshq/kuery): a read-only, multi-cluster Kubernetes
query engine that syncs objects from N clusters into SQLite/Postgres via dynamic informers
and exposes a single POST-only API (`kuery.io/v1alpha1 Query`) supporting relationship
traversal that plain list/watch can't do:

- `owners` / `owners+` and `descendants` / `descendants+` (ownerReference chains, transitive
  via recursive CTE)
- `references` — spec-field extraction (Pod→Secret/ConfigMap/PVC/SA, Ingress→Service, …),
  extensible per-CRD via the `kuery.io/refs` annotation
- `selects` / `selected-by` — label-selector containment, both directions
- `events` — involvedObject matches
- `linked` / `linked+` and `grouped` — **cross-cluster** relations via the
  `kuery.io/relates-to` annotation and the `kuery.io/group` label

Kuery is already kcp-aware (APIExport identity disambiguation in `internal/sync/kcp.go`) and
has an Engage/Disengage cluster lifecycle. It has **no UI and no authz** — both are this
provider's job:

- faros supplies the **clusters** (connected `edges.faros.sh` KubernetesClusters,
  reachable through the edges provider's proxy)
  and the **tenant boundary**;
- the provider supplies **tenant-scoped query access** and the **visualization** (inventory,
  object graph, impact view).

## Architecture

```
Browser / MCP client
   │  bearer
   ▼
hub /services/providers/kuery/{api/*, mcp, mcp/sse}
   │  proxy injects X-Faros-Tenant + X-Faros-Cluster + X-Faros-User
   ▼
kuery provider pod
   │
   ├── engagement controller ── watches KubernetesCluster CRs (permission claim, tenant-scoped)
   │     on connect:    Engage("{logicalClusterID}/{edgeName}", cluster.Cluster via edges provider proxy)
   │     on disconnect: Disengage → kuery GC reaps stale objects
   │
   ├── embedded kuery ── informers stream edge objects through reverse tunnels
   │     into one local store (SQLite PVC; Postgres for production)
   │
   └── tenant-scoped query API ── rewrites spec.cluster on every Query to the
         caller's trusted logical-cluster prefix before handing it to the engine
```

### Repository layout

```
providers/kuery/                      module github.com/faroshq/provider-kuery
├── main.go                           init | serve (same pattern as infrastructure provider)
├── engagement/                       watch KubernetesCluster CRs → Engage/Disengage kuery clusters
├── server/                           tenant-scoped REST: /api/query, /api/impact, /api/edges
├── mcpserver/                        kuery_query, kuery_impact MCP tools
├── portal/                           Vue 3 + cytoscape.js graph UI
├── apis/v1alpha1/                    SavedView CRD (tenant-facing, satisfies the APIExport requirement)
├── manifest.yaml                     CatalogEntry
├── deploy/chart/
└── Dockerfile
```

### Data path: edges provider proxy as the cluster endpoint

For every connected `edges.faros.sh/v1alpha1` `KubernetesCluster` in a tenant
workspace, the engagement controller builds a
`rest.Config` with

```
Host = {hub}/services/providers/edges/edgeproxy/clusters/{cluster}/apis/
       edges.faros.sh/v1alpha1/kubernetesclusters/{edgeName}/k8s
```

— the edges provider's current consumer-egress route — wraps it in a
controller-runtime `cluster.Cluster`, and `Engage`s it into
kuery's sync controller under the name `{logicalClusterID}/{edgeName}`. Kuery's discovery +
dynamic informers then stream the edge's objects through the existing reverse tunnel into
the local store. On KubernetesCluster disconnect/delete the controller `Disengage`s; kuery's GC handles
stale-cluster and stale-object cleanup (TTL-based).

### Tenant isolation

Kuery has no authorization of its own, so its API is **never exposed directly**. The
provider backend is the only entry point: it requires both `X-Faros-Tenant` and the
hub-derived `X-Faros-Cluster`, then forcibly rewrites every query's `spec.cluster` filter to
the tenant's logical-cluster prefix (`{logicalClusterID}/…`) before forwarding to the
engine. The APIExport event that drives ingestion carries that same logical-cluster ID, so
the join does not depend on any provider-authored status field. One shared store, isolation
enforced at the single choke point. `FAROS_DEV_ALLOW_TENANT_QUERY` mirrors the infrastructure
provider's dev escape hatch.

Kuery's relationship to the **edge providers** also follows the platform
[provider-isolation rule](./providers.md#provider-isolation-the-cross-provider-boundary):
it consumes `KubernetesCluster` CRs through an exact tenant-scoped
`edges.faros.sh/kubernetesclusters` permission claim and reaches the clusters
through the edges provider's authenticated proxy as the kuery provider ServiceAccount.
The CatalogEntry points the claim identity at the `edges` provider workspace, so
hub readiness rejects a stale or wrong non-empty identity hash.

This replaces the removed `faros.sh/edges` claim. Existing tenant APIBindings
are not silently widened or rewritten: the provider inventory marks them for
access review, and the tenant must explicitly confirm the current
`edges.faros.sh/kubernetesclusters` claim before background engagement resumes.
That confirmation also reconciles any existing Kuery edge-proxy role to the
least-privilege rules described below.

### MCP tools (the primary consumer)

The provider serves `/mcp` + `/mcp/sse` (proxied at `/services/providers/kuery/mcp`) and
registers in the aggregate. Tools:

- `kuery_query` — full QuerySpec passthrough (tenant-scoped): fleet-wide filter by
  kind/labels/conditions/jsonpath, with optional relations and sparse projection.
- `kuery_impact` — convenience wrapper: given one object ref, runs
  `[descendants+, references, selected-by, events, linked+]` and returns the related set.

For an agent, this collapses "check all edges for X" from dozens of tunneled kubectl calls
into one query — the main practical justification for syncing the data at all.

### Impact view

Select any object in the UI → the same `kuery_impact` query → render an interactive graph
with the blast radius highlighted. Because `linked`/`grouped` are cross-cluster, this can
show e.g. "this ConfigMap feeds 4 Pods on edge-a and is grouped with a Deployment on
edge-b". Present it as *declared* coupling, not a complete dependency map (see Summary).
Sparse projection (kuery's field projection compiled to SQL JSON functions) keeps payloads
small enough for interactive use.

### SavedView CRD

`CatalogEntry.spec.apiExport.requiredResources` requires at least one stable
resource declaration. Provider init publishes the **SavedView**
APIResourceSchema and APIExport; the CatalogEntry declares
`kuery.providers.faros.sh/savedviews` as the minimum the hub must observe before
Enable. SavedView is a named, tenant-authored query + layout (root object,
relation set, depth, filters) that the portal can list and re-open. This gives
tenants GitOps-able saved graphs and the APIExport a useful API — the same
"tenant-authored CRDs" stance as the code provider (no CachedResource, no
virtual storage).

## Key decisions

### 1. Embed kuery as a library — don't sidecar it

Kuery's engine/sync/store live under `internal/`, so the provider cannot import them today,
and the kuery binary only accepts `--kubeconfigs` at startup — there is no dynamic cluster
add/remove over the wire. Since both repos are ours, the cleanest path is a small upstream
kuery refactor moving `internal/{engine,sync,store,gc}` to `pkg/`, after which the provider
embeds kuery: single container, programmatic Engage/Disengage, one process, no
credential-passing hop. The alternative (sidecar + a new kuery admin API for cluster
registration) adds a network boundary and an auth surface for no benefit.

During development, `go replace` against the kuery checkout works even with the monorepo
submodule layout.

### 2. Edges-proxy authorization — the Enable-time grant

The edges provider proxy SAR-checks the caller for verb **`proxy`** on resource
**`kubernetesclusters`** in API group **`edges.faros.sh`** in the tenant workspace.
Interactive edges clients forward the *user's* bearer token per request. Kuery
needs a **long-lived credential for background watches** — a user token
is the wrong shape — and permission claims don't help: they grant access via the APIExport
virtual workspace, not direct SAR passes in tenant workspaces.

There are two halves, both small:

**Authn — teach authorize() about provider SA tokens.** authorize() currently runs the
TokenReview *in the target tenant workspace*. kcp SA tokens are logical-cluster-scoped, so
the provider's SA token (home: `root:faros:providers:kuery`) fails authentication there
before RBAC is consulted. The front proxy already handles this pattern
(`pkg/server/proxy/proxy.go`: `parseServiceAccountToken` → route to the token's home
cluster). Extend authorize() the same way:

1. If the token parses as an SA token, TokenReview in the SA's **home cluster** (kcp
   verifies the signature there, so the home cluster is verified, not just claimed).
2. SAR in the tenant workspace with **kcp's native cross-workspace SA identity**
   `system:kcp:serviceaccount:{homeCluster}:{ns}:{name}`. A bare
   `system:serviceaccount:{ns}:{name}` is ambiguous — any tenant could create a same-named
   SA in their own workspace and satisfy the binding. The qualified format is not invented:
   kcp's **GlobalServiceAccount** feature gate (beta, default-on since kube 1.35 in the kcp
   fork) makes kcp's own RBAC resolution alias every SA to exactly this form
   (`EffectiveUsers` in the fork's `pkg/registry/rbac/validation/kcp.go`; proven
   cross-workspace by kcp's e2e `TestAPIResourceSchemaVirtualWorkspaceAuthorization`).
   Emitting the same format means the Enable-time grant binding also authorizes the
   provider SA on kcp-native paths, not just faros's delegated SAR.

**Authz — materialize the grant on Enable.**

- CatalogEntry declares intent next to `permissionClaims` (e.g. `spec.edgeProxyAccess:
  true`) so the portal's Enable dialog shows "this provider gets proxied read access to
  your edges" — same consent model as tenant-scoped claims.
- The existing server-side Enable endpoint (`pkg/hub/restapi/providers_enable.go`, which
  already creates the APIBinding) additionally applies in the tenant workspace:
  - ClusterRole `faros:provider:{name}:edges-proxy` — verb `proxy` on
    `edges.faros.sh/kubernetesclusters`, plus verb `access` on nonResourceURL
    `/`. It deliberately carries no status, Secret, Namespace, delegated-review,
    LinuxServer, or direct-read privileges; the accepted APIExport claim owns
    Kuery's KubernetesCluster watch. The non-resource rule is
    required: kcp's workspaceContentAuthorizer checks `access` before any resource RBAC,
    and a foreign SA is not covered by the tenant workspace's `system:authenticated`
    grants (the SAR also drops its groups). kcp's own cross-workspace SA e2e pairs the
    rules the same way. Verified end-to-end by
    `TestIEdgeProxyGrantAuthorizesProviderSA` in `test/e2e/suites/provider`.
  - ClusterRoleBinding to the qualified subject above
- Disable deletes both. Out-of-band APIBindings (kubectl) don't get the grant in v1; a
  reconciling binding-watcher can come later if needed.
- v1 grants all edges in the workspace; `resourceNames` gives per-edge narrowing later.

Runtime properties: the SAR runs once per proxied request, and kuery's watches are
long-lived streams — one TokenReview+SAR per watch (re)establishment, negligible.
Revocation self-heals: Disable deletes the APIBinding → the provider's claimed Edge watch
dies → engagement controller Disengages → kuery GC purges the tenant's rows; the RBAC
deletion only cuts off already-established proxy streams at reconnect.

Rejected alternative: hub-minted provider tokens checked against a registry allowlist of
enabled tenants (piggybacking on the proxy's static-token bypass). Simpler, but a bespoke
authz path with a powerful bearer secret and no kcp-native audit trail — RBAC keeps "what
can this provider touch" answerable with kubectl.

### 3. Sync scale and defaults

Full-object sync of every edge through the tunnels is the cost center.

- Default to a resource **whitelist** (workloads, config, RBAC, networking — not every CR),
  not kuery's blacklist: edge links are often the scarcest resource in the system, and
  continuous full-object sync is the cost center. Make the list a chart value. (Note:
  excluding events disables the `events` relation.)
- Per-tenant object quotas (engagement controller refuses/flags edges past a budget).
- SQLite on a PVC by default; Postgres as the production chart option (kuery supports both,
  with GIN indexes on Postgres).
- Kuery's safety limits (30s query timeout, 10k row cap, depth cap 20) stay as-is.

## Historical phasing

- **Phase 0 — unblock.** Kuery upstream refactor (`internal/` → `pkg/`) — **done**
  ([kuery#3](https://github.com/faroshq/kuery/pull/3), merged 2026-06-11); hub-side
  `proxy` grant on edges-provider resources for provider ServiceAccounts on tenant Enable — **implemented**
  (design above, key decision 2): SA-aware `authorize()` in
  `pkg/virtual/builder/auth.go`, qualified identities in `pkg/util/identity`,
  `CatalogEntry.spec.edgeProxyAccess`, grant lifecycle in the server-side
  enable/disable endpoints (`pkg/hub/restapi/providers_enable.go`).
- **Phase 1 — skeleton.** Clone the quickstart pattern: binary with `/healthz` + heartbeat,
  provider-owned SavedView schema plus CatalogEntry contract, Helm chart, Makefile targets
  (`build-kuery-provider`, `run-provider-kuery`, `install-provider-kuery`, …). Visible in
  the portal catalog.
- **Phase 2 — data + MCP.** **Implemented** (providers/kuery: `core/`, `engagement/`,
  `queryapi/`, `mcpserver/`). Engagement controller (watch KubernetesClusters via
  permission claim `edges.faros.sh/kubernetesclusters` get/list/watch,
  tenantScoped) + embedded kuery sync +
  tenant-scoped `/api/query` + MCP tools (`kuery_query`, `kuery_impact`) into the
  aggregator. This is the value milestone: agents can query the fleet.
  Implementation notes vs. this design: tenant scoping rides on kuery *cluster
  labels* (`tenant`, a bare identifier). Both upstream kuery follow-ups are done:
  label keys are bound SQL parameters ([kuery#4](https://github.com/faroshq/kuery/pull/4)
  — the query API still replaces caller-supplied cluster labels wholesale, defense
  in depth), and sync supports a whitelist ([kuery#5](https://github.com/faroshq/kuery/pull/5))
  which the chart now defaults to the workloads/config/RBAC/networking set.
  End-to-end suite with a real connected edge is a Phase 3 work item.
- **Phase 3 — UI.** **Implemented** (inventory + impact list): portal micro-frontend
  with the edge/object inventory table (edge selector via the provider's new
  `/api/edges`, kind/namespace/name filters) and the impact drill-down (declared blast
  radius grouped by relation). The cytoscape graph remains a later enhancement — the
  list view covers the daily-use cases; e2e with a real connected agent is the
  remaining Phase 3 work item.
- **Phase 4 — polish.** SavedView reconciliation, Postgres chart option,
  `split-kuery.yaml` workflow + `faroshq/provider-kuery` mirror with deploy key (see
  `docs/provider-publishing.md`).

## Open questions

- Cross-shard behavior: tenant workspaces on a different shard than the provider workspace
  may hit the known CachedResource/discovery issues; SavedView is plain APIBinding-bound
  CRDs so it should be unaffected, but verify during Phase 2.
- Event sync: opt-in per tenant? Events are high-churn and the relation is per-cluster only.
