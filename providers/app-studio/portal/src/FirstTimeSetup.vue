<script setup lang="ts">
import { ArrowRight } from 'lucide-vue-next'
import type { ProjectCreateReadiness } from './createReadiness'
import GitRecommendationBanner from './GitRecommendationBanner.vue'

withDefaults(defineProps<{
  readiness: ProjectCreateReadiness | null
  llmConfigured: boolean
  llmModel?: string
  loading: boolean
  gitError?: string
  llmError?: string
  completion?: boolean
  gitLoading?: boolean
  codeConnectionsUrl: string
  codeCatalogUrl: string
}>(), { llmModel: '', gitError: '', llmError: '', completion: false })

const emit = defineEmits<{ connectModel: []; retry: []; finish: []; back: []; skipGit: [] }>()

</script>

<template>
  <section class="mx-auto w-full max-w-[900px] rounded-lg border border-border-subtle bg-surface p-5 sm:p-8" aria-label="App Studio workspace setup">
    <h2 class="text-[18px] font-semibold text-text-primary">Workspace setup</h2>
    <p class="mt-2 mb-6 text-[12px] leading-5 text-text-secondary">Connect an AI model to start building. Git is optional and recommended.</p>

    <h1 v-if="!loading" class="mb-4 text-[26px] font-semibold text-text-primary">{{ completion ? 'App Studio is ready' : llmConfigured ? 'Connect Git (recommended)' : 'Connect an AI model' }}</h1>
    <div v-if="loading" role="status" aria-live="polite" aria-busy="true" class="py-8 text-[13px] text-text-secondary">Checking AI model setup…</div>
    <div v-else-if="completion" class="grid gap-4">
      <p class="text-[14px] leading-6 text-text-secondary">Your AI model is connected. Start building and connect Git whenever you are ready.</p>
      <p class="text-[12px] text-text-secondary">AI model: {{ llmModel || 'Connected' }}</p>
      <p v-if="readiness?.gitConnection.ready" class="text-[12px] text-success">GitHub connected: {{ readiness.gitConnection.connectionRef }}</p>
      <div class="flex flex-wrap justify-between gap-3">
        <button type="button" class="k-btn k-btn--ghost" @click="emit('back')">Back to projects</button>
        <button type="button" class="k-btn k-btn--primary" @click="emit('finish')">Create your first project <ArrowRight class="h-4 w-4" /></button>
      </div>
    </div>
    <div v-else-if="llmConfigured" class="grid gap-4">
      <GitRecommendationBanner :readiness="readiness" :checking="!!gitLoading" :error="gitError" :connection-url="codeConnectionsUrl" :catalog-url="codeCatalogUrl" @retry="emit('retry')">
        <button type="button" class="k-btn k-btn--text" @click="emit('skipGit')">Skip for now</button>
      </GitRecommendationBanner>
    </div>
    <div v-else class="grid gap-4">
      <p v-if="readiness?.gitConnection.ready" class="text-[12px] text-success">GitHub connected</p>
      <p class="max-w-[65ch] text-[14px] leading-6 text-text-secondary">Add the provider credential App Studio will use to plan projects, write code, and respond to feedback.</p>
      <p class="text-[12px] leading-5 text-text-secondary">Credentials stay in this workspace. App Studio tests the provider connection before saving.</p>
      <p v-if="llmError" class="text-[12px] text-danger" role="alert">{{ llmError }}</p>
      <div class="flex flex-wrap gap-3">
        <button type="button" class="k-btn k-btn--primary" @click="emit('connectModel')">Connect AI model <ArrowRight class="h-4 w-4" /></button>
        <button v-if="llmError" type="button" class="k-btn k-btn--ghost" @click="emit('retry')">Check again</button>
      </div>
    </div>
  </section>
</template>
