import { describe, expect, it } from 'vitest'

const source = import.meta.glob('./views/InstanceDetailPage.vue', {
  query: '?raw',
  import: 'default',
  eager: true,
})['./views/InstanceDetailPage.vue'] as string

describe('Infrastructure instance detail conformance', () => {
  it('keeps route navigation outside the shared resource shell and guards deletion', () => {
    expect(source.indexOf('class="k-btn k-btn--ghost k-back-action instance-detail__back"')).toBeLessThan(source.indexOf('<ResourcePage'))
    expect(source).toContain('@click.prevent="goBack"')
    expect(source).toContain('if (deleting.value || deletionInProgress.value) return')
  })

  it('retains the shared detail renderer and product-facing summary facts', () => {
    expect(source).toContain('ResourceStatCards')
    expect(source).toContain('id: \'created\'')
    expect(source).toContain('label: \'Child resources\'')
    expect(source).toContain('resolve(field, inst)')
    expect(source).toContain('<ViewValue :value="resolve(field, inst)"')
    expect(source).toContain(':title="group.title || \'\'"')
    expect(source).toContain('JSON.stringify(inst.values, null, 2)')
    expect(source).toContain("{ key: 'kind', label: 'Kind' }")
    expect(source).toContain("{ key: 'name', label: 'Name' }")
    expect(source).toContain("{ key: 'namespaceLabel', label: 'Namespace' }")
    expect(source).toContain("{ key: 'phaseLabel', label: 'Phase' }")
  })
})
