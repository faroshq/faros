# App Studio development command execution

App Studio exposes a bounded `exec_command` assistant tool for compiler, test,
lint, type-check, formatter-check, code-generation, and diagnostic commands.
Normal execution reuses the selected component's persistent development
workspace, toolchain, dependency installation, and caches. It does not create a
second source-only executor pod.

Infrastructure remains the runtime authority. App Studio never receives a
runtime kubeconfig and never executes tenant source inside the App Studio
provider. Calls cross the provider boundary through Infrastructure's published,
caller-authorized data-plane `exec` capability.

## Request path

1. App Studio waits for the current run-local source mutation to complete its
   ordered development synchronization.
2. It captures a stable component snapshot from FileStore and binds its
   component-relative digest to the workspace's durable source revision.
3. The approval UI discloses the sanitized direct argv, component, relative
   working directory, timeout, application-container authority, application
   network profile, and runtime-only write policy.
4. App Studio calls Infrastructure as the tenant caller:
   `POST .../{resource}/{instance}/components/{component}/exec`. The start
   request carries the expected revision and digest, not another file snapshot.
5. Infrastructure re-authorizes the caller for both the instance and the
   template's explicit exec capability, resolves the platform-owned live
   control Service, and calls the normal `kedge-dev-agent` `/exec` endpoint with
   the instance control token.
6. The agent re-hashes the actual managed source files. It runs the direct argv
   only when the live persistent workspace exactly matches the expected
   revision and digest, and returns bounded stdout, stderr, exit state, and
   truncation metadata.

Start, poll, and cancel are caller-bound and idempotent per assistant run and
tool call. App Studio records the effect through its durable run-event ledger;
approval checkpoints register the typed exec input so a second approved command
can resume after the first one.

## One development workspace

FileStore remains the durable assistant-authored source authority. Each project
workspace has a monotonic durable source revision. Full component syncs carry
that revision, a digest, and the complete managed text-file set.

The live component workspace is derived but persistent state:

- managed source is made authoritative on every full sync;
- a reserved manifest records only platform-managed source paths;
- paths absent from the next managed set are deleted;
- `node_modules`, compiler caches, build output, logs, and other runtime-created
  paths are never inferred as source and are preserved;
- a repeated revision/digest is a no-op only after the agent re-hashes the
  actual files; and
- a stale revision or conflicting digest fails closed.

This removes the former split between a preview PVC with installed dependencies
and a disposable exec `emptyDir` without them. Package-install reload rules and
later `npm run build`, tests, linters, and framework CLIs observe the same
toolchain state.

Commands may mutate the derived runtime workspace. Those mutations can affect
the running preview but are not synchronized back to FileStore, and a later
authoritative source sync restores managed files. There is no implicit two-way
watcher. A future runtime-to-FileStore feature must be a separate approved,
bounded change-set operation with baseline hashes and conflict handling.

The manifest is an integrity and convergence record, not a security boundary:
a command with application-container authority can alter it. The next full sync
repairs or reconstructs it before another normal App Studio execution; direct
agent callers instead receive a revision/digest mismatch until they resync.

## Template opt-in and request limits

Execution is disabled unless a development component also declares a data-plane
`exec` capability and a proxy `sync` endpoint for the live control Service. The
template declaration can only lower provider ceilings:

```yaml
spec:
  development:
    components:
      app:
        workspacePath: .
        devImage: "${kedge.devImage.node}"
        workingDir: /workspace
        startCommand: npm run dev -- --host 0.0.0.0
  dataPlane:
    components:
      app:
        exec:
          maxTimeoutSeconds: 120
          maxOutputBytes: 262144
        endpoints:
          sync: { ... }
```

The caller cannot choose an image, absolute component root, environment
overrides, volume, service account, Kubernetes object, or control endpoint.
Workdir is relative to the selected component root. Argv count and token size,
timeout, response bytes, session count, and retention are bounded. There is no
implicit command-string shell, PTY, host path, pod exec, or connection upgrade.
An explicit interpreter in argv is allowed because arbitrary project scripts
can already launch one.

## Authority and safety boundary

Persistent exec intentionally has the live development container's authority.
It shares the container user, PID and network namespaces, mounted workspace,
dependency tree, caches, and any other application mounts. The agent supplies a
small sanitized child environment and accepts no environment overrides, but
that is defense in depth rather than container isolation: code with the same
UID may inspect process state, mounted credentials, or application-accessible
network services. It can also change runtime files.

This is an explicit product tradeoff, not an accidental security claim. App
Studio already runs assistant-authored source through the same development
container; `exec_command` adds a direct, separately approved way to invoke its
toolchain. The safety controls are:

- tenant-caller authorization at the Infrastructure API boundary;
- explicit per-template exec opt-in and provider-owned target resolution;
- exact command-scope disclosure and approval;
- direct argv, confined relative cwd, bounded time/output, process-group
  cancellation, and serialized workspace operations;
- revision/digest verification immediately before execution;
- no App Studio or runtime-cluster credential in the request; and
- no automatic runtime-to-FileStore writeback.

Do not describe this mode as a credential-isolated or network-denied sandbox.
Templates that mount high-value production credentials are not appropriate for
assistant-driven development execution. Runtime namespaces should use
development-scoped identities and least-privilege network and secret access.

## Disposable compatibility executor

Infrastructure retains `KubernetesExecutor` as an explicit operator fallback
(`KEDGE_INFRA_EXECUTOR=kubernetes`) during rollout. That mode creates an
ephemeral source-staging pod with no application mounts and default-deny egress,
but it does not reuse the live dependency tree or cache and therefore does not
provide the normal App Studio developer experience. Production deployments
using the fallback must retain its pod, NetworkPolicy, and pod-proxy RBAC and a
NetworkPolicy-enforcing CNI. The persistent default needs the already-existing
Service proxy and control-secret access used by development sync/log/restart.

Stateful PTY sessions, package-registry policy controls, and approved
runtime-to-FileStore change sets remain separate future capabilities.
