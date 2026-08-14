// CANONICAL SOURCE — provider-sdk/portalkit. Do not edit vendored copies under
// providers/*/portal/src/portalkit/; edit here and run `make sync-portalkit`.
//
// Shared, framework-agnostic scaffolding for a provider's dashboard tile — the
// <faros-dashboard-tile-{name}> element the console mounts on its dashboard
// page. Rendering stays with each provider (its own resources, its own words);
// what lives here is the plumbing every tile was otherwise going to re-derive:
//
//   - the poll loop and its cadence
//   - "no workspace selected yet" and "provider not bound yet" as EMPTY, not
//     as an error banner — a tile is glanceable chrome, and a red box for a
//     workspace that simply has not been bootstrapped is noise
//   - the faros-navigate dispatch that turns a row into a console route
//   - the recency sort + cap that keeps every tile the same height
//
// Deliberately plain TypeScript, like the rest of portalkit: it is synced into
// vanilla-TS and Vue portals alike, so it must not import a framework.

// TileContext is the subset of farosContext a tile needs. The console pushes
// the full object; tiles only ever read these.
export interface TileContext {
  token?: string | null
  tenant?: string | null
  basePath?: string
}

// Note there is no `theme` here, though the console pushes one. A tile renders
// inside the console's own DOM and inherits its CSS variables, so branching on
// the theme is how two cards end up disagreeing about what dark means. Tiles
// that need a colour take it from the shared palette classes.

// TILE_POLL_MS matches the console's own list cadence. Anything tighter spends
// hub round trips on a card users glance at.
export const TILE_POLL_MS = 30000

// TILE_ROWS is the row cap every tile shares so the dashboard grid stays even.
export const TILE_ROWS = 4

// benignReasons are the API reasons that mean "nothing here yet" rather than
// "something is broken": no workspace selected, or the provider's APIs are not
// bound in this workspace. Both are ordinary states for a fresh tenant.
const benignReasons = new Set([
  'TenantMissing',
  'APIBindingMissing',
  'NotFound',
])

export interface TileError {
  reason?: string
  message?: string
}

// isBenignTileError reports whether a failed load should render as an empty
// tile. Callers that get true must clear their error state, not set it.
export function isBenignTileError(err: unknown): boolean {
  if (!err) return true
  const reason = (err as TileError).reason
  if (reason && benignReasons.has(reason)) return true
  const message = ((err as TileError).message ?? String(err)).toLowerCase()
  // The hub answers a not-yet-bootstrapped workspace with a 404 whose body
  // names the missing resource rather than a typed reason.
  return message.includes('server could not find the requested resource')
    || message.includes('no workspace selected')
}

// tileErrorText renders a failure for the tile's one-line error slot.
export function tileErrorText(err: unknown): string {
  const reason = (err as TileError).reason
  const message = (err as TileError).message ?? String(err)
  return reason ? `${reason} — ${message}` : message
}

// mostRecent sorts by an ISO timestamp accessor, newest first, and caps the
// list. Undated items sort last rather than being dropped — a row with no
// timestamp is still a row the user may need to click.
export function mostRecent<T>(items: T[], at: (item: T) => string | undefined, limit = TILE_ROWS): T[] {
  return [...items]
    .sort((a, b) => (at(b) || '').localeCompare(at(a) || ''))
    .slice(0, limit)
}

// countBy tallies items by a key accessor — the breakdown row every tile shows
// under its headline.
export function countBy<T>(items: T[], key: (item: T) => string): Record<string, number> {
  const out: Record<string, number> = {}
  for (const item of items) {
    const k = key(item)
    if (!k) continue
    out[k] = (out[k] ?? 0) + 1
  }
  return out
}

// navigateFromTile bubbles a console route request out of the tile. The
// console's DashboardTile listener turns it into
// router.push('/providers/{name}/' + path), so tiles never import a router.
export function navigateFromTile(el: Element | null | undefined, path: string): void {
  el?.dispatchEvent(new CustomEvent('faros-navigate', { detail: { path }, bubbles: true }))
}

export interface TilePoller {
  // start runs load immediately and then on the interval.
  start(): void
  stop(): void
  // refresh runs load once, out of band — for a context change.
  refresh(): void
}

// createTilePoller owns the load-now-then-poll lifecycle, including the
// overlap guard: a slow load must not stack up behind the interval, which is
// how a tile against a struggling backend turns into a request flood.
export function createTilePoller(load: () => Promise<void>, intervalMs = TILE_POLL_MS): TilePoller {
  let handle: ReturnType<typeof setInterval> | null = null
  let inFlight = false

  const run = () => {
    if (inFlight) return
    inFlight = true
    void load().finally(() => {
      inFlight = false
    })
  }

  return {
    start() {
      run()
      if (handle === null) handle = setInterval(run, intervalMs)
    },
    stop() {
      if (handle !== null) {
        clearInterval(handle)
        handle = null
      }
    },
    refresh: run,
  }
}

// hasWorkspaceContext reports whether the console has pushed a workspace yet.
// Loading before it has produces a guaranteed failure, so tiles check first and
// render empty instead.
export function hasWorkspaceContext(ctx: TileContext | null | undefined): boolean {
  return !!ctx && !!ctx.tenant
}
