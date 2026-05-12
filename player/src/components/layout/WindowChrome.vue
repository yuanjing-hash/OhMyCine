<script setup lang="ts">
import { getCurrentWindow } from '@tauri-apps/api/window'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

defineProps<{
  hideNav?: boolean
}>()

const appWindow = getCurrentWindow()
const { t } = useI18n()
const route = useRoute()
const router = useRouter()

let dragStart: { x: number, y: number } | null = null
let isDragStarting = false

async function minimize() {
  await appWindow.minimize()
}

async function toggleMaximize() {
  if (await appWindow.isMaximized())
    await appWindow.unmaximize()
  else
    await appWindow.maximize()
}

async function close() {
  await appWindow.close()
}

function beginDrag(event: MouseEvent) {
  if (event.button !== 0)
    return
  dragStart = { x: event.screenX, y: event.screenY }
}

async function dragIfMoved(event: MouseEvent) {
  if (!dragStart || isDragStarting)
    return

  const deltaX = Math.abs(event.screenX - dragStart.x)
  const deltaY = Math.abs(event.screenY - dragStart.y)
  if (deltaX < 4 && deltaY < 4)
    return

  isDragStarting = true
  dragStart = null
  try {
    if (await appWindow.isMaximized())
      await appWindow.unmaximize()
    await appWindow.startDragging()
  }
  finally {
    isDragStarting = false
  }
}

function endDrag() {
  dragStart = null
}
</script>

<template>
  <div class="window-chrome pointer-events-none fixed inset-x-0 top-0 h-16">
    <!-- full-width invisible drag region so the top area still drags above route/loading content -->
    <div
      data-tauri-drag-region
      class="pointer-events-auto absolute inset-x-0 top-0 z-0 h-16"
      @dblclick="toggleMaximize"
      @mousedown="beginDrag"
      @mouseleave="endDrag"
      @mousemove="dragIfMoved"
      @mouseup="endDrag"
    />

    <!-- Center navigation glass panel -->
    <nav v-if="!hideNav" class="glass-panel pointer-events-auto absolute left-1/2 top-3 z-10 flex -translate-x-1/2 items-center gap-1 rounded-2xl px-2 py-1.5">
      <button
        class="gp-btn flex h-10 items-center gap-2 rounded-xl px-5 text-sm font-semibold transition-all duration-200"
        :class="route.path === '/' || route.path.startsWith('/source') ? 'is-active' : ''"
        @click="router.push('/')"
      >
        <svg width="16" height="16" viewBox="0 0 20 20" fill="none">
          <path d="M3 10l7-7 7 7M5 8v8h3v-4h4v4h3V8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        {{ t('nav.home') }}
      </button>

      <div class="gp-divider h-6 w-px" />

      <button
        class="gp-btn flex h-10 items-center gap-2 rounded-xl px-5 text-sm font-semibold transition-all duration-200"
        :class="route.path === '/settings' ? 'is-active' : ''"
        @click="router.push('/settings')"
      >
        <svg width="16" height="16" viewBox="0 0 20 20" fill="none">
          <circle cx="10" cy="10" r="3" stroke="currentColor" stroke-width="1.5" />
          <path d="M10 1v3M10 16v3M1 10h3M16 10h3M3.5 3.5l2 2M14.5 14.5l2 2M3.5 16.5l2-2M14.5 5.5l2-2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
        </svg>
        {{ t('nav.settings') }}
      </button>
    </nav>

    <!-- Separate window controls glass panel -->
    <div class="glass-panel pointer-events-auto absolute right-6 top-3 z-10 flex items-center gap-1 rounded-2xl px-2 py-1.5">
      <button
        class="gp-btn gp-win-ctrl flex h-10 w-10 items-center justify-center rounded-xl transition-colors"
        @click.stop="minimize"
      >
        <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
          <path d="M2 6h8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
        </svg>
      </button>
      <button
        class="gp-btn gp-win-ctrl flex h-10 w-10 items-center justify-center rounded-xl transition-colors"
        @click.stop="toggleMaximize"
      >
        <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
          <rect x="2.5" y="2.5" width="7" height="7" rx="1.2" stroke="currentColor" stroke-width="1.5" />
        </svg>
      </button>
      <button
        class="gp-btn gp-win-ctrl gp-close flex h-10 w-10 items-center justify-center rounded-xl transition-colors"
        @click.stop="close"
      >
        <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
          <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.window-chrome {
  z-index: 1000;
}

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
.gp-win-ctrl {
  color: var(--gp-text-dim);
}
.gp-win-ctrl:hover {
  color: var(--gp-text-full);
  background: var(--gp-hover);
}
.gp-close:hover {
  color: white;
  background: var(--gp-close-hover);
}
</style>
