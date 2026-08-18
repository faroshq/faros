export interface FarosContext {
  token?: string | null
  user?: { email?: string; sub?: string } | null
  tenant?: string | null
  theme?: 'light' | 'dark' | 'system'
  basePath?: string
  subPath?: string
}

export interface SyncCondition {
  type: string
  status: string
  reason?: string
  message?: string
  lastTransitionTime?: string
  observedGeneration?: number
}

export interface SyncClaimReference {
  group: string
  resource: string
  verbs: string[]
}

export interface SyncTargetRequirement {
  apiVersion: string
  kind: string
  resource: string
  namespace?: string
  state: 'Granted' | 'AuthorizationRequired' | 'TargetAPIUnavailable' | 'Unsupported' | string
  message?: string
  claim?: SyncClaimReference
}

export interface SyncInventoryItem {
  apiVersion: string
  kind: string
  resource: string
  namespace?: string
  name: string
  uid?: string
  sourcePath?: string
}

export interface RepositorySyncSnapshot {
  name: string
  uid?: string
  generation?: number
  createdAt?: string
  deletionTimestamp?: string
  repositoryRef: string
  ref?: string
  path?: string
  intervalSeconds?: number
  prune: boolean
  observedGeneration?: number
  phase?: string
  observedRevision?: string
  appliedRevision?: string
  inventory: SyncInventoryItem[]
  targetRequirements: SyncTargetRequirement[]
  conditions: SyncCondition[]
}

export type SyncEvidenceState =
  | 'pending'
  | 'awaiting-authorization'
  | 'failed'
  | 'deleting'
  | 'ready'
  | 'unknown'

export interface ErrorResponse extends Error {
  reason: 'Unauthorized' | 'MissingBackend' | 'NotFound' | 'ProtocolError' | 'NetworkError' | 'GraphQLError' | 'TenantMissing'
  retryable?: boolean
}

export interface RepositorySyncListResult {
  items: RepositorySyncSnapshot[]
}
