import {
  DEFAULT_LAYOUT_MODE,
  isLayoutMode,
  nextLayoutMenuIndex,
  readLayoutPreference,
  writeLayoutPreference,
  type LayoutPreferenceStorage,
} from './layoutPreference.js'

function assert(condition: unknown, label: string): asserts condition {
  if (!condition) throw new Error(label)
}

assert(DEFAULT_LAYOUT_MODE === 'grid', 'grid is the default layout')
assert(isLayoutMode('grid'), 'grid is valid')
assert(isLayoutMode('list'), 'list is valid')
assert(!isLayoutMode('table'), 'unknown values are invalid')

const values = new Map<string, string>()
const storage: LayoutPreferenceStorage = {
  getItem: key => values.get(key) ?? null,
  setItem: (key, value) => { values.set(key, value) },
}

assert(readLayoutPreference('layout', storage) === 'grid', 'missing preferences use grid')
values.set('layout', 'table')
assert(readLayoutPreference('layout', storage) === 'grid', 'invalid preferences use grid')
writeLayoutPreference('layout', 'list', storage)
assert(readLayoutPreference('layout', storage) === 'list', 'list persists without normalization')

const deniedStorage: LayoutPreferenceStorage = {
  getItem: () => { throw new Error('denied') },
  setItem: () => { throw new Error('denied') },
}
assert(readLayoutPreference('layout', deniedStorage) === 'grid', 'read failures use grid')
writeLayoutPreference('layout', 'list', deniedStorage)
assert(readLayoutPreference('layout', null) === 'grid', 'unavailable storage uses grid')

assert(nextLayoutMenuIndex('ArrowDown', 0) === 1, 'ArrowDown advances')
assert(nextLayoutMenuIndex('ArrowDown', 1) === 0, 'ArrowDown wraps')
assert(nextLayoutMenuIndex('ArrowUp', 0) === 1, 'ArrowUp wraps')
assert(nextLayoutMenuIndex('ArrowUp', 1) === 0, 'ArrowUp retreats')
assert(nextLayoutMenuIndex('Home', 1) === 0, 'Home selects the first item')
assert(nextLayoutMenuIndex('End', 0) === 1, 'End selects the last item')
assert(nextLayoutMenuIndex('PageDown', 0) === null, 'unhandled keys do not move focus')
