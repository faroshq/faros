import assert from 'node:assert/strict'
import test from 'node:test'
import { createServer } from 'vite'

const vite = await createServer({
  appType: 'custom',
  cacheDir: '/tmp/faros-vite-release-selection',
  configFile: false,
  server: { middlewareMode: true },
})
const {
  adjacentDeployableRelease,
  formatReleaseAge,
  newestDeployableRelease,
  releaseHasPromotionEvidence,
  reconcileReleaseSelection,
  releaseActionLabel,
  releaseMissingEvidence,
  orderReleases,
} = await vite.ssrLoadModule('/src/releaseSelection.ts')
test.after(async () => vite.close())

const release = (commitSHA, overrides = {}) => ({
  commitSHA,
  releaseID: `release-${commitSHA}`,
  deployable: true,
  live: false,
  createdAt: '2026-08-18T10:00:00Z',
  ...overrides,
})

test('orders releases newest first and defaults to the newest deployable release', () => {
  const releases = [
    release('old', { createdAt: '2026-08-17T10:00:00Z' }),
    release('incomplete-newest', { createdAt: '2026-08-19T10:00:00Z', deployable: false, missing: ['api'] }),
    release('newest-complete', { createdAt: '2026-08-18T12:00:00Z' }),
  ]

  assert.deepEqual(orderReleases(releases).map(({ commitSHA }) => commitSHA), ['incomplete-newest', 'newest-complete', 'old'])
  assert.equal(newestDeployableRelease(releases).commitSHA, 'newest-complete')
  assert.equal(reconcileReleaseSelection('', releases), 'newest-complete')
})

test('preserves a selected release through refresh and falls back when it disappears', () => {
  const releases = [release('newest'), release('selected', { createdAt: '2026-08-17T10:00:00Z' })]
  const refreshed = [release('newest', { createdAt: '2026-08-19T10:00:00Z' }), release('selected', { createdAt: '2026-08-17T10:00:00Z' })]
  assert.equal(reconcileReleaseSelection('selected', refreshed), 'selected')
  assert.equal(reconcileReleaseSelection('selected', refreshed, true), 'newest')
  assert.equal(reconcileReleaseSelection('selected', [release('newest')]), 'newest')
  assert.equal(reconcileReleaseSelection('selected', [release('selected', { releaseID: '' }), release('newest')]), 'newest')
})

test('moves keyboard selection across deployable releases with wrapping', () => {
  const releases = [
    release('newest'),
    release('incomplete', { deployable: false, createdAt: '2026-08-18T09:00:00Z' }),
    release('oldest', { createdAt: '2026-08-17T10:00:00Z' }),
  ]
  assert.equal(adjacentDeployableRelease(releases, 'newest', 'next').commitSHA, 'oldest')
  assert.equal(adjacentDeployableRelease(releases, 'oldest', 'next').commitSHA, 'newest')
  assert.equal(adjacentDeployableRelease(releases, 'newest', 'previous').commitSHA, 'oldest')
  assert.equal(adjacentDeployableRelease(releases, 'oldest', 'first').commitSHA, 'newest')
  assert.equal(adjacentDeployableRelease(releases, 'newest', 'last').commitSHA, 'oldest')
})

test('labels deploy, redeploy, and historical selection without calling it a source rollback', () => {
  const live = release('live', { live: true })
  const historical = release('historical', { createdAt: '2026-08-17T10:00:00Z' })
  const newer = release('newer', { createdAt: '2026-08-19T10:00:00Z' })
  assert.equal(releaseActionLabel(null, []), 'Deploy selected release')
  assert.equal(releaseActionLabel(release('new'), [release('new')]), 'Deploy selected release')
  assert.equal(releaseActionLabel(live, [live, historical]), 'Redeploy current release')
  assert.equal(releaseActionLabel(historical, [live, historical]), 'Roll back to this release')
  assert.equal(releaseActionLabel(newer, [newer, live, historical]), 'Deploy selected release')
  assert.doesNotMatch(releaseActionLabel(historical, [live, historical]), /Git|source/i)
})

test('retains explicit missing evidence and derives missing components', () => {
  assert.deepEqual(releaseMissingEvidence(release('explicit', { deployable: false, missing: ['api', 'api', ' web '] })), ['api', 'web'])
  assert.deepEqual(releaseMissingEvidence(release('derived', {
    deployable: false,
    components: [
      { name: 'web', imageInput: 'webImage', built: true },
      { name: 'api', imageInput: 'apiImage', built: false },
    ],
  })), ['api'])
})

test('requires the server-derived release identity before enabling promotion', () => {
  assert.equal(releaseHasPromotionEvidence(release('complete')), true)
  assert.equal(releaseHasPromotionEvidence(release('missing-id', { releaseID: '' })), false)
  assert.equal(releaseHasPromotionEvidence(release('missing-sha', { commitSHA: '' })), false)
  assert.equal(newestDeployableRelease([
    release('newest-without-id', { releaseID: '', createdAt: '2026-08-19T10:00:00Z' }),
    release('older-complete'),
  ]).commitSHA, 'older-complete')
})

test('formats compact relative ages for timeline metadata', () => {
  const now = Date.parse('2026-08-18T12:00:00Z')
  assert.equal(formatReleaseAge('2026-08-18T11:59:40Z', now), 'just now')
  assert.equal(formatReleaseAge('2026-08-18T11:58:00Z', now), '2 minutes ago')
  assert.equal(formatReleaseAge('2026-08-10T12:00:00Z', now), '8 days ago')
  assert.equal(formatReleaseAge(undefined, now), '')
})
