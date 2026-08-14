<script setup lang="ts">
// Dashboard tile for the code provider, mounted by
// <faros-dashboard-tile-code> (see element.ts).
//
// What a user needs to know about Code at a glance is not "how many
// repositories exist" — it is whether the thing repositories depend on is
// working. A Connection is the credential every repository, package crawl and
// commit rides on; when one stops validating, everything under it fails at
// once and the repository list looks fine right up until you click into it.
// So the headline counts repositories, but the breakdown leads with broken
// connections and the rows carry each repository's own ready state.
//
// Read-only, and silent about a workspace that has not been bootstrapped: see
// portalkit/dashboardtile.

import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, setTenant, setToken } from './api'
import type { Connection, Repository } from './types'
import {
  createTilePoller,
  hasWorkspaceContext,
  isBenignTileError,
  mostRecent,
  navigateFromTile,
  tileErrorText,
  type TileContext,
  type TilePoller,
} from './portalkit/dashboardtile'

const props = defineProps<{ context: TileContext | null }>()

const rootRef = ref<HTMLElement | null>(null)
const repositories = ref<Repository[]>([])
const connections = ref<Connection[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
let poller: TilePoller | null = null

const stats = computed(() => {
  const repos = repositories.value.length
  const broken = connections.value.filter((c) => !c.validated).length
  const notReady = repositories.value.filter((r) => !r.ready).length
  return { repos, connections: connections.value.length, broken, notReady }
})

// Repositories carry no timestamp in the list projection, so order by name for
// a stable list rather than faking recency.
const rows = computed(() => mostRecent(repositories.value, (r) => r.name))

async function load() {
  const ctx = props.context
  if (!hasWorkspaceContext(ctx)) {
    repositories.value = []
    connections.value = []
    error.value = null
    loading.value = false
    return
  }
  setToken(ctx?.token ?? null)
  setTenant(ctx?.tenant ?? null)
  try {
    // Both lists in parallel: the tile is worthless without the connection
    // health, and serialising doubles the time the card sits on "Loading".
    const [repos, conns] = await Promise.all([api.listRepositories(), api.listConnections()])
    repositories.value = repos
    connections.value = conns
    error.value = null
  } catch (e) {
    repositories.value = []
    connections.value = []
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
    <div v-if="loading" class="text-[11px] text-text-muted">Loading repositories&hellip;</div>
    <div v-else-if="error" class="text-[11px] text-danger">Failed to load: {{ error }}</div>

    <template v-else>
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
        <span class="inline-flex items-center gap-1 text-text-primary">
          <span class="font-semibold tabular-nums">{{ stats.repos }}</span>
          <span class="text-text-muted">{{ stats.repos === 1 ? 'repository' : 'repositories' }}</span>
        </span>
        <span class="inline-flex items-center gap-1 text-text-muted">
          <span class="tabular-nums">{{ stats.connections }}</span>
          <span>{{ stats.connections === 1 ? 'connection' : 'connections' }}</span>
        </span>
        <!-- A broken connection is the failure everything else inherits, so it
             is the one number that earns colour on this tile. -->
        <span v-if="stats.broken > 0" class="inline-flex items-center gap-1 text-danger">
          <span class="tabular-nums">{{ stats.broken }}</span>
          <span>not validated</span>
        </span>
        <span v-if="stats.notReady > 0" class="inline-flex items-center gap-1 text-warning">
          <span class="tabular-nums">{{ stats.notReady }}</span>
          <span>not ready</span>
        </span>
      </div>

      <ul v-if="rows.length" class="space-y-1">
        <li v-for="repo in rows" :key="repo.name">
          <button
            type="button"
            class="flex w-full items-center justify-between gap-2 rounded-md px-2 py-1 text-left text-[11px] transition hover:bg-surface-hover"
            @click="navigateFromTile(rootRef, `repositories/${repo.name}`)"
          >
            <span class="min-w-0 truncate font-mono text-text-primary">{{ repo.repo || repo.name }}</span>
            <span
              class="shrink-0 tabular-nums"
              :class="repo.ready ? 'text-success' : 'text-warning'"
            >{{ repo.ready ? 'ready' : 'pending' }}</span>
          </button>
        </li>
      </ul>

      <p v-else-if="stats.connections === 0" class="text-[11px] text-text-muted">
        No git connection yet — connect a provider to create repositories.
      </p>
      <p v-else class="text-[11px] text-text-muted">No repositories yet.</p>
    </template>
  </div>
</template>
