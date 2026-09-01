<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Loader2, Plus, Save, X } from 'lucide-vue-next'
import ProductionForm from './ProductionForm.vue'
import { productionFormValuesFromSchema } from './productionForm'
import type { ProjectDependency, ProjectDependencyCatalog, ProjectDependencyMutation, ProjectDevelopmentService } from './types'

const props = withDefaults(defineProps<{
  catalog: ProjectDependencyCatalog
  services: ProjectDevelopmentService[]
  dependency?: ProjectDependency | null
  busy?: boolean
}>(), { dependency: null, busy: false })

const emit = defineEmits<{
  save: [payload: { name: string; mutation: ProjectDependencyMutation }]
  cancel: []
}>()

const form = reactive({ name: '', template: '', targetService: '', targetInterface: '', sourceInterface: '' })
const values = ref<Record<string, unknown>>({})
const valuesValid = ref(true)
const error = ref('')
const editing = computed(() => !!props.dependency)
const selectedTarget = computed(() => props.catalog.targetInterfaces.find(item => item.name === form.targetInterface))
const compatibleTemplates = computed(() => props.catalog.templates.filter(template => template.provides.some(item => item.type === selectedTarget.value?.type)))
const selectedTemplate = computed(() => props.catalog.templates.find(template => template.name === form.template))
const compatibleSources = computed(() => selectedTemplate.value?.provides.filter(item => item.type === selectedTarget.value?.type) ?? [])

function selectDefaults() {
  const dependency = props.dependency
  form.name = dependency?.name ?? ''
  form.targetService = dependency?.targetRef.kind === 'developmentService' ? dependency.targetRef.name : (props.services[0]?.name ?? '')
  form.targetInterface = dependency?.targetInterface ?? props.catalog.targetInterfaces[0]?.name ?? ''
  const target = props.catalog.targetInterfaces.find(item => item.name === form.targetInterface)
  form.template = dependency?.template ?? props.catalog.templates.find(template => template.provides.some(item => item.type === target?.type))?.name ?? ''
  const template = props.catalog.templates.find(item => item.name === form.template)
  form.sourceInterface = dependency?.sourceInterface ?? template?.provides.find(item => item.type === target?.type)?.name ?? ''
  values.value = productionFormValuesFromSchema(template?.schema, dependency?.values ?? template?.sampleValues ?? {})
  error.value = ''
}

watch(() => [props.dependency?.name, props.catalog.templates.length, props.catalog.targetInterfaces.length, props.services.length], selectDefaults, { immediate: true })

watch(() => form.targetInterface, () => {
  if (!compatibleTemplates.value.some(item => item.name === form.template)) form.template = compatibleTemplates.value[0]?.name ?? ''
})

watch(() => form.template, (_current, previous) => {
  const source = compatibleSources.value[0]
  if (!compatibleSources.value.some(item => item.name === form.sourceInterface)) form.sourceInterface = source?.name ?? ''
  if (previous && previous !== form.template) values.value = productionFormValuesFromSchema(selectedTemplate.value?.schema, selectedTemplate.value?.sampleValues ?? {})
})

function submit() {
  const name = form.name.trim()
  if (!name || !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name) || name.length > 63) {
    error.value = 'Use a lowercase dependency name with letters, numbers, and hyphens.'
    return
  }
  if (!form.targetService) {
    error.value = 'Choose a development service to receive this dependency.'
    return
  }
  if (!form.targetInterface || !form.template || !form.sourceInterface || !valuesValid.value) {
    error.value = 'Choose compatible interfaces and complete the provider settings.'
    return
  }
  error.value = ''
  emit('save', {
    name,
    mutation: {
      environment: 'development',
      template: form.template,
      values: values.value,
      sourceInterface: form.sourceInterface,
      targetRef: { kind: 'developmentService', name: form.targetService },
      targetInterface: form.targetInterface,
    },
  })
}
</script>

<template>
  <form class="grid gap-4 rounded-lg border border-accent/30 bg-surface p-3" aria-labelledby="dependency-form-heading" @submit.prevent="submit">
    <div class="flex items-start justify-between gap-3">
      <div><h5 id="dependency-form-heading" class="text-[12px] font-semibold text-text-primary">{{ editing ? `Edit ${dependency?.name}` : 'Add durable dependency' }}</h5><p class="mt-0.5 text-[11px] leading-4 text-text-muted">The stateful Instance is retained when removed. Credential delivery is typed, runtime-only, and cleaned up with the connection.</p></div>
      <button type="button" class="flex h-7 items-center gap-1 rounded-md border border-border-subtle px-2 text-[11px] text-text-secondary hover:bg-surface-hover" :disabled="busy" @click="emit('cancel')"><X class="h-3 w-3" :stroke-width="1.75" />Cancel</button>
    </div>
    <div v-if="error" class="rounded-md border border-danger/30 bg-danger-subtle px-3 py-2 text-[12px] text-danger" role="alert">{{ error }}</div>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="grid gap-1.5"><span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Dependency name</span><input v-model="form.name" :disabled="editing || busy" maxlength="63" required class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 font-mono text-[12px] text-text-primary outline-none focus:border-accent/60 disabled:opacity-60" placeholder="database" /></label>
      <label class="grid gap-1.5"><span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Target service</span><select v-model="form.targetService" :disabled="busy || !services.length" required class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 font-mono text-[12px] text-text-primary outline-none focus:border-accent/60 disabled:opacity-60"><option value="" disabled>{{ services.length ? 'Choose a service' : 'Create a development service first' }}</option><option v-for="service in services" :key="service.name" :value="service.name">{{ service.name }}</option></select></label>
      <label class="grid gap-1.5"><span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Target interface</span><select v-model="form.targetInterface" :disabled="busy || !catalog.targetInterfaces.length" required class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 font-mono text-[12px] text-text-primary outline-none focus:border-accent/60 disabled:opacity-60"><option v-for="item in catalog.targetInterfaces" :key="item.name" :value="item.name">{{ item.name }} · {{ item.type }}</option></select></label>
      <label class="grid gap-1.5"><span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Provider template</span><select v-model="form.template" :disabled="busy || !compatibleTemplates.length" required class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] text-text-primary outline-none focus:border-accent/60 disabled:opacity-60"><option v-for="template in compatibleTemplates" :key="template.name" :value="template.name">{{ template.displayName || template.name }}</option></select></label>
      <label class="grid gap-1.5 sm:col-span-2"><span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Provided interface</span><select v-model="form.sourceInterface" :disabled="busy || !compatibleSources.length" required class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 font-mono text-[12px] text-text-primary outline-none focus:border-accent/60 disabled:opacity-60"><option v-for="item in compatibleSources" :key="item.name" :value="item.name">{{ item.name }} · {{ item.type }}</option></select></label>
    </div>
    <div v-if="selectedTemplate" class="grid gap-2 border-t border-border-subtle pt-3"><div><h6 class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Provider settings</h6><p v-if="selectedTemplate.description" class="mt-1 text-[11px] leading-4 text-text-muted">{{ selectedTemplate.description }}</p></div><ProductionForm :schema="selectedTemplate.schema ?? null" :values="values" :disabled="busy" path-prefix="dependency" @update:values="values = $event" @validity="valuesValid = $event" /></div>
    <div class="flex justify-end border-t border-border-subtle pt-3"><button type="submit" class="inline-flex h-8 items-center gap-1.5 rounded-md bg-accent px-3 text-[11px] font-medium text-white hover:bg-accent-hover disabled:opacity-50" :disabled="busy || !services.length"><Loader2 v-if="busy" class="h-3.5 w-3.5 animate-spin" :stroke-width="1.75" /><Save v-else-if="editing" class="h-3.5 w-3.5" :stroke-width="1.75" /><Plus v-else class="h-3.5 w-3.5" :stroke-width="1.75" />{{ editing ? 'Save dependency' : 'Add dependency' }}</button></div>
  </form>
</template>
