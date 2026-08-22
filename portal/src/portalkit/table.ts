// CANONICAL SOURCE — provider-sdk/portalkit-vue. Do not edit vendored copies
// under providers/*/portal/src/portalkit/; edit here and run `make sync-portalkit`.

export interface TableFilterOption {
  value: string
  label: string
}

export interface TableFilterDefinition {
  key: string
  label: string
  allLabel?: string
  options?: TableFilterOption[]
}

function scalarText(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return String(value)
  return ''
}

function collectText(value: unknown, seen: Set<unknown>): string[] {
  const scalar = scalarText(value)
  if (scalar) return [scalar]
  if (!value || typeof value !== 'object' || seen.has(value)) return []
  seen.add(value)
  if (Array.isArray(value)) return value.flatMap(item => collectText(item, seen))
  return Object.values(value as Record<string, unknown>).flatMap(item => collectText(item, seen))
}

export function tableSearchText(row: Record<string, unknown>, keys?: string[]): string {
  const values = keys?.length ? keys.map(key => row[key]) : Object.values(row)
  return values.flatMap(value => collectText(value, new Set())).join(' ').toLocaleLowerCase()
}

export function tableFilterValues(value: unknown): string[] {
  if (Array.isArray(value)) return value.flatMap(tableFilterValues)
  const text = scalarText(value).trim()
  return text ? [text] : []
}

export function deriveTableFilterOptions(
  rows: Array<Record<string, unknown>>,
  definition: TableFilterDefinition,
): TableFilterOption[] {
  if (definition.options) return definition.options
  const values = new Set<string>()
  rows.forEach(row => tableFilterValues(row[definition.key]).forEach(value => values.add(value)))
  return [...values]
    .sort((left, right) => left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' }))
    .map(value => ({ value, label: value }))
}

export function filterTableRows(
  rows: Array<Record<string, unknown>>,
  query: string,
  searchKeys: string[] | undefined,
  selectedFilters: Record<string, string>,
): Array<Record<string, unknown>> {
  const normalizedQuery = query.trim().toLocaleLowerCase()
  return rows.filter(row => {
    if (normalizedQuery && !tableSearchText(row, searchKeys).includes(normalizedQuery)) return false
    return Object.entries(selectedFilters).every(([key, selected]) =>
      !selected || tableFilterValues(row[key]).some(value => value === selected),
    )
  })
}

export function tablePageCount(total: number, pageSize: number): number {
  return Math.max(1, Math.ceil(Math.max(0, total) / Math.max(1, pageSize)))
}

export function paginateTableRows(
  rows: Array<Record<string, unknown>>,
  page: number,
  pageSize: number,
): Array<Record<string, unknown>> {
  const safeSize = Math.max(1, pageSize)
  const safePage = Math.max(1, Math.min(page, tablePageCount(rows.length, safeSize)))
  const start = (safePage - 1) * safeSize
  return rows.slice(start, start + safeSize)
}

export function tableRange(total: number, page: number, pageSize: number): { start: number; end: number } {
  if (total <= 0) return { start: 0, end: 0 }
  const safeSize = Math.max(1, pageSize)
  const safePage = Math.max(1, Math.min(page, tablePageCount(total, safeSize)))
  const start = (safePage - 1) * safeSize + 1
  return { start, end: Math.min(total, start + safeSize - 1) }
}
