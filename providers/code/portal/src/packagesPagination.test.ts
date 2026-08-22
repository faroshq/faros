import { describe, expect, it } from 'vitest'
import {
  EMPTY_PACKAGE_FILTERS,
  hasActivePackageFilters,
  PACKAGE_FILTERS,
  packagePageInfo,
  packageVisibility,
} from './packagesPagination'

describe('package table pagination state', () => {
  it('treats whitespace as inactive and any selected value as active', () => {
    expect(hasActivePackageFilters('  ', { ...EMPTY_PACKAGE_FILTERS })).toBe(false)
    expect(hasActivePackageFilters('  image  ', { ...EMPTY_PACKAGE_FILTERS })).toBe(true)
    expect(hasActivePackageFilters('', { ...EMPTY_PACKAGE_FILTERS, type: 'container' })).toBe(true)
  })

  it('normalizes page cursors without inventing a total', () => {
    expect(packagePageInfo()).toEqual({ hasNext: false, nextCursor: null })
    expect(packagePageInfo('next-page')).toEqual({ hasNext: true, nextCursor: 'next-page' })
    expect(packagePageInfo('')).toEqual({ hasNext: false, nextCursor: null })
  })

  it('declares the complete GitHub package filter vocabulary', () => {
    const byKey = Object.fromEntries(PACKAGE_FILTERS.map(filter => [filter.key, filter]))
    expect(byKey.type.options?.map(option => option.value)).toEqual([
      'container', 'docker', 'npm', 'maven', 'rubygems', 'nuget',
    ])
    expect(byKey.visibility.options?.map(option => option.value)).toEqual([
      'public', 'internal', 'private', 'unknown',
    ])
    expect(byKey.status.options?.map(option => option.value)).toEqual([
      'ready', 'pending', 'failed', 'Deleting',
    ])
  })

  it('keeps missing host visibility filterable as unknown while display can stay blank', () => {
    expect(packageVisibility(undefined)).toBe('unknown')
    expect(packageVisibility('private')).toBe('private')
  })
})
