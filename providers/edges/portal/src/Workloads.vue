<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { RefreshCw, Plus, ChevronRight, ChevronDown, Store, Rocket } from 'lucide-vue-next'
import { listWorkloads, createWorkload, deleteWorkload, deployMarketplaceApp, listEdges, type WorkloadDraft } from './api'
import type { Workload, Edge, ErrorResponse } from './types'
import { confirmDialog } from './portalkit/confirm'
import ResourceTable from './portalkit/ResourceTable.vue'
import ResourceTableDeleteButton from './portalkit/ResourceTableDeleteButton.vue'
import StatusBadge from './portalkit/StatusBadge.vue'
import { MARKETPLACE_CATEGORIES, type MarketplaceApp } from './marketplace'

const workloads = ref<Workload[]>([])
const edges = ref<Edge[]>([])
const loading = ref(true)
const loaded = ref(false)
const error = ref<string | null>(null)
const workloadColumns = [
  { key: 'expand', label: '' },
  { key: 'name', label: 'Name' },
  { key: 'image', label: 'Image' },
  { key: 'placement', label: 'Placement' },
  { key: 'status', label: 'Status' },
  { key: 'ready', label: 'Ready' },
  { key: 'actions', label: '' },
]
const workloadRows = computed<Array<Record<string, unknown>>>(() => workloads.value.map(workload => ({
  ...workload,
  expand: '',
  image: workload.image || '—',
  placement: `${workload.strategy || 'Spread'} · ${selectorText(workload.selector)}`,
  status: workload.phase || 'Pending',
  ready: `${workload.readyReplicas ?? 0}/${workload.replicas ?? 1}`,
  actions: '',
})))

// Marketplace deploy state.
const showMarket = ref(true)
const deployApp = ref<MarketplaceApp | null>(null)
const deployName = ref('')
const deployEdge = ref('')
function openDeploy(app: MarketplaceApp) {
  deployApp.value = app
  deployName.value = app.type
  deployEdge.value = edges.value[0]?.name ?? ''
  error.value = null
}
function closeDeploy() {
  deployApp.value = null
}
async function onDeploy() {
  const app = deployApp.value
  if (!app || !deployName.value.trim() || !deployEdge.value) return
  busy.value = true
  error.value = null
  try {
    await deployMarketplaceApp({
      name: deployName.value.trim(),
      edgeName: deployEdge.value,
      chart: app.chart,
      values: app.values,
      serviceType: app.type,
      port: app.port,
    })
    closeDeploy()
    await refresh()
  } catch (e) {
    error.value = (e as ErrorResponse)?.message ?? 'Deploy failed'
  } finally {
    busy.value = false
  }
}
const credentialHint: Record<string, string> = {
  'api-key': 'API key (mint it in the app, paste on the Services tab)',
  'user-pass': '"username:password" (paste on the Services tab)',
  password: 'web password (paste on the Services tab)',
  optional: 'no token needed',
}

const showCreate = ref(false)
const busy = ref(false)
const draft = ref<{ name: string; image: string; replicas: number; strategy: 'Spread' | 'Singleton'; selector: string }>({
  name: '',
  image: 'nginx:latest',
  replicas: 1,
  strategy: 'Spread',
  selector: 'env=dev',
})

const expanded = ref<string | null>(null)
function toggle(name: string) {
  expanded.value = expanded.value === name ? null : name
}

async function refresh() {
  loading.value = true
  error.value = null
  try {
    ;[workloads.value, edges.value] = await Promise.all([listWorkloads(), listEdges()])
    loaded.value = true
    if (!deployEdge.value && edges.value.length) deployEdge.value = edges.value[0].name
  } catch (e) {
    error.value = (e as ErrorResponse)?.message ?? 'Failed to load workloads'
  } finally {
    loading.value = false
  }
}

function parseSelector(s: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const pair of s.split(',')) {
    const [k, v] = pair.split('=').map((x) => x.trim())
    if (k && v) out[k] = v
  }
  return out
}

async function onCreate() {
  if (!draft.value.name.trim() || !draft.value.image.trim()) return
  busy.value = true
  error.value = null
  try {
    const d: WorkloadDraft = {
      name: draft.value.name.trim(),
      image: draft.value.image.trim(),
      replicas: Number(draft.value.replicas) || 1,
      strategy: draft.value.strategy,
      selector: parseSelector(draft.value.selector),
    }
    await createWorkload(d)
    showCreate.value = false
    draft.value = { name: '', image: 'nginx:latest', replicas: 1, strategy: 'Spread', selector: 'env=dev' }
    await refresh()
  } catch (e) {
    error.value = (e as ErrorResponse)?.message ?? 'Create failed'
  } finally {
    busy.value = false
  }
}

async function onDelete(w: Workload) {
  if (!(await confirmDialog({ title: `Delete workload "${w.name}"?`, message: 'Its Deployments on every edge are removed.', danger: true, confirmLabel: 'Delete' }))) return
  try {
    await deleteWorkload(w.name)
    await refresh()
  } catch (e) {
    error.value = (e as ErrorResponse)?.message ?? 'Delete failed'
  }
}

onMounted(refresh)
const timer = setInterval(refresh, 10000)
onUnmounted(() => clearInterval(timer))

function selectorText(s?: Record<string, string>): string {
  if (!s || !Object.keys(s).length) return 'all edges'
  return Object.entries(s).map(([k, v]) => `${k}=${v}`).join(', ')
}
function workloadEdges(row: Record<string, unknown>): NonNullable<Workload['edges']> {
  return Array.isArray(row.edges) ? row.edges as NonNullable<Workload['edges']> : []
}
</script>

<template>
  <div class="edges-app">
    <header class="edges-header">
      <div>
        <h1>Workloads</h1>
        <p>Deploy a workload across matching Kubernetes edges. Each edge's agent runs it locally.</p>
      </div>
      <div class="header-actions">
        <button class="btn" :disabled="loading" @click="refresh">
          <RefreshCw :size="14" :class="{ spin: loading }" /> Refresh
        </button>
        <button class="btn primary" @click="showCreate = !showCreate">
          <Plus :size="14" /> New workload
        </button>
      </div>
    </header>

    <div v-if="error" class="banner error">{{ error }}</div>

    <!-- Marketplace -->
    <div class="market">
      <div class="market-head clickable" @click="showMarket = !showMarket">
        <component :is="showMarket ? ChevronDown : ChevronRight" :size="16" />
        <Store :size="16" />
        <h3>Marketplace</h3>
        <span class="muted">one-click self-hosted apps, deployed as Helm workloads onto an edge</span>
      </div>
      <div v-if="showMarket" class="market-body">
        <div v-if="edges.length === 0" class="muted pad">
          Connect a KubernetesCluster edge first — marketplace apps deploy onto one.
        </div>
        <div v-for="grp in MARKETPLACE_CATEGORIES" :key="grp.category" class="market-cat">
          <div class="market-cat-label">{{ grp.category }}</div>
          <div class="market-grid">
            <div v-for="app in grp.apps" :key="app.type" class="market-card">
              <div class="market-card-top">
                <span class="market-name">{{ app.label }}</span>
                <span class="chip">{{ app.category }}</span>
              </div>
              <p class="market-desc">{{ app.description }}</p>
              <div class="market-meta muted mono">{{ app.chart.chart }}@{{ app.chart.version }} · :{{ app.port }}</div>
              <button class="btn primary sm" :disabled="edges.length === 0" @click="openDeploy(app)">
                <Rocket :size="13" /> Deploy
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Deploy dialog -->
    <div v-if="deployApp" class="wiz-card" style="margin-bottom: 16px;">
      <h3>Deploy {{ deployApp.label }}</h3>
      <div class="row" style="gap: 12px; align-items: flex-start;">
        <label class="fld" style="flex: 1;">
          <span class="lbl">Name</span>
          <input v-model="deployName" class="input" :placeholder="deployApp.type" />
        </label>
        <label class="fld" style="flex: 1;">
          <span class="lbl">Edge</span>
          <select v-model="deployEdge" class="input">
            <option v-for="e in edges" :key="e.name" :value="e.name">{{ e.name }}</option>
          </select>
        </label>
      </div>
      <div class="muted" style="margin: 4px 0 12px;">
        Deploys the chart onto <b>{{ deployEdge || '—' }}</b> and wires an edges Service.
        Auth: {{ credentialHint[deployApp.credential] }}.
      </div>
      <div class="wiz-actions">
        <button class="btn" @click="closeDeploy">Cancel</button>
        <button class="btn primary" :disabled="busy || !deployName.trim() || !deployEdge" @click="onDeploy">
          <Rocket :size="14" /> Deploy
        </button>
      </div>
    </div>

    <!-- Create form -->
    <div v-if="showCreate" class="wiz-card" style="margin-bottom: 16px;">
      <h3>New workload</h3>
      <label class="fld">
        <span class="lbl">Name</span>
        <input v-model="draft.name" class="input" placeholder="nginx-demo" />
      </label>
      <label class="fld">
        <span class="lbl">Image</span>
        <input v-model="draft.image" class="input" placeholder="nginx:latest" />
      </label>
      <div class="row" style="gap: 12px; align-items: flex-start;">
        <label class="fld" style="flex: 1;">
          <span class="lbl">Replicas</span>
          <input v-model="draft.replicas" type="number" min="1" class="input" />
        </label>
        <label class="fld" style="flex: 1;">
          <span class="lbl">Strategy</span>
          <select v-model="draft.strategy" class="input">
            <option value="Spread">Spread (all matching edges)</option>
            <option value="Singleton">Singleton (one edge)</option>
          </select>
        </label>
      </div>
      <label class="fld">
        <span class="lbl">Edge selector (key=value, comma-separated)</span>
        <input v-model="draft.selector" class="input" placeholder="env=dev" />
      </label>
      <div class="wiz-actions">
        <button class="btn" @click="showCreate = false">Cancel</button>
        <button class="btn primary" :disabled="busy || !draft.name.trim() || !draft.image.trim()" @click="onCreate">Create</button>
      </div>
    </div>

    <ResourceTable
      :columns="workloadColumns"
      :rows="workloadRows"
      row-key="name"
      :loaded="loaded"
      :loading="loading"
      :error="error"
      retryable
      searchable
      search-placeholder="Search workloads…"
      :filters="[{ key: 'strategy', label: 'Strategy' }, { key: 'status', label: 'Status', allLabel: 'Any status' }]"
      paginated
      :page-size="10"
      empty-text="No workloads yet. Create one to deploy it across matching edges."
      @retry="refresh"
      @row-click="(row) => toggle(String(row.name))"
    >
      <template #expand="{ row }"><component :is="expanded === row.name ? ChevronDown : ChevronRight" :size="14" /></template>
      <template #name="{ value }"><span class="name">{{ value }}</span></template>
      <template #image="{ value }"><span class="mono muted">{{ value }}</span></template>
      <template #placement="{ value }"><span class="muted">{{ value }}</span></template>
      <template #status="{ value }"><StatusBadge :status="String(value)" /></template>
      <template #ready="{ value }"><span class="mono">{{ value }}</span></template>
      <template #actions="{ row }"><div class="row-actions"><ResourceTableDeleteButton :label="`Delete workload ${String(row.name)}`" @click="onDelete(row as unknown as Workload)" /></div></template>
      <template #after-row="{ row }">
        <tr v-if="expanded === row.name" class="detail-row">
          <td :colspan="workloadColumns.length">
            <div class="es-head">Per-edge status</div>
            <div v-if="workloadEdges(row).length === 0" class="muted">Not scheduled onto any edge yet (no edge matches the selector, or agents haven't reported).</div>
            <div v-else class="es-list">
              <div v-for="edge in workloadEdges(row)" :key="edge.edgeName" class="es-item">
                <span class="es-name">{{ edge.edgeName }}</span>
                <StatusBadge :status="edge.phase || 'Pending'" />
                <span class="es-ready mono">{{ edge.readyReplicas ?? 0 }} ready</span>
                <span v-if="edge.message" class="muted es-msg">{{ edge.message }}</span>
              </div>
            </div>
          </td>
        </tr>
      </template>
    </ResourceTable>
  </div>
</template>
