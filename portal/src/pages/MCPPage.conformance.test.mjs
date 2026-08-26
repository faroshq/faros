import assert from 'node:assert/strict'
import fs from 'node:fs'
import test from 'node:test'

const page = fs.readFileSync(new URL('./MCPPage.vue', import.meta.url), 'utf8')
const router = fs.readFileSync(new URL('../router/index.ts', import.meta.url), 'utf8')

test('MCP detail keeps the shared resource composition and deep-link contract', () => {
  assert.match(router, /path: '\/mcp\/:name'/)
  assert.match(router, /name: 'mcp-detail'/)
  assert.match(page, /<a[\s\S]*href="\/ui\/mcp"/)
  assert.match(page, /<ResourcePage/)
  assert.match(page, /<ResourceStatCards[\s\S]*density="compact"/)
  assert.match(page, /<ResourceSectionCard/)
  assert.match(page, /<StatusBadge/)
  assert.match(page, /<ResourceTableDeleteButton/)
})

test('MCP connect snippets stay masked and selectors expose state', () => {
  const template = page.slice(page.indexOf('<template>'))
  assert.match(page, /const TOKEN_PLACEHOLDER = '<token>'/)
  assert.match(page, /snippet\(c, selectedClient\.value, TOKEN_PLACEHOLDER\)/)
  assert.match(page, /client === 'claude-desktop'/)
  assert.match(page, /client === 'codex'/)
  assert.match(page, /id: 'claude-code'/)
  assert.match(page, /id: 'claude-desktop'/)
  assert.match(page, /id: 'codex'/)
  assert.match(page, /return `claude mcp add/)
  assert.match(page, /return JSON\.stringify\(/)
  assert.match(page, /--bearer-token-env-var/)
  assert.doesNotMatch(template, /selectedConnect\.token(?!Ready)/)
  assert.match(page, /role="tablist" aria-label="AI client setup"/)
  assert.match(page, /:aria-selected="selectedClient === c\.id"/)
  assert.match(page, /:aria-expanded="isProviderOpen\(p\.name\)"/)
  assert.match(page, /:aria-controls="providerPanelID\(p\.name, providerIndex\)"/)
  assert.doesNotMatch(page, /v-html=/)
})

test('MCP reads preserve snapshots and expose recoverable failures', () => {
  assert.match(page, /const selectedResourceMissing = computed/)
  assert.match(page, /was not found in this workspace/)
  assert.match(page, /:stale="loaded && !!error"/)
  assert.match(page, /connectError/)
  assert.match(page, /@click="loadConnect\(selectedServer\.name\)"/)
  assert.match(page, /Deleting this MCP server\. The last successful snapshot remains visible/)
  assert.match(page, /servers\.value = servers\.value\.filter\(\(server\) => server\.name !== name\)/)
  assert.match(page, /if \(selected\.value === name\) void router\.replace\(\{ name: 'mcp' \}\)/)
  assert.match(page, /void load\(\)/)
  assert.match(page, /connect\.value = \{\}/)
})

test('MCP delete failures remain visible from both list and detail views', () => {
  const listStart = page.indexOf('<template v-if="!selected">')
  const detailStart = page.indexOf('<template v-else>', listStart)
  assert.notEqual(listStart, -1)
  assert.notEqual(detailStart, -1)

  const listView = page.slice(listStart, detailStart)
  const detailView = page.slice(detailStart)
  assert.match(listView, /mutationError/)
  assert.match(listView, /role="alert"/)
  assert.match(detailView, /mutationError/)
  assert.match(detailView, /role="alert"/)
})

test('MCP delete failures are fenced to the active server route', () => {
  const removeStart = page.indexOf('async function remove(name: string)')
  const removeEnd = page.indexOf('watch(selected,', removeStart)
  const remove = page.slice(removeStart, removeEnd)
  const catchStart = remove.indexOf('} catch')
  assert.notEqual(catchStart, -1)
  assert.match(remove.slice(catchStart), /selected\.value === name/)
})
