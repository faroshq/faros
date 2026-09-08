# Faros frontend audit — 7 September 2026

Faros has a recognizable, coherent visual system and a strong shared resource-management foundation. Sharp surfaces, restrained violet, semantic status labels, shared tables, contextual creation guidance, and explicit Production/Preview separation make it feel like one platform. The highest-value work is now protecting **truthful state, safe actions, and usable space**. A new visual identity would not address the consequential weaknesses.

Three findings deserve the first implementation package: previews advertised as available without corresponding preview evidence; destructive confirmations that initially focus Delete; and a dashboard that can remain compressed into three columns after a window is narrowed. App Studio also loses the visible active tab during ordinary multi-tool work. Below those are recurring problems in recovery copy, technical information hierarchy, touch sizing, and navigation persistence.

**Outcome:** 12 consolidated findings: **0 demonstrated Critical, 3 High, 9 Medium**. Four visual opportunities are ranked separately. These are scoped findings, not a claim that blocked journeys are defect-free or that the product passes a complete accessibility audit.

## Scope, baseline, and evidence rules

This was an audit only. Audit-owned repository changes are this report and its evidence. No application, test, configuration, design-system, or provider code was changed; no PR was opened. One temporary read-only MCP server, `frontend-audit-20260907`, was created through the UI and deleted through the UI. Its disappearance was verified. No cloud resources, agents, messages, publications, invitations, or provider installations were created.

- Source baseline: checkout `/home/crwilhit/github/faros`, HEAD `e788ffda2eab2602c40da6760d6f63ae998ad191`.
- Pre-existing local changes: `portal/src/components/AccountAccessMenu.vue`, `portal/src/pages/OrganizationsWorkspace.conformance.test.mjs`, `portal/src/pages/TenantSettingsPage.vue`, `providers/edges/internal/tunnel/auth.go`, and untracked `providers/edges/internal/tunnel/auth_self_authentication_test.go`. Settings findings describe this working checkout, not an assertion about an unmodified release.
- Ending revision: `9bcd6f2860e26362bc3e04a009a577de4f1fc310`. The checkout advanced through unrelated Organization/Settings work during the audit; see the [baseline note](coverage.md#host-routes-and-shell). The audit did not modify Git state.
- Runtime: existing local development stack at `https://localhost:9443/ui/`, Chromium, static development login, active `default` Workspace. `/healthz` answered successfully. The stack was not restarted or rebuilt. Source and live evidence are recorded separately; byte identity of every running provider bundle with HEAD was not established.
- Viewports: 1440×1000 desktop; 390×844 narrow window; App Studio additionally 768×1024, 1024×768, and 3840×2160; Databricks creation at 3840×2160. Both `html.dark` and `html.light` were rendered across accessible major provider families. These checks used the HTML theme classes; they do not prove every canvas or iframe responds to a host context theme event.
- Additional checks: desktop-to-narrow resizing versus cold narrow loading, long identifiers and display text, a 64-column schema table, native and shared control interaction, modal focus and Escape, browser back/refresh, coarse-pointer emulation, CSS zoom at 200%, and reduced-motion computed styles. CSS zoom is a reflow sample, not certification of browser zoom or text-only enlargement.
- Browser-only failure simulations supplied a 503 on a loaded Edges collection and a 403 on its initial GraphQL read. They changed only that browser's responses. They are labeled **simulated**, not actual service or permission incidents.
- Acceptance before reporting: discover routes and surface owners; inspect every major family in source; view every accessible major family; exercise representative complete and recovery journeys; retain screenshots and reproduction steps for live findings; separate unavailable states; verify report links and audit-only file scope.

Confidence describes evidence, not severity: **High** = direct runtime reproduction supported by source, or a deterministic source contract; **Medium** = source-supported design judgment or an unverified causal explanation. Effort means approximate implementation and focused verification for an engineer familiar with the area: **S** ≤1–2 days, **M** 3–5 days, **L** 1–2 weeks. Estimates exclude unrelated service restoration.

## The standard used

### Authority and intended identity

The [design knowledge base](../../design/README.md) is the normative router. [The former design book](../../design-book.md) is a compatibility pointer, not a second standard. Each entry's `status`, design/implementation authority, implementation state, and verification metadata matters. An active document with partial verification is guidance, not proof of rendered conformance.

Implementation authority is [root tokens](../../../portal/src/assets/main.css), [canonical PortalKit CSS and plain TypeScript](../../../provider-sdk/portalkit/), [canonical Vue PortalKit](../../../provider-sdk/portalkit-vue/), and host components. Provider `src/portalkit/` copies are distribution outputs. Provider-local composition and domain content remain provider-owned.

| Contract | What it means for this audit |
| --- | --- |
| [Violet Circuit principles](../../design/foundations/principles.md) | Violet-tinted near-black ground, hairlines, sharp geometry, restrained depth, and state-bearing glow. Light mode is also supported. No demand for decorative gradients or bigger cards. |
| [Typography](../../design/foundations/typography.md), [colors](../../design/foundations/colors.md), [geometry](../../design/foundations/geometry.md) | Instrument Sans for UI, Archivo for display, IBM Plex Mono for machine information. Dense small labels are deliberate. Cards 6px, controls 4px, tags 3px; use semantic tokens. Density still has to preserve comprehension. |
| [Fluid shell](../../design/patterns/fluid-shell.md) | Ordinary pages share a fluid column. Readability limits belong inside task regions. Full-bleed workbenches own their viewport and scrolling. Large-monitor whitespace alone is not a violation. |
| [Creation](../../design/patterns/resource-creation.md) | Independent resources get focused routes; compact parent-owned additions may remain inline or modal. Back, clear task title, prerequisites, and Cancel → domain action form one journey. Adoption is explicitly incremental. |
| [Resource reads](../../design/patterns/resource-reads.md) | Preserve authoritative populated and empty snapshots through background work; foreground reads show feedback; failures remain distinguishable from empty data. |
| [Accessible interaction](../../design/accessibility/interaction.md) | Native semantics, complete keyboard paths, visible focus, named controls, focus containment/return, both-theme contrast, 44×44 coarse-pointer targets, reduced motion, recoverable graph representations. |
| [UI copy](../../design/content/ui-copy.md) | Product meaning before raw resource kinds or diagnostics; distinguish requested, observed, and completed outcomes; never echo secrets. |
| [AI evidence](../../design/ai/evidence-and-status.md) | Runtime/binding readiness, preview URL availability, document connection, rendered behavior, release artifacts, rollout, and access are separate claims. |

Explicit contextual variation was respected: [fixed-dark terminals and Dex; white app-owned preview canvases; conversational bubble radii; third-party identity artwork; Kuery's categorical graph palette](../../design/quality/exceptions.md). The graph exception does not establish accessibility. [Known oddities](../../design/quality/known-oddities.md) leave graph review incomplete and describe unshipped command-palette, custom date/time, and slider directions. None was treated as a missing shipped feature.

[Navigation/feedback](../../design/patterns/navigation-and-feedback.md) allows separate primary actions for independently persisted settings regions. Agents' Suggest/Ask/Auto and App Studio's response/approval modes have [different documented authority semantics](../../design/ai/agents-autonomy-and-runs.md); making their labels superficially identical would conceal meaningful differences.

### Ambiguities that affect the result

1. Guided forms remove the normal `42rem` cap and can turn three fields into a multi-column, viewport-wide task; the prose still calls for bounded simple forms. FA-010 covers the unresolved precedence.
2. The default icon rail deliberately hides the Workspace label, even though Workspace is the operating boundary. Compliance can therefore weaken scope awareness. FA-012 addresses the standard itself.
3. The confirmation contract delegates to the existing shared focus behavior without saying which action should initially receive focus in a destructive dialog. That does not make the current default safe: FA-002 is a UX defect, not an invented rule violation.
4. Component documents frequently record pending browser verification. The successful gates below validate documentation and distribution, not all of these pending behavior claims.

### Mechanical verification

`make verify-design-docs verify-portalkit verify-ui-conformance` passed. The conformance scanner inspected **378 files with 0 violations**. The Impeccable detector produced ten copies of one warning, `transition: width`, all for the synchronized progress-bar recipe. The [documented 300ms progress exception](../../design/foundations/geometry.md) makes these false positives for this audit. There is no evidence here for a general hardcoded-color, radius, font, or component-copy-drift finding.

Browser evidence remains necessary: the coarse-pointer tab height, viewport failures, and state-language defects below coexist with those passing gates. No application build or backend test suite was needed for this documentation-only deliverable.

## Prioritized findings

| ID | Priority | Classification | Finding | Scope / effort |
| --- | --- | --- | --- | --- |
| FA-001 | High | UX defect | Resizing a loaded dashboard can leave unreadable tiles | Host dashboard; M |
| FA-002 | High | UX defect | Destructive confirmations initially focus Delete | Shared Vue confirmation; S |
| FA-003 | High | Design-system violation | “Preview up” overstates the available evidence | App Studio summaries; M |
| FA-004 | Medium | UX defect | Newly active workbench tabs remain outside the visible strip | App Studio tabs; S–M |
| FA-005 | Medium | Design-system violation | Provider route tabs miss the coarse-pointer size contract | Shared tabs; S |
| FA-006 | Medium | Design-system violation | Recovery exposes transport JSON instead of task guidance | Resource read error adapters/callers; M |
| FA-007 | Medium | Design-system violation | Machine representations displace the task's explanation | Infrastructure and App Studio Integrations; M |
| FA-008 | Medium | UX defect | Degraded-provider view offers no local recovery action | Host ProviderFrame; S |
| FA-009 | Medium | Consistency gap | Kuery's view and investigation state do not survive navigation | Kuery route state; M |
| FA-010 | Medium | Design-system gap | Simple guided forms have no coherent readability limit | Creation contract/recipe/callers; M |
| FA-011 | Medium | UX defect | Thread rail consumes space needed by the conversation and authority controls | App Studio pane allocation; M |
| FA-012 | Medium | Design-system gap | Default shell makes the operating Workspace invisible at rest | Shell scope contract; M |

### FA-001 — Loaded dashboard does not reliably reflow after resize

**Surface/state:** `/ui/`, populated dashboard; load at 1440×1000, resize to 390×844, wait three seconds.

**Observation and impact:** Six tiles remain in three columns, each approximately **79px wide**. Provider names, status, recent resources, and Open actions clip. The surrounding page fits the viewport, so document-level overflow checks alone miss the broken task. Users narrowing a window cannot read the dashboard normally.

**Evidence:** [Settled resize failure](evidence/dashboard-resize-settled.png), [cold narrow counterexample](evidence/dashboard-cold-390.png), [desktop](evidence/dashboard-dark.png). A fresh 390px load produced one column with 270px tiles; this is specifically a reflow/lifecycle failure, not “Faros has no responsive dashboard.”

**Source:** [DashboardPage.vue:66–105 and grid wiring at 379](../../../portal/src/pages/DashboardPage.vue#L66); [dashboardLayout.ts:358](../../../portal/src/stores/dashboardLayout.ts#L358); [AppLayout.vue:965–993](../../../portal/src/components/AppLayout.vue#L965). Measurement is attached once on mount and returns if the slot's `pageRef` is not available. The shell can defer that slot. This is a plausible cause to confirm alongside layout reconciliation; the audit did not instrument the observer's lifecycle.

**Direction:** Make observation follow the actual mounted content region and reconcile saved geometry on width changes without persisting a transient narrow layout over the user's wide arrangement. Verify cold and warm navigation, resize both directions, sidebar expansion, and stored custom layouts. **Owner:** host dashboard. **Effort M. Confidence High for behavior; Medium for precise cause.**

### FA-002 — Destructive dialog gives initial keyboard authority to Delete

**Surface/state:** shared Vue `ConfirmDialog`, reproduced from the MCP collection's temporary endpoint row.

**Reproduce:** Hover the row, activate its Delete action, and inspect focus before doing anything else. **Delete** is focused. Tab moves to Cancel; Escape cancels and returns focus to the invoking row action. Reopening and explicitly selecting Delete removed only the audit endpoint.

**Impact:** An extra Enter, habitual confirmation keystroke, or key repeat can activate the irreversible choice. Good focus containment and return do not compensate for the initial destructive default.

**Evidence:** [Initial MCP confirmation](evidence/confirm-initial-focus.png); [cleanup](evidence/mcp-cleanup.png). [ConfirmDialog.vue:57–71](../../../provider-sdk/portalkit-vue/ConfirmDialog.vue#L57) confirms on Enter unless Cancel is focused and unconditionally focuses `confirmBtn`. All consumers of this canonical Vue behavior are potentially affected. A second [settled Edges confirmation](evidence/edge-confirm-settled.png) also focused Delete; it was canceled with Escape and returned focus. Only the temporary MCP resource was deleted.

**Direction:** Initially focus Cancel for destructive confirmations, retain deliberate focus-based activation, and explicitly associate the consequence text with the dialog. Define safe initial focus in the [confirmation contract](../../design/components/confirm-dialog.md). Preserve the working Escape, Tab containment, and focus return. **This is a UX defect; current documentation does not explicitly require Cancel focus.** **Effort S. Confidence High; live keyboard reproduction.**

### FA-003 — Preview availability is inferred from binding readiness

**Surface/state:** dashboard App Studio tile versus `/ui/providers/app-studio/daily-dog-reminder` → Preview.

**Observation and impact:** The dashboard reports **“2 preview up.”** The project's Preview subsequently reports **“Starting”** and explicitly says the development environment does not have a URL yet. The user is sent from a reassuring availability summary into an unavailable preview. This undermines confidence in health signals across the product.

**Evidence:** [Dashboard](evidence/dashboard-dark.png), [project Preview](evidence/studio-preview.png). These are observations during this audit, not an assertion that the resources were transactionally frozen between captures. The deterministic evidence is [DashboardTile.vue:71–83, 167–168](../../../providers/app-studio/portal/src/DashboardTile.vue#L71): a Ready environment or **any** Ready binding increments `previewReady`; neither preview URL nor document evidence is required. The audit did not execute an app test inside the preview.

**Exact rule:** [AI evidence and status → Preview states name the evidence boundary](../../design/ai/evidence-and-status.md#preview-states-name-the-evidence-boundary) and [UI copy → Tell state apart](../../design/content/ui-copy.md#normative-contract). A component's readiness does not prove preview availability or rendered behavior.

**Direction:** Rename binding-only counts to the precise runtime state they represent, or derive an explicitly named preview-availability projection from the proper URL/access boundary. Keep Loaded/Unverified/document checks narrower still. Audit the Share “Available” label and production aggregate for the same inference pattern without assuming they share the cause. **Owner:** App Studio summary and state projection, with contract tests for mixed-ready bindings and absent URL. **Effort M. Confidence High for source mismatch and observed inconsistency.**

### FA-004 — Active workbench tabs open beyond the visible strip

**Surface/state:** App Studio project, 1440×1000 with conversation and workbench visible.

**Reproduce:** Use New tab to open Integrations, Publishing, History, Project Settings, Code, Review, Skills, then Instances. The selected content changes, but the selected tab is not brought into view. For Instances the strip measured `clientWidth=752`, `scrollWidth=1224`, `scrollLeft=0`; the active control began at **x=1705**, beyond the visible right edge **x=1352**.

**Impact:** Users lose confirmation of where they are, cannot reach the active tab's close action normally, and accumulate hidden navigation state. Horizontal scrolling provides a workaround. [1440px failure](evidence/studio-instances.png), [1024px comparison](evidence/studio-many-tabs-1024.png), [4K comparison](evidence/studio-many-tabs-4k.png). The same sequence fits at 4K.

**Source/direction:** [App.vue:9768–9840](../../../providers/app-studio/portal/src/App.vue#L9768) renders a scrolling tab strip and roving active control. Reveal the selected tab on creation/activation, maintain visible overflow affordances, and provide keyboard access to reorder if drag ordering remains offered. Preserve tab names, selected semantics, and existing close actions. **Owner:** App Studio workbench navigation. **Effort S–M. Confidence High; live geometry and visual reproduction.**

### FA-005 — Shared provider tabs remain too short for coarse pointers

**Surface/state:** Edges route navigation at 390×844 with `hasTouch=true`; browser confirmed `any-pointer: coarse`.

**Observation:** Edges, Workloads, and Services tabs measured **34.25px high**, while the host navigation controls measured 44×44. This is an integration inconsistency in the shared navigation layer, not a claim that every small desktop control needs enlargement.

**Evidence:** [Touch-emulated Edges](evidence/touch-edges.png); [Tabs.vue](../../../provider-sdk/portalkit-vue/Tabs.vue); [canonical `.k-tab` and final narrow rule](../../../provider-sdk/portalkit/faros-ui.css#L1169). The narrow rule adjusts horizontal padding, not the missing coarse-pointer height.

**Exact rule:** [Accessible interaction → Support touch and hybrid input](../../design/accessibility/interaction.md#normative-contract) requires at least 44×44px interactive targets for coarse pointers. This is a **Faros contract violation**, not a claim that the 44px requirement alone is a WCAG AA failure.

**Direction:** Extend the canonical tabs recipe's coarse/hybrid target size and verify overflow at the same time; distribute through PortalKit. Known shared scope includes provider route tabs, with Edges as the live representative. Physical-device hit accuracy remains untested. **Effort S. Confidence High for geometry; emulated rather than physical touch.**

### FA-006 — Resource recovery renders transport JSON as the explanation

**Surface/state:** Edges collection, initial read failure and failed foreground refresh after a populated read.

**Reproduce:** In a browser-only interceptor, return HTTP 503 from the Edges GraphQL request with `{ "errors": [{ "message": "Audit simulated service unavailable" }] }`, then click Refresh. Repeat with HTTP 403 on the initial read. The alert renders the serialized response rather than a product-level description. [Loaded failure](evidence/edge-refresh-error.png), [initial simulated permission failure](evidence/edge-initial-403-confirmed.png).

**Impact:** People must interpret protocol syntax to decide whether to retry, wait, change Workspace, or seek access. Raw server strings also prevent consistent bounds on diagnostic content; this test did **not** demonstrate disclosure of real credentials.

**Source and exact rule:** [Edges api.ts:132–139](../../../providers/edges/portal/src/api.ts#L132) forwards response text as `message`; [ResourceTable.vue:596,643](../../../provider-sdk/portalkit-vue/ResourceTable.vue#L596) displays caller error text. [UI copy → Lead with the product meaning / Tell state apart](../../design/content/ui-copy.md#normative-contract) requires the task and recovery meaning first. The same raw-error transport pattern warrants inspection at other resource callers; not every provider error state was reproduced.

**Direction:** Keep error categorization in transport/domain adapters, provide bounded product copy plus available recovery, and put safe diagnostic detail in a disclosure. Do not turn 403 into an empty collection. Preserve the behavior that **passed**: retained rows, immediate Refresh feedback, and successful recovery once the interceptor was removed. **Effort M. Confidence High; simulated failures clearly bounded.**

### FA-007 — Machine-oriented content displaces the user's task

**Representative surfaces:** a Pending Infrastructure instance; App Studio Integrations with one automatic Databricks binding; template catalog copy.

**Observation:** The instance detail leads its body with raw `values` JSON, including platform plumbing and identifiers. A one-day Pending resource has zero reported children, yet the prominent content does not explain what evidence is missing or where to investigate. Integrations gives primary hierarchy to an `auto-databricks-…` ID, a resource-kind path, an action version, and a digest inside nested panels. Template descriptions regularly lead with Deployment/StatefulSet/Secret/HTTPRoute mechanics.

**Impact:** Both consumers and operators have to translate implementation structures into “what can this do?” or “what is preventing progress?” A platform engineer needs technical details, but also benefits from an accurate first explanation.

**Evidence:** [Instance](evidence/instance-detail-dark.png), [Integrations](evidence/studio-integrations.png), [catalog](evidence/providers-infrastructure-dark.png). [InstanceDetailPage.vue:334–352](../../../providers/infrastructure/portal/src/views/InstanceDetailPage.vue#L334) uses JSON as the first fallback when no view groups exist. [ProjectIntegrations.vue:290 onward](../../../providers/app-studio/portal/src/ProjectIntegrations.vue#L290) foregrounds materialization/compatibility language and raw binding metadata.

**Exact rule:** [UI copy → Lead with the product meaning](../../design/content/ui-copy.md#normative-contract). Canonical ResourcePage does not require these particular sections; this is a caller composition/content issue, not a reason to replace the shared page.

**Direction:** For instances, explain observed phase, age, missing child/resource evidence, and a safe diagnostic path before collapsed values. For integrations, lead with the resource and permitted action; retain IDs, digest, and authority audit in details. For templates, lead with outcome and exposure/persistence constraints, then implementation. Never invent a controller cause when none was reported. **Effort M across these representatives. Confidence High for presentation; recommended wording needs domain review.**

### FA-008 — Degraded provider is an informational dead end

**Surface/state:** `/ui/providers/code` while Code is degraded.

The page accurately says “Provider backend is unavailable,” but offers no Retry status, last checked time, or contextual route to provider status. A user cannot distinguish a current observation from cached catalog state. Browser refresh is the workaround. [Screenshot](evidence/providers-code-dark.png).

[ProviderFrame.vue:513–524](../../../portal/src/pages/ProviderFrame.vue#L513) renders this branch without the retry actions present in adjacent catalog-error and binding-error branches. The audit did not restore Code or infer why its backend was unavailable.

**Direction:** Add a bounded status recheck and an appropriate provider-management/help route, showing when the status was last observed. Rechecking must not imply restarting or repairing the backend. Do not offer a repair action the caller cannot perform. **Owner:** shared provider host, affecting any provider in this state. **Effort S. Confidence High; degraded state live, recovery after backend restoration unverified.**

### FA-009 — Kuery does not give investigations durable navigation state

**Surface/state:** `/ui/providers/kuery`, switching from Topology to Inventory or Playground.

Click Inventory, then refresh: Topology is selected again and the URL never identified Inventory. Browser history also has no view entry to return to. This differs from Code/Edges/Databricks/Infrastructure route-owned navigation and Agents' hash routes. [Inventory](evidence/kuery-inventory.png), [Playground](evidence/kuery-playground.png).

[App.vue:20–38](../../../providers/kuery/portal/src/App.vue#L20) keeps the selected view solely in a ref; the impact anchor is similarly local. Query/filter and impact restoration are source-based concerns; a populated impact investigation was unavailable because no edge was queryable.

**Direction:** Define URL state for the selected view and stable investigation identity. Preserve local unsent query drafts where appropriate, with an explicit policy for sensitive query contents. Refresh/back must restore orientation without silently rerunning expensive work. Keep cached visited views for responsiveness. **Effort M. Confidence High for view reset; Medium for wider investigation impact.**

### FA-010 — Guided creation conflicts with simple-form readability

**Surface/state:** Databricks connection creation with three inputs, desktop and 4K; shared guided creation recipe.

At 1440px, a short connection form and lengthy guidance occupy most of a large panel. At 4K, individual field width was measured at about **1039px**, and the three-field task spreads across the available region. This is not automatically improved by giving it more screen area. By comparison, Infrastructure's simple PostgreSQL fields remain in a readable local region.

[Desktop](evidence/db-create-light-1440.png), [4K](evidence/db-create-4k.png), [PostgreSQL comparison](evidence/postgres-provision.png). [Canonical CSS:418–469](../../../provider-sdk/portalkit/faros-ui.css#L418) caps the normal surface, then removes that cap for guided forms; [Databricks' large-container form rules](../../../providers/databricks/portal/src/style.css#L804) deliberately add columns.

**Conflicting guidance:** [Fluid shell](../../design/patterns/fluid-shell.md) limits simple form regions to about 42rem, while [resource creation](../../design/patterns/resource-creation.md) and the guided recipe encourage fluid paired guidance without defining the precedence for simple tasks.

**Direction:** Specify separate bounds for fields, guidance, and the enclosing page; distinguish simple guided tasks from dense schema-driven provisioning. Constrain the field region without reintroducing provider-level centered page wrappers. Remove duplicate prerequisite explanations when inline help already says the same thing. **This is a design-system gap, not a request to shrink every provisioning screen. Effort M. Confidence Medium; rendered measurements are confirmed, ideal width is a design decision.**

### FA-011 — App Studio allocates too little width to actual conversation

**Surface/state:** 1440×1000 project with thread rail, conversation, and workbench all visible; more severe at 768px/1024px intermediate widths.

The thread rail occupies a substantial portion of the conversation side, leaving roughly a 300px prose region. Assistant paragraphs become long narrow columns, while approval/model controls compress to abbreviated fragments. The workbench remains spacious even when its content is short. [Rail open](evidence/studio-integrations.png), [768px](evidence/studio-768.png), [rail hidden](evidence/studio-thread-rail-hidden.png).

**Impact:** Reading a response or checking action authority becomes harder during the intended conversation-plus-tool workflow. The rail toggle is a working recovery, but users must discover and manage it themselves.

**Source/direction:** [App.vue workbench/conversation composition](../../../providers/app-studio/portal/src/App.vue#L9000) and [ThreadRail.vue](../../../providers/app-studio/portal/src/ThreadRail.vue). Protect a minimum useful conversation region; collapse the secondary thread rail into a labeled overlay before squeezing prose and authority controls. Retain user resizing and a deliberate show/hide control. Base breakpoints on pane content width, not only the window. **Effort M. Confidence High for observed compression, Medium for the proposed allocation.**

### FA-012 — Default navigation hides the operating Workspace

**Surface/state:** normal collection and workbench routes with the default 56px icon rail.

The resting shell shows a Workspace icon but no visible Workspace name. Its accessible name and tooltip carry detailed scope, and expanding the sidebar reveals the label. The collection itself often does not restate it. [Default rail](evidence/providers-edges-dark.png), [expanded rail](evidence/shell-expanded.png).

This follows the [navigation/feedback contract](../../design/patterns/navigation-and-feedback.md), which places the tenant chip in expanded mode. It nevertheless weakens orientation in a product whose resources and credentials are Workspace-scoped. This audit had one usable Workspace and did not reproduce a wrong-Workspace mutation; the finding is about the standard's adequacy, not a proven authorization failure.

**Direction:** Define an always-visible, concise Workspace identifier at the shell/task boundary, retaining Organization as secondary provenance and the full accessible name. Test its placement in both ordinary and full-bleed pages; avoid another oversized header. Preserve Settings' useful distinction between the inspected Workspace and the active operating Workspace. **Effort M. Confidence Medium; design-system gap with direct visibility evidence.**

## Visual opportunities — ranked separately

These are proposed directions, not additional defects or implementation commitments. They preserve Violet Circuit's geometry, restrained color, and machine-detail vocabulary.

| Rank / ID | Surface and task | User value | Reach | Effort | Confidence |
| --- | --- | --- | --- | --- | --- |
| 1 / VO-01 | Resource and project health: understand what is ready and what blocks progress | Very high | Infrastructure + App Studio, reusable by other resources | L | High need; Medium data completeness |
| 2 / VO-02 | Integrations: understand the assistant's actual capabilities and authority | High | App Studio; potential Agents reuse | M | High |
| 3 / VO-03 | Workbench: read, decide, and inspect a tool together | High | Daily App Studio use | M–L | High |
| 4 / VO-04 | Kuery impact: explain a particular dependency chain | High per investigation | Operators using connected fleets | L | Medium; populated graph not available |

### VO-01 — An evidence-led operational summary

The Pending instance exposes values and conditions; the dashboard reduces preview state too far. Use a compact sequence of **observed boundaries** above the details: request accepted → resources reported → runtime ready → preview/access available. Show the current blocker or “not reported yet,” the age of that observation, and a link to its evidence. Opening a stage reveals the existing resource/condition record; it should not generate a second inventory or pretend to know backend causes.

Benefit: one glance explains both progress and the next useful inspection. Tradeoff: different providers expose different evidence, so stages must be domain-owned and unavailable observations explicit. Use existing ResourcePage, ConditionsPanel, semantic badges, and disclosures first. Extend the system only for a compact boundary/status composition; the release pipeline is an existing model to learn from, not a generic pipeline to copy everywhere. Use lines and labels; animate only an actually active stage. The [current instance](evidence/instance-detail-dark.png) and [release view](evidence/studio-publishing-settled.png) make the need and available vocabulary concrete.

### VO-02 — Capabilities before binding records

Replace Integrations' dominant machine IDs and nested boxes with a compact capability table or grouped rows: **resource**, **allowed action**, **authority**, **current availability**. For the observed Databricks binding, identify the forecast table and `query_table/v1` in readable terms, then offer “View authority and technical details” for resource path, digest, origin, and timestamp. Keep automatic/read-only status explicit; do not imply users can revoke or edit a server-owned binding if that operation is unavailable.

Benefit: users can answer “what data can this assistant use?” and operators can inspect the same evidence without translating the top-level screen. Tradeoff: descriptions must come from trustworthy provider metadata or carefully owned copy, not guessed model summaries. Existing ResourceTable/ResourceSectionCard/disclosures suffice for the first version. A future shared human-readable action-label contract would reduce provider drift. This is chiefly subtraction and hierarchy, not a new dashboard. [Current surface](evidence/studio-integrations.png).

### VO-03 — A workbench organized around the current pair of tasks

Protect conversation + active tool as the primary pair. Make the thread list a persistent choice that becomes a labeled overlay when it would consume the conversation's minimum readable width. Reveal selected tabs automatically; add an overflow picker listing open tools with their active/busy/review state. Keep response and approval policy text readable, opening full details on keyboard/pointer activation.

Benefit: users retain control and orientation without manually managing three cramped panes. Tradeoff: collapsing a familiar rail can surprise users, so explain the affordance and remember a deliberate override where it remains usable. Existing workbench tabs, thread rail, ActionMenu, and pane controls provide most of the parts; the design system needs a **pane-budget and overflow contract**, not a new visual style. Compare [open rail](evidence/studio-integrations.png), [hidden rail](evidence/studio-thread-rail-hidden.png), and [many tabs](evidence/studio-instances.png).

### VO-04 — Explain one impact path before displaying the whole graph

Kuery already has Graph/List alternatives and explicitly bounded results. Extend impact inspection with a selected-resource explanation: “This object affects these dependents through these relations,” then ordered path rows naming each relation and a bounded affected count. Selecting a row highlights that path in the graph; keyboard users receive the same relation text and can navigate resources without touching the canvas. Show truncation and unknown coverage beside the count.

Benefit: an operator can explain a proposed change to another person, rather than presenting an undifferentiated network. Tradeoff: relation direction and confidence must reflect the query's actual semantics; no causal claims should be inferred from visual proximity. Existing graph/list infrastructure and ResourcePage suffice for basic paths; a shared path-row/legend component and explicit graph accessibility verification would be extensions. Need is source-supported by [ImpactView.vue](../../../providers/kuery/portal/src/components/ImpactView.vue); usability with a real fleet remains unverified. Avoid animation or a topology homepage redesign until this bounded task is proven.

## Recommended sequence

1. **Protect trust and safe activation:** FA-003 state projections and FA-002 destructive focus. Verify mixed-ready/absent-URL states and keyboard confirmation at the canonical boundary.
2. **Stabilize space during real use:** FA-001 dashboard reflow, FA-004 active-tab visibility, FA-005 coarse-pointer tabs, then FA-011 conversation allocation. Check warm navigation, width changes, keyboard, and both themes.
3. **Make failures and pending resources actionable:** FA-006 transport-to-product errors, FA-008 provider status recheck, and the instance portion of FA-007. Preserve working stale-data behavior.
4. **Clarify navigation and task content:** FA-009 Kuery URLs; the Integrations/catalog portion of FA-007; clarify FA-010 and FA-012 before changing their shared rules.
5. **Prototype the ambitious work:** VO-01 and VO-02 first, then VO-03; validate VO-04 with representative connected-fleet data. An inspectable prototype and bounded behavior package should precede broad shared UI changes.

## Proposed design-system clarifications and extensions

- Add explicit destructive-dialog initial focus, description association, overflow, and focus-return examples.
- Require coarse-pointer verification of **canonical route tabs**, not only ordinary buttons and row actions.
- Define availability labels by evidence source across tiles, workbenches, Share, and release surfaces.
- Add a bounded error-content interface: product message, recovery action, safe diagnostic disclosure, and stale timestamp where available.
- Resolve simple guided versus dense provisioning layout precedence, with separate field/help width bounds.
- Specify minimum useful pane widths, rail collapse behavior, active-tab reveal, and overflow navigation.
- Reconsider visible Workspace context for the default rail while preserving the inspected-versus-active Settings distinction.
- Preserve the documented graph accessibility gap until a real canvas/list/keyboard/assistive-technology review is completed.

## Existing patterns worth preserving and expanding

- **Shared resource reads:** the loaded Edges row survived a failed refresh; initial failure did not become an empty collection; retry restored data. [Pending](evidence/edge-refresh-pending.png), [failure](evidence/edge-refresh-error.png), [recovery](evidence/edge-recovered.png).
- **Honest bounded data:** Databricks says its schema cache holds 64 of 88 columns instead of claiming completeness. [Table detail](evidence/table-populated.png).
- **Resource creation skeleton:** prerequisites, non-secret live summaries, explicit Cancel, and post-create detail navigation are strong. MCP completed its local lifecycle; Databricks and Edges creation were inspected without provisioning external resources.
- **Production versus Preview:** Share separates audiences and persistence, and Publishing distinguishes commit/build/image verification/deploy/access. The separation is valuable even where availability inputs need correction. [Share](evidence/studio-share-final.png), [Publishing](evidence/studio-publishing-settled.png).
- **Settings scope honesty:** inspecting a Workspace is labeled separately from changing the active operating context. Existing local Settings work was included in this snapshot. [Settings](evidence/settings-workspaces-dark.png).
- **Theme coherence:** accessible provider families keep the same shapes and hierarchy in light and dark. Status uses words as well as color. Preserve the sanctioned dark terminal and app-owned preview boundaries.
- **Native and reusable interactions:** native table semantics, row-specific delete labels, confirmation Escape/focus return, route-owned creation, masked MCP setup snippets, and graph/list alternatives are useful foundations.

## Coverage matrix and explicit verification gaps

The complete [coverage matrix](coverage.md) inventories the host, all eight provider frontend families, shared components, routes, states, and journeys. It distinguishes source inspection, browser viewing, interaction, and blocked completion. The [screenshot index](evidence/index.md) lists the retained captures. [Evidence notes and observations](evidence/observations.json) retain the measured assertions and simulation boundaries.

Major runtime gaps: Code could not mount because it was degraded; Quickstart was absent from the active catalog; Self-Hosting had no connected Kubernetes edge; Kuery had no queryable fleet; Agents had no populated run/approval trace; production had no complete deployable image set. Initial App Studio setup, first-Workspace completion, actual model calls/streaming, new attachment uploads, external imports, cloud provisioning, invites, release/deployment, and real alternate-role authorization were not executed. No screen reader, physical touch device, Firefox/WebKit, comprehensive computed-contrast scan, production bundle/performance benchmark, or end-to-end browser zoom certification was run.

These are limits of the audit, not reasons to assume those flows work or to manufacture defects. Source-only concerns and ambitious proposals remain labeled accordingly.
