# Providers — extending faros with provider UIs, virtual workspaces, and APIs

**Status:** Implemented architecture and provider-authoring reference
**Owner:** Faros maintainers
**Last updated:** 2026-08-16

---

## Current-state summary

**Goal in one sentence:** make faros pluggable so third parties can ship
"providers" that bring an `APIExport`, optional UI, optional backend HTTP
service, and optional controllers — all installed via Helm, discovered and
wired up by hub controllers, surfaced in the portal under
`/providers/{name}`, proxied to avoid CORS.

**Decisions already pinned** (don't re-litigate; jump to §"Hub changes" for
the how):

| # | Decision | Rationale |
|---|---|---|
| 0 | API group = **`providers.faros.sh`** (separate from `faros.sh`) | Catalog entries and bindings are platform-owner-only. Excluding them from the `core.faros.sh` merged APIExport keeps them out of tenant workspaces. Tenants interact via portal/hub mediation, not raw CR access |
| 1 | Terminology = **provider** (not "addon") | `root:faros:providers` already exists; first-party faros `APIExport`s already live there |
| 2 | UI embedding = **iframe via hub proxy** | Same-origin → no CORS. Any frontend stack. Module Federation rejected (Vue lock-in + build coupling) |
| 3 | Provider workspace = `root:faros:providers:{name}`, created by the hub's admin `Provider` reconciler before provider init | Onboarding and runtime registration have separate, observable ownership |
| 4 | Distribution = **one Helm chart per provider**, installed in the host cluster; its init container uses the onboarded provider kubeconfig to initialize the provider workspace | The chart never requires a platform-admin kcp credential, while the provider owns its API surface |
| 5 | Registration = **hybrid**: chart renders a `CatalogEntry` that provider init applies; provider runtime heartbeats every 30s (`POST /api/providers/{name}/heartbeat`, TTL 90s) | Declarative contract + runtime liveness |
| 6 | VW = **APIExport-only by default**; `spec.virtualWorkspace.url` is an opt-in escape hatch under `/services/providers/{name}/vw/*` | Most providers won't need a VW; lowers bar |
| 7 | Provider→kcp identity = SA `provider` in the provider's workspace; admin onboarding mints a kubeconfig for provider init and runtime | Keeps the provider inside its own workspace and separates credentials from CatalogEntry registration |
| 8 | Schema delivery = provider-owned `APIResourceSchema` files applied by provider `init`; `CatalogEntry.spec.apiExport.requiredResources` declares the stable minimum the hub must observe before Enable | Prevents bindings to an empty or incomplete export without making the hub own provider schemas |
| 9 | PermissionClaim acceptance = **auto-accept-all** at Enable time, but ONLY for claims marked `tenantScoped: true`. Non-tenant-scoped claims refused unless admin sets `faros.sh/accept-untrusted-claims=true` on the `CatalogEntry` | Simplest safe default; per-claim toggles deferred to v2 |
| 10 | Tenant Enable = **server-mediated creation of a direct kcp `APIBinding` in the tenant workspace**. No `ProviderBinding` CRD. Provider init installs the export bind grant; the hub verifies provider readiness and exact declared claims before creating the binding. | Keeps lifecycle kcp-native while making the mutation boundary deterministic and auditable |

**Deferred (do NOT block phase 1):**

- GraphQL discovery of provider CRs after `APIBinding` lands — **must work
  by end of phase 3**; gateway already does APIExport-based discovery for
  first-party CRs, so expected to "just work", but needs validation. If it
  doesn't, file follow-up; do not gate phase 1–2.
- Cross-provider dependencies — **explicitly out of scope** for v1. A
  provider's controller can error out if its prerequisite APIExport isn't
  bound.
- Heartbeat over kcp leases instead of HTTP — possible v2 simplification.
- Per-permission-claim UI toggles — v2.

The phase plans later in this document are retained as historical design context.
The current contract is the generated `CatalogEntry` API plus the onboarding,
provider-init, readiness, and Enable flows described above and below.

**Portal integration anchors** (referenced throughout):

- Layout + side nav: [portal/src/components/AppLayout.vue](../portal/src/components/AppLayout.vue) — hardcoded `navItems` at lines 48-53 becomes computed
- Bootstrap point: [portal/src/App.vue](../portal/src/App.vue) — auth detect + load providers store before render
- Static routes: [portal/src/router/index.ts](../portal/src/router/index.ts)
- GraphQL queries: [portal/src/graphql/queries/](../portal/src/graphql/queries/) (new `providers.ts`)
- Dev proxy: [portal/vite.config.ts](../portal/vite.config.ts)
- CSP injection point: [pkg/hub/portal.go](../pkg/hub/portal.go) — middleware around the embedded SPA handler

---

## Goal

Make faros a pluggable platform. A *provider* is a self-contained extension
that brings:

1. An **`APIExport`** in kcp that user tenants bind to consume the
   provider — the *one required piece*.
2. A **UI** (micro-frontend, any stack) shown inside the faros portal —
   optional.
3. Optional **controllers** reconciling the provider's resources.
4. Optional **custom HTTP backend** (REST/GraphQL/WebSocket) for the UI
   to talk to, proxied through the hub.
5. Optional **virtual workspace** (advanced) for non-CRD verbs.

A user opens the portal, browses the "Providers" view (catalog), clicks
**Enable** on a provider, and:

- The provider's APIs become available in their tenant workspace via an
  `APIBinding`.
- The provider's UI (if any) appears under `/providers/{name}` in the
  portal — proxied through the hub, so it is same-origin and there are no
  CORS concerns.

## Why "provider" (terminology)

The kcp workspace `root:faros:providers` already exists and is where
faros's own `APIExport`s live (`faros.sh`, `tenancy.faros.sh`,
`core.faros.sh`). See
[config/kcp/workspace-providers.yaml](../config/kcp/workspace-providers.yaml)
and [config/kcp/embed.go](../config/kcp/embed.go).

A third-party provider therefore lives at `root:faros:providers:{name}` —
sibling to the first-party providers, with identical mechanics. No new
top-level workspace, no new vocabulary.

## Non-goals (v1)

- Hot-reloading provider controllers inside the hub process (providers run
  as separate Deployments).
- Cross-provider dependency resolution / version compatibility matrices.
- A public provider marketplace / registry. Distribution is Helm chart +
  `kubectl apply`.
- Per-provider auth policies (single OIDC at the hub).
- Per-permission-claim consent UI (v1: accept-all on Enable; per-claim
  toggles deferred to v2).

---

## Architecture overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                            faros-hub                                  │
│                                                                       │
│  /ui/*                       → embedded SPA (Vue portal)              │
│  /ui/providers/{p}/*         → reverse proxy → catalog.spec.ui.url    │
│  /services/providers/{p}/*   → reverse proxy → catalog.spec.backend.url│
│  /clusters/*, /services/agent-proxy, /services/mcp ... (unchanged)    │
│  /api/providers/{p}/heartbeat (POST, known-provider runtime signal)   │
│                                                                       │
│  Provider controller: watches admin Provider                          │
│    - creates root:faros:providers:{p} + `provider` ServiceAccount     │
│    - mints the provider kubeconfig                                    │
│                                                                       │
│  Catalog controller: watches provider-owned CatalogEntry              │
│    - verifies APIExport identity, resources, schemas, and claims      │
│    - rebuilds proxy routing table; tracks health + heartbeats         │
│                                                                       │
│  Tenants APIBind to provider APIExports DIRECTLY in their workspace   │
│    - Portal calls the hub's server-side Enable endpoint               │
│    - Provider init installs the export-local `bind` grant             │
│    - Hub verifies readiness + exact claims before APIBinding mutation │
└──────────────────────────────────────────────────────────────────────┘
        │ kcp                              │ HTTP (in-cluster Service)
        ▼                                  ▼
┌────────────────────────────┐   ┌────────────────────────────────────┐
│ root:faros:providers:cost  │   │ Provider pod (e.g. cost)           │
│   APIExport cost.faros.sh  │◄──│   - mounts faros-provider-kubeconfig│
│   APIResourceSchema(s)     │   │   - runs controllers against kcp    │
│   SA: provider             │   │   - serves UI on :3000 (optional)   │
└────────────────────────────┘   │   - serves backend HTTP on :8080    │
        ▲                        │   - heartbeats hub every 30s        │
        │ APIBinding (kcp        └────────────────────────────────────┘
        │ serves natively)
┌────────────────────────────┐
│ root:faros:tenants:alice   │
│   (user workspace — sees   │
│    cost CRs natively)      │
└────────────────────────────┘
```

Single origin from the browser's perspective: every request goes to
`faros.example.com`. The hub fans out to providers internally.

**Key clarification on traffic flow:** provider CRs are served by kcp via
the normal `/clusters/...` path on the hub — the same flow as faros's own
CRDs today. The `/services/providers/{name}` proxy is *only* for the
provider's own custom HTTP backend (REST/GraphQL/WS), not for CR traffic.

---

## Provider isolation (the cross-provider boundary)

**A provider's backend layer is private. Another provider may never reach
into it. All cross-provider interaction goes through the owning provider's
*published* API surface.**

This is the single rule that keeps the provider plane composable. State it
as three parts:

1. **Each provider owns a backend layer, and that layer is private to it.**
   The "backend layer" is everything behind the provider's published API:
   its controllers, its runtime/target clusters and the credentials to
   them, its databases and object stores, its internal Services, its kro
   RGDs / Terraform / cloud SDK calls — every implementation detail of
   *how* it materializes and operates what it exports. No other component
   holds a handle to any of it.

2. **A provider must NOT touch another provider's backend layer.**
   Concretely, a provider must never:
   - hold a second credential (kubeconfig, DB DSN, API key) to another
     provider's runtime cluster, database, or internal service;
   - call another provider's internal Service / pod / REST endpoint
     directly, or share its datastore;
   - encode another provider's backend topology (cluster URLs, namespaces,
     service names) in its own config.

3. **Cross-provider interaction goes only through the other provider's
   published interface, as the tenant/caller:**
   - **kcp `APIExport` resources** — the other provider's CRDs, consumed by
     binding to its `APIExport` (an `APIBinding` in the tenant workspace)
     and reading/writing its CRs over the normal `/clusters/...` path.
     Control-plane state (spec/status) flows this way.
   - **Virtual-workspace/data-plane subresources** on those resources — for
     example `{template-resource}/{name}/log`,
     `{template-resource}/{name}/proxy/{path}`, or a component-scoped
     `{template-resource}/{name}/components/{component}/sync` — for streams,
     proxies, and other verbs that aren't plain CRUD.
     The owning provider serves them against *its* backend; the caller
     never sees that backend.

   Both are invoked **as the tenant user** (the caller's forwarded bearer
   token, scoped to the workspace — see contract 2 in
   [`provider-connectivity-contract.md`](./provider-connectivity-contract.md))
   and **routed by binding, never by a hardcoded backend URL**. The calling
   provider resolves *which* provider backs a workspace from the
   binding/APIExport, not from its own configuration.

**Why the rule pays off:**

- **Substitutability / BYO.** Because the caller addresses a *bound
  resource* and not a backend URL, the workspace can be backed by a
  *different instance* of the owning provider — its own runtime cluster,
  its own APIExport — with **zero change** in the caller. A provider that
  reached a backend directly would be welded to one deployment.
- **Single owner per backend.** Exactly one provider holds the credential
  and the operational responsibility for a given runtime/datastore. No
  duplicated clients, no two-writers-one-cluster ambiguity.
- **Contained blast radius.** A provider's compromise or outage is bounded
  by its published claims, not by who else happens to hold a key into its
  cluster.

**Reference implementation.** App Studio used to hold
`APP_STUDIO_RUNTIME_KUBECONFIG` — a direct credential into the
infrastructure provider's runtime cluster. That is exactly the violation
this rule forbids. The current implementation selects an infrastructure
Template, creates its development instance through the tenant API, and calls
the infrastructure provider's published data-plane subresources as the tenant
user (for example, `/dataplane/clusters/{workspace}/{resource}/{name}/` plus
`components/{component}/sync`). App Studio carries no runtime credential and
does not know the provider's backend topology. See the current boundary in
[`app-studio-sandbox-runtime.md`](./app-studio-sandbox-runtime.md) and the
retained historical proposal in
[`app-studio-runtime-decoupling.md`](./app-studio-runtime-decoupling.md).

> Cross-provider *dependency resolution* (ordering, version-compatibility
> matrices) remains out of scope for v1 (see Non-goals). The isolation rule
> is about
> the *access boundary*, not orchestration: a provider may consume another
> provider's published API, but it owns the failure handling when a
> prerequisite binding isn't present.

### Provider Actions

Provider Actions extends the isolation boundary with catalog-declared,
versioned capabilities served on the provider's **embedded virtual
workspace** — the same resource-addressed data-plane shape as the
infrastructure `dataplane/` verbs. App Studio stores a non-owning
`providerReference`, grants an exact provider action, resource reference, and
schema digest, and **materializes the grant as kcp RBAC** (`create` on the
action's virtual subresource, e.g. `tables/query_table`, name-scoped) on the
workload identity. Invocations ride the ordinary hub backend proxy at
`/services/providers/{name}/actions/clusters/{clusterID}/{resource}/{rname}/{action}/{version}`;
the owning provider authorizes them as the caller with two SSAR gates
(visibility on the resource, verb on the subresource) — uniform for humans
and workloads, mirroring how data-plane exec is authorized. There is no
dedicated hub action router; the proxy reserves only the hub-internal
`/workload-identities/*` prefix. The current shipped action is Databricks
`query_table/v1`; the generic catalog also carries schemas, execution mode,
read-only/risk/idempotency policy, limits, consent, and deprecation metadata.
See the [Provider Actions contract and verification
guide](./provider-actions.md) for the workload exchange, SDK, provider
boundary, and verification commands, and
[cross-provider-simplification.md](./cross-provider-simplification.md) for
how this pattern generalizes (decision #6's `spec.virtualWorkspace.url` dial
target is retired in favor of reserved prefixes on `spec.backend.url`).

### Provider assistant skills

`CatalogEntry.spec.assistantSkills` is an inline, versioned package contract
for read-only App Studio guidance. Each package carries `packageName`,
`version`, a complete raw `SKILL.md`, optional package-relative resources, and
a canonical `sha256:` digest. The digest covers the package identity, version,
document bytes, and resources in deterministic path order; it is provenance
and integrity, not an authority grant. The authenticated hub
`/api/providers` catalog distributes validated inline bytes. It never follows
a provider URL and does not grant credentials, tools, models, permissions, or
runtime authority.

Publication is bounded: at most 64 packages per `CatalogEntry`, each document
is at most 32 KiB, each supporting resource at most 64 KiB, and all documents
and resources for one provider entry are at most 512 KiB. Invalid packages are
isolated with bounded sanitized warnings where possible. These limits apply to
the published artifact; they do not make a skill trusted instructions.

App Studio projects expose valid packages as read-only, provider-qualified
system skills (`providers/<provider>/<packageName>`). They use the existing
catalog progressive-disclosure flow: metadata discovery, explicit
`load_skill`, bounded `read_skill_resource`, activation policy, and immutable
catalog/digest snapshots for a turn. Distribution is not provider or action
enablement: a published provider package follows the system-skill default of
enabled, and each project may disable or re-enable the qualified skill. A
transient provider heartbeat/readiness change does not revoke declared
guidance. If the request has no bearer or the provider skill catalog is
temporarily unavailable, App Studio preserves bundled and project skills and
emits only a bounded sanitized warning where applicable. Provider Actions and
their grants remain the authoritative, fail-closed path for data access or
other effects; a skill can never create or widen that authority.

---

## CRDs

Two new CRDs, both in the faros API group, both first-party (added to the
existing `faros.sh` `APIExport`).

### `CatalogEntry` (cluster-scoped, in `root:faros:providers`)

Installed by an administrator via the provider's Helm chart, which targets
the host Kubernetes cluster API. The hub's catalog controller projects it
into kcp.

```yaml
apiVersion: providers.faros.sh/v1alpha1
kind: CatalogEntry
metadata:
  name: cost-insights
spec:
  displayName: "Cost Insights"
  description: "Per-edge cost attribution and forecasting."
  vendor: "Acme Cloud"
  version: "1.2.0"
  iconURL: "/ui/providers/cost-insights/icon.svg"  # served via UI proxy

  # OPTIONAL: micro-frontend. Omit if provider has no UI.
  ui:
    url: "http://cost-insights-ui.cost-insights.svc.cluster.local"
    indexPath: "/"

  # OPTIONAL: custom HTTP backend (NOT for CR traffic — CRs go via kcp).
  # Omit if provider only exposes CRs.
  backend:
    url: "http://cost-insights.cost-insights.svc.cluster.local:8080"
    healthPath: "/healthz"

  # OPTIONAL: opt-in to serving a kcp virtual workspace for non-CRD verbs.
  # Omit for v1 — only needed if provider needs custom resource verbs.
  virtualWorkspace:
    url: "http://cost-insights.cost-insights.svc.cluster.local:6443"

  # REQUIRED for a tenant-enableable API provider: reference to the APIExport
  # that provider init owns. Runtime-only integrations may omit it; the hub
  # refuses tenant Enable when no export is declared or until it is complete.
  apiExport:
    name: "cost.faros.sh"
    # Minimum static API surface that must already be published before the
    # provider can be Enabled. Each entry must exist in APIExport.spec.resources
    # and reference an existing APIResourceSchema. Dynamic extra resources are
    # allowed, but every exported resource must reference an existing schema
    # whose group/plural match the export entry. Keep this list synchronized
    # with the provider's init output.
    requiredResources:
      - group: cost.faros.sh
        name: greetings
    # Exact mirror of the APIExport's permissionClaims (kcp-enforced). The hub
    # verifies group/resource and order-insensitive verb-set parity before
    # offering Enable, then presents this declaration in the consent dialog.
    # Any identity-bearing claim must declare identitySource. The hub resolves
    # that source's current APIExport identity and requires an exact match with
    # the provider-owned APIExport claim. Identity-less built-in Kubernetes API
    # claims must omit identitySource.
    permissionClaims:
      - resource: configmaps
        verbs: [get, list, watch]
        # Tenant-scoped flag tells the binding controller this is safe to
        # auto-accept. Out-of-tenant claims are refused.
        tenantScoped: true

status:
  # Filled by catalog controller
  workspace: "root:faros:providers:cost-insights"
  endpoints:
    ui: "http://cost-insights-ui.cost-insights.svc.cluster.local"
    backend: "http://cost-insights.cost-insights.svc.cluster.local:8080"

  # Filled by heartbeat. Provider.Ready() requires at least an APIExport or a
  # runtime endpoint; every declared APIExport must pass APIExportReady, every
  # declared backend must pass health, and heartbeat must remain fresh after
  # the provider has sent its first one.
  lastHeartbeat: "2026-05-22T10:15:00Z"
  reportedVersion: "1.2.0"

  conditions:
    - type: APIExportReady
    - type: BackendHealthy   # only present if .spec.backend set
    - type: Ready
```

### Tenant Enable = direct kcp `APIBinding` (no second CRD)

We deliberately do NOT ship a `ProviderBinding` CRD. Tenants enable a
provider by creating a vanilla kcp `APIBinding` in their own workspace,
pointing at the provider's `APIExport`. This is the kcp-native pattern;
adding a second CRD would only re-wrap what `APIBinding` already does.

```yaml
# Created in the tenant's workspace (e.g. root:faros:tenants:alice)
# by the portal, calling kcp as the user when they click Enable.
apiVersion: apis.kcp.io/v1alpha2
kind: APIBinding
metadata:
  name: cost-insights
spec:
  reference:
    export:
      path: "root:faros:providers:cost-insights"
      name: "cost.faros.sh"
  permissionClaims:
    - resource: configmaps
      verbs: [get, list, watch]
      state: Accepted
```

**Why this works safely:**

- **Tenants need `bind` verb on the provider's `APIExport`.** Provider init
  installs the export-local bind grant; without it, APIBinding creation fails
  with 403.
- **Permission claims cross two gates.** Catalog readiness requires the
  CatalogEntry and APIExport to carry the exact same group/resource/verb set,
  with identity hashes for every identity-bearing custom API claim. Enable
  auto-accepts only the declared `tenantScoped` claims unless an administrator
  explicitly permits an untrusted claim.
- **Claim upgrades preserve tenant consent and bound resources.** When an
  APIExport adds or replaces a claim, kcp leaves the new tuple unapplied on
  existing APIBindings. The enabled-provider inventory compares each binding's
  group/resource/verbs set with the current CatalogEntry (and honors kcp's
  `PermissionClaimsValid` condition for identity mismatches), and the portal
  shows **Review access** on drift. Confirming that dialog updates
  `spec.permissionClaims` on the existing APIBinding in place; it never
  deletes/recreates the binding, and it preserves prior accept/reject decisions
  for unchanged claims. This is the required rollout path for new provider
  permissions.
- **Audit and inventory** ("who enabled X?") = list `APIBindings` across
  tenant workspaces filtered by `reference.export.path`. Acceptable at
  current scale; revisit if it ever isn't.
- **Uninstall** deletes the admin `Provider`; its finalizer removes the provider
  workspace, including CatalogEntry, APIExport, and schemas. Existing tenant
  APIBindings are not silently rewritten by the catalog controller; kcp marks
  their now-broken export reference NotReady until the tenant disables the
  provider or an explicit migration owns that cleanup.
- **Disable** = tenant deletes their own `APIBinding`. No special API.

---

## Hub changes

### 1. Provider onboarding and catalog observation (`pkg/hub/providers/`)

Two reconcilers intentionally split ownership:

1. **Admin `Provider` reconcile** ensures
   `root:faros:providers:{name}`, the workspace-local `provider`
   ServiceAccount, and the kubeconfig Secret used by the chart. It does not
   create provider APIs.
2. **Provider `init`** applies the provider-owned `APIResourceSchema`s,
   `APIExport`, `APIExportEndpointSlice`, bind grant, and `CatalogEntry` using
   that kubeconfig. The CatalogEntry must live in
   `root:faros:providers:<name>`; the catalog reconciler rejects same-named
   entries observed in any other consumer workspace.
3. **Catalog reconcile** parses runtime endpoints and observes the declared
   APIExport. Before setting `APIExportReady=True`, it requires a valid export
   identity, every `requiredResources` entry, a present and matching schema for
   every actual exported resource, and exact permission-claim parity, including
   every identity-bearing claim's trusted identity hash. It then upserts the
   routing registry and records backend/heartbeat health.

This split is deliberate: provider API contents remain provider-owned, while
tenant Enable stays server-gated on independently observed state.

### 2. In-memory routing registry (`pkg/hub/providers/`)

```go
type Registry struct {
    mu     sync.RWMutex
    byName map[string]*Provider
}

type Provider struct {
    Name       string
    UIURL      *url.URL  // may be nil
    BackendURL *url.URL  // may be nil
    Ready      bool
    Version    string
}

func (r *Registry) Get(name string) (*Provider, bool)
func (r *Registry) List() []*Provider
func (r *Registry) Upsert(p *Provider)
func (r *Registry) Delete(name string)
```

Pure in-memory; rebuilt on hub restart from the `CatalogEntry`
list. No external store.

### 3. Heartbeat endpoint

```
POST /api/providers/{name}/heartbeat
Authorization: Bearer <provider-SA-token>
Content-Type: application/json

{ "version": "1.2.0", "buildTime": "...", "status": "healthy" }
```

- Accepts heartbeats only for a provider name already in the registry. The
  current handler does not perform provider-SA identity validation itself;
  deployments must protect the endpoint at the surrounding ingress/auth layer.
- Updates `CatalogEntry.status.lastHeartbeat` and
  `reportedVersion`.
- TTL: 90 seconds. The catalog controller periodically reconciles every
  provider that has heartbeated and persists `Ready=false` after expiry,
  including UI-only providers without a backend health probe.
- Cheap: providers heartbeat every 30s; tiny payload.

### 4. Generic provider proxy

Provider availability and proxy routability are deliberately distinct. An
APIExport-only provider is `Ready` and can be Enabled without declaring any HTTP
endpoint. Proxy routes additionally require `RuntimeReady`: a valid endpoint
for that route plus the same backend-health and heartbeat gates. An API-only
provider therefore never becomes an accidental proxy target.

Two route prefixes registered in [pkg/hub/server.go](../pkg/hub/server.go):

```go
// New paths in pkg/api/url/paths.go
const (
    PathPrefixProvidersUI      = "/ui/providers"
    PathPrefixProvidersBackend = "/services/providers"
)

router.PathPrefix(apiurl.PathPrefixProvidersUI + "/").Handler(
    providers.NewUIProxy(registry, logger))
router.PathPrefix(apiurl.PathPrefixProvidersBackend + "/").Handler(
    providers.NewBackendProxy(registry, authMiddleware, logger))
```

Proxy behavior:

- Parse `{name}` from path: `/ui/providers/cost-insights/foo` → name=`cost-insights`, rest=`/foo`.
- Look up in registry; **404** if unknown, **503** if not Ready.
- Backend proxy: requires standard faros auth middleware; forwards the
  user's `Authorization` header and adds `X-Faros-User`, `X-Faros-Tenant`.
- UI proxy: no auth requirement on static assets; injects
  `X-Faros-Base-Path: /ui/providers/{name}` so the provider can rewrite
  absolute links.
- Standard `httputil.ReverseProxy` with header sanitization.

Note: if `spec.virtualWorkspace.url` is set, the backend proxy also
recognizes a `/services/providers/{name}/vw/*` sub-path and routes it to
the VW URL instead. This is the opt-in advanced path.

### 5. Provider-init RBAC + server-side Enable plumbing

Provider init creates the bind grant next to its APIExport. The hub's
server-side Enable endpoint then:

1. **Uses the provider-owned `bind` grant.** Provider init creates / updates a
   `ClusterRole` named
   `faros:providers:bind:{name}` in the provider's workspace with rules
   `[apiGroups: ["apis.kcp.io"], resources: ["apiexports"], verbs: ["bind"], resourceNames: ["{name}"]]`,
   plus its binding.
2. **Re-checks provider readiness before mutation.** A missing, incomplete, or
   drifted APIExport returns a conflict before any tenant APIBinding is created.
3. **Builds accepted claims from the verified CatalogEntry declaration.** Only
   claims marked `tenantScoped` are auto-accepted unless the administrator has
   explicitly allowed an untrusted claim. The APIExport must carry the exact
   same group/resource/verb set.
4. **Creates the tenant APIBinding** and, when requested, the separate
   edge-proxy access grant.

There is no separate "binding reconciler" — the tenant's `APIBinding`
itself is the reconciled state, and kcp handles its lifecycle.

### 6. Bootstrap

The kcp bootstrap in [pkg/hub/bootstrap](../pkg/hub/bootstrap) already
creates `root:faros:providers`. We add:

- `APIResourceSchema` and `APIExport` for `CatalogEntry` in the
  `providers.faros.sh` group (admin-only — bound only in
  `root:faros:providers`, never in tenant workspaces, hence excluded from
  the merged `core.faros.sh` APIExport).
- New embed paths for these schemas in
  [config/kcp/embed.go](../config/kcp/embed.go).

No new workspaces in bootstrap; provider sub-workspaces are created
lazily on `CatalogEntry` admission.

---

## Portal changes

The portal is Vue 3 + Pinia + urql + Vite, with a single shared layout
([portal/src/components/AppLayout.vue](../portal/src/components/AppLayout.vue))
that every page wraps. Routes are static today
([portal/src/router/index.ts](../portal/src/router/index.ts)) and the side
nav reads a hardcoded `navItems` const at
[portal/src/components/AppLayout.vue:48-53](../portal/src/components/AppLayout.vue#L48-L53).
Both become provider-aware.

### Files to create

| Path | Purpose |
|---|---|
| `portal/src/stores/providers.ts` | Pinia store: catalog list, current user's bindings, derived nav items, route registration |
| `portal/src/router/providers.ts` | `registerProviderRoutes(bindings)` — idempotent `router.addRoute()` calls |
| `portal/src/graphql/queries/providers.ts` | `LIST_PROVIDER_CATALOG_ENTRIES`, `LIST_PROVIDER_BINDINGS`, plus result types |
| `portal/src/pages/ProvidersPage.vue` | The `/providers` catalog view (grid of cards, Enable/Disable) |
| `portal/src/pages/ProviderFrame.vue` | Per-provider iframe host; handles postMessage handshake, loading state, theme propagation |
| `portal/src/components/ProviderEnableDialog.vue` | Modal listing `permissionClaims` (read from `CatalogEntry.spec.apiExport.permissionClaims` via `/api/providers`); on confirm, the portal POSTs an `APIBinding` directly to kcp in the user's workspace with the claims marked `Accepted` |
| `portal/sdk/index.ts` (new package `@faros/provider-sdk`) | `useFaros()` composable for providers' UIs: token, user, tenant, theme, `onNavigate` |
| `portal/sdk/package.json`, `tsconfig.json`, `README.md` | SDK packaging — publish to npm or include as workspace |

### Files to edit

| Path | Edit |
|---|---|
| [portal/src/App.vue](../portal/src/App.vue) | After `auth.detectAuthMode()`, if authenticated, `await providersStore.load()` before rendering `<router-view />`. Show loading spinner during. This guarantees dynamic routes exist *before* Vue tries to match a deep link like `/providers/cost/foo`. |
| [portal/src/router/index.ts](../portal/src/router/index.ts) | Add static catalog route `{ path: '/providers', name: 'providers', component: () => import('@/pages/ProvidersPage.vue') }` **before** the `:pathMatch(.*)*` not-found route at line 62. Provider sub-routes added dynamically by the store. |
| [portal/src/components/AppLayout.vue](../portal/src/components/AppLayout.vue) | Replace the static `navItems` array (lines 48-53) with a `computed` that merges static items with `providersStore.enabledNavItems`. Add a static "Providers" entry (catalog browser) before the dynamic block. Render dynamic items with `<img :src="iconURL">` instead of `<component :is="icon">` so providers can use their own icons. |
| [portal/src/graphql/mutations.ts](../portal/src/graphql/mutations.ts) | Add `CREATE_PROVIDER_BINDING`, `DELETE_PROVIDER_BINDING` |
| [portal/vite.config.ts](../portal/vite.config.ts) | Add proxy entries so dev-mode shell on `:3000` forwards `/services` and `/ui/providers/*` to the hub at `:9443`. The `/ui/providers/*` rule must take precedence over Vite's own `/ui/` static serving (use `bypass: () => undefined` only for that prefix). |
| [pkg/hub/portal.go](../pkg/hub/portal.go) | Add `Content-Security-Policy` header to portal HTML responses: `default-src 'self'; frame-src 'self' <configured platform frame sources>; img-src 'self' data:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'`. `frame-src 'self'` permits provider iframes (proxied = same-origin); configured platform frame sources permit owned surfaces such as App Studio preview hosts. |

### Reactive providers store

The current implementation lives at
[portal/src/stores/providers.ts](../portal/src/stores/providers.ts). It
holds a single `items: ProviderDTO[]` array loaded from the hub's
admin-mediated `/api/providers`. Today every authenticated user sees every
installed provider in the nav.

**Phase 3 change** (when direct-APIBinding Enable lands): split into two
sources:

- `catalog: ProviderDTO[]` — what's installed on the platform (hub
  `/api/providers`, unchanged).
- `enabled: APIBinding[]` — what the *current user* has bound, queried
  via kcp's APIBinding list in the user's workspace, filtered by
  `reference.export.path` starting with `root:faros:providers:`.

`enabledNavItems` becomes `enabled.filter(ready).map(...)`. The catalog
page shows union with status badges (Available / Enabled / Pending).

### Route registration (sketch)

```ts
// portal/src/router/providers.ts
import { router } from './index'

const registered = new Set<string>()

export function registerProviderRoutes(names: string[]) {
  for (const name of names) {
    if (registered.has(name)) continue
    router.addRoute({
      path: `/providers/${name}/:rest(.*)*`,
      name: `provider-${name}`,
      component: () => import('@/pages/ProviderFrame.vue'),
      props: route => ({
        providerName: name,
        subPath: route.params.rest ?? '',
      }),
    })
    registered.add(name)
  }
}
```

### `ProviderFrame.vue` (concrete)

```vue
<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import AppLayout from '@/components/AppLayout.vue'
import { useProvidersStore } from '@/stores/providers'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { useRouter } from 'vue-router'

const props = defineProps<{ providerName: string; subPath: string }>()
const providers = useProvidersStore()
const auth = useAuthStore()
const theme = useThemeStore()
const router = useRouter()
const iframe = ref<HTMLIFrameElement | null>(null)

const entry = computed(() =>
  providers.catalog.find(c => c.metadata.name === props.providerName)
)

// Cache-bust on version change so a provider chart upgrade doesn't show
// stale assets.
const src = computed(() => {
  const v = entry.value?.status?.reportedVersion ?? '0'
  return `/ui/providers/${props.providerName}/${props.subPath}?v=${v}`
})

// postMessage handshake. Only respond to messages whose source is OUR
// iframe; only post back to that iframe's contentWindow.
function onMessage(e: MessageEvent) {
  if (e.source !== iframe.value?.contentWindow) return
  if (e.data?.type === 'faros.ready') {
    iframe.value?.contentWindow?.postMessage({
      type: 'faros.context',
      token: auth.token,
      user: auth.user,
      tenant: auth.clusterName,
      theme: theme.mode,
      basePath: `/ui/providers/${props.providerName}`,
    }, window.location.origin)
  } else if (e.data?.type === 'faros.navigate') {
    // Provider wants to update browser URL (e.g. /providers/cost/foo)
    router.push(`/providers/${props.providerName}/${e.data.path}`)
  }
}

onMounted(() => window.addEventListener('message', onMessage))
onUnmounted(() => window.removeEventListener('message', onMessage))
</script>

<template>
  <AppLayout>
    <div v-if="!entry?.status?.ready" class="loading-state">
      Provider starting…
    </div>
    <iframe
      v-else
      ref="iframe"
      :src="src"
      class="w-full h-full border-0"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
      :title="entry.spec.displayName"
    />
  </AppLayout>
</template>
```

### Provider SDK (`@faros/provider-sdk`) — concrete

```ts
// portal/sdk/index.ts
import { ref, onMounted, onUnmounted } from 'vue'

export interface FarosContext {
  token: string
  user: { email: string; userId: string }
  tenant: string         // logical cluster name
  theme: 'light' | 'dark' | 'system'
  basePath: string       // e.g. /ui/providers/cost
}

export function useFaros() {
  const ctx = ref<FarosContext | null>(null)

  function onMessage(e: MessageEvent) {
    if (e.source !== window.parent) return
    if (e.data?.type === 'faros.context') ctx.value = e.data
  }

  onMounted(() => {
    window.addEventListener('message', onMessage)
    // Tell the shell we're ready to receive context
    window.parent.postMessage({ type: 'faros.ready' }, '*')
  })
  onUnmounted(() => window.removeEventListener('message', onMessage))

  function navigate(path: string) {
    window.parent.postMessage({ type: 'faros.navigate', path }, '*')
  }

  return { ctx, navigate }
}
```

Optional — a provider's UI works without the SDK; it just won't share
state (no token, no theme, no synced URL).

### Deep-link behavior

User pastes `https://faros.example.com/ui/#/providers/cost/forecasts`
into a fresh browser. Sequence:

1. Vue boots, `App.vue` `onMounted` calls `auth.detectAuthMode()`.
2. If not authenticated → `router.beforeEach` redirects to `/login` (no
   change from today).
3. If authenticated → `await providersStore.load()`. This populates the
   store AND calls `registerProviderRoutes(...)` *before* the first
   `<router-view />` render.
4. Vue Router resolves `/providers/cost/forecasts` → `ProviderFrame.vue`
   with `providerName=cost`, `subPath=forecasts`. Iframe loads
   `/ui/providers/cost/forecasts`.

The key is awaiting the store load in `App.vue` before rendering. Without
that, the not-found route swallows the deep link.

### Dev-mode wiring

Vite dev server serves `/ui/*` as Vue assets and proxies `/apis`,
`/healthz` to the hub today. We add:

```ts
// vite.config.ts (excerpt)
server: {
  port: 3000,
  proxy: {
    '/apis':     { target: 'https://localhost:9443', changeOrigin: true, secure: false, ws: true },
    '/healthz':  { target: 'https://localhost:9443', changeOrigin: true, secure: false },
    // NEW:
    '/services': { target: 'https://localhost:9443', changeOrigin: true, secure: false, ws: true },
    // /ui/providers/{name}/* MUST go to hub, NOT vite's static dir.
    // Vite proxy matches first; rewrite-strip not needed because hub
    // expects the full path.
    '/ui/providers': { target: 'https://localhost:9443', changeOrigin: true, secure: false },
  },
},
```

In production the hub already proxies these routes directly — no Vite in
the picture.

### Providers catalog page

`/providers` (`ProvidersPage.vue`) — grid of cards from
`providersStore.catalog`. Each card shows:

- Icon (`<img>` from `entry.spec.iconURL` — proxied via hub).
- Display name, vendor, version, description.
- Status badge: Available / Enabled (= an `APIBinding` exists in your
  workspace) / Pending (provider not Ready).
- Primary button:
  - **Enable** when not bound → opens `ProviderEnableDialog.vue` listing
    `permissionClaims`; on confirm, the portal POSTs the `APIBinding`
    directly to kcp in the user's workspace.
  - **Disable** when bound → confirm + delete the user's `APIBinding`.
  - **Re-accept** when the catalog's `permissionClaims` no longer match
    what the user's `APIBinding` has accepted → re-shows the dialog with
    the new claims highlighted; user confirm = patch the `APIBinding`.

`ProviderEnableDialog.vue` lists `permissionClaims` from the
`CatalogEntry`, distinguishes `tenantScoped` vs non
(non-tenant-scoped claims show a red warning explaining the admin
override needed). Confirm → calls the mutation, sets
`acceptedClaimsHash` to a SHA256 of the sorted claims list.

---

## Provider author experience

A provider ships as one Helm chart installed in the host cluster. Its init
container reaches only the already-onboarded provider workspace through the
provider kubeconfig; it does not receive a platform-admin kcp credential.

```
provider-cost-insights/
├── Chart.yaml
├── values.yaml
└── templates/
    ├── namespace.yaml
    ├── serviceaccount.yaml
    ├── deployment.yaml          # init bootstrap + provider runtime
    ├── service.yaml             # ClusterIP services for UI and backend
    └── catalogentry.yaml        # ConfigMap consumed by provider init
└── files/schemas/               # provider-owned APIResourceSchemas
```

`helm install cost-insights ./chart` →

1. A platform admin applies an admin `Provider`. The hub creates
   `root:faros:providers:cost-insights`, its `provider` ServiceAccount, and the
   provider kubeconfig Secret.
2. The chart's init container waits for that Secret, then idempotently applies
   the provider's schemas, APIExport, endpoint slice, bind grant, and rendered
   CatalogEntry into the provider workspace.
3. The serve container starts its controllers and HTTP surfaces, then
   heartbeats only when its own required runtime is usable.
4. The catalog controller observes the APIExport contract and runtime health.
   `APIExportReady=True` requires the declared minimum, valid schema references,
   exact claims, and a valid export identity; `Ready=True` additionally reflects
   endpoint/backend and heartbeat gates.
5. Users see it in `/providers`; Enable performs a final readiness check before
   creating the tenant APIBinding.

### Kubeconfig sources for provider init

The normal flow is admin-onboarded: the hub owns workspace/identity creation and
provider init owns the workspace contents. A fully standalone installation may
instead supply an existing workspace-admin kubeconfig and take responsibility
for creating the workspace and credential out of band. The infrastructure
provider exposes this as `bootstrap.kubeconfigSource=supplied`.

The key simplification is **one provider-workspace kubeconfig used by init and,
when needed, serve**. Infrastructure exposes two sources through
`bootstrap.kubeconfigSource`:

**`hubMinted` (default)** — clean division of responsibility:

```
Platform admin                         Provider owner
─────────────                          ─────────────
applies admin Provider                 helm install … --set bootstrap.enabled=true
   │                                       │
   ▼  hub Provider controller              ▼  pod scheduled, waits for the Secret
creates root:faros:providers:<name>    init container (`<provider> init`)
mints provider-workspace kubeconfig       uses the supplied Secret to install
   │                                      schemas / APIExport / CatalogEntry
   └── admin/operator supplies Secret     ▼  serve container starts
```

The minted `provider` SA is **cluster-admin within the provider
workspace** (`EnsureProviderSA`), so it's powerful enough to do init's
installs and run serve. The chart's init volume is **not** `optional`, so the
pod blocks until its operator or deployment workflow supplies the Secret.

**`supplied`** — fully standalone, no hub: you provide a
workspace-admin kubeconfig (`bootstrap.kcpKubeconfig` /
`kcpKubeconfigSecretRef`) and own the prerequisites (workspace exists,
kubeconfig targets it).

Trade-offs:

- **hubMinted needs no separate kcp admin credential** — the platform already
  minted a workspace-scoped identity.
- **Privilege**: serve runs with cluster-admin-in-workspace rather than a
  narrower runtime identity unless the provider mints one during init.

All models converge on the same bootstrap contract: provider init receives a
kubeconfig for its own workspace and owns the API objects it publishes there.

### Minimal provider backend contract

A provider's backend (if it declares one) MUST:

- Heartbeat: `POST /api/providers/{name}/heartbeat` to the hub every 30s
  (helper in the SDK).
- `GET /healthz` → 200 while the HTTP process is live.
- A provider whose API depends on a controller SHOULD expose `GET /readyz` and
  return 200 only while that controller is usable. Its Deployment readiness
  probe and CatalogEntry `backend.healthPath` must both use `/readyz`; keep
  `/healthz` for liveness.

A provider's controller (the kcp-talking part) MUST:

- Wait for `faros-provider-kubeconfig` Secret to appear before starting.
- Run provider init before serving: publish every stable API in
  `requiredResources`, reference a valid matching APIResourceSchema from every
  actual APIExport resource, and stamp permission claims exactly as declared in
  both CatalogEntry copies. First-party claims require the target APIExport's
  identity hash.
- Use the kubeconfig's `provider` SA identity. The SA only has rights in
  the provider's own workspace; cross-workspace access is via the
  `APIExport`'s VirtualWorkspace endpoint (kcp serves this natively
  using the APIExport's identity).

A provider's UI MUST:

- Serve static assets such that internal links are relative or rooted at
  `/ui/providers/{name}/`. Use `X-Faros-Base-Path` from the proxy if a
  build-time base is needed.

---

## Security considerations

- **Auth token forwarding** (backend proxy): the user's bearer token is
  forwarded to the provider backend. Operators MUST trust the providers
  they install. Same trust model as installing any cluster operator.
- **Provider→kcp isolation**: provider SAs are scoped to their own
  workspace. Cross-tenant access only via the APIExport mechanism, which
  kcp gates by `permissionClaims`.
- **Provider→provider isolation**: a provider never holds a credential into
  another provider's backend (runtime cluster, DB, internal Service). All
  cross-provider access is through the other provider's published
  `APIExport` resources + VW subresources, as the tenant user — see
  §"Provider isolation". This contains blast radius (one owner per backend)
  and is what makes BYO compute work.
- How published apps get their URL and access control (the template-embedded
  access gate + kcp RBAC grants) is documented in
  [Published apps: template-native access](./app-studio-publishing.md).
- **Permission claim gate**: the binding controller refuses any claim not
  marked `tenantScoped`. An override exists
  (`faros.sh/accept-untrusted-claims=true`) but is admin-only
  (host-cluster RBAC on the `CatalogEntry` resource).
- **iframe sandboxing**: `sandbox` attribute set; no
  `allow-top-navigation`.
- **CSP**: hub portal CSP allows `frame-src 'self'` plus explicitly configured
  platform-owned frame sources, such as App Studio preview hosts.
- **Internal-only services**: providers should be `ClusterIP`. Hub is the
  only public ingress. Network policies recommended.
- **Heartbeat endpoint**: protect it at ingress until route-specific
  provider-SA validation is enforced in the handler.

---

## Historical phased delivery

The remaining phase tables and implementation checklists record how the first
provider system was planned and landed. They are retained for design history,
not as current operational instructions; use the current-state and provider
author sections above for the supported contract.

| Phase | Scope | Verifiable outcome |
|---|---|---|
| 1 | `CatalogEntry` CRD + catalog controller (workspace + SA + Secret + schema apply) + registry + heartbeat endpoint + backend proxy | An example provider's chart installs, hub provisions everything, provider pod heartbeats, `/services/providers/example/*` reaches the backend |
| 2 | UI proxy + `ProviderFrame.vue` + dynamic routes + providers store + AppLayout nav integration + CSP + dev proxy | A static "hello" provider UI loads inside the portal at `/providers/hello`, side nav shows it, theme + token propagate via postMessage |
| 3 | Catalog controller adds RBAC grant (`ClusterRole` + binding for tenant identity) + `MaximalPermissionPolicy` apply on the provider's APIExport. Portal: EnableDialog + direct `APIBinding` create against kcp + nav filter to user's APIBindings + GraphQL validation of bound CRs. | Users can enable/disable from the portal; an `APIBinding` lands in their workspace; provider CRs visible AND queryable via embedded GraphQL gateway. |
| 4 | Provider SDK + example chart in `examples/provider-hello/` | Third party can copy the example and ship a working provider end-to-end |
| 5 | Hardening: RBAC fuzz, cache-bust verification, e2e tests, optional `virtualWorkspace` opt-in, claim re-acceptance flow on chart upgrade | Ready to declare stable |

## Historical deferred items

1. **GraphQL discovery of provider CRs** — REQUIRED by end of phase 3, not
   optional. Once a tenant workspace has an `APIBinding` to a provider's
   `APIExport`, the embedded GraphQL gateway MUST expose the bound CRs in
   that workspace's schema. The gateway already discovers schemas via
   APIExport for first-party faros resources (see
   [pkg/hub/graphql.go](../pkg/hub/graphql.go) and
   [cmd/graphql/main.go](../cmd/graphql/main.go) — points at
   `root:faros:providers`). Expected to work transparently, but validate in
   phase 3 with the example provider's `Greeting` CR appearing in GraphQL.
   If discovery is not automatic, the binding controller will need to
   trigger a gateway refresh — file as a follow-up task, do NOT block phase
   1 or 2.
2. **Cross-provider dependencies** — out of scope for v1.
3. **Heartbeat over kcp leases** — possible v2 simplification.
4. **Per-permission-claim UI toggles** — v2.

---

## Historical phase 1 implementation plan

Phase 1 = the full backend skeleton, no portal changes yet. Verifiable by
installing a stub provider chart and curling
`/services/providers/example/healthz` through the hub.

### Historical phase 1 snapshot

This table records the original phase-1 landing points. Paths may still exist,
but the ownership descriptions in the current-state sections above are
authoritative.

| Path | Purpose |
|---|---|
| [apis/providers/v1alpha1/types_catalogentry.go](../apis/providers/v1alpha1/types_catalogentry.go) | `CatalogEntry` Go type (admin-only group `providers.faros.sh`) |
| [apis/providers/v1alpha1/groupversion_info.go](../apis/providers/v1alpha1/groupversion_info.go) | Scheme registration for the new group |
| [config/crds/providers.faros.sh_catalogentries.yaml](../config/crds/providers.faros.sh_catalogentries.yaml) | Host-cluster CRD (codegen) |
| [config/kcp/apiresourceschema-catalogentries.providers.faros.sh.yaml](../config/kcp/apiresourceschema-catalogentries.providers.faros.sh.yaml) | kcp APIResourceSchema (codegen) |
| [config/kcp/apiexport-providers.faros.sh.yaml](../config/kcp/apiexport-providers.faros.sh.yaml) | Admin-only APIExport (excluded from `core.faros.sh` merge) |
| [hack/gen-core-apiexport/main.go](../hack/gen-core-apiexport/main.go) | Excludes `apiexport-providers.faros.sh.yaml` from the merged tenant-facing core export |
| [pkg/hub/providers/registry.go](../pkg/hub/providers/registry.go) | In-memory routing table |
| [pkg/hub/providers/proxy.go](../pkg/hub/providers/proxy.go) | `NewUIProxy`, `NewBackendProxy` reverse proxies |
| [pkg/hub/providers/controller.go](../pkg/hub/providers/controller.go) | Catalog observer: endpoint parsing, APIExport contract verification, routing registry, and readiness conditions |
| [pkg/hub/providers/api.go](../pkg/hub/providers/api.go) | `GET /api/providers` admin-mediated list endpoint backing the portal |
| [pkg/hub/portal_security.go](../pkg/hub/portal_security.go) | `WithPortalSecurityHeaders` middleware (CSP) — applied to both embedded SPA and `--portal-dev-url` proxy |
| [pkg/apiurl/urls.go](../pkg/apiurl/urls.go) | `PathPrefixProvidersUI`, `PathPrefixProvidersProxy` constants |
| [pkg/hub/server.go](../pkg/hub/server.go) | Route registration; second multicluster manager bound to `providers.faros.sh` for the catalog controller |
| [pkg/hub/scheme.go](../pkg/hub/scheme.go) | Registers the new providers group |
| [pkg/hub/kcp/bootstrap.go](../pkg/hub/kcp/bootstrap.go) | `ensureProvidersSelfBinding` — APIBinding in `root:faros:providers` so catalog entries can live there |
| [providers/quickstart/](../providers/quickstart/) | Reference provider — Go binary, Dockerfile, `manifest.yaml`, README |
| [portal/src/stores/providers.ts](../portal/src/stores/providers.ts) | Pinia store fetching `/api/providers` |
| [portal/src/router/providers.ts](../portal/src/router/providers.ts) | Dynamic `/providers/:name/:rest(.*)*` route registration |
| [portal/src/pages/ProvidersPage.vue](../portal/src/pages/ProvidersPage.vue) | Catalog grid |
| [portal/src/pages/ProviderFrame.vue](../portal/src/pages/ProviderFrame.vue) | Iframe host + postMessage handshake |
| [portal/src/components/AppLayout.vue](../portal/src/components/AppLayout.vue) | `navItems` computed, merges static + provider entries; renders icon URLs |
| [portal/vite.config.ts](../portal/vite.config.ts) | Dev proxy entries for `/api/providers`, `/services/providers`, `/ui/providers` |

### Key code anchors (from current tree)

- Route registration block: [pkg/hub/server.go:307-359](../pkg/hub/server.go#L307-L359)
- Exec-credential kubeconfig pattern (model for provider kubeconfig
  minting): [pkg/server/proxy/proxy.go](../pkg/server/proxy/proxy.go)
- Existing APIExport YAML (template for the new one's permissionClaims):
  [config/kcp/apiexport-faros.sh.yaml](../config/kcp/apiexport-faros.sh.yaml)
- Bootstrap entry point: `pkg/hub/bootstrap` + invocation around
  [pkg/hub/server.go:280-301](../pkg/hub/server.go#L280-L301)
- kcp embedded FS: [config/kcp/embed.go](../config/kcp/embed.go)
- Static path constants live in `pkg/api/url/` (referenced as `apiurl` in
  `pkg/hub/server.go`)
- Workspace YAML pattern:
  [config/kcp/workspace-providers.yaml](../config/kcp/workspace-providers.yaml)

### Provider contract verification recipe

1. `make codegen && make build` — clean build.
2. Start the hub against an embedded kcp:
   `./bin/faros-hub --embedded-kcp --static-auth-tokens=test:user-default`.
3. Run `make install-provider-quickstart`. This applies only the admin
   `Provider` onboarding record; wait for its Ready condition so the provider
   workspace and credential exist.
4. Run `make init-provider-quickstart`. Init applies the declared
   APIResourceSchema, APIExport, endpoint slice, bind grant, and CatalogEntry in
   `root:faros:providers:quickstart`. Observe that `APIExportReady` and `Ready`
   become True only after the export identity, required resource, referenced
   schema, exact claims, and backend health check are all ready.
5. `curl -H "Authorization: Bearer test" \
   http://localhost:9443/services/providers/quickstart/healthz` → reaches the
   quickstart backend.
6. POST a heartbeat with the SA token from the Secret →
   `status.lastHeartbeat` updates.
7. Run `make uninstall-provider-quickstart`. Deleting the admin `Provider`
   removes the registry entry and its finalizer tears down the provider
   workspace, ServiceAccount, CatalogEntry, API surface, and kubeconfig Secret.

### What phase 1 deliberately does NOT do

- No portal changes.
- No tenant Enable/Disable flow yet — every authenticated user sees every
  installed provider. Phase 3 adds the per-tenant `APIBinding` create from
  the portal and filters the nav.
- No GraphQL validation.
- No Helm example chart yet (phase 4).
- No `virtualWorkspace` opt-in path (phase 5).

---

## Historical phase 2 implementation plan (portal)

Phase 2 = the full portal wiring. Verifiable by serving a static "hello"
provider UI and seeing it load inside the portal frame.

See §"Portal changes" above for the file create/edit lists. Order of
operations:

1. **CSP first** ([pkg/hub/portal.go](../pkg/hub/portal.go)) — without an
   explicit `frame-src` entry the iframe is blocked. Add a small middleware
   that sets the header on portal HTML responses only.
2. **UI proxy** — `pkg/hub/providers/proxy.go` (already created in phase
   1) gets the `NewUIProxy` handler wired into the router. Existing
   backend proxy stays.
3. **GraphQL queries** + **Pinia store** + **route registration helper** —
   landed together; nothing depends on order between them.
4. **App.vue** — await `providersStore.load()` before mounting
   `<router-view />`. Critical for deep-link bootstrapping.
5. **AppLayout.vue** — replace static `navItems` with computed.
6. **ProvidersPage.vue + ProviderFrame.vue** — render the catalog and
   frame.
7. **EnableDialog.vue** — wired only enough to display the claims; the
   actual mutation lands in phase 3 (binding controller).
8. **Provider SDK** — published as workspace package; consumed by the
   example provider in phase 4.

### Phase 2 verification recipe

1. With phase 1 deployed, install a stub `CatalogEntry` with a
   simple HTTP server behind `spec.ui.url` that serves an
   `index.html` containing `<h1>hello provider</h1>` and a small script
   that calls `useFaros()` (or just `postMessage({ type: 'faros.ready' })`
   directly).
2. Open the portal in a browser. Side nav and `/providers` show the new
   provider immediately — Phase 1A/2 do not gate visibility per tenant.
   Phase 3 adds the Enable/Disable flow and the nav filter.
4. Click it. URL becomes `/providers/hello`. Iframe loads.
5. Open browser devtools → confirm:
   - `POST` request from iframe arrived with auth token (visible in
     iframe's console if the stub echoes it).
   - No CSP violations.
   - No CORS errors.
6. Toggle theme in the shell — if the stub iframe handles `faros.context`
   re-broadcasts, its background flips. (Optional check.)
7. Reload the deep link `https://faros.example.com/ui/#/providers/hello`
   in a fresh tab → still works (proves the store loads before route
   resolution).

### What phase 2 deliberately does NOT do

- Catalog Enable/Disable buttons (UI present, mutation is phase 3).
- Per-claim consent toggles (phase 3 ships only the all-or-nothing
  dialog).
- WebSocket support in the backend proxy (add in phase 5 if needed).
- Example provider chart (phase 4).

---

## Example: a minimal provider

Tracked under `examples/provider-hello/` once phase 1 lands. Structure: one
Go binary serving `/healthz` + `/api/hello` + a static `index.html`; one
controller using `faros-provider-kubeconfig` to manage a `Greeting` CR;
Helm chart from §"Provider author experience".
