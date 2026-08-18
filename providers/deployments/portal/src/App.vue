<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import DeploymentsListView from './views/DeploymentsListView.vue'
import DeploymentDetailView from './views/DeploymentDetailView.vue'
import { setBasePath, setTenant, setToken } from './api'
import { parseRoute } from './route'
import type { FarosContext } from './types'

const props = defineProps<{ ctx: FarosContext | null }>()

const route = computed(() => parseRoute(props.ctx?.subPath))
const hasTenant = computed(() => !!props.ctx?.tenant)

watch(() => props.ctx?.basePath, value => setBasePath(value), { immediate: true })
watch(() => props.ctx?.token, value => setToken(value), { immediate: true })
watch(() => props.ctx?.tenant, value => setTenant(value), { immediate: true })

const rootRef = ref<HTMLElement | null>(null)
function navigate(path: string): void {
  rootRef.value?.dispatchEvent(new CustomEvent('faros-navigate', {
    detail: { path },
    bubbles: true,
  }))
}

function openDeployment(name: string): void {
  navigate('deployments/' + encodeURIComponent(name))
}
</script>

<template>
  <div ref="rootRef" class="app">
    <template v-if="!hasTenant">
      <section class="page state-card">
        <p class="eyebrow">Deployments</p>
        <h1 class="page-title">Select a workspace</h1>
        <p class="muted">Choose a workspace to inspect projected release intent and runtime evidence.</p>
      </section>
    </template>
    <template v-else>
      <DeploymentDetailView
        v-if="route.name"
        :name="route.name"
        :tenant="props.ctx?.tenant"
        @back="navigate('deployments')"
      />
      <DeploymentsListView
        v-else
        :tenant="props.ctx?.tenant"
        @open="openDeployment"
      />
    </template>
  </div>
</template>
