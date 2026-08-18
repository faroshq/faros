<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { LoaderCircle, X } from 'lucide-vue-next'

import { createRepositorySync } from '../api'
import { hasCreateRepositorySyncErrors, validateCreateRepositorySync } from '../createRepositorySyncForm'
import type { CreateRepositorySyncInput } from '../types'

const emit = defineEmits<{
  cancel: []
  created: [name: string]
}>()

const nameInput = ref<HTMLInputElement | null>(null)
const errorSummary = ref<HTMLElement | null>(null)
const submitting = ref(false)
const serverError = ref('')
const errors = reactive({
  name: '',
  repositoryRef: '',
  path: '',
  intervalSeconds: '',
})
const form = reactive<CreateRepositorySyncInput>({
  name: '',
  repositoryRef: '',
  ref: '',
  path: '.faros',
  intervalSeconds: 30,
  prune: true,
})

let previousFocus: HTMLElement | null = null

function clearErrors(): void {
  errors.name = ''
  errors.repositoryRef = ''
  errors.path = ''
  errors.intervalSeconds = ''
  serverError.value = ''
}

function validate(): boolean {
  clearErrors()
  Object.assign(errors, validateCreateRepositorySync(form))
  return !hasCreateRepositorySyncErrors(errors)
}

async function submit(): Promise<void> {
  if (submitting.value || !validate()) {
    if (!submitting.value) await nextTick(() => errorSummary.value?.focus())
    return
  }

  submitting.value = true
  try {
    const created = await createRepositorySync({
      name: form.name,
      repositoryRef: form.repositoryRef.trim(),
      ref: form.ref?.trim(),
      path: form.path?.trim(),
      intervalSeconds: form.intervalSeconds,
      prune: form.prune,
    })
    emit('created', created.name)
  } catch (error) {
    serverError.value = error instanceof Error ? error.message : 'Repository sync could not be created.'
    await nextTick(() => errorSummary.value?.focus())
  } finally {
    submitting.value = false
  }
}

function cancel(): void {
  if (!submitting.value) emit('cancel')
}

onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  void nextTick(() => nameInput.value?.focus())
})

onBeforeUnmount(() => {
  const target = previousFocus
  previousFocus = null
  void nextTick(() => target?.isConnected && target.focus())
})
</script>

<template>
  <section class="panel create-sync-panel" aria-labelledby="create-sync-title" :aria-busy="submitting">
    <header class="panel-head">
      <div>
        <p class="eyebrow">New sync</p>
        <h2 id="create-sync-title" class="panel-title">Create repository sync</h2>
        <p class="create-description">Apply reviewed desired state from a Code repository into this workspace.</p>
      </div>
      <button class="button icon-button" type="button" :disabled="submitting" aria-label="Cancel creating repository sync" @click="cancel">
        <X :size="16" :stroke-width="1.75" aria-hidden="true" />
      </button>
    </header>

    <div
      v-if="errors.name || errors.repositoryRef || errors.path || errors.intervalSeconds || serverError"
      ref="errorSummary"
      class="error-summary"
      role="alert"
      aria-live="assertive"
      tabindex="-1"
    >
      {{ serverError || errors.name || errors.repositoryRef || errors.path || errors.intervalSeconds }}
    </div>

    <form class="sync-form" novalidate @submit.prevent="submit">
      <label class="create-field" for="repository-sync-name">
        <span class="field-label">Name</span>
        <input
          id="repository-sync-name"
          ref="nameInput"
          v-model="form.name"
          class="field-input mono"
          type="text"
          autocomplete="off"
          placeholder="pen-store-production"
          maxlength="253"
          required
          aria-required="true"
          :aria-invalid="Boolean(errors.name)"
          :aria-describedby="errors.name ? 'repository-sync-name-hint repository-sync-name-error' : 'repository-sync-name-hint'"
          :disabled="submitting"
          @input="errors.name = ''; serverError = ''"
        >
        <span id="repository-sync-name-hint" class="field-hint">Stable DNS-1123 name for this sync.</span>
        <span v-if="errors.name" id="repository-sync-name-error" class="field-error">{{ errors.name }}</span>
      </label>

      <label class="create-field" for="repository-sync-repository">
        <span class="field-label">Repository</span>
        <input
          id="repository-sync-repository"
          v-model="form.repositoryRef"
          class="field-input mono"
          type="text"
          autocomplete="off"
          placeholder="pen-store-app"
          required
          aria-required="true"
          :aria-invalid="Boolean(errors.repositoryRef)"
          :aria-describedby="errors.repositoryRef ? 'repository-sync-repository-hint repository-sync-repository-error' : 'repository-sync-repository-hint'"
          :disabled="submitting"
          @input="errors.repositoryRef = ''; serverError = ''"
        >
        <span id="repository-sync-repository-hint" class="field-hint">Exact repository resource name from the Code provider.</span>
        <span v-if="errors.repositoryRef" id="repository-sync-repository-error" class="field-error">{{ errors.repositoryRef }}</span>
      </label>

      <div class="field-row">
        <label class="create-field" for="repository-sync-ref">
          <span class="field-label">Git ref</span>
          <input
            id="repository-sync-ref"
            v-model="form.ref"
            class="field-input mono"
            type="text"
            autocomplete="off"
            placeholder="Repository default"
            :disabled="submitting"
            @input="serverError = ''"
          >
          <span class="field-hint">Branch, tag, or commit. Blank uses the repository default.</span>
        </label>

        <label class="create-field" for="repository-sync-path">
          <span class="field-label">Target path</span>
          <input
            id="repository-sync-path"
            v-model="form.path"
            class="field-input mono"
            type="text"
            autocomplete="off"
            placeholder=".faros"
            :aria-invalid="Boolean(errors.path)"
            :aria-describedby="errors.path ? 'repository-sync-path-hint repository-sync-path-error' : 'repository-sync-path-hint'"
            :disabled="submitting"
            @input="errors.path = ''; serverError = ''"
          >
          <span id="repository-sync-path-hint" class="field-hint">Repository-relative directory containing desired-state manifests.</span>
          <span v-if="errors.path" id="repository-sync-path-error" class="field-error">{{ errors.path }}</span>
        </label>
      </div>

      <label class="create-field interval-field" for="repository-sync-interval">
        <span class="field-label">Sync interval</span>
        <span class="input-unit">
          <input
            id="repository-sync-interval"
            v-model.number="form.intervalSeconds"
            class="field-input mono"
            type="number"
            inputmode="numeric"
            min="10"
            max="3600"
            step="1"
            required
            aria-required="true"
            :aria-invalid="Boolean(errors.intervalSeconds)"
            :aria-describedby="errors.intervalSeconds ? 'repository-sync-interval-hint repository-sync-interval-error' : 'repository-sync-interval-hint'"
            :disabled="submitting"
            @input="errors.intervalSeconds = ''; serverError = ''"
          >
          <span aria-hidden="true">seconds</span>
        </span>
        <span id="repository-sync-interval-hint" class="field-hint">Deployments checks from every 10 seconds up to hourly.</span>
        <span v-if="errors.intervalSeconds" id="repository-sync-interval-error" class="field-error">{{ errors.intervalSeconds }}</span>
      </label>

      <label class="checkbox-field" for="repository-sync-prune">
        <input id="repository-sync-prune" v-model="form.prune" type="checkbox" :disabled="submitting">
        <span>
          <strong>Prune removed objects</strong>
          <small>Delete owned objects when manifests are removed or when this sync is deleted. Disable to leave them in place.</small>
        </span>
      </label>

      <footer class="form-actions">
        <button class="button ghost" type="button" :disabled="submitting" @click="cancel">Cancel</button>
        <button class="button primary" type="submit" :disabled="submitting">
          <LoaderCircle v-if="submitting" class="spinning" :size="14" :stroke-width="1.75" aria-hidden="true" />
          {{ submitting ? 'Creating…' : 'Create sync' }}
        </button>
      </footer>
    </form>
  </section>
</template>
