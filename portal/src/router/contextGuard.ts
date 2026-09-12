import { isNavigationFailure, NavigationFailureType, type Router } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useAdminStore } from '@/stores/admin'
import { useTenantStore } from '@/stores/tenant'
import { useRouteContextStore } from '@/stores/routeContext'
import { parsePortalScope, scopedPath } from '@/portalkit/navigation'
import { rememberPortalNext } from '@/auth/portalNext'

export function installContextGuard(router: Router): void {
  let navigation = 0
  // Vue Router skips guards when the user returns to the already-current URL
  // while another navigation is pending. Cancel that pending authority read too.
  router.afterEach((to, _from, failure) => {
    if (!isNavigationFailure(failure, NavigationFailureType.duplicated)) return
    const context = useRouteContextStore()
    if (context.state !== 'loading' && context.destination === to.fullPath) return
    navigation++
    context.invalidate()
    context.destination = to.fullPath
    const scope = parsePortalScope(to.path)
    if (scope && !to.meta.public) void context.resolve(scope, true)
  })
  router.beforeEach(async (to) => {
    const attempt = ++navigation
    const current = () => attempt === navigation
    const context = useRouteContextStore()
    context.destination = to.fullPath
    const scope = parsePortalScope(to.path)
    if (to.meta.public || !scope || context.state !== 'ready' ||
      scope.orgUUID !== context.target?.orgUUID || scope.workspaceUUID !== context.target?.workspaceUUID) {
      context.invalidate()
      if (!to.meta.public) context.state = 'loading'
    }
    const tenant = useTenantStore()
    tenant.routeManaged = true
    const auth = useAuthStore()
    if (!to.meta.public) {
      // A hard refresh restores the portal bearer synchronously from storage,
      // but its HttpOnly browser session is re-established asynchronously.
      // Wait for that bootstrap before mounting provider routes; otherwise a
      // private preview iframe can enter app authorization first and bounce
      // through embedded login before the shared session exists.
      await auth.detectAuthMode()
      if (!current()) return false
      // A bearer authenticates the portal caller; a cluster target is selected
      // separately by the workspace control and is intentionally empty on
      // organization-only routes.
      if (!auth.token) {
        rememberPortalNext(to.fullPath)
        return { name: 'login', query: { returnTo: to.fullPath } }
      }
    }
    // Admin-only routes: confirm access before loading. Non-admins are bounced to
    // the dashboard so the page never mounts and never fires admin data fetches.
    if (to.meta.admin) {
      const admin = useAdminStore()
      const ok = admin.isAdmin === null ? await admin.checkAccess() : admin.isAdmin
      if (!current()) return false
      if (!ok) return { name: 'landing' }
    }
    if (to.name === 'landing') {
      context.invalidate()
      context.state = 'loading'
      try {
        await tenant.fetchOrgs()
        if (!current()) return false
        const org = tenant.orgs.find((item) => item.uuid === tenant.orgUUID && !item.deletionRequestedAt)
          ?? tenant.orgs.find((item) => item.personal && !item.deletionRequestedAt)
          ?? tenant.orgs.find((item) => !item.deletionRequestedAt)
        if (!org || tenant.orgLoadState !== 'ready') return { name: 'organizations' }
        if (tenant.workspaceMode === 'organization') return `/${org.uuid}/settings/workspaces`
        await tenant.fetchWorkspaces(org.uuid, { selectDefault: false })
        if (!current()) return false
        const list = tenant.workspaceLoadStateByOrg[org.uuid] === 'ready' ? tenant.workspacesByOrg[org.uuid] ?? [] : []
        const workspace = list.find((item) => item.uuid === tenant.workspaceUUID && item.clusterName && !item.deletionRequestedAt)
          ?? list.find((item) => item.clusterName && !item.deletionRequestedAt)
        return scopedPath(workspace ? '/' : '/settings/workspaces', { orgUUID: org.uuid, workspaceUUID: workspace?.uuid ?? null })
      } catch {
        if (!current()) return false
        return { name: 'organizations' }
      }
    }
    if (scope && !to.meta.public) return await context.resolve(scope) && current() ? undefined : false
    context.invalidate()
  })
}
