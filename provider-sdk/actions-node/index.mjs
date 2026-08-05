/**
 * Server-side App Studio integration gateway client.
 *
 * This module is intentionally provider-neutral. It carries the Kedge caller
 * credential to App Studio, where the server resolves a project binding and
 * enforces its versioned action allow-list before forwarding through the hub
 * MCP federation. It never accepts Databricks credentials, provider URLs, or
 * raw SQL.
 *
 * Browser use is forbidden: a Kedge caller credential is a server secret.
 * The client throws when a browser-like global is present, even when a bundler
 * makes fetch available. Keep this dependency in a server-only generated-app
 * process (API route, worker, or job), never in a client bundle.
 */

export class ActionsClientError extends Error {
  constructor(message, { status = 0, body = undefined } = {}) {
    super(message);
    this.name = 'ActionsClientError';
    this.status = status;
    this.body = body;
  }
}

function assertServerOnly() {
  if (typeof window !== 'undefined' || typeof document !== 'undefined') {
    throw new ActionsClientError(
      'The Kedge Actions SDK is server-only; never expose a caller credential to a browser'
    );
  }
}

function normalizeToken(value) {
  const token = String(value ?? '').trim();
  if (!token) return '';
  return /^Bearer\s+/i.test(token) ? token : `Bearer ${token}`;
}

function joinURL(baseURL, path) {
  const base = String(baseURL ?? '').trim().replace(/\/+$/, '');
  if (!base) throw new ActionsClientError('baseURL is required');
  return `${base}/${String(path).replace(/^\/+/, '')}`;
}

async function resolveCredential(options) {
  const token = typeof options.token === 'function' ? await options.token() : options.token;
  if (token) return token;
  // A static token is useful for the local prototype only. Requiring the
  // caller to pass it explicitly prevents a production process from silently
  // falling back to a development secret.
  if (options.devToken) return options.devToken;
  if (options.allowDevelopmentToken === true) {
    return process.env.KEDGE_ACTIONS_DEV_TOKEN ?? '';
  }
  return '';
}

function actionPath(project, alias) {
  return `/api/projects/${encodeURIComponent(project)}/integrations/${encodeURIComponent(alias)}/invoke`;
}

export class ActionsClient {
  constructor(options = {}) {
    assertServerOnly();
    this.baseURL = options.baseURL ?? options.baseUrl;
    this.project = String(options.project ?? '').trim();
    if (!this.project) throw new ActionsClientError('project is required');
    this.fetch = options.fetch ?? globalThis.fetch;
    if (typeof this.fetch !== 'function') throw new ActionsClientError('fetch is required');
    this.token = options.token;
    this.devToken = options.devToken;
    this.allowDevelopmentToken = options.allowDevelopmentToken === true;
    this.headers = { ...(options.headers ?? {}) };
    if (typeof options.getToken === 'function' && this.token === undefined) {
      this.token = options.getToken;
    }
  }

  integration(alias) {
    const name = String(alias ?? '').trim();
    if (!name) throw new ActionsClientError('integration alias is required');
    return {
      invoke: (action, input = {}) => this.invoke(name, action, input),
    };
  }

  async invoke(alias, action, input = {}) {
    assertServerOnly();
    const actionName = String(action ?? '').trim();
    if (!actionName) throw new ActionsClientError('action is required');
    if (input === null || typeof input !== 'object' || Array.isArray(input)) {
      throw new ActionsClientError('action input must be an object');
    }
    if (actionName.toLowerCase() === 'query_table/v1') {
      for (const forbidden of ['tableRef', 'sql', 'query', 'token', 'bearerToken', 'host', 'warehouseId', 'connection']) {
        if (Object.prototype.hasOwnProperty.call(input, forbidden)) {
          throw new ActionsClientError(`query_table/v1 input cannot include ${forbidden}; the gateway resolves provider state server-side`);
        }
      }
    }
    const token = normalizeToken(await resolveCredential(this));
    if (!token) {
      throw new ActionsClientError(
        'a Kedge caller credential is required; pass token/getToken on the server or opt into a local development token'
      );
    }
    const headers = {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      ...this.headers,
      Authorization: token,
    };
    const response = await this.fetch(joinURL(this.baseURL, actionPath(this.project, alias)), {
      method: 'POST',
      headers,
      body: JSON.stringify({ action: actionName, input }),
    });
    const text = await response.text();
    let body;
    try {
      body = text ? JSON.parse(text) : undefined;
    } catch {
      body = text;
    }
    if (!response.ok) {
      const message = body?.message ?? body?.error ?? `integration action failed with HTTP ${response.status}`;
      throw new ActionsClientError(String(message), { status: response.status, body });
    }
    return body?.result ?? body;
  }

  queryTable(alias, input = {}) {
    return this.integration(alias).invoke('query_table/v1', input);
  }
}

export function createActionsClient(options) {
  return new ActionsClient(options);
}

export default createActionsClient;
