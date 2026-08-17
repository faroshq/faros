# Plan — secrets, MCP governance, edge autonomy + Helm flow

**Status:** Plan draft (not started)
**Last updated:** 2026-08-16
**Reads as a delta on:** [cross-provider-simplification.md](./cross-provider-simplification.md),
[mcp-architecture.md](./mcp-architecture.md), [edges-marketplace.md](./edges-marketplace.md)

Three workstreams picked from the Aug 2026 provider audit + market research:

| # | Workstream | Why now |
|---|---|---|
| A | Secrets (platform hygiene + a tenant-facing secrets capability) | Secrets are the weakest primitive in the tree: six providers hold blanket `secrets` claims (the M3 side-door), four separate identity minters exist, and every provider invents its own credential storage. Externally, secretless/short-lived-credential flows are the most-praised feature of Boundary/Teleport/Cloudflare. |
| B | MCP governance layer | The hub aggregate forwards the caller bearer **unverified** today. Market is converging hard on MCP policy+audit (Teleport secure MCP GA, ngrok MCP gateway, Cloudflare MCP Server Portals Aug 2026); EU AI Act high-risk enforcement began Aug 2026; CVE-2026-46519 showed "read-only" enforced at discovery, not execution, is exploitable. faros already owns identity + tenancy + tunnels — governance is the missing third of the wedge. |
| C | Edge autonomy + Helm install flow | The edges marketplace v1 is implemented but never live-tested; edge behavior when the hub is unreachable is implicit, not designed. Edge/air-gapped is the fastest-growing fleet segment (Red Hat 2026); KubeEdge's held-upgrade + offline-autonomy semantics are the reference point. |

Sequencing: **A1 → B** (governance wants verified identity + short-lived tokens),
**C is independent** and can run in parallel.

---

## Workstream A — Secrets

### A.0 Current state (from the audit — don't re-derive)

- Six providers hold blanket `secrets` permission claims; app-studio and
  vibe-studio read the code provider's git PAT out of its `Connection` Secret
  (Contract-3 violation #1). Databricks (`secrets: [get]`, narrow) is the model
  citizen.
- Four identity minters, three of which mint **never-expiring** SAs with no GC
  (agents, vibe-studio sessions, edges per-edge SAs).
- Credential storage is ad hoc per provider: agents = per-tenant Secrets
  (`faros-agents-model-<name>`), app-studio = Postgres + envelope encryption,
  edges Services = `authSecretRef` Secrets, databricks/code = tenant Secret refs.

### A.1 Hygiene track (implements X-4 + X-5; prerequisite for B)

1. **Close the side-door.** Narrow every provider's `secrets` claim to
   provider-owned material only (labeled selector or named prefixes). Delete
   both PAT-reading paths; the registry pull-secret flow becomes a published
   code-provider capability (`RegistryCredential` CR or declared action that
   mints a scoped pull token), consumed via APIBinding — per
   cross-provider-simplification Part 3/P1.
2. **One scoped-identity service.** Generalize
   `pkg/hub/serviceaccounts/workload_identity.go` into the hub-owned minter
   (deterministic SA per owner tuple, TokenRequest-only TTL'd tokens,
   resourceNames-scoped ClusterRoles, GC keyed on the owning object). Migrate
   the agents / vibe-studio / edges minters onto it; providers drop their
   `serviceaccounts`/`clusterroles`/`clusterrolebindings` claims (ends M7).
3. **Audit surface.** One place lists every standing identity + scope + age;
   this becomes the credentials panel in the portal tenant settings.

### A.2 Tenant-facing secrets provider (new provider, `providers/secrets/`)

Scaffold from quickstart per AGENTS.md §5.6. APIExport `secrets.faros.sh`.

**CRDs:**

- `SecretStore` — connection to an external backend. Backends behind an
  interface (mirror `code/backend/interface.go`): start with **Vault
  (KV v2)** + **Kubernetes** (a Secret namespace on a connected edge, read over
  the edges proxy); cloud secret managers (AWS/GCP/Azure) later. Credential for
  the store itself is a tenant Secret ref, validated by a controller with
  `Validated`/`Ready` conditions (mirror databricks `Connection`).
- `SyncedSecret` — declaratively projects an external secret into a workspace
  Secret (ESO `ExternalSecret` semantics: refresh interval, key remapping,
  status with last-sync + version/hash). This is what infrastructure templates
  and app-studio consume instead of hand-placed Secrets.
- `CredentialLease` — short-lived credential issuance: caller (human, agent,
  workload identity from A.1) requests a lease on a named SecretStore path;
  provider returns a TTL'd credential **via the data-plane grammar** (P2
  two-gate: SSAR `get` on the CredentialLease + `create` on
  `credentialleases/issue`), never by writing a long-lived Secret. Every issue
  is an audit event.

**MCP projection (P3-compliant):** `secrets__list_stores`,
`secrets__get_lease` — thin wrappers over the P2 executor; no tool-held
credentials. Values are never returned through `tools/list`-discoverable
metadata; leases are the only read path for agents.

**Explicitly out of scope:** becoming a Vault. faros stores no master secrets
beyond tenant Secret refs; external stores stay the source of truth.

**Phases:**
1. Provider skeleton + `SecretStore` (Vault backend) + `SyncedSecret` + portal
   page. Verify: Vault KV pair appears as a workspace Secret, rotates on
   refresh interval.
2. `CredentialLease` + data-plane issue verb + audit events. Verify: lease from
   an agents-provider run, TTL expiry observed, denied without the RBAC verb
   grant.
3. Kubernetes-on-edge backend (over edges proxy) + consume `SyncedSecret` from
   an infrastructure template (`spec.envFrom` style typed ref, not a name
   convention).

---

## Workstream B — MCP governance layer

### B.0 Current state

- One aggregate endpoint per `MCPServer`, built stateless per request; edge
  tool families in-binary, Ready providers federated as `<provider>__<tool>`
  with the caller's bearer + `X-Faros-Tenant` forwarded.
- **The hub does not verify the bearer or its right to the addressed cluster
  before fan-out** (`pkg/hub/mcpaggregate/handler.go` — flagged in
  cross-provider-simplification P3).
- Client credential = per-MCPServer **long-lived legacy SA token**
  (`MCPServer.status.tokenSecretRef`).
- No policy (any bound tool is callable), no audit trail, no rate/size caps,
  no read-only mode.

### B.1 Target

`MCPServer` becomes the governance object — the policy is declared where the
endpoint is declared:

```yaml
spec:
  policy:
    mode: readonly | readwrite        # enforced at CALL time, not discovery
    tools:
      allow: ["kubernetes__get_*", "kuery__*"]   # glob allowlist; deny wins
      deny:  ["linux__exec"]
    limits: { maxCallsPerMinute: 60, maxResultBytes: 1Mi }
    approval:
      require: ["linux__exec", "infrastructure__provision"]  # → approvals flow
```

**Phase 0 — authenticate the front door (small, do first):**
- Verify the bearer and its binding to the addressed `{cluster}` before any
  fan-out (X-6). Reject cross-tenant tokens at the hub, not at each provider.
- Authenticate the remaining discovery/heartbeat surfaces (X-7).

**Phase 1 — policy enforcement:**
- Evaluate `spec.policy` in the aggregate on **every `tools/call`** (and filter
  `tools/list` as UX, never as the security boundary — the CVE-2026-46519
  lesson). `mode: readonly` maps to a verb classification each tool family /
  federated provider declares per tool (`readOnlyHint` honored only when the
  backing executor is P1-read or a declared read verb).
- Deny-by-default option per MCPServer for newly-appearing federated tools.

**Phase 2 — audit trail:**
- Structured event per call: caller identity, MCPServer, tool, args digest
  (never raw args by default), decision (allowed/denied/approval-pending),
  duration, result size, error class. Sink: sharedstore/Postgres with
  retention; portal MCP page gains a per-server audit tab; export hook (JSONL)
  for SIEM. This is the EU-AI-Act-shaped deliverable.

**Phase 3 — kill the long-lived token:**
- Replace the legacy SA token with TokenRequest-minted TTL'd tokens from the
  A.1 identity service + a refresh story for MCP clients (rotating connect
  command in the portal; OAuth device flow for clients that support the MCP
  auth spec). Legacy token stays as a deprecated opt-in during migration.
- Approval-gated tools ride the agents provider's approvals inbox (or a
  hub-level inbox if agents isn't enabled) — JIT access, ChatOps later.

**Verification:** e2e suite `test/e2e/suites/mcpgovernance/`: denied tool call
(allowlist), readonly blocking a write tool at call time despite it appearing
in a stale `tools/list`, cross-tenant token rejected at the hub, audit row
written for allow+deny, rate cap trips.

---

## Workstream C — Edge autonomy + Helm install flow

### C.1 Helm marketplace: finish + harden (v1 exists, never live-tested)

Per [edges-marketplace.md](./edges-marketplace.md) — the remaining work is
verification, then v2:

1. **Live-test pass on a kind edge** (Tiltfile.cluster): simple Workload →
   Deployment + ClusterIP Service; helm Workload (grafana) → rendered bundle in
   Placement, chart objects + PVC on edge, Service name == workload name
   (fullnameOverride); delete prunes. Fix chart versions in
   `portal/src/marketplace.ts` and gabe565 *arr values shapes as found.
2. **Auth wire formats**: exercise qbittorrent (cookie), pihole (session),
   grafana (Bearer) end-to-end to the `<service>_*` MCP tools; fix
   `svc_catalog.go` where the documented API disagrees with reality.
3. **v2 features** (post-verification, in order): chart **upgrades** (bump
   `spec.helm.version` → re-render → SSA apply diff; surface pending upgrades in
   the portal), values editing with re-render, private chart repos (credential
   via the A.2 `SyncedSecret`, not a raw Secret), `--include-crds` support for
   charts that need it, uninstall confirmation UX.
4. **Catalog expansion** (cheap wins in the core homelab audience): Immich,
   Paperless-ngx, Frigate, Uptime Kuma, Nextcloud, Vaultwarden — each is a
   marketplace row + Service preset + (usually) an existing community chart.

### C.2 Edge autonomy (design first, then implement)

Today autonomy is *implicit*: Deployments materialized on the edge keep
running when the hub is unreachable, and the agent's informer just retries.
Nothing is designed for the disconnected case. Make it explicit:

1. **Local desired-state cache.** Agent persists the last-applied Placement
   bundle set on the edge (ConfigMap or on-disk in the agent's data dir,
   content-addressed). On agent restart with hub unreachable: reconcile from
   cache instead of doing nothing. Cache invalidated by hub resync on
   reconnect.
2. **Offline semantics doc + status buffering.** Define what each feature does
   disconnected (workloads: keep converging from cache; services/MCP:
   unavailable by design; SSH: unavailable). Buffer edge status/events locally
   (bounded ring) and replay on reconnect so the hub timeline has no holes.
3. **Held upgrades** (KubeEdge v1.22 pattern): an
   `edges.faros.sh/hold-upgrade` annotation on the edge pauses agent
   self-upgrade and marketplace/workload bundle changes; the version controller
   surfaces "held, N pending" in the portal. Release per-edge or per-selector
   (fleet-staged rollouts fall out of this).
4. **Reconnect hygiene.** Jittered backoff on the tunnel dial (avoid thundering
   herd after a hub restart across a fleet); already-partially-there token
   persistence stays the auth path.

**Verification:** e2e in `edgesconn` suite: kill hub → edge workloads still
Running; restart agent while hub down → cache reconcile converges a drifted
Deployment; reconnect → status backfill visible; held edge ignores a version
bump until released.

---

## Explicit non-goals (this round)

- No new secret *storage* engine (external stores remain source of truth).
- No p2p data plane / relays (tracked separately from the market research).
- No GitOps provider — C.1's bundle+SSA+prune path is deliberately kept small;
  Argo/Flux integration is its own future provider.

## Dependency graph

```
A.1 hygiene (identity service, narrow claims)
 ├──▶ B Phase 3 (short-lived MCP tokens)
 └──▶ A.2 secrets provider (leases use identity service)
B Phase 0–2 ──(independent of A, do Phase 0 immediately)
C.1 marketplace verification ──▶ C.1 v2 ──▶ catalog expansion
C.2 autonomy design ──▶ C.2 impl (independent of A/B)
```
