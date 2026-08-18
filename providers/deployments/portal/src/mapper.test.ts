import {
  conditionIsTrue,
  evidenceState,
  evidenceTone,
  mapResourceList,
} from './mapper.js'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const release = {
  metadata: { name: 'release-42', generation: 1, creationTimestamp: '2026-08-18T00:00:00Z' },
  spec: {
    source: { repositoryRef: 'org/catalog', revision: 'sha256:deadbeef' },
    blueprintRef: { name: 'application' },
    artifacts: [{ name: 'web', image: 'ghcr.io/example/web@sha256:abc' }],
  },
}

function deployment(overrides: Record<string, unknown> = {}) {
  return {
    metadata: { name: 'production', uid: 'dep-uid', generation: 3 },
    spec: { releaseRef: 'release-42', rolloutID: 'rollout-3', mode: 'production', deletionPolicy: 'Retain' },
    status: {
      observedGeneration: 3,
      phase: 'Ready',
      conditions: [
        { type: 'Applied', status: 'True', observedGeneration: 3, reason: 'BackendConverged' },
        { type: 'Ready', status: 'True', observedGeneration: 3, reason: 'BackendReady' },
      ],
      observedRolloutID: 'rollout-3',
      backendRef: { apiVersion: 'infrastructure.faros.sh/v1alpha1', kind: 'Instance', resource: 'instances', name: 'production' },
      outputs: { url: 'https://example.test' },
      ...overrides,
    },
  }
}

const ready = mapResourceList([release], [deployment()])[0]
assert(ready.release?.revision === 'sha256:deadbeef', 'release intent did not join to Deployment')
assert(conditionIsTrue(ready, 'Applied'), 'current Applied condition was not recognized')
assert(conditionIsTrue(ready, 'Ready'), 'current Ready condition was not recognized')
assert(evidenceState(ready) === 'ready', 'current Ready evidence was not presented as ready')
assert(evidenceTone('applied') === 'warning', 'Applied evidence did not remain visibly pending runtime readiness')

const stale = mapResourceList([release], [deployment({ observedGeneration: 2, conditions: [
  { type: 'Applied', status: 'True', observedGeneration: 2 },
  { type: 'Ready', status: 'True', observedGeneration: 2 },
] })])[0]
assert(!conditionIsTrue(stale, 'Ready'), 'stale Ready condition was presented as current')
assert(evidenceState(stale) === 'pending', 'stale rollout was not pending')

const invalid = mapResourceList([release], [deployment({ phase: 'Invalid', conditions: [
  { type: 'Applied', status: 'False', observedGeneration: 3 },
  { type: 'Ready', status: 'False', observedGeneration: 3 },
] })])[0]
assert(evidenceState(invalid) === 'invalid', 'Invalid phase was not explicit')

const deleting = mapResourceList([release], [{
  ...deployment(),
  metadata: { name: 'production', uid: 'dep-uid', generation: 3, deletionTimestamp: '2026-08-18T01:00:00Z' },
}])[0]
assert(evidenceState(deleting) === 'deleting', 'deleting metadata was not explicit')

const unknown = mapResourceList([], [deployment({ phase: undefined, conditions: [], observedGeneration: undefined })])[0]
assert(evidenceState(unknown) === 'unknown', 'missing evidence was not explicit')
assert(!unknown.release, 'a missing Release was silently fabricated')
