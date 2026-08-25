// @vitest-environment happy-dom

import { createApp, nextTick, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import InstanceDetailPage from './views/InstanceDetailPage.vue'
import { createResourceTombstones } from './refresh'
import type { Instance } from './types'
import { api } from './api'
import { confirmDialog } from './portalkit/confirm'

vi.mock('./api', () => ({
  api: {
    getInstance: vi.fn(),
    getTemplate: vi.fn(),
    deleteInstance: vi.fn(),
  },
  isContextChangedError: vi.fn(() => false),
}))

vi.mock('./portalkit/confirm', () => ({
  confirmDialog: vi.fn(),
}))

const instance: Instance = {
  uid: 'uid-instance-1',
  name: 'demo-instance',
  namespace: 'default',
  template: 'demo-template',
  phase: 'Ready',
  message: 'Ready snapshot',
  values: { snapshot: 'retained-value' },
  children: [],
  createdAt: '2026-08-25T00:00:00Z',
  generation: 1,
  observedGeneration: 1,
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

async function flush(): Promise<void> {
  await Promise.resolve()
  await nextTick()
  await new Promise<void>(resolve => window.setTimeout(resolve, 0))
  await nextTick()
}

function text(selector: string): string {
  return document.querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim() || ''
}

describe('mounted Infrastructure instance detail deletion behavior', () => {
  let app: App<Element> | null = null
  let host: HTMLDivElement

  beforeEach(() => {
    vi.mocked(api.getInstance).mockResolvedValue({ ...instance })
    vi.mocked(api.getTemplate).mockResolvedValue({ template: { view: null } as never })
    vi.mocked(confirmDialog).mockResolvedValue(true)
    host = document.createElement('div')
    document.body.appendChild(host)
  })

  afterEach(() => {
    app?.unmount()
    app = null
    host.remove()
    vi.clearAllMocks()
  })

  it('retains the snapshot and recovers every action after a deferred delete fails', async () => {
    const deleteRequest = deferred<void>()
    vi.mocked(api.deleteInstance).mockReturnValue(deleteRequest.promise)
    const navigate = vi.fn()
    app = createApp(InstanceDetailPage, {
      instanceName: instance.name,
      tombstones: createResourceTombstones(),
      onNavigate: navigate,
    })
    app.mount(host)
    await flush()

    expect(text('.k-resource-stat-card[data-k-resource-stat-card="status"]')).toContain('Ready')
    expect(host.textContent).toContain('retained-value')
    const details = host.querySelector<HTMLDetailsElement>('.instance-detail__menu')
    const deleteButton = host.querySelector<HTMLButtonElement>('.instance-detail__menu-item')
    const back = host.querySelector<HTMLAnchorElement>('.instance-detail__back')
    const refresh = host.querySelector<HTMLButtonElement>('.instance-detail__actions > button')
    expect(details).not.toBeNull()
    expect(deleteButton).not.toBeNull()
    expect(back).not.toBeNull()
    expect(refresh).not.toBeNull()

    details!.setAttribute('open', '')
    deleteButton!.click()
    await flush()

    expect(details!.hasAttribute('open')).toBe(false)
    expect(text('.k-resource-stat-card[data-k-resource-stat-card="status"]')).toContain('Deleting')
    expect(host.querySelector('[role="status"][aria-live="polite"].instance-message')?.textContent)
      .toContain('Deleting this instance.')
    expect(host.textContent).toContain('retained-value')
    expect(refresh!.disabled).toBe(true)
    expect(back!.getAttribute('aria-disabled')).toBe('true')
    back!.click()
    refresh!.click()
    expect(navigate).not.toHaveBeenCalled()
    expect(api.getInstance).toHaveBeenCalledTimes(1)

    deleteRequest.reject({ reason: 'HTTPError', message: 'delete failed' })
    await flush()

    expect(host.querySelector('[role="alert"][aria-live="assertive"]')?.textContent)
      .toContain('HTTPError: delete failed')
    expect(host.querySelector('[role="status"][aria-live="polite"].instance-message')).toBeNull()
    expect(text('.k-resource-stat-card[data-k-resource-stat-card="status"]')).toContain('Ready')
    expect(host.textContent).toContain('retained-value')
    expect(refresh!.disabled).toBe(false)
    expect(back!.getAttribute('aria-disabled')).toBeNull()
    back!.click()
    expect(navigate).toHaveBeenCalledWith('instances')
  })
})
