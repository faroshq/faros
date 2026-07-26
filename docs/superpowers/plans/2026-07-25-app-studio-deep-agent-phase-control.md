# App Studio DeepAgent Phase Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run App Studio's mutation-capable turns through Eino DeepAgent while enforcing an App Studio-owned assess, approve, mutate, verify, commit, and report lifecycle.

**Architecture:** `deep.New` replaces the direct `adk.NewChatModelAgent` constructor but retains App Studio's existing tools, retry policy, checkpointing, approval wrappers, reduction, summarization, and tool-search middleware. A final `ChatModelAgentMiddleware` derives the current phase from Eino's persisted message history plus App Studio's approved-plan state, then filters `ToolInfos` so the model cannot repeat plan approval, commit unverified work, or continue using tools after completion. DeepAgent filesystem, shell, and default general subagent capabilities remain disabled because they would bypass App Studio's tenant, permission, audit, and workspace-sync boundaries.

**Tech Stack:** Go, Eino ADK v0.9.9, Eino `adk/prebuilt/deep`, App Studio GraphTools, Go `testing`.

## Global Constraints

- Keep `github.com/cloudwego/eino` at v0.9.9.
- Preserve App Studio's existing `projectEinoAssistantTool` wrapper for every project, runtime, and repository mutation.
- Do not register DeepAgent filesystem or shell tools.
- Set `WithoutGeneralSubAgent: true`; no generic subagent may inherit App Studio mutation tools.
- Keep deterministic readiness and runtime verification in the existing Eino GraphTools.
- Keep discussion, guidance, exploration, and read-only debugging behavior unchanged.
- Enable `write_todos` only for mutation-capable turns, and expose it only when the active approved plan contains multiple steps.
- Do not introduce a token, output, or tool-call budget as a substitute for phase transitions.

---

### Task 1: Add phase derivation and tool filtering

**Files:**
- Create: `providers/app-studio/api/assistant_eino_phase.go`
- Create: `providers/app-studio/api/assistant_eino_phase_test.go`

**Interfaces:**
- Consumes: `projectEinoAssistantRunState.ApprovedPlan()`, `projectAssistantRunRequest.InitialApprovedPlan`, `projectAssistantToolRisk`, `projectAssistantToolBundle`, and Eino `adk.ChatModelAgentState`.
- Produces: `projectEinoAssistantPhaseMiddleware(req, runState) adk.ChatModelAgentMiddleware` and deterministic phase/tool-selection helpers.

- [ ] **Step 1: Write failing tests for phase derivation**

  Cover these message-history states:

  - No approved plan on an implementation turn produces `approval`.
  - An approved plan without a successful write produces `mutate`.
  - A successful workspace write after the latest verification produces `verify`.
  - A non-ready verification after the latest write produces `repair`.
  - A reachable verification after the latest write produces `commit` for normal turns and `report` for initial project creation.
  - A successful commit produces `report`.
  - A later successful write invalidates an earlier successful verification and returns to `verify`.

- [ ] **Step 2: Run the phase tests and confirm the missing implementation failure**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'TestProjectEinoAssistantPhase' -count=1
  ```

  Expected: build failure because the phase middleware and phase constants do not exist.

- [ ] **Step 3: Implement message-history phase derivation**

  Add a small phase enum:

  ```go
  type projectEinoAssistantPhase string

  const (
      projectEinoAssistantPhaseApproval projectEinoAssistantPhase = "approval"
      projectEinoAssistantPhaseMutate   projectEinoAssistantPhase = "mutate"
      projectEinoAssistantPhaseVerify   projectEinoAssistantPhase = "verify"
      projectEinoAssistantPhaseRepair   projectEinoAssistantPhase = "repair"
      projectEinoAssistantPhaseCommit   projectEinoAssistantPhase = "commit"
      projectEinoAssistantPhaseReport   projectEinoAssistantPhase = "report"
  )
  ```

  Scan `state.Messages` for successful App Studio tool results. Track the latest successful edit, `verify_development_runtime`, and `commit_project_files` indices. Treat tool results beginning with App Studio's failed, denied, or permission-barrier text as unsuccessful. Decode the verification JSON and treat only `reachable`, `ready`, and `available` as successful terminal verification states.

- [ ] **Step 4: Write failing tests for phase-specific tool exposure**

  Construct `schema.ToolInfo` values with the same `risk` and `bundle` metadata that `projectEinoAssistantTool.Info` emits. Assert:

  - `approval` exposes read/input/plan tools but no write/runtime/commit tools.
  - `mutate` hides the approval-plan and commit tools.
  - `verify` exposes `verify_development_runtime` plus repair-capable edit/runtime tools, but hides ordinary inspection, plan, and commit tools.
  - `repair` exposes diagnostic workspace reads, runtime reads, edit/runtime tools, and the verifier, but hides plan and commit tools.
  - `commit` exposes only `commit_project_files`.
  - `report` exposes no tools.
  - `write_todos` is exposed only in `mutate` or `repair` when the approved plan has more than one step.

- [ ] **Step 5: Implement `BeforeModelRewriteState` filtering**

  Embed `*adk.BaseChatModelAgentMiddleware`, derive the phase before every model call, and rebuild both `state.ToolInfos` and `state.DeferredToolInfos` using phase-aware predicates. Register this middleware after tool-search so it has the final say over model-visible tools.

- [ ] **Step 6: Run the phase tests**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'TestProjectEinoAssistantPhase' -count=1
  ```

  Expected: PASS.

### Task 2: Construct the App Studio agent through DeepAgent

**Files:**
- Modify: `providers/app-studio/api/assistant_eino_engine.go`
- Modify: `providers/app-studio/api/assistant_eino_engine_test.go`

**Interfaces:**
- Consumes: `projectEinoAssistantPhaseMiddleware`, existing App Studio Eino handlers, tools, model retry configuration, and `maxAssistantToolTurns`.
- Produces: a `deep.New`-constructed `adk.ResumableAgent` returned through the existing `adk.Agent` boundary.

- [ ] **Step 1: Write failing engine tests**

  Add tests proving:

  - An implementation turn with a multi-step approved plan receives Eino's `write_todos` tool.
  - A one-step approved plan does not expose `write_todos`.
  - Discussion turns do not expose `write_todos`.
  - The approval-plan tool disappears from the next model call after approval.
  - The commit tool is absent after a write and appears only after a successful `verify_development_runtime` result.

- [ ] **Step 2: Run focused engine tests and confirm failure**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'TestEinoAssistantEngine.*(Deep|Phase|Verification)' -count=1
  ```

  Expected: new tests fail because the engine still constructs a plain `ChatModelAgent`.

- [ ] **Step 3: Replace the constructor with `deep.New`**

  Import `github.com/cloudwego/eino/adk/prebuilt/deep` and configure:

  ```go
  agent, err := deep.New(ctx, &deep.Config{
      Name:                   "app-studio-project-assistant",
      Description:            "Runs App Studio project assistant turns.",
      ChatModel:              chatModel,
      Instruction:            projectEinoAssistantDeepInstruction,
      ToolsConfig:            existingToolsConfig,
      MaxIteration:           maxAssistantToolTurns,
      WithoutWriteTodos:      !projectEinoAssistantTurnUsesDeepTodos(req.TurnPolicy),
      WithoutGeneralSubAgent: true,
      Handlers:               handlers,
      ModelRetryConfig:       projectEinoAssistantModelRetryConfig(),
  })
  ```

  Leave `Backend`, `Shell`, `StreamingShell`, and `SubAgents` unset. Append the App Studio phase middleware after reduction, summarization, tool search, and safe-tool-error handlers.

- [ ] **Step 4: Add the App Studio DeepAgent instruction**

  The instruction must distinguish the two planning concepts: `request_project_plan_approval` grants mutation authority, while `write_todos` only tracks a previously approved multi-step implementation. It must direct the model to treat the currently exposed tools as the authoritative phase and to return a concise report when no tools remain.

- [ ] **Step 5: Run focused engine and phase tests**

  Run:

  ```bash
  cd providers/app-studio
  go test ./api -run 'Test(EinoAssistantEngine|ProjectEinoAssistantPhase)' -count=1
  ```

  Expected: PASS.

### Task 3: Verify compatibility and regressions

**Files:**
- Modify only files required by failures directly caused by Tasks 1-2.

**Interfaces:**
- Consumes: the completed DeepAgent phase-controlled engine.
- Produces: passing App Studio provider tests and a focused diff suitable for independent review.

- [ ] **Step 1: Format the changed Go files**

  Run:

  ```bash
  gofmt -w providers/app-studio/api/assistant_eino_phase.go providers/app-studio/api/assistant_eino_phase_test.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_engine_test.go
  ```

- [ ] **Step 2: Run the complete App Studio provider suite**

  Run:

  ```bash
  cd providers/app-studio
  go test ./... -count=1
  ```

  Expected: PASS.

- [ ] **Step 3: Build the standalone provider**

  Run:

  ```bash
  make build-app-studio-provider
  ```

  Expected: the portal and Go provider binary build successfully.

- [ ] **Step 4: Check the patch**

  Run:

  ```bash
  git diff --check
  git status --short
  ```

  Expected: no whitespace errors and only the plan plus the phase/engine implementation and tests are modified.

- [ ] **Step 5: Request independent review**

  The reviewer must check correctness of phase transitions, approval/checkpoint compatibility, verification-result parsing, hidden-tool execution safety, DeepAgent configuration, latency impact, and missing regression tests.

- [ ] **Step 6: Address findings and rerun verification**

  Repeat:

  ```bash
  cd providers/app-studio
  go test ./... -count=1
  ```

  Expected: PASS after all accepted review fixes.
