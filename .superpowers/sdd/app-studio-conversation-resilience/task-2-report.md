# Task 2 report — server-owned assistant supervisor

## Files

- `providers/app-studio/api/assistant_supervisor.go` — lifecycle-owned workers,
  serialized revision transitions, capacity-one subscribers, durable snapshots,
  terminal abort and persistence-failure handling.
- `providers/app-studio/api/assistant_supervisor_http.go` — start, latest,
  reconnectable snapshot SSE, orphan reconciliation, and start responses.
- `providers/app-studio/api/server.go`, `projects.go` — route registration,
  asynchronous resume and abort dispatch, request id.
- `providers/app-studio/api/assistant_supervisor_test.go` — detached starter,
  coalescing subscriber, duplicate worker, and terminal abort tests.

## RED / GREEN evidence

RED: `go test ./api -run TestProjectAssistantSupervisor -count=1` initially
failed with undefined `newProjectAssistantSupervisor` and
`projectAssistantSnapshotAccumulator` after adding the first two behavioural
tests.

GREEN: the same focused suite now passes, including the added duplicate-start
and late-completion-after-abort cases.

## Verification

- `go test ./api -count=1` — pass.
- `go test -race ./api -run TestProjectAssistantSupervisor -count=1` — pass.
- `go test ./...` from `providers/app-studio` — pass.
- `git diff --check` — pass.

## Self-review

An independent review identified duplicate worker, abort overwrite, and CAS
ordering hazards. The supervisor now permits one worker per run, serializes
run transitions with a per-run lock, makes terminal state immutable, and saves
before publishing. Starts return the existing durable run on idempotent
conflict. Orphan reconciliation runs before start/latest.

## Concerns

The existing Eino audit/checkpoint code still owns portions of checkpoint and
audit persistence. The supervisor preserves those existing paths while
persisting the display snapshot, but a follow-up should consolidate those
writes behind the same transition accumulator to eliminate legacy write paths.

## Follow-up integration evidence

The follow-up routes a supervisor-owned run through the Eino request: the
engine reuses its durable run instead of creating another audit row; audit
finalization writes its encrypted audit blob through `UpdateRun`; permission
and input checkpoints write `status`, `requestID`, and the opaque checkpoint
payload through the same serialized snapshot transition. Fresh and resumed
tool/action callbacks update only the existing sanitized message metadata.

Verification after this integration:

- `go test ./api -count=1` — pass.
- `go test -race ./api -run TestProjectAssistantSupervisor -count=1` — pass.
- `go test ./...` from `providers/app-studio` — pass.
- `git diff --check` — pass.

Self-review: terminal statuses remain immutable in the accumulator, so Eino
completion cannot overwrite an abort. Checkpoint and audit payloads remain on
the existing encrypted store boundary; the supervisor changes only which store
write operation owns their durable revision transition.

## Fix round 1

Pending worker ownership is released after a segment returns, so resume may
claim the persisted pending run exactly once instead of being rejected by an
old in-memory worker flag. Resume no longer writes `running` before the legacy
claim operation. The supervisor now maintains committed and working snapshots:
new subscribers receive only a successfully persisted revision, and a failed
write reconstructs `failed` from the last committed revision before attempting
its terminal save.

Fresh verification: `go test ./api -count=1`, `go test -race ./api -run
TestProjectAssistantSupervisor -count=1`, and `go test ./...` all passed.

## Partial fix-round checkpoint

This checkpoint adds a real trailing text flush (rather than dropping a quiet
coalesced update), binds production worker ownership to the provider signal
context through `NewWithWorkspaceContext`, and persists `interrupted` before
HTTP shutdown/store teardown. It also adds a supervisor-aware resume/checkpoint
write gate, immediate sanitized resume action/permission/input metadata
snapshots, committed-vs-working snapshots for subscriber visibility, and
ownership tests for pending segment release and restart attach.

RED/GREEN: the new supervisor ownership and lifecycle tests initially required
the missing lifecycle/ownership behavior; after the changes the focused
`TestProjectAssistantSupervisor|TestResumeProjectAssistant` suite passes.
Full verification for this checkpoint passed:

- `go test ./api -run 'TestProjectAssistantSupervisor|TestResumeProjectAssistant' -count=1`
- `go test ./api -count=1`
- `go test -race ./api -run TestProjectAssistantSupervisor -count=1`
- `go test ./...`

Remaining: route-level resume and broader HTTP lifecycle coverage is not yet
implemented. The attempted fixture route is the real GraphQL client pattern:
an `httptest` GraphQL endpoint must return both `ProjectYaml` and the settings
secret response consumed by `readProjectLLMSettings`, with a blocking fake
assistant engine and a valid Eino checkpoint. No test-only production seam was
introduced while tracing that contract.

## Fix round 2

This round closes the remaining supervisor and HTTP lifecycle findings. Parent
signal cancellation now invokes durable supervisor shutdown before workers are
cancelled, so active runs become `interrupted`, not `aborted`. Resume claims
are serialized and published as a committed `running` revision, and
supervisor-owned resume message/audit/checkpoint updates use the accumulator.

Reconnect cursors at an already-terminal committed revision now return a
closed channel rather than a nil channel (which kept HTTP SSE handlers alive),
and the SSE route reconciles a true post-restart orphan before emitting it.
Attached-run abort now writes terminal audit state and removes the active
pending interrupt in its same snapshot transition. The text flush saves the
latest working content at persist time rather than a stale captured string.
Start responses now find the user immediately preceding their active assistant
message in the store's ascending message order.

RED/GREEN coverage added:

- terminal cursor subscriptions close immediately;
- serialized resume claim emits a durable `running` revision;
- parent cancellation persists `interrupted`;
- attached abort persists aborted audit and clears its interrupt metadata;
- a deterministic timer/chunk interleave retains newer trailing text;
- start response returns its matching user message;
- restarted SSE reconciles `running` to `interrupted`;
- a real HTTP GraphQL fixture (`ProjectYaml` plus `SecretYaml`) proves route
  resume detaches from the initiating request, emits `running`, and responds
  to explicit abort.

Verification:

- `go test ./api -count=1` — pass.
- `go test -race ./api -run TestProjectAssistantSupervisor -count=1` — pass.
- `go test ./...` from `providers/app-studio` — pass.
- `git diff --check` — pass.

Residual concern: legacy non-supervisor callers retain their existing direct
store fallback for compatibility; all HTTP-supervised execution takes the
serialized transition path.

Fresh review then found two ordering/lifecycle defects and they were amended:
final resumed assistant message cleanup now happens before the terminal
snapshot, and an aborted paused run is removed from the supervisor map so a
new turn can start without a process restart. Focused supervisor/resume and
full API tests passed after that amendment.
