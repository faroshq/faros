import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { createServer } from 'vite'

const vite = await createServer({ appType: 'custom', cacheDir: '/tmp/faros-vite-project-dependencies', server: { middlewareMode: true, hmr: false } })
const { api } = await vite.ssrLoadModule('/src/api.ts')
test.after(async () => vite.close())

const context = { token: 'token-1', basePath: '/ui/providers/app-studio' }

function installRequestMock(responses, calls) {
  const previousFetch = globalThis.fetch
  const previousStorage = globalThis.localStorage
  const storage = new Map([['faros:portal:tenant', JSON.stringify({ orgUUID: 'org-1', workspaceUUID: 'workspace-1' })]])
  globalThis.localStorage = { getItem(key) { return storage.get(key) ?? null } }
  globalThis.fetch = async (path, init = {}) => {
    calls.push({ path: String(path), init })
    const response = responses.shift()
    return { ok: true, status: response === null ? 204 : 200, async text() { return response === null ? '' : JSON.stringify(response) } }
  }
  return () => {
    globalThis.fetch = previousFetch
    if (previousStorage === undefined) delete globalThis.localStorage
    else globalThis.localStorage = previousStorage
  }
}

test('dependency API keeps the catalog, mutation, and retained delete wire shapes explicit', async () => {
  const dependency = { name: 'database', environment: 'development', template: 'database', sourceRef: { kind: 'binding', name: 'dep-database' }, targetRef: { kind: 'developmentService', name: 'web' }, sourceInterface: 'default', targetInterface: 'postgresql', deletionPolicy: 'Retain' }
  const catalog = { templates: [{ name: 'database', schema: { type: 'object' }, provides: [{ name: 'default', type: 'postgresql', keys: ['uri'] }] }], targetInterfaces: [{ name: 'postgresql', type: 'postgresql', mappings: [{ sourceKey: 'uri', targetKey: 'DATABASE_URL' }] }] }
  const calls = []
  const restore = installRequestMock([catalog, { items: [dependency] }, { dependency, items: [dependency] }, null], calls)
  try {
    assert.deepEqual(await api.getProjectDependencyCatalog(context, 'shop'), catalog)
    assert.deepEqual(await api.listProjectDependencies(context, 'shop'), [dependency])
    const mutation = { environment: 'development', template: 'database', values: { version: '16' }, sourceInterface: 'default', targetRef: { kind: 'developmentService', name: 'web' }, targetInterface: 'postgresql' }
    assert.deepEqual(await api.upsertProjectDependency(context, 'shop', 'database', mutation), { dependency, items: [dependency] })
    await api.deleteProjectDependency(context, 'shop', 'database')
    assert.match(calls[0].path, /\/shop\/dependencies\/catalog$/)
    assert.match(calls[1].path, /\/shop\/dependencies$/)
    assert.equal(calls[2].init.method, 'PUT')
    assert.deepEqual(JSON.parse(calls[2].init.body), mutation)
    assert.match(calls[3].path, /\/shop\/dependencies\/database\?environment=development$/)
    assert.equal(calls[3].init.method, 'DELETE')
  } finally {
    restore()
  }
})

test('dependency UI uses schema-driven settings, shows Retain, confirms removal, and has no raw connectionRefs input', async () => {
  const [panel, form, serviceForm, servicesPanel, composition] = await Promise.all([
    readFile(new URL('./ProjectDependenciesPanel.vue', import.meta.url), 'utf8'),
    readFile(new URL('./ProjectDependencyForm.vue', import.meta.url), 'utf8'),
    readFile(new URL('./DevelopmentServiceForm.vue', import.meta.url), 'utf8'),
    readFile(new URL('./DevelopmentServicesPanel.vue', import.meta.url), 'utf8'),
    readFile(new URL('./ProjectCompositionPanel.vue', import.meta.url), 'utf8'),
  ])
  assert.match(form, /<ProductionForm/)
  assert.match(form, /compatibleTemplates/)
  assert.match(form, /stateful Instance is retained/i)
  assert.match(panel, /confirmDialog/)
  assert.match(panel, /Credential delivery will be removed immediately/)
  assert.match(panel, />Retain</)
  assert.match(composition, /<ProjectDependenciesPanel/)
  assert.doesNotMatch(serviceForm, /connectionRefs(Text)?/)
  assert.doesNotMatch(servicesPanel, /connectionRefs:/)
})
