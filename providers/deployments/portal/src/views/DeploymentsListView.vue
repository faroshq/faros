<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { listDeployments } from '../api'
import { conditionIsTrue, evidenceLabel, evidenceTone, evidenceState } from '../mapper'
import ResourceTable from '../portalkit/ResourceTable.vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import { beginRead, completeRead, failRead, initialReadState, readErrorMessage } from '../state'
import type { DeploymentSnapshot, FarosContext } from '../types'

const props = defineProps<{ tenant?: FarosContext['tenant'] }>()
const emit = defineEmits<{ open: [name: string] }>()

const read = ref(initialReadState<DeploymentSnapshot[]>([]))
let requestSerial = 0
let inFlight: Promise<void> | null = null
const POLL_INTERVAL_MS = 10_000
let pollTimer: number | undefined

const rows = computed(() => read.value.data.map(deployment => {
  const state = evidenceState(deployment)
  return {
    name: deployment.name,
    release: deployment.releaseRef,
    rollout: `${deployment.rolloutID} / ${deployment.observedRolloutID ?? '—'}`,
    applied: conditionIsTrue(deployment, 'Applied') ? 'Applied' : 'Not applied',
    ready: conditionIsTrue(deployment, 'Ready') ? 'Ready' : 'Not ready',
    phase: deployment.phase || 'Unknown',
    state: evidenceLabel(state),
    tone: evidenceTone(state),
  }
}))

const loaded = computed(() => read.value.phase === 'loaded' || read.value.phase === 'stale')

async function refresh(): Promise<void> {
  if (inFlight) return inFlight
  const serial = ++requestSerial
  read.value = beginRead(read.value)
  inFlight = (async () => {
    try {
      const result = await listDeployments()
      if (serial === requestSerial) read.value = completeRead(result.items)
    } catch (error) {
      if (serial !== requestSerial) return
      const message = readErrorMessage(error, 'Deployments could not be read.')
      const retryable = (error as { retryable?: boolean }).retryable !== false
      read.value = failRead(read.value, message, retryable)
    } finally {
      inFlight = null
    }
  })()
  return inFlight
}

onMounted(() => {
  pollTimer = window.setInterval(() => { void refresh() }, POLL_INTERVAL_MS)
})

onBeforeUnmount(() => {
  if (pollTimer === undefined) return
  window.clearInterval(pollTimer)
  pollTimer = undefined
})

watch(() => props.tenant, () => {
  requestSerial++
  inFlight = null
  read.value = initialReadState<DeploymentSnapshot[]>([])
  void refresh()
}, { immediate: true })
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <p class="eyebrow">Deployments</p>
        <h1 class="page-title">Runtime evidence</h1>
        <p class="page-meta">Read-only view of immutable Release intent and observed Infrastructure state.</p>
      </div>
      <button
        class="button ghost"
        type="button"
        :disabled="read.loading"
        :aria-busy="read.loading"
        @click="refresh"
      >
        <RefreshCw :size="14" :class="{ spinning: read.loading }" aria-hidden="true" />
        {{ read.loading ? 'Refreshing…' : 'Refresh' }}
      </button>
    </header>

    <div class="read-contract" role="note">
      <span class="read-contract-dot" aria-hidden="true" />
      <span>Deployments observes evidence only. Release, review, and rollout mutations remain owned by their source providers.</span>
    </div>

    <p v-if="!loaded && read.loading" class="read-status" role="status">Loading deployments…</p>

    <ResourceTable
      :columns="[
        { key: 'name', label: 'Deployment' },
        { key: 'release', label: 'Desired release' },
        { key: 'rollout', label: 'Rollout desired / observed' },
        { key: 'applied', label: 'Applied' },
        { key: 'ready', label: 'Ready' },
        { key: 'phase', label: 'Backend phase' },
        { key: 'state', label: 'Evidence' },
      ]"
      :rows="rows"
      row-key="name"
      :loaded="loaded"
      :loading="read.loading"
      :error="read.error"
      :stale="read.phase === 'stale'"
      :retryable="read.retryable"
      empty-text="No Deployments have been projected into this workspace."
      @row-click="row => emit('open', String(row.name))"
      @retry="refresh"
    >
      <template #name="{ value }">
        <button
          class="deployment-name-trigger"
          type="button"
          :aria-label="`Open deployment ${String(value)}`"
          @click.stop="emit('open', String(value))"
        >
          <span class="mono link-text">{{ value }}</span>
        </button>
      </template>
      <template #release="{ value }"><span class="mono">{{ value }}</span></template>
      <template #rollout="{ value }"><span class="mono rollout-value">{{ value }}</span></template>
      <template #applied="{ value }">
        <StatusBadge :status="String(value)" tone="warning" />
      </template>
      <template #ready="{ value }">
        <StatusBadge :status="String(value)" :tone="value === 'Ready' ? 'success' : 'warning'" />
      </template>
      <template #phase="{ value }"><span class="mono">{{ value }}</span></template>
      <template #state="{ value, row }">
        <StatusBadge :status="String(value)" :tone="row.tone as 'success' | 'warning' | 'danger' | 'muted'" />
      </template>
    </ResourceTable>
  </section>
</template>
