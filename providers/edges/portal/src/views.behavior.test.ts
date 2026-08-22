import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRenderer, nextTick, type Component } from 'vue'

const api = vi.hoisted(() => ({
  listServices: vi.fn(),
  listServicesPage: vi.fn(),
  listEdges: vi.fn(),
  fetchServiceCatalog: vi.fn(),
  createKubeEdgeService: vi.fn(),
  deleteEdgeService: vi.fn(),
  updateEdgeService: vi.fn(),
  connectEdgeService: vi.fn(),
  listWorkloads: vi.fn(),
  listWorkloadsPage: vi.fn(),
  createWorkload: vi.fn(),
  deleteWorkload: vi.fn(),
  deployMarketplaceApp: vi.fn(),
}))

vi.mock('./api', () => api)

import Services from './Services.vue'
import Workloads from './Workloads.vue'

type HostNode = {
  type: string
  props: Record<string, unknown>
  children: HostNode[]
  parent: HostNode | null
  text?: string
}

function createHostRenderer() {
  const renderer = createRenderer<HostNode, HostNode>({
    patchProp(node, key, _previous, value) {
      node.props[key] = value
    },
    insert(node, parent, anchor = null) {
      node.parent = parent
      if (anchor) {
        const index = parent.children.indexOf(anchor)
        parent.children.splice(index < 0 ? parent.children.length : index, 0, node)
      } else {
        parent.children.push(node)
      }
    },
    remove(node) {
      const index = node.parent?.children.indexOf(node) ?? -1
      if (index >= 0 && node.parent) node.parent.children.splice(index, 1)
      node.parent = null
    },
    createElement(type) {
      return { type, props: {}, children: [], parent: null }
    },
    createText(text) {
      return { type: '#text', text, props: {}, children: [], parent: null }
    },
    createComment(text) {
      return { type: '#comment', text, props: {}, children: [], parent: null }
    },
    setText(node, text) {
      node.text = text
    },
    setElementText(node, text) {
      node.children = [{ type: '#text', text, props: {}, children: [], parent: node }]
    },
    parentNode(node) {
      return node.parent
    },
    nextSibling(node) {
      const siblings = node.parent?.children ?? []
      const index = siblings.indexOf(node)
      return index >= 0 ? siblings[index + 1] ?? null : null
    },
    querySelector() {
      return null
    },
    setScopeId() {},
    cloneNode(node) {
      return { ...node, props: { ...node.props }, children: [...node.children] }
    },
    insertStaticContent() {
      const text: HostNode = { type: '#text', text: '', props: {}, children: [], parent: null }
      return [text, text]
    },
  })
  return { renderer, root: { type: '#root', props: {}, children: [], parent: null } as HostNode }
}

async function mount(component: Component, props: Record<string, unknown> = {}) {
  const previousDocument = globalThis.document
  globalThis.document = {
    getElementById: () => null,
    createElement: () => ({ id: '', textContent: '' }),
    head: { appendChild() {} },
  } as unknown as Document
  const { renderer, root } = createHostRenderer()
  const app = renderer.createApp(component, props)
  app._context.provides[Symbol.for('v-scx')] = { modules: new Set() }
  const proxy = app.mount(root) as unknown as { $: { setupState: Record<string, any> } }
  await nextTick()
  return {
    instance: proxy.$,
    root,
    unmount() {
      app.unmount()
      globalThis.document = previousDocument
    },
  }
}

async function flush() {
  await Promise.resolve()
  await Promise.resolve()
  await nextTick()
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

const edge = { name: 'edge-a', type: 'kubernetes', connected: true }
const service = { name: 'svc-a', edgeName: 'edge-a', serviceType: 'generic', phase: 'Ready' }
const workload = {
  name: 'workload-a', image: 'nginx', replicas: 1, strategy: 'Spread', phase: 'Running',
  edges: [{ edgeName: 'edge-a', phase: 'Running', readyReplicas: 1, message: 'ready' }],
}

beforeEach(() => {
  vi.clearAllMocks()
  api.fetchServiceCatalog.mockResolvedValue([])
  api.listEdges.mockResolvedValue([edge])
  api.listServices.mockResolvedValue([service])
  api.listWorkloads.mockResolvedValue([workload])
  api.listServicesPage.mockResolvedValue({ items: [service], continue: undefined })
  api.listWorkloadsPage.mockResolvedValue({ items: [workload], continue: undefined })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('edge list views', () => {
  const views = [
    {
      label: 'services',
      component: Services,
      pageMock: api.listServicesPage,
      allMock: api.listServices,
      change: 'handleServiceTableChange',
      pageRows: [{ ...service, name: 'svc-b' }],
      filters: { edgeName: '', typeLabel: '', status: '' },
    },
    {
      label: 'workloads',
      component: Workloads,
      pageMock: api.listWorkloadsPage,
      allMock: api.listWorkloads,
      change: 'handleWorkloadTableChange',
      pageRows: [{ ...workload, name: 'workload-b' }],
      filters: { strategy: '', status: '' },
    },
  ] as const

  it.each(views)('$label loads unfiltered first/next/previous pages and resets the cursor for a page-size change', async (view) => {
    const mounted = await mount(view.component)
    try {
      await flush()
      expect(view.pageMock).toHaveBeenCalledWith({ limit: 10 })
      expect(view.allMock).not.toHaveBeenCalled()
      expect(api.listEdges).toHaveBeenCalled()

      const state = mounted.instance.setupState
      if (view.label === 'services') {
        expect(state.serviceRows).toMatchObject([{ name: 'svc-a', edgeName: 'edge-a', status: 'Ready' }])
      } else {
        expect(state.workloadRows).toMatchObject([{ name: 'workload-a', edges: [{ edgeName: 'edge-a', phase: 'Running' }] }])
      }
      view.pageMock.mockResolvedValueOnce({ items: view.pageRows, continue: 'opaque-next' })
      state[view.change]({ reason: 'page', page: 2, pageSize: 25, query: '', filters: view.filters, cursor: 'opaque-next' })
      await flush()
      expect(view.pageMock).toHaveBeenLastCalledWith({ limit: 25, continue: 'opaque-next' })

      view.pageMock.mockResolvedValueOnce({ items: view.pageRows, continue: 'opaque-next' })
      state[view.change]({ reason: 'page', page: 1, pageSize: 25, query: '', filters: view.filters, cursor: null })
      await flush()
      expect(view.pageMock).toHaveBeenLastCalledWith({ limit: 25 })

      view.pageMock.mockResolvedValueOnce({ items: view.pageRows, continue: undefined })
      state[view.change]({ reason: 'page-size', page: 1, pageSize: 50, query: '', filters: view.filters, cursor: null })
      await flush()
      expect(view.pageMock).toHaveBeenLastCalledWith({ limit: 50 })
    } finally {
      mounted.unmount()
    }
  })

  it.each(views)('$label switches active query/filter reads to a complete client list and returns to server paging when cleared', async (view) => {
    const mounted = await mount(view.component)
    try {
      await flush()
      view.pageMock.mockClear()
      view.allMock.mockClear()
      view.allMock.mockResolvedValue([{ ...view.pageRows[0], name: `${view.label}-complete` }])
      const state = mounted.instance.setupState

      state[view.change]({ reason: 'filter', page: 1, pageSize: 10, query: '', filters: { ...view.filters, [view.label === 'services' ? 'status' : 'strategy']: view.label === 'services' ? 'Ready' : 'Spread' }, cursor: null })
      await flush()
      expect(view.allMock).toHaveBeenCalledTimes(1)
      expect(view.pageMock).not.toHaveBeenCalled()
      expect(state.tableMode).toBe('client')
      expect(state.tablePage).toBe(1)
      expect(state.tableCursor).toBeNull()

      // Once the complete list is resident, changing the local query does not
      // trigger another network read.
      state[view.change]({ reason: 'query', page: 1, pageSize: 10, query: 'needle', filters: view.filters, cursor: null })
      await flush()
      expect(view.allMock).toHaveBeenCalledTimes(1)

      // Clearing the query/facets returns to a bounded server page.
      view.pageMock.mockResolvedValueOnce({ items: view.pageRows, continue: undefined })
      state[view.change]({ reason: 'filter', page: 1, pageSize: 10, query: '', filters: view.filters, cursor: null })
      await flush()
      expect(view.pageMock).toHaveBeenLastCalledWith({ limit: 10 })
      expect(state.tableMode).toBe('server')
    } finally {
      mounted.unmount()
    }
  })

  it.each(views)('$label ignores an older response after a newer request replaces its query', async (view) => {
    const mounted = await mount(view.component)
    try {
      await flush()
      const first = deferred<unknown[]>()
      const second = deferred<unknown[]>()
      view.allMock.mockReset()
      view.allMock.mockImplementationOnce(() => first.promise).mockImplementationOnce(() => second.promise)
      const state = mounted.instance.setupState

      state[view.change]({ reason: 'query', page: 1, pageSize: 10, query: 'old', filters: view.filters, cursor: null })
      state[view.change]({ reason: 'query', page: 1, pageSize: 10, query: 'new', filters: view.filters, cursor: null })
      first.resolve([{ ...view.pageRows[0], name: `${view.label}-old` }])
      second.resolve([{ ...view.pageRows[0], name: `${view.label}-new` }])
      await flush()

      const rows = view.label === 'services' ? state.services : state.workloads
      expect(rows).toMatchObject([{ name: `${view.label}-new` }])
      expect(state.error).toBeNull()
    } finally {
      mounted.unmount()
    }
  })

  it.each(views)('$label ignores an older failure after a newer request succeeds', async (view) => {
    const mounted = await mount(view.component)
    try {
      await flush()
      const first = deferred<unknown[]>()
      const second = deferred<unknown[]>()
      view.allMock.mockReset()
      view.allMock.mockImplementationOnce(() => first.promise).mockImplementationOnce(() => second.promise)
      const state = mounted.instance.setupState

      state[view.change]({ reason: 'query', page: 1, pageSize: 10, query: 'old', filters: view.filters, cursor: null })
      state[view.change]({ reason: 'query', page: 1, pageSize: 10, query: 'new', filters: view.filters, cursor: null })
      first.reject({ reason: 'ProtocolError', message: 'old request failed' })
      second.resolve([{ ...view.pageRows[0], name: `${view.label}-new` }])
      await flush()

      const rows = view.label === 'services' ? state.services : state.workloads
      expect(rows).toMatchObject([{ name: `${view.label}-new` }])
      expect(state.error).toBeNull()
    } finally {
      mounted.unmount()
    }
  })

  it.each(views)('$label retains the last page while a replacement page is pending or fails', async (view) => {
    const mounted = await mount(view.component)
    try {
      await flush()
      const state = mounted.instance.setupState
      const previous = view.label === 'services' ? state.services : state.workloads
      const pending = deferred<unknown>()
      view.pageMock.mockReset()
      view.pageMock.mockImplementationOnce(() => pending.promise)

      const read = state.refresh()
      await Promise.resolve()
      expect(view.label === 'services' ? state.services : state.workloads).toEqual(previous)
      pending.reject({ reason: 'ProtocolError', message: 'page failed' })
      await read
      expect(view.label === 'services' ? state.services : state.workloads).toEqual(previous)
      expect(state.error).toBe('page failed')
    } finally {
      mounted.unmount()
    }
  })

  it('derives service filter options from the complete catalog and edge fleet', async () => {
    api.fetchServiceCatalog.mockResolvedValue([
      { type: 'generic', displayName: 'Generic HTTP', category: 'Other', auth: 'none', credential: {} },
      { type: 'home', displayName: 'Home Assistant', category: 'Home', auth: 'bearer', credential: {} },
    ])
    api.listEdges.mockResolvedValue([{ ...edge, name: 'edge-a' }, { ...edge, name: 'edge-b' }])
    const mounted = await mount(Services)
    try {
      await flush()
      const state = mounted.instance.setupState
      expect(state.serviceFilters).toMatchObject([
        { key: 'edgeName', options: [{ value: 'edge-a' }, { value: 'edge-b' }] },
        { key: 'typeLabel', options: [{ value: 'Generic HTTP' }, { value: 'Home Assistant' }] },
        { key: 'status', options: [
          { value: 'Pending', label: 'Pending' }, { value: 'Detected', label: 'Detected' },
          { value: 'Ready', label: 'Ready' }, { value: 'Unreachable', label: 'Unreachable' },
        ] },
      ])
    } finally {
      mounted.unmount()
    }
  })

  it('keeps every workload strategy and status option available', async () => {
    const mounted = await mount(Workloads)
    try {
      await flush()
      const state = mounted.instance.setupState
      expect(state.workloadFilters).toEqual([
        { key: 'strategy', label: 'Strategy', options: [{ value: 'Spread', label: 'Spread' }, { value: 'Singleton', label: 'Singleton' }] },
        { key: 'status', label: 'Status', allLabel: 'Any status', options: [
          { value: 'Pending', label: 'Pending' }, { value: 'Running', label: 'Running' },
          { value: 'Failed', label: 'Failed' }, { value: 'Unknown', label: 'Unknown' },
        ] },
      ])
    } finally {
      mounted.unmount()
    }
  })
})
