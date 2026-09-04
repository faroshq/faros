import { describe, expect, it, vi } from 'vitest'
import type { ResourceTableFilterElement } from '../portalkit/resource-table-filter'
import '../portalkit/resource-table-filter'

function mountFilter(): ResourceTableFilterElement {
  const filter = document.createElement('faros-resource-table-filter')
  filter.label = 'Kind'
  filter.allLabel = 'All kinds'
  filter.options = [
    { value: 'connection', label: 'Connection' },
    { value: 'channel', label: 'Channel' },
  ]
  document.body.append(filter)
  return filter
}

describe('PortalKit resource table filter web component', () => {
  it('uses the standard visible-label combobox and portalled listbox', () => {
    const filter = mountFilter()
    const root = filter.querySelector('.k-table__filter')!
    const trigger = filter.querySelector<HTMLButtonElement>('.k-table__filter-trigger')!
    expect(root.textContent).toContain('Kind')
    expect(trigger.type).toBe('button')
    expect(trigger.getAttribute('role')).toBe('combobox')
    expect(trigger.getAttribute('aria-labelledby')).toContain('label')
    expect(trigger.textContent).toContain('All')

    trigger.click()
    const panel = document.body.querySelector('.k-table__filter-panel')!
    expect(panel).not.toBeNull()
    expect(filter.contains(panel)).toBe(false)
    expect(panel.querySelector('[role="listbox"]')).not.toBeNull()
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
  })

  it('commits one keyboard selection and closes on disconnect', () => {
    const filter = mountFilter()
    const changed = vi.fn()
    filter.addEventListener('change', changed)
    const trigger = filter.querySelector<HTMLButtonElement>('.k-table__filter-trigger')!
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))

    expect(filter.value).toBe('connection')
    expect(changed).toHaveBeenCalledOnce()
    expect((changed.mock.calls[0][0] as CustomEvent<string>).detail).toBe('connection')
    expect(document.body.querySelector('.k-table__filter-panel')).toBeNull()

    trigger.click()
    filter.remove()
    expect(document.body.querySelector('.k-table__filter-panel')).toBeNull()
  })
})
