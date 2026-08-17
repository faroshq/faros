# Deployments provider POC

This headless provider owns the deployment lifecycle boundary between App
Studio and Infrastructure. App Studio creates an immutable `Release` and points
a `Deployment` at it; this provider reads the referenced Infrastructure
`Template`, materializes its production instance, and projects stable status.

The POC supports `className: kro-direct`. A Deployment can select
`mode: development` to let the Infrastructure development overlay supply the
runtime image, or `mode: production` (the default) to require and map immutable
Release artifacts. Git-authored configuration is reconciled exactly: fields
removed from the Deployment are removed from the backend, while fields computed
by Infrastructure remain untouched.

`deletionPolicy: Retain` is the default and detaches the backend when the
Deployment is removed. `deletionPolicy: Delete` gives the Deployment ownership
of the backend and waits for its deletion before completing finalization. The
provider manages the current first-party instance resources (`applications`,
`simplewebapps`, and `workers`).

Bootstrap requires `FAROS_PROVIDER_KUBECONFIG` and
`DEPLOYMENTS_INFRA_IDENTITY_HASH`, the identity hash of the Infrastructure
APIExport. `deployments-provider init` installs the two APIResourceSchemas,
APIExport, endpoint slice, bind grant, and optional CatalogEntry. The default
server port is 8093 and exposes `/healthz` and controller-gated `/readyz`.
