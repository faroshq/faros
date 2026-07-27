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
