<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Loader2, Pencil, Plus, X } from 'lucide-vue-next'
import { api } from './api'
import { confirmDialog } from './portalkit/confirm'
import { toast } from './portalkit/toast'
import type {
  FarosContext,
  ProjectComponent,
  ProjectDevelopmentService,
  ProjectDevelopmentServiceMutation,
} from './types'

const props = withDefaults(defineProps<{
  ctx: FarosContext | null
  projectName: string
  components?: ProjectComponent[]
  service?: ProjectDevelopmentService | null
  initialPort?: number
  disabled?: boolean
}>(), {
  components: () => [],
  service: null,
  initialPort: 0,
  disabled: false,
})

const emit = defineEmits<{
  (event: 'saved', service: ProjectDevelopmentService): void
  (event: 'cancel'): void
  (event: 'error', message: string): void
}>()

type ServiceVisibility = 'private' | 'public'
type RestartPolicy = 'Always' | 'OnFailure' | 'Never'

interface ServiceFormState {
  name: string
  componentRef: string
  enabled: boolean
  argvText: string
  workingDirectory: string
  protocol: string
  port: number
  healthPath: string
  visibility: ServiceVisibility
  restartPolicy: RestartPolicy
}

function emptyForm(): ServiceFormState {
  return {
    name: '',
    componentRef: '',
    enabled: true,
    argvText: '',
    workingDirectory: '.',
    protocol: 'HTTP',
    port: 3000,
    healthPath: '/',
    visibility: 'private',
    restartPolicy: 'Always',
  }
}

function serviceToForm(service: ProjectDevelopmentService): ServiceFormState {
  return {
    name: service.name,
    componentRef: service.componentRef || '',
    enabled: service.enabled !== false,
    argvText: service.command?.argv?.join('\n') || '',
    workingDirectory: service.command?.workingDirectory || '.',
    protocol: service.endpoint?.protocol || 'HTTP',
    port: service.endpoint?.port || 3000,
    healthPath: service.endpoint?.healthPath || '/',
    visibility: service.exposure?.visibility === 'public' ? 'public' : 'private',
    restartPolicy: service.restartPolicy === 'OnFailure' || service.restartPolicy === 'Never' ? service.restartPolicy : 'Always',
  }
}

const form = reactive<ServiceFormState>(emptyForm())
const formError = ref('')
const saving = ref(false)
const editing = computed(() => !!props.service)
const title = computed(() => editing.value ? `Edit ${props.service?.name || 'development service'}` : 'Add development service')
const submitLabel = computed(() => editing.value ? 'Save changes' : 'Add service')

function resetFromProps() {
  Object.assign(form, props.service ? serviceToForm(props.service) : emptyForm())
  if (!props.service && props.initialPort && props.initialPort > 0) form.port = props.initialPort
  formError.value = ''
}

watch(
  () => [props.service?.name, props.initialPort] as const,
  resetFromProps,
  { immediate: true },
)

function validate(): { service: string; body: ProjectDevelopmentServiceMutation } | null {
  const service = form.name.trim()
  if (!service) {
    formError.value = 'Service name is required.'
    return null
  }
  if (service.length > 63 || !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(service)) {
    formError.value = 'Use a lowercase DNS name with letters, numbers, and hyphens.'
    return null
  }
  const argv = form.argvText.split(/\r?\n/).map(value => value.trim()).filter(Boolean)
  if (!argv.length) {
    formError.value = 'Command is required. Enter one argv item per line.'
    return null
  }
  if (argv.length > 32) {
    formError.value = 'Command accepts at most 32 argv items.'
    return null
  }
  const workingDirectory = form.workingDirectory.trim() || '.'
  if (workingDirectory.startsWith('/') || workingDirectory.split('/').some(part => part === '..')) {
    formError.value = 'Working directory must remain inside the workspace.'
    return null
  }
  const port = Number(form.port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    formError.value = 'Port must be an integer between 1 and 65535.'
    return null
  }
  const healthPath = form.healthPath.trim() || '/'
  if (!healthPath.startsWith('/') || healthPath.length > 512) {
    formError.value = 'Health path must be an absolute path of at most 512 characters.'
    return null
  }
  if (editing.value && props.service && service !== props.service.name) {
    formError.value = 'The service name cannot change while editing.'
    return null
  }
  formError.value = ''
  return {
    service,
    body: {
      componentRef: form.componentRef.trim() || undefined,
      enabled: form.enabled,
      command: { argv, workingDirectory },
      endpoint: { protocol: 'HTTP', port, healthPath },
      exposure: { visibility: form.visibility },
      restartPolicy: form.restartPolicy,
    },
  }
}

async function confirmPublicExposure(service: string): Promise<boolean> {
  return confirmDialog({
    title: `Expose ${service} publicly?`,
    message: 'Public preview traffic can reach this service without the private project access gate. Confirm only if that is intentional.',
    confirmLabel: 'Expose publicly',
    danger: true,
  })
}

function reportError(cause: unknown) {
  const message = cause instanceof Error ? cause.message : String(cause)
  formError.value = message
  emit('error', message)
  toast('error', message)
}

async function save() {
  if (saving.value || props.disabled) return
  const parsed = validate()
  if (!parsed) return
  if (form.visibility === 'public' && !(await confirmPublicExposure(parsed.service))) return
  saving.value = true
  try {
    const result = await api.upsertDevelopmentService(props.ctx, props.projectName, parsed.service, {
      ...parsed.body,
      ...(form.visibility === 'public' ? { confirmPublic: true } : {}),
    })
    toast('ok', `${parsed.service} configuration saved.`)
    emit('saved', result.service)
  } catch (cause) {
    reportError(cause)
  } finally {
    saving.value = false
  }
}

function cancel() {
  if (!saving.value) emit('cancel')
}
</script>

<template>
  <form class="grid gap-3 rounded-md border border-accent/30 bg-surface p-3" aria-labelledby="development-service-form-heading" @submit.prevent="save">
    <div class="flex flex-wrap items-start justify-between gap-2">
      <div>
        <h4 id="development-service-form-heading" class="text-[12px] font-semibold text-text-primary">{{ title }}</h4>
        <p class="mt-0.5 text-[11px] text-text-muted">The command is argv-only. Enter one argument per line; shell interpretation is never implied.</p>
      </div>
      <button type="button" class="flex h-7 items-center gap-1 rounded-md border border-border-subtle px-2 text-[11px] text-text-secondary hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50" :disabled="saving" @click="cancel">
        <X class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" />
        Cancel
      </button>
    </div>

    <div v-if="formError" class="rounded-md border border-danger/30 bg-danger-subtle px-3 py-2 text-[12px] text-danger" role="alert" aria-live="polite">{{ formError }}</div>

	<div class="grid gap-3">
      <label class="grid gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Service name</span>
        <input v-model="form.name" :disabled="editing || saving" required maxlength="63" autocomplete="off" class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] font-mono text-text-primary outline-none transition placeholder:text-text-muted focus:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60" placeholder="web" aria-describedby="development-service-name-help" />
        <span id="development-service-name-help" class="text-[11px] text-text-muted">Lowercase DNS name, up to 63 characters.</span>
      </label>
      <label class="grid gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Component ref <span class="normal-case tracking-normal text-text-muted">(optional)</span></span>
        <input v-model="form.componentRef" list="development-component-options" :disabled="saving" maxlength="253" autocomplete="off" class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] font-mono text-text-primary outline-none transition placeholder:text-text-muted focus:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60" placeholder="frontend" />
        <span class="text-[11px] text-text-muted">Must match a declared Project component when set.</span>
        <datalist id="development-component-options">
          <option v-for="component in components" :key="component.name" :value="component.name">{{ component.kind }}</option>
        </datalist>
      </label>
    </div>

    <label class="grid gap-1.5">
      <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Command argv</span>
      <textarea v-model="form.argvText" :disabled="saving" required rows="3" spellcheck="false" class="min-h-[76px] resize-y rounded-md border border-border-subtle bg-surface-raised px-2.5 py-2 text-[12px] font-mono leading-5 text-text-primary outline-none transition placeholder:text-text-muted focus:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60" placeholder="npm\nrun\ndev"></textarea>
      <span class="text-[11px] text-text-muted">The first line is the executable. Arguments are passed literally, without a shell.</span>
    </label>

    <div class="grid gap-3 sm:grid-cols-2">
      <label class="grid gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Working directory</span>
        <input v-model="form.workingDirectory" :disabled="saving" maxlength="512" autocomplete="off" class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] font-mono text-text-primary outline-none transition placeholder:text-text-muted focus:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60" placeholder="." />
        <span class="text-[11px] text-text-muted">Workspace-relative; use . for the root.</span>
      </label>
	</div>

    <div class="grid gap-3 sm:grid-cols-3">
      <label class="grid gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Protocol</span>
        <select v-model="form.protocol" disabled class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] font-mono text-text-primary outline-none disabled:cursor-not-allowed disabled:opacity-60">
          <option value="HTTP">HTTP</option>
        </select>
        <span class="text-[11px] text-text-muted">HTTP preview routes are supported.</span>
      </label>
      <label class="grid gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Port</span>
        <input v-model.number="form.port" :disabled="saving" type="number" min="1" max="65535" required class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] font-mono text-text-primary outline-none transition focus:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60" />
        <span class="text-[11px] text-text-muted">Port listened to inside the sandbox.</span>
      </label>
      <label class="grid gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Health path</span>
        <input v-model="form.healthPath" :disabled="saving" maxlength="512" autocomplete="off" class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] font-mono text-text-primary outline-none transition placeholder:text-text-muted focus:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60" placeholder="/" />
        <span class="text-[11px] text-text-muted">Absolute path used for readiness checks.</span>
      </label>
    </div>

    <div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end">
      <label class="grid gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Exposure</span>
        <select v-model="form.visibility" :disabled="saving" class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] font-mono text-text-primary outline-none transition focus:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60">
          <option value="private">Private · authenticated preview</option>
          <option value="public">Public · external traffic</option>
        </select>
        <span class="text-[11px] text-text-muted">Public saves always require an explicit confirmation.</span>
      </label>
      <label class="grid gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Restart policy</span>
        <select v-model="form.restartPolicy" :disabled="saving" class="h-9 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] font-mono text-text-primary outline-none transition focus:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60">
          <option value="Always">Always</option>
          <option value="OnFailure">On failure</option>
          <option value="Never">Never</option>
        </select>
        <span class="text-[11px] text-text-muted">Infrastructure owns process supervision.</span>
      </label>
      <label class="flex h-9 items-center gap-2 rounded-md border border-border-subtle bg-surface-raised px-2.5 text-[12px] text-text-secondary">
        <input v-model="form.enabled" :disabled="saving" type="checkbox" class="h-3.5 w-3.5 accent-accent" />
        Enabled
      </label>
    </div>

    <div class="flex flex-wrap justify-end gap-1.5 border-t border-border-subtle pt-3">
      <button type="button" class="h-8 rounded-md border border-border-subtle px-2.5 text-[11px] font-medium text-text-secondary hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50" :disabled="saving" @click="cancel">Cancel</button>
      <button type="submit" class="flex h-8 items-center gap-1.5 rounded-md bg-accent px-2.5 text-[11px] font-medium text-white transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50" :disabled="saving">
        <Loader2 v-if="saving" class="h-3 w-3 animate-spin" :stroke-width="1.75" aria-hidden="true" />
        <Plus v-else-if="!editing" class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" />
        <Pencil v-else class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" />
        {{ submitLabel }}
      </button>
    </div>
  </form>
</template>
