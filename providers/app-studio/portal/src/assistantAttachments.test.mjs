import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { createServer } from 'vite'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

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

test('history renders image previews through the scoped API while text stays compact', async () => {
  const [{ default: AttachmentComponent }, { api }, app, apiSource] = await Promise.all([
    vite.ssrLoadModule('/src/AssistantMessageAttachments.vue'),
    vite.ssrLoadModule('/src/api.ts'),
    readFile(new URL('./App.vue', import.meta.url), 'utf8'),
    readFile(new URL('./api.ts', import.meta.url), 'utf8'),
  ])
  const originalGet = api.getAssistantAttachment
  const calls = []
  // The component and this test resolve the same Vite module instance. Keep
  // the fetch stub at the existing scoped API seam rather than bypassing it.
  assert.equal(typeof originalGet, 'function')
  api.getAssistantAttachment = async (...args) => {
    calls.push(args)
    return new Blob(['image-bytes'], { type: 'image/png' })
  }
  const image = {
    id: 'att-image', filename: 'screen.png', contentType: 'image/png', sizeBytes: 12,
    sha256: 'a'.repeat(64), createdAt: '2026-08-31T00:00:00Z',
  }
  const text = {
    id: 'att-text', filename: 'plan.txt', contentType: 'text/plain', sizeBytes: 42,
    sha256: 'b'.repeat(64), createdAt: '2026-08-31T00:00:00Z',
  }
  try {
    const html = await renderToString(createSSRApp(AttachmentComponent, {
      attachments: [image, text],
      ctx: { token: 'user-token' },
      projectName: 'demo',
    }))
    assert.match(html, /aria-busy="true"/)
    assert.match(html, /Loading screen\.png/)
    assert.match(html, /plan\.txt/)
    await Promise.resolve()
    assert.equal(calls.length, 1)
    assert.equal(calls[0][1], 'demo')
    assert.equal(calls[0][2], 'att-image')
    assert.ok(calls[0][3] instanceof AbortSignal)
  } finally {
    api.getAssistantAttachment = originalGet
  }
  assert.match(app, /<AssistantMessageAttachments[\s\S]*:ctx="props\.ctx"[\s\S]*:project-name="message\.projectID"/)
  assert.match(apiSource, /async getAssistantAttachment\(ctx: FarosContext \| null, name: string, attachmentID: string, signal\?: AbortSignal\)/)
  assert.match(apiSource, /cache: 'no-cache'[\s\S]*signal,/)
})

test('image preview lifecycle revokes object URLs and aborts stale scoped requests', async () => {
  const componentSource = await readFile(new URL('./AssistantMessageAttachments.vue', import.meta.url), 'utf8')
  assert.match(componentSource, /URL\.createObjectURL\(blob\)/)
  assert.match(componentSource, /URL\.revokeObjectURL\(preview\.url\)/)
  assert.match(componentSource, /URL\.revokeObjectURL\(url\)/)
  assert.match(componentSource, /new AbortController\(\)/)
  assert.match(componentSource, /api\.getAssistantAttachment\(props\.ctx, props\.projectName, attachment\.id, controller\.signal\)/)
  assert.match(componentSource, /onBeforeUnmount\(\(\) => \{[\s\S]*releasePreview\(attachmentID\)/)
})
