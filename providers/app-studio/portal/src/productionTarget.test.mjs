import assert from 'node:assert/strict'
import test from 'node:test'
import { createServer } from 'vite'

const vite = await createServer({
  appType: 'custom',
  cacheDir: '/tmp/faros-vite-production-target',
  configFile: false,
  server: { middlewareMode: true },
})
const { productionTargetMappingsComplete, updateProductionTargetMapping } = await vite.ssrLoadModule('/src/productionTarget.ts')
test.after(async () => vite.close())

const targets = [
  { name: 'api-runtime', imageInput: 'apiImage' },
  { name: 'web-runtime', imageInput: 'webImage' },
]
const components = [
  { name: 'api', kind: 'Service', sourcePath: 'api' },
  { name: 'web', kind: 'Service', sourcePath: 'web' },
]

test('requires an explicit distinct source for every target component', () => {
  assert.equal(productionTargetMappingsComplete(targets, components, []), false)
  assert.equal(productionTargetMappingsComplete(targets, components, [
    { targetComponent: 'api-runtime', componentRef: 'api' },
  ]), false)
  assert.equal(productionTargetMappingsComplete(targets, components, [
    { targetComponent: 'api-runtime', componentRef: 'api' },
    { targetComponent: 'web-runtime', componentRef: 'api' },
  ]), false)
  assert.equal(productionTargetMappingsComplete(targets, components, [
    { targetComponent: 'api-runtime', componentRef: 'api' },
    { targetComponent: 'web-runtime', componentRef: 'web' },
  ]), true)
})

test('selecting a source moves it instead of silently mapping it twice', () => {
  const next = updateProductionTargetMapping([
    { targetComponent: 'api-runtime', componentRef: 'api' },
    { targetComponent: 'web-runtime', componentRef: 'web' },
  ], 'web-runtime', 'api')
  assert.deepEqual(next, [{ targetComponent: 'web-runtime', componentRef: 'api' }])
  assert.deepEqual(updateProductionTargetMapping(next, 'web-runtime', ''), [])
})
