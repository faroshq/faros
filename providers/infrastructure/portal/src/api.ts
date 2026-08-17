// GraphQL client for the infrastructure provider's portal.
//
// Every read and write goes through the hub's embedded GraphQL gateway at
// /graphql/<cluster> — the same workspace-scoped, caller-authenticated path the
// rest of the platform uses. The shell pushes farosContext.tenant (kcp cluster
// name, used as the /graphql path segment) and farosContext.token (bearer).
//
// Templates and per-template instance CRDs live in the infrastructure group, so
// they surface under the GraphQL field `infrastructure_faros_sh`. Instance
// kinds are declared per Template, so their list field (`<Plural>`) is discovered
// by introspection; reads of an instance's arbitrary spec use the gateway's raw
// `<Kind>Yaml` escape hatch (parsed with js-yaml), and writes use `applyYaml` /
// `delete<Kind>` mutations — no field schema needs to be known ahead of time.

import { load as yamlLoad } from 'js-yaml'
import type { ErrorResponse, Instance, JSONSchema, Template, TemplateCondition, TemplateExposure, TemplateView } from './types'
import { columnsNeedInstanceData } from './view'

const GROUP = 'infrastructure.faros.sh'
const VERSION = 'v1alpha1'
// GraphQL field for the group (dots → underscores, per the gateway's sanitizer).
const GROUP_FIELD = 'infrastructure_faros_sh'

let bearerToken: string | null = null
let clusterName: string | null = null
let contextGeneration = 0
let pageAuthority: object = {}

interface ApiContext {
  token: string | null
  cluster: string
  generation: number | null
  authorityKey: string
}

export interface PortalApiContext {
  token?: string | null
  tenant?: string | null
  // Independently mounted callers can supply an opaque identity object and
  // replace it whenever their tenant/token authority changes. Object identity
  // is collision-free and lets the API cache without retaining credentials.
  authority?: object
}

// The page and dashboard tile share this module but receive context separately.
// Cache by collision-free object identity, represented by an opaque non-secret
// key, and bound the derived cache entries with a small LRU. The WeakMap never
// retains an identity after its owner and in-flight requests release it.
const MAX_CACHED_AUTHORITIES = 8
const authorityKeys = new WeakMap<object, string>()
const authorityUses = new Map<string, number>()
let nextAuthorityID = 0
let authorityUse = 0

function authorityKey(identity: object): string {
  let key = authorityKeys.get(identity)
  if (!key) {
    key = `authority-${++nextAuthorityID}`
    authorityKeys.set(identity, key)
  }
  authorityUses.set(key, ++authorityUse)
  if (authorityUses.size > MAX_CACHED_AUTHORITIES) {
    let oldestKey: string | undefined
    let oldestUse = Number.POSITIVE_INFINITY
    for (const [candidate, usedAt] of authorityUses) {
      if (usedAt < oldestUse) {
        oldestKey = candidate
        oldestUse = usedAt
      }
    }
    if (oldestKey) {
      authorityUses.delete(oldestKey)
      cachedIndexes.delete(oldestKey)
      capabilityByAuthority.delete(oldestKey)
    }
  }
  return key
}

function authorityIsActive(id: string): boolean {
  return authorityUses.has(id)
}

export function setContext(context: PortalApiContext) {
  const nextToken = context.token || null
  const nextCluster = context.tenant || null
  if (nextToken === bearerToken && nextCluster === clusterName) return
  const tenantChanged = nextCluster !== clusterName
  bearerToken = nextToken
  clusterName = nextCluster
  contextGeneration += 1
  pageAuthority = {}
  if (tenantChanged) {
    // eslint-disable-next-line no-console
    console.debug('[infrastructure] tenant clusterName →', nextCluster)
  }
}

// setBasePath is a no-op: the gateway path is built from the cluster name, not
// the provider basePath. Kept so App.vue's watcher type-checks.
export function setBasePath(_ctxBasePath?: string | null) {
  void _ctxBasePath
}
export function setToken(token?: string | null) {
  setContext({ token, tenant: clusterName })
}
export function setTenant(name?: string | null) {
  setContext({ token: bearerToken, tenant: name })
}

function captureContext(explicit?: PortalApiContext): ApiContext {
  const cluster = explicit ? explicit.tenant || null : clusterName
  const token = explicit ? explicit.token || null : bearerToken
  if (!cluster) {
    throw <ErrorResponse>{ reason: 'TenantMissing', message: 'no workspace selected' }
  }
  return {
    token,
    cluster,
    generation: explicit ? null : contextGeneration,
    // Callers without a stable identity deliberately get a request-local cache
    // identity. The page and dashboard provide stable objects that they rotate
    // with authority, so their normal refreshes still reuse discovery data.
    authorityKey: authorityKey(explicit?.authority ?? (explicit ? {} : pageAuthority)),
  }
}

function assertContextCurrent(context: ApiContext): void {
  // Explicit contexts are immutable request snapshots owned by their caller
  // (the independently mounted dashboard tile). Global contexts are owned by
  // App.vue and must still be invalidated on a workspace/token transition.
  if (context.generation !== null &&
    (context.generation !== contextGeneration || context.cluster !== clusterName || context.token !== bearerToken)) {
    throw <ErrorResponse>{ reason: 'ContextChanged', message: 'workspace context changed while the request was in flight' }
  }
}

export function isContextChangedError(error: unknown): boolean {
  return (error as { reason?: string }).reason === 'ContextChanged'
}

function protocolError(message: string): ErrorResponse {
  return { reason: 'ProtocolError', message }
}

function isGraphQLResourceNotFound(error: unknown, resource: string, name: string): boolean {
  const value = error as { reason?: string; message?: string }
  return value.reason === 'GraphQLError' &&
    value.message === `${resource}.${GROUP} "${name}" not found`
}

// ── GraphQL transport ───────────────────────────────────────────────────────
// graphqlQuery POSTs a query/mutation to /graphql/<cluster> and returns data,
// mapping gateway errors onto the {reason,message} contract the views branch on.
async function graphqlQuery<T>(context: ApiContext, query: string, variables: Record<string, unknown> = {}): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json', Accept: 'application/json' }
  if (context.token) headers['Authorization'] = 'Bearer ' + context.token
  const res = await fetch('/graphql/' + context.cluster, {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: JSON.stringify({ query, variables }),
  })
  const text = await res.text()
  assertContextCurrent(context)
  if (!res.ok) {
    throw <ErrorResponse>{ reason: 'HTTPError', message: `${res.status}: ${text || res.statusText}` }
  }
  let parsed: unknown
  try {
    parsed = text ? JSON.parse(text) : null
  } catch {
    throw protocolError('GraphQL gateway returned malformed JSON')
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw protocolError('GraphQL gateway returned an invalid response envelope')
  }
  const body = parsed as { data?: T; errors?: unknown }
  if ('errors' in body) {
    if (!Array.isArray(body.errors)) {
      throw protocolError('GraphQL gateway response included a malformed errors field')
    }
    if (body.errors.length) {
      const messages = body.errors.map(error => {
        if (!error || typeof error !== 'object' || Array.isArray(error) || typeof (error as { message?: unknown }).message !== 'string') {
          throw protocolError('GraphQL gateway response included a malformed error entry')
        }
        return (error as { message: string }).message
      })
      const message = messages.join('; ')
      const reason = /apibinding|no matches for kind/i.test(message) ? 'APIBindingMissing' : 'GraphQLError'
      throw <ErrorResponse>{ reason, message }
    }
  }
  if (!body.data || typeof body.data !== 'object' || Array.isArray(body.data)) {
    throw protocolError('GraphQL gateway response did not include an object data field')
  }
  return body.data
}

// applyCR applies a manifest (create-or-update) via the gateway's applyYaml and
// returns the resulting object (applyYaml serialises it as a JSON string).
async function applyCR(context: ApiContext, manifest: Record<string, unknown>): Promise<RawObject> {
  const data = await graphqlQuery<{ applyYaml?: unknown }>(
    context,
    'mutation($y: String!) { applyYaml(yaml: $y) }',
    { y: JSON.stringify(manifest) },
  )
  const raw = data.applyYaml
  if (raw === undefined || raw === null) throw protocolError('applyYaml response was missing')
  try {
    const object = typeof raw === 'string' ? JSON.parse(raw) : raw
    if (!object || typeof object !== 'object' || Array.isArray(object)) {
      throw protocolError('applyYaml returned an invalid resource')
    }
    const resource = object as RawObject
    const expectedMetadata = manifest.metadata
    if (!expectedMetadata || typeof expectedMetadata !== 'object' || Array.isArray(expectedMetadata) ||
      resource.apiVersion !== manifest.apiVersion || resource.kind !== manifest.kind ||
      resource.metadata?.name !== (expectedMetadata as { name?: unknown }).name) {
      throw protocolError('applyYaml returned a different resource than requested')
    }
    return resource
  } catch (error) {
    if ((error as ErrorResponse).reason === 'ProtocolError') throw error
    throw protocolError('applyYaml returned malformed JSON')
  }
}

// Infra<V> shapes a gateway response nested under the infra group/version. The
// literal keys match GROUP_FIELD / VERSION, which are literal-typed consts, so
// `data[GROUP_FIELD]?.[VERSION]` indexes cleanly.
type Infra<V> = { infrastructure_faros_sh?: { v1alpha1?: V } }

interface RawObject {
  apiVersion?: string
  kind?: string
  metadata?: {
    uid?: string
    name?: string
    namespace?: string
    creationTimestamp?: string
    generation?: number
    labels?: Record<string, string>
  }
  spec?: Record<string, unknown>
  // status carries the well-known phase/message/conditions plus any
  // controller-computed output fields (url, fqdn, …) a template's View may
  // reference — hence the open-ended index signature.
  status?: {
    phase?: string
    message?: string
    conditions?: Array<{ type: string; status: string; observedGeneration?: number; reason?: string; message?: string; lastTransitionTime?: string }> | null
    [k: string]: unknown
  } | null
}

// ── Mappers ─────────────────────────────────────────────────────────────────
function parseOptionalObject<T extends Record<string, unknown>>(value: unknown, field: string): T | undefined {
  if (value === undefined || value === null || value === '') return undefined
  let parsed = value
  if (typeof value === 'string') {
    try {
      parsed = JSON.parse(value)
    } catch {
      throw protocolError(`Template ${field} contained malformed JSON`)
    }
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw protocolError(`Template ${field} was not an object`)
  }
  return parsed as T
}

function parseOptionalPresentationObject<T extends Record<string, unknown>>(value: unknown, field: string): T | undefined {
  try {
    return parseOptionalObject<T>(value, field)
  } catch (error) {
    // Presentation metadata is deliberately non-authoritative. A malformed
    // optional view/default payload falls back to the standard rendering; the
    // resource/envelope itself is still validated strictly everywhere else.
    if ((error as ErrorResponse).reason === 'ProtocolError') return undefined
    throw error
  }
}

interface RawTemplateStatus {
  observedGeneration?: unknown
  backend?: unknown
  conditions?: unknown
}

function templateFromGQL(
  metadata: { name: string; generation?: unknown },
  spec: Record<string, unknown>,
  rawStatus?: RawTemplateStatus | null,
): Template {
  const name = metadata.name
  if (!name) throw protocolError('Template item was missing metadata.name')
  if (!spec || typeof spec !== 'object' || Array.isArray(spec)) {
    throw protocolError(`Template ${name} was missing an object spec`)
  }
  if (spec.instanceCRD !== undefined &&
    (!spec.instanceCRD || typeof spec.instanceCRD !== 'object' || Array.isArray(spec.instanceCRD))) {
    throw protocolError(`Template ${name} instanceCRD was not an object`)
  }
  const instanceCRD = (spec.instanceCRD ?? {}) as { kind?: string; resource?: string }
  if (typeof instanceCRD.kind !== 'string' || !instanceCRD.kind) {
    throw protocolError(`Template ${name} was missing instanceCRD.kind`)
  }
  if (typeof instanceCRD.resource !== 'string' || !instanceCRD.resource) {
    throw protocolError(`Template ${name} was missing instanceCRD.resource`)
  }
  // spec.schema is a preserve-unknown-fields field → the gateway returns it as a
  // JSON string (JSONString scalar); parse it back into the JSONSchema object.
  const inputsSchema = parseOptionalObject<JSONSchema & Record<string, unknown>>(spec.schema, 'schema')
  if (!inputsSchema) throw protocolError(`Template ${name} was missing schema`)
  // sampleValues is a preserve-unknown-fields field too → same JSONString
  // treatment as schema: parse the string form, accept an object as-is.
  const sampleValues = parseOptionalPresentationObject<Record<string, unknown>>(spec.sampleValues, 'sampleValues')
  // view is a preserve-unknown-fields field → JSONString from the gateway;
  // same parse-the-string / accept-an-object treatment as schema/sampleValues.
  const view = parseOptionalPresentationObject<TemplateView & Record<string, unknown>>(spec.view, 'view')
  if (metadata.generation !== undefined &&
    (typeof metadata.generation !== 'number' || !Number.isSafeInteger(metadata.generation) || metadata.generation < 0)) {
    throw protocolError(`Template ${name} metadata.generation had an invalid shape`)
  }
  if (rawStatus != null && (typeof rawStatus !== 'object' || Array.isArray(rawStatus))) {
    throw protocolError(`Template ${name} status was not an object`)
  }
  const status = rawStatus ?? undefined
  if (status?.observedGeneration != null &&
    (typeof status.observedGeneration !== 'number' || !Number.isSafeInteger(status.observedGeneration) || status.observedGeneration < 0)) {
    throw protocolError(`Template ${name} status.observedGeneration had an invalid shape`)
  }
  if (status?.conditions != null && !Array.isArray(status.conditions)) {
    throw protocolError(`Template ${name} status.conditions was not an array`)
  }
  const conditions: TemplateCondition[] = (status?.conditions ?? []).map((condition, index) => {
    if (!condition || typeof condition !== 'object' || Array.isArray(condition) ||
      typeof condition.type !== 'string' || typeof condition.status !== 'string') {
      throw protocolError(`Template ${name} condition ${index} had an invalid shape`)
    }
    const c = condition as Record<string, unknown>
    if (c.observedGeneration != null &&
      (typeof c.observedGeneration !== 'number' || !Number.isSafeInteger(c.observedGeneration) || c.observedGeneration < 0)) {
      throw protocolError(`Template ${name} condition ${index} observedGeneration had an invalid shape`)
    }
    return {
      type: c.type as string,
      status: c.status as string,
      observedGeneration: typeof c.observedGeneration === 'number' ? c.observedGeneration : undefined,
      reason: typeof c.reason === 'string' ? c.reason : undefined,
      message: typeof c.message === 'string' ? c.message : undefined,
      time: typeof c.lastTransitionTime === 'string' ? c.lastTransitionTime : undefined,
    }
  })
  const backend = status?.backend
  if (backend != null && (typeof backend !== 'object' || Array.isArray(backend))) {
    throw protocolError(`Template ${name} status.backend was not an object`)
  }
  const backendStatus = backend as { ready?: unknown; message?: unknown } | undefined
  if (backendStatus?.ready != null && typeof backendStatus.ready !== 'boolean') {
    throw protocolError(`Template ${name} status.backend.ready had an invalid shape`)
  }
  if (backendStatus?.message != null && typeof backendStatus.message !== 'string') {
    throw protocolError(`Template ${name} status.backend.message had an invalid shape`)
  }
  const generation = metadata.generation as number | undefined
  const observedGeneration = typeof status?.observedGeneration === 'number' ? status.observedGeneration : undefined
  const readyCondition = conditions.find(condition => condition.type === 'Ready')
  const readyObservedGeneration = readyCondition?.observedGeneration ?? observedGeneration
  const current = generation === undefined ||
    (readyObservedGeneration !== undefined && readyObservedGeneration >= generation)
  const ready = current && readyCondition?.status === 'True' && backendStatus?.ready === true
  const readinessMessage = ready
    ? undefined
    : !current && generation !== undefined
      ? `Waiting for the template controller to observe generation ${generation}.`
      : readyCondition?.message ||
        (typeof backendStatus?.message === 'string' ? backendStatus.message : undefined) ||
        readyCondition?.reason ||
        'Template setup is still in progress.'
  return {
    name,
    displayName: (spec.displayName as string) || name,
    description: (spec.description as string) ?? '',
    ready,
    readinessMessage,
    generation,
    observedGeneration,
    conditions,
    category: spec.category as string | undefined,
    cloud: spec.cloud as string | undefined,
    exposure: spec.exposure as TemplateExposure | undefined,
    version: spec.version as string | undefined,
    iconURL: spec.iconURL as string | undefined,
    kind: instanceCRD.kind ?? '',
    resource: instanceCRD.resource,
    inputsSchema,
    sampleValues,
    view,
  }
}

// instanceFromObj collapses a per-template CR (any object with metadata/spec/
// status) into the Instance shape the views read. The originating Template is
// taken from the faros.sh/template label, falling back to the kind.
function instanceFromObj(c: RawObject, templateByKind: Map<string, string>): Instance {
  if (!c || typeof c !== 'object' || Array.isArray(c) || !c.metadata || typeof c.metadata !== 'object' || !c.metadata.name) {
    throw protocolError('Instance item was missing metadata.name')
  }
  if (c.status != null && (typeof c.status !== 'object' || Array.isArray(c.status))) {
    throw protocolError(`Instance ${c.metadata.name} status was not an object`)
  }
  if (c.status?.conditions != null && !Array.isArray(c.status.conditions)) {
    throw protocolError(`Instance ${c.metadata.name} conditions was not an array`)
  }
  const labels = c.metadata.labels ?? {}
  const tmpl = labels['faros.sh/template'] || (c.kind ? templateByKind.get(c.kind) ?? c.kind : '')
  const conditions = (c.status?.conditions ?? []).map((condition, index) => {
    if (!condition || typeof condition !== 'object' ||
      typeof condition.type !== 'string' || typeof condition.status !== 'string') {
      throw protocolError(`Instance ${c.metadata!.name} condition ${index} had an invalid shape`)
    }
    return {
      type: condition.type,
      status: condition.status,
      observedGeneration: typeof condition.observedGeneration === 'number'
        ? condition.observedGeneration
        : undefined,
      reason: condition.reason,
      message: condition.message,
      time: condition.lastTransitionTime,
    }
  })
  // status outputs: everything under .status except the conditions/children
  // arrays (promoted to their own fields), so a View can reference status.*.
  let status: Record<string, unknown> | undefined
  if (c.status && typeof c.status === 'object') {
    const { conditions: _c, children: _ch, ...rest } = c.status as Record<string, unknown>
    void _c
    void _ch
    if (Object.keys(rest).length > 0) status = rest
  }
  const generation = c.metadata?.generation
  // Dynamic instance CRDs guarantee observedGeneration on condition entries,
  // not at the top level of status. Ready is the authority for the aggregate
  // phase; an unrelated current condition must not mask a stale Ready. If
  // either side is absent, freshness is unknown rather than stale, so preserve
  // the controller-reported phase.
  const observedGeneration = conditions.find(condition => condition.type === 'Ready')?.observedGeneration
  const reconciled = generation === undefined || observedGeneration === undefined || observedGeneration >= generation
  const reportedPhase = c.status?.phase || (conditions.find(x => x.type === 'Ready')?.status === 'True' ? 'Ready' : 'Pending')
  return {
    uid: c.metadata?.uid,
    name: c.metadata.name,
    namespace: c.metadata.namespace ?? '',
    template: tmpl,
    phase: reconciled ? reportedPhase : 'Pending',
    message: c.status?.message || (!reconciled && generation !== undefined
      ? `Waiting for the controller to observe generation ${generation}.`
      : undefined),
    conditions,
    values: c.spec,
    status,
    createdAt: c.metadata.creationTimestamp ?? '',
    generation,
    observedGeneration,
  }
}

// ── Template + instance-field index ─────────────────────────────────────────
// Listing instances needs each kind's GraphQL list field (`<Plural>` =
// Pluralize(Kind), not derivable client-side), so we discover it by introspection
// once and cache it alongside the Templates (10s TTL — both change rarely).
interface InfraIndex {
  fetchedAt: number
  templates: Template[]
  templateByKind: Map<string, string>
  // kind → GraphQL list field name (only kinds whose CRD is actually established
  // in the workspace, so a Template with no bound CRD is naturally skipped).
  listFieldByKind: Map<string, string>
}
// Both the independently mounted provider page and dashboard tile use this
// module. Cache discovery by the complete authority identity so staggered host
// context delivery cannot reuse another workspace/caller's Templates.
const cachedIndexes = new Map<string, InfraIndex>()
const INDEX_TTL_MS = 10_000

// introspectVersionFields walks Query → infrastructure_faros_sh → v1alpha1
// in a single introspection query and returns its fields with (unwrapped) type
// names, so we can map each instance kind to its list field.
async function introspectVersionFields(context: ApiContext): Promise<Array<{ name: string; typeName: string }>> {
  const q = `{ __type(name: "Query") { fields { name type { fields { name type { name fields { name type { name kind ofType { name kind } } } } } } } } }`
  const data = await graphqlQuery<{
    __type?: { fields?: Array<{ name: string; type?: { fields?: Array<{ name: string; type?: { fields?: Array<{ name: string; type?: { name?: string; ofType?: { name?: string } } }> } }> } }> }
  }>(context, q)
  const group = (data.__type?.fields ?? []).find(f => f.name === GROUP_FIELD)
  const version = (group?.type?.fields ?? []).find(f => f.name === VERSION)
  if (!group || !version || !Array.isArray(version.type?.fields)) {
    throw protocolError('GraphQL introspection did not expose the infrastructure API version')
  }
  return version.type.fields.map(f => ({
    name: f.name,
    typeName: f.type?.ofType?.name ?? f.type?.name ?? '',
  }))
}

// sampleValues is a recent Template field. A gateway whose schema was built from
// an older CRD that predates it has no such field, and selecting an absent field
// is a hard GraphQL error that would break the whole catalog/provision query. So
// select it optimistically and, on that specific error, remember it's missing and
// retry without it (degrading to no form pre-fill). null = not yet probed.
interface CapabilityState {
  sampleValuesSupported: boolean | null
  viewSupported: boolean | null
  exposureSupported: boolean | null
  instanceConditionObservedGenerationUnsupported: Set<string>
}
const capabilityByAuthority = new Map<string, CapabilityState>()

function capabilities(context: ApiContext): CapabilityState {
  let state = capabilityByAuthority.get(context.authorityKey)
  if (!state) {
    state = {
      sampleValuesSupported: null,
      viewSupported: null,
      exposureSupported: null,
      instanceConditionObservedGenerationUnsupported: new Set(),
    }
    capabilityByAuthority.set(context.authorityKey, state)
  }
  return state
}

// templateSpec is the shared Template spec selection set. sampleValues/view/
// exposure are omitted once we've learned the gateway doesn't expose them.
function templateSpec(context: ApiContext): string {
  const state = capabilities(context)
  const sv = state.sampleValuesSupported === false ? '' : ' sampleValues'
  const vw = state.viewSupported === false ? '' : ' view'
  const ex = state.exposureSupported === false ? '' : ' exposure'
  return `displayName description category version iconURL backend instanceCRD { group version resource kind } schema${sv}${vw}${ex}`
}

// templateQuery runs a Template query built from templateSpec(), retrying when
// the gateway rejects an optional field (older CRD) by remembering it's missing
// and rebuilding the selection without it. Loops so a gateway missing both
// sampleValues and view degrades in two passes rather than failing.
async function templateQuery<T>(context: ApiContext, make: (spec: string) => string, variables: Record<string, unknown> = {}): Promise<T> {
  const state = capabilities(context)
  for (;;) {
    try {
      return await graphqlQuery<T>(context, make(templateSpec(context)), variables)
    } catch (e) {
      const error = e as { reason?: string; message?: string }
      const msg = error.message ?? ''
      // Capability downgrade is valid only for GraphQL schema validation.
      // HTTP/protocol/authorization failures can contain the same words and
      // must retain their original authority instead of being retried away.
      if (error.reason === 'GraphQLError' && state.sampleValuesSupported !== false && /Cannot query field ["']sampleValues["']/i.test(msg)) {
        state.sampleValuesSupported = false
        continue
      }
      if (error.reason === 'GraphQLError' && state.viewSupported !== false && /Cannot query field ["']view["']/i.test(msg)) {
        state.viewSupported = false
        continue
      }
      if (error.reason === 'GraphQLError' && state.exposureSupported !== false && /Cannot query field ["']exposure["']/i.test(msg)) {
        state.exposureSupported = false
        continue
      }
      throw e
    }
  }
}

async function refreshIndex(context: ApiContext): Promise<InfraIndex> {
  const [tmplData, versionFields] = await Promise.all([
    templateQuery<Infra<{ Templates?: { items?: Array<{ metadata: { name: string; generation?: number }; spec: Record<string, unknown>; status?: RawTemplateStatus | null }> } }>>(
      context,
      spec => `{ ${GROUP_FIELD} { ${VERSION} { Templates { items { metadata { name generation } spec { ${spec} } status { observedGeneration backend { ready message } conditions { type status observedGeneration reason message lastTransitionTime } } } } } } }`,
    ),
    introspectVersionFields(context),
  ])

  const templateList = tmplData[GROUP_FIELD]?.[VERSION]?.Templates
  if (!templateList || !Array.isArray(templateList.items)) {
    throw protocolError('Template list response was missing its items array')
  }
  const items = templateList.items
  const templates = items.map((item, index) => {
    if (!item || typeof item !== 'object' || Array.isArray(item) ||
      !item.metadata || typeof item.metadata !== 'object' || typeof item.metadata.name !== 'string' ||
      !item.spec || typeof item.spec !== 'object' || Array.isArray(item.spec)) {
      throw protocolError(`Template list item ${index} had an invalid shape`)
    }
    return templateFromGQL(item.metadata, item.spec, item.status)
  })
  const templateByKind = new Map<string, string>()
  for (const t of templates) if (t.kind) templateByKind.set(t.kind, t.name)

  // Map kind → list field via the resource-type relationship: the list field's
  // type is `<resourceType>List`, the single field's type is `<resourceType>`.
  const listByResourceType = new Map<string, string>()
  const resourceTypeByKind = new Map<string, string>()
  for (const f of versionFields) {
    if (!f.typeName) continue
    if (f.typeName.endsWith('List')) listByResourceType.set(f.typeName.slice(0, -'List'.length), f.name)
    else if (f.typeName === 'String') continue // <Kind>Yaml fields
    else resourceTypeByKind.set(f.name, f.typeName) // single field: name === Kind
  }
  // Only map kinds that are actual Template instances — the schema also exposes
  // Template (and other) resources whose status has no phase/message, and which
  // must not be swept into the instance list.
  const instanceKinds = new Set(templates.map(t => t.kind).filter(Boolean))
  const listFieldByKind = new Map<string, string>()
  for (const [kind, resourceType] of resourceTypeByKind) {
    if (!instanceKinds.has(kind)) continue
    const lf = listByResourceType.get(resourceType)
    if (lf) listFieldByKind.set(kind, lf)
  }

  assertContextCurrent(context)
  const index = { fetchedAt: Date.now(), templates, templateByKind, listFieldByKind }
  // An explicit tile request may finish after its authority was LRU-evicted by
  // newer host contexts. Return its result to that caller without resurrecting
  // an evicted cache entry.
  if (authorityIsActive(context.authorityKey)) cachedIndexes.set(context.authorityKey, index)
  return index
}

async function getIndex(context: ApiContext, force = false): Promise<InfraIndex> {
  const cached = cachedIndexes.get(context.authorityKey)
  if (!force && cached && Date.now() - cached.fetchedAt < INDEX_TTL_MS) return cached
  return refreshIndex(context)
}

// Build the wire manifest for a per-template instance CR. The kind/apiVersion
// come from the Template's instanceCRD (all instances live in the infra group);
// the input `values` go under .spec verbatim.
function buildInstanceManifest(kind: string, name: string, templateName: string, values: Record<string, unknown>) {
  return {
    apiVersion: GROUP + '/' + VERSION,
    kind,
    metadata: { name, labels: { 'faros.sh/template': templateName } },
    spec: values,
  }
}

function versionPayload<V extends object>(data: Infra<V>, operation: string): V {
  const group = data[GROUP_FIELD]
  const version = group?.[VERSION]
  if (!version || typeof version !== 'object' || Array.isArray(version)) {
    throw protocolError(`${operation} response was missing the infrastructure API version`)
  }
  return version
}

function parseYamlResource(text: string, operation: string): RawObject {
  try {
    const object = yamlLoad(text)
    if (!object || typeof object !== 'object' || Array.isArray(object)) {
      throw protocolError(`${operation} returned an invalid YAML resource`)
    }
    return object as RawObject
  } catch (error) {
    if ((error as ErrorResponse).reason === 'ProtocolError') throw error
    throw protocolError(`${operation} returned malformed YAML`)
  }
}

async function getInstanceInContext(
  context: ApiContext,
  name: string,
  idx: InfraIndex,
  templateName?: string,
): Promise<Instance> {
  const requestedTemplate = templateName
    ? idx.templates.find(template => template.name === templateName)
    : undefined
  if (templateName && !requestedTemplate) {
    throw <ErrorResponse>{ reason: 'TemplateNotFound', message: 'template ' + templateName + ' not found' }
  }
  if (requestedTemplate && !requestedTemplate.kind) {
    throw <ErrorResponse>{
      reason: 'ProviderNotReady',
      message: `instance API for template ${templateName} is not established yet`,
    }
  }
  if (requestedTemplate?.kind && !idx.listFieldByKind.has(requestedTemplate.kind)) {
    throw <ErrorResponse>{ reason: 'ProviderNotReady', message: `instance API for template ${templateName} is not established yet` }
  }
  const kinds = requestedTemplate?.kind ? [requestedTemplate.kind] : [...idx.listFieldByKind.keys()]
  const probes = kinds.map(async kind => {
    const resource = idx.templates.find(template => template.kind === kind)?.resource
    if (!resource) throw protocolError(`${kind} read could not resolve its resource`)
    try {
      const data = await graphqlQuery<Infra<Record<string, string>>>(
        context,
        `query($n: String!) { ${GROUP_FIELD} { ${VERSION} { ${kind}Yaml(name: $n) } } }`,
        { n: name },
      )
      const version = versionPayload(data, `${kind} read`)
      const field = kind + 'Yaml'
      if (!(field in version)) throw protocolError(`${kind} read response was missing ${field}`)
      const text = version[field]
      if (typeof text !== 'string' || !text) throw protocolError(`${kind} read returned invalid YAML data`)
      return parseYamlResource(text, `${kind} read`)
    } catch (error) {
      // The gateway surfaces Kubernetes GET misses as GraphQL errors instead
      // of a nullable <Kind>Yaml result. A miss is expected while probing the
      // other dynamic kinds; only transport, schema, and protocol errors abort
      // the aggregate lookup.
      if (isGraphQLResourceNotFound(error, resource, name)) return null
      throw error
    }
  })
  const found = (await Promise.all(probes)).filter((item): item is RawObject => item !== null)
  assertContextCurrent(context)
  if (found.length === 0) {
    const unprobeableTemplate = !requestedTemplate && idx.templates.some(template =>
      !!template.kind && !idx.listFieldByKind.has(template.kind),
    )
    if (unprobeableTemplate) {
      throw <ErrorResponse>{
        reason: 'ProviderNotReady',
        message: 'not all instance APIs are established in this workspace yet',
      }
    }
    throw <ErrorResponse>{ reason: 'InstanceNotFound', message: 'instance ' + name + ' not found' }
  }
  if (found.length > 1) {
    throw <ErrorResponse>{
      reason: 'InstanceAmbiguous',
      message: `instance name ${name} exists under more than one template; open it from the instance list`,
    }
  }
  const raw = found[0]
  if (typeof raw.kind !== 'string' || !raw.kind) {
    throw protocolError(`${requestedTemplate?.kind ?? 'instance'} read returned a resource without kind`)
  }
  const instance = instanceFromObj(raw, idx.templateByKind)
  const labeledTemplate = idx.templates.find(template => template.name === instance.template)
  if (!labeledTemplate || labeledTemplate.kind !== raw.kind) {
    throw protocolError(`instance ${name} template identity did not match its resource kind`)
  }
  if (requestedTemplate && instance.template !== requestedTemplate.name) {
    // A kind can be shared by multiple templates. The targeted route is an
    // identity tuple, so a CR labeled for a sibling template is absent here
    // and must never be displayed or deleted through this route.
    throw <ErrorResponse>{ reason: 'InstanceNotFound', message: `instance ${name} not found for template ${templateName}` }
  }
  return instance
}

export const api = {
  async listTemplates(filter: { category?: string; cloud?: string } = {}, explicitContext?: PortalApiContext): Promise<{ items: Template[] }> {
    const context = captureContext(explicitContext)
    const idx = await refreshIndex(context)
    let items = idx.templates
    if (filter.category) items = items.filter(t => t.category === filter.category)
    if (filter.cloud) items = items.filter(t => t.cloud === filter.cloud)
    return { items }
  },

  async getTemplate(name: string, explicitContext?: PortalApiContext): Promise<{ template: Template }> {
    const context = captureContext(explicitContext)
    const data = await templateQuery<Infra<{ Template?: { metadata: { name: string; generation?: number }; spec: Record<string, unknown>; status?: RawTemplateStatus | null } }>>(
      context,
      spec => `query($n: String!) { ${GROUP_FIELD} { ${VERSION} { Template(name: $n) { metadata { name generation } spec { ${spec} } status { observedGeneration backend { ready message } conditions { type status observedGeneration reason message lastTransitionTime } } } } } }`,
      { n: name },
    )
    const version = versionPayload(data, 'Template read')
    if (!('Template' in version)) throw protocolError('Template read response was missing Template')
    const t = version.Template
    if (!t) throw <ErrorResponse>{ reason: 'TemplateNotFound', message: 'template ' + name + ' not found' }
    if (!t.metadata || typeof t.metadata !== 'object' || typeof t.metadata.name !== 'string' ||
      !t.spec || typeof t.spec !== 'object' || Array.isArray(t.spec)) {
      throw protocolError('Template read returned an invalid resource shape')
    }
    return { template: templateFromGQL(t.metadata, t.spec, t.status) }
  },

  async createInstance(body: {
    templateName: string
    templateVersion?: string
    name: string
    values: Record<string, unknown>
  }): Promise<Instance> {
    const context = captureContext()
    // Re-read Template readiness at the write boundary. The catalog index is
    // deliberately cached for reads, but an unavailable backend must never be
    // provisioned from a stale Ready snapshot.
    const idx = await getIndex(context, true)
    const tmpl = idx.templates.find(t => t.name === body.templateName)
    if (!tmpl || !tmpl.kind) {
      throw <ErrorResponse>{ reason: 'TemplateNotFound', message: 'template ' + body.templateName + ' not found' }
    }
    if (!tmpl.ready) {
      throw <ErrorResponse>{
        reason: 'TemplateNotReady',
        message: tmpl.readinessMessage || `template ${body.templateName} is not ready`,
      }
    }
    const manifest = buildInstanceManifest(tmpl.kind, body.name, body.templateName, body.values)
    const created = await applyCR(context, manifest)
    assertContextCurrent(context)
    return instanceFromObj(created, idx.templateByKind)
  },

  async listInstances(explicitContext?: PortalApiContext): Promise<{ items: Instance[]; templates: Template[] }> {
    const context = captureContext(explicitContext)
    const idx = await getIndex(context)
    const kinds = [...idx.listFieldByKind.keys()]
    if (kinds.length === 0) {
      if (idx.templates.some(template => !!template.kind)) {
        throw <ErrorResponse>{ reason: 'ProviderNotReady', message: 'instance APIs are not established in this workspace yet' }
      }
      return { items: [], templates: idx.templates }
    }
    // One LIST per established kind, in parallel. metadata + status only — the
    // list view never needs the (arbitrary) spec, so we don't select it.
    const lists = await Promise.all(
      kinds.map(async kind => {
        const field = idx.listFieldByKind.get(kind)!
        const state = capabilities(context)
        const query = (includeObservedGeneration: boolean) => {
          const observedGeneration = includeObservedGeneration ? ' observedGeneration' : ''
          return `{ ${GROUP_FIELD} { ${VERSION} { ${field} { items { metadata { uid name namespace creationTimestamp generation labels } status { phase message conditions { type status${observedGeneration} reason message lastTransitionTime } } } } } } } }`
        }
        let data: Infra<Record<string, { items?: RawObject[] }>>
        const includeObservedGeneration = !state.instanceConditionObservedGenerationUnsupported.has(kind)
        try {
          data = await graphqlQuery<Infra<Record<string, { items?: RawObject[] }>>>(
            context,
            query(includeObservedGeneration),
          )
        } catch (error) {
          const gqlError = error as { reason?: string; message?: string }
          const message = gqlError.message ?? ''
          if (!includeObservedGeneration || gqlError.reason !== 'GraphQLError' ||
            !/Cannot query field ["']observedGeneration["'] on type ["'][^"']+["']/i.test(message)) {
            throw error
          }
          // APIResourceSchemas are immutable and tenant APIBindings may still
          // expose a prior per-kind schema. Retry only this kind without the
          // optional freshness field; all other condition data remains useful.
          state.instanceConditionObservedGenerationUnsupported.add(kind)
          data = await graphqlQuery<Infra<Record<string, { items?: RawObject[] }>>>(context, query(false))
        }
        const list = versionPayload(data, `${kind} list`)[field]
        if (!list || !Array.isArray(list.items)) {
          throw protocolError(`${kind} list response was missing its items array`)
        }
        return list.items
      }),
    )
    const items = lists.flat().map(c => instanceFromObj(c, idx.templateByKind))
    // Enrich instances whose template defines columns referencing spec.*/status.*
    // — the LIST above selects only metadata + status phase/conditions, so fetch
    // the full object (incl. arbitrary spec/status) via the <Kind>Yaml escape
    // hatch for just those instances. Runs in parallel; disappearance between
    // reads leaves the cell empty, while transport/protocol failures keep the
    // previous table snapshot stale rather than presenting partial data.
    await Promise.all(
      items.map(async i => {
        const tmpl = idx.templates.find(t => t.name === i.template)
        if (!tmpl?.kind || !columnsNeedInstanceData(tmpl.view)) return
        let data: Infra<Record<string, string | null>>
        try {
          data = await graphqlQuery<Infra<Record<string, string | null>>>(
            context,
            `query($n: String!) { ${GROUP_FIELD} { ${VERSION} { ${tmpl.kind}Yaml(name: $n) } } }`,
            { n: i.name },
          )
        } catch (error) {
          // The object can disappear between LIST and enrichment GET. The
          // gateway surfaces that Kubernetes GET miss as a GraphQL error rather
          // than a nullable Yaml field; only the exact miss is benign.
          if (isGraphQLResourceNotFound(error, tmpl.resource, i.name)) return
          throw error
        }
        const version = versionPayload(data, `${tmpl.kind} enrichment`)
        const field = tmpl.kind + 'Yaml'
        if (!(field in version)) throw protocolError(`${tmpl.kind} enrichment response was missing ${field}`)
        const text = version[field]
        if (text === null) return // resource disappeared after the list snapshot
        if (typeof text !== 'string' || !text) throw protocolError(`${tmpl.kind} enrichment returned invalid YAML data`)
        const full = instanceFromObj(parseYamlResource(text, `${tmpl.kind} enrichment`), idx.templateByKind)
        i.values = full.values
        i.status = full.status
      }),
    )
    assertContextCurrent(context)
    return { items, templates: idx.templates }
  },

  async getInstance(name: string, templateName?: string): Promise<Instance> {
    const context = captureContext()
    const idx = await getIndex(context)
    return getInstanceInContext(context, name, idx, templateName)
  },

  async getInstanceDetail(name: string, templateName?: string): Promise<{ instance: Instance; template?: Template }> {
    const context = captureContext()
    const idx = await getIndex(context)
    const instance = await getInstanceInContext(context, name, idx, templateName)
    return { instance, template: idx.templates.find(template => template.name === instance.template) }
  },

  async deleteInstance(name: string, templateName?: string): Promise<void> {
    const context = captureContext()
    const idx = await getIndex(context)
    // Resolve which kind the CR is, then delete<Kind>.
    let inst: Instance
    try {
      inst = await getInstanceInContext(context, name, idx, templateName)
    } catch (error) {
      // The object may already be gone, including after a successful delete
      // whose response was lost. Exact absence makes retry safe; all other
      // discovery errors retain their original authority and propagate.
      if ((error as ErrorResponse).reason === 'InstanceNotFound') return
      throw error
    }
    const kind = idx.templates.find(t => t.name === inst.template)?.kind
    if (!kind) {
      throw <ErrorResponse>{ reason: 'InstanceNotFound', message: 'cannot resolve kind for ' + name }
    }
    let data: Infra<Record<string, unknown>>
    try {
      data = await graphqlQuery<Infra<Record<string, unknown>>>(
        context,
        `mutation($n: String!) { ${GROUP_FIELD} { ${VERSION} { delete${kind}(name: $n) } } }`,
        { n: name },
      )
    } catch (error) {
      const resource = idx.templates.find(t => t.kind === kind)?.resource
      if (resource && isGraphQLResourceNotFound(error, resource, name)) return
      throw error
    }
    const version = versionPayload(data, `${kind} delete`)
    if (!(`delete${kind}` in version)) throw protocolError(`${kind} delete response was missing its result`)
    assertContextCurrent(context)
  },
}
