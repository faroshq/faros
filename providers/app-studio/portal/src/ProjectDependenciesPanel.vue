<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { Database, Loader2, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-vue-next'
import { api } from './api'
import ProjectDependencyForm from './ProjectDependencyForm.vue'
import StatusBadge from './portalkit/StatusBadge.vue'
import { confirmDialog } from './portalkit/confirm'
import type { FarosContext, ProjectDependency, ProjectDependencyCatalog, ProjectDependencyMutation, ProjectDevelopmentService } from './types'

const props = defineProps<{ ctx: FarosContext | null; projectName: string }>()
const dependencies = ref<ProjectDependency[]>([])
const catalog = ref<ProjectDependencyCatalog>({ templates: [], targetInterfaces: [] })
const services = ref<ProjectDevelopmentService[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const status = ref('')
const editing = ref<ProjectDependency | null | undefined>(undefined)
let serial = 0

async function load() {
  const projectName = props.projectName
  const current = ++serial
  loading.value = true
  error.value = ''
  try {
    const [dependencyResult, catalogResult, serviceResult] = await Promise.all([
      api.listProjectDependencies(props.ctx, projectName),
      api.getProjectDependencyCatalog(props.ctx, projectName),
      api.listDevelopmentServices(props.ctx, projectName),
    ])
    if (current !== serial || projectName !== props.projectName) return
    dependencies.value = dependencyResult
    catalog.value = catalogResult
    services.value = serviceResult.items ?? []
  } catch (cause) {
    if (current === serial) error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    if (current === serial) loading.value = false
  }
}

watch(() => props.projectName, () => { editing.value = undefined; status.value = ''; void load() })
onMounted(load)

async function save(payload: { name: string; mutation: ProjectDependencyMutation }) {
  busy.value = true
  error.value = ''
  status.value = ''
  try {
    const response = await api.upsertProjectDependency(props.ctx, props.projectName, payload.name, payload.mutation)
    dependencies.value = response.items
    editing.value = undefined
    status.value = `${payload.name} dependency saved. Provisioning continues in the background.`
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    busy.value = false
  }
}

async function remove(dependency: ProjectDependency) {
  const confirmed = await confirmDialog({
    title: `Remove ${dependency.name}?`,
    message: 'Credential delivery will be removed immediately. The stateful provider Instance and its data will be retained and detached from this Project.',
    confirmLabel: 'Remove connection',
    danger: true,
  })
  if (!confirmed) return
  busy.value = true
  error.value = ''
  status.value = ''
  try {
    await api.deleteProjectDependency(props.ctx, props.projectName, dependency.name, dependency.environment)
    dependencies.value = dependencies.value.filter(item => item.name !== dependency.name || item.environment !== dependency.environment)
    status.value = `${dependency.name} disconnected. Its stateful Instance was retained.`
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="grid gap-3 border-t border-border-subtle pt-4" aria-labelledby="project-dependencies-heading" :aria-busy="loading">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex items-start gap-2"><Database class="mt-0.5 h-4 w-4 text-text-muted" :stroke-width="1.75" /><div><h4 id="project-dependencies-heading" class="text-[12px] font-semibold text-text-primary">Durable dependencies</h4><p class="mt-0.5 text-[11px] leading-4 text-text-muted">Provision typed data services without placing credentials in the Project or sandbox configuration. Dependency Instances use <code class="font-mono">Retain</code>.</p></div></div>
      <button type="button" class="inline-flex h-8 items-center gap-1.5 rounded-md border border-border-subtle bg-surface px-2.5 text-[11px] font-medium text-text-secondary hover:bg-surface-hover" :disabled="busy || loading" @click="load"><RefreshCw class="h-3.5 w-3.5" :class="loading ? 'animate-spin' : ''" :stroke-width="1.75" />Refresh</button>
    </div>
    <div v-if="error || status" class="rounded-md border px-3 py-2 text-[12px]" :class="error ? 'border-danger/30 bg-danger-subtle text-danger' : 'border-success/30 bg-success-subtle text-success'" :role="error ? 'alert' : 'status'">{{ error || status }}</div>
    <div v-if="loading" class="flex min-h-20 items-center justify-center gap-2 text-[12px] text-text-muted"><Loader2 class="h-4 w-4 animate-spin" :stroke-width="1.75" />Loading dependencies…</div>
    <ProjectDependencyForm v-else-if="editing !== undefined" :catalog="catalog" :services="services" :dependency="editing" :busy="busy" @save="save" @cancel="editing = undefined" />
    <template v-else>
      <div v-for="dependency in dependencies" :key="`${dependency.environment}:${dependency.name}`" class="grid gap-2 rounded-lg border border-border-subtle bg-surface p-3 sm:grid-cols-[minmax(0,1fr)_auto]">
        <div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><code class="font-mono text-[12px] font-semibold text-text-primary">{{ dependency.name }}</code><StatusBadge :status="dependency.status?.phase || 'Pending'" /><span class="k-badge font-mono">Retain</span></div><p class="mt-1 text-[11px] text-text-secondary"><span class="font-medium">{{ dependency.template }}</span> · <code class="font-mono">{{ dependency.sourceInterface }}</code> → <code class="font-mono">{{ dependency.targetRef.name }}/{{ dependency.targetInterface }}</code></p><p v-if="dependency.status?.message" class="mt-1 text-[10px] leading-4 text-text-muted">{{ dependency.status.message }}</p><p v-if="dependency.status?.revision" class="mt-1 font-mono text-[10px] text-text-muted">revision {{ dependency.status.revision }}</p></div>
        <div class="flex items-start gap-1"><button type="button" class="flex h-8 w-8 items-center justify-center rounded-md text-text-muted hover:bg-surface-hover hover:text-text-primary" :aria-label="`Edit dependency ${dependency.name}`" :disabled="busy" @click="editing = dependency"><Pencil class="h-3.5 w-3.5" :stroke-width="1.75" /></button><button type="button" class="flex h-8 w-8 items-center justify-center rounded-md text-text-muted hover:bg-danger-subtle hover:text-danger" :aria-label="`Remove dependency ${dependency.name}`" :disabled="busy" @click="remove(dependency)"><Trash2 class="h-3.5 w-3.5" :stroke-width="1.75" /></button></div>
      </div>
      <div v-if="!dependencies.length" class="rounded-lg border border-dashed border-border-subtle bg-surface px-4 py-5 text-center"><p class="text-[12px] font-medium text-text-primary">No durable dependencies</p><p class="mt-1 text-[11px] text-text-muted">Add a compatible database, cache, or other provider Template.</p></div>
      <div class="flex justify-end"><button type="button" class="inline-flex h-8 items-center gap-1.5 rounded-md border border-accent/30 bg-accent/10 px-3 text-[12px] font-medium text-accent hover:bg-accent/20 disabled:opacity-50" :disabled="busy || !catalog.templates.length || !services.length" :title="!services.length ? 'Create a development service before connecting a dependency.' : undefined" @click="editing = null"><Plus class="h-3.5 w-3.5" :stroke-width="1.75" />Add dependency</button></div>
    </template>
  </section>
</template>
