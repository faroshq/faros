// "Set up self-hosted search with an agent" — the assisted path through the
// three-step websearch/searxng dance.
//
// It is a convenience sitting next to the normal create form, never a
// replacement: it renders at all only when (a) capabilities say infrastructure
// federates into this workspace's tool surface, so an agent really can call
// provision, and (b) there is at least one agent to drive it. Either missing
// and this component is invisible — including when the capability probe itself
// failed, because a broken assisted flow is worse than no assisted flow.
//
// What it does: create the Connection (with a client-side token, and NO baseURL
// — the instance does not exist yet), then hand the agent a prompt and drop the
// user into its chat to watch the work happen. There is no separate progress
// surface on purpose: the chat already streams tool calls.

import { html, nothing, type TemplateResult } from 'lit'
import { state } from 'lit/decorators.js'
import { StoreElement } from '../ui/base'
import { icon } from '../ui/icon'
import { mutate } from '../mutate'
import { DNS_LABEL_RE, INSTANCE_SIZES, generateAccessToken, searxngSetupPrompt, type InstanceSize } from '../assisted-setup'
import type { ConnectionWrite } from '../types'

export class AssistedSearch extends StoreElement {
  @state() private open = false
  @state() private agent = ''
  @state() private connName = 'search'
  @state() private instance = ''
  @state() private size: InstanceSize = 'small'
  @state() private errors: Record<string, string> = {}
  @state() private busy = false

  // instanceTouched tracks whether the user has edited the instance name. Until
  // they do it mirrors the connection name, which is the default the two share.
  private instanceTouched = false

  private start(): void {
    this.open = true
    this.agent = this.store.agents.data[0]?.metadata.name || ''
    this.connName = 'search'
    this.instance = ''
    this.instanceTouched = false
    this.size = 'small'
    this.errors = {}
  }

  private cancel(): void {
    this.open = false
  }

  private instanceName(): string {
    return (this.instanceTouched ? this.instance : this.connName).trim()
  }

  private validate(conn: string, inst: string): Record<string, string> {
    const errors: Record<string, string> = {}
    if (!conn) errors.connName = 'A name is required.'
    else if (!DNS_LABEL_RE.test(conn)) errors.connName = 'Lowercase letters, digits and dashes only.'
    else if (this.store.connections.data.some((c) => c.metadata.name === conn))
      errors.connName = 'A connection with that name already exists.'
    if (!inst) errors.instance = 'A name is required.'
    else if (!DNS_LABEL_RE.test(inst)) errors.instance = 'Lowercase letters, digits and dashes only.'
    if (!this.agent) errors.agent = 'Pick the agent that should do this.'
    return errors
  }

  private async submit(e: Event): Promise<void> {
    e.preventDefault()
    if (this.busy) return
    const conn = this.connName.trim()
    const inst = this.instanceName()
    const errors = this.validate(conn, inst)
    this.errors = errors
    if (Object.keys(errors).length) return

    // Ordering is deliberate and matches the manual flow: the Connection (and
    // therefore its Secret) must exist before the instance is provisioned,
    // because the instance gates on the token stored in that Secret. baseURL is
    // left empty — there is no URL to set until the agent reports status.url.
    const body: ConnectionWrite = {
      type: 'websearch',
      name: conn,
      secret: generateAccessToken(),
      config: { provider: 'searxng' },
    }
    this.busy = true
    const res = await mutate(this.store, {
      run: () => this.api.createConnection(body),
      success: `Connection “${conn}” created — handing the rest to ${this.agent}.`,
      failure: 'Create failed',
      reload: ['connections'],
    })
    this.busy = false
    if (!res) return

    this.store.setPendingPrompt(this.agent, searxngSetupPrompt({ connection: conn, instance: inst, size: this.size }))
    this.open = false
    // The chat playground lives in the agent's Config pane (right-hand split),
    // so that is where "go to its chat" lands.
    this.navigate({ kind: 'agent', name: this.agent, tab: 'config' })
  }

  render(): TemplateResult {
    // Both gates matter: no infrastructure tools means the agent cannot
    // provision anything, no agents means there is nobody to ask.
    if (!this.store.hasProvider('infrastructure') || !this.store.agents.data.length) return html`${nothing}`
    return html`${this.card()}${this.open ? this.dialog() : nothing}`
  }

  private card(): TemplateResult {
    return html`<div class="agents-assist">
      <span class="agents-assist-ic">${icon('sparkles')}</span>
      <div class="agents-assist-body">
        <strong>Set up self-hosted search with an agent</strong>
        <span class="muted"
          >One of your agents can provision the SearXNG instance for you and report its URL — instead of you hopping to Infrastructure
          and back.</span
        >
      </div>
      <button type="button" class="secondary" @click=${() => this.start()}>Set it up</button>
    </div>`
  }

  private dialog(): TemplateResult {
    const agents = this.store.agents.data
    return html`<div
      class="agents-overlay"
      @click=${(e: Event) => e.target === e.currentTarget && this.cancel()}
      @keydown=${(e: KeyboardEvent) => e.key === 'Escape' && this.cancel()}
    >
      <form class="agents-dialog" role="dialog" aria-modal="true" aria-label="Assisted search setup" @submit=${(e: Event) => void this.submit(e)}>
        <header class="agents-dialog-head">
          <span class="agents-dialog-ic">${icon('sparkles')}</span>
          <h3>Self-hosted search, set up for you</h3>
        </header>
        <p class="muted">
          We create the web-search connection with a strong token, then your agent provisions the <code>searxng</code> instance and
          tells you the URL to paste back in.
        </p>

        ${agents.length > 1
          ? html`<label>
              Agent
              <select aria-invalid=${this.errors.agent ? 'true' : nothing} @change=${(e: Event) => (this.agent = (e.target as HTMLSelectElement).value)}>
                ${agents.map((a) => html`<option value=${a.metadata.name} ?selected=${a.metadata.name === this.agent}>${a.spec?.displayName || a.metadata.name}</option>`)}
              </select>
              ${this.errors.agent ? html`<span class="agents-fielderr">${this.errors.agent}</span>` : nothing}
            </label>`
          : html`<p class="agents-hint">Driven by your agent <strong>${this.agent}</strong>.</p>`}

        <label>
          Connection name
          <input
            name="connName"
            .value=${this.connName}
            autocomplete="off"
            aria-invalid=${this.errors.connName ? 'true' : nothing}
            @input=${(e: Event) => (this.connName = (e.target as HTMLInputElement).value)}
          />
          ${this.errors.connName
            ? html`<span class="agents-fielderr">${this.errors.connName}</span>`
            : html`<span class="agents-hint">What agents reference to search the web.</span>`}
        </label>

        <label>
          Instance name
          <input
            name="instance"
            .value=${this.instanceName()}
            autocomplete="off"
            aria-invalid=${this.errors.instance ? 'true' : nothing}
            @input=${(e: Event) => {
              this.instanceTouched = true
              this.instance = (e.target as HTMLInputElement).value
            }}
          />
          ${this.errors.instance
            ? html`<span class="agents-fielderr">${this.errors.instance}</span>`
            : html`<span class="agents-hint">The infrastructure instance the agent provisions.</span>`}
        </label>

        <label>
          Size
          <div class="agents-modeseg" role="group" aria-label="Instance size">
            ${INSTANCE_SIZES.map(
              (s) => html`<button type="button" class="agents-modebtn ${s === this.size ? 'sel' : ''}" @click=${() => (this.size = s)}>${s}</button>`,
            )}
          </div>
        </label>

        <div class="agents-form-actions">
          <button type="submit" ?disabled=${this.busy}>${icon('sparkles')} Create and hand off</button>
          <button type="button" class="secondary" @click=${() => this.cancel()}>Cancel</button>
        </div>
      </form>
    </div>`
  }
}

if (!customElements.get('agents-assisted-search')) customElements.define('agents-assisted-search', AssistedSearch)
