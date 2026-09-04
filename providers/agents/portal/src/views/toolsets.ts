// Toolsets — shared bundles of Tools that agents link, rendered as a section of
// the Connections tab. Checked tool-connections drive the DERIVED families; the
// families list is never hand-picked.

import { html, nothing, type TemplateResult } from 'lit'
import { property, state } from 'lit/decorators.js'
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
import { confirmModal } from '../portalkit/modal'
import { mutate } from '../mutate'
import type { CreateSuccessDetail } from '../router'
import type { Toolset } from '../types'

export class Toolsets extends StoreElement {
  @property({ type: Boolean }) routeOwned = false
  @property({ type: Boolean }) createRoute = false
  // editing is null when closed, otherwise the existing toolset being edited.
  @state() private editing: string | null = null
  @state() private draftName = ''
  @state() private draftDisplay = ''
  @state() private draftConns: string[] = []
  @state() private createBusy = false
  @state() private toolsetTable = createResourceTableState()

  private openCreate(): void {
    if (this.routeOwned) {
      this.navigate({ kind: 'create', resource: 'toolset' })
      return
    }
    this.editing = ''
    this.draftName = ''
    this.draftDisplay = ''
    this.draftConns = []
  }

  private openEdit(t: Toolset): void {
    this.editing = t.metadata.name
    this.draftName = t.metadata.name
    this.draftDisplay = t.spec.displayName || ''
    this.draftConns = [...(t.spec.connections || [])]
  }

  private async save(e: Event): Promise<void> {
    e.preventDefault()
    if (this.createBusy) return
    const form = e.currentTarget as HTMLFormElement
    // Read the create id from the form as well as the draft state. This keeps
    // the route surface correct for native form submission and for callers
    // that set an input before dispatching submit in an embedded host.
    const name = (form.querySelector<HTMLInputElement>('[name=name]')?.value || this.draftName).trim()
    const connections = this.draftConns
    const families = this.store.familiesFor(connections)
    const displayName = this.draftDisplay.trim()
    const editing = this.editing
    this.createBusy = true
    let res: Toolset | undefined
    try {
      res = editing
        ? await mutate(this.store, {
            run: () => this.api.patchToolset(editing, { displayName, families, connections }),
            success: 'Toolset updated.',
            failure: 'Update failed',
            reload: ['toolsets'],
          })
        : await mutate(this.store, {
            run: () => this.api.createToolset({ name, displayName, families, connections }),
            success: 'Toolset created.',
            failure: 'Create failed',
            reload: ['toolsets'],
          })
    } finally {
      this.createBusy = false
    }
    if (res && this.routeOwned && !editing) {
      this.dispatchEvent(
        new CustomEvent<CreateSuccessDetail>('agents-create-success', {
          detail: { resource: 'toolset', name, item: res },
          bubbles: true,
          composed: true,
        }),
      )
    } else if (res) {
      this.editing = null
    }
  }

  private async del(name: string): Promise<void> {
    const authority = this.captureAuthority()
    const ok = await confirmModal({
      title: `Delete toolset “${name}”?`,
      message: 'Agents linking it will lose those tools.',
      danger: true,
      confirmLabel: 'Delete',
    })
    if (!ok || !this.authorityIsCurrent(authority)) return
    await mutate(authority.store, {
      run: () => authority.api.deleteToolset(name),
      success: 'Toolset deleted.',
      failure: 'Delete failed',
      reload: ['toolsets'],
    })
  }

  private usedBy(name: string): number | null {
    if (!this.store.agents.hasSnapshot || this.store.agents.error) return null
    return this.store.agents.data.filter((a) =>
      [...(a.spec?.tools?.interactive?.toolsets || []), ...(a.spec?.tools?.background?.toolsets || [])].includes(name),
    ).length
  }

  private table(rows: Toolset[]): TemplateResult {
    return resourceTable({
      ariaLabel: 'Toolsets',
      rows,
      rowKey: (toolset) => toolset.metadata.name,
      state: this.toolsetTable,
      onStateChange: (next: ResourceTableState) => (this.toolsetTable = next),
      searchPlaceholder: 'Search toolsets…',
      searchText: (toolset) => [
        toolset.metadata.name,
        toolset.spec.displayName,
        ...(toolset.spec.connections || []),
      ].filter(Boolean).join(' '),
      filters: [
        {
          key: 'usage',
          label: 'Usage',
          allLabel: 'All usage',
          value: (toolset) => {
            const used = this.usedBy(toolset.metadata.name)
            return used === null ? 'unknown' : used > 0 ? 'used' : 'unused'
          },
          labelFor: (value) => ({ used: 'In use', unused: 'Unused', unknown: 'Unknown' })[value] || value,
        },
      ],
      columns: [
        {
          key: 'name',
          label: 'Name',
          primary: true,
          render: (toolset) => html`<span class="agents-resource-name" title=${toolset.metadata.name}>${toolset.spec.displayName || toolset.metadata.name}</span>
            ${toolset.spec.displayName ? html`<code class="agents-resource-id">${toolset.metadata.name}</code>` : nothing}`,
        },
        {
          key: 'tools',
          label: 'Tools',
          render: (toolset) => {
            const connections = toolset.spec.connections || []
            return connections.length
              ? html`<span class="agents-resource-tags">${connections.map((connection) => html`<span class="k-badge agents-badge">${connection}</span>`)}</span>`
              : html`<span class="muted">—</span>`
          },
        },
        {
          key: 'usedBy',
          label: 'Used by',
          render: (toolset) => {
            const used = this.usedBy(toolset.metadata.name)
            return used === null
              ? html`<span class="muted" title="Agent assignments are unavailable">Unknown</span>`
              : html`<span class="muted">${used} agent${used === 1 ? '' : 's'}</span>`
          },
        },
      ],
      actions: (toolset) => html`
        ${resourceTableAction({
          icon: 'pencil',
          label: `Edit toolset ${toolset.metadata.name}`,
          tone: 'edit',
          onClick: () => this.openEdit(toolset),
        })}
        ${resourceTableAction({
          icon: 'trash',
          label: `Delete toolset ${toolset.metadata.name}`,
          tone: 'delete',
          onClick: () => void this.del(toolset.metadata.name),
        })}
      `,
    })
  }

  render(): TemplateResult {
    if (this.createRoute) return this.createSurface()
    const slice = this.store.toolsets
    const connections = this.store.connections
    const showFirstRun = slice.hasSnapshot && slice.data.length === 0
    const renderFirstRun = (): TemplateResult => {
      const retryConnections = () => void this.store.load('connections')
      const connectionSnapshot = connections.hasSnapshot
      const hasToolConnections = connectionSnapshot && this.store.toolConnections().length > 0
      const guide = firstRunGuide({
        icon: 'package',
        title: hasToolConnections ? 'Bundle tools for reuse' : 'Create your first toolset',
        description: hasToolConnections
          ? 'Group available tool connections so the same capability bundle can be attached to multiple agents.'
          : connectionSnapshot
            ? 'Start with core and edge capabilities now. External tool connections are optional and can be added whenever the bundle needs them.'
            : 'Start with core and edge capabilities now. Available external tool connections will appear after they finish loading.',
        primaryLabel: 'Create toolset',
        primary: () => this.navigate({ kind: 'create', resource: 'toolset' }),
        secondaryLabel: connectionSnapshot && !hasToolConnections ? 'Create connection' : undefined,
        secondary: connectionSnapshot && !hasToolConnections
          ? () => this.navigate({ kind: 'create', resource: 'connection' })
          : undefined,
        steps: hasToolConnections
          ? [
              { label: 'Connection', description: 'Callable external tools are available' },
              { label: 'Toolset', description: 'Bundle the tools for reuse' },
              { label: 'Agent', description: 'Attach the bundle from Config' },
            ]
          : [
              { label: 'Toolset', description: 'Begin with core and edge capabilities' },
              { label: 'Agent', description: 'Attach the bundle from Config' },
              { label: 'Expand', description: 'Add external connections when needed' },
            ],
        currentStep: hasToolConnections ? 1 : 0,
        journeyLabel: 'Toolset setup path',
      })

      if (!connectionSnapshot) {
        const notice = connections.error
          ? errorState(`Could not load optional tool connections. ${connections.error}`, retryConnections)
          : loadingState('Loading optional tool connections…')
        return html`${notice}${guide}`
      }
      return connections.error ? html`${staleState(connections.error, retryConnections)}${guide}` : guide
    }
    return html`<div class="agents-panel k-card agents-route-panel">
      <div class="agents-panel-head">
        <h3>${icon('package')} Toolsets</h3>
        ${this.editing === null && !showFirstRun ? html`<button class="k-btn k-btn--ghost secondary" @click=${() => this.openCreate()}>${icon('plus')} New toolset</button>` : nothing}
      </div>
      <p class="muted">Shared bundles of Tools. Define once, link from any agent's Config pane.</p>
      ${sliceView<Toolset>({
        slice,
        emptyIcon: 'package',
        emptyText: 'No toolsets yet.',
        empty: renderFirstRun,
        retry: () => void this.store.load('toolsets'),
        content: (rows) => this.table(rows),
      })}
      ${this.editing !== null ? this.form() : nothing}
    </div>`
  }

  private createSurface(): TemplateResult {
    return html`<div class="agents-menu agents-create-page k-create-page">
      <button type="button" class="k-btn k-btn--ghost k-back-action" ?disabled=${this.createBusy} @click=${() => this.cancelCreate()}>${icon('arrow-left')} Connections</button>
      <header class="k-create-header"><h1 class="k-create-title">Create toolset</h1><p class="k-create-description">Bundle reusable tools once, then attach the toolset to any agent.</p></header>
      ${this.form()}
    </div>`
  }

  private form(): TemplateResult {
    const toolConns = this.store.toolConnections()
    const isEdit = !!this.editing
    return html`<form class=${this.createRoute ? 'agents-toolset-form agents-guided-form k-create-surface k-create-surface--guided' : 'agents-toolset-form k-card'} aria-busy=${this.createBusy ? 'true' : 'false'} @submit=${(e: Event) => void this.save(e)}>
      <div class=${this.createRoute ? 'k-create-body k-create-body--guided' : ''}>
      <div class=${this.createRoute ? 'k-create-fields' : ''}>
      ${this.createRoute ? nothing : html`<h4>${isEdit ? html`Edit toolset <code>${this.editing}</code>` : 'New toolset'}</h4>`}
      ${isEdit
        ? html`<label>Display name<input class="k-input" .value=${this.draftDisplay} @input=${(e: Event) => (this.draftDisplay = (e.target as HTMLInputElement).value)} /></label>`
        : html`<div class="agents-grid2">
            <label
              >Name *<input class="k-input"
                name="name"
                required
                pattern="[a-z0-9-]+"
                placeholder="dev-tools"
                .value=${this.draftName}
                ?disabled=${this.createBusy}
                @input=${(e: Event) => (this.draftName = (e.target as HTMLInputElement).value)}
            /></label>
            <label
              >Display name<input class="k-input"
                placeholder="optional"
                .value=${this.draftDisplay}
                ?disabled=${this.createBusy}
                @input=${(e: Event) => (this.draftDisplay = (e.target as HTMLInputElement).value)}
            /></label>
          </div>`}
      <fieldset class="agents-tools">
        <legend>Tools</legend>
        <div class="agents-checkrow">
          ${toolConns.length
            ? toolConns.map(
                (c) => html`<label class="agents-check">
                  <input
                    type="checkbox"
                    .checked=${this.draftConns.includes(c.metadata.name)}
                    ?disabled=${this.createBusy}
                    @change=${(e: Event) => {
                      const on = (e.target as HTMLInputElement).checked
                      this.draftConns = on
                        ? [...this.draftConns, c.metadata.name]
                        : this.draftConns.filter((x) => x !== c.metadata.name)
                    }}
                  />
                  ${c.metadata.name} <span class="agents-hint">${c.spec.type}</span>
                </label>`,
              )
            : html`<span class="muted">No tools yet — create MCP/GitHub/web tools above. Cluster edges are always on.</span>`}
        </div>
        <span class="agents-hint">Tool families are derived from these connections — never picked by hand.</span>
      </fieldset>
      </div>
      ${this.createRoute ? createGuidance({
        icon: 'package',
        title: 'Build a reusable capability bundle',
        description: 'Choose existing tool connections; Faros derives the required tool families from those selections.',
        prerequisites: [
          toolConns.length ? 'At least one tool connection is available in this workspace.' : 'Create a tool connection first if this bundle should expose external tools.',
          'Cluster edge tools remain available independently and do not need a connection here.',
        ],
        values: [
          { label: 'Toolset', value: this.draftName.trim() || 'Not entered yet', technical: true },
          { label: 'Display name', value: this.draftDisplay.trim() || 'Same as name' },
          { label: 'Connections', value: this.draftConns.length ? this.draftConns.join(', ') : 'None selected', technical: true },
          { label: 'Families', value: this.store.familiesFor(this.draftConns).join(', '), technical: true },
        ],
        nextSteps: [
          'Faros creates the bundle without changing any existing agents.',
          'Attach the toolset to interactive or background work from agent Config.',
          'Connection authorization is still checked when an agent invokes a tool.',
        ],
      }) : nothing}
      </div>
      <div class=${this.createRoute ? 'k-create-actions' : 'agents-form-actions'}>
        <button type="button" class="k-btn k-btn--ghost secondary" ?disabled=${this.createBusy} @click=${() => this.cancelCreate()}>Cancel</button>
        <button class="k-btn k-btn--primary" type="submit" ?disabled=${this.createBusy}>${this.createBusy ? 'Creating…' : isEdit ? 'Save' : 'Create toolset'}</button>
      </div>
    </form>`
  }

  private cancelCreate(): void {
    if (this.createBusy) return
    if (this.createRoute) {
      this.dispatchEvent(new CustomEvent('agents-cancel', { bubbles: true, composed: true }))
      return
    }
    this.editing = null
  }
}

if (!customElements.get('agents-toolsets')) customElements.define('agents-toolsets', Toolsets)
