export interface Route {
  page: 'deployments'
  name?: string
}

export function parseRoute(subPath?: string | null): Route {
  const clean = (subPath ?? '').replace(/^\/+|\/+$/g, '')
  if (!clean || clean === 'deployments') return { page: 'deployments' }
  const [head, name] = clean.split('/')
  if (head === 'deployments' && name) {
    try {
      const decodedName = decodeURIComponent(name)
      if (decodedName) return { page: 'deployments', name: decodedName }
    } catch {
      // A stale or malformed shell route should never break the provider frame.
    }
  }
  // A stale shell route should never leave an empty provider frame.
  return { page: 'deployments' }
}
