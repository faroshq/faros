import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

const vite = await createServer({ appType: 'custom', cacheDir: '/tmp/faros-vite-first-time-setup', configFile: false, plugins: [vue()], server: { middlewareMode: true, hmr: false } })
const { default: FirstTimeSetup } = await vite.ssrLoadModule('/src/FirstTimeSetup.vue')
test.after(async () => vite.close())

const base = {
  readiness: { gitConnection: { ready: false, status: 'connection-missing' } },
  llmConfigured: false,
  llmModel: '',
  loading: false,
  gitError: '',
  llmError: '',
  completion: false,
  codeConnectionsUrl: '/ui/providers/code/connections',
  codeCatalogUrl: '/providers',
}
const render = (props = {}) => renderToString(createSSRApp(FirstTimeSetup, { ...base, ...props }))

test('keeps first-time setup separate from the project prompt', async () => {
  const html = await render()
  assert.match(html, /aria-label="App Studio workspace setup"/)
  assert.doesNotMatch(html, /aria-labelledby="app-studio-setup-title"/)
  assert.match(html, /Connect an AI model/)
  assert.match(html, /Git \(recommended\)/)
  assert.doesNotMatch(html, /Skip for now/)
  assert.doesNotMatch(html, /What are we building|Describe what you want to build|<textarea/)
})

test('requires the model even when Git is ready', async () => {
  const html = await render({ readiness: { gitConnection: { ready: true, status: 'ready', connectionRef: 'github-workspace' } } })
  assert.match(html, /GitHub connected/)
  assert.match(html, /Connect an AI model/)
  assert.match(html, /tests the provider connection before saving/)
})

test('surfaces terminal Git validation failures with a recovery action', async () => {
  const html = await render({
    llmConfigured: true,
    readiness: {
      gitConnection: {
        ready: false,
        status: 'failed',
        connectionRef: 'github-workspace',
        message: 'The git host rejected the credential.',
      },
    },
  })
  assert.match(html, /The git host rejected the credential\./)
  assert.match(html, /Fix Git connection/)
  assert.match(html, /Check failed/)
})

test('completion hands off to normal project creation', async () => {
  const html = await render({
    readiness: { gitConnection: { ready: true, status: 'ready', connectionRef: 'github-workspace' } },
    llmConfigured: true,
    llmModel: 'gpt-5.4',
    completion: true,
  })
  assert.match(html, /App Studio is ready/)
  assert.match(html, /Create your first project/)
  assert.match(html, /gpt-5\.4/)
})

test('App gates the new-project composer behind setup', async () => {
  const app = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  assert.match(app, /<template v-if="firstTimeSetupVisible">[\s\S]*<FirstTimeSetup[\s\S]*<template v-else-if="wizardOpen">/)
  assert.match(app, /@connect-model="openSettings"/)
  assert.match(app, /@finish="finishFirstTimeSetup"/)
})

test('optional Git step remains skippable while Git is missing, checking, or failed', async () => {
  for (const status of ['provider-missing', 'connection-missing', 'validating', 'failed']) {
    const html = await render({ llmConfigured: true, readiness: { gitConnection: { ready: false, status } }, gitLoading: status === 'validating' })
    assert.match(html, /Skip for now/)
    assert.match(html, /Connect Git \(recommended\)/)
    assert.doesNotMatch(html, /GitHub connected/)
  }
})
test('completion without Git does not claim Git was connected', async () => {
  const html = await render({ llmConfigured: true, completion: true })
  assert.match(html, /App Studio is ready/)
  assert.doesNotMatch(html, /Git and an AI model are connected|GitHub<\/dt>/)
})

test('Git skip storage is scoped to a signed-in user and workspace', async () => {
  const { gitOnboardingStorageKey } = await vite.ssrLoadModule('/src/useGitOnboarding.ts')
  const context = { orgUUID: 'org-a', workspaceUUID: 'ws-a', user: { sub: 'alice' } }
  const key = gitOnboardingStorageKey(context)
  assert.ok(key)
  assert.notEqual(key, gitOnboardingStorageKey({ ...context, workspaceUUID: 'ws-b' }))
  assert.notEqual(key, gitOnboardingStorageKey({ ...context, user: { sub: 'bob' } }))
  assert.equal(gitOnboardingStorageKey({ ...context, user: null }), null)
})
