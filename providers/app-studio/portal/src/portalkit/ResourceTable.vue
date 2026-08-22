<!-- CANONICAL SOURCE — provider-sdk/portalkit-vue. Do not edit vendored copies under providers/*/portal/src/portalkit/; edit here and run `make sync-portalkit`. -->
<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { AlertCircle, ChevronLeft, ChevronRight, Inbox, Search, X } from 'lucide-vue-next'
import resourceTableStyles from './ResourceTable.css?raw'
import {
  deriveTableFilterOptions,
  filterTableRows,
  paginateTableRows,
  tablePageCount,
  tableRange,
  type TableFilterDefinition,
} from './table'

const STYLE_ID = 'faros-portalkit-resource-table-css'

const props = withDefaults(defineProps<{
  columns: Array<{ key: string; label: string }>
  rows: Array<Record<string, unknown>>
  /** Stable row identity. Resource names/ids are used when omitted. */
  rowKey?: string | ((row: Record<string, unknown>, index: number) => string | number)
  /** True after the first authoritative read has completed successfully. */
  loaded?: boolean | null
  /** True while a read is in flight. Cached rows remain visible after load. */
  loading?: boolean
  error?: string | null
  /** Marks cached rows as stale when the latest read failed. */
  stale?: boolean
  /** Shows the built-in Retry action. Callers must handle the retry event. */
  retryable?: boolean
  emptyText?: string
  filterEmptyText?: string
  interactive?: boolean
  searchable?: boolean
  searchPlaceholder?: string
  searchKeys?: string[]
  filters?: TableFilterDefinition[]
  paginated?: boolean
  pageSize?: number
  pageSizeOptions?: number[]
}>(), {
  // Vue casts an omitted Boolean prop to false in child components. A null
  // sentinel preserves omission so legacy callers retain loading -> content
  // behavior while explicit false still means the first read is incomplete.
  loaded: null,
  stale: false,
  retryable: false,
  emptyText: 'No data',
  filterEmptyText: 'No resources match these filters.',
  interactive: true,
  searchable: false,
  searchPlaceholder: 'Search…',
  searchKeys: () => [],
  filters: () => [],
  paginated: false,
  pageSize: 10,
  pageSizeOptions: () => [10, 25, 50],
})

const query = ref('')
const page = ref(1)
const selectedPageSize = ref(Math.max(1, props.pageSize))
const selectedFilters = reactive<Record<string, string>>({})

const explicitReadState = computed(() => props.loaded !== null)
const showInitialError = computed(() =>
  explicitReadState.value ? props.loaded === false && !!props.error : !!props.error,
)
const showInitialLoading = computed(() =>
  explicitReadState.value ? props.loaded === false : !!props.loading,
)
const ariaBusy = computed(() =>
  explicitReadState.value
    ? (!!props.loading && !(props.loaded === false && !!props.error)) || (props.loaded === false && !props.error)
    : !!props.loading,
)
const filterOptions = computed(() => Object.fromEntries(
  props.filters.map(definition => [definition.key, deriveTableFilterOptions(props.rows, definition)]),
))
const filteredRows = computed(() => filterTableRows(
  props.rows,
  query.value,
  props.searchKeys.length ? props.searchKeys : props.columns.map(column => column.key).filter(key => key !== 'actions'),
  selectedFilters,
))
const totalPages = computed(() => tablePageCount(filteredRows.value.length, selectedPageSize.value))
const visibleRows = computed(() => props.paginated
  ? paginateTableRows(filteredRows.value, page.value, selectedPageSize.value)
  : filteredRows.value,
)
const visibleRange = computed(() => tableRange(filteredRows.value.length, page.value, selectedPageSize.value))
const activeFilters = computed(() => !!query.value.trim() || Object.values(selectedFilters).some(Boolean))
const showControls = computed(() =>
  (props.searchable || props.filters.length > 0) && (props.rows.length > 0 || activeFilters.value),
)
const showPagination = computed(() => props.rows.length > 0 && props.paginated)
const normalizedPageSizes = computed(() => [...new Set([...props.pageSizeOptions, props.pageSize])]
  .filter(value => Number.isFinite(value) && value > 0)
  .sort((left, right) => left - right))

const emit = defineEmits<{
  rowClick: [row: Record<string, unknown>]
  retry: []
}>()

watch(() => props.pageSize, value => { selectedPageSize.value = Math.max(1, value); page.value = 1 })
watch([query, selectedPageSize, () => props.filters.map(filter => selectedFilters[filter.key] ?? '').join('\u0000')], () => { page.value = 1 })
watch(totalPages, value => { page.value = Math.min(page.value, value) })
watch(() => props.filters.map(filter => filter.key), keys => {
  keys.forEach(key => { if (!(key in selectedFilters)) selectedFilters[key] = '' })
  Object.keys(selectedFilters).forEach(key => { if (!keys.includes(key)) delete selectedFilters[key] })
}, { immediate: true })

onMounted(() => {
  if (document.getElementById(STYLE_ID)) return
  const style = document.createElement('style')
  style.id = STYLE_ID
  style.textContent = resourceTableStyles
  document.head.appendChild(style)
})

function onRowClick(row: Record<string, unknown>) {
  if (props.interactive) emit('rowClick', row)
}

function rowIdentity(row: Record<string, unknown>, index: number): string | number {
  if (typeof props.rowKey === 'function') return props.rowKey(row, index)
  if (typeof props.rowKey === 'string') {
    const value = row[props.rowKey]
    if (typeof value === 'string' || typeof value === 'number') return value
  }
  for (const key of ['name', 'id', 'uid']) {
    const value = row[key]
    if (typeof value === 'string' || typeof value === 'number') return value
  }
  return index
}

function clearFilters() {
  query.value = ''
  Object.keys(selectedFilters).forEach(key => { selectedFilters[key] = '' })
}

function previousPage() { page.value = Math.max(1, page.value - 1) }
function nextPage() { page.value = Math.min(totalPages.value, page.value + 1) }
</script>

<template>
  <div class="resource-table" :aria-busy="ariaBusy">
    <span class="resource-table-live" role="status" aria-live="polite" aria-atomic="true" style="block-size: 1px; clip: rect(0 0 0 0); clip-path: inset(50%); inline-size: 1px; margin: -1px; overflow: hidden; padding: 0; position: absolute; white-space: nowrap;">
      {{ explicitReadState && loading && loaded ? 'Updating…' : '' }}
    </span>
    <div v-if="showInitialError" class="resource-table-error" role="alert" aria-live="assertive">
      <AlertCircle class="resource-table-error-icon" :stroke-width="1.75" />
      <span class="resource-table-error-message">{{ error }}</span>
      <button v-if="retryable" class="resource-table-retry" type="button" @click="emit('retry')">Retry</button>
    </div>

    <div v-else-if="showInitialLoading" class="resource-table-loading" role="status" aria-live="polite" aria-label="Loading resources">
      <div class="resource-table-loading-head"><div class="shimmer resource-table-skeleton resource-table-skeleton-short" /></div>
      <div v-for="i in 5" :key="i" class="resource-table-loading-row">
        <div class="shimmer resource-table-skeleton resource-table-skeleton-wide" />
        <div class="shimmer resource-table-skeleton resource-table-skeleton-mid" />
        <div class="shimmer resource-table-skeleton resource-table-skeleton-small" />
      </div>
    </div>

    <template v-else>
      <div v-if="explicitReadState && error" class="resource-table-stale" role="alert" aria-live="assertive">
        <AlertCircle class="resource-table-error-icon" :stroke-width="1.75" />
        <span class="resource-table-error-message">{{ stale ? 'Showing the last successful result. ' : '' }}{{ error }}</span>
        <button v-if="retryable" class="resource-table-retry" type="button" @click="emit('retry')">Retry</button>
      </div>

      <div v-if="showControls" class="resource-table-controls" role="search" aria-label="Filter table">
        <label v-if="searchable" class="resource-table-search">
          <span class="sr-only" style="position:absolute;block-size:1px;inline-size:1px;overflow:hidden;clip:rect(0 0 0 0)">Search table</span>
          <Search class="resource-table-search-icon" :stroke-width="1.75" aria-hidden="true" />
          <input v-model="query" class="resource-table-search-input" type="search" :placeholder="searchPlaceholder" autocomplete="off">
          <button v-if="query" class="resource-table-search-clear" type="button" aria-label="Clear search" @click="query = ''"><X :stroke-width="1.75" /></button>
        </label>
        <label v-for="filter in filters" :key="filter.key">
          <span class="sr-only" style="position:absolute;block-size:1px;inline-size:1px;overflow:hidden;clip:rect(0 0 0 0)">Filter by {{ filter.label }}</span>
          <select v-model="selectedFilters[filter.key]" class="resource-table-filter-select" :aria-label="`Filter by ${filter.label}`">
            <option value="">{{ filter.allLabel || `All ${filter.label.toLocaleLowerCase()}` }}</option>
            <option v-for="option in filterOptions[filter.key]" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </label>
        <button v-if="activeFilters" class="resource-table-clear-filters" type="button" @click="clearFilters">Clear filters</button>
      </div>

      <div class="resource-table-scroll" role="region" aria-label="Scrollable table" tabindex="0">
        <table class="resource-table-table">
          <thead><tr class="resource-table-head-row"><th v-for="col in columns" :key="col.key" class="resource-table-heading">{{ col.label }}</th></tr></thead>
          <tbody>
            <template v-for="(row, i) in visibleRows" :key="rowIdentity(row, i)">
              <tr class="stagger-item resource-table-row" :class="{ 'is-interactive': interactive }" :style="{ animationDelay: `${i * 35}ms` }" @click="onRowClick(row)">
                <td v-for="col in columns" :key="col.key" class="resource-table-cell">
                  <slot :name="col.key" :value="row[col.key]" :row="row">{{ row[col.key] }}</slot>
                </td>
              </tr>
              <slot name="after-row" :row="row" />
            </template>
            <tr v-if="visibleRows.length === 0"><td :colspan="columns.length" class="resource-table-empty-cell">
              <Inbox class="resource-table-empty-icon" :stroke-width="1.25" />
              <p class="resource-table-empty-label">{{ activeFilters ? filterEmptyText : emptyText }}</p>
            </td></tr>
          </tbody>
        </table>
      </div>

      <footer v-if="showPagination" class="resource-table-pagination" aria-label="Table pagination">
        <div class="resource-table-range" aria-live="polite">
          <template v-if="filteredRows.length">Showing <strong>{{ visibleRange.start }}–{{ visibleRange.end }}</strong> of <strong>{{ filteredRows.length }}</strong></template>
          <template v-else>Showing <strong>0</strong> results</template>
        </div>
        <label class="resource-table-page-size">Rows per page
          <select v-model.number="selectedPageSize" class="resource-table-page-size-select" aria-label="Rows per page">
            <option v-for="size in normalizedPageSizes" :key="size" :value="size">{{ size }}</option>
          </select>
        </label>
        <div class="resource-table-page-actions">
          <button class="resource-table-page-button" type="button" aria-label="Previous page" :disabled="page <= 1" @click="previousPage"><ChevronLeft :stroke-width="1.75" /></button>
          <span class="resource-table-page-indicator" aria-live="polite">{{ page }} / {{ totalPages }}</span>
          <button class="resource-table-page-button" type="button" aria-label="Next page" :disabled="page >= totalPages || filteredRows.length === 0" @click="nextPage"><ChevronRight :stroke-width="1.75" /></button>
        </div>
      </footer>
    </template>
  </div>
</template>
