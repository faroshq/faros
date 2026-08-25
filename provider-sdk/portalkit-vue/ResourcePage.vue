<!--
  CANONICAL SOURCE — provider-sdk/portalkit-vue. Do not edit vendored copies
  under providers/*/portal/src/portalkit/; edit here and run
  `make sync-portalkit`.

  ResourcePage owns page composition and read-state presentation only. It does
  not fetch, route, or decide what the resource body contains. Callers provide
  the snapshot and map `retry` to their own behavior. The optional eyebrow,
  summary, and body slots remain caller-owned.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { AlertCircle } from 'lucide-vue-next'
import { ensureFarosUIStyles } from '../portalkit/styles'
import type { ResourceReadState, ResourceRefreshMode } from '../portalkit/page-state'

ensureFarosUIStyles()

const props = withDefaults(defineProps<{
  title: string
  eyebrow?: string
  subtitle?: string
  loaded?: ResourceReadState['loaded']
  loading?: ResourceReadState['loading']
  refreshMode?: ResourceRefreshMode
  error?: ResourceReadState['error']
  stale?: ResourceReadState['stale']
  retryable?: ResourceReadState['retryable']
}>(), {
  eyebrow: '',
  subtitle: '',
  // A null sentinel preserves the distinction between an omitted read
  // contract and an explicit first read that has not completed.
  loaded: null,
  loading: false,
  refreshMode: 'foreground',
  error: null,
  stale: false,
  retryable: false,
})

const emit = defineEmits<{ retry: [] }>()

const explicitReadState = computed(() => props.loaded !== null)
const showInitialError = computed(() =>
  explicitReadState.value ? props.loaded === false && Boolean(props.error) : Boolean(props.error),
)
const showInitialLoading = computed(() =>
  explicitReadState.value ? props.loaded === false && !props.error : Boolean(props.loading),
)
const ariaBusy = computed(() =>
  explicitReadState.value
    ? (props.loaded === false && !props.error) || (Boolean(props.loading) && props.loaded === true)
    : Boolean(props.loading),
)
</script>

<template>
  <section class="k-resource-page" :aria-busy="ariaBusy">
    <!-- Keep refresh announcements out of layout while preserving the caller-owned body. -->
    <span
      class="k-resource-page__live"
      role="status"
      aria-live="polite"
      aria-atomic="true"
      style="block-size: 1px; clip: rect(0 0 0 0); clip-path: inset(50%); inline-size: 1px; margin: -1px; overflow: hidden; padding: 0; position: absolute; white-space: nowrap;"
    >
      {{ explicitReadState && loading && loaded ? 'Updating…' : '' }}
    </span>
    <header class="k-resource-page__header">
      <div class="k-resource-page__heading">
        <p v-if="eyebrow" class="k-resource-page__eyebrow">{{ eyebrow }}</p>
        <h1 class="k-resource-page__title">{{ title }}</h1>
        <p v-if="subtitle" class="k-resource-page__subtitle">{{ subtitle }}</p>
        <div v-if="$slots.meta" class="k-resource-page__meta"><slot name="meta" /></div>
      </div>
      <div v-if="$slots.status || $slots.actions" class="k-resource-page__header-side">
        <div v-if="$slots.status" class="k-resource-page__status"><slot name="status" /></div>
        <div v-if="$slots.actions" class="k-resource-page__actions"><slot name="actions" /></div>
      </div>
    </header>

    <div v-if="showInitialError" class="k-resource-page__read-error" role="alert" aria-live="assertive">
      <AlertCircle class="k-resource-page__read-icon" :size="16" :stroke-width="1.75" aria-hidden="true" />
      <span class="k-resource-page__read-message">{{ error }}</span>
      <button v-if="retryable" type="button" class="k-btn k-btn--ghost k-resource-page__retry" @click="emit('retry')">Retry</button>
    </div>

    <div v-else-if="showInitialLoading" class="k-resource-page__loading" role="status" aria-live="polite" :aria-label="`Loading ${title}`">
      <div class="shimmer k-resource-page__skeleton k-resource-page__skeleton--short" />
      <div class="shimmer k-resource-page__skeleton k-resource-page__skeleton--wide" />
      <div class="shimmer k-resource-page__skeleton k-resource-page__skeleton--medium" />
    </div>

    <template v-else>
      <div v-if="explicitReadState && error" class="k-resource-page__stale" role="alert" aria-live="assertive">
        <AlertCircle class="k-resource-page__read-icon" :size="16" :stroke-width="1.75" aria-hidden="true" />
        <span class="k-resource-page__read-message">
          {{ stale ? 'Showing the last successful result. ' : '' }}{{ error }}
        </span>
        <button v-if="retryable" type="button" class="k-btn k-btn--ghost k-resource-page__retry" @click="emit('retry')">Retry</button>
      </div>
      <div v-if="$slots.summary" class="k-resource-page__summary">
        <slot name="summary" />
      </div>
      <div class="k-resource-page__body">
        <slot name="body"><slot /></slot>
      </div>
    </template>
  </section>
</template>
