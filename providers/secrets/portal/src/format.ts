// Small display formatters shared by the two views.

// fmtAge renders a kubectl-style age ("42s", "12m", "3h", "5d") from an RFC3339
// creation timestamp. Empty/unparseable input renders as an em dash.
export function fmtAge(ts?: string): string {
  if (!ts) return '—'
  const t = Date.parse(ts)
  if (Number.isNaN(t)) return '—'
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.floor(h / 24)}d`
}

// shortHash abbreviates a "sha256:<hex>" content hash to its first 8 hex chars
// for table cells; the full value belongs in a title attribute.
export function shortHash(v?: string): string {
  if (!v) return '—'
  const hex = v.startsWith('sha256:') ? v.slice('sha256:'.length) : v
  return hex.slice(0, 8)
}

// fmtTime renders an RFC3339 timestamp as a compact local date-time.
export function fmtTime(ts?: string): string {
  if (!ts) return '—'
  const t = Date.parse(ts)
  if (Number.isNaN(t)) return ts
  return new Date(t).toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}
