<script setup lang="ts">
import type { KnownSubtitleTrackInput, MpvZOrderStrategy, RenderSurfaceBounds, VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import type { SubtitleTrack as DataSourceSubtitleTrack } from '@/services/datasource/types'
import { LogicalSize } from '@tauri-apps/api/dpi'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRoute } from 'vue-router'
import PlayerControls from '@/components/player/PlayerControls.vue'
import VideoPlayer from '@/components/player/VideoPlayer.vue'
import { useMpv } from '@/composables/useMpv'
import { redactSensitiveText, toSafeErrorMessage } from '@/services/datasource/errors'
import { getPlaybackMediaContext } from '@/services/playbackContext'
import { useDataSourceStore } from '@/stores/datasource'

const AUTO_HIDE_DELAY = 2800

const route = useRoute()
const store = useDataSourceStore()
const appWindow = getCurrentWindow()
const mediaTitle = ref('未命名影片')
const mediaPath = ref('')
const displayMediaPath = computed(() => redactSensitiveText(mediaPath.value))
const chromeVisible = ref(true)
const controlsInteracting = ref(false)
const isWindowFocused = ref(true)
const lastRenderBounds = ref<RenderSurfaceBounds | null>(null)
const topChromeRef = ref<HTMLElement | null>(null)
const bottomChromeRef = ref<HTMLElement | null>(null)
const topOcclusion = ref(0)
const bottomOcclusion = ref(0)
const diagnosticsOpen = ref(false)
const pictureSettingsError = ref<string | null>(null)
const isPlayerFullscreen = ref(false)
// Single active strategy for this slice: transparent Tauri/WebView overlay above a full-bleed mpv
// video underlay. Legacy top/bottom occlusion strategies are neutralized in Rust.
const renderStrategy = ref<MpvZOrderStrategy>('transparentOverlay')
let hideTimer: number | undefined
let renderInitPromise: Promise<void> | null = null
let boundsUpdateInFlight = false
let pendingRenderBounds: RenderSurfaceBounds | null = null
let playbackCleanupStarted = false

const {
  isPlaying,
  currentTime,
  duration,
  volume,
  playbackSpeed,
  subtitleTracks,
  audioTracks,
  currentSubtitle,
  currentAudio,
  videoAspectMode,
  videoFitMode,
  renderStatus,
  renderError,
  renderDiagnostics,
  trackError,
  initializeRender,
  updateRenderSurfaceBounds,
  setRenderStrategy,
  refreshTrackState,
  setKnownSubtitleTracks,
  load,
  togglePause,
  seek,
  seekRelative,
  setVolume,
  setPlaybackSpeed,
  setSubtitle,
  setAudio,
  setVideoAspect,
  setVideoFit,
  stop,
} = useMpv()

const hasMedia = computed(() => mediaPath.value.length > 0)
const playbackQueueItemCount = computed(() => hasMedia.value ? 1 : 0)
const shouldShowChrome = computed(() => chromeVisible.value || !hasMedia.value || !isPlaying.value || controlsInteracting.value)
const isTransparentRootActive = computed(() => hasMedia.value && renderStatus.value === 'ready')

async function updateChromeOcclusion() {
  await nextTick()

  if (!hasMedia.value) {
    topOcclusion.value = 0
    bottomOcclusion.value = 0
    return
  }

  // The transparent-overlay model keeps video full-bleed behind the WebView. Keep these values at
  // zero so legacy occlusion does not shrink the mpv underlay away from the Vue chrome.
  topOcclusion.value = 0
  bottomOcclusion.value = 0
}

function clearHideTimer() {
  if (!hideTimer)
    return
  window.clearTimeout(hideTimer)
  hideTimer = undefined
}

function canAutoHideChrome() {
  return hasMedia.value && isPlaying.value && !controlsInteracting.value && isWindowFocused.value
}

function scheduleChromeHide() {
  clearHideTimer()
  if (!canAutoHideChrome())
    return

  hideTimer = window.setTimeout(() => {
    if (canAutoHideChrome())
      chromeVisible.value = false
  }, AUTO_HIDE_DELAY)
}

function revealChrome() {
  chromeVisible.value = true
  scheduleChromeHide()
}

function handleControlsInteraction(next: boolean) {
  controlsInteracting.value = next
  chromeVisible.value = true
  if (next)
    clearHideTimer()
  else
    scheduleChromeHide()
}

function handleWindowBlur() {
  isWindowFocused.value = false
  revealChrome()
}

function handleWindowResize() {
  void updateChromeOcclusion()
}

async function ensureRenderInitialized() {
  if (!renderInitPromise) {
    renderInitPromise = initializeRender().then(async () => {
      if (lastRenderBounds.value)
        await updateRenderSurfaceBounds(lastRenderBounds.value)
    })
  }

  await renderInitPromise
}

function handleWindowFocus() {
  isWindowFocused.value = true
  revealChrome()
}

function queryStringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function aspectRatioValue(mode: VideoAspectMode): number | null {
  switch (mode) {
    case '16:9':
      return 16 / 9
    case '4:3':
      return 4 / 3
    case 'cinema':
      return 2.35
    default:
      return null
  }
}

async function syncKnownSubtitleTracks() {
  const contextId = queryStringValue(route.query.contextId)
  const playbackContext = contextId ? getPlaybackMediaContext(contextId) : null
  const sourceId = queryStringValue(route.query.sourceId) || playbackContext?.sourceId || ''
  const itemId = queryStringValue(route.query.itemId) || playbackContext?.itemId || ''
  const contextSubtitles = playbackContext && playbackContext.sourceId === sourceId && playbackContext.itemId === itemId
    ? playbackContext.subtitles
    : []

  if (!sourceId || !itemId) {
    setKnownSubtitleTracks(contextSubtitles.map(mapKnownSubtitleTrack))
    return
  }

  try {
    store.loadConfigs()
    await store.syncManager()
    const source = store.getSource(sourceId)
    if (!source) {
      setKnownSubtitleTracks(contextSubtitles.map(mapKnownSubtitleTrack))
      return
    }

    const detail = await source.getDetail(itemId)
    setKnownSubtitleTracks(mergeDataSourceSubtitleTracks(contextSubtitles, detail.subtitles ?? []).map(mapKnownSubtitleTrack))
  }
  catch {
    setKnownSubtitleTracks(contextSubtitles.map(mapKnownSubtitleTrack))
  }
}

function mergeDataSourceSubtitleTracks(...groups: readonly DataSourceSubtitleTrack[][]): DataSourceSubtitleTrack[] {
  const seen = new Set<string>()
  const merged: DataSourceSubtitleTrack[] = []

  for (const track of groups.flat()) {
    const key = [track.source ?? '', track.index, track.language, track.title ?? '', track.codec ?? '', track.url ?? ''].join('|')
    if (seen.has(key))
      continue

    seen.add(key)
    merged.push(track)
  }

  return merged
}

function mapKnownSubtitleTrack(track: DataSourceSubtitleTrack): KnownSubtitleTrackInput {
  const source = track.source === 'external' ? 'external' : 'detail'
  const hasUrl = Boolean(track.url)
  return {
    id: track.index,
    source,
    language: track.language && track.language !== 'Unknown' ? track.language : null,
    title: track.title,
    codec: track.codec,
    isDefault: track.isDefault,
    url: track.url,
    selectable: hasUrl,
    unavailableReason: source === 'external'
      ? '该外部字幕缺少可加载地址，暂时只能在详情页确认存在。'
      : '该字幕来自媒体详情，但当前播放流未暴露可直接加载的字幕地址。',
  }
}

async function resizeWindowForAspect(mode: VideoAspectMode) {
  const ratio = aspectRatioValue(mode)
  if (!ratio)
    return

  try {
    const fullscreen = isPlayerFullscreen.value || document.fullscreenElement !== null || await appWindow.isFullscreen()
    if (fullscreen)
      return

    const size = await appWindow.innerSize()
    if (size.width <= 0 || size.height <= 0)
      return

    const scaleFactor = await appWindow.scaleFactor()
    const currentWidth = size.width / scaleFactor
    const currentHeight = size.height / scaleFactor
    const area = currentWidth * currentHeight
    const nextWidth = Math.round(Math.sqrt(area * ratio))
    const nextHeight = Math.round(nextWidth / ratio)
    const width = Math.max(720, nextWidth)
    const height = Math.max(420, nextHeight)

    if (await appWindow.isMaximized())
      await appWindow.unmaximize()
    await appWindow.setSize(new LogicalSize(width, height))
    pictureSettingsError.value = null
  }
  catch (error) {
    pictureSettingsError.value = toSafeErrorMessage(error, '窗口尺寸调整失败，已保留当前画面比例设置。')
  }
}

watch(
  () => [route.query.path, route.query.sourceId, route.query.itemId, route.query.contextId],
  async ([path]) => {
    const nextPath = typeof path === 'string' ? path : ''
    mediaPath.value = nextPath
    mediaTitle.value = typeof route.query.title === 'string' ? route.query.title : '未命名影片'
    pictureSettingsError.value = null
    revealChrome()

    if (nextPath) {
      await syncKnownSubtitleTracks()
      await ensureRenderInitialized()
      if (playbackCleanupStarted)
        return
      await load(nextPath)
      if (playbackCleanupStarted)
        await stopPlaybackSilently()
    }
    else {
      setKnownSubtitleTracks([])
    }
  },
  { immediate: true },
)

watch(isPlaying, () => {
  revealChrome()
})

watch([shouldShowChrome, hasMedia], () => {
  void updateChromeOcclusion()
})

async function handleFileDrop(path: string) {
  mediaPath.value = path
  mediaTitle.value = path.split(/[\\/]/).pop() || '本地视频'
  pictureSettingsError.value = null
  setKnownSubtitleTracks([])
  revealChrome()
  await ensureRenderInitialized()
  if (playbackCleanupStarted)
    return
  await load(path)
  if (playbackCleanupStarted)
    await stopPlaybackSilently()
}

async function flushRenderBounds() {
  if (boundsUpdateInFlight || !pendingRenderBounds)
    return

  boundsUpdateInFlight = true
  const bounds = pendingRenderBounds
  pendingRenderBounds = null
  try {
    await updateRenderSurfaceBounds(bounds)
  }
  finally {
    boundsUpdateInFlight = false
    if (pendingRenderBounds)
      void flushRenderBounds()
  }
}

function handleRenderBounds(bounds: RenderSurfaceBounds) {
  lastRenderBounds.value = bounds
  pendingRenderBounds = bounds
  void flushRenderBounds()
}

function toggleDiagnosticsPanel() {
  diagnosticsOpen.value = !diagnosticsOpen.value
}

async function handleSetStrategy(strategy: MpvZOrderStrategy) {
  if (renderStrategy.value === strategy)
    return
  renderStrategy.value = strategy
  await setRenderStrategy(strategy)
  // Re-report bounds so Rust reapplies SetWindowPos immediately. Legacy strategies are neutralized
  // to the transparent-overlay underlay model.
  if (lastRenderBounds.value) {
    pendingRenderBounds = lastRenderBounds.value
    void flushRenderBounds()
  }
}

function requestRenderBoundsSync() {
  if (!lastRenderBounds.value)
    return

  pendingRenderBounds = lastRenderBounds.value
  void flushRenderBounds()
}

function scheduleRenderBoundsSync() {
  requestRenderBoundsSync()
  window.requestAnimationFrame(requestRenderBoundsSync)
  window.setTimeout(requestRenderBoundsSync, 160)
  window.setTimeout(requestRenderBoundsSync, 420)
}

async function handleFullscreenChanged(fullscreen: boolean) {
  isPlayerFullscreen.value = fullscreen
  await nextTick()
  scheduleRenderBoundsSync()
}

async function handleSetVideoAspect(mode: VideoAspectMode) {
  await setVideoAspect(mode)
  await resizeWindowForAspect(mode)
  scheduleRenderBoundsSync()
}

async function handleSetVideoFit(mode: VideoFitMode) {
  await setVideoFit(mode)
  scheduleRenderBoundsSync()
}

async function stopPlaybackSilently() {
  try {
    await stop()
  }
  catch {
    // Route cleanup must never expose native/player details or block navigation.
  }
}

async function stopPlaybackForRouteExit() {
  playbackCleanupStarted = true
  if (!hasMedia.value && !isPlaying.value)
    return

  await stopPlaybackSilently()
}

function handleGlobalKeydown(event: KeyboardEvent) {
  // Ctrl+Shift+D (or Cmd+Shift+D) surfaces the diagnostics panel from the WebView overlay.
  if ((event.ctrlKey || event.metaKey) && event.shiftKey && (event.key === 'D' || event.key === 'd')) {
    event.preventDefault()
    diagnosticsOpen.value = !diagnosticsOpen.value
  }
}

function syncTransparentRootClass(active: boolean) {
  const classList = ['player-render-surface-transparent']
  for (const cls of classList) {
    if (active) {
      document.documentElement.classList.add(cls)
      document.body.classList.add(cls)
    }
    else {
      document.documentElement.classList.remove(cls)
      document.body.classList.remove(cls)
    }
  }
}

function syncPlayerChromeClass(visible: boolean) {
  const className = 'player-chrome-hidden'
  if (visible) {
    document.documentElement.classList.remove(className)
    document.body.classList.remove(className)
  }
  else {
    document.documentElement.classList.add(className)
    document.body.classList.add(className)
  }
}

onBeforeRouteLeave(async () => {
  await stopPlaybackForRouteExit()
})

onMounted(() => {
  document.documentElement.classList.add('player-render-surface-active')
  document.body.classList.add('player-render-surface-active')
  syncTransparentRootClass(isTransparentRootActive.value)
  syncPlayerChromeClass(shouldShowChrome.value)
  window.addEventListener('blur', handleWindowBlur)
  window.addEventListener('focus', handleWindowFocus)
  window.addEventListener('resize', handleWindowResize)
  window.addEventListener('keydown', handleGlobalKeydown)
  void updateChromeOcclusion()
  void ensureRenderInitialized()
  scheduleChromeHide()
})

onBeforeUnmount(() => {
  void stopPlaybackForRouteExit()
  document.documentElement.classList.remove('player-render-surface-active')
  document.body.classList.remove('player-render-surface-active')
  document.documentElement.classList.remove('player-render-surface-transparent')
  document.body.classList.remove('player-render-surface-transparent')
  document.documentElement.classList.remove('player-chrome-hidden')
  document.body.classList.remove('player-chrome-hidden')
  clearHideTimer()
  window.removeEventListener('blur', handleWindowBlur)
  window.removeEventListener('focus', handleWindowFocus)
  window.removeEventListener('resize', handleWindowResize)
  window.removeEventListener('keydown', handleGlobalKeydown)
})

watch(
  isTransparentRootActive,
  (active) => {
    syncTransparentRootClass(active)
  },
  { immediate: true },
)

watch(
  shouldShowChrome,
  (visible) => {
    syncPlayerChromeClass(visible)
  },
  { immediate: true },
)
</script>

<template>
  <div
    class="player-view relative h-screen w-full overflow-hidden text-white"
    :class="[
      { 'is-chrome-hidden': !shouldShowChrome },
      isTransparentRootActive ? 'player-view--transparent' : 'bg-black',
    ]"
    @mousemove="revealChrome"
    @mouseleave="scheduleChromeHide"
    @touchstart.passive="revealChrome"
  >
    <VideoPlayer
      :is-playing="isPlaying"
      :has-media="hasMedia"
      :render-status="renderStatus"
      :render-error="renderError"
      :render-diagnostics="renderDiagnostics"
      :render-strategy="renderStrategy"
      :top-occlusion="topOcclusion"
      :bottom-occlusion="bottomOcclusion"
      :diagnostics-open="diagnosticsOpen"
      @file-drop="handleFileDrop"
      @render-bounds="handleRenderBounds"
      @toggle-diagnostics="toggleDiagnosticsPanel"
      @set-strategy="handleSetStrategy"
    />

    <div
      v-if="hasMedia"
      class="pointer-events-auto absolute inset-x-0 top-0 z-5 h-24"
      aria-hidden="true"
      @mouseenter="revealChrome"
      @mousemove="revealChrome"
    />
    <div
      v-if="hasMedia"
      class="pointer-events-auto absolute inset-x-0 bottom-0 z-5 h-32"
      aria-hidden="true"
      @mouseenter="revealChrome"
      @mousemove="revealChrome"
    />

    <Transition name="player-chrome-top">
      <div
        v-show="shouldShowChrome"
        ref="topChromeRef"
        class="pointer-events-none absolute inset-x-0 top-0 z-10 bg-gradient-to-b from-black via-black/82 to-transparent px-6 pb-8 pt-16"
      >
        <div class="max-w-4xl">
          <p class="text-xs uppercase tracking-[0.24em] text-white/38">
            Now Playing
          </p>
          <h1 class="mt-2 truncate text-2xl font-bold text-white drop-shadow-lg">
            {{ mediaTitle }}
          </h1>
          <p v-if="mediaPath" class="mt-2 truncate text-xs text-white/35">
            {{ displayMediaPath }}
          </p>
        </div>
      </div>
    </Transition>

    <!-- Bottom chrome: always in DOM when media loaded so occlusion stays valid.
         Controls fade via opacity transition; the gradient div itself never disappears. -->
    <div
      v-if="hasMedia"
      ref="bottomChromeRef"
      class="pointer-events-auto absolute inset-x-0 bottom-0 z-20 bg-gradient-to-t from-black via-black/86 to-transparent px-6 pb-6 pt-10 transition-opacity duration-300"
      :class="shouldShowChrome ? 'opacity-100' : 'opacity-0'"
      @mouseenter="revealChrome"
      @mousemove="revealChrome"
    >
      <PlayerControls
        :is-playing="isPlaying"
        :current-time="currentTime"
        :duration="duration"
        :volume="volume"
        :playback-speed="playbackSpeed"
        :subtitle-tracks="subtitleTracks"
        :audio-tracks="audioTracks"
        :queue-item-count="playbackQueueItemCount"
        :current-subtitle="currentSubtitle"
        :current-audio="currentAudio"
        :video-aspect-mode="videoAspectMode"
        :video-fit-mode="videoFitMode"
        :track-error="trackError"
        :picture-settings-error="pictureSettingsError"
        @toggle-pause="togglePause"
        @seek="seek"
        @seek-relative="seekRelative"
        @set-volume="setVolume"
        @set-playback-speed="setPlaybackSpeed"
        @set-subtitle="setSubtitle"
        @set-audio="setAudio"
        @set-video-aspect="handleSetVideoAspect"
        @set-video-fit="handleSetVideoFit"
        @refresh-tracks="refreshTrackState"
        @fullscreen-changed="handleFullscreenChanged"
        @interaction-change="handleControlsInteraction"
      />
    </div>
  </div>
</template>

<style scoped>
:global(html.player-render-surface-active),
:global(body.player-render-surface-active),
:global(body.player-render-surface-active #app),
:global(body.player-render-surface-active .app-window),
:global(body.player-render-surface-active main) {
  background: #030305;
}

:global(body.player-render-surface-active main.cinema-scrollbar) {
  overflow: hidden;
  scrollbar-width: none;
}

:global(body.player-render-surface-active main.cinema-scrollbar::-webkit-scrollbar) {
  width: 0;
  height: 0;
  display: none;
}

/* Transparent overlay chain: every CSS layer from html/body/#app/.app-window/main/.player-view
   down to the Player surface root must be transparent so the transparent Tauri/WebView window can
   reveal the mpv video underlay behind it. Non-player routes keep the opaque Cinema OS background. */
:global(html.player-render-surface-transparent),
:global(body.player-render-surface-transparent),
:global(body.player-render-surface-transparent #app),
:global(body.player-render-surface-transparent .app-window),
:global(body.player-render-surface-transparent main) {
  background: transparent !important;
  background-color: transparent !important;
}

.player-view {
  cursor: default;
}

.player-view.is-chrome-hidden {
  cursor: none;
}

.player-view--transparent {
  background: transparent;
  background-color: transparent;
}

.player-chrome-top-enter-active,
.player-chrome-top-leave-active,
.player-chrome-bottom-enter-active,
.player-chrome-bottom-leave-active {
  transition: opacity 260ms var(--ease-out), transform 260ms var(--ease-out);
}

.player-chrome-top-enter-from,
.player-chrome-top-leave-to {
  opacity: 0;
  transform: translateY(-18px);
}

.player-chrome-bottom-enter-from,
.player-chrome-bottom-leave-to {
  opacity: 0;
  transform: translateY(24px);
}
</style>
