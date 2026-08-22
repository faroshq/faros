import type { TableFilterDefinition } from './portalkit/table'

export const PACKAGE_PAGE_SIZE = 10

// GitHub's package API supports these six ecosystems. Keep the options
// explicit because server pagination only supplies one page to ResourceTable;
// deriving options from that page would make filters silently incomplete.
export const PACKAGE_FILTERS: TableFilterDefinition[] = [
  {
    key: 'type',
    label: 'Type',
    options: [
      { value: 'container', label: 'Container' },
      { value: 'docker', label: 'Docker' },
      { value: 'npm', label: 'npm' },
      { value: 'maven', label: 'Maven' },
      { value: 'rubygems', label: 'RubyGems' },
      { value: 'nuget', label: 'NuGet' },
    ],
  },
  {
    key: 'visibility',
    label: 'Visibility',
    options: [
      { value: 'public', label: 'Public' },
      { value: 'internal', label: 'Internal' },
      { value: 'private', label: 'Private' },
      // GitHub may omit visibility for a package while it is being crawled.
      { value: 'unknown', label: 'Unknown' },
    ],
  },
  {
    key: 'status',
    label: 'Status',
    allLabel: 'Any status',
    options: [
      { value: 'ready', label: 'Ready' },
      { value: 'pending', label: 'Pending' },
      { value: 'failed', label: 'Failed' },
      { value: 'Deleting', label: 'Deleting' },
    ],
  },
]

export interface PackageFilterValues {
  type: string
  visibility: string
  status: string
}

export const EMPTY_PACKAGE_FILTERS: PackageFilterValues = {
  type: '',
  visibility: '',
  status: '',
}

export type PackagePaginationMode = 'server' | 'client'

export interface PackagePageInfo {
  hasNext: boolean
  nextCursor: string | null
}

export function clonePackageFilters(filters: PackageFilterValues): PackageFilterValues {
  return { type: filters.type, visibility: filters.visibility, status: filters.status }
}

export function hasActivePackageFilters(query: string, filters: PackageFilterValues): boolean {
  return !!query.trim() || Object.values(filters).some(Boolean)
}

export function packagePageInfo(nextCursor?: string): PackagePageInfo {
  const cursor = nextCursor || null
  return { hasNext: cursor !== null, nextCursor: cursor }
}

export function packageVisibility(value: string | undefined): string {
  return value || 'unknown'
}
