import { describe, expect, it } from 'vitest'

import instanceListSource from './views/InstanceListPage.vue?raw'

function inactiveChangeBranch(source: string): string {
  const conditionOffset = source.indexOf('if (!activeQuery)')
  expect(conditionOffset).toBeGreaterThanOrEqual(0)
  const openBrace = source.indexOf('{', conditionOffset)
  expect(openBrace).toBeGreaterThanOrEqual(0)
  let depth = 0
  for (let offset = openBrace; offset < source.length; offset += 1) {
    if (source[offset] === '{') depth += 1
    if (source[offset] !== '}') continue
    depth -= 1
    if (depth === 0) return source.slice(conditionOffset, offset + 1)
  }
  throw new Error('unterminated inactive query branch')
}

describe('instance server pagination transitions', () => {
  it('preserves the incoming page and opaque cursor for unfiltered page changes', () => {
    const branch = inactiveChangeBranch(instanceListSource)
    expect(branch).toMatch(/void load\(\)/)
    expect(branch).not.toMatch(/page\.value = 1/)
    expect(branch).not.toMatch(/cursor\.value = null/)
    expect(branch).toContain('wasClientMode || change.reason === \'query\' || change.reason === \'filter\'')
    expect(branch).not.toContain("change.reason === 'page'")
  })

  it('resets a client-side clear to the first server page', () => {
    const helperOffset = instanceListSource.indexOf('function resetToFirstServerPage()')
    expect(helperOffset).toBeGreaterThanOrEqual(0)
    const helper = instanceListSource.slice(helperOffset, instanceListSource.indexOf('\n}', helperOffset) + 2)
    expect(helper).toContain('page.value = 1')
    expect(helper).toContain('cursor.value = null')

    const branch = inactiveChangeBranch(instanceListSource)
    expect(branch).toContain('if (wasClientMode || change.reason === \'query\' || change.reason === \'filter\') resetToFirstServerPage()')
  })
})
