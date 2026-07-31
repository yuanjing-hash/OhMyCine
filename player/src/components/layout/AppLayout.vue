<script setup lang="ts">
import type { NavigationShortcutBindings, NavigationShortcutTarget } from '@/services/navigationShortcuts'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { loadNavigationShortcutBindings, NAVIGATION_SHORTCUTS_CHANGED_EVENT, navigationShortcutTargetForEvent, shouldIgnoreNavigationShortcut } from '@/services/navigationShortcuts'
import { useDataSourceStore } from '@/stores/datasource'
import BackButton from './BackButton.vue'
import DataSourceSidebar from './DataSourceSidebar.vue'
import FloatingControls from './FloatingControls.vue'
import WindowChrome from './WindowChrome.vue'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const isPlayerRoute = computed(() => route.name === 'player')
const navigationShortcuts = ref<NavigationShortcutBindings>(loadNavigationShortcutBindings())

function reloadNavigationShortcuts() {
  navigationShortcuts.value = loadNavigationShortcutBindings()
}

function handleNavigationShortcut(event: KeyboardEvent) {
  if (shouldIgnoreNavigationShortcut(event))
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
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleNavigationShortcut)
  window.removeEventListener(NAVIGATION_SHORTCUTS_CHANGED_EVENT, reloadNavigationShortcuts)
})
</script>

<template>
  <div
    class="app-window relative text-text font-sans"
    :class="isPlayerRoute ? 'app-window--player' : 'app-window--cinema'"
  >
    <!-- Content fills the full area -->
    <main class="cinema-scrollbar absolute inset-0 z-0 overflow-auto">
      <slot />
    </main>

    <!-- Floating glass sidebar — hidden on player page for immersive layout -->
    <DataSourceSidebar v-if="!isPlayerRoute" />

    <!-- Floating back navigation for non-home pages — hidden on player page -->
    <BackButton v-if="!isPlayerRoute" />

    <!-- Floating glass top bar: always visible (drag region + window controls).
         On player page, hide center nav buttons only. -->
    <WindowChrome :hide-nav="isPlayerRoute" />

    <!-- Bottom-right floating controls (player + theme) -->
    <FloatingControls />
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
