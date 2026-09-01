<script setup lang="ts">
import { Boxes, Loader2 } from 'lucide-vue-next'
import type { ProjectBuildComponent, ProjectComponent, ProjectComponentMapping, ProductionTemplate, ProductionTemplateComponent } from './types'

defineProps<{
  templates: ProductionTemplate[]
  selectedTemplate: string
  projectComponents: ProjectComponent[]
  buildComponents: ProjectBuildComponent[]
  targetComponents: ProductionTemplateComponent[]
  componentMappings: ProjectComponentMapping[]
  loading?: boolean
  busy?: boolean
  error?: string | null
}>()

const emit = defineEmits<{
  'update:template': [name: string]
  'update:mapping': [payload: { targetComponent: string; componentRef: string }]
  retry: []
}>()
</script>

<template>
  <section class="grid gap-3" aria-labelledby="production-target-heading" :aria-busy="loading">
    <div class="flex items-start gap-2">
      <Boxes class="mt-0.5 h-4 w-4 shrink-0 text-text-muted" :stroke-width="1.75" />
      <div><h3 id="production-target-heading" class="text-[12px] font-semibold text-text-primary">Production target</h3><p class="mt-0.5 text-[11px] leading-4 text-text-muted">Choose the infrastructure Template and explicitly connect each target runtime to a built Project component.</p></div>
    </div>

    <label class="grid gap-1.5">
      <span class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Infrastructure template</span>
      <span class="relative block">
        <Loader2 v-if="loading" class="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 animate-spin text-text-muted" :stroke-width="1.75" />
        <Boxes v-else class="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-text-muted" :stroke-width="1.75" />
        <select :value="selectedTemplate" class="h-9 w-full appearance-none rounded-md border border-border-subtle bg-surface py-0 pl-9 pr-3 text-[12px] text-text-primary outline-none focus:border-accent/50 disabled:opacity-60" :disabled="loading || busy || templates.length === 0" @change="emit('update:template', ($event.target as HTMLSelectElement).value)">
          <option value="" disabled>Select a production template</option>
          <option v-if="selectedTemplate && !templates.some((template) => template.name === selectedTemplate)" :value="selectedTemplate">{{ selectedTemplate }}</option>
          <option v-for="template in templates" :key="template.name" :value="template.name">{{ template.displayName || template.name }}</option>
        </select>
      </span>
      <span v-if="selectedTemplate" class="text-[10px] text-text-muted">Template identity: <code class="font-mono">{{ selectedTemplate }}</code></span>
    </label>

    <div v-if="error" class="flex flex-wrap items-center gap-2 rounded-md border border-danger/30 bg-danger-subtle px-3 py-2 text-[12px] text-danger" role="alert"><span>{{ error }}</span><button type="button" class="font-medium underline underline-offset-2" @click="emit('retry')">Retry</button></div>
    <p v-else-if="!loading && templates.length === 0" class="rounded-md border border-warning/30 bg-warning-subtle px-3 py-2 text-[11px] leading-4 text-warning">No production-compatible Templates expose a launchable image input.</p>

    <fieldset v-if="selectedTemplate && targetComponents.length" class="grid gap-2 border-t border-border-subtle pt-3">
      <legend class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Component mappings</legend>
      <div v-for="target in targetComponents" :key="target.name" class="grid items-center gap-2 rounded-md border border-border-subtle bg-surface-overlay p-2 sm:grid-cols-[minmax(0,1fr)_18px_minmax(0,1fr)]">
        <div class="min-w-0"><div class="font-mono text-[12px] font-medium text-text-primary">{{ target.name }}</div><div class="truncate font-mono text-[10px] text-text-muted">input {{ target.imageInput }}</div></div>
        <span class="hidden text-center text-text-muted sm:block" aria-hidden="true">←</span>
        <label class="grid gap-1"><span class="sr-only">Project component for {{ target.name }}</span><select :value="componentMappings.find((mapping) => mapping.targetComponent === target.name)?.componentRef ?? ''" class="h-8 rounded-md border border-border-subtle bg-surface px-2 text-[12px] text-text-primary outline-none focus:border-accent/50 disabled:opacity-60" :disabled="busy" @change="emit('update:mapping', { targetComponent: target.name, componentRef: ($event.target as HTMLSelectElement).value })"><option value="">Select Project component</option><option v-for="component in projectComponents" :key="component.name" :value="component.name" :disabled="!buildComponents.some((build) => build.name === component.name)">{{ component.name }} · {{ component.kind }}</option></select></label>
      </div>
      <p class="text-[10px] leading-4 text-text-muted">Mappings are stored on the Project production binding. App Studio does not infer or persist a mapping you have not selected.</p>
    </fieldset>
  </section>
</template>
