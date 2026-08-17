// GraphQL client for the flattened infrastructure API: a read-only Template
// catalog and one stable Instance kind whose spec.template selects the product.

import { load as yamlLoad } from 'js-yaml'
import type { ErrorResponse, Instance, InstanceChild, JSONSchema, Template, TemplateCondition, TemplateExposure, TemplateView } from './types'
import { columnsNeedInstanceData } from './view'

const GROUP = 'infrastructure.faros.sh'
const VERSION = 'v1alpha1'
const GROUP_FIELD = 'infrastructure_faros_sh'
const MAX_CACHED_AUTHORITIES = 8
const CACHE_TTL_MS = 10_000
const RECENT_CREATE_TTL_MS = 30_000

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
  authority?: object
}

interface TemplateCache {
  fetchedAt: number
  templates: Template[]
}

interface CapabilityState {
  sampleValuesSupported: boolean | null
  viewSupported: boolean | null
  exposureSupported: boolean | null
}

const authorityKeys = new WeakMap<object, string>()
const authorityUses = new Map<string, number>()
const cachedTemplates = new Map<string, TemplateCache>()
const capabilityByAuthority = new Map<string, CapabilityState>()
const recentCreates = new Map<string, { createdAt: number; resource: RawObject }>()
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
      cachedTemplates.delete(oldestKey)
      capabilityByAuthority.delete(oldestKey)
      for (const key of recentCreates.keys()) {
        if (key.startsWith(oldestKey + '\0')) recentCreates.delete(key)
      }
    }
  }
  return key
}

function authorityIsActive(key: string): boolean {
  return authorityUses.has(key)
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

// Compatibility wrappers for callers outside App.vue.
export function setBasePath(_basePath?: string | null) { void _basePath }
export function setToken(token?: string | null) { setContext({ token, tenant: clusterName }) }
export function setTenant(tenant?: string | null) { setContext({ token: bearerToken, tenant }) }

function captureContext(explicit?: PortalApiContext): ApiContext {
  const cluster = explicit ? explicit.tenant || null : clusterName
  const token = explicit ? explicit.token || null : bearerToken
  if (!cluster) throw <ErrorResponse>{ reason: 'TenantMissing', message: 'no workspace selected' }
  return {
    token,
    cluster,
    generation: explicit ? null : contextGeneration,
    authorityKey: authorityKey(explicit?.authority ?? (explicit ? {} : pageAuthority)),
  }
}

function assertContextCurrent(context: ApiContext): void {
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

function recentCreateKey(context: ApiContext, name: string): string {
  return context.authorityKey + '\0' + name
}

function rememberCreate(context: ApiContext, resource: RawObject): void {
  const name = resource.metadata?.name
  if (!name || !authorityIsActive(context.authorityKey)) return
  recentCreates.set(recentCreateKey(context, name), { createdAt: Date.now(), resource })
}

function recentCreate(context: ApiContext, name: string): RawObject | null {
  const key = recentCreateKey(context, name)
  const entry = recentCreates.get(key)
  if (!entry) return null
  if (Date.now() - entry.createdAt >= RECENT_CREATE_TTL_MS) {
    recentCreates.delete(key)
    return null
  }
  return entry.resource
}

function isGraphQLResourceNotFound(error: unknown, name: string): boolean {
  const value = error as { reason?: string; message?: string }
  return value.reason === 'GraphQLError' &&
    value.message === `instances.${GROUP} "${name}" not found`
}

async function graphqlQuery<T>(context: ApiContext, query: string, variables: Record<string, unknown> = {}): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json', Accept: 'application/json' }
  if (context.token) headers.Authorization = 'Bearer ' + context.token
  const response = await fetch('/graphql/' + context.cluster, {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: JSON.stringify({ query, variables }),
  })
  const text = await response.text()
  assertContextCurrent(context)
  if (!response.ok) {
    throw <ErrorResponse>{ reason: 'HTTPError', message: `${response.status}: ${text || response.statusText}` }
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
        if (!error || typeof error !== 'object' || Array.isArray(error) ||
          typeof (error as { message?: unknown }).message !== 'string') {
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

type Infra<V> = { infrastructure_faros_sh?: { v1alpha1?: V } }

interface RawObject {
  apiVersion?: string
  kind?: string
  metadata?: {
    uid?: string
    name?: string
    namespace?: string
    creationTimestamp?: string
    deletionTimestamp?: string
    generation?: number
    labels?: Record<string, string>
  }
  spec?: {
    template?: unknown
    values?: unknown
  }
  status?: unknown
}

interface RawTemplateStatus {
  observedGeneration?: unknown
  backend?: unknown
  conditions?: unknown
}

function versionPayload<V extends object>(data: Infra<V>, operation: string): V {
  const version = data[GROUP_FIELD]?.[VERSION]
  if (!version || typeof version !== 'object' || Array.isArray(version)) {
    throw protocolError(`${operation} response was missing the infrastructure API version`)
  }
  return version
}

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
    const metadata = manifest.metadata
    if (!metadata || typeof metadata !== 'object' || Array.isArray(metadata) ||
      resource.apiVersion !== manifest.apiVersion || resource.kind !== manifest.kind ||
      resource.metadata?.name !== (metadata as { name?: unknown }).name) {
      throw protocolError('applyYaml returned a different resource than requested')
    }
    return resource
  } catch (error) {
    if ((error as ErrorResponse).reason === 'ProtocolError') throw error
    throw protocolError('applyYaml returned malformed JSON')
  }
}

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
    if ((error as ErrorResponse).reason === 'ProtocolError') return undefined
    throw error
  }
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
  const instanceCRD = (spec.instanceCRD ?? {}) as { kind?: string }
  if (typeof instanceCRD.kind !== 'string' || !instanceCRD.kind) {
    throw protocolError(`Template ${name} was missing instanceCRD.kind`)
  }
  const inputsSchema = parseOptionalObject<JSONSchema & Record<string, unknown>>(spec.schema, 'schema')
  if (!inputsSchema) throw protocolError(`Template ${name} was missing schema`)
  const sampleValues = parseOptionalPresentationObject<Record<string, unknown>>(spec.sampleValues, 'sampleValues')
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
  const conditions: TemplateCondition[] = (status?.conditions ?? []).map((raw, index) => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
      throw protocolError(`Template ${name} condition ${index} had an invalid shape`)
    }
    const condition = raw as Record<string, unknown>
    if (typeof condition.type !== 'string' || typeof condition.status !== 'string') {
      throw protocolError(`Template ${name} condition ${index} had an invalid shape`)
    }
    if (condition.observedGeneration != null &&
      (typeof condition.observedGeneration !== 'number' || !Number.isSafeInteger(condition.observedGeneration))) {
      throw protocolError(`Template ${name} condition ${index} observedGeneration had an invalid shape`)
    }
    return {
      type: condition.type,
      status: condition.status,
      observedGeneration: typeof condition.observedGeneration === 'number' ? condition.observedGeneration : undefined,
      reason: typeof condition.reason === 'string' ? condition.reason : undefined,
      message: typeof condition.message === 'string' ? condition.message : undefined,
      time: typeof condition.lastTransitionTime === 'string' ? condition.lastTransitionTime : undefined,
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
    kind: instanceCRD.kind,
    inputsSchema,
    sampleValues,
    view,
  }
}

function instanceFromObj(object: RawObject): Instance {
  if (!object || typeof object !== 'object' || Array.isArray(object) || !object.metadata ||
    typeof object.metadata !== 'object' || typeof object.metadata.name !== 'string' || !object.metadata.name) {
    throw protocolError('Instance item was missing metadata.name')
  }
  const name = object.metadata.name
  if (object.metadata.generation != null &&
    (typeof object.metadata.generation !== 'number' ||
      !Number.isSafeInteger(object.metadata.generation) || object.metadata.generation < 0)) {
    throw protocolError(`Instance ${name} metadata.generation had an invalid shape`)
  }
  if (object.metadata.deletionTimestamp != null &&
    (typeof object.metadata.deletionTimestamp !== 'string' || !object.metadata.deletionTimestamp)) {
    throw protocolError(`Instance ${name} metadata.deletionTimestamp had an invalid shape`)
  }
  if (object.spec != null && (typeof object.spec !== 'object' || Array.isArray(object.spec))) {
    throw protocolError(`Instance ${name} spec was not an object`)
  }
  if (object.status != null && (typeof object.status !== 'object' || Array.isArray(object.status))) {
    throw protocolError(`Instance ${name} status was not an object`)
  }
  const rawStatus = object.status as Record<string, unknown> | undefined
  if (rawStatus?.conditions != null && !Array.isArray(rawStatus.conditions)) {
    throw protocolError(`Instance ${name} conditions was not an array`)
  }
  if (rawStatus?.phase != null && typeof rawStatus.phase !== 'string') {
    throw protocolError(`Instance ${name} status.phase had an invalid shape`)
  }
  if (rawStatus?.message != null && typeof rawStatus.message !== 'string') {
    throw protocolError(`Instance ${name} status.message had an invalid shape`)
  }
  if (rawStatus?.children != null && !Array.isArray(rawStatus.children)) {
    throw protocolError(`Instance ${name} status.children was not an array`)
  }
  const template = typeof object.spec?.template === 'string'
    ? object.spec.template
    : object.metadata.labels?.['faros.sh/template'] || ''
  if (!template) throw protocolError(`Instance ${name} was missing spec.template`)

  let values: Record<string, unknown> | undefined
  const rawValues = object.spec?.values
  if (typeof rawValues === 'string') {
    let parsed: unknown
    try {
      parsed = JSON.parse(rawValues)
    } catch {
      throw protocolError(`Instance ${name} spec.values contained malformed JSON`)
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw protocolError(`Instance ${name} spec.values was not an object`)
    }
    values = parsed as Record<string, unknown>
  } else if (rawValues != null) {
    if (typeof rawValues !== 'object' || Array.isArray(rawValues)) {
      throw protocolError(`Instance ${name} spec.values was not an object`)
    }
    values = rawValues as Record<string, unknown>
  }

  const conditions = ((rawStatus?.conditions ?? []) as unknown[]).map((raw, index) => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
      throw protocolError(`Instance ${name} condition ${index} had an invalid shape`)
    }
    const condition = raw as Record<string, unknown>
    if (typeof condition.type !== 'string' || typeof condition.status !== 'string') {
      throw protocolError(`Instance ${name} condition ${index} had an invalid shape`)
    }
    if (condition.observedGeneration != null &&
      (typeof condition.observedGeneration !== 'number' || !Number.isSafeInteger(condition.observedGeneration))) {
      throw protocolError(`Instance ${name} condition ${index} observedGeneration had an invalid shape`)
    }
    return {
      type: condition.type,
      status: condition.status,
      observedGeneration: typeof condition.observedGeneration === 'number' ? condition.observedGeneration : undefined,
      reason: typeof condition.reason === 'string' ? condition.reason : undefined,
      message: typeof condition.message === 'string' ? condition.message : undefined,
      time: typeof condition.lastTransitionTime === 'string' ? condition.lastTransitionTime : undefined,
    }
  })

  const children: InstanceChild[] = ((rawStatus?.children ?? []) as unknown[]).map((raw, index) => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
      throw protocolError(`Instance ${name} child ${index} had an invalid shape`)
    }
    const child = raw as Record<string, unknown>
    if (typeof child.apiVersion !== 'string' || !child.apiVersion ||
      typeof child.kind !== 'string' || !child.kind ||
      typeof child.name !== 'string' || !child.name ||
      (child.namespace != null && typeof child.namespace !== 'string') ||
      (child.phase != null && typeof child.phase !== 'string')) {
      throw protocolError(`Instance ${name} child ${index} had an invalid shape`)
    }
    return {
      apiVersion: child.apiVersion,
      kind: child.kind,
      name: child.name,
      namespace: typeof child.namespace === 'string' ? child.namespace : undefined,
      phase: typeof child.phase === 'string' ? child.phase : undefined,
    }
  })

  const topObserved = rawStatus?.observedGeneration
  if (topObserved != null &&
    (typeof topObserved !== 'number' || !Number.isSafeInteger(topObserved) || topObserved < 0)) {
    throw protocolError(`Instance ${name} status.observedGeneration had an invalid shape`)
  }
  const generation = object.metadata.generation
  // The flattened Instance controller stamps this field with the tenant
  // Instance's generation. Mirrored runtime conditions carry the generation
  // of a different object and remain useful for display only.
  const observedGeneration = typeof topObserved === 'number' ? topObserved : undefined
  const reconciled = generation === undefined ||
    (observedGeneration !== undefined && observedGeneration >= generation)
  const ready = conditions.find(condition => condition.type === 'Ready')?.status === 'True'
  const reportedPhase = typeof rawStatus?.phase === 'string' ? rawStatus.phase : ready ? 'Ready' : 'Pending'
  const deletionTimestamp = object.metadata.deletionTimestamp

  let status: Record<string, unknown> | undefined
  if (rawStatus) {
    const { conditions: _conditions, children: _children, ...rest } = rawStatus
    void _conditions
    void _children
    if (Object.keys(rest).length) status = rest
  }

  return {
    uid: object.metadata.uid,
    name,
    namespace: object.metadata.namespace ?? '',
    template,
    deletionTimestamp,
    phase: deletionTimestamp ? 'Deleting' : reconciled ? reportedPhase : 'Pending',
    message: deletionTimestamp
      ? 'Deletion is in progress while provisioned resources are cleaned up.'
      : !reconciled && generation !== undefined
      ? `Waiting for the controller to observe generation ${generation}.`
      : typeof rawStatus?.message === 'string' ? rawStatus.message : undefined,
    conditions,
    children,
    values,
    status,
    createdAt: object.metadata.creationTimestamp ?? '',
    generation,
    observedGeneration,
  }
}

function instanceIdentity(object: RawObject): { name: string; uid?: string } {
  if (!object || typeof object !== 'object' || Array.isArray(object) || !object.metadata ||
    typeof object.metadata !== 'object' || typeof object.metadata.name !== 'string' || !object.metadata.name) {
    throw protocolError('Instance item was missing metadata.name')
  }
  return { name: object.metadata.name, uid: object.metadata.uid }
}

function capabilities(context: ApiContext): CapabilityState {
  let state = capabilityByAuthority.get(context.authorityKey)
  if (!state) {
    state = { sampleValuesSupported: null, viewSupported: null, exposureSupported: null }
    capabilityByAuthority.set(context.authorityKey, state)
  }
  return state
}

function templateSpec(context: ApiContext): string {
  const state = capabilities(context)
  const sampleValues = state.sampleValuesSupported === false ? '' : ' sampleValues'
  const view = state.viewSupported === false ? '' : ' view'
  const exposure = state.exposureSupported === false ? '' : ' exposure'
  return `displayName description category version iconURL backend instanceCRD { group version resource kind } schema${sampleValues}${view}${exposure}`
}

async function templateQuery<T>(
  context: ApiContext,
  makeQuery: (spec: string) => string,
  variables: Record<string, unknown> = {},
): Promise<T> {
  const state = capabilities(context)
  for (;;) {
    try {
      return await graphqlQuery<T>(context, makeQuery(templateSpec(context)), variables)
    } catch (error) {
      const value = error as { reason?: string; message?: string }
      const message = value.message ?? ''
      if (value.reason === 'GraphQLError' && state.sampleValuesSupported !== false &&
        /Cannot query field ["']sampleValues["']/i.test(message)) {
        state.sampleValuesSupported = false
        continue
      }
      if (value.reason === 'GraphQLError' && state.viewSupported !== false &&
        /Cannot query field ["']view["']/i.test(message)) {
        state.viewSupported = false
        continue
      }
      if (value.reason === 'GraphQLError' && state.exposureSupported !== false &&
        /Cannot query field ["']exposure["']/i.test(message)) {
        state.exposureSupported = false
        continue
      }
      throw error
    }
  }
}

async function fetchTemplates(context: ApiContext): Promise<Template[]> {
  const data = await templateQuery<Infra<{ Templates?: { items?: Array<{
    metadata: { name: string; generation?: number }
    spec: Record<string, unknown>
    status?: RawTemplateStatus | null
  }> } }>>(
    context,
    spec => `{ ${GROUP_FIELD} { ${VERSION} { Templates { items { metadata { name generation } spec { ${spec} } status { observedGeneration backend { ready message } conditions { type status observedGeneration reason message lastTransitionTime } } } } } } }`,
  )
  const list = versionPayload(data, 'Template list').Templates
  if (!list || !Array.isArray(list.items)) {
    throw protocolError('Template list response was missing its items array')
  }
  const templates = list.items.map((item, index) => {
    if (!item || typeof item !== 'object' || Array.isArray(item) ||
      !item.metadata || typeof item.metadata.name !== 'string' ||
      !item.spec || typeof item.spec !== 'object' || Array.isArray(item.spec)) {
      throw protocolError(`Template list item ${index} had an invalid shape`)
    }
    return templateFromGQL(item.metadata, item.spec, item.status)
  })
  assertContextCurrent(context)
  if (authorityIsActive(context.authorityKey)) {
    cachedTemplates.set(context.authorityKey, { fetchedAt: Date.now(), templates })
  }
  return templates
}

async function getTemplates(context: ApiContext, force = false): Promise<Template[]> {
  const cached = cachedTemplates.get(context.authorityKey)
  if (!force && cached && Date.now() - cached.fetchedAt < CACHE_TTL_MS) return cached.templates
  return fetchTemplates(context)
}

function buildInstanceManifest(name: string, templateName: string, values: Record<string, unknown>) {
  return {
    apiVersion: GROUP + '/' + VERSION,
    kind: 'Instance',
    metadata: { name, labels: { 'faros.sh/template': templateName } },
    spec: { template: templateName, values },
  }
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

async function fetchInstanceYaml(context: ApiContext, name: string): Promise<RawObject | null> {
  try {
    const data = await graphqlQuery<Infra<{ InstanceYaml?: unknown }>>(
      context,
      `query($n: String!) { ${GROUP_FIELD} { ${VERSION} { InstanceYaml(name: $n) } } }`,
      { n: name },
    )
    const version = versionPayload(data, 'Instance read')
    if (!('InstanceYaml' in version)) throw protocolError('Instance read response was missing InstanceYaml')
    const text = version.InstanceYaml
    if (text === null) return null
    if (typeof text !== 'string' || !text) throw protocolError('Instance read returned invalid YAML data')
    const resource = parseYamlResource(text, 'Instance read')
    recentCreates.delete(recentCreateKey(context, name))
    return resource
  } catch (error) {
    if (isGraphQLResourceNotFound(error, name)) return null
    throw error
  }
}

export const api = {
  async listTemplates(
    filter: { category?: string; cloud?: string } = {},
    explicitContext?: PortalApiContext,
  ): Promise<{ items: Template[] }> {
    const context = captureContext(explicitContext)
    let items = await fetchTemplates(context)
    if (filter.category) items = items.filter(template => template.category === filter.category)
    if (filter.cloud) items = items.filter(template => template.cloud === filter.cloud)
    return { items }
  },

  async getTemplate(name: string, explicitContext?: PortalApiContext): Promise<{ template: Template }> {
    const context = captureContext(explicitContext)
    const data = await templateQuery<Infra<{ Template?: {
      metadata: { name: string; generation?: number }
      spec: Record<string, unknown>
      status?: RawTemplateStatus | null
    } }>>(
      context,
      spec => `query($n: String!) { ${GROUP_FIELD} { ${VERSION} { Template(name: $n) { metadata { name generation } spec { ${spec} } status { observedGeneration backend { ready message } conditions { type status observedGeneration reason message lastTransitionTime } } } } } }`,
      { n: name },
    )
    const version = versionPayload(data, 'Template read')
    if (!('Template' in version)) throw protocolError('Template read response was missing Template')
    const template = version.Template
    if (!template) throw <ErrorResponse>{ reason: 'TemplateNotFound', message: 'template ' + name + ' not found' }
    if (!template.metadata || typeof template.metadata.name !== 'string' ||
      !template.spec || typeof template.spec !== 'object' || Array.isArray(template.spec)) {
      throw protocolError('Template read returned an invalid resource shape')
    }
    return { template: templateFromGQL(template.metadata, template.spec, template.status) }
  },

  async createInstance(body: {
    templateName: string
    templateVersion?: string
    name: string
    values: Record<string, unknown>
  }): Promise<Instance> {
    const context = captureContext()
    const templates = await getTemplates(context, true)
    const template = templates.find(candidate => candidate.name === body.templateName)
    if (!template) {
      throw <ErrorResponse>{ reason: 'TemplateNotFound', message: 'template ' + body.templateName + ' not found' }
    }
    if (!template.ready) {
      throw <ErrorResponse>{
        reason: 'TemplateNotReady',
        message: template.readinessMessage || `template ${body.templateName} is not ready`,
      }
    }
    const created = await applyCR(context, buildInstanceManifest(body.name, body.templateName, body.values))
    assertContextCurrent(context)
    rememberCreate(context, created)
    return instanceFromObj(created)
  },

  async listInstances(explicitContext?: PortalApiContext): Promise<{
    items: Instance[]
    templates: Template[]
    identities: Array<{ name: string; uid?: string }>
  }> {
    const context = captureContext(explicitContext)
    const [templates, data] = await Promise.all([
      getTemplates(context),
      graphqlQuery<Infra<{ Instances?: { items?: RawObject[] } }>>(
        context,
        `{ ${GROUP_FIELD} { ${VERSION} { Instances { items { metadata { uid name namespace creationTimestamp deletionTimestamp generation labels } spec { template } status { observedGeneration phase message conditions { type status observedGeneration reason message lastTransitionTime } } } } } } }`,
      ),
    ])
    const list = versionPayload(data, 'Instance list').Instances
    if (!list || !Array.isArray(list.items)) {
      throw protocolError('Instance list response was missing its items array')
    }
    // A DELETE can remain finalizing while runtime resources are cleaned up.
    // Keep the terminating object in the list as an inert Deleting row. Raw
    // identities let the shared UID marker survive stale snapshots and be
    // released only when a successful list proves the old UID is absent.
    const identities = list.items.map(instanceIdentity)
    const items = list.items.map(instanceFromObj)
    await Promise.all(items.map(async instance => {
      const template = templates.find(candidate => candidate.name === instance.template)
      if (!template || !columnsNeedInstanceData(template.view)) return
      const full = await fetchInstanceYaml(context, instance.name)
      if (!full) return
      const parsed = instanceFromObj(full)
      // The name may have been deleted and recreated between the list and
      // detail reads. Never enrich the listed UID with a replacement's state.
      if (instance.uid && parsed.uid && instance.uid !== parsed.uid) return
      instance.values = parsed.values
      instance.status = parsed.status
      // Deletion is monotonic for one Kubernetes object incarnation. A
      // cache-backed detail read can lag the list that first exposed
      // deletionTimestamp, so never let that older response make the same UID
      // active again.
      const deletionTimestamp = instance.deletionTimestamp ?? parsed.deletionTimestamp
      instance.deletionTimestamp = deletionTimestamp
      instance.phase = deletionTimestamp ? 'Deleting' : parsed.phase
      instance.message = deletionTimestamp
        ? 'Deletion is in progress while provisioned resources are cleaned up.'
        : parsed.message
    }))
    assertContextCurrent(context)
    return { items, templates, identities }
  },

  async getInstance(name: string): Promise<Instance> {
    const context = captureContext()
    const found = await fetchInstanceYaml(context, name) ?? recentCreate(context, name)
    if (!found) throw <ErrorResponse>{ reason: 'InstanceNotFound', message: 'instance ' + name + ' not found' }
    return instanceFromObj(found)
  },

  async getInstanceDetail(name: string): Promise<{ instance: Instance; template?: Template }> {
    const context = captureContext()
    const [read, templates] = await Promise.all([
      fetchInstanceYaml(context, name),
      getTemplates(context),
    ])
    const found = read ?? recentCreate(context, name)
    if (!found) throw <ErrorResponse>{ reason: 'InstanceNotFound', message: 'instance ' + name + ' not found' }
    const instance = instanceFromObj(found)
    return { instance, template: templates.find(template => template.name === instance.template) }
  },

  async deleteInstance(name: string): Promise<void> {
    const context = captureContext()
    try {
      const data = await graphqlQuery<Infra<Record<string, unknown>>>(
        context,
        `mutation($n: String!) { ${GROUP_FIELD} { ${VERSION} { deleteInstance(name: $n) } } }`,
        { n: name },
      )
      const version = versionPayload(data, 'Instance delete')
      if (!('deleteInstance' in version)) {
        throw protocolError('Instance delete response was missing its result')
      }
    } catch (error) {
      if (isGraphQLResourceNotFound(error, name)) {
        recentCreates.delete(recentCreateKey(context, name))
        return
      }
      throw error
    }
    recentCreates.delete(recentCreateKey(context, name))
    assertContextCurrent(context)
  },
}
