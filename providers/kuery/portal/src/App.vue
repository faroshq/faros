<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import type { ObjectResult } from './api'
import type { FarosContext } from './element'
import { errorMessage, serviceBase, tenantHeaders } from './kuery'
import ImpactView from './components/ImpactView.vue'
import InventoryView from './components/InventoryView.vue'
import PlaygroundView from './components/PlaygroundView.vue'
import TopologyView from './components/TopologyView.vue'
import Tabs from './portalkit/Tabs.vue'

const props = defineProps<{ state: { context: FarosContext | null } }>()
const context = computed(() => props.state.context)
const identity = computed(() => `${context.value?.basePath || ''}|${context.value?.orgUUID || ''}|${context.value?.workspaceUUID || ''}`)
const tokenReady = computed(() => !!context.value?.token)
const active = ref<'topology' | 'inventory' | 'playground'>('topology')
const impact = ref<ObjectResult | null>(null)
const edges = ref<string[]>([])
const edgesLoaded = ref(false)
const edgesLoading = ref(false)
const edgesError = ref('')
let edgesController: AbortController | null = null

const tabs = [
  { id: 'topology', label: 'Topology' },
  { id: 'inventory', label: 'Inventory' },
  { id: 'playground', label: 'Playground' },
]

function selectTab(id: string): void {
  if (id === 'topology' || id === 'inventory' || id === 'playground') active.value = id
}

async function loadEdges(): Promise<void> {
  edgesController?.abort()
  edgesController = null
  const base = serviceBase(context.value)
  if (!base || !context.value?.token) { edgesLoading.value = false; return }
  const controller = new AbortController()
  edgesController = controller
  edgesLoading.value = true
  edgesError.value = ''
  try {
    const response = await fetch(`${base}/api/edges`, {
      credentials: 'same-origin', headers: tenantHeaders(context.value), signal: controller.signal,
    })
    const body = await response.text()
    if (!response.ok) throw new Error(`Edge discovery failed (${response.status}): ${body.slice(0, 200)}`)
    const parsed = body ? JSON.parse(body) as { edges?: string[] } : {}
    edges.value = parsed.edges ?? []
    edgesLoaded.value = true
  } catch (error) {
    const message = errorMessage(error, 'Retry edge discovery.')
    if (message) edgesError.value = message
  } finally {
    if (edgesController === controller) { edgesLoading.value = false; edgesController = null }
  }
}

watch(identity, () => {
  impact.value = null
  edges.value = []
  edgesLoaded.value = false
  edgesError.value = ''
  void loadEdges()
}, { immediate: true })

watch(tokenReady, (ready, wasReady) => { if (ready && !wasReady) void loadEdges() })
onBeforeUnmount(() => { edgesController?.abort(); edgesController = null })
</script>

<template>
  <div class="kuery-shell">
    <ImpactView v-if="impact" :key="`${identity}:impact`" :context="context" :anchor="impact" @back="impact = null" @inspect="impact = $event" />
    <div v-show="!impact" class="kuery-collection-surfaces">
      <div class="kuery-topbar">
        <Tabs :tabs="tabs" :active="active" aria-label="Kuery views" @select="selectTab" />
        <span class="k-badge" :class="edges.length ? 'k-badge--success' : 'k-badge--warning'">
          {{ edgesLoading && !edgesLoaded ? 'Discovering edges' : `${edges.length} edge${edges.length === 1 ? '' : 's'} engaged` }}
        </span>
      </div>
      <div v-if="edgesError" class="kuery-inline-error" role="alert">
        <span>{{ edgesError }}</span><button type="button" class="k-btn k-btn--ghost" @click="loadEdges">Retry</button>
      </div>
      <TopologyView v-show="active === 'topology'" :key="`${identity}:topology`" :context="context" :edges="edges" :active="!impact && active === 'topology'" @inspect="impact = $event" />
      <InventoryView v-show="active === 'inventory'" :key="`${identity}:inventory`" :context="context" :edges="edges" @inspect="impact = $event" />
      <PlaygroundView v-show="active === 'playground'" :key="`${identity}:playground`" :context="context" :active="!impact && active === 'playground'" />
    </div>
  </div>
</template>
