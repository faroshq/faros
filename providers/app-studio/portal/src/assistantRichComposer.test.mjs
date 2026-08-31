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

test('projects canonical contentParts with bounded selection identity', async () => {
  const { assistantThreadItemsToMessages } = await vite.ssrLoadModule('/src/assistantThreadProjection.ts')
  const [message] = assistantThreadItemsToMessages([{
    id: 'user-rich',
    turnID: 'run-rich',
    type: 'userMessage',
    status: 'completed',
    content: 'Review this',
    data: {
      skills: [{ id: 'project:review', name: 'Review', description: 'public', scope: 'project' }],
      contextResources: [{ provider: 'demo', resourceRef: { apiVersion: 'demo.example/v1', kind: 'Table', resource: 'tables', name: 'trips' } }],
      contentParts: [
        { type: 'text', text: 'Review ' },
        { type: 'skill', skillID: 'project:review' },
        { type: 'text', text: ' this ' },
        { type: 'resource', resourceIndex: 0 },
        { type: 'resource', resourceIndex: 3 },
        { type: 'skill', skillID: 'private:not-selected' },
      ],
    },
    sequence: 1,
    createdAt: '2026-08-10T00:00:00Z',
  }], 'demo')

  assert.deepEqual(message.metadata.assistantContentParts, [
    { type: 'text', text: 'Review ' },
    { type: 'skill', skillID: 'project:review' },
    { type: 'text', text: ' this ' },
    { type: 'resource', resourceIndex: 0 },
  ])
})

test('rich composer source keeps editor input plain and chip deletion atomic', async () => {
  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  assert.match(source, /contenteditable/)
  assert.match(source, /event\.key === 'Backspace'/)
  assert.match(source, /event\.key === 'Delete'/)
  assert.match(source, /range\.deleteContents\(\)/)
  assert.match(source, /event\.clipboardData\?\.getData\('text\/plain'\)/)
  assert.match(source, /const offsets = selectionOffsets\(\)[\s\S]*rangeForOffsets\(start, offsets\[1\]\)/)
  assert.match(source, /setCaretOffset\(start \+ text\.length, true\)/)
  assert.match(source, /composing\.value/)
  assert.match(source, /assistantSlashToken\(visibleTextFromDOM\(\), caret\)/)
  assert.doesNotMatch(source, /[\u2726\u25c7]/u)
})

test('rich composer exposes one combined Files picker while retaining command entry', async () => {
  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  assert.match(source, /Paperclip/)
  assert.match(source, /aria-label="Add"/)
  assert.match(source, /@click="openAttachmentPicker"[\s\S]*Paperclip[\s\S]*Files/)
  assert.match(source, /:accept="ASSISTANT_ATTACHMENT_ACCEPT"/)
  assert.match(source, /:accept="ASSISTANT_ATTACHMENT_ACCEPT"[\s\S]*multiple/)
  assert.match(source, /@click="openPalette"/)
  assert.doesNotMatch(source, /Screenshot|Text file/)
})

test('rich composer renders image uploads through the lifecycle-safe thumbnail preview', async () => {
  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  const preview = await readFile(new URL('./AssistantAttachmentPreview.vue', import.meta.url), 'utf8')
  assert.match(source, /AssistantAttachmentPreview/)
  assert.match(source, /chip\.file && assistantAttachmentIsImage\(chip\.file\)/)
  assert.match(source, /@retry="retryAttachment\(chip\)"/)
  assert.match(source, /@remove="removeAttachment\(chip\)"/)
  assert.match(preview, /URL\.createObjectURL\(file\)/)
  assert.match(preview, /URL\.revokeObjectURL\(previewURL\.value\)/)
  assert.match(preview, /watch\(\(\) => props\.file, syncPreview, \{ immediate: true \}\)/)
  assert.match(preview, /onBeforeUnmount\(revokePreview\)/)
  assert.match(preview, /absolute right-1 top-1[\s\S]*aria-label="Remove attachment"/)
})

test('Add flyouts dismiss on outside pointer/focus while preserving in-composer interactions', async () => {
  const [rich, preProject, dismiss] = await Promise.all([
    readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8'),
    readFile(new URL('./AssistantPreProjectComposer.vue', import.meta.url), 'utf8'),
    readFile(new URL('./useDismissibleAddMenu.ts', import.meta.url), 'utf8'),
  ])
  assert.match(dismiss, /function isInside\(target: EventTarget \| null\)/)
  assert.match(dismiss, /container\.contains\(target\)/)
  assert.match(dismiss, /if \(!open\.value \|\| isInside\(event\.target\)\) return/)
  assert.match(dismiss, /function handleFocusIn\(event: FocusEvent\)/)
  assert.match(dismiss, /function handleKeydown\(event: KeyboardEvent\)/)
  assert.match(dismiss, /event\.key !== 'Escape'/)
  assert.match(dismiss, /nextTick\(\(\) => \{[\s\S]*trigger\.value\?\.focus\(\{ preventScroll: true \}\)/)
  assert.match(dismiss, /function dismissibleMenuNavigationIndex\(key: string, currentIndex: number, itemCount: number\)/)
  assert.match(dismiss, /watch\(open, \(isOpen\) => \{[\s\S]*focusFirstMenuItem\(\)/)
  assert.match(dismiss, /root\.value\?\.addEventListener\('keydown', handleMenuKeydown\)/)
  assert.match(dismiss, /document\.addEventListener\('pointerdown', handlePointerDown, true\)/)
  assert.match(dismiss, /document\.addEventListener\('focusin', handleFocusIn, true\)/)
  assert.match(dismiss, /document\.removeEventListener\('pointerdown', handlePointerDown, true\)/)
  assert.match(dismiss, /document\.removeEventListener\('focusin', handleFocusIn, true\)/)
  assert.match(dismiss, /onBeforeUnmount\(\(\) => \{[\s\S]*removeEventListener\('keydown', handleKeydown, true\)/)
  for (const source of [rich, preProject]) {
    assert.match(source, /ref="rootRef"/)
    assert.match(source, /ref="addMenuRootRef" class="contents"/)
    assert.match(source, /useDismissibleAddMenu\(\{[\s\S]*open: attachmentMenuOpen,[\s\S]*root: addMenuRootRef,[\s\S]*onClose: closeAttachmentMenu/)
    assert.match(source, /role="menu"[\s\S]*aria-label="Add"/)
    assert.match(source, /@click="openAttachmentPicker"/)
  }
  assert.match(rich, /ref="rootRef" class="relative min-h-\[72px\]"[\s\S]*ref="addMenuRootRef" class="contents"/)
  assert.match(preProject, /ref="rootRef"[\s\S]*@paste\.self="handlePaste"[\s\S]*ref="addMenuRootRef" class="contents"/)
  assert.match(preProject, /<slot name="menu" \/>/)
  assert.match(rich, /@click="openPalette"/)
})

test('rich attachment removal retries deletion without re-uploading the file', async () => {
  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  const retryStart = source.indexOf('function retryAttachment(')
  const retryEnd = source.indexOf('\n}\n\nasync function removeAttachment', retryStart)
  assert.ok(retryStart >= 0 && retryEnd > retryStart, 'retry and removal handlers must remain explicit')
  const retryBody = source.slice(retryStart, retryEnd)
  assert.match(source, /retryAction\?: 'upload' \| 'delete'/)
  assert.match(source, /chip\.retryAction = 'delete'/)
  assert.match(source, /chip\.retryAction === 'delete' \? 'Removal failed' : chip\.retryAction === 'upload' \? 'Upload failed' : 'Cannot attach'/)
  assert.match(retryBody, /if \(chip\.retryAction === 'delete'\) \{[\s\S]*void removeAttachment\(chip\)[\s\S]*return/)
  assert.ok(retryBody.indexOf('void removeAttachment(chip)') < retryBody.indexOf('void uploadAttachment(chip.file, chip.clientID)'), 'delete retry must not enqueue an upload')
  assert.match(source, /:retry-action="chip\.retryAction"/)
  assert.match(source, /attachmentStatusLabel\(chip\)/)
})

test('cancelled uploads retain a recoverable File and treat absent draft deletes as success', async () => {
  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  const uploadStart = source.indexOf('async function uploadAttachment(')
  const uploadEnd = source.indexOf('\n}\n\nfunction handleAttachmentInput', uploadStart)
  const removeStart = source.indexOf('async function removeAttachment(')
  const removeEnd = source.indexOf('\n}\n\nfunction detectSlash', removeStart)
  assert.ok(uploadStart >= 0 && uploadEnd > uploadStart, 'upload handler must remain explicit')
  assert.ok(removeStart >= 0 && removeEnd > removeStart, 'remove handler must remain explicit')
  const uploadBody = source.slice(uploadStart, uploadEnd)
  const removeBody = source.slice(removeStart, removeEnd)
  assert.match(uploadBody, /isAttachmentAbortError\(error\)/)
  assert.match(uploadBody, /bestEffortDeleteAttachment\(chip, props\.projectName\)/)
  assert.match(uploadBody, /current\.status = 'staged'/)
  assert.match(uploadBody, /current\.error = undefined/)
  assert.match(removeBody, /chip\.controller\?\.abort\(\)/)
  assert.match(removeBody, /bestEffortDeleteAttachment\(chip, props\.projectName\)/)
  assert.match(removeBody, /isProjectAPINotFoundError\(error\)/)
  assert.match(removeBody, /attachmentChips\.value = attachmentChips\.value\.filter/)
})

test('Add menu keyboard navigation wraps, skips disabled entries, and restores trigger focus on Escape', async () => {
  const { dismissibleMenuNavigationIndex } = await vite.ssrLoadModule('/src/useDismissibleAddMenu.ts')
  assert.equal(dismissibleMenuNavigationIndex('ArrowDown', -1, 3), 0)
  assert.equal(dismissibleMenuNavigationIndex('ArrowDown', 2, 3), 0)
  assert.equal(dismissibleMenuNavigationIndex('ArrowUp', -1, 3), 2)
  assert.equal(dismissibleMenuNavigationIndex('ArrowUp', 0, 3), 2)
  assert.equal(dismissibleMenuNavigationIndex('Home', 2, 3), 0)
  assert.equal(dismissibleMenuNavigationIndex('End', 0, 3), 2)
  assert.equal(dismissibleMenuNavigationIndex('ArrowDown', 0, 0), null)
  assert.equal(dismissibleMenuNavigationIndex('PageDown', 0, 3), null)

  const [rich, preProject, dismiss] = await Promise.all([
    readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8'),
    readFile(new URL('./AssistantPreProjectComposer.vue', import.meta.url), 'utf8'),
    readFile(new URL('./useDismissibleAddMenu.ts', import.meta.url), 'utf8'),
  ])
  for (const source of [rich, preProject]) {
    assert.match(source, /ref="attachmentMenuTriggerRef"/)
    assert.match(source, /trigger: attachmentMenuTriggerRef/)
  }
  assert.match(dismiss, /event\.key !== 'Escape'/)
  assert.match(dismiss, /onClose\(\)[\s\S]*trigger\.value\?\.focus/)
  assert.match(dismiss, /key === 'ArrowDown'/)
  assert.match(dismiss, /key === 'ArrowUp'/)
  assert.match(dismiss, /key === 'Home'/)
  assert.match(dismiss, /key === 'End'/)
  assert.match(dismiss, /item\.hasAttribute\('disabled'\)/)
  assert.match(dismiss, /item\.getAttribute\('aria-disabled'\) !== 'true'/)
})

test('local attachment validation chips are removal-only while upload failures retain retry', async () => {
  const { assistantAttachmentValidationError, newAssistantStagedAttachment } = await vite.ssrLoadModule('/src/assistantAttachments.ts')
  const invalidFile = { name: 'notes.pdf', type: 'application/pdf', size: 10 }
  const staged = newAssistantStagedAttachment(invalidFile, assistantAttachmentValidationError(invalidFile))
  assert.ok(staged.error)
  assert.equal(staged.retryable, false)

  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  const localErrorStart = source.indexOf('function attachmentError(')
  const localErrorEnd = source.indexOf('\n}\n\nfunction appendAttachmentError', localErrorStart)
  assert.ok(localErrorStart >= 0 && localErrorEnd > localErrorStart)
  assert.doesNotMatch(source.slice(localErrorStart, localErrorEnd), /retryAction/)
  assert.match(source, /if \(!chip\.retryAction\) return/)
  assert.match(source, /current\.retryAction = 'upload'/)
  assert.match(source, /:retryable="chip\.status === 'error' && !!chip\.retryAction"/)
  assert.match(source, /chip\.retryAction === 'delete' \? 'Removal failed' : chip\.retryAction === 'upload' \? 'Upload failed' : 'Cannot attach'/)
})

test('rich composer presents a hoverable annotation preview with an in-pill clear control', async () => {
  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  const appSource = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  const disclosure = await readFile(new URL('./AssistantMessageAnnotations.vue', import.meta.url), 'utf8')
  assert.match(source, /const localAnnotations = computed/)
  assert.match(source, /if \(part\.type !== 'annotation'\) editor\.append\(createChip\(part\)\)/)
  assert.match(source, /for \(const part of localParts\.value\) \{[\s\S]*if \(part\.type === 'annotation'\) append\(part\)/)
  assert.match(source, /<AssistantMessageAnnotations/)
  assert.match(source, /annotationDocumentId\?: string/)
  assert.match(source, /annotationPagePath\?: string/)
  assert.match(source, /unresolvedAnnotationIds\?: string\[\]/)
  assert.match(source, /:current-document-id="annotationDocumentId"/)
  assert.match(source, /:current-page-path="annotationPagePath"/)
  assert.match(source, /:unresolved-annotation-ids="unresolvedAnnotationIds"/)
  assert.match(source, /:rebind-across-documents="true"/)
  assert.match(disclosure, /currentDocumentId\?: string/)
  assert.match(disclosure, /props\.currentDocumentId\.trim\(\)/)
  assert.match(disclosure, /props\.rebindAcrossDocuments/)
  assert.match(disclosure, /annotation\.pagePath === props\.currentPagePath/)
  assert.match(appSource, /:annotation-document-id="developmentPreviewAnnotationDocumentID"/)
  assert.doesNotMatch(appSource, /:annotation-document-i-d=/)
  assert.match(source, /:clearable="true"/)
  assert.match(source, /@remove-all="removeAllAnnotations"/)
  assert.doesNotMatch(source, /@update-annotation=/)
  assert.doesNotMatch(source, /@remove-annotation=/)
  assert.match(disclosure, /@mouseenter="show"/)
  assert.match(disclosure, /@mouseleave="hide"/)
  assert.match(disclosure, /@focusin="show"/)
  assert.match(disclosure, /:aria-describedby="panelID"/)
  assert.match(disclosure, /role="tooltip"/)
  assert.match(disclosure, /aria-label="Clear annotations"/)
  assert.match(disclosure, /@click\.stop="removeAll"/)
  assert.match(disclosure, /class="group relative inline-flex max-w-full"/)
  assert.match(disclosure, /group-hover:w-6/)
  assert.match(disclosure, /group-hover:opacity-100/)
  assert.match(disclosure, /group-focus-within:opacity-100/)
  assert.doesNotMatch(disclosure, /Edit annotation/)
  assert.doesNotMatch(disclosure, /Remove annotation/)
  assert.doesNotMatch(disclosure, />Remove all</)
  assert.doesNotMatch(source, /dataset\.annotationID/)

  const { default: AssistantMessageAnnotations } = await vite.ssrLoadModule('/src/AssistantMessageAnnotations.vue')
  const html = await renderToString(createSSRApp(AssistantMessageAnnotations, {
    annotations: [{
      id: 'annotation-composer-1', comment: 'Adjust this control', documentID: 'old-document', pagePath: '/',
      viewport: { width: 1024, height: 768 }, target: { tag: 'button', text: 'Save' },
    }],
    currentDocumentId: 'new-document',
    clearable: true,
    disclosureID: 'composer-annotations',
  }))
  assert.match(html, /aria-describedby="composer-annotations-panel"/)
  assert.match(html, /role="tooltip"/)
  assert.match(html, /aria-label="Clear annotations"/)
  assert.match(html, /Stale preview/)
  assert.match(html, /Adjust this control/)

})

test('marks DOM-owned input before emitting props so queued sync preserves the caret', async () => {
  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  const marked = source.indexOf('lastRenderedSignature.value = stateSignature(content, parts, localSkills.value, localResources.value)')
  const modelUpdate = source.indexOf("emit('update:modelValue', content)")
  assert.ok(marked >= 0, 'input path should mark the DOM-owned signature')
  assert.ok(modelUpdate > marked, 'the signature must be marked before the parent prop update')
  assert.match(source, /renderParts\(parts, props\.modelValue, nextSkills, nextResources\)/)
})

test('clears detached selections before external rerenders and preserves the divergence gate', async () => {
  const source = await readFile(new URL('./AssistantRichComposer.vue', import.meta.url), 'utf8')
  const renderStart = source.indexOf('function renderParts(')
  const renderEnd = source.indexOf('\n}\n\ninterface Segment', renderStart)
  assert.ok(renderStart >= 0 && renderEnd > renderStart, 'renderParts should remain a bounded helper')
  const renderBody = source.slice(renderStart, renderEnd)
  const clearSelection = renderBody.indexOf('savedSelection.value = null')
  const replaceChildren = renderBody.indexOf('editor.replaceChildren()')
  assert.ok(clearSelection >= 0, 'rerenders must invalidate saved native ranges')
  assert.ok(replaceChildren > clearSelection, 'saved ranges must be cleared before old nodes are detached')

  const syncStart = source.indexOf('function syncFromProps()')
  const syncEnd = source.indexOf('\n}\n\nwatch(', syncStart)
  assert.ok(syncStart >= 0 && syncEnd > syncStart, 'syncFromProps should remain a bounded helper')
  const syncBody = source.slice(syncStart, syncEnd)
  assert.match(syncBody, /const signature = stateSignature\(props\.modelValue, parts, nextSkills, nextResources\)/)
  assert.match(syncBody, /if \(signature !== lastRenderedSignature\.value\) \{[\s\S]*renderParts\(parts, props\.modelValue, nextSkills, nextResources\)/)
})
