<script setup lang="ts">
import { computed } from 'vue'
import { Check, Cpu, KeyRound, Loader2, Pencil, Plus, RefreshCw, Star, Trash2 } from 'lucide-vue-next'
import ModelIDSelector from './ModelIDSelector.vue'
import ModelConnectionCard from './portalkit/ModelConnectionCard.vue'
import ModelUsageSection from './portalkit/ModelUsageSection.vue'
import type { LLMProviderPreset } from './llmDiscovery'
import type { ProjectLLMDiscoveredModel, ProjectLLMSettings } from './types'

type LLMCredentialMode = 'api-key' | 'service-account-json'

const props = defineProps<{
  modelTests?: Record<string, { state: string; tone: 'success' | 'danger' | 'muted'; error?: string }>
  settings: ProjectLLMSettings | null
  loading: boolean
  loadError: string | null
  saving: boolean
  status: string | null
  actionError: string | null
  editorOpen: boolean
  creationRoute: boolean
  editingModelID: string | null
  name: string
  provider: string
  providerPreset: LLMProviderPreset
  credentialMode: LLMCredentialMode
  baseURL: string
  model: string
  apiKey: string
  nameError: string
  baseURLError: string
  modelError: string
  credentialError: string
  credentialRequired: boolean
  baseURLPlaceholder: string
  apiKeyPlaceholder: string
  apiKeyHint: string
  providerGuidance: string
  modelHint: string
  googleProvider: boolean
  googleServiceAccountMode: boolean
  customProvider: boolean
  discoveredModels: ProjectLLMDiscoveredModel[]
  discoveryLoading: boolean
  discoveryError: string | null
  discoveryStatus: string | null
  canDiscover: boolean
  testing: boolean
  testStatus: string | null
  testError: string | null
  requireConnectionTest: boolean
  connectionTested: boolean
}>()

const recommendedDiscoveredModels = computed(() => props.discoveredModels.filter((model) => model.compatibility === 'recommended').slice(0, 4))

const emit = defineEmits<{
  retry: []
  openEditor: [modelID?: string]
  cancelEditor: []
  save: []
  test: []
  testSaved: [modelID: string]
  delete: [modelID: string]
  setDefault: [modelID: string]
  selectProvider: [provider: LLMProviderPreset]
  discover: []
  selectDiscoveredModel: [model: ProjectLLMDiscoveredModel]
  'update:name': [value: string]
  'update:credentialMode': [mode: LLMCredentialMode]
  'update:baseURL': [value: string]
  'update:model': [value: string]
  'update:apiKey': [value: string]
}>()
</script>

<template>
  <section class="grid gap-4" :aria-label="creationRoute ? 'Connect model' : 'Models'">
    <div v-if="!creationRoute && !editorOpen && !(loading && !settings) && (settings?.models.length ?? 0) > 0" class="flex justify-end">
      <span
        class="inline-flex"
        :title="(settings?.models.length ?? 0) >= 20 ? 'This workspace already has the maximum of 20 models.' : undefined"
      >
        <button
          type="button"
          class="k-btn k-btn--primary shrink-0"
          :disabled="(settings?.models.length ?? 0) >= 20"
          @click="emit('openEditor')"
        >
          <Plus class="h-4 w-4" :stroke-width="1.75" />
          Connect model
        </button>
      </span>
    </div>

    <div v-if="loading && !settings && !creationRoute" class="grid min-h-48 content-start gap-3 rounded-md border border-dashed border-border-subtle bg-surface p-4" role="status" aria-live="polite" aria-busy="true">
      <div class="shimmer h-4 w-36 rounded bg-surface-overlay" />
      <div class="shimmer h-24 w-full rounded bg-surface-overlay" />
      <div class="text-[12px] text-text-muted">Loading models…</div>
    </div>
    <div v-else-if="loadError && !settings && !creationRoute" class="flex min-h-48 flex-col items-start justify-center gap-2 rounded-md border border-danger/30 bg-danger-subtle p-4 text-[12px] text-danger" role="alert">
      <div>{{ loadError }}</div>
      <button type="button" class="k-btn k-btn--ghost" @click="emit('retry')">Retry</button>
    </div>

    <template v-else>
      <div v-if="loading" class="flex items-center gap-2 text-[11px] text-text-muted" role="status" aria-live="polite" aria-busy="true">
        <Loader2 class="h-3.5 w-3.5 animate-spin text-accent" :stroke-width="1.75" />
        Refreshing models…
      </div>
      <div v-if="loadError" class="k-inline-notification k-inline-notification--error" role="alert">
        <span>{{ loadError }}</span>
        <button type="button" class="k-btn k-btn--ghost" @click="emit('retry')">Retry</button>
      </div>
      <div v-if="actionError" class="k-inline-notification k-inline-notification--error" role="alert">{{ actionError }}</div>
      <div v-else-if="status" class="k-inline-notification k-inline-notification--success" role="status" aria-live="polite">{{ status }}</div>
      <div v-if="testError" class="k-inline-notification k-inline-notification--error" role="alert">{{ testError }}</div>
      <div v-else-if="testStatus" class="k-inline-notification k-inline-notification--success" role="status" aria-live="polite"><Check class="h-3.5 w-3.5" :stroke-width="2" />{{ testStatus }}</div>

      <div v-if="settings?.models.length && !creationRoute && !editorOpen" class="k-model-grid">
        <ModelConnectionCard v-for="saved in settings.models" :key="saved.id" :name="saved.name" :model="saved.model" :endpoint="saved.baseURL" :configured="saved.configured" :is-default="saved.default" :busy="saving"
          :test-state="modelTests?.[saved.id]?.state" :test-tone="modelTests?.[saved.id]?.tone">
          <p v-if="saved.catalog" class="text-[11px] text-text-secondary">${{ saved.catalog.inputPer1M }} input · ${{ saved.catalog.outputPer1M }} output<br />USD per 1M tokens · catalog estimate</p><p v-else class="text-[11px] text-text-secondary">Pricing unavailable</p>
          <p class="text-[11px] text-text-secondary">{{ saved.default ? 'Default for new projects' : 'Available in project model pickers' }}</p>
          <p v-if="modelTests?.[saved.id]?.error" class="k-inline-notification k-inline-notification--error" role="alert">{{ modelTests[saved.id].error }}</p>
          <template #actions>

            <button type="button" class="k-btn k-btn--ghost" :disabled="saving" @click="emit('openEditor', saved.id)">
              <Pencil class="h-3.5 w-3.5" :stroke-width="1.75" /> Edit
            </button>
            <button type="button" class="k-btn k-btn--ghost" :disabled="saving || !saved.configured || modelTests?.[saved.id]?.state === 'Testing…'" @click="emit('testSaved', saved.id)">Test connection</button>
            <span
              v-if="!saved.default"
              class="inline-flex"
              :title="!saved.configured ? 'Add a credential before making this model the default.' : undefined"
            >
              <button
                type="button"
                class="k-btn k-btn--ghost"
                :disabled="saving || !saved.configured"
                :aria-label="!saved.configured ? `Make ${saved.name} default unavailable: add a credential first` : `Make ${saved.name} default`"
                @click="emit('setDefault', saved.id)"
              >
                <Star class="h-3.5 w-3.5" :stroke-width="1.75" /> Make default
              </button>
            </span>
            <button type="button" class="k-icon-action" :disabled="saving" :aria-label="`Delete ${saved.name}`" @click="emit('delete', saved.id)">
              <Trash2 class="h-3.5 w-3.5" :stroke-width="1.75" />
            </button>
          </template>
        </ModelConnectionCard>
      </div>

      <div v-else-if="!editorOpen && !creationRoute" class="flex min-h-44 flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border-subtle bg-surface px-5 py-8 text-center">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg border border-border-subtle bg-surface-overlay text-text-muted"><Cpu class="h-5 w-5" :stroke-width="1.75" /></div>
        <div>
          <h4 class="text-[13px] font-semibold text-text-primary">No models configured</h4>
          <p class="mt-1 max-w-md text-[12px] leading-5 text-text-muted">Connect a provider endpoint and credential before creating or chatting in projects.</p>
        </div>
        <button type="button" class="k-btn k-btn--primary" @click="emit('openEditor')">
          <Plus class="h-4 w-4" :stroke-width="1.75" /> Connect model
        </button>
      </div>

      <ModelUsageSection v-if="!editorOpen && !creationRoute && settings?.models.length" provider="App Studio" />

      <form v-if="editorOpen || creationRoute" class="k-create-surface" :class="{ 'k-create-surface--wide': creationRoute }" aria-label="Model configuration form" :aria-busy="saving" novalidate @submit.prevent="emit('save')">
        <div class="k-create-body">
          <div v-if="!creationRoute" class="flex flex-wrap items-start gap-3">
            <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-border-subtle bg-surface text-text-muted"><KeyRound class="h-4 w-4" :stroke-width="1.75" /></div>
            <div class="min-w-0">
              <h4 class="text-[13px] font-semibold text-text-primary">{{ editingModelID ? 'Edit model' : 'Connect model' }}</h4>
              <p class="mt-0.5 text-[11px] leading-4 text-text-muted">Give this connection a recognizable name, then configure its endpoint and workspace credential.</p>
            </div>
          </div>

          <label for="model-display-name" class="grid gap-1.5 text-[11px] font-medium text-text-secondary">
            Display name
            <input
              id="model-display-name"
              :value="name"
              class="k-input h-10"
              :class="nameError ? 'border-danger' : ''"
              placeholder="e.g. GPT-5.6 High"
              maxlength="80"
              :disabled="saving"
              :aria-invalid="Boolean(nameError)"
              aria-required="true"
              :aria-describedby="nameError ? 'model-display-name-error' : 'model-display-name-hint'"
              @input="emit('update:name', ($event.target as HTMLInputElement).value)"
            />
            <span v-if="nameError" id="model-display-name-error" class="text-[11px] font-normal leading-4 text-danger" role="alert">{{ nameError }}</span>
            <span v-else id="model-display-name-hint" class="text-[11px] font-normal leading-4 text-text-muted">Use a name people can recognize in project and chat model pickers.</span>
          </label>

          <section class="grid gap-3 border-t border-border-subtle pt-4" aria-labelledby="model-provider-heading">
            <h5 id="model-provider-heading" class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">Connection</h5>
            <div class="grid gap-3 sm:grid-cols-2">
              <label for="model-provider" class="grid min-w-0 gap-1.5 text-[11px] font-medium text-text-secondary">Provider
                <select id="model-provider" :value="providerPreset" class="k-input" :disabled="saving" aria-describedby="model-provider-hint" @change="emit('selectProvider', ($event.target as HTMLSelectElement).value as LLMProviderPreset)"><option value="openai">OpenAI</option><option value="google">Google AI Studio</option><option value="custom">Custom OpenAI-compatible</option></select>
                <span id="model-provider-hint" class="text-[11px] font-normal leading-4 text-text-muted">{{ providerGuidance }}</span>
              </label>
              <label v-if="googleProvider" for="model-credential-method" class="grid min-w-0 content-start gap-1.5 text-[11px] font-medium text-text-secondary">Credential method
                <select id="model-credential-method" :value="credentialMode" class="k-input" :disabled="saving" @change="emit('update:credentialMode', ($event.target as HTMLSelectElement).value as LLMCredentialMode)"><option value="api-key">Gemini API key</option><option value="service-account-json">Vertex AI service account</option></select>
              </label>
              <label v-else-if="customProvider" for="model-base-url" class="grid min-w-0 gap-1.5 text-[11px] font-medium text-text-secondary">Base URL
                <input id="model-base-url" :value="baseURL" class="k-input h-10 min-w-0 font-mono text-[12px]" :class="baseURLError ? 'border-danger' : ''" :placeholder="baseURLPlaceholder" :disabled="saving" :aria-invalid="Boolean(baseURLError)" aria-required="true" aria-describedby="model-base-url-help" type="url" @input="emit('update:baseURL', ($event.target as HTMLInputElement).value)" />
                <span id="model-base-url-help" class="text-[11px] font-normal leading-4" :class="baseURLError ? 'text-danger' : 'text-text-muted'" :role="baseURLError ? 'alert' : undefined">{{ baseURLError || 'App Studio adds /chat/completions and queries /models.' }}</span>
              </label>
              <div v-else class="grid min-w-0 content-start gap-1.5 text-[11px] font-medium text-text-secondary">
                API endpoint
                <div class="k-input font-mono text-[11px] text-text-muted">
                  <span class="truncate" :title="baseURL">{{ baseURL }}</span>
                </div>
              </div>
            </div>
          </section>

          <section class="grid gap-2 border-t border-border-subtle pt-4" aria-labelledby="model-credential-heading">
            <h5 id="model-credential-heading" class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">Credential</h5>
            <label for="model-credential" class="sr-only">{{ googleServiceAccountMode ? 'Service account JSON' : 'API key' }}</label>
            <textarea v-if="googleServiceAccountMode" id="model-credential" :value="apiKey" class="k-input min-h-[140px] resize-y font-mono text-[12px] leading-5" :class="credentialError ? 'border-danger' : ''" :placeholder="apiKeyPlaceholder" autocomplete="off" :disabled="saving" :aria-invalid="Boolean(credentialError)" :aria-required="credentialRequired" aria-describedby="model-credential-help" @input="emit('update:apiKey', ($event.target as HTMLTextAreaElement).value)" />
            <input v-else id="model-credential" :value="apiKey" class="k-input h-10" :class="credentialError ? 'border-danger' : ''" :placeholder="editingModelID && !credentialRequired ? `${apiKeyPlaceholder} (leave blank to keep current)` : apiKeyPlaceholder" type="password" autocomplete="new-password" :disabled="saving" :aria-invalid="Boolean(credentialError)" :aria-required="credentialRequired" aria-describedby="model-credential-help" @input="emit('update:apiKey', ($event.target as HTMLInputElement).value)" />
            <p id="model-credential-help" class="text-[11px] leading-4" :class="credentialError ? 'text-danger' : 'text-text-muted'" :role="credentialError ? 'alert' : undefined">{{ credentialError || apiKeyHint }}</p>
          </section>

          <section class="grid gap-3 border-t border-border-subtle pt-4" aria-labelledby="model-selection-heading">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h5 id="model-selection-heading" class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">Model</h5>
              <span class="inline-flex" :title="!canDiscover ? (googleServiceAccountMode ? 'Vertex AI model discovery is not available yet.' : 'Enter a credential before finding models.') : undefined">
                <button type="button" class="app-studio-touch-target k-btn k-btn--ghost h-8 px-2.5 text-[11px]" :disabled="saving || discoveryLoading || !canDiscover" @click="emit('discover')">
                  <Loader2 v-if="discoveryLoading" class="h-3.5 w-3.5 animate-spin" :stroke-width="1.75" />
                  <RefreshCw v-else class="h-3.5 w-3.5" :stroke-width="1.75" />
                  {{ discoveryLoading ? 'Finding models…' : 'Find models' }}
                </button>
              </span>
            </div>
            <label for="model-id" class="grid min-w-0 content-start gap-1.5 text-[11px] font-medium text-text-secondary">Model ID
              <ModelIDSelector
                :model-value="model"
                :models="discoveredModels"
                :disabled="saving"
                :invalid="Boolean(modelError)"
                :described-by="modelError ? 'model-id-error' : 'model-id-hint'"
                @update:model-value="emit('update:model', $event)"
                @select="emit('selectDiscoveredModel', $event)"
              />
              <span v-if="modelError" id="model-id-error" class="text-[11px] font-normal leading-4 text-danger" role="alert">{{ modelError }}</span>
              <span v-else id="model-id-hint" class="text-[11px] font-normal leading-4 text-text-muted">{{ modelHint }} Find models to load the full catalog, then search or enter an ID.</span>
            </label>
            <div v-if="recommendedDiscoveredModels.length" class="grid gap-2" aria-label="Recommended models">
              <span class="text-[9px] font-semibold uppercase tracking-wide text-text-muted">Recommended for App Studio</span>
              <div class="flex flex-wrap gap-2">
                <button v-for="available in recommendedDiscoveredModels" :key="available.id" type="button" class="k-btn k-btn--ghost" :disabled="saving" @click="emit('selectDiscoveredModel', available)">
                  {{ available.name }}
                </button>
              </div>
            </div>
            <p v-if="discoveryError" class="k-inline-notification k-inline-notification--error" role="alert">{{ discoveryError }} You can still enter a model ID manually.</p>
            <p v-else-if="discoveryStatus" class="text-[11px] leading-4 text-text-muted" role="status" aria-live="polite">{{ discoveryStatus }}</p>
          </section>

        </div>
        <p class="px-4 text-[11px] text-text-secondary">Testing sends a small model request and may incur a charge.</p>
        <footer class="k-create-actions">
          <button type="button" class="k-btn k-btn--ghost" :disabled="saving || testing" @click="emit('cancelEditor')">Cancel</button>
          <button type="button" class="k-btn k-btn--ghost" :disabled="saving || testing || !model.trim() || (credentialRequired && !apiKey.trim()) || Boolean(baseURLError)" @click="emit('test')">
            <Loader2 v-if="testing" class="h-4 w-4 animate-spin motion-reduce:animate-none" :stroke-width="1.75" />
            <Check v-else-if="connectionTested" class="h-4 w-4 text-success" :stroke-width="2" />
            <RefreshCw v-else class="h-4 w-4" :stroke-width="1.75" />
            {{ testing ? 'Testing…' : connectionTested ? 'Connection verified' : 'Test connection' }}
          </button>
          <button class="k-btn k-btn--primary" :disabled="saving || testing || (requireConnectionTest && !connectionTested)">
            <Loader2 v-if="saving" class="h-4 w-4 animate-spin" :stroke-width="1.75" /><Check v-else class="h-4 w-4" :stroke-width="1.75" />
            {{ saving ? (editingModelID ? 'Saving changes…' : 'Connecting model…') : editingModelID ? 'Save changes' : 'Connect model' }}
          </button>
        </footer>
      </form>
    </template>
  </section>
</template>
