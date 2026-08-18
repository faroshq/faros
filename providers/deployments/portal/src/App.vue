<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import DeploymentsListView from './views/DeploymentsListView.vue'
import DeploymentDetailView from './views/DeploymentDetailView.vue'
import { setBasePath, setTenant, setToken } from './api'
import { parseRoute } from './route'
import type { FarosContext, SyncClaimReference } from './types'

const props = defineProps<{ ctx: FarosContext | null }>()
const route = computed(() => parseRoute(props.ctx?.subPath))
const hasTenant = computed(() => !!props.ctx?.tenant)

watch(() => props.ctx?.basePath, value => setBasePath(value), { immediate: true })
watch(() => props.ctx?.token, value => setToken(value), { immediate: true })
watch(() => props.ctx?.tenant, value => setTenant(value), { immediate: true })

const rootRef = ref<HTMLElement | null>(null)
function navigate(path: string): void {
  rootRef.value?.dispatchEvent(new CustomEvent('faros-navigate', { detail: { path }, bubbles: true }))
}

function openSync(name: string): void {
  navigate('deployments/' + encodeURIComponent(name))
}

function authorize(name: string, claims: SyncClaimReference[]): void {
  const params = new URLSearchParams({
    configure: 'deployments',
    return: `/providers/deployments/deployments/${encodeURIComponent(name)}`,
  })
  for (const claim of claims) params.append('claim', `${claim.group}/${claim.resource}`)
  navigate(`/providers?${params.toString()}`)
}
</script>

<template>
  <div ref="rootRef" class="app">
    <template v-if="!hasTenant">
      <section class="page state-card">
        <p class="eyebrow">Deployments</p>
        <h1 class="page-title">Select a workspace</h1>
        <p class="muted">Choose a workspace to inspect Git desired-state synchronization.</p>
      </section>
    </template>
    <template v-else>
      <DeploymentDetailView
        v-if="route.name"
        :name="route.name"
        :tenant="props.ctx?.tenant"
        @back="navigate('deployments')"
        @authorize="claims => authorize(route.name!, claims)"
      />
      <DeploymentsListView v-else :tenant="props.ctx?.tenant" @open="openSync" />
    </template>
  </div>
</template>
