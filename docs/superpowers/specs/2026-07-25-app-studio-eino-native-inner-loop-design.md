# App Studio Eino-Native Inner Loop

**Status:** Approved direction, pending implementation

**Date:** 2026-07-25

## Goal

Improve App Studio's normal LLM coding loop by using Eino's native agent
capabilities where they fit, while retaining Kedge-specific authorization,
tenancy, approval, audit, commit, and runtime-verification boundaries.

The implementation must remove unnecessary `tool_search` model rounds from
ordinary App Studio work, recover from transient model failures, repair
incomplete tool-call histories, and return safe tool failures to the model
without weakening interruption or authorization behavior.

## Current Problems

1. Once four non-collaboration tools are available, App Studio defers every
   non-collaboration tool behind Eino's default `tool_search`. This includes
   ordinary project inspection, editing, workflow, runtime, and commit tools.
2. App Studio also injects a system-message catalog containing every allowed
   tool name and description. This duplicates Eino's dynamic-tool discovery
   material without avoiding the search round.
3. `ChatModelAgent` has no `ModelRetryConfig`, so transient provider failures
   fail the run immediately.
4. Event collection treats Eino's `WillRetryError` as fatal and publishes
   streaming chunks before knowing whether Eino will reject and retry the
   attempt.
5. Maximum-iteration detection uses error-string matching instead of Eino's
   exported sentinel.
6. Incomplete assistant tool calls can remain in restored/imported history
   without corresponding tool results.
7. Local and MCP tools convert failures inside the App Studio adapter, but
   graph or future raw Eino tools can still terminate a run. Current error text
   is truncated but not consistently credential-redacted.

## Considered Approaches

### A. Eino-native resilience with Kedge policy adapters — selected

Keep App Studio-owned tools direct, reserve Eino tool search for discovered
provider/MCP tools, add Eino retry and dangling-call middleware, and add one
Kedge-aware safe-error middleware. This removes the common extra model round
without replacing product safety contracts.

### B. Add reduction and filesystem middleware in the same change

This would require a durable, tenant- and run-scoped artifact store, unique
offload keys, bounded readback, retention, quotas, and audit-preserving
rewriters. Eino filesystem write/edit tools would also bypass App Studio's
approved-plan, permission, UI-event, audit, and development-sync path. This
approach is rejected until that storage and authorization contract is designed.

### C. Migrate the coding loop to DeepAgent

DeepAgent adds planning and filesystem capabilities, but its stock mutation
tools do not implement App Studio's product boundaries. It would also expand
the change into a new orchestration model before the existing `ChatModelAgent`
path is made reliable. This approach is rejected for this slice.

## Design

### Tool exposure

Every tool created from App Studio's local registry or graph workflows remains
static after the existing turn-policy filtering. This includes workflow,
workspace read/edit, collaboration, repository, runtime, build, and checkpoint
tools.

Only discovered aggregate MCP/provider tools are marked searchable. The Eino
tool wrapper records an internal `ToolInfo.Extra` marker when wrapping such a
tool. `projectEinoAssistantToolSearchSets` partitions solely on that marker;
there is no tool-count threshold and no classification by risk or bundle.

Eino's default tool-search middleware remains enabled when searchable provider
tools exist. Native model tool search remains disabled because the pinned
OpenAI and Gemini adapters have not been demonstrated to implement Eino's
deferred-tool protocol.

Successful tool discovery no longer injects a generic name/description catalog.
The prompt retains only provider-specific operating guidance that is not
expressed by tool schemas, currently the Databricks data-access safety
contract. Discovery failures continue to produce an explicit system message.
The builder prompt no longer instructs the model to search for named App Studio
tools.

### Middleware order

The `ChatModelAgent` handler chain is:

1. Eino `patchtoolcalls`
2. Eino summarization
3. Eino tool search, only when searchable provider tools exist
4. App Studio safe tool-error middleware

The patch middleware uses a custom tool-result message stating that completion
is unknown and current project/runtime state must be inspected before retrying.
It must not claim an interrupted action was canceled because a side effect may
have completed before its response was lost.

The safe-error middleware wraps invokable and enhanced-invokable tools. It
turns ordinary errors into bounded, redacted `Tool call failed:` results so the
model can react. It propagates Eino interrupt/rerun errors, request
cancellation, deadlines, stream cancellation, and Kubernetes
forbidden/unauthorized errors unchanged.

Local and MCP tools retain their existing adapter-level failure handling because
that layer owns App Studio UI events and durable tool-message recording. Both
paths use the same redaction helper.

### Model retry and output delivery

`ChatModelAgentConfig.ModelRetryConfig` permits at most two retries. It retries
only demonstrably transient failures while the run context remains live:

- OpenAI or Gemini status 408, 409, 425, 429, and 5xx responses;
- temporary or timeout network errors;
- unexpected EOF and connection-reset-style transport failures.

It does not retry generic errors, caller cancellation/deadline, or permanent
400/401/403/404 responses. It does not retry tools or successful-but-suboptimal
model output.

Event collection recognizes `adk.WillRetryError` and continues draining the
agent run. Assistant chunks for an individual model attempt are buffered until
that attempt reaches successful EOF and concatenation. A rejected attempt
therefore emits no user-visible text, and an accepted attempt emits once.

Retry exhaustion remains a normal run error wrapping
`adk.ErrExceedMaxRetries`. No bespoke outer retry loop is added.

### Iteration handling

Maximum-iteration detection uses:

```go
errors.Is(err, adk.ErrExceedMaxIterations)
```

The existing no-tools final-answer fallback remains unchanged. A lookalike
error string must not trigger the fallback.

### State and safety boundaries

Eino checkpoints remain authoritative for in-flight interrupt/resume state.
App Studio's conversation, tool-event, and audit stores remain authoritative
for durable product history.

This change does not alter:

- tenant or workspace resolution;
- turn-policy filtering;
- approved-plan envelopes;
- tool permission interrupts;
- commit approval or repository mutation;
- runtime and infrastructure permissions;
- development synchronization;
- tool execution ordering.

## Deferred Work

Eino reduction and filesystem middleware are deliberately deferred. A later
design may add reduction after App Studio has a durable offload store with:

- tenant/run-scoped encrypted content;
- retry-safe unique keys;
- bounded read-only retrieval;
- retention and quota enforcement;
- checkpoint-compatible references; and
- argument/result rewriting that preserves mutation paths and outcomes.

DeepAgent may be evaluated only after the existing `ChatModelAgent` loop is
measured with these changes.

## Verification

Implementation follows red-green TDD and must demonstrate:

1. App Studio-owned tools are visible on the first model request.
2. Only explicitly marked MCP/provider tools are deferred.
3. Ordinary implementation turns have no `tool_search`.
4. Successful discovery does not inject a duplicate generic tool catalog.
5. Provider-specific Databricks guidance and discovery-failure guidance remain.
6. Permission interrupt/resume executes an approved direct write exactly once.
7. Transient model errors retry within the configured bound; permanent and
   canceled failures do not.
8. Rejected retry output is never sent to the user.
9. Dangling tool calls receive an unknown-completion synthetic result.
10. Safe tool errors redact credentials and preserve Eino/Kubernetes control
    flow.
11. Wrapped `adk.ErrExceedMaxIterations` triggers the existing fallback while a
    matching string does not.
12. Focused tests, the full standalone App Studio provider test suite, provider
    build, lint, and `git diff --check` pass.
