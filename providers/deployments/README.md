# Deployments provider

This headless provider owns the deployment lifecycle boundary between App
Studio and Infrastructure. App Studio creates an immutable `Release` and points
a `Deployment` at it; this provider resolves the admitted `kro-direct`
blueprint contract, materializes its Infrastructure instance, and projects
stable status.

The provider is intentionally headless: it exposes a Kubernetes API and a
controller, not a portal, user-facing REST API, MCP server, or Git credential
surface. Code is the sole Git owner; Deployments consumes only projected
`Release` and `Deployment` resources.

The initial runtime adapter supports `className: kro-direct` with the
`application` blueprint. The mapping is provider-owned because Infrastructure
Templates use virtual storage and cannot be read through a cross-provider KCP
permission claim. A future API can move that resolved contract into the
immutable Release without changing the controller authority boundary. A
Deployment can select `mode: development` to let the Infrastructure development
overlay supply the runtime image, or `mode: production` (the default) to require
and map immutable Release artifacts. Git-authored configuration is reconciled exactly: fields
removed from the Deployment are removed from the backend, while fields computed
by Infrastructure remain untouched.

`deletionPolicy: Retain` is the default and detaches the backend when the
Deployment is removed. `deletionPolicy: Delete` gives the Deployment ownership
of the backend and waits for its deletion before completing finalization. The
provider currently manages only the stable Infrastructure `instances` resource;
additional adapters must add their own claims when they are implemented.

The Infrastructure handoff is always the flattened Instance contract:

```yaml
apiVersion: infrastructure.faros.sh/v1alpha1
kind: Instance
spec:
  template: application
  values: # template-shaped values, including release image overrides
    webImage: ghcr.io/example/web@sha256:...
```

`application` is a template name carried as data. It does not create a
template-specific kind or claim; the only Infrastructure permission claim is
for the stable `instances` resource.

Bootstrap requires `FAROS_PROVIDER_KUBECONFIG` and
`DEPLOYMENTS_INFRA_IDENTITY_HASH`, the identity hash of the Infrastructure
APIExport. `deployments-provider init` installs the two APIResourceSchemas,
APIExport, endpoint slice, bind grant, and optional CatalogEntry. The default
server port is 8093 and exposes `/healthz` and controller-gated `/readyz`.

## Provider lifecycle

Deployments follows the same standalone lifecycle as Code:

1. The platform applies `provider.yaml` and `manifest.yaml` to onboard the
   provider workspace and runtime identity.
2. `deployments-provider init` uses the provider-workspace kubeconfig and
   `provider-sdk/install` to apply schemas, the APIExport, permission claims,
   APIExportEndpointSlice, and bind grant.
3. `deployments-provider serve` starts the multicluster controller and reports
   ready only after its APIExport discovery cache synchronizes.
4. Tenants enable Deployments after Infrastructure. The accepted Infrastructure
   claims are the controller's only mutation authority.

The CatalogEntry declares Infrastructure as an enable-time dependency. Init
requires `DEPLOYMENTS_INFRA_IDENTITY_HASH`, so bootstrap also fails closed if
the claim identity was not supplied.

## Local development

Both `Tiltfile` and `Tiltfile.cluster` expose four resources under the
`providers-deployments` label:

- `deployments-register`
- `deployments-init`
- `deployments`
- `deployments-unregister`

Initialize providers in dependency order:

```text
Infrastructure -> Deployments -> Code -> App Studio
```

For the embedded-kcp workflow, the equivalent commands are:

```sh
make install-provider-deployments
make init-provider-deployments
make run-provider-deployments
```

For `Tiltfile.cluster`, use the Tilt resources; they supply the front-proxy
kubeconfig/server overrides and rewrite the host backend URL for the in-cluster
hub.

## Build and test

```sh
make build-deployments-provider
make test-deployments-provider
make codegen-deployments-provider
```

The live multi-shard provider contract is part of:

```sh
make e2e-tilt-cluster
```

That suite verifies CatalogEntry readiness, exported `Release`/`Deployment`
resources, the exact Infrastructure claims and identity hash, both health
endpoints, and a fresh tenant's accepted-claim path from Release/Deployment to
an Infrastructure Instance with `Retain` finalization. Controller unit tests
cover status projection, managed configuration removal, and both finalization
policies in more detail.

## Helm

The chart is at `deploy/chart`. Required deployment inputs are:

- `providerKubeconfig.secretName`: provider-workspace kubeconfig Secret.
- `infrastructureIdentityHash`: identity hash of
  `infrastructure.providers.faros.sh`.
- `hub.url` and, when required, `hub.tokenSecretRef`.

The pod uses a dedicated ServiceAccount with host-cluster token automounting
disabled. Both init and serve authenticate to kcp only through the mounted
provider kubeconfig.

## Publishing

The provider module path is `github.com/faroshq/provider-deployments`. The
split workflow is configured to publish source to the read-only
`faroshq/provider-deployments` mirror, while release tags of the form
`providers/deployments/vX.Y.Z` publish
`ghcr.io/faroshq/faros-deployments-provider` and the Helm chart from this
monorepo.
