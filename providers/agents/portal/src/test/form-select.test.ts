import { describe, expect, it, vi } from 'vitest'
import type { FormSelectElement } from '../portalkit/form-select'
import '../portalkit/form-select'

function mountSelect(): FormSelectElement {
  const label = document.createElement('span')
  label.id = 'model-label'
  label.textContent = 'Model credential'
  const select = document.createElement('faros-form-select')
  select.labelledby = label.id
  select.options = [
    { value: '', label: '— no model —' },
    { value: 'main', label: 'main (gpt-5)' },
    { value: 'disabled', label: 'disabled', disabled: true },
    { value: 'backup', label: 'backup (claude)' },
  ]
  select.value = 'main'
  document.body.append(label, select)
  return select
}

describe('PortalKit form select web component', () => {
  it('matches the canonical combobox/listbox and portalled menu contract', () => {
    const select = mountSelect()
    const trigger = select.querySelector<HTMLButtonElement>('[role="combobox"]')!
    expect(trigger.classList.contains('k-input')).toBe(true)
    expect(trigger.classList.contains('k-form-select__trigger')).toBe(true)
    expect(trigger.getAttribute('aria-labelledby')).toContain('model-label')
    expect(trigger.getAttribute('aria-expanded')).toBe('false')

    trigger.click()
    const panel = document.body.querySelector<HTMLElement>('.k-form-select__portal')!
    expect(panel).not.toBeNull()
    expect(select.contains(panel)).toBe(false)
    expect(panel.getAttribute('role')).toBe('listbox')
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    expect(trigger.getAttribute('aria-activedescendant')).toContain('option-1')
  })

  it('wraps keyboard navigation, skips disabled options, and emits the value', () => {
    const select = mountSelect()
    const changed = vi.fn()
    select.addEventListener('change', changed)
    const trigger = select.querySelector<HTMLButtonElement>('[role="combobox"]')!

    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    expect(trigger.getAttribute('aria-activedescendant')).toContain('option-3')
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))

    expect(select.value).toBe('backup')
    expect(changed).toHaveBeenCalledOnce()
    expect((changed.mock.calls[0][0] as CustomEvent<string>).detail).toBe('backup')
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
  })

  it('closes on Escape and restores focus to the trigger', async () => {
    const select = mountSelect()
    const trigger = select.querySelector<HTMLButtonElement>('[role="combobox"]')!
    trigger.click()
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    await Promise.resolve()

    expect(document.body.querySelector('.k-form-select__portal')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })
})
