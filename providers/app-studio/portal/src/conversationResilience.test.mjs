import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import ts from 'typescript'

const source = await readFile(new URL('./conversationResilience.ts', import.meta.url), 'utf8')
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 } })
const state = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`)

const message = (id, content) => ({ id, projectID: 'p', role: 'assistant', content, createdAt: '2026-01-01T00:00:00Z' })
const snapshot = (revision, content, status = 'running') => ({ run: { id: 'run-1', status, revision, activeMessageID: 'a-1' }, message: message('a-1', content) })

test('mergeConversationSnapshot keeps the stable assistant message ID and rejects older or duplicate revisions', () => {
  const initial = { messages: [message('u-1', 'hello'), message('a-1', 'old')], runs: {} }
  const current = state.mergeConversationSnapshot(initial, snapshot(2, 'new'))
  const old = state.mergeConversationSnapshot(current, snapshot(1, 'stale'))
  const duplicate = state.mergeConversationSnapshot(current, snapshot(2, 'duplicate'))
  assert.deepEqual(current.messages.map(({ id, content }) => ({ id, content })), [{ id: 'u-1', content: 'hello' }, { id: 'a-1', content: 'new' }])
  assert.equal(current.messages.filter((item) => item.id === 'a-1').length, 1)
  assert.strictEqual(old, current)
  assert.strictEqual(duplicate, current)
})

test('replaceOptimisticUserMessage replaces a local user message without duplicating it', () => {
  const optimistic = message('optimistic-client-1', 'ship it')
  const persisted = { ...optimistic, id: 'user-1' }
  const result = state.replaceOptimisticUserMessage([message('prior', 'earlier'), optimistic], optimistic.id, persisted)
  assert.deepEqual(result.map((item) => item.id), ['prior', 'user-1'])
})

test('conversation run controller reconnects from the accepted revision with capped exponential backoff', async () => {
  const calls = []
  const scheduled = []
  const delays = []
  const controller = new state.ConversationRunController({
    connect: async (_runID, afterRevision) => { calls.push(afterRevision); throw new Error('network') },
    abort: async () => {},
    setTimeout: (fn, delay) => { delays.push(delay); scheduled.push({ fn, delay }); return scheduled.length },
    clearTimeout: () => {},
  })
  controller.start('run-1', 3)
  for (let index = 0; index < 4; index++) {
    await Promise.resolve()
    const next = scheduled.shift()
    assert.ok(next, `expected retry ${index + 1}`)
    next.fn()
  }
  await Promise.resolve()
  assert.deepEqual(calls, [3, 3, 3, 3, 3])
  assert.deepEqual(delays, [1_000, 2_000, 4_000, 8_000, 10_000])
  controller.disconnect()
})

test('stop aborts the backend run before disconnecting the active subscription', async () => {
  const events = []
  const controller = new state.ConversationRunController({
    connect: async () => { events.push('connect') },
    abort: async () => { events.push('abort') },
    setTimeout: () => 0,
    clearTimeout: () => {},
  })
  controller.start('run-1', 0)
  await Promise.resolve()
  controller.setDisconnect(() => events.push('disconnect'))
  await controller.stop()
  assert.deepEqual(events, ['connect', 'abort', 'disconnect'])
})
