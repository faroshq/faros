import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const readSource = (file) => readFileSync(resolve(process.cwd(), 'src', file), 'utf8')
const styles = readSource('style.css')
const serviceEdit = readSource('ServiceEdit.vue')

describe('Service detail narrow-screen regressions', () => {
  it('contains the resource composition and collapses facts before the viewport can overflow', () => {
    expect(styles).toMatch(/\.service-detail\s*\{[\s\S]*max-width:\s*100%;/)
    expect(styles).toMatch(/\.service-detail__resource\s*\{[\s\S]*width:\s*100%;[\s\S]*max-width:\s*100%;/)
    expect(styles).toMatch(/\.service-detail__resource,[\s\S]*\.service-detail__card\s*>\s*\*\s*>\s*\*\s*,[\s\S]*box-sizing:\s*border-box;[\s\S]*min-width:\s*0;/)
    expect(styles).toMatch(/\.service-detail__page > header > :first-child,[\s\S]*overflow-wrap:\s*anywhere;[\s\S]*word-break:\s*break-word;/)
    expect(styles).toMatch(/@media\s*\(max-width:\s*420px\)[\s\S]*\.service-detail__facts\s*\{\s*grid-template-columns:\s*minmax\(0,\s*1fr\);/)
  })

  it('keeps no-auth services out of the missing-credential path with human-facing copy', () => {
    expect(serviceEdit).toMatch(/const credentialsRequired = computed\(\(\) => \(entry\.value\?\.auth \?\? ''\)\.toLowerCase\(\) !== 'none'\)/)
    expect(serviceEdit).toMatch(/No credentials required for this service\./)
    expect(serviceEdit).toMatch(/value: !credentialsRequired\.value \? 'Not required'/)
    expect(serviceEdit).toMatch(/tone: !credentialsRequired\.value \? 'default'/)
  })
})
