import { createApp, h, reactive, type App as VueApp } from 'vue'
import App from './App.vue'
import type { FarosContext } from './types'

export class DeploymentsElement extends HTMLElement {
  private vueApp: VueApp | null = null
  private host: HTMLDivElement | null = null
  private state = reactive<{ ctx: FarosContext | null }>({ ctx: null })

  set farosContext(value: FarosContext | null) {
    this.state.ctx = value
  }

  get farosContext(): FarosContext | null {
    return this.state.ctx
  }

  connectedCallback(): void {
    if (this.vueApp) return
    this.host = document.createElement('div')
    this.host.className = 'deployments-host'
    this.appendChild(this.host)
    this.vueApp = createApp({ render: () => h(App, { ctx: this.state.ctx }) })
    this.vueApp.mount(this.host)
  }

  disconnectedCallback(): void {
    this.vueApp?.unmount()
    this.vueApp = null
    if (this.host?.parentNode === this) this.removeChild(this.host)
    this.host = null
  }
}
