import { computed, type Ref } from 'vue'

import { createKueryApi, type KueryApi, type QuerySpec, type QueryStatus } from './api'
import type { FarosContext } from './element'
import { serviceBase as providerServiceBase } from './portalkit/tenant'

export function serviceBase(context: FarosContext | null): string {
  return providerServiceBase(context?.basePath || '').replace(/\/+$/, '')
}

export function tenantHeaders(context: FarosContext | null): Record<string, string> {
  const headers: Record<string, string> = {}
  if (context?.token) headers.Authorization = `Bearer ${context.token}`
  if (context?.orgUUID) headers['X-Faros-Org'] = context.orgUUID
  if (context?.workspaceUUID) headers['X-Faros-Workspace'] = context.workspaceUUID
  return headers
}

export function useKueryApi(context: Ref<FarosContext | null>): { api: Readonly<Ref<KueryApi | null>>; query: (spec: QuerySpec, signal?: AbortSignal) => Promise<QueryStatus> } {
  const api = computed(() => {
    const basePath = serviceBase(context.value)
    return basePath && context.value?.token
      ? createKueryApi({ basePath, headers: () => tenantHeaders(context.value) })
      : null
  })
  return {
    api,
    query: async (spec, signal) => {
      if (!api.value) throw new Error('Kuery is waiting for workspace context')
      return api.value.query(spec, { signal })
    },
  }
}

export function errorMessage(error: unknown, recovery: string): string {
  if (error instanceof DOMException && error.name === 'AbortError') return ''
  const detail = error instanceof Error ? error.message : String(error)
  return `${detail}. ${recovery}`
}

export function edgeName(cluster = ''): string { return cluster.split('/').pop() || cluster || '—' }

export function resourceLabel(row: { object?: { kind?: string; metadata?: { namespace?: string; name?: string } } }): string {
  const object = row.object ?? {}
  const metadata = object.metadata ?? {}
  return `${object.kind || 'Object'} ${metadata.namespace ? `${metadata.namespace}/` : ''}${metadata.name || '?'}`
}

export function age(timestamp?: string): string {
  if (!timestamp) return '—'
  const milliseconds = Date.now() - new Date(timestamp).getTime()
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return '—'
  const minutes = Math.floor(milliseconds / 60_000)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  return hours < 48 ? `${hours}h` : `${Math.floor(hours / 24)}d`
}
