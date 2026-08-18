<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { X, ShieldCheck, ShieldAlert, Loader2 } from 'lucide-vue-next'
import type { ProviderDTO, PermissionClaim } from '@/stores/providers'

const props = defineProps<{
  provider: ProviderDTO | null
  reviewExisting?: boolean
  claimDecisions?: Record<string, boolean>
  busy?: boolean
}>()

const emit = defineEmits<{
  cancel: []
  confirm: [accept: PermissionClaim[]]
}>()

// One boolean per claim, indexed by claim key. tenantScoped claims default
// to accepted; non-tenantScoped default to rejected so the user has to
// explicitly opt-in to anything that escapes their workspace.
const accepted = ref<Record<string, boolean>>({})
const dialogRef = ref<HTMLElement | null>(null)
let previousFocus: HTMLElement | null = null

const claimKey = (c: PermissionClaim) => `${c.group ?? ''}/${c.resource}`

watch(
  () => ({ provider: props.provider, claimDecisions: props.claimDecisions }),
  ({ provider: p, claimDecisions }) => {
    if (!p) {
      const target = previousFocus
      previousFocus = null
      if (target) void nextTick(() => target.focus())
      return
    }
    if (!previousFocus && document.activeElement instanceof HTMLElement) previousFocus = document.activeElement
    const next: Record<string, boolean> = {}
    for (const c of p.permissionClaims ?? []) {
      const key = claimKey(c)
      const canAccept = !!c.tenantScoped || !!p.allowUntrustedClaims
      next[key] = canAccept && (claimDecisions && Object.prototype.hasOwnProperty.call(claimDecisions, key)
        ? claimDecisions[key]
        : !!c.tenantScoped)
    }
    accepted.value = next
    void nextTick(() => dialogRef.value?.focus())
  },
  { immediate: true },
)

const claims = computed(() => props.provider?.permissionClaims ?? [])
const hasUntrustedAccepted = computed(() =>
  claims.value.some((c) => !c.tenantScoped && accepted.value[claimKey(c)]),
)

function toggle(c: PermissionClaim) {
  const k = claimKey(c)
  accepted.value = { ...accepted.value, [k]: !accepted.value[k] }
}

function onConfirm() {
  if (!props.provider || props.busy) return
  const accept = claims.value.filter((c) => accepted.value[claimKey(c)])
  emit('confirm', accept)
}

function cancel() {
  if (!props.busy) emit('cancel')
}

function onKeydown(event: KeyboardEvent) {
  if (!props.provider) return
  if (event.key === 'Escape' && !props.busy) {
    cancel()
    return
  }
  if (event.key !== 'Tab' || !dialogRef.value) return
  const focusable = [...dialogRef.value.querySelectorAll<HTMLElement>(
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
  )]
  if (focusable.length === 0) {
    event.preventDefault()
    dialogRef.value.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && (document.activeElement === first || document.activeElement === dialogRef.value)) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  previousFocus?.focus()
  previousFocus = null
})
</script>

<template>
  <div
    v-if="provider"
    class="fixed inset-0 z-[80] flex items-center justify-center bg-surface/80 backdrop-blur-sm p-4"
    @click.self="cancel"
  >
    <div
      ref="dialogRef"
      class="w-full max-w-lg rounded-xl border border-border-default bg-surface-raised shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-labelledby="provider-access-dialog-title"
      aria-describedby="provider-access-dialog-description"
      tabindex="-1"
    >
      <div class="flex items-center justify-between border-b border-border-subtle px-4 py-3">
        <div>
          <h2 id="provider-access-dialog-title" class="text-sm font-semibold text-text-primary">{{ reviewExisting ? 'Review access for' : 'Enable' }} {{ provider.displayName }}</h2>
          <p id="provider-access-dialog-description" class="mt-0.5 text-[11px] text-text-muted">
            {{ reviewExisting ? 'This provider changed its access request. Review it before the new claims are applied.' : 'Review what this provider will be able to access in your workspace.' }}
          </p>
        </div>
        <button
          type="button"
          class="text-text-muted hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-60"
          aria-label="Close provider access dialog"
          :disabled="busy"
          @click="cancel"
        >
          <X class="h-4 w-4" :stroke-width="1.75" />
        </button>
      </div>

      <div class="max-h-[60vh] overflow-y-auto px-4 py-3">
        <div v-if="claims.length === 0" class="rounded-lg border border-border-subtle bg-surface-overlay/50 px-3 py-4 text-center text-xs text-text-muted">
          This provider does not request access to any tenant resources.
          Clicking Confirm will bind its APIs into your workspace.
        </div>

        <ul v-else class="space-y-2">
          <li
            v-for="c in claims"
            :key="claimKey(c)"
            class="rounded-lg border bg-surface-overlay/30 px-3 py-2"
            :class="c.tenantScoped ? 'border-border-subtle' : 'border-warning/30'"
          >
            <label class="flex cursor-pointer items-start gap-3">
              <input
                type="checkbox"
                class="mt-1 h-3.5 w-3.5 accent-accent"
                :checked="!!accepted[claimKey(c)]"
                :disabled="busy || (!c.tenantScoped && !provider.allowUntrustedClaims)"
                @change="toggle(c)"
              />
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <ShieldCheck v-if="c.tenantScoped" class="h-3.5 w-3.5 text-success" :stroke-width="2" />
                  <ShieldAlert v-else class="h-3.5 w-3.5 text-warning" :stroke-width="2" />
                  <span class="font-mono text-[11px] text-text-primary">
                    {{ c.group ? `${c.group}/` : '' }}{{ c.resource }}
                  </span>
                </div>
                <p class="mt-0.5 text-[10px] text-text-muted">
                  Verbs: <span class="font-mono">{{ (c.verbs ?? []).join(', ') || 'none' }}</span>
                </p>
                <p v-if="!c.tenantScoped" class="mt-1 text-[10px] text-warning">
                  {{ provider.allowUntrustedClaims
                    ? 'Not marked tenant-scoped — the catalog owner approved this elevated request, but you must still choose whether to accept it.'
                    : 'Not marked tenant-scoped — the catalog owner has not approved this elevated request, so it cannot be accepted.' }}
                </p>
              </div>
            </label>
          </li>
        </ul>

        <div
          v-if="provider.edgeProxyAccess"
          class="mt-3 rounded-lg border border-border-subtle bg-surface-overlay/30 px-3 py-2"
        >
          <div class="flex items-center gap-2">
            <ShieldCheck class="h-3.5 w-3.5 text-success" :stroke-width="2" />
            <span class="text-[11px] font-medium text-text-primary">Edge cluster access</span>
          </div>
          <p class="mt-0.5 text-[10px] text-text-muted">
            This provider will get proxied read access to the edge clusters
            connected to this workspace (background connections through the
            hub's edges-proxy). Removed when you disable the provider.
          </p>
        </div>

        <div v-if="hasUntrustedAccepted" class="mt-3 rounded-md border border-warning/30 bg-warning-subtle px-3 py-2 text-[11px] text-warning">
          You've accepted at least one claim that isn't tenant-scoped. The
          provider's controllers will be able to read or write the indicated
          resources cluster-wide subject to its MaximalPermissionPolicy.
        </div>
      </div>

      <div class="flex items-center justify-end gap-2 border-t border-border-subtle px-4 py-3">
        <button
          type="button"
          class="rounded-lg border border-border-subtle px-3 py-1 text-[11px] font-medium text-text-muted transition-colors hover:text-text-primary"
          :disabled="busy"
          @click="cancel"
        >
          Cancel
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded-lg border border-accent/30 bg-accent/15 px-3 py-1 text-[11px] font-medium text-accent transition-colors hover:bg-accent/25 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="busy"
          @click="onConfirm"
        >
          <Loader2 v-if="busy" class="h-3 w-3 animate-spin" :stroke-width="2" />
          {{ reviewExisting ? 'Confirm access update' : 'Confirm & Enable' }}
        </button>
      </div>
    </div>
  </div>
</template>
