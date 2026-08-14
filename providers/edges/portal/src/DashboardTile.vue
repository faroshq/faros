<script setup lang="ts">
// Dashboard tile for the edges provider, mounted by
// <faros-dashboard-tile-edges> (see element.ts).
//
// An edge fleet is interesting when something stops reporting, not when
// everything is fine — so this tile inverts the usual "most recent first"
// ordering and lists the edges whose heartbeat is OLDEST. A disconnected edge
// is the row you need; a healthy one is the row you scroll past. The headline
// is therefore "N of M connected" rather than a bare total, because the ratio
// is the fact worth glancing at.

import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { listEdges, setTenant, setToken } from './api'
import type { Edge } from './types'
import {
  TILE_ROWS,
  createTilePoller,
  hasWorkspaceContext,
  isBenignTileError,
  navigateFromTile,
  tileErrorText,
  type TileContext,
  type TilePoller,
} from './portalkit/dashboardtile'

const props = defineProps<{ context: TileContext | null }>()

const rootRef = ref<HTMLElement | null>(null)
const edges = ref<Edge[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
let poller: TilePoller | null = null

const stats = computed(() => {
  const total = edges.value.length
  const connected = edges.value.filter((e) => e.connected).length
  return { total, connected, offline: total - connected }
})

// Disconnected first, then by oldest heartbeat. An edge that has never
// reported (no timestamp) sorts to the very top: it is the most likely to be
// mid-enrolment or broken, and the least likely to be noticed elsewhere.
const rows = computed(() =>
  [...edges.value]
    .sort((a, b) => {
      if (a.connected !== b.connected) return a.connected ? 1 : -1
      return (a.lastHeartbeatTime || '').localeCompare(b.lastHeartbeatTime || '')
    })
    .slice(0, TILE_ROWS),
)

// Heartbeats are the whole point of the list, so render them as an age rather
// than a timestamp nobody can subtract at a glance.
function age(iso?: string): string {
  if (!iso) return 'never'
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return 'unknown'
  const seconds = Math.max(0, Math.round((Date.now() - then) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}

async function load() {
  const ctx = props.context
  if (!hasWorkspaceContext(ctx)) {
    edges.value = []
    error.value = null
    loading.value = false
    return
  }
  setToken(ctx?.token ?? null)
  setTenant(ctx?.tenant ?? null)
  try {
    edges.value = await listEdges()
    error.value = null
  } catch (e) {
    edges.value = []
    error.value = isBenignTileError(e) ? null : tileErrorText(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  poller = createTilePoller(load)
  poller.start()
})
onUnmounted(() => poller?.stop())
watch(() => props.context, () => poller?.refresh())
</script>

<template>
  <div ref="rootRef" class="space-y-3">
    <div v-if="loading" class="text-[11px] text-text-muted">Loading edges&hellip;</div>
    <div v-else-if="error" class="text-[11px] text-danger">Failed to load: {{ error }}</div>

    <template v-else>
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
        <span class="inline-flex items-center gap-1 text-text-primary">
          <span class="font-semibold tabular-nums">{{ stats.connected }}</span>
          <span class="text-text-muted">of</span>
          <span class="tabular-nums">{{ stats.total }}</span>
          <span class="text-text-muted">connected</span>
        </span>
        <span v-if="stats.offline > 0" class="inline-flex items-center gap-1 text-danger">
          <span class="tabular-nums">{{ stats.offline }}</span>
          <span>offline</span>
        </span>
      </div>

      <ul v-if="rows.length" class="space-y-1">
        <li v-for="edge in rows" :key="`${edge.type}/${edge.name}`">
          <button
            type="button"
            class="flex w-full items-center justify-between gap-2 rounded-md px-2 py-1 text-left text-[11px] transition hover:bg-surface-hover"
            @click="navigateFromTile(rootRef, `${edge.name}?type=${edge.type}`)"
          >
            <span class="flex min-w-0 items-center gap-1.5">
              <span
                class="h-1.5 w-1.5 shrink-0 rounded-full"
                :class="edge.connected ? 'bg-success' : 'bg-danger'"
                aria-hidden="true"
              />
              <span class="min-w-0 truncate font-mono text-text-primary">{{ edge.name }}</span>
            </span>
            <span
              class="shrink-0 tabular-nums"
              :class="edge.connected ? 'text-text-muted' : 'text-danger'"
            >{{ age(edge.lastHeartbeatTime) }}</span>
          </button>
        </li>
      </ul>

      <p v-else class="text-[11px] text-text-muted">No edges enrolled yet.</p>
    </template>
  </div>
</template>
