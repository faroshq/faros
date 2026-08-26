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

  it('separates unsupported, optional, and required credential states', () => {
    expect(serviceEdit).toMatch(/const credentialsSupported = computed\(\(\) => \{[\s\S]*auth !== '' && auth !== 'none'/)
    expect(serviceEdit).toMatch(/const credentialsOptional = computed\(\(\) => credentialsSupported\.value && !!entry\.value\?\.credential\.optional\)/)
    expect(serviceEdit).toMatch(/const credentialsRequired = computed\(\(\) => credentialsSupported\.value && !credentialsOptional\.value\)/)
    expect(serviceEdit).toMatch(/value: 'Not configured \(optional\)'[\s\S]*tone: 'default'/)
    expect(serviceEdit).toMatch(/value: 'Missing'[\s\S]*tone: 'warning'/)
    expect(serviceEdit).toMatch(/No credentials required for this service\./)
    expect(serviceEdit).toMatch(/<template v-if="credentialsSupported">/)
    expect(serviceEdit).toMatch(/credentialsOptional \? ' \(optional\)'/)
  })

  it('summarizes credential health once while retaining the detailed credentials section', () => {
    expect(serviceEdit).not.toMatch(/id: 'credentials'/)
    expect(serviceEdit).toMatch(/id: 'status'[\s\S]*detail: credentialState\.value\.detail/)
    expect(serviceEdit).toMatch(/<ResourceSectionCard[^>]*id="service-credentials"[^>]*eyebrow="Access"[^>]*title="Credentials"/)
  })

  it('keeps deletion status visible and politely announced after the menu closes', () => {
    expect(serviceEdit).toMatch(/const deleting = ref\(false\)/)
    expect(serviceEdit).toMatch(/if \(deleting\.value\) return 'Deleting'/)
    expect(serviceEdit).toMatch(/<p v-if="deleting" class="waiting" role="status" aria-live="polite">Deleting this service\./)
    expect(serviceEdit).toMatch(/actionsMenu\.value\?\.removeAttribute\('open'\)/)
    expect(serviceEdit).toMatch(/deleting\.value = true[\s\S]*await deleteEdgeService\(name\)[\s\S]*emit\('deleted'\)/)
    expect(serviceEdit).toMatch(/mutationError\.value = errorMessage\(error, 'Delete failed'\)[\s\S]*deleting\.value = false/)
  })

  it('uses the provider UI route as the service backlink fallback', () => {
    expect(serviceEdit).toMatch(/<a class="k-btn k-btn--ghost service-detail__back" href="\/ui\/providers\/edges\/services" @click\.prevent="emit\('back'\)"/)
  })
})
