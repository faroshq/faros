import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import ts from 'typescript'

const source = await readFile(new URL('./assistantPlan.ts', import.meta.url), 'utf8')
const { outputText } = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ES2022,
    target: ts.ScriptTarget.ES2022,
  },
})
const moduleURL = `data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`
const { assistantPlanProgress, parseAssistantPlan } = await import(moduleURL)

test('parses an ordered three-step assistant plan', () => {
  assert.deepEqual(
    parseAssistantPlan({
      steps: [
        { content: 'Inspect the quote form', status: 'completed' },
        { content: 'Update the quote form', activeForm: 'Updating the quote form', status: 'in_progress' },
        { content: 'Verify the preview', status: 'pending' },
      ],
    }),
    {
      steps: [
        { content: 'Inspect the quote form', status: 'completed' },
        { content: 'Update the quote form', activeForm: 'Updating the quote form', status: 'in_progress' },
        { content: 'Verify the preview', status: 'pending' },
      ],
    },
  )
})

test('rejects plans with zero or more than fifty steps', () => {
  assert.equal(parseAssistantPlan({ steps: [] }), undefined)
  assert.equal(
    parseAssistantPlan({
      steps: Array.from({ length: 51 }, () => ({ content: 'Inspect project', status: 'pending' })),
    }),
    undefined,
  )
})

test('rejects blank content and unsupported statuses', () => {
  assert.equal(parseAssistantPlan({ steps: [{ content: '  ', status: 'pending' }] }), undefined)
  assert.equal(parseAssistantPlan({ steps: [{ content: 'Inspect project', status: 'blocked' }] }), undefined)
})

test('rejects labels longer than one hundred twenty UTF-8 bytes', () => {
  assert.equal(
    parseAssistantPlan({ steps: [{ content: 'x'.repeat(121), status: 'pending' }] }),
    undefined,
  )
  assert.equal(
    parseAssistantPlan({ steps: [{ content: '😀'.repeat(31), status: 'pending' }] }),
    undefined,
  )
})

test('rejects a non-string active form', () => {
  assert.equal(
    parseAssistantPlan({ steps: [{ content: 'Inspect project', activeForm: 42, status: 'pending' }] }),
    undefined,
  )
})

test('strips arbitrary metadata outside the documented plan fields', () => {
  assert.deepEqual(
    parseAssistantPlan({
      steps: [{ content: 'Inspect project', activeForm: 'Inspecting project', status: 'pending', secret: 'discard' }],
      internal: { debug: true },
    }),
    {
      steps: [{ content: 'Inspect project', activeForm: 'Inspecting project', status: 'pending' }],
    },
  )
})

test('derives compact progress using active form before content', () => {
  const plan = {
    steps: [
      { content: 'Inspect the quote form', status: 'completed' },
      { content: 'Update the quote form', activeForm: 'Updating the quote form', status: 'in_progress' },
      { content: 'Verify the preview', status: 'pending' },
    ],
  }

  assert.deepEqual(assistantPlanProgress(plan), {
    completed: 1,
    total: 3,
    activeLabel: 'Updating the quote form',
  })
  assert.equal(
    assistantPlanProgress({ steps: [{ content: 'Verify the preview', status: 'in_progress' }] }).activeLabel,
    'Verify the preview',
  )
})
