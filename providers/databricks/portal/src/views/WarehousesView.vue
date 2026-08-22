<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import SplitCreateButton from '../components/SplitCreateButton.vue'
import ResourceTable from '../portalkit/ResourceTable.vue'
import ResourceTableDeleteButton from '../portalkit/ResourceTableDeleteButton.vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import { api } from '../api'
import { confirmDialog } from '../portalkit/confirm'
import type { ResourceTableChange } from '../portalkit/table'
import type { Connection, ErrorResponse, Warehouse } from '../types'
import { createLatestRefreshController, createOperationLocks, operationKey, type LatestRefreshController } from '../refresh'
import { resourceNameError } from '../resourceName'
import {
  cloneWarehouseFilters,
  DATABRICKS_PAGE_SIZE,
  DATABRICKS_SUPPORT_PAGE_SIZE,
  EMPTY_WAREHOUSE_FILTERS,
  hasActiveFilters,
  pageInfo as toPageInfo,
  serverCursorChange,
  type DatabricksPaginationMode,
  type WarehouseFilterValues,
  warehouseFilters,
} from '../databricksPagination'

const emit = defineEmits<{ (e: 'open', name: string): void; (e: 'browse', trigger?: HTMLElement): void }>()

const connections = ref<Connection[]>([])
const warehouses = ref<Warehouse[]>([])
const loading = ref(false)
const loaded = ref(false)
const error = ref<string | null>(null)
const mutationError = ref<string | null>(null)
const operations = createOperationLocks()
const warehouseMode = ref<DatabricksPaginationMode>('server')
const warehousePage = ref(1)
const warehousePageSize = ref(DATABRICKS_PAGE_SIZE)
const warehouseQuery = ref('')
const warehouseFiltersValue = ref<WarehouseFilterValues>(cloneWarehouseFilters(EMPTY_WAREHOUSE_FILTERS))
const warehouseCursor = ref<string | null>(null)
const warehousePageInfo = ref<ReturnType<typeof toPageInfo> | null>(null)

const showForm = ref(false)
const submitting = ref(false)
const formError = ref<string | null>(null)
const nameInput = ref<HTMLInputElement | null>(null)
const formErrorRef = ref<HTMLElement | null>(null)
let timer: number | undefined
let refresh!: LatestRefreshController

const rows = computed<Array<Record<string, unknown>>>(() => warehouses.value
  .filter(wh => !operations.isTombstoned(operationKey('warehouse', wh.name), wh.uid))
  .map(wh => ({ ...wh })))

const form = reactive({
  name: '',
  connectionRef: '',
  warehouseID: '',
})

const filterDefinitions = computed(() => warehouseFilters(connections.value))

function errMessage(e: unknown): string {
  const err = e as ErrorResponse
  return err.reason ? `${err.reason}: ${err.message}` : err.message || String(e)
}

function resetForm() {
  form.name = ''
  form.connectionRef = connections.value[0]?.name ?? ''
  form.warehouseID = ''
  formError.value = null
}

function load() {
  refresh.request()
}

function operationLocked(name: string): boolean {
  return operations.isLocked(operationKey('warehouse', name))
}

function operationPhase(name: string) {
  return operations.phase(operationKey('warehouse', name))
}

function openResource(name: string): void {
  if (!operationLocked(name)) emit('open', name)
}

function startCreate() {
  resetForm()
  showForm.value = true
  void nextTick(() => nameInput.value?.focus())
}

function closeForm() {
  resetForm()
  showForm.value = false
}

function browseCatalog(trigger?: HTMLElement) {
  if (showForm.value) closeForm()
  emit('browse', trigger)
}

async function focusFormError(message: string) {
  formError.value = message
  await nextTick()
  formErrorRef.value?.focus()
}

async function submit() {
  formError.value = null
  mutationError.value = null
  if (!loaded.value) {
    await focusFormError('Warehouse list is still loading. Retry the read before creating a warehouse.')
    return
  }
  if (!form.name || !form.connectionRef || !form.warehouseID) {
    await focusFormError('Name, connection, and warehouse ID are required.')
    return
  }
  const nameError = resourceNameError(form.name, 'Name')
  if (nameError) {
    await focusFormError(nameError)
    return
  }
  const desiredName = form.name.trim()
  const lock = operationKey('warehouse', desiredName)
  if (operations.isTombstoned(lock)) {
    await focusFormError(`Warehouse "${desiredName}" is still being removed. Retry after the list refresh confirms it is gone.`)
    return
  }
  if (!operations.acquire(lock, 'creating')) {
    await focusFormError(`Warehouse "${desiredName}" already has an update in progress.`)
    return
  }
  submitting.value = true
  try {
    // A server page is not a complete duplicate or foreign-key check. Read
    // both authoritative collections before applying the new warehouse.
    const [existing, availableConnections] = await Promise.all([
      api.listWarehouses(),
      api.listConnections(),
    ])
    if (existing.some(warehouse => warehouse.name === desiredName)) {
      await focusFormError(`Warehouse "${desiredName}" already exists.`)
      return
    }
    if (!availableConnections.some(connection => connection.name === form.connectionRef)) {
      await focusFormError('Selected connection is no longer available in this workspace.')
      return
    }
    await api.saveWarehouse({
      name: desiredName,
      connectionRef: form.connectionRef,
      warehouseID: form.warehouseID,
    })
    resetForm()
    showForm.value = false
    load()
  } catch (e) {
    await focusFormError(errMessage(e))
  } finally {
    submitting.value = false
    operations.release(lock)
  }
}

interface WarehouseRequest {
  mode: DatabricksPaginationMode
  active: boolean
  page: number
  pageSize: number
  query: string
  filters: WarehouseFilterValues
  cursor: string | null
}

function currentWarehouseRequest(): WarehouseRequest {
  return {
    mode: warehouseMode.value,
    active: hasActiveFilters(warehouseQuery.value, warehouseFiltersValue.value),
    page: warehousePage.value,
    pageSize: warehousePageSize.value,
    query: warehouseQuery.value,
    filters: cloneWarehouseFilters(warehouseFiltersValue.value),
    cursor: warehouseCursor.value,
  }
}

function warehouseRequestIsCurrent(requestID: number, request: WarehouseRequest): boolean {
  const current = currentWarehouseRequest()
  return refresh.isCurrent(requestID) &&
    current.mode === request.mode &&
    current.active === request.active &&
    current.page === request.page &&
    current.pageSize === request.pageSize &&
    current.query === request.query &&
    current.cursor === request.cursor &&
    current.filters.connectionRef === request.filters.connectionRef &&
    current.filters.state === request.filters.state &&
    current.filters.status === request.filters.status
}

function handleWarehouseChange(change: ResourceTableChange): void {
  const filters: WarehouseFilterValues = {
    connectionRef: change.filters.connectionRef || '',
    state: change.filters.state || '',
    status: change.filters.status || '',
  }
  const active = hasActiveFilters(change.query, filters)
  const serverChange = serverCursorChange(change)
  warehousePage.value = change.page
  warehousePageSize.value = change.pageSize
  warehouseQuery.value = change.query
  warehouseFiltersValue.value = filters
  warehouseCursor.value = change.cursor
  warehousePageInfo.value = null

  if (!active) {
    warehouseMode.value = 'server'
    warehouses.value = []
    warehousePage.value = serverChange.page
    warehouseCursor.value = serverChange.cursor
    load()
    return
  }

  if (warehouseMode.value === 'client') return
  warehouses.value = []
  load()
}

async function remove(row: Record<string, unknown>) {
  const wh = row as unknown as Warehouse
  const ok = await confirmDialog({
    title: `Delete warehouse "${wh.name}"?`,
    message: 'Tables that reference this warehouse will stop refreshing schema metadata.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  const lock = operationKey('warehouse', wh.name)
  if (!operations.acquire(lock, 'deleting')) {
    mutationError.value = `Warehouse "${wh.name}" already has an operation in progress.`
    return
  }
  mutationError.value = null
  try {
    await api.deleteWarehouse(wh.name)
    operations.tombstone(lock, wh.uid)
    warehouses.value = warehouses.value.filter(item => item.name !== wh.name)
    load()
  } catch (e) {
    mutationError.value = errMessage(e)
  } finally {
    operations.release(lock)
  }
}

refresh = createLatestRefreshController(async requestID => {
  const request = currentWarehouseRequest()
  loading.value = true
  if (request.active && request.mode === 'server') {
    warehouses.value = []
    warehousePageInfo.value = null
  }
  try {
    const connPage = await api.listConnectionsPage({ limit: DATABRICKS_SUPPORT_PAGE_SIZE })
    if (!warehouseRequestIsCurrent(requestID, request)) return
    connections.value = connPage.items

    if (request.active || request.mode === 'client') {
      const warehouseList = await api.listWarehouses()
      if (!warehouseRequestIsCurrent(requestID, request)) return
      warehouses.value = warehouseList
      if (request.mode === 'server') {
        warehouseMode.value = 'client'
        warehousePage.value = 1
      }
      warehouseCursor.value = null
      warehousePageInfo.value = null
      operations.reconcile('warehouse', warehouseList.map(({ name, uid }) => ({ name, uid })))
    } else {
      const warehousePageResult = await api.listWarehousesPage({
        limit: request.pageSize,
        ...(request.cursor ? { continue: request.cursor } : {}),
      })
      if (!warehouseRequestIsCurrent(requestID, request)) return
      warehouses.value = warehousePageResult.items
      warehouseCursor.value = request.cursor
      warehousePageInfo.value = toPageInfo(warehousePageResult.continue)
    }
    loaded.value = true
    error.value = null
    if (connections.value.length && !connections.value.some(c => c.name === form.connectionRef)) {
      form.connectionRef = connections.value[0].name
    }
  } catch (e) {
    if (!warehouseRequestIsCurrent(requestID, request)) return
    const err = e as ErrorResponse
    error.value = err.reason === 'TenantMissing' ? null : errMessage(e)
  } finally {
    if (refresh.isCurrent(requestID)) loading.value = false
  }
})

onMounted(() => {
  load()
  timer = window.setInterval(load, 5000)
})
onUnmounted(() => {
  window.clearInterval(timer)
  refresh.stop()
})

</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <h2 class="page-title">Warehouses</h2>
        <p class="page-meta">SQL warehouses available to imported Databricks tables. Click one to inspect status and defaults.</p>
      </div>
      <SplitCreateButton kind="warehouse" :disabled="submitting" @manual="startCreate" @browse="browseCatalog" />
    </header>

    <p v-if="loaded && !connections.length" class="empty">Add a connection first, then import warehouses under it.</p>

    <div v-if="showForm" class="panel">
      <h3 class="panel-title">New warehouse</h3>
      <form class="form" @submit.prevent="submit">
        <div class="field">
          <label class="field-label" for="warehouse-connection">Connection</label>
          <select id="warehouse-connection" v-model="form.connectionRef" :disabled="submitting" required aria-required="true" aria-describedby="warehouse-connection-hint warehouse-form-error" :aria-invalid="!!formError">
            <option v-for="conn in connections" :key="conn.name" :value="conn.name">{{ conn.name }}</option>
          </select>
          <span id="warehouse-connection-hint" class="field-hint">The Databricks workspace connection this warehouse belongs to.</span>
        </div>
        <div class="field">
          <label class="field-label" for="warehouse-name">Object name</label>
          <input id="warehouse-name" ref="nameInput" v-model="form.name" :disabled="submitting" placeholder="orders-sql" autocomplete="off" required aria-required="true" aria-describedby="warehouse-name-hint warehouse-form-error" :aria-invalid="!!formError" />
          <span id="warehouse-name-hint" class="field-hint">How this warehouse is referred to from faros. Use lowercase letters, numbers, and hyphens; the name is preserved exactly.</span>
        </div>
        <div class="field">
          <label class="field-label" for="warehouse-id">Warehouse ID</label>
          <input id="warehouse-id" v-model="form.warehouseID" :disabled="submitting" placeholder="abc123def4567890" autocomplete="off" required aria-required="true" aria-describedby="warehouse-id-hint warehouse-form-error" :aria-invalid="!!formError" />
          <span id="warehouse-id-hint" class="field-hint">In Databricks: SQL → SQL Warehouses → open the warehouse. Use the 16-character ID from Connection details (/sql/1.0/warehouses/&lt;id&gt;), not the numeric ?o= workspace ID. The token identity needs “Can use” permission.</span>
        </div>
        <div class="actions">
          <button class="primary" type="submit" :disabled="submitting">{{ submitting ? 'Creating…' : 'Create' }}</button>
          <button class="secondary" type="button" :disabled="submitting" @click="closeForm">Cancel</button>
          <span v-if="formError" id="warehouse-form-error" ref="formErrorRef" class="error" role="alert" aria-live="assertive" tabindex="-1">{{ formError }}</span>
        </div>
      </form>
    </div>

    <div v-if="mutationError" class="error mutation-error" role="alert" aria-live="assertive">
      <span>{{ mutationError }}</span>
      <button class="secondary" type="button" @click="mutationError = null">Dismiss</button>
    </div>

    <ResourceTable
      :columns="[
        { key: 'name', label: 'Name' },
        { key: 'connectionRef', label: 'Connection' },
        { key: 'warehouseID', label: 'Warehouse ID' },
        { key: 'state', label: 'State' },
        { key: 'status', label: 'Status' },
        { key: 'actions', label: '' },
      ]"
      :rows="rows"
      searchable
      search-placeholder="Search warehouses…"
      :filters="filterDefinitions"
      paginated
      :pagination-mode="warehouseMode"
      :page="warehousePage"
      :page-size="warehousePageSize"
      :query="warehouseQuery"
      :filter-values="warehouseFiltersValue"
      :cursor="warehouseCursor"
      :page-info="warehousePageInfo"
      row-key="name"
      :loaded="loaded"
      :loading="loading"
      :error="error"
      :stale="loaded && !!error"
      retryable
      empty-text="No warehouses yet."
      @retry="load"
      @change="handleWarehouseChange"
      @row-click="(row) => openResource(String(row.name))"
    >
      <template #name="{ value }">
        <button class="link" type="button" :disabled="operationLocked(String(value))" @click.stop="openResource(String(value))">{{ value }}</button>
      </template>
      <template #connectionRef="{ value }">{{ value }}</template>
      <template #warehouseID="{ value }"><code>{{ value }}</code></template>
      <template #state="{ row }">{{ row.state || '—' }}</template>
      <template #status="{ row }">
        <StatusBadge :status="String(row.status)" />
        <span v-if="row.message" class="row-message">{{ row.message }}</span>
      </template>
      <template #actions="{ row }">
        <div class="row-actions">
          <ResourceTableDeleteButton
            :label="`Delete warehouse ${String(row.name)}`"
            :busy-label="`Deleting warehouse ${String(row.name)}…`"
            :busy="operationPhase(String(row.name)) === 'deleting'"
            :disabled="operationLocked(String(row.name))"
            @click="remove(row)"
          />
        </div>
      </template>
    </ResourceTable>

  </section>
</template>
