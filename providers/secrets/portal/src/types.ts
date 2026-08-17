// FarosContext is the shell→element contract: the portal sets element
// .farosContext after auth and on every workspace/token change. subPath is the
// trailing segment of /providers/secrets/<subPath> the shell's router pushes.
export interface FarosContext {
  token?: string | null
  user?: { email?: string; sub?: string } | null
  tenant?: string | null
  theme?: 'light' | 'dark' | 'system'
  basePath?: string
  subPath?: string
}

// ErrorResponse is the {reason, message} contract the views render against.
// kcp Status errors are mapped into this shape in api.ts.
export interface ErrorResponse {
  reason: string
  message: string
}

// ConditionInfo is a single status condition, surfaced verbatim so the
// reason/message a controller recorded is visible (not just flattened to a
// badge). lastTransitionTime tells "never reconciled" apart from "just failed".
export interface ConditionInfo {
  type: string
  status: string
  reason?: string
  message?: string
  lastTransitionTime?: string
}

// SecretStoreRow is the portal's view of one (cluster-scoped) SecretStore:
// the vault coordinates, the credential Secret it references, and the
// Validated/Ready outcome — plus every condition verbatim so the row can be
// expanded into a ConditionsPanel without a second fetch.
export interface SecretStoreRow {
  name: string
  backend: string
  address: string
  mount?: string
  vaultNamespace?: string
  secretName: string
  secretNamespace?: string
  secretKey?: string
  backendVersion?: string
  validated: boolean
  ready: boolean
  message?: string
  creationTimestamp?: string
  generation?: number
  observedGeneration?: number
  conditions: ConditionInfo[]
}

// SyncedSecretDataMap is one spec.data entry: remote property → Secret key.
export interface SyncedSecretDataMap {
  secretKey: string
  path: string
  property?: string
}

// SyncedSecretRow is the portal's view of one (namespaced) SyncedSecret:
// where it reads from, what it projects, and the last sync outcome.
export interface SyncedSecretRow {
  name: string
  namespace: string
  store: string
  targetSecret: string
  refreshInterval: string
  dataFrom: string[]
  data: SyncedSecretDataMap[]
  syncedKeys?: number
  syncedVersion?: string
  lastSyncTime?: string
  ready: boolean
  message?: string
  creationTimestamp?: string
  generation?: number
  observedGeneration?: number
  conditions: ConditionInfo[]
}
