<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Sparkles, Package, Layers, Loader2, ArrowRight } from 'lucide-vue-next'
import type { KedgeContext } from './types'
import type { ProjectPlan } from './types'
import { api } from './api'

// A mini creation wizard: intake → blueprint (recommended template + whether
// starter code attaches) → confirm → create. It mirrors vibe-studio's
// wizard-first flow so an app-studio project opens on a runnable placeholder
// rather than an empty directory. The parent owns the actual createProject +
// first-turn kickoff; this component only proposes and confirms.

const props = defineProps<{
  ctx: KedgeContext | null
  // disabled blocks Create while the parent isn't ready (setup incomplete).
  disabled?: boolean
  disabledReason?: string
}>()

const emit = defineEmits<{
  // create carries the confirmed intake for the parent to run.
  create: [payload: { prompt: string; templateName?: string; displayName?: string }]
  cancel: []
}>()

type Step = 'intake' | 'blueprint'

const step = ref<Step>('intake')
const prompt = ref('')
const planning = ref(false)
const error = ref<string | null>(null)
const plan = ref<ProjectPlan | null>(null)
const chosenTemplate = ref<string>('')
const displayName = ref('')

const canPlan = computed(() => prompt.value.trim().length > 0 && !planning.value)

const activeTemplate = computed(() =>
  plan.value?.availableTemplates.find((t) => t.name === chosenTemplate.value) ?? null,
)

const willAttachScaffold = computed(() => {
  // The recommended template's scaffold comes back on the plan; a user-picked
  // alternative carries hasScaffold on its catalog entry.
  if (chosenTemplate.value && chosenTemplate.value === plan.value?.template) {
    return !!plan.value?.scaffold
  }
  return !!activeTemplate.value?.hasScaffold
})

async function runPlan() {
  if (!canPlan.value) return
  planning.value = true
  error.value = null
  try {
    const result = await api.planProject(props.ctx, { prompt: prompt.value.trim() })
    plan.value = result
    chosenTemplate.value = result.template ?? ''
    displayName.value = result.displayName
    step.value = 'blueprint'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not plan the project. Try again.'
  } finally {
    planning.value = false
  }
}

function confirmCreate() {
  if (props.disabled) return
  emit('create', {
    prompt: prompt.value.trim(),
    templateName: chosenTemplate.value || undefined,
    displayName: displayName.value.trim() || undefined,
  })
}

function back() {
  step.value = 'intake'
}

watch(
  () => props.ctx,
  () => {
    // Reset when the workspace context changes under us.
    step.value = 'intake'
    plan.value = null
    error.value = null
  },
)
</script>

<template>
  <div class="wizard">
    <div v-if="step === 'intake'" class="intake">
      <label class="lead">
        <Sparkles :size="16" />
        <span>What do you want to build?</span>
      </label>
      <textarea
        v-model="prompt"
        class="prompt"
        rows="3"
        placeholder="e.g. A storefront for a produce co-op with a product catalog and checkout"
        @keydown.meta.enter.prevent="runPlan"
        @keydown.ctrl.enter.prevent="runPlan"
      />
      <div class="actions">
        <button class="ghost" type="button" @click="emit('cancel')">Cancel</button>
        <button class="primary" type="button" :disabled="!canPlan" @click="runPlan">
          <Loader2 v-if="planning" :size="15" class="spin" />
          <template v-else>Plan project <ArrowRight :size="15" /></template>
        </button>
      </div>
      <p v-if="error" class="err">{{ error }}</p>
    </div>

    <div v-else class="blueprint">
      <p class="blueprint-lead">Here's the plan — review and create.</p>

      <label class="field">
        <span>Project name</span>
        <input v-model="displayName" type="text" class="text" />
      </label>

      <label class="field">
        <span>Template</span>
        <select v-model="chosenTemplate" class="text">
          <option value="">No template (start empty)</option>
          <option v-for="t in plan?.availableTemplates ?? []" :key="t.name" :value="t.name">
            {{ t.displayName || t.name }}{{ t.hasScaffold ? ' — includes starter code' : '' }}
          </option>
        </select>
      </label>

      <div v-if="activeTemplate" class="shape">
        <div class="shape-row">
          <Layers :size="15" />
          <span>{{ Object.keys(activeTemplate.components || {}).length }} component(s):
            {{ Object.keys(activeTemplate.components || {}).join(', ') }}</span>
        </div>
        <div class="shape-row" :class="{ good: willAttachScaffold }">
          <Package :size="15" />
          <span v-if="willAttachScaffold">Starter code will be attached — the project opens on a working placeholder.</span>
          <span v-else>No starter code — the assistant builds from scratch.</span>
        </div>
      </div>

      <p v-if="disabled && disabledReason" class="err">{{ disabledReason }}</p>

      <div class="actions">
        <button class="ghost" type="button" @click="back">Back</button>
        <button class="primary" type="button" :disabled="disabled" @click="confirmCreate">
          Create project
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.wizard {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
}
.lead,
.blueprint-lead {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  font-size: 14px;
}
.prompt,
.text {
  width: 100%;
  border: 1px solid var(--border, #d9dfd7);
  border-radius: 10px;
  padding: 10px 12px;
  font: inherit;
  background: var(--card, #fff);
  color: inherit;
  box-sizing: border-box;
}
.prompt {
  resize: vertical;
  min-height: 72px;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
}
.field > span {
  color: var(--muted, #5d6b61);
}
.shape {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border: 1px solid var(--border, #d9dfd7);
  border-radius: 10px;
  background: var(--bg, #f7f5ef);
}
.shape-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--muted, #5d6b61);
}
.shape-row.good {
  color: var(--accent, #3f7d4a);
  font-weight: 600;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border-radius: 999px;
  padding: 9px 16px;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid transparent;
}
button:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.primary {
  background: var(--accent, #3f7d4a);
  color: #fff;
}
.ghost {
  background: transparent;
  border-color: var(--border, #d9dfd7);
  color: inherit;
}
.err {
  color: #b3261e;
  font-size: 13px;
  margin: 0;
}
.spin {
  animation: wizard-spin 0.8s linear infinite;
}
@keyframes wizard-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
