import type {
  BackendReference,
  DeploymentSnapshot,
  DeploymentCondition,
  EvidenceState,
  ReleaseArtifact,
  ReleaseIntent,
} from './types.js'

type RecordLike = Record<string, unknown>

function record(value: unknown, label: string): RecordLike {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${label} must be an object`)
  }
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
  if (!value) return undefined
  if (typeof value === 'string') {
    try {
      return objectValue(JSON.parse(value))
    } catch {
      return undefined
    }
  }
  if (typeof value !== 'object' || Array.isArray(value)) return undefined
  return value as RecordLike
}

function stringMap(value: unknown): Record<string, string> {
  const source = objectValue(value)
  if (!source) return {}
  return Object.fromEntries(
    Object.entries(source)
      .filter(([, item]) => typeof item === 'string')
      .map(([key, item]) => [key, String(item)]),
  )
}

export function mapCondition(value: unknown): DeploymentCondition {
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

export function mapRelease(value: unknown): ReleaseIntent {
  const raw = record(value, 'release')
  const metadata = record(raw.metadata, 'release.metadata')
  const spec = record(raw.spec, 'release.spec')
  const source = record(spec.source, 'release.spec.source')
  const blueprint = record(spec.blueprintRef, 'release.spec.blueprintRef')
  const rawArtifacts = spec.artifacts === undefined ? [] : spec.artifacts
  if (!Array.isArray(rawArtifacts)) throw new Error('release.spec.artifacts must be an array')
  const artifacts: ReleaseArtifact[] = rawArtifacts.map((item, index) => {
    const artifact = record(item, `release.spec.artifacts[${index}]`)
    return {
      name: requiredString(artifact.name, `release.spec.artifacts[${index}].name`),
      image: requiredString(artifact.image, `release.spec.artifacts[${index}].image`),
    }
  })
  return {
    name: requiredString(metadata.name, 'release.metadata.name'),
    generation: optionalNumber(metadata.generation),
    repositoryRef: requiredString(source.repositoryRef, 'release.spec.source.repositoryRef'),
    revision: requiredString(source.revision, 'release.spec.source.revision'),
    blueprint: requiredString(blueprint.name, 'release.spec.blueprintRef.name'),
    artifacts,
    createdAt: optionalString(metadata.creationTimestamp),
  }
}

function mapBackendRef(value: unknown): BackendReference | undefined {
  if (value === null || value === undefined) return undefined
  const ref = record(value, 'deployment.status.backendRef')
  return {
    apiVersion: requiredString(ref.apiVersion, 'backendRef.apiVersion'),
    kind: requiredString(ref.kind, 'backendRef.kind'),
    resource: requiredString(ref.resource, 'backendRef.resource'),
    name: requiredString(ref.name, 'backendRef.name'),
    uid: optionalString(ref.uid),
  }
}

export function mapDeployment(value: unknown, releases: ReadonlyMap<string, ReleaseIntent> = new Map()): DeploymentSnapshot {
  const raw = record(value, 'deployment')
  const metadata = record(raw.metadata, 'deployment.metadata')
  const spec = record(raw.spec, 'deployment.spec')
  const status = objectValue(raw.status) ?? {}
  const rawConditions = status.conditions === undefined ? [] : status.conditions
  if (!Array.isArray(rawConditions)) throw new Error('deployment.status.conditions must be an array')
  const releaseRef = requiredString(spec.releaseRef, 'deployment.spec.releaseRef')
  return {
    name: requiredString(metadata.name, 'deployment.metadata.name'),
    uid: optionalString(metadata.uid),
    generation: optionalNumber(metadata.generation),
    createdAt: optionalString(metadata.creationTimestamp),
    deletionTimestamp: optionalString(metadata.deletionTimestamp),
    releaseRef,
    className: optionalString(spec.className) ?? 'kro-direct',
    mode: optionalString(spec.mode) ?? 'production',
    deletionPolicy: optionalString(spec.deletionPolicy) ?? 'Retain',
    rolloutID: requiredString(spec.rolloutID, 'deployment.spec.rolloutID'),
    configuration: objectValue(spec.configuration),
    observedGeneration: optionalNumber(status.observedGeneration),
    phase: optionalString(status.phase),
    conditions: rawConditions.map(mapCondition),
    activeReleaseRef: optionalString(status.activeReleaseRef),
    lastSuccessfulReleaseRef: optionalString(status.lastSuccessfulReleaseRef),
    observedRolloutID: optionalString(status.observedRolloutID),
    url: optionalString(status.url),
    outputs: stringMap(status.outputs),
    backendRef: mapBackendRef(status.backendRef),
    release: releases.get(releaseRef),
  }
}

export function mapResourceList(
  releasesValue: unknown,
  deploymentsValue: unknown,
): DeploymentSnapshot[] {
  if (!Array.isArray(releasesValue) || !Array.isArray(deploymentsValue)) {
    throw new Error('GraphQL returned malformed Deployments or Releases list')
  }
  const releases = new Map<string, ReleaseIntent>()
  for (const item of releasesValue) {
    const release = mapRelease(item)
    releases.set(release.name, release)
  }
  return deploymentsValue.map(item => mapDeployment(item, releases))
}

function condition(snapshot: DeploymentSnapshot, type: string): DeploymentCondition | undefined {
  return snapshot.conditions.find(item => item.type.toLowerCase() === type.toLowerCase())
}

export function isCurrentEvidence(snapshot: DeploymentSnapshot, item?: DeploymentCondition): boolean {
  const observed = item?.observedGeneration ?? snapshot.observedGeneration
  if (observed === undefined) return false
  if (snapshot.generation === undefined) return observed > 0
  return observed >= snapshot.generation
}

export function conditionIsTrue(snapshot: DeploymentSnapshot, type: string): boolean {
  const item = condition(snapshot, type)
  return item?.status === 'True' && isCurrentEvidence(snapshot, item)
}

export function evidenceState(snapshot: DeploymentSnapshot): EvidenceState {
  if (snapshot.deletionTimestamp) return 'deleting'
  if (snapshot.phase?.toLowerCase() === 'invalid') return 'invalid'
  if (snapshot.phase?.toLowerCase() === 'ready' && conditionIsTrue(snapshot, 'Ready')) return 'ready'
  if (conditionIsTrue(snapshot, 'Applied')) return 'applied'
  if (snapshot.phase?.toLowerCase() === 'pending' || snapshot.conditions.length > 0) return 'pending'
  return 'unknown'
}

export function evidenceLabel(state: EvidenceState): string {
  switch (state) {
    case 'ready': return 'Ready'
    case 'applied': return 'Applied'
    case 'pending': return 'Pending'
    case 'invalid': return 'Invalid'
    case 'deleting': return 'Deleting'
    default: return 'Unknown'
  }
}

export function evidenceTone(state: EvidenceState): 'success' | 'warning' | 'danger' | 'muted' {
  switch (state) {
    case 'ready':
      return 'success'
    case 'applied':
      return 'warning'
    case 'pending':
      return 'warning'
    case 'invalid':
    case 'deleting':
      return 'danger'
    default:
      return 'muted'
  }
}
