<script setup lang="ts">
import { getCurrentWindow } from '@tauri-apps/api/window'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

defineProps<{
  hideNav?: boolean
}>()

const appWindow = isTauriRuntime() ? getCurrentWindow() : null
const { t } = useI18n()
const route = useRoute()
const router = useRouter()

let dragStart: { x: number, y: number } | null = null
let isDragStarting = false
let disposed = false
const windowEventUnlisteners: Array<() => void> = []
const isMaximized = ref(false)
const isFullscreen = ref(false)

async function syncWindowState() {
  if (!appWindow)
    return
  try {
    const [maximized, fullscreen] = await Promise.all([
      appWindow.isMaximized(),
      appWindow.isFullscreen(),
    ])
    isMaximized.value = maximized
    isFullscreen.value = fullscreen
    syncNativeWindowClasses(maximized, fullscreen)
  }
  catch {
    // Browser development mode has no native window state.
  }
}

function syncNativeWindowClasses(maximized: boolean, fullscreen: boolean) {
  for (const target of [document.documentElement, document.body]) {
    target.classList.toggle('native-window-maximized', maximized && !fullscreen)
    target.classList.toggle('native-window-fullscreen', fullscreen)
  }
}

function trackWindowListener(listener: Promise<() => void>) {
  void listener.then((unlisten) => {
    if (disposed)
      unlisten()
    else
      windowEventUnlisteners.push(unlisten)
  }).catch(() => undefined)
}

async function minimize() {
  await appWindow?.minimize()
}

async function toggleMaximize() {
  if (!appWindow)
    return
  if (await appWindow.isFullscreen())
    return
  if (await appWindow.isMaximized())
    await appWindow.unmaximize()
  else
    await appWindow.maximize()
  await syncWindowState()
}

async function close() {
  await appWindow?.close()
}

function goBack() {
  if (window.history.state?.back)
    router.back()
  else
    router.push('/')
}

function beginDrag(event: MouseEvent) {
  if (event.button !== 0)
    return
  dragStart = { x: event.screenX, y: event.screenY }
}

async function dragIfMoved(event: MouseEvent) {
  if (!appWindow || !dragStart || isDragStarting)
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

function isTauriRuntime(): boolean {
  const root = globalThis as {
    readonly __TAURI_INTERNALS__?: unknown
    readonly window?: { readonly __TAURI_INTERNALS__?: unknown }
  }
  return root.__TAURI_INTERNALS__ != null || root.window?.__TAURI_INTERNALS__ != null
}

onMounted(() => {
  void syncWindowState()
  if (appWindow) {
    trackWindowListener(appWindow.onResized(syncWindowState))
    trackWindowListener(appWindow.onFocusChanged(syncWindowState))
  }
})

onBeforeUnmount(() => {
  disposed = true
  for (const unlisten of windowEventUnlisteners)
    unlisten()
  windowEventUnlisteners.length = 0
  syncNativeWindowClasses(false, false)
})
</script>

<template>
  <div class="window-chrome pointer-events-none fixed inset-x-0 top-0 h-16">
    <!-- full-width invisible drag region so the top area still drags above route/loading content -->
    <div
      data-tauri-drag-region
      class="desktop-window-drag pointer-events-auto absolute inset-x-0 top-0 z-0 h-16"
      :class="{ hidden: isFullscreen }"
      @dblclick="toggleMaximize"
      @mousedown="beginDrag"
      @mouseleave="endDrag"
      @mousemove="dragIfMoved"
      @mouseup="endDrag"
    />

    <!-- Player route keeps a compact back affordance above the drag region without restoring the full nav. -->
    <button
      v-if="hideNav && route.path !== '/'"
      v-show="!isFullscreen"
      class="glass-panel player-window-back pointer-events-auto absolute left-6 top-3 z-20 flex h-10 items-center gap-2 rounded-2xl px-3 text-sm font-semibold transition-all duration-200"
      type="button"
      title="返回"
      aria-label="返回上一页"
      @click="goBack"
    >
      <svg width="16" height="16" viewBox="0 0 20 20" fill="none" aria-hidden="true">
        <path d="M12.5 4.5L7 10l5.5 5.5M8 10h8" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      <span class="player-window-back-label">返回</span>
    </button>

    <!-- Center navigation glass panel -->
    <nav v-if="!hideNav" v-show="!isFullscreen" class="desktop-window-nav glass-panel pointer-events-auto absolute left-1/2 top-3 z-10 flex -translate-x-1/2 items-center gap-1 rounded-2xl px-2 py-1.5">
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
    <div v-show="!isFullscreen" class="desktop-window-controls glass-panel pointer-events-auto absolute right-6 top-3 z-10 flex items-center gap-1 rounded-2xl px-2 py-1.5">
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
        :title="isMaximized ? '还原窗口' : '最大化窗口'"
        :aria-label="isMaximized ? '还原窗口' : '最大化窗口'"
        @click.stop="toggleMaximize"
      >
        <svg v-if="isMaximized" width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
          <rect x="1.75" y="3.75" width="6.5" height="6.5" rx="1" stroke="currentColor" stroke-width="1.25" />
          <path d="M3.75 3.5V2.75a1 1 0 0 1 1-1h4.5a1 1 0 0 1 1 1v4.5a1 1 0 0 1-1 1H8.5" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" />
        </svg>
        <svg v-else width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
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

.player-window-back {
  color: var(--gp-text);
}
.player-window-back:hover,
.player-window-back:focus-visible {
  color: var(--gp-text-full);
  background: var(--gp-hover);
  transform: translateX(-2px);
}
.player-window-back:active {
  color: var(--gp-text-full);
  background: var(--gp-active);
  transform: translateX(-1px) scale(0.98);
}
:global(body.player-chrome-hidden) .player-window-back {
  pointer-events: none;
  opacity: 0;
  transform: translateY(-6px);
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

@media (max-width: 767px) {
  .desktop-window-nav {
    display: none !important;
  }
}

@media (max-width: 767px) {
  .desktop-window-drag,
  .desktop-window-controls {
    display: none !important;
  }

  .player-window-back {
    left: 1rem;
    top: max(0.75rem, env(safe-area-inset-top));
    width: 2.75rem;
    height: 2.75rem;
    justify-content: center;
    border-radius: 50%;
    padding: 0;
    background: rgba(9, 11, 17, 0.72);
  }

  .player-window-back-label {
    display: none;
  }
}
</style>
