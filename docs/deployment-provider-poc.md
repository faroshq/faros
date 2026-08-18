# Reviewed desired-state delivery POC

This POC makes a reviewed Git tree authoritative for selected App Studio
environments. Deployments is a standalone Faros provider, but it is deliberately
not an application runtime provider: it fetches a bounded repository directory,
validates it, and applies the exact Kubernetes objects it contains to the tenant
workspace. The APIs serving those objects remain responsible for interpretation,
reconciliation, status, and runtime readiness.

## Ownership boundary

| Concern | Owner |
| --- | --- |
| Repositories, branches, pull requests, credentials, and bounded checkouts | Code provider |
| Project scaffolding, build admission, and proposed environment changes | App Studio |
| Source resolution, preflight, apply, inventory, pruning, and cleanup | Deployments provider |
| Target API behavior and runtime readiness | The provider serving each target API |

This produces the dependency `Deployments -> Code`. Deployments does not depend
on Infrastructure or App Templates. Infrastructure `Instance`, core `ConfigMap`,
and other resources are target capabilities, not part of Deployments' domain.

App Studio is an author of proposed Git changes, not another writer for a
Git-managed environment. Code is the only component that talks to the Git host
or holds Git credentials. Deployments obtains content through Code's bounded,
short-lived `RepositoryCheckout` capability.

## RepositorySync

Deployments exposes one API:

```yaml
apiVersion: deployments.faros.sh/v1alpha1
kind: RepositorySync
metadata:
  name: pen-store-production
spec:
  repositoryRef: pen-store-app
  ref: main
  path: .faros/environments/production
  intervalSeconds: 30
  prune: true
```

Every YAML document below `spec.path` is desired state. The controller:

1. Requests an immutable, path-bounded checkout from Code.
2. Parses all documents and resolves each GVK through workspace discovery.
3. Dry-run preflights the entire revision before the first persistent write.
4. Refuses conflicting documents and objects owned by another writer.
5. Applies objects with server-side apply and records exact inventory.
6. When `prune` is true, deletes inventory removed from Git using UID
   preconditions.

The engine contains no target-kind allowlist and no target-specific adapter.
Target objects may be namespaced or cluster-scoped. Inventory records API
version, kind, resource, namespace, name, UID, and source path.

`RepositorySync.status` separates delivery stages:

- `SourceReady`: Code resolved and transferred the reviewed revision.
- `AuthorizationReady`: every target API is available and authorized.
- `Applied`: the exact revision was applied and requested pruning completed.

`phase: Synced` means desired state was applied. It never means the resulting
application or infrastructure is ready. Consumers observe readiness directly on
the target resource.

## Authorization

Kubernetes and kcp authorization still apply to a generic engine. Deployments
cannot safely grant itself access merely because a repository contains a new
kind. Its APIExport therefore has:

- one required Code `repositorycheckouts` claim; and
- explicit optional claims for target resource types an installation chooses to
  offer.

The initial POC advertises optional Infrastructure `instances` and core
`configmaps`. These demonstrate a provider-owned API and a native Kubernetes API;
they are not hard-coded controller dependencies. Additional target types require
an explicit optional CatalogEntry/APIExport claim with the target export's
identity hash where kcp requires one.

Before applying anything, RepositorySync evaluates every desired object. Missing
or unaccepted access produces `phase: AwaitingAuthorization` and a
`targetRequirements` entry; no document from that revision is partially applied.
The Deployments portal links to the provider access dialog with the exact claim
tuples preselected. Authorization is additive: accepting a new claim preserves
all existing grants and uses the APIExport's authoritative identity and verbs.

This makes capability changes visible tenant decisions without coupling the
sync engine to the target provider.

## Interchangeability boundary

`RepositorySync` is intentionally a small declarative draft contract, but the
POC does not yet promise a provider-swappable ABI. App Studio currently names
the `deployments.faros.sh` API group, the Deployments APIExport identity, and the
Deployments catalog entry when guiding access setup. Another implementation can
reuse the behavior and schema, but a different kcp APIExport identity is not a
transparent replacement.

Before promoting this API beyond the POC, move the contract to a platform-owned
group such as `delivery.faros.sh`, add a controller or class identity so exactly
one implementation owns each sync, and publish conformance tests for source,
authorization, apply, inventory, pruning, and status semantics. App Studio would
then claim only the platform delivery API; Deployments would become one
replaceable implementation of that contract. The current target-neutral spec
and status model are designed to be the input to that extraction.

## App Studio delivery policy

Each Project chooses one writer per environment:

```yaml
spec:
  delivery:
    development:
      mode: Direct
    production:
      mode: GitOps
    gitOps:
      ref: main
      path: .faros
      changePolicy: PullRequest
      requiredApprovals: 1
```

In `Direct` mode, App Studio manages the Infrastructure `Instance` immediately.
In `GitOps` mode, App Studio commits the concrete target manifest under
`.faros/environments/<environment>/instance.yaml`, opens a Code
`ChangeRequest`, and treats the target binding as read-only. After merge,
RepositorySync applies the target object. App Studio observes production phase,
URL, and rollout revision directly from the Infrastructure Instance.

App Studio's Deployments permission claim is optional. Direct delivery, project
operation, and project deletion do not require Deployments to be installed or
enabled. Reviewed delivery is disabled with actionable access guidance until
Deployments is available and the `repositorysyncs` claim is applied.

The delivery policy is immutable in this POC because changing writers requires a
real ownership migration. Projects created by older POC revisions whose
production bindings reference the removed Deployments `Deployment` API must be
recreated or explicitly migrated.

## Cleanup and finalizers

Applied objects carry RepositorySync owner, source-path, and revision
annotations. A sync refuses to overwrite an object owned by another writer.

- With `prune: true`, removing an object from Git or deleting the
  RepositorySync deletes its recorded inventory and waits for target finalizers.
- With `prune: false`, RepositorySync deletion removes its ownership annotations
  and retains the target objects.
- Revoked target access is reported as `AwaitingAuthorization`; the controller
  does not silently leak inventory and remove its finalizer.

App Studio does not explicitly delete optional RepositorySync resources during
Project finalization. Their Project owner reference provides garbage collection,
so an unavailable Deployments provider cannot strand the Project finalizer.

## Local development and verification

Initialize Code before Deployments. Infrastructure is initialized independently
and is needed only for RepositorySync trees that contain Infrastructure objects.

```sh
make install-provider-code
make init-provider-code
make install-provider-deployments
make init-provider-deployments
```

The Tilt dependency graph follows the same rule: `deployments-init` waits for
`code-init`, not `infrastructure-init` or App Studio.

The Tiltcluster acceptance flow registers the provider, verifies that
RepositorySync is its only exported API, syncs an exact Infrastructure Instance
and ConfigMap from the Git fixture, checks `Synced`/`Applied`, independently
checks Infrastructure runtime readiness, and verifies generic cleanup.
