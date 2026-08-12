import { createRouter, createWebHistory, type RouteLocationNormalized } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { Permissions, type PermissionCode } from '@/auth/generated-permissions'
import { findNavigationItem, getFirstVisibleUserManagementPath, legacyRouteRedirects, userManagementPermissions } from '@/navigation'
import AppLayout from '@/layouts/AppLayout.vue'
import SetupView from '@/views/SetupView.vue'
import LoginView from '@/views/LoginView.vue'
import DashboardView from '@/views/DashboardView.vue'
import UserManagementView from '@/views/UserManagementView.vue'
import UsersView from '@/views/UsersView.vue'
import RolesView from '@/views/RolesView.vue'
import AuditView from '@/views/AuditView.vue'
import PlannedView from '@/views/PlannedView.vue'
import StorageView from '@/views/StorageView.vue'
import ForbiddenView from '@/views/ForbiddenView.vue'

function navigationMeta(id: string) {
  const item = findNavigationItem(id)
  return { title: item.label, permissionsAny: [...item.permissionsAny] }
}

const plannedRoutes = [
  ['discovery/recommendations', 'recommendations'],
  ['discovery/explore', 'explore'],
  ['subscriptions/manage', 'subscriptions'],
  ['subscriptions/workflows', 'workflows'],
  ['subscriptions/calendar', 'calendar'],
  ['automation/tasks', 'tasks'],
  ['automation/downloads', 'downloads'],
  ['automation/organization', 'organization'],
  ['automation/strm-import', 'strm-import'],
  ['automation/files', 'files'],
  ['system/sites', 'sites'],
  ['system/plugins', 'plugins'],
  ['system/settings', 'settings'],
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
        { path: 'system/connections', name: 'connections-storage', component: StorageView, meta: navigationMeta('connections-storage') },
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
