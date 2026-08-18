<script setup lang="ts">
import { CircleAlert, Check, Loader2, RefreshCw, X } from 'lucide-vue-next'

import type { ReleasePipelineView } from './promotionState'

defineProps<{
  pipeline: ReleasePipelineView
  takingLonger?: boolean
  needsAttention?: boolean
  refreshing?: boolean
}>()

const emit = defineEmits<{
  refresh: []
}>()
</script>

<template>
  <section
    class="grid gap-3"
    aria-label="Release pipeline"
  >
    <ol class="grid gap-2 sm:grid-cols-5">
      <li
        v-for="step in pipeline.steps"
        :key="step.key"
        class="grid grid-cols-[18px_minmax(0,1fr)] items-start gap-2 border-l border-border-subtle pl-2 first:border-l-0 first:pl-0 sm:grid-cols-1 sm:border-l-0 sm:border-t sm:pt-2"
        :class="step.state === 'error' ? 'text-danger' : step.state === 'done' ? 'text-success' : ['current', 'attention'].includes(step.state) ? 'text-warning' : 'text-text-muted'"
      >
        <span class="flex h-[18px] w-[18px] items-center justify-center" aria-hidden="true">
          <Check v-if="step.state === 'done'" class="h-3.5 w-3.5" :stroke-width="2" />
          <X v-else-if="step.state === 'error'" class="h-3.5 w-3.5" :stroke-width="2" />
          <CircleAlert v-else-if="step.state === 'attention'" class="h-3.5 w-3.5" :stroke-width="1.75" />
          <Loader2 v-else-if="step.state === 'current' && pipeline.transitional && !needsAttention" class="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" :stroke-width="1.75" />
          <span v-else class="h-1.5 w-1.5 rounded-full bg-current" />
        </span>
        <span class="min-w-0">
          <span class="block text-[10px] font-semibold uppercase tracking-wide">{{ step.label }}</span>
          <span v-if="step.detail" class="block truncate font-mono text-[10px] text-text-muted" :title="step.detail">{{ step.detail }}</span>
        </span>
      </li>
    </ol>

    <div
      class="grid gap-1 border-t border-border-subtle pt-3"
      :role="pipeline.state === 'failed' ? 'alert' : 'status'"
      :aria-live="pipeline.state === 'failed' ? 'assertive' : 'polite'"
      aria-atomic="true"
    >
      <div class="flex flex-wrap items-start justify-between gap-2">
        <div class="min-w-0">
          <p class="text-[13px] font-medium" :class="pipeline.tone === 'danger' ? 'text-danger' : pipeline.tone === 'success' ? 'text-success' : pipeline.tone === 'warning' ? 'text-warning' : 'text-text-primary'">
            {{ pipeline.message }}
          </p>
          <p class="mt-0.5 text-[11px] leading-4 text-text-muted">
            {{ takingLonger && pipeline.transitional ? 'Taking longer than usual. ' : '' }}{{ pipeline.detail }}
          </p>
          <p v-if="pipeline.requestedRevision || pipeline.observedRevision" class="mt-1 font-mono text-[10px] text-text-muted">
            Requested revision: {{ pipeline.requestedRevision || '—' }} · Current observed: {{ pipeline.observedRevision || '—' }}
          </p>
        </div>
        <div class="flex shrink-0 items-center gap-3">
          <button
            v-if="pipeline.artifactLag || ['artifact_attention', 'unavailable'].includes(pipeline.state)"
            type="button"
            class="inline-flex h-8 items-center gap-1.5 rounded-md border border-border-subtle bg-surface px-3 text-[11px] font-medium text-text-secondary transition hover:bg-surface-hover hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-60"
            :disabled="refreshing"
            @click="emit('refresh')"
          >
            <RefreshCw class="h-3.5 w-3.5" :class="refreshing ? 'animate-spin motion-reduce:animate-none' : ''" :stroke-width="1.75" aria-hidden="true" />
            {{ refreshing ? 'Checking…' : 'Check again' }}
          </button>
          <a
            v-if="pipeline.buildURL"
            :href="pipeline.buildURL"
            target="_blank"
            rel="noopener noreferrer"
            class="text-[11px] font-medium text-accent hover:underline"
          >View build</a>
        </div>
      </div>
      <p
        v-if="pipeline.missing.length && !['needs_commit', 'failed', 'finalizing', 'artifact_attention', 'unavailable'].includes(pipeline.state)"
        class="font-mono text-[10px] text-text-muted"
      >
        Missing: {{ pipeline.missing.join(', ') }}
      </p>
    </div>

    <div
      v-if="pipeline.artifacts.length && (pipeline.artifactLag || ['artifact_attention', 'unavailable'].includes(pipeline.state))"
      class="grid gap-2 border-t border-border-subtle pt-3"
      aria-label="Release image evidence"
    >
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">Release image evidence</p>
        <p class="mt-0.5 text-[11px] leading-4 text-text-muted">Deployment requires an image matching the full commit tag for every component.</p>
      </div>
      <dl class="grid gap-2">
        <div
          v-for="artifact in pipeline.artifacts"
          :key="artifact.component"
          class="grid gap-2 rounded-lg border border-border-subtle bg-surface px-3 py-2 sm:grid-cols-[minmax(7rem,0.7fr)_minmax(0,1.3fr)_minmax(0,1fr)]"
        >
          <div class="min-w-0">
            <dt class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">Component</dt>
            <dd class="mt-0.5 flex items-center gap-1.5 font-mono text-[11px] text-text-primary">
              <Check v-if="artifact.verified" class="h-3.5 w-3.5 shrink-0 text-success" :stroke-width="2" aria-hidden="true" />
              <CircleAlert v-else class="h-3.5 w-3.5 shrink-0 text-warning" :stroke-width="1.75" aria-hidden="true" />
              <span class="truncate">{{ artifact.component }}</span>
            </dd>
          </div>
          <div class="min-w-0">
            <dt class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">Expected package and tag</dt>
            <dd class="mt-0.5 break-all font-mono text-[10px] leading-4 text-text-secondary">{{ artifact.packageMatcher }} · {{ artifact.expectedTag || 'No commit tag yet' }}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">Observed</dt>
            <dd class="mt-0.5 break-all font-mono text-[10px] leading-4" :class="artifact.verified ? 'text-success' : 'text-text-muted'">
              <template v-if="artifact.observedTag || artifact.digest">{{ artifact.observedTag || 'tag unavailable' }}<br v-if="artifact.observedTag && artifact.digest">{{ artifact.digest }}</template>
              <template v-else>Not observed</template>
            </dd>
          </div>
        </div>
      </dl>
    </div>
  </section>
</template>
