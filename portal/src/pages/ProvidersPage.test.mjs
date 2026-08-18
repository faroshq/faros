import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const page = await readFile(new URL('./ProvidersPage.vue', import.meta.url), 'utf8')
const dialog = await readFile(new URL('../components/ProviderEnableDialog.vue', import.meta.url), 'utf8')

test('enabled providers expose update access while preserving disable', () => {
  assert.match(page, /function openUpdateDialog\(p: ProviderDTO\)/)
  assert.match(page, /dialogMode\.value = 'update'/)
  assert.match(
    page,
    /<template v-else>[\s\S]*@click="openUpdateDialog\(p\)"[\s\S]*Update access[\s\S]*@click="onDisable\(p\)"[\s\S]*Disable[\s\S]*<\/template>/,
  )
  assert.match(page, /:mode="dialogMode"/)
})

test('update access resubmits through the idempotent provider enable action', () => {
  assert.match(page, /await providers\.enable\(p, accept\)/)
  assert.match(page, /The enable endpoint is idempotent and reconciles an existing binding/)
})

test('update mode uses declared consent defaults without inferring applied claims', () => {
  assert.match(dialog, /next\[claimKey\(c\)\] = !!c\.tenantScoped && !c\.optional/)
  assert.match(dialog, /mode === 'update'/)
  assert.match(dialog, /This replaces the current grant set\. Re-select any optional access you want to retain\./)
  assert.match(page, /We do not infer which optional claims the existing binding/)
})
