<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ArrowLeft, Copy, ExternalLink, RefreshCw } from 'lucide-vue-next'
import { copyText, followableURL, getDeployment } from '../api'
import { evidenceLabel, evidenceState, evidenceTone, isCurrentEvidence } from '../mapper'
import ConditionsPanel from '../portalkit/ConditionsPanel.vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import { beginRead, completeRead, failRead, initialReadState, readErrorMessage } from '../state'
import type { DeploymentCondition, DeploymentSnapshot, FarosContext } from '../types'

const props = defineProps<{ name: string; tenant?: FarosContext['tenant'] }>()
const emit = defineEmits<{ back: [] }>()

const read = ref(initialReadState<DeploymentSnapshot | null>(null))
const copied = ref<string | null>(null)
let requestSerial = 0
let inFlight: Promise<void> | null = null
const POLL_INTERVAL_MS = 10_000
let pollTimer: number | undefined

const deployment = computed(() => read.value.data)
const state = computed(() => deployment.value ? evidenceState(deployment.value) : 'unknown')
const loaded = computed(() => (read.value.phase === 'loaded' || read.value.phase === 'stale') && deployment.value !== null)
const runtimeURL = computed(() => followableURL(deployment.value?.url))
const rolloutMatches = computed(() => {
  const current = deployment.value
  return !!current && !!current.observedRolloutID && current.observedRolloutID === current.rolloutID && isCurrentEvidence(current)
})

function currentCondition(type: string): DeploymentCondition | undefined {
  return deployment.value?.conditions.find(condition => condition.type.toLowerCase() === type.toLowerCase())
}

function conditionLabel(type: string): string {
  const current = deployment.value
  const item = currentCondition(type)
  if (!current || !item) return 'Unknown'
  if (!isCurrentEvidence(current, item)) return 'Stale evidence'
  return item.status === 'True' ? type : item.status === 'False' ? `Not ${type.toLowerCase()}` : 'Unknown'
}

function conditionTone(type: string): 'success' | 'warning' | 'danger' | 'muted' {
  const current = deployment.value
  const item = currentCondition(type)
  if (!current || !item || !isCurrentEvidence(current, item)) return 'muted'
  if (item.status === 'True') return type.toLowerCase() === 'applied' ? 'warning' : 'success'
  if (current.phase?.toLowerCase() === 'invalid') return 'danger'
  return 'warning'
}

function refText(value: DeploymentSnapshot['backendRef']): string {
  if (!value) return ''
  return `${value.apiVersion}/${value.resource}/${value.name}`
}

async function copyValue(value: string, key: string): Promise<void> {
  if (!(await copyText(value))) return
  copied.value = key
  window.setTimeout(() => {
    if (copied.value === key) copied.value = null
  }, 1800)
}

function openRuntimeURL(): void {
  if (!runtimeURL.value) return
  window.open(runtimeURL.value, '_blank', 'noopener,noreferrer')
}

function followBackendEvidence(): void {
  document.getElementById('backend-evidence')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function refresh(): Promise<void> {
  if (inFlight) return inFlight
  const serial = ++requestSerial
  read.value = beginRead(read.value)
  inFlight = (async () => {
    try {
      const result = await getDeployment(props.name)
      if (serial === requestSerial) read.value = completeRead(result)
    } catch (error) {
      if (serial !== requestSerial) return
      const message = readErrorMessage(error, 'Deployment could not be read.')
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

watch(() => [props.name, props.tenant], () => {
  requestSerial++
  inFlight = null
  read.value = initialReadState<DeploymentSnapshot | null>(null)
  void refresh()
}, { immediate: true })
</script>

<template>
  <section class="page detail-page">
    <header class="page-head detail-head">
      <div>
        <button class="button text-button" type="button" @click="emit('back')">
          <ArrowLeft :size="14" aria-hidden="true" /> Back to Deployments
        </button>
        <p class="eyebrow">Deployment evidence</p>
        <h1 class="page-title mono">{{ name }}</h1>
        <p class="page-meta">Immutable intent, controller observation, and backend evidence.</p>
      </div>
      <div class="detail-actions">
        <button class="button ghost" type="button" :disabled="read.loading" :aria-busy="read.loading" @click="refresh">
          <RefreshCw :size="14" :class="{ spinning: read.loading }" aria-hidden="true" />
          {{ read.loading ? 'Refreshing…' : 'Refresh' }}
        </button>
        <button v-if="loaded && deployment?.backendRef" class="button ghost" type="button" @click="followBackendEvidence">
          Follow backend evidence
        </button>
      </div>
    </header>

    <div v-if="read.phase === 'stale'" class="state-card stale-card" role="alert">
      <strong>Showing the last successful result.</strong>
      <span>{{ read.error }}</span>
      <button v-if="read.retryable" class="button text-button" type="button" @click="refresh">Retry</button>
    </div>

    <div v-if="read.phase === 'error'" class="state-card error-card" role="alert">
      <p class="eyebrow">Deployment unavailable</p>
      <p>{{ read.error }}</p>
      <button v-if="read.retryable" class="button ghost" type="button" @click="refresh">Retry read</button>
    </div>

    <div v-else-if="!loaded" class="detail-skeleton" role="status" aria-label="Loading deployment evidence">
      <div class="shimmer skeleton-line skeleton-wide" />
      <div class="detail-grid">
        <div class="shimmer skeleton-block" />
        <div class="shimmer skeleton-block" />
      </div>
    </div>

    <template v-else-if="deployment">
      <div class="evidence-banner" :class="`evidence-${state}`" role="status">
        <StatusBadge :status="evidenceLabel(state)" :tone="evidenceTone(state)" />
        <span v-if="state === 'ready'">Current Ready evidence is present for this rollout.</span>
        <span v-else-if="state === 'applied'">The backend has applied the desired release; runtime Ready evidence is not current yet.</span>
        <span v-else-if="state === 'pending'">The controller has not produced current Ready evidence.</span>
        <span v-else-if="state === 'invalid'">The desired release or blueprint is invalid; no runtime readiness is claimed.</span>
        <span v-else-if="state === 'deleting'">Deletion is in progress; runtime evidence is being retired.</span>
        <span v-else>Readiness evidence is unavailable or has not been observed.</span>
      </div>

      <div class="detail-grid">
        <section class="panel" aria-labelledby="intent-heading">
          <div class="panel-head">
            <div>
              <p class="eyebrow">Desired state</p>
              <h2 id="intent-heading" class="panel-title">Release intent</h2>
            </div>
            <StatusBadge :status="deployment.release ? 'Available' : 'Unavailable'" :tone="deployment.release ? 'success' : 'warning'" />
          </div>
          <template v-if="deployment.release">
            <dl class="facts">
              <div><dt>Release</dt><dd class="mono">{{ deployment.release.name }}</dd></div>
              <div><dt>Repository</dt><dd class="mono breakable">{{ deployment.release.repositoryRef }}</dd></div>
              <div><dt>Revision</dt><dd class="mono breakable">{{ deployment.release.revision }}</dd></div>
              <div><dt>Blueprint</dt><dd class="mono">{{ deployment.release.blueprint }}</dd></div>
            </dl>
            <div class="copy-row">
              <button class="button small" type="button" @click="copyValue(deployment.release.revision, 'revision')">
                <Copy :size="13" aria-hidden="true" /> {{ copied === 'revision' ? 'Copied' : 'Copy revision' }}
              </button>
            </div>
            <h3 class="subheading">Immutable artifacts</h3>
            <ul v-if="deployment.release.artifacts.length" class="artifact-list">
              <li v-for="artifact in deployment.release.artifacts" :key="artifact.name">
                <span class="mono">{{ artifact.name }}</span>
                <span class="mono breakable">{{ artifact.image }}</span>
              </li>
            </ul>
            <p v-else class="muted">No artifacts were declared in this Release.</p>
          </template>
          <div v-else class="state-card warning-card">
            <strong>Release intent unavailable.</strong>
            <span>The referenced Release <code>{{ deployment.releaseRef }}</code> is not readable in this workspace yet.</span>
          </div>
        </section>

        <section class="panel" aria-labelledby="rollout-heading">
          <div class="panel-head">
            <div>
              <p class="eyebrow">Desired / observed</p>
              <h2 id="rollout-heading" class="panel-title">Rollout</h2>
            </div>
            <StatusBadge :status="rolloutMatches ? 'Current' : 'Not current'" :tone="rolloutMatches ? 'success' : 'warning'" />
          </div>
          <dl class="facts">
            <div><dt>Desired release</dt><dd class="mono">{{ deployment.releaseRef }}</dd></div>
            <div><dt>Active release</dt><dd class="mono">{{ deployment.activeReleaseRef || '—' }}</dd></div>
            <div><dt>Last successful</dt><dd class="mono">{{ deployment.lastSuccessfulReleaseRef || '—' }}</dd></div>
            <div><dt>Desired rollout ID</dt><dd class="mono breakable">{{ deployment.rolloutID }}</dd></div>
            <div><dt>Observed rollout ID</dt><dd class="mono breakable">{{ deployment.observedRolloutID || '—' }}</dd></div>
            <div><dt>Generation observed</dt><dd class="mono">{{ deployment.observedGeneration ?? '—' }} / {{ deployment.generation ?? '—' }}</dd></div>
            <div><dt>Mode</dt><dd class="mono">{{ deployment.mode }}</dd></div>
            <div><dt>Deletion policy</dt><dd class="mono">{{ deployment.deletionPolicy }}</dd></div>
          </dl>
        </section>
      </div>

      <div class="detail-grid">
        <section class="panel" aria-labelledby="reconciliation-heading">
          <p class="eyebrow">Controller evidence</p>
          <h2 id="reconciliation-heading" class="panel-title">Applied versus Ready</h2>
          <div class="condition-summary">
            <div class="condition-summary-item">
              <span class="condition-summary-label">Applied</span>
              <StatusBadge :status="conditionLabel('Applied')" :tone="conditionTone('Applied')" />
              <span class="muted">Desired backend configuration</span>
            </div>
            <div class="condition-summary-item">
              <span class="condition-summary-label">Ready</span>
              <StatusBadge :status="conditionLabel('Ready')" :tone="conditionTone('Ready')" />
              <span class="muted">Current runtime health</span>
            </div>
          </div>
          <ConditionsPanel
            :conditions="deployment.conditions"
            :generation="deployment.generation"
            :observed-generation="deployment.observedGeneration"
            empty-text="No controller conditions have been observed yet."
          />
        </section>

        <section id="backend-evidence" class="panel" tabindex="-1" aria-labelledby="backend-heading">
          <div class="panel-head">
            <div>
              <p class="eyebrow">Infrastructure handoff</p>
              <h2 id="backend-heading" class="panel-title">Backend evidence</h2>
            </div>
            <StatusBadge :status="deployment.phase || 'Unknown'" :tone="evidenceTone(state)" />
          </div>
          <template v-if="deployment.backendRef">
            <dl class="facts">
              <div><dt>Reference</dt><dd class="mono breakable">{{ refText(deployment.backendRef) }}</dd></div>
              <div><dt>Kind</dt><dd class="mono">{{ deployment.backendRef.kind }}</dd></div>
              <div><dt>UID</dt><dd class="mono breakable">{{ deployment.backendRef.uid || '—' }}</dd></div>
              <div><dt>Phase</dt><dd class="mono">{{ deployment.phase || 'Unknown' }}</dd></div>
            </dl>
            <button class="button small" type="button" @click="copyValue(refText(deployment.backendRef), 'backend')">
              <Copy :size="13" aria-hidden="true" /> {{ copied === 'backend' ? 'Copied' : 'Copy backend ref' }}
            </button>
            <div class="runtime-link-row">
              <span class="field-label">Reported URL</span>
              <a v-if="runtimeURL" class="runtime-link mono breakable" :href="runtimeURL" target="_blank" rel="noopener noreferrer">{{ deployment.url }} <ExternalLink :size="12" aria-hidden="true" /></a>
              <span v-else class="muted">No safe runtime URL reported.</span>
              <button v-if="runtimeURL" class="button small" type="button" @click="openRuntimeURL">Open reported URL</button>
            </div>
            <div>
              <span class="field-label">Outputs</span>
              <dl v-if="Object.keys(deployment.outputs).length" class="outputs-list">
                <div v-for="(value, key) in deployment.outputs" :key="key"><dt class="mono">{{ key }}</dt><dd class="mono breakable">{{ value }}</dd></div>
              </dl>
              <p v-else class="muted">No backend outputs have been reported.</p>
            </div>
          </template>
          <div v-else class="state-card warning-card">
            <strong>Backend evidence unavailable.</strong>
            <span>The controller has not recorded an Infrastructure backend reference for this Deployment.</span>
          </div>
        </section>
      </div>
    </template>
  </section>
</template>
