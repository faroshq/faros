import {
  conditionIsTrue,
  evidenceTone,
  mapRepositorySync,
  mapRepositorySyncList,
  syncEvidenceState,
} from './mapper.js'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

function repositorySync(status: Record<string, unknown> = {}) {
  return {
    metadata: { name: 'pen-store-production', uid: 'sync-uid', generation: 3 },
    spec: { repositoryRef: 'pen-store-app', ref: 'main', path: '.faros/production', prune: true },
    status: {
      observedGeneration: 3,
      phase: 'Synced',
      observedRevision: '508862cd',
      appliedRevision: '508862cd',
      inventory: [{
        apiVersion: 'infrastructure.faros.sh/v1alpha1', kind: 'Instance', resource: 'instances',
        name: 'pen-store-production', uid: 'instance-uid', sourcePath: '.faros/production/instance.yaml',
      }],
      targetRequirements: [{
        apiVersion: 'infrastructure.faros.sh/v1alpha1', kind: 'Instance', resource: 'instances', state: 'Authorized',
        claim: { group: 'infrastructure.faros.sh', resource: 'instances', verbs: ['get', 'create', 'patch'] },
      }],
      conditions: [
        { type: 'SourceReady', status: 'True', observedGeneration: 3 },
        { type: 'AuthorizationReady', status: 'True', observedGeneration: 3 },
        { type: 'Applied', status: 'True', observedGeneration: 3 },
      ],
      ...status,
    },
  }
}

const synced = mapRepositorySync(repositorySync())
assert(synced.repositoryRef === 'pen-store-app', 'repository source was not mapped')
assert(synced.inventory[0].resource === 'instances', 'exact inventory resource was not mapped')
assert(synced.inventory[0].sourcePath?.endsWith('instance.yaml'), 'inventory source path was lost')
assert(synced.targetRequirements[0].claim?.group === 'infrastructure.faros.sh', 'target claim was not mapped')
assert(conditionIsTrue(synced, 'Applied'), 'current Applied condition was not recognized')
assert(syncEvidenceState(synced) === 'ready', 'Synced apply evidence was not presented as applied')
assert(evidenceTone('ready') === 'success', 'successful apply evidence was not successful')

const waiting = mapRepositorySync(repositorySync({
  phase: 'AwaitingAuthorization',
  appliedRevision: undefined,
  targetRequirements: [{
    apiVersion: 'example.faros.sh/v1alpha1', kind: 'Widget', resource: 'widgets', state: 'AwaitingAuthorization',
    claim: { group: 'example.faros.sh', resource: 'widgets', verbs: ['get', 'create', 'patch'] },
  }],
  conditions: [{ type: 'AuthorizationReady', status: 'False', observedGeneration: 3, reason: 'PermissionClaimsRequired' }],
}))
assert(syncEvidenceState(waiting) === 'awaiting-authorization', 'missing target access was not explicit')
assert(waiting.targetRequirements[0].claim?.resource === 'widgets', 'required claim was not retained')

const stale = mapRepositorySync(repositorySync({
  observedGeneration: 2,
  conditions: [{ type: 'Applied', status: 'True', observedGeneration: 2 }],
}))
assert(!conditionIsTrue(stale, 'Applied'), 'stale Applied condition was presented as current')
assert(syncEvidenceState(stale) === 'pending', 'stale apply evidence was not pending')

const failed = mapRepositorySync(repositorySync({ phase: 'Failed', conditions: [
  { type: 'Applied', status: 'False', observedGeneration: 3, reason: 'ApplyConflict' },
] }))
assert(syncEvidenceState(failed) === 'failed', 'apply failure was not explicit')

const list = mapRepositorySyncList([repositorySync(), repositorySync({ phase: 'Reconciling', appliedRevision: undefined })])
assert(list.length === 2, 'RepositorySync list was not mapped')

let rejectedMissingIdentity = false
try {
  mapRepositorySync({ metadata: { name: 'broken' }, spec: { ref: 'main' } })
} catch {
  rejectedMissingIdentity = true
}
assert(rejectedMissingIdentity, 'malformed RepositorySync was silently accepted')
