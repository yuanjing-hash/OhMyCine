<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useDataSourceStore } from '@/stores/datasource'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const isVisible = ref(false)

const sourceIcons: Record<string, string> = {
  emby: 'E',
  jellyfin: 'J',
  alist: 'A',
  clouddrive2: 'C',
  local: 'L',
  server: 'S',
  115: '1',
  123: '2',
  quark: 'Q',
}

function getSourceIcon(type: string) {
  return sourceIcons[type] ?? '?'
}
</script>

<template>
  <div
    class="fixed bottom-24 left-0 top-24 z-40 w-2"
    @mouseenter="isVisible = true"
  />

  <Transition name="sidebar-reveal">
    <aside
      v-show="isVisible"
      class="glass-panel fixed left-6 top-1/2 z-40 flex -translate-y-1/2 flex-col items-center gap-2 select-none rounded-2xl px-1.5 py-4"
      @mouseenter="isVisible = true"
      @mouseleave="isVisible = false"
    >
      <button
        class="gp-btn flex h-11 w-11 items-center justify-center rounded-xl transition-all duration-200"
        :class="route.path === '/' ? 'is-active' : ''"
        :title="t('nav.home')"
        @click="router.push('/')"
      >
        <svg width="18" height="18" viewBox="0 0 20 20" fill="none">
          <path d="M3 10l7-7 7 7M5 8v8h3v-4h4v4h3V8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>

      <div class="gp-divider mx-auto h-px w-7" />

      <button
        v-for="config in store.orderedConfigs"
        :key="config.id"
        class="gp-btn flex h-11 w-11 items-center justify-center rounded-xl text-sm font-bold transition-all duration-200"
        :class="route.params.sourceId === config.id ? 'is-active' : ''"
        :title="config.displayName ?? config.name"
        @click="router.push(`/source/${config.id}`)"
      >
        <img v-if="config.iconUrl" :src="config.iconUrl" class="h-6 w-6 rounded" :alt="config.name">
        <span v-else>{{ getSourceIcon(config.type) }}</span>
      </button>

      <button
        v-if="store.configs.length === 0"
        class="gp-btn flex h-11 w-11 items-center justify-center rounded-xl transition-all duration-200"
        style="color: var(--gp-text-dim)"
        title="Add a data source in Settings"
        @click="router.push('/settings')"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round">
          <path d="M12 5v14M5 12h14" />
        </svg>
      </button>
    </aside>
  </Transition>
</template>

<style scoped>
.gp-btn {
  color: var(--gp-text);
}
.gp-btn:hover {
  color: var(--gp-text-full);
  background: var(--gp-hover);
}
.gp-btn.is-active {
  color: var(--gp-text-full);
  background: var(--gp-active);
}
.gp-divider {
  background: var(--gp-divider);
}
.sidebar-reveal-enter-active,
.sidebar-reveal-leave-active {
  transition: opacity 0.24s cubic-bezier(0.16, 1, 0.3, 1), transform 0.24s cubic-bezier(0.16, 1, 0.3, 1);
}
.sidebar-reveal-enter-from,
.sidebar-reveal-leave-to {
  opacity: 0;
  transform: translateX(-12px) translateY(-50%) scale(0.96);
}
.sidebar-reveal-enter-to,
.sidebar-reveal-leave-from {
  opacity: 1;
  transform: translateX(0) translateY(-50%) scale(1);
}
</style>
