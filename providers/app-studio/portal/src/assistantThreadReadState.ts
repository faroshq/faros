export interface AssistantThreadReadStateThread {
  id: string
  updatedAt: string
}

export interface AssistantThreadReadStateStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

interface StoredAssistantThreadReadState {
  version: 1
  seen: Record<string, string>
  manualUnread: Record<string, true>
}

function defaultStorage(): AssistantThreadReadStateStorage | undefined {
  try {
    if (typeof window === 'undefined') return undefined
    return window.localStorage
  } catch {
    return undefined
  }
}

export function assistantThreadReadStateStorageKey(scopeKey: string): string {
  return `${scopeKey}:read-state:v1`
}

function readState(
  scopeKey: string,
  storage: AssistantThreadReadStateStorage | null | undefined,
): StoredAssistantThreadReadState | undefined {
  if (!scopeKey || !storage) return undefined
  try {
    const raw = storage.getItem(assistantThreadReadStateStorageKey(scopeKey))
    if (!raw) return undefined
    const value = JSON.parse(raw) as Partial<StoredAssistantThreadReadState>
    if (value.version !== 1 || !value.seen || typeof value.seen !== 'object') return undefined
    const manualUnread = value.manualUnread && typeof value.manualUnread === 'object'
      ? Object.fromEntries(Object.keys(value.manualUnread).map((threadID) => [threadID, true as const]))
      : {}
    return { version: 1, seen: value.seen, manualUnread }
  } catch {
    return undefined
  }
}

function writeState(
  scopeKey: string,
  state: StoredAssistantThreadReadState,
  storage: AssistantThreadReadStateStorage | null | undefined,
) {
  if (!scopeKey || !storage) return
  try {
    storage.setItem(assistantThreadReadStateStorageKey(scopeKey), JSON.stringify(state))
  } catch {
    // Read markers are a browser convenience; storage failures never block chat.
  }
}

function updatedAfterSeen(updatedAt: string, seenAt: string): boolean {
  const updated = Date.parse(updatedAt)
  const seen = Date.parse(seenAt)
  if (Number.isFinite(updated) && Number.isFinite(seen)) return updated > seen
  return updatedAt !== seenAt
}

/**
 * Return threads that changed after their last successful selection. The first
 * observation establishes a quiet baseline, while the active thread is always
 * considered read because its transcript is the conversation currently shown.
 */
export function reconcileAssistantThreadReadState(
  scopeKey: string,
  threads: readonly AssistantThreadReadStateThread[],
  activeThreadID: string,
  storage: AssistantThreadReadStateStorage | null | undefined = defaultStorage(),
): string[] {
  if (!scopeKey) return []
  const stored = readState(scopeKey, storage)
  const seen: Record<string, string> = {}
  const manualUnread: Record<string, true> = {}
  const unread: string[] = []

  for (const thread of threads) {
    const updatedAt = thread.updatedAt?.trim()
    if (!thread.id || !updatedAt) continue
    const previous = stored?.seen[thread.id]
    if (thread.id === activeThreadID) {
      seen[thread.id] = updatedAt
      continue
    }
    if (stored?.manualUnread[thread.id]) {
      seen[thread.id] = previous || updatedAt
      manualUnread[thread.id] = true
      unread.push(thread.id)
      continue
    }
    if (!stored || !previous) {
      seen[thread.id] = updatedAt
      continue
    }
    seen[thread.id] = previous
    if (updatedAfterSeen(updatedAt, previous)) unread.push(thread.id)
  }

  writeState(scopeKey, { version: 1, seen, manualUnread }, storage)
  return unread
}

export function markAssistantThreadRead(
  scopeKey: string,
  thread: AssistantThreadReadStateThread,
  storage: AssistantThreadReadStateStorage | null | undefined = defaultStorage(),
) {
  if (!scopeKey || !thread.id || !thread.updatedAt) return
  const stored = readState(scopeKey, storage) ?? { version: 1, seen: {}, manualUnread: {} }
  const { [thread.id]: _removed, ...manualUnread } = stored.manualUnread
  writeState(scopeKey, {
    version: 1,
    seen: { ...stored.seen, [thread.id]: thread.updatedAt },
    manualUnread,
  }, storage)
}

export function markAssistantThreadUnread(
  scopeKey: string,
  thread: AssistantThreadReadStateThread,
  storage: AssistantThreadReadStateStorage | null | undefined = defaultStorage(),
) {
  if (!scopeKey || !thread.id || !thread.updatedAt) return
  const stored = readState(scopeKey, storage) ?? { version: 1, seen: {}, manualUnread: {} }
  writeState(scopeKey, {
    version: 1,
    seen: { ...stored.seen, [thread.id]: stored.seen[thread.id] || thread.updatedAt },
    manualUnread: { ...stored.manualUnread, [thread.id]: true },
  }, storage)
}
