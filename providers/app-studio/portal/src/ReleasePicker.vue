<script setup lang="ts">
import { computed, nextTick } from 'vue'
import { CircleAlert, Loader2, RefreshCw } from 'lucide-vue-next'

import type { ProjectRelease } from './types'
import {
  adjacentDeployableRelease,
  formatReleaseAge,
  formatReleaseDate,
  orderReleases,
  releaseActionLabel,
  releaseHasPromotionEvidence,
  releaseMissingEvidence,
  selectedRelease,
} from './releaseSelection'

export type ReleaseLoadState = 'idle' | 'loading' | 'ready' | 'error'

const props = withDefaults(defineProps<{
  releases: ProjectRelease[]
  selectedCommit: string
  loadState: ReleaseLoadState
  error?: string | null
  refreshing?: boolean
  actionBusy?: boolean
  actionDisabled?: boolean
  actionDisabledReason?: string
}>(), {
  error: null,
  refreshing: false,
  actionBusy: false,
  actionDisabled: false,
  actionDisabledReason: '',
})

const emit = defineEmits<{
  select: [commitSHA: string]
  retry: []
  refresh: []
  promote: []
}>()

const orderedReleases = computed(() => orderReleases(props.releases))
const selected = computed(() => selectedRelease(orderedReleases.value, props.selectedCommit))
const actionLabel = computed(() => releaseActionLabel(selected.value, orderedReleases.value))
const actionDisabled = computed(() => Boolean(
  props.actionDisabled ||
  props.actionBusy ||
  !releaseHasPromotionEvidence(selected.value),
))

function releaseDate(release: ProjectRelease): string {
  return formatReleaseDate(release.completedAt || release.createdAt)
}

function releaseAge(release: ProjectRelease): string {
  return formatReleaseAge(release.completedAt || release.createdAt)
}

function shortCommitSHA(commitSHA: string): string {
  const normalized = commitSHA.trim()
  return normalized ? normalized.slice(0, 7) : 'unknown'
}

function selectRelease(release: ProjectRelease) {
  if (!releaseHasPromotionEvidence(release) || props.actionBusy) return
  emit('select', release.commitSHA)
}

function releaseOptionID(release: ProjectRelease): string {
  return `app-studio-release-${release.commitSHA.replace(/[^a-zA-Z0-9_-]/g, '-')}`
}

function moveReleaseSelection(direction: 'next' | 'previous' | 'first' | 'last') {
  if (props.actionBusy) return
  const release = adjacentDeployableRelease(orderedReleases.value, props.selectedCommit, direction)
  if (!release) return
  emit('select', release.commitSHA)
  void nextTick(() => document.getElementById(releaseOptionID(release))?.focus())
}
</script>

<template>
  <section
    class="grid gap-3 rounded-lg border border-border-subtle bg-surface p-3"
    aria-label="Release history"
    :aria-busy="loadState === 'loading' || refreshing"
  >
    <header class="flex flex-wrap items-start justify-between gap-3 border-b border-border-subtle pb-3">
      <div class="min-w-0">
        <h3 class="text-[13px] font-semibold text-text-primary">Release history</h3>
        <p class="mt-1 text-[11px] leading-4 text-text-muted">Choose a deployable release to update production images. Settings and access stay unchanged.</p>
      </div>
      <button
        type="button"
        class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md border border-border-subtle bg-surface-overlay px-2.5 text-[11px] font-medium text-text-secondary transition hover:bg-surface-hover hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-60"
        :disabled="refreshing || actionBusy"
        :aria-label="refreshing ? 'Refreshing releases' : 'Refresh releases'"
        @click="emit('refresh')"
      >
        <Loader2 v-if="refreshing" class="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" :stroke-width="1.75" aria-hidden="true" />
        <RefreshCw v-else class="h-3.5 w-3.5" :stroke-width="1.75" aria-hidden="true" />
        {{ refreshing ? 'Refreshing…' : 'Refresh' }}
      </button>
    </header>

    <div v-if="loadState === 'loading' && releases.length === 0" class="grid gap-2 border-y border-border-subtle py-3" role="status" aria-live="polite" aria-busy="true">
      <div class="shimmer h-4 w-40 rounded bg-surface-hover motion-reduce:animate-none" aria-hidden="true" />
      <div class="shimmer h-12 w-full rounded-sm bg-surface-hover motion-reduce:animate-none" aria-hidden="true" />
      <div class="shimmer h-12 w-full rounded-sm bg-surface-hover motion-reduce:animate-none" aria-hidden="true" />
      <p class="text-[11px] text-text-muted">Loading release history…</p>
    </div>

    <div v-else-if="loadState === 'error' && releases.length === 0" class="grid gap-2 border-y border-danger/30 py-3 text-[12px] text-danger" role="alert">
      <div class="flex items-start gap-2">
        <CircleAlert class="mt-0.5 h-4 w-4 shrink-0" :stroke-width="1.75" aria-hidden="true" />
        <span>{{ error || 'Release history is unavailable.' }}</span>
      </div>
      <button type="button" class="justify-self-start font-medium underline underline-offset-2" :disabled="refreshing" @click="emit('retry')">Retry</button>
    </div>

    <div v-else-if="loadState === 'ready' && releases.length === 0" class="grid gap-1 border-y border-dashed border-border-subtle py-4" role="status" aria-live="polite">
      <p class="text-[12px] font-medium text-text-secondary">No releases yet</p>
      <p class="text-[11px] leading-4 text-text-muted">Commit the project and wait for a deployable build before deploying.</p>
    </div>

    <div v-else class="grid gap-2">
      <div v-if="error" class="flex flex-wrap items-center justify-between gap-2 border-y border-warning/30 py-2 text-[11px] text-warning" role="alert">
        <span>{{ error }} Showing the last loaded release history.</span>
        <button type="button" class="font-medium underline underline-offset-2" :disabled="refreshing" @click="emit('retry')">Retry</button>
      </div>

      <div id="app-studio-release-selection-help" class="sr-only">Select one deployable release. Incomplete releases are shown for evidence but cannot be deployed.</div>
      <div
        class="relative"
        role="radiogroup"
        aria-orientation="vertical"
        aria-label="Available releases"
        aria-describedby="app-studio-release-selection-help"
      >
        <div class="pointer-events-none absolute bottom-1.5 left-[5px] top-1.5 w-px bg-border-subtle" aria-hidden="true" />
        <div v-for="release in orderedReleases" :key="release.commitSHA" class="relative grid grid-cols-[12px_minmax(0,1fr)] gap-3 pb-3 last:pb-0">
          <div class="relative flex justify-center">
            <span
              class="relative z-10 mt-1 h-2.5 w-2.5 shrink-0 rounded-full border-2 bg-surface"
              :class="release.live ? 'border-success bg-success' : 'border-border-default'"
              aria-hidden="true"
            />
          </div>
          <div class="min-w-0">
            <button
              :id="releaseOptionID(release)"
              type="button"
              role="radio"
              class="group grid w-full gap-1 rounded-md border px-2 py-1.5 text-left transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50"
              :class="releaseHasPromotionEvidence(release)
                ? selectedCommit === release.commitSHA
                  ? 'border-accent/50 bg-accent-subtle/20 text-text-primary'
                  : 'border-transparent text-text-secondary hover:border-border-subtle hover:bg-surface-hover/40 hover:text-text-primary'
                : 'cursor-not-allowed border-transparent text-text-muted opacity-75'"
              :aria-checked="releaseHasPromotionEvidence(release) && selectedCommit === release.commitSHA"
              :aria-disabled="!releaseHasPromotionEvidence(release)"
              :tabindex="releaseHasPromotionEvidence(release) && selectedCommit === release.commitSHA ? 0 : -1"
              :disabled="!releaseHasPromotionEvidence(release) || actionBusy"
              @click="selectRelease(release)"
              @keydown.left.prevent="moveReleaseSelection('previous')"
              @keydown.up.prevent="moveReleaseSelection('previous')"
              @keydown.right.prevent="moveReleaseSelection('next')"
              @keydown.down.prevent="moveReleaseSelection('next')"
              @keydown.home.prevent="moveReleaseSelection('first')"
              @keydown.end.prevent="moveReleaseSelection('last')"
            >
              <span class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <span class="min-w-0 flex-1 truncate text-[13px] font-semibold text-text-primary" :title="release.message || 'No commit message'">{{ release.message || 'No commit message' }}</span>
                <span v-if="release.live" class="font-mono text-[10px] font-medium uppercase tracking-wide text-success" aria-label="Currently configured production images" title="Currently configured production images">Current production</span>
                <span v-else-if="!releaseHasPromotionEvidence(release)" class="font-mono text-[10px] font-medium uppercase tracking-wide text-warning" aria-label="Incomplete release">Incomplete</span>
              </span>
              <span class="flex flex-wrap items-center gap-x-2 gap-y-1 text-[10px] text-text-muted">
                <code class="font-mono text-text-secondary" :title="`Full commit SHA: ${release.commitSHA}`">{{ shortCommitSHA(release.commitSHA) }}</code>
                <span v-if="releaseAge(release)" aria-hidden="true">·</span>
                <time v-if="releaseAge(release)" class="shrink-0" :datetime="release.completedAt || release.createdAt" :title="releaseDate(release)">{{ releaseAge(release) }}</time>
              </span>
              <span v-if="!releaseHasPromotionEvidence(release)" class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-warning">Missing: {{ releaseMissingEvidence(release).join(', ') || 'immutable release evidence' }}</span>
            </button>
            <a v-if="release.commitURL" :href="release.commitURL" target="_blank" rel="noopener noreferrer" class="mt-0.5 inline-flex min-h-7 items-center px-2 text-[11px] font-medium text-text-secondary hover:text-accent hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50">View commit</a>
          </div>
        </div>
      </div>
    </div>

    <footer class="grid gap-2 border-t border-border-subtle pt-3">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="min-w-0">
          <p class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Selected release</p>
          <p class="mt-0.5 truncate font-mono text-[12px] text-text-primary" :title="selected?.commitSHA || undefined">{{ selected?.commitSHA || 'No deployable release selected' }}</p>
        </div>
        <button
          type="button"
          class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md border border-accent bg-accent px-3 text-[12px] font-semibold text-surface shadow-[0_0_16px_var(--color-accent-glow)] transition hover:bg-accent-hover hover:shadow-[0_0_22px_var(--color-accent-glow)] disabled:cursor-not-allowed disabled:opacity-60 disabled:shadow-none"
          :disabled="actionDisabled"
          :aria-label="actionLabel"
          @click="emit('promote')"
        >
          <Loader2 v-if="actionBusy" class="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" :stroke-width="1.75" aria-hidden="true" />
          {{ actionBusy ? 'Deploying…' : actionLabel }}
        </button>
      </div>
      <p v-if="actionDisabledReason" class="text-[11px] leading-4 text-text-muted" role="status">{{ actionDisabledReason }}</p>
      <p v-else class="text-[11px] leading-4 text-text-muted">Production settings and access are unchanged.</p>
    </footer>
  </section>
</template>
