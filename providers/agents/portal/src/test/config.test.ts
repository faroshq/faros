// Agent config: the pointer-patch semantics each section relies on, and the
// fields that only recently became writable (description, limits).

import { describe, expect, it, vi } from 'vitest'
import type { AgentConfig } from '../views/agent-config'
import type { AgentPatch } from '../types'
import '../views/agent-config'
import { familiesForConns } from '../conn-defs'
import { presetByID } from '../presets'
import { clearToasts, subscribeToasts } from '../ui/toast'
import { agentFixture, makeStore, mount, settle, stubApi } from './helpers'

async function mountConfig(spec: Record<string, unknown> = {}) {
  const patchAgent = vi.fn().mockImplementation((_n: string, body: AgentPatch) => Promise.resolve({ metadata: { name: 'scout' }, spec: body }))
  const api = stubApi({ patchAgent })
  const store = makeStore(api)
  store.agents.data = [agentFixture('scout', spec)]
  store.agents.loaded = true
  const el = await mount<AgentConfig>('agents-agent-config', { store, api, name: 'scout' })
  return { el, patchAgent }
}

function sectionButton(el: AgentConfig, label: string): HTMLButtonElement {
  return [...el.querySelectorAll('button')].find((b) => b.textContent?.includes(label)) as HTMLButtonElement
}

describe('agent config', () => {
  it('saves an editable description with the persona section', async () => {
    const { el, patchAgent } = await mountConfig({ description: 'watches the deploy queue' })
    const inputs = [...el.querySelectorAll<HTMLInputElement>('input')]
    const desc = inputs.find((i) => i.value === 'watches the deploy queue')!
    expect(desc).toBeDefined() // editable input, not a read-only block

    desc.value = 'now watches everything'
    desc.dispatchEvent(new Event('input'))
    await settle(el)
    sectionButton(el, 'Save persona').click()
    await settle(el, 4)

    expect(patchAgent).toHaveBeenCalledWith('scout', expect.objectContaining({ description: 'now watches everything' }))
    // The persona patch must not carry keys another section owns.
    const patch = patchAgent.mock.calls[0][1] as AgentPatch
    expect(patch.autonomy).toBeUndefined()
    expect(patch.channels).toBeUndefined()
  })

  it('hydrates and saves maxToolTurns / timeoutSeconds alongside the budget', async () => {
    const { el, patchAgent } = await mountConfig({ limits: { maxToolTurns: 12, timeoutSeconds: 600 } })
    const inputs = [...el.querySelectorAll<HTMLInputElement>('input')]
    const turns = inputs.find((i) => i.value === '12')!
    const timeout = inputs.find((i) => i.value === '600')!
    expect(turns).toBeDefined()
    expect(timeout).toBeDefined()

    timeout.value = '900'
    timeout.dispatchEvent(new Event('input'))
    await settle(el)
    sectionButton(el, 'Save policy').click()
    await settle(el, 4)

    expect(patchAgent).toHaveBeenCalledWith(
      'scout',
      expect.objectContaining({ maxToolTurns: 12, timeoutSeconds: 900, autonomy: 'ask' }),
    )
  })

  it('sends 0 for a blank limit, which the backend reads as the provider default', async () => {
    const { el, patchAgent } = await mountConfig()
    sectionButton(el, 'Save policy').click()
    await settle(el, 4)
    expect(patchAgent.mock.calls[0][1]).toMatchObject({ maxToolTurns: 0, timeoutSeconds: 0, budgetTokens: 0 })
  })

  it('rolls the optimistic edit back when the save fails', async () => {
    const patchAgent = vi.fn().mockRejectedValue(new Error('409 conflict'))
    // The post-failure resync is made to fail too, so the restored value can
    // only have come from the rollback and not from a refetch.
    const api = stubApi({ patchAgent, listAgents: () => Promise.reject(new Error('unavailable')) })
    const store = makeStore(api)
    store.agents.data = [agentFixture('scout', { description: 'original' })]
    store.agents.loaded = true
    const el = await mount<AgentConfig>('agents-agent-config', { store, api, name: 'scout' })

    const desc = [...el.querySelectorAll<HTMLInputElement>('input')].find((i) => i.value === 'original')!
    desc.value = 'changed'
    desc.dispatchEvent(new Event('input'))
    await settle(el)
    sectionButton(el, 'Save persona').click()
    await settle(el, 4)

    expect(store.agent('scout')?.spec.description).toBe('original')
  })
})

describe('connection test feedback', () => {
  it('surfaces the reason from a failed channel test (HTTP error convention)', async () => {
    const testConnection = vi.fn().mockRejectedValue(new Error('telegram: 403 bot was blocked by the user'))
    const api = stubApi({ testConnection })
    const store = makeStore(api)
    store.agents.data = [agentFixture('scout', { channels: [{ name: 'primary', connectionRef: 'tg', primary: true }] })]
    store.connections.data = [{ metadata: { name: 'tg' }, spec: { type: 'telegram', channel: '123' } }]
    store.agents.loaded = true
    const el = await mount<AgentConfig>('agents-agent-config', { store, api, name: 'scout' })

    // The toast host is rendered by the shell, so read the bus directly.
    clearToasts()
    const raised: string[] = []
    const off = subscribeToasts((ts) => raised.push(...ts.map((t) => `${t.kind}:${t.message}`)))

    const testBtn = [...el.querySelectorAll('.agents-inbound-actions button')].find((b) => b.textContent?.includes('Test')) as HTMLButtonElement
    testBtn.click()
    await settle(el, 4)
    off()

    expect(testConnection).toHaveBeenCalledWith('tg')
    const failure = raised.find((m) => m.startsWith('error:'))
    expect(failure).toContain('Test of “tg” failed')
    expect(failure).toContain('blocked by the user')
  })
})

// Research fan-out is a capability of the agent, not a wired connection. That
// makes it the one family a user picks directly — and the one that the
// derive-families-from-connections rule would silently drop.
describe('research fan-out grant', () => {
  const spawnRow = (el: AgentConfig): HTMLInputElement[] => {
    const fs = [...el.querySelectorAll('fieldset')].find((f) => f.textContent?.includes('Research fan-out'))!
    return [...fs.querySelectorAll<HTMLInputElement>('input[type=checkbox]')]
  }

  it('grants and revokes spawn for interactive runs', async () => {
    const { el, patchAgent } = await mountConfig({ tools: { interactive: { families: ['core', 'web'] } } })
    const [linked] = spawnRow(el)
    expect(linked.checked).toBe(false)

    linked.checked = true
    linked.dispatchEvent(new Event('change'))
    await settle(el, 4)
    expect(patchAgent.mock.calls[0][1].interactiveFamilies).toEqual(expect.arrayContaining(['core', 'web', 'spawn']))
  })

  it('turning it off also clears the background grant', async () => {
    const { el, patchAgent } = await mountConfig({
      tools: { interactive: { families: ['core', 'spawn'] }, background: { families: ['core', 'spawn'] } },
    })
    const [linked] = spawnRow(el)
    expect(linked.checked).toBe(true)

    linked.checked = false
    linked.dispatchEvent(new Event('change'))
    await settle(el, 4)
    const patch = patchAgent.mock.calls[0][1]
    expect(patch.interactiveFamilies).not.toContain('spawn')
    expect(patch.backgroundFamilies).not.toContain('spawn')
  })

  // The trap: familiesForConns rebuilds the list from scratch on every tool
  // grant, so without carrying spawn over, wiring a tool would switch fan-out
  // off behind the user's back.
  it('survives a rebuild of families from connections', () => {
    const connType = (n: string) => (n === 'gh' ? 'github' : undefined)
    const rebuilt = familiesForConns(['gh'], connType, ['core', 'spawn'])
    expect(rebuilt).toContain('spawn')
    expect(rebuilt).toContain('github')
    expect(rebuilt).toContain('core')
  })

  it('does not invent spawn when it was never granted', () => {
    expect(familiesForConns(['gh'], () => 'github', ['core'])).not.toContain('spawn')
    expect(familiesForConns([], () => undefined)).toEqual(['core'])
  })
})

// The preset is the front door to research fan-out: if it grants the wrong
// families or discards a prompt the user typed, it is worse than no preset.
describe('agent presets', () => {
  it('research grants web + spawn and supplies the persona', () => {
    const body = presetByID('research').apply({ name: 'r', modelCredential: 'main' })
    expect(body.interactiveFamilies).toEqual(['core', 'web', 'spawn'])
    expect(body.systemPrompt).toContain('spawn one worker per sub-question')
    expect(body.systemPrompt).toContain('join ONCE')
  })

  it('never discards a prompt the user typed', () => {
    const body = presetByID('research').apply({ name: 'r', systemPrompt: 'You are mine.' })
    expect(body.systemPrompt).toBe('You are mine.')
    // The grant still applies — the tools are the part the user cannot guess.
    expect(body.interactiveFamilies).toContain('spawn')
  })

  it('blank changes nothing', () => {
    const input = { name: 'r', modelCredential: 'main' }
    expect(presetByID('blank').apply({ ...input })).toEqual(input)
  })

  it('an unknown id falls back to blank rather than throwing', () => {
    expect(presetByID('nope').id).toBe('blank')
  })
})
