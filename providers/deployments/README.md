# Deployments provider

Deployments applies reviewed desired state from a Code repository to a tenant
workspace. It owns source resolution, validation, authorization reporting,
server-side apply, inventory, pruning, and cleanup. It does not interpret the
objects it applies or claim that their runtimes are ready.

The provider exposes one API:

```yaml
apiVersion: deployments.faros.sh/v1alpha1
kind: RepositorySync
metadata:
  name: pen-store-production
spec:
  repositoryRef: pen-store-app
  ref: production
  path: .faros/production
  intervalSeconds: 30
  prune: true
```

Every YAML document below the selected directory is treated as exact desired
state. A tree may contain an Infrastructure `Instance`, a core `ConfigMap`, or
another provider's resource. Deployments resolves each GVK through the tenant
workspace API, preflights every document before the first write, and applies
the objects using the `deployments-repository-sync` server-side apply field
manager. Target providers reconcile their resources and publish runtime status
on those resources.

RepositorySync status separates the stages:

- `SourceReady`: Code resolved and transferred the reviewed revision.
- `AuthorizationReady`: all target APIs are available to Deployments.
- `Applied`: the revision was applied and any requested pruning completed.

`phase: Synced` means desired state was applied. It does not mean the target
workload is operational. `targetRequirements` reports the API, resource,
namespace, authorization state, and an advertised optional claim when one can
be granted through the provider access UI.

## Authorization

Code is the only provider dependency. Its `repositorycheckouts` permission
claim is required because Code owns Git credentials and transfers a bounded,
short-lived source bundle. Deployments never receives repository credentials.

Target access is optional and tenant-authorized. The initial catalog advertises:

- `infrastructure.faros.sh/instances` for Infrastructure desired state.
- core `configmaps` as a native workspace example.

If a desired target is unavailable because its optional claim was not accepted,
the sync reports `AwaitingAuthorization` without partially applying the
revision. Other target APIs can be added as explicit optional claims without
adding provider dependencies or target-specific controller code.

`DEPLOYMENTS_CODE_IDENTITY_HASH` is required during bootstrap. The optional
`DEPLOYMENTS_INFRA_IDENTITY_HASH` adds the Infrastructure Instance claim to the
APIExport when Infrastructure is installed. Core ConfigMaps require no identity
hash. Serving also requires `DEPLOYMENTS_CODE_URL` for Code's internal bundle
transfer endpoint.

## Ownership, pruning, and deletion

Applied objects carry source-owner, source-path, and revision annotations. A
sync refuses to overwrite an object owned by a different writer. Inventory
records exact API version, kind, resource, namespace, name, UID, and source
path.

With `prune: true`, objects removed from Git are deleted using the recorded UID
as a precondition. Deleting the RepositorySync also deletes its owned inventory
and waits for target finalizers. With `prune: false`, deletion removes only the
sync annotations and retains the target objects. Missing APIs are treated as
already unavailable during cleanup; denied access is surfaced as
`AwaitingAuthorization` rather than silently abandoning objects.

## Local development

Deployments follows the standalone provider lifecycle:

```sh
make install-provider-deployments
make init-provider-deployments
make run-provider-deployments
```

In Tilt, initialize `code-init` before `deployments-init`. Infrastructure is not
an ordering dependency. Install and authorize it only when a repository sync
needs Infrastructure resources.

Focused verification:

```sh
make codegen-deployments-provider
make test-deployments-provider
```

The provider module path is `github.com/faroshq/provider-deployments`; release
tags `providers/deployments/vX.Y.Z` publish the standalone image and Helm chart.
