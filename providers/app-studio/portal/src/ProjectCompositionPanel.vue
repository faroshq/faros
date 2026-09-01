<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { Boxes, Check, GitBranch, Loader2, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-vue-next'
import { api } from './api'
import ProjectComponentEditor from './ProjectComponentEditor.vue'
import ProjectDependenciesPanel from './ProjectDependenciesPanel.vue'
import { confirmDialog } from './portalkit/confirm'
import type { FarosContext, Project, ProjectBuildConfiguration, ProjectComponent } from './types'

const props = defineProps<{
  ctx: FarosContext | null
  project: Project
}>()

const emit = defineEmits<{
  updated: [payload: { components: ProjectComponent[]; build: ProjectBuildConfiguration }]
}>()

const components = ref<ProjectComponent[]>([])
const build = ref<ProjectBuildConfiguration>({})
const loading = ref(false)
const busy = ref(false)
const error = ref<string | null>(null)
const status = ref<string | null>(null)
const editing = ref<ProjectComponent | null | undefined>(undefined)
const workflowPath = ref('')
let loadSerial = 0

function notifyUpdated() {
  emit('updated', { components: [...components.value], build: { ...build.value } })
}

async function load() {
  const projectName = props.project.name
  const serial = ++loadSerial
  loading.value = true
  error.value = null
  try {
    const [componentResult, buildResult] = await Promise.all([
      api.listProjectComponents(props.ctx, projectName),
      api.getProjectBuildConfiguration(props.ctx, projectName),
    ])
    if (serial !== loadSerial || props.project.name !== projectName) return
    components.value = componentResult
    build.value = buildResult
    workflowPath.value = buildResult.workflowPath ?? ''
    notifyUpdated()
  } catch (err) {
    if (serial !== loadSerial || props.project.name !== projectName) return
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    if (serial === loadSerial) loading.value = false
  }
}

watch(() => props.project.name, () => {
  editing.value = undefined
  status.value = null
  void load()
})

onMounted(load)

function beginAdd() {
  error.value = null
  status.value = null
  editing.value = null
}

function beginEdit(component: ProjectComponent) {
  error.value = null
  status.value = null
  editing.value = { ...component, build: component.build ? { ...component.build } : undefined, ports: component.ports?.map((port) => ({ ...port })) }
}

async function saveComponent(component: ProjectComponent) {
  const projectName = props.project.name
  busy.value = true
  error.value = null
  status.value = null
  try {
    const response = await api.upsertProjectComponent(props.ctx, projectName, component.name, {
      kind: component.kind,
      sourcePath: component.sourcePath,
      build: component.build,
      ports: component.ports,
    })
    if (props.project.name !== projectName) return
    components.value = response.components
    editing.value = undefined
    status.value = `Component ${component.name} saved.`
    notifyUpdated()
  } catch (err) {
    if (props.project.name === projectName) error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}

async function removeComponent(component: ProjectComponent) {
  const confirmed = await confirmDialog({
    title: `Delete ${component.name}?`,
    message: 'This removes the component declaration, not its source files. Development services must stop referencing it first.',
    confirmLabel: 'Delete component',
    danger: true,
  })
  if (!confirmed) return
  const projectName = props.project.name
  busy.value = true
  error.value = null
  status.value = null
  try {
    await api.deleteProjectComponent(props.ctx, projectName, component.name)
    if (props.project.name !== projectName) return
    components.value = components.value.filter((candidate) => candidate.name !== component.name)
    status.value = `Component ${component.name} deleted.`
    notifyUpdated()
  } catch (err) {
    if (props.project.name === projectName) error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}

function workflowValid(value: string): boolean {
  return /^\.github\/workflows\/[^/]+\.ya?ml$/.test(value.trim())
}

async function saveWorkflow(clear = false) {
  const normalized = workflowPath.value.trim()
  if (!clear && !workflowValid(normalized)) {
    error.value = 'Workflow path must name a .yml or .yaml file directly under .github/workflows/.'
    return
  }
  const projectName = props.project.name
  busy.value = true
  error.value = null
  status.value = null
  try {
    const response = await api.setProjectBuildConfiguration(props.ctx, projectName, clear ? null : normalized)
    if (props.project.name !== projectName) return
    build.value = response
    workflowPath.value = response.workflowPath ?? ''
    status.value = clear ? 'Build workflow cleared.' : 'Build workflow saved.'
    notifyUpdated()
  } catch (err) {
    if (props.project.name === projectName) error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="grid gap-4 rounded-lg border border-border-subtle bg-surface-overlay/40 p-3" aria-labelledby="project-composition-heading" :aria-busy="loading">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex min-w-0 items-start gap-2.5">
        <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-border-subtle bg-surface"><Boxes class="h-4 w-4 text-text-muted" :stroke-width="1.75" /></div>
        <div><h3 id="project-composition-heading" class="text-[12px] font-semibold text-text-primary">Application composition</h3><p class="mt-0.5 text-[11px] leading-4 text-text-muted">Declare stable source and build units. Development services and production targets refer to these identities.</p></div>
      </div>
      <button type="button" class="inline-flex h-8 items-center gap-1.5 rounded-md border border-border-subtle bg-surface px-2.5 text-[11px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:opacity-60" :disabled="busy || loading" @click="load"><RefreshCw class="h-3.5 w-3.5" :class="loading ? 'animate-spin' : ''" :stroke-width="1.75" />Refresh</button>
    </div>

    <div v-if="error || status" class="rounded-md border px-3 py-2 text-[12px]" :class="error ? 'border-danger/30 bg-danger-subtle text-danger' : 'border-success/30 bg-success-subtle text-success'" :role="error ? 'alert' : 'status'" aria-live="polite">{{ error || status }}</div>

    <div v-if="loading" class="flex min-h-24 items-center justify-center gap-2 text-[12px] text-text-muted"><Loader2 class="h-4 w-4 animate-spin" :stroke-width="1.75" />Loading composition…</div>
    <template v-else>
      <ProjectComponentEditor v-if="editing !== undefined" :component="editing" :busy="busy" @save="saveComponent" @cancel="editing = undefined" />
      <div v-else class="grid gap-2">
        <div v-for="component in components" :key="component.name" class="grid gap-2 rounded-lg border border-border-subtle bg-surface p-3 sm:grid-cols-[minmax(0,1fr)_auto]">
          <div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><code class="font-mono text-[12px] font-semibold text-text-primary">{{ component.name }}</code><span class="k-badge">{{ component.kind }}</span></div><div class="mt-1 truncate font-mono text-[11px] text-text-muted">{{ component.sourcePath }}</div><div v-if="component.build" class="mt-1 text-[10px] text-text-muted">Build <code class="font-mono">{{ component.build.contextPath }}</code> with <code class="font-mono">{{ component.build.dockerfilePath }}</code></div><div v-if="component.ports?.length" class="mt-2 flex flex-wrap gap-1"><span v-for="port in component.ports" :key="port.name" class="k-badge font-mono">{{ port.name }} · {{ port.protocol }} {{ port.containerPort }}</span></div></div>
          <div class="flex items-start gap-1"><button type="button" class="flex h-8 w-8 items-center justify-center rounded-md text-text-muted hover:bg-surface-hover hover:text-text-primary" :aria-label="`Edit component ${component.name}`" :disabled="busy" @click="beginEdit(component)"><Pencil class="h-3.5 w-3.5" :stroke-width="1.75" /></button><button type="button" class="flex h-8 w-8 items-center justify-center rounded-md text-text-muted hover:bg-danger-subtle hover:text-danger" :aria-label="`Delete component ${component.name}`" :disabled="busy" @click="removeComponent(component)"><Trash2 class="h-3.5 w-3.5" :stroke-width="1.75" /></button></div>
        </div>
        <div v-if="!components.length" class="rounded-lg border border-dashed border-border-subtle bg-surface p-4 text-center"><p class="text-[12px] font-medium text-text-primary">No components declared</p><p class="mt-1 text-[11px] text-text-muted">Add each service or worker that will be built and deployed.</p></div>
        <div class="flex justify-end"><button type="button" class="inline-flex h-8 items-center gap-1.5 rounded-md border border-accent/30 bg-accent/10 px-3 text-[12px] font-medium text-accent hover:bg-accent/20 disabled:opacity-60" :disabled="busy || components.length >= 64" @click="beginAdd"><Plus class="h-3.5 w-3.5" :stroke-width="1.75" />Add component</button></div>
      </div>

      <form class="grid gap-3 border-t border-border-subtle pt-4" aria-labelledby="build-workflow-heading" @submit.prevent="saveWorkflow(false)">
        <div class="flex items-start gap-2"><GitBranch class="mt-0.5 h-4 w-4 shrink-0 text-text-muted" :stroke-width="1.75" /><div><h4 id="build-workflow-heading" class="text-[12px] font-semibold text-text-primary">Repository build workflow</h4><p class="mt-0.5 text-[11px] leading-4 text-text-muted">App Studio observes and dispatches this existing workflow; it never creates or edits the file.</p></div></div>
        <label class="grid gap-1.5"><span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Workflow path</span><input v-model="workflowPath" class="h-9 rounded-md border border-border-subtle bg-surface px-2.5 font-mono text-[12px] text-text-primary outline-none placeholder:text-text-muted focus:border-accent/50 disabled:opacity-60" placeholder=".github/workflows/build.yaml" :disabled="busy" aria-describedby="build-workflow-help" /><span id="build-workflow-help" class="text-[10px] leading-4 text-text-muted">Must be a <code class="font-mono">.yml</code> or <code class="font-mono">.yaml</code> file directly under <code class="font-mono">.github/workflows/</code>.</span></label>
        <div class="flex flex-wrap justify-end gap-2"><button v-if="build.workflowPath" type="button" class="h-8 rounded-md border border-border-subtle bg-surface px-3 text-[12px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:opacity-60" :disabled="busy" @click="saveWorkflow(true)">Clear workflow</button><button type="submit" class="inline-flex h-8 items-center gap-1.5 rounded-md border border-accent/30 bg-accent/10 px-3 text-[12px] font-medium text-accent hover:bg-accent/20 disabled:opacity-60" :disabled="busy || !workflowValid(workflowPath)"><Check class="h-3.5 w-3.5" :stroke-width="1.75" />Save workflow</button></div>
      </form>

		<ProjectDependenciesPanel :ctx="ctx" :project-name="project.name" />
    </template>
  </section>
</template>
