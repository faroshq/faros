<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Plus, Trash2 } from 'lucide-vue-next'
import type { ProjectComponent, ProjectComponentKind, ProjectComponentPort, ProjectComponentProtocol } from './types'

const props = defineProps<{
  component?: ProjectComponent | null
  busy?: boolean
}>()

const emit = defineEmits<{
  save: [component: ProjectComponent]
  cancel: []
}>()

const name = ref('')
const kind = ref<ProjectComponentKind>('Service')
const sourcePath = ref('.')
const buildEnabled = ref(false)
const contextPath = ref('.')
const dockerfilePath = ref('Dockerfile')
const ports = ref<ProjectComponentPort[]>([])

const editing = computed(() => Boolean(props.component))
const valid = computed(() => {
  if (!/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name.value.trim())) return false
  if (!sourcePath.value.trim()) return false
  if (buildEnabled.value && (!contextPath.value.trim() || !dockerfilePath.value.trim())) return false
  return ports.value.every((port) =>
    /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(port.name.trim()) &&
    Number.isInteger(port.containerPort) && port.containerPort >= 1 && port.containerPort <= 65535,
  ) && new Set(ports.value.map((port) => port.name.trim())).size === ports.value.length
})

function hydrate(component?: ProjectComponent | null) {
  name.value = component?.name ?? ''
  kind.value = component?.kind ?? 'Service'
  sourcePath.value = component?.sourcePath ?? '.'
  buildEnabled.value = Boolean(component?.build)
  contextPath.value = component?.build?.contextPath ?? component?.sourcePath ?? '.'
  dockerfilePath.value = component?.build?.dockerfilePath ?? 'Dockerfile'
  ports.value = (component?.ports ?? []).map((port) => ({ ...port }))
}

watch(() => props.component, hydrate, { immediate: true })

function addPort() {
  ports.value = [...ports.value, { name: `http-${ports.value.length + 1}`, protocol: 'HTTP', containerPort: 3000 }]
}

function updatePort(index: number, field: keyof ProjectComponentPort, value: string | number) {
  ports.value = ports.value.map((port, candidate) => candidate === index
    ? { ...port, [field]: field === 'containerPort' ? Number(value) : value }
    : port)
}

function removePort(index: number) {
  ports.value = ports.value.filter((_, candidate) => candidate !== index)
}

function submit() {
  if (!valid.value || props.busy) return
  emit('save', {
    name: name.value.trim(),
    kind: kind.value,
    sourcePath: sourcePath.value.trim(),
    ...(buildEnabled.value ? { build: { contextPath: contextPath.value.trim(), dockerfilePath: dockerfilePath.value.trim() } } : {}),
    ...(kind.value === 'Service' && ports.value.length
      ? { ports: ports.value.map((port) => ({ ...port, name: port.name.trim() })) }
      : {}),
  })
}
</script>

<template>
  <form class="grid gap-4 rounded-lg border border-border-subtle bg-surface p-3" :aria-busy="busy" @submit.prevent="submit">
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="grid gap-1.5">
        <span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Component name</span>
        <input v-model="name" class="h-9 rounded-md border border-border-subtle bg-surface-overlay px-2.5 font-mono text-[13px] text-text-primary outline-none focus:border-accent/50 disabled:opacity-60" placeholder="web" :disabled="busy || editing" required pattern="[a-z0-9]([-a-z0-9]*[a-z0-9])?" />
        <span class="text-[10px] leading-4 text-text-muted">Stable DNS-style identity. Names cannot be changed after creation.</span>
      </label>
      <label class="grid gap-1.5">
        <span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Runtime role</span>
        <select v-model="kind" class="h-9 rounded-md border border-border-subtle bg-surface-overlay px-2.5 text-[13px] text-text-primary outline-none focus:border-accent/50 disabled:opacity-60" :disabled="busy">
          <option value="Service">Service</option>
          <option value="Worker">Worker</option>
        </select>
      </label>
    </div>

    <label class="grid gap-1.5">
      <span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Source path</span>
      <input v-model="sourcePath" class="h-9 rounded-md border border-border-subtle bg-surface-overlay px-2.5 font-mono text-[13px] text-text-primary outline-none focus:border-accent/50 disabled:opacity-60" placeholder="apps/web" :disabled="busy" required />
      <span class="text-[10px] leading-4 text-text-muted">Repository-relative directory owned by this component.</span>
    </label>

    <fieldset class="grid gap-3 border-t border-border-subtle pt-3">
      <label class="flex items-center gap-2 text-[12px] font-medium text-text-secondary">
        <input v-model="buildEnabled" type="checkbox" class="h-4 w-4 accent-accent" :disabled="busy" />
        Build a container image for production
      </label>
      <div v-if="buildEnabled" class="grid gap-3 sm:grid-cols-2">
        <label class="grid gap-1.5"><span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Build context</span><input v-model="contextPath" class="h-9 rounded-md border border-border-subtle bg-surface-overlay px-2.5 font-mono text-[13px] text-text-primary outline-none focus:border-accent/50" required /></label>
        <label class="grid gap-1.5"><span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Dockerfile</span><input v-model="dockerfilePath" class="h-9 rounded-md border border-border-subtle bg-surface-overlay px-2.5 font-mono text-[13px] text-text-primary outline-none focus:border-accent/50" required /></label>
      </div>
    </fieldset>

    <fieldset v-if="kind === 'Service'" class="grid gap-3 border-t border-border-subtle pt-3">
      <div class="flex items-center justify-between gap-3">
        <div><legend class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Declared ports</legend><p class="mt-1 text-[10px] leading-4 text-text-muted">Production network contract; development listeners are configured separately.</p></div>
        <button type="button" class="inline-flex h-8 items-center gap-1.5 rounded-md border border-border-subtle bg-surface-overlay px-2.5 text-[11px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary" :disabled="busy || ports.length >= 32" @click="addPort"><Plus class="h-3.5 w-3.5" :stroke-width="1.75" />Add port</button>
      </div>
      <div v-for="(port, index) in ports" :key="index" class="grid gap-2 rounded-md border border-border-subtle bg-surface-overlay p-2 sm:grid-cols-[minmax(0,1fr)_110px_110px_32px]">
        <label class="grid gap-1"><span class="sr-only">Port name</span><input :value="port.name" class="h-8 rounded-md border border-border-subtle bg-surface px-2 font-mono text-[12px] text-text-primary outline-none focus:border-accent/50" placeholder="http" required @input="updatePort(index, 'name', ($event.target as HTMLInputElement).value)" /></label>
        <label class="grid gap-1"><span class="sr-only">Protocol</span><select :value="port.protocol" class="h-8 rounded-md border border-border-subtle bg-surface px-2 text-[12px] text-text-primary outline-none focus:border-accent/50" @change="updatePort(index, 'protocol', ($event.target as HTMLSelectElement).value as ProjectComponentProtocol)"><option>HTTP</option><option>HTTPS</option><option>TCP</option></select></label>
        <label class="grid gap-1"><span class="sr-only">Container port</span><input :value="port.containerPort" type="number" min="1" max="65535" class="h-8 rounded-md border border-border-subtle bg-surface px-2 font-mono text-[12px] text-text-primary outline-none focus:border-accent/50" required @input="updatePort(index, 'containerPort', ($event.target as HTMLInputElement).value)" /></label>
        <button type="button" class="flex h-8 w-8 items-center justify-center rounded-md text-text-muted hover:bg-danger-subtle hover:text-danger" :aria-label="`Remove port ${port.name || index + 1}`" @click="removePort(index)"><Trash2 class="h-3.5 w-3.5" :stroke-width="1.75" /></button>
      </div>
      <p v-if="!ports.length" class="text-[11px] text-text-muted">No production ports declared.</p>
    </fieldset>

    <div class="flex justify-end gap-2 border-t border-border-subtle pt-3">
      <button type="button" class="h-8 rounded-md border border-border-subtle bg-surface-overlay px-3 text-[12px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary" :disabled="busy" @click="emit('cancel')">Cancel</button>
      <button type="submit" class="h-8 rounded-md border border-accent bg-accent px-3 text-[12px] font-semibold text-on-accent shadow-[0_0_14px_var(--color-accent-glow)] hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-60" :disabled="busy || !valid">{{ editing ? 'Save component' : 'Add component' }}</button>
    </div>
  </form>
</template>
