import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { createServer } from 'vite'

let vite
test.before(async () => {
  vite = await createServer({ appType: 'custom', server: { middlewareMode: true, hmr: false } })
})
test.after(async () => vite?.close())

test('projects the immutable receipt contract and rejects oversized/malformed receipts', async () => {
  const { projectAssistantAttachmentReceipt, MAX_ASSISTANT_ATTACHMENT_BYTES } = await vite.ssrLoadModule('/src/assistantAttachments.ts')
  const receipt = {
    id: 'att-1', filename: 'screen.png', contentType: 'image/png', sizeBytes: 12,
    sha256: 'A'.repeat(64), createdAt: '2026-08-31T00:00:00Z', draft: false,
  }
  assert.deepEqual(projectAssistantAttachmentReceipt(receipt), {
    id: 'att-1', filename: 'screen.png', contentType: 'image/png', sizeBytes: 12,
    sha256: 'a'.repeat(64), createdAt: '2026-08-31T00:00:00Z',
  })
  assert.equal(projectAssistantAttachmentReceipt({ ...receipt, sizeBytes: MAX_ASSISTANT_ATTACHMENT_BYTES + 1 }), null)
  assert.equal(projectAssistantAttachmentReceipt({ ...receipt, sha256: 'not-a-digest' }), null)
  assert.equal(projectAssistantAttachmentReceipt({ ...receipt, createdAt: '' }), null)
})

test('accepts screenshot/text inputs while keeping server text limits visible to the portal', async () => {
  const {
    ASSISTANT_LARGE_PASTE_BYTES,
    assistantAttachmentIsSupported,
    assistantAttachmentMaxBytes,
    MAX_ASSISTANT_TEXT_ATTACHMENT_BYTES,
  } = await vite.ssrLoadModule('/src/assistantAttachments.ts')
  assert.equal(ASSISTANT_LARGE_PASTE_BYTES, 10 << 10)
  assert.equal(assistantAttachmentIsSupported({ name: 'screen.png', type: 'image/png' }), true)
  assert.equal(assistantAttachmentIsSupported({ name: 'screen.webp', type: 'image/webp' }), true)
  assert.equal(assistantAttachmentIsSupported({ name: 'screen.png', type: 'application/octet-stream' }), true)
  assert.equal(assistantAttachmentIsSupported({ name: 'screen.JPEG', type: 'binary/octet-stream' }), true)
  assert.equal(assistantAttachmentIsSupported({ name: 'animation.gif', type: 'image/gif' }), false)
  assert.equal(assistantAttachmentIsSupported({ name: 'notes.md', type: 'text/markdown' }), true)
  assert.equal(assistantAttachmentIsSupported({ name: 'notes.md', type: 'application/octet-stream' }), true)
  assert.equal(assistantAttachmentIsSupported({ name: 'notes.txt', type: 'binary/octet-stream' }), true)
  assert.equal(assistantAttachmentIsSupported({ name: 'notes.txt', type: '' }), true)
  assert.equal(assistantAttachmentIsSupported({ name: 'notes.html', type: 'text/html' }), false)
  assert.equal(assistantAttachmentIsSupported({ name: 'data.json', type: 'application/json' }), false)
  assert.equal(assistantAttachmentIsSupported({ name: 'data.json', type: 'application/octet-stream' }), false)
  assert.equal(assistantAttachmentIsSupported({ name: 'notes.html', type: 'binary/octet-stream' }), false)
  assert.equal(assistantAttachmentIsSupported({ name: 'screen.png', type: 'application/x-png' }), false)
  assert.equal(assistantAttachmentMaxBytes({ name: 'notes.txt', type: 'text/plain' }), MAX_ASSISTANT_TEXT_ATTACHMENT_BYTES)
  assert.equal(assistantAttachmentMaxBytes({ name: 'notes.txt', type: '' }), MAX_ASSISTANT_TEXT_ATTACHMENT_BYTES)
})

test('keeps upload protocol and durable-accept clearing explicit in the portal seams', async () => {
  const [api, composer, app] = await Promise.all([
    readFile(new URL('./api.ts', import.meta.url), 'utf8'),
    readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8'),
    readFile(new URL('./App.vue', import.meta.url), 'utf8'),
  ])
  assert.match(api, /assistant\/attachments/)
  assert.match(api, /method: 'POST'/)
  assert.match(api, /'DELETE'/)
  assert.match(api, /listAssistantAttachments/)
  assert.match(composer, /ASSISTANT_LARGE_PASTE_BYTES/)
  assert.match(composer, /getAsFile\(\)/)
  assert.match(composer, /Retry attachment upload/)
  assert.match(composer, /update:attachmentsPending/)
  assert.match(app, /startPostAccepted = true[\s\S]*clearSelectedTurnAttachments\(\)/)
})
