# Task 2 report: Tighten DeepAgent phase tool exposure

## Outcome

`projectEinoAssistantPhaseAllowsTool` now makes the mutation lifecycle directional while retaining the existing tool metadata as the source of truth for ordinary tools.

- Approval retains read, input, and plan tools plus discovery through `tool_search`.
- Mutate allows canonical `write_file`, `apply_patch`, and `mkdir` edits, exact initial-template selection, `ask_follow_up`, and eligible `write_todos`.
- Verify allows `verify_development_runtime` only.
- Repair allows targeted workspace reads, canonical `write_file`, `apply_patch`, and `mkdir` edits, exact initial-template selection, runtime tools, `ask_follow_up`, and eligible `write_todos`.
- Commit allows `commit_project_files` only; report allows no tools.

## Test-first evidence

The phase allowance table was expanded before implementation. The required focused test command failed against the old predicates because mutation admitted workspace/workflow/runtime tools, verification admitted edit/runtime tools, repair lacked follow-up, and commit admitted `commit_files`.

After the filter change:

```text
cd providers/app-studio && go test ./api -run 'TestProjectEinoAssistantPhase' -count=1
ok   github.com/faroshq/provider-app-studio/api

cd providers/app-studio && go test ./api -count=1
ok   github.com/faroshq/provider-app-studio/api
```

`git diff --check` also passed.

## Independent review follow-up

Review found that write-risk fallback metadata classified `hydrate_workspace` as an edit and that base-name normalization let namespaced or case-variant verifier/commit names satisfy exclusive phases. The follow-up tests use the real implementation tool factory, prove `hydrate_workspace` is present in the source inventory, and verify that mutate/repair expose only the three canonical approved edit operations.

Verifier and commit exposure now also requires an exact raw canonical name with the expected risk and bundle metadata. The hidden-tool wrapper preserves the raw tool name for that phase authorization check; existing argument, target-path, and operation authorization remains in the permission layer.

The same focused and full API commands passed after this review fix.

## Local E2E follow-up

A local `neat-todo` run reached mutation without a bound development template. The prompt correctly makes template binding the first implementation step, but the narrowed phase hid `select_project_template`, forcing the assistant to hand the action back to the user.

Mutate and repair now carry one documented workflow/write exception for the exact raw canonical `select_project_template` tool with its actual factory metadata. Real factory-inventory tests prove it is visible in both phases while `hydrate_workspace` remains hidden; namespaced/case lookalikes and wrong bundle metadata are rejected. Permission middleware continues to own automatic approval or user interruption for the operation.

The focused phase suite and full API suite passed after the E2E follow-up.

## Scope and review

Only the two Task 2 source/test files plus this required report are included. The subsequent independent review findings are addressed by the follow-up described above.
