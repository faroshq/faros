// Lit adapter for PortalKit's canonical ResourceTable DOM and CSS contract.
//
// The shared ResourceTable implementation is currently a Vue SFC, while the
// Agents portal is Lit. Keep framework-specific rendering here so collection
// views provide the same queryable table behavior without hand-building a
// second visual language in every view.

import { html, nothing, type TemplateResult } from 'lit'
import { repeat } from 'lit/directives/repeat.js'
import { icon, type IconName } from './icon'
import type { ResourceTableFilterOption } from '../portalkit/resource-table-filter'

import '../portalkit/resource-table-filter'

export interface ResourceTableState {
  query: string
  filters: Record<string, string>
  page: number
  pageSize: number
}

export interface ResourceTableFilter<Row> {
  key: string
  label: string
  allLabel?: string
  value: (row: Row) => string
  labelFor?: (value: string) => string
}

export interface ResourceTableColumn<Row> {
  key: string
  label: string
  primary?: boolean
  align?: 'start' | 'center' | 'end'
  render: (row: Row) => unknown
}

export interface ResourceTableOptions<Row> {
  ariaLabel: string
  rows: Row[]
  rowKey: (row: Row) => string
  columns: ResourceTableColumn<Row>[]
  state: ResourceTableState
  onStateChange: (state: ResourceTableState) => void
  searchPlaceholder: string
  searchText: (row: Row) => string
  filters?: ResourceTableFilter<Row>[]
  pageSizeOptions?: number[]
  emptyText?: string
  searchEmptyText?: string
  actions?: (row: Row) => unknown
}

export interface ResourceTableActionOptions {
  icon: IconName
  label: string
  tone?: 'neutral' | 'accent' | 'warning' | 'danger' | 'edit' | 'delete'
  disabled?: boolean
  onClick: (event: MouseEvent) => void
}

export function createResourceTableState(): ResourceTableState {
  return { query: '', filters: {}, page: 1, pageSize: 10 }
}

export function resourceTableAction(options: ResourceTableActionOptions): TemplateResult {
  const tone = options.tone || 'neutral'
  return html`<button
    class="k-table-action k-table-action--${tone}"
    type="button"
    data-k-tip=${options.label}
    aria-label=${options.label}
    ?disabled=${options.disabled}
    @click=${(event: MouseEvent) => {
      event.stopPropagation()
      options.onClick(event)
    }}
  >${icon(options.icon, 'k-table-action__icon')}</button>`
}

export function resourceTable<Row>(options: ResourceTableOptions<Row>): TemplateResult {
  const filters = options.filters || []
  const filterOptions = Object.fromEntries(filters.map((filter) => [
    filter.key,
    [...new Set(options.rows.map((row) => filter.value(row)).filter(Boolean))]
      .sort((left, right) => left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' })),
  ]))
  const normalizedFilters = Object.fromEntries(filters.map((filter) => {
    const selected = options.state.filters[filter.key] || ''
    return [filter.key, selected && filterOptions[filter.key].includes(selected) ? selected : '']
  }))
  const normalizedQuery = options.state.query.trim().toLocaleLowerCase()
  const activeFilters = filters.some((filter) => normalizedFilters[filter.key])
  const filtered = options.rows.filter((row) => {
    if (normalizedQuery && !options.searchText(row).toLocaleLowerCase().includes(normalizedQuery)) return false
    return filters.every((filter) => {
      const selected = normalizedFilters[filter.key]
      return !selected || filter.value(row) === selected
    })
  })
  const pageSize = Math.max(1, options.state.pageSize)
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize))
  const page = Math.min(Math.max(1, options.state.page), pageCount)
  const startIndex = (page - 1) * pageSize
  const visibleRows = filtered.slice(startIndex, startIndex + pageSize)
  const rangeStart = filtered.length ? startIndex + 1 : 0
  const rangeEnd = Math.min(filtered.length, startIndex + visibleRows.length)
  const pageSizes = [...new Set([...(options.pageSizeOptions || [10, 25, 50]), pageSize])].sort((a, b) => a - b)
  const primaryColumn = options.columns.find((column) => column.primary) || options.columns[0]
  const filtersChanged = filters.some((filter) => (options.state.filters[filter.key] || '') !== normalizedFilters[filter.key])
  if (page !== options.state.page || filtersChanged) {
    queueMicrotask(() => options.onStateChange({ ...options.state, filters: normalizedFilters, page }))
  }
  const update = (change: Partial<ResourceTableState>, resetPage = false): void => {
    options.onStateChange({ ...options.state, ...change, page: resetPage ? 1 : change.page ?? options.state.page })
  }
  const clear = (): void => update({ query: '', filters: {} }, true)
  const noMatch = normalizedQuery || activeFilters
    ? options.searchEmptyText || 'No resources match your search and selected filters.'
    : options.emptyText || 'No resources found.'

  return html`<div class="k-table k-table--resource k-table--queryable">
    <div class="k-table__controls" role="search" aria-label="Filter ${options.ariaLabel.toLocaleLowerCase()}">
      <label class="k-table__search">
        <span class="sr-only">Search ${options.ariaLabel}</span>
        ${icon('search', 'k-table__search-icon')}
        <input
          class="k-table__search-input"
          type="search"
          .value=${options.state.query}
          aria-label="Search ${options.ariaLabel}"
          placeholder=${options.searchPlaceholder}
          autocomplete="off"
          @input=${(event: Event) => update({ query: (event.target as HTMLInputElement).value }, true)}
        />
        ${options.state.query
          ? html`<button class="k-table__search-clear" type="button" aria-label="Clear search" @click=${() => update({ query: '' }, true)}>${icon('x')}</button>`
          : nothing}
      </label>
      ${filters.map((filter) => {
        const values = filterOptions[filter.key]
        const selected = normalizedFilters[filter.key]
        const selectorOptions: ResourceTableFilterOption[] = values.map((value) => ({
          value,
          label: filter.labelFor?.(value) || value,
        }))
        return html`<faros-resource-table-filter
          .label=${filter.label}
          .allLabel=${filter.allLabel || 'All'}
          .value=${selected}
          .options=${selectorOptions}
          @change=${(event: CustomEvent<string>) => update({ filters: { ...normalizedFilters, [filter.key]: event.detail } }, true)}
        ></faros-resource-table-filter>`
      })}
      ${normalizedQuery || activeFilters
        ? html`<button class="k-table__clear-filters" type="button" @click=${clear}>Clear ${normalizedQuery && activeFilters ? 'search and filters' : normalizedQuery ? 'search' : 'filters'}</button>`
        : nothing}
    </div>

    <div class="k-table__scroll" role="region" aria-label="${options.ariaLabel} scroll area" tabindex="0">
      <table class="k-table__table" aria-label=${options.ariaLabel}>
        <thead><tr class="k-table__head-row">
          ${options.columns.map((column) => html`<th
            class="k-table__heading k-table__heading--${column.align || 'start'} ${column === primaryColumn ? 'k-table__heading--primary' : ''}"
          >${column.label}</th>`)}
        </tr></thead>
        <tbody>
          ${repeat(
            visibleRows,
            options.rowKey,
            (row) => html`<tr class="stagger-item k-table__row">
              ${options.columns.map((column) => html`<td
                class="k-table__cell k-table__cell--${column.align || 'start'} ${column === primaryColumn ? 'k-table__cell--primary' : ''}"
              >
                ${column === primaryColumn
                  ? html`<div class="k-table__primary">
                      <div class="k-table__primary-content"><span class="k-table__primary-value">${column.render(row)}</span></div>
                      ${options.actions ? html`<div class="k-table__primary-actions">${options.actions(row)}</div>` : nothing}
                    </div>`
                  : column.render(row)}
              </td>`)}
            </tr>`,
          )}
          ${visibleRows.length === 0
            ? html`<tr><td colspan=${Math.max(1, options.columns.length)} class="k-table__empty-cell">
                ${icon('inbox', 'k-table__empty-icon')}
                <p class="k-table__empty-label">${noMatch}</p>
              </td></tr>`
            : nothing}
        </tbody>
      </table>
    </div>

    <footer class="k-table__pagination" aria-label="Table pagination">
      <div class="k-table__range" aria-live="polite">
        ${filtered.length
          ? html`Showing <strong>${rangeStart}–${rangeEnd}</strong> of <strong>${filtered.length}</strong>`
          : html`Showing <strong>0</strong> results`}
      </div>
      <label class="k-table__page-size">Rows per page
        <select
          class="k-table__page-size-select"
          aria-label="Rows per page"
          .value=${String(pageSize)}
          @change=${(event: Event) => update({ pageSize: Number((event.target as HTMLSelectElement).value) }, true)}
        >${pageSizes.map((size) => html`<option value=${size}>${size}</option>`)}</select>
      </label>
      <div class="k-table__page-actions">
        <button class="k-table__page-button" type="button" aria-label="Previous page" ?disabled=${page <= 1} @click=${() => update({ page: page - 1 })}>${icon('arrow-left')}</button>
        <span class="k-table__page-indicator" aria-live="polite">${page} / ${pageCount}</span>
        <button class="k-table__page-button" type="button" aria-label="Next page" ?disabled=${page >= pageCount} @click=${() => update({ page: page + 1 })}>${icon('chevron-right')}</button>
      </div>
    </footer>
  </div>`
}
