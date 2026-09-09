---
name: faros
description: Use when driving a faros hub as a user from a laptop, CI, or an AI coding agent (Claude Code, Codex, Cursor) rather than developing faros itself. Covers logging in with the faros CLI, picking an org and workspace, wiring the workspace MCP endpoint, building and shipping an App Studio project end to end (template, GitHub repo, dev sandbox, CI build, promote to production, publish and share), deploying containers and databases with the infrastructure provider, creating and invoking hosted agents, and reaching edge clusters and servers with kubectl, ssh, or MCP. Read before answering any question about faros CLI flags, hub URLs, org or workspace IDs, provider REST paths, kubectl resource kinds, or MCP tool names.
---

# faros for coding agents

faros is a multi-tenant control plane. One hub fronts everything: your kcp
workspace (a Kubernetes-style API server you talk to with kubectl), the
providers you enable in that workspace (App Studio, code, infrastructure,
agents, kuery, databricks), and the edges you connect (Kubernetes clusters and
Linux servers behind NAT that dial out to the hub). Every call you make, on
any surface, runs as **you**, with your workspace RBAC. Nothing here grants
authority; it only tells you where the doors are.

This file is the map. The `references/` directory holds the exhaustive
per-area material (every route, every CRD field, every MCP tool). Read the
reference for an area before doing non-trivial work in it:

| Area | Reference |
|---|---|
| CLI, login, tokens, org/workspace IDs, hub REST, URL grammar, GraphQL | [references/access.md](references/access.md) |
| App Studio projects: templates, assistant, sandbox, build, promote, publish, integrations, skills | [references/app-studio.md](references/app-studio.md) |
| code provider: GitHub connections, repositories, commits, CI status | [references/code.md](references/code.md) |
| infrastructure provider: templates, instances, URLs, access gate, dev sandboxes | [references/infrastructure.md](references/infrastructure.md) |
| agents provider: agents, runs, channels, schedules, deep research | [references/agents.md](references/agents.md) |
| MCP aggregate, MCPServer, per-edge MCP, edges, kuery, service catalog | [references/mcp-and-edges.md](references/mcp-and-edges.md) |

One thing the references cannot give you, because it is not in the code:
**section 9** collects what only appears when you actually drive a hub — how
to call MCP tools from a plain shell, which 403s are not about permissions,
and which alarming states are just latency. Read it before you conclude
something is broken.

## 1. Rules that override everything else

1. **There is no default hub.** `faros login` needs `--hub-url` or
   `FAROS_HUB_URL`. If you do not know the hub URL, ask. Never assume
   `console.faros.sh` or any other host exists.
2. **Address workspaces by cluster ID, never by path.** kubectl and every
   `/clusters/...` URL use the workspace's `clusterName` (a hash like
   `11tcw27t4rdtnacy`). Path-form `/clusters/root:faros:...` is rejected with 403.
3. **Tenant headers carry UUIDs.** Hub REST and provider REST take
   `X-Faros-Org: <org uuid>` and `X-Faros-Workspace: <workspace uuid>`.
   Display names are not identifiers. Never send `X-Faros-Tenant` or
   `X-Faros-Cluster`; the hub injects those and strips anything you send.
4. **No TTY means explicit flags.** `faros use` without both `--org` and
   `--workspace` errors in a non-interactive shell. `faros login` without
   `--token` opens a browser.
5. **Prefer kubectl over `faros apply`.** `faros apply` guesses plurals by
   appending `s` and does a full replace. `faros get` knows only `edges`,
   `workloads`, `placements`. For everything else use kubectl against the
   `faros` context.
6. **Do not invent tool names, routes, or fields, and do not trust this
   skill's *lists*.** Tool names on the aggregate MCP endpoint are
   `<provider>__<tool>` with a double underscore. Routes and fields here were
   read from code on 2026-09-09; when a live hub disagrees, the live hub wins.
   Shapes and mechanisms age well, but any enumeration — which providers
   exist, which are built in, which tools you have — is per-hub and per-org.
   Enumerate at runtime with `GET /api/providers` and MCP `tools/list`.
   Say so instead of guessing.
7. **Check readiness before promising outcomes.** A template with
   `exposure: internal` never gets a URL. A build is promotable only when
   every component's image digest exists for the exact commit. A dev preview
   is ready only when `authorize-development-preview` says `ready: true`.
8. **Secrets stay out of prompts, logs, and files you commit.** Model API
   keys, GitHub tokens, Telegram bot tokens, and the MCP bearer are write-only
   inputs. Reference Kubernetes Secrets by name; never echo their values.
9. **Destructive calls need the user's explicit ask**: deleting a project
   (also deletes the dev instance), deleting a code `Repository` (deletes
   the GitHub repo), `delete_instance`, `delete_agent` (deletes runs and
   memories), `edge delete`, `pods_delete`, and any `<svc>_call_service`
   that moves physical things.
10. **An App Studio project is created by App Studio, in one call.**
    `POST /api/projects` creates the code `Repository`, the GitHub repo, the
    seeded first commit and the dev `Instance` as one owned set. Do not
    hand-assemble a project by creating a `Repository` CR and an `Instance`
    yourself and hoping App Studio adopts them — it will not, and you get
    parts that build but can never be promoted or published. Adopting an
    existing repo has its own supported input, `existingRepositoryRef`.
    Creating an `Instance` directly is a different, valid workflow (section 6),
    not a step toward a project.
11. **Tell "not yet" from "wrong" before you act.** Much of the platform is
    asynchronous, and several normal intermediate states are indistinguishable
    from errors: a new hostname fails TLS until its certificate is issued, a
    promotion reads `none` until the ghcr crawler runs, a Ready project has no
    repository yet. Waiting and rebuilding are opposite responses, so identify
    which you are looking at (section 9.4) before doing either. The exception
    that never resolves: an `exposure: internal` template has no URL coming.
12. **Direct `git push` is not promotable.** App Studio only recognizes
    commits recorded through faros (`code__commit_files` or the App Studio
    reconciler). Pushing to GitHub directly builds in CI but App Studio
    cannot select that commit for promotion. See section 5.

## 2. Get connected

### 2.1 Install and log in

```bash
# Install one of:
kubectl krew index add faros https://github.com/faroshq/krew-index.git && kubectl krew install faros/faros
go install github.com/faroshq/faros/cmd/faros@latest
# or a release binary from https://github.com/faroshq/faros/releases

export FAROS_HUB_URL=https://<hub>        # ask the user; no default exists
faros login                               # OIDC: opens a browser, PKCE, localhost callback
faros login --token "$FAROS_TOKEN"        # static-token hubs / unattended
faros use --org <org> --workspace <ws>    # names or UUIDs; both flags in CI
faros version
```

`faros login` writes a kubeconfig context named `faros` into `$KUBECONFIG`
(or `~/.kube/config`) and points it at `<hub>/clusters/<clusterName>`.
OIDC tokens are cached in `~/.config/faros/tokens/`. `faros use` rewrites the
context's server URL to the chosen workspace. The krew form is `kubectl faros <cmd>`.

Sanity check where you are pointed:

```bash
kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}'
# https://<hub>/clusters/<clusterName>
```

### 2.2 Get a bearer token for curl

| Hub auth | How to get `$TOKEN` |
|---|---|
| Static token | The token you logged in with. Also in the kubeconfig: `kubectl config view --raw -o jsonpath='{.users[?(@.name=="faros")].user.token}'` |
| OIDC | `curl -s $FAROS_HUB_URL/healthz` returns `issuerUrl` and `clientId`. Then `faros get-token --oidc-issuer-url <issuer> --oidc-client-id <id>` prints an ExecCredential JSON; take `.status.token`. It refreshes from the cache written by `faros login`. |
| Long-lived, for MCP clients | `GET /api/orgs/{org}/workspaces/{ws}/mcpservers/default/connect` returns the MCPServer's ServiceAccount token (see 2.4). |

### 2.3 Resolve org, workspace, and cluster IDs

```bash
HUB=$FAROS_HUB_URL
A="Authorization: Bearer $TOKEN"
curl -s "$HUB/api/orgs" -H "$A"
#  {"items":[{"uuid":"7f3a…","displayName":"acme","personal":false,"role":"admin",…}]}
ORG=7f3a…
curl -s "$HUB/api/orgs/$ORG/workspaces" -H "$A" -H "X-Faros-Org: $ORG"
#  {"items":[{"uuid":"9c4b…","displayName":"platform","clusterName":"11tcw27t4rdtnacy",…}]}
WS=9c4b…
CLUSTER=11tcw27t4rdtnacy
T="-H X-Faros-Org:$ORG -H X-Faros-Workspace:$WS"   # tenant headers for REST calls
```

An empty `clusterName` means the workspace is still provisioning. Every user
gets a personal org and a default workspace on first login.

### 2.4 Enable providers in the workspace

Providers are catalog entries. App Studio depends on `code` and
`infrastructure`; enable those first. `agents`, `kuery`, `databricks` are
optional. **`edges` is an ordinary provider** with its own APIExport and must
be enabled like any other — it is not compiled into the hub. The MCP aggregate
is not a provider at all: it is a hub endpoint, mounted unconditionally, that
federates providers.

**The catalog is the authority, and it differs per org.** Read it before you
plan anything; do not assume a provider is present, ready, or platform-scoped.
On one dev hub in 2026-09 the catalog held exactly six entries — `agents`,
`app-studio`, `code`, `edges`, `infrastructure`, `kuery` — none of which
advertised `builtin`, and `infrastructure` was `scope: org` (a self-hosted
provider shadowing the platform one). `scope` is the field that decides
whether a provider's tools reach the MCP aggregate; see section 9.

```bash
curl -s "$HUB/api/providers" -H "$A" -H "X-Faros-Org: $ORG"          # catalog with ready, dependencies, permissionClaims
curl -s "$HUB/api/orgs/$ORG/workspaces/$WS/providers/enabled" -H "$A" $T
for p in code infrastructure app-studio; do
  curl -s -X POST "$HUB/api/orgs/$ORG/workspaces/$WS/providers/$p/enable" -H "$A" $T \
    -H 'Content-Type: application/json' \
    -d '{"acceptedClaims":[{"group":"","resource":"secrets"}]}'      # accept the claims the catalog lists for that provider
done
```

Enabling creates a kcp `APIBinding` named after the provider. A 409 lists
missing dependencies. Pass every `(group, resource)` from the provider's
`permissionClaims` as accepted; verbs are never user-supplied.

### 2.5 Wire the workspace MCP endpoint into your client

```bash
faros mcp url --mcpserver-name default
# prints:
#   https://<hub>/services/mcpserver/<cluster>/apis/faros.sh/v1alpha1/mcpservers/default/mcp
#   claude mcp add --transport http faros-default "<url>" -H "Authorization: Bearer <token>"
#   a claude_desktop_config.json snippet, and a `codex mcp add` line
```

On OIDC hubs the printed token is a placeholder because the kubeconfig
carries an exec plugin, not a token. Use the connect endpoint instead:

```bash
curl -s "$HUB/api/orgs/$ORG/workspaces/$WS/mcpservers/default/connect" -H "$A" $T
# {"endpointURL":"…/mcpservers/default/mcp","serverName":"faros","token":"<SA token>","tokenReady":true}
claude mcp add --transport http faros "<endpointURL>" -H "Authorization: Bearer <token>"
claude mcp list
```

Every enabled platform provider's tools appear on that one endpoint as
`<provider>__<tool>`, plus `edges__*` for connected clusters and services.
Per-edge endpoints exist too: `faros mcp url --edge <name>` (Kubernetes edges only).

## 3. Pick the right surface

| You want to | Use | Notes |
|---|---|---|
| Read or write workspace resources (edges, instances, templates, agents CRs, code repos, secrets) | `kubectl` with the `faros` context | Cluster-scoped CRDs; `-o yaml` works; server-side apply works |
| Do App Studio work (projects, assistant, sandbox, promote, publish) | REST at `$HUB/services/providers/app-studio/api/...` | App Studio has no MCP server and no CLI |
| Commit files to a project repo, read a repo, check CI | MCP `code__*` or the code CRDs | Only faros-recorded commits are promotable |
| Provision or update a workload without App Studio | MCP `infrastructure__*` or an `Instance` CR | Same object the portal writes |
| Run a hosted agent, manage schedules and channels | MCP `agents__*` or REST at `$HUB/services/providers/agents/api/...` | Runs are not CRDs |
| kubectl on an edge cluster | `faros kubeconfig edge <name>` or `faros connect <name>` | Proxied through the hub as you |
| Shell on a Linux edge | `faros ssh <name> [-- cmd]` | No port forwarding |
| Query objects across all edges | MCP `kuery__kuery_query` or `POST $HUB/services/providers/kuery/api/query` | Read-only, declared relations |
| Graph-style reads of any workspace API | `POST $HUB/graphql/<cluster>` | What the portals use; group dots become underscores |

REST calls to a provider need three headers: `Authorization: Bearer`,
`X-Faros-Org`, `X-Faros-Workspace`. Errors come back as Kubernetes `Status`
JSON. Lists are `{"items":[…]}`.

**Two things to settle before you pick a row.** First, an `MCP <provider>__*`
cell is a promise only if that provider is platform-scoped in this org —
org-scoped providers are excluded from the aggregate, so `infrastructure__*`
may simply not exist for you. Second, you do not need an MCP client to use
those tools: the endpoint is plain JSON-RPC over HTTP. Both are in section 9.

## 4. Playbook: build and ship an app with App Studio

`AS=$HUB/services/providers/app-studio` below. Full route table and payloads
in [references/app-studio.md](references/app-studio.md).

### Step 0: preconditions

```bash
curl -s "$AS/api/projects/create-readiness" -H "$A" $T
# {"gitConnection":{"ready":true,"status":"ready","connectionRef":"github-default"}}
```

If `status` is `connection-missing`, create a GitHub connection. It is a
Secret plus a `Connection` CR in your workspace (namespace `default`). The
PAT needs `repo, workflow, delete_repo, read:org, admin:public_key, read:packages`.

```bash
kubectl create secret generic gh-token --from-literal=token="$GITHUB_PAT"
kubectl apply -f - <<'EOF'
apiVersion: code.faros.sh/v1alpha1
kind: Connection
metadata: { name: github-default }
spec: { provider: github, type: pat, owner: <github-org-or-user>, secretRef: { name: gh-token } }
EOF
kubectl get connection github-default -o jsonpath='{.status.conditions}'   # wait for Validated=True
```

The portal alternative is "Connect with GitHub" at `/ui/providers/code/connections`
(OAuth, browser only).

If you will use the assistant, App Studio needs a model. Settings are
workspace-wide, OpenAI-compatible, key is write-only:

```bash
curl -s -X POST "$AS/api/projects/llm-settings/models" -H "$A" $T -H 'Content-Type: application/json' \
  -d '{"name":"main","provider":"openai-compatible","baseURL":"https://api.anthropic.com/v1","model":"claude-sonnet-4-5","apiKey":"…"}'
curl -s -X POST "$AS/api/projects/llm-settings/test" -H "$A" $T
curl -s "$AS/api/projects/llm-settings" -H "$A" $T          # configured:true, defaultModelID
```

### Step 1: pick a template

```bash
curl -s "$AS/api/projects/development-templates" -H "$A" $T
```

| Template | Shape | Public URL | Dev toolchain |
|---|---|---|---|
| `application` | `web/` + `api/` + Postgres behind one host, `/api/*` routed to the api container | yes | Node.js only |
| `simple-webapp` | one container, one port | yes | Node.js only |
| `worker` | Deployment, no Service | no | |
| `universal-coding-sandbox` | scratch sandbox | no | disabled by default |

Read the template's `spec.agent.usage` before writing code; it is the
runtime contract (bind `0.0.0.0`, `$PORT`, `DATABASE_URL`, retry the first
DB connect, same-origin `/api/*`):

```bash
kubectl get template application -o jsonpath='{.spec.agent.usage}'
```

For a blueprint from a prompt without creating anything:
`POST $AS/api/projects/plan {"prompt":"…","templateName":"application"}`.

### Step 2: create the project

**One call creates everything, and it must be this call.** App Studio owns
the set — repository, first commit, dev instance — and ownership is what
later makes promotion and publishing work. There is no supported way to
assemble a project out of parts you created yourself.

| Want | Do | Not |
|---|---|---|
| A new project | `POST /api/projects` with `templateName` | Create a `Repository` CR, then an `Instance`, and hope they get adopted |
| A project around a repo that already exists | `POST /api/projects` with `existingRepositoryRef` (candidates from `GET …/import-repositories`) | Point a new project at a repo by name and expect it to bind |
| Just a running container, no git loop | An `Instance` directly (section 6) | Create a project and delete the parts you did not want |

```bash
curl -s -X POST "$AS/api/projects" -H "$A" $T -H 'Content-Type: application/json' \
  -d '{"name":"shop","displayName":"Shop","templateName":"application","description":"Storefront demo"}'
# 201 → {"name":"shop","uid":"…","phase":"…","template":"application","repository":{…},"environments":[…]}
```

Other creation modes: `{"prompt":"…","inferDevelopmentTemplate":true}` lets
the assistant choose; `{"existingRepositoryRef":"<code Repository name>"}`
adopts an existing repository CR and hydrates the workspace from it
(candidates: `GET $AS/api/projects/import-repositories`). `POST $AS/api/projects/stream`
is the same call as SSE with progress events.

What happens, all from that one request: a `code.faros.sh` `Repository` CR is
created (private, `autoInit`), the code provider creates the GitHub repo, the
scaffold repo is seeded into the workspace and committed as the first commit,
and a dev instance `<project>-dev` (`farosMode: development`,
`access: private`) is provisioned on the infrastructure runtime cluster.

You will see those objects with `kubectl get repositories.code.faros.sh` and
`kubectl get instances.infrastructure.faros.sh`, which makes it tempting to
conclude you could have written them yourself. Read them, do not create them:
they carry ownership App Studio put there. The reverse is also true — deleting
the project deletes its dev instance, so do not treat those objects as
independently yours.

Wait and inspect. **`phase: Ready` on the project is not the gate** — the
project reports Ready while its repository is still being created, and a
`code__commit_files` against it fails with `repository "shop" not found`.
Poll `.repository.ready == true` before you commit anything; a fresh project
can report `repository.status: RepositoryMissing` for the first few seconds.

```bash
curl -s "$AS/api/projects/shop" -H "$A" $T | jq '{phase, repoReady: .repository.ready, repoStatus: .repository.status}'
curl -s "$AS/api/projects/shop" -H "$A" $T | jq '{phase, template, repository, environments}'
curl -s "$AS/api/projects/shop/checkpoints" -H "$A" $T     # Template / Git / CI / Production, each done|pending|blocked|error with remediation
kubectl get instance shop-dev -o jsonpath='{.status.phase} {.status.url}'
```

### Step 3: develop

There are three ways to change code. Pick one per session and do not mix
them mid-turn.

**A. The App Studio assistant (only path that writes files through App Studio).**

```bash
TH=thread-cart      # you may choose the id; omit it and read `.id` from the 201 response instead
curl -s -X POST "$AS/api/projects/shop/assistant/threads" -H "$A" $T -H 'Content-Type: application/json' -d "{\"id\":\"$TH\",\"title\":\"cart\"}"
curl -s -X POST "$AS/api/projects/shop/assistant/threads/$TH/turns" -H "$A" $T -H 'Content-Type: application/json' \
  -d '{"content":"Add a cart page backed by /api/cart. Do not commit.","clientUserMessageID":"m1","collaborationMode":"Default"}'
curl -N "$AS/api/projects/shop/assistant/threads/$TH/events" -H "$A" $T      # SSE; Last-Event-ID resumes
curl -s "$AS/api/projects/shop/assistant/threads/$TH/turns/active" -H "$A" $T # 204 when idle
```

Approvals and questions arrive as events; answer with
`POST …/turns/{turn}/approval {"requestID":"…","decision":"allow"}` or
`…/input {"requestID":"…","answer":"…"}`. `collaborationMode` is `Default`,
`Plan`, or `Review`. Steer a running turn with `…/turns/{turn}/steer`,
stop it with `…/interrupt`. The assistant's `exec_command` is the only way to
run commands inside the sandbox from outside.

**B. Local editor plus `code__commit_files` (recommended for a coding agent).**

```bash
REPO=$(curl -s "$AS/api/projects/shop" -H "$A" $T | jq -r .repository.htmlURL)
git clone "$REPO" shop && cd shop            # GitHub credentials, not a faros token
# edit locally, run tests locally, then commit THROUGH faros:
```

Call the MCP tool `code__commit_files` with
`{"repositoryRef":"shop","branch":"main","message":"Add cart","files":[{"path":"api/cart.js","content":"…"}],"deletePaths":[]}`.
Limits: 500 files, 2 MiB per file, 16 MiB total, UTF-8 text only. A helper
that turns `git diff --name-status <base>` into that payload keeps the loop
honest.

**If you have no MCP client wired, you can still call it.** The aggregate is
plain JSON-RPC over HTTP, so a shell session is not blocked — see section 9.2
for the exact invocation. This matters because `code__commit_files` is the
only way to record a promotable commit, so "I have no MCP tools" is never a
reason to fall back to `git push`.

Then pull the workspace and sandbox up to date:

```bash
curl -s -X POST "$AS/api/projects/shop/hydrate-workspace" -H "$A" $T -H 'Content-Type: application/json' -d '{}'   # git → workspace
curl -s -X POST "$AS/api/projects/shop/sync-development" -H "$A" $T                                                # workspace → sandbox
```

Then `git pull` locally so your clone matches the commit faros created.

**C. Plain `git push`.** Works for CI and for the GitHub repo, but see rule 12:
App Studio will not see the commit for promotion. Use it only when you plan to
deploy with the infrastructure provider directly (section 6) or when the
user accepts that limitation.

**Preview, logs, restart** (any path):

```bash
curl -s -X POST "$AS/api/projects/shop/authorize-development-preview" -H "$A" $T
# {"ready":true,"previewURL":"https://shop-dev-<hash>.<domain>","desiredAccess":"private",…}
curl -N "$AS/api/projects/shop/development-logs?component=api" -H "$A" $T
curl -s "$AS/api/projects/shop/development-status" -H "$A" $T
curl -s -X POST "$AS/api/projects/shop/restart-development?component=api" -H "$A" $T
curl -s "$AS/api/projects/shop/files" -H "$A" $T
curl -s "$AS/api/projects/shop/files/content?path=api/cart.js" -H "$A" $T
```

Dev previews are private by default; `POST …/preview {"mode":"public"}` opens
them, `…/preview/grants {"user":"…"}` invites a workspace member.

Expect **409** on template switch, hydrate, sync, and delete while an
assistant turn owns the project. Wait for `turns/active` to return 204.

### Step 4: build

Every commit on the default branch runs the scaffold's
`.github/workflows/build.yaml`, which pushes one image per component to ghcr
tagged `sha-<commit>`. The code provider crawls ghcr into `Package` CRs every
two minutes. A release is promotable only when every component has a digest
for the exact newest faros-recorded commit.

```bash
curl -s "$AS/api/projects/shop/promotion" -H "$A" $T | jq '{promotable, build: .build.status, missing: .build.missing, commit: .build.commitSHA}'
curl -s "$AS/api/projects/shop/releases" -H "$A" $T
```

If `build.status` is `incomplete` or `none`, check CI with MCP
`code__build_status {"repositoryRef":"shop","workflowFileName":"build.yaml","maxLogLines":200}`
and re-run with `code__rebuild`. Both need the `workflow` scope on the PAT.

**`none` usually means "not yet", not "wrong".** There are two distinct causes
and they need opposite responses:

| `build.commitSHA` | Meaning | Do |
|---|---|---|
| Your commit's SHA | CI has not finished, or the ghcr crawler has not run | Wait. The crawl is every two minutes, so `none` for a couple of minutes *after* a green CI run is normal |
| Missing, or an older SHA | The commit was not recorded through faros | Re-commit with `code__commit_files` |

Measured on a dev hub in 2026-09: image build ~3.5 min, then `built` roughly
two minutes after CI went green. Budget about six minutes from commit to
promotable and do not diagnose anything before then.

### Step 5: promote to production

```bash
curl -s -X POST "$AS/api/projects/shop/promote" -H "$A" $T -H 'Content-Type: application/json' \
  -d '{"values":{"access":"private","expose":{"hostnamePrefix":"shop"}}}'
# {"environment":"production","instance":"shop-prod","rolloutRevision":"…","commitSHA":"…","components":[…]}
kubectl get instance shop-prod -o jsonpath='{.status.phase} {.status.url}'
```

Promotion creates or updates one infrastructure `Instance` named
`<project>-prod` with `farosMode: production` and each image input pinned
to the built digest. `name`, `farosMode`, image inputs, `farosRedeployRevision`,
`farosCluster`, `credentialsSecretName` are platform-owned; anything you pass
for them is overridden. `expose.hostnamePrefix` is settable on first deploy
only. Re-promoting keeps the instance name, URL, and grants and rolls only
pods. Pass `{"commitSHA":"…"}` to promote a specific faros-recorded commit.

### Step 6: publish and share

```bash
curl -s "$AS/api/projects/shop/publishing" -H "$A" $T
curl -s -X POST "$AS/api/projects/shop/publishing" -H "$A" $T -H 'Content-Type: application/json' -d '{"mode":"public"}'      # or restricted
curl -s -X POST "$AS/api/projects/shop/publishing/grants" -H "$A" $T -H 'Content-Type: application/json' -d '{"user":"<user>","invite":"person@example.com"}'
curl -s -X DELETE "$AS/api/projects/shop/publishing" -H "$A" $T          # back to private, drops grants
```

Visibility is the prod instance's `access` value behind an
infrastructure-owned access gate; changing it never redeploys. Grants are
plain kcp RBAC (ClusterRole `faros-app-access.<instance>` plus one binding
per member).

### Step 7: integrations, skills, memory

- **Integrations** bind a provider resource and a versioned action to the
  project, e.g. Databricks `query_table/v1`:
  `POST $AS/api/projects/shop/integrations {"alias":"sales","provider":"databricks","kind":"providerReference","resourceRef":{"apiVersion":"databricks.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"order-history"},"allowedActions":[{"name":"query_table","version":"v1","schemaDigest":"sha256:…"}],"consentAccepted":true}`.
  The app calls it with the server-only `@faros/actions-node` SDK. Digest and
  action version come from `GET $HUB/api/providers`; never guess them.
- **Skills** for the assistant live in the repo at
  `.agents/skills/<package>/SKILL.md` (frontmatter `name` and `description`
  only; any other field is rejected). Toggle with
  `POST $AS/api/projects/shop/assistant/skills/activation {"id":"<qualified id>","enabled":false}`.
  Provider skills are qualified `providers/<provider>/<package>`.
- **Memory**: `PATCH $AS/api/projects/shop/memory {"goals":[…],"requirements":[…],"constraints":[…]}`
  is injected into every assistant turn. A root `AGENTS.md` in the repo is
  also injected (32 KiB cap).
- **Delete**: `DELETE $AS/api/projects/shop?uid=<project uid>`. The
  repository CR and GitHub repo survive; the dev instance does not. Confirmed
  on a live hub 2026-09-09: a deleted project left its `Repository` CR and
  GitHub repo in place with nothing owning them.
  That leaves an **orphan you will meet again**. Project names can be reused
  after the async delete, but the `Repository` of the same name is still
  there, so recreating a project under the old name is not a clean slate.
  Check first, and either adopt the orphan or pick a different name:

  ```bash
  curl -s "$AS/api/projects" -H "$A" $T | jq -r '.items[].name'   # projects
  kubectl get repositories.code.faros.sh                          # repositories, some possibly orphaned
  ```

  To adopt: `POST /api/projects {"existingRepositoryRef":"<repo>"}`. To
  discard: delete the `Repository` CR — but that **deletes the GitHub repo
  with it** (rule 9), so ask first.

## 5. Playbook: local development discipline

Use this when the user wants to work in their own editor against an App
Studio project and still ship through App Studio.

1. Confirm the project is settled: `GET …/assistant/threads/…/turns/active` is
   204 for every thread, and `GET …/checkpoints` shows Git `done`.
2. Clone `repository.htmlURL`. Never push to `main` directly (rule 12).
   Local branches are fine for your own iteration.
3. Make the change locally. Run the project's own tests locally. The dev
   toolchain in the sandbox is Node.js only for `application` and
   `simple-webapp`; a Dockerfile does not change that.
4. Record the change through faros with `code__commit_files` (section 4,
   step 3B). One call per logical commit. Deleted files go in `deletePaths`.
5. `POST …/hydrate-workspace {}` then `POST …/sync-development`. Check
   `authorize-development-preview` and `development-logs`.
6. `git pull` locally. Wait for `GET …/promotion` to report `built`, then promote.
7. If the user only needs the dev sandbox and not App Studio's git loop,
   skip App Studio: provision an `Instance` with `farosMode: development`
   through `infrastructure__provision` and push files with
   `infrastructure__dev_sync` (16 MiB cap, paths must fall under a declared
   component `workspacePath`). Logs: `infrastructure__dev_logs`.

## 6. Playbook: deploy without App Studio

**Choose this path deliberately, at the start.** It is a different product,
not a lower-level way to reach the same place: you get a running workload with
no repository, no CI, no promotion, and no publishing flow. Nothing here grows
into an App Studio project later, and an `Instance` you create by hand will
never be adopted by one. Pick it when the user wants a container or a database
running and has not asked for a git-backed app; pick section 4 otherwise.

The infrastructure provider exposes exactly two kinds in your workspace:
`Template` (read-only catalog) and `Instance`. Which product an instance is
lives in `spec.template`; the template's JSON schema shapes `spec.values`.

```bash
kubectl get templates                     # simple-webapp, application, worker, cron-job, database, redis-cache, browser, searxng
kubectl get template simple-webapp -o jsonpath='{.spec.schema}' | jq .
kubectl apply -f - <<'EOF'
apiVersion: infrastructure.faros.sh/v1alpha1
kind: Instance
metadata: { name: hello, labels: { faros.sh/template: simple-webapp } }
spec:
  template: simple-webapp            # immutable
  values:
    name: hello
    image: ghcr.io/you/hello:v1      # required in production mode
    port: 8080
    replicas: 1
    env: { LOG_LEVEL: info }         # world-readable; never secrets
    access: public                   # or private (platform sign-in)
EOF
kubectl get instance hello -o jsonpath='{.status.phase} {.status.url}'
```

- URL shape: `https://<hostnamePrefix|name>-<12-hex tenant hash>.<platform base domain>`.
  Custom domains are not supported. TLS terminates at the platform Gateway.
- Private images: create a `dockerconfigjson` Secret named `<instance>-registry`
  in namespace `default` of your workspace before applying; the controller
  bridges it to the runtime namespace.
- Update in place with MCP `infrastructure__update_instance {"name":"hello","values":{"image":"ghcr.io/you/hello:v2"}}`
  (RFC 7386 merge patch) or `kubectl patch`. Rejected for immutable fields
  (`name`, `farosMode`, platform-stamped fields, template-declared immutables
  such as `database.version`).
- Invalid values are admitted and reported as condition `Valid=False`
  reason `InvalidValues`; the last good runtime keeps running.
- `database` (Postgres) and `redis-cache` are `exposure: internal`: no URL,
  consumed pod-to-pod inside the runtime cluster. Outputs land in
  `status.host`, `status.port`, and a connection Secret named in status.
- Workloads run on the provider's private runtime cluster. You cannot kubectl
  into it; production instances have no exec or log path. Development-mode
  instances expose `log`, `sync`, `restart`, `exec` through the data plane
  and the `infrastructure__dev_*` tools.
- Never set `expose.fqdn`, `farosCluster`, `credentialsSecretName`,
  `farosRedeployRevision`, `farosNetworkPhase`, or `farosActions*`.

## 7. Playbook: hosted agents

`AG=$HUB/services/providers/agents`. Agents are CRs (`agents.faros.sh/v1alpha1`:
`Agent`, `Connection`, `Schedule`, `Trigger`, `Toolset`); runs, sessions, and
memories live in the provider's database and are reachable only through REST
or MCP. Only OpenAI-compatible model endpoints are implemented; the tenant
brings the API key.

```bash
# 1. model credential (write-only apiKey; Secret faros-agents-model-<name> in ns default)
curl -s -X POST "$AG/api/credentials" -H "$A" $T -H 'Content-Type: application/json' \
  -d '{"name":"main","provider":"openai-compatible","baseURL":"https://api.anthropic.com/v1","model":"claude-sonnet-4-5","apiKey":"…"}'
curl -s -X POST "$AG/api/credentials/main/test" -H "$A" $T
# 2. agent
curl -s -X POST "$AG/api/agents" -H "$A" $T -H 'Content-Type: application/json' \
  -d '{"name":"researcher","displayName":"Researcher","systemPrompt":"You are a careful research assistant. Cite sources.","autonomy":"auto","modelCredential":"main","interactiveFamilies":["core","web","spawn"],"backgroundFamilies":["core","web"],"budgetUSD":"25"}'
# 3. invoke and wait (wait caps at 120 s)
curl -s -X POST "$AG/api/agents/researcher/runs" -H "$A" $T -H 'Content-Type: application/json' \
  -d '{"task":"Compare X and Y with sources.","wait":120,"idempotencyKey":"cmp-1"}'
curl -s "$AG/api/runs/$RUN_ID/wait?timeoutSeconds=300" -H "$A" $T
curl -s "$AG/api/runs/$RUN_ID" -H "$A" $T | jq '{phase, output, sources, steps, children}'
```

Same thing over MCP: `agents__save_model_credential`, `agents__create_agent`,
`agents__update_agent`, `agents__run_agent {"agent":"researcher","task":"…","wait":120}`,
`agents__get_run {"runId":"…","wait":300}`. `wait` blocks, so the MCP client's
transport timeout must exceed it.

Things that surprise people:

- `run_agent` and `POST /runs` are **background** runs: they use
  `tools.background` and never get edge or aggregate-MCP access. Only chat
  and channel runs are interactive and get `edges__*` tools automatically.
- Deep research is not a flag. Grant `spawn` plus `web`; the agent fans out
  workers and joins them. `spawn` without `web` means workers answer from
  the model alone.
- Update semantics: only fields you pass change; list fields replace wholesale.
- Channels: create a `Connection` (`telegram`, `slack`, `discord`, `smtp`),
  bind it in `channels[]` on the agent, then
  `POST /api/connections/{name}/enable-inbound {"publicBaseURL":"https://<hub>"}`.
  A connection can be the inbound channel of one agent only. OAuth connect
  flows are portal-only.
- Approvals in `GET /api/inbox?state=pending` are resolved by a human with
  `POST /api/inbox/{id}/resolve`; there is deliberately no MCP tool for it.

## 8. Playbook: edges

```bash
faros edge create home-lab --labels env=home         # Kubernetes edge (default type)
faros edge create my-vps --type server               # Linux server
faros edge join-command home-lab                     # reprint the helm / agent join commands
faros edge list                                      # NAME TYPE PHASE CONNECTED AGENT VERSION AGE
kubectl get kubernetesclusters.edges.faros.sh,linuxservers.edges.faros.sh -o wide

faros kubeconfig edge home-lab > home-lab.kubeconfig  # kubectl through the hub, as you
kubectl --kubeconfig home-lab.kubeconfig get nodes
faros connect home-lab && kubectl get pods -A && faros connect :   # in-place kubeconfig switch

faros ssh my-vps -- uptime                            # server edges; no -L/-R
faros mcp url --edge home-lab                         # per-edge MCP (Kubernetes edges only)
```

On the aggregate MCP endpoint the edges provider contributes the
kubernetes-mcp-server toolset (`edges__pods_list`, `edges__pods_log`,
`edges__pods_exec`, `edges__resources_create_or_update`, `edges__helm_install`,
…) with a `cluster` parameter to pick the edge, `edges__cluster_list` to
enumerate edges, and one tool bundle per discovered `Service`
(`edges__<service>_<tool>`, e.g. `edges__ha_call_service` for Home Assistant).
Fleet-wide reads across edges go through `kuery__kuery_query` and
`kuery__kuery_impact`.

## 9. Calling the platform from a shell, and reading its failures

Everything in this section was learned by driving a live hub, not by reading
code, and every claim below was re-tested against one. None of it is visible
in the source, and each item cost real time before it was written down.

### 9.1 A 403 is often the HTTP client, not your token

The hub sits behind Cloudflare, which blocks requests by user agent. Python's
default (`Python-urllib/…`, and `requests` likewise) is rejected with a **403
whose body is Cloudflare error 1010**, `browser_signature_banned`.

It is easy to misread. The status is 403, the same as a permissions failure,
and it fires identically for a valid user bearer and a valid ServiceAccount
token — so it looks like "my token lacks permission" and sends you into RBAC,
claims, and membership for nothing.

Measured against `/api/orgs` on 2026-09-09 with one identical valid token:

| Client | Result |
|---|---|
| `curl` | 200 |
| Python, default user agent | 403, `error code: 1010` |
| Python, `User-Agent: Mozilla/5.0 (…)` | 200 |

So the rule is about the header, not the language. **Prefer `curl`.** If you
want Python's ergonomics for building a large JSON body, build the file there
and send it with `curl --data-binary @payload.json`; that also sidesteps
shell-quoting a payload with embedded code. If you must use Python for the
request itself, set a browser-like `User-Agent` explicitly.

Tell it apart from a real 403 by reading the body: `error_code: 1010` or any
mention of `cloudflare` is the edge. A genuine faros denial comes back as a
Kubernetes `Status` JSON.

### 9.2 The MCP aggregate is plain HTTP; no MCP client required

`tools/call` is reachable with `curl`, which means a shell session can use
`code__commit_files` and every other tool. Get the endpoint and a long-lived
token from the connect route (section 2.5), then:

```bash
curl -s -X POST "$MCP_URL" \
  -H "Authorization: Bearer $MCP_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'

# tools/call takes {"name": "<provider>__<tool>", "arguments": {…}}
curl -s -X POST "$MCP_URL" -H "Authorization: Bearer $MCP_TOKEN" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  --data-binary @payload.json
```

Three parsing details, all verified 2026-09-09:

- **`Accept` must contain both `application/json` and `text/event-stream`.**
  Anything else is a `400` with the body
  `Accept must contain both 'application/json' and 'text/event-stream'`.
- **Replies are always SSE**, even for a single result:
  `content-type: text/event-stream`, an `event: message` line, then
  `data: {…}`. Strip the leading `data: ` and parse the last JSON object.
- **A failing tool still returns HTTP 200.** The failure is prose inside
  `result.content[].text` — that is where `repository "x" not found` and the
  GitHub rate-limit message arrive. Check the body, never just the status.

### 9.3 `tools/list` is the only honest inventory

Which tools exist depends on which providers are **platform-scoped in your
org**, because org-scoped (BYO) providers are excluded from the aggregate.
This is not hypothetical: on a hub where `infrastructure` was `scope: org`,
the aggregate carried 69 tools from `agents`, `code`, `edges` and `kuery`
only — **no `infrastructure__*` at all**, so every infrastructure action had
to go through kubectl or provider REST instead.

Call `tools/list` and read the prefixes before planning a route that depends
on a tool. Being *enabled* predicts nothing — an org-scoped provider can be
enabled, Ready, and working perfectly over kubectl while contributing no MCP
tools at all. The catalog's `scope` field is a good predictor (`org` means
excluded), but `tools/list` is the only authority. `app-studio` is always
absent by design: it is an MCP client, not a server, and returns 404 on `/mcp`.

When a tool you expected is missing, the fix is a different surface, not a
retry: kubectl against the workspace, or the provider's REST API.

### 9.4 Latency is not failure

Several states look like errors for the first few minutes of a resource's
life. Distinguish "not yet" from "wrong" before acting, because the response
to the two is opposite — wait, or rebuild.

| Symptom | Usually | Confirm it is only latency |
|---|---|---|
| New `Instance` URL fails the TLS handshake (curl exit 35) | The certificate for that hostname is still being issued; allow five to ten minutes | An older instance on the same base domain serves 200, and `openssl s_client -connect <host>:443 -servername <host>` shows no matching subject CN yet |
| `promotion.build.status: none` right after a green CI run | The ghcr crawler runs every two minutes | `build.commitSHA` already equals your commit |
| Fresh project, `repository.status: RepositoryMissing` | The `Repository` CR is still being created | `phase` is Ready but `.repository.ready` is false; poll it |
| `Instance` phase Ready but no `status.url` | Not latency. The template is `exposure: internal` and never gets one | `kubectl get template <t> -o jsonpath='{.spec.exposure}'` |

The last row is the one that is *not* latency, and it is why "wait and retry"
must never be the automatic response.

### 9.5 A rate-limited GitHub PAT reports itself as a scope problem

`code__commit_files` surfaces GitHub rate limiting as
`github: forbidden — token lacks scope or rate-limited (403)`. The wording
invites you to go fix PAT scopes when nothing is wrong with them. Read the
detail after the colon: `API rate limit exceeded for user ID …` with a
`rate reset in …` hint.

The limit is **per token**, so `gh api rate_limit` showing 5000/5000 proves
nothing about the PAT in the code `Connection` — that is a different token for
the same user. Wait for the window and retry; each failed attempt leaves a
`RepositoryCommit` in phase `Failed`, so a repository full of failed commits
is the signature of a loop that retried through a rate limit.

## 10. Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `no hub configured` | Set `--hub-url` or `FAROS_HUB_URL` |
| `no interactive terminal; pass --org and --workspace` | You are in CI; pass both flags to `faros use` |
| 400 `no workspace selected` on a bare `/api` call | Use `/clusters/<clusterName>/…` |
| 403 `address workspaces by cluster ID` | You used a `root:…` path; resolve `clusterName` via `/api/orgs/{org}/workspaces` |
| 403 on a provider REST call | Missing `X-Faros-Org` or `X-Faros-Workspace`, or you are not a member |
| `faros mcp url` prints `Bearer <your-token>` | OIDC hub; use the `mcpservers/default/connect` endpoint for a token |
| 401 on the MCP endpoint | Bearer missing or not a member of that workspace; 429 means retry after 60 s |
| `create-readiness` → `connection-missing` | Create a code `Connection` (section 4 step 0) |
| 409 on template, hydrate, sync, or delete | An assistant turn owns the project; wait for `turns/active` to be 204 |
| `promotion.build.status` is `none` after pushing | The commit was not recorded through faros; use `code__commit_files` |
| `promotion.build.status` is `incomplete` | Some component has no `sha-<commit>` image; run `code__build_status`, then `code__rebuild` |
| Instance `Valid=False` / `InvalidValues` | `spec.values` violates the template schema; read `describe_template` |
| Instance ready but no `status.url` | Template `exposure: internal`; never poll for a URL |
| Agent run has no edge tools | Background run; only chat and channel runs get `edges__*` |
| Agent MCP tools say "open the agents UI once" | The provider has not seen this workspace over the UI path yet; open the Agents page once, retry |
| `faros ssh` fails on an OIDC hub | Known gap: the ssh path only sends a bearer when the kubeconfig carries a literal token |
| 403 whose body mentions `cloudflare` or `error_code: 1010` | Not authorization. Cloudflare blocked the HTTP client; use `curl` (section 9.1) |
| `code__commit_files` → `repository "<name>" not found` | The project is Ready but its `Repository` is not; poll `.repository.ready` (section 4 step 2) |
| `github: forbidden — token lacks scope or rate-limited` | Read past the colon. Usually the PAT's hourly limit, not scopes (section 9.5) |
| New instance URL fails TLS, curl exit 35 | The hostname's certificate is still being issued; wait (section 9.4) |
| An `<provider>__*` tool does not exist | That provider is org-scoped, so it is excluded from the aggregate; use kubectl or its REST API (section 9.3) |
| MCP endpoint returns `400 Accept must contain both …` | Send `Accept: application/json, text/event-stream` (section 9.2) |

## 11. Known stale documentation

These claims appear in older docs or READMEs and are wrong as of 2026-09-09.
Do not repeat them:

- `faros mcp url --name default` (flag is `--mcpserver-name`).
- A hosted hub at `console.faros.sh` as the CLI default (removed).
- `list_targets` on the aggregate MCP endpoint (does not exist; use `edges__cluster_list`).
- An `MCPServer.spec` edge label selector (no such field).
- Linux/SSH MCP tools for server edges (none; use `faros ssh`).
- `kro_*` tool names on the infrastructure provider (now `list_templates`, `provision`, …).
- `Application`, `PostgresDatabase` as tenant kinds (they are runtime-internal; tenants see `Instance`).
- **"Built-in providers (edges, mcp, quickstart) need no enabling."** This
  skill said that until 2026-09-09. `edges` is an ordinary catalog provider
  with an APIExport and a binding; `mcp` is a hub endpoint rather than a
  provider; neither `mcp` nor `quickstart` appeared in the catalog at all, and
  no entry advertised `builtin`. Trust `GET /api/providers` over any list of
  provider names written down anywhere, including here.
- `Edge` and `VirtualWorkload` kinds in `faros.sh/v1alpha1` (only `MCPServer` remains).
- Workspace paths `root:faros:orgs:…` (code uses `root:faros:tenants:…`, and you never type them anyway).
- `timeoutSeconds` in the agents invoke body (not implemented).
