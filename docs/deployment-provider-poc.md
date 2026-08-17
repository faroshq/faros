# Git-owned deployments provider POC

This POC lets a project choose the desired-state boundary independently for
development and production. Its recommended policy keeps development direct and
fast while making the repository authoritative for production. Deployments is
implemented as a standalone, first-class Faros provider: it owns an APIExport,
CatalogEntry, provider workspace, minted runtime identity, multicluster
controller, Helm chart, heartbeat/readiness contract, Tilt lifecycle, image and
split-module publishing, and live provider-registration e2e coverage. The
complete Git-host promotion flow remains a manual acceptance path.

## Ownership boundary

The design deliberately separates three responsibilities:

| Concern | Owner |
| --- | --- |
| Repository operations, branches, pull requests, and merged-tree projection | Code provider |
| Project bootstrap, build admission, and proposing configuration changes | App Studio |
| Release/Deployment API, runtime reconciliation, finalization, and status | Deployments provider |
| Template schema, instance CRDs, cloud/runtime resources, and provider health | Infrastructure provider |

App Studio is an author of proposed Git changes, not a second desired-state
writer. The Code provider is the only component that talks to the Git host. The
Deployments provider never reads Git and never accepts an App Studio-specific
repository credential.

## Per-environment delivery policy

Each Project declares one writer for each environment:

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

`Direct` gives App Studio a writable provider-resource binding for that
environment. `GitOps` gives App Studio only a read-only runtime reference; one
shared `RepositorySync` projects the selected repository branch into the
Git-owned environments.

New repositories default to Direct development plus GitOps production. The
project wizard also offers Direct everywhere. The API can represent the other
environment combinations explicitly; GitOps development retains the initial
development Release/Deployment bootstrap, while the recommended hybrid writes
no development manifests to Git. Imported repositories default to Direct for
both environments, and any GitOps import is rejected until a bootstrap
migration can prove and transfer ownership without overwriting the existing
tree. Projects stored before this field existed also resolve to Direct/Direct.

The complete delivery policy is immutable in this POC. Switching either
environment is an ownership migration, not a settings toggle: a future workflow
must stop the old writer, snapshot the live and Git inventories, transfer
ownership metadata, start the new writer, and verify convergence before
declaring success. The Project controller selects bindings per environment, so
it can reconcile a direct development backend and the production
`RepositorySync` concurrently without writing the Git-owned production
Deployment.

## Bootstrap and steady state

```text
Create project (recommended policy)
  ├─ App Studio scaffolds source and directly provisions development
  └─ Project controller creates RepositorySync for production
       └─ Code watches .faros on the default branch
            └─ an empty manifest tree is valid until the first promotion

Development change
  └─ App Studio updates the direct development binding immediately

Production configuration or image change
  └─ App Studio commits generated manifests to a branch based on the default branch
       └─ Code ChangeRequest opens an approval-gated pull request
            └─ merge changes the default branch
                 └─ RepositorySync projects the merged revision
                      └─ Deployments reconciles it
```

Any project with a GitOps environment gets exactly one `RepositorySync`
binding. RepositorySync safely accepts zero manifests, so the recommended
hybrid does not fabricate a development Release merely to make `.faros`
non-empty. Production manifests first enter the tree through promotion. A
project using GitOps development still receives the development inventory in
its initial scaffold. Direct/Direct projects get neither RepositorySync nor
`.faros` inventory. Existing and adopted projects remain Direct/Direct until an
explicit migration establishes a valid Git inventory; the POC does not silently
overwrite an imported repository.

## Code provider GitOps API

The Code provider adds two cluster-scoped resources:

- `ChangeRequest` describes a pull request independently of a specific Git
  host. Its observed state includes the host URL and number, head revision,
  approval count, merge revision, phase, and conditions. `AfterApproval` may
  ask the host to merge after the configured approval threshold, but repository
  branch protection remains authoritative.
- `RepositorySync` identifies a repository, ref, and bounded path (normally
  `.faros`). It resolves the ref to an immutable commit, validates every YAML
  document before applying any of them, accepts only
  `deployments.faros.sh/v1alpha1` `Release` and `Deployment` objects, and records
  the applied revision and inventory.

`RepositoryCommit` also has an optional base ref. Creating a new proposal branch
must fork the repository's default branch (or the requested base), never create
an unrelated root commit.

The sync controller labels/annotates its projected resources and treats the
repository tree as the exact desired specification. A removed Release is
retained because it is immutable deployment history. A removed Deployment is
retained by default and deleted only when its last Git-owned specification
explicitly selected `deletionPolicy: Delete`. This makes repository pruning
intentional rather than an accidental infrastructure teardown.

`RepositorySync` has a cleanup finalizer. Deleting a Project therefore cannot
drop the source registration and strand objects with stale Git ownership:
retained inventory is detached, while only a Deployment whose controller-
recorded last-applied policy is `Delete` is removed. The config revision is
recorded separately from the Release's source revision.

## Deployments API and runtime adapter

The provider exposes `deployments.faros.sh/v1alpha1`:

- `Release` is immutable after creation. It records source repository and code
  revision, a blueprint reference, and admitted component image references.
- `Deployment` is mutable Git-projected desired state. It selects a Release,
  configuration, mode (`development` or `production`), deletion policy, class,
  and rollout identity.

The built-in `kro-direct` class currently admits the canonical `application`
blueprint contract and translates it into an Infrastructure `Application`.
Infrastructure Templates use virtual storage and therefore cannot be selected
through another provider's permission claim; the POC keeps this mapping
provider-owned until Release can carry an immutable resolved blueprint
snapshot. Production maps admitted Release image artifacts to component image
inputs. Development leaves image selection to the Infrastructure development
overlay. In both modes the adapter reserves the platform-owned name, mode, and
rollout revision, then projects backend phase, URL, outputs, and release
references into `Deployment.status`.

Configuration fields removed from Git are removed from the backend instance;
provider-computed and unrelated backend-managed fields are preserved. This is a
managed ownership merge, not a blanket replacement or an indefinitely additive
merge.

## Revision semantics

Two revisions are intentionally distinct:

- `Release.spec.source.revision` is the source commit whose admitted build
  produced the image artifacts.
- `RepositorySync.status.appliedRevision` is the merged configuration commit.
  The sync controller uses it as the Deployment rollout identity.

A configuration-only merge therefore rolls out the same artifacts with changed
configuration without pretending that a new source build occurred. CI for a
shared source/config repository should path-filter `.faros`-only changes to
avoid an image-build loop.

## App Studio promotion

When production is GitOps-managed, promotion retains the clean-workspace and
exact-build artifact gates, then generates an immutable production Release
manifest and a production Deployment manifest. It commits them to a
deterministic proposal branch and creates an approval-gated `ChangeRequest`. It
does not create the Release or directly update the Deployment. Approval alone
also does not deploy: only a merge that is subsequently applied by
`RepositorySync` can change runtime desired state. Direct production preserves
the existing immediate Release/Deployment path.

Project environment bindings may retain read-only references to the Git-owned
Deployment or its Infrastructure backend so existing App Studio status and
preview surfaces can observe it. Those references are not mutation authority.
Production publishing or access changes for a Git-owned production environment
must use the same proposal flow or fail explicitly; this POC rejects those
direct mutations where it does not yet provide a specialized proposal endpoint.
With the recommended policy, development preview access and Template changes
remain direct App Studio mutations. Falling back to a direct production patch
would create two competing writers.

## GitOps engine scope

This POC uses a hand-rolled, deliberately narrow pull reconciler in the Code
provider. It is not a general-purpose Flux or Argo CD replacement: it reads one
bounded repository subtree and supports only Release and Deployment resources.
The stable hand-off is the Deployments API, so a future Flux/Argo source adapter
could project the same resources without changing App Studio or the runtime
driver.

## Claims and rollout compatibility

The Code APIExport needs delegated access to Releases and Deployments, and App
Studio needs claims for Code `RepositorySync` and `ChangeRequest` resources in
addition to its existing repository and deployment reads. These claims require
the serving APIExport identity hashes and must match provider init,
`manifest.yaml`, and Helm CatalogEntry copies. Code and App Studio init/chart
bootstrap fail closed when the Code or Deployments identities required by this
flow are absent.

This POC deliberately does not preserve existing tenant APIBindings. Re-enable
the affected providers or create a fresh tenant so the new cross-provider
claims are accepted.

App Studio does not yet collapse the three native lifecycle resources into one
durable aggregate phase. Its promotion response reports `PendingApproval`, then
`ChangeRequest`, `RepositorySync`, and `Deployment` remain authoritative for
review/merge, config application, and runtime readiness respectively.

## Acceptance path

Source-level acceptance includes:

```sh
make codegen-code-provider codegen-deployments-provider codegen-app-studio-provider
(cd providers/code && go test -count=1 ./...)
(cd providers/deployments && go test -count=1 ./...)
(cd providers/app-studio && go test -count=1 ./...)
git diff --check
```

First-class provider acceptance against the Tilt multi-shard stack is:

```sh
make tilt-cluster
# After infrastructure-init, deployments-register, and deployments-init are ready:
make e2e-tilt-cluster
```

That gate proves the provider process is ready, its authoritative CatalogEntry
is Ready, its APIExport publishes `releases` and `deployments`, and its exact
Infrastructure permission claims carry the live Infrastructure identity hash.
It also creates an isolated tenant, binds both providers with accepted claims,
materializes an Infrastructure `Application` from a `Release`/`Deployment`, and
verifies default `Retain` finalization detaches rather than deletes the backend.

Full product acceptance additionally requires a real repository:

1. Create a default project and verify development has a writable
   provider-resource binding, production is GitOps, one RepositorySync exists,
   and no development manifests were added to `.faros`.
2. Change the Template or preview policy and verify development converges
   directly without a pull request.
3. Verify an empty RepositorySync inventory does not make production appear
   deployed or ready.
4. Admit an exact build, promote it, and verify App Studio creates a proposal
   branch and open ChangeRequest without changing Release/Deployment resources.
5. Approve but do not merge and verify runtime state remains unchanged.
6. Merge and verify the new config revision is projected, reconciled, and
   reported separately from the Release source revision.
7. Create a Direct/Direct project and verify it has no RepositorySync and
   preserves the immediate production path.
8. Commit an invalid or partially valid `.faros` tree and verify none of that
   revision is applied.
9. Remove a retained Deployment and verify infrastructure is not deleted; then
   repeat with explicit `deletionPolicy: Delete` and verify finalizer behavior.

Passing generated-schema or unit checks alone does not establish live Git-host
authorization, tenant claim acceptance, merge protection, runtime readiness,
registry access, or endpoint publication.
