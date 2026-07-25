# App Studio Eino-Native Inner Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ordinary App Studio coding tools direct and add Eino-native retry, tool-history repair, typed iteration handling, and safe model-visible tool failures.

**Architecture:** Existing turn-policy filtering remains the authority over the tool set. App Studio local and graph tools stay static, discovered MCP/provider tools alone carry a searchable marker, and Eino tool search handles only those marked tools. A focused recovery module configures Eino middleware and retry behavior while the existing adapter retains Kedge permission, audit, commit, and development-sync behavior.

**Tech Stack:** Go 1.24, CloudWeGo Eino ADK v0.9.9, Eino OpenAI/Gemini adapters, Kubernetes API errors, standalone App Studio provider tests.

## Global Constraints

- Preserve Kedge tenancy, turn-policy filtering, approved-plan envelopes, permission interrupts, commit approval, runtime permissions, development synchronization, and sequential tool execution.
- Keep all App Studio-owned local and graph tools static after policy filtering.
- Defer only discovered MCP/provider tools; do not enable native model tool search.
- Remove the generic duplicate tool name/description prompt while retaining Databricks safety guidance and discovery-failure guidance.
- Retry at most two times and only for demonstrably transient model failures while the run context remains live.
- Never retry tool execution.
- Preserve Eino interrupt/rerun, cancellation, deadline, stream-cancellation, and Kubernetes forbidden/unauthorized errors.
- Use Eino checkpoints only for in-flight execution; do not replace App Studio durable conversation or audit state.
- Do not add reduction, filesystem middleware, DeepAgent, provider failover, or an outer retry loop.
- Follow red-green TDD and keep each task's diff surgical.

---

### Task 1: Keep App Studio tools direct and defer only provider tools

**Files:**
- Modify: `providers/app-studio/api/assistant_eino_tool.go`
- Modify: `providers/app-studio/api/assistant_eino_engine.go`
- Modify: `providers/app-studio/api/assistant_eino_engine_test.go`
- Modify: `providers/app-studio/api/llm.go`
- Modify: `providers/app-studio/api/assistant_turn_profile_test.go`
- Modify: `providers/app-studio/api/repository_flow_test.go`

**Interfaces:**
- Consumes: `projectAssistantToolsForTurnPolicy`, `projectEinoAssistantTool.Info`, `projectEinoAssistantToolDiscovery.MCPTools`, and Eino `toolsearch.New`.
- Produces: `newProjectEinoAssistantSearchableMCPTool`, `projectEinoToolSearchableExtraKey`, and marker-based `projectEinoAssistantToolSearchSets`.

- [ ] **Step 1: Write failing marker and first-request behavior tests**

Replace the bundle-threshold test with behavior tests that construct production
local tools plus a fake MCP tool:

```go
func TestEinoAssistantToolSearchKeepsAppStudioToolsStatic(t *testing.T) {
    // Build the implementation-profile production toolbox.
    // Assert readiness, list/read/search, write/patch/mkdir, plan approval,
    // commit, runtime, and collaboration tools are all in staticTools.
    // Assert dynamicTools is empty when no MCP tools were discovered.
}

func TestEinoAssistantToolSearchDefersOnlySearchableMCPTools(t *testing.T) {
    local := newProjectEinoAssistantTool(localTool, req, state)
    provider := newProjectEinoAssistantSearchableMCPTool(mcpTool, req, state)
    staticTools, dynamicTools, err := projectEinoAssistantToolSearchSets(
        context.Background(),
        []einotool.BaseTool{local, provider},
    )
    // Assert local is static and provider is dynamic.
}
```

Update the direct-write interrupt test so its scripted model requests
`write_file` on the first model call. Assert the first request contains
`write_file`, excludes `tool_search` when no provider tool exists, and resumes
the approved write exactly once.

Add prompt assertions:

```go
if strings.Contains(prompt, "Available tools in this workspace") {
    t.Fatalf("prompt duplicates the Eino tool catalog: %q", prompt)
}
if strings.Contains(prompt, projectToolReadProjectFile+":") {
    t.Fatalf("prompt duplicates local tool descriptions: %q", prompt)
}
```

Preserve assertions that Databricks guidance and discovery-failure guidance
remain present. Remove builder-prompt expectations requiring `tool_search` or
`select:<tool_name>`.

Update `TestProjectAssistantToolRegistryListsLocalToolsInOrder` to include the
already-registered `projectToolGetProjectCheckpoints` between
`projectToolHydrateWorkspace` and `projectToolCheckProjectBuild`. This is the
single pre-existing baseline failure and does not change production behavior.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestEinoAssistantToolSearchKeepsAppStudioToolsStatic|TestEinoAssistantToolSearchDefersOnlySearchableMCPTools|TestEinoAssistantEngineRequestsPermissionForDirectWriteTool|TestGenerateProjectAssistantStreamIncludesDiscoveredToolPromptOnFirstInput|TestGenerateProjectAssistantStreamDiscoversDatabricksToolsForDataTableQuestions|TestProjectAssistantModePromptsPutBuilderGuidanceOnlyOnWriteProfiles' -count=1
```

Expected: failures show local tools still become dynamic, the searchable-MCP
constructor does not exist, the direct write is not available initially, and
the generic tool catalog is still injected.

- [ ] **Step 3: Add explicit searchable-MCP metadata**

In `assistant_eino_tool.go`, add:

```go
const (
    projectEinoToolParametersExtraKey = "parametersJSON"
    projectEinoToolSearchableExtraKey = "appStudioSearchableMCP"
)
```

Add a `searchableMCP bool` field to `projectEinoAssistantTool`, preserve the
existing constructors as non-searchable, and add:

```go
func newProjectEinoAssistantSearchableMCPTool(
    server *Server,
    tool projectAssistantTool,
    req projectAssistantRunRequest,
    runState *projectEinoAssistantRunState,
) einotool.BaseTool {
    return projectEinoAssistantTool{
        server:        server,
        tool:          tool,
        req:           req,
        runState:      runState,
        searchableMCP: true,
    }
}
```

Set `info.Extra[projectEinoToolSearchableExtraKey] = true` only for marked
wrappers.

Change the tool factory to policy-filter and wrap local tools separately from
`discovery.MCPTools`. Graph and local tools use the normal constructor; MCP
tools use `newProjectEinoAssistantSearchableMCPTool`.

- [ ] **Step 4: Replace threshold partitioning with marker partitioning**

Remove `projectEinoAssistantBundleSearchMinTools`,
`projectEinoAssistantToolCanUseSearch`, and bundle-based decisions. Implement:

```go
func projectEinoAssistantToolUsesSearch(info *schema.ToolInfo) bool {
    if info == nil || info.Extra == nil {
        return false
    }
    searchable, _ := info.Extra[projectEinoToolSearchableExtraKey].(bool)
    return searchable
}
```

`projectEinoAssistantToolSearchSets` calls `Info` once per non-nil tool and
places only marked tools into `dynamicTools`.

- [ ] **Step 5: Remove duplicate catalog prose**

Delete the builder instruction that tells the model to load named App Studio
tools using `tool_search`.

Change `projectMCPToolsPrompt` so it emits no generic catalog. It returns only
the existing Databricks guidance when Databricks tools are present, otherwise
the empty string. Keep `projectMCPToolsFailurePrompt` unchanged.

- [ ] **Step 6: Run focused and package tests and verify GREEN**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestEinoAssistantToolSearchKeepsAppStudioToolsStatic|TestEinoAssistantToolSearchDefersOnlySearchableMCPTools|TestEinoAssistantEngineRequestsPermissionForDirectWriteTool|TestGenerateProjectAssistantStreamIncludesDiscoveredToolPromptOnFirstInput|TestGenerateProjectAssistantStreamDiscoversDatabricksToolsForDataTableQuestions|TestProjectAssistantModePromptsPutBuilderGuidanceOnlyOnWriteProfiles' -count=1
go test ./api -count=1
```

Expected: both commands exit zero.

- [ ] **Step 7: Commit**

```bash
git add providers/app-studio/api/assistant_eino_tool.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_engine_test.go providers/app-studio/api/llm.go providers/app-studio/api/assistant_turn_profile_test.go providers/app-studio/api/repository_flow_test.go
git commit -m "perf(app-studio): keep core assistant tools direct"
```

### Task 2: Repair dangling tool calls and use Eino's iteration sentinel

**Files:**
- Create: `providers/app-studio/api/assistant_eino_recovery.go`
- Create: `providers/app-studio/api/assistant_eino_recovery_test.go`
- Modify: `providers/app-studio/api/assistant_eino_engine.go`
- Modify: `providers/app-studio/api/assistant_eino_engine_test.go`

**Interfaces:**
- Consumes: Eino `patchtoolcalls.New`, `adk.ErrExceedMaxIterations`, and `adk.ChatModelAgentMiddleware`.
- Produces: `projectEinoAssistantPatchToolCallsMiddleware(context.Context)` and typed `projectEinoAssistantMaxIterationsExceeded`.

- [ ] **Step 1: Write failing recovery tests**

Add:

```go
func TestProjectEinoAssistantPatchToolCallsMarksCompletionUnknown(t *testing.T) {
    middleware, err := projectEinoAssistantPatchToolCallsMiddleware(context.Background())
    // Build ChatModelAgentState with an assistant write_file tool call and no
    // following tool result. Invoke BeforeModelRewriteState.
    // Assert a schema.Tool message is inserted with the same tool-call ID and
    // text containing "completion is unknown" and "inspect current".
}

func TestProjectEinoAssistantMaxIterationsExceededUsesSentinel(t *testing.T) {
    if !projectEinoAssistantMaxIterationsExceeded(
        fmt.Errorf("wrapped: %w", adk.ErrExceedMaxIterations),
    ) {
        t.Fatal("wrapped Eino max-iteration sentinel was not recognized")
    }
    if projectEinoAssistantMaxIterationsExceeded(
        errors.New("exceeds max iterations"),
    ) {
        t.Fatal("lookalike string must not be recognized")
    }
}
```

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestProjectEinoAssistantPatchToolCallsMarksCompletionUnknown|TestProjectEinoAssistantMaxIterationsExceededUsesSentinel' -count=1
```

Expected: compilation/test failures show the middleware constructor is absent
and string matching incorrectly accepts the lookalike error.

- [ ] **Step 3: Implement the patch middleware**

Create `assistant_eino_recovery.go` with the project license header and:

```go
func projectEinoAssistantPatchToolCallsMiddleware(
    ctx context.Context,
) (adk.ChatModelAgentMiddleware, error) {
    return patchtoolcalls.New(ctx, &patchtoolcalls.Config{
        PatchedContentGenerator: func(
            _ context.Context,
            toolName string,
            _ string,
        ) (string, error) {
            return "The result for " + toolName +
                " was not recorded. Its completion is unknown; inspect current project or runtime state before retrying.",
                nil
        },
    })
}
```

Construct this middleware in `newAgent` and place it first in `Handlers`, before
summarization and conditional tool search.

- [ ] **Step 4: Replace max-iteration string matching**

Implement:

```go
func projectEinoAssistantMaxIterationsExceeded(err error) bool {
    return errors.Is(err, adk.ErrExceedMaxIterations)
}
```

- [ ] **Step 5: Run focused, interrupt/resume, and package tests**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestProjectEinoAssistantPatchToolCallsMarksCompletionUnknown|TestProjectEinoAssistantMaxIterationsExceededUsesSentinel|TestEinoAssistantEngineResumesApprovedToolThroughTurnLoop|TestEinoAssistantEngineResumesFollowUpThroughTurnLoop' -count=1
go test ./api -count=1
```

Expected: both commands exit zero.

- [ ] **Step 6: Commit**

```bash
git add providers/app-studio/api/assistant_eino_recovery.go providers/app-studio/api/assistant_eino_recovery_test.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_engine_test.go
git commit -m "fix(app-studio): repair incomplete Eino tool history"
```

### Task 3: Retry transient model failures through Eino

**Files:**
- Modify: `providers/app-studio/api/assistant_eino_recovery.go`
- Modify: `providers/app-studio/api/assistant_eino_recovery_test.go`
- Modify: `providers/app-studio/api/assistant_eino_engine.go`
- Modify: `providers/app-studio/api/assistant_eino_engine_test.go`

**Interfaces:**
- Consumes: `adk.ModelRetryConfig`, `adk.RetryContext`,
  `openaimodel.APIError`, `genai.APIError`, and `adk.WillRetryError`.
- Produces: `projectEinoAssistantModelRetryConfig`,
  `projectEinoAssistantShouldRetryModelError`, and
  `projectEinoAssistantWillRetry`.

- [ ] **Step 1: Write failing retry-classification tests**

Add table-driven tests with literal expectations:

```go
tests := []struct {
    name string
    err  error
    want bool
}{
    {"openai 429", &openaimodel.APIError{HTTPStatusCode: 429}, true},
    {"openai 503", &openaimodel.APIError{HTTPStatusCode: 503}, true},
    {"openai 400", &openaimodel.APIError{HTTPStatusCode: 400}, false},
    {"openai 401", &openaimodel.APIError{HTTPStatusCode: 401}, false},
    {"gemini 429", genai.APIError{Code: 429}, true},
    {"gemini 503", genai.APIError{Code: 503}, true},
    {"gemini 403", genai.APIError{Code: 403}, false},
    {"unexpected eof", io.ErrUnexpectedEOF, true},
    {"generic", errors.New("provider failed"), false},
    {"canceled", context.Canceled, false},
    {"deadline", context.DeadlineExceeded, false},
}
```

Test the config behavior as well: retryable error returns `Retry: true`,
non-retryable returns false, canceled retry context returns false, and
`MaxRetries == 2`.

- [ ] **Step 2: Run retry-classification tests and verify RED**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestProjectEinoAssistantShouldRetryModelError|TestProjectEinoAssistantModelRetryConfig' -count=1
```

Expected: compilation failures show the retry helpers are absent.

- [ ] **Step 3: Implement narrow transient retry classification**

Implement `projectEinoAssistantShouldRetryModelError` with `errors.As` for
OpenAI and Gemini API errors, `errors.Is` for `io.ErrUnexpectedEOF` and common
connection-reset/broken-pipe errors, and `net.Error` timeout/temporary checks.
The HTTP retry set is exactly 408, 409, 425, 429, and 500 through 599.

Implement:

```go
func projectEinoAssistantModelRetryConfig() *adk.ModelRetryConfig {
    return &adk.ModelRetryConfig{
        MaxRetries: 2,
        ShouldRetry: func(
            ctx context.Context,
            retryCtx *adk.RetryContext,
        ) *adk.RetryDecision {
            if retryCtx == nil || ctx.Err() != nil ||
                retryCtx.OutputMessage != nil ||
                !projectEinoAssistantShouldRetryModelError(retryCtx.Err) {
                return &adk.RetryDecision{}
            }
            return &adk.RetryDecision{
                Retry:        true,
                RejectReason: "transient model provider failure",
            }
        },
    }
}
```

Wire it through `ChatModelAgentConfig.ModelRetryConfig`.

- [ ] **Step 4: Write failing event/output tests**

Add a scripted streaming model whose first call returns a retryable error before
producing a message and whose second call returns `"accepted response"`.
Assert:

- two model calls occurred;
- the run result is `"accepted response"`;
- `OnChunk` contains only `"accepted response"`; and
- `adk.WillRetryError` does not terminate event collection.

Add exhaustion and permanent-error cases:

```go
// Always transient: three total model calls and final error wraps
// adk.ErrExceedMaxRetries.
// OpenAI 401: one model call and no retry.
```

- [ ] **Step 5: Run event/output tests and verify RED**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestEinoAssistantRetriesTransientModelFailure|TestEinoAssistantExhaustsTransientModelRetries|TestEinoAssistantDoesNotRetryPermanentModelFailure' -count=1
```

Expected: the first retry signal is returned as a fatal run error, exhaustion
does not make three calls, or permanent/transient behavior is not distinguished.

- [ ] **Step 6: Handle retry events and attempt output atomically**

Add:

```go
func projectEinoAssistantWillRetry(err error) bool {
    var retryErr *adk.WillRetryError
    return errors.As(err, &retryErr)
}
```

In `collectProjectAssistantTurnEvents`, continue when `event.Err` is a
`WillRetryError`. If `projectEinoAssistantMessageOutput` returns a
`WillRetryError`, continue draining instead of failing the turn.

Change `projectEinoAssistantMessageOutput` to collect stream chunks without
calling `OnChunk`. After successful EOF and `schema.ConcatMessages`, publish
the concatenated assistant content once. If receive/concat fails, publish
nothing from that attempt and return the error.

Update `TestProjectEinoAssistantMessageOutputPublishesAssistantStreamChunksBeforeEOF`
to assert that no chunk is published before EOF and the accepted concatenated
content is published once after EOF. Rename it to
`TestProjectEinoAssistantMessageOutputPublishesAcceptedStreamAfterEOF`.

- [ ] **Step 7: Run focused and package tests and verify GREEN**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestProjectEinoAssistantShouldRetryModelError|TestProjectEinoAssistantModelRetryConfig|TestEinoAssistantRetriesTransientModelFailure|TestEinoAssistantExhaustsTransientModelRetries|TestEinoAssistantDoesNotRetryPermanentModelFailure|TestProjectEinoAssistantMessageOutput' -count=1
go test ./api -count=1
```

Expected: both commands exit zero.

- [ ] **Step 8: Commit**

```bash
git add providers/app-studio/api/assistant_eino_recovery.go providers/app-studio/api/assistant_eino_recovery_test.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_engine_test.go
git commit -m "fix(app-studio): retry transient Eino model failures"
```

### Task 4: Redact and safely return ordinary tool errors

**Files:**
- Modify: `providers/app-studio/api/assistant_eino_recovery.go`
- Modify: `providers/app-studio/api/assistant_eino_recovery_test.go`
- Modify: `providers/app-studio/api/assistant_eino_engine.go`
- Modify: `providers/app-studio/api/assistant_eino_tool.go`
- Modify: `providers/app-studio/api/assistant_eino_tool_test.go`

**Interfaces:**
- Consumes: Eino invokable/enhanced-invokable middleware endpoints,
  `compose.IsInterruptRerunError`, `adk.ErrStreamCanceled`, and Kubernetes API
  error predicates.
- Produces: `projectEinoAssistantSafeToolErrorMiddleware`,
  `projectEinoAssistantPropagateToolError`, and
  `projectEinoAssistantSafeErrorText`.

- [ ] **Step 1: Write failing sanitizer and middleware tests**

Add table-driven sanitizer tests for Authorization Bearer/Basic, standalone
Bearer values, `api_key`, `access_token`, `token`, `secret`, `password`,
Cookie/Set-Cookie, URL userinfo, and `sk-` values. Assert each secret is absent,
`[REDACTED]` is present, and the result obeys `truncateProjectToolInfo`.

Add middleware tests proving ordinary invokable and enhanced-invokable errors
become successful `Tool call failed:` results.

Add propagation tests for:

```go
context.Canceled
context.DeadlineExceeded
adk.ErrStreamCanceled
apierrors.NewForbidden(k8sschema.GroupResource{Group: "ai.kedge.faros.sh", Resource: "projects"}, "demo", errors.New("denied"))
apierrors.NewUnauthorized("denied")
```

Alias Kubernetes runtime schema as `k8sschema` in the test and use
`k8sschema.GroupResource`. Use a real Eino stateful interrupt to cover the
interrupt/rerun branch.

Add a local-tool adapter test whose error contains:

```text
Authorization: Bearer sk-super-secret
```

Assert both the model-visible result and failed UI event exclude the secret.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestProjectEinoAssistantSafeErrorText|TestProjectEinoAssistantSafeToolErrorMiddleware|TestProjectEinoAssistantSafeToolErrorMiddlewarePropagatesControlFlow|TestProjectEinoAssistantToolRedactsFailedResult' -count=1
```

Expected: compilation/test failures show the sanitizer and middleware are
absent and the adapter exposes the raw secret.

- [ ] **Step 3: Implement bounded credential redaction**

Define package-level compiled regular expressions for:

```text
Authorization Bearer/Basic
standalone Bearer tokens
api_key/access_token/token/secret/password assignments
Cookie and Set-Cookie values
URL userinfo passwords
OpenAI-style sk- tokens
```

Implement:

```go
func projectEinoAssistantSafeErrorText(err error) string {
    if err == nil {
        return ""
    }
    value := err.Error()
    for _, pattern := range projectEinoAssistantSecretPatterns {
        value = pattern.pattern.ReplaceAllString(value, pattern.replacement)
    }
    return truncateProjectToolInfo(value)
}
```

Change `finishFailedToolCall` to sanitize once and use the safe text for both
the UI event and `"Tool call failed: "+safeReason`.

- [ ] **Step 4: Implement Kedge-aware safe tool middleware**

Embed `*adk.BaseChatModelAgentMiddleware` and wrap invokable plus
enhanced-invokable endpoints. Return ordinary failures as bounded, sanitized
tool results.

`projectEinoAssistantPropagateToolError` returns true for:

```go
compose.IsInterruptRerunError(err)
errors.Is(err, context.Canceled)
errors.Is(err, context.DeadlineExceeded)
errors.Is(err, adk.ErrStreamCanceled)
apierrors.IsForbidden(err)
apierrors.IsUnauthorized(err)
```

Append this middleware last in `newAgent` after patching, summarization, and
conditional tool search.

- [ ] **Step 5: Run focused and package tests and verify GREEN**

Run:

```bash
cd providers/app-studio
go test ./api -run 'TestProjectEinoAssistantSafeErrorText|TestProjectEinoAssistantSafeToolErrorMiddleware|TestProjectEinoAssistantSafeToolErrorMiddlewarePropagatesControlFlow|TestProjectEinoAssistantToolRedactsFailedResult|TestEinoAssistantEngineResumesApprovedToolThroughTurnLoop' -count=1
go test ./api -count=1
```

Expected: both commands exit zero.

- [ ] **Step 6: Commit**

```bash
git add providers/app-studio/api/assistant_eino_recovery.go providers/app-studio/api/assistant_eino_recovery_test.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_tool.go providers/app-studio/api/assistant_eino_tool_test.go
git commit -m "fix(app-studio): make Eino tool failures model-safe"
```

## Final Verification

- [ ] **Step 1: Format changed Go files**

Run:

```bash
gofmt -w providers/app-studio/api/assistant_eino_tool.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_recovery.go providers/app-studio/api/assistant_eino_engine_test.go providers/app-studio/api/assistant_eino_recovery_test.go providers/app-studio/api/assistant_eino_tool_test.go providers/app-studio/api/llm.go providers/app-studio/api/assistant_turn_profile_test.go providers/app-studio/api/repository_flow_test.go
```

- [ ] **Step 2: Run complete provider verification**

Run:

```bash
cd providers/app-studio
go test ./... -count=1
go build ./...
go vet ./...
```

Then run from the repository root:

```bash
git diff --check
git status --short
```

Expected: tests, build, vet, and diff check exit zero; status contains only the
intended App Studio and committed design/plan changes.
