import { createRouter, createWebHistory, type RouteLocationNormalized } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { Permissions, type PermissionCode } from '@/auth/generated-permissions'
import { findNavigationItem, getFirstVisibleUserManagementPath, legacyRouteRedirects, userManagementPermissions } from '@/navigation'
import { downloadsRouteContract, mediaLibrariesRouteContract, organizationRouteContract, playersRouteContract, pluginsRouteContract, strmRouteContract } from '@/router/contracts'
import AppLayout from '@/layouts/AppLayout.vue'
import SetupView from '@/views/SetupView.vue'
import LoginView from '@/views/LoginView.vue'
import DashboardView from '@/views/DashboardView.vue'
import UserManagementView from '@/views/UserManagementView.vue'
import UsersView from '@/views/UsersView.vue'
import RolesView from '@/views/RolesView.vue'
import AuditView from '@/views/AuditView.vue'
import RuntimeLogsView from '@/views/RuntimeLogsView.vue'
import PlannedView from '@/views/PlannedView.vue'
import StorageView from '@/views/StorageView.vue'
import PlayersView from '@/views/PlayersView.vue'
import MediaRulesView from '@/views/MediaRulesView.vue'
import MediaLibrariesView from '@/views/MediaLibrariesView.vue'
import TasksView from '@/views/TasksView.vue'
import DownloadsView from '@/views/DownloadsView.vue'
import OrganizationView from '@/views/OrganizationView.vue'
import STRMView from '@/views/STRMView.vue'
import SettingsView from '@/views/SettingsView.vue'
import PluginsView from '@/views/PluginsView.vue'
import ForbiddenView from '@/views/ForbiddenView.vue'
import RecommendationsView from '@/views/RecommendationsView.vue'
import ExploreView from '@/views/ExploreView.vue'
import SitesView from '@/views/SitesView.vue'
import DiscoveryDetailView from '@/views/DiscoveryDetailView.vue'
import FollowsView from '@/views/FollowsView.vue'

function navigationMeta(id: string) {
  const item = findNavigationItem(id)
  return { title: item.label, permissionsAny: [...item.permissionsAny] }
}

const plannedRoutes = [
  ['subscriptions/workflows', 'workflows'],
  ['subscriptions/calendar', 'calendar'],
  ['automation/files', 'files'],
] as const

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/setup', component: SetupView, meta: { public: true, title: '首次设置' } },
    { path: '/login', component: LoginView, meta: { public: true, title: '登录' } },
    { path: '/forbidden', component: ForbiddenView, meta: { title: '无权访问' } },
    ...Object.entries(legacyRouteRedirects).map(([path, redirect]) => ({ path, redirect })),
    {
      path: '/',
      component: AppLayout,
      children: [
        { path: '', name: 'dashboard', component: DashboardView, meta: navigationMeta('dashboard') },
        { path: 'discovery/recommendations', name: 'recommendations', component: RecommendationsView, meta: navigationMeta('recommendations') },
        { path: 'discovery/details/:provider/:mediaType/:providerID', name: 'discovery-detail', component: DiscoveryDetailView, meta: navigationMeta('recommendations') },
        { path: 'discovery/explore', name: 'explore', component: ExploreView, meta: navigationMeta('explore') },
        { path: 'subscriptions/manage', name: 'subscriptions', component: FollowsView, meta: navigationMeta('subscriptions') },
        { path: 'system/connections', name: 'connections-storage', component: StorageView, meta: navigationMeta('connections-storage') },
        { path: playersRouteContract.path, name: playersRouteContract.name, component: PlayersView, meta: navigationMeta(playersRouteContract.navigationID) },
        { path: mediaLibrariesRouteContract.path, name: mediaLibrariesRouteContract.name, component: MediaLibrariesView, meta: navigationMeta(mediaLibrariesRouteContract.navigationID) },
        { path: 'system/media-rules', name: 'media-rules', component: MediaRulesView, meta: navigationMeta('media-rules') },
        { path: 'system/sites', name: 'sites', component: SitesView, meta: navigationMeta('sites') },
        { path: 'automation/tasks', name: 'tasks', component: TasksView, meta: navigationMeta('tasks') },
        { path: downloadsRouteContract.path, name: downloadsRouteContract.name, component: DownloadsView, meta: navigationMeta(downloadsRouteContract.navigationID) },
		{ path: organizationRouteContract.path, name: organizationRouteContract.name, component: OrganizationView, meta: navigationMeta(organizationRouteContract.navigationID) },
		{ path: strmRouteContract.path, name: strmRouteContract.name, component: STRMView, meta: navigationMeta(strmRouteContract.navigationID) },
        { path: 'system/settings', name: 'settings', component: SettingsView, meta: navigationMeta('settings') },
        { path: pluginsRouteContract.path, name: pluginsRouteContract.name, component: PluginsView, meta: navigationMeta(pluginsRouteContract.navigationID) },
        ...plannedRoutes.map(([path, id]) => ({ path, name: id, component: PlannedView, meta: navigationMeta(id) })),
        {
          path: 'system/users',
          name: 'user-management',
          component: UserManagementView,
          meta: { title: '用户管理', permissionsAny: [...userManagementPermissions] },
          children: [
            { path: 'accounts', name: 'user-accounts', component: UsersView, meta: { title: '账户 · 用户管理', permissionsAny: [Permissions.UsersRead] } },
            { path: 'roles', name: 'user-roles', component: RolesView, meta: { title: '角色与权限 · 用户管理', permissionsAny: [Permissions.RolesRead] } },
          ],
        },
        { path: 'logs/audit', name: 'audit-log', component: AuditView, meta: { title: '审计日志', permissionsAny: [Permissions.AuditRead] } },
        { path: 'logs/runtime', name: 'runtime-logs', component: RuntimeLogsView, meta: { title: '运行日志', permissionsAny: [Permissions.LogsRead] } },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

function redirectUserManagement(to: RouteLocationNormalized, permissions: readonly PermissionCode[]) {
  if (to.name !== 'user-management') return null
  return getFirstVisibleUserManagementPath(permissions) ?? '/forbidden'
}

router.beforeEach(async to => {
  const auth = useAuthStore()
  await auth.bootstrap()
  if (auth.recoveryRequired && to.path !== '/setup') return '/setup'
  if (auth.setupRequired && to.path !== '/setup') return '/setup'
  if (!auth.setupRequired && to.path === '/setup') return auth.user ? '/' : '/login'
  if (!auth.user && !to.meta.public) return { path: '/login', query: { redirect: to.fullPath } }
  if (auth.user && to.path === '/login') return '/'
  if (auth.user) {
    const managementRedirect = redirectUserManagement(to, auth.user.permissions)
    if (managementRedirect) return managementRedirect
  }
  if (to.meta.permissionsAny && !auth.canAny(to.meta.permissionsAny)) return '/forbidden'
  return true
})

router.afterEach(to => { document.title = `${String(to.meta.title ?? '管理端')} · OhMyCine Server` })
