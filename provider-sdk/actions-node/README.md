# `@kedge/actions-node`

`@kedge/actions-node` is a server-only SDK for generated App Studio
applications. It invokes a project integration through App Studio's
authenticated gateway; the gateway selects the provider and bound resource:

```js
import { createActionsClient } from '@kedge/actions-node';

const kedge = createActionsClient({
  baseURL: process.env.KEDGE_ACTIONS_BASE_URL,
  project: process.env.KEDGE_PROJECT,
  // The coordinator atomically refreshes this file; the SDK reads it for
  // every request so an in-flight workload never needs the bootstrap token.
  tokenFile: process.env.KEDGE_ACTIONS_TOKEN_FILE,
});

const rows = await kedge.integration('sales').invoke('query_table/v1', {
  columns: ['order_id', 'total'],
  limit: 25,
});
```

The credential is sent only by the server-side process. Do not import this
module into browser code, expose its token through client-side configuration,
or pass provider URLs, credentials, resource references, or other topology in
action input. The SDK throws when `window` or `document` is present as a
defense against accidental browser bundling.

## Development sandbox delivery

App Studio development sandboxes receive the canonical package through the
platform-owned `kedge-dev-agent` image. At startup, its installer validates the
package metadata (`package.json`) and required `index.mjs`/`index.d.ts` files,
then atomically installs them into a shared `emptyDir`. The app and executor
containers mount that same volume read-only at `/node_modules`, so generated
code uses the standard bare import
`import { createActionsClient } from '@kedge/actions-node';` without `npm install`
and without mutating the project PVC or its `node_modules`.

This is development-sandbox injection only. Production workloads still require
the normal pinned package install/publication path; this mechanism does not
claim that production publication or installation is complete. The image-build
and Docker shared-volume Node import checks pass. In the local POC, Ready pod
`data-dashboard` passed the executor bare-import check (function), the app
SDK/environment/token preflight, and a `taxi-trips` `query_table/v1` call that
returned `rowCount=1`, `columnCount=6`, and `truncated=false` without printing
row data. This is local POC evidence only, not a production claim.

Use an atomically refreshed token file (the default when
`KEDGE_ACTIONS_TOKEN_FILE` is set), a static workload token, or a refreshable credential provider. A provider is
called with `{ forceRefresh, signal }` and is called again with
`forceRefresh: true` after a single HTTP 401:

```js
const kedge = createActionsClient({
  baseURL: process.env.KEDGE_APP_STUDIO_URL,
  project: process.env.KEDGE_PROJECT,
  getToken: ({ forceRefresh }) => tokenStore.get({ forceRefresh }),
});
```

When `tokenFile` is omitted, the SDK reads `KEDGE_ACTIONS_TOKEN_FILE` on every
request. This is the shared, read-only application token published by the
development coordinator. Never point it at the coordinator-only projected
bootstrap token path.

`baseURL`, `project`, `org`, and `workspace` default to
`KEDGE_ACTIONS_BASE_URL`, `KEDGE_PROJECT`, `KEDGE_ACTIONS_ORG`, and
`KEDGE_ACTIONS_WORKSPACE`. The latter two are sent as `X-Kedge-Org` and
`X-Kedge-Workspace` headers. The base URL must be absolute HTTPS; tests may
explicitly set `allowInsecureLoopback: true` for an HTTP loopback URL.

Every request can carry retry and tracing metadata. `timeoutMs` aborts the
request locally; `signal` can be used by the enclosing server request:

```js
const value = await kedge.integration('sales').invoke('lookup/v1', { key: 'order-1' }, {
  signal: request.signal,
  timeoutMs: 10_000,
  idempotencyKey: 'job-42-attempt-1',
  requestID: 'request-42',
  actionDeadlineMs: 15_000,
});
```

The successful return value is `result`. `invokeEnvelope` returns the complete
stable envelope (`requestID`, provider, action/version, bound `resourceRef`,
and `result`). A provider failure throws `ProviderActionError` with stable
`code`, `message`, `retryable`, request and binding metadata. Transport and
configuration failures throw `ActionsClientError` with a machine-readable
`code` such as `timeout`, `aborted`, `network_error`, or `invalid_response`.
