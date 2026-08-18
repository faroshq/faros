# Deployments provider

This headless provider owns the deployment lifecycle boundary between App
Studio and Infrastructure. App Studio creates an immutable `Release` and points
a `Deployment` at it; this provider resolves the admitted `kro-direct`
blueprint contract, materializes its Infrastructure instance, and projects
stable status.

The provider exposes a Kubernetes API, a controller, and a small read-only
portal. The portal explains projected `Release` intent, observed rollout
conditions, and Infrastructure backend evidence; it does not patch, delete,
force-sync, redeploy, or present Code review state. Code remains the Git and
credential owner; Deployments requests bounded source trees through Code's
`RepositoryCheckout` contract and consumes only that result plus its own
`Release`/`Deployment` resources.

The initial runtime adapter supports `className: kro-direct` with these
blueprints:

- `application`: launchable components `web` (`webImage`) and `api` (`apiImage`).
- `simple-webapp`: one launchable component `app` (`image`).

The mapping is provider-owned because Infrastructure Templates use virtual
storage and cannot be read through a cross-provider KCP permission claim. A
future API can move that resolved contract into the immutable Release without
changing the controller authority boundary. A Deployment can select
`mode: development` to let the Infrastructure development overlay supply the
runtime image, or `mode: production` (the default) to require and map immutable
Release artifacts. Git-authored configuration is reconciled exactly: fields
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

Bootstrap requires `FAROS_PROVIDER_KUBECONFIG`,
`DEPLOYMENTS_CODE_IDENTITY_HASH` (the Code APIExport identity), and
`DEPLOYMENTS_INFRA_IDENTITY_HASH` (the Infrastructure APIExport identity).
`deployments-provider init` installs the APIResourceSchemas, APIExport,
endpoint slice, bind grant, and optional CatalogEntry. The default server port
is 8093 and exposes `/healthz` and controller-gated `/readyz`.
Serving also requires `DEPLOYMENTS_CODE_URL`, the internal Code provider base
URL used to redeem short-lived, scope/name/digest-bound checkout capabilities.
The transfer contains only the bounded source bundle; Deployments never sees a
Git credential and source contents are not stored in Kubernetes status.

## Provider lifecycle

Deployments follows the same standalone lifecycle as Code:

1. The platform applies `provider.yaml` and `manifest.yaml` to onboard the
   provider workspace and runtime identity.
2. Code and Infrastructure initialize independently, then Deployments init
   uses the provider-workspace kubeconfig and `provider-sdk/install` to apply
   schemas, the APIExport, permission claims, APIExportEndpointSlice, and bind
   grant.
3. `deployments-provider serve` starts the multicluster controller and reports
   ready only after its APIExport discovery cache synchronizes.
4. Tenants enable Code and Infrastructure before Deployments. The accepted
   Code `RepositoryCheckout` and Infrastructure `instances` claims are the
   controller's only cross-provider authority.

The CatalogEntry declares Code and Infrastructure as enable-time dependencies.
Init requires both provider identity hashes, so bootstrap fails closed if either
claim identity was not supplied.

## Local development

Both `Tiltfile` and `Tiltfile.cluster` expose four resources under the
`providers-deployments` label:

- `deployments-register`
- `deployments-init`
- `deployments`
- `deployments-unregister`

Initialize providers in dependency order:

```text
Code + Infrastructure -> Deployments -> App Studio
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
make build-deployments-provider-portal
make build-deployments-provider
make test-deployments-provider
make codegen-deployments-provider
```

The portal is served from the same binary at `/` (and through the hub UI proxy
at `/ui/providers/deployments/`). It reads tenant resources with the caller's
bearer token through `/graphql/<tenant>`. Refreshes preserve the last
successful result and label stale, pending, invalid, deleting, unknown, and
unavailable evidence explicitly.

The live multi-shard provider contract is part of:

```sh
make e2e-tilt-cluster
```

That suite verifies CatalogEntry readiness, exported
`RepositorySync`/`Release`/`Deployment`
resources, the exact Code `RepositoryCheckout` and Infrastructure claims and
identity hashes, both health endpoints, and a fresh tenant's accepted-claim
path from Release/Deployment to an Infrastructure Instance with `Retain`
finalization. Controller unit tests
cover status projection, managed configuration removal, and both finalization
policies in more detail.

## Helm

The chart is at `deploy/chart`. Required deployment inputs are:

- `providerKubeconfig.secretName`: provider-workspace kubeconfig Secret.
- `codeIdentityHash`: identity hash of `code.providers.faros.sh`.
- `code.url`: internal base URL of the Code provider bundle endpoint.
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
