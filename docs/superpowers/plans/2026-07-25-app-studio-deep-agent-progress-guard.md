# App Studio DeepAgent Progress Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent mutation-capable App Studio DeepAgent turns from terminating with analysis-only prose while required approval, mutation, verification, or commit work remains.

**Architecture:** Extend Eino v0.9.9's `ModelRetryConfig.ShouldRetry` to reject one successful no-tool response in a nonterminal mutation phase, retry with a non-persistent phase reminder, and require a tool call through Eino model options. Tighten the existing phase middleware so mutate and verify phases expose only actions that advance their lifecycle. Restore DeepAgent's built-in instruction separately by appending the App Studio-specific contract through `BeforeAgent` middleware, allowing its behavioral effect to be measured independently.

**Tech Stack:** Go, Eino ADK v0.9.9, Eino DeepAgent, Go `testing`, App Studio local-dev E2E.

## Global Constraints

- Keep `github.com/cloudwego/eino` at v0.9.9.
- Preserve App Studio's tenant-scoped tool wrappers, approval interrupts, audit events, checkpoint/resume behavior, and post-write development synchronization.
- Do not add DeepAgent filesystem, shell, streaming shell, default general subagent, or custom subagents.
- Use Eino `ModelRetryConfig.ShouldRetry`, `RetryDecision.ModifiedInputMessages`, and `model.WithToolChoice`; do not add a bespoke outer model loop.
- Retry a successful analysis-only model response at most once per model invocation.
- Accept prose normally for read-only profiles and the terminal report phase.
- Do not treat `write_todos` as mutation authorization or durable cross-turn state.
- Do not introduce token, output, wall-clock, or arbitrary tool-call budgets.
- Keep the changes surgical and test-first.

---

### Task 1: Reject analysis-only output in nonterminal mutation phases

**Files:**
- Modify: `providers/app-studio/api/assistant_eino_recovery.go`
- Modify: `providers/app-studio/api/assistant_eino_recovery_test.go`
- Modify: `providers/app-studio/api/assistant_eino_engine.go`

**Interfaces:**
- Consumes: `projectEinoAssistantPhaseForState`, `projectEinoAssistantPhaseLifecycleApplies`, `projectAssistantRunRequest`, `projectEinoAssistantRunState`, Eino `adk.RetryContext`, and Eino model options.
- Produces: `projectEinoAssistantModelRetryConfig(req projectAssistantRunRequest, runState *projectEinoAssistantRunState) *adk.ModelRetryConfig`.

- [ ] **Step 1: Write the failing semantic-retry tests**

  Add table-driven tests that call the real retry decision callback and prove:

  - An implementation turn in `approval` with a successful prose-only response retries.
  - A successful response containing a tool call is accepted.
  - A discussion turn's prose-only response is accepted.
  - A mutation turn in terminal `report` accepts prose.
  - A second semantic retry attempt is accepted rather than looping.
  - Existing transient provider error retry behavior remains unchanged.

  For the retry case, assert `Retry=true`, `RejectReason` identifies incomplete phase progress, `ModifiedInputMessages` contains the original input plus a phase-specific system reminder, `PersistModifiedInputMessages=false`, and `AdditionalOptions` contains forced tool choice.

- [ ] **Step 2: Run the focused test and confirm the intended failure**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'TestProjectEinoAssistantModelRetry' -count=1
  ```

  Expected: FAIL because successful model output is currently always accepted.

- [ ] **Step 3: Implement the minimal Eino-native semantic retry**

  Change the retry factory to receive `req` and `runState`. Preserve the existing transient-error branch. When `retryCtx.OutputMessage` is successful, contains no tool calls, the mutation lifecycle applies, the derived phase is not `report`, and `RetryAttempt == 1`, return a retry decision with:

  - The original input messages plus one non-persistent system reminder naming the active phase and required next action.
  - `model.WithToolChoice(schema.ToolChoiceForced)` as an additional option.
  - Zero backoff for this semantic correction.
  - A structured reject reason suitable for callback/log attribution.

  Do not reject empty prose that contains tool calls, read-only output, report output, or later retry attempts.

- [ ] **Step 4: Run the focused tests**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'TestProjectEinoAssistantModelRetry' -count=1
  ```

  Expected: PASS.

### Task 2: Make mutation and verification phases directional

**Files:**
- Modify: `providers/app-studio/api/assistant_eino_phase.go`
- Modify: `providers/app-studio/api/assistant_eino_phase_test.go`
- Modify only directly affected engine tests in `providers/app-studio/api/assistant_eino_engine_test.go`.

**Interfaces:**
- Consumes: existing phase derivation and tool metadata.
- Produces: phase-specific tool exposure in `projectEinoAssistantPhaseAllowsTool`.

- [ ] **Step 1: Change the phase tests first**

  Assert:

  - `approval` retains read, input, and plan tools but hides write, runtime-mutation, and commit tools.
  - `mutate` exposes approved workspace write operations, `ask_follow_up`, and eligible `write_todos`; it hides broad workspace reads, workflow reads, runtime tools, `tool_search`, plan approval, and commit.
  - `verify` exposes only `verify_development_runtime`.
  - `repair` retains targeted workspace reads, writes, runtime diagnosis/repair, the verifier, `ask_follow_up`, and eligible `write_todos`.
  - `commit` exposes only commit.
  - `report` exposes no tools.

- [ ] **Step 2: Run the phase tests and confirm they fail against the permissive implementation**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'TestProjectEinoAssistantPhase' -count=1
  ```

  Expected: FAIL because mutate and verify currently expose non-directional tools.

- [ ] **Step 3: Implement the narrow predicates**

  Use existing tool risk and bundle metadata. Keep authorization enforcement in the tool wrapper. Do not create another phase state machine or counter.

- [ ] **Step 4: Run focused phase and engine tests**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'Test(EinoAssistantEngine|ProjectEinoAssistantPhase)' -count=1
  ```

  Expected: PASS.

### Task 3: Restore DeepAgent's built-in instruction without losing App Studio rules

**Files:**
- Modify: `providers/app-studio/api/assistant_eino_engine.go`
- Modify: `providers/app-studio/api/assistant_eino_engine_test.go`

**Interfaces:**
- Produces: `projectEinoAssistantInstructionMiddleware() adk.ChatModelAgentMiddleware`.

- [ ] **Step 1: Write a failing middleware/engine test**

  Prove that the App Studio phase contract is appended to a pre-existing instruction rather than replacing it, and that a constructed agent receives the App Studio contract while `deep.Config.Instruction` is left empty.

- [ ] **Step 2: Run the focused test and confirm failure**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'TestEinoAssistantEngine.*Instruction' -count=1
  ```

  Expected: FAIL because App Studio currently supplies its contract directly as DeepAgent's replacement instruction.

- [ ] **Step 3: Add a small `BeforeAgent` instruction middleware**

  Leave `deep.Config.Instruction` empty so Eino selects its built-in prompt. Append only the existing App Studio-specific approval/phase contract in `BeforeAgent`. Do not copy or vendor Eino's built-in prompt.

- [ ] **Step 4: Run focused tests**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'Test(EinoAssistantEngine|ProjectEinoAssistantPhase|ProjectEinoAssistantModelRetry)' -count=1
  ```

  Expected: PASS.

### Task 4: Verify locally and repeat the iterative project flow

**Files:**
- Modify only files required by failures caused by Tasks 1-3.

**Interfaces:**
- Consumes: completed semantic retry, directional phase filtering, and additive DeepAgent instruction.
- Produces: verified provider build and local-dev behavior evidence.

- [ ] **Step 1: Format and run the complete provider suite**

  Run:

  ```bash
  gofmt -w providers/app-studio/api/assistant_eino_recovery.go providers/app-studio/api/assistant_eino_recovery_test.go providers/app-studio/api/assistant_eino_phase.go providers/app-studio/api/assistant_eino_phase_test.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_engine_test.go
  cd providers/app-studio
  go test ./... -count=1
  ```

  Expected: PASS.

- [ ] **Step 2: Build the standalone provider**

  Run:

  ```bash
  make build-app-studio-provider
  ```

  Expected: PASS.

- [ ] **Step 3: Refresh local development and run a fresh initial todo-app build**

  Run the provider through the existing Tilt local-dev stack and create a fresh project using the established todo-list prompt. Capture total time, model rounds, action kinds, first mutation time, terminal phase, runtime readiness, and preview reachability.

- [ ] **Step 4: Replay the five iterative improvement prompts**

  Replay Important/star filter, delete undo, keyboard shortcuts, Complete visible, and overdue filter/sort against the same project. Success requires actual workspace mutation and verification for each explicit implementation turn; prose-only completion is a failure.

- [ ] **Step 5: Inspect the final diff and request independent review**

  Run:

  ```bash
  git diff --check
  git status --short
  ```

  The reviewer must inspect semantic-retry termination, streaming retry behavior, checkpoint/resume compatibility, phase tool availability, approval safety, prompt ordering, and missing regression tests.

