import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const page = await readFile(new URL('./ProvidersPage.vue', import.meta.url), 'utf8')
const dialog = await readFile(new URL('../components/ProviderEnableDialog.vue', import.meta.url), 'utf8')

test('enabled providers expose update access while preserving disable', () => {
  assert.match(page, /async function openUpdateDialog\(p: ProviderDTO, preselectClaims: string\[\] = \[\]\)/)
  assert.match(page, /dialogMode\.value = 'update'/)
  assert.match(
    page,
    /<template v-else>[\s\S]*@click="openUpdateDialog\(p\)"[\s\S]*Update access[\s\S]*@click="onDisable\(p\)"[\s\S]*Disable[\s\S]*<\/template>/,
  )
  assert.match(page, /:mode="dialogMode"/)
})

test('update access loads current state and uses the additive authorization action', () => {
  assert.match(page, /const access = await providers\.loadAccess\(p\)/)
  assert.match(page, /await providers\.authorize\(p, accept\)/)
  assert.match(page, /:access="dialogAccess"/)
})

test('update mode preselects and locks existing grants without revoking by omission', () => {
  assert.match(dialog, /!!current\?\.accepted \|\| \(requested\.has\(claimKey\(c\)\) && !!current\?\.offered\)/)
  assert.match(dialog, /alreadyAccepted\(c\) \|\| isUnavailable\(c\)/)
  assert.match(dialog, /mode === 'update'/)
  assert.match(dialog, /Access updates are additive\. Existing grants remain authorized/)
  assert.doesNotMatch(dialog, /replaces the current grant set/)
})

test('provider access deep links preselect all requested claims and honor an internal return path', () => {
  assert.match(page, /route\.query\.configure/)
  assert.match(page, /dialogPreselectClaims\.value = requested/)
  assert.match(page, /Array\.isArray\(claim\)/)
  assert.match(page, /returnPath\.startsWith\('\/'\) && !returnPath\.startsWith\('\/\/'\)/)
  assert.match(dialog, /`\$\{c\.group \?\? ''\}\/\$\{c\.resource\}`/)
})

test('provider access deep links explain claims the provider does not offer', () => {
  assert.match(dialog, /const unavailableRequestedClaims = computed/)
  assert.match(dialog, /Requested access is not offered/)
  assert.match(dialog, /add these optional claims to the Deployments CatalogEntry and APIExport/)
})
