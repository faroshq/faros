import { mapRelease, mapResourceList } from './mapper.js'
import type { DeploymentListResult, DeploymentSnapshot, ErrorResponse, ReleaseIntent } from './types.js'

const GROUP_FIELD = 'deployments_faros_sh'
const VERSION = 'v1alpha1'

let bearerToken: string | null = null
let clusterName: string | null = null

export function setBasePath(_basePath?: string | null): void {
  // Deployments CRs are caller-authenticated through the hub GraphQL route.
  // The provider context's basePath is intentionally not used to construct a
  // second service URL.
  void _basePath
}

export function setToken(token?: string | null): void {
  bearerToken = token || null
}

export function setTenant(name?: string | null): void {
  clusterName = name || null
}

/** Build one caller-authenticated GraphQL path segment for a tenant. */
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
  if (!clusterName) throw errorResponse('TenantMissing', 'Select a workspace to view Deployments.', false)
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'Content-Type': 'application/json',
  }
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
    throw errorResponse('NetworkError', 'The workspace gateway could not be reached. Retry the read.')
  }
  const text = await response.text()
  if (!response.ok) {
    const reason = response.status === 401
      ? 'Unauthorized'
      : response.status === 403
        ? classifyMessage(text)
        : 'GraphQLError'
    throw errorResponse(reason, text || `Workspace gateway returned HTTP ${response.status}.`)
  }
  let body: { data?: T; errors?: Array<{ message?: string }> }
  try {
    body = (text ? JSON.parse(text) : {}) as { data?: T; errors?: Array<{ message?: string }> }
  } catch {
    throw errorResponse('ProtocolError', 'Workspace gateway returned malformed JSON. Retry the read.')
  }
  if (Array.isArray(body.errors) && body.errors.length > 0) {
    const message = body.errors.map(error => error.message || 'GraphQL error').join('; ')
    throw errorResponse(classifyMessage(message), message)
  }
  if (!body.data) throw errorResponse('ProtocolError', 'Workspace gateway returned no data. Retry the read.')
  return body.data
}

const META = 'metadata { name uid generation creationTimestamp deletionTimestamp }'
const CONDITIONS = 'conditions { type status reason message lastTransitionTime observedGeneration }'
const RELEASE = `${META} spec { source { repositoryRef revision } blueprintRef { name } artifacts { name image } }`
const DEPLOYMENT = `${META} spec { releaseRef className mode deletionPolicy configuration rolloutID } status { observedGeneration phase ${CONDITIONS} activeReleaseRef lastSuccessfulReleaseRef observedRolloutID url outputs backendRef { apiVersion kind resource name uid } }`

interface GraphQLEnvelope {
  deployments_faros_sh?: {
    v1alpha1?: {
      Releases?: { items?: unknown[] }
      Deployments?: { items?: unknown[] }
      Release?: unknown | null
      Deployment?: unknown | null
    }
  }
}

function scope(data: GraphQLEnvelope): NonNullable<GraphQLEnvelope['deployments_faros_sh']>['v1alpha1'] {
  return data.deployments_faros_sh?.v1alpha1
}

export async function listDeployments(): Promise<DeploymentListResult> {
  const data = await graphqlQuery<GraphQLEnvelope>(
    `query { ${GROUP_FIELD} { ${VERSION} { Releases { items { ${RELEASE} } } Deployments { items { ${DEPLOYMENT} } } } } }`,
  )
  const result = scope(data)
  if (!result || !Array.isArray(result.Releases?.items) || !Array.isArray(result.Deployments?.items)) {
    throw errorResponse('ProtocolError', 'Workspace gateway returned an incomplete Deployments list. Retry the read.')
  }
  try {
    return { items: mapResourceList(result.Releases.items, result.Deployments.items) }
  } catch (error) {
    const message = error instanceof Error ? error.message : 'Workspace gateway returned malformed Deployments data.'
    throw errorResponse('ProtocolError', message)
  }
}

export async function getDeployment(name: string): Promise<DeploymentSnapshot> {
  const data = await graphqlQuery<GraphQLEnvelope>(
    `query($n: String!) { ${GROUP_FIELD} { ${VERSION} { Deployment(name: $n) { ${DEPLOYMENT} } } } }`,
    { n: name },
  )
  const envelope = scope(data)
  if (!envelope || !envelope.Deployment) throw errorResponse('NotFound', `Deployment "${name}" was not found.`, false)
  try {
    const deployment = envelope.Deployment as Record<string, unknown>
    const releaseRef = (((deployment.spec as Record<string, unknown> | undefined)?.releaseRef) as string | undefined) ?? ''
    let release: ReleaseIntent | undefined
    if (releaseRef) {
      try {
        release = (await getRelease(releaseRef)).intent
      } catch (error) {
        // Keep the Deployment evidence visible when its immutable Release has
        // not reached the tenant yet. The detail view labels the missing
        // intent explicitly instead of collapsing the whole read.
        if ((error as ErrorResponse).reason !== 'NotFound') throw error
      }
    }
    const mapped = mapResourceList([], [deployment])[0]
    mapped.release = release
    return mapped
  } catch (error) {
    if ((error as ErrorResponse).reason) throw error
    const message = error instanceof Error ? error.message : 'Workspace gateway returned malformed Deployment data.'
    throw errorResponse('ProtocolError', message)
  }
}

async function getRelease(name: string): Promise<{ raw: unknown; intent: ReleaseIntent }> {
  if (!name) throw errorResponse('ProtocolError', 'Deployment has no release reference.')
  const data = await graphqlQuery<GraphQLEnvelope>(
    `query($n: String!) { ${GROUP_FIELD} { ${VERSION} { Release(name: $n) { ${RELEASE} } } } }`,
    { n: name },
  )
  const raw = scope(data)?.Release
  if (!raw) throw errorResponse('NotFound', `Release "${name}" was not found.`, false)
  try {
    const intent = mapRelease(raw)
    return { raw, intent }
  } catch (error) {
    const message = error instanceof Error ? error.message : 'Workspace gateway returned malformed Release data.'
    throw errorResponse('ProtocolError', message)
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

export function followableURL(value?: string): string | null {
  if (!value) return null
  try {
    const parsed = new URL(value, window.location.origin)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null
    return parsed.href
  } catch {
    return null
  }
}
