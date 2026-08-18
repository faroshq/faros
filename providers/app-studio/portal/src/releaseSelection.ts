import type { ProjectRelease } from './types'

export type ReleaseActionKind = 'deploy' | 'redeploy' | 'rollback'

function clean(value: string | null | undefined): string {
  return typeof value === 'string' ? value.trim() : ''
}

function releaseTime(release: ProjectRelease): number {
  for (const value of [release.completedAt, release.createdAt]) {
    const parsed = value ? Date.parse(value) : Number.NaN
    if (Number.isFinite(parsed)) return parsed
  }
  return 0
}

export function releaseHasPromotionEvidence(
  release: ProjectRelease | null | undefined,
): release is ProjectRelease & { releaseID: string } {
  return Boolean(release?.deployable && clean(release.commitSHA) && clean(release.releaseID))
}

/** Keep the newest release first without changing the server order of ties. */
export function orderReleases(releases: readonly ProjectRelease[]): ProjectRelease[] {
  return releases
    .map((release, index) => ({ release, index }))
    .sort((left, right) => releaseTime(right.release) - releaseTime(left.release) || left.index - right.index)
    .map(({ release }) => release)
}

export function newestDeployableRelease(releases: readonly ProjectRelease[]): ProjectRelease | null {
  return orderReleases(releases).find(releaseHasPromotionEvidence) ?? null
}

/**
 * Preserve a user's selected release through refreshes. A project transition
 * or a removed release intentionally falls back to the newest deployable one.
 */
export function reconcileReleaseSelection(
  previousCommitSHA: string | null | undefined,
  releases: readonly ProjectRelease[],
  projectChanged = false,
): string {
  const previous = clean(previousCommitSHA)
  if (!projectChanged && previous && releases.some((release) => clean(release.commitSHA) === previous && releaseHasPromotionEvidence(release))) {
    return previous
  }
  return clean(newestDeployableRelease(releases)?.commitSHA)
}

export function selectedRelease(
  releases: readonly ProjectRelease[],
  commitSHA: string | null | undefined,
): ProjectRelease | null {
  const selected = clean(commitSHA)
  return releases.find((release) => clean(release.commitSHA) === selected) ?? null
}

/** Resolve the next keyboard-selectable release using radio-group wrapping. */
export function adjacentDeployableRelease(
  releases: readonly ProjectRelease[],
  commitSHA: string | null | undefined,
  direction: 'next' | 'previous' | 'first' | 'last',
): ProjectRelease | null {
  const deployable = orderReleases(releases).filter(releaseHasPromotionEvidence)
  if (deployable.length === 0) return null
  if (direction === 'first') return deployable[0] ?? null
  if (direction === 'last') return deployable[deployable.length - 1] ?? null
  const currentIndex = deployable.findIndex((release) => clean(release.commitSHA) === clean(commitSHA))
  const start = currentIndex >= 0 ? currentIndex : 0
  const offset = direction === 'next' ? 1 : -1
  return deployable[(start + offset + deployable.length) % deployable.length] ?? null
}

export function releaseMissingEvidence(release: ProjectRelease): string[] {
  const missing = (release.missing ?? []).map(clean).filter(Boolean)
  if (missing.length > 0) return [...new Set(missing)]
  return [...new Set((release.components ?? []).filter((component) => !component.built).map((component) => clean(component.name)).filter(Boolean))]
}

export function releaseActionKind(
  release: ProjectRelease | null | undefined,
  releases: readonly ProjectRelease[],
): ReleaseActionKind {
  if (release?.live) return 'redeploy'
  const ordered = orderReleases(releases)
  const selectedIndex = release ? ordered.findIndex((candidate) => candidate.commitSHA === release.commitSHA) : -1
  const liveIndex = ordered.findIndex((candidate) => candidate.live)
  // A historical release is one that is older than the currently configured
  // release. A newer deployable release is a normal deployment, even while an
  // older release remains current.
  if (selectedIndex >= 0 && liveIndex >= 0 && selectedIndex > liveIndex) return 'rollback'
  return 'deploy'
}

export function releaseActionLabel(
  release: ProjectRelease | null | undefined,
  releases: readonly ProjectRelease[],
): string {
  switch (releaseActionKind(release, releases)) {
    case 'redeploy':
      return 'Redeploy current release'
    case 'rollback':
      return 'Roll back to this release'
    default:
      return 'Deploy selected release'
  }
}

export function formatReleaseDate(value: string | null | undefined): string {
  const parsed = value ? Date.parse(value) : Number.NaN
  if (!Number.isFinite(parsed)) return ''
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(parsed)
}

/** Compact history age, matching the timeline's conversational metadata. */
export function formatReleaseAge(value: string | null | undefined, now = Date.now()): string {
  const parsed = value ? Date.parse(value) : Number.NaN
  if (!Number.isFinite(parsed)) return ''
  const elapsedSeconds = Math.round((parsed - now) / 1000)
  const absoluteSeconds = Math.abs(elapsedSeconds)
  if (absoluteSeconds < 45) return 'just now'
  const ranges: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ['year', 365 * 24 * 60 * 60],
    ['month', 30 * 24 * 60 * 60],
    ['day', 24 * 60 * 60],
    ['hour', 60 * 60],
    ['minute', 60],
  ]
  const [unit, seconds] = ranges.find(([, threshold]) => absoluteSeconds >= threshold) ?? ['second', 1]
  return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(Math.round(elapsedSeconds / seconds), unit)
}
