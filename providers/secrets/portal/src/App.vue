<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { FarosContext } from './types'
import { setBasePath, setTenant, setToken } from './api'
import StoresView from './views/StoresView.vue'
import SyncedSecretsView from './views/SyncedSecretsView.vue'
import ConfirmDialog from './portalkit/ConfirmDialog.vue'

// Sub-path routing (the shell pushes the trailing /providers/secrets/<sub> segment):
//   ''  | 'stores'   → SecretStores
//   'synced'         → SyncedSecrets
const props = defineProps<{ ctx: FarosContext | null }>()

type Page = 'stores' | 'synced'

function parse(sub: string | null | undefined): Page {
  const s = (sub ?? '').replace(/^\/+|\/+$/g, '')
  if (s === 'synced' || s.startsWith('synced/')) return 'synced'
  return 'stores'
}

const page = computed(() => parse(props.ctx?.subPath))

// Feed identity into the api client whenever the shell re-pushes context.
watch(() => props.ctx?.basePath, v => setBasePath(v), { immediate: true })
watch(() => props.ctx?.token, v => setToken(v), { immediate: true })
watch(() => props.ctx?.tenant, v => setTenant(v), { immediate: true })

const hasTenant = computed(() => !!props.ctx?.tenant)

// navigate dispatches a faros-navigate CustomEvent from the component root so
// it bubbles up to the <faros-provider-secrets> element, where ProviderFrame
// listens and pushes the shell's vue-router. detail.path is the trailing
// segment the shell appends to /providers/secrets/.
const rootRef = ref<HTMLElement | null>(null)
function navigate(path: string) {
  const el = rootRef.value
  if (!el) return
  el.dispatchEvent(new CustomEvent('faros-navigate', { detail: { path }, bubbles: true }))
}
</script>

<template>
  <div ref="rootRef" class="app">
    <nav class="tabs">
      <button :class="{ active: page === 'stores' }" @click="navigate('stores')">Stores</button>
      <button :class="{ active: page === 'synced' }" @click="navigate('synced')">Synced Secrets</button>
    </nav>

    <p v-if="!hasTenant" class="empty">Select a workspace to manage secrets.</p>

    <template v-else>
      <SyncedSecretsView v-if="page === 'synced'" />
      <StoresView v-else />
    </template>

    <ConfirmDialog />
  </div>
</template>
