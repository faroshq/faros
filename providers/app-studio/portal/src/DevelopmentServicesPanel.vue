<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Loader2, Plus, RefreshCw, Server, WandSparkles } from 'lucide-vue-next'
import { api } from './api'
import DevelopmentServiceCard from './DevelopmentServiceCard.vue'
import DevelopmentServiceForm from './DevelopmentServiceForm.vue'
import { confirmDialog } from './portalkit/confirm'
import { toast } from './portalkit/toast'
import type {
  FarosContext,
  ProjectComponent,
  ProjectDevelopmentListener,
  ProjectDevelopmentService,
  ProjectDevelopmentServiceMutation,
  ProjectDevelopmentServicesResponse,
} from './types'

const props = withDefaults(defineProps<{
  ctx: FarosContext | null
  projectName: string
  components?: ProjectComponent[]
  disabled?: boolean
  embedded?: boolean
}>(), {
  components: () => [],
  disabled: false,
  embedded: false,
})

const emit = defineEmits<{
  (event: 'services-updated', response: ProjectDevelopmentServicesResponse): void
  (event: 'error', message: string): void
}>()

type BusyAction = '' | 'toggle' | 'restart' | 'delete'

const services = ref<ProjectDevelopmentService[]>([])
const listeners = ref<ProjectDevelopmentListener[]>([])
const primaryService = ref('')
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
const savingPrimary = ref(false)
const busyAction = ref('')
const formOpen = ref(false)
const formService = ref<ProjectDevelopmentService | null>(null)
const formInitialPort = ref(0)
const formKey = ref(0)
const logsService = ref('')
const logsLoading = ref(false)
const logsError = ref('')
const logsText = ref('')
const headingRef = ref<HTMLElement | null>(null)
let requestSerial = 0
let logsSerial = 0
let refreshTimer: number | undefined

const effectivePrimaryService = computed(() => primaryService.value || fallbackPrimary(services.value))
const primaryTarget = computed(() => services.value.find(service => service.name === effectivePrimaryService.value) ?? null)
const primaryOptions = computed(() => services.value.map(service => ({ name: service.name, label: service.name })))

function fallbackPrimary(items: ProjectDevelopmentService[]): string {
  const ready = items.find(service => service.ready && !!service.url)
  return ready?.name || items[0]?.name || ''
}

function applyResponse(response: ProjectDevelopmentServicesResponse) {
  services.value = Array.isArray(response.items) ? response.items : []
  listeners.value = Array.isArray(response.listeners) ? response.listeners : []
  primaryService.value = response.primaryServiceRef?.trim() || ''
  emit('services-updated', {
    ...response,
    items: services.value,
    listeners: listeners.value,
  })
}

async function load() {
  const projectName = props.projectName.trim()
  if (!projectName) {
    requestSerial += 1
    services.value = []
    listeners.value = []
    primaryService.value = ''
    loaded.value = false
    error.value = ''
    return
  }
  const serial = ++requestSerial
  loading.value = true
  try {
    const response = await api.listDevelopmentServices(props.ctx, projectName)
    if (serial !== requestSerial || props.projectName.trim() !== projectName) return
    let detectedListeners = response.listeners
    try {
      detectedListeners = await api.listDetectedDevelopmentListeners(props.ctx, projectName)
    } catch {
      // Configured services remain authoritative when observation is
      // temporarily unavailable; retain listener hints from the list call.
    }
    if (serial !== requestSerial || props.projectName.trim() !== projectName) return
    applyResponse({ ...response, listeners: detectedListeners })
    loaded.value = true
    error.value = ''
  } catch (cause) {
    if (serial !== requestSerial || props.projectName.trim() !== projectName) return
    error.value = cause instanceof Error ? cause.message : String(cause)
    emit('error', error.value)
  } finally {
    if (serial === requestSerial) loading.value = false
  }
}

function scheduleRefresh() {
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer)
  refreshTimer = undefined
  if (!props.projectName.trim()) return
  refreshTimer = window.setInterval(() => {
    if (!loading.value && !props.disabled && !formOpen.value) void load()
  }, 5000)
}

async function persistPrimary(next: string) {
  const projectName = props.projectName.trim()
  if (!projectName || savingPrimary.value || next === primaryService.value) return
  const previous = primaryService.value
  savingPrimary.value = true
  error.value = ''
  try {
    const result = await api.setPrimaryDevelopmentService(props.ctx, projectName, next)
    primaryService.value = result.primaryServiceRef?.trim() || ''
    emit('services-updated', {
      items: services.value,
      primaryServiceRef: result.primaryServiceRef,
      listeners: listeners.value,
    })
    toast('ok', next ? `Primary preview set to ${next}.` : 'Primary preview returned to automatic selection.')
  } catch (cause) {
    primaryService.value = previous
    reportActionError(cause)
  } finally {
    savingPrimary.value = false
  }
}

async function selectPrimary(event: Event) {
  await persistPrimary((event.target as HTMLSelectElement).value.trim())
}

function reportActionError(cause: unknown) {
  const message = cause instanceof Error ? cause.message : String(cause)
  error.value = message
  emit('error', message)
  toast('error', message)
}

function openCreate(initialPort = 0) {
  if (props.disabled) return
  formService.value = null
  formInitialPort.value = initialPort
  formKey.value += 1
  formOpen.value = true
}

function openEdit(service: ProjectDevelopmentService) {
  if (props.disabled) return
  formService.value = service
  formInitialPort.value = 0
  formKey.value += 1
  formOpen.value = true
}

function closeForm() {
  formOpen.value = false
  formService.value = null
  formInitialPort.value = 0
}

async function handleFormSaved() {
  closeForm()
  await load()
}

function handleFormError(message: string) {
  error.value = message
  emit('error', message)
}

function mutationFromService(service: ProjectDevelopmentService, enabled = service.enabled): ProjectDevelopmentServiceMutation {
  const body: ProjectDevelopmentServiceMutation = {
    componentRef: service.componentRef || undefined,
    enabled,
		restartPolicy: service.restartPolicy || 'Always',
	}
  if (service.command?.argv?.length) body.command = {
    argv: service.command.argv,
    workingDirectory: service.command.workingDirectory || '.',
  }
  if (service.endpoint?.port) body.endpoint = {
    protocol: service.endpoint.protocol || 'HTTP',
    port: service.endpoint.port,
    healthPath: service.endpoint.healthPath || '/',
  }
  if (service.exposure?.visibility) body.exposure = { visibility: service.exposure.visibility }
  return body
}

function busyActionFor(service: ProjectDevelopmentService): BusyAction {
  const prefix = `${service.name}:`
  if (!busyAction.value.startsWith(prefix)) return ''
  const action = busyAction.value.slice(prefix.length)
  return action === 'toggle' || action === 'restart' || action === 'delete' ? action : ''
}

async function toggleService(service: ProjectDevelopmentService) {
  if (props.disabled || busyActionFor(service)) return
  const enabled = service.enabled === false
  if (enabled && service.exposure?.visibility === 'public' && !(await confirmDialog({
    title: `Keep ${service.name} public?`,
    message: 'Enabling this service restores a preview route that accepts external traffic. Confirm only if that is intentional.',
    confirmLabel: 'Enable publicly',
    danger: true,
  }))) return
  busyAction.value = `${service.name}:toggle`
  try {
    await api.upsertDevelopmentService(props.ctx, props.projectName, service.name, {
      ...mutationFromService(service, enabled),
      // Infrastructure requires an explicit acknowledgement for a public
      // visibility write. Disabling does not widen exposure, so it does not
      // interrupt the user with a confirmation modal.
      ...(service.exposure?.visibility === 'public' ? { confirmPublic: true } : {}),
    })
    toast('ok', `${service.name} ${enabled ? 'enabled' : 'disabled'}.`)
    await load()
  } catch (cause) {
    reportActionError(cause)
  } finally {
    busyAction.value = ''
  }
}

async function restartService(service: ProjectDevelopmentService) {
  if (props.disabled || busyActionFor(service)) return
  busyAction.value = `${service.name}:restart`
  try {
    await api.restartDevelopmentService(props.ctx, props.projectName, service.name)
    toast('ok', `Restart requested for ${service.name}.`)
    await load()
  } catch (cause) {
    reportActionError(cause)
  } finally {
    busyAction.value = ''
  }
}

async function deleteService(service: ProjectDevelopmentService) {
  if (props.disabled || busyActionFor(service)) return
  if (!(await confirmDialog({
    title: `Delete ${service.name}?`,
    message: 'The service declaration and its preview route will be removed. The sandbox workspace is not deleted.',
    confirmLabel: 'Delete service',
    danger: true,
  }))) return
  busyAction.value = `${service.name}:delete`
  try {
    await api.deleteDevelopmentService(props.ctx, props.projectName, service.name)
    if (logsService.value === service.name) closeLogs()
    toast('ok', `${service.name} deleted.`)
    await load()
  } catch (cause) {
    reportActionError(cause)
  } finally {
    busyAction.value = ''
  }
}

function boundedLogs(value: string): string {
  const maxChars = 48 * 1024
  if (value.length <= maxChars) return value
  return `[showing the last ${maxChars.toLocaleString()} characters]\n${value.slice(-maxChars)}`
}

async function toggleLogs(service: ProjectDevelopmentService) {
  if (logsService.value === service.name) {
    closeLogs()
    return
  }
  const serial = ++logsSerial
  logsService.value = service.name
  logsText.value = ''
  logsError.value = ''
  logsLoading.value = true
  try {
    const value = await api.getDevelopmentServiceLogs(props.ctx, props.projectName, service.name)
    if (serial !== logsSerial) return
    logsText.value = boundedLogs(value)
  } catch (cause) {
    if (serial !== logsSerial) return
    logsError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    if (serial === logsSerial) logsLoading.value = false
  }
}

function closeLogs() {
  logsSerial += 1
  logsService.value = ''
  logsText.value = ''
  logsError.value = ''
  logsLoading.value = false
}

function configureListener(listener: ProjectDevelopmentListener) {
  openCreate(listener.port)
}

function focus() {
  headingRef.value?.focus()
}

defineExpose({ focus })

watch(
  () => [props.projectName, props.ctx?.token, props.ctx?.tenant, props.ctx?.subPath, props.disabled] as const,
  (next, previous) => {
    if (previous && next[0] !== previous[0]) closeForm()
    void load()
    scheduleRefresh()
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  requestSerial += 1
  logsSerial += 1
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer)
})
</script>

<template>
  <section
    class="grid gap-3"
    :class="embedded ? 'border-t border-border-subtle pt-4' : 'rounded-md border border-border-subtle bg-surface-raised/70 p-3'"
    aria-labelledby="development-services-heading"
  >
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex min-w-0 items-start gap-2">
        <div class="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-border-subtle bg-surface-overlay">
          <Server class="h-3.5 w-3.5 text-accent" :stroke-width="1.75" aria-hidden="true" />
        </div>
        <div class="min-w-0">
          <h3 id="development-services-heading" ref="headingRef" tabindex="-1" class="text-[13px] font-semibold text-text-primary outline-none focus-visible:ring-2 focus-visible:ring-accent/40">Preview services</h3>
          <p class="mt-0.5 text-[11px] leading-4 text-text-muted">Configure process routes in the universal sandbox. Listener discovery is observation-only and never opens a port automatically.</p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <button type="button" class="flex h-7 items-center gap-1.5 rounded-md border border-accent/40 bg-accent-subtle px-2 text-[11px] font-medium text-accent transition hover:border-accent/70 hover:bg-accent/20 disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled || !projectName" @click="openCreate()">
          <Plus class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" />
          Add service
        </button>
        <button type="button" class="flex h-7 items-center gap-1.5 rounded-md border border-border-subtle bg-surface px-2 text-[11px] font-medium text-text-secondary transition hover:bg-surface-hover hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50" :disabled="loading || disabled || !projectName" title="Refresh development services" @click="load">
          <RefreshCw class="h-3 w-3" :class="loading ? 'animate-spin' : ''" :stroke-width="1.75" aria-hidden="true" />
          Refresh
        </button>
      </div>
    </div>

    <div v-if="error" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-danger/30 bg-danger-subtle px-3 py-2 text-[12px] text-danger" role="alert">
      <span class="min-w-0 break-words">{{ error }}</span>
      <button type="button" class="shrink-0 font-medium underline underline-offset-2" @click="load">Retry</button>
    </div>

    <div v-if="loading && !loaded" class="flex items-center gap-2 rounded-md border border-dashed border-border-subtle px-3 py-4 text-[12px] text-text-muted" role="status" aria-live="polite" aria-busy="true">
      <Loader2 class="h-3.5 w-3.5 animate-spin text-accent" :stroke-width="1.75" />
      Reading configured services…
    </div>

    <DevelopmentServiceForm
      v-if="formOpen"
      :key="formKey"
      :ctx="ctx"
      :project-name="projectName"
      :components="components"
      :service="formService"
      :initial-port="formInitialPort"
      :disabled="disabled"
      @saved="handleFormSaved"
      @cancel="closeForm"
      @error="handleFormError"
    />

    <template v-if="services.length">
      <label class="grid max-w-md gap-1.5">
        <span class="text-[11px] font-medium uppercase tracking-wide text-text-muted">Primary preview</span>
        <span class="relative block">
          <select :value="primaryService" class="h-9 w-full appearance-none rounded-md border border-border-subtle bg-surface px-2.5 pr-8 text-[12px] font-mono text-text-primary outline-none transition focus:border-accent/50 disabled:cursor-not-allowed disabled:opacity-60" aria-label="Primary development preview service" :disabled="savingPrimary || disabled" @change="selectPrimary">
            <option value="">Automatic ready service</option>
            <option v-for="option in primaryOptions" :key="option.name" :value="option.name">{{ option.label }}</option>
          </select>
          <WandSparkles class="pointer-events-none absolute right-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-text-muted" :stroke-width="1.75" aria-hidden="true" />
        </span>
        <span class="text-[11px] text-text-muted">Stored on the Project development environment; it changes the default preview only.</span>
      </label>

      <div class="grid gap-2" aria-label="Configured development services">
        <DevelopmentServiceCard
          v-for="service in services"
          :key="service.name"
          :service="service"
          :primary="service.name === effectivePrimaryService"
          :persisted-primary="service.name === primaryService"
          :disabled="disabled"
          :busy-action="busyActionFor(service)"
          :logs-open="logsService === service.name"
          :logs-loading="logsLoading && logsService === service.name"
          :logs-error="logsService === service.name ? logsError : ''"
          :logs-text="logsService === service.name ? logsText : ''"
          @set-primary="persistPrimary(service.name)"
          @edit="openEdit(service)"
          @toggle="toggleService(service)"
          @restart="restartService(service)"
          @delete="deleteService(service)"
          @toggle-logs="toggleLogs(service)"
          @close-logs="closeLogs"
        />
      </div>

      <div v-if="listeners.length" class="grid gap-2 border-t border-border-subtle pt-3" aria-labelledby="detected-listeners-heading">
        <div class="flex items-center gap-2">
          <WandSparkles class="h-3.5 w-3.5 text-text-muted" :stroke-width="1.75" aria-hidden="true" />
          <h4 id="detected-listeners-heading" class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Detected listeners · suggestions only</h4>
        </div>
        <div class="grid gap-1.5">
          <div v-for="listener in listeners" :key="`${listener.process || 'process'}-${listener.port}-${listener.address || ''}`" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border-subtle bg-surface px-2.5 py-2">
            <span class="min-w-0 break-words font-mono text-[11px] text-text-secondary">{{ listener.process || 'process' }} :{{ listener.port }}{{ listener.protocol ? ` ${listener.protocol}` : '' }}{{ listener.address ? ` · ${listener.address}` : '' }}</span>
            <button type="button" class="h-6 shrink-0 rounded-md border border-border-subtle px-1.5 text-[10px] font-medium text-text-secondary hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled" @click="configureListener(listener)">Configure service</button>
          </div>
        </div>
        <p class="text-[11px] text-text-muted">Configuring a suggestion still requires a command, an explicit save, and (for public exposure) confirmation.</p>
      </div>
    </template>

    <div v-else-if="loaded && !formOpen" class="grid gap-2 rounded-md border border-dashed border-border-subtle px-3 py-4 text-[12px] leading-5 text-text-muted">
      <p>No DevelopmentService is configured yet. Add one here or declare it through the assistant with its command and port.</p>
      <p>Listener suggestions below are observational only; they never become exposed routes without a saved service declaration.</p>
      <div v-if="listeners.length" class="grid gap-1.5 pt-1" aria-label="Detected listener suggestions">
        <div v-for="listener in listeners" :key="`${listener.process || 'process'}-${listener.port}-${listener.address || ''}`" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border-subtle bg-surface px-2.5 py-2">
          <span class="font-mono text-[11px] text-text-secondary">{{ listener.process || 'process' }} :{{ listener.port }}</span>
          <button type="button" class="h-6 rounded-md border border-border-subtle px-1.5 text-[10px] font-medium text-text-secondary hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled" @click="configureListener(listener)">Configure service</button>
        </div>
      </div>
    </div>

    <div v-if="primaryTarget && !primaryTarget.ready" class="text-[11px] text-text-muted" role="status" aria-live="polite">
      Primary preview is waiting for <span class="font-mono text-text-secondary">{{ primaryTarget.name }}</span> to report a ready route.
    </div>
  </section>
</template>
