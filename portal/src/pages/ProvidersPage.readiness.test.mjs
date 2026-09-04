import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./ProvidersPage.vue', import.meta.url), 'utf8')

test('provider cards distinguish unavailable providers from pending work', () => {
  assert.match(source, /!p\.ready \? 'Not ready'/)
  assert.match(source, /p\.readinessMessage \|\| 'Provider is unavailable\.'/)
  assert.doesNotMatch(source, /Provider is starting/)
})
