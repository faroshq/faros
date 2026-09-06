// Matches the provider API's ServiceEdgeLabelValue. The spec remains the
// authority; this label lets the API filter the relation before pagination.
export const serviceEdgeLabel = 'edges.faros.sh/edge'

export async function serviceEdgeLabelValue(edgeName: string): Promise<string> {
  if (edgeName.length <= 63 && (edgeName === '' || /^[A-Za-z0-9]([-_.A-Za-z0-9]*[A-Za-z0-9])?$/.test(edgeName))) {
    return edgeName
  }
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(edgeName))
  const hex = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
  return `sha256-${hex.slice(0, 56)}`
}
