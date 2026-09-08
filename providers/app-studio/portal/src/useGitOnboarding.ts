import { computed, ref, watch } from 'vue'
import type { FarosContext } from './types'

export function gitOnboardingStorageKey(ctx: FarosContext | null): string | null {
  const user = ctx?.user?.sub || ctx?.user?.userId || ctx?.user?.email
  if (!user || !ctx?.orgUUID || !ctx?.workspaceUUID) return null
  return `faros:app-studio:git-skipped:${JSON.stringify([user, ctx.orgUUID, ctx.workspaceUUID])}`
}

export function useGitOnboarding(context: () => FarosContext | null) {
  const skipped = ref(false)
  const key = computed(() => gitOnboardingStorageKey(context()))
  watch(key, (value) => {
    try { skipped.value = value ? localStorage.getItem(value) === '1' : false }
    catch { skipped.value = false }
  }, { immediate: true })
  function skip() {
    skipped.value = true
    try { if (key.value) localStorage.setItem(key.value, '1') } catch { /* Session-only when storage is unavailable. */ }
  }
  return { skipped, skip }
}
