# Provider Actions (prototype)

Provider Actions is the small, bounded contract that lets a generated App
Studio application invoke a provider-owned capability through the hub. It is
an integration prototype, not a general RPC layer or a production-readiness
claim. The current implementation has one executable action:
Databricks `query_table/v1`.

## Contract

An App Studio environment stores an integration as a
`ProjectProviderBindingSpec` with `kind: providerReference`. The reference is
non-owning: App Studio may observe the provider object with a GET, but never
creates, updates, or deletes that object and never adds an owner reference.
The reference identifies the provider, API version, kind, resource, and name
(`resourceRef`). The binding also carries an explicit versioned action
allow-list:

```yaml
name: taxi
provider: databricks
kind: providerReference
resourceRef:
  apiVersion: databricks.kedge.faros.sh/v1alpha1
  kind: Table
  resource: tables
  name: taxi-trips
allowedActions:
  - name: query_table
    version: v1
```

`revoked: true` disables a declared action while preserving the binding and
its history. An omitted action, unsupported version, or revoked declaration
is rejected before a provider tool is called. The integration CRUD and
invoke surface is served by App Studio under
`/services/providers/app-studio/api/projects/{project}/integrations`; invoke
accepts an action such as `query_table/v1` and an object-shaped `input`.

The contract is intentionally generic at the binding boundary. The gateway
currently adapts only `databricks/query_table/v1`; another provider or action
is reported as not implemented until an adapter is added.

Implementation anchors are [the App Studio integration gateway](../providers/app-studio/api/integrations.go),
[the non-owning reference reconciler](../providers/app-studio/api/provider_resources.go),
[the Databricks TableQuery controller](../providers/databricks/controller/tablequery/controller.go),
and [the server-only Node SDK](../provider-sdk/actions-node/index.mjs).

## Request sequence

```text
generated app (server)
  -> @kedge/actions-node
  -> App Studio integration gateway
       authenticate caller; find project + alias + environment
       enforce providerReference and allowedActions/revoked
       GET the bound Table; inject its tableRef
       reject caller-supplied tableRef, SQL, credentials, host, or warehouse
  -> hub MCP aggregate: databricks__query_table
  -> Databricks provider MCP server
       create transient TableQuery in the caller's tenant
       controller resolves Table -> Warehouse -> Connection -> Secret
       provider builds an exact SELECT and executes it
       return bounded structured rows; delete the transient query
  -> generated app
```

The gateway forwards the caller's bearer credential and tenant context to the
hub aggregate. It does not choose a Databricks URL or hold a Databricks
credential. The provider resolves its own connection, warehouse, and Secret
from tenant-scoped resources. This preserves the provider-isolation boundary:
cross-provider access uses the owning provider's published API/MCP surface,
as the tenant caller, rather than an internal service or backend credential.
See [Provider isolation in the provider architecture](./providers.md#provider-isolation-the-cross-provider-boundary).

## `query_table/v1`

The action requires `actionVersion: v1`, a bound `tableRef` (injected by the
gateway), optional exact column names, and an optional `limit`. The caller
cannot submit SQL, a different table, a warehouse ID, a connection host, or
any provider credential. A missing limit defaults to 100; the maximum is 100.
Identifiers are validated and quoted individually, and requested columns must
be present in the imported Table schema.

The result contains `actionVersion`, `tableRef`, column metadata, rows, and an
optional `truncated` flag. Results are bounded to at most 100 rows, 64
columns, and 64 KiB of serialized row data. The provider's TableQuery
controller gives backend execution a 30-second deadline; the tenant adapter
polls for at most 45 seconds and cleans up the transient object with a
bounded three-second context.

The controller only executes when the current dependency conditions are
satisfied: the Table and Warehouse are `Ready`, the Connection is both
`Validated` and `Ready`, the Table/Warehouse connection references agree, and
the Connection uses the supported PAT auth type. Failures are recorded as a
sanitized, truncated status message; credential, token, Secret, and similar
backend details are not returned to the caller.

The hub MCP federation bounds provider discovery at 15 seconds and a provider
call at 90 seconds. The caller's cancellation context wins over those bounds.
An unavailable or unbound integration fails closed, and a revoked or
non-allow-listed action does not reach the upstream provider.

## Server-only SDK

`@kedge/actions-node` is a server-only convenience client. It sends the
caller credential to App Studio and exposes either the generic
`integration(alias).invoke(...)` form or the `queryTable(alias, ...)` helper:

```js
import { createActionsClient } from '@kedge/actions-node';

const kedge = createActionsClient({
  baseURL: process.env.KEDGE_HUB_URL + '/services/providers/app-studio',
  project: process.env.KEDGE_PROJECT,
  token: process.env.KEDGE_CALLER_TOKEN, // server-side caller capability
});

const rows = await kedge.queryTable('taxi', {
  columns: ['trip_id', 'fare_amount'],
  limit: 25,
});
```

The SDK rejects browser-like globals and requires an explicit credential. A
`devToken` or `allowDevelopmentToken` is available only for local prototypes;
the synthetic token is not workload identity. Production generated apps still
need a short-lived workload capability issued for the caller and passed to
the server process. Never put this SDK or its token in browser code, and never
pass provider URLs, Databricks credentials, or raw SQL.

## Extending the prototype

To add a provider action:

1. Define a versioned, provider-neutral action contract and its bounded input
   and output. Keep provider credentials, backend URLs, and arbitrary query
   languages out of the caller input.
2. Publish the action through the provider's API/MCP surface and enforce
   tenant-scoped authorization there. The provider remains the sole owner of
   its backend and secrets.
3. Add an App Studio adapter that validates the provider reference and
   versioned allow-list, injects only server-resolved resource identity, and
   maps provider errors to a sanitized caller contract.
4. Add deterministic contract tests for allow/revoke/version failures,
   non-owning references, cancellation, bounds, and credential non-disclosure;
   then add a live smoke only when a real provider and tenant are available.

Adding an action does not make all providers executable automatically. The
gateway's current implementation is deliberately limited to Databricks
`query_table/v1`.

## Verification

The deterministic local suite builds an embedded hub, host-process App Studio
and Databricks providers, a local TLS fake Databricks endpoint, and a generated
Node app. It verifies the full generated-app → SDK → App Studio → hub MCP →
provider path, including exact SQL/projection, bounded output, no direct
provider URL or PAT in app output, and fail-closed unbound/version/revoked
cases:

```bash
make e2e-provider-actions
```

The SDK unit tests can be run independently:

```bash
cd provider-sdk/actions-node && npm test
```

An opt-in smoke exercises an existing local setup without creating resources.
Set `KEDGE_E2E_PROVIDER_ACTIONS_LIVE=true`, `KEDGE_LIVE_HUB_URL`,
`KEDGE_LIVE_PROJECT`, and `KEDGE_LIVE_CALLER_TOKEN` (optionally
`KEDGE_LIVE_ACTION_ALIAS`, `KEDGE_LIVE_ORG`, and `KEDGE_LIVE_WORKSPACE`), then
run:

```bash
make e2e-provider-actions-live
```

The local deterministic run and the real local `taxi-trips` SDK query have
passed. These checks verify the prototype path; they do not establish general
provider coverage, durable action scheduling, or production workload
identity.

## Prototype limitations and boundary

- Only Databricks `query_table/v1` is implemented. The binding shape is
  generic, but generic dispatch and provider discovery are not.
- The local E2E uses a synthetic caller token. It proves routing and
  authorization behavior, not issuance, rotation, or attestation of a
  workload identity.
- `TableQuery` is transient, but its bounded rows live in control-plane status
  until cleanup. Cleanup is attempted and bounded; retention and crash
  recovery policy are not a production data-retention design.
- The action is synchronous and bounded. There is no durable queue, retry
  policy, progress stream, or resumable execution contract.
- Provider isolation remains mandatory. A future adapter must call the owning
  provider's published API/MCP surface as the tenant caller and must not add a
  second credential or direct URL into another provider's backend.
