import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import ts from 'typescript'

const source = await readFile(new URL('./assistantThreadReadState.ts', import.meta.url), 'utf8')
const { outputText } = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ES2022,
    target: ts.ScriptTarget.ES2022,
  },
})
const moduleURL = `data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`
const {
  assistantThreadReadStateStorageKey,
  markAssistantThreadRead,
  markAssistantThreadUnread,
  reconcileAssistantThreadReadState,
} = await import(moduleURL)

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial))
  return {
    values,
    getItem(key) { return values.has(key) ? values.get(key) : null },
    setItem(key, value) { values.set(key, value) },
  }
}

const thread = (id, updatedAt) => ({ id, updatedAt })

test('establishes a quiet baseline and keeps the selected thread read', () => {
  const storage = memoryStorage()
  const scope = 'project-scope-a'
  const initial = [thread('active', '2026-08-26T10:00:00Z'), thread('other', '2026-08-26T09:00:00Z')]

  assert.deepEqual(reconcileAssistantThreadReadState(scope, initial, 'active', storage), [])
  assert.deepEqual(reconcileAssistantThreadReadState(scope, [
    thread('active', '2026-08-26T11:00:00Z'),
    thread('other', '2026-08-26T09:00:00Z'),
  ], 'active', storage), [])
})

test('marks only changed non-active threads unread until they are viewed', () => {
  const storage = memoryStorage()
  const scope = 'project-scope-a'
  const initial = [thread('active', '2026-08-26T10:00:00Z'), thread('other', '2026-08-26T09:00:00Z')]
  reconcileAssistantThreadReadState(scope, initial, 'active', storage)

  const changed = [thread('active', '2026-08-26T10:00:00Z'), thread('other', '2026-08-26T12:00:00Z')]
  assert.deepEqual(reconcileAssistantThreadReadState(scope, changed, 'active', storage), ['other'])
  assert.deepEqual(reconcileAssistantThreadReadState(scope, changed, 'other', storage), [])
})

test('manual read markers and project scopes stay independent', () => {
  const storage = memoryStorage()
  const initial = [thread('one', '2026-08-26T10:00:00Z'), thread('two', '2026-08-26T09:00:00Z')]
  reconcileAssistantThreadReadState('scope-a', initial, 'one', storage)
  reconcileAssistantThreadReadState('scope-b', initial, 'one', storage)

  const renamed = thread('two', '2026-08-26T12:00:00Z')
  markAssistantThreadRead('scope-a', renamed, storage)
  assert.deepEqual(reconcileAssistantThreadReadState('scope-a', [initial[0], renamed], 'one', storage), [])
  assert.deepEqual(reconcileAssistantThreadReadState('scope-b', [initial[0], renamed], 'one', storage), ['two'])
  assert.notEqual(assistantThreadReadStateStorageKey('scope-a'), assistantThreadReadStateStorageKey('scope-b'))
})

test('manual unread markers persist until explicitly read or selected', () => {
  const storage = memoryStorage()
  const threads = [thread('one', '2026-08-26T10:00:00Z'), thread('two', '2026-08-26T09:00:00Z')]
  reconcileAssistantThreadReadState('scope', threads, 'one', storage)

  markAssistantThreadUnread('scope', threads[1], storage)
  assert.deepEqual(reconcileAssistantThreadReadState('scope', threads, 'one', storage), ['two'])
  markAssistantThreadRead('scope', threads[1], storage)
  assert.deepEqual(reconcileAssistantThreadReadState('scope', threads, 'one', storage), [])

  markAssistantThreadUnread('scope', threads[1], storage)
  assert.deepEqual(reconcileAssistantThreadReadState('scope', threads, 'two', storage), [])
})

test('storage failures do not block thread navigation', () => {
  const broken = {
    getItem() { throw new Error('blocked') },
    setItem() { throw new Error('full') },
  }
  assert.doesNotThrow(() => reconcileAssistantThreadReadState('scope', [thread('one', 'invalid')], 'one', broken))
  assert.doesNotThrow(() => markAssistantThreadRead('scope', thread('one', 'invalid'), broken))
  assert.doesNotThrow(() => markAssistantThreadUnread('scope', thread('one', 'invalid'), broken))
})
