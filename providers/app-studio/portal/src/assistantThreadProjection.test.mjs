import assert from 'node:assert/strict'
import test from 'node:test'
import { createServer } from 'vite'

let vite
test.before(async () => {
  vite = await createServer({ appType: 'custom', server: { middlewareMode: true, hmr: false } })
})
test.after(async () => vite?.close())

test('projects terminal worked duration from canonical agent message data', async () => {
  const { assistantThreadItemsToMessages } = await vite.ssrLoadModule('/src/assistantThreadProjection.ts')
  const { parseAssistantProgress } = await vite.ssrLoadModule('/src/assistantProgress.ts')
  const messages = assistantThreadItemsToMessages([{
    id: 'assistant-1',
    turnID: 'run-1',
    type: 'agentMessage',
    status: 'completed',
    content: 'Done',
    data: {
      assistantProgress: {
        version: 1,
        messages: [],
        messageSequences: [],
        workedDurationMs: 83_400,
      },
    },
    sequence: 4,
    createdAt: '2026-08-02T17:42:09Z',
  }], 'demo')

  assert.equal(messages.length, 1)
  assert.equal(messages[0].metadata.assistantStatus, 'completed')
  assert.equal(parseAssistantProgress(messages[0].metadata.assistantProgress)?.workedDurationMs, 83_400)
})

test('keeps action items alongside agent progress in the thread projection', async () => {
  const { assistantThreadItemsToMessages } = await vite.ssrLoadModule('/src/assistantThreadProjection.ts')
  const messages = assistantThreadItemsToMessages([{
    id: 'assistant-1', turnID: 'run-1', type: 'agentMessage', status: 'completed', content: 'Done',
    data: { assistantProgress: { version: 1, messages: [], messageSequences: [], workedDurationMs: 2_400 } },
    sequence: 1, createdAt: '2026-08-02T17:42:09Z',
  }, {
    id: 'read-1', turnID: 'run-1', type: 'dynamicToolCall', status: 'completed', content: 'Read file',
    data: { id: 'read-1', kind: 'inspect', status: 'succeeded', title: 'Read file', severity: 'normal', sequence: 1 },
    sequence: 2, createdAt: '2026-08-02T17:42:10Z',
  }], 'demo')

  assert.equal(messages[0].metadata.assistantProgress.workedDurationMs, 2_400)
  assert.equal(messages[0].metadata.assistantActionFeed.length, 1)
})
