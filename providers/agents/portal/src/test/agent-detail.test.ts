import { describe, expect, it, vi } from 'vitest'
import type { AgentDetail } from '../views/agent-detail'
import '../views/agent-detail'
import { agentFixture, makeStore, mount, settle, stubApi, text } from './helpers'

describe('agent resource detail conformance', () => {
  it('uses the PortalKit resource composition while retaining the config and chat workbench', async () => {
    const api = stubApi()
    const store = makeStore(api)
    store.agents.data = [agentFixture('scout', { displayName: 'Scout', description: 'Watches the deploy queue.' })]
    store.agents.loaded = true
    store.agents.hasSnapshot = true

    const el = await mount<AgentDetail>('agents-agent-detail', { store, api, name: 'scout', tab: 'config' })
    const back = el.querySelector<HTMLAnchorElement>('.k-back-action')!
    const page = el.querySelector<HTMLElement>('.k-resource-page')!

    expect(back).not.toBeNull()
    expect(back.getAttribute('href')).toBe('#/agents')
    expect(back.compareDocumentPosition(page) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(page.querySelectorAll('h1.k-resource-page__title')).toHaveLength(1)
    expect(text(page.querySelector('h1'))).toBe('Scout')
    expect(text(page.querySelector('.k-resource-page__kind'))).toBe('Agent')
    expect(text(page.querySelector('.k-resource-page__meta code'))).toBe('scout')
    expect(text(page.querySelector('.k-resource-page__subtitle'))).toBe('Watches the deploy queue.')
    expect(page.querySelector('.k-resource-page__actions .k-btn--danger')).not.toBeNull()

    const tabs = [...page.querySelectorAll<HTMLButtonElement>('[data-k-tab-id]')]
    expect(tabs.map((tab) => tab.dataset.kTabId)).toEqual(['config', 'runs'])
    expect(tabs[0].getAttribute('aria-current')).toBe('page')
    expect(page.querySelector('.k-resource-page__body .agents-split')).not.toBeNull()
    expect(page.querySelector('.agents-split-config agents-agent-config')).not.toBeNull()
    expect(page.querySelector('.agents-split-chat agents-agent-chat')).not.toBeNull()

    const cards = [...page.querySelectorAll<HTMLElement>('[data-k-resource-section-card]')]
    expect(cards.length).toBeGreaterThanOrEqual(7)
    for (const card of cards) {
      const labelledBy = card.getAttribute('aria-labelledby')
      expect(labelledBy).toBeTruthy()
      expect(card.querySelector(`h2#${labelledBy}.k-resource-section-card__title`)).not.toBeNull()
      expect(card.querySelector('.k-resource-section-card__body')).not.toBeNull()
    }
  })

  it('shows a read failure instead of claiming the agent is absent', async () => {
    const listAgents = vi.fn().mockRejectedValue(new Error('provider unavailable'))
    const api = stubApi({ listAgents })
    const store = makeStore(api)
    store.agents.loaded = true
    store.agents.error = 'provider unavailable'

    const el = await mount<AgentDetail>('agents-agent-detail', { store, api, name: 'scout', tab: 'config' })
    const error = el.querySelector<HTMLElement>('.k-resource-page__read-error')!

    expect(error).not.toBeNull()
    expect(error.getAttribute('role')).toBe('alert')
    expect(text(error)).toContain('Could not load this agent')
    expect(text(el)).not.toContain('No agent named')

    error.querySelector<HTMLButtonElement>('.k-resource-page__retry')!.click()
    await settle(el, 2)
    expect(listAgents).toHaveBeenCalledTimes(1)
  })

  it('keeps a refresh failure visible when the last snapshot did not contain the agent', async () => {
    const api = stubApi()
    const store = makeStore(api)
    store.agents.loaded = true
    store.agents.hasSnapshot = true
    store.agents.error = 'refresh timed out'

    const el = await mount<AgentDetail>('agents-agent-detail', { store, api, name: 'scout', tab: 'config' })

    expect(text(el.querySelector('.k-resource-page__stale'))).toContain('Could not refresh the agent list')
    expect(text(el.querySelector('.agents-state-empty'))).toContain('last loaded workspace snapshot')
  })
})
