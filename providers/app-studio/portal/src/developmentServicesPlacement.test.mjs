import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const app = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
const panel = await readFile(new URL('./DevelopmentServicesPanel.vue', import.meta.url), 'utf8')

test('keeps Preview outcome-focused and places the full service editor in Development settings', () => {
  const previewStart = app.indexOf('v-else-if="activeWorkbenchTab?.kind === \'preview\'"')
  const previewEnd = app.indexOf('v-else-if="activeWorkbenchTab?.kind === \'code\'"', previewStart)
  const preview = app.slice(previewStart, previewEnd)
  const settingsStart = app.indexOf('aria-label="Development settings"')
  const settingsEnd = app.indexOf('v-else-if="publishingInWorkbench"', settingsStart)
  const settings = app.slice(settingsStart, settingsEnd)

  assert.ok(previewStart >= 0 && previewEnd > previewStart)
  assert.ok(settingsStart >= 0 && settingsEnd > settingsStart)
  assert.doesNotMatch(preview, /<DevelopmentServicesPanel/)
  assert.match(settings, /<DevelopmentServicesPanel[\s\S]*embedded[\s\S]*@services-updated="handleDevelopmentServicesUpdated"/)
  assert.ok(settings.indexOf('<DevelopmentServicesPanel') < settings.indexOf('id="development-preview-access-heading"'))
  assert.match(panel, /embedded\?: boolean/)
  assert.match(panel, /embedded \? 'border-t border-border-subtle pt-4'/)
  assert.match(panel, />Preview services</)
})

test('loads a stale-fenced service summary independently of the Settings editor', () => {
  assert.match(app, /async function loadDevelopmentServicesSummary[\s\S]*api\.listDevelopmentServices\(props\.ctx, projectName\)/)
  assert.match(app, /serial !== developmentServicesSummarySerial \|\| selected\.value\?\.name !== projectName/)
  assert.match(app, /if \(developmentServicesSummaryInFlight\) return[\s\S]*developmentServicesSummaryInFlight = true/)
  assert.match(app, /developmentServicesLoaded\.value = true/)
  assert.match(app, /developmentServicesError\.value = cause instanceof Error/)
  assert.match(app, /window\.setInterval\(\(\) => \{[\s\S]*!developmentServicesSummaryInFlight && !settingsInWorkbench\.value[\s\S]*loadDevelopmentServicesSummary\(\{ background: true \}\)[\s\S]*\}, 5000\)/)
  assert.match(app, /watch\(\s*\(\) => \[[\s\S]*selected\.value\?\.name[\s\S]*props\.ctx\?\.token[\s\S]*props\.ctx\?\.tenant[\s\S]*props\.ctx\?\.subPath[\s\S]*resetDevelopmentServicesSummary\(\)[\s\S]*loadDevelopmentServicesSummary\(\)[\s\S]*scheduleDevelopmentServicesSummaryRefresh\(\)/)
})

test('deep-links an unconfigured universal preview to the focused Preview services settings', () => {
  assert.match(app, /const universalPreviewNeedsSetup = computed[\s\S]*!selected\.value\.template[\s\S]*developmentServicesLoaded\.value[\s\S]*!developmentServicesAvailable\.value/)
  assert.match(app, /\? 'Set up your preview'/)
  assert.match(app, />\s*Configure preview\s*</)
  assert.match(app, /async function openDevelopmentServicesSettings\(\)[\s\S]*openBuiltInWorkbenchTab\('settings'\)[\s\S]*developmentServicesPanelRef\.value\?\.focus\(\)/)
  assert.match(panel, /defineExpose\(\{ focus \}\)/)
  assert.match(panel, /ref="headingRef" tabindex="-1"/)
})

test('keeps configured pending and error previews truthful with a compact management path', () => {
  assert.match(app, /developmentServicesAvailable\.value[\s\S]*developmentPreviewServiceTarget\.value\?\.error[\s\S]*'Preview needs attention'[\s\S]*`Starting \$\{developmentPreviewService\.value \|\| 'preview'\}…`/)
  assert.match(app, /developmentPreviewServiceTarget\.value\?\.process\?\.message/)
  assert.match(app, /v-else-if="developmentServicesAvailable"[\s\S]*Manage preview service/)
  assert.match(app, /developmentServicesError && !developmentServicesAvailable && !selected\?\.template[\s\S]*>Retry</)
})
