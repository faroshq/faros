import { serverCursorChange } from './databricksPagination.js'

function equal(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`)
  }
}

equal(
  serverCursorChange({ reason: 'page', page: 3, cursor: 'opaque-page-3' }),
  { page: 3, cursor: 'opaque-page-3' },
  'server page events preserve page and cursor',
)
equal(
  serverCursorChange({ reason: 'page-size', page: 1, cursor: null }),
  { page: 1, cursor: null },
  'page-size events preserve the table reset',
)
equal(
  serverCursorChange({ reason: 'query', page: 4, cursor: 'stale-query-cursor' }),
  { page: 1, cursor: null },
  'clearing a query resets server pagination',
)
equal(
  serverCursorChange({ reason: 'filter', page: 2, cursor: 'stale-filter-cursor' }),
  { page: 1, cursor: null },
  'clearing a filter resets server pagination',
)
