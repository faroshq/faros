# App Studio sandboxed command execution

App Studio exposes a bounded `exec_command` assistant tool for compiler, test,
lint, type-check, formatter-check, and diagnostic commands. Execution belongs
to the infrastructure provider: App Studio never receives a runtime kubeconfig
and never executes tenant source in the App Studio process or the live
application container.

## Request path

1. App Studio resolves the selected development component and waits for the
   current source-mutation revision to finish its normal development sync.
2. It reads an exact text snapshot of that component from FileStore, computes a
   digest over those bytes, and requests approval under the existing runtime
   tool policy.
3. It calls the infrastructure data plane as the tenant caller:
   `POST .../{resource}/{instance}/components/{component}/exec`.
4. Infrastructure re-authorizes the caller for the instance and the explicit
   exec operation, resolves the component's platform-owned development image,
   and starts a disposable executor pod in the instance runtime namespace.
5. The executor pod stages the snapshot on an `emptyDir`, executes direct argv,
   and returns bounded stdout, stderr, exit status, duration, and changed-path
   diagnostics. Its filesystem is then discarded.

The start/poll/cancel protocol is idempotent per assistant run and tool call.
App Studio records it through the existing assistant run-event ledger, so an
incomplete effect is not silently replayed after a supervisor restart.

## Template opt-in

Execution is disabled unless a development component also declares a data-plane
`exec` capability. The declaration can only lower provider ceilings:

```yaml
spec:
  development:
    components:
      backend:
        workspacePath: api
        devImage: "${kedge.devImage.node}"
        workingDir: /workspace
        startCommand: npm run dev
  dataPlane:
    components:
      backend:
        exec:
          maxTimeoutSeconds: 120
          maxOutputBytes: 262144
          maxFiles: 512
          maxFileBytes: 1048576
        endpoints: { ... }
```

The caller cannot choose an image, absolute working directory, environment,
network profile, volume, service account, or Kubernetes object. A requested
working directory is relative to the component snapshot.

## Executor boundary

The disposable pod uses the existing `kedge-dev-agent` injector image, but runs
the binary in a separate `--exec-server` mode. That mode serves only unauthenticated
health and an authenticated `/exec` endpoint. It does not start the development
supervisor or expose sync, restart, logs, env, process, or proxy operations.

The pod:

- mounts only ephemeral tool, source, and `/tmp` volumes;
- never mounts the application's workspace PVC, secrets, or data volumes;
- has no application environment and accepts no environment overrides;
- disables service-account-token automount;
- runs non-root with privilege escalation disabled, all capabilities dropped,
  a read-only root filesystem, and RuntimeDefault seccomp;
- has CPU, memory, ephemeral-storage, output, and wall-clock limits;
- receives a default-deny egress NetworkPolicy; and
- is deleted when the command completes, is cancelled, or times out.

The command is direct argv. There is no implicit command-string shell, PTY, host path, pod
exec, or connection upgrade. The agent uses a fixed minimal `PATH`, `HOME`,
`TMPDIR`, `PWD`, and locale rather than inheriting provider or application
environment variables. Source paths and working directories are confined with
`os.Root`; symlink traversal, binary/NUL source, duplicate paths, oversized
input, and trailing JSON are rejected before command start. Timeout and
cancellation kill the whole process group.

The runtime cluster must use a NetworkPolicy-enforcing CNI. The provider creates
the deny policy before the selected pod, but Kubernetes cannot make a non-enforcing
CNI honor it. Because Kubernetes policies are additive, executor startup also
fails closed if an existing policy selects the pod and grants any egress.
Production deployments should also pin both toolchain and agent images by
digest. The runtime credential used by the infrastructure provider
must be allowed to create, get, list, and delete executor pods; create, list,
and delete their NetworkPolicies; and POST through `pods/proxy` in the application
namespaces. The operator-managed in-cluster deployment already uses
its existing broad runtime role; externally supplied `KRO_KUBECONFIG` identities
must be granted an equivalent narrowly scoped executor policy.

## Source of truth and deferred capabilities

FileStore remains the assistant's working-tree authority. Executor mutations
are reported only as changed/deleted path diagnostics and are discarded with
the pod. There is intentionally no runtime-to-FileStore synchronization.

A caller may explicitly select an interpreter in argv, just as a package manager
or test runner may launch one internally. Executable filtering is not a security
boundary once arbitrary project code can run; the pod sandbox, credential
isolation, network policy, limits, and approval are the boundary.

A future writeback feature must be a separate approved change-set operation:
it should carry baseline hashes, reject binary/symlink/oversized changes, and
reuse FileStore's atomic unified-patch preflight, snapshot, conflict, and
rollback machinery. Package-registry egress, dependency caches, implicit
command-string shell mode, and stateful PTY sessions are also separate future capabilities rather than
implicit expansions of `exec_command`.
