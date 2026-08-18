import { mapRepositorySync, mapRepositorySyncList } from './mapper.js'
import type {
  CreateRepositorySyncInput,
  ErrorResponse,
  RepositorySyncListResult,
  RepositorySyncSnapshot,
} from './types.js'

const GROUP_FIELD = 'deployments_faros_sh'
const VERSION = 'v1alpha1'

let bearerToken: string | null = null
let clusterName: string | null = null

export function setBasePath(_basePath?: string | null): void {
  void _basePath
}

export function setToken(token?: string | null): void {
  bearerToken = token || null
}

export function setTenant(name?: string | null): void {
  clusterName = name || null
}

export function graphqlPath(tenant: string): string {
  return '/graphql/' + encodeURIComponent(tenant)
}

function errorResponse(reason: ErrorResponse['reason'], message: string, retryable = true): ErrorResponse {
  return Object.assign(new Error(message), { reason, retryable }) as ErrorResponse
}

function classifyMessage(message: string): ErrorResponse['reason'] {
  if (/401|unauthori[sz]ed|authentication required|invalid bearer/i.test(message)) return 'Unauthorized'
  if (/forbidden|permission denied/i.test(message)) return 'Unauthorized'
  if (/apibinding|no matches for kind|resource .* not found|does not exist/i.test(message)) return 'MissingBackend'
  return 'GraphQLError'
}

async function graphqlQuery<T>(query: string, variables: Record<string, unknown> = {}): Promise<T> {
  if (!clusterName) throw errorResponse('TenantMissing', 'Select a workspace to manage repository syncs.', false)
  const headers: Record<string, string> = { Accept: 'application/json', 'Content-Type': 'application/json' }
  if (bearerToken) headers.Authorization = 'Bearer ' + bearerToken
  let response: Response
  try {
    response = await fetch(graphqlPath(clusterName), {
      method: 'POST',
      credentials: 'same-origin',
      headers,
      body: JSON.stringify({ query, variables }),
    })
  } catch {
    throw errorResponse('NetworkError', 'The workspace gateway could not be reached. Retry the request.')
  }
  const bodyText = await response.text()
  if (!response.ok) {
    const reason = response.status === 401 ? 'Unauthorized' : classifyMessage(bodyText)
    throw errorResponse(reason, bodyText || `Workspace gateway returned HTTP ${response.status}.`)
  }
  let body: { data?: T; errors?: Array<{ message?: string }> }
  try {
    body = (bodyText ? JSON.parse(bodyText) : {}) as typeof body
  } catch {
    throw errorResponse('ProtocolError', 'Workspace gateway returned malformed JSON. Retry the request.')
  }
  if (body.errors?.length) {
    const message = body.errors.map(error => error.message || 'GraphQL error').join('; ')
    throw errorResponse(classifyMessage(message), message)
  }
  if (!body.data) throw errorResponse('ProtocolError', 'Workspace gateway returned no data. Retry the request.')
  return body.data
}

const META = 'metadata { name uid generation creationTimestamp deletionTimestamp }'
const CONDITIONS = 'conditions { type status reason message lastTransitionTime observedGeneration }'
const CLAIM = 'claim { group resource verbs }'
const REQUIREMENTS = `targetRequirements { apiVersion kind resource namespace state message ${CLAIM} }`
const INVENTORY = 'inventory { apiVersion kind resource namespace name uid sourcePath }'
const SYNC = `${META} spec { repositoryRef ref path intervalSeconds prune } status { observedGeneration phase observedRevision appliedRevision ${INVENTORY} ${REQUIREMENTS} ${CONDITIONS} }`

interface GraphQLEnvelope {
  deployments_faros_sh?: {
    v1alpha1?: {
      RepositorySyncs?: { items?: unknown[] }
      RepositorySync?: unknown | null
      createRepositorySync?: unknown | null
    }
  }
}

function scope(data: GraphQLEnvelope): NonNullable<GraphQLEnvelope['deployments_faros_sh']>['v1alpha1'] {
  return data.deployments_faros_sh?.v1alpha1
}

export async function listRepositorySyncs(): Promise<RepositorySyncListResult> {
  const data = await graphqlQuery<GraphQLEnvelope>(
    `query { ${GROUP_FIELD} { ${VERSION} { RepositorySyncs { items { ${SYNC} } } } } }`,
  )
  const items = scope(data)?.RepositorySyncs?.items
  if (!Array.isArray(items)) throw errorResponse('ProtocolError', 'Workspace gateway returned an incomplete RepositorySync list. Retry the read.')
  try {
    return { items: mapRepositorySyncList(items) }
  } catch (error) {
    throw errorResponse('ProtocolError', error instanceof Error ? error.message : 'Workspace gateway returned malformed RepositorySync data.')
  }
}

export async function getRepositorySync(name: string): Promise<RepositorySyncSnapshot> {
  const data = await graphqlQuery<GraphQLEnvelope>(
    `query($n: String!) { ${GROUP_FIELD} { ${VERSION} { RepositorySync(name: $n) { ${SYNC} } } } }`,
    { n: name },
  )
  const raw = scope(data)?.RepositorySync
  if (!raw) throw errorResponse('NotFound', `RepositorySync "${name}" was not found.`, false)
  try {
    return mapRepositorySync(raw)
  } catch (error) {
    throw errorResponse('ProtocolError', error instanceof Error ? error.message : 'Workspace gateway returned malformed RepositorySync data.')
  }
}

export async function createRepositorySync(input: CreateRepositorySyncInput): Promise<RepositorySyncSnapshot> {
  const spec: Record<string, unknown> = { repositoryRef: input.repositoryRef }
  if (input.ref) spec.ref = input.ref
  if (input.path) spec.path = input.path
  if (input.intervalSeconds !== undefined) spec.intervalSeconds = input.intervalSeconds
  if (input.prune !== undefined) spec.prune = input.prune

  const data = await graphqlQuery<GraphQLEnvelope>(
    `mutation CreateRepositorySync($object: DeploymentsFarosShV1alpha1RepositorySync_Input!) {
      ${GROUP_FIELD} { ${VERSION} { createRepositorySync(object: $object) { ${SYNC} } } }
    }`,
    { object: { metadata: { name: input.name }, spec } },
  )
  const raw = scope(data)?.createRepositorySync
  if (!raw) throw errorResponse('ProtocolError', 'Workspace gateway returned no created RepositorySync. Retry the request.')
  try {
    return mapRepositorySync(raw)
  } catch (error) {
    throw errorResponse('ProtocolError', error instanceof Error ? error.message : 'Workspace gateway returned malformed RepositorySync data.')
  }
}

export async function copyText(value: string): Promise<boolean> {
  if (!value) return false
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
      return true
    }
  } catch {
    // Fall through to the selection-based browser fallback.
  }
  if (typeof document === 'undefined') return false
  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  let copied = false
  try {
    copied = document.execCommand('copy')
  } catch {
    copied = false
  }
  textarea.remove()
  return copied
}
