<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Check, Loader2, RefreshCw } from 'lucide-vue-next'
import type { ApiClient } from '../api'
import type { Credential, CredentialWrite, CredentialTestResult } from '../types'
import { PROVIDER_PRESETS } from '../conn-defs'
import FormSelect from '../portalkit/FormSelect.vue'
import ModelIDSelector from '../portalkit/ModelIDSelector.vue'
import { confirmDialog } from '../portalkit/confirm'

const props = defineProps<{ api: ApiClient; credential?: Credential; busy: boolean; error?: string | null }>()
const emit = defineEmits<{ save: [body: CredentialWrite, result: CredentialTestResult]; cancel: [] }>()
const name = ref(props.credential?.name || '')
const provider = props.credential?.provider || 'openai-compatible'
const baseURL = ref(props.credential?.baseURL || PROVIDER_PRESETS[0].baseURL)
const model = ref(props.credential?.model || '')
const apiKey = ref('')
const preset = ref(PROVIDER_PRESETS.find(item => item.baseURL === baseURL.value)?.id || 'custom')
const options = PROVIDER_PRESETS.map(item => ({ value: item.id, label: item.label }))
const models = ref<string[]>([])
const discovering = ref(false)
const testing = ref(false)
const discoveryError = ref('')
const testError = ref('')
const validationError = ref('')
const testedFingerprint = ref('')
const testResult = ref<CredentialTestResult>({ ok: false, latencyMS: 0 })
let generation = 0
const fingerprint = computed(() => JSON.stringify([provider, baseURL.value.trim(), model.value.trim(), apiKey.value.trim()]))
const baseline = fingerprint.value
const verified = computed(() => testedFingerprint.value === fingerprint.value)
const locked = computed(() => props.busy || testing.value || discovering.value)
const discoveredModels = computed(() => models.value.map(id => ({ id, name: id, compatibility: 'available' as const })))
const credentialChanged = computed(() => !props.credential || baseURL.value.replace(/\/+$/, '') !== (props.credential.baseURL || PROVIDER_PRESETS[0].baseURL).replace(/\/+$/, ''))
watch(fingerprint, () => { generation++; testedFingerprint.value = ''; testError.value = ''; validationError.value = '' })
watch([baseURL, apiKey], () => { models.value = []; discoveryError.value = '' })
onBeforeUnmount(() => { generation++; apiKey.value = '' })
function draft() { return { provider, baseURL: baseURL.value.trim(), model: model.value.trim(), apiKey: apiKey.value.trim(), ...(props.credential ? { existingName: props.credential.name } : {}) } }
function valid(requireModel: boolean): boolean {
 validationError.value = ''
 try { const url = new URL(baseURL.value); if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error() }
 catch { validationError.value = 'Enter a valid HTTP or HTTPS API endpoint.' }
 if (!apiKey.value.trim() && (credentialChanged.value || props.credential?.hasAPIKey === false)) validationError.value = 'Enter an API key for this endpoint.'
 if (requireModel && !model.value.trim()) validationError.value = 'Choose or enter a model ID.'
 return !validationError.value
}
async function probe(discover: boolean) {
 if (locked.value || !valid(!discover)) return
 const serial = ++generation
 const snapshot = fingerprint.value
 if (discover) { discovering.value = true; discoveryError.value = '' } else { testing.value = true; testError.value = ''; testedFingerprint.value = '' }
 try {
  const result = await (discover ? props.api.discoverCredentialDraft(draft()) : props.api.testCredentialDraft(draft()))
  if (serial !== generation || snapshot !== fingerprint.value) return
  if (!result.ok) throw new Error(result.error || 'The provider did not confirm this connection.')
  if (discover) models.value = result.models || []
  else { testedFingerprint.value = snapshot; testResult.value = result }
 } catch (error) {
  if (serial !== generation) return
  if (discover) discoveryError.value = (error as Error).message
  else testError.value = (error as Error).message
 } finally { if (serial === generation) { discovering.value = false; testing.value = false } }
}
function submit() {
 if (locked.value || !verified.value || !valid(true)) return
 if (!props.credential && !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(name.value.trim())) { validationError.value = 'Use lowercase letters and numbers separated by hyphens for the name.'; return }
 emit('save', { name: name.value.trim(), provider, baseURL: baseURL.value.trim(), model: model.value.trim(), ...(apiKey.value.trim() ? { apiKey: apiKey.value.trim() } : {}) }, testResult.value)
}
async function cancel() {
 if (locked.value) return
 if ((fingerprint.value !== baseline || name.value !== (props.credential?.name || '')) && !await confirmDialog({ title: 'Discard model changes?', message: 'Your unsaved model connection changes will be lost.', confirmLabel: 'Discard changes' })) return
 emit('cancel')
}
defineExpose({ cancel, locked })
</script>
<template>
 <form class="agents-model-create k-create-surface k-create-surface--wide k-model-form" aria-label="Model configuration form" :aria-busy="locked" @submit.prevent="submit">
  <div class="k-create-body">
   <label class="k-model-form-field">Name
    <input v-model="name" class="k-input" name="name" required pattern="[a-z0-9]+(-[a-z0-9]+)*" :disabled="locked || !!credential" placeholder="e.g. everyday" />
    <span class="k-model-form-hint">Use lowercase letters, numbers, and hyphens. The name is used in agent assignments.</span>
   </label>
   <section class="k-model-form-section" aria-labelledby="agents-model-provider-heading">
    <h5 id="agents-model-provider-heading" class="k-model-form-heading">Connection</h5>
    <div class="k-model-form-columns">
     <label class="k-model-form-field"><span id="agents-model-provider-label">Provider</span><FormSelect :model-value="preset" :options="options" labelledby="agents-model-provider-label" :disabled="locked" @update:model-value="preset = $event; baseURL = PROVIDER_PRESETS.find(item => item.id === $event)?.baseURL || baseURL" /><span class="k-model-form-hint">Use an OpenAI-compatible provider or gateway.</span></label>
     <label class="k-model-form-field">{{ preset === 'custom' ? 'Base URL' : 'API endpoint' }}<input v-model="baseURL" class="k-input mono" name="baseURL" type="url" required :readonly="preset !== 'custom'" :disabled="locked" /><span v-if="preset === 'custom'" class="k-model-form-hint">Agents adds /chat/completions and queries /models.</span></label>
    </div>
   </section>
   <section class="k-model-form-section" aria-labelledby="agents-model-credential-heading">
    <h5 id="agents-model-credential-heading" class="k-model-form-heading">Credential</h5>
    <label for="agents-model-key" class="sr-only">API key</label>
    <input id="agents-model-key" v-model="apiKey" class="k-input" name="apiKey" type="password" autocomplete="new-password" :required="credentialChanged" :disabled="locked" :placeholder="credential ? 'API key (leave blank to keep current)' : 'API key'" aria-describedby="agents-model-key-help" />
    <p id="agents-model-key-help" class="k-model-form-hint">{{ credential ? 'The saved key can only be reused with the same provider endpoint.' : 'Stored for this workspace and never returned to the browser.' }}</p>
   </section>
   <section class="k-model-form-section" aria-labelledby="agents-model-selection-heading">
    <div class="k-model-form-row"><h5 id="agents-model-selection-heading" class="k-model-form-heading">Model</h5><button class="k-btn k-btn--ghost" type="button" :disabled="locked || (!apiKey.trim() && (credentialChanged || credential?.hasAPIKey === false))" @click="probe(true)"><Loader2 v-if="discovering" :stroke-width="1.75" /><RefreshCw v-else :stroke-width="1.75" />{{ discovering ? 'Finding models…' : 'Find models' }}</button></div>
    <label for="model-id" class="k-model-form-field">Model ID<ModelIDSelector v-model="model" :models="discoveredModels" :disabled="locked" described-by="agents-model-help" /><span id="agents-model-help" class="k-model-form-hint">Use the exact model identifier. Find models to load the full catalog, then search or enter an ID.</span></label>
    <p v-if="discoveryError" class="k-inline-notification k-inline-notification--error" role="alert">{{ discoveryError }} You can still enter a model ID manually.</p>
   </section>
   <p v-if="validationError || error" class="k-inline-notification k-inline-notification--error" role="alert">{{ validationError || error }}</p>
   <p v-if="testError" class="k-inline-notification k-inline-notification--error" role="alert">{{ testError }}</p>
   <p v-else-if="verified" class="k-inline-notification k-inline-notification--success" role="status">Connection verified. The model responded successfully.</p>
  </div>
  <p class="k-model-form-test-hint">Testing sends a small model request and may incur a charge.</p>
  <footer class="k-create-actions"><button type="button" class="k-btn k-btn--ghost" :disabled="locked" @click="cancel">Cancel</button><button type="button" class="k-btn k-btn--ghost" :disabled="locked || !model.trim()" @click="probe(false)"><Loader2 v-if="testing" :stroke-width="1.75" /><Check v-else-if="verified" :stroke-width="1.75" /><RefreshCw v-else :stroke-width="1.75" />{{ testing ? 'Testing…' : verified ? 'Connection verified' : 'Test connection' }}</button><button type="submit" class="k-btn k-btn--primary" :disabled="locked || !verified"><Loader2 v-if="busy" :stroke-width="1.75" /><Check v-else :stroke-width="1.75" />{{ busy ? (credential ? 'Saving changes…' : 'Connecting model…') : credential ? 'Save changes' : 'Connect model' }}</button></footer>
 </form>
</template>
