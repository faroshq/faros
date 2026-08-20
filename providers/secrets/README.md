# faros secrets provider

Projects secrets from external stores into tenant workspaces. Implements
workstream A.2 of
[docs/plan-secrets-mcp-governance-edge-autonomy.md](../../docs/plan-secrets-mcp-governance-edge-autonomy.md):
external stores stay the source of truth; faros stores no master secrets
beyond tenant Secret refs.

## Resources (APIExport `secrets.providers.faros.sh`, group `secrets.faros.sh`)

- **SecretStore** (cluster-scoped) — connection to an external backend.
  Backends sit behind the `backend.StoreBackend` seam
  ([backend/interface.go](backend/interface.go)); **Vault KV v2** is the v1
  implementation ([backend/vault](backend/vault)). The store credential is a
  tenant Secret ref (`spec.secretRef`, default namespace `default`, key
  `token`), validated by the SecretStore controller with `Validated`/`Ready`
  conditions — mirroring the code provider's `Connection`.
- **SyncedSecret** (namespaced) — declaratively projects external secret
  material into a workspace Secret with ExternalSecret-style semantics:
  `spec.refreshInterval` (default 1h, floor 10s), key remapping via
  `spec.data[]`/`spec.dataFrom[]`, and status carrying `lastSyncTime`,
  `syncedVersion` (content hash) and `syncedKeys`. The projected Secret is
  labeled `secrets.faros.sh/managed-by=syncedsecret` and owned by the
  SyncedSecret; the controller **refuses to overwrite Secrets it does not
  manage** and deletes the projection on SyncedSecret deletion.

Planned next (per the plan doc): `CredentialLease` (short-lived credential
issuance over the data-plane grammar) once the A.1 workload-identity service
lands, then the kubernetes-on-edge backend over the edges proxy.

## Layout

Standalone provider module (`github.com/faroshq/provider-secrets`), scaffolded
from `providers/quickstart` with the code provider's controller shape:

- `main.go` / `controller_manager.go` — serve + kcp apiexport multicluster
  manager (opt-in via `FAROS_PROVIDER_KUBECONFIG`).
- `init_cmd.go` — one-shot workspace bootstrap via `provider-sdk/install`
  (schemas, APIExport, endpoint slice, bind grant, CatalogEntry
  self-registration). Permission claims live here + `manifest.yaml` + the
  chart CatalogEntry — keep all three in lockstep.
- `apis/v1alpha1/` — API types (`make codegen-secrets-provider` regenerates
  deepcopy, CRDs, and the chart's APIResourceSchemas).
- `controller/secretstore`, `controller/syncedsecret` — reconcilers.
- `backend/` — store seam, Vault implementation, dev stub
  (`SECRETS_DEV_STUB_BACKEND=true` swaps the stub in for local dev without a
  Vault).
- `portal/` — Vite + Vue micro-frontend (`<faros-provider-secrets>`), embedded
  via `assets.go`.
- `deploy/chart/` — Helm chart (Deployment + Service + CatalogEntry ConfigMap
  + schemas).

## Local dev

```sh
# Terminal 1: hub with embedded kcp + static auth
make run-hub-embedded-static

# Terminal 2: register + bootstrap
make install-provider-secrets
make init-provider-secrets

# Terminal 3: run (portal on :8090)
make run-provider-secrets
```

Then Enable the provider for a tenant in the portal, create a credential
Secret + `SecretStore`, and a `SyncedSecret`:

```yaml
apiVersion: secrets.faros.sh/v1alpha1
kind: SecretStore
metadata:
  name: my-vault
spec:
  backend: vault
  vault:
    address: https://vault.example.com:8200
    mount: secret
  secretRef:
    name: my-vault-token   # Secret in namespace "default", key "token"
---
apiVersion: secrets.faros.sh/v1alpha1
kind: SyncedSecret
metadata:
  name: app-config
  namespace: default
spec:
  storeRef: { name: my-vault }
  refreshInterval: 5m
  dataFrom:
    - path: app/config
```
