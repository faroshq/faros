import assert from 'node:assert/strict';
import test from 'node:test';
import { ActionsClientError, createActionsClient } from './index.mjs';

test('invokes through the App Studio gateway with the caller token', async () => {
  let request;
  const client = createActionsClient({
    baseURL: 'https://hub.example/services/providers/app-studio/',
    project: 'demo app',
    token: 'caller-token',
    fetch: async (url, options) => {
      request = { url, options };
      return new Response(JSON.stringify({ result: { rows: [{ id: 1 }] } }), { status: 200 });
    },
  });
  const result = await client.integration('sales').invoke('query_table/v1', { limit: 1 });
  assert.deepEqual(result, { rows: [{ id: 1 }] });
  assert.equal(request.url, 'https://hub.example/services/providers/app-studio/api/projects/demo%20app/integrations/sales/invoke');
  assert.equal(request.options.headers.Authorization, 'Bearer caller-token');
  assert.deepEqual(JSON.parse(request.options.body), { action: 'query_table/v1', input: { limit: 1 } });
});

test('queryTable uses the versioned generic action', async () => {
  let body;
  const client = createActionsClient({
    baseURL: 'https://hub.example/services/providers/app-studio',
    project: 'demo',
    token: 'token',
    fetch: async (_url, options) => {
      body = JSON.parse(options.body);
      return new Response(JSON.stringify({ result: [] }), { status: 200 });
    },
  });
  await client.queryTable('sales', { columns: ['id'] });
  assert.equal(body.action, 'query_table/v1');
});

test('carries caller-supplied local tenant context without choosing a provider URL', async () => {
  let request;
  const client = createActionsClient({
    baseURL: 'https://hub.example/services/providers/app-studio',
    project: 'demo',
    token: 'caller-token',
    headers: {
      Authorization: 'Bearer attacker-token',
      'X-Kedge-Org': 'org-a',
      'X-Kedge-Workspace': 'workspace-a',
    },
    fetch: async (url, options) => {
      request = { url, options };
      return new Response(JSON.stringify({ result: { ok: true } }), { status: 200 });
    },
  });
  const result = await client.integration('sales').invoke('lookup/v1', { key: 'order-1' });
  assert.deepEqual(result, { ok: true });
  assert.equal(request.options.headers.Authorization, 'Bearer caller-token');
  assert.equal(request.options.headers['X-Kedge-Org'], 'org-a');
  assert.equal(request.options.headers['X-Kedge-Workspace'], 'workspace-a');
  assert.equal(request.url.includes('databricks'), false);
  assert.deepEqual(JSON.parse(request.options.body), {
    action: 'lookup/v1',
    input: { key: 'order-1' },
  });
});

test('requires a server credential and does not hide HTTP failures', async () => {
  const noCredential = createActionsClient({ baseURL: 'http://hub', project: 'demo', fetch: async () => new Response('{}') });
  await assert.rejects(() => noCredential.queryTable('sales'), ActionsClientError);
  const failure = createActionsClient({
    baseURL: 'http://hub', project: 'demo', token: 'token',
    fetch: async () => new Response(JSON.stringify({ message: 'revoked' }), { status: 403 }),
  });
  await assert.rejects(
    () => failure.queryTable('sales'),
    (err) => err instanceof ActionsClientError && err.status === 403 && err.message === 'revoked',
  );
});

test('development credentials are explicit and never inferred by default', async () => {
  const prior = process.env.KEDGE_ACTIONS_DEV_TOKEN;
  process.env.KEDGE_ACTIONS_DEV_TOKEN = 'local-token';
  try {
    const implicit = createActionsClient({
      baseURL: 'http://hub', project: 'demo',
      fetch: async () => new Response('{}'),
    });
    await assert.rejects(() => implicit.queryTable('sales'), /credential is required/);

    let authorization;
    const explicit = createActionsClient({
      baseURL: 'http://hub', project: 'demo', allowDevelopmentToken: true,
      fetch: async (_url, options) => {
        authorization = options.headers.Authorization;
        return new Response(JSON.stringify({ result: [] }), { status: 200 });
      },
    });
    await explicit.queryTable('sales');
    assert.equal(authorization, 'Bearer local-token');

    const staticDevelopment = createActionsClient({
      baseURL: 'http://hub', project: 'demo', devToken: 'synthetic-token',
      fetch: async (_url, options) => {
        authorization = options.headers.Authorization;
        return new Response(JSON.stringify({ result: [] }), { status: 200 });
      },
    });
    await staticDevelopment.queryTable('sales');
    assert.equal(authorization, 'Bearer synthetic-token');
  } finally {
    if (prior === undefined) delete process.env.KEDGE_ACTIONS_DEV_TOKEN;
    else process.env.KEDGE_ACTIONS_DEV_TOKEN = prior;
  }
});

test('does not accept provider-controlled query inputs', async () => {
  const client = createActionsClient({
    baseURL: 'http://hub', project: 'demo', token: 'token',
    fetch: async () => new Response('{}'),
  });
  await assert.rejects(() => client.queryTable('sales', { tableRef: 'other' }), /tableRef/);
  await assert.rejects(() => client.queryTable('sales', { sql: 'select 1' }), /sql/);
});

test('fails closed when browser globals are present', () => {
  const priorWindow = globalThis.window;
  globalThis.window = {};
  try {
    assert.throws(
      () => createActionsClient({ baseURL: 'http://hub', project: 'demo', token: 'token' }),
      /server-only/,
    );
  } finally {
    if (priorWindow === undefined) delete globalThis.window;
    else globalThis.window = priorWindow;
  }
});
