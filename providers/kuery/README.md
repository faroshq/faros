# Kuery provider

> [!IMPORTANT]
> **Read-only mirror — do not push or open PRs here.**
> The standalone [`faroshq/provider-kuery`](https://github.com/faroshq/provider-kuery)
> repository is **automatically synced** from the faros monorepo
> [`faroshq/faros`](https://github.com/faroshq/faros) (path `providers/kuery/`)
> via [splitsh-lite](https://github.com/splitsh/lite). Every sync force-updates
> the mirror, so any direct change here is overwritten. File issues and PRs
> against [`faroshq/faros`](https://github.com/faroshq/faros) instead.

Fleet-wide object search, relationship traversal, and impact analysis across
the edge clusters connected to a faros workspace — built on
[kuery](https://github.com/faroshq/kuery), a multi-cluster query engine that
syncs objects into a local SQL store and answers relationship queries
(owners, descendants, spec references, selector matches, cross-cluster
links) that plain list/watch can't.

The full design — architecture, tenant isolation, the Enable-time
edges-proxy grant, value ranking, and phasing — lives in the faros repo at
[`docs/kuery-provider-architecture.md`](../../docs/kuery-provider-architecture.md).

## Status: Phase 2 — fleet query engine

What works today:

- **Embedded kuery engine** (`core/`): SQLite (default, on the chart's
  PVC) or Postgres store, the query engine, the multi-cluster sync
  controller, and the stale-cluster GC. Sync is whitelist-driven — the
  chart defaults to the workloads/config/RBAC/networking set
  (`sync.whitelist`); non-whitelisted types stay discoverable but ship no
  objects.
- **Edge engagement** (`engagement/`): watches `KubernetesCluster` objects from
  `edges.faros.sh` across every
  tenant workspace that Enabled the provider (APIExport virtual
  workspace), and syncs each connected cluster through the edges provider's
  KubernetesCluster proxy using the provider SA credential the Enable-time grant
  authorizes. Store clusters are keyed `{logicalClusterID}/{edgeName}` and
  labelled with the same trusted logical-cluster ID.
- **Tenant-scoped query API** (`queryapi/`): `POST /api/query` takes a
  kuery `QuerySpec`; the cluster filter is force-rewritten to the caller's
  hub-derived `X-Faros-Cluster` before it reaches the engine — the only path to
  the store. `X-Faros-Tenant` remains required as the authenticated tenant path.
- **MCP tools** (`mcpserver/`): `kuery_query` (fleet-wide spec
  passthrough) and `kuery_impact` (declared blast radius of one object) at
  `/mcp` + `/mcp/sse`.
- **Portal UI** (`portal/`): fleet inventory table (edge/kind/namespace/
  name filters, click-through) and the impact view — the declared blast
  radius of one object, grouped by relation. Edge selector fed by
  `GET /api/edges` (engaged edges for the caller's tenant).
- **Registration surface** from Phase 1: heartbeats, a provider-owned SavedView
  schema/APIExport, CatalogEntry (`savedviews` required resource,
  `kubernetesclusters.edges.faros.sh` claim,
  `edgeProxyAccess`), and Helm chart.

What lands next (see the design doc): an e2e suite asserting edge-object
sync end to end with a real connected agent, SavedView reconciliation,
and — as a later enhancement — the cytoscape object graph.

## Layout

```
main.go          serve loop: healthz, /api/query, /api/edges, /api/status, /mcp, portal, heartbeat
core/            embedded kuery wiring (store, engine, sync, gc)
engagement/      KubernetesCluster watcher → Engage/Disengage via the edges provider proxy
queryapi/        tenant-scoped POST /api/query (the store's only entry point)
mcpserver/       kuery_query + kuery_impact MCP tools
assets.go        //go:embed of portal/dist
manifest.yaml    CatalogEntry (SavedView required resource, KubernetesCluster claim, edgeProxyAccess)
portal/          Vite + TS micro-frontend (custom element)
deploy/chart/    Helm chart (host cluster only; PVC for the SQLite store)
```

## Deploying to a cluster (Helm)

The provider runs on the **host cluster** and registers itself into the hub.
The chart is published as an OCI artifact at
`oci://ghcr.io/faroshq/charts/faros-kuery-provider`.

### Prerequisites

- A reachable faros hub (`hub.url`).
- A **provider kubeconfig** — the workspace-admin kubeconfig minted via the
  admin onboarding flow (`/bonkers`). Stored as a Secret whose key **must be
  `kubeconfig`**.
- The `status.identityHash` of APIExport `edges.providers.faros.sh` in
  `root:faros:providers:edges` (`apiExport.edgesIdentityHash`) so the provider can bind the
  `edges.faros.sh/kubernetesclusters` permission claim.
- For the Postgres store: a running PostgreSQL with a Secret exposing a
  connection `uri` (e.g. a CloudNativePG cluster, whose `*-app` Secret carries
  `uri`). Skip this for the default SQLite store.

### 1. Namespace

```bash
kubectl create namespace faros-prod-provider-kuery
```

### 2. Provider kubeconfig Secret

The key **must** be `kubeconfig` (this matches the chart default
`providerKubeconfig.secretName=faros-provider-kubeconfig`):

```bash
kubectl -n faros-prod-provider-kuery create secret generic faros-provider-kubeconfig \
  --from-file=kubeconfig="faros/provider-kuery.kubeconfig"
```

### 3. (Postgres only) build the DSN

Read the connection URI from the database Secret and require TLS:

```bash
DSN="$(kubectl -n faros-prod-provider-kuery get secret kuery-pg-app \
  -o jsonpath='{.data.uri}' | base64 -d)?sslmode=require"
echo "$DSN"
```

### 4. Install / upgrade

```bash
helm upgrade --install kuery oci://ghcr.io/faroshq/charts/faros-kuery-provider:0.0.8 \
  -n faros-prod-provider-kuery \
  --set image.tag=v0.0.8 \
  --set hub.url=https://faros-faros-hub.faros-prod.svc.cluster.local:9443 \
  --set hub.insecure=true \
  --set hub.tokenSecretRef.name="" \
  --set apiExport.edgesIdentityHash="<identity-hash>" \
  --set catalogEntry.enabled=true \
  --set store.driver=postgres \
  --set store.persistence.enabled=false \
  --set-string store.dsn="$DSN"
```

Key flags:

| Flag | Meaning |
| --- | --- |
| `image.tag` | Provider image version (match the chart release). |
| `hub.url` | In-cluster hub address. |
| `hub.insecure` | Skip hub TLS verification (in-cluster, self-signed). |
| `hub.tokenSecretRef.name=""` | No static hub token — auth is via the provider kubeconfig. |
| `apiExport.edgesIdentityHash` | Current edges provider APIExport identity for the KubernetesCluster permission claim. |
| `catalogEntry.enabled=true` | Init container self-registers the CatalogEntry. |
| `store.driver` | `postgres` or `sqlite` (default). |
| `store.persistence.enabled` | PVC for the SQLite store; set `false` with Postgres. |
| `store.dsn` | Postgres DSN (use `--set-string`, it contains `:`/`?`/`&`). |

Upgrades from the removed `faros.sh/edges` claim are intentionally not
auto-granted. Existing tenant APIBindings are shown as **Review access** after
the new CatalogEntry is observed; a tenant user must review and confirm the
replacement `edges.faros.sh/kubernetesclusters` claim before Kuery resumes edge
engagement. The same confirmation reconciles the existing Kuery edge-proxy role
down to workspace admission plus KubernetesCluster `proxy`; it does not grant
edge status, Secret, Namespace, delegated-review, LinuxServer, or direct-read
access.

For the **default SQLite store**, drop the `store.*` Postgres flags and set
`store.driver=sqlite` with `store.persistence.enabled=true` (the chart mounts a
PVC at `/data`).

### 5. Verify

```bash
kubectl -n faros-prod-provider-kuery rollout status deploy/kuery-faros-kuery-provider
kubectl -n faros-prod-provider-kuery logs deploy/kuery-faros-kuery-provider -c provider --tail=50
```

A healthy provider logs `updated endpointslice object` and serves
`GET /healthz`. Connect an edge in a tenant workspace that Enabled the
provider, then open the Kuery portal tab.

## Local development

From the faros repo root:

```bash
make build-kuery-provider        # portal build + go build
make run-provider-kuery          # run against a local hub
make install-provider-kuery      # apply manifest.yaml to embedded kcp
```

## Tilt

Both Tiltfiles (embedded-kcp `Tiltfile` and in-cluster `Tiltfile.cluster`)
include a `providers-kuery` group:

1. `kuery` — builds + serves on :8084 (auto-restarts on source change).
2. ▶ `kuery-register` — applies the admin Provider record used by the local
   onboarding flow; the Provider controller creates the workspace, SA, and
   kubeconfig, not the provider API surface.
3. ▶ `kuery-init` — applies the SavedView APIResourceSchema, APIExport,
   APIExportEndpointSlice, bind grant, and CatalogEntry using the provider
   credential; Tilt then restarts `kuery` with edge engagement enabled.
4. Connect an edge and open the portal — the Kuery tab shows the fleet
   inventory. (▶ `kuery-unregister` deletes the Provider; its finalizer tears
   down the workspace and provider-owned CatalogEntry.)
