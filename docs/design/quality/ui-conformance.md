---
{"schema":1,"id":"design.quality.ui-conformance","title":"Provider UI conformance contract","kind":"policy","status":"active","authority":{"design":"normative","implementation":"canonical"},"implementation":{"state":"shipped","notes":"The dependency-free scanner and focused tests enforce the shared UI vocabulary and exact exception registry."},"appliesTo":["portal","provider-portals","portalkit","dex"],"owner":"design-system","canonicalSource":[{"path":"docs/design/quality/ui-conformance.md#provider-ui-conformance-contract","role":"design"},{"path":"hack/verify-ui-conformance.mjs","role":"implementation"},{"path":"hack/verify-ui-conformance.test.mjs","role":"implementation"},{"path":"hack/ui-conformance.config.json","role":"implementation"}],"verification":{"state":"verified","checks":[{"kind":"command","ref":"make verify-ui-conformance","status":"passing","evidence":"Current scanner and focused tests covered 429 files with zero violations across 31 focused tests."},{"kind":"browser","ref":"Core and provider rendered fixture matrices","status":"passing","evidence":"Six core and six provider dark/light desktop/mobile/hybrid cases passed with actual fonts loaded; auth, Dex, and host hybrid fixtures also passed. This does not establish all routes, live Tilt, or assistive-technology verification."}]},"relatedDocuments":[]}
---

# Provider UI conformance contract

`make verify-ui-conformance` runs focused fixture tests and the dependency-free
Node scanner. The stylesheet authority is `provider-sdk/portalkit/faros-ui.css`.
The host copy at `portal/src/assets/faros-ui.css` and vendored `src/portalkit/`
copies are checked separately by `make verify-portalkit`. The manifest rejects
unregistered canonical files and copy checks reject missing, stale, or
unexpected assets.

Standalone bundles use the shared `styles.ts` handoff. The computed
`--faros-ui-canonical: 1` marker preserves a host stylesheet only when its
`--faros-ui-core-version` is compatible with the current core version 18
contract (`FAROS_UI_CORE_VERSION` in `provider-sdk/portalkit/styles.ts`). A
stale or unversioned host remains untouched while canonical CSS imported
through Vite's `?inline` loader is appended under a versioned fallback ID.
Vite may minify that runtime fallback; the authored stylesheet and synced
source copies remain byte-identical. Existing style elements are never
replaced, and newer host CSS always wins. Optional AgentKit presentation has
its independent marker and version, owned by `provider-sdk/agentkit/styles.ts`;
the core handoff does not load or scan those optional recipes.

The scanner covers the canonical `provider-sdk/portalkit`,
`provider-sdk/portalkit-vue`, `provider-sdk/agentkit`, and
`provider-sdk/agentkit-vue` roots, plus the configured provider roots `portal`,
`hack/dex/web`, and each `providers/*/portal` tree. App Studio's standalone
bundle is included in that provider scan across its runtime Vue and TypeScript
sources; the host portal scan remains a separate root and does not subsume or
duplicate Studio. This source scanner is separate from host Tailwind's `@source`
list.
Generated `dist`, dependency `node_modules`, test files, and byte-synced
provider copies are excluded. Repeatable `--canonical-root` and
`--provider-root` flags, or matching comma-separated environment variables,
support isolated checkouts without weakening provider scanning.

Diagnostics cover legacy pre-k hooks, provider CSS redeclarations of `.k-*`,
native dialogs, Unicode/emoji used as icon content, unknown `--color-*` tokens,
raw colors, pill/soft radii, and provider-local common-widget selectors. Arrow
notation in prose and keyboard hints is not an icon violation; icon context,
edge affordance, icon-ish class/ARIA context, or CSS/DOM content assignment is
required. Common-widget matching is intentionally narrow: page-composition names
such as `header`, `form`, `field`, and `list` are not enough, while a focused
primitive name with a visibly restated widget recipe is actionable. Output is
sorted by repository-relative path, line, column, and rule.

The only suppression mechanism is the structured
[`hack/ui-conformance-exceptions.json`](../../../hack/ui-conformance-exceptions.json)
registry. Each exact exception references `design.quality.exceptions`; the
scanner resolves that ID and checks its source locator. See the
[exception policy](exceptions.md).

The durable acceptance rule is focused tests pass and the scanner reports zero
diagnostics. A previous run covered host marker preservation, App Studio accent
text, Code recipes, native table rows with nested controls, and Kuery's narrow
inventory scroll; those are evidence examples, not a debt baseline. Source
validation is not a substitute for browser testing.
