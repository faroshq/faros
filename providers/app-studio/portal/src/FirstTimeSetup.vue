<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight, Check, ExternalLink, Loader2, RefreshCw } from 'lucide-vue-next'
import type { ProjectCreateReadiness } from './createReadiness'

const props = withDefaults(defineProps<{
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

type SetupState = 'checking' | 'ready' | 'missing' | 'pending' | 'error'

const gitState = computed<SetupState>(() => {
  if (props.gitLoading) return 'checking'
  if (props.gitError) return 'error'
  if (props.readiness?.gitConnection.ready) return 'ready'
  if (props.readiness?.gitConnection.status === 'failed') return 'error'
  if (props.readiness?.gitConnection.status === 'validating') return 'pending'
  return 'missing'
})
const modelState = computed<SetupState>(() => {
  if (props.loading) return 'checking'
  if (props.llmError) return 'error'
  return props.llmConfigured ? 'ready' : 'missing'
})
const activeStep = computed<'git' | 'model'>(() => props.llmConfigured ? 'git' : 'model')
const gitAction = computed(() => {
  switch (props.readiness?.gitConnection.status) {
    case 'provider-missing': return { href: props.codeCatalogUrl, label: 'Enable Code provider' }
    case 'failed': return { href: props.codeConnectionsUrl, label: 'Fix Git connection' }
    case 'validating': return { href: props.codeConnectionsUrl, label: 'View Git connection' }
    default: return { href: props.codeConnectionsUrl, label: 'Connect GitHub' }
  }
})
const retryVisible = computed(() => gitState.value === 'error' || gitState.value === 'pending' || modelState.value === 'error')

function stateLabel(state: SetupState): string {
  if (state === 'ready') return 'Connected'
  if (state === 'checking') return 'Checking'
  if (state === 'pending') return 'Validating'
  if (state === 'error') return 'Check failed'
  return 'Not connected'
}
</script>

<template>
  <section class="mx-auto w-full max-w-[900px] rounded-lg border border-border-subtle bg-surface p-5 sm:p-8" aria-label="App Studio workspace setup">
    <h2 class="text-[18px] font-semibold text-text-primary">Workspace setup</h2>
    <p class="mt-2 text-[12px] leading-5 text-text-secondary">Connect an AI model to start building. Git is optional and recommended.</p>
    <ol class="my-5 flex list-none flex-wrap gap-5 border-y border-border-subtle py-3 text-[12px]">
      <li v-for="item in [{ id: 'model', label: 'AI model', state: modelState }, { id: 'git', label: 'Git (recommended)', state: gitState }]" :key="item.id" :aria-current="activeStep === item.id && !completion ? 'step' : undefined" class="flex items-center gap-2">
        <Check v-if="item.state === 'ready'" class="h-4 w-4 text-success" aria-hidden="true" />
        <Loader2 v-else-if="item.state === 'checking'" class="h-4 w-4 animate-spin motion-reduce:animate-none" aria-hidden="true" />
        <span class="font-medium text-text-primary">{{ item.label }}</span>
        <span class="text-text-secondary">{{ stateLabel(item.state) }}</span>
      </li>
    </ol>
    <div v-if="loading" role="status" aria-live="polite" aria-busy="true" class="py-8 text-[13px] text-text-secondary">Checking AI model setup…</div>
    <div v-else-if="completion" class="grid gap-4">
      <h1 class="text-[26px] font-semibold text-text-primary">App Studio is ready</h1>
      <p class="text-[14px] leading-6 text-text-secondary">Your AI model is connected. You can create a project now and connect Git whenever you are ready.</p>
      <p class="text-[12px] text-text-secondary">AI model: {{ llmModel || 'Connected' }}</p>
      <p v-if="readiness?.gitConnection.ready" class="text-[12px] text-success">GitHub connected: {{ readiness.gitConnection.connectionRef }}</p>
      <div class="flex flex-wrap justify-between gap-3">
        <button type="button" class="k-btn k-btn--ghost" @click="emit('back')">Back to projects</button>
        <button type="button" class="k-btn k-btn--primary" @click="emit('finish')">Create your first project <ArrowRight class="h-4 w-4" /></button>
      </div>
    </div>
    <div v-else-if="activeStep === 'git'" class="grid gap-4">
      <h1 class="text-[26px] font-semibold text-text-primary">Connect Git (recommended)</h1>
      <p class="max-w-[65ch] text-[14px] leading-6 text-text-secondary">Keep an external copy of your source, collaborate through a repository, and use Git-based build workflows. You can also start without Git.</p>
      <p v-if="gitError || gitState === 'error'" class="text-[12px] leading-5 text-danger" role="alert">{{ gitError || readiness?.gitConnection.message || 'The Git connection could not be validated. Review it or skip for now.' }}</p>
      <p v-else-if="gitState === 'pending' || gitState === 'checking'" class="text-[12px] text-text-secondary" role="status">{{ readiness?.gitConnection.message || 'Checking Git connection. You can skip while this completes.' }}</p>
      <div class="flex flex-wrap gap-3">
        <a :href="gitAction.href" target="_blank" rel="noopener noreferrer" class="k-btn k-btn--primary no-underline">{{ gitAction.label }} <ExternalLink class="h-3.5 w-3.5" /></a>
        <button type="button" class="k-btn k-btn--ghost" @click="emit('skipGit')">Skip for now</button>
        <button v-if="retryVisible" type="button" class="k-btn k-btn--ghost" @click="emit('retry')"><RefreshCw class="h-3.5 w-3.5" /> Check again</button>
      </div>
    </div>
    <div v-else class="grid gap-4">
      <p v-if="readiness?.gitConnection.ready" class="text-[12px] text-success">GitHub connected</p>
      <h1 class="text-[26px] font-semibold text-text-primary">Connect an AI model</h1>
      <p class="max-w-[65ch] text-[14px] leading-6 text-text-secondary">Add the provider credential App Studio will use to plan projects, write code, and respond to feedback.</p>
      <p class="text-[12px] leading-5 text-text-secondary">Your credential stays in this workspace. App Studio tests the provider connection before saving it for project creation and chat.</p>
      <p v-if="llmError" class="text-[12px] text-danger" role="alert">{{ llmError }}</p>
      <div class="flex flex-wrap gap-3">
        <button type="button" class="k-btn k-btn--primary" @click="emit('connectModel')">Connect AI model <ArrowRight class="h-4 w-4" /></button>
        <button v-if="retryVisible" type="button" class="k-btn k-btn--ghost" @click="emit('retry')">Check again</button>
      </div>
    </div>
  </section>
</template>
