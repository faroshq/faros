# Task 2 report: Tighten DeepAgent phase tool exposure

## Outcome

`projectEinoAssistantPhaseAllowsTool` now makes the mutation lifecycle directional while retaining the existing tool metadata as the source of truth for ordinary tools.

- Approval retains read, input, and plan tools plus discovery through `tool_search`.
- Mutate allows edit-bundle writes, `ask_follow_up`, and eligible `write_todos` only.
- Verify allows `verify_development_runtime` only.
- Repair allows workspace-read, edit, and runtime bundles plus `ask_follow_up` and eligible `write_todos`.
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

## Scope and review

Only the two Task 2 source/test files plus this required report are included. A fresh independent review agent was not available because all four collaboration slots were already occupied.
