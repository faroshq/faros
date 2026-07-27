# App Studio Eino Read-Only Filesystem Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace App Studio's three bespoke workspace-read tools with Eino v0.9.9's canonical `ls`, `read_file`, `glob`, and `grep` tools while retaining App Studio's mutation, approval, audit, phase, and development-sync boundaries.

**Architecture:** Add a per-run `filesystem.Backend` adapter that captures one `workspace.FileStore` and `workspace.Scope`. It reads through the existing safe store and delegates Eino-compatible listing, glob, file-type, regex, multiline, and context behavior to Eino's own in-memory backend. Install Eino's typed filesystem middleware only for workspace-read turn policies, disable its write/edit tools, and add narrow phase/telemetry bridges for the four exact canonical names. The pinned typed `MiddlewareConfig` path does not install the legacy large-result offloading wrapper.

**Tech Stack:** Go 1.26.3, `github.com/cloudwego/eino v0.9.9`, Eino ADK DeepAgent/filesystem middleware, App Studio `workspace.FileStore`, Go tests.

## Global Constraints

- Expose exactly `ls`, `read_file`, `glob`, and `grep` as Eino-owned project-read tools.
- Remove `list_project_files`, `read_project_file`, and `search_project_files` from the advertised App Studio registry and prompts.
- Keep App Studio `write_file`, `apply_patch`, and `mkdir` unchanged and registry-backed.
- Do not set `deep.Config.Backend`; construct `filesystem.New` explicitly.
- Set `WriteFileToolConfig.Disable` and `EditFileToolConfig.Disable` to `true`. Do not reference `WithoutLargeToolResultOffloading`: that field exists on the legacy `Config`, not the typed `MiddlewareConfig` used by `filesystem.New`.
- Do not configure `Shell`, `StreamingShell`, multimodal reads, general subagents, or any new artifact store.
- All model paths are project-relative and fixed to the request's organization, workspace, and project scope.
- Unknown metadata-free tools remain denied; only the four exact un-namespaced canonical read names receive fallback `read` / `workspace_read` metadata.
- Read telemetry may expose sanitized paths or search expressions but must never include file contents or matching source lines.
- Production code follows strict red-green-refactor: every behavior test must be observed failing before its implementation is added.

---

## File Structure

- Create `providers/app-studio/workspace/eino_backend.go`: scoped, fail-closed implementation of Eino's backend protocol.
- Create `providers/app-studio/workspace/eino_backend_test.go`: real filesystem contract and isolation tests.
- Create `providers/app-studio/api/assistant_eino_filesystem.go`: middleware construction, canonical-name set, turn-policy gate, and read-only telemetry wrapper.
- Create `providers/app-studio/api/assistant_eino_filesystem_test.go`: middleware inventory, descriptions, telemetry, and real invocation tests.
- Modify `providers/app-studio/api/assistant_eino_engine.go`: install filesystem and telemetry middleware before safe-error and phase middleware.
- Modify `providers/app-studio/api/assistant_eino_phase.go`: classify only exact canonical Eino read tools when metadata is absent.
- Modify `providers/app-studio/api/assistant_tool_registry.go`: remove the three bespoke read implementations and their ordering hook.
- Modify `providers/app-studio/api/assistant_tool.go`: classify canonical reads as `workspace_read`.
- Modify `providers/app-studio/api/llm.go`: replace constants/prompts and add content-safe canonical argument/result summaries.
- Modify `providers/app-studio/api/assistant_ui_events.go`: classify and label all four canonical reads as inspection actions.
- Modify `providers/app-studio/api/assistant_audit.go`: extract canonical read paths without exposing content.
- Modify the focused tests in `providers/app-studio/api/assistant_audit_test.go`, `assistant_eino_engine_test.go`, `assistant_eino_phase_test.go`, `assistant_eino_reduction_test.go`, `assistant_events_test.go`, `assistant_turn_profile_test.go`, `development_environment_test.go`, and `repository_flow_test.go`.

---

### Task 1: Tenant-scoped Eino read-only backend

**Files:**

- Create: `providers/app-studio/workspace/eino_backend.go`
- Create: `providers/app-studio/workspace/eino_backend_test.go`
- Reuse unchanged: `providers/app-studio/workspace/store.go`

**Interfaces:**

- Consumes: `NewFileStore(root string) *FileStore`, `FileStore.ListFiles`, `FileStore.ReadFile`, and `Scope`.
- Produces:

```go
type EinoReadOnlyBackend struct {
	store *FileStore
	scope Scope
}

func NewEinoReadOnlyBackend(store *FileStore, scope Scope) (*EinoReadOnlyBackend, error)

func (b *EinoReadOnlyBackend) LsInfo(context.Context, *einofs.LsInfoRequest) ([]einofs.FileInfo, error)
func (b *EinoReadOnlyBackend) Read(context.Context, *einofs.ReadRequest) (*einofs.FileContent, error)
func (b *EinoReadOnlyBackend) GlobInfo(context.Context, *einofs.GlobInfoRequest) ([]einofs.FileInfo, error)
func (b *EinoReadOnlyBackend) GrepRaw(context.Context, *einofs.GrepRequest) ([]einofs.GrepMatch, error)
func (b *EinoReadOnlyBackend) Write(context.Context, *einofs.WriteRequest) error
func (b *EinoReadOnlyBackend) Edit(context.Context, *einofs.EditRequest) error
```

- Internal bounds:

```go
const (
	maxEinoBackendAggregateBytes = 16 << 20
	maxEinoBackendMatches        = 1000
	einoBackendCandidateMarker   = "__app_studio_eino_candidate__"
)

var errEinoReadOnlyWorkspace = errors.New("App Studio project filesystem backend is read-only")
```

- `NewEinoReadOnlyBackend` calls `store.scopeDir(scope)` once to reject nil/unconfigured stores and incomplete or unsafe scope segments.
- Directory inputs accept only empty string or `"."` for the project root; `/`, absolute paths, `..`, `.git`, `node_modules`, and NULs fail through `cleanProjectPath`.

- [ ] **Step 1: Write failing constructor, scope-isolation, and path-safety tests**

Add real `FileStore` fixtures for two scopes and assert:

```go
func TestEinoReadOnlyBackendIsScopedToOneProject(t *testing.T) {
	store := NewFileStore(t.TempDir())
	scopeA := Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "alpha"}
	scopeB := Scope{OrgUUID: "org-b", WorkspaceUUID: "ws-2", ProjectName: "beta"}
	if err := store.ApplyFiles(context.Background(), scopeA, []File{{Path: "src/a.go", Content: "package alpha\n"}}); err != nil {
		t.Fatalf("ApplyFiles scope A returned error: %v", err)
	}
	if err := store.ApplyFiles(context.Background(), scopeB, []File{{Path: "secret.txt", Content: "beta secret\n"}}); err != nil {
		t.Fatalf("ApplyFiles scope B returned error: %v", err)
	}

	backend, err := NewEinoReadOnlyBackend(store, scopeA)
	if err != nil {
		t.Fatalf("NewEinoReadOnlyBackend returned error: %v", err)
	}
	infos, err := backend.GlobInfo(context.Background(), &einofs.GlobInfoRequest{Pattern: "**/*"})
	if err != nil {
		t.Fatalf("GlobInfo returned error: %v", err)
	}
	if got := einoFileInfoPaths(infos); !slices.Equal(got, []string{"src/a.go"}) {
		t.Fatalf("paths = %v, want only alpha project file", got)
	}
}
```

Use table cases `"/etc/passwd"`, `"../beta/secret.txt"`, `".git/config"`, and `"node_modules/pkg/index.js"` against `Read`, `GlobInfo`, and `GrepRaw`; every case must return an error. Add a symlink fixture under the scoped directory and prove it is neither listed nor readable.

Add these test-only literal projection helpers:

```go
func einoFileInfoPaths(infos []einofs.FileInfo) []string {
	paths := make([]string, 0, len(infos))
	for _, info := range infos {
		paths = append(paths, info.Path)
	}
	return paths
}

func einoGrepPaths(matches []einofs.GrepMatch) []string {
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		paths = append(paths, match.Path)
	}
	return paths
}
```

- [ ] **Step 2: Run the new safety tests and verify RED**

Run:

```bash
cd providers/app-studio
go test -count=1 ./workspace -run 'TestEinoReadOnlyBackend(IsScopedToOneProject|RejectsUnsafePathsAndSymlinks)$'
```

Expected: compile failure because `NewEinoReadOnlyBackend` does not exist.

- [ ] **Step 3: Implement constructor, bounded inventory, and fail-closed mutations**

Implement:

```go
func NewEinoReadOnlyBackend(store *FileStore, scope Scope) (*EinoReadOnlyBackend, error) {
	if store == nil {
		return nil, errors.New("project workspace store is not configured")
	}
	if _, err := store.scopeDir(scope); err != nil {
		return nil, err
	}
	return &EinoReadOnlyBackend{store: store, scope: scope}, nil
}

func (b *EinoReadOnlyBackend) projectFiles(ctx context.Context) ([]FileInfo, error) {
	list, err := b.store.ListFiles(ctx, b.scope, ListOptions{Limit: MaxListLimit})
	if err != nil {
		return nil, err
	}
	if list.Truncated {
		return nil, fmt.Errorf("project has more than %d files; narrow path or glob", MaxListLimit)
	}
	return list.Files, nil
}

func (b *EinoReadOnlyBackend) Write(context.Context, *einofs.WriteRequest) error {
	return errEinoReadOnlyWorkspace
}

func (b *EinoReadOnlyBackend) Edit(context.Context, *einofs.EditRequest) error {
	return errEinoReadOnlyWorkspace
}
```

Add `cleanEinoDirectoryPath` that maps `""` and `"."` to the internal Eino root `/`, otherwise returns `"/"+cleanProjectPath(raw)`.

- [ ] **Step 4: Write failing `ls`, `read_file`, and `glob` contract tests**

Use files `README.md`, `src/App.tsx`, `src/components/Card.tsx`, and `test/App.test.tsx`. Assert:

```go
root, _ := backend.LsInfo(ctx, &einofs.LsInfoRequest{})
// literal expected immediate children
wantRoot := []einofs.FileInfo{
	{Path: "README.md", IsDir: false, Size: int64(len("readme\n"))},
	{Path: "src", IsDir: true},
	{Path: "test", IsDir: true},
}

src, _ := backend.LsInfo(ctx, &einofs.LsInfoRequest{Path: "src"})
wantSrcPaths := []string{"App.tsx", "components"}

read, _ := backend.Read(ctx, &einofs.ReadRequest{
	FilePath: "src/App.tsx",
	Offset:   2,
	Limit:    2,
})
if read.Content != "line two\nline three" {
	t.Fatalf("content = %q, want literal two-line page", read.Content)
}

glob, _ := backend.GlobInfo(ctx, &einofs.GlobInfoRequest{
	Path:    "src",
	Pattern: "**/*.tsx",
})
if got := einoFileInfoPaths(glob); !slices.Equal(got, []string{"src/App.tsx", "src/components/Card.tsx"}) {
	t.Fatalf("glob paths = %v", got)
}
```

Also assert empty and binary files, an offset after EOF, `Limit: 0`, stable lexical ordering, and an inventory larger than `MaxListLimit` returning the explicit narrow-request error.

- [ ] **Step 5: Run the read contract tests and verify RED**

Run:

```bash
cd providers/app-studio
go test -count=1 ./workspace -run 'TestEinoReadOnlyBackend(ListsImmediateChildren|ReadsLinePages|GlobsProjectRelativePaths|EnforcesBoundsAndBinaryRules)$'
```

Expected: failures from unimplemented `LsInfo`, `Read`, and `GlobInfo`.

- [ ] **Step 6: Implement `LsInfo`, `Read`, and `GlobInfo` using Eino's reference backend**

Create a metadata snapshot with one internal absolute Eino path per safe App Studio file:

```go
func einoMetadataSnapshot(ctx context.Context, files []FileInfo) (*einofs.InMemoryBackend, error) {
	snapshot := einofs.NewInMemoryBackend()
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := snapshot.Write(ctx, &einofs.WriteRequest{
			FilePath: "/" + file.Path,
			Content:  einoBackendCandidateMarker,
		}); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}
```

Delegate `LsInfo` and `GlobInfo` to that snapshot, then normalize every returned path to the documented project-relative shape and sort it. Replace the marker content length with the original `FileInfo.Size` for regular-file results; synthetic directory sizes remain zero. For `GlobInfo` with a non-root `Path`, prefix the cleaned base path back onto Eino's base-relative results.

Implement `Read` through:

```go
file, err := b.store.ReadFile(ctx, b.scope, ReadOptions{
	Path:     req.FilePath,
	MaxBytes: MaxReadMaxBytes,
})
```

Reject `file.Binary` and `file.Truncated`. Treat offsets below 1 as 1, preserve `Limit: 0` as “remaining bounded file,” and return the requested line slice without adding line numbers; Eino's tool formats those.

- [ ] **Step 7: Write failing rich `grep` behavior tests**

Use literal fixtures that independently prove:

- regex matches and 1-based line numbers;
- `CaseInsensitive`;
- `Path`;
- `Glob`;
- Eino file-type aliases, including `FileType: "js"` matching `.jsx`;
- `EnableMultiline`;
- `BeforeLines` and `AfterLines`;
- invalid regular expressions and invalid glob patterns return validation errors;
- stable `(Path, Line, Content)` ordering;
- binary files are skipped;
- aggregate content over 16 MiB and results over 1000 return a narrow-request error;
- a canceled context stops inventory/read/search work; and
- `Write` and `Edit` return `errEinoReadOnlyWorkspace` and leave files unchanged.

The file-type assertion must use Eino's behavior rather than a copied App Studio extension map:

```go
matches, err := backend.GrepRaw(ctx, &einofs.GrepRequest{
	Pattern:  "needle",
	FileType: "js",
})
if err != nil {
	t.Fatalf("GrepRaw returned error: %v", err)
}
if got := einoGrepPaths(matches); !slices.Equal(got, []string{"src/view.jsx"}) {
	t.Fatalf("paths = %v, want Eino js alias to include jsx", got)
}
```

- [ ] **Step 8: Run the grep tests and verify RED**

Run:

```bash
cd providers/app-studio
go test -count=1 ./workspace -run 'TestEinoReadOnlyBackend(Grep|RejectsMutations)'
```

Expected: failure because `GrepRaw` is not implemented.

- [ ] **Step 9: Implement `GrepRaw` by delegating filtering and matching to Eino**

Use a two-stage Eino snapshot:

1. Populate a metadata snapshot with `einoBackendCandidateMarker`.
2. Copy the request, replace only its pattern with `regexp.QuoteMeta(einoBackendCandidateMarker)`, clear case/multiline/context, and call the snapshot's `GrepRaw`. This reuses Eino v0.9.9's own `Path`, `Glob`, and private file-type alias filtering without copying its table.
3. Deduplicate the resulting candidate paths.
4. Read only those candidates through `FileStore.ReadFile` with `MaxReadMaxBytes`; skip binary files, reject truncated files, and reject aggregate UTF-8 content over `maxEinoBackendAggregateBytes`.
5. Populate a second `einofs.InMemoryBackend` with the real text and call its `GrepRaw` with the original request. This reuses Eino's regex, case, multiline, and context behavior.
6. Normalize paths, sort by path/line/content, and reject more than `maxEinoBackendMatches`.

Do not call the scoped backend's public `Write`; only the private Eino snapshots may use their in-memory `Write`.

- [ ] **Step 10: Run workspace tests, format, and commit**

Run:

```bash
gofmt -w providers/app-studio/workspace/eino_backend.go providers/app-studio/workspace/eino_backend_test.go
cd providers/app-studio
go test -count=1 ./workspace
go test -race -count=1 ./workspace
```

Expected: all workspace tests pass.

Commit:

```bash
git add providers/app-studio/workspace/eino_backend.go providers/app-studio/workspace/eino_backend_test.go
git commit -m "feat(app-studio): add scoped Eino filesystem backend"
```

---

### Task 2: Eino middleware, policy/phase gate, and read telemetry

**Files:**

- Create: `providers/app-studio/api/assistant_eino_filesystem.go`
- Create: `providers/app-studio/api/assistant_eino_filesystem_test.go`
- Modify: `providers/app-studio/api/assistant_eino_engine.go:276-348`
- Modify: `providers/app-studio/api/assistant_eino_phase.go:647-746,875-884`
- Modify: `providers/app-studio/api/assistant_eino_phase_test.go`
- Modify: `providers/app-studio/api/assistant_eino_engine_test.go`

**Interfaces:**

- Consumes: `workspace.NewEinoReadOnlyBackend`, `projectAssistantRunRequest.WorkspaceScope`, `Server.workspaces`, and `projectAssistantTurnPolicy.AllowsTool`.
- Produces:

```go
const (
	projectToolLS       = "ls"
	projectToolReadFile = "read_file"
	projectToolGlob     = "glob"
	projectToolGrep     = "grep"
)

func projectEinoAssistantFilesystemReadTool(name string) bool

func projectEinoAssistantFilesystemMiddleware(
	ctx context.Context,
	store *workspace.FileStore,
	req projectAssistantRunRequest,
) (adk.ChatModelAgentMiddleware, error)

func projectEinoAssistantFilesystemTelemetryMiddleware(
	req projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
) adk.ChatModelAgentMiddleware
```

- `projectEinoAssistantFilesystemReadTool` compares the trimmed raw name, not `projectToolBaseName`; `provider__read_file` must return false.

- [ ] **Step 1: Write failing middleware inventory and turn-policy tests**

Test `BeforeAgent` on the returned middleware and assert the exact inventory:

```go
want := []string{projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep}
```

Assert `write_file`, `edit_file`, and `execute` are absent. Assert every description says `project-relative` and none says `absolute path` or `any file on the machine`.

Use discussion, guidance, exploration, debugging, debug-fix, and implementation policies. The helper must return nil for discussion/guidance and a middleware for the other four profiles.

- [ ] **Step 2: Run middleware inventory tests and verify RED**

Run:

```bash
cd providers/app-studio
go test -count=1 ./api -run 'TestProjectEinoAssistantFilesystem(MiddlewareInventory|TurnPolicyGate)$'
```

Expected: compile failure because the filesystem middleware helpers do not exist.

- [ ] **Step 3: Implement the explicit Eino filesystem middleware**

Use custom descriptions with correct schemas and project-relative language:

```go
middleware, err := einofilesystem.New(ctx, &einofilesystem.MiddlewareConfig{
	Backend: backend,
	LsToolConfig:       &einofilesystem.ToolConfig{Desc: &projectEinoFilesystemLSDescription},
	ReadFileToolConfig: &einofilesystem.ToolConfig{Desc: &projectEinoFilesystemReadDescription},
	GlobToolConfig:     &einofilesystem.ToolConfig{Desc: &projectEinoFilesystemGlobDescription},
	GrepToolConfig:     &einofilesystem.ToolConfig{Desc: &projectEinoFilesystemGrepDescription},
	WriteFileToolConfig: &einofilesystem.ToolConfig{Disable: true},
	EditFileToolConfig:  &einofilesystem.ToolConfig{Disable: true},
	CustomSystemPrompt: &projectEinoFilesystemInstruction,
})
```

Return nil before constructing a backend when the normalized turn policy rejects:

```go
projectAssistantToolSpec{Name: projectToolReadFile, Risk: projectAssistantToolRiskRead}
```

Do not set `UseMultiModalRead`, `Shell`, or `StreamingShell`.

- [ ] **Step 4: Write failing phase metadata and direct-invocation tests**

Extend phase tests with:

```go
for _, name := range []string{projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep} {
	risk, bundle, ok := projectEinoAssistantPhaseToolMetadata(&schema.ToolInfo{Name: name})
	// assert ok, read, workspace_read
}
```

Assert `provider__read_file`, `edit_file`, `execute`, and an arbitrary metadata-free tool remain unclassified and denied. Assert canonical reads survive mutate, verify, and repair phase filtering and an invocation using a hidden/noncanonical name never reaches its endpoint.

- [ ] **Step 5: Run phase tests and verify RED**

Run:

```bash
cd providers/app-studio
go test -count=1 ./api -run 'TestProjectEinoAssistantPhase.*(Filesystem|CanonicalRead|HiddenTool)'
```

Expected: canonical tools fail metadata or phase checks.

- [ ] **Step 6: Add exact-name fallback metadata**

Update `projectEinoAssistantPhaseToolMetadata` so explicit valid `Extra` metadata remains authoritative, then apply this fallback only when metadata is missing:

```go
if tool != nil && projectEinoAssistantFilesystemReadTool(tool.Name) {
	return projectAssistantToolRiskRead, projectAssistantToolBundleWorkspaceRead, true
}
```

Never call `projectToolBaseName` in that fallback.

- [ ] **Step 7: Write failing telemetry tests**

Wrap a real `read_file` endpoint and assert:

- callbacks receive `requested`, `running`, then `succeeded`;
- the sanitized argument summary contains `path src/App.tsx`, `offset 2`, and `limit 20`;
- the success summary contains no source text;
- run state records the tool result with the real tool-call ID;
- a backend error emits `failed`, records a bounded safe failure result, and still returns the error for the existing safe-error middleware;
- `provider__read_file` and `write_file` pass through without filesystem telemetry.

For the direct middleware unit test, assert the existing `"tool-1"` fallback ID because Eino exposes no public context setter for `compose.GetToolCallID`. The real-agent integration test below must assert the model-provided call ID. Use a real `projectEinoAssistantRunState`, not a mock.

- [ ] **Step 8: Run telemetry tests and verify RED**

Run:

```bash
cd providers/app-studio
go test -count=1 ./api -run 'TestProjectEinoAssistantFilesystemTelemetry'
```

Expected: compile failure because the telemetry middleware does not exist.

- [ ] **Step 9: Implement the telemetry wrapper**

Implement `WrapInvokableToolCall` for the four exact names. Reuse:

```go
compose.GetToolCallID(ctx)
projectEinoToolArguments(argumentsInJSON)
summarizeProjectToolArgumentsMap(name, args)
summarizeProjectToolResult(name, result)
projectEinoAssistantSafeErrorText(err)
truncateProjectToolInfo(...)
```

Emit `requested` and `running` before calling the endpoint. On success emit `succeeded` and record the actual result. On failure emit `failed`, record `"Tool call failed: "+safeError`, and return the original error so `projectEinoAssistantSafeToolErrorMiddleware` retains responsibility for model-visible error conversion. Do not invoke permission policy, mutation synchronization, or builder events.

- [ ] **Step 10: Write a failing real-agent integration test**

Build an exploration request with a real `Server`, scoped `FileStore`, and fake model. The model's first response calls:

```json
{"name":"read_file","arguments":{"file_path":"README.md","offset":1,"limit":20}}
```

and its second response reports completion. Assert:

- the first model inventory includes all four canonical reads;
- neither Eino `edit_file` nor `execute` appears;
- the only `write_file` in an implementation inventory is the App Studio registry tool with `Extra["risk"] == "write"` and `Extra["bundle"] == "edit"`;
- the model receives line-numbered README content from Eino;
- read progress events and run-state evidence exist; and
- discussion/guidance requests expose none of the four canonical reads.

- [ ] **Step 11: Run the integration test and verify RED**

Run:

```bash
cd providers/app-studio
go test -count=1 ./api -run 'TestEinoAssistantEngineUsesScopedCanonicalFilesystemReads$'
```

Expected: canonical tools are absent from the agent.

- [ ] **Step 12: Wire middleware into `newAgent`**

After tool-search middleware and before safe-error/phase middleware:

```go
filesystemMiddleware, err := projectEinoAssistantFilesystemMiddleware(ctx, e.server.workspaces, req)
if err != nil {
	return nil, fmt.Errorf("create App Studio Eino filesystem middleware: %w", err)
}
if filesystemMiddleware != nil {
	handlers = append(
		handlers,
		filesystemMiddleware,
		projectEinoAssistantFilesystemTelemetryMiddleware(req, runState),
	)
}
handlers = append(handlers, &projectEinoAssistantSafeToolErrorMiddleware{
	BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
})
handlers = append(handlers, projectEinoAssistantPhaseMiddleware(req, runState))
```

Fail if an allowed workspace-read turn has no server or workspace store. This is a production invariant; update isolated engine tests to provide a real server/store rather than silently omitting the boundary.

- [ ] **Step 13: Format, run focused tests, and commit**

Run:

```bash
gofmt -w providers/app-studio/api/assistant_eino_filesystem.go providers/app-studio/api/assistant_eino_filesystem_test.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_engine_test.go providers/app-studio/api/assistant_eino_phase.go providers/app-studio/api/assistant_eino_phase_test.go
cd providers/app-studio
go test -count=1 ./api
```

Expected: all API tests pass with the temporary overlap of canonical and legacy read tools.

Commit:

```bash
git add providers/app-studio/api/assistant_eino_filesystem.go providers/app-studio/api/assistant_eino_filesystem_test.go providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_engine_test.go providers/app-studio/api/assistant_eino_phase.go providers/app-studio/api/assistant_eino_phase_test.go
git commit -m "feat(app-studio): enable canonical Eino read tools"
```

---

### Task 3: Remove legacy reads and migrate prompts, UI, audit, and tests

**Files:**

- Modify: `providers/app-studio/api/assistant_tool_registry.go`
- Modify: `providers/app-studio/api/assistant_tool.go`
- Modify: `providers/app-studio/api/llm.go`
- Modify: `providers/app-studio/api/assistant_ui_events.go`
- Modify: `providers/app-studio/api/assistant_audit.go`
- Modify: `providers/app-studio/api/assistant_audit_test.go`
- Modify: `providers/app-studio/api/assistant_eino_engine_test.go`
- Modify: `providers/app-studio/api/assistant_eino_phase_test.go`
- Modify: `providers/app-studio/api/assistant_eino_reduction_test.go`
- Modify: `providers/app-studio/api/assistant_events_test.go`
- Modify: `providers/app-studio/api/assistant_turn_profile_test.go`
- Modify: `providers/app-studio/api/development_environment_test.go`
- Modify: `providers/app-studio/api/repository_flow_test.go`

**Interfaces:**

- Consumes: canonical constants and middleware from Task 2.
- Produces: one coherent assistant surface with canonical Eino reads and bespoke App Studio writes.
- Produces: `func projectAssistantNonEmptyLineCount(string) int`, used only to summarize Eino plain-text list/search results without echoing them.

- [ ] **Step 1: Write failing registry and product-surface tests**

Update/add assertions that:

```go
for _, legacy := range []string{"list_project_files", "read_project_file", "search_project_files"} {
	if registry.Has(legacy) {
		t.Fatalf("legacy read tool %q remains in App Studio registry", legacy)
	}
}
for _, mutation := range []string{projectToolWriteFile, projectToolApplyPatch, projectToolMkdir} {
	if !registry.Has(mutation) {
		t.Fatalf("App Studio mutation tool %q is missing", mutation)
	}
}
```

Assert prompts contain `ls`, `read_file`, `glob`, and `grep`, contain inspect-before-edit guidance, and contain none of the three legacy names.

Assert canonical event argument summaries use these literal fields:

- `ls`: `path`
- `read_file`: `file_path`, `offset`, `limit`
- `glob`: `pattern`, `path`
- `grep`: `pattern`, `path`, `glob`, `type`, `output_mode`, `head_limit`, `offset`

Assert summaries and audit entries never contain the supplied file body or matching source line.

- [ ] **Step 2: Run the surface tests and verify RED**

Run:

```bash
cd providers/app-studio
go test -count=1 ./api -run 'Test(ProjectAssistant.*(Legacy|Canonical|WorkspaceInspect)|SummarizeProjectTool.*WorkspaceRead|ProjectAssistantRunAudit.*Canonical)'
```

Expected: failures because legacy registry entries/prompts and legacy summaries still exist.

- [ ] **Step 3: Remove legacy registry tools and constants**

Delete the three read `projectAssistantToolFunc` entries from `projectAssistantLocalToolRegistry`. Remove the `projectToolSearchProjectFiles` ordering trigger from `projectAssistantAllToolSpecs`; append workflow specs once after local specs.

Delete:

```go
projectToolListProjectFiles
projectToolReadProjectFile
projectToolSearchProjectFiles
```

Use the canonical constants from `assistant_eino_filesystem.go` throughout.

Update `projectAssistantToolBundleForSpec`:

```go
case projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep:
	return projectAssistantToolBundleWorkspaceRead
```

- [ ] **Step 4: Migrate prompts and safe summaries**

Replace prompt guidance with:

```text
Use ls and glob to discover project-relative paths, read_file for bounded targeted reads, and grep to locate code. Inspect relevant existing files before editing.
```

In `summarizeProjectToolArgumentsMap`, map `file_path` to the UI/audit label `path` without retaining raw JSON. Summarize the four canonical schemas explicitly.

In `summarizeProjectToolResult`, do not echo Eino's plain-text output:

```go
switch projectToolBaseName(name) {
case projectToolReadFile:
	return "file read"
case projectToolLS, projectToolGlob:
	return fmt.Sprintf("%d path(s)", projectAssistantNonEmptyLineCount(result))
case projectToolGrep:
	return fmt.Sprintf("%d result line(s)", projectAssistantNonEmptyLineCount(result))
}
```

Treat Eino's `"No files found"` and `"No matches found"` as zero results. Update the single-read tool-loop fallback to key on `projectToolReadFile`.

- [ ] **Step 5: Migrate UI and audit classification**

Update inspection handling:

```go
case projectToolLS, projectToolReadFile, projectToolGlob, projectToolGrep:
	return projectAssistantUIActionInspect
```

Give `read_file` the existing path-specific “Reading/Read” labels, `ls`/`glob` path-count labels, and `grep` result-count labels.

Update `projectAssistantAuditToolPath` to accept the four canonical names. Because argument summaries normalize `file_path` to `path`, continue parsing only bounded `path ...` summary segments. Search expressions may be present in the audit argument summary but never in the result summary.

- [ ] **Step 6: Update all legacy test fixtures**

Apply these explicit mappings:

| Old fixture | New fixture |
|---|---|
| `list_project_files {"limit":25}` | `ls {"path":"src"}` |
| `read_project_file {"path":"src/App.tsx","maxBytes":4096}` | `read_file {"file_path":"src/App.tsx","offset":1,"limit":200}` |
| `search_project_files {"query":"needle","maxResults":10}` | `grep {"pattern":"needle","path":"src","glob":"**/*.tsx","output_mode":"content","head_limit":10}` |

In phase inventory tests, include all four canonical names and keep exact-name spoof tests for `provider__read_file`.

In reduction tests, rename read calls/results to `read_file` but preserve the assertion that mixed read/write groups are not compacted.

In preview-refresh tests, use successful `read_file` and retain the expected `false`.

Remove `TestProjectLocalToolRunsWorkspaceReadTool`; Task 2's real middleware integration test is now the owning behavioral test. Keep mutation registry execution tests unchanged.

- [ ] **Step 7: Run API tests and fix only migration regressions**

Run:

```bash
cd providers/app-studio
go test -count=1 ./api
```

Expected: all API tests pass. Any remaining literal legacy tool name is a failure unless it is inside the explicit “legacy names absent” test.

- [ ] **Step 8: Scan for stale names and unsafe Eino capabilities**

Run:

```bash
rg -n 'list_project_files|read_project_file|search_project_files' providers/app-studio/api providers/app-studio/workspace
rg -n 'Backend:|Shell:|StreamingShell:|WithoutLargeToolResultOffloading|WriteFileToolConfig|EditFileToolConfig' providers/app-studio/api/assistant_eino_engine.go providers/app-studio/api/assistant_eino_filesystem.go
```

Expected:

- the first command returns only the explicit negative compatibility assertions;
- the second shows the scoped typed middleware configuration, no shell configuration, disabled Eino writes/edits, and no nonexistent typed offloading field.

- [ ] **Step 9: Format and run the standalone provider gates**

Run:

```bash
gofmt -w \
  providers/app-studio/api/assistant_audit.go \
  providers/app-studio/api/assistant_audit_test.go \
  providers/app-studio/api/assistant_eino_engine_test.go \
  providers/app-studio/api/assistant_eino_phase_test.go \
  providers/app-studio/api/assistant_eino_reduction_test.go \
  providers/app-studio/api/assistant_events_test.go \
  providers/app-studio/api/assistant_tool.go \
  providers/app-studio/api/assistant_tool_registry.go \
  providers/app-studio/api/assistant_turn_profile_test.go \
  providers/app-studio/api/assistant_ui_events.go \
  providers/app-studio/api/development_environment_test.go \
  providers/app-studio/api/llm.go \
  providers/app-studio/api/repository_flow_test.go
cd providers/app-studio
go test -count=1 ./...
go test -race -count=1 ./workspace ./api
go vet ./...
cd ../..
make build-app-studio-provider
git diff --check
```

Expected: every command exits zero with no warnings attributable to this change.

- [ ] **Step 10: Commit the coherent product-surface migration**

```bash
git add \
  providers/app-studio/api/assistant_audit.go \
  providers/app-studio/api/assistant_audit_test.go \
  providers/app-studio/api/assistant_eino_engine_test.go \
  providers/app-studio/api/assistant_eino_phase_test.go \
  providers/app-studio/api/assistant_eino_reduction_test.go \
  providers/app-studio/api/assistant_events_test.go \
  providers/app-studio/api/assistant_tool.go \
  providers/app-studio/api/assistant_tool_registry.go \
  providers/app-studio/api/assistant_turn_profile_test.go \
  providers/app-studio/api/assistant_ui_events.go \
  providers/app-studio/api/development_environment_test.go \
  providers/app-studio/api/llm.go \
  providers/app-studio/api/repository_flow_test.go
git commit -m "refactor(app-studio): adopt Eino filesystem read tools"
```

---

## Final branch verification

After all three task reviews are clean:

- Run `git status --short --branch` and confirm only the expected ahead count.
- Run `git diff --check origin/pr-472...HEAD`.
- Run `cd providers/app-studio && go test -count=1 ./...`.
- Run `cd providers/app-studio && go test -race -count=1 ./workspace ./api`.
- Run `cd providers/app-studio && go vet ./...`.
- Run `make build-app-studio-provider`.
- Dispatch a fresh whole-branch reviewer over `origin/pr-472...HEAD`, including deferred findings from the SDD ledger.
- Do not push or open a PR unless the user separately requests publication.
