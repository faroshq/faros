import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const app = await readFile(new URL('./App.vue', import.meta.url), 'utf8')

test('mounts the App Studio-owned thread rail against canonical lifecycle handlers', () => {
  assert.match(app, /import ThreadRail from '\.\/ThreadRail\.vue'/)
  assert.match(app, /<ThreadRail[\s\S]*:threads="assistantThreads"[\s\S]*:active-thread-i-d="activeAssistantThreadID"/)
  assert.match(app, /<ThreadRail[\s\S]*:unread-thread-i-ds="unreadAssistantThreadIDs"/)
  assert.match(app, /<ThreadRail[\s\S]*:pinned-thread-i-ds="pinnedAssistantThreadIDs"/)
  assert.match(app, /<ThreadRail[\s\S]*@select="selectAssistantThread"[\s\S]*@create="createAssistantThread"/)
  assert.match(app, /<ThreadRail[\s\S]*@archive="archiveAssistantThread"[\s\S]*@toggle-pin="toggleThreadPin"[\s\S]*@set-unread="setThreadUnread"/)
  assert.doesNotMatch(app, /Manage threads|manageAssistantThreads|@manage=/)
})

test('keeps pin and manual read state project scoped and archives through the canonical API', () => {
  assert.match(app, /from '\.\/assistantThreadPinState'/)
  assert.match(app, /readAssistantThreadPins\([\s\S]*assistantThreadFocusStorageKey\(assistantThreadFocusScope\(projectName\)\)/)
  assert.match(app, /toggleAssistantThreadPin\([\s\S]*pinnedAssistantThreadIDs\.value/)
  assert.match(app, /markAssistantThreadUnread\(scopeKey, thread\)/)
  assert.match(app, /markAssistantThreadRead\(scopeKey, thread\)/)
  assert.match(app, /threadID === activeAssistantThreadID\.value[\s\S]*setThreadUnread\(threadID, false\)/)
  assert.match(app, /api\.patchAssistantThread\(props\.ctx, projectName, threadID, \{ archived: true \}\)/)
  assert.match(app, /const remaining = assistantThreads\.value\.filter\(\(thread\) => thread\.id !== threadID\)/)
  assert.match(app, /if \(nextThreadID\)[\s\S]*await selectAssistantThread\(nextThreadID\)[\s\S]*activeAssistantThreadID\.value === threadID[\s\S]*createAssistantThread\(\)/)
})

test('derives unread dots from persisted project-scoped update markers', () => {
  assert.match(app, /from '\.\/assistantThreadReadState'/)
  assert.match(app, /reconcileAssistantThreadReadState\([\s\S]*assistantThreadFocusStorageKey\(assistantThreadFocusScope\(projectName\)\)/)
  assert.doesNotMatch(app, /ThreadsWorkbench|builtin:threads|activeWorkbenchTab\?\.kind === 'threads'/)
})

test('keeps identity and thread controls in one workspace-wide title bar', () => {
  assert.match(app, /const activeAssistantThreadTitle = computed/)
  assert.match(app, /<header data-app-studio-titlebar/)
  assert.match(app, /aria-label="Toggle thread side panel"[\s\S]*@click="toggleThreadPanel"/)
  assert.match(app, /aria-label="Toggle thread side panel"[\s\S]*@pointerenter="previewThreadPanel"[\s\S]*@pointerleave="closeThreadPanelPreview"/)
  assert.match(app, /aria-label="Toggle thread side panel"[\s\S]*@focusin="previewThreadPanel"[\s\S]*@focusout="closeThreadPanelPreview"/)
  assert.match(app, /function toggleThreadPanel\(\)[\s\S]*threadRailRef\.value\?\.toggle\?\.\(\)/)
  assert.match(app, /function previewThreadPanel\(\)[\s\S]*threadRailRef\.value\?\.previewEnter\?\.\(\)/)
  assert.match(app, /function closeThreadPanelPreview\(\)[\s\S]*threadRailRef\.value\?\.previewLeave\?\.\(\)/)
  assert.match(app, /function beginAssistantThreadTitleRename\(\)[\s\S]*assistantThreadTitleInput\.value\?\.focus\(\)/)
  assert.match(app, /function renameAssistantThread\(threadID: string, title: string\)[\s\S]*api\.patchAssistantThread\(props\.ctx, projectName, threadID, \{ title: normalizedTitle \}\)/)
  assert.match(app, /function commitAssistantThreadTitleRename\(\)[\s\S]*renameAssistantThread\(thread\.id, normalizedTitle\)/)
  assert.match(app, /aria-label="Rename thread"[\s\S]*@click="beginAssistantThreadTitleRename"/)
  assert.match(app, /ref="assistantThreadTitleInput"[\s\S]*@keydown\.enter\.exact\.prevent="commitAssistantThreadTitleRename"[\s\S]*@keydown\.esc\.stop\.prevent="cancelAssistantThreadTitleRename"[\s\S]*@blur="commitAssistantThreadTitleRename"/)
  assert.match(app, /<span class="truncate">\{\{ activeAssistantThreadTitle \}\}<\/span>/)
  assert.match(app, /selected\.displayName \|\| selected\.name \|\| 'Project'/)
  assert.match(app, /selected\.repository\?\.name \|\| selected\.repository\?\.ref \|\| 'No repository'/)
  const titleBarStart = app.indexOf('<header data-app-studio-titlebar')
  const titleBarEnd = app.indexOf('</header>', titleBarStart)
  const railStart = app.indexOf('<ThreadRail', titleBarEnd)
  assert.ok(titleBarStart >= 0 && titleBarEnd > titleBarStart && railStart > titleBarEnd)
  const titleBar = app.slice(titleBarStart, titleBarEnd)
  assert.match(app, /const APP_STUDIO_ICON_URL = '\/ui\/providers\/app-studio\/icon\.svg'/)
  assert.match(titleBar, /<img :src="APP_STUDIO_ICON_URL" alt="" class="h-4 w-4 object-contain"/)
  assert.doesNotMatch(titleBar, /<MessageSquare/)
  assert.doesNotMatch(titleBar, /aria-label="New thread"|@click="createAssistantThread"/)
})

test('uses the thread side panel as the only conversation-switching surface', () => {
  assert.doesNotMatch(app, /Open conversation threads|openConversationThreads|Switch thread:/)
  assert.doesNotMatch(app, /Switch conversations, rename threads|builtInTab: 'threads'/)
})
