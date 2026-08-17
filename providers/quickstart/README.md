# Quickstart provider

> [!IMPORTANT]
> **Read-only mirror — do not push or open PRs here.**
> The standalone [`faroshq/provider-quickstart`](https://github.com/faroshq/provider-quickstart)
> repository is **automatically synced** from the faros monorepo
> [`faroshq/faros`](https://github.com/faroshq/faros) (path `providers/quickstart/`)
> via [splitsh-lite](https://github.com/splitsh/lite). Every sync force-updates
> the mirror, so any direct change here is overwritten. File issues and PRs
> against [`faroshq/faros`](https://github.com/faroshq/faros) instead.
>
> This is also the canonical "copy me" template for a standalone provider repo:
> it ships its own `Dockerfile` and Helm chart (`deploy/chart/`). The image and
> chart are built and published from the faros monorepo CI (every PR builds
> them, so breaks are caught before the sync); the mirror itself carries no
> build workflows.

A minimal reference provider proving the faros plugin surface end-to-end.
See [docs/providers.md](../../docs/providers.md) for the architecture this
example demonstrates.

## What it shows

- A single binary serving both the **UI** (HTML page, mounted at
  `/ui/providers/quickstart/` in the portal) and the **backend HTTP API**
  (mounted at `/services/providers/quickstart/`).
- The `postMessage` handshake (`faros.ready` → `faros.context`) — the page
  receives `{ user, tenant, theme, basePath }` from the portal shell.
- That the hub's auth middleware forwards the user's bearer token to the
  provider backend (the `/api/hello` response includes the
  `X-Faros-User` header and the token length).

## Run it locally

In one terminal, the provider binary:

```sh
cd providers/quickstart
go run .
# listening on :8081
```

In another, the faros hub (embedded kcp is the easiest path):

```sh
./bin/faros-hub \
  --embedded-kcp \
  --static-auth-tokens=test:user-default \
  --listen-addr=:9443
```

Onboard the provider, initialize its API surface, and register its
`CatalogEntry` (the root Makefile wraps the development credentials and paths):

```sh
make install-provider-quickstart
make init-provider-quickstart
```

The first target applies the admin `Provider` record. The second runs
`quickstart-provider init`, which applies the Greeting APIResourceSchema,
APIExport, endpoint slice, bind grant, and CatalogEntry in the onboarded
provider workspace. (The Helm chart passes its rendered CatalogEntry to that
same init command.) The hub only
observes the contract; it does not materialize provider APIs from CatalogEntry.

Check the hub picked it up:

```sh
kubectl get catalogentry quickstart -o yaml
# status.conditions[APIExportReady].status: "True"
# status.conditions[Ready].status: "True" after runtime health/heartbeat gates
```

Curl the backend through the hub proxy:

```sh
curl -sk -H "Authorization: Bearer test" \
  https://localhost:9443/services/providers/quickstart/api/hello | jq
```

Expected response:

```json
{
  "message": "hello from the quickstart provider",
  "provider": "quickstart",
  "servedAt": "2026-05-22T...",
  "userHeader": "",
  "tokenLength": 11
}
```

`tokenLength` proves the hub forwarded the `Authorization` header.

Open the UI in a browser:

```
https://localhost:9443/ui/providers/quickstart/
```

You should see the demo HTML page. The "Backend API" section fetches
`/services/providers/quickstart/api/hello` from the browser, proving the
backend proxy works from the page too.

## Build the image

```sh
docker build -t faros-quickstart-provider:dev providers/quickstart
```

## Deploying in-cluster

The included Helm chart installs the provider Deployment, Service, init
container, and rendered CatalogEntry. First onboard the admin `Provider` and
supply the resulting provider-workspace kubeconfig as the chart's
`providerKubeconfig.secretName`; then install `deploy/chart/`. The init container
applies the checked-in schemas and APIExport before self-registering the
CatalogEntry, whose URLs already use the in-cluster Service DNS.

### Two ways a provider's kcp credentials get bootstrapped

quickstart uses the standard split model: the hub's admin `Provider` controller
creates the workspace and provider identity; `quickstart-provider init` owns the
Greeting schema, APIExport, endpoint slice, bind grant, and CatalogEntry.
quickstart does not read kcp while serving, so the kubeconfig is needed by init,
not by the runtime HTTP process.

A provider that needs kcp at runtime may reuse that workspace credential or
mint a narrower runtime identity during init. The infrastructure provider shows
the latter pattern. In every case, `CatalogEntry.apiExport.requiredResources`
declares the stable minimum and the hub verifies the complete observed export
and exact permission claims before tenant Enable.
