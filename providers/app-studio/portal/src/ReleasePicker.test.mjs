import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const picker = await readFile(new URL('./ReleasePicker.vue', import.meta.url), 'utf8')

test('renders release history as a restrained vertical deployment timeline', () => {
  assert.match(picker, />Release history</)
  assert.match(picker, /role="radiogroup"[\s\S]*aria-orientation="vertical"/)
  assert.match(picker, /release\.live \? 'border-success bg-success' : 'border-border-default'/)
  assert.match(picker, />Current production</)
  assert.match(picker, />Incomplete</)
  assert.match(picker, /selectedCommit === release\.commitSHA[\s\S]*border-accent\/50/)
  assert.match(picker, /motion-reduce:animate-none/)
  assert.match(picker, /shimmer[^"\n]*motion-reduce:animate-none/)
  assert.doesNotMatch(picker, /GitBranch|<Check|>Complete</)
})

test('keeps commit navigation separate from the selectable radio row', () => {
  const radioStart = picker.indexOf('role="radio"')
  const radioEnd = picker.indexOf('</button>', radioStart)
  const commitLink = picker.indexOf('>View commit</a>', radioEnd)
  assert.ok(radioStart >= 0 && radioEnd > radioStart && commitLink > radioEnd)
  assert.doesNotMatch(picker.slice(radioStart, radioEnd), /<a\b/)
})
