<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { Permissions, type PermissionCode } from '@/auth/generated-permissions'

interface NavItem { label: string; to?: string; permission: PermissionCode; planned?: boolean; note?: string }

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const navItems: NavItem[] = [
  { label: '仪表盘', to: '/', permission: Permissions.DashboardRead },
  { label: '用户管理', to: '/users', permission: Permissions.UsersRead },
  { label: '角色与权限', to: '/roles', permission: Permissions.RolesRead },
  { label: '审计日志', to: '/audit', permission: Permissions.AuditRead },
  { label: '连接管理', permission: Permissions.ConnectionsRead, planned: true, note: '下一纵向切片' },
  { label: '存储目标', permission: Permissions.DestinationsRead, planned: true, note: '规划中' },
  { label: 'STRM Run', permission: Permissions.StrmRunsRead, planned: true, note: '规划中' },
  { label: '发现与下载', permission: Permissions.DiscoveryRead, planned: true, note: '后续阶段' },
]
const visibleItems = computed(() => navItems.filter(item => auth.can(item.permission)))

async function logout() { await auth.logout(); await router.replace('/login') }
</script>

<template>
  <div class="min-h-screen lg:grid lg:grid-cols-[17rem_1fr]">
    <aside class="border-b border-white/8 bg-slate-950/68 px-4 py-4 backdrop-blur-2xl lg:sticky lg:top-0 lg:h-screen lg:border-b-0 lg:border-r lg:px-5 lg:py-6">
      <div class="mb-5 flex items-center justify-between lg:mb-8">
        <RouterLink to="/" class="flex items-center gap-3">
          <span class="grid h-10 w-10 place-items-center rounded-3 bg-cyan-300 text-lg font-900 text-slate-950">O</span>
          <span><strong class="block tracking-wide">OhMyCine</strong><small class="text-slate-500">SERVER CONSOLE</small></span>
        </RouterLink>
        <button class="btn-secondary lg:hidden" @click="logout">退出</button>
      </div>
      <nav class="flex gap-2 overflow-x-auto pb-1 lg:block lg:space-y-1 lg:overflow-visible">
        <template v-for="item in visibleItems" :key="item.label">
          <RouterLink v-if="item.to" :to="item.to" class="min-w-max flex items-center justify-between rounded-3 px-3 py-2.5 text-sm transition hover:bg-white/7" :class="route.path === item.to ? 'bg-white/10 text-cyan-200' : 'text-slate-300'">
            {{ item.label }}
          </RouterLink>
          <div v-else class="min-w-max rounded-3 px-3 py-2.5 text-sm text-slate-600" :title="item.note">
            <span>{{ item.label }}</span><span class="ml-2 text-[10px] uppercase tracking-wider">{{ item.note }}</span>
          </div>
        </template>
      </nav>
      <div class="mt-8 hidden border-t border-white/8 pt-5 lg:block">
        <div class="text-sm font-600 text-slate-200">{{ auth.user?.display_name }}</div>
        <div class="mt-1 text-xs text-slate-500">@{{ auth.user?.username }} · {{ auth.user?.is_owner ? 'Owner' : auth.user?.roles.join(', ') }}</div>
        <button class="btn-secondary mt-4 w-full" @click="logout">安全退出</button>
      </div>
    </aside>
    <main class="min-w-0 p-4 sm:p-6 lg:p-8 xl:p-10">
      <RouterView v-slot="{ Component }"><Transition name="page" mode="out-in"><component :is="Component" /></Transition></RouterView>
    </main>
  </div>
</template>
