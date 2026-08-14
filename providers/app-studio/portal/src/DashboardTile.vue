<script setup lang="ts">
// Dashboard tile for App Studio, mounted by
// <faros-dashboard-tile-app-studio> (see element.ts).
//
// App Studio's tile answers "where was I" more than "what exists", so it
// orders by updatedAt and shows each project's two runtime states side by
// side: the development preview and production. Those are the two questions a
// user actually has about a project — is my preview up, and is the promoted
// version live — and they live in different environments, so the tile reads
// them off the environment list rather than the project phase.

import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from './api'
import type { FarosContext, Project, ProjectEnvironment } from './types'
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
const projects = ref<Project[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
let poller: TilePoller | null = null

function environment(project: Project, name: string): ProjectEnvironment | undefined {
  return (project.environments ?? []).find((env) => env.name === name)
}

// A binding phase of Ready is the only state that means "you can open it".
// Anything else — provisioning, failed, or an environment that was never
// bound — reads as not ready, because from the dashboard they are the same
// action: go look at the project.
function environmentReady(project: Project, name: string): boolean {
  const env = environment(project, name)
  if (!env) return false
  const bindings = env.bindings ?? []
  if (bindings.length === 0) return (env.phase ?? '') === 'Ready'
  return bindings.some((b) => (b.phase ?? '') === 'Ready')
}

const stats = computed(() => {
  const total = projects.value.length
  const previewReady = projects.value.filter((p) => environmentReady(p, 'development')).length
  const productionReady = projects.value.filter((p) => environmentReady(p, 'production')).length
  return { total, previewReady, productionReady }
})

const rows = computed(() =>
  mostRecent(projects.value, (p) => p.updatedAt || p.createdAt).map((project) => ({
    project,
    preview: environmentReady(project, 'development'),
    production: !!environment(project, 'production') && environmentReady(project, 'production'),
    promoted: !!environment(project, 'production'),
  })),
)

async function load() {
  const ctx = props.context
  if (!hasWorkspaceContext(ctx)) {
    projects.value = []
    error.value = null
    loading.value = false
    return
  }
  try {
    // The App Studio client takes the context per call rather than through
    // module-level setters, so the tile passes its own — no shared mutable
    // state with the full provider app when both are mounted.
    projects.value = await api.listProjects(ctx as FarosContext)
    error.value = null
  } catch (e) {
    projects.value = []
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
    <div v-if="loading" class="text-[11px] text-text-muted">Loading projects&hellip;</div>
    <div v-else-if="error" class="text-[11px] text-danger">Failed to load: {{ error }}</div>

    <template v-else>
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
        <span class="inline-flex items-center gap-1 text-text-primary">
          <span class="font-semibold tabular-nums">{{ stats.total }}</span>
          <span class="text-text-muted">{{ stats.total === 1 ? 'project' : 'projects' }}</span>
        </span>
        <span class="inline-flex items-center gap-1 text-text-muted">
          <span class="tabular-nums">{{ stats.previewReady }}</span>
          <span>preview up</span>
        </span>
        <span v-if="stats.productionReady > 0" class="inline-flex items-center gap-1 text-success">
          <span class="tabular-nums">{{ stats.productionReady }}</span>
          <span>in production</span>
        </span>
      </div>

      <ul v-if="rows.length" class="space-y-1">
        <li v-for="row in rows" :key="row.project.name">
          <button
            type="button"
            class="flex w-full items-center justify-between gap-2 rounded-md px-2 py-1 text-left text-[11px] transition hover:bg-surface-hover"
            @click="navigateFromTile(rootRef, row.project.name)"
          >
            <span class="min-w-0 truncate text-text-primary">
              {{ row.project.displayName || row.project.name }}
            </span>
            <span class="flex shrink-0 items-center gap-1.5">
              <span
                class="rounded px-1 py-px text-[10px] uppercase tracking-wide"
                :class="row.preview ? 'bg-success/15 text-success' : 'bg-surface-hover text-text-muted'"
              >dev</span>
              <!-- A project that was never promoted has no production state to
                   report, so it shows nothing rather than a misleading red. -->
              <span
                v-if="row.promoted"
                class="rounded px-1 py-px text-[10px] uppercase tracking-wide"
                :class="row.production ? 'bg-success/15 text-success' : 'bg-warning/15 text-warning'"
              >prod</span>
            </span>
          </button>
        </li>
      </ul>

      <p v-else class="text-[11px] text-text-muted">No projects yet — create one to get started.</p>
    </template>
  </div>
</template>
