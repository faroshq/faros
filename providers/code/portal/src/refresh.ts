import { reactive } from 'vue'

export interface LatestRefreshController {
  request(): void
  invalidate(): void
  stop(): void
  isCurrent(requestID: number): boolean
}

export type OperationPhase = 'creating' | 'saving' | 'deleting'

export interface OperationLocks {
  acquire(key: string, phase?: OperationPhase): boolean
  release(key: string): void
  isLocked(key: string): boolean
  phase(key: string): OperationPhase | undefined
  tombstone(key: string, uid?: string): void
  isTombstoned(key: string, uid?: string): boolean
  reconcile(kind: string, resources: readonly { name: string; uid?: string }[]): void
}

// Operation ownership is local to a mounted route. App.vue remounts routes when
// tenant/token or resource identity changes, preventing old-context mutations
// from leaking locks or tombstones into the new route.
export function createOperationLocks(): OperationLocks {
  const locked = reactive(new Map<string, OperationPhase>())
  const tombstones = reactive(new Map<string, string | null>())
  return {
    acquire(key, phase = 'saving') {
      if (locked.has(key)) return false
      locked.set(key, phase)
      return true
    },
    release(key) {
      locked.delete(key)
    },
    isLocked(key) {
      return locked.has(key)
    },
    phase(key) {
      return locked.get(key)
    },
    tombstone(key, uid) {
      tombstones.set(key, uid ?? null)
    },
    isTombstoned(key, uid) {
      if (!tombstones.has(key)) return false
      const expectedUID = tombstones.get(key)
      return expectedUID === null ? uid === undefined : uid === undefined || expectedUID === uid
    },
    reconcile(kind, resources) {
      const present = new Map(resources.map(resource => [resource.name, resource.uid]))
      const prefix = `${kind}:`
      for (const [key, expectedUID] of [...tombstones]) {
        if (!key.startsWith(prefix)) continue
        const name = key.slice(prefix.length)
        const currentUID = present.get(name)
        if (!present.has(name) ||
          (expectedUID !== null && currentUID !== undefined && currentUID !== expectedUID) ||
          (expectedUID === null && currentUID !== undefined)) {
          tombstones.delete(key)
        }
      }
    },
  }
}

export function operationKey(kind: string, name: string): string {
  return `${kind}:${name}`
}

// Serializes timer/manual/mutation refreshes. A request made while one is in
// flight supersedes that result and queues one latest read, so stale responses
// cannot overwrite newer state.
export function createLatestRefreshController(
  task: (requestID: number) => Promise<void>,
): LatestRefreshController {
  let generation = 0
  let active = false
  let queued = false
  let stopped = false

  const request = () => {
    if (stopped) return
    if (active) {
      generation += 1
      queued = true
      return
    }
    const requestID = ++generation
    active = true
    void task(requestID).catch(() => {
      // Tasks own user-facing error state.
    }).finally(() => {
      active = false
      if (queued && !stopped) {
        queued = false
        request()
      }
    })
  }

  return {
    request,
    invalidate() {
      if (stopped) return
      generation += 1
      if (active) queued = true
    },
    stop() {
      stopped = true
      generation += 1
      queued = false
    },
    isCurrent(requestID) {
      return !stopped && requestID === generation
    },
  }
}
