<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import ViewValue from '../components/ViewValue.vue'
import { api, isContextChangedError } from '../api'
import ResourceTable from '../portalkit/ResourceTable.vue'
import ResourceTableDeleteButton from '../portalkit/ResourceTableDeleteButton.vue'
import { confirmDialog } from '../portalkit/confirm'
import { createLatestRefreshController, createResourceTombstones } from '../refresh'
import { resolve, type ResolvedValue } from '../view'
import type { Instance, TemplateView, ViewColumn } from '../types'

const emit = defineEmits<{
  (e: 'navigate', view: string): void
  (e: 'select', name: string, template: string): void
}>()

const items = ref<Instance[]>([])
const error = ref<string | null>(null)
const loading = ref(false)
const loaded = ref(false)
const deletingInstanceKey = ref<string | null>(null)
const deleteError = ref<string | null>(null)
const viewByTemplate = ref<Map<string, TemplateView>>(new Map())
const tombstones = createResourceTombstones()
let pollHandle: number | null = null

interface DynamicColumn {
  key: string
  label: string
  definitionByTemplate: Map<string, ViewColumn>
}

const dynamicColumns = computed<DynamicColumn[]>(() => {
  const byHeader = new Map<string, Map<string, ViewColumn>>()
  for (const instance of items.value) {
    const columns = viewByTemplate.value.get(instance.template)?.columns ?? []
    for (const column of columns) {
      let definitions = byHeader.get(column.header)
      if (!definitions) {
        definitions = new Map()
        byHeader.set(column.header, definitions)
      }
      definitions.set(instance.template, column)
    }
  }
  return [...byHeader].map(([label, definitionByTemplate]) => ({
    key: `view:${label}`,
    label,
    definitionByTemplate,
  }))
})

const columns = computed(() => [
  { key: 'name', label: 'Name' },
  { key: 'template', label: 'Template' },
  ...dynamicColumns.value.map(({ key, label }) => ({ key, label })),
  { key: 'status', label: 'Status' },
  { key: 'age', label: 'Age' },
  { key: 'actions', label: '' },
])

function instanceKey(instance: Pick<Instance, 'template' | 'name'>): string {
  return `${instance.template}/${instance.name}`
}

const visibleItems = computed(() => items.value.filter(item => !tombstones.has(instanceKey(item), item.uid)))

const rows = computed<Array<Record<string, unknown>>>(() => visibleItems.value.map(instance => {
  const row: Record<string, unknown> = {
    name: instance.name,
    rowKey: instanceKey(instance),
    template: instance.template,
    status: instance.phase,
    age: formatAge(instance.createdAt),
    actions: '',
    instance,
  }
  for (const column of dynamicColumns.value) {
    const definition = column.definitionByTemplate.get(instance.template)
    row[column.key] = definition ? resolve(definition, instance) : null
  }
  return row
}))

function rowInstance(row: Record<string, unknown>): Instance {
  return row.instance as Instance
}

function resolvedValue(value: unknown): ResolvedValue | null {
  return value && typeof value === 'object' ? value as ResolvedValue : null
}

function errorMessage(error: unknown, fallback: string): string {
  const value = error as { reason?: string; message?: string }
  return value.reason ? `${value.reason}: ${value.message || fallback}` : value.message || fallback
}

const refresh = createLatestRefreshController(async requestID => {
  loading.value = true
  try {
    // listInstances returns the exact Template index used to discover and
    // enrich these rows, so dynamic columns cannot arrive in a second paint.
    const result = await api.listInstances()
    if (!refresh.isCurrent(requestID)) return
    const views = new Map<string, TemplateView>()
    for (const template of result.templates) if (template.view) views.set(template.name, template.view)
    viewByTemplate.value = views
    items.value = result.items
    tombstones.reconcile(result.items.map(item => ({ name: instanceKey(item), uid: item.uid })))
    loaded.value = true
    error.value = null
  } catch (caught) {
    if (!refresh.isCurrent(requestID) || isContextChangedError(caught)) return
    error.value = errorMessage(caught, 'failed to list instances')
  } finally {
    if (refresh.isCurrent(requestID)) loading.value = false
  }
})

function load(): Promise<void> {
  return refresh.request()
}

async function deleteInstance(instance: Instance) {
  if (deletingInstanceKey.value !== null) return
  deleteError.value = null
  const confirmed = await confirmDialog({
    title: `Delete instance "${instance.name}"?`,
    message: `This permanently deletes "${instance.name}" (${instance.template}) and the resources it provisioned.`,
    confirmLabel: 'Delete instance',
    danger: true,
  })
  if (!confirmed || deletingInstanceKey.value !== null) return

  deletingInstanceKey.value = instanceKey(instance)
  try {
    await api.deleteInstance(instance.name, instance.template)
    tombstones.add(instanceKey(instance), instance.uid)
    await load()
  } catch (caught) {
    if (!isContextChangedError(caught)) deleteError.value = errorMessage(caught, 'delete failed')
  } finally {
    deletingInstanceKey.value = null
  }
}

function selectInstance(row: Record<string, unknown>) {
  const instance = rowInstance(row)
  if (deletingInstanceKey.value === instanceKey(instance) || tombstones.has(instanceKey(instance), instance.uid)) return
  emit('select', instance.name, instance.template)
}

function formatAge(timestamp?: string): string {
  if (!timestamp) return '-'
  const then = new Date(timestamp).getTime()
  if (Number.isNaN(then)) return '-'
  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

onMounted(() => {
  void load()
  pollHandle = window.setInterval(() => { void load() }, 10000)
})
onUnmounted(() => {
  if (pollHandle !== null) window.clearInterval(pollHandle)
  refresh.stop()
})
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <h2 class="page-title">My instances</h2>
        <p class="page-meta">Provisioned into the active workspace.</p>
      </div>
      <div class="instance-list-actions">
        <span class="refresh-cadence">auto-refresh 10s</span>
        <button type="button" class="primary" @click="emit('navigate', 'catalog')">Browse templates</button>
      </div>
    </header>

    <div v-if="deleteError" class="mutation-error" role="alert" aria-live="assertive">
      <span>{{ deleteError }}</span>
      <button type="button" class="read-retry" @click="deleteError = null">Dismiss</button>
    </div>

    <ResourceTable
      :columns="columns"
      :rows="rows"
      row-key="rowKey"
      :loaded="loaded"
      :loading="loading"
      :error="error"
      :stale="loaded && !!error"
      retryable
      empty-text="No instances in this workspace yet."
      @retry="load"
      @row-click="selectInstance"
    >
      <template #name="{ value }"><span class="instance-name">{{ value }}</span></template>
      <template #template="{ value }"><code>{{ value }}</code></template>
      <template v-for="column in dynamicColumns" :key="column.key" v-slot:[column.key]="{ value }">
        <ViewValue v-if="resolvedValue(value)" :value="resolvedValue(value)!" />
        <span v-else class="cell-empty">—</span>
      </template>
      <template #status="{ row }"><StatusBadge :status="rowInstance(row).phase" /></template>
      <template #age="{ value }"><span class="cell-mono">{{ value }}</span></template>
      <template #actions="{ row }">
        <div class="row-actions">
          <ResourceTableDeleteButton
            :label="`Delete instance ${rowInstance(row).name}`"
            :busy-label="`Deleting instance ${rowInstance(row).name}…`"
            :busy="deletingInstanceKey === instanceKey(rowInstance(row))"
            :disabled="deletingInstanceKey !== null"
            @click="deleteInstance(rowInstance(row))"
          />
        </div>
      </template>
    </ResourceTable>

    <div v-if="loaded && !error && visibleItems.length === 0" class="empty-followup">
      <span>Each workspace has its own instances.</span>
      <button type="button" class="link" @click="emit('navigate', 'catalog')">Browse templates</button>
    </div>
  </section>
</template>
