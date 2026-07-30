# App Studio Workspace Checkpoints Plan

## Outcome

Move workspace history and restoration out of the conversation thread and into
a first-class **Checkpoints** tab in App Studio's right-hand Workbench.

The tab presents an ordered timeline of workspace states. A user can inspect
what changed at each checkpoint and restore the workspace to any earlier point
without rewriting Git history, deleting the conversation, or rolling back
deployed infrastructure.

This is more than relocating the existing button. The current implementation
stores a per-assistant-run undo delta and can only restore that run when its
touched files still match the run's immediate result. It cannot reconstruct an
arbitrary earlier workspace state after later runs. The backend contract must
be made point-in-time before the UI promises point-in-time restoration.

## Product Decisions

### What a checkpoint means

- A checkpoint is the complete tracked workspace state after a committed
  workspace-changing transaction.
- App Studio creates an initial baseline when the project workspace is created,
  or lazily and atomically before the first mutation of an existing workspace.
- App Studio publishes the resulting checkpoint after every assistant run that
  committed workspace changes, including a run that later failed for an
  unrelated reason. Run success is not the checkpoint boundary; committed
  workspace mutation is.
- Other whole-workspace mutation paths, such as repository hydration, use the
  same transaction and checkpoint boundary. Hydration becomes all-or-nothing
  instead of exposing a partially written workspace.
- Read-only, discussion-only, failed-before-mutation, and runtime-only runs do
  not create checkpoints.
- A restore creates a new timeline event; it never deletes or rewrites the old
  history.
- The pre-restore head remains in history as the recovery point. Restoring back
  to it reverses the restore; no duplicate snapshot is required.

### What restore changes

Restore changes the App Studio workspace source tree and refreshes the
development preview.

Restore does **not** change:

- Git commits or branches;
- conversation messages or assistant work-item history;
- project template selection;
- production deployments or provider resources; or
- external databases and services.

The confirmation dialog and completion state must state this boundary plainly.

### Naming

The existing Template, Git, CI, and Production values remain on the compatible
`GET /api/projects/{project}/checkpoints` lifecycle endpoint. Reserve the
user-facing **Checkpoints** Workbench label for restorable workspace history and
use an explicit workspace namespace for the new API. A broader lifecycle
terminology rename is a separate cleanup, not part of this slice.

### Collaboration and authorization

Recommended policy, to confirm before implementation: workspace checkpoints are
collaborative project history, not private assistant-run history. A user with
permission to mutate the selected App Studio project may list and restore its
checkpoints regardless of which collaborator created them. A read-only project
viewer may list checkpoints but may not restore them.

This deliberately differs from the current run-oriented undo handler, which
authorizes only the actor who initiated the assistant run. If App Studio intends
checkpoints to remain actor-private, the API, timeline, and acceptance tests
must instead filter by actor; implementation must not infer one policy from the
current handler.

The checkpoint records `createdBy` and restore audit actor for attribution.
Authorization remains tenant-, workspace-, project-name-, and project-UID
scoped, so checkpoint IDs cannot cross project incarnations or tenant
boundaries.

### Tracked workspace boundary

For this feature, the tracked workspace is the source model already supported
by `workspace.FileStore`:

- regular project-relative UTF-8 text files only;
- no symlinks, binary files, device files, or executable-mode preservation;
- directories are implicit and empty directories are not checkpointed;
- at most 500 files, 256 KiB per file, and 16 MiB total checkpoint content; and
- App Studio's internal checkpoint storage is outside and excluded from the
  project manifest.

Checkpointed mutation paths fail before changing the live tree if their result
would exceed those limits. The UI and documentation call this the **tracked
workspace**, not the entire sandbox filesystem.

## Proposed Experience

### Workbench entry

Add `checkpoints` as a built-in Workbench tab, following the existing Preview,
Review, Providers, and Publishing patterns in
`providers/app-studio/portal/src/workbench.ts`.

- Show **Checkpoints** in the default tab set immediately after Preview so the
  feature is discoverable.
- Keep it closeable, reorderable, and reopenable from the New tab launcher.
- Use the existing Workbench tab ARIA, horizontal overflow, and stacked
  narrow-screen behavior; do not introduce a second navigation system.
- Use a Lucide history icon and the shared design tokens/components.

### Timeline

Render checkpoints newest first in an independently scrolling panel:

```text
Current workspace
│
● Added dashboard filters                         4 files
│  Today, 2:41 PM · You
│
● Built the initial dashboard                    11 files
│  Today, 2:18 PM · You
│  [Restore]
│
○ Project workspace created
```

Each entry contains:

- a stable title derived from the user request or persisted run summary;
- timestamp and actor;
- affected-file count and a bounded, expandable path list;
- source type, such as assistant change, repository hydration, or restore;
- current/restored/superseded state where applicable;
- restore eligibility and a user-readable reason when unavailable; and
- a Restore action for eligible non-current checkpoints.

The panel needs explicit loading, empty, error, refreshing, and restoring
states. Empty state copy should explain that checkpoints appear after App
Studio changes project files.

### Restore interaction

Use the shared `confirmDialog()` before restoring:

> Restore the workspace to “Built the initial dashboard”? Your current
> workspace checkpoint will remain available so you can restore it again. Git
> history, conversations, and deployments will not change.

While restoring:

- disable every restore action;
- reject a restore if an assistant run or another project mutation owns the
  project mutation slot;
- keep the selected project identity attached to the request so a project
  switch cannot apply stale UI results; and
- show progress only in the Checkpoints tab.

After success:

- reload the checkpoint timeline;
- reload conversation state only if needed for passive history metadata;
- refresh and reauthorize the development preview;
- show the newly created `Restored from …` timeline event; and
- announce completion through a bounded `aria-live` status.

Remove the `Restore workspace` button and its error/busy state from assistant
messages. A conversation message may retain a passive “Workspace restored”
audit marker for existing data, but it must not remain the control surface.

## Backend Contract

### Checkpoint record

Add a first-class store record scoped by organization UUID, workspace UUID,
project name, and project UID:

```text
id
parentCheckpointID
sourceType
sourceRunID
sourceMessageID
createdBy
title
createdAt
fileCount
changedPaths
manifestRef
workspaceDigest
restoredFromCheckpointID
status
```

`changedPaths` must be bounded. The immutable workspace manifest is the
authoritative restore input; assistant messages are display context, not the
checkpoint index.

Store checkpoint metadata through the existing memory, PostgreSQL, and
encryption-aware store implementations. Do not build the timeline by scanning
conversation messages or by adding a broad `ListAssistantRuns` API solely for
this feature.

Treat checkpoint titles, actors, changed paths, manifests, and file blobs as
sensitive project data. The encryption wrapper encrypts those fields and binds
them to organization, workspace, project UID, and checkpoint ID through
associated data. When encryption is enabled, use a project-scoped keyed digest
as the blob lookup key so deduplication does not expose a global plaintext
content hash. Key rotation follows the existing decrypt-with-old /
write-with-current-key behavior.

### Workspace representation

Replace the current restore promise of “undo this run's touched files” with an
immutable logical manifest for every checkpoint:

- the manifest enumerates all tracked workspace files and their content hashes;
- content blobs are content-addressed so unchanged files are deduplicated;
- paths, symlinks, file type, UTF-8, file-count, and size constraints reuse the
  existing workspace safety checks;
- the manifest and blobs live in the workspace store, while durable checkpoint
  metadata carries the manifest reference and digest; and
- incomplete manifests are never published as restorable checkpoints.

This model is preferable to replaying a chain of per-run undo deltas: restore
creates a new history head, so delta replay would need increasingly complex
branch and supersession rules. A materialized logical manifest makes every
checkpoint independently verifiable and restorable.

Checkpoint records form an append-only event chain. `parentCheckpointID` is the
head that was current immediately before the event. A normal mutation event
points to its newly captured manifest. A restore event also gets a new ID and
becomes the authoritative head, but its `manifestRef` equals the immutable
target checkpoint's manifest and `restoredFromCheckpointID` names that target.
The target and the former head remain unchanged and restorable. Repeating a
restore request with the same idempotency key returns the same restore event.

### APIs

Add:

```text
GET  /api/projects/{project}/workspace-checkpoints
POST /api/projects/{project}/workspace-checkpoints/{checkpoint}/restore
```

The list response should contain checkpoint display metadata plus:

```text
current
restorable
unavailableReason
```

The restore response should contain:

```text
targetCheckpoint
restoreEvent
previousHeadCheckpoint
fileCount
developmentSyncScheduled
```

Retire the new `POST /api/projects/{project}/assistant/{run}/undo` endpoint once
the pane uses the point-in-time API. Because the endpoint is not on `main` yet,
prefer replacing it in the current change rather than carrying a compatibility
alias.

### Restore transaction

Preserve the current authorization and concurrency boundaries:

1. resolve and authorize the tenant, project UID, and actor;
2. reject a missing, foreign, incomplete, or non-restorable checkpoint;
3. require project-mutation permission, require no active assistant action, and
   reserve the project mutation slot;
4. compare the current workspace digest with the known timeline head;
5. if it differs, return a conflict and do not overwrite untracked newer work;
6. retain the current head as the recovery point;
7. stage the target manifest and validate every tracked file before changing
   the live tree;
8. apply the target atomically, or roll the entire workspace back on failure;
9. persist a new restore event whose parent is the former head and whose
   manifest is the target manifest;
10. schedule development-environment sync; and
11. return the new authoritative timeline head.

Use explicit pending/completed/failed restore status so a process failure
between workspace mutation and metadata persistence can be reconciled after
restart. The current ordering mutates files before appending message metadata;
that gap must not carry into the point-in-time contract.

### Retention and cleanup

- Delete checkpoint metadata, manifests, and unreferenced blobs when a project
  is deleted.
- Retain the initial baseline plus the 50 most recent checkpoint events per
  project by default. Make the event limit configurable, but do not add
  age-based pruning in this slice.
- Never prune the current head, either endpoint of an in-progress restore, or
  the target of a retained restore event. A pruned gap is reported as such
  rather than relinking immutable history.
- Garbage-collect orphaned manifests left by failed checkpoint publication.
- Keep retention independent from conversation-message retention.
- Emit structured audit data for checkpoint creation and restore without
  storing file contents in audit records.

## Implementation Sequence

### Phase 1: establish the checkpoint domain

1. Introduce the checkpoint record and Store methods for create, get, list,
   restore-state transition, and delete.
2. Implement memory, PostgreSQL, and encryption-wrapper behavior.
3. Add immutable workspace manifests, content-addressed blobs, digest
   calculation, staged restore, rollback, and cleanup.
4. Inventory every workspace mutation path, including assistant `write_file`
   and `apply_patch`, repository hydration, template/bootstrap materialization,
   restore, and any direct `ApplyFiles` caller.
5. Route every mutation path through one project-scoped admission and
   transaction boundary. No mutation may interleave between manifest
   validation, live-tree application, and new-head publication.
6. Publish an initial baseline and then one checkpoint for every committed
   mutation result. Roll back an operation that cannot publish its checkpoint.

Success criterion: two later assistant runs may touch the same file, and either
earlier checkpoint still reconstructs its exact tracked workspace state.

### Phase 2: expose and secure the API

1. Add list and restore handlers and response types.
2. Enforce project-UID and tenant isolation plus the existing project
   read/mutate permission split.
3. Preserve mutation-slot serialization and active-run conflict behavior.
4. Add current-head digest conflict detection and restart reconciliation.
5. Schedule development sync only after a successful restore.
6. Remove the run-oriented undo endpoint and message-mutation dependency.

Success criterion: an authorized user can list only the selected project's
checkpoints and restore one atomically; foreign, stale, or concurrent requests
cannot mutate the workspace.

### Phase 3: add the Checkpoints Workbench tab

1. Add the built-in tab descriptor, default placement, launcher item, and icon.
2. Add typed API client methods and checkpoint view types.
3. Lazy-load the timeline when the tab or selected project changes, with a
   request serial that discards stale responses.
4. Implement timeline, path disclosure, empty/error/loading states, restore
   confirmation, disabled states, and completion status.
5. Refresh the preview and timeline from the restore response.
6. Remove the conversation-thread restore button and message-derived
   eligibility logic.

Success criterion: restoration is discoverable and fully operable from the
right pane, with no restore control in the conversation.

### Phase 4: retention, observability, and migration

1. Wire project deletion and retention cleanup.
2. Add counters/logs for checkpoint creation, restore success, conflict,
   rollback, and reconciliation.
3. Treat existing per-run delta directories as non-listable legacy data and
   clean them through a bounded migration/garbage-collection path; do not
   present them as full checkpoints.
4. Update App Studio documentation with the exact workspace-only restore
   boundary.

## Verification

### Workspace and store tests

- a new/existing workspace gets an initial restorable baseline before its first
  mutation;
- checkpoint manifests deduplicate unchanged content;
- multiple writes to one file in a run publish one final state;
- a run that mutates and then fails still publishes its committed workspace
  state;
- repository hydration either commits every file and one checkpoint or rolls
  back completely;
- restoring across multiple later checkpoints reconstructs the exact tree;
- files created after the target are removed;
- files deleted after the target are restored;
- restore creates a new head while the previous head remains restorable;
- manual/untracked workspace divergence produces a conflict without mutation;
- unsupported files and file/per-file/total-size limits fail before mutation;
- corrupt or incomplete blobs fail before mutation;
- a mid-apply failure rolls back the entire workspace;
- restart reconciliation produces one authoritative head;
- memory and PostgreSQL implementations have matching ordering and isolation;
- encrypted titles, paths, manifests, and blobs round-trip with scoped
  associated data, key rotation, and project-local deduplication; and
- project deletion and retention remove only in-scope data.

### API tests

- list ordering and pagination are stable;
- tenant and project UID isolation are enforced;
- project viewers can list but not restore, while project editors can restore
  checkpoints created by another collaborator;
- non-terminal/active assistant work blocks restore;
- foreign and missing checkpoints return indistinguishable not-found responses
  where required by the existing authorization posture;
- stale-head conflict, idempotent request handling, restore failure, and retry
  are covered;
- development sync is scheduled once after success and never after failure;
  and
- Git/repository state is not mutated.

### Portal tests

- Checkpoints is present by default, opens once, closes, reopens, activates, and
  reorders like other built-in tabs;
- project switches discard stale timeline responses;
- loading, empty, error, conflict, current, and non-restorable states render;
- confirmation copy states the workspace-only boundary;
- only one restore can run at a time;
- success refreshes the timeline and preview;
- the conversation has no restore action; and
- keyboard, focus, narrow-screen scrolling, and screen-reader status behavior
  remain usable.

Run the focused provider verification from `providers/app-studio`:

```bash
go test ./workspace ./store ./api
npm --prefix portal run test:workbench
npm --prefix portal run test:checkpoints
npm --prefix portal run typecheck
npm --prefix portal run build
go test ./...
```

Then run `git diff --check`. If the local App Studio service is available,
trigger its Tilt resource and smoke-test checkpoint list, restore, preview
refresh, and recovery restore against the exact organization, workspace, and
project.

## Acceptance Criteria

- The right Workbench visibly contains a Checkpoints tab.
- The conversation contains no `Restore workspace` button.
- Every displayed checkpoint represents an independently restorable complete
  tracked workspace state, not a single-run undo delta.
- Restoring an older checkpoint reproduces its exact tracked workspace tree
  even when later runs changed the same files.
- An initial baseline exists before the first mutation.
- Every committed workspace mutation either publishes a new head or is rolled
  back; failed runs cannot silently diverge from checkpoint history.
- The pre-restore head remains available as the recovery point.
- Restore cannot clobber unknown newer workspace changes.
- Restore is atomic from the user's perspective and recoverable after process
  interruption.
- Git history, conversation history, lifecycle stages, provider resources, and
  deployments are unchanged.
- Preview sync occurs after a successful restore.
- Authorization, concurrency, retention, accessibility, and responsive
  behavior have focused automated coverage.
