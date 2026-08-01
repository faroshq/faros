# App Studio Business-Layer Review and OpenCode V2 Comparison

**Review date:** July 2026  
**App Studio baseline reviewed:** `ea36782e`  
**OpenCode V2 snapshot reviewed:** `a45c2b917e`  
**Current committed kedge baseline:** `5cb6fcf2d1`, which includes the App Studio hardening work, principally in `917ffe48`

## Executive summary

App Studio is moving in the right direction. Its strongest architectural choice is that product policy belongs to App Studio rather than to Eino: durable WorkItems, actor and Project UID scoping, mutation grants, approval modes, lifecycle transitions, and provider boundaries are business concerns. Eino should execute a turn inside those constraints; it should not decide or persist them.

The initial review found five material gaps and a collection of compatibility paths that weakened that boundary. Those findings have since been remediated. The current design now has an App Studio-owned execution authority, a transport-neutral tool port, atomic lifecycle persistence, server-issued bootstrap authority, restart-safe Stop/Resume for durable permission and input checkpoints, and separate discussion history. Active execution interrupted by a provider restart is suspended rather than continued automatically. The obsolete alternate execution and authorization paths were removed.

Relative to OpenCode V2, App Studio is stronger in domain-specific authorization and application lifecycle policy. OpenCode V2 is stronger as a general-purpose agent runtime: durable session input, event replay, context compaction, richer tool infrastructure, and operational telemetry. Its interactive CLI queue remains process-local and is not restart-safe. Those OpenCode-inspired capabilities remain comparison material only; they have not been adopted into App Studio.

## Architectural boundary

The intended ownership split is:

- **App Studio owns business truth:** WorkItems, actors, project identity, approval preferences, mutation authority, grants, lifecycle state, persistence, restart behavior, and user-visible status.
- **Eino owns turn execution:** model interaction, tool-selection flow, interrupt production, and bounded continuation within authority supplied by App Studio.
- **Provider boundaries own side effects:** workspace, git, runtime, and MCP operations remain behind explicit App Studio ports and provider contracts.

The important implementation seams are:

- [`assistant_execution_authority.go`](../providers/app-studio/api/assistant_execution_authority.go) — App Studio-owned execution and mutation authority.
- [`assistant_tool_port.go`](../providers/app-studio/api/assistant_tool_port.go) — tool discovery and invocation without exposing HTTP transport to Eino.
- [`assistant_supervisor.go`](../providers/app-studio/api/assistant_supervisor.go) — in-process run ownership, serialization, cancellation, and snapshot publication.
- [`assistant_checkpoint.go`](../providers/app-studio/api/assistant_checkpoint.go) — durable permission/input checkpoints and guarded resume.
- [`postgres_work_item.go`](../providers/app-studio/store/postgres_work_item.go) — atomic WorkItem, run, and assistant-message transitions.
- [`postgres_bootstrap_permit.go`](../providers/app-studio/store/postgres_bootstrap_permit.go) — server-issued, single-use project bootstrap authority.

## Initial findings and remediation status

### 1. Destructive schema migration

**Original risk:** the WorkItem v2 migration dropped prior messages, runs, and WorkItems before recreating the schema. That was acceptable only under an explicit disposable-data assumption and was unsafe as a normal upgrade.

**Current status:** remediated. PostgreSQL migration is additive. Empty legacy tables can be upgraded in place; populated legacy tables that lack immutable `project_uid` identity stop safely before mutation rather than guessing tenant/project ownership. Migration coverage exercises the upgraded CRUD path when an external PostgreSQL DSN is available.

### 2. Non-atomic terminal lifecycle state

**Original risk:** the WorkItem and run could commit before the assistant message. A message persistence failure could therefore leave a terminal run paired with a durable `Working` message, with no safe way to replay the consumed WorkItem transition.

**Current status:** remediated. Terminal and Stop transitions now persist the run, WorkItem, and active assistant message as one store operation. The supervisor publishes only the committed snapshot.

### 3. Client-controlled bootstrap authority

**Original risk:** a request boolean could identify a turn as the initial project prompt and install broad run-local write authority. The client was effectively asserting a privileged origin condition.

**Current status:** remediated. Project creation issues a private, single-use bootstrap permit bound to Project UID, actor, prompt digest, and client request identity. The first valid build consumes it; callers cannot manufacture bootstrap authority by setting a public flag.

### 4. Leaky Eino boundary

**Original risk:** the engine request exposed HTTP, store scope, and supervisor details. Eino-specific code could directly load WorkItems, persist grants, and invoke lifecycle mutation admission.

**Current status:** remediated. The execution-authority interface owns WorkItem context, plan installation and retirement, mutation admission, promotion, and checkpoint/audit/run persistence. The supervisor and accumulator decide and atomically commit terminal lifecycle outcomes. The tool port owns MCP discovery and invocation. Eino consumes these capabilities without owning their storage or transport implementation.

### 5. Discussion continuity lost to execution isolation

**Original risk:** non-WorkItem runs loaded only the current user message. This prevented mutation authority from leaking across runs, but ordinary read-only discussion also forgot prior context.

**Current status:** remediated. Discussion history is loaded separately from WorkItem execution history. Read-only conversational continuity no longer implies inherited mutation authority.

## Vestigial paths removed

The cleanup removed alternate execution and authorization models that competed with the durable WorkItem design:

- The synthetic project-wide `approved-plan-grant` run and its load/save/retire implementation.
- Engine fallback execution without a real durable run.
- Mutation admission for nil or mode-less compatibility runs.
- Legacy project/message POST-SSE endpoints and their portal parsers.
- The pre-supervisor streaming/persistence path.
- The duplicate `/abort` lifecycle; idempotent `/stop` is authoritative.
- Paused-run normalization that inferred identity for legacy rows.
- Validation that accepted missing run origin or mode.
- The environment-level auto-approval bypass.
- Cancellation receipts stored inside authorization grants.
- Obsolete assistant-run CAS and reservation scaffolding.

This matters beyond code size. Each removed path was a second answer to one of the core questions—who owns a run, what authorizes a mutation, or which state is durable. A bulletproof business layer needs one answer to each.

## Additional hardening completed

- Stop and Resume work after provider restart by reattaching durable pending runs to the supervisor.
- Stop and Resume serialize through the same transition lock and revision-checked snapshot write, preventing stale overwrites.
- Cancellation clears the active plan grant and records idempotency separately.
- Adaptive runs promote to WorkItems only through the execution authority.
- Malformed or stale resume requests fail before claiming or terminalizing a valid checkpoint.
- Concurrent Eino callbacks are serialized and closed before terminal processing, eliminating a race found by `go test -race`.
- Approval mode now defaults to `auto_approve` for new users/projects. Explicit `always_ask` preferences remain durable, while legacy runs missing the field retain the conservative interpretation.

## Relative position versus OpenCode V2

### Where App Studio is ahead

App Studio has a stronger domain-specific control plane:

- Durable, actor-bound WorkItems rather than generic session intent.
- Immutable Project UID isolation, protecting against reused project names.
- Path- and capability-scoped mutation grants.
- Durable permission and follow-up checkpoints.
- Explicit plan → mutate → verify → commit lifecycle policy.
- Provider-isolated runtime and git boundaries suitable for BYO compute.
- Conservative retry behavior for uncertain side effects.
- Atomic linkage between execution state and the user-visible assistant message.

These are not incidental features. They encode application-delivery policy that should not be delegated to a general agent engine.

### Where App Studio remains behind

| App Studio deficit | OpenCode V2 capability | Why it matters |
|---|---|---|
| No durable prompt inbox or defined mid-run input policy | Durable user-message insertion can influence an active session loop; OpenCode's interactive CLI queue is process-local | App Studio needs an explicit durable admission contract before it can safely accept intent during active work. |
| Snapshot-oriented reconnect and audit | Ordered durable event journal with replay cursors | Reconnect, debugging, projections, and external consumers can reconstruct exact history. |
| Run-local summarization | Durable, model-budget-aware context compaction and overflow recovery | Long-lived work can continue without silently losing the reasoning context required for correctness. |
| No general command harness | Bounded subprocess execution with timeout, cancellation, cleanup, and permission policy | Build, test, and lint become first-class verified actions rather than bespoke integrations. |
| Narrow patch mechanics | Multi-file patch preflight, partial-application reporting, snapshots, and revert | Larger changes become safer and easier to review or undo. |
| Mostly input-oriented tool schemas | Input/output validation, stale-registration rejection, and retained oversized output handling | Tool contracts fail earlier and remain diagnosable across version changes. |
| Limited model/tool telemetry | Token and cost accounting, reasoning/provider metadata, tool settlement, duration, and changed-file records | Operators can diagnose quality, latency, and cost rather than treating a run as a black box. |
| Project-level provider settings | Model/provider catalog, variants, and credential resolution | Model portability and policy can evolve without coupling projects to one configuration shape. |

## Recommended parity roadmap

These items were deliberately deferred and should be treated as separate product decisions, not implied follow-up work:

1. **Sandbox-runtime command execution.** Add build/test/lint execution only inside the project sandbox or runtime provider. Never expose an unsandboxed provider-host shell.
2. **Durable event journal and prompt inbox.** Define queue/steer semantics on durable admission and replay, ideally scoped to WorkItems rather than copying OpenCode's process-local interactive queue or a generic session abstraction wholesale.
3. **Token-budget-aware context compaction and provenance.** Persist compaction state and introduce an App Studio context-epoch contract so restart and replay preserve which privileged instructions and environment shaped each turn. Context epochs are a proposed App Studio concept, not an OpenCode V2 feature.
4. **File-change snapshots and guarded revert.** Capture diffs at the workspace boundary, present them to the user, and make revert an explicit authorized operation.
5. **Operational telemetry.** Persist model calls, token/cost information, retry decisions, tool duration, and side-effect settlement.
6. **Model catalog and credential-resolution seam.** Put provider/model selection behind the existing Eino model factory without moving application policy into Eino.

## Important caveats

- OpenCode V2 is useful reference material, not a finished gold standard. At the reviewed snapshot it still lacked complete clustered execution ownership, safe automatic post-crash continuation, and a comprehensive provider timeout/retry policy.
- OpenCode's interactive prompt queue was process-local at the reviewed snapshot. It must not be described as durable queue/steer delivery.
- The OpenCode comparison was source-based. Its Bun test suite was not executed in the review environment because Bun was unavailable.
- App Studio's in-memory/unit, race, vet, portal, and provider-build checks passed after remediation. External PostgreSQL integration coverage was skipped when `APP_STUDIO_TEST_POSTGRES_DSN` was unset.
- This document describes the reviewed snapshots and the committed remediation. Before using it as a release sign-off, revalidate the final commit and production migration environment.

## Bottom line

App Studio now has the right architectural center: durable application and authorization policy above Eino, with Eino acting as an execution engine rather than the system of record. The known alternate authorization and lifecycle paths have been removed, and no concrete vestigial path remained after independent review.

The remaining gap versus OpenCode V2 is primarily runtime substrate, not business correctness. App Studio does not need to copy OpenCode's session model. It should selectively adopt durable admission/replay, context management, command and patch ergonomics, and observability while preserving the stronger WorkItem and provider-isolation model it already has.
