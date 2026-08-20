// GraphQL client for the secrets provider's portal.
//
// Every read and write goes through the hub's embedded GraphQL gateway at
// /graphql/<cluster> — reads as `secrets_faros_sh { v1alpha1 { … } }` queries,
// writes as delete mutations (and applyYaml for create-or-update). The shell
// pushes farosContext.tenant (kcp cluster name, used as the /graphql path
// segment) and farosContext.token (bearer). Same auth/path model as the code
// provider's portal.

import type {
  ErrorResponse,
  SecretStoreRow,
  SyncedSecretDataMap,
  SyncedSecretRow,
} from './types'

const GROUP = 'secrets.faros.sh'
const GROUP_FIELD = 'secrets_faros_sh'
const VERSION = 'v1alpha1'
// Portal-created credential Secrets live in the default namespace under the
// default "token" key — matching the SecretStore API's own defaults.
const CRED_NAMESPACE = 'default'
const TOKEN_KEY = 'token'
// CRED_SUFFIX names portal-created credential Secrets (<store>-vault-token).
// deleteStore only removes the Secret when spec.secretRef matches this
// convention, so a store pointed at a pre-existing shared Secret never takes
// that Secret down with it.
const CRED_SUFFIX = '-vault-token'

let bearerToken: string | null = null
let clusterName: string | null = null

// setBasePath is a no-op: kcp paths are built from the cluster name, not the
// provider basePath. Kept so App.vue's watcher type-checks.
export function setBasePath(_ctxBasePath?: string | null) {
  void _ctxBasePath
}
export function setToken(token?: string | null) {
  bearerToken = token || null
}
export function setTenant(name?: string | null) {
  clusterName = name || null
}

interface KCPMetadata {
  name: string
  namespace?: string
  uid?: string
  resourceVersion?: string
  generation?: number
  creationTimestamp?: string
}
interface KCPCondition {
  type: string
  status: string
  reason?: string
  message?: string
  lastTransitionTime?: string
}
interface RawCR {
  metadata: KCPMetadata
  spec?: Record<string, unknown>
  status?: { conditions?: KCPCondition[] } & Record<string, unknown>
}

// graphqlQuery runs a query against the hub's embedded GraphQL gateway at
// /graphql/<cluster> (same origin as the portal). The gateway serves every CRD
// bound in the tenant workspace — including the secrets provider's — so the
// portal reads/writes CRs without a custom REST endpoint. Auth is the caller's
// own bearer token; the workspace is the path segment.
async function graphqlQuery<T>(query: string, variables: Record<string, unknown>): Promise<T> {
  if (!clusterName) {
    throw <ErrorResponse>{ reason: 'TenantMissing', message: 'no workspace selected' }
  }
  const headers: Record<string, string> = { 'Content-Type': 'application/json', Accept: 'application/json' }
  if (bearerToken) headers['Authorization'] = 'Bearer ' + bearerToken
  const res = await fetch('/graphql/' + clusterName, {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: JSON.stringify({ query, variables }),
  })
  const text = await res.text()
  if (!res.ok) {
    throw <ErrorResponse>{ reason: res.status === 404 ? 'NotFound' : 'HTTPError', message: text || res.statusText }
  }
  const body = (text ? JSON.parse(text) : {}) as { data?: T; errors?: { message: string }[] }
  if (body.errors && body.errors.length) {
    throw <ErrorResponse>{ reason: 'GraphQLError', message: body.errors.map(e => e.message).join('; ') }
  }
  return (body.data ?? {}) as T
}

function condTrue(cr: RawCR, type: string): boolean {
  return (cr.status?.conditions ?? []).some(c => c.type === type && c.status === 'True')
}
function condMsg(cr: RawCR, type: string): string | undefined {
  const c = (cr.status?.conditions ?? []).find(x => x.type === type)
  if (!c || c.status === 'True') return undefined
  return c.message || c.reason
}
function conditions(cr: RawCR) {
  return (cr.status?.conditions ?? []).map(c => ({
    type: c.type,
    status: c.status,
    reason: c.reason,
    message: c.message,
    lastTransitionTime: c.lastTransitionTime,
  }))
}

// ── Mappers ────────────────────────────────────────────────────────────────
function storeFromCR(cr: RawCR): SecretStoreRow {
  const spec = cr.spec ?? {}
  const status = cr.status ?? {}
  const vault = (spec.vault as Record<string, unknown> | undefined) ?? {}
  const secretRef = (spec.secretRef as Record<string, unknown> | undefined) ?? {}
  return {
    name: cr.metadata.name,
    backend: String(spec.backend ?? ''),
    address: vault.address ? String(vault.address) : '',
    mount: vault.mount ? String(vault.mount) : undefined,
    vaultNamespace: vault.namespace ? String(vault.namespace) : undefined,
    secretName: String(secretRef.name ?? ''),
    secretNamespace: secretRef.namespace ? String(secretRef.namespace) : undefined,
    secretKey: secretRef.key ? String(secretRef.key) : undefined,
    backendVersion: status.backendVersion ? String(status.backendVersion) : undefined,
    validated: condTrue(cr, 'Validated'),
    ready: condTrue(cr, 'Ready'),
    message: condMsg(cr, 'Validated') ?? condMsg(cr, 'Ready'),
    creationTimestamp: cr.metadata.creationTimestamp,
    generation: typeof cr.metadata.generation === 'number' ? cr.metadata.generation : undefined,
    observedGeneration: typeof status.observedGeneration === 'number' ? status.observedGeneration : undefined,
    conditions: conditions(cr),
  }
}

function syncedFromCR(cr: RawCR): SyncedSecretRow {
  const spec = cr.spec ?? {}
  const status = cr.status ?? {}
  const storeRef = (spec.storeRef as Record<string, unknown> | undefined) ?? {}
  const target = (spec.target as Record<string, unknown> | undefined) ?? {}
  const data = Array.isArray(spec.data) ? (spec.data as Array<Record<string, unknown>>) : []
  const dataFrom = Array.isArray(spec.dataFrom) ? (spec.dataFrom as Array<Record<string, unknown>>) : []
  return {
    name: cr.metadata.name,
    namespace: cr.metadata.namespace ?? '',
    store: String(storeRef.name ?? ''),
    // The projected Secret: what the controller wrote (status), else the
    // declared target, else the SyncedSecret's own name (the API default).
    targetSecret: String(status.secretName || target.name || cr.metadata.name),
    refreshInterval: String(spec.refreshInterval ?? '1h'),
    dataFrom: dataFrom.map(d => String(d.path ?? '')),
    data: data.map(d => {
      const remote = (d.remoteRef as Record<string, unknown> | undefined) ?? {}
      return {
        secretKey: String(d.secretKey ?? ''),
        path: String(remote.path ?? ''),
        property: remote.property ? String(remote.property) : undefined,
      }
    }),
    syncedKeys: typeof status.syncedKeys === 'number' ? status.syncedKeys : undefined,
    syncedVersion: status.syncedVersion ? String(status.syncedVersion) : undefined,
    lastSyncTime: status.lastSyncTime ? String(status.lastSyncTime) : undefined,
    ready: condTrue(cr, 'Ready'),
    message: condMsg(cr, 'Ready'),
    creationTimestamp: cr.metadata.creationTimestamp,
    generation: typeof cr.metadata.generation === 'number' ? cr.metadata.generation : undefined,
    observedGeneration: typeof status.observedGeneration === 'number' ? status.observedGeneration : undefined,
    conditions: conditions(cr),
  }
}

// dns1123 turns arbitrary text into a safe object name.
function dns1123(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 253) || 'x'
}

// ── GraphQL write helpers ──────────────────────────────────────────────────
// All writes go through the gateway's mutation API (no kcp REST proxy). applyCR
// wraps applyYaml, whose server-side create-or-update semantics make writes
// idempotent and handle the "adopt an existing object" case (e.g. a leftover
// credential Secret) without client-side resourceVersion juggling.
async function applyCR(manifest: Record<string, unknown>): Promise<RawCR> {
  const data = await graphqlQuery<{ applyYaml?: unknown }>(
    'mutation($y: String!) { applyYaml(yaml: $y) }',
    { y: JSON.stringify(manifest) },
  )
  // applyYaml returns the applied object as a JSON string (JSONString scalar);
  // tolerate an already-parsed object too.
  const raw = data.applyYaml
  return (typeof raw === 'string' ? JSON.parse(raw || '{}') : raw ?? {}) as RawCR
}

// deleteSecret removes a credential Secret (core/v1, namespaced) by name.
// Best-effort: a missing Secret is not an error for the caller.
async function deleteSecret(name: string, namespace: string): Promise<void> {
  await graphqlQuery(
    'mutation($n: String!, $ns: String!) { v1 { deleteSecret(name: $n, namespace: $ns) } }',
    { n: name, ns: namespace },
  )
}

// ── GraphQL read helpers ───────────────────────────────────────────────────
// The gateway returns each CR as a metadata/spec/status object — the same shape
// the kcp REST proxy does — so the *FromCR mappers consume GraphQL items as-is.
// The group secrets.faros.sh is the GraphQL field secrets_faros_sh (dots →
// underscores), list fields are the capitalised plural (SecretStores).
const GQL_COND = 'conditions { type status reason message lastTransitionTime }'
const F_STORE = `metadata { name uid resourceVersion generation creationTimestamp } spec { backend vault { address mount namespace } secretRef { name namespace key } } status { observedGeneration backendVersion ${GQL_COND} }`
const F_SYNCED = `metadata { name namespace uid resourceVersion generation creationTimestamp } spec { storeRef { name } refreshInterval target { name } data { secretKey remoteRef { path property } } dataFrom { path property } } status { observedGeneration secretName lastSyncTime syncedVersion syncedKeys ${GQL_COND} }`

// gqlList queries a resource's list field and returns the RawCR-shaped items.
// Namespaced kinds (SyncedSecrets) list across every namespace when no
// namespace argument is given — exactly what the workspace-wide table wants.
async function gqlList(kind: string, fields: string): Promise<RawCR[]> {
  const query = `query { ${GROUP_FIELD} { ${VERSION} { ${kind} { items { ${fields} } } } } }`
  const data = await graphqlQuery<{ [k: string]: { [v: string]: Record<string, { items?: RawCR[] }> } | undefined }>(query, {})
  return data[GROUP_FIELD]?.[VERSION]?.[kind]?.items ?? []
}

export const api = {
  // ── SecretStores (cluster-scoped) ─────────────────────────────────────────
  async listStores(): Promise<SecretStoreRow[]> {
    return (await gqlList('SecretStores', F_STORE)).map(storeFromCR)
  },

  // createStore creates the SecretStore, and — when the user pasted a token —
  // the credential Secret it references, in that order so the Secret can
  // own-reference the store and be garbage-collected with it (same flow as the
  // code portal's connect()). Idempotent: applyYaml adopts leftovers.
  async createStore(input: {
    name: string
    address: string
    mount?: string
    vaultNamespace?: string
    credential:
      | { mode: 'existing'; secretName: string; secretNamespace?: string; secretKey?: string }
      | { mode: 'token'; token: string }
  }): Promise<SecretStoreRow> {
    const name = dns1123(input.name)
    const vault: Record<string, unknown> = { address: input.address }
    if (input.mount) vault.mount = input.mount
    if (input.vaultNamespace) vault.namespace = input.vaultNamespace

    const ownSecret = input.credential.mode === 'token'
    const secretRef: Record<string, unknown> = ownSecret
      ? { name: name + CRED_SUFFIX, namespace: CRED_NAMESPACE, key: TOKEN_KEY }
      : {
          name: input.credential.mode === 'existing' ? input.credential.secretName : '',
          ...(input.credential.mode === 'existing' && input.credential.secretNamespace
            ? { namespace: input.credential.secretNamespace }
            : {}),
          ...(input.credential.mode === 'existing' && input.credential.secretKey
            ? { key: input.credential.secretKey }
            : {}),
        }

    // 1) SecretStore referencing the (possibly not-yet-created) Secret.
    const store = await applyCR({
      apiVersion: `${GROUP}/${VERSION}`,
      kind: 'SecretStore',
      metadata: { name },
      spec: { backend: 'vault', vault, secretRef },
    })

    // 2) Secret holding the pasted token, owned by the store so kcp GC removes
    // it with the store. applyCR's create-or-update adopts a leftover Secret.
    if (ownSecret && input.credential.mode === 'token') {
      await applyCR({
        apiVersion: 'v1',
        kind: 'Secret',
        metadata: {
          name: name + CRED_SUFFIX,
          namespace: CRED_NAMESPACE,
          ownerReferences: [{ apiVersion: `${GROUP}/${VERSION}`, kind: 'SecretStore', name, uid: store.metadata.uid }],
        },
        type: 'Opaque',
        stringData: { [TOKEN_KEY]: input.credential.token },
      })
    }
    return storeFromCR(store)
  },

  // deleteStore removes the SecretStore, then — only when the referenced
  // credential Secret follows the portal's <name>-vault-token convention (i.e.
  // this portal created it) — the Secret too. A store pointed at a shared,
  // pre-existing Secret never deletes that Secret; the ownerReference on
  // portal-created ones lets GC cover the raced case anyway.
  async deleteStore(store: SecretStoreRow): Promise<void> {
    await graphqlQuery(
      `mutation($n: String!) { ${GROUP_FIELD} { ${VERSION} { deleteSecretStore(name: $n) } } }`,
      { n: store.name },
    )
    if (store.secretName === store.name + CRED_SUFFIX) {
      try {
        await deleteSecret(store.secretName, store.secretNamespace || CRED_NAMESPACE)
      } catch (e) {
        // best-effort: a since-deleted Secret (GC raced us) is fine
        if (!/not\s*found/i.test((e as ErrorResponse).message ?? '')) throw e
      }
    }
  },

  // ── SyncedSecrets (namespaced, listed across all namespaces) ──────────────
  async listSynced(): Promise<SyncedSecretRow[]> {
    return (await gqlList('SyncedSecrets', F_SYNCED)).map(syncedFromCR)
  },

  async createSynced(input: {
    name: string
    namespace?: string
    store: string
    refreshInterval?: string
    targetName?: string
    dataFrom: string[]
    data: SyncedSecretDataMap[]
  }): Promise<SyncedSecretRow> {
    const name = dns1123(input.name)
    const spec: Record<string, unknown> = { storeRef: { name: input.store } }
    if (input.refreshInterval) spec.refreshInterval = input.refreshInterval
    if (input.targetName) spec.target = { name: input.targetName }
    const dataFrom = input.dataFrom.map(p => p.trim()).filter(p => p)
    if (dataFrom.length) spec.dataFrom = dataFrom.map(path => ({ path }))
    const data = input.data.filter(d => d.secretKey.trim() && d.path.trim())
    if (data.length) {
      spec.data = data.map(d => ({
        secretKey: d.secretKey.trim(),
        remoteRef: { path: d.path.trim(), ...(d.property?.trim() ? { property: d.property.trim() } : {}) },
      }))
    }
    const created = await applyCR({
      apiVersion: `${GROUP}/${VERSION}`,
      kind: 'SyncedSecret',
      metadata: { name, namespace: input.namespace || CRED_NAMESPACE },
      spec,
    })
    return syncedFromCR(created)
  },

  async deleteSynced(name: string, namespace: string): Promise<void> {
    await graphqlQuery(
      `mutation($n: String!, $ns: String!) { ${GROUP_FIELD} { ${VERSION} { deleteSyncedSecret(name: $n, namespace: $ns) } } }`,
      { n: name, ns: namespace },
    )
  },
}
