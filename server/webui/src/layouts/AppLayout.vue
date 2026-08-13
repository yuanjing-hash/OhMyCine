<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Permissions } from '@/auth/generated-permissions'
import { useAuthStore } from '@/stores/auth'
import { buildVisibleNavigation, dashboardNavigation, type NavigationGroupID } from '@/navigation'
import ThemeToggle from '@/components/ThemeToggle.vue'

type PanelID = 'search' | 'logs' | 'notifications' | 'account'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const sidebarCollapsed = ref(localStorage.getItem('omc:sidebar-collapsed') === 'true')
const mobileDrawerOpen = ref(false)
const activePanel = ref<PanelID | null>(null)
const panelElement = ref<HTMLElement | null>(null)
const drawerElement = ref<HTMLElement | null>(null)
const menuButton = ref<HTMLButtonElement | null>(null)
const searchButton = ref<HTMLButtonElement | null>(null)
const lastTrigger = ref<HTMLElement | null>(null)
const compactNavigation = ref(window.matchMedia('(max-width: 1023px)').matches)
const expandedGroups = reactive<Record<NavigationGroupID, boolean>>({ discovery: true, subscriptions: true, automation: true, system: true })
const compactNavigationQuery = window.matchMedia('(max-width: 1023px)')

const visibleGroups = computed(() => buildVisibleNavigation(auth.user?.permissions ?? []))
const showDashboard = computed(() => auth.can(Permissions.DashboardRead))
const pageTitle = computed(() => String(route.meta.title ?? '管理端').split(' · ')[0])
const panelTitle = computed(() => ({ search: '全局搜索', logs: '日志中心', notifications: '通知中心', account: '账户菜单' }[activePanel.value ?? 'search']))

function isCurrentPath(path: string) {
  return path === '/' ? route.path === '/' : route.path === path || route.path.startsWith(`${path}/`)
}

function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
  localStorage.setItem('omc:sidebar-collapsed', String(sidebarCollapsed.value))
}

function toggleGroup(group: NavigationGroupID) { expandedGroups[group] = !expandedGroups[group] }

function openDrawer() {
  activePanel.value = null
  mobileDrawerOpen.value = true
  lastTrigger.value = menuButton.value
  void nextTick(() => focusInitialElement(drawerElement.value))
}

function openPanel(panel: PanelID, trigger: HTMLElement | null) {
  mobileDrawerOpen.value = false
  activePanel.value = panel
  lastTrigger.value = trigger
  void nextTick(() => focusInitialElement(panelElement.value))
}

function togglePanel(panel: PanelID, event: Event) {
  const trigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  if (activePanel.value === panel) {
    closePanel(true)
    return
  }
  openPanel(panel, trigger)
}

function closePanel(restoreFocus = false) {
  activePanel.value = null
  if (restoreFocus) void nextTick(() => lastTrigger.value?.focus())
}

function closeDrawer(restoreFocus = false) {
  mobileDrawerOpen.value = false
  if (restoreFocus) void nextTick(() => menuButton.value?.focus())
}

function focusableElements(root: HTMLElement | null) {
  if (!root) return []
  return [...root.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])')]
    .filter(element => !element.hasAttribute('hidden') && element.getClientRects().length > 0)
}

function focusInitialElement(root: HTMLElement | null) {
  focusableElements(root)[0]?.focus()
  if (root && !root.contains(document.activeElement)) root.focus()
}

function trapFocus(event: KeyboardEvent, root: HTMLElement | null) {
  const elements = focusableElements(root)
  const first = elements[0]
  const last = elements.at(-1)
  if (!root || !first || !last) {
    event.preventDefault()
    root?.focus()
    return
  }
  const current = document.activeElement
  if (event.shiftKey && (current === root || current === first || !root.contains(current))) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && (current === last || !root.contains(current))) {
    event.preventDefault()
    first.focus()
  }
}

function onGlobalKeydown(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && !event.altKey && event.key.toLowerCase() === 'k') {
    event.preventDefault()
    if (activePanel.value === 'search') closePanel(true)
    else openPanel('search', searchButton.value)
    return
  }
  if (event.key === 'Escape') {
    if (activePanel.value) closePanel(true)
    else if (mobileDrawerOpen.value) closeDrawer(true)
    return
  }
  if (event.key === 'Tab') {
    if (activePanel.value) trapFocus(event, panelElement.value)
    else if (mobileDrawerOpen.value) trapFocus(event, drawerElement.value)
  }
}

function onCompactNavigationChange(event: MediaQueryListEvent) {
  compactNavigation.value = event.matches
  if (!event.matches) mobileDrawerOpen.value = false
}

async function logout() {
  closePanel()
  await auth.logout()
  await router.replace('/login')
}

watch(() => route.fullPath, () => { activePanel.value = null; mobileDrawerOpen.value = false })
watch([mobileDrawerOpen, activePanel], ([drawer, panel]) => { document.body.style.overflow = drawer || panel ? 'hidden' : '' })
onMounted(() => {
  document.addEventListener('keydown', onGlobalKeydown)
  compactNavigationQuery.addEventListener('change', onCompactNavigationChange)
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onGlobalKeydown)
  compactNavigationQuery.removeEventListener('change', onCompactNavigationChange)
  document.body.style.overflow = ''
})
</script>

<template>
  <div class="app-shell" :class="{ 'app-shell--collapsed': sidebarCollapsed }">
    <div v-if="mobileDrawerOpen || activePanel" class="shell-scrim" aria-hidden="true" @click="activePanel ? closePanel(true) : closeDrawer(true)"></div>

    <aside id="mobile-navigation" ref="drawerElement" class="shell-sidebar" :class="{ 'shell-sidebar--mobile-open': mobileDrawerOpen }" :aria-hidden="compactNavigation && !mobileDrawerOpen ? 'true' : undefined" :aria-modal="mobileDrawerOpen ? 'true' : undefined" :inert="compactNavigation && !mobileDrawerOpen" :role="mobileDrawerOpen ? 'dialog' : undefined" :aria-label="mobileDrawerOpen ? '主导航' : undefined" tabindex="-1">
      <div class="sidebar-brand">
        <RouterLink to="/" class="brand-link" aria-label="OhMyCine Server 仪表盘">
          <span class="brand-mark">O</span>
          <span class="brand-copy"><strong>OhMyCine</strong><small>Server 管理端</small></span>
        </RouterLink>
        <button class="icon-button sidebar-close" type="button" aria-label="关闭导航" @click="closeDrawer(true)">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
        </button>
      </div>

      <nav class="sidebar-nav" aria-label="主导航">
        <RouterLink v-if="showDashboard" :to="dashboardNavigation.to" class="nav-link nav-link--standalone" :class="{ 'nav-link--active': isCurrentPath('/') }" @click="closeDrawer()">
          <span class="nav-dot"></span><span class="nav-short" aria-hidden="true">仪</span><span class="nav-label">{{ dashboardNavigation.label }}</span>
        </RouterLink>

        <section v-for="group in visibleGroups" :key="group.id" class="nav-group">
          <button class="nav-group__toggle" type="button" :title="sidebarCollapsed ? group.label : undefined" :aria-expanded="expandedGroups[group.id]" :aria-controls="`nav-group-${group.id}`" @click="toggleGroup(group.id)">
            <span class="nav-group__label">{{ group.label }}</span><span class="nav-short" aria-hidden="true">{{ group.label.slice(0, 1) }}</span>
            <svg viewBox="0 0 24 24" aria-hidden="true" :class="{ 'rotate-180': expandedGroups[group.id] }"><path d="m7 10 5 5 5-5" /></svg>
          </button>
          <div v-show="expandedGroups[group.id]" :id="`nav-group-${group.id}`" class="nav-group__items">
            <RouterLink v-for="item in group.items" :key="item.id" :to="item.to" class="nav-link" :title="sidebarCollapsed ? item.label : undefined" :class="{ 'nav-link--active': isCurrentPath(item.to) }" @click="closeDrawer()">
              <span class="nav-dot"></span><span class="nav-short" aria-hidden="true">{{ item.label.slice(0, 1) }}</span><span class="nav-label">{{ item.label }}</span><span v-if="item.planned" class="nav-state">规划</span>
            </RouterLink>
          </div>
        </section>
      </nav>

      <footer class="sidebar-footer">
        <span class="server-presence"><i></i><span class="brand-copy">Server 已连接</span></span>
        <small class="brand-copy">管理端 v0.2</small>
      </footer>
    </aside>

    <section class="shell-workspace">
      <header class="shell-topbar">
        <div class="topbar-title">
          <button ref="menuButton" class="icon-button mobile-menu" type="button" aria-label="打开主导航" :aria-expanded="mobileDrawerOpen" aria-controls="mobile-navigation" @click="openDrawer">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h16" /></svg>
          </button>
          <button class="icon-button collapse-button" type="button" :aria-label="sidebarCollapsed ? '展开侧栏' : '收起侧栏'" @click="toggleSidebar">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5h16v14H4zM9 5v14m6-10-3 3 3 3" /></svg>
          </button>
          <div><small>OhMyCine Server</small><strong>{{ pageTitle }}</strong></div>
        </div>

        <div class="topbar-tools" aria-label="全局工具">
          <ThemeToggle />
          <button ref="searchButton" class="search-trigger" type="button" :aria-expanded="activePanel === 'search'" aria-controls="global-tool-panel" @click="togglePanel('search', $event)">
            <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6" /><path d="m16 16 4 4" /></svg>
            <span>搜索媒体、任务、设置…</span><kbd>Ctrl/⌘ K</kbd>
          </button>
          <button class="icon-button" type="button" aria-label="日志中心" :aria-expanded="activePanel === 'logs'" aria-controls="global-tool-panel" @click="togglePanel('logs', $event)">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h14v16H5zM8 8h8M8 12h8M8 16h5" /></svg>
          </button>
          <button class="icon-button" type="button" aria-label="通知中心" :aria-expanded="activePanel === 'notifications'" aria-controls="global-tool-panel" @click="togglePanel('notifications', $event)">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4" /></svg>
          </button>
          <button class="avatar-button" type="button" :aria-expanded="activePanel === 'account'" aria-controls="global-tool-panel" @click="togglePanel('account', $event)">
            <span>{{ auth.user?.display_name.slice(0, 1).toUpperCase() }}</span><i aria-hidden="true"></i><span class="sr-only">账户菜单</span>
          </button>
        </div>

        <section v-if="activePanel" id="global-tool-panel" ref="panelElement" class="tool-panel" role="dialog" aria-modal="true" :aria-label="panelTitle" tabindex="-1">
          <header class="tool-panel__header"><h2>{{ panelTitle }}</h2><button class="icon-button" type="button" aria-label="关闭" @click="closePanel(true)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg></button></header>

          <div v-if="activePanel === 'search'" class="tool-panel__body">
            <label class="label" for="global-search-preview">搜索范围</label>
            <input id="global-search-preview" class="input" value="" readonly placeholder="全局搜索服务尚未实现" aria-describedby="search-state" />
            <p id="search-state" class="tool-empty">当前没有搜索索引或聚合 API。未来仅搜索当前账户有权读取的媒体、任务、订阅、连接和设置，不会生成模拟结果。</p>
          </div>

          <div v-else-if="activePanel === 'logs'" class="tool-panel__body space-y-3">
            <RouterLink v-if="auth.can(Permissions.LogsRead)" to="/logs/runtime" class="tool-row" @click="closePanel()"><div><strong>运行日志</strong><small>按模块、组件、插件和业务关联筛选</small></div><span>打开</span></RouterLink>
            <RouterLink v-if="auth.can(Permissions.AuditRead)" to="/logs/audit" class="tool-row" @click="closePanel()"><div><strong>审计日志</strong><small>查看已实现的安全与配置变更记录</small></div><span>打开</span></RouterLink>
            <p v-else class="tool-empty">当前账户没有审计日志读取权限，不显示审计内容。</p>
          </div>

          <div v-else-if="activePanel === 'notifications'" class="tool-panel__body">
            <span class="status-chip status-chip--planned">尚未实现</span>
            <p class="tool-empty">通知历史、未读状态与 WebSocket 权限投影尚未实现，因此这里没有虚假未读数或演示事件。</p>
          </div>

          <div v-else class="tool-panel__body">
            <div class="account-summary"><span>{{ auth.user?.display_name.slice(0, 1).toUpperCase() }}</span><div><strong>{{ auth.user?.display_name }}</strong><small>@{{ auth.user?.username }} · {{ auth.user?.is_owner ? 'Owner' : auth.user?.roles.join('、') }}</small></div></div>
            <div class="space-y-2">
              <button class="tool-row tool-row--disabled" type="button" disabled><span>我的资料</span><small>待 self-service API</small></button>
              <button class="tool-row tool-row--disabled" type="button" disabled><span>修改密码</span><small>待 self-service API</small></button>
              <button class="tool-row tool-row--disabled" type="button" disabled><span>登录会话</span><small>待 self-service API</small></button>
            </div>
            <button class="btn-danger mt-5 w-full" type="button" @click="logout">安全退出</button>
          </div>
        </section>
      </header>

      <main class="shell-content">
        <RouterView v-slot="{ Component }"><Transition name="page" mode="out-in"><component :is="Component" /></Transition></RouterView>
      </main>
    </section>
  </div>
</template>

<style scoped>
.app-shell { min-height: 100vh; }
.shell-sidebar { position: fixed; inset: 0 auto 0 0; z-index: 40; display: flex; width: 17.5rem; flex-direction: column; border-right: 1px solid var(--border); background: var(--sidebar); padding: 1.1rem; transition: width .2s ease, transform .2s ease; }
.shell-workspace { min-width: 0; padding-left: 17.5rem; transition: padding-left .2s ease; }
.app-shell--collapsed .shell-sidebar { width: 5.25rem; }
.app-shell--collapsed .shell-workspace { padding-left: 5.25rem; }
.app-shell--collapsed .brand-copy, .app-shell--collapsed .nav-label, .app-shell--collapsed .nav-state, .app-shell--collapsed .nav-group__label, .app-shell--collapsed .nav-dot { display: none; }
.app-shell--collapsed .nav-link { justify-content: center; }
.app-shell--collapsed .nav-group__toggle { justify-content: center; }
.sidebar-brand { display: flex; min-height: 3rem; align-items: center; justify-content: space-between; gap: .75rem; }
.brand-link { display: flex; min-width: 0; align-items: center; gap: .75rem; }
.brand-mark { display: grid; height: 2.6rem; width: 2.6rem; flex: 0 0 auto; place-items: center; border-radius: var(--radius-md); background: var(--accent); color: var(--text-on-accent); font-size: 1.1rem; font-weight: 900; }
.brand-copy strong, .brand-copy small { display: block; white-space: nowrap; }
.brand-copy small { margin-top: .15rem; color: var(--text-subtle); font-size: .66rem; }
.sidebar-nav { margin: 1.5rem -.4rem 0; flex: 1; overflow-y: auto; padding: 0 .4rem 1rem; }
.nav-group { margin-top: 1.1rem; }
.nav-group__toggle { display: flex; width: 100%; align-items: center; justify-content: space-between; border: 0; background: transparent; padding: .45rem .7rem; color: var(--text-subtle); font-size: .72rem; font-weight: 700; }
.nav-group__toggle svg { height: 1rem; width: 1rem; fill: none; stroke: currentColor; stroke-width: 1.8; transition: transform .16s ease; }
.nav-group__items { display: grid; gap: .18rem; }
.nav-link { display: flex; min-height: 2.65rem; align-items: center; gap: .7rem; border-radius: var(--radius-md); padding: .6rem .7rem; color: var(--text-muted); font-size: .87rem; transition: background .16s ease, color .16s ease; }
.nav-link:hover { background: var(--surface-hover); color: var(--text); }
.nav-link--standalone { margin-bottom: .25rem; }
.nav-link--active { background: var(--accent-soft); color: var(--accent); box-shadow: inset 3px 0 var(--accent); }
.nav-dot { height: .35rem; width: .35rem; flex: 0 0 auto; border-radius: 99px; background: currentColor; opacity: .6; }
.nav-short { display: none; height: 1.65rem; min-width: 1.65rem; place-items: center; border: 1px solid var(--border); border-radius: var(--radius-sm); font-size: .72rem; font-weight: 750; letter-spacing: 0; }
.app-shell--collapsed .nav-short { display: inline-grid; }
.nav-label { min-width: 0; flex: 1; }
.nav-state { border: 1px solid var(--border); border-radius: 999px; padding: .08rem .35rem; color: var(--text-subtle); font-size: .58rem; }
.sidebar-footer { display: flex; align-items: center; justify-content: space-between; gap: .5rem; border-top: 1px solid var(--border); padding: 1rem .35rem 0; color: var(--text-subtle); font-size: .68rem; }
.server-presence { display: flex; align-items: center; gap: .45rem; }
.server-presence i { height: .45rem; width: .45rem; border-radius: 99px; background: var(--success); }
.shell-topbar { position: sticky; top: 0; z-index: 30; display: flex; min-height: 4.25rem; align-items: center; justify-content: space-between; gap: 1rem; border-bottom: 1px solid var(--border); background: var(--topbar); padding: .65rem clamp(1rem, 2.5vw, 2rem); }
.topbar-title { display: flex; min-width: 0; align-items: center; gap: .8rem; }
.topbar-title small, .topbar-title strong { display: block; }
.topbar-title small { color: var(--text-subtle); font-size: .66rem; }
.topbar-title strong { overflow: hidden; margin-top: .1rem; text-overflow: ellipsis; white-space: nowrap; font-size: .95rem; }
.topbar-tools { display: flex; align-items: center; gap: .45rem; }
.icon-button, .avatar-button, .search-trigger { display: inline-flex; min-height: 2.5rem; align-items: center; justify-content: center; border: 1px solid var(--border); background: var(--surface); color: var(--text-muted); transition: border-color .16s ease, background .16s ease, color .16s ease; }
.icon-button:hover, .avatar-button:hover, .search-trigger:hover { border-color: var(--accent); background: var(--accent-soft); color: var(--accent); }
.icon-button { width: 2.5rem; border-radius: var(--radius-md); padding: 0; }
.icon-button svg, .search-trigger svg { height: 1.15rem; width: 1.15rem; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.7; }
.search-trigger { min-width: min(22vw, 18rem); justify-content: flex-start; gap: .65rem; border-radius: var(--radius-md); padding: 0 .85rem; color: var(--text-subtle); }
.search-trigger span { flex: 1; overflow: hidden; text-align: left; text-overflow: ellipsis; white-space: nowrap; font-size: .78rem; }
.search-trigger kbd { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: .12rem .35rem; color: var(--text-subtle); font-size: .65rem; }
.avatar-button { position: relative; width: 2.75rem; border-radius: 999px; padding: 0; }
.avatar-button > span:first-child { display: grid; height: 2rem; width: 2rem; place-items: center; border-radius: 999px; background: var(--accent-soft); color: var(--accent); font-weight: 800; }
.avatar-button i { position: absolute; right: .08rem; bottom: .08rem; height: .48rem; width: .48rem; border: 2px solid var(--surface); border-radius: 99px; background: var(--success); }
.mobile-menu, .sidebar-close { display: none; }
.tool-panel { position: fixed; top: 4.7rem; right: clamp(1rem, 2.5vw, 2rem); z-index: 60; width: min(27rem, calc(100vw - 2rem)); max-height: calc(100vh - 6rem); overflow-y: auto; border: 1px solid var(--border-strong); border-radius: var(--radius-lg); background: var(--surface); box-shadow: var(--shadow-overlay); outline: none; }
.tool-panel__header { display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid var(--border); padding: 1rem 1.15rem; }
.tool-panel__header h2 { margin: .15rem 0 0; font-size: 1.05rem; }
.tool-panel__body { padding: 1.15rem; }
.tool-empty { margin: 1rem 0 0; border: 1px dashed var(--border-strong); border-radius: var(--radius-md); padding: 1rem; color: var(--text-muted); font-size: .8rem; line-height: 1.65; }
.tool-row { display: flex; width: 100%; min-height: 3.4rem; align-items: center; justify-content: space-between; gap: 1rem; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface-muted); padding: .75rem .85rem; color: var(--text); text-align: left; }
.tool-row:hover:not(.tool-row--disabled) { border-color: var(--accent); background: var(--accent-soft); }
.tool-row strong, .tool-row small { display: block; }
.tool-row small { margin-top: .2rem; color: var(--text-subtle); font-size: .7rem; }
.tool-row--disabled { cursor: default; opacity: .78; }
.account-summary { display: flex; align-items: center; gap: .85rem; margin-bottom: 1rem; border-bottom: 1px solid var(--border); padding-bottom: 1rem; }
.account-summary > span { display: grid; height: 2.8rem; width: 2.8rem; place-items: center; border-radius: 999px; background: var(--accent-soft); color: var(--accent); font-weight: 800; }
.account-summary strong, .account-summary small { display: block; }
.account-summary small { margin-top: .2rem; color: var(--text-subtle); font-size: .72rem; }
.shell-content { padding: clamp(1.25rem, 3vw, 2.5rem); }
.shell-scrim { position: fixed; inset: 0; z-index: 35; background: var(--overlay); }
.rotate-180 { transform: rotate(180deg); }

@media (max-width: 1023px) {
  .shell-sidebar { transform: translateX(-105%); width: min(20rem, 86vw); box-shadow: var(--shadow-drawer); }
  .shell-sidebar--mobile-open { transform: translateX(0); }
  .shell-workspace, .app-shell--collapsed .shell-workspace { padding-left: 0; }
  .app-shell--collapsed .shell-sidebar { width: min(20rem, 86vw); }
  .app-shell--collapsed .brand-copy, .app-shell--collapsed .nav-label, .app-shell--collapsed .nav-state, .app-shell--collapsed .nav-group__label, .app-shell--collapsed .nav-dot { display: initial; }
  .app-shell--collapsed .nav-short { display: none; }
  .app-shell--collapsed .nav-link, .app-shell--collapsed .nav-group__toggle { justify-content: space-between; }
  .mobile-menu, .sidebar-close { display: inline-flex; }
  .collapse-button { display: none; }
}

@media (max-width: 767px) {
  .shell-topbar { min-height: 4rem; padding: .6rem .75rem; }
  .topbar-title small { display: none; }
  .topbar-title strong { max-width: 7.5rem; }
  .search-trigger { min-width: 2.75rem; width: 2.75rem; padding: 0; justify-content: center; }
  .search-trigger span, .search-trigger kbd { display: none; }
  .topbar-tools { gap: .25rem; }
  .tool-panel { inset: 4rem 0 0; width: 100%; max-height: none; border-width: 1px 0 0; border-radius: 0; }
  .shell-content { padding: 1.1rem .85rem 2rem; }
}

@media (max-width: 479px) {
  .topbar-title > div { display: none; }
}

@media (prefers-reduced-motion: reduce) {
  .shell-sidebar, .shell-workspace, .nav-link, .icon-button, .avatar-button, .search-trigger, .nav-group__toggle svg { transition: none; }
}
</style>
