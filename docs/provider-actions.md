# Provider Actions

Provider Actions is the catalog-backed, synchronous action contract for
server-side generated applications. Providers publish versioned action
metadata in their `CatalogEntry`; App Studio grants an exact action and
resource to a Project; the hub authorizes and forwards the invocation to the
provider's declared VirtualWorkspace action endpoint. The public contract is
generic, but the only shipped action is Databricks `query_table/v1`.

## Catalog contract

`CatalogEntry.spec.actions` is the provider's public action catalog. Each
entry is keyed by an ID such as `query_table/v1` and declares all policy and
validation data needed by callers without exposing a provider URL or
credential model:

| Field | Meaning |
|---|---|
| `id`, `displayName`, `description` | Stable name/version plus human-facing metadata. IDs are `name/vN`. |
| `boundResource` | Exact API version, kind, and resource whose identity is supplied by the Project binding. |
| `inputSchema`, `outputSchema` | JSON Schemas for caller input and provider result. Schemas are local, bounded, and compiled by the hub. |
| `schemaDigest` | `sha256:` digest over the canonical input/output schema envelope. The hub and App Studio require an exact match. |
| `executionMode` | `sync` or `async`; the current hub transport accepts `sync` only. |
| `readOnly` | Provider declaration that the action does not mutate the bound resource. |
| `risk` | `low`, `medium`, or `high`, used by consent and UI policy. |
| `idempotency` | `inherent`, `keyed`, or `none`; keyed idempotency returns `501` until durable deduplication exists. |
| `limits` | Timeout, input bytes, output bytes, and result-item bounds. |
| `consent` | Whether explicit approval is required, including its prompt and scope. |
| `deprecation` | Optional deprecation message, replacement action ID, and sunset timestamp. Deprecated actions cannot receive new grants. |

The hub validates the complete declaration, canonicalizes and compiles both
schemas, and stores the normalized metadata in its provider registry. Malformed
catalog state fails closed before it can enter the action router. The
portal-facing `/api/providers` projection exposes discovery and consent
metadata, but not transport URLs.

Databricks publishes `query_table/v1` bound to
`databricks.kedge.faros.sh/v1alpha1 / Table / tables`. Its catalog declaration
is `sync`, `readOnly: true`, `risk: low`, `idempotency: inherent`, with a
45-second timeout, 8 KiB input cap, 64 KiB output cap, and 100 result-item
cap. Consent is not required. Its input schema permits only optional exact
`columns` (at most 64) and `limit` (1–100); its output schema contains
`actionVersion`, `tableRef`, column metadata, rows (at most 100), and an
optional `truncated` flag. The declaration's schema digest is
`sha256:9d466354d5434778c39c74123156aba76510128b0d48c5f521836770561ab853`.

## Project grants and audit

An App Studio Project environment stores a provider reference as
`kind: providerReference`. It is non-owning: App Studio may GET the referenced
provider object for status, but never creates, updates, deletes, or owns it.
The binding carries the exact `resourceRef` and a list of action grants:

```yaml
name: sales
provider: databricks
kind: providerReference
resourceRef:
  apiVersion: databricks.kedge.faros.sh/v1alpha1
  kind: Table
  resource: tables
  name: order-history
allowedActions:
  - name: query_table
    version: v1
    schemaDigest: sha256:<catalog-digest>
```

On integration create or reactivation, App Studio fetches the caller-scoped
hub catalog and requires an exact provider, action/version, bound resource,
schema digest, and non-deprecated action. If catalog consent is required,
`consentAccepted: true` is also required. The server writes
`grantedBy` and `grantedAt`; client-supplied audit values are ignored. A
revoke preserves the grant and its original digest/audit, then records
server-owned `revokedBy` and `revokedAt`. Repeated revocation is idempotent;
reactivation requires a fresh catalog verification and consent.

This is generic catalog-backed authorization, not a provider-specific App
Studio adapter. Integration CRUD is exposed under
`/services/providers/app-studio/api/projects/{project}/integrations`; invoke
uses the same alias and accepts a provider-neutral action name/version.

## Invocation and security boundary

```text
generated server application
  -> @kedge/actions-node
  -> App Studio integration invoke
       verify persisted grant, digest, and exact bound resource
       POST /api/provider-actions/invoke to the hub
  -> hub provider-action router
       resolve Ready CatalogEntry action and tenant cluster
       verify workload identity + exact Project grant when caller is a workload
       validate input schema and byte/result/time limits
       POST provider VirtualWorkspace /actions/{name}/{version}
  -> provider action handler
       authorize the bound resource and return a bounded JSON result
```

The hub request body contains `provider`, `action`, `actionVersion`,
`schemaDigest`, `resourceRef`, and `input`. The hub compares every identity
field with the catalog, validates the input schema, limits the request, rejects
redirects, and validates the provider's JSON result against the output schema.
It forwards only the authenticated bearer, resolved tenant/user/cluster
headers, request ID, trace context, idempotency key, and bounded deadline.
Caller cancellation and the declared action timeout bound the synchronous
request. The stable response envelope carries `requestID`, provider,
action/version, the bound resource reference, and either `result` or a typed
error (`code`, `message`, `retryable`).

The direct hub route is `POST /api/provider-actions/invoke`. The provider
VirtualWorkspace route is `/actions/{name}/{version}`. The public provider
backend proxy reserves `/actions` and `/actions/*` and returns `404`, so a
caller cannot bypass the hub's live grant, digest, schema, and revocation
checks through `/services/providers/{name}`.

Production workload callers use the workload exchange and a short-lived
workload capability:

1. The development runtime reads a projected bootstrap token, posts the exact
   tenant/project/project UID/environment/instance tuple to
   `/api/provider-actions/workload/exchange`, and never exposes that bootstrap
   token to the generated application.
2. The hub asks the Infrastructure provider to perform online attestation at
   `/workload-identities/review`. Infrastructure performs an audience-bound
   TokenReview and verifies the pod identity and exact runtime tuple.
3. The hub verifies the live Project environment, instance, provider resource
   references, and action grant, then issues a short-lived Kedge
   ServiceAccount token with GET-only resource scope. The current token TTL is
   ten minutes and the token is not persisted in a Secret or annotation.
4. The runtime atomically refreshes a mode-`0600` token file. The generated
   server reads that file on each request, or uses a refreshable credential
   provider; a single `401` triggers one forced refresh.

The SDK is server-only. Its base URL must be absolute HTTPS; HTTP is allowed
only for an explicit loopback test override. Do not pass provider URLs,
provider credentials, resource coordinates, or raw SQL in action input. The
runtime's `KEDGE_ACTIONS_CA_FILE` can add an explicitly configured CA for the
workload exchange, but the source does not provide automatic custom-CA
distribution. Production external URLs therefore require HTTPS with a
system- or publicly-trusted certificate unless deployment configuration
explicitly supplies the CA.

## Server-side SDK

Use the generic `integration(alias).invoke` API. The SDK never exposes a
provider-specific convenience method:

```js
import { createActionsClient } from '@kedge/actions-node';

const kedge = createActionsClient({
  baseURL: process.env.KEDGE_ACTIONS_BASE_URL,
  project: process.env.KEDGE_PROJECT,
  tokenFile: process.env.KEDGE_ACTIONS_TOKEN_FILE,
});

const result = await kedge.integration('sales').invoke(
  'query_table/v1',
  { columns: ['order_id', 'total'], limit: 25 },
  { requestID: 'request-42', timeoutMs: 10_000 },
);
console.log(result);
```

`tokenFile` defaults to `KEDGE_ACTIONS_TOKEN_FILE`; it is read for every
request. A `getToken`/credential provider receives `{ forceRefresh, signal }`
and is retried once after an HTTP `401`. The SDK propagates caller aborts and
local timeouts, rejects browser globals, and returns typed transport or
provider-action errors. There is no development-token fallback.

### Development sandbox delivery

In an App Studio development sandbox, the Infrastructure `kedge-dev-agent`
image carries the canonical `@kedge/actions-node` package. Its installer
validates the package metadata (`package.json`) and runtime/type files
(`index.mjs` and `index.d.ts`) before atomically copying them into a
platform-owned shared `emptyDir`. The app and executor containers mount that
same volume read-only at `/node_modules`, so generated code uses the standard
bare import `import { createActionsClient } from '@kedge/actions-node';`
without `npm install` and without mutating the project PVC or its
`node_modules`.

This delivery path is limited to development sandboxes. Production workloads
still need a normal pinned package install/publication; these checks do not
claim production publication or installation is complete. The image-build and
Docker shared-volume Node import checks pass. In the local POC, Ready pod
`data-dashboard` passed the executor bare-import check (function), the app
SDK/environment/token preflight, and a `taxi-trips` `query_table/v1` call that
returned `rowCount=1`, `columnCount=6`, and `truncated=false` without printing
row data. This is local POC evidence only, not a production claim.

## Databricks implementation

`POST /actions/query_table/v1` is the primary app path. The request-scoped
Databricks executor performs delegated authorization for the exact imported
Table, resolves `Table → Warehouse → Connection → Secret` with provider
authority, requires current Table/Warehouse `Ready` and Connection
`Validated`/`Ready` conditions, checks the connection references and PAT auth
type, then builds a quoted projection and bounded `SELECT`. It never creates a
query resource and never persists result rows in control-plane status. Provider
and credential details are sanitized from errors.

The optional `/mcp` and `/mcp/sse` surfaces are controlled by
`DATABRICKS_MCP_ENABLED` (enabled by default for compatibility). When enabled,
the MCP `query_table` tool reuses the same request-scoped executor; it is an
optional presentation adapter and is not required by the primary generated-app
action path. Setting `DATABRICKS_MCP_ENABLED=false` leaves direct actions
available.

The provider accepts only the imported Table resource reference, exact column
identifiers, and a limit from 1 through 100. SQL text, hosts, warehouse IDs,
connection handles, and credentials are not caller inputs. The backend uses
the Databricks SQL Statements API and the provider's configured host allowlist.

## Observability and residual limits

The hub emits low-cardinality metrics for provider, action, version, outcome,
HTTP status, and error class, plus duration and request/response byte
histograms. Logs and forwarded W3C trace context carry request IDs and
resource/action identity without prompt text, raw input, credentials, or
sensitive backend values. Avoid adding tenant IDs, project names, resource
names, digests, or arbitrary error text as metric labels.

The transport is synchronous and bounded. Keyed idempotency currently returns
`501`; there are no durable jobs, progress streams, or resume handles. The
portal and grant contract require an exact resource reference; resource-name
discovery is not a picker supplied by the action transport. Only
`query_table/v1` is shipped today.

## Verification commands

The deterministic suite runs the embedded hub, Infrastructure attestation
fixture, App Studio, Databricks, a local TLS fake, and a generated Node app.
It exchanges a workload token, writes it to a token file, disables Databricks
MCP, and verifies direct `/actions` routing, exact Project grants, digest drift,
tenant isolation, bounded results, and credential non-disclosure:

```bash
make e2e-provider-actions
```

SDK unit tests:

```bash
cd provider-sdk/actions-node && npm test
```

The opt-in live command reads an already-refreshed workload token file. Set
`KEDGE_E2E_PROVIDER_ACTIONS_LIVE=true`, `KEDGE_LIVE_HUB_URL`,
`KEDGE_LIVE_PROJECT`, and `KEDGE_LIVE_ACTIONS_TOKEN_FILE` (optionally
`KEDGE_LIVE_ACTION_ALIAS`, `KEDGE_LIVE_ORG`, and `KEDGE_LIVE_WORKSPACE`):

```bash
make e2e-provider-actions-live
```

These are verification commands; this document does not claim that a current
deterministic or live run has passed.

Implementation anchors: [CatalogEntry action types](../apis/providers/v1alpha1/types_catalogentry.go),
[hub action router](../pkg/hub/provideractions/handler.go),
[hub workload exchange](../pkg/hub/workloadidentity/workloadidentity.go),
[App Studio grant verification](../providers/app-studio/api/provider_action_catalog.go),
[App Studio forwarding](../providers/app-studio/api/integrations.go),
[server-only SDK](../provider-sdk/actions-node/index.mjs), and
[Databricks direct action executor](../providers/databricks/tenant/action.go).
