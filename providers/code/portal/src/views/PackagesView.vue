<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '../api'
import type { ErrorResponse, PackageRow } from '../types'
import ResourceTable from '../portalkit/ResourceTable.vue'
import StatusBadge from '../portalkit/StatusBadge.vue'
import { createLatestRefreshController, type LatestRefreshController } from '../refresh'
import {
  clonePackageFilters,
  EMPTY_PACKAGE_FILTERS,
  hasActivePackageFilters,
  PACKAGE_FILTERS,
  PACKAGE_PAGE_SIZE,
  packagePageInfo as toPackagePageInfo,
  packageVisibility,
  type PackageFilterValues,
  type PackagePageInfo,
  type PackagePaginationMode,
} from '../packagesPagination'

const emit = defineEmits<{ (e: 'open', repositoryRef: string): void }>()

const packages = ref<PackageRow[]>([])
const error = ref<string | null>(null)
const loading = ref(false)
const loaded = ref(false)
const packageMode = ref<PackagePaginationMode>('server')
const packagePage = ref(1)
const packagePageSize = ref(PACKAGE_PAGE_SIZE)
const packageQuery = ref('')
const packageFilters = ref<PackageFilterValues>(clonePackageFilters(EMPTY_PACKAGE_FILTERS))
const packageCursor = ref<string | null>(null)
const pageInfo = ref<PackagePageInfo | null>(null)
const columns = [
  { key: 'repositoryRef', label: 'Repository' },
  { key: 'name', label: 'Package' },
  { key: 'type', label: 'Type' },
  { key: 'visibility', label: 'Visibility' },
  { key: 'versionCount', label: 'Versions' },
  { key: 'status', label: 'Status' },
  { key: 'url', label: '' },
]
function controllerCaughtUp(resource: { generation?: number; observedGeneration?: number }): boolean {
  return resource.generation === undefined ||
    (resource.observedGeneration !== undefined && resource.observedGeneration >= resource.generation)
}
const rows = computed<Array<Record<string, unknown>>>(() => [...packages.value]
  .sort((a, b) => a.repositoryRef.localeCompare(b.repositoryRef) || a.type.localeCompare(b.type) || a.name.localeCompare(b.name))
  .map(item => {
    const deleting = !!item.deletionTimestamp
    return {
      ...item,
      visibility: packageVisibility(item.visibility),
      deleting,
      rowKey: `${item.repositoryRef}:${item.uid || `${item.type}/${item.name}`}`,
      status: deleting ? 'Deleting' : !controllerCaughtUp(item) ? 'pending' : item.ready ? 'ready' : item.message ? 'failed' : 'pending',
      url: item.htmlURL || '',
    }
  }))

let timer: number | undefined
let refresh!: LatestRefreshController

function errMessage(e: unknown): string {
  const err = e as ErrorResponse
  return err.reason ? `${err.reason}: ${err.message}` : err.message || String(e)
}

function load() {
  refresh.request()
}

interface PackageRequest {
  mode: PackagePaginationMode
  active: boolean
  page: number
  pageSize: number
  query: string
  filters: PackageFilterValues
  cursor: string | null
}

function currentPackageRequest(): PackageRequest {
  const filters = clonePackageFilters(packageFilters.value)
  return {
    mode: packageMode.value,
    active: hasActivePackageFilters(packageQuery.value, filters),
    page: packagePage.value,
    pageSize: packagePageSize.value,
    query: packageQuery.value,
    filters,
    cursor: packageCursor.value,
  }
}

function packageRequestIsCurrent(requestID: number, request: PackageRequest): boolean {
  const current = currentPackageRequest()
  return refresh.isCurrent(requestID) &&
    current.mode === request.mode &&
    current.active === request.active &&
    current.page === request.page &&
    current.pageSize === request.pageSize &&
    current.query === request.query &&
    current.cursor === request.cursor &&
    current.filters.type === request.filters.type &&
    current.filters.visibility === request.filters.visibility &&
    current.filters.status === request.filters.status
}

refresh = createLatestRefreshController(async requestID => {
  const request = currentPackageRequest()
  loading.value = true
  // A page from the inactive server query must never be rendered as though it
  // matched a newly-entered search/filter. Keep old rows for same-page polling,
  // but clear them when switching to the complete client-side query.
  if (request.active && request.mode === 'server') {
    packages.value = []
    pageInfo.value = null
  }
  try {
    if (request.active || request.mode === 'client') {
      const next = await api.listAllPackages()
      if (!packageRequestIsCurrent(requestID, request)) return
      packages.value = next
      if (request.mode === 'server') {
        packageMode.value = 'client'
        packagePage.value = 1
      }
      packageCursor.value = null
      pageInfo.value = null
    } else {
      const next = await api.listAllPackagesPage({
        limit: request.pageSize,
        ...(request.cursor ? { continue: request.cursor } : {}),
      })
      if (!packageRequestIsCurrent(requestID, request)) return
      packages.value = next.items
      packageCursor.value = request.cursor
      pageInfo.value = toPackagePageInfo(next.continue)
    }
    loaded.value = true
    error.value = null
  } catch (e) {
    if (!packageRequestIsCurrent(requestID, request)) return
    const err = e as ErrorResponse
    error.value = err.reason === 'TenantMissing' ? null : errMessage(e)
  } finally {
    if (refresh.isCurrent(requestID)) loading.value = false
  }
})

function handlePackageChange(change: {
  reason: 'page' | 'page-size' | 'query' | 'filter'
  page: number
  pageSize: number
  query: string
  filters: Record<string, string>
  cursor: string | null
}) {
  const filters: PackageFilterValues = {
    type: change.filters.type || '',
    visibility: change.filters.visibility || '',
    status: change.filters.status || '',
  }
  const active = hasActivePackageFilters(change.query, filters)
  packagePage.value = change.page
  packagePageSize.value = change.pageSize
  packageQuery.value = change.query
  packageFilters.value = filters
  packageCursor.value = change.cursor
  pageInfo.value = null

  if (!active) {
    packageMode.value = 'server'
    packages.value = []
    load()
    return
  }

  if (packageMode.value === 'client') return
  packages.value = []
  load()
}

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
        <h2 class="page-title">Packages</h2>
        <p class="page-meta">Artifacts (container images, npm/maven packages, …) published under the workspace's repositories. Observed state — they appear automatically when artifacts are pushed.</p>
      </div>
    </header>

    <ResourceTable
      :columns="columns"
      :rows="rows"
      searchable
      search-placeholder="Search packages…"
      :filters="PACKAGE_FILTERS"
      :pagination-mode="packageMode"
      :page="packagePage"
      :page-size="packagePageSize"
      :query="packageQuery"
      :filter-values="packageFilters"
      :cursor="packageCursor"
      :page-info="pageInfo"
      row-key="rowKey"
      :loaded="loaded"
      :loading="loading"
      :error="error"
      :stale="loaded && !!error"
      retryable
      empty-text="No packages published in this workspace yet."
      :interactive="false"
      @retry="load"
      @change="handlePackageChange"
    >
      <template #repositoryRef="{ row }">
        <button v-if="!row.deleting" class="link" type="button" @click="emit('open', String(row.repositoryRef))">{{ row.repositoryRef }}</button>
        <span v-else>{{ row.repositoryRef }}</span>
      </template>
      <template #name="{ row }"><strong><a v-if="row.htmlURL && !row.deleting" :href="String(row.htmlURL)" target="_blank" rel="noopener">{{ row.name }}</a><template v-else>{{ row.name }}</template></strong></template>
      <template #type="{ value }"><span class="badge muted">{{ value }}</span></template>
      <template #visibility="{ value }"><span class="muted">{{ value === 'unknown' ? '—' : value }}</span></template>
      <template #versionCount="{ value }"><span class="muted">{{ value || 0 }}</span></template>
      <template #status="{ row }"><StatusBadge :status="String(row.status)" :tone="row.deleting ? 'warning' : null" :title="String(row.message || '')" /></template>
      <template #url="{ row }"><a v-if="row.htmlURL && !row.deleting" class="link" :href="String(row.htmlURL)" target="_blank" rel="noopener">View ↗</a></template>
    </ResourceTable>
    <p class="muted">Packages appear automatically when artifacts are pushed (e.g. <code>docker push</code>, <code>npm publish</code>); the provider crawls each repository periodically.</p>
  </section>
</template>
