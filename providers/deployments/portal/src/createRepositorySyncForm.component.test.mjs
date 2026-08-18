import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const componentSource = await readFile(new URL('./views/RepositorySyncCreateForm.vue', import.meta.url), 'utf8')
const listSource = await readFile(new URL('./views/DeploymentsListView.vue', import.meta.url), 'utf8')
const styleSource = await readFile(new URL('./style.css', import.meta.url), 'utf8')

test('create form exposes the prototype fields, defaults, and consequences', () => {
  assert.match(componentSource, /aria-labelledby="create-sync-title"/)
  assert.match(componentSource, /:aria-busy="submitting"/)
  for (const label of ['Name', 'Repository', 'Git ref', 'Target path', 'Sync interval']) {
    assert.match(componentSource, new RegExp(`<span class="field-label">${label}</span>`))
  }
  assert.match(componentSource, /path: '\.faros'/)
  assert.match(componentSource, /intervalSeconds: 30/)
  assert.match(componentSource, /prune: true/)
  assert.match(componentSource, /Delete owned objects when manifests are removed or when this sync is deleted/)
  assert.match(componentSource, /\{\{ submitting \? 'Creating…' : 'Create sync' \}\}/)
})

test('create form and list preserve the busy, error, and success-navigation contract', () => {
  assert.match(componentSource, /if \(submitting\.value \|\| !validate\(\)\)/)
  assert.match(componentSource, /role="alert"/)
  assert.match(componentSource, /emit\('created', created\.name\)/)
  assert.match(componentSource, /serverError\.value = error instanceof Error/)
  assert.match(listSource, /function created\(name: string\)[\s\S]*emit\('open', name\)/)
  assert.match(listSource, /aria-controls="create-repository-sync-panel"/)
  assert.match(listSource, /:aria-expanded="creating"/)
})

test('form styles are injected through the provider namespace', () => {
  assert.doesNotMatch(componentSource, /<style\b/)
  assert.match(styleSource, /faros-provider-deployments \.sync-form/)
  assert.match(styleSource, /faros-provider-deployments \.field-input:focus-visible/)
  assert.match(styleSource, /faros-provider-deployments \.error-summary/)
})
