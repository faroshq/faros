import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const frame = await readFile(new URL('./ProviderFrame.vue', import.meta.url), 'utf8')

test('provider navigation preserves absolute portal destinations', () => {
  assert.match(frame, /p\.startsWith\('\/'\) \? p : `\/providers\/\$\{entry\.value\.name\}\/\$\{p\}`/)
})
