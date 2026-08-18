<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { X, ShieldCheck, ShieldAlert, Loader2 } from 'lucide-vue-next'
import type { ProviderDTO, PermissionClaim, ProviderAccessClaim, ProviderAccessState } from '@/stores/providers'

const props = defineProps<{
  provider: ProviderDTO | null
  // Update mode reuses the same consent surface when an already-bound
  // provider gains optional capabilities or the user changes their grants.
  mode?: 'enable' | 'update'
  access?: ProviderAccessState | null
  accessLoading?: boolean
  preselectClaims?: string[]
}>()

const emit = defineEmits<{
  cancel: []
  confirm: [accept: PermissionClaim[]]
}>()

// One boolean per claim, indexed by claim key. Required tenantScoped claims
// default to accepted; optional and non-tenantScoped claims default to
// rejected so optional capabilities and anything escaping the workspace need
// explicit consent.
const accepted = ref<Record<string, boolean>>({})
const busy = ref(false)

const claimKey = (c: PermissionClaim) => `${c.group ?? ''}/${c.resource}`

watch(
  () => [props.provider, props.mode, props.access, props.preselectClaims] as const,
  ([p, mode, access, preselectClaims]) => {
    if (!p) return
    const next: Record<string, boolean> = {}
    const requested = new Set(preselectClaims ?? [])
    for (const c of p.permissionClaims ?? []) {
      const current = access?.claims?.find((candidate) => claimKey(candidate) === claimKey(c))
      next[claimKey(c)] = mode === 'update'
        ? !!current?.accepted || (requested.has(claimKey(c)) && !!current?.offered)
        : (!!c.tenantScoped && !c.optional) || requested.has(claimKey(c))
    }
    accepted.value = next
    busy.value = false
  },
  { immediate: true },
)

const claims = computed(() => props.provider?.permissionClaims ?? [])
const unavailableRequestedClaims = computed(() => {
  const declared = new Set(claims.value.map(claimKey))
  return [...new Set(props.preselectClaims ?? [])].filter((key) => !declared.has(key))
})
const currentClaim = (c: PermissionClaim): ProviderAccessClaim | undefined =>
  props.access?.claims?.find((candidate) => claimKey(candidate) === claimKey(c))
const alreadyAccepted = (c: PermissionClaim): boolean => !!currentClaim(c)?.accepted
const isUnavailable = (c: PermissionClaim): boolean =>
  props.mode === 'update' && !!props.access && !currentClaim(c)?.offered
const hasUntrustedAccepted = computed(() =>
  claims.value.some((c) => !c.tenantScoped && accepted.value[claimKey(c)]),
)

function toggle(c: PermissionClaim) {
  if (alreadyAccepted(c) || isUnavailable(c)) return
  const k = claimKey(c)
  accepted.value = { ...accepted.value, [k]: !accepted.value[k] }
}

function onConfirm() {
  if (!props.provider) return
  busy.value = true
  const accept = claims.value.filter(
    (c) => accepted.value[claimKey(c)] && (props.mode !== 'update' || !alreadyAccepted(c)),
  )
  emit('confirm', accept)
}
</script>

<template>
  <div
    v-if="provider"
    class="fixed inset-0 z-[80] flex items-center justify-center bg-surface/80 backdrop-blur-sm p-4"
    @click.self="$emit('cancel')"
  >
    <div class="w-full max-w-lg rounded-xl border border-border-default bg-surface-raised shadow-2xl">
      <div class="flex items-center justify-between border-b border-border-subtle px-4 py-3">
        <div>
          <h2 class="text-sm font-semibold text-text-primary">
            {{ mode === 'update' ? 'Update access for' : 'Enable' }} {{ provider.displayName }}
          </h2>
          <p class="mt-0.5 text-[11px] text-text-muted">
            Review what this provider will be able to access in your workspace.
          </p>
          <p v-if="mode === 'update'" class="mt-1 text-[10px] text-warning">
            Access updates are additive. Existing grants remain authorized and cannot be removed here.
          </p>
        </div>
        <button class="text-text-muted hover:text-text-primary" @click="$emit('cancel')">
          <X class="h-4 w-4" :stroke-width="1.75" />
        </button>
      </div>

      <div class="max-h-[60vh] overflow-y-auto px-4 py-3">
        <div v-if="accessLoading" class="space-y-2" aria-label="Loading provider access">
          <div v-for="i in 3" :key="i" class="h-16 rounded-lg border border-border-subtle bg-surface-overlay/50 shimmer" />
        </div>

        <div v-else-if="claims.length === 0" class="rounded-lg border border-border-subtle bg-surface-overlay/50 px-3 py-4 text-center text-xs text-text-muted">
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
                :disabled="alreadyAccepted(c) || isUnavailable(c)"
                @change="toggle(c)"
              />
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <ShieldCheck v-if="c.tenantScoped" class="h-3.5 w-3.5 text-success" :stroke-width="2" />
                  <ShieldAlert v-else class="h-3.5 w-3.5 text-warning" :stroke-width="2" />
                  <span class="font-mono text-[11px] text-text-primary">
                    {{ c.group ? `${c.group}/` : '' }}{{ c.resource }}
                  </span>
                  <span v-if="c.optional" class="rounded-md border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                    Optional
                  </span>
                  <span v-if="alreadyAccepted(c)" class="rounded-sm border border-success/30 bg-success-subtle px-1.5 py-0.5 font-mono text-[10px] text-success">
                    {{ currentClaim(c)?.applied ? 'Applied' : 'Pending' }}
                  </span>
                </div>
                <p v-if="c.purpose" class="mt-1 text-[11px] leading-relaxed text-text-secondary">
                  {{ c.purpose }}
                </p>
                <p class="mt-0.5 text-[10px] text-text-muted">
                  Verbs: <span class="font-mono">{{ (c.verbs ?? []).join(', ') || 'none' }}</span>
                </p>
                <p v-if="c.optional" class="mt-1 text-[10px] text-accent">
                  Optional capability — leave unchecked to omit this access from the APIBinding.
                </p>
                <p v-if="isUnavailable(c)" class="mt-1 text-[10px] text-warning">
                  This capability is declared in the catalog but is not offered by the provider's current APIExport.
                </p>
                <p v-if="!c.tenantScoped" class="mt-1 text-[10px] text-warning">
                  Not marked tenant-scoped — provider could reach beyond your workspace.
                  Only accept if you trust the chart vendor.
                </p>
              </div>
            </label>
          </li>
        </ul>

        <div
          v-if="unavailableRequestedClaims.length"
          class="mt-3 rounded-lg border border-warning/30 bg-warning-subtle px-3 py-2"
          role="alert"
        >
          <div class="flex items-start gap-2">
            <ShieldAlert class="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-warning" :stroke-width="2" />
            <div>
              <p class="text-[11px] font-medium text-warning">Requested access is not offered</p>
              <p class="mt-1 text-[10px] leading-relaxed text-text-secondary">
                Deployments detected target APIs that are not in its current permission claims.
                A platform operator must add these optional claims to the Deployments CatalogEntry and APIExport before they can be authorized.
              </p>
              <ul class="mt-1 space-y-0.5">
                <li v-for="claim in unavailableRequestedClaims" :key="claim" class="font-mono text-[10px] text-text-primary">
                  {{ claim.startsWith('/') ? claim.slice(1) : claim }}
                </li>
              </ul>
            </div>
          </div>
        </div>

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
          class="rounded-lg border border-border-subtle px-3 py-1 text-[11px] font-medium text-text-muted transition-colors hover:text-text-primary"
          @click="$emit('cancel')"
        >
          Cancel
        </button>
        <button
          class="inline-flex items-center gap-1 rounded-lg border border-accent/30 bg-accent/15 px-3 py-1 text-[11px] font-medium text-accent transition-colors hover:bg-accent/25 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="busy || accessLoading"
          @click="onConfirm"
        >
          <Loader2 v-if="busy" class="h-3 w-3 animate-spin" :stroke-width="2" />
          {{ mode === 'update' ? 'Confirm & Update' : 'Confirm & Enable' }}
        </button>
      </div>
    </div>
  </div>
</template>
