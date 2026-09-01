<script setup lang="ts">
import {
  ExternalLink,
  FileText,
  Loader2,
  Pencil,
  RefreshCw,
  Terminal,
  Trash2,
  WandSparkles,
  X,
} from 'lucide-vue-next'
import StatusBadge from './portalkit/StatusBadge.vue'
import type { ProjectDevelopmentService } from './types'

withDefaults(defineProps<{
  service: ProjectDevelopmentService
  primary?: boolean
  persistedPrimary?: boolean
  disabled?: boolean
  busyAction?: '' | 'toggle' | 'restart' | 'delete'
  logsOpen?: boolean
  logsLoading?: boolean
  logsError?: string
  logsText?: string
}>(), {
  primary: false,
  persistedPrimary: false,
  disabled: false,
  busyAction: '',
  logsOpen: false,
  logsLoading: false,
  logsError: '',
  logsText: '',
})

const emit = defineEmits<{
  (event: 'set-primary'): void
  (event: 'edit'): void
  (event: 'toggle'): void
  (event: 'restart'): void
  (event: 'delete'): void
  (event: 'toggle-logs'): void
  (event: 'close-logs'): void
}>()

function statusFor(service: ProjectDevelopmentService): string {
  if (service.ready) return 'Ready'
  if (service.phase?.trim()) return service.phase.trim()
  if (service.process?.running || service.process?.portListening) return 'Starting'
  return service.enabled === false ? 'Disabled' : 'Pending'
}

function statusTone(service: ProjectDevelopmentService): 'success' | 'warning' | 'danger' | 'muted' {
  if (service.ready) return 'success'
  if (service.error || service.phase?.toLowerCase() === 'failed') return 'danger'
  if (service.enabled === false) return 'muted'
  return 'warning'
}

function serviceSummary(service: ProjectDevelopmentService): string {
  const process = service.process
  if (process?.message?.trim()) return process.message.trim()
  if (process?.reachable && process.portListening) return 'Process and declared port are reachable'
  if (process?.running && !process.portListening) return 'Process is running but is not listening on the declared port'
  if (process?.portListening && !process.reachable) return 'Port is listening but the sandbox probe cannot reach it'
  if (service.error) return service.error
  return 'Waiting for the process, route, and readiness checks'
}

function commandSummary(service: ProjectDevelopmentService): string {
  const argv = service.command?.argv ?? []
  return argv.length ? argv.join(' ') : 'Not declared'
}

function workingDirectorySummary(service: ProjectDevelopmentService): string {
  return service.command?.workingDirectory?.trim() || '.'
}

function displayVisibility(service: ProjectDevelopmentService): string {
  return service.exposure?.visibility === 'public' ? 'Public' : 'Private'
}

function logsHeadingID(service: ProjectDevelopmentService): string {
  const safeName = service.name.toLowerCase().replace(/[^a-z0-9-]+/g, '-')
  return `development-service-logs-heading-${safeName || 'service'}`
}
</script>

<template>
  <article class="grid min-w-0 gap-2 rounded-md border border-border-subtle bg-surface p-3">
    <div class="flex min-w-0 flex-wrap items-center gap-2">
      <code class="min-w-0 max-w-full truncate text-[12px] font-semibold text-text-primary">{{ service.name }}</code>
      <span v-if="primary" class="k-badge k-badge--muted">{{ persistedPrimary ? 'primary' : 'auto primary' }}</span>
      <StatusBadge :status="statusFor(service)" :tone="statusTone(service)" />
      <span class="ml-auto text-[11px] text-text-muted">{{ displayVisibility(service) }}</span>
    </div>

    <div class="grid min-w-0 gap-1 text-[11px] text-text-secondary sm:grid-cols-3">
      <span class="min-w-0 font-mono">{{ service.endpoint?.protocol || 'HTTP' }} :{{ service.endpoint?.port || '—' }}</span>
      <span>Process: {{ service.process?.running ? 'running' : 'stopped' }}</span>
      <span>Route: {{ service.ready ? 'reachable' : 'not ready' }}</span>
    </div>

    <dl class="grid min-w-0 gap-x-4 gap-y-1 text-[11px] sm:grid-cols-2">
      <div class="min-w-0"><dt class="inline text-text-muted">Command: </dt><dd class="inline break-words font-mono text-text-secondary">{{ commandSummary(service) }}</dd></div>
      <div class="min-w-0"><dt class="inline text-text-muted">Workdir: </dt><dd class="inline break-words font-mono text-text-secondary">{{ workingDirectorySummary(service) }}</dd></div>
      <div class="min-w-0"><dt class="inline text-text-muted">Health: </dt><dd class="inline break-words font-mono text-text-secondary">{{ service.endpoint?.healthPath || '/' }}</dd></div>
      <div class="min-w-0"><dt class="inline text-text-muted">Component: </dt><dd class="inline break-words font-mono text-text-secondary">{{ service.componentRef || 'none' }}</dd></div>
      <div class="min-w-0"><dt class="inline text-text-muted">Restart: </dt><dd class="inline font-mono text-text-secondary">{{ service.restartPolicy || 'Always' }}</dd></div>
      <div class="min-w-0"><dt class="inline text-text-muted">Connections: </dt><dd class="inline break-words font-mono text-text-secondary">{{ service.connectionRefs?.join(', ') || 'none' }}</dd></div>
    </dl>

    <p class="text-[11px] leading-4 text-text-muted">{{ serviceSummary(service) }}</p>
    <p v-if="service.process && (service.process.restartCount || service.process.lastExitCode !== undefined)" class="text-[11px] text-text-muted">
      Restarts: <span class="font-mono text-text-secondary">{{ service.process.restartCount || 0 }}</span>
      <span v-if="service.process.lastExitCode !== undefined"> · last exit <span class="font-mono text-text-secondary">{{ service.process.lastExitCode }}</span></span>
    </p>
    <a v-if="service.url" :href="service.url" target="_blank" rel="noopener noreferrer" class="flex min-w-0 items-center gap-1 text-[11px] font-mono text-accent hover:underline" :title="service.url">
      <span class="truncate">{{ service.url }}</span>
      <ExternalLink class="h-3 w-3 shrink-0" :stroke-width="1.75" aria-hidden="true" />
    </a>

    <div class="flex flex-wrap items-center gap-1.5 border-t border-border-subtle pt-2">
      <button type="button" class="flex h-7 items-center gap-1 rounded-md border border-border-subtle px-2 text-[11px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled || persistedPrimary" :aria-label="`Set ${service.name} as primary preview`" @click="emit('set-primary')">
        <WandSparkles class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" />
        Set primary
      </button>
      <button type="button" class="flex h-7 items-center gap-1 rounded-md border border-border-subtle px-2 text-[11px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled" @click="emit('edit')">
        <Pencil class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" />
        Edit
      </button>
      <button type="button" class="h-7 rounded-md border border-border-subtle px-2 text-[11px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled || busyAction === 'toggle'" @click="emit('toggle')">
        <Loader2 v-if="busyAction === 'toggle'" class="inline h-3 w-3 animate-spin" :stroke-width="1.75" aria-hidden="true" />
        {{ service.enabled === false ? 'Enable' : 'Disable' }}
      </button>
      <button type="button" class="flex h-7 items-center gap-1 rounded-md border border-border-subtle px-2 text-[11px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled || busyAction === 'restart'" @click="emit('restart')">
        <RefreshCw class="h-3 w-3" :class="busyAction === 'restart' ? 'animate-spin' : ''" :stroke-width="1.75" aria-hidden="true" />
        Restart
      </button>
      <button type="button" class="flex h-7 items-center gap-1 rounded-md border border-border-subtle px-2 text-[11px] font-medium text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled || logsLoading" @click="emit('toggle-logs')">
        <Loader2 v-if="logsLoading" class="h-3 w-3 animate-spin" :stroke-width="1.75" aria-hidden="true" />
        <Terminal v-else class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" />
        {{ logsOpen ? 'Hide logs' : 'Logs' }}
      </button>
      <button type="button" class="flex h-7 items-center gap-1 rounded-md border border-danger/30 px-2 text-[11px] font-medium text-danger hover:bg-danger-subtle disabled:cursor-not-allowed disabled:opacity-50" :disabled="disabled || busyAction === 'delete'" @click="emit('delete')">
        <Trash2 class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" />
        Delete
      </button>
    </div>

    <div v-if="logsOpen" class="grid gap-2 rounded-md border border-border-subtle bg-surface-raised p-2.5" :aria-labelledby="logsHeadingID(service)" aria-live="polite">
      <div class="flex items-center justify-between gap-2">
        <div class="flex min-w-0 items-center gap-1.5">
          <FileText class="h-3.5 w-3.5 shrink-0 text-accent" :stroke-width="1.75" aria-hidden="true" />
          <h5 :id="logsHeadingID(service)" class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">Service logs</h5>
        </div>
        <button type="button" class="flex h-6 items-center gap-1 rounded-md border border-border-subtle px-1.5 text-[10px] text-text-muted hover:bg-surface-hover hover:text-text-primary" aria-label="Close logs" @click="emit('close-logs')"><X class="h-3 w-3" :stroke-width="1.75" aria-hidden="true" /> Close</button>
      </div>
      <div v-if="logsError" class="rounded-md border border-danger/30 bg-danger-subtle px-2.5 py-2 text-[11px] text-danger" role="alert">{{ logsError }}</div>
      <div v-else-if="logsLoading" class="flex items-center gap-2 px-1 py-3 text-[11px] text-text-muted" role="status"><Loader2 class="h-3.5 w-3.5 animate-spin text-accent" :stroke-width="1.75" /> Reading the latest service output…</div>
      <pre v-else class="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border-subtle bg-surface px-2.5 py-2 text-[11px] leading-4 text-text-secondary">{{ logsText || 'No logs have been emitted by the service yet.' }}</pre>
      <p class="text-[10px] leading-4 text-text-muted">Logs are read from this service's authenticated Infrastructure subresource and are bounded before display.</p>
    </div>
  </article>
</template>
