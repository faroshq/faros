<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '../api'
import type { ErrorResponse, SecretStoreRow, SyncedSecretDataMap, SyncedSecretRow } from '../types'
import { fmtAge, fmtTime, shortHash } from '../format'
import { confirmDialog } from '../portalkit/confirm'
import ResourceTable from '../portalkit/ResourceTable.vue'
import ResourceTableDeleteButton from '../portalkit/ResourceTableDeleteButton.vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import ConditionsPanel from '../portalkit/ConditionsPanel.vue'

const synced = ref<SyncedSecretRow[]>([])
const stores = ref<SecretStoreRow[]>([])
const error = ref<string | null>(null)
const loading = ref(false)
const loaded = ref(false)
const deleting = ref<string | null>(null)

// Row click expands the synced secret's conditions below the table.
const selectedKey = ref<string | null>(null)
const selected = computed(() => synced.value.find(s => rowKey(s) === selectedKey.value) ?? null)
function rowKey(s: { namespace: string; name: string }): string {
  return s.namespace + '/' + s.name
}

// create form
const showForm = ref(false)
const name = ref('')
const namespace = ref('default')
const store = ref('')
const refreshInterval = ref('1h')
const targetName = ref('')
// dataFrom pulls every property at a path; data cherry-picks/remaps keys.
const dataFrom = ref<string[]>([''])
const dataMaps = ref<SyncedSecretDataMap[]>([])
const submitting = ref(false)
const formError = ref<string | null>(null)

let timer: number | undefined

function resetForm() {
  name.value = targetName.value = ''
  namespace.value = 'default'
  store.value = stores.value[0]?.name ?? ''
  refreshInterval.value = '1h'
  dataFrom.value = ['']
  dataMaps.value = []
  formError.value = null
}

function addDataFrom() {
  dataFrom.value.push('')
}
function removeDataFrom(i: number) {
  dataFrom.value.splice(i, 1)
}
function addDataMap() {
  dataMaps.value.push({ secretKey: '', path: '', property: '' })
}
function removeDataMap(i: number) {
  dataMaps.value.splice(i, 1)
}

async function load() {
  loading.value = true
  try {
    const [syncedRows, storeRows] = await Promise.all([api.listSynced(), api.listStores()])
    synced.value = syncedRows
    stores.value = storeRows
    if (!store.value && storeRows.length) store.value = storeRows[0].name
    error.value = null
    loaded.value = true
  } catch (e) {
    const err = e as ErrorResponse
    error.value = err.reason === 'TenantMissing' ? null : `${err.reason}: ${err.message}`
  } finally {
    loading.value = false
  }
}

async function submit() {
  formError.value = null
  if (!name.value || !store.value) {
    formError.value = 'name and store are required'
    return
  }
  const paths = dataFrom.value.map(p => p.trim()).filter(p => p)
  const maps = dataMaps.value.filter(d => d.secretKey.trim() && d.path.trim())
  if (!paths.length && !maps.length) {
    formError.value = 'add at least one path (or key mapping) to sync'
    return
  }
  submitting.value = true
  try {
    await api.createSynced({
      name: name.value,
      namespace: namespace.value || 'default',
      store: store.value,
      refreshInterval: refreshInterval.value || undefined,
      targetName: targetName.value || undefined,
      dataFrom: paths,
      data: maps,
    })
    resetForm()
    showForm.value = false
    await load()
  } catch (e) {
    const err = e as ErrorResponse
    formError.value = `${err.reason}: ${err.message}`
  } finally {
    submitting.value = false
  }
}

async function remove(s: SyncedSecretRow) {
  const ok = await confirmDialog({
    title: `Delete synced secret "${s.name}"?`,
    message: `The projected Secret "${s.targetSecret}" in namespace "${s.namespace}" is removed with it.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  deleting.value = rowKey(s)
  try {
    await api.deleteSynced(s.name, s.namespace)
    if (selectedKey.value === rowKey(s)) selectedKey.value = null
    await load()
  } catch (e) {
    const err = e as ErrorResponse
    error.value = `${err.reason}: ${err.message}`
  } finally {
    deleting.value = null
  }
}

function onRowClick(row: Record<string, unknown>) {
  const k = String(row.key)
  selectedKey.value = selectedKey.value === k ? null : k
}

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'namespace', label: 'Namespace' },
  { key: 'store', label: 'Store' },
  { key: 'targetSecret', label: 'Target Secret' },
  { key: 'refreshInterval', label: 'Refresh' },
  { key: 'syncedKeys', label: 'Keys' },
  { key: 'syncedVersion', label: 'Version' },
  { key: 'lastSyncTime', label: 'Last Sync' },
  { key: 'ready', label: 'Ready' },
  { key: 'actions', label: '' },
]

const rows = computed<Array<Record<string, unknown>>>(() =>
  synced.value.map(s => ({ ...s, key: rowKey(s), age: fmtAge(s.creationTimestamp) })),
)

onMounted(() => {
  load()
  timer = window.setInterval(load, 5000)
})
onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <h2 class="page-title">Synced secrets</h2>
        <p class="page-meta">
          A synced secret projects material from a store into a workspace Secret on a refresh
          interval — declare paths and key mappings instead of hand-placing Secrets.
        </p>
      </div>
      <div class="actions">
        <button class="primary" @click="showForm = !showForm">{{ showForm ? 'Cancel' : 'Add synced secret' }}</button>
      </div>
    </header>

    <div v-if="showForm" class="panel">
      <h3 class="panel-title">New synced secret</h3>
      <form class="form" @submit.prevent="submit">
        <div class="field"><span class="field-label">Name</span><input v-model="name" placeholder="db-credentials" autocomplete="off" /></div>
        <div class="field"><span class="field-label">Namespace</span><input v-model="namespace" placeholder="default" autocomplete="off" /></div>
        <div class="field">
          <span class="field-label">Store</span>
          <select v-model="store">
            <option v-for="s in stores" :key="s.name" :value="s.name">{{ s.name }}</option>
          </select>
          <p v-if="!stores.length" class="muted">No secret stores yet — create one on the Stores tab first.</p>
        </div>
        <div class="field"><span class="field-label">Refresh interval (optional — defaults to "1h")</span><input v-model="refreshInterval" placeholder="1h" autocomplete="off" /></div>
        <div class="field"><span class="field-label">Target Secret name (optional — defaults to the synced secret's name)</span><input v-model="targetName" placeholder="" autocomplete="off" /></div>

        <div class="field">
          <span class="field-label">Pull whole paths (dataFrom)</span>
          <div v-for="(_, i) in dataFrom" :key="'df' + i" class="row-line">
            <input v-model="dataFrom[i]" placeholder="apps/myapp/db" autocomplete="off" />
            <button class="danger" type="button" @click="removeDataFrom(i)" :disabled="dataFrom.length === 1 && !dataFrom[0]">Remove</button>
          </div>
          <div><button class="secondary" type="button" @click="addDataFrom">Add path</button></div>
        </div>

        <div class="field">
          <span class="field-label">Key mappings (optional — cherry-pick and rename properties)</span>
          <div v-for="(m, i) in dataMaps" :key="'dm' + i" class="row-line">
            <input v-model="m.secretKey" placeholder="secret key" autocomplete="off" />
            <input v-model="m.path" placeholder="remote path" autocomplete="off" />
            <input v-model="m.property" placeholder="property (optional)" autocomplete="off" />
            <button class="danger" type="button" @click="removeDataMap(i)">Remove</button>
          </div>
          <div><button class="secondary" type="button" @click="addDataMap">Add mapping</button></div>
        </div>

        <div class="actions">
          <button class="primary" type="submit" :disabled="submitting">{{ submitting ? 'Creating…' : 'Create' }}</button>
          <button class="secondary" type="button" @click="() => { showForm = false; resetForm() }">Cancel</button>
          <span v-if="formError" class="error">{{ formError }}</span>
        </div>
      </form>
    </div>

    <ResourceTable
      :columns="columns"
      :rows="rows"
      row-key="key"
      :loaded="loaded"
      :loading="loading"
      :error="error"
      :stale="!!error && synced.length > 0"
      retryable
      empty-text="No synced secrets yet. Add one to project material from a store."
      @row-click="onRowClick"
      @retry="load"
    >
      <template #name="{ value }"><span class="mono">{{ value }}</span></template>
      <template #namespace="{ value }"><span class="mono">{{ value }}</span></template>
      <template #store="{ value }"><span class="mono">{{ value }}</span></template>
      <template #targetSecret="{ value }"><span class="mono">{{ value }}</span></template>
      <template #refreshInterval="{ value }"><span class="mono">{{ value }}</span></template>
      <template #syncedKeys="{ value }">{{ value ?? '—' }}</template>
      <template #syncedVersion="{ row }">
        <span class="mono" :title="String(row.syncedVersion ?? '')">{{ shortHash(row.syncedVersion as string | undefined) }}</span>
      </template>
      <template #lastSyncTime="{ value }">{{ fmtTime(value as string | undefined) }}</template>
      <template #ready="{ row }">
        <StatusBadge :status="row.ready ? 'ready' : 'pending'" />
      </template>
      <template #actions="{ row }">
        <ResourceTableDeleteButton
          label="Delete synced secret"
          :busy="deleting === row.key"
          @click="remove(row as unknown as SyncedSecretRow)"
        />
      </template>
    </ResourceTable>

    <div v-if="selected" class="panel">
      <div class="panel-head">
        <h3 class="panel-title">{{ selected.namespace }}/{{ selected.name }} — conditions</h3>
        <button class="link" @click="selectedKey = null">Close</button>
      </div>
      <p v-if="selected.message" class="muted">{{ selected.message }}</p>
      <ConditionsPanel
        :conditions="selected.conditions"
        :generation="selected.generation"
        :observed-generation="selected.observedGeneration"
      />
    </div>
  </section>
</template>
