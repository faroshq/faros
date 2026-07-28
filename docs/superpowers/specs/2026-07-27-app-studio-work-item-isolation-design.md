# App Studio Work Item Isolation Design

**Status:** Proposed for review

**Date:** 2026-07-27

**Related designs:**

- [App Studio Conversation Resilience](../../app-studio-conversation-resilience.md)
- [App Studio Goal-Achieving Inner Loop](2026-07-23-app-studio-goal-achieving-inner-loop-design.md)
- [App Studio Stale Plan Replanning](2026-07-26-app-studio-stale-plan-replanning-design.md)
- [App Studio Workspace Mutation Grant](2026-07-27-app-studio-workspace-mutation-grant-design.md)
- [App Studio Active Plan Dock](2026-07-27-app-studio-plan-dock-design.md)

This design supersedes the cross-run grant lifetime in the Workspace Mutation
Grant design. Its capability and path model remains valid, but a reusable grant
is now bounded by WorkItem identity and freshness and ends no later than commit,
suspension, cancellation, or completion.

## Summary

App Studio will introduce a durable `WorkItem` as the sole owner of persistent
task execution intent. A conversation supplies context, an `AssistantRun`
records one execution attempt, and Eino supplies the agent runtime and
checkpoint mechanics. None of those objects may independently authorize
continuation or mutation.

Every user action that starts a run will be recorded as one of:

- a new request;
- a read-only discussion;
- an explicit continuation of a named WorkItem; or
- an exact resume of a pending interrupt.

An omitted turn operation is always discussion. A model may propose that a
message represents new mutating work, but only an explicit user activation
creates an active WorkItem and mutation authority.

Mutation grants will be scoped to a WorkItem, its approved plan revision, the
authenticated actor, the immutable Project UID, and current
repository/workspace preconditions. Todos will remain a progress projection
only. A failed, aborted, stopped, provider-interrupted, superseded, or stale
execution will suspend its WorkItem and revoke its mutation grants.

This design prevents an unfinished task from silently becoming part of a later,
unrelated request while preserving intentional multi-turn continuation.

## Problem

App Studio currently persists three different kinds of state at project scope:

1. the conversation transcript;
2. resumable `AssistantRun` snapshots and Eino checkpoints; and
3. one reserved `approved-plan-grant` row.

The system has no durable object representing the user work being pursued.
Consequently, it reconstructs work identity from indirect signals:

- a new turn loads recent project-wide messages;
- the fallback router carries the most permissive intent found in recent user
  messages;
- a fresh run automatically loads the active project-wide plan grant; and
- Eino DeepAgent can generate a new todo projection from that combined input.

This creates a confused-deputy path. A conversational question can be classified
as an implementation turn because an earlier message requested mutation. The
fresh run can then inherit an earlier grant and recreate unfinished steps from
the transcript even though the current user did not continue that work.

### Confirmed incident shape

The `inspire-me-daily` project demonstrated the failure:

1. A quote-submission request partially changed the workspace and left an
   approved cross-turn plan.
2. A later red-theme request was interrupted.
3. A subsequent question asking whether the theme had changed was routed as
   implementation.
4. The resulting run entered mutation mode, repeatedly inspected the workspace,
   and recreated an old quote-submission todo alongside the red-theme work.

The old todo was not restored from an Eino checkpoint. It was reconstructed
because the fresh run received project-wide history plus project-wide mutation
authority. The run then failed without a durable user-facing failure reason,
leaving the old grant available to later turns.

The current code path makes that sequence concrete:

- `llm.go` loads a project-wide recent-message window and forwards history to
  the model;
- `assistant_turn_profile.go` merges intent from recent user messages and
  prevents the reconciled route from becoming less permissive than that
  fallback;
- `assistant_eino_engine.go` loads the reserved project-wide grant into fresh
  runs;
- `assistant_approved_plan.go` and `assistant_eino_tool.go` persist that grant
  independently of a durable task identity; and
- the AssistantRun uniqueness rule treats `pending_permission` and
  `pending_input` as nonterminal, although the request contract does not define
  how unrelated work should resolve that pending run.

## Decision

Introduce a first-class, tenant-scoped `WorkItem` above `AssistantRun`.

- A **Conversation** is a chronological user-visible transcript.
- A **Turn** records how one user message relates to work.
- A **WorkItem** is the durable unit of user intent and lifecycle.
- An **AssistantRun** is one attempt to execute or discuss a Turn.
- An **ApprovalGrant** authorizes bounded actions for one WorkItem plan
  revision.
- **WorkItemProgress** is the current user-visible plan/todo projection.
- An **Eino checkpoint** resumes one exact interrupted AssistantRun.
- **WorkspaceState** fences a WorkItem from a changed or recreated project.

The WorkItem is the only object that may connect a later turn to earlier
execution intent. Transcript proximity, model classification, todos, Eino
session values, and checkpoint existence are never sufficient.

## Goals

- Prevent tasks, todos, approvals, and mutation intent from leaking into
  unrelated turns.
- Preserve intentional multi-turn continuation and exact approval/input resume.
- Keep Eino as the execution, middleware, interrupt, and checkpoint substrate.
- Reuse the durable server-owned `AssistantRun` and snapshot supervisor.
- Make routing, continuation, grant use, suspension, and failure auditable.
- Fail closed when the project, plan, actor, or work identity is stale.
- Preserve the single-active-run project contract.

## Non-goals

- Replacing Eino, DeepAgent, TurnLoop, or Eino checkpoint serialization.
- Building a general workflow scheduler or background retry system.
- Running multiple mutating WorkItems concurrently in one project.
- Treating todos as authoritative workflow state.
- Changing which development operations are automatically authorized by the
  existing goal-achieving transaction policy.
- Introducing multi-replica execution ownership.

## Ownership Boundary

| Object | Owner | Purpose | May authorize mutation? |
| --- | --- | --- | --- |
| Conversation message | App Studio | User-visible history | No |
| Turn | App Studio | Relationship between a message and a WorkItem | No |
| WorkItem | App Studio | Canonical intent, plan revision, and lifecycle | Only through a valid grant |
| ApprovalGrant | App Studio | Actor- and scope-bound authority | Yes |
| WorkspaceState | App Studio | Project incarnation and workspace freshness fence | No |
| AssistantRun | App Studio | Attempt, audit, snapshots, and terminal outcome | No |
| Eino session values | Eino runtime, seeded by App Studio | Per-run execution context | No |
| Eino checkpoint | Eino runtime, persisted by App Studio | Exact interrupt continuation | No |
| DeepAgent todos / PlanTask subtasks | Eino runtime | Planning and progress | No |

Eino explicitly leaves conversation/session persistence and checkpoint identity
to the application. App Studio will therefore use Eino primitives inside the
WorkItem boundary rather than treating an Eino session or todo list as the
boundary itself.

## Durable Model

### WorkItem

Add `app_studio_assistant_work_items` with:

| Field | Meaning |
| --- | --- |
| tenant/project scope | `org_uuid`, `workspace_uuid`, `project_name`, and immutable `project_uid` |
| `work_item_id` | Stable opaque identifier |
| `origin_message_id` | Immutable message that created the work |
| `created_by` | Stable authenticated actor identifier |
| encrypted title and intent | User-visible title plus canonical goal, constraints, and acceptance criteria |
| encrypted plan | Approved steps, capabilities, and target paths |
| `plan_revision` | Increments only when approved intent or scope changes |
| `revision` | CAS revision for all row transitions |
| `status` | `proposed`, `active`, `suspended`, `completed`, or `cancelled` |
| `status_reason` | Bounded machine-readable lifecycle reason |
| `active_run_id` | Current attempt, when one exists |
| project preconditions | Repository binding, expected WorkspaceState revision, and workspace fingerprint |
| progress | Sanitized WorkItemProgress projection and progress revision |
| timestamps | Created and updated time |

The encrypted title, intent, and plan fields follow the existing encrypted-store
contract. Indexable identities, statuses, revisions, and timestamps remain
plaintext. Raw prompts, file contents, tool arguments, results, and credentials
must not be copied into WorkItem metadata.

`project_uid` is the Kubernetes Project object's UID, not its reusable name.
Every new record and current-state query includes it. Deleting and recreating a
Project with the same name creates a new UID, so stale rows left by best-effort
cleanup are never visible to the new Project.

The first version enforces at most one `active` mutating WorkItem per Project
UID. Multiple `proposed` and `suspended` WorkItems may exist. Activating an
unrelated WorkItem suspends the previously active item only when it has no
nonterminal AssistantRun, then revokes its grants. A running or pending
permission/input AssistantRun is not idle and is never silently preempted.

V1 creates WorkItems for mutating development transactions. Read-only
discussion remains represented by a Turn and AssistantRun without a WorkItem.
The schema does not prevent adding durable read-only WorkItems later, but this
design does not require them.

### Turn

Add `app_studio_assistant_turns` with:

- tenant/project scope, immutable `project_uid`, and `turn_id`;
- `user_message_id`;
- authenticated actor;
- `kind`: `new`, `discussion`, `continue`, or `resume`;
- nullable `work_item_id`;
- nullable exact `run_id`, `request_id`, and `interrupt_id` for resume;
- the normalized route/profile selected for the run; and
- creation timestamp.

The store enforces these relationships with check constraints and scoped
foreign keys:

- `new` and `continue` require `work_item_id` and forbid the resume tuple;
- `resume` requires `work_item_id`, `run_id`, `request_id`, and `interrupt_id`;
- `discussion` forbids the resume tuple and may reference a WorkItem only for
  bounded read-only context; and
- every referenced WorkItem and run must have the same tenant scope and Project
  UID as the Turn.

`Turn.kind` is durable audit data. A model may recommend a route, but it cannot
change the Turn's relationship to a WorkItem, prove that the latest message
explicitly requests mutation, or grant itself continuation.

### AssistantRun

Every AssistantRun requires `turn_id`. Its `work_item_id` is nullable only for
discussion runs. Store validation requires the run's WorkItem ID to equal its
Turn's WorkItem ID, including both being null for an unbound discussion.

Message, Turn, and AssistantRun records require immutable `project_uid` from
their first write. The field is non-null and has no project-name-only fallback.

`AssistantRun` remains the execution-attempt and snapshot record described by
the conversation-resilience design. A WorkItem may have multiple runs over its
lifetime. Only one run may be active for a project, preserving the current
supervisor and database invariant.

### ApprovalGrant

Store grants in `app_studio_assistant_approval_grants`. The schema and runtime
do not represent a grant as an AssistantRun or reserve an AssistantRun ID for
grant state.

Each grant records:

- tenant/project scope including immutable `project_uid`;
- `grant_id` and `work_item_id`;
- `plan_revision`;
- issuer/approver actor;
- capability and canonical target paths;
- repository binding, WorkspaceState revision, and workspace fingerprint
  preconditions;
- optional exact action identity for direct approval:
  tool, canonical argument digest, run, request, and interrupt;
- `active`, `consumed`, or `revoked` status;
- revocation/consumption reason; and
- timestamps and optional expiry.

Narrative plan fields and action arguments remain encrypted or represented by
safe digests. Grants are never widened by model output.

### WorkspaceState

Add `app_studio_project_workspace_state`, keyed by tenant scope, project name,
and immutable `project_uid`, with:

- a monotonic `revision`;
- the current repository binding;
- a deterministic workspace fingerprint; and
- execution-fence status: `ready` or `reconciliation_required`, with a bounded
  reason;
- update timestamp.

The fingerprint is SHA-256 over a canonical, sorted stream of
workspace-relative path, file type, and content-digest entries, excluding
provider-owned ephemeral runtime data. Its exact encoding is versioned.
Repository identity, branch, and HEAD are included separately in the repository
binding. A WorkItem and each of its grants record the revision and fingerprint
they expect.

Every mutation-capable dispatch participates in one per-Project execution
fence, including source edits, file APIs, template hydration/reset, repository
checkout/commit, runtime rebuild, production promotion, provider mutations, and
project deletion. A tool holds its execution lease from the final authorization
reload through side-effect outcome persistence. Source-changing operations also
advance WorkspaceState. This requirement is part of the boundary; a mutating
path that bypasses the fence may not ship.

Every transition that can remove, replace, or create mutation authority uses
the exclusive side of the same fence: Stop, suspend, cancel, complete,
plan-revision change, grant revocation, and WorkItem activation. If a tool
acquires the fence first, the transition cancels its run context and waits for
the tool to finish, acknowledge cancellation, or persist an unknown outcome
before returning. A non-cancellable external action is reconciled through its
provider-specific contract. Until that reconciliation succeeds,
WorkspaceState is `reconciliation_required` and no WorkItem may activate or
mutate. If revocation acquires the fence first, the tool's mandatory reload
observes the inactive WorkItem or grant and cannot begin. Therefore Stop never
returns while an old tool can still begin or complete an unrecorded later
mutation, and new work cannot activate until prior authority and outcomes are
fenced. Multi-replica execution would require a distributed lease/fencing token
and remains outside V1.

The filesystem and relational store cannot be committed atomically. A tool
therefore:

1. acquires the per-Project execution fence and reloads the current Project
   UID, WorkItem, grant, and WorkspaceState;
2. recomputes the fingerprint and validates the expected revision,
   fingerprint, repository binding, and target-file digest;
3. performs the bounded filesystem mutation;
4. recomputes the fingerprint; and
5. transactionally CAS-advances WorkspaceState and the owning WorkItem/grant
   preconditions before publishing success.

If the process fails after the filesystem write but before the CAS, the next
operation observes a fingerprint mismatch and suspends the WorkItem for
reconciliation. It never treats the write as authorized merely because the
stored revision did not advance. A successful WorkItem-owned write advances its
own expected preconditions; it does not make its next step stale.
Reconciliation may advance WorkspaceState to the observed fingerprint, but it
never refreshes an existing WorkItem or grant. Continuing that work requires a
fresh state review and authorization.

### WorkItemProgress

WorkItemProgress contains:

- a bounded list of user-visible steps;
- `pending`, `in_progress`, or `completed` status per step;
- at most one in-progress step;
- the current high-level phase;
- last safe status and failure reason; and
- a monotonic progress revision.

Eino `write_todos` or PlanTask may propose this projection. App Studio validates
and persists it, but it does not establish the WorkItem goal, attach a Turn,
advance authorization, or prove completion. Server-observed tool, verification,
and commit results control lifecycle transitions.

## Core Invariants

1. A conversation transcript never grants continuation or mutation authority.
2. A new Turn has no WorkItem unless the server creates one or the request names
   one through an explicit continuation/resume operation.
3. A WorkItem plan revision is immutable after approval. Scope changes create a
   new revision and revoke grants for the old revision.
4. Every mutation checks WorkItem status, plan revision, actor, capability,
   target, Project UID, repository binding, WorkspaceState revision, workspace
   fingerprint, and target digest immediately before the side effect.
5. A grant is usable only by its owning active WorkItem.
6. Todos and model-authored plans are progress data, never authority.
7. Only an exact pending run/request/interrupt tuple may resume an Eino
   checkpoint.
8. A terminal run cannot leave reusable mutation authority behind.
9. A failed or ambiguous external action suspends work until current state is
   reconciled by the existing provider-specific contract; it is not
   automatically retried.
10. Tenant scope, project name, and immutable Project UID are present in every
    current-state key and query.
11. A run snapshot, WorkItem transition, and grant consumption/revocation are
    persisted atomically before the corresponding state is published to
    subscribers.

## Turn Routing and Continuation

### Latest message is authoritative

The router classifies the latest explicit user message. Historical messages may
help interpret references, but may not escalate the route, attach a WorkItem, or
enable mutating tools.

The current escalate-only merge across recent user messages is removed. A
status question remains discussion even if an earlier message requested code
changes.

### Explicit activation

Intent detection and authorization are separate:

- An omitted `turn` operation records a `discussion` Turn and exposes no
  mutating tools.
- If the router believes that discussion contains a new implementation request,
  it may create a `proposed` WorkItem and return a sanitized **Start task**
  confirmation. Classification alone cannot activate it or create a grant.
- The portal activates the proposal only after an explicit user action, using
  the proposal ID and a new `clientRequestID`. Activation records a `new` Turn,
  changes the WorkItem to `active`, and independently evaluates the existing
  routine-development authorization policy.
- A composer mode or other first-party UI that the user explicitly selects as
  **Start task** may submit `turn.kind: "new"` directly. Merely pressing Send in
  the ordinary chat composer is not that signal.
- The structured initial-project prompt is already an explicit application
  creation action and may create an active WorkItem directly.

The server, not the model or arbitrary client fields, derives the canonical
goal, capabilities, and paths. If explicit activation cannot be proven, the
request remains discussion.

### Explicit continuation

Continuation is represented structurally, not inferred from transcript
proximity.

- The portal's **Continue** or **Resume task** action sends `workItemID`.
- A permission/input response uses the existing resume endpoint and exact
  pending run/request identifier; the server resolves its WorkItem.
- Free-form text such as "continue" without a selected WorkItem does not attach
  authority. If exactly one resumable item exists, App Studio may present it as
  a confirmation choice, but it must not mutate before confirmation.
- Activating a new proposal suspends any idle active item and revokes its
  grants. An item is idle only when it has no nonterminal AssistantRun.
- If the active run is `pending_permission` or `pending_input`, a new task start
  returns `409 pending_run` with that exact run and WorkItem. The user must
  Resume it or explicitly Stop/Discard it. Stop terminalizes the pending run,
  suspends or cancels the WorkItem, and revokes its grants before another
  WorkItem can activate.
- Because V1 retains one nonterminal run per Project, any request that would
  start another AssistantRun—including discussion—returns the same conflict
  while permission/input is pending. The portal can display the durable pending
  snapshot without starting an LLM run.
- A discussion may reference a WorkItem for bounded context while still
  receiving no mutation tools.

### Context assembly

Mutation-capable runs receive:

1. system instructions and current Turn;
2. the selected WorkItem's canonical goal, approved plan revision, progress,
   and bounded linked messages;
3. a fresh project/repository/workspace snapshot; and
4. only the tools allowed by the Turn and valid grants.

They do not receive an undifferentiated project-wide recent-message window.
Discussion turns may receive broader conversational history, but the presence
of that history never changes tool authorization.

## Grant Lifecycle

### Creation

A grant is created only by:

- an existing product policy that treats a server-validated `new` or `continue`
  Turn as authorization for routine development actions;
- an explicit approved plan; or
- an exact tool-level approval interrupt.

In every case App Studio, not the model, derives the capability and canonical
scope. A model/router classification alone cannot create a grant: the
authorization policy independently requires explicit user activation or
continuation recorded in the current Turn and fails closed when that intent is
ambiguous.

### Validation

Before mutation, the tool boundary atomically validates:

- authenticated actor matches the grant policy;
- WorkItem is `active`;
- WorkItem and grant plan revisions match;
- Project UID, repository binding, WorkspaceState revision, workspace
  fingerprint, and target digest are fresh;
- capability and canonical target are covered;
- direct approvals match the exact action digest and pending interrupt; and
- the grant has not expired, been consumed, or been revoked.

The initial policy is same-actor reuse. Cross-user continuation or approval is
denied until a separate collaboration policy is designed.

### Consumption and revocation

- Commit approval is one-shot and consumes the applicable write authority.
- Completing or cancelling a WorkItem revokes every remaining grant.
- Failed, aborted, stopped, provider-interrupted, or no-progress runs suspend
  the WorkItem and revoke its grants.
- Pending permission/input preserves the WorkItem and the minimum grant needed
  to resume that exact checkpoint. It remains a nonterminal active run and
  blocks another run for the Project.
- Suspending an item because a new request supersedes it revokes its grants.
- Resuming a suspended WorkItem re-reads project state and requires a fresh
  grant when prior preconditions no longer match.

All transitions use compare-and-swap so terminalizing an old run cannot revoke
a newer explicitly approved plan revision. Run terminalization, WorkItem status,
and grant state commit in one store transaction; persistence failure stops
orchestration rather than publishing a partial lifecycle transition.

## Eino Integration

App Studio currently uses Eino v0.9.9 DeepAgent, TurnLoop, session values,
middleware, and stateful interrupt/resume. Those remain the implementation
foundation.

### Run and checkpoint namespace

Each AssistantRun creates its own Eino TurnLoop and checkpoint. The checkpoint
namespace includes both WorkItem and run identity. Only an exact resume of that
run restores the checkpoint.

A new continuation run does not restore an earlier terminal run's Eino
checkpoint. It receives a fresh Eino run seeded from the durable WorkItem and
current project state. This prevents stale run-local todos and tool history from
becoming new-run state.

### Session values

App Studio passes a small immutable-by-convention execution envelope through
`adk.WithSessionValues`:

- WorkItem ID;
- WorkItem and plan revisions;
- Turn and run IDs;
- authenticated actor;
- Project UID, repository binding, WorkspaceState revision, and workspace
  fingerprint; and
- the existing fresh project snapshot.

Eino v0.9.9 permits middleware and tools to add or replace session values, so
the envelope is untrusted runtime context, not the source of truth. Tools reload
and validate the durable WorkItem/grant immediately before mutation; changing a
session value can never change authorization.

### Todos and PlanTask

DeepAgent `write_todos` remains sufficient for the initial progress projection.
Eino PlanTask may later replace it when dependency-aware subtasks are useful,
provided its backend is isolated by WorkItem. Neither option replaces
WorkItem, Turn, or ApprovalGrant.

### Deterministic workflow

The existing App Studio phase middleware remains the first implementation of
`plan -> mutate -> verify -> commit -> report`. A later Eino Workflow/GraphTool
may encode that deterministic transaction, but it is not required to establish
task isolation and must still use the App Studio WorkItem/grant boundary.

## Failure and Recovery Semantics

Runs persist a bounded `failure_reason` enum. At minimum:

- `model_error`;
- `tool_error`;
- `persistence_error`;
- `iteration_budget_exhausted`;
- `no_progress`;
- `stale_work_item`;
- `stale_project_state`;
- `authorization_revoked`;
- `external_outcome_unknown`;
- `provider_interrupted`.

Raw model/tool errors remain in redacted server logs or encrypted audit data,
not user-visible metadata.

### No-progress circuit breaker

After an approved WorkItem enters mutation phase, eight consecutive model
iterations without one of the following terminate the run as `no_progress`:

- a workspace mutation;
- an approval/input interrupt;
- an authorized plan-revision transition;
- a verify/commit/report phase transition; or
- a terminal user-facing answer.

Reads, searches, repeated readiness checks, and `write_todos` alone do not reset
the counter. Existing repeated-tool detection remains independently active.
Global Eino iteration exhaustion maps to `iteration_budget_exhausted`.

### Lifecycle outcomes

| Run outcome | WorkItem outcome | Grant outcome |
| --- | --- | --- |
| Requested outcome completed; source validated and committed when applicable | `completed` | Consumed/revoked |
| Discussion completed | Unchanged or none | Unchanged |
| Pending permission/input | Remains `active` | Preserve exact resumable scope |
| Explicit Stop | `suspended` | Revoked |
| Model/tool/no-progress failure | `suspended` | Revoked |
| Provider shutdown/interruption | `suspended` | Revoked |
| Stale actor/project/plan | `suspended` | Revoked |
| User discards task | `cancelled` | Revoked |

Resume always reloads the Project UID, workspace, repository, WorkspaceState,
and fingerprint. Unknown completion of an external side effect is reconciled
through that tool's existing provider-specific contract before retry; App
Studio does not repeat it blindly. A uniform external-action ledger is a
follow-up, not part of this isolation cut.

## HTTP Contract

Extend the durable message start request:

```json
{
  "content": "Continue the theme update",
  "clientRequestID": "uuid",
  "turn": {
    "kind": "continue",
    "workItemID": "work-item-id"
  }
}
```

For ordinary new messages, `turn` may be omitted. The server creates a
`discussion` Turn. It may return a proposed WorkItem, but omission never means
"start mutating work" or "continue whatever was active."

The client-supplied `turn` object is a requested operation, not trusted state.
The server validates WorkItem tenant scope, actor policy, lifecycle, revision,
Project UID, and exact resumable identifiers before recording or executing it.
`turn.kind: "new"` is accepted only for a proposal activation or a
server-recognized explicit product action such as initial project creation.

Add:

- `GET /api/projects/{project}/assistant/work-items`
- `GET /api/projects/{project}/assistant/work-items/{workItem}`
- `POST /api/projects/{project}/assistant/work-items/{workItem}/activate`
- `POST /api/projects/{project}/assistant/work-items/{workItem}/continue`
- `POST /api/projects/{project}/assistant/work-items/{workItem}/cancel`
- `POST /api/projects/{project}/assistant/runs/{run}/stop`

Existing permission/input resume endpoints remain exact-run operations. Their
responses include WorkItem identity and current revision.

Every mutating start, activate, continue, resume, cancel, and stop request
requires `clientRequestID`. Its idempotency record binds the actor, Project UID,
operation, WorkItem/Turn/run identifiers, and canonical request digest. Reusing
an ID with different content or a different WorkItem/Turn returns conflict; it
cannot relink an earlier request to new authority.

Every run snapshot includes `turnID`, nullable `workItemID`, WorkItem status,
and safe failure reason. Every streaming transport creates the same durable
Turn and, when applicable, WorkItem records before execution.

## Current Implementation Seams

The implementation extends these existing boundaries:

- `store/store.go` and store implementations for tenant-scoped messages,
  AssistantRuns, encryption, and compare-and-swap persistence;
- `assistant_supervisor*.go` for one active run, durable snapshots, and terminal
  transitions;
- `assistant_turn_profile.go` and `llm.go` for Turn classification and context
  assembly;
- `assistant_approved_plan.go` and `assistant_permission.go` for grant
  persistence and capability/path enforcement;
- `assistant_eino_engine.go`, `assistant_eino_state.go`, and
  `assistant_eino_interrupt.go` for Eino TurnLoop, session values, checkpoints,
  and resume; and
- `App.vue` plus the existing plan-dock components for active and suspended work
  presentation.

No new service, external broker, or second agent runtime is introduced.

## Portal Contract

- Show the active WorkItem title and status beside the existing plan dock.
- Show a proposed mutation as a bounded **Start task** confirmation; no plan
  execution or mutation controls appear before activation.
- Key the live plan/progress projection by WorkItem and active run, not merely
  by the latest assistant message.
- After failure, interruption, or Stop, remove the active plan dock and show a
  bounded suspended-task card with **Resume** and **Discard**.
- Resume sends the selected WorkItem ID and never relies on free-form transcript
  inference.
- Starting unrelated work makes the new item active and leaves the prior item
  available under suspended tasks.
- When a pending permission/input run blocks new work, show **Resume**, **Stop**,
  and **Discard** for that exact run instead of silently starting or queueing
  another task.
- Discussion and status questions do not display mutation progress or expose
  mutation controls.
- Multiple tabs converge through WorkItem and run revisions; stale actions
  receive conflict and refresh current state.

## Security and Tenancy

- Every current WorkItem, Turn, Run, Grant, WorkspaceState, and query is scoped
  by organization, workspace, project name, and immutable Project UID.
- The authenticated actor is recorded server-side; client-supplied identity is
  ignored.
- A missing or unstable authenticated actor is rejected for activation,
  continuation, resume, approval, cancellation, and mutation. Cross-user
  activation, continuation, resume, approval, or cancellation is denied in V1.
- The server canonicalizes paths and action arguments before hashing or
  comparing grant scope.
- Authorization is checked immediately before mutation, not only when the model
  selects a tool.
- Grant and WorkItem transitions use CAS and produce a sanitized audit event.
- Sensitive narrative fields use existing encryption-at-rest behavior.
- WorkItem progress and portal snapshots contain no credentials, bearer tokens,
  raw source, tool arguments, tool results, prompts, or unsanitized errors.
- A checkpoint ID or interrupt ID is a routing identifier, not a bearer
  capability.
- Idempotency keys are bound to actor, Project UID, operation, and canonical
  request digest and cannot be replayed across WorkItems or Turns.

## Deployment and Initialization

This design targets net-new deployments with no pre-existing App Studio
conversation, run, checkpoint, grant, or workspace data.

- The initial schema creates WorkItem, Turn, ApprovalGrant, WorkspaceState,
  message, and AssistantRun storage in their final form.
- Project UID is non-null on every project-scoped record from its first write.
- Project creation calls `EnsureWorkspaceState`, and every assistant entry point
  calls it under the per-Project execution fence before persisting a message,
  Turn, grant, or run. The operation fast-paths a valid existing row or creates
  it idempotently for the current Project UID, computes its initial
  fingerprint, and fails closed if initialization or fingerprinting fails.
- Grants exist only as WorkItem-scoped ApprovalGrant records.
- Provider and portal versions deploy together against the same schema.

## Alternatives Considered

### 1. Prompt and classifier changes only

Teach the model not to revive old work and classify only the latest message.

This is necessary containment but not a sufficient boundary. Prompt behavior
cannot bind authorization, withstand ambiguous messages, resolve multi-tab
races, or prevent a stale checkpoint/grant from being selected elsewhere.

### 2. Keep one project-wide session and revoke grants more aggressively

Clear the current grant on terminal failure and rely on project conversation
history for continuation.

This closes the confirmed path but loses intentional work identity. It cannot
reliably distinguish "continue the quote task" from "start the theme task," and
it makes future collaboration and multiple suspended tasks ambiguous.

### 3. Use Eino PlanTask or TurnLoop as the WorkItem

Treat the Eino todo/task backend or checkpoint key as the durable task record.

Eino intentionally leaves session identity, history persistence, and
authorization to the application. PlanTask manages agent subtasks, while
checkpoint IDs resume execution locations. Neither binds user intent, actor,
tenant, immutable Project UID, workspace freshness, or mutation grants.

### 4. Durable App Studio WorkItem

This is the selected design. It introduces one domain boundary while reusing
the existing durable-run supervisor and Eino runtime. It is more work than a
router patch, but it provides an enforceable answer to continuation,
authorization, suspension, and stale-state recovery.

## Delivery Phases

### Phase 1: Isolation foundation

- Add WorkItem, Turn, immutable Project UID, WorkspaceState, store parity,
  encryption, CAS, and indexes.
- Make the latest message authoritative; history cannot escalate a route.
- Default omitted turn operations to discussion and require explicit task
  activation before mutation.
- Link new messages and runs to durable Turns and WorkItems.
- Put every mutating tool behind the per-Project execution fence; add
  WorkspaceState advance, fingerprint, and target-digest checks to
  source-changing paths.
- Assemble mutation context from the selected WorkItem instead of all recent
  project messages.
- Persist safe failure reasons and add the mutation-phase no-progress circuit
  breaker.

### Phase 2: WorkItem-scoped grants and portal controls

- Add WorkItem-scoped grants with no project-global grant path.
- Enforce actor, plan revision, path/capability, and project preconditions at
  the tool boundary.
- Add proposed/active/suspended WorkItem UI, explicit Start/Continue/Resume,
  pending-run Stop, and Discard.

### Phase 3: Eino workflow refinement

- Evaluate PlanTask for dependency-aware WorkItemProgress.
- Evaluate Workflow/GraphTool for deterministic development transaction phases.
- Evaluate a uniform external-action ledger and provider idempotency contract
  where current provider-specific reconciliation is insufficient.

Phase 3 is optional and must not delay the isolation boundary.

## Acceptance Criteria

- A failed quote-submission task cannot place quote todos or mutation authority
  into a later theme-status question.
- A status/advisory question after mutation history remains read-only and
  exposes no edit, todo, or commit tools.
- A classifier's mutation recommendation creates at most a proposed WorkItem;
  no mutating run or grant exists until explicit user activation.
- An explicit Continue action resumes only the selected WorkItem.
- A new unrelated mutation request suspends the idle prior WorkItem and revokes
  its grants.
- A pending permission/input run is nonterminal. Starting another task returns
  `409 pending_run` until the user resumes or explicitly stops/discards it.
- A fresh continuation run receives WorkItem state but not a terminal run's Eino
  checkpoint or run-local todos.
- Exact permission/input resume restores only the recorded WorkItem/run/
  interrupt tuple.
- Failed, aborted, interrupted, stale, completed, and cancelled work leaves no
  reusable mutation grant.
- Pending permission/input remains resumable.
- An actor mismatch, stale plan revision, changed repository binding, changed
  WorkspaceState/fingerprint, target-digest mismatch, or Project UID mismatch
  denies mutation before the side effect.
- Deleting and recreating a Project with the same name cannot expose or resume
  WorkItems, runs, grants, messages, or checkpoints from the old Project UID,
  even when old-row cleanup failed.
- A crash between filesystem mutation and WorkspaceState CAS leaves a
  fingerprint mismatch that suspends work rather than silently authorizing the
  next mutation.
- Eight mutation-phase iterations without semantic progress stop with a durable
  `no_progress` reason.
- Concurrent WorkItem/grant transitions fail with revision conflict instead of
  overwriting newer state.
- Concurrent Stop-versus-mutation tests cover both fence orderings for source
  write, repository commit, runtime rebuild/promotion, and provider mutation:
  Stop either waits for the already-fenced operation to reach a recorded
  outcome or wins first and prevents it. After Stop returns, the old WorkItem
  cannot mutate and new work may safely activate.
- An unknown non-cancellable external outcome sets
  `reconciliation_required`; no WorkItem activates or mutates until
  provider-specific reconciliation clears it.
- Reusing `clientRequestID` with different content, Turn kind, WorkItem, or run
  returns conflict and cannot transfer authority.
- Empty actor identity and cross-user continue/resume/cancel operations are
  rejected.
- A clean deployment stores grants only as WorkItem-scoped ApprovalGrant
  records and rejects any project-scoped write without Project UID.
- A crash after Kubernetes Project creation but before SQL WorkspaceState
  insertion is recovered by the next fenced `EnsureWorkspaceState`; fault
  injection proves that no assistant record or run starts if initialization
  fails.
- Tenant isolation tests prove that WorkItems, Turns, grants, runs, and
  checkpoints cannot be read or resumed across organization/workspace/project
  scope.
- Existing conversation refresh/reconnect, encrypted-store, approval,
  checkpoint, phase, and plan-dock tests remain green.

## Verification Matrix

| Layer | Required coverage |
| --- | --- |
| Store | WorkItem/Turn/grant/WorkspaceState CRUD, Turn-kind presence constraints, Run/Turn WorkItem equality, atomic CAS transitions, one-active-item index, immutable Project UID, encryption, retention, tenant scope |
| Router | Latest-message authority, proposal without authority, explicit activation/continuation, discussion cannot escalate |
| Context | Only selected WorkItem messages enter mutation runs |
| Authorization | Actor, revision, capability, path, Project UID, WorkspaceState/fingerprint, target digest, consumption, revocation, request idempotency |
| Eino | Exact checkpoint resume, fresh continuation run, todos remain non-authoritative, session-value tampering cannot change authorization |
| Supervisor | Every run terminal outcome updates WorkItem and grants atomically; pending runs block new starts; Stop versus file/commit/runtime/provider mutation orderings are fenced; fault injection proves no partial published transition |
| Recovery | No-progress, iteration exhaustion, stale/recreated Project, workspace-write/CAS crash, unknown external outcome blocks activation until reconciliation |
| Bootstrap | Clean schema requires Project UID, fenced `EnsureWorkspaceState` survives Project/SQL crash boundaries, and grants exist only as WorkItem-scoped ApprovalGrant records |
| Portal | Proposed/active/suspended states, explicit Start/Resume/Stop/Discard, pending conflict, revision conflicts, multi-tab refresh |
| Integration | Reproduce the incident sequence and prove the old quote task cannot enter the theme turn |
