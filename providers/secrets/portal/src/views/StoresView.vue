<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '../api'
import type { ErrorResponse, SecretStoreRow } from '../types'
import { fmtAge } from '../format'
import { confirmDialog } from '../portalkit/confirm'
import ResourceTable from '../portalkit/ResourceTable.vue'
import ResourceTableDeleteButton from '../portalkit/ResourceTableDeleteButton.vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import ConditionsPanel from '../portalkit/ConditionsPanel.vue'

const stores = ref<SecretStoreRow[]>([])
const error = ref<string | null>(null)
const loading = ref(false)
const loaded = ref(false)
const deleting = ref<string | null>(null)

// Row click expands the store's conditions below the table.
const selectedName = ref<string | null>(null)
const selected = computed(() => stores.value.find(s => s.name === selectedName.value) ?? null)

// create form
const showForm = ref(false)
const name = ref('')
const address = ref('')
const mount = ref('')
const vaultNamespace = ref('')
// Credential: paste a token (the portal creates the Secret, owned by the
// store) or reference a Secret that already exists in the workspace.
const credMode = ref<'token' | 'existing'>('token')
const token = ref('')
const secretName = ref('')
const secretNamespace = ref('')
const secretKey = ref('')
const submitting = ref(false)
const formError = ref<string | null>(null)

let timer: number | undefined

function resetForm() {
  name.value = address.value = mount.value = vaultNamespace.value = ''
  token.value = secretName.value = secretNamespace.value = secretKey.value = ''
  credMode.value = 'token'
  formError.value = null
}

async function load() {
  loading.value = true
  try {
    stores.value = await api.listStores()
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
  if (!name.value || !address.value) {
    formError.value = 'name and vault address are required'
    return
  }
  if (credMode.value === 'token' && !token.value) {
    formError.value = 'paste a vault token or reference an existing secret'
    return
  }
  if (credMode.value === 'existing' && !secretName.value) {
    formError.value = 'the credential secret name is required'
    return
  }
  submitting.value = true
  try {
    await api.createStore({
      name: name.value,
      address: address.value,
      mount: mount.value || undefined,
      vaultNamespace: vaultNamespace.value || undefined,
      credential: credMode.value === 'token'
        ? { mode: 'token', token: token.value }
        : {
            mode: 'existing',
            secretName: secretName.value,
            secretNamespace: secretNamespace.value || undefined,
            secretKey: secretKey.value || undefined,
          },
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

async function remove(s: SecretStoreRow) {
  const ok = await confirmDialog({
    title: `Delete store "${s.name}"?`,
    message: 'SyncedSecrets referencing it will stop syncing.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  deleting.value = s.name
  try {
    await api.deleteStore(s)
    if (selectedName.value === s.name) selectedName.value = null
    await load()
  } catch (e) {
    const err = e as ErrorResponse
    error.value = `${err.reason}: ${err.message}`
  } finally {
    deleting.value = null
  }
}

function onRowClick(row: Record<string, unknown>) {
  const n = String(row.name)
  selectedName.value = selectedName.value === n ? null : n
}

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'backend', label: 'Backend' },
  { key: 'address', label: 'Address' },
  { key: 'validated', label: 'Validated' },
  { key: 'ready', label: 'Ready' },
  { key: 'backendVersion', label: 'Version' },
  { key: 'age', label: 'Age' },
  { key: 'actions', label: '' },
]

const rows = computed<Array<Record<string, unknown>>>(() =>
  stores.value.map(s => ({ ...s, age: fmtAge(s.creationTimestamp) })),
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
        <h2 class="page-title">Secret stores</h2>
        <p class="page-meta">
          A store binds this workspace to one external secret backend (Vault). Synced secrets read
          through it; the external store stays the source of truth.
        </p>
      </div>
      <div class="actions">
        <button class="primary" @click="showForm = !showForm">{{ showForm ? 'Cancel' : 'Add store' }}</button>
      </div>
    </header>

    <div v-if="showForm" class="panel">
      <h3 class="panel-title">New secret store</h3>
      <form class="form" @submit.prevent="submit">
        <div class="field"><span class="field-label">Name</span><input v-model="name" placeholder="prod-vault" autocomplete="off" /></div>
        <div class="field"><span class="field-label">Vault address</span><input v-model="address" placeholder="https://vault.example.com:8200" autocomplete="off" /></div>
        <div class="field"><span class="field-label">Mount (KV v2, optional — defaults to "secret")</span><input v-model="mount" placeholder="secret" autocomplete="off" /></div>
        <div class="field"><span class="field-label">Vault namespace (Enterprise, optional)</span><input v-model="vaultNamespace" placeholder="" autocomplete="off" /></div>

        <div class="field">
          <span class="field-label">Credential</span>
          <select v-model="credMode">
            <option value="token">Paste a Vault token (stored as a new Secret)</option>
            <option value="existing">Reference an existing Secret</option>
          </select>
        </div>
        <template v-if="credMode === 'token'">
          <div class="field"><span class="field-label">Vault token</span><input v-model="token" type="password" placeholder="hvs.…" autocomplete="off" /></div>
          <p class="muted">The token is stored as a Secret in your workspace, owned by the store so it is cleaned up with it. The provider validates it and reports the result below.</p>
        </template>
        <template v-else>
          <div class="field"><span class="field-label">Secret name</span><input v-model="secretName" placeholder="vault-credentials" autocomplete="off" /></div>
          <div class="field"><span class="field-label">Secret namespace (optional — defaults to "default")</span><input v-model="secretNamespace" placeholder="default" autocomplete="off" /></div>
          <div class="field"><span class="field-label">Secret key (optional — defaults to "token")</span><input v-model="secretKey" placeholder="token" autocomplete="off" /></div>
        </template>

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
      :loaded="loaded"
      :loading="loading"
      :error="error"
      :stale="!!error && stores.length > 0"
      retryable
      empty-text="No secret stores yet. Add one to connect this workspace to Vault."
      @row-click="onRowClick"
      @retry="load"
    >
      <template #name="{ value }"><span class="mono">{{ value }}</span></template>
      <template #backend="{ value }"><span class="mono">{{ value }}</span></template>
      <template #address="{ value }"><span class="mono">{{ value || '—' }}</span></template>
      <template #validated="{ row }">
        <StatusBadge :status="row.validated ? 'validated' : 'pending'" :tone="row.validated ? 'success' : 'warning'" />
      </template>
      <template #ready="{ row }">
        <StatusBadge :status="(row.ready ? 'ready' : 'pending')" />
      </template>
      <template #backendVersion="{ value }"><span class="mono">{{ value || '—' }}</span></template>
      <template #actions="{ row }">
        <ResourceTableDeleteButton
          label="Delete store"
          :busy="deleting === row.name"
          @click="remove(row as unknown as SecretStoreRow)"
        />
      </template>
    </ResourceTable>

    <div v-if="selected" class="panel">
      <div class="panel-head">
        <h3 class="panel-title">{{ selected.name }} — conditions</h3>
        <button class="link" @click="selectedName = null">Close</button>
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
