// Connections tab: shared credentials for external systems. Each is a Tool
// agents call, a Channel they message you on, or a generic Connection.
// Type-driven create (CONN_DEFS carries the per-type setup guides), edit, test /
// enable-inbound / OAuth-connect actions. Toolsets live here as a section —
// both are "reusable capability config".

import { html, nothing, type TemplateResult } from 'lit'
import { property, state } from 'lit/decorators.js'
import { unsafeHTML } from 'lit/directives/unsafe-html.js'
import { StoreElement } from '../ui/base'
import { icon } from '../ui/icon'
import {
  createResourceTableState,
  resourceTable,
  resourceTableAction,
  type ResourceTableState,
} from '../ui/resource-table'
import { errorState, loadingState, sliceView, staleState } from '../ui/states'
import { createGuidance, firstRunGuide } from '../ui/create-flow'
import { toast } from '../ui/toast'
import { confirmModal } from '../portalkit/modal'
import { mutate } from '../mutate'
import type { CreateSuccessDetail, EditCancelDetail, EditSuccessDetail } from '../router'
import {
  CATEGORY_META,
  CONN_DEFS,
  connCategory,
  connShape,
  type ConnCategory,
  type ConnField,
  type ConnTypeDef,
} from '../conn-defs'
import type { Agent, Connection, ConnectionWrite, Toolset } from '../types'

import './toolsets'
import './assisted-search'

// needsInstance flags a self-hosted search connection that names no instance.
// The websearch tool errors at runtime in that state and nothing else in the
// row shows it, so the table says so explicitly.
function needsInstance(c: Connection): boolean {
  return selfHostedSearch(c) && !c.spec.config?.instance
}

// selfHostedSearch is the websearch mode with no credential and no URL: it
// names an infrastructure instance and is reached over the data plane.
function selfHostedSearch(c: Connection): boolean {
  return c.spec.type === 'websearch' && c.spec.config?.provider === 'searxng'
}

// instanceBacked covers every connection addressed by instance name rather than
// by URL — self-hosted search and any MCP workload in this workspace. Those
// edit an instance name; everything else edits an endpoint and a secret.
function instanceBacked(c: Connection): boolean {
  return selfHostedSearch(c) || (c.spec.type === 'mcp' && !!c.spec.config?.instance)
}

// unwired flags a tool connection no agent has been granted. A tool family only
// exists for an agent that was wired the connection backing it, so an unwired
// search connection means every agent still answers "I can't make web
// requests" — a failure that is otherwise completely invisible from here.
function wiringState(
  c: Connection,
  agents: Agent[],
  toolsets: Toolset[],
  agentsAuthoritative: boolean,
  toolsetsAuthoritative: boolean,
): 'wired' | 'unwired' | 'unknown' | null {
  if (connCategory(c.spec.type) !== 'tool') return null
  if (!agentsAuthoritative) return 'unknown'
  const name = c.metadata.name
  const direct = agents.some((a) => {
    const t = a.spec?.tools
    return (
      (t?.interactive?.connections || []).includes(name) ||
      (t?.background?.connections || []).includes(name)
    )
  })
  if (direct) return 'wired'
  const grantedToolsets = new Set(agents.flatMap((a) => [
    ...(a.spec?.tools?.interactive?.toolsets || []),
    ...(a.spec?.tools?.background?.toolsets || []),
  ]))
  if (grantedToolsets.size === 0) return 'unwired'
  if (!toolsetsAuthoritative) return 'unknown'
  return toolsets.some((toolset) => grantedToolsets.has(toolset.metadata.name) && (toolset.spec.connections || []).includes(name))
    ? 'wired'
    : 'unwired'
}

export class Connections extends StoreElement {
  // The collection and create surfaces share the type definitions, but only
  // the route-owned instance renders a consequential create flow. Keeping the
  // property explicit also leaves standalone consumers on the compact picker
  // behaviour until they opt into hash-owned navigation.
  @property({ type: Boolean }) routeOwned = false
  @property({ type: Boolean }) createRoute = false
  @property({ type: String }) createType = ''
  @property({ type: Boolean }) editRoute = false
  @property({ type: String }) editName = ''
  @state() private connType: string | null = null
  @state() private connMode = ''
  @state() private editing: string | null = null
  @state() private createBusy = false
  @state() private editBusy = false
  @state() private createValues: Record<string, string> = {}
  @state() private editValues: Record<string, string> = {}
  @state() private connectionTable = createResourceTableState()
  private editValuesFor = ''
  private focusedEditFor = ''

  protected willUpdate(changed?: Map<PropertyKey, unknown>): void {
    super.willUpdate()
    if (changed?.has('createType') && changed.get('createType') !== undefined) {
      this.createValues = {}
      this.connMode = ''
    }
    if (this.editRoute && this.editName && this.editValuesFor !== this.editName) {
      const connection = this.store.connections.data.find((item) => item.metadata.name === this.editName)
      if (connection) this.hydrateEdit(connection)
    }
  }

  protected updated(): void {
    if (!this.editRoute) {
      this.focusedEditFor = ''
      return
    }
    const input = this.querySelector<HTMLInputElement>('.agents-conn-form input:not([disabled])')
    if (input && this.focusedEditFor !== this.editName) {
      this.focusedEditFor = this.editName
      input.focus()
      return
    }
    const editReadSettled = Boolean(
      this.store.connections.error || this.store.connections.loaded || this.store.connections.hasSnapshot,
    )
    if (!input && editReadSettled && document.activeElement === document.body) {
      this.querySelector<HTMLElement>('[data-edit-heading]')?.focus()
    }
  }

  private async create(def: ConnTypeDef, form: HTMLFormElement): Promise<void> {
    if (this.createBusy) return
    const v: Record<string, string> = {}
    form.querySelectorAll<HTMLInputElement>('input[name]').forEach((el) => (v[el.name] = el.value.trim()))
    const mode = this.connMode || def.modes?.[0].id || ''
    const body = def.build(v, mode)
    this.createBusy = true
    let res: Connection | undefined
    try {
      res = await mutate(this.store, {
        run: () => this.api.createConnection(body),
        success: 'Connection created.',
        failure: 'Create failed',
        reload: ['connections'],
      })
    } finally {
      this.createBusy = false
    }
    if (res && this.routeOwned) {
      this.dispatchEvent(
        new CustomEvent<CreateSuccessDetail>('agents-create-success', {
          detail: { resource: 'connection', name: body.name, item: res },
          bubbles: true,
          composed: true,
        }),
      )
    } else if (res) {
      this.connType = null
      this.connMode = ''
    }
  }

  private async saveEdit(c: Connection, form: HTMLFormElement, usesChannel: boolean): Promise<void> {
    if (this.editBusy) return
    const g = (n: string): string => (form.querySelector<HTMLInputElement>(`[name=${n}]`)?.value || '').trim()
    this.editValues = {
      displayName: g('displayName'),
      endpoint: g('endpoint'),
      instance: g('instance'),
      secret: g('secret'),
    }
    const patch: ConnectionWrite = { displayName: g('displayName') }
    // A self-hosted search connection is addressed by instance name, not by
    // URL — it is reached over the platform's internal data plane. config is
    // replaced wholesale by the patch endpoint, so provider must be re-sent.
    if (instanceBacked(c)) patch.config = { ...c.spec.config, instance: g('instance') }
    else if (usesChannel) patch.channel = g('endpoint')
    else patch.baseURL = g('endpoint')
    const secret = g('secret')
    if (secret) patch.secret = secret
    this.editBusy = true
    let res: Connection | undefined
    try {
      res = await mutate(this.store, {
        run: () => this.api.patchConnection(c.metadata.name, patch),
        success: 'Connection updated.',
        failure: 'Update failed',
        reload: ['connections'],
      })
    } finally {
      this.editBusy = false
    }
    if (!res) return
    this.editValuesFor = ''
    this.editValues = {}
    if (this.routeOwned && this.editRoute) {
      this.dispatchEvent(new CustomEvent<EditSuccessDetail>('agents-edit-success', {
        detail: { resource: 'connection', name: c.metadata.name, item: res },
        bubbles: true,
        composed: true,
      }))
    } else {
      this.editing = null
    }
  }

  private async del(name: string): Promise<void> {
    const authority = this.captureAuthority()
    const ok = await confirmModal({ title: `Delete connection “${name}”?`, danger: true, confirmLabel: 'Delete' })
    if (!ok || !this.authorityIsCurrent(authority)) return
    await mutate(authority.store, {
      run: () => authority.api.deleteConnection(name),
      success: 'Connection deleted.',
      failure: 'Delete failed',
      reload: ['connections'],
    })
  }

  // Connection tests signal failure with an HTTP error (502 SendFailed, 400 for
  // a non-messaging type) carrying the reason in the status body, whereas
  // credential tests answer 200 with {ok:false, error} — see models.ts. Both
  // conventions are handled where they are; do not "align" one blindly.
  private async test(name: string): Promise<void> {
    await mutate(this.store, {
      run: () => this.api.testConnection(name),
      success: `Test message sent via ${name}. Check the channel.`,
      failure: `Test of “${name}” failed`,
    })
  }

  private async enableInbound(name: string): Promise<void> {
    const res = await mutate(this.store, {
      run: () => this.api.enableInbound(name),
      failure: 'Enable inbound failed',
      reload: ['connections'],
    })
    if (res) toast(res.registered ? 'ok' : 'info', `${res.note} ${res.webhookURL}`)
  }

  private async oauth(name: string): Promise<void> {
    const res = await mutate(this.store, { run: () => this.api.oauthAuthorize(name), failure: 'OAuth connect failed' })
    if (!res) return
    window.open(res.authorizeURL, '_blank', 'noopener')
    toast('info', 'Authorize in the opened tab, then refresh.')
  }

  render(): TemplateResult {
    if (this.createRoute) return this.createRouteSurface()
    if (this.editRoute) return this.editRouteSurface()
    const connections = this.store.connections
    const showFirstRun = connections.loaded && connections.data.length === 0 && (!connections.error || connections.hasSnapshot)
    return html`
      <div class="agents-menu">
        <div class="agents-panel k-card agents-route-panel">
          <div class="agents-panel-head">
            <h3 tabindex="-1" data-connections-heading>Connections</h3>
            ${this.routeOwned && !showFirstRun
              ? html`<button class="k-btn k-btn--primary" @click=${() => this.navigate({ kind: 'create', resource: 'connection' })}>${icon('plus')} New connection</button>`
              : nothing}
          </div>
          <p class="muted">
            Shared credentials for external systems. Each is a ${icon('wrench')} <strong>Tool</strong> agents call, a
            ${icon('megaphone')} <strong>Channel</strong> they message you on, or a ${icon('plug')} generic
            <strong>Connection</strong>. Stored as Secrets in your workspace.
          </p>
          ${sliceView<Connection>({
            slice: connections,
            emptyIcon: 'plug',
            emptyText: 'No connections yet — add one below.',
            empty: () => firstRunGuide({
              icon: 'plug',
              title: 'Connect tools and channels',
              description: 'Add reusable access to an external system once, then grant it to agents directly or through a toolset.',
              primaryLabel: 'Create connection',
              primary: () => this.navigate({ kind: 'create', resource: 'connection' }),
              steps: [
                { label: 'Connection', description: 'Tool, channel, or external credential' },
                { label: 'Toolset', description: 'Optional reusable capability bundle' },
                { label: 'Agent', description: 'Grant access from agent Config' },
              ],
              currentStep: 0,
              journeyLabel: 'Connection setup path',
            }),
            retry: () => void this.store.load('connections'),
            content: (rows) => this.table(rows),
          })}
          ${this.routeOwned
            ? this.editing
              ? this.editorArea()
              : html`<agents-assisted-search .store=${this.store} .api=${this.api}></agents-assisted-search>`
            : this.editorArea()}
        </div>
        <agents-toolsets .store=${this.store} .api=${this.api} .routeOwned=${this.routeOwned}></agents-toolsets>
      </div>
    `
  }

  private createRouteSurface(): TemplateResult {
    if (!this.createType) {
      return html`<div class="agents-menu agents-create-page k-create-page">
        <button type="button" class="k-btn k-btn--ghost k-back-action" ?disabled=${this.createBusy} @click=${() => this.cancelCreate()}>${icon('arrow-left')} Connections</button>
        <header class="k-create-header"><h1 class="k-create-title">Create connection</h1><p class="k-create-description">Choose the tool, channel, or external service you want agents to use.</p></header>
        <div class="k-create-surface k-create-surface--wide">
          <div class="k-create-body">${this.picker(false)}</div>
        </div>
      </div>`
    }
    if (this.createType === 'assisted-search') {
      return html`<div class="agents-menu agents-create-page">
        <agents-assisted-search .store=${this.store} .api=${this.api} .page=${true}></agents-assisted-search>
      </div>`
    }
    const def = CONN_DEFS.find((d) => d.id === this.createType)
    if (!def) {
      return html`<div class="agents-create-page k-create-page">
        <button type="button" class="k-btn k-btn--ghost k-back-action" ?disabled=${this.createBusy} @click=${() => this.cancelCreate()}>${icon('arrow-left')} Connections</button>
        <header class="k-create-header"><h1 class="k-create-title">Connection type unavailable</h1><p class="k-create-description">That connection type is not available in this version of the Agents provider.</p></header>
      </div>`
    }
    return html`<div class="agents-menu agents-create-page k-create-page">
      <button type="button" class="k-btn k-btn--ghost k-back-action" ?disabled=${this.createBusy} @click=${() => this.cancelCreate()}>${icon('arrow-left')} Connections</button>
      <header class="k-create-header"><h1 class="k-create-title">Connect ${def.label}</h1><p class="k-create-description">${def.desc}</p></header>
      ${this.createForm(def)}
    </div>`
  }

  private table(rows: Connection[]): TemplateResult {
    const endpoint = (c: Connection): string => c.spec.config?.instance || c.spec.baseURL || c.spec.channel || ''
    return resourceTable({
      ariaLabel: 'Connections',
      rows,
      rowKey: (c) => c.metadata.name,
      state: this.connectionTable,
      onStateChange: (next: ResourceTableState) => (this.connectionTable = next),
      searchPlaceholder: 'Search connections…',
      searchText: (c) => [
        c.metadata.name,
        c.spec.displayName,
        c.spec.type,
        connShape(c).typeLabel,
        endpoint(c),
      ].filter(Boolean).join(' '),
      filters: [
        {
          key: 'kind',
          label: 'Kind',
          allLabel: 'All kinds',
          value: (c) => connCategory(c.spec.type),
          labelFor: (value) => CATEGORY_META[value as ConnCategory]?.label || value,
        },
        {
          key: 'type',
          label: 'Type',
          allLabel: 'All types',
          value: (c) => connShape(c).typeLabel,
        },
      ],
      columns: [
        {
          key: 'name',
          label: 'Name',
          primary: true,
          render: (c) => html`<span class="agents-resource-name" title=${c.metadata.name}>${c.spec.displayName || c.metadata.name}</span>
            ${c.spec.displayName ? html`<code class="agents-resource-id">${c.metadata.name}</code>` : nothing}
            ${c.status?.webhookPath ? html`<span class="agents-inbound-on" aria-label="Inbound enabled" data-k-tip="Inbound enabled">${icon('swap')}</span>` : nothing}
            ${c.status?.oauthConnected ? html`<span class="agents-inbound-on" aria-label="OAuth connected" data-k-tip="OAuth connected">${icon('link')}</span>` : nothing}`,
        },
        {
          key: 'kind',
          label: 'Kind',
          render: (c) => {
            const cat = connCategory(c.spec.type)
            const meta = CATEGORY_META[cat]
            return html`<span class="k-badge agents-badge agents-badge-cat agents-cat-${cat}">${icon(meta.icon)} ${meta.label}</span>`
          },
        },
        {
          key: 'type',
          label: 'Type',
          render: (c) => html`<span class="k-badge agents-badge">${connShape(c).typeLabel}</span>`,
        },
        {
          key: 'endpoint',
          label: 'Endpoint / channel',
          render: (c) => {
            const wiring = wiringState(
              c,
              this.store.agents.data,
              this.store.toolsets.data,
              this.store.agents.hasSnapshot && !this.store.agents.error,
              this.store.toolsets.hasSnapshot && !this.store.toolsets.error,
            )
            return html`<span class="agents-resource-endpoint" title=${endpoint(c)}>${endpoint(c) || '—'}</span>
              ${needsInstance(c)
                ? html`<span class="k-badge agents-badge k-badge--warning agents-badge-warn" title="Edit this connection and name the searxng instance it should search through">needs an instance</span>`
                : nothing}
              ${wiring === 'unwired'
                ? html`<span class="k-badge agents-badge k-badge--warning agents-badge-warn" title="No agent has been granted this tool — add it under an agent's Config → Tools">not wired to an agent</span>`
                : wiring === 'unknown'
                  ? html`<span class="k-badge agents-badge k-badge--muted" title="Agent or toolset assignments are unavailable">wiring unknown</span>`
                  : nothing}`
          },
        },
      ],
      actions: (c) => {
        const name = c.metadata.name
        const cat = connCategory(c.spec.type)
        return html`
          ${resourceTableAction({
            icon: 'pencil',
            label: `Edit connection ${name}`,
            tone: 'edit',
            onClick: () => {
              if (this.routeOwned) this.navigate({ kind: 'edit', resource: 'connection', name })
              else {
                this.hydrateEdit(c)
                this.editing = name
                this.connType = null
              }
            },
          })}
          ${cat === 'channel'
            ? html`${resourceTableAction({ icon: 'send', label: `Send a test message via ${name}`, tone: 'accent', onClick: () => void this.test(name) })}
                ${resourceTableAction({
                  icon: 'swap',
                  label: c.status?.webhookPath ? `Re-enable inbound chat for ${name}` : `Enable inbound chat for ${name}`,
                  tone: 'neutral',
                  onClick: () => void this.enableInbound(name),
                })}`
            : nothing}
          ${c.spec.auth === 'oauth'
            ? resourceTableAction({
                icon: 'link',
                label: c.status?.oauthConnected ? `Reconnect OAuth for ${name}` : `Connect OAuth for ${name}`,
                tone: 'accent',
                onClick: () => void this.oauth(name),
              })
            : nothing}
          ${resourceTableAction({ icon: 'trash', label: `Delete connection ${name}`, tone: 'delete', onClick: () => void this.del(name) })}
        `
      },
    })
  }

  // ---- create / edit forms -------------------------------------------------

  private editorArea(): TemplateResult {
    const editConn = this.editing ? this.store.connections.data.find((c) => c.metadata.name === this.editing) : undefined
    if (editConn) return this.editForm(editConn)
    const def = this.connType ? CONN_DEFS.find((d) => d.id === this.connType) : null
    if (def) return this.createForm(def)
    return this.picker()
  }

  private picker(showHeading = true): TemplateResult {
    const tile = (d: ConnTypeDef): TemplateResult => html`<button
      class="k-btn k-btn--ghost agents-conn-tile"
      @click=${() => {
        if (this.routeOwned) {
          this.navigate({ kind: 'create', resource: 'connection', type: d.id })
        } else {
          this.connType = d.id
          this.connMode = ''
        }
      }}
    >
      <span class="agents-conn-glyph">${icon(d.glyph)}</span>
      <span class="agents-conn-name">${d.label}</span>
      <span class="muted">${d.desc}</span>
    </button>`
    return html`<div class="agents-conn-picker">
      ${showHeading ? html`<h4>Add a connection</h4>` : nothing}
      ${(['tool', 'channel', 'connection'] as ConnCategory[]).map((cat) => {
        const defs = CONN_DEFS.filter((d) => connCategory(d.id) === cat)
        if (!defs.length) return nothing
        const m = CATEGORY_META[cat]
        return html`<div class="agents-conn-group">
          <h5 class="agents-conn-grouphead">${icon(m.icon)} ${m.label}s <span class="muted">— ${m.blurb}</span></h5>
          <div class="agents-conn-types">${defs.map(tile)}</div>
          ${cat === 'tool'
            ? html`<agents-assisted-search .store=${this.store} .api=${this.api}></agents-assisted-search>`
            : nothing}
        </div>`
      })}
    </div>`
  }

  private field(f: ConnField): TemplateResult {
    return html`<label>
      ${f.label}${f.required ? ' *' : ''}
      <input class="k-input"
        name=${f.key}
        type=${f.password ? 'password' : 'text'}
        placeholder=${f.placeholder || ''}
        ?required=${f.required}
        autocomplete="off"
        .value=${this.createValues[f.key] || ''}
        ?disabled=${this.createBusy}
        @input=${(e: Event) => (this.createValues = { ...this.createValues, [f.key]: (e.target as HTMLInputElement).value })}
      />
      ${f.hint ? html`<span class="agents-hint">${f.hint}</span>` : nothing}
    </label>`
  }

  private createForm(def: ConnTypeDef): TemplateResult {
    // A routed connection element can be reused while the type segment changes
    // (for example after browser back/forward). Ignore a mode from the prior
    // type instead of indexing an absent mode.
    const mode = def.modes?.some((m) => m.id === this.connMode) ? this.connMode : def.modes?.[0].id || ''
    const activeMode = def.modes?.find((m) => m.id === mode)
    let fields = def.modes ? activeMode?.fields || [] : def.fields || []
    const advanced = [...(def.advanced || []), ...(activeMode?.advanced || [])]
    // Platform OAuth app configured (operator env)? Then OAuth modes need no
    // client id/secret — drop those fields.
    const isOAuthMode = fields.some((f) => f.key === 'clientID')
    const platformApp = isOAuthMode && this.store.oauthApps.has(def.id)
    if (platformApp) fields = fields.filter((f) => f.key !== 'clientID' && f.key !== 'clientSecret')
    return html`<form
      class=${this.routeOwned ? 'agents-conn-form agents-guided-form k-create-surface k-create-surface--guided' : 'agents-conn-form k-card'}
      aria-busy=${this.createBusy ? 'true' : 'false'}
      @submit=${(e: Event) => {
        e.preventDefault()
        void this.create(def, e.target as HTMLFormElement)
      }}
    >
      <div class=${this.routeOwned ? 'k-create-body k-create-body--guided' : ''}>
      <div class=${this.routeOwned ? 'k-create-fields' : ''}>
      <div class="agents-conn-formhead">
        <button
          type="button"
          class="k-btn k-btn--ghost agents-back"
          ?disabled=${this.createBusy}
          @click=${() => (this.routeOwned ? this.navigate({ kind: 'create', resource: 'connection' }) : (this.connType = null))}
        >${icon('arrow-left')} connection types</button>
        ${this.routeOwned ? nothing : html`<h4>${icon(def.glyph)} ${def.label}</h4>`}
      </div>
      ${this.routeOwned ? nothing : html`<p class="muted">${def.desc}</p>`}
      ${def.setup
        ? html`<details class="agents-setup" open>
            <summary>Before you start — setup steps</summary>
            <ol>
              ${def.setup.map((s) => html`<li>${unsafeHTML(s)}</li>`)}
            </ol>
          </details>`
        : nothing}
      <label>
        Name *
        <input class="k-input" name="name" required pattern="[a-z0-9-]+" placeholder=${`my-${def.id}`} autocomplete="off"
          .value=${this.createValues.name || ''}
          ?disabled=${this.createBusy}
          @input=${(e: Event) => (this.createValues = { ...this.createValues, name: (e.target as HTMLInputElement).value })}
        />
        <span class="agents-hint">A short id you'll reference from agents.</span>
      </label>
      ${def.modes
        ? html`<div class="agents-modeseg" role="group" aria-label="Authentication mode">
            ${def.modes.map(
              (m) => html`<button type="button" class="k-btn k-btn--ghost agents-modebtn ${m.id === mode ? 'sel' : ''}" ?disabled=${this.createBusy} aria-pressed=${m.id === mode ? 'true' : 'false'} @click=${() => (this.connMode = m.id)}>
                ${m.label}
              </button>`,
            )}
          </div>`
        : nothing}
      ${platformApp
        ? html`<div class="agents-platform-note">
            ${icon('check')} Using the platform's ${def.label} OAuth app — no client id/secret needed. Create it, then click
            <strong>Connect</strong>.
          </div>`
        : nothing}
      ${fields.map((f) => this.field(f))}
      ${advanced.length
        ? html`<details class="agents-adv"><summary>Advanced</summary>${advanced.map((f) => this.field(f))}</details>`
        : nothing}
      </div>
      ${this.routeOwned ? createGuidance({
        icon: def.glyph,
        title: `Connect ${def.label}`,
        description: 'Prepare the external identity once; agents receive access only when you grant this connection later.',
        prerequisites: [
          def.setup?.length ? 'Review the provider setup steps shown in the form.' : `Have the ${def.label} endpoint or credential ready.`,
          `Required fields: ${['Name', ...fields.filter((field) => field.required).map((field) => field.label)].join(', ')}.`,
        ],
        values: [
          { label: 'Connection', value: this.createValues.name?.trim() || 'Not entered yet', technical: true },
          { label: 'Type', value: def.label },
          { label: 'Mode', value: activeMode?.label || 'Default' },
          { label: 'Required details', value: `${fields.filter((field) => field.required && this.createValues[field.key]?.trim()).length} of ${fields.filter((field) => field.required).length} entered` },
        ],
        nextSteps: [
          'Faros stores secret values in this workspace and does not show them after creation.',
          'Test or authorize the connection from the Connections page when the type supports it.',
          'Grant the connection to an agent directly or add it to a shared toolset.',
        ],
      }) : nothing}
      </div>
      <div class=${this.routeOwned ? 'k-create-actions' : ''}>
        ${this.routeOwned ? html`<button type="button" class="k-btn k-btn--ghost secondary" ?disabled=${this.createBusy} @click=${() => this.cancelCreate()}>Cancel</button>` : nothing}
        <button class="k-btn k-btn--primary" type="submit" ?disabled=${this.createBusy}>${this.createBusy ? 'Creating…' : 'Create connection'}</button>
      </div>
    </form>`
  }

  private cancelCreate(): void {
    if (this.createBusy) return
    this.createValues = {}
    if (this.routeOwned) {
      this.dispatchEvent(new CustomEvent('agents-cancel', { bubbles: true, composed: true }))
      return
    }
    this.connType = null
  }

  private editRouteSurface(): TemplateResult {
    const slice = this.store.connections
    const connection = slice.data.find((item) => item.metadata.name === this.editName)
    let body: TemplateResult
    if (slice.error && !slice.hasSnapshot) body = errorState(slice.error, () => void this.store.load('connections'))
    else if (!slice.loaded && !slice.hasSnapshot) body = loadingState('Loading connection…')
    else if (!connection && slice.error) body = html`${staleState(slice.error, () => void this.store.load('connections'))}${errorState(`No connection named “${this.editName}” appears in the last loaded workspace snapshot.`)}`
    else if (!connection) body = errorState(`Connection “${this.editName}” was not found in this workspace.`, () => void this.store.load('connections'))
    else body = html`${slice.error ? staleState(slice.error, () => void this.store.load('connections')) : nothing}${this.editForm(connection, true)}`

    return html`<div class="agents-menu agents-create-page k-create-page">
      <button type="button" class="k-btn k-btn--ghost k-back-action" ?disabled=${this.editBusy} @click=${() => this.cancelEdit()}>${icon('arrow-left')} Connections</button>
      <header class="k-create-header">
        <h1 class="k-create-title" tabindex="-1" data-edit-heading>Edit connection</h1>
        <p class="k-create-description">Update <code>${this.editName}</code> without exposing its stored credential.</p>
      </header>
      ${body}
    </div>`
  }

  private cancelEdit(): void {
    if (this.editBusy) return
    this.editValuesFor = ''
    this.editValues = {}
    if (this.routeOwned && this.editRoute) {
      this.dispatchEvent(new CustomEvent<EditCancelDetail>('agents-edit-cancel', {
        detail: { resource: 'connection', name: this.editName },
        bubbles: true,
        composed: true,
      }))
      return
    }
    this.editing = null
  }

  private hydrateEdit(c: Connection): void {
    const usesChannel = connCategory(c.spec.type) === 'channel' || Boolean(c.spec.channel)
    this.editValuesFor = c.metadata.name
    this.editValues = {
      displayName: c.spec.displayName || '',
      endpoint: (usesChannel ? c.spec.channel : c.spec.baseURL) || '',
      instance: c.spec.config?.instance || '',
      secret: '',
    }
  }

  private setEditValue(key: string, event: Event): void {
    this.editValues = { ...this.editValues, [key]: (event.target as HTMLInputElement).value }
  }

  private editForm(c: Connection, routePage = false): TemplateResult {
    const cat = connCategory(c.spec.type)
    const usesChannel = cat === 'channel' || !!c.spec.channel
    const shape = connShape(c)
    let endpointLabel: string
    if (shape.discordWebhook) endpointLabel = 'Webhook URL'
    else if (shape.discordBot) endpointLabel = 'Channel ID (optional)'
    else if (!usesChannel) endpointLabel = 'Endpoint URL'
    else if (c.spec.type === 'slack') endpointLabel = 'Webhook URL / channel'
    else if (c.spec.type === 'smtp') endpointLabel = 'Send to'
    else endpointLabel = 'Channel / chat ID'
    const isOAuth = c.spec.auth === 'oauth'
    return html`<form
      class=${routePage ? 'agents-conn-form k-create-surface k-create-surface--wide' : 'agents-conn-form k-card'}
      aria-busy=${this.editBusy ? 'true' : 'false'}
      @submit=${(e: Event) => {
        e.preventDefault()
        void this.saveEdit(c, e.target as HTMLFormElement, usesChannel)
      }}
    >
      <div class=${routePage ? 'k-create-body k-create-fields' : ''}>
      ${routePage ? nothing : html`<div class="agents-conn-form k-cardhead">
        <button type="button" class="k-btn k-btn--ghost agents-back" @click=${() => this.cancelEdit()}>${icon('arrow-left')} connections</button>
        <h4>
          Edit ${icon(CATEGORY_META[cat].icon)} <code>${c.metadata.name}</code>
          ${c.spec.type === 'discord' ? html`<span class="k-badge agents-badge">${shape.typeLabel}</span>` : nothing}
        </h4>
      </div>`}
      <label>Display name<input class="k-input" name="displayName" .value=${this.editValues.displayName ?? c.spec.displayName ?? ''} placeholder=${c.metadata.name} ?disabled=${this.editBusy} @input=${(event: Event) => this.setEditValue('displayName', event)} /></label>
      ${instanceBacked(c)
        ? html`<label>
            Instance name *
            <input class="k-input" name="instance" .value=${this.editValues.instance ?? c.spec.config?.instance ?? ''} placeholder="search" required autocomplete="off" ?disabled=${this.editBusy} @input=${(event: Event) => this.setEditValue('instance', event)} />
            <span class="agents-hint">
              The instance under Infrastructure. Agents reach it over the platform's internal path — there is no URL and no token.
            </span>
          </label>`
        : html`<label>${endpointLabel}<input class="k-input" name="endpoint" .value=${this.editValues.endpoint ?? (usesChannel ? c.spec.channel : c.spec.baseURL) ?? ''} ?disabled=${this.editBusy} @input=${(event: Event) => this.setEditValue('endpoint', event)} /></label>`}
      ${instanceBacked(c)
        ? nothing
        : shape.discordWebhook
        ? nothing
        : isOAuth
          ? html`<p class="agents-hint">
              This is an OAuth connection — use the ${icon('link')} button in the table to re-authorize. Client credentials aren't
              edited here.
            </p>`
          : html`<label>
              New ${shape.discordBot ? 'bot token' : 'secret / token'}
              <input class="k-input" name="secret" type="password" .value=${this.editValues.secret || ''} placeholder="leave blank to keep the current one" autocomplete="off" ?disabled=${this.editBusy} @input=${(event: Event) => this.setEditValue('secret', event)} />
              <span class="agents-hint">Only set this to rotate the credential.</span>
            </label>`}
      </div>
      <div class=${routePage ? 'k-create-actions' : 'agents-form-actions'}>
        ${routePage ? html`<button type="button" class="k-btn k-btn--ghost secondary" ?disabled=${this.editBusy} @click=${() => this.cancelEdit()}>Cancel</button>` : nothing}
        <button class="k-btn k-btn--primary" type="submit" ?disabled=${this.editBusy}>${this.editBusy ? 'Saving…' : 'Save changes'}</button>
        ${routePage ? nothing : html`<button type="button" class="k-btn k-btn--ghost secondary" ?disabled=${this.editBusy} @click=${() => this.cancelEdit()}>Cancel</button>`}
      </div>
    </form>`
  }
}

if (!customElements.get('agents-connections')) customElements.define('agents-connections', Connections)
