import type {
  RepositorySyncSnapshot,
  SyncClaimReference,
  SyncCondition,
  SyncEvidenceState,
  SyncInventoryItem,
  SyncTargetRequirement,
} from './types.js'

type RecordLike = Record<string, unknown>

function record(value: unknown, label: string): RecordLike {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`)
  return value as RecordLike
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined
}

function requiredString(value: unknown, label: string): string {
  const result = optionalString(value)
  if (!result) throw new Error(`${label} is required`)
  return result
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function objectValue(value: unknown): RecordLike | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  return value as RecordLike
}

function list(value: unknown, label: string): unknown[] {
  if (value === undefined || value === null) return []
  if (!Array.isArray(value)) throw new Error(`${label} must be an array`)
  return value
}

export function mapCondition(value: unknown): SyncCondition {
  const condition = record(value, 'condition')
  return {
    type: requiredString(condition.type, 'condition.type'),
    status: requiredString(condition.status, 'condition.status'),
    reason: optionalString(condition.reason),
    message: optionalString(condition.message),
    lastTransitionTime: optionalString(condition.lastTransitionTime),
    observedGeneration: optionalNumber(condition.observedGeneration),
  }
}

function mapClaim(value: unknown): SyncClaimReference | undefined {
  if (value === undefined || value === null) return undefined
  const claim = record(value, 'targetRequirement.claim')
  const verbs = list(claim.verbs, 'targetRequirement.claim.verbs')
  if (!verbs.every(verb => typeof verb === 'string')) throw new Error('targetRequirement.claim.verbs must contain strings')
  return {
    group: typeof claim.group === 'string' ? claim.group : '',
    resource: requiredString(claim.resource, 'targetRequirement.claim.resource'),
    verbs: verbs as string[],
  }
}

function mapRequirement(value: unknown): SyncTargetRequirement {
  const requirement = record(value, 'targetRequirement')
  return {
    apiVersion: requiredString(requirement.apiVersion, 'targetRequirement.apiVersion'),
    kind: requiredString(requirement.kind, 'targetRequirement.kind'),
    resource: requiredString(requirement.resource, 'targetRequirement.resource'),
    namespace: optionalString(requirement.namespace),
    state: requiredString(requirement.state, 'targetRequirement.state'),
    message: optionalString(requirement.message),
    claim: mapClaim(requirement.claim),
  }
}

function mapInventoryItem(value: unknown): SyncInventoryItem {
  const item = record(value, 'inventory item')
  return {
    apiVersion: requiredString(item.apiVersion, 'inventory.apiVersion'),
    kind: requiredString(item.kind, 'inventory.kind'),
    resource: requiredString(item.resource, 'inventory.resource'),
    namespace: optionalString(item.namespace),
    name: requiredString(item.name, 'inventory.name'),
    uid: optionalString(item.uid),
    sourcePath: optionalString(item.sourcePath),
  }
}

export function mapRepositorySync(value: unknown): RepositorySyncSnapshot {
  const raw = record(value, 'repositorySync')
  const metadata = record(raw.metadata, 'repositorySync.metadata')
  const spec = record(raw.spec, 'repositorySync.spec')
  const status = objectValue(raw.status) ?? {}
  return {
    name: requiredString(metadata.name, 'repositorySync.metadata.name'),
    uid: optionalString(metadata.uid),
    generation: optionalNumber(metadata.generation),
    createdAt: optionalString(metadata.creationTimestamp),
    deletionTimestamp: optionalString(metadata.deletionTimestamp),
    repositoryRef: requiredString(spec.repositoryRef, 'repositorySync.spec.repositoryRef'),
    ref: optionalString(spec.ref),
    path: optionalString(spec.path),
    intervalSeconds: optionalNumber(spec.intervalSeconds),
    prune: spec.prune === true,
    observedGeneration: optionalNumber(status.observedGeneration),
    phase: optionalString(status.phase),
    observedRevision: optionalString(status.observedRevision),
    appliedRevision: optionalString(status.appliedRevision),
    inventory: list(status.inventory, 'repositorySync.status.inventory').map(mapInventoryItem),
    targetRequirements: list(status.targetRequirements, 'repositorySync.status.targetRequirements').map(mapRequirement),
    conditions: list(status.conditions, 'repositorySync.status.conditions').map(mapCondition),
  }
}

export function mapRepositorySyncList(value: unknown): RepositorySyncSnapshot[] {
  if (!Array.isArray(value)) throw new Error('GraphQL returned a malformed RepositorySync list')
  return value.map(mapRepositorySync)
}

function condition(snapshot: RepositorySyncSnapshot, type: string): SyncCondition | undefined {
  return snapshot.conditions.find(item => item.type.toLowerCase() === type.toLowerCase())
}

export function isCurrentEvidence(snapshot: RepositorySyncSnapshot, item?: SyncCondition): boolean {
  const observed = item?.observedGeneration ?? snapshot.observedGeneration
  if (observed === undefined) return false
  if (snapshot.generation === undefined) return observed > 0
  return observed >= snapshot.generation
}

export function conditionIsTrue(snapshot: RepositorySyncSnapshot, type: string): boolean {
  const item = condition(snapshot, type)
  return item?.status === 'True' && isCurrentEvidence(snapshot, item)
}

export function syncEvidenceState(snapshot: RepositorySyncSnapshot): SyncEvidenceState {
  if (snapshot.deletionTimestamp) return 'deleting'
  switch (snapshot.phase?.toLowerCase()) {
    case 'awaitingauthorization': return 'awaiting-authorization'
    case 'failed': return 'failed'
    case 'synced':
    case 'ready': return conditionIsTrue(snapshot, 'Applied') ? 'ready' : 'pending'
    case 'pending':
    case 'reconciling': return 'pending'
    default: return snapshot.conditions.length ? 'pending' : 'unknown'
  }
}

export function evidenceLabel(state: SyncEvidenceState): string {
  switch (state) {
    case 'ready': return 'Applied'
    case 'awaiting-authorization': return 'Access required'
    case 'pending': return 'Reconciling'
    case 'failed': return 'Failed'
    case 'deleting': return 'Deleting'
    default: return 'Unknown'
  }
}

export function evidenceTone(state: SyncEvidenceState): 'success' | 'warning' | 'danger' | 'muted' {
  switch (state) {
    case 'ready': return 'success'
    case 'awaiting-authorization':
    case 'pending': return 'warning'
    case 'failed':
    case 'deleting': return 'danger'
    default: return 'muted'
  }
}
