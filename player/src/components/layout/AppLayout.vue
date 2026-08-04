<script setup lang="ts">
import type { NavigationShortcutBindings, NavigationShortcutTarget } from '@/services/navigationShortcuts'
import type { PlayerShortcutBindings } from '@/services/playerShortcuts'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { APP_SCROLL_TO_TOP_EVENT } from '@/services/appScroll'
import { loadNavigationShortcutBindings, NAVIGATION_SHORTCUTS_CHANGED_EVENT, navigationShortcutTargetForEvent, shouldIgnoreNavigationShortcut } from '@/services/navigationShortcuts'
import { loadPlayerShortcutBindings, PLAYER_SHORTCUTS_CHANGED_EVENT, playerShortcutTargetForEvent } from '@/services/playerShortcuts'
import { isNativeAndroidRuntime } from '@/services/runtimePlatform'
import { useDataSourceStore } from '@/stores/datasource'
import BackButton from './BackButton.vue'
import DataSourceSidebar from './DataSourceSidebar.vue'
import FloatingControls from './FloatingControls.vue'
import MobileNavigation from './MobileNavigation.vue'
import WindowChrome from './WindowChrome.vue'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const isPlayerRoute = computed(() => route.name === 'player')
const isNativeAndroid = isNativeAndroidRuntime()
const navigationShortcuts = ref<NavigationShortcutBindings>(loadNavigationShortcutBindings())
const playerShortcuts = ref<PlayerShortcutBindings>(loadPlayerShortcutBindings())
const mainScrollRef = ref<HTMLElement | null>(null)

async function scrollContentToTop() {
  await nextTick()
  window.requestAnimationFrame(() => {
    mainScrollRef.value?.scrollTo({ top: 0, left: 0, behavior: 'auto' })
  })
}

function reloadNavigationShortcuts() {
  navigationShortcuts.value = loadNavigationShortcutBindings()
}

function reloadPlayerShortcuts() {
  playerShortcuts.value = loadPlayerShortcutBindings()
}

function handleNavigationShortcut(event: KeyboardEvent) {
  if (shouldIgnoreNavigationShortcut(event))
    return
  if (isPlayerRoute.value && playerShortcutTargetForEvent(event, playerShortcuts.value))
    return
  const target = navigationShortcutTargetForEvent(event, navigationShortcuts.value)
  if (!target)
    return
  event.preventDefault()
  navigateToShortcutTarget(target)
}

function navigateToShortcutTarget(target: NavigationShortcutTarget) {
  if (target === 'home') {
    void router.push({ name: 'home' })
    return
  }
  if (target === 'settings') {
    void router.push({ name: 'settings' })
    return
  }
  if (target === 'datasources') {
    void router.push({ name: 'settings', query: { section: 'datasources' } })
    return
  }
  const sourceId = target.slice('source:'.length)
  const source = store.configs.find(config => config.id === sourceId)
  if (source && source.enabled !== false)
    void router.push({ name: 'source', params: { sourceId } })
}

onMounted(() => {
  store.loadConfigs()
  window.addEventListener('keydown', handleNavigationShortcut)
  window.addEventListener(NAVIGATION_SHORTCUTS_CHANGED_EVENT, reloadNavigationShortcuts)
  window.addEventListener(PLAYER_SHORTCUTS_CHANGED_EVENT, reloadPlayerShortcuts)
  window.addEventListener(APP_SCROLL_TO_TOP_EVENT, scrollContentToTop)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleNavigationShortcut)
  window.removeEventListener(NAVIGATION_SHORTCUTS_CHANGED_EVENT, reloadNavigationShortcuts)
  window.removeEventListener(PLAYER_SHORTCUTS_CHANGED_EVENT, reloadPlayerShortcuts)
  window.removeEventListener(APP_SCROLL_TO_TOP_EVENT, scrollContentToTop)
})

watch(() => route.fullPath, scrollContentToTop, { flush: 'post' })
</script>

<template>
  <div
    class="app-window relative text-text font-sans"
    :class="isPlayerRoute ? 'app-window--player' : 'app-window--cinema'"
  >
    <!-- Content fills the full area -->
    <main ref="mainScrollRef" class="cinema-scrollbar absolute inset-0 z-0 overflow-auto">
      <slot />
    </main>

    <!-- Floating glass sidebar — hidden on player page for immersive layout -->
    <DataSourceSidebar v-if="!isPlayerRoute" />

    <MobileNavigation v-if="!isPlayerRoute" />

    <!-- Floating back navigation for non-home pages — hidden on player page -->
    <BackButton v-if="!isPlayerRoute" />

    <!-- Floating glass top bar: always visible (drag region + window controls).
         On player page, hide center nav buttons only. -->
    <WindowChrome v-if="!isNativeAndroid" :hide-nav="isPlayerRoute" />

    <!-- Bottom-right floating controls (player + theme) -->
    <FloatingControls v-if="!isNativeAndroid" />
  </div>
</template>

<style scoped>
.app-window--cinema {
  background: var(--color-bg);
}

.app-window--player {
  background: transparent;
}
</style>
