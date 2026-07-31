// Assisted setup: capability gating of the affordance, the token the flow
// mints, the prompt it composes, and the one-shot handoff into chat.

import { describe, expect, it, vi } from 'vitest'
import { DNS_LABEL_RE, connectionSecretName, generateAccessToken, searxngSetupPrompt } from '../assisted-setup'
import type { AgentChat } from '../views/agent-chat'
import type { Connections } from '../views/connections'
import type { Capabilities, Connection, Route } from '../types'
import '../views/connections'
import '../views/agent-chat'
import { agentFixture, makeStore, mount, settle, stubApi, text } from './helpers'

function connFixture(name: string, spec: Partial<Connection['spec']> = {}): Connection {
  return { metadata: { name }, spec: { type: 'websearch', ...spec } }
}

async function mountConnections(caps: Capabilities, agents = [agentFixture('scout')], extra: Record<string, unknown> = {}) {
  const api = stubApi(extra)
  const store = makeStore(api)
  store.agents.data = agents
  store.agents.loaded = true
  store.capabilities.data = caps
  store.capabilities.loaded = true
  const el = await mount<Connections>('agents-connections', { store, api })
  return { el, store, api }
}

describe('capability gating', () => {
  it('offers assisted setup when infrastructure federates and an agent exists', async () => {
    const { el } = await mountConnections({ providers: ['infrastructure', 'code'] })
    expect(el.querySelector('.agents-assist')).not.toBeNull()
    expect(text(el.querySelector('.agents-assist'))).toContain('Set up self-hosted search with an agent')
  })

  it('hides it when infrastructure is not among the enabled providers', async () => {
    const { el } = await mountConnections({ providers: ['code', 'kuery'] })
    expect(el.querySelector('.agents-assist')).toBeNull()
  })

  it('hides it silently when the capability probe could not be made', async () => {
    // unavailable means "we could not tell" — infrastructure may well be
    // enabled, but offering a flow we can't stand behind is worse than not
    // offering it. Nothing alarming is rendered either.
    const { el } = await mountConnections({ providers: [], unavailable: true, message: 'could not reach the tool endpoint' })
    expect(el.querySelector('.agents-assist')).toBeNull()
    expect(text(el)).not.toContain('could not reach')
  })

  it('hides it when there is no agent to drive the flow', async () => {
    const { el } = await mountConnections({ providers: ['infrastructure'] }, [])
    expect(el.querySelector('.agents-assist')).toBeNull()
  })

  it('treats a failed capability fetch as unavailable rather than an error', async () => {
    const store = makeStore(stubApi({ capabilities: () => Promise.reject(new Error('boom')) }))
    await store.loadCapabilities()
    expect(store.capabilities.error).toBeNull()
    expect(store.capabilities.data.unavailable).toBe(true)
    expect(store.hasProvider('infrastructure')).toBe(false)
  })
})

describe('generated access token', () => {
  it('is long and strictly alphanumeric', () => {
    // The instance's nginx gate compares the token inside a string literal, so
    // quotes, backslashes and $ must never appear.
    for (let i = 0; i < 20; i++) {
      const t = generateAccessToken()
      expect(t.length).toBeGreaterThanOrEqual(32)
      expect(t).toMatch(/^[A-Za-z0-9]+$/)
    }
  })

  it('does not repeat itself', () => {
    const seen = new Set(Array.from({ length: 50 }, () => generateAccessToken()))
    expect(seen.size).toBe(50)
  })

  it('honours an explicit length', () => {
    expect(generateAccessToken(64)).toHaveLength(64)
  })
})

describe('composed prompt', () => {
  const prompt = searxngSetupPrompt({ connection: 'search', instance: 'searxng-1', size: 'medium' })

  it('points tokenSecretRef at the connection secret the backend writes', () => {
    expect(connectionSecretName('search')).toBe('kedge-agents-conn-search')
    expect(prompt).toContain('tokenSecretRef: `kedge-agents-conn-search`')
  })

  it('carries the instance name and size, and demands the real schema first', () => {
    expect(prompt).toContain('name: `searxng-1`')
    expect(prompt).toContain('size: `medium`')
    expect(prompt).toContain('describe_template')
    expect(prompt).toContain('get_instance')
    expect(prompt).toContain('status.url')
  })

  it('forbids inventing the token and asking questions', () => {
    expect(prompt).toContain('Do NOT generate a token')
    expect(prompt).toContain('without asking me any questions')
  })
})

describe('assisted setup flow', () => {
  it('creates the connection with no baseURL, hands off the prompt and navigates to the agent', async () => {
    const createConnection = vi.fn().mockResolvedValue({ metadata: { name: 'search' }, spec: { type: 'websearch' } })
    const { el, store } = await mountConnections({ providers: ['infrastructure'] }, [agentFixture('scout')], { createConnection })
    const routes: Route[] = []
    document.addEventListener('agents-navigate', (e) => routes.push((e as CustomEvent<Route>).detail))

    el.querySelector<HTMLButtonElement>('.agents-assist button')!.click()
    await settle(el, 4)
    const dialog = el.querySelector<HTMLFormElement>('.agents-dialog')!
    // A single agent means no picker at all.
    expect(dialog.querySelector('select')).toBeNull()
    expect(dialog.querySelector<HTMLInputElement>('input[name=connName]')!.value).toBe('search')
    // The instance name defaults to the connection name.
    expect(dialog.querySelector<HTMLInputElement>('input[name=instance]')!.value).toBe('search')

    dialog.dispatchEvent(new Event('submit', { cancelable: true }))
    await settle(el, 6)

    expect(createConnection).toHaveBeenCalledTimes(1)
    const body = createConnection.mock.calls[0][0]
    expect(body).toMatchObject({ type: 'websearch', name: 'search', config: { provider: 'searxng' } })
    // Deliberate: the instance does not exist yet, so there is no URL to set.
    expect(body.baseURL).toBeUndefined()
    expect(body.secret).toMatch(/^[A-Za-z0-9]{32,}$/)

    expect(routes).toEqual([{ kind: 'agent', name: 'scout', tab: 'config' }])
    const handed = store.takePendingPrompt('scout')
    expect(handed).toContain('tokenSecretRef: `kedge-agents-conn-search`')
  })

  it('rejects names that are not DNS labels before touching the API', async () => {
    const createConnection = vi.fn()
    const { el } = await mountConnections({ providers: ['infrastructure'] }, [agentFixture('scout')], { createConnection })
    el.querySelector<HTMLButtonElement>('.agents-assist button')!.click()
    await settle(el, 4)

    const input = el.querySelector<HTMLInputElement>('input[name=connName]')!
    input.value = 'Search Me'
    input.dispatchEvent(new Event('input'))
    el.querySelector<HTMLFormElement>('.agents-dialog')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await settle(el, 4)

    expect(createConnection).not.toHaveBeenCalled()
    expect(text(el.querySelector('.agents-fielderr'))).toContain('Lowercase letters')
    expect(DNS_LABEL_RE.test('search')).toBe(true)
    expect(DNS_LABEL_RE.test('-search')).toBe(false)
  })
})

describe('connection needing an instance URL', () => {
  it('flags a searxng connection with no baseURL, and not one that has it', async () => {
    const { el } = await mountConnections({ providers: [] }, [], {
      listConnections: () =>
        Promise.resolve([
          connFixture('search', { config: { provider: 'searxng' } }),
          connFixture('wired', { config: { provider: 'searxng' }, baseURL: 'https://searxng.example.com' }),
          connFixture('brave', { config: { provider: 'brave' } }),
        ]),
    })
    await el.store.load('connections')
    await settle(el, 4)

    const rows = [...el.querySelectorAll('tbody tr')]
    expect(rows).toHaveLength(3)
    expect(text(rows[0])).toContain('needs an instance URL')
    expect(text(rows[1])).toContain('searxng.example.com')
    expect(text(rows[2])).not.toContain('needs an instance URL')
  })
})

describe('auto-send on arrival', () => {
  async function mountChatWithHandoff(prompt: string | null) {
    const chatStream = vi.fn(async function* () {
      yield { event: 'start', data: { runID: 'r1', sessionID: 's1' } }
      yield { event: 'done', data: { runID: 'r1', content: 'on it' } }
    })
    const api = stubApi({ chatStream })
    const store = makeStore(api)
    store.agents.data = [agentFixture('scout')]
    store.agents.loaded = true
    if (prompt) store.setPendingPrompt('scout', prompt)
    const el = await mount<AgentChat>('agents-agent-chat', { store, api, name: 'scout' })
    await settle(el, 6)
    return { el, store, chatStream }
  }

  it('sends the handed-off prompt once and never again', async () => {
    const { el, chatStream } = await mountChatWithHandoff('provision searxng please')
    expect(chatStream).toHaveBeenCalledTimes(1)
    expect(chatStream.mock.calls[0][1]).toBe('provision searxng please')

    // Re-render, a tab switch back (same element re-entering an update) and a
    // name re-assignment must not resend.
    el.requestUpdate()
    await settle(el, 4)
    el.name = 'scout'
    await settle(el, 4)
    expect(chatStream).toHaveBeenCalledTimes(1)

    // A second chat element for the same agent finds the handoff consumed.
    const twin = await mount<AgentChat>('agents-agent-chat', { store: el.store, api: el.api, name: 'scout' })
    await settle(twin, 4)
    expect(chatStream).toHaveBeenCalledTimes(1)
  })

  it('sends nothing when there is no handoff', async () => {
    const { chatStream } = await mountChatWithHandoff(null)
    expect(chatStream).not.toHaveBeenCalled()
  })

  it('only claims a prompt addressed to this agent', async () => {
    const store = makeStore(stubApi())
    store.setPendingPrompt('scout', 'hello')
    expect(store.takePendingPrompt('other')).toBeNull()
    expect(store.takePendingPrompt('scout')).toBe('hello')
    expect(store.takePendingPrompt('scout')).toBeNull()
  })
})
