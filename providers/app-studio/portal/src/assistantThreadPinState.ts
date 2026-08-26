export interface AssistantThreadPinStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

function defaultStorage(): AssistantThreadPinStorage | undefined {
  try {
    if (typeof window === 'undefined') return undefined
    return window.localStorage
  } catch {
    return undefined
  }
}

export function assistantThreadPinStorageKey(scopeKey: string): string {
  return `${scopeKey}:pins:v1`
}

export function readAssistantThreadPins(
  scopeKey: string,
  threadIDs: readonly string[],
  storage: AssistantThreadPinStorage | null | undefined = defaultStorage(),
): string[] {
  if (!scopeKey || !storage) return []
  const available = new Set(threadIDs)
  try {
    const raw = storage.getItem(assistantThreadPinStorageKey(scopeKey))
    if (!raw) return []
    const value: unknown = JSON.parse(raw)
    if (!Array.isArray(value)) return []
    const pins = [...new Set(value.filter((threadID): threadID is string =>
      typeof threadID === 'string' && available.has(threadID),
    ))]
    if (pins.length !== value.length) storage.setItem(assistantThreadPinStorageKey(scopeKey), JSON.stringify(pins))
    return pins
  } catch {
    return []
  }
}

export function toggleAssistantThreadPin(
  scopeKey: string,
  threadID: string,
  currentPins: readonly string[],
  storage: AssistantThreadPinStorage | null | undefined = defaultStorage(),
): string[] {
  if (!scopeKey || !threadID) return [...currentPins]
  const pins = currentPins.includes(threadID)
    ? currentPins.filter((candidate) => candidate !== threadID)
    : [threadID, ...currentPins]
  try {
    storage?.setItem(assistantThreadPinStorageKey(scopeKey), JSON.stringify(pins))
  } catch {
    // Pinning is a browser convenience; storage failures do not block chat.
  }
  return pins
}
