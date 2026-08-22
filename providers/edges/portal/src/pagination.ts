import type { TablePageInfo } from './portalkit/table'

export type PaginationMode = 'server' | 'client'

export interface TableRequestState {
  mode: PaginationMode
  active: boolean
  page: number
  pageSize: number
  query: string
  filters: Record<string, string>
  cursor: string | null
}

/** Query and selected filters switch a table from one-page server mode to a complete local view. */
export function hasActiveTableFilters(query: string, filters: Record<string, string>): boolean {
  return !!query.trim() || Object.values(filters).some(Boolean)
}

/** Keep ResourceTable's cursor contract explicit without inventing a total. */
export function tablePageInfo(nextCursor?: string | null): TablePageInfo {
  const cursor = nextCursor || null
  return { hasNext: cursor !== null, nextCursor: cursor }
}

/** Compare every user-visible request dimension before committing an async read. */
export function sameTableRequest(current: TableRequestState, expected: TableRequestState): boolean {
  if (current.mode !== expected.mode || current.active !== expected.active ||
    current.page !== expected.page || current.pageSize !== expected.pageSize ||
    current.query !== expected.query || current.cursor !== expected.cursor) return false
  const keys = new Set([...Object.keys(current.filters), ...Object.keys(expected.filters)])
  return [...keys].every(key => current.filters[key] === expected.filters[key])
}
