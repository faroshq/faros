# Task 1 report — durable store contract

## Scope

Implemented the Task 1 durable `AssistantRun` store contract. Production changes
are confined to `providers/app-studio/store`; the only API change updates an
existing test fixture so independent assistant runs no longer share a tenant /
project scope under the new one-nonterminal-run invariant.

## Files changed

- `providers/app-studio/store/store.go`
  - Added `ClientRequestID`, `ActiveMessageID`, and `Revision`.
  - Added `failed` and `interrupted` terminal statuses.
  - Added atomic create/snapshot-save and run lookup operations to `Store`.
- `providers/app-studio/store/memory.go`
  - Added atomic idempotent creation, revision CAS snapshot saving, lookups, and
    one-nonterminal-run enforcement for both new and legacy writers.
  - Preserves immutable client-request identity, creation time, and revision.
- `providers/app-studio/store/postgres.go`
  - Added v4 additive migration, scoped client-request and active-run indexes,
    legacy nonterminal interruption, transactional create/snapshot operations,
    lookup methods, and conflict normalization.
- `providers/app-studio/store/encryption.go`
  - Applied existing encryption behavior to the new atomic create/snapshot and
    lookup operations.
- `providers/app-studio/store/assistant_run_contract_test.go`
  - Added memory/encrypted contract tests for atomic creation, duplicate
    recovery, active conflicts, concurrent creation, revision CAS, immutable
    identity, lookups, and encrypted snapshots.
- `providers/app-studio/store/postgres_test.go`
  - Added external-Postgres contract/index and legacy-v3-pending migration
    fixtures.
- `providers/app-studio/store/store_test.go`
  - Updated an older retention fixture to comply with one active run.
- `providers/app-studio/api/assistant_eino_engine_test.go`
  - Isolated independent profile subtests by project scope; this preserves the
    test's intended tool-inventory coverage under the store invariant.

## RED / GREEN evidence

Initial RED (before durable methods existed):

```text
go test ./store -run 'Test(MemoryStoreImplementsDurableAssistantRunContract|EncryptedStoreImplementsDurableAssistantRunContract)' -count=1
FAIL: MemoryStore does not implement durable assistant-run store contract
FAIL: encrypted store does not implement durable assistant-run store contract
```

Review-driven RED (before parity fixes):

```text
go test ./store -run 'TestMemoryStore(LegacyWritersRejectSecondNonterminalAssistantRun|SnapshotPreservesClientRequestAndCreationTime)' -count=1
FAIL: second SaveAssistantRun error = <nil>, want conflict
FAIL: snapshot changed immutable run identity
```

GREEN after implementation and review fixes:

```text
go test ./store -count=1
ok github.com/faroshq/provider-app-studio/store

go test -race ./store -count=1
ok github.com/faroshq/provider-app-studio/store

go test ./api -run TestEinoAssistantEngineUsesScopedCanonicalFilesystemReads -count=1
ok github.com/faroshq/provider-app-studio/api

go test ./...
ok github.com/faroshq/provider-app-studio
ok github.com/faroshq/provider-app-studio/api
ok github.com/faroshq/provider-app-studio/client
ok github.com/faroshq/provider-app-studio/store
ok github.com/faroshq/provider-app-studio/workspace

git diff --check
exit 0
```

The external Postgres tests are present but skipped locally because
`APP_STUDIO_TEST_POSTGRES_DSN` is unset. Their fixture verifies the v4 indexes,
idempotent create/CAS behavior, and migration of a legacy pending row to
`interrupted`.

## Self-review

- Atomicity: Postgres create and snapshot operations use one transaction; memory
  uses one mutex critical section; the encrypted wrapper prepares all encrypted
  values before calling the single inner operation.
- Idempotency and exclusivity: the scoped partial unique indexes and the memory
  lock provide equivalent duplicate-recovery and one-active-run outcomes.
- Migration: v4 is additive and changes every legacy nonterminal run to
  `interrupted`, preventing invalid legacy rows from blocking a new durable run.
- Immutability: snapshot saves preserve the original client request ID and
  creation time across memory and Postgres.
- Retention/deletion: existing store retention and project deletion continue to
  remove assistant runs; their tests remain green.

## Concerns

- The external-Postgres migration/index tests require an explicitly configured
  `APP_STUDIO_TEST_POSTGRES_DSN` and could not run in this workspace.
- Task 2 must switch the assistant execution lifecycle to the new atomic store
  operations; this task intentionally does not change API, supervisor, or
  portal behavior.

## Review fix round 1

### Findings fixed

- Memory idempotent recovery now performs a complete client-request lookup
  before it evaluates active-run exclusivity. A retry for a completed request
  deterministically returns that completed run even while a newer run is active.
- Durable encrypted-create and encrypted-snapshot tests now verify encrypted
  user and assistant message content in the raw inner store, as well as
  decrypted wrapper reads.
- Durable `DeleteProjectMessages` tests now cover memory, encrypted, and
  DSN-gated Postgres stores for both messages and assistant runs.

### RED / GREEN evidence

Focused RED before the retry-ordering fix:

```text
go test ./store -run TestMemoryStoreRecoversCompletedRequestWhileAnotherRunIsActive -count=1
FAIL: retry completed request on attempt 0: assistant run version conflict:
project already has active assistant run "run-2"
```

Focused GREEN after the two-pass lookup change and added encryption/deletion
coverage:

```text
go test ./store -run 'Test(MemoryStoreRecoversCompletedRequestWhileAnotherRunIsActive|MemoryAndEncryptedStoresDeleteDurableRunAndMessages|EncryptedStoreEncryptsDurableAssistantRunSnapshots)' -count=1
ok github.com/faroshq/provider-app-studio/store

go test ./store -count=1
ok github.com/faroshq/provider-app-studio/store

go test -race ./store -count=1
ok github.com/faroshq/provider-app-studio/store

go test ./...
ok github.com/faroshq/provider-app-studio
ok github.com/faroshq/provider-app-studio/api
ok github.com/faroshq/provider-app-studio/client
ok github.com/faroshq/provider-app-studio/store
ok github.com/faroshq/provider-app-studio/workspace

git diff --check
exit 0
```

The expanded Postgres deletion assertion remains DSN-gated alongside the
existing Postgres migration/index coverage and was not runnable locally because
`APP_STUDIO_TEST_POSTGRES_DSN` is unset.

## Review fix round 2

### Finding fixed

`MemoryStore.SaveAssistantRun` now rejects a second, different run with the
same non-empty `ClientRequestID`, matching the Postgres scoped unique index.
The guard runs under the existing store mutex and excludes the same run ID, so
normal updates remain valid. `encryptedStore.SaveAssistantRun` delegates to the
same inner-store check; the regression covers both wrappers.

### RED / GREEN evidence

Focused RED before the memory parity fix:

```text
go test ./store -run TestMemoryAndEncryptedStoresRejectDuplicateLegacyClientRequestID -count=1
FAIL: duplicate SaveAssistantRun error = <nil>, want conflict
```

Focused GREEN after adding the locked duplicate client-request check:

```text
go test ./store -run TestMemoryAndEncryptedStoresRejectDuplicateLegacyClientRequestID -count=1
ok github.com/faroshq/provider-app-studio/store
```
