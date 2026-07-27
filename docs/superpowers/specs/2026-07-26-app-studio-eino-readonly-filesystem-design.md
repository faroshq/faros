# App Studio Eino Read-Only Filesystem Design

## Goal

Replace App Studio's bespoke read-only project filesystem tools with Eino
v0.9.9's canonical filesystem tools while preserving every App Studio boundary
that authorizes or observes mutations.

The resulting DeepAgent tool surface will expose:

- Eino `ls`, `read_file`, `glob`, and `grep` for project inspection; and
- App Studio `write_file`, `apply_patch`, and `mkdir` for project mutation.

App Studio's legacy `list_project_files`, `read_project_file`, and
`search_project_files` tool names will no longer be advertised.

## Selected approach

Create a tenant- and project-scoped, read-only implementation of Eino's
`filesystem.Backend` in the App Studio `workspace` package. Construct Eino's
filesystem middleware explicitly and disable its `write_file` and `edit_file`
tools.

Do not set `deep.Config.Backend`. That shortcut registers Eino's mutation tools
alongside its read tools and uses filesystem descriptions that are not tailored
to App Studio's project-relative path contract.

The middleware will use the canonical Eino names:

- `ls`
- `read_file`
- `glob`
- `grep`

Its descriptions and additional instruction will say that paths are relative to
the current App Studio project. Large-result offloading and multimodal reads
remain disabled because both introduce write or storage behavior outside this
slice.

## Scoped backend

The backend is created for one assistant run from the existing
`workspace.FileStore` and `workspace.Scope`. It never receives a host root from
the model and cannot select another organization, workspace, or project.

The adapter will implement Eino's backend interface as follows:

- `LsInfo` lists the requested project-relative directory with stable ordering.
- `Read` returns bounded UTF-8 text and honors Eino's one-based line offset and
  line limit.
- `GlobInfo` matches project-relative paths using Eino-compatible `**` glob
  semantics and stable ordering.
- `GrepRaw` supports Eino's regex, case, path, glob, file-type, multiline, and
  surrounding-context inputs over bounded project files.
- `Write` and `Edit` always return a read-only-backend error, even though their
  middleware tools are disabled.

The implementation will extend or reuse `FileStore` primitives rather than
open files through a second, weaker path layer. All operations retain the
existing scope validation, project-relative path normalization, `.git` and
`node_modules` exclusion, component-level symlink rejection, regular-file
checks, binary detection, UTF-8 handling, context cancellation, and byte
bounds.

Listings and searches are bounded. If an operation exceeds its result or byte
budget, it reports that the request must be narrowed instead of silently
presenting a complete-looking partial result. Unsupported file-type filters or
regex features return a model-visible validation error.

## Agent integration

The filesystem middleware is installed only for turn policies that permit the
existing `workspace_read` bundle. Discussion and guidance turns therefore do
not gain filesystem access merely because middleware exists.

The middleware is inserted before App Studio's phase middleware. The phase
middleware will recognize only the four exact canonical names as trusted
`read`/`workspace_read` tools when they originate from the configured
filesystem middleware. Unknown metadata-free tools remain denied. Phase
filtering and invocation-time checks both enforce the same policy so a model
cannot call a hidden tool by name.

Existing assistant prompts will be updated to teach the canonical workflow:
use `ls` and `glob` to discover paths, `read_file` for bounded targeted reads,
and `grep` to locate code.

The three legacy read-only tools will be removed from the App Studio registry.
Completed calls with their old names may remain in checkpoint history, but
read-only tools never create approval interrupts, so no alias is required to
resume an outstanding mutation. Existing malformed or dangling tool calls
continue through the current unknown-tool repair path.

## Preserved mutation boundary

This change does not migrate filesystem mutations to Eino.

App Studio remains the sole implementation of:

- `write_file`
- `apply_patch`
- `mkdir`

Those tools continue through `projectEinoAssistantTool` and retain approved-plan
path envelopes, permission interrupts, canonical mutation accounting, audit and
progress events, safe error shaping, development synchronization, and commit
workflow behavior.

No Eino shell, streaming shell, generic subagent, `edit_file`, or backend
`write_file` capability is enabled.

## Observability

Add a narrow invocation middleware for the four exact canonical read names.
It emits the same requested, running, succeeded, and failed progress events as
the legacy read tools and records their tool results in run state for checkpoint
and audit continuity. Its summaries include the tool name and sanitized
project-relative path or search expression, but never file contents.

This wrapper only supplies observability. It does not perform permission
interrupts, authorize mutations, synchronize development state, or replace
Eino's tool implementation. Mutation audit behavior is unchanged.

## Alternatives considered

### Keep App Studio aliases

Eino permits overriding tool names, so the middleware could continue exposing
`list_project_files`, `read_project_file`, and `search_project_files`. This
reduces prompt churn but does not test DeepAgent's normal filesystem vocabulary
and leaves App Studio with a nonstandard surface. It was not selected.

### Configure `deep.Config.Backend`

This is the smallest wiring change, but it also enables Eino's mutation tools
and default absolute-path-oriented descriptions. Intercepting those writes
after registration would create two mutation paths and a fragile authorization
boundary. It was rejected.

### Migrate reads and writes together

This would exercise more of Eino's default DeepAgent behavior, but its raw
mutation backend does not know App Studio's cross-turn grants, approval
interrupts, path envelopes, audit trail, development sync, or commit contract.
It is outside this experiment.

## Validation

Backend unit tests will prove:

- organization, workspace, and project isolation;
- rejection of absolute paths, traversal, reserved directories, and symlinks;
- deterministic directory and glob results;
- bounded line-based reads and binary-file behavior;
- regex grep behavior, filters, case handling, multiline handling, and context;
- cancellation and result-limit errors; and
- fail-closed `Write` and `Edit` methods.

Agent tests will prove:

- `ls`, `read_file`, `glob`, and `grep` are visible only in policies and phases
  that allow workspace reads;
- the three legacy read names are absent;
- Eino `write_file` and `edit_file` are absent;
- App Studio `write_file`, `apply_patch`, and `mkdir` remain present and retain
  their metadata and permission path;
- direct invocation cannot bypass phase filtering; and
- prompts refer only to the canonical names.

Focused workspace and API tests will run first, followed by the standalone
App Studio provider's full test, race-test, vet, and Makefile build gates.

## Non-goals

- Migrating project mutations to Eino
- Enabling shell execution or general subagents
- Adding multimodal project-file reads
- Adding large-result artifact storage or offloading
- Changing repository hydration, development runtime, or commit semantics
- Changing public App Studio HTTP or Kubernetes APIs
