import { describe, expect, it } from 'vitest'

import { hasActiveTableFilters, sameTableRequest, tablePageInfo, type TableRequestState } from './pagination'

describe('edges table pagination state', () => {
  it('treats whitespace as inactive and any selected filter as active', () => {
    expect(hasActiveTableFilters('  ', { edgeName: '', status: '' })).toBe(false)
    expect(hasActiveTableFilters(' nginx ', { edgeName: '', status: '' })).toBe(true)
    expect(hasActiveTableFilters('', { edgeName: 'edge-a', status: '' })).toBe(true)
  })

  it('normalizes empty continuation metadata without claiming a total', () => {
    expect(tablePageInfo()).toEqual({ hasNext: false, nextCursor: null })
    expect(tablePageInfo('')).toEqual({ hasNext: false, nextCursor: null })
    expect(tablePageInfo('opaque-next')).toEqual({ hasNext: true, nextCursor: 'opaque-next' })
  })

  it('rejects stale reads when any request dimension changed', () => {
    const request: TableRequestState = {
      mode: 'server', active: false, page: 1, pageSize: 10,
      query: '', filters: { edgeName: '', status: '' }, cursor: null,
    }
    expect(sameTableRequest(request, { ...request, filters: { ...request.filters } })).toBe(true)
    expect(sameTableRequest(request, { ...request, cursor: 'next' })).toBe(false)
    expect(sameTableRequest(request, { ...request, filters: { edgeName: 'edge-a', status: '' } })).toBe(false)
    expect(sameTableRequest(request, { ...request, mode: 'client', active: true })).toBe(false)
  })
})
