import type { Connection, Warehouse } from './types.js'
import type { ResourceTableChange, TableFilterDefinition, TableFilterOption, TablePageInfo } from './portalkit/table.js'

/** The default page shown by the resource lists before a query is entered. */
export const DATABRICKS_PAGE_SIZE = 10

/** Supporting resources are loaded once for form/filter choices. */
export const DATABRICKS_SUPPORT_PAGE_SIZE = 100

export type DatabricksPaginationMode = 'server' | 'client'

export interface ConnectionFilterValues {
  authType: string
  status: string
}

export interface WarehouseFilterValues {
  connectionRef: string
  state: string
  status: string
}

export interface TableFilterValues {
  warehouseRef: string
  status: string
}

export const EMPTY_CONNECTION_FILTERS: ConnectionFilterValues = {
  authType: '',
  status: '',
}

export const EMPTY_WAREHOUSE_FILTERS: WarehouseFilterValues = {
  connectionRef: '',
  state: '',
  status: '',
}

export const EMPTY_TABLE_FILTERS: TableFilterValues = {
  warehouseRef: '',
  status: '',
}

// These are the values produced by api.ts. Keep the options explicit because
// server pagination only supplies one page; deriving them from that page makes
// a filter silently incomplete.
export const RESOURCE_STATUS_OPTIONS: TableFilterOption[] = [
  { value: 'Ready', label: 'Ready' },
  { value: 'Pending', label: 'Pending' },
  { value: 'Retrying', label: 'Retrying' },
  { value: 'Needs attention', label: 'Needs attention' },
  { value: 'Status unavailable', label: 'Status unavailable' },
]

// Databricks' SQL warehouse API uses these states. Keep UNKNOWN available for
// old controllers and newly introduced upstream states that have no value yet.
export const WAREHOUSE_STATE_OPTIONS: TableFilterOption[] = [
  { value: 'PENDING', label: 'Pending' },
  { value: 'STARTING', label: 'Starting' },
  { value: 'RUNNING', label: 'Running' },
  { value: 'STOPPING', label: 'Stopping' },
  { value: 'STOPPED', label: 'Stopped' },
  { value: 'DELETING', label: 'Deleting' },
  { value: 'FAILED', label: 'Failed' },
  { value: 'UNKNOWN', label: 'Unknown' },
]

export const CONNECTION_FILTERS: TableFilterDefinition[] = [
  {
    key: 'authType',
    label: 'Auth',
    options: [{ value: 'pat', label: 'PAT' }],
  },
  {
    key: 'status',
    label: 'Status',
    allLabel: 'Any status',
    options: RESOURCE_STATUS_OPTIONS,
  },
]

function resourceOptions(values: readonly string[]): TableFilterOption[] {
  return [...new Set(values.filter(Boolean))]
    .sort((left, right) => left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' }))
    .map(value => ({ value, label: value }))
}

export function warehouseFilters(connections: readonly Connection[]): TableFilterDefinition[] {
  return [
    {
      key: 'connectionRef',
      label: 'Connection',
      options: resourceOptions(connections.map(connection => connection.name)),
    },
    {
      key: 'state',
      label: 'State',
      options: WAREHOUSE_STATE_OPTIONS,
    },
    {
      key: 'status',
      label: 'Status',
      allLabel: 'Any status',
      options: RESOURCE_STATUS_OPTIONS,
    },
  ]
}

export function tableFilters(warehouses: readonly Warehouse[]): TableFilterDefinition[] {
  return [
    {
      key: 'warehouseRef',
      label: 'Warehouse',
      options: resourceOptions(warehouses.map(warehouse => warehouse.name)),
    },
    {
      key: 'status',
      label: 'Status',
      allLabel: 'Any status',
      options: RESOURCE_STATUS_OPTIONS,
    },
  ]
}

export function cloneConnectionFilters(filters: ConnectionFilterValues): ConnectionFilterValues {
  return { authType: filters.authType, status: filters.status }
}

export function cloneWarehouseFilters(filters: WarehouseFilterValues): WarehouseFilterValues {
  return { connectionRef: filters.connectionRef, state: filters.state, status: filters.status }
}

export function cloneTableFilters(filters: TableFilterValues): TableFilterValues {
  return { warehouseRef: filters.warehouseRef, status: filters.status }
}

export function hasActiveFilters<T extends object>(query: string, filters: T): boolean {
  return !!query.trim() || Object.values(filters).some(Boolean)
}

/**
 * Keep server cursor navigation controlled by the table event. Query/filter
 * changes are the only events that intentionally return to the first page;
 * ordinary page and page-size events already carry their authoritative cursor
 * and page values.
 */
export function serverCursorChange(change: Pick<ResourceTableChange, 'reason' | 'page' | 'cursor'>): { page: number; cursor: string | null } {
  const reset = change.reason === 'query' || change.reason === 'filter'
  return reset ? { page: 1, cursor: null } : { page: change.page, cursor: change.cursor }
}

/** Cursor values are opaque; never manufacture a total from remainingItemCount. */
export function pageInfo(nextCursor?: string): TablePageInfo {
  const cursor = nextCursor || null
  return { hasNext: cursor !== null, nextCursor: cursor }
}
