<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ArrowLeft, Copy, RefreshCw, ShieldCheck } from 'lucide-vue-next'
import { copyText, getRepositorySync } from '../api'
import { evidenceLabel, evidenceTone, isCurrentEvidence, syncEvidenceState } from '../mapper'
import ConditionsPanel from '../portalkit/ConditionsPanel.vue'
import ResourceTable from '../portalkit/ResourceTable.vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import { beginRead, completeRead, failRead, initialReadState, readErrorMessage } from '../state'
import type { FarosContext, RepositorySyncSnapshot, SyncClaimReference, SyncCondition } from '../types'

const props = defineProps<{ name: string; tenant?: FarosContext['tenant'] }>()
const emit = defineEmits<{ back: []; authorize: [claims: SyncClaimReference[]] }>()

const read = ref(initialReadState<RepositorySyncSnapshot | null>(null))
const copied = ref(false)
let requestSerial = 0
let inFlight: Promise<void> | null = null
const POLL_INTERVAL_MS = 10_000
let pollTimer: number | undefined

const sync = computed(() => read.value.data)
const state = computed(() => sync.value ? syncEvidenceState(sync.value) : 'unknown')
const loaded = computed(() => (read.value.phase === 'loaded' || read.value.phase === 'stale') && sync.value !== null)
const missingClaims = computed(() => {
  const unique = new Map<string, SyncClaimReference>()
  for (const requirement of sync.value?.targetRequirements ?? []) {
    if (requirement.state.toLowerCase() !== 'awaitingauthorization' || !requirement.claim) continue
    unique.set(`${requirement.claim.group}/${requirement.claim.resource}`, requirement.claim)
  }
  return [...unique.values()]
})
const inventoryRows = computed(() => (sync.value?.inventory ?? []).map(item => ({
  key: `${item.apiVersion}/${item.resource}/${item.namespace || ''}/${item.name}`,
  identity: `${item.apiVersion}/${item.resource}`,
  kind: item.kind,
  location: item.namespace ? `${item.namespace}/${item.name}` : item.name,
  source: item.sourcePath || '—',
  uid: item.uid || '—',
})))
const requirementRows = computed(() => (sync.value?.targetRequirements ?? []).map(item => ({
  target: `${item.apiVersion}/${item.kind}`,
  resource: item.resource,
  state: item.state,
  message: item.message || '—',
})))

function currentCondition(type: string): SyncCondition | undefined {
  return sync.value?.conditions.find(condition => condition.type.toLowerCase() === type.toLowerCase())
}

function conditionLabel(type: string): string {
  const current = sync.value
  const condition = currentCondition(type)
  if (!current || !condition) return 'Not observed'
  if (!isCurrentEvidence(current, condition)) return 'Stale'
  return condition.status === 'True' ? 'Complete' : condition.status === 'False' ? 'Blocked' : 'Unknown'
}

function conditionTone(type: string): 'success' | 'warning' | 'danger' | 'muted' {
  const current = sync.value
  const condition = currentCondition(type)
  if (!current || !condition || !isCurrentEvidence(current, condition)) return 'muted'
  if (condition.status === 'True') return 'success'
  return type === 'AuthorizationReady' ? 'warning' : 'danger'
}

async function copyRevision(): Promise<void> {
  const revision = sync.value?.appliedRevision || sync.value?.observedRevision
  if (!revision || !(await copyText(revision))) return
  copied.value = true
  window.setTimeout(() => { copied.value = false }, 1800)
}

async function refresh(): Promise<void> {
  if (inFlight) return inFlight
  const serial = ++requestSerial
  read.value = beginRead(read.value)
  inFlight = (async () => {
    try {
      const result = await getRepositorySync(props.name)
      if (serial === requestSerial) read.value = completeRead(result)
    } catch (error) {
      if (serial !== requestSerial) return
      read.value = failRead(
        read.value,
        readErrorMessage(error, 'Repository sync could not be read.'),
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

watch(() => [props.name, props.tenant], () => {
  requestSerial++
  inFlight = null
  read.value = initialReadState<RepositorySyncSnapshot | null>(null)
  void refresh()
}, { immediate: true })
</script>

<template>
  <section class="page detail-page">
    <header class="page-head detail-head">
      <div>
        <button class="button text-button" type="button" @click="emit('back')">
          <ArrowLeft :size="14" aria-hidden="true" /> Back to repository syncs
        </button>
        <p class="eyebrow">Repository sync</p>
        <h1 class="page-title mono">{{ name }}</h1>
        <p class="page-meta">Source, authorization, and apply evidence for one Git revision.</p>
      </div>
      <div class="detail-actions">
        <button class="button ghost" type="button" :disabled="read.loading" :aria-busy="read.loading" @click="refresh">
          <RefreshCw :size="14" :class="{ spinning: read.loading }" aria-hidden="true" />
          {{ read.loading ? 'Refreshing…' : 'Refresh' }}
        </button>
      </div>
    </header>

    <div v-if="read.phase === 'stale'" class="state-card stale-card" role="alert">
      <strong>Showing the last successful result.</strong><span>{{ read.error }}</span>
      <button v-if="read.retryable" class="button text-button" type="button" @click="refresh">Retry</button>
    </div>
    <div v-if="read.phase === 'error'" class="state-card error-card" role="alert">
      <p class="eyebrow">Repository sync unavailable</p><p>{{ read.error }}</p>
      <button v-if="read.retryable" class="button ghost" type="button" @click="refresh">Retry read</button>
    </div>
    <div v-else-if="!loaded" class="detail-skeleton" role="status" aria-label="Loading repository sync evidence">
      <div class="shimmer skeleton-line skeleton-wide" /><div class="detail-grid"><div class="shimmer skeleton-block" /><div class="shimmer skeleton-block" /></div>
    </div>

    <template v-else-if="sync">
      <div class="evidence-banner" :class="`evidence-${state}`" role="status">
        <StatusBadge :status="evidenceLabel(state)" :tone="evidenceTone(state)" />
        <span v-if="state === 'ready'">The observed Git revision is applied. Runtime health remains owned by each target provider.</span>
        <span v-else-if="state === 'awaiting-authorization'">The complete revision is blocked before writes until the requested access is granted.</span>
        <span v-else-if="state === 'pending'">Source, planning, or apply reconciliation is still in progress.</span>
        <span v-else-if="state === 'failed'">The revision could not be applied. Inspect the controller conditions below.</span>
        <span v-else-if="state === 'deleting'">Cleanup is in progress for objects owned by this sync.</span>
        <span v-else>No current synchronization evidence is available.</span>
      </div>

      <div v-if="missingClaims.length" class="authorization-card" role="alert">
        <div>
          <p class="eyebrow">Access required</p>
          <h2 class="panel-title">Authorize {{ missingClaims.length }} target resource {{ missingClaims.length === 1 ? 'type' : 'types' }}</h2>
          <p class="page-meta">The grant is explicit and workspace-scoped. Existing provider grants will be preserved.</p>
        </div>
        <button class="button primary" type="button" @click="emit('authorize', missingClaims)">
          <ShieldCheck :size="14" aria-hidden="true" /> Review access
        </button>
      </div>

      <div class="detail-grid">
        <section class="panel" aria-labelledby="source-heading">
          <p class="eyebrow">Desired state source</p><h2 id="source-heading" class="panel-title">Git directory</h2>
          <dl class="facts">
            <div><dt>Repository</dt><dd class="mono breakable">{{ sync.repositoryRef }}</dd></div>
            <div><dt>Ref</dt><dd class="mono">{{ sync.ref || 'repository default' }}</dd></div>
            <div><dt>Path</dt><dd class="mono">{{ sync.path || '.faros' }}</dd></div>
            <div><dt>Observed revision</dt><dd class="mono breakable">{{ sync.observedRevision || '—' }}</dd></div>
            <div><dt>Applied revision</dt><dd class="mono breakable">{{ sync.appliedRevision || '—' }}</dd></div>
            <div><dt>Prune</dt><dd class="mono">{{ sync.prune ? 'Enabled' : 'Disabled' }}</dd></div>
          </dl>
          <button v-if="sync.appliedRevision || sync.observedRevision" class="button small" type="button" @click="copyRevision">
            <Copy :size="13" aria-hidden="true" /> {{ copied ? 'Copied' : 'Copy revision' }}
          </button>
        </section>

        <section class="panel" aria-labelledby="stages-heading">
          <p class="eyebrow">Convergence</p><h2 id="stages-heading" class="panel-title">Sync stages</h2>
          <div class="condition-summary">
            <div v-for="stage in ['SourceReady', 'AuthorizationReady', 'Applied']" :key="stage" class="condition-summary-item">
              <span class="condition-summary-label">{{ stage.replace('Ready', '') || stage }}</span>
              <StatusBadge :status="conditionLabel(stage)" :tone="conditionTone(stage)" />
            </div>
          </div>
          <p class="muted">Applied is the terminal claim here. Target object status is intentionally not projected as deployment readiness.</p>
        </section>
      </div>

      <section class="panel" aria-labelledby="requirements-heading">
        <p class="eyebrow">Preflight</p><h2 id="requirements-heading" class="panel-title">Target resource access</h2>
        <ResourceTable
          :columns="[
            { key: 'target', label: 'Target type' },
            { key: 'resource', label: 'Resource' },
            { key: 'state', label: 'Access' },
            { key: 'message', label: 'Evidence' },
          ]"
          :rows="requirementRows"
          row-key="target"
          :loaded="true"
          empty-text="No target resources have been planned for this revision."
        >
          <template #target="{ value }"><span class="mono">{{ value }}</span></template>
          <template #resource="{ value }"><span class="mono">{{ value }}</span></template>
          <template #state="{ value }"><StatusBadge :status="String(value)" :tone="String(value).toLowerCase() === 'authorized' ? 'success' : 'warning'" /></template>
          <template #message="{ value }"><span class="muted">{{ value }}</span></template>
        </ResourceTable>
      </section>

      <section class="panel" aria-labelledby="inventory-heading">
        <p class="eyebrow">Applied inventory</p><h2 id="inventory-heading" class="panel-title">Objects owned by this sync</h2>
        <ResourceTable
          :columns="[
            { key: 'identity', label: 'API / resource' },
            { key: 'kind', label: 'Kind' },
            { key: 'location', label: 'Namespace / name' },
            { key: 'source', label: 'Source file' },
            { key: 'uid', label: 'UID' },
          ]"
          :rows="inventoryRows"
          row-key="key"
          :loaded="true"
          empty-text="No objects have been applied for this revision."
        >
          <template #identity="{ value }"><span class="mono">{{ value }}</span></template>
          <template #kind="{ value }"><span class="mono">{{ value }}</span></template>
          <template #location="{ value }"><span class="mono">{{ value }}</span></template>
          <template #source="{ value }"><span class="mono">{{ value }}</span></template>
          <template #uid="{ value }"><span class="mono breakable">{{ value }}</span></template>
        </ResourceTable>
      </section>

      <section class="panel" aria-labelledby="conditions-heading">
        <p class="eyebrow">Controller evidence</p><h2 id="conditions-heading" class="panel-title">Conditions</h2>
        <ConditionsPanel
          :conditions="sync.conditions"
          :generation="sync.generation"
          :observed-generation="sync.observedGeneration"
          empty-text="No synchronization conditions have been observed yet."
        />
      </section>
    </template>
  </section>
</template>
