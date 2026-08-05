# `@kedge/actions-node`

This is a small server-side SDK for generated App Studio applications. It
invokes a project integration through App Studio's authenticated gateway:

```js
import { createActionsClient } from '@kedge/actions-node';

const kedge = createActionsClient({
  baseURL: process.env.KEDGE_HUB_URL + '/services/providers/app-studio',
  project: process.env.KEDGE_PROJECT,
  // Read this from the server-side workload credential store.
  token: process.env.KEDGE_CALLER_TOKEN,
});

const rows = await kedge.integration('sales').invoke('query_table/v1', {
  columns: ['order_id', 'total'],
  limit: 25,
});
// Equivalent convenience form:
const sameRows = await kedge.queryTable('sales', { limit: 25 });
```

The credential is sent only by the server-side process. Do not import this
module into browser code, expose its token through client-side configuration,
or pass Databricks credentials, provider URLs, or raw SQL. The SDK throws when
`window` or `document` is present as a defense against accidental browser
bundling.

For local prototypes only, pass an explicit synthetic `devToken`, or opt into
`allowDevelopmentToken: true` to read `KEDGE_ACTIONS_DEV_TOKEN`. That token is
a development convenience, not production workload identity.
