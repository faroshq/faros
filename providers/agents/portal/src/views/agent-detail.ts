// Agent detail page — the one canonical place an agent is edited.
//
// Config tab = two panes: the full configuration form on the left, a live chat
// playground on the right (edit config, try it immediately). Runs tab = this
// agent's Activity feed, filtered.

import { html, nothing, type TemplateResult } from 'lit'
import { property } from 'lit/decorators.js'
import { StoreElement } from '../ui/base'
import { icon } from '../ui/icon'
import { confirmModal } from '../portalkit/modal'
import { tabClass, tabsClass } from '../portalkit/tabs'
import { mutate } from '../mutate'
import { hashFor, type AgentTab } from '../router'
import type { Agent } from '../types'

import './agent-config'
import './agent-chat'
import './activity'

const TABS: [AgentTab, TemplateResult][] = [
  ['config', html`${icon('sliders')} Config`],
  ['runs', html`${icon('gauge')} Runs`],
]

export class AgentDetail extends StoreElement {
  @property({ type: String }) name = ''
  @property({ type: String }) tab: AgentTab = 'config'

  private async del(): Promise<void> {
    const authority = this.captureAuthority()
    const name = this.name
    const ok = await confirmModal({
      title: `Delete agent “${name}”?`,
      message: 'This also deletes its chat history.',
      danger: true,
      confirmLabel: 'Delete',
    })
    if (!ok || !this.authorityIsCurrent(authority)) return
    const res = await mutate(authority.store, {
      run: () => authority.api.deleteAgent(name),
      success: `Agent “${name}” deleted.`,
      failure: 'Delete failed',
      reload: ['agents'],
    })
    if (res !== undefined && this.authorityIsCurrent(authority)) this.navigate({ kind: 'menu', menu: 'agents' })
  }

  private backLink(): TemplateResult {
    return html`<a class="k-btn k-btn--ghost k-back-action" href=${hashFor({ kind: 'menu', menu: 'agents' })}>
      ${icon('arrow-left')} Agents
    </a>`
  }

  private pageHeader(a?: Agent): TemplateResult {
    const title = a?.spec?.displayName || a?.metadata.name || this.name
    const showName = Boolean(a && title !== a.metadata.name)
    return html`<header class="k-resource-page__header">
      <div class="k-resource-page__heading">
        <h1 class="k-resource-page__title">${title}</h1>
        <div class="k-resource-page__meta">
          <span class="k-resource-page__kind">Agent</span>
          ${showName
            ? html`<span class="k-resource-page__separator" aria-hidden="true">·</span><code>${a!.metadata.name}</code>`
            : nothing}
          ${a?.status?.suspendedReason
            ? html`<span class="k-resource-page__separator" aria-hidden="true">·</span>
                <span class="k-resource-page__status">
                  <span class="k-badge k-badge--warning agents-badge-warn">${a.status.suspendedReason}</span>
                </span>`
            : nothing}
        </div>
        ${a?.spec?.description ? html`<p class="k-resource-page__subtitle">${a.spec.description}</p>` : nothing}
      </div>
      ${a
        ? html`<div class="k-resource-page__header-side">
            <div class="k-resource-page__actions">
              <button class="k-btn k-btn--danger" type="button" @click=${() => void this.del()}>${icon('trash')} Delete</button>
            </div>
          </div>`
        : nothing}
    </header>`
  }

  private initialReadState(): TemplateResult {
    const slice = this.store.agents
    if (slice.error && !slice.hasSnapshot) {
      return html`<div class="k-resource-page__read-error" role="alert" aria-live="assertive">
        <span class="k-resource-page__read-icon" aria-hidden="true">${icon('circle')}</span>
        <span class="k-resource-page__read-message">Could not load this agent. ${slice.error}</span>
        <button
          class="k-btn k-btn--ghost k-resource-page__retry"
          type="button"
          ?disabled=${slice.loading}
          aria-busy=${slice.loading ? 'true' : nothing}
          @click=${() => void this.store.load('agents')}
        >
          ${slice.loading ? 'Retrying…' : 'Retry'}
        </button>
      </div>`
    }
    if (!slice.loaded) {
      return html`<div class="k-resource-page__loading k-loading-reveal" role="status" aria-live="polite" aria-label="Loading ${this.name}">
        <div class="shimmer k-resource-page__skeleton k-resource-page__skeleton--short"></div>
        <div class="shimmer k-resource-page__skeleton k-resource-page__skeleton--wide"></div>
        <div class="shimmer k-resource-page__skeleton k-resource-page__skeleton--medium"></div>
      </div>`
    }
    return html`${slice.error && slice.hasSnapshot
        ? html`<div class="k-resource-page__stale" role="status" aria-live="polite">
            <span class="k-resource-page__read-icon" aria-hidden="true">${icon('circle')}</span>
            <span class="k-resource-page__read-message">Could not refresh the agent list. ${slice.error}</span>
            <button
              class="k-btn k-btn--ghost k-resource-page__retry"
              type="button"
              ?disabled=${slice.loading}
              aria-busy=${slice.loading ? 'true' : nothing}
              @click=${() => void this.store.load('agents')}
            >
              ${slice.loading ? 'Retrying…' : 'Retry'}
            </button>
          </div>`
        : nothing}
      <div class="k-card agents-state agents-state-empty" role="status">
        ${icon('bot')} No agent named “${this.name}” in ${slice.error ? 'the last loaded workspace snapshot' : 'this workspace'}.
      </div>`
  }

  render(): TemplateResult {
    const a = this.store.agent(this.name)
    if (!a) {
      return html`<div class="agents-detail">
        ${this.backLink()}
        <section class="k-resource-page" aria-busy=${this.store.agents.loading ? 'true' : nothing}>
          ${this.pageHeader()}
          <div class="k-resource-page__body">${this.initialReadState()}</div>
        </section>
      </div>`
    }
    return html`
      <div class="agents-detail">
        ${this.backLink()}
        <section class="k-resource-page" aria-busy=${this.store.agents.loading ? 'true' : nothing}>
          <span
            class="k-resource-page__live"
            role="status"
            aria-live="polite"
            aria-atomic="true"
            style="block-size: 1px; clip: rect(0 0 0 0); clip-path: inset(50%); inline-size: 1px; margin: -1px; overflow: hidden; padding: 0; position: absolute; white-space: nowrap;"
          >
            ${this.store.agents.loading ? `Updating ${a.spec?.displayName || a.metadata.name}…` : ''}
          </span>
          ${this.pageHeader(a)}
          ${this.store.agents.error
            ? html`<div class="k-resource-page__stale" role="status" aria-live="polite">
                <span class="k-resource-page__read-icon" aria-hidden="true">${icon('circle')}</span>
                <span class="k-resource-page__read-message">Showing the last loaded agent. ${this.store.agents.error}</span>
                <button
                  class="k-btn k-btn--ghost k-resource-page__retry"
                  type="button"
                  ?disabled=${this.store.agents.loading}
                  aria-busy=${this.store.agents.loading ? 'true' : nothing}
                  @click=${() => void this.store.load('agents')}
                >
                  ${this.store.agents.loading ? 'Retrying…' : 'Retry'}
                </button>
              </div>`
            : nothing}
          <div class="k-resource-page__body agents-resource-body">
            <nav class="${tabsClass('agents-subnav')}" aria-label="Agent sections">
              ${TABS.map(
                ([id, label]) => html`<button
                  class="k-btn k-btn--ghost ${tabClass({ active: this.tab === id, className: 'agents-subtab' })}"
                  type="button"
                  data-k-tab-id=${id}
                  aria-current=${this.tab === id ? 'page' : nothing}
                  @click=${() => this.navigate({ kind: 'agent', name: this.name, tab: id })}
                >
                  ${label}
                </button>`,
              )}
            </nav>
            ${this.tab === 'runs'
              ? html`<agents-activity .store=${this.store} .api=${this.api} .agent=${this.name}></agents-activity>`
              : html`<div class="agents-split">
                  <div class="agents-split-config">
                    <agents-agent-config .store=${this.store} .api=${this.api} .name=${this.name}></agents-agent-config>
                  </div>
                  <div class="agents-split-chat">
                    <agents-agent-chat .store=${this.store} .api=${this.api} .name=${this.name}></agents-agent-chat>
                  </div>
                </div>`}
          </div>
        </section>
      </div>
    `
  }
}

if (!customElements.get('agents-agent-detail')) customElements.define('agents-agent-detail', AgentDetail)
