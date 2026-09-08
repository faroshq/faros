# Coverage and verification boundaries

Companion to the [prioritized audit](README.md). Route discovery came from the host router, runtime provider registration, each provider's route parser or view switch, and the components mounted by those entries. The sample follows actual ownership boundaries: shared resource primitives were inspected canonically, repeated provider collections were sampled, and workbenches, wizards, graph surfaces, and deviations were inspected separately.

**Legend:** “Source” means relevant implementation and composition inspected, not a line-by-line review of every file. “Viewed” means rendered in the existing local stack. “Exercised” lists the specific interaction and outcome; merely loading a URL does not count as completing its journey. “Gap” records unavailable data, deliberately unperformed writes, and unverified behavior. A linked screenshot is one representative frame, not proof of an entire flow.

Paths below are application URLs. Host routes start at `/ui`; provider-relative routes append to `/ui/providers/<name>`. The source paths linked below are repository files. The complete source/runtime baseline, theme method, and limitations are in the main report.

## Host routes and shell

Authority: [host router](../../../portal/src/router/index.ts), [provider registration store](../../../portal/src/stores/providers.ts), [ProviderFrame](../../../portal/src/pages/ProviderFrame.vue), and [AppLayout](../../../portal/src/components/AppLayout.vue). Provider routes are registered dynamically; absence from the static route array does not mean they are absent from the product.

| Surface / route | Source | Viewed | Exercised / evidence | Gap |
| --- | --- | --- | --- | --- |
| Login `/ui/login`; authentication callback `/ui/auth/callback` | Login, callback, auth guard | Token login, including [390px](evidence/login-narrow.png) | Local development sign-in → dashboard | OIDC/Dex redirect, expired session, and invalid real identity not exercised |
| Dashboard `/ui/` | DashboardPage, dashboardLayout, provider tile composition | Populated six-tile dashboard, both themes, narrow | Warm resize and cold narrow comparison; expand/collapse shell. [Desktop](evidence/dashboard-dark.png), [resize](evidence/dashboard-resize-settled.png), [cold narrow](evidence/dashboard-cold-390.png) | No drag-layout save, cold empty account, or live metric history test |
| Navigation dock, Workspace control, account menu | AppLayout, WorkspaceSwitcher, AccountAccessMenu | Default icon rail, expanded rail, menu, Settings context | Sidebar expansion/collapse; [account menu](evidence/account-menu.png); keyboard focus in sampled controls; coarse-pointer targets measured | Moving/docking the rail, persisted layout, switching between two populated Workspaces, and alternate-role menu restrictions not completed |
| Provider catalog `/ui/providers` | ProvidersPage, provider state and self-hosting composition | Ready/degraded cards and Self-Host unavailable state | Catalog → provider; [disabled Self-Host explanation](evidence/provider-self-hosting.png) inspected | No connected Kubernetes edge; installation wizard completion, enable/disable, BYO registration and permissions acceptance not executed |
| Workspace Settings `/ui/settings/workspaces`, `/ui/settings/workspaces/:workspaceUUID`; `/ui/settings` redirect | TenantSettingsPage and scope contract | Active/inspected scope, overview and editable name state | Open rename → inspect → cancel; [overview](evidence/settings-workspaces-dark.png), [rename](evidence/workspace-rename.png) | No rename save, invitation, membership/role change, service-account creation, deletion, or multi-Workspace context switch |
| Organizations `/ui/organizations`, `/ui/organizations/new`, `/ui/settings/organizations` | OrganizationsPage, OrganizationCreatePage, TenantSettingsPage | [Chooser](evidence/organizations-dark.png), [creation](evidence/organizations-new-dark.png), [Settings](evidence/settings-organizations-dark.png) | Navigation and form inspection | No Organization creation or actual switch; available account scope is limited. Concurrent Settings change is recorded below |
| MCP `/ui/mcp`, `/ui/create/mcp-server`, `/ui/mcp/:name` | MCPPage, shared creation/table/confirmation | Empty/populated collections, guided creation, detail, masked setup snippets | Complete temporary local lifecycle: create → detail → reload → list → cancel deletion via Escape → explicit deletion → absence verified. [Created](evidence/mcp-created.png), [cleanup](evidence/mcp-cleanup.png) | No external MCP-client call, live downstream tool invocation, or token rotation |
| Platform admin `/ui/bonkers/providers`, `/identities`, `/organizations`, `/users` under `/bonkers` | Bonkers shell and four sections | All four rendered with local admin identity | Read-only navigation. [Providers](evidence/bonkers-providers-dark.png), [identities](evidence/bonkers-identities-dark.png), [Organizations](evidence/bonkers-organizations-dark.png), [users](evidence/bonkers-users-dark.png) | Admin mutations, non-admin guard behavior, dense pagination and privilege changes not exercised |
| Help and CLI overlays | HelpSupportModal, CliQuickstartModal | [Help](evidence/help-dialog.png), [CLI setup](evidence/cli-dialog.png) | Help initially focused Close; Shift+Tab remained inside; Escape dismissed and returned focus. Account → CLI setup → inspect → dismiss | External support destinations, command copy/install, CLI authentication and all overlay variants unverified |
| Not-found route, welcome/setup and terminal | NotFoundPage, WelcomeWizard, TerminalDock | [404](evidence/not-found.png); welcome and terminal source-only | Invalid route rendered a clear dashboard return link | Welcome completion and terminal session/resize/keyboard unverified |

The checkout began at `e788ffda2eab2602c40da6760d6f63ae998ad191` with the local modifications listed in the main report. During the audit it advanced to `9bcd6f2860e26362bc3e04a009a577de4f1fc310` through PR #666, touching only AccountAccessMenu, TenantSettingsPage, and their Organization conformance test between those revisions. The audit did not make that change. Settings and Organization evidence is a working-session snapshot, not a single immutable release capture. Findings in unrelated dashboard, shared confirmation/tabs, provider framing, and provider sources are unaffected by that commit's file scope. The final working tree was checked for audit-owned changes separately.

## Edges

Authority: [routes.ts](../../../providers/edges/portal/src/routes.ts), [App.vue](../../../providers/edges/portal/src/App.vue), and the provider's list/detail/creation components.

| Route / surface | Source | Viewed | Exercised / evidence | Gap |
| --- | --- | --- | --- | --- |
| Root / `edges` collection | Route, data adapter, ResourceTable integration | One disconnected Kubernetes edge; light/dark/narrow/touch | Search for nonexistent name → no matches; clear → row returns. Custom filter opened/navigated with keyboard; Enter selected Kubernetes and retained the matching row. Refresh failure, retained row, retry recovery. [Collection](evidence/providers-edges-dark.png), [no match](evidence/edge-no-match.png), [filter](evidence/custom-filter-keyboard.png), [selected](evidence/custom-filter-selected.png) | No populated mixed Kubernetes/Linux fleet; no server-side continuation sequence or bulk operation |
| `edges/kubernetes/dev-edge-kube-1`; Linux detail sibling | Detail composition, tabs, conditions, terminal integration | [Kubernetes detail](evidence/edge-detail-dark.png) | Deep link and detail read | Live terminal, Kubernetes resource browsing and Linux detail lacked connected targets |
| `connect/edge` wizard | Wizard stages, kind choice, form and post-create instructions | Configure step at desktop/narrow, prerequisites and guidance | Form/navigation inspection; [connect](evidence/edge-connect-dark.png) | Did not create an edge, install an agent, or reach connected completion |
| `services`, `create/service`, service detail/edit | Service list, presets, create/detail composition | Empty list; [creation](evidence/service-create-dark.png) | Form affordance and prerequisite inspection | No host/LAN service connection, credential submission, saved resource or tool call |
| `workloads`, `deploy/workload/manual`, marketplace path | Workload views, deployment and marketplace composition | Empty list, [manual deploy](evidence/workload-deploy-dark.png) | Entry and form inspection | No cluster deployment, chart installation, workload health/logs or marketplace completion |

## Infrastructure

Authority: [routes.ts](../../../providers/infrastructure/portal/src/routes.ts), [App.vue](../../../providers/infrastructure/portal/src/App.vue), and [views](../../../providers/infrastructure/portal/src/views/).

| Route / surface | Source | Viewed | Exercised / evidence | Gap |
| --- | --- | --- | --- | --- |
| Root / `templates` | Template catalog, groups, card/action composition | Populated catalog, both themes, narrow | Template action → actual `templates/database` route. [Catalog](evidence/providers-infrastructure-dark.png) | Not every template schema or organization-specific template sampled |
| `templates/:id`, representative `templates/database` | Dynamic schema form, provisioning state, help and summary | [Postgres provisioning](evidence/postgres-provision.png) | Actual catalog-to-form navigation; defaults and guidance inspected | No cloud/cluster provisioning write, success, rollback or create failure |
| `instances` | Instance collection and shared resource reads | Four instances, Ready and Pending, long names | List → existing detail. [Instances](evidence/instances-dark.png) | No scale/delete/upgrade write or populated pagination |
| `instances/daily-dog-reminder-dev` | InstanceDetailPage, dynamic view fallback, conditions | Pending, zero reported child resources, JSON values | Read-only detail/conditions. [Detail](evidence/instance-detail-dark.png) | Cause of Pending not diagnosed; runtime logs/data plane/private backend deliberately outside audit |
| `missing-credentials` | MissingCredentialsPage and route | Source only | None | Credential failure/remediation state not present in the live sample |

## Code

Authority: [routes.ts](../../../providers/code/portal/src/routes.ts), [App.vue](../../../providers/code/portal/src/App.vue), and provider resource/creation components. **The host blocked provider mounting because Code was degraded.** All rows below are source-only for the provider UI; the [degraded host surface](evidence/providers-code-dark.png) was viewed live. No inference of visual quality is made from source alone.

| Route / surface | Source | Viewed / exercised | Gap |
| --- | --- | --- | --- |
| Root / `connections`, `connections/:name` | Connection collection/detail and actions | Host unavailable state only | Actual connection data, edit/delete and recovery |
| `create/connection/token`, `create/connection/github` | Token and GitHub App setup forms | Not mounted | Validation, OAuth/App redirect, secret handling during submit, saved outcome |
| `repositories`, `repositories/:name` | Repository collection/detail and tabs | Not mounted | Files/branches/collaborators/deploy-key interactions and external Git state |
| `create/repository` | Guided creation and completion navigation | Not mounted | Real creation, conflict, cancellation during save |
| `packages` | Package collection and states | Not mounted | Populated package metadata, navigation and filtering |

No attempt was made to repair the provider. The audit's finding concerns the host's recovery affordance, not an inferred Code implementation defect.

## Databricks

Authority: [route.ts](../../../providers/databricks/portal/src/route.ts), [App.vue](../../../providers/databricks/portal/src/App.vue), and [ResourceImportWizard](../../../providers/databricks/portal/src/ResourceImportWizard.vue).

| Route / surface | Source | Viewed | Exercised / evidence | Gap |
| --- | --- | --- | --- | --- |
| Root / `connections`; `connections/prod` | Collection/detail and state adapters | Ready connection, detail, both themes | List and deep-link reads. [Detail](evidence/db-connection-dark.png) | No test/rotate/delete credential action |
| `warehouses`, `warehouses/serverless-starter-warehouse` | Warehouse collection/detail | Ready registered warehouse | View detail; open split action menu. [Warehouse](evidence/warehouse-detail.png), [menu](evidence/db-split-menu.png) | No external warehouse start/stop or registration write |
| `tables`; `tables/samples-accuweather-forecast-daily-calendar-imperial` | Table collection/detail, schema columns table | Ready table; 64 cached of 88 columns with explicit truncation | List → detail; Next page showed 26–50 of 64; search from page two returned the one matching `city_name` row; clearing reset to 1–25 of 64; browser back returned to Tables. [Schema](evidence/table-populated.png), [page two](evidence/schema-page2.png), [filtered](evidence/schema-filter-settled.png) | No SQL/query action, full external schema retrieval, other type/nullable permutations or all sorting combinations |
| `tables/:name/edit` | Edit route and form | [Actual table edit](evidence/table-edit-populated.png) | Entry/form inspection | No edit save or reconciliation after save |
| `create/connection` | Guided create, form semantics and validation | Desktop/light/dark/narrow/4K/200% CSS zoom | Required/invalid-name checks; Cancel returned to Connections. [Invalid](evidence/db-invalid-validation.png), [4K](evidence/db-create-4k.png), [zoom](evidence/db-zoom-confirmed.png) | No real credential submission; exact browser zoom/text enlargement not certified |
| `create/warehouse/manual`, `create/table/manual` | Manual registration forms | [Warehouse](evidence/warehouse-manual.png), [Table](evidence/table-manual.png) | Entry and prerequisite/form inspection | No registration mutation, conflicting names or success state |
| `create/warehouse/browse`; sibling table browse flow | Import wizard and step contracts | Warehouse browse entry, connection selection and staged guidance | Open browse → inspect → Cancel → Warehouses. [Wizard](evidence/db-import-wizard.png) | No external discovery, metadata selection/import completion, partial batch outcomes; table browse source-only |

An early shortened table identifier and incomplete `/create/warehouse`/`create/table` URLs were investigator mistakes. They were replaced with actual collection links and parser-valid routes; resulting not-found/fallback captures are excluded from findings.

## Agents

Authority: [hash router](../../../providers/agents/portal/src/router.ts), [App.vue](../../../providers/agents/portal/src/App.vue), and its creation/configuration/run components. Hash routes append to `/ui/providers/agents`.

| Route / surface | Source | Viewed | Exercised / evidence | Gap |
| --- | --- | --- | --- | --- |
| `#/agents`, default | Agent collection and local navigation | One existing `test` agent, light/dark/narrow | Navigate to existing config. [Collection](evidence/providers-agents-dark.png) | No agent created, started, stopped or messaged |
| `#/agents/test/config`, `#/agents/test/runs` | Config, conversation, autonomy and run entry | Config, empty chat/runs, model and tool context | Read-only config/tab navigation. [Config](evidence/agent-detail-dark.png), [runs](evidence/agent-runs-dark.png) | No configuration save, active streaming, budget exhaustion or populated run |
| `#/activity`, `#/activity/:runID` | Feed, approval/run-trace composition | Empty activity | [Activity](evidence/agents-activity-dark.png) | Populated trace, approval/reject, tool expansion, nested/spawned agent and cancellation source-only |
| `#/connections`, connection/toolset edit siblings | Connections/toolsets and edit routes | Empty connections/toolsets | [Connections](evidence/agents-connections-dark.png) | Actual connection or toolset edit/save and external-channel setup |
| `#/models` | Model credentials and status | One existing model record | [Models](evidence/agents-models-dark.png) | No credential test, real model call or credential change |
| `#/create/agent`, `#/create/model`, `#/create/toolset` | Guided forms and success/cancel composition | All three forms | [Agent](evidence/agent-create-dark.png), [model](evidence/agent-create-model-dark.png), [toolset](evidence/agent-create-toolset-dark.png) | No creation submission/completion; validation not exercised for every form |
| `#/create/connection[/<type>]` | Type picker and type-specific form routing | [Connection type picker](evidence/agent-create-connection-dark.png) | Entry inspection | Type-specific credential submission and Slack/Telegram/Discord/email callbacks unverified |
| `#/agents/test/schedules/create`, `.../triggers/create`; edit siblings | Automation form contracts/routes | Schedule and trigger creation | [Schedule](evidence/agent-schedule-dark.png), [trigger](evidence/agent-trigger-dark.png) | No saved automation or firing; editing existing automation source-only |

The different autonomy labels in Agents and App Studio were checked against their separate authority contracts. Different labels alone were not counted as inconsistency.

## App Studio

Authority: [App.vue](../../../providers/app-studio/portal/src/App.vue), project panels, [FirstTimeSetup](../../../providers/app-studio/portal/src/FirstTimeSetup.vue), and [ProjectShareDialog](../../../providers/app-studio/portal/src/ProjectShareDialog.vue). Project workbench panels are internal tabs, not separate top-level host routes.

| Route / surface | Source | Viewed | Exercised / evidence | Gap |
| --- | --- | --- | --- | --- |
| Provider root | Project collection, summaries, creation entry | Two existing projects, both themes/narrow | Collection → `daily-dog-reminder`. [Projects](evidence/providers-app-studio-dark.png) | Large project collections, filtering at scale, project deletion |
| `~new` and first-time setup | Creation, prerequisites, FirstTimeSetup | [New project](evidence/studio-create-dark.png) | Form inspection | Configured account bypassed first-time setup; no project creation, model setup or initial generation |
| `~models` and model editor | Model settings/selection source | [Model list](evidence/studio-models-dark.png) | Navigation | Credential editor submission/testing and unavailable model recovery not exercised |
| `daily-dog-reminder`, conversation and thread rail | Conversation composition, history, queue/status, composer and panes | Existing historical conversation with interrupted/canceled outcomes | Rail show/hide, window/pane constraints, response/approval/model affordance inspection. [Project](evidence/studio-project-settled.png), [rail hidden](evidence/studio-thread-rail-hidden.png) | No prompt sent; live streaming, steering, cancellation and reconnect were not triggered. Historical messages do not prove live behavior |
| Workbench launcher and multi-tab navigation | Tab state, roles, overflow, active content | All listed panel types opened | New tab → panel repeated; 1440/1024/4K geometry compared. [Overflow](evidence/studio-instances.png), [4K](evidence/studio-many-tabs-4k.png) | Drag reordering, full keyboard reorder and tab persistence across a browser restart unverified |
| Preview | Evidence states, frame/toolbar, availability source | Starting, no URL, disabled open/annotation | [Preview](evidence/studio-preview.png) | No document connection, actual app render, sync success, annotation or interactive app checks |
| Integrations | Binding display and capability/authority metadata | One automatic Databricks binding | Open/read. [Integrations](evidence/studio-integrations.png) | Binding attach/change/delete and unavailable-authority variants not performed |
| Publishing | Production settings and release-stage components | No completed production deployment; stages and prerequisites | Open/read. [Publishing](evidence/studio-publishing-settled.png) | No complete image set; build/deploy/rollback/access checks not executed |
| History | History/repository state presentation | Empty/no usable commit history in the sample | [History](evidence/studio-history.png) | Commit diff, restore and conflict recovery |
| Project Settings | Settings controls, action scope, resource state | Settled settings panel | [Settings](evidence/studio-project-settings-settled.png) | Saves, destructive lifecycle changes, secret changes |
| Code | Repository/file browser and editor integration | Existing tree/browser surface | Open/read. [Code](evidence/studio-code.png) | File writes, save/format, conflict resolution and committed result |
| Review | Change/review composition | Empty review state | [Review](evidence/studio-review.png) | Populated patch review, approval and change application |
| Skills | Skill list and capability text | Existing skill entries | [Skills](evidence/studio-skills.png) | Skill detail editing, creation/import, actual invocation |
| Instances | Embedded Infrastructure surface | Four existing Ready/Pending instances | Open/read within constrained workbench. [Instances](evidence/studio-instances.png) | Embedded creation, resource lifecycle and write authorization |
| Share | Separate Production/Preview audiences and access forms | Production setup needed; Preview audience controls | Open, inspect, close; [Share](evidence/studio-share-final.png) | No save, invitation, public link change or external-user access verification |
| Attachments, slash/resource menu, tool/artifact details | Composer/attachment/history composition | Historical attachment context only; no new upload | Source inspection | Screenshot upload, large text file, upload interruption, active tool details and artifact download unverified |

## Kuery and Quickstart

Authority: [Kuery App.vue](../../../providers/kuery/portal/src/App.vue), [Kuery components](../../../providers/kuery/portal/src/components/), and [Quickstart portal](../../../providers/quickstart/portal/src/).

| Surface | Source | Viewed | Exercised / evidence | Gap |
| --- | --- | --- | --- | --- |
| Kuery Topology | Filters, graph/list composition and relationship palette | No queryable edge; graph and list alternatives | Switch Graph/List, inspect empty/disconnected guidance. [Topology](evidence/providers-kuery-dark.png), [List](evidence/kuery-list.png) | Actual graph density, selection, panning/zoom, layout performance and screen-reader fallback |
| Kuery Inventory | Shared resources and query-state composition | Empty inventory | Switch to Inventory → reload → Topology; URL unchanged. [Inventory](evidence/kuery-inventory.png) | Populated data, multi-edge continuation, sorting/filter combinations |
| Kuery Playground | Query editor/results and view state | Editor and unavailable/empty result region | Switch view/read. [Playground](evidence/kuery-playground.png) | No fleet query executed; typing latency under results load, large result set and errors |
| Kuery Impact | ImpactView and relation/path semantics | Source only | None | No queryable fleet to open populated impact. VO-04 is a source-supported proposal, not a completed usability test |
| Quickstart | Vanilla TypeScript custom-element entry, sample Greeting CRUD/probe, context and backend integration | Absent from active provider catalog | Host unavailable/unknown provider attempted; provider UI not mounted | All rendered and interactive reference-provider behavior. Its deliberately technical reference content is not judged as an ordinary consumer resource page |

## Shared component and interaction sampling

Canonical authority is [PortalKit Vue](../../../provider-sdk/portalkit-vue/), [plain PortalKit and CSS](../../../provider-sdk/portalkit/), and host components. Vendored copies were checked through the repository parity gate. No canonical drift finding was manufactured from permitted provider-specific composition.

| Pattern | Source inspected | Live representative / interaction | Verification limit |
| --- | --- | --- | --- |
| ResourceTable / filters / pagination | ResourceTable, ResourceTableFilter, read-state integration | Edges populated/empty/search/failure; Databricks 64 schema rows; pagination, filter keyboard and back | No exhaustive sort/selection/continuation combinations; most live collections have few rows |
| ResourcePage / sections / ConditionsPanel / badges | Canonical composition and caller sections | Edges, Infrastructure, Databricks details; Ready/Pending/Disconnected plus text | Correct underlying controller state was not independently proven |
| Guided creation / FormField / FormSelect / input | Canonical recipes and host/provider consumers | MCP complete local cycle; Databricks required/invalid name and cancel; Edges/Agents/Infrastructure forms | External completion, all input types and all validation branches unverified |
| ConfirmDialog | Canonical focus/keyboard/return implementation | MCP Delete initially focused; Tab to Cancel; Escape returns focus; intentional deletion completed. [Edge corroborating dialog](evidence/edge-confirm-settled.png) canceled | No destructive edge action taken; other confirmation variants source-only |
| Tabs | Canonical provider tabs and App Studio's workbench implementation | Coarse-pointer height, selection and active overflow measured | No physical touch device or full assistive-technology interaction |
| ActionMenu / custom filter popup / native select | Canonical role, keyboard and dismiss paths | Databricks split menu; Edges filter keyboard opening/selection; native page-size control viewed | Full screen-reader announcements and every outside-click boundary unverified |
| Tooltips / accessible names / icon controls | Host labels, row action labels, canonical tooltip behavior | Row-specific deletion, Workspace accessible name, pointer-dependent actions inspected | No exhaustive tooltip content/timing or screen-reader audit |
| Toast / alert / inline validation | ToastHost and callers; ResourceTable error regions | Local creation/deletion feedback and detail navigation; simulated inline errors/retry; form invalidity | Toast timeout/pause/live-region announcement and all severities not comprehensively exercised |
| Loading / progress / skeletons | Canonical recipes and resource-read contract | Initial retry loading, foreground refresh, retained data and disabled state | No backend polling stress, streaming throughput or production loading benchmark |
| Terminal / graph / preview exceptions | Documented boundaries and integration source | Preview not yet available; graph no fleet | Actual terminal canvas, iframe contrast, graph accessibility and their theme transitions unverified |

## Theme, viewport, accessibility and state matrix

| Dimension | Actually checked | Result / evidence | Not established |
| --- | --- | --- | --- |
| Desktop dark, 1440×1000 | Host, all accessible major providers, lists/details/creation, admin | Coherent visual vocabulary; captured throughout this report | Rendered coverage for blocked Code/Quickstart |
| Desktop light, 1440×1000 | Dashboard, catalog, Settings, MCP, Edges/Connect, Databricks/Create, Infrastructure, Agents/Config, App Studio list/project, Kuery | 14 route samples. [Dashboard](evidence/dashboard-light-1440.png), [Edges](evidence/edges-light-1440.png), [Databricks](evidence/databricks-light-1440.png), [Infrastructure](evidence/infrastructure-light-1440.png), [Agents](evidence/agents-light-1440.png), [Studio](evidence/studio-light-1440.png), [Kuery](evidence/kuery-light-1440.png) | Every nested panel or actual host-context theme event. Theme classes were set directly |
| Narrow dark, 390×844 | Same 14-route sample; login separately | Scrolling/overflow inspected; warm dashboard failure confirmed separately from cold pass. [Edges](evidence/edges-dark-390.png), [Databricks create](evidence/db-create-dark-390.png), [Agents config](evidence/agent-config-dark-390.png), [Studio](evidence/studio-dark-390.png) | Physical mobile-browser viewport/input behavior; all narrow/light combinations |
| Intermediate workbench | App Studio 768×1024 and 1024×768 | Pane compression and active-tab overflow. [768](evidence/studio-768.png), [1024 tabs](evidence/studio-many-tabs-1024.png) | Every user-selected pane ratio and rail placement |
| 3840×2160 | App Studio project/many tabs; Databricks simple creation | Tabs fit at 4K; simple field stretches to 1039px. [Workbench](evidence/studio-many-tabs-4k.png), [form](evidence/db-create-4k.png) | Whole product at 4K; no claim that fluid full-width pages inherently violate the system |
| Long names/dense content | Existing technical table identifier, binding ID/digest, historical prose; audit MCP long display name; 64 cached schema columns | Detail wrapping/metadata hierarchy; column truncation explicitly disclosed | Synthetic thousands-row sets, translated strings and arbitrary unbroken payloads |
| Keyboard/focus | Confirmation Tab/Escape/return, shared filter popup, form controls, named icon actions | Destructive default failure; sampled containment/return and filter operation worked | Full screen-reader, focus-order, hotkey-conflict and all-modal certification |
| Coarse pointer | Chromium `hasTouch`, `any-pointer: coarse` true | Host buttons 44×44; provider tabs 34.25px high. [Capture](evidence/touch-edges.png) | Physical hit accuracy. This is a Faros 44px contract check, not an automatic WCAG AA finding |
| Zoom | Databricks creation at CSS `zoom: 2` | Reflow sample inspected. [Capture](evidence/db-zoom-confirmed.png) | Browser zoom, text-only zoom, all routes at 200% or 400% |
| Reduced motion | Chromium reduced-motion media emulation; computed App Studio pane styles | Transition `none 1e-05s`; animation duration `1e-05s` | Every animated state, streaming/graph motion; dashboard had no suitable active animation sample |
| Contrast / semantic color | Documentation/tokens, rendered both themes, labels alongside status color | No broad token-contrast defect claimed | No comprehensive computed foreground/background contrast scan or color-blindness simulation |
| Performance | Navigation, loading, filtering and resizing during ordinary local use | Specific reflow and feedback observations | No production build, bundle budget, memory/CPU profile, network-throttle benchmark or active streaming measurement |

## Journey/state results and evidence hygiene

- **Completed safe local write:** MCP creation accepted → detail showed Provisioning → reload retained resource → list/confirmation → deletion → absence. Provisioning was not reported as runtime readiness. Only the temporary audit endpoint was changed.
- **Completed recovery, browser simulated:** populated Edges request delayed/503 → immediate refresh feedback + retained data/error → remove interceptor → Retry → row returns. Separate scoped initial 403 → error rather than empty data → Retry/loading → recovered collection. [Initial failure](evidence/edge-initial-403-confirmed.png), [retry](evidence/edge-retry-pending.png), [recovered](evidence/edge-initial-recovered.png).
- **Completed form exit:** Databricks invalid/required input feedback → Cancel → owning collection; import entry → Cancel → Warehouses. No credentials were submitted.
- **Completed navigation sample:** Databricks Tables → actual detail → browser back → Tables. Kuery Inventory → refresh → Topology reproduced loss of orientation. App Studio repeated tab creation reproduced hidden active selection.
- **Available real states:** populated, empty, Pending/Starting, Ready, Disconnected, provider unavailable, disabled prerequisites, no matches, selected tabs/filter, historical interruption, local create accepted, deletion complete. Real alternate-role permission state and live provider recovery were unavailable.
- **Simulation isolation:** an early wildcard interception also matched a Vite module path. That blank page was an invalid test setup, not a finding. Confirmed runs restricted interception to `^https://localhost:9443/graphql/`. Wrong guessed routes, screenshots taken before a view mounted, and attempted clicks on disabled controls are excluded from defect evidence.
- **Private working artifacts:** browser storage state and raw request/DOM dumps remain outside the report. The durable evidence set contains selected screenshots plus concise, sanitized [observations](evidence/observations.json); it does not include bearer storage or credential files.

## Follow-up coverage needed for release confidence

1. Restore Code and enable the reference Quickstart only in an authorized test environment; render their real collections, details and complete creation/recovery journeys.
2. Use connected Kubernetes and Linux edges for terminal, service, workload, marketplace, Self-Host and populated Kuery investigations. Include realistic dense/long-name data and representative keyboard/list alternatives to the graph.
3. Use configured test credentials and disposable resources to complete external import/provisioning, first-user setup, model calls, agent approval/cancel, and App Studio screenshot/large-text attachment plus active streaming/reconnect.
4. Validate build/image/deploy/access boundaries on a disposable project and verify Preview and Production as their own surfaces, including alternate-user access.
5. Run actual alternate-role/Workspace tests, screen-reader and physical-touch checks, computed contrast, browser/text zoom, non-Chromium browsers and proportionate production performance measurements.

These are explicitly unverified packages, not prerequisites silently imposed on the completed audit or assertions that the underlying journeys fail.
