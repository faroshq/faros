---
{"schema":1,"id":"design.components.model-connections","title":"Model connections","kind":"component","status":"active","authority":{"design":"normative","implementation":"canonical"},"implementation":{"state":"shipped","notes":"Shared cards, selector and usage-section composition are used by Agents and App Studio. Aggregate usage is available in Agents only."},"appliesTo":["provider-portals","portalkit"],"owner":"design-system","canonicalSource":[{"path":"docs/design/components/model-connections.md#model-connections","role":"design"},{"path":"provider-sdk/portalkit-vue/ModelConnectionCard.vue","role":"implementation"},{"path":"provider-sdk/portalkit-vue/ModelIDSelector.vue","role":"implementation"},{"path":"provider-sdk/portalkit-vue/modelIDSelection.ts","role":"implementation"},{"path":"provider-sdk/portalkit-vue/ModelUsageSection.vue","role":"implementation"},{"path":"provider-sdk/portalkit/faros-ui.css","role":"implementation"}],"verification":{"state":"partial","checks":[{"kind":"command","ref":"make test-model-connections","status":"passing","evidence":"Provider UI checks cover required verification, discovery without saving, draft retention, key replacement and stale authority results."},{"kind":"command","ref":"make test-model-connections-api","status":"passing","evidence":"Both provider API packages and Agents model tests pass; stored-key reuse rejects changed endpoints."},{"kind":"browser","ref":"Models custom-element root fixture","status":"passing","evidence":"Final current-tree fixture mounts both provider custom elements on both Models and create routes at 1440px and 390px in light and dark themes; the full-root 16/16 matrix passes with mobile 44px controls, no overflow, usage expansion containment, and model card checks. Evidence: /var/tmp/codex-build/models-heavy-render/check-final-current.json."}]},"relatedDocuments":[{"id":"design.patterns.resource-creation","relation":"implements"},{"id":"design.components.status-badge","relation":"see-also"},{"id":"design.accessibility.interaction","relation":"see-also"}]}
---

# Model connections

## Purpose

Provide one recognizable pattern for connecting and maintaining workspace model
credentials while retaining each provider's assignment semantics.

## Use when

Use `ModelConnectionCard` for a saved connection and `ModelIDSelector` for
selecting a discovered or manually entered model. Use `ModelUsageSection` below
the collection to separate connection management from reporting.

## Avoid when

Do not imply that a stored credential proves a working model. Do not treat a
successful model-list request as a successful model response. Do not substitute
zero cost for missing pricing or unavailable reporting.

## Anatomy and variants

Cards show identity, endpoint, credential state, test state, provider-owned
metadata and actions. App Studio supplies its default designation; Agents
supplies primary and fallback assignments. The default slot carries pricing or
capabilities, and the actions slot carries caller-owned mutations.

## Behavior

Connect and edit use a focused form. Discovery changes the draft selection only.
A new or edited connection must pass a model-response test before the UI saves
it. Changing the endpoint, credential or model invalidates that verification.
Saved credentials can be reused by probes only against their original provider
and endpoint. Stored keys are never returned to the browser. These UI gates do
not change the providers' API upsert contracts or share credentials between
providers. Test results are session-local, not persisted health guarantees.

## Content

Use “Connect model,” “Edit,” “Test connection,” and “Find models.” State input
and output prices separately as USD per million tokens and label them catalog
estimates. Both providers use `provider-sdk/modelcatalog`; its existing rates
are a reference snapshot, not live pricing. App Studio does not yet have
aggregate usage reporting. Agents preserves window selection, cost, tokens,
runs, errors, latency, daily spend and model/agent breakdowns.

## Layout and responsive behavior

Models routes use an unboxed page header with the title and Connect model action
on one wrapping row, followed by supporting copy. Do not wrap the collection in
a settings-dialog frame or place the primary action in a separate toolbar.
Keep the Models title, supporting copy, and Connect model action at the canvas
level while existing saved cards remain in the collection.
Match Databricks collection spacing: the host owns the outer inset, with no
additional page padding and 16px between content groups.

Use `.k-model-grid` for compact cards, constrained to 280–360px where space
permits and a single column on narrower surfaces. Route-owned create forms use
`.k-create-page` as the root (`display: block`, `width: 100%`, `min-width: 0`),
with explicit 14px font size and 21px line height, one back action, one heading,
and one form surface. The explicit route typography prevents inherited inline
link offsets. Agents Models removes
its former `agents-menu` and `agents-create-page` wrappers so its create route
matches App Studio's wrapper geometry. The `.k-back-action` arrow is a fixed
14px by 14px flex-none SVG. Focused forms use the shared wide creation surface,
with the name followed by Connection, Credential, and Model sections. Use the
`.k-model-form-*` recipes for identical section geometry and responsive columns
across both providers; keep field help inside the form surface.

Usage summaries appear below the collection. `ModelUsageSection` is one bordered,
raised section card with 20px padding and a 6px radius; its title, time controls,
metrics, and expandable detail content stay inside that boundary. App Studio's
usage-unavailable message remains inside the same card without a nested box.
Shared recipes ship through `make sync-portalkit` and stylesheet version 9,
including compatibility fallback for older host stylesheets.

## Accessibility

The shared selector retains keyboard search, arrow navigation, manual IDs,
disabled unsuitable models and focus restoration. Label credential and test
states independently. Keep error text visible with alert semantics and test
feedback announced as status. Preserve the route's heading focus target.

## Code and evidence

Run `make test-model-connections`, `make test-model-connections-api`, and the
three design gates. The custom-element-root fixture mounts both providers on
both Models and create routes at 1440px and 390px in light and dark themes.
The final current-tree full-root 16/16 matrix passes with mobile 44px controls,
no overflow, usage expansion containment, and model card checks. Evidence is
recorded at `/var/tmp/codex-build/models-heavy-render/check-final-current.json`.
A rendered fixture verifies appearance separately from source tests. App Studio
loads Models settings on demand; its bootstrap keeps an independent Vite
preload helper so it stays a repeatable classic script. The existing build
checks enforce that contract and both page and total budgets.

## Related guidance

See [resource creation](../patterns/resource-creation.md) and
[interaction accessibility](../accessibility/interaction.md).
