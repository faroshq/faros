import assert from 'node:assert/strict'
import test from 'node:test'
import { createServer } from 'vite'

const vite = await createServer({
  appType: 'custom',
  cacheDir: '/tmp/faros-vite-project-composition',
  server: { middlewareMode: true, hmr: false },
})
const { api } = await vite.ssrLoadModule('/src/api.ts')
test.after(async () => vite.close())

const context = {
  token: 'token-1',
  basePath: '/ui/providers/app-studio',
}

function installRequestMock(responses, calls) {
  const previousFetch = globalThis.fetch
  const previousStorage = globalThis.localStorage
  const storage = new Map([['faros:portal:tenant', JSON.stringify({ orgUUID: 'org-1', workspaceUUID: 'workspace-1' })]])
  globalThis.localStorage = { getItem(key) { return storage.get(key) ?? null } }
  globalThis.fetch = async (path, init = {}) => {
    calls.push({ path: String(path), init })
    const response = responses.shift()
    return {
      ok: true,
      status: response === null ? 204 : 200,
      async text() { return response === null ? '' : JSON.stringify(response) },
    }
  }
  return () => {
    globalThis.fetch = previousFetch
    if (previousStorage === undefined) delete globalThis.localStorage
    else globalThis.localStorage = previousStorage
  }
}

test('normalizes the component mutation response and sends an explicit build-workflow clear', async () => {
  const component = { name: 'web', kind: 'Service', sourcePath: 'apps/web' }
  const calls = []
  const restore = installRequestMock([
    { component, project: { items: [component] } },
    {},
  ], calls)
  try {
    const saved = await api.upsertProjectComponent(context, 'shop', 'web', {
      kind: 'Service',
      sourcePath: 'apps/web',
    })
    assert.deepEqual(saved, { component, components: [component] })

    await api.setProjectBuildConfiguration(context, 'shop', null)
    assert.equal(calls[0].init.method, 'PUT')
    assert.match(calls[0].path, /\/shop\/components\/web$/)
    assert.deepEqual(JSON.parse(calls[1].init.body), { clear: true })
    assert.match(calls[1].path, /\/shop\/build$/)
  } finally {
    restore()
  }
})
