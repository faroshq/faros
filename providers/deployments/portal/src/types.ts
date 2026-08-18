export interface FarosContext {
  token?: string | null
  user?: { email?: string; sub?: string } | null
  tenant?: string | null
  theme?: 'light' | 'dark' | 'system'
  basePath?: string
  subPath?: string
}

export interface ReleaseArtifact {
  name: string
  image: string
}

export interface ReleaseIntent {
  name: string
  generation?: number
  repositoryRef: string
  revision: string
  blueprint: string
  artifacts: ReleaseArtifact[]
  createdAt?: string
}

export interface BackendReference {
  apiVersion: string
  kind: string
  resource: string
  name: string
  uid?: string
}

export interface DeploymentCondition {
  type: string
  status: string
  reason?: string
  message?: string
  lastTransitionTime?: string
  observedGeneration?: number
}

export interface DeploymentSnapshot {
  name: string
  uid?: string
  generation?: number
  createdAt?: string
  deletionTimestamp?: string
  releaseRef: string
  className: string
  mode: string
  deletionPolicy: string
  rolloutID: string
  configuration?: Record<string, unknown>
  observedGeneration?: number
  phase?: string
  conditions: DeploymentCondition[]
  activeReleaseRef?: string
  lastSuccessfulReleaseRef?: string
  observedRolloutID?: string
  url?: string
  outputs: Record<string, string>
  backendRef?: BackendReference
  release?: ReleaseIntent
}

export type EvidenceState = 'pending' | 'invalid' | 'deleting' | 'ready' | 'applied' | 'unknown'

export interface ErrorResponse extends Error {
  reason: 'Unauthorized' | 'MissingBackend' | 'NotFound' | 'ProtocolError' | 'NetworkError' | 'GraphQLError' | 'TenantMissing'
  retryable?: boolean
}

export interface DeploymentListResult {
  items: DeploymentSnapshot[]
}
