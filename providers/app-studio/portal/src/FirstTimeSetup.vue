<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight } from 'lucide-vue-next'
import type { ProjectCreateReadiness } from './createReadiness'
import GitRecommendationBanner from './GitRecommendationBanner.vue'

const props = defineProps<{
  readiness: ProjectCreateReadiness | null
  llmConfigured: boolean
  llmModel?: string
  loading: boolean
  gitError?: string
  llmError?: string
  completion?: boolean
  gitLoading?: boolean
  gitSkipped?: boolean
  codeConnectionsUrl: string
  codeCatalogUrl: string
}>()

const gitStep = computed(() => !props.readiness?.gitConnection.ready && !props.gitSkipped)
const currentStep = computed(() => props.completion ? 2 : gitStep.value ? 0 : 1)
const steps = computed(() => [
  { label: 'Connect Git', description: props.readiness?.gitConnection.ready ? 'Connected' : props.gitSkipped ? 'Skipped for now' : 'Recommended' },
  { label: 'AI model', description: props.llmConfigured ? (props.llmModel || 'Connected') : 'Required' },
])
const emit = defineEmits<{ connectModel: []; retry: []; finish: []; back: []; skipGit: []; revisitGit: [] }>()

</script>

<template>
  <section class="mx-auto w-full max-w-[900px] rounded-lg border border-border-subtle bg-surface p-5 sm:p-8" aria-label="App Studio workspace setup">
    <h2 class="text-[18px] font-semibold text-text-primary">Workspace setup</h2>
    <ol class="k-first-run__journey my-6" aria-label="Workspace setup progress">
      <li v-for="(step, index) in steps" :key="step.label" class="k-first-run__step" :class="{ 'is-current': index === currentStep, 'is-complete': index < currentStep }" :aria-current="index === currentStep ? 'step' : undefined">
        <span class="k-first-run__marker" aria-hidden="true">{{ index + 1 }}</span>
        <span class="k-first-run__step-copy"><strong>{{ step.label }}</strong><small>{{ step.description }}</small><button v-if="index === 0 && gitSkipped && !readiness?.gitConnection.ready" type="button" class="k-btn k-btn--text justify-self-start" @click="emit('revisitGit')">Back to Git</button></span>
      </li>
    </ol>
    <h1 v-if="!gitStep" class="mb-4 text-[26px] font-semibold text-text-primary">{{ completion ? 'App Studio is ready' : 'Connect an AI model' }}</h1>
    <GitRecommendationBanner v-if="gitStep" :readiness="readiness" :checking="!!gitLoading" :error="gitError" :connection-url="codeConnectionsUrl" :catalog-url="codeCatalogUrl" @retry="emit('retry')">
      <button type="button" class="k-btn k-btn--text" @click="emit('skipGit')">Skip for now</button>
    </GitRecommendationBanner>
    <div v-else-if="loading" role="status" aria-live="polite" aria-busy="true" class="py-8 text-[13px] text-text-secondary">Checking AI model setup…</div>
    <div v-else-if="completion" class="flex flex-wrap justify-between gap-3">
        <button type="button" class="k-btn k-btn--ghost" @click="emit('back')">Back to projects</button>
        <button type="button" class="k-btn k-btn--primary" @click="emit('finish')">Create your first project <ArrowRight class="h-4 w-4" /></button>
    </div>
    <div v-else-if="!llmConfigured" class="grid gap-4">
        <p class="text-[14px] leading-6 text-text-secondary">Connect a model to plan and build your projects.</p>
        <p class="text-[12px] leading-5 text-text-secondary">Credentials stay in this workspace and are tested before saving.</p>
        <p v-if="llmError" class="text-[12px] text-danger" role="alert">{{ llmError }}</p>
        <div class="flex flex-wrap gap-3">
          <button type="button" class="k-btn k-btn--primary" @click="emit('connectModel')">Connect AI model</button>
          <button v-if="llmError" type="button" class="k-btn k-btn--ghost" @click="emit('retry')">Check again</button>
        </div>
    </div>
  </section>
</template>
