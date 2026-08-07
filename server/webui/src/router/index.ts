import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { Permissions } from '@/auth/generated-permissions'
import AppLayout from '@/layouts/AppLayout.vue'
import SetupView from '@/views/SetupView.vue'
import LoginView from '@/views/LoginView.vue'
import DashboardView from '@/views/DashboardView.vue'
import UsersView from '@/views/UsersView.vue'
import RolesView from '@/views/RolesView.vue'
import AuditView from '@/views/AuditView.vue'
import ForbiddenView from '@/views/ForbiddenView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/setup', component: SetupView, meta: { public: true, title: '首次设置' } },
    { path: '/login', component: LoginView, meta: { public: true, title: '登录' } },
    { path: '/forbidden', component: ForbiddenView, meta: { title: '无权访问' } },
    {
      path: '/', component: AppLayout, children: [
        { path: '', name: 'dashboard', component: DashboardView, meta: { title: '仪表盘', permissionsAny: [Permissions.DashboardRead] } },
        { path: 'users', name: 'users', component: UsersView, meta: { title: '用户管理', permissionsAny: [Permissions.UsersRead] } },
        { path: 'roles', name: 'roles', component: RolesView, meta: { title: '角色与权限', permissionsAny: [Permissions.RolesRead] } },
        { path: 'audit', name: 'audit', component: AuditView, meta: { title: '审计日志', permissionsAny: [Permissions.AuditRead] } },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async to => {
  const auth = useAuthStore()
  await auth.bootstrap()
  if (auth.recoveryRequired && to.path !== '/setup') return '/setup'
  if (auth.setupRequired && to.path !== '/setup') return '/setup'
  if (!auth.setupRequired && to.path === '/setup') return auth.user ? '/' : '/login'
  if (!auth.user && !to.meta.public) return { path: '/login', query: { redirect: to.fullPath } }
  if (auth.user && to.path === '/login') return '/'
  if (to.meta.permissionsAny && !auth.canAny(to.meta.permissionsAny)) return '/forbidden'
  return true
})

router.afterEach(to => { document.title = `${String(to.meta.title ?? '管理端')} · OhMyCine Server` })
