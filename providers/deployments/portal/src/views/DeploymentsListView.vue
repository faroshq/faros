<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { listRepositorySyncs } from '../api'
import { evidenceLabel, evidenceTone, syncEvidenceState } from '../mapper'
import ResourceTable from '../portalkit/ResourceTable.vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import { beginRead, completeRead, failRead, initialReadState, readErrorMessage } from '../state'
import type { FarosContext, RepositorySyncSnapshot } from '../types'

const props = defineProps<{ tenant?: FarosContext['tenant'] }>()
const emit = defineEmits<{ open: [name: string] }>()

const read = ref(initialReadState<RepositorySyncSnapshot[]>([]))
let requestSerial = 0
let inFlight: Promise<void> | null = null
const POLL_INTERVAL_MS = 10_000
let pollTimer: number | undefined

const rows = computed(() => read.value.data.map(sync => {
  const state = syncEvidenceState(sync)
  return {
    name: sync.name,
    repository: sync.repositoryRef,
    source: `${sync.ref || 'default'} / ${sync.path || '.faros'}`,
    revision: sync.appliedRevision || sync.observedRevision || '—',
    targets: sync.targetRequirements.length || sync.inventory.length,
    phase: evidenceLabel(state),
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
      const result = await listRepositorySyncs()
      if (serial === requestSerial) read.value = completeRead(result.items)
    } catch (error) {
      if (serial !== requestSerial) return
      read.value = failRead(
        read.value,
        readErrorMessage(error, 'Repository syncs could not be read.'),
        (error as { retryable?: boolean }).retryable !== false,
      )
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
  if (pollTimer !== undefined) window.clearInterval(pollTimer)
})

watch(() => props.tenant, () => {
  requestSerial++
  inFlight = null
  read.value = initialReadState<RepositorySyncSnapshot[]>([])
  void refresh()
}, { immediate: true })
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <p class="eyebrow">Deployments</p>
        <h1 class="page-title">Repository syncs</h1>
        <p class="page-meta">Git revisions projected into this workspace. Target providers own runtime readiness.</p>
      </div>
      <button class="button ghost" type="button" :disabled="read.loading" :aria-busy="read.loading" @click="refresh">
        <RefreshCw :size="14" :class="{ spinning: read.loading }" aria-hidden="true" />
        {{ read.loading ? 'Refreshing…' : 'Refresh' }}
      </button>
    </header>

    <div class="read-contract" role="note">
      <span class="read-contract-dot" aria-hidden="true" />
      <span>Applied means the desired objects were synchronized. It does not assert that their workloads are healthy.</span>
    </div>

    <p v-if="!loaded && read.loading" class="read-status" role="status">Loading repository syncs…</p>

    <ResourceTable
      :columns="[
        { key: 'name', label: 'Sync' },
        { key: 'repository', label: 'Repository' },
        { key: 'source', label: 'Ref / path' },
        { key: 'revision', label: 'Applied revision' },
        { key: 'targets', label: 'Targets' },
        { key: 'phase', label: 'Sync state' },
      ]"
      :rows="rows"
      row-key="name"
      :loaded="loaded"
      :loading="read.loading"
      :error="read.error"
      :stale="read.phase === 'stale'"
      :retryable="read.retryable"
      empty-text="No repository syncs are configured in this workspace."
      @row-click="row => emit('open', String(row.name))"
      @retry="refresh"
    >
      <template #name="{ value }">
        <button class="deployment-name-trigger" type="button" :aria-label="`Open repository sync ${String(value)}`" @click.stop="emit('open', String(value))">
          <span class="mono link-text">{{ value }}</span>
        </button>
      </template>
      <template #repository="{ value }"><span class="mono">{{ value }}</span></template>
      <template #source="{ value }"><span class="mono">{{ value }}</span></template>
      <template #revision="{ value }"><span class="mono breakable">{{ value }}</span></template>
      <template #targets="{ value }"><span class="mono">{{ value }}</span></template>
      <template #phase="{ value, row }">
        <StatusBadge :status="String(value)" :tone="row.tone as 'success' | 'warning' | 'danger' | 'muted'" />
      </template>
    </ResourceTable>
  </section>
</template>
