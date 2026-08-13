<script setup lang="ts">
import type { KnownSubtitleTrackInput, MpvOrientationMode, MpvZOrderStrategy, RenderSurfaceBounds, SubtitleTrackOption, Track, VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import type { DanmakuSearchAnime, DanmakuSearchEpisode } from '@/services/danmaku/types'
import type { SubtitleTrack as DataSourceSubtitleTrack, MediaItem, MediaStreamRequest, PlaybackRequest, ProviderPlaybackProgressEvent, ProviderPlaybackSyncDiagnostic, SubtitleSearchOrigin, SubtitleSearchResult } from '@/services/datasource/types'
import type { MediaPlaybackPreference, MediaPlaybackPreferenceIdentity, MediaSubtitlePreference, MediaTrackPreference } from '@/services/mediaPlaybackPreferences'
import type { PlaybackQueueState } from '@/services/playbackContext'
import type { PlaybackHistoryEntry, PlaybackProgressUpsert } from '@/services/playbackHistory'
import type { PlayerShortcutBindings, PlayerShortcutTarget } from '@/services/playerShortcuts'
import type { SubtitleKeywordMode, SubtitleLanguage, SubtitleSearchMediaContext } from '@/services/subtitle'
import { LogicalSize } from '@tauri-apps/api/dpi'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { open } from '@tauri-apps/plugin-dialog'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import BufferingIndicator from '@/components/player/BufferingIndicator.vue'
import DanmakuOverlay from '@/components/player/DanmakuOverlay.vue'
import DanmakuSearchDialog from '@/components/player/DanmakuSearchDialog.vue'
import MobilePlayerControls from '@/components/player/MobilePlayerControls.vue'
import PlayerControls from '@/components/player/PlayerControls.vue'
import SubtitleSearchDialog from '@/components/player/SubtitleSearchDialog.vue'
import VideoPlayer from '@/components/player/VideoPlayer.vue'
import { useDanmaku } from '@/composables/useDanmaku'
import { useMpv } from '@/composables/useMpv'
import { searchDanmaku } from '@/services/danmaku/client'
import { resolveDanmakuMediaIdentity } from '@/services/danmaku/identity'
import { redactSensitiveText, toSafeErrorMessage } from '@/services/datasource/errors'
import { getMediaPlaybackPreference, saveMediaPlaybackPreference } from '@/services/mediaPlaybackPreferences'
import { getPlaybackMediaContext } from '@/services/playbackContext'
import { createSafeStreamIdentity, getPlaybackProgress, isCompletedPosition, savePlaybackProgress, shouldResumePlayback } from '@/services/playbackHistory'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { loadPlayerInteractionSettings, PLAYBACK_SPEED_OPTIONS } from '@/services/playerInteractionSettings'
import { videoAspectRatioValue as aspectRatioValue, compactPlayerTrackLabel as compactTrackLabel, formatPlaybackTime, playerRenderBackendLabel as renderBackendLabel, playerRenderStatusLabel as renderStatusLabel, safePlayerMenuText as safeMenuText, playerVideoAspectLabel as videoAspectLabel, playerVideoFitLabel as videoFitLabel } from '@/services/playerPresentation'
import { loadPlayerShortcutBindings, PLAYER_SHORTCUTS_CHANGED_EVENT, playerShortcutTargetForEvent } from '@/services/playerShortcuts'
import { isNearbyDoubleTap, resolveTouchGestureAxis, touchSeekTarget, touchVerticalLevel } from '@/services/playerTouchGestures'
import { isNativeAndroidRuntime } from '@/services/runtimePlatform'
import { isVideoFileName } from '@/services/scraper/pathUtils'
import { describeLocalSubtitleSearchProviders, downloadLocalSubtitle, importLocalSubtitle, loadSubtitleSearchSettings, searchLocalSubtitles } from '@/services/subtitle'
import { useDataSourceStore } from '@/stores/datasource'
import { usePlayerChromeStore } from '@/stores/playerChrome'

const AUTO_HIDE_DELAY = 3000
const HISTORY_SAVE_INTERVAL = 10000
const HISTORY_MIN_SAVE_POSITION = 1
const HISTORY_MIN_RESUME_POSITION = 30
const HOME_REFRESH_AFTER_PLAYBACK_DELAY = 1200
const LOCAL_FILE_SOURCE_ID = 'local-file'
const CONTEXT_MENU_WIDTH = 224
const CONTEXT_MENU_MAX_HEIGHT = 360
const CONTEXT_MENU_MARGIN = 12
const ARROW_TAP_SEEK_SECONDS = 5
const ARROW_HOLD_DELAY = 350
const REWIND_HOLD_INTERVAL = 220
const MEDIA_PREFERENCE_SAVE_DELAY = 180
const TOUCH_FEEDBACK_HIDE_DELAY = 850
const TOUCH_CLICK_SUPPRESSION_MS = 650
const TOUCH_MOVEMENT_THRESHOLD = 24
const TOUCH_AXIS_DOMINANCE = 1.25

interface ContextMenuPosition {
  x: number
  y: number
}

interface PlaybackContextMenuDetail {
  label: string
  value: string
}

type TouchGestureMode = 'pending' | 'seek' | 'brightness' | 'volume'

interface TouchGestureSession {
  pointerId: number
  simulatedWithMouse: boolean
  holdArrowKey: 'ArrowLeft' | 'ArrowRight'
  holdArrowStarted: boolean
  startX: number
  startY: number
  width: number
  height: number
  mode: TouchGestureMode
  startPosition: number
  targetPosition: number
  startVolume: number
  startBrightness: number
  startedAt: number
  leftSide: boolean
}

interface TouchGestureFeedback {
  kind: 'seek' | 'brightness' | 'volume' | 'action'
  title: string
  value: string
  percent?: number
}

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const playerChromeStore = usePlayerChromeStore()
const appWindow = isTauriRuntime() ? getCurrentWindow() : null
const isNativeAndroidPlayer = isNativeAndroidRuntime()
const mediaTitle = ref('未命名影片')
const mediaPath = ref('')
const mediaHeaders = ref<Record<string, string>>({})
const activeSourceId = ref('')
const activeItemId = ref('')
const activeLibraryId = ref('')
const activeMediaType = ref<MediaItem['type'] | undefined>()
const activePosterUrl = ref('')
const activeBackdropUrl = ref('')
const activeTitleLogoUrl = ref('')
const failedTitleLogoUrls = ref<Set<string>>(new Set())
const playbackQueue = ref<PlaybackQueueState | null>(null)
const playbackContextId = ref('')
const queueSwitchError = ref<string | null>(null)
const isQueueSwitching = ref(false)
const displayMediaPath = computed(() => redactSensitiveText(mediaPath.value))
const chromeVisible = ref(true)
const chromeManuallyHidden = ref(false)
const controlsInteracting = ref(false)
const playerControlsRef = ref<{
  dismissTransientUi: () => void
  toggleFullscreenFromShortcut: () => Promise<void>
  openDanmakuSettingsFromShortcut: () => void
} | null>(null)
const playerShortcuts = ref<PlayerShortcutBindings>(loadPlayerShortcutBindings())
const keyboardOsdMessage = ref('')
const touchGestureFeedback = ref<TouchGestureFeedback | null>(null)
const lastRenderBounds = ref<RenderSurfaceBounds | null>(null)
const topChromeRef = ref<HTMLElement | null>(null)
const bottomChromeRef = ref<HTMLElement | null>(null)
const topOcclusion = ref(0)
const bottomOcclusion = ref(0)
const diagnosticsOpen = ref(false)
const contextMenuOpen = ref(false)
const playbackDetailOpen = ref(false)
const subtitleSearchOpen = ref(false)
const danmakuSearchOpen = ref(false)
const danmakuSearchResults = ref<DanmakuSearchAnime[]>([])
const danmakuSearchHasMore = ref(false)
const danmakuSearchLoading = ref(false)
const danmakuSearchSelectingEpisodeId = ref<number | null>(null)
const danmakuSearchError = ref<string | null>(null)
const subtitleSearchRequiresSourceChoice = ref(false)
const subtitleSearchOrigin = ref<SubtitleSearchOrigin | null>(null)
const subtitleSearchResults = ref<SubtitleSearchResult[]>([])
const subtitleSearchLoading = ref(false)
const subtitleDownloadingId = ref<string | null>(null)
const localSubtitleImporting = ref(false)
const subtitleSearchError = ref<string | null>(null)
const subtitleSearchProviderSummary = ref('')
const subtitleSearchDefaultLanguage = ref<SubtitleLanguage>(loadSubtitleSearchSettings().defaultLanguage)
const contextMenuPosition = ref<ContextMenuPosition>({ x: CONTEXT_MENU_MARGIN, y: CONTEXT_MENU_MARGIN })
const pictureSettingsError = ref<string | null>(null)
const providerSyncError = ref<string | null>(null)
const providerSyncDiagnostics = ref<ProviderPlaybackSyncDiagnostic[]>([])
const resumeMessage = ref<string | null>(null)
const isPlayerFullscreen = ref(false)
// Single active strategy for this slice: transparent Tauri/WebView overlay above a full-bleed mpv
// video underlay. Legacy top/bottom occlusion strategies are neutralized in Rust.
const renderStrategy = ref<MpvZOrderStrategy>('transparentOverlay')
let hideTimer: number | undefined
let nativeWindowFocusUnlisten: (() => void) | undefined
let playerViewDisposed = false
let renderInitPromise: Promise<void> | null = null
let boundsUpdateInFlight = false
let pendingRenderBounds: RenderSurfaceBounds | null = null
let playbackCleanupStarted = false
let playbackStopPromise: Promise<void> | null = null
let historySaveTimer: number | undefined
let resumeMessageTimer: number | undefined
let homeRefreshTimer: number | undefined
let lastSavedPosition = -1
let playbackStartPosition = 0
let playbackProgressReady = false
const resumeSeekTimers = new Set<number>()
let pendingResumeSeek: { path: string, position: number } | null = null
let activeCachedSubtitlePath: string | null = null
let mediaPreferenceSaveTimer: number | undefined
let mediaPreferenceRestoreGeneration = 0
let restoringMediaPreference = false
let persistedSubtitlePreference: MediaSubtitlePreference | null | undefined
let persistedAudioPreference: MediaTrackPreference | null | undefined
let pendingTrackPreference: {
  preference: MediaPlaybackPreference
  generation: number
  audioRestored: boolean
  subtitleRestored: boolean
} | null = null
let trackPreferenceRestoreInFlight = false
let heldArrowKey: 'ArrowLeft' | 'ArrowRight' | null = null
let heldArrowOwner: 'keyboard' | 'touch' | null = null
let heldArrowTimer: number | undefined
let rewindHoldTimer: number | undefined
let arrowHoldActivated = false
let arrowBasePlaybackSpeed = 1
let pendingKeyboardVolume: number | null = null
let keyboardVolumeCommand: Promise<void> = Promise.resolve()
let keyboardChromeSuppression = 0
let keyboardOsdTimer: number | undefined
let keyboardPreviousVolume = 100
let touchGestureSession: TouchGestureSession | null = null
let lastTouchTap: { x: number, y: number, at: number } | null = null
let touchSingleTapTimer: number | undefined
let touchFeedbackTimer: number | undefined
let suppressPlayerClickUntil = 0
let suppressPlayerContextMenuUntil = 0
let pendingTouchLevelUpdate: { kind: 'brightness' | 'volume', value: number } | null = null
let touchLevelUpdateInFlight = false

const {
  isPlaying,
  currentTime,
  duration,
  volume,
  videoBrightness,
  displayBrightness,
  displayBrightnessSupported,
  playbackSpeed,
  subtitleDelay,
  subtitleTracks,
  audioTracks,
  currentSubtitle,
  currentAudio,
  trackStateReady,
  videoAspectMode,
  videoFitMode,
  renderStatus,
  renderError,
  renderBackend,
  renderDiagnostics,
  playbackDiagnostics,
  videoReady,
  isBuffering,
  bufferSpeedBytesPerSecond,
  orientationSupported,
  orientationMode,
  videoDynamicRange,
  trackError,
  initializeRender,
  updateRenderSurfaceBounds,
  setRenderStrategy,
  setOrientationMode,
  setKnownSubtitleTracks,
  load,
  togglePause,
  seek: seekMpv,
  seekRelative: seekRelativeMpv,
  setVolume,
  setVideoBrightness,
  setDisplayBrightness,
  applyPlaybackSpeed,
  setPlaybackSpeed,
  applySubtitleDelay,
  setSubtitleDelay,
  setSubtitle,
  addExternalSubtitle,
  setAudio,
  setVideoAspect,
  setVideoFit,
  stop,
} = useMpv()

const {
  settings: danmakuSettings,
  comments: danmakuComments,
  loading: danmakuLoading,
  error: danmakuError,
  loadForMedia: loadDanmakuForMedia,
  selectSearchEpisode: selectDanmakuSearchEpisode,
  resetForMediaChange: resetDanmakuForMediaChange,
  updateSettings: updateDanmakuSettings,
  toggleEnabled: toggleDanmaku,
} = useDanmaku()
let danmakuLoadTimer: number | undefined
let danmakuSearchGeneration = 0

const hasMedia = computed(() => mediaPath.value.length > 0)
const currentQueueItem = computed(() => {
  const queue = playbackQueue.value
  return queue ? queue.items[queue.currentIndex] : null
})
const currentTitleLogoUrl = computed(() => {
  const url = activeTitleLogoUrl.value || currentQueueItem.value?.titleLogoUrl || ''
  return url && !failedTitleLogoUrls.value.has(url) ? url : ''
})
const playbackQueueItemCount = computed(() => playbackQueue.value?.items.length ?? (hasMedia.value ? 1 : 0))
const canPlayPrevious = computed(() => Boolean(playbackQueue.value && playbackQueue.value.currentIndex > 0 && !isQueueSwitching.value))
const canPlayNext = computed(() => Boolean(playbackQueue.value && playbackQueue.value.currentIndex < playbackQueue.value.items.length - 1 && !isQueueSwitching.value))
const shouldShowChrome = computed(() => !chromeManuallyHidden.value && (chromeVisible.value || !hasMedia.value || !isPlaying.value || controlsInteracting.value || contextMenuOpen.value || playbackDetailOpen.value || subtitleSearchOpen.value || danmakuSearchOpen.value || danmakuLoading.value))
const isTransparentRootActive = computed(() => hasMedia.value && renderStatus.value === 'ready' && videoReady.value)
const contextMenuTitle = computed(() => safeMenuText(mediaTitle.value || currentQueueItem.value?.title || currentQueueItem.value?.name, '未命名影片'))
const contextMenuSource = computed(() => currentSafeSourceLabel())
const playbackProgressPercent = computed(() => duration.value > 0 ? Math.min(100, Math.max(0, (currentTime.value / duration.value) * 100)) : 0)
const playbackStatsHeadline = computed(() => `${formatPlaybackTime(currentTime.value)} / ${formatPlaybackTime(duration.value)} · ${Math.round(playbackProgressPercent.value)}%`)
const contextMenuDetails = computed<PlaybackContextMenuDetail[]>(() => [
  { label: '状态 / 速度', value: `${isPlaying.value ? 'Playing' : 'Paused'} · ${playbackSpeed.value.toFixed(2)}x` },
  { label: '播放位置', value: playbackStatsHeadline.value },
  { label: '音量', value: `${Math.round(volume.value)}%` },
  { label: '动态范围', value: `${videoDynamicRange.value.label} · ${videoDynamicRange.value.details}` },
  { label: '音轨', value: selectedAudioTrackLabel() },
  { label: '字幕', value: selectedSubtitleTrackLabel() },
  { label: '画面模式', value: `${videoAspectLabel(videoAspectMode.value)} / ${videoFitLabel(videoFitMode.value)}` },
  { label: '渲染', value: `${renderStatusLabel(renderStatus.value)} · ${renderBackendLabel(renderBackend.value)}` },
  { label: '队列', value: playbackQueuePositionLabel() },
  { label: '来源', value: contextMenuSource.value },
])

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
  return hasMedia.value && isPlaying.value && !chromeManuallyHidden.value && !controlsInteracting.value && !contextMenuOpen.value && !playbackDetailOpen.value && !danmakuLoading.value
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
  if (chromeManuallyHidden.value || keyboardChromeSuppression > 0)
    return
  chromeVisible.value = true
  scheduleChromeHide()
}

function revealChromeFromPointer() {
  if (touchGestureSession?.simulatedWithMouse)
    return
  chromeManuallyHidden.value = false
  chromeVisible.value = true
  scheduleChromeHide()
}

function hideChromeFromKeyboard() {
  clearHideTimer()
  chromeManuallyHidden.value = true
  playerControlsRef.value?.dismissTransientUi()
  controlsInteracting.value = false
  chromeVisible.value = false
}

function reloadPlayerShortcuts() {
  playerShortcuts.value = loadPlayerShortcutBindings()
}

function adjustVolumeFromKeyboard(delta: number): number {
  const base = pendingKeyboardVolume ?? volume.value
  const target = Math.max(0, Math.min(100, base + delta))
  pendingKeyboardVolume = target
  keyboardVolumeCommand = keyboardVolumeCommand
    .catch(() => undefined)
    .then(() => setVolume(target))
    .finally(() => {
      if (pendingKeyboardVolume === target)
        pendingKeyboardVolume = null
    })
  return target
}

function showKeyboardOsd(message: string) {
  if (keyboardOsdTimer)
    window.clearTimeout(keyboardOsdTimer)
  keyboardOsdMessage.value = message
  keyboardOsdTimer = window.setTimeout(() => {
    keyboardOsdTimer = undefined
    keyboardOsdMessage.value = ''
  }, 1800)
}

function showTouchFeedback(feedback: TouchGestureFeedback, autoHide = false) {
  if (touchFeedbackTimer)
    window.clearTimeout(touchFeedbackTimer)
  touchFeedbackTimer = undefined
  touchGestureFeedback.value = feedback
  if (autoHide)
    scheduleTouchFeedbackHide()
}

function scheduleTouchFeedbackHide() {
  if (touchFeedbackTimer)
    window.clearTimeout(touchFeedbackTimer)
  touchFeedbackTimer = window.setTimeout(() => {
    touchFeedbackTimer = undefined
    touchGestureFeedback.value = null
  }, TOUCH_FEEDBACK_HIDE_DELAY)
}

function queueTouchLevelUpdate(kind: 'brightness' | 'volume', value: number) {
  pendingTouchLevelUpdate = { kind, value }
  if (!touchLevelUpdateInFlight)
    void flushTouchLevelUpdates()
}

async function flushTouchLevelUpdates() {
  touchLevelUpdateInFlight = true
  try {
    while (pendingTouchLevelUpdate) {
      const update = pendingTouchLevelUpdate
      pendingTouchLevelUpdate = null
      try {
        if (update.kind === 'volume')
          await setVolume(update.value)
        else
          await setDisplayBrightness(update.value)
      }
      catch {
        // A failed frame update must not leave the gesture state locked.
      }
    }
  }
  finally {
    touchLevelUpdateInFlight = false
  }
}

function isTouchGestureTarget(target: EventTarget | null): boolean {
  return !(target instanceof Element && target.closest('button, input, select, textarea, a, [role="dialog"], [role="menu"], [data-player-click-ignore]'))
}

function isProtectedSystemGestureStart(event: PointerEvent, bounds: DOMRect): boolean {
  if (!isNativeAndroidPlayer || event.pointerType !== 'touch')
    return false
  const topGuard = Math.max(48, Math.min(88, bounds.height * 0.08))
  const bottomGuard = Math.max(24, Math.min(48, bounds.height * 0.04))
  return event.clientY - bounds.top <= topGuard || bounds.bottom - event.clientY <= bottomGuard
}

function handlePlayerTouchPointerDown(event: PointerEvent) {
  const simulatedWithMouse = event.pointerType === 'mouse' && event.altKey && event.button === 0
  if ((event.pointerType !== 'touch' && !simulatedWithMouse) || !hasMedia.value || touchGestureSession || !isTouchGestureTarget(event.target))
    return

  const host = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  if (!host)
    return

  const bounds = host.getBoundingClientRect()
  if (isProtectedSystemGestureStart(event, bounds))
    return
  event.preventDefault()
  host.setPointerCapture(event.pointerId)
  suppressPlayerClickUntil = Date.now() + TOUCH_CLICK_SUPPRESSION_MS
  suppressPlayerContextMenuUntil = Date.now() + TOUCH_CLICK_SUPPRESSION_MS
  touchGestureSession = {
    pointerId: event.pointerId,
    simulatedWithMouse,
    holdArrowKey: event.clientX < bounds.left + bounds.width / 2 ? 'ArrowLeft' : 'ArrowRight',
    holdArrowStarted: false,
    startX: event.clientX,
    startY: event.clientY,
    width: Math.max(1, bounds.width),
    height: Math.max(1, bounds.height),
    mode: 'pending',
    startPosition: currentTime.value,
    targetPosition: currentTime.value,
    startVolume: volume.value,
    startBrightness: displayBrightness.value,
    startedAt: Date.now(),
    leftSide: event.clientX < bounds.left + bounds.width / 2,
  }
  touchGestureSession.holdArrowStarted = beginHeldArrow(touchGestureSession.holdArrowKey, 'touch')
}

function handlePlayerTouchPointerMove(event: PointerEvent) {
  const session = touchGestureSession
  if (!session || session.pointerId !== event.pointerId)
    return

  event.preventDefault()
  const deltaX = event.clientX - session.startX
  const deltaY = event.clientY - session.startY
  if (session.mode === 'pending') {
    if (session.holdArrowStarted && heldArrowOwner === 'touch' && arrowHoldActivated)
      return
    const axis = resolveTouchGestureAxis(
      deltaX,
      deltaY,
      session.simulatedWithMouse ? 12 : TOUCH_MOVEMENT_THRESHOLD,
      session.simulatedWithMouse ? 1 : TOUCH_AXIS_DOMINANCE,
    )
    if (axis === 'pending')
      return
    if (session.holdArrowStarted) {
      session.holdArrowStarted = false
      void releaseHeldArrow(false, 'touch')
    }
    session.mode = axis === 'horizontal' ? 'seek' : session.leftSide ? 'brightness' : 'volume'
    clearHideTimer()
  }

  if (session.mode === 'seek') {
    session.targetPosition = touchSeekTarget(session.startPosition, deltaX, session.width, duration.value)
    const offset = Math.round(session.targetPosition - session.startPosition)
    showTouchFeedback({
      kind: 'seek',
      title: offset >= 0 ? '快进' : '后退',
      value: `${offset >= 0 ? '+' : ''}${offset} 秒 · ${formatPlaybackTime(session.targetPosition)}`,
    })
    return
  }

  if (session.mode === 'brightness') {
    if (!displayBrightnessSupported.value) {
      showTouchFeedback({ kind: 'brightness', title: '屏幕亮度', value: '当前显示器不支持' })
      return
    }
    const level = touchVerticalLevel(session.startBrightness, deltaY, session.height)
    queueTouchLevelUpdate('brightness', level)
    showTouchFeedback({ kind: 'brightness', title: '屏幕亮度', value: `${Math.round(level)}%`, percent: level })
    return
  }

  const level = touchVerticalLevel(session.startVolume, deltaY, session.height)
  queueTouchLevelUpdate('volume', level)
  showTouchFeedback({ kind: 'volume', title: '音量', value: `${Math.round(level)}%`, percent: level })
}

function handlePlayerTouchPointerEnd(event: PointerEvent, cancelled = false) {
  const session = touchGestureSession
  if (!session || session.pointerId !== event.pointerId)
    return

  const host = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  if (host?.hasPointerCapture(event.pointerId))
    host.releasePointerCapture(event.pointerId)
  touchGestureSession = null
  suppressPlayerClickUntil = Date.now() + TOUCH_CLICK_SUPPRESSION_MS
  suppressPlayerContextMenuUntil = Date.now() + TOUCH_CLICK_SUPPRESSION_MS

  const completedArrowHold = session.holdArrowStarted && heldArrowOwner === 'touch' && arrowHoldActivated
  if (session.holdArrowStarted) {
    session.holdArrowStarted = false
    void releaseHeldArrow(false, 'touch')
  }

  if (completedArrowHold) {
    scheduleTouchFeedbackHide()
    return
  }

  const pendingTap = session.mode === 'pending'
    && (!cancelled || Date.now() - session.startedAt <= 700)
  if (pendingTap) {
    handleTouchTap(event.clientX, event.clientY)
    return
  }

  if (!cancelled && session.mode === 'seek')
    void seek(session.targetPosition).catch(() => undefined)
  scheduleTouchFeedbackHide()
}

function handleTouchTap(x: number, y: number) {
  const currentTap = { x, y, at: Date.now() }
  if (isNearbyDoubleTap(lastTouchTap, currentTap)) {
    if (touchSingleTapTimer)
      window.clearTimeout(touchSingleTapTimer)
    touchSingleTapTimer = undefined
    lastTouchTap = null
    const willPause = isPlaying.value
    showTouchFeedback({
      kind: 'action',
      title: willPause ? '已暂停' : '继续播放',
      value: '双击画面',
    }, true)
    void handleTogglePause().catch(() => undefined)
    return
  }

  lastTouchTap = currentTap
  if (touchSingleTapTimer)
    window.clearTimeout(touchSingleTapTimer)
  touchSingleTapTimer = window.setTimeout(() => {
    touchSingleTapTimer = undefined
    lastTouchTap = null
    toggleChromeFromTouch()
  }, 320)
}

function handleVisibilityChange() {
  if (!document.hidden)
    return
  const session = touchGestureSession
  touchGestureSession = null
  pendingTouchLevelUpdate = null
  lastTouchTap = null
  if (session?.holdArrowStarted)
    void releaseHeldArrow(false, 'touch')
  playerControlsRef.value?.dismissTransientUi()
  controlsInteracting.value = false
  scheduleChromeHide()
}

function toggleChromeFromTouch() {
  chromeManuallyHidden.value = false
  if (chromeVisible.value) {
    clearHideTimer()
    playerControlsRef.value?.dismissTransientUi()
    controlsInteracting.value = false
    chromeVisible.value = false
    return
  }
  chromeVisible.value = true
  scheduleChromeHide()
}

async function runKeyboardAction(action: () => Promise<void> | void) {
  keyboardChromeSuppression += 1
  try {
    await action()
  }
  finally {
    keyboardChromeSuppression = Math.max(0, keyboardChromeSuppression - 1)
  }
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
  playerControlsRef.value?.dismissTransientUi()
  controlsInteracting.value = false
  closePlaybackContextMenu(false)
  void releaseHeldArrow(false)
  scheduleChromeHide()
}

function handleApplicationPointerLeave() {
  if (!hasMedia.value)
    return
  playerControlsRef.value?.dismissTransientUi()
  controlsInteracting.value = false
  closePlaybackContextMenu(false)
  scheduleChromeHide()
}

function handlePlayerBack() {
  if (window.history.state?.back)
    router.back()
  else
    void router.push('/')
}

function markTitleLogoFailed(url: string) {
  failedTitleLogoUrls.value = new Set([...failedTitleLogoUrls.value, url])
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
  revealChrome()
}

function handleWindowResize() {
  void updateChromeOcclusion()
}

function queryStringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function syncPlaybackQueueFromRoute() {
  const contextId = queryStringValue(route.query.contextId)
  const routeSourceId = queryStringValue(route.query.sourceId)
  const itemId = queryStringValue(route.query.itemId)
  const playbackContext = contextId ? getPlaybackMediaContext(contextId) : null

  playbackContextId.value = contextId
  if (!playbackContext?.queue || playbackContext.queue.items.length === 0 || (routeSourceId && playbackContext.sourceId !== routeSourceId)) {
    playbackQueue.value = null
    return
  }

  const routeIndex = itemId
    ? playbackContext.queue.items.findIndex(item => item.id === itemId)
    : -1
  if (itemId && routeIndex < 0) {
    playbackQueue.value = null
    return
  }

  const currentIndex = routeIndex >= 0 ? routeIndex : playbackContext.queue.currentIndex
  playbackQueue.value = {
    items: playbackContext.queue.items.map(item => ({ ...item })),
    currentIndex: Math.min(Math.max(currentIndex, 0), playbackContext.queue.items.length - 1),
  }
}

function currentPlaybackContext() {
  const context = playbackContextId.value ? getPlaybackMediaContext(playbackContextId.value) : null
  if (!context)
    return null

  const sourceId = queryStringValue(route.query.sourceId)
  const itemId = queryStringValue(route.query.itemId)
  if ((sourceId && context.sourceId !== sourceId) || (itemId && context.itemId !== itemId && !context.queue?.items.some(item => item.id === itemId)))
    return null

  return context
}

function currentPlaybackItem() {
  return currentQueueItem.value ?? currentPlaybackContext()?.currentItem
}

function currentDisplaySourceId(): string {
  return activeSourceId.value || currentPlaybackContext()?.sourceId || currentQueueItem.value?.sourceId || (hasMedia.value ? LOCAL_FILE_SOURCE_ID : '')
}

function currentSafeSourceLabel(): string {
  const sourceId = currentDisplaySourceId()
  if (sourceId === LOCAL_FILE_SOURCE_ID)
    return '本地文件'

  const config = store.configs.find(item => item.id === sourceId)
  return safeMenuText(config?.displayName || config?.name || sourceId, '媒体来源')
}

function selectedAudioTrackLabel(): string {
  const track = audioTracks.value.find(item => item.id === currentAudio.value)
  if (!track)
    return currentAudio.value == null ? '自动' : `#${currentAudio.value}`

  return compactTrackLabel([track.language, track.title, track.codec, track.channels ? `${track.channels}ch` : null], `#${track.id}`)
}

function selectedSubtitleTrackLabel(): string {
  if (!currentSubtitle.value)
    return '关闭'

  const track = subtitleTracks.value.find(item => item.id === currentSubtitle.value)
  if (!track)
    return String(currentSubtitle.value)

  const source = track.source === 'embedded'
    ? '内封'
    : track.source === 'downloaded'
      ? '本地下载'
      : track.source === 'provider'
        ? '媒体源'
        : '详情'
  return compactTrackLabel([source, track.language, track.title, track.codec], String(track.id))
}

function playbackQueuePositionLabel(): string {
  const queue = playbackQueue.value
  if (!queue)
    return hasMedia.value ? '单个媒体' : '无媒体'

  return `${queue.currentIndex + 1} / ${queue.items.length}`
}

function syncActiveMediaMetadataFromRoute() {
  const context = currentPlaybackContext()
  const item = currentPlaybackItem()
  activeSourceId.value = queryStringValue(route.query.sourceId) || item?.sourceId || context?.sourceId || ''
  activeItemId.value = queryStringValue(route.query.itemId) || item?.id || context?.itemId || ''
  activeLibraryId.value = item?.libraryId ?? ''
  activeMediaType.value = item?.type
  activePosterUrl.value = item?.posterUrl ?? ''
  activeBackdropUrl.value = item?.backdropUrl ?? ''
  activeTitleLogoUrl.value = item?.titleLogoUrl ?? ''
  mediaTitle.value = item?.title || item?.name || context?.title || '未命名影片'
}

async function resolvePlaybackLoadRequest(): Promise<MediaStreamRequest> {
  const context = currentPlaybackContext()
  const sourceId = queryStringValue(route.query.sourceId) || context?.sourceId || ''
  const itemId = queryStringValue(route.query.itemId) || context?.itemId || ''
  const locator = context?.locator

  if (locator?.kind === 'localPath' && context?.sourceId === sourceId && context.itemId === itemId)
    return { url: locator.path }

  if (!sourceId || !itemId)
    throw new Error('缺少可解析的播放上下文。')

  store.loadConfigs()
  await store.syncManager()
  const source = store.getSource(sourceId)
  if (!source)
    throw new Error('数据源不可用，请检查设置或重新登录。')

  const request: PlaybackRequest = {
    itemId,
    mediaSourceId: currentMediaSourceId(),
  }
  return source.getStreamRequest
    ? source.getStreamRequest(request)
    : { url: await source.getStreamURL(request.itemId) }
}

function currentHistoryIdentity(): Pick<PlaybackProgressUpsert, 'sourceId' | 'mediaIdentity'> | null {
  if (!mediaPath.value)
    return null

  const context = currentPlaybackContext()
  const sourceId = activeSourceId.value || context?.sourceId || currentQueueItem.value?.sourceId || LOCAL_FILE_SOURCE_ID
  const itemId = activeItemId.value || context?.itemId || currentQueueItem.value?.id || ''
  const mediaIdentity = itemId || createSafeStreamIdentity(sourceId, undefined, mediaPath.value)

  if (!sourceId || !mediaIdentity)
    return null

  return { sourceId, mediaIdentity }
}

function currentMediaPreferenceIdentity(): MediaPlaybackPreferenceIdentity | null {
  return currentHistoryIdentity()
}

function trackPreference(track: Track | SubtitleTrackOption | undefined): MediaTrackPreference | null {
  if (!track)
    return null
  return {
    language: track.language ?? null,
    title: track.title ?? null,
    codec: track.codec ?? null,
    channels: track.channels ?? null,
    trackId: numericTrackId(track),
  }
}

function currentSubtitlePreference(): MediaSubtitlePreference {
  const selected = subtitleTracks.value.find(track => track.id === currentSubtitle.value)
  if (activeCachedSubtitlePath) {
    return {
      kind: 'cachedExternal',
      track: trackPreference(selected),
      cachedPath: activeCachedSubtitlePath,
    }
  }
  if (!currentSubtitle.value)
    return { kind: 'off' }
  return {
    kind: 'embedded',
    track: trackPreference(selected),
  }
}

function currentAudioPreference(): MediaTrackPreference | null {
  return trackPreference(audioTracks.value.find(track => track.id === currentAudio.value))
}

function clearMediaPreferenceSaveTimer() {
  if (!mediaPreferenceSaveTimer)
    return
  window.clearTimeout(mediaPreferenceSaveTimer)
  mediaPreferenceSaveTimer = undefined
}

function scheduleMediaPreferenceSave() {
  if (restoringMediaPreference)
    return
  clearMediaPreferenceSaveTimer()
  const identity = currentMediaPreferenceIdentity()
  if (!identity)
    return
  mediaPreferenceSaveTimer = window.setTimeout(() => {
    mediaPreferenceSaveTimer = undefined
    void saveMediaPreferenceNow(identity)
  }, MEDIA_PREFERENCE_SAVE_DELAY)
}

async function saveMediaPreferenceNow(identity = currentMediaPreferenceIdentity(), force = false): Promise<boolean> {
  clearMediaPreferenceSaveTimer()
  if (!identity || (restoringMediaPreference && !force))
    return false
  const subtitlePreference = persistedSubtitlePreference !== undefined
    ? persistedSubtitlePreference
    : trackStateReady.value
      ? currentSubtitlePreference()
      : null
  const audioPreference = persistedAudioPreference !== undefined
    ? persistedAudioPreference
    : trackStateReady.value
      ? currentAudioPreference()
      : null
  const saved = await saveMediaPlaybackPreference({
    ...identity,
    subtitle: subtitlePreference,
    audio: audioPreference,
    subtitleDelay: subtitleDelay.value,
    playbackSpeed: playbackSpeed.value,
    videoBrightness: videoBrightness.value,
    aspectMode: videoAspectMode.value,
    fitMode: videoFitMode.value,
  })
  if (saved) {
    persistedSubtitlePreference = subtitlePreference
    persistedAudioPreference = audioPreference
  }
  return saved
}

async function restoreMediaPlaybackPreference() {
  const identity = currentMediaPreferenceIdentity()
  if (!identity)
    return
  const generation = ++mediaPreferenceRestoreGeneration
  const preference = await getMediaPlaybackPreference(identity)
  if (!preference || generation !== mediaPreferenceRestoreGeneration || playbackCleanupStarted)
    return

  persistedSubtitlePreference = preference.subtitle ?? null
  persistedAudioPreference = preference.audio ?? null
  restoringMediaPreference = true
  try {
    await applyPlaybackSpeed(preference.playbackSpeed)
    await applySubtitleDelay(preference.subtitleDelay)
    await setVideoBrightness(preference.videoBrightness)
    await setVideoAspect(preference.aspectMode)
    await setVideoFit(preference.fitMode)
    pendingTrackPreference = {
      preference,
      generation,
      audioRestored: preference.audio == null,
      subtitleRestored: preference.subtitle == null,
    }
    await restorePendingTrackPreference()
    scheduleRenderBoundsSync()
  }
  finally {
    restoringMediaPreference = false
  }
}

async function restorePendingTrackPreference() {
  const pending = pendingTrackPreference
  if (!pending || trackPreferenceRestoreInFlight || pending.generation !== mediaPreferenceRestoreGeneration)
    return

  trackPreferenceRestoreInFlight = true
  restoringMediaPreference = true
  try {
    if (!pending.audioRestored)
      pending.audioRestored = await restoreAudioPreference(pending.preference)
    if (pending !== pendingTrackPreference || pending.generation !== mediaPreferenceRestoreGeneration)
      return
    if (!pending.subtitleRestored)
      pending.subtitleRestored = await restoreSubtitlePreference(pending.preference)
    if (pending !== pendingTrackPreference || pending.generation !== mediaPreferenceRestoreGeneration)
      return
    if (pending === pendingTrackPreference && pending.audioRestored && pending.subtitleRestored)
      pendingTrackPreference = null
  }
  finally {
    restoringMediaPreference = false
    trackPreferenceRestoreInFlight = false
  }
}

function cancelPendingTrackPreferenceRestore() {
  mediaPreferenceRestoreGeneration += 1
  pendingTrackPreference = null
}

function resetPersistedTrackPreferences() {
  persistedSubtitlePreference = undefined
  persistedAudioPreference = undefined
}

async function restoreAudioPreference(preference: MediaPlaybackPreference): Promise<boolean> {
  const track = matchTrackPreference(audioTracks.value, preference.audio)
  if (!track)
    return false
  try {
    await setAudio(track.id)
    return true
  }
  catch {
    return false
  }
}

async function restoreSubtitlePreference(preference: MediaPlaybackPreference): Promise<boolean> {
  const subtitle = preference.subtitle
  if (!subtitle)
    return true
  if (subtitle.kind === 'off') {
    try {
      activeCachedSubtitlePath = null
      await setSubtitle(null)
      return true
    }
    catch {
      return false
    }
  }
  if (subtitle.kind === 'cachedExternal' && subtitle.cachedPath) {
    if (isNativeAndroidPlayer && !videoReady.value)
      return false
    try {
      await addExternalSubtitle(
        subtitle.cachedPath,
        subtitle.track?.title ?? subtitle.track?.language ?? '已保存字幕',
        subtitle.track?.language ?? undefined,
      )
      activeCachedSubtitlePath = subtitle.cachedPath
      return true
    }
    catch {
      return false
    }
  }
  const track = matchTrackPreference(subtitleTracks.value, subtitle.track)
  if (!track)
    return false
  try {
    activeCachedSubtitlePath = null
    await setSubtitle(track.id)
    return true
  }
  catch {
    return false
  }
}

function matchTrackPreference<T extends Track | SubtitleTrackOption>(tracks: readonly T[], preference: MediaTrackPreference | null | undefined): T | null {
  if (!preference)
    return null
  if (preference.trackId != null) {
    const exactTrack = tracks.find(track => numericTrackId(track) === preference.trackId)
    if (exactTrack)
      return exactTrack
  }
  let best: { track: T, score: number } | null = null
  for (const track of tracks) {
    let score = 0
    if (sameTrackText(preference.title, track.title))
      score += 6
    if (sameTrackText(preference.language, track.language))
      score += 4
    if (sameTrackText(preference.codec, track.codec))
      score += 2
    if (preference.channels != null && track.channels === preference.channels)
      score += 2
    if (!best || score > best.score)
      best = { track, score }
  }
  const minimumScore = preference.title || preference.language
    ? 4
    : preference.codec || preference.channels != null
      ? 2
      : Number.POSITIVE_INFINITY
  return best && best.score >= minimumScore ? best.track : null
}

function numericTrackId(track: Track | SubtitleTrackOption): number | null {
  if ('mpvId' in track && typeof track.mpvId === 'number')
    return track.mpvId
  return typeof track.id === 'number' ? track.id : null
}

function sameTrackText(expected: string | null | undefined, actual: string | null | undefined): boolean {
  if (!expected || !actual)
    return false
  return expected.trim().toLocaleLowerCase() === actual.trim().toLocaleLowerCase()
}

function currentHistoryPayload(): PlaybackProgressUpsert | null {
  const identity = currentHistoryIdentity()
  if (!identity)
    return null

  const context = currentPlaybackContext()
  const queueItem = currentQueueItem.value
  const itemId = activeItemId.value || context?.itemId || queueItem?.id || undefined
  const libraryId = activeLibraryId.value || queueItem?.libraryId
  const mediaType = activeMediaType.value ?? queueItem?.type
  const position = effectivePlaybackPosition()
  const mediaDuration = duration.value > 0 ? duration.value : queueItem?.duration

  return {
    ...identity,
    libraryId,
    itemId,
    title: mediaTitle.value || context?.title || queueItem?.title || queueItem?.name || '未命名影片',
    streamIdentity: createSafeStreamIdentity(identity.sourceId, itemId, mediaPath.value),
    mediaType,
    posterUrl: activePosterUrl.value || queueItem?.posterUrl,
    backdropUrl: activeBackdropUrl.value || queueItem?.backdropUrl,
    titleLogoUrl: activeTitleLogoUrl.value || queueItem?.titleLogoUrl,
    position,
    duration: mediaDuration,
    completed: isCompletedPosition(position, mediaDuration),
  }
}

function effectivePlaybackPosition(): number {
  const pending = pendingResumeSeek
  if (pending && pending.path === mediaPath.value)
    return Math.max(0, pending.position)
  return Math.max(0, currentTime.value)
}

function currentMediaSourceId(): string | undefined {
  const routeMediaSourceId = queryStringValue(route.query.mediaSourceId)
  if (routeMediaSourceId)
    return routeMediaSourceId

  const context = currentPlaybackContext()
  const sourceId = activeSourceId.value || queryStringValue(route.query.sourceId)
  const itemId = activeItemId.value || queryStringValue(route.query.itemId)
  if (!context || context.sourceId !== sourceId || context.itemId !== itemId)
    return undefined

  if (context.locator.kind === 'dataSource')
    return context.locator.mediaSourceId ?? context.mediaSourceId

  return context.mediaSourceId
}

function routeResumePosition(): number | undefined {
  return currentPlaybackItem()?.resumePosition
}

function shouldResumePosition(position: number | undefined, mediaDuration: number | undefined): position is number {
  if (typeof position !== 'number' || !Number.isFinite(position) || position < 30)
    return false

  return !isCompletedPosition(position, mediaDuration)
}

function syncProviderDiagnostics(sourceId: string): void {
  const source = store.getSource(sourceId)
  providerSyncDiagnostics.value = source?.getPlaybackSyncDiagnostics?.() ?? providerSyncDiagnostics.value
}

function rememberProviderTriggerDiagnostic(payload: PlaybackProgressUpsert, event: ProviderPlaybackProgressEvent, endpoint: string, message: string): void {
  providerSyncDiagnostics.value = [{
    timestamp: new Date().toISOString(),
    sourceId: payload.sourceId || 'unknown',
    event,
    stage: 'trigger',
    ok: false,
    endpoint,
    itemIdPresent: Boolean(payload.itemId),
    mediaSourceIdPresent: Boolean(currentMediaSourceId()),
    playSessionIdPresent: false,
    position: Number.isFinite(payload.position) ? Math.max(0, payload.position) : 0,
    message: redactSensitiveText(message),
  }, ...providerSyncDiagnostics.value].slice(0, 12)
}

async function syncProviderProgress(payload: PlaybackProgressUpsert, event: ProviderPlaybackProgressEvent): Promise<void> {
  if (!payload.sourceId || !Number.isFinite(payload.position)) {
    rememberProviderTriggerDiagnostic(payload, event, 'PlayerView.syncProviderProgress', '未触发 provider sync：缺少 sourceId 或 position。')
    return
  }

  if (payload.sourceId === LOCAL_FILE_SOURCE_ID) {
    providerSyncDiagnostics.value = []
    return
  }

  const source = store.getSource(payload.sourceId)
  if (!source) {
    rememberProviderTriggerDiagnostic(payload, event, 'DataSourceManager.getSource', '未触发 provider sync：播放中的数据源实例不可用。')
    return
  }

  if (!source.syncPlaybackProgress) {
    providerSyncDiagnostics.value = []
    return
  }

  try {
    await source.syncPlaybackProgress({
      itemId: payload.itemId ?? '',
      mediaSourceId: currentMediaSourceId(),
      mediaType: payload.mediaType,
      position: payload.position,
      duration: payload.duration,
      startPosition: playbackStartPosition,
      isPaused: event === 'paused' || event === 'stopped' || event === 'completed' || !isPlaying.value,
      completed: payload.completed ?? false,
      event,
      playbackRate: playbackSpeed.value,
    })
    providerSyncError.value = null
  }
  catch (error) {
    providerSyncError.value = toSafeErrorMessage(error, 'Emby 播放进度同步失败。')
  }
  finally {
    syncProviderDiagnostics(payload.sourceId)
  }
}

function providerEventForPayload(payload: PlaybackProgressUpsert, fallback: ProviderPlaybackProgressEvent): ProviderPlaybackProgressEvent {
  return payload.completed ? 'completed' : fallback
}

function isLowPositionTerminalEvent(payload: PlaybackProgressUpsert, event: ProviderPlaybackProgressEvent): boolean {
  return !payload.completed && (event === 'paused' || event === 'stopped') && payload.position < HISTORY_MIN_RESUME_POSITION
}

function shouldRefreshHomeAfterProgressEvent(event: ProviderPlaybackProgressEvent, providerEvent: ProviderPlaybackProgressEvent): boolean {
  return event === 'stopped' || event === 'completed' || providerEvent === 'completed'
}

function scheduleHomeSectionsRefreshAfterPlayback() {
  if (homeRefreshTimer)
    window.clearTimeout(homeRefreshTimer)

  homeRefreshTimer = window.setTimeout(() => {
    homeRefreshTimer = undefined
    void store.loadHomeSections({ force: true, background: true })
  }, HOME_REFRESH_AFTER_PLAYBACK_DELAY)
}

function shouldSaveLocalProgress(payload: PlaybackProgressUpsert, force: boolean, event: ProviderPlaybackProgressEvent): boolean {
  if (payload.completed)
    return payload.position >= HISTORY_MIN_SAVE_POSITION

  if (force && isLowPositionTerminalEvent(payload, event))
    return false

  return payload.position >= HISTORY_MIN_SAVE_POSITION
}

async function saveCurrentProgress(force = false, event: ProviderPlaybackProgressEvent = 'progress') {
  if (!playbackProgressReady)
    return

  const payload = currentHistoryPayload()
  if (!payload)
    return

  const providerEvent = providerEventForPayload(payload, event)
  if (!force && (!isPlaying.value || payload.position < HISTORY_MIN_SAVE_POSITION || Math.abs(payload.position - lastSavedPosition) < HISTORY_MIN_SAVE_POSITION)) {
    if (event !== 'progress' && !isLowPositionTerminalEvent(payload, event))
      void syncProviderProgress(payload, providerEvent)
    return
  }

  if (!shouldSaveLocalProgress(payload, force, event)) {
    if (event !== 'progress') {
      const providerSync = syncProviderProgress(payload, providerEvent)
      if (shouldRefreshHomeAfterProgressEvent(event, providerEvent)) {
        await providerSync
        scheduleHomeSectionsRefreshAfterPlayback()
      }
      else {
        void providerSync
      }
    }
    return
  }

  const saved = await savePlaybackProgress(payload)
  if (saved)
    lastSavedPosition = saved.position

  const providerSync = syncProviderProgress(payload, providerEvent)
  if (shouldRefreshHomeAfterProgressEvent(event, providerEvent)) {
    await providerSync
    scheduleHomeSectionsRefreshAfterPlayback()
  }
  else {
    void providerSync
  }
}

function syncProviderPlaybackStarted() {
  const payload = currentHistoryPayload()
  if (payload)
    void syncProviderProgress(payload, 'started')
}

async function readSavedProgress(): Promise<PlaybackHistoryEntry | null> {
  const identity = currentHistoryIdentity()
  if (!identity)
    return null

  return getPlaybackProgress(identity)
}

async function resumeSavedProgressIfAvailable() {
  const saved = await readSavedProgress()
  const fallbackPosition = routeResumePosition()
  const fallbackDuration = duration.value > 0 ? duration.value : currentQueueItem.value?.duration
  const position = shouldResumePosition(fallbackPosition, fallbackDuration)
    ? fallbackPosition
    : shouldResumePlayback(saved)
      ? saved.position
      : undefined

  if (position == null) {
    cancelPendingResumeSeek()
    playbackStartPosition = 0
    playbackProgressReady = true
    resumeMessage.value = null
    return
  }

  playbackStartPosition = position
  pendingResumeSeek = { path: mediaPath.value, position }
  resumeMessage.value = `正在恢复到 ${formatPlaybackTime(position)}`
  await seekResumePosition(position)
  playbackProgressReady = true
}

async function seekResumePosition(position: number) {
  clearResumeSeekTimers()
  await seekResumePositionSilently(position)
  scheduleResumeSeek(position, 250, true)
  for (const delay of [900, 1800, 3200])
    scheduleResumeSeek(position, delay, false)
}

async function seekResumePositionSilently(position: number) {
  try {
    // Android may accept a seek while the remote stream is still opening, then reset time-pos to
    // zero when FILE_LOADED arrives. Do not publish an optimistic frontend time for resume seeks:
    // only a native time update after video readiness may confirm and clear the pending resume.
    if (isNativeAndroidPlayer)
      await seekMpv(position, { optimistic: false })
    else
      await seekMpv(position)
  }
  catch {
    // Resume seek is retried after media metadata settles; failures must not break playback startup.
  }
}

function scheduleResumeSeek(position: number, delay: number, force: boolean) {
  const path = mediaPath.value
  const timer = window.setTimeout(() => {
    resumeSeekTimers.delete(timer)
    if (playbackCleanupStarted || !mediaPath.value || mediaPath.value !== path || pendingResumeSeek?.path !== path)
      return
    if ((!isNativeAndroidPlayer || videoReady.value) && Math.abs(currentTime.value - position) <= 5) {
      completePendingResumeSeek(path, position)
      return
    }
    if (force || Math.abs(currentTime.value - position) > 5)
      void seekResumePositionSilently(position)
  }, delay)
  resumeSeekTimers.add(timer)
}

async function applyPendingResumeSeekWhenReady() {
  const pending = pendingResumeSeek
  if (!pending || pending.path !== mediaPath.value || playbackCleanupStarted)
    return
  if (!videoReady.value && duration.value <= 0)
    return
  await seekResumePositionSilently(pending.position)
}

function completePendingResumeSeek(path: string, position: number) {
  if (pendingResumeSeek?.path !== path || Math.abs(pendingResumeSeek.position - position) > 0.01)
    return
  pendingResumeSeek = null
  clearResumeSeekTimers()
  resumeMessage.value = `已从 ${formatPlaybackTime(position)} 继续播放`
  clearResumeMessageTimer()
  resumeMessageTimer = window.setTimeout(() => {
    resumeMessageTimer = undefined
    resumeMessage.value = null
  }, 3600)
}

function cancelPendingResumeSeek() {
  const hadPendingResume = pendingResumeSeek != null
  pendingResumeSeek = null
  clearResumeSeekTimers()
  if (hadPendingResume) {
    clearResumeMessageTimer()
    resumeMessage.value = null
  }
}

async function seek(position: number) {
  cancelPendingResumeSeek()
  await seekMpv(position)
}

async function seekRelative(offset: number) {
  cancelPendingResumeSeek()
  await seekRelativeMpv(offset)
}

function clearResumeSeekTimers() {
  for (const timer of resumeSeekTimers)
    window.clearTimeout(timer)
  resumeSeekTimers.clear()
}

function clearResumeMessageTimer() {
  if (!resumeMessageTimer)
    return

  window.clearTimeout(resumeMessageTimer)
  resumeMessageTimer = undefined
}

function resetHistorySaveState() {
  cancelPendingResumeSeek()
  clearResumeMessageTimer()
  lastSavedPosition = -1
  playbackStartPosition = 0
  playbackProgressReady = false
  resumeMessage.value = null
}

function startHistorySaveTimer() {
  if (historySaveTimer)
    return

  historySaveTimer = window.setInterval(() => {
    void saveCurrentProgress(false)
  }, HISTORY_SAVE_INTERVAL)
}

function clearHistorySaveTimer() {
  if (!historySaveTimer)
    return

  window.clearInterval(historySaveTimer)
  historySaveTimer = undefined
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
  const source = track.url ? 'provider' : 'detail'
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
    unavailableReason: source === 'provider'
      ? '该外部字幕缺少可加载地址，暂时只能在详情页确认存在。'
      : '该字幕来自媒体详情，但当前播放流未暴露可直接加载的字幕地址。',
  }
}

function openSubtitleSearch() {
  subtitleSearchDefaultLanguage.value = loadSubtitleSearchSettings().defaultLanguage
  subtitleSearchResults.value = []
  subtitleSearchError.value = null
  subtitleSearchLoading.value = false
  subtitleDownloadingId.value = null
  subtitleSearchProviderSummary.value = ''

  const sourceId = currentDisplaySourceId()
  const sourceType = store.configs.find(config => config.id === sourceId)?.type
  subtitleSearchRequiresSourceChoice.value = sourceType === 'emby'
  subtitleSearchOrigin.value = sourceType === 'emby' ? null : 'local'
  subtitleSearchOpen.value = true
  if (subtitleSearchOrigin.value === 'local')
    void refreshLocalSubtitleProviderSummary()
  revealChrome()
}

function openDanmakuSearch() {
  danmakuSearchGeneration++
  danmakuSearchResults.value = []
  danmakuSearchHasMore.value = false
  danmakuSearchError.value = null
  danmakuSearchOpen.value = true
}

function currentDanmakuMediaIdentity() {
  const context = currentPlaybackContext()
  const detail = context?.detail
  const playbackItem = currentPlaybackItem()
  const seriesName = detail?.seriesName ?? playbackItem?.seriesName
  return resolveDanmakuMediaIdentity({
    mediaTitle: mediaTitle.value,
    fileName: seriesName?.trim() ? '' : currentDanmakuFileName(),
    seriesName,
    seasonNumber: detail?.seasonNumber ?? playbackItem?.seasonNumber,
    episodeNumber: detail?.episodeNumber ?? playbackItem?.episodeNumber,
  })
}

function currentDanmakuFileName(): string {
  const context = currentPlaybackContext()
  const candidates = [
    context?.locator.kind === 'localPath' ? context.locator.path : undefined,
    currentPlaybackItem()?.path,
    context?.detail?.path,
  ]
  for (const candidate of candidates) {
    if (!candidate || /^[a-z][a-z0-9+.-]*:\/\//i.test(candidate) || candidate.includes('?') || candidate.includes('#'))
      continue
    const fileName = subtitleFileNameFromPath(candidate)
    if (fileName && isVideoFileName(fileName))
      return fileName
  }
  return ''
}

function closeDanmakuSearch() {
  danmakuSearchGeneration++
  danmakuSearchOpen.value = false
  danmakuSearchLoading.value = false
  danmakuSearchSelectingEpisodeId.value = null
  danmakuSearchError.value = null
  danmakuSearchResults.value = []
}

async function runDanmakuSearch(keyword: string, episode: string) {
  const currentGeneration = ++danmakuSearchGeneration
  danmakuSearchLoading.value = true
  danmakuSearchError.value = null
  danmakuSearchResults.value = []
  danmakuSearchHasMore.value = false
  try {
    const result = await searchDanmaku(danmakuSettings.value, keyword, episode)
    if (danmakuSearchGeneration !== currentGeneration)
      return
    danmakuSearchResults.value = result.animes
    danmakuSearchHasMore.value = result.hasMore
    if (!result.animes.length)
      danmakuSearchError.value = '没有找到匹配的作品或剧集，请尝试更换关键词。'
  }
  catch (reason) {
    if (danmakuSearchGeneration === currentGeneration)
      danmakuSearchError.value = toSafeErrorMessage(reason, '弹幕搜索失败。')
  }
  finally {
    if (danmakuSearchGeneration === currentGeneration)
      danmakuSearchLoading.value = false
  }
}

async function chooseDanmakuSearchEpisode(anime: DanmakuSearchAnime, episode: DanmakuSearchEpisode) {
  const currentGeneration = ++danmakuSearchGeneration
  danmakuSearchSelectingEpisodeId.value = episode.episodeId
  danmakuSearchError.value = null
  try {
    await selectDanmakuSearchEpisode(anime.animeId, anime.animeTitle, episode)
    if (danmakuSearchGeneration !== currentGeneration)
      return
    if (danmakuError.value) {
      danmakuSearchError.value = danmakuError.value
    }
    else {
      danmakuSearchOpen.value = false
      danmakuSearchResults.value = []
      showTransientPlayerMessage(`已加载弹幕：${anime.animeTitle} · ${episode.episodeTitle || episode.episodeId}`)
    }
  }
  finally {
    if (danmakuSearchGeneration === currentGeneration)
      danmakuSearchSelectingEpisodeId.value = null
  }
}

function resetDanmakuUiForMediaChange() {
  danmakuSearchGeneration++
  danmakuSearchOpen.value = false
  danmakuSearchResults.value = []
  danmakuSearchHasMore.value = false
  danmakuSearchLoading.value = false
  danmakuSearchSelectingEpisodeId.value = null
  danmakuSearchError.value = null
  resetDanmakuForMediaChange()
}

async function refreshLocalSubtitleProviderSummary() {
  const context = currentSubtitleSearchContext()
  subtitleSearchProviderSummary.value = await describeLocalSubtitleSearchProviders(context)
}

async function loadLocalSubtitleFile() {
  if (localSubtitleImporting.value)
    return

  localSubtitleImporting.value = true
  try {
    const selected = await open({
      multiple: false,
      directory: false,
      title: '载入本地字幕',
      filters: [{ name: '字幕文件', extensions: ['srt', 'ass', 'ssa', 'vtt', 'sub'] }],
    })
    if (typeof selected !== 'string' || !selected.trim())
      return

    cancelPendingTrackPreferenceRestore()
    const cacheOwner = currentMediaPreferenceIdentity() ?? undefined
    const imported = await importLocalSubtitle(selected, cacheOwner)
    const title = subtitleFileNameFromPath(selected) || '本地字幕'
    await addExternalSubtitle(imported.path, title, undefined, 'downloaded')
    activeCachedSubtitlePath = imported.path
    persistedSubtitlePreference = currentSubtitlePreference()
    await saveMediaPreferenceNow(undefined, true)
    showTransientPlayerMessage(`已载入本地字幕：${title}`)
    scheduleChromeHide()
  }
  catch (error) {
    showTransientPlayerMessage(toSafeErrorMessage(error, '本地字幕载入失败。'))
  }
  finally {
    localSubtitleImporting.value = false
  }
}

function closeSubtitleSearch() {
  if (subtitleSearchLoading.value || subtitleDownloadingId.value)
    return
  subtitleSearchOpen.value = false
  subtitleSearchError.value = null
  subtitleSearchResults.value = []
  scheduleChromeHide()
}

function selectSubtitleSearchOrigin(origin: SubtitleSearchOrigin) {
  subtitleSearchOrigin.value = origin
  subtitleSearchResults.value = []
  subtitleSearchError.value = null
  subtitleSearchProviderSummary.value = origin === 'emby' ? '本次查询：当前 Emby 服务器字幕提供器' : ''
  if (origin === 'local')
    void refreshLocalSubtitleProviderSummary()
}

function resetSubtitleSearchOrigin() {
  if (!subtitleSearchRequiresSourceChoice.value)
    return
  subtitleSearchOrigin.value = null
  subtitleSearchResults.value = []
  subtitleSearchError.value = null
}

async function searchSubtitles(language: SubtitleLanguage, keyword: string, keywordMode: SubtitleKeywordMode) {
  const origin = subtitleSearchOrigin.value
  if (!origin)
    return

  subtitleSearchLoading.value = true
  subtitleSearchError.value = null
  subtitleSearchResults.value = []
  try {
    const context = currentSubtitleSearchContext(keyword, keywordMode)
    if (origin === 'emby') {
      store.loadConfigs()
      await store.syncManager()
      const source = store.getSource(currentDisplaySourceId())
      if (!source?.searchSubtitles)
        throw new Error('当前 Emby 数据源不支持字幕搜索。')
      subtitleSearchResults.value = await source.searchSubtitles({ ...context, language })
    }
    else {
      subtitleSearchProviderSummary.value = await describeLocalSubtitleSearchProviders(context)
      subtitleSearchResults.value = await searchLocalSubtitles({ ...context, language })
    }

    if (subtitleSearchResults.value.length === 0)
      subtitleSearchError.value = `${subtitleSearchProviderSummary.value || '已启用的字幕提供器'}没有返回符合当前媒体和语言的字幕，可以切换语言或关键词后重试。`
  }
  catch (error) {
    subtitleSearchError.value = toSafeErrorMessage(error, '字幕搜索失败。')
  }
  finally {
    subtitleSearchLoading.value = false
  }
}

async function downloadAndLoadSubtitle(result: SubtitleSearchResult) {
  cancelPendingTrackPreferenceRestore()
  subtitleDownloadingId.value = result.id
  subtitleSearchError.value = null
  try {
    if (result.origin === 'emby') {
      store.loadConfigs()
      await store.syncManager()
      const source = store.getSource(currentDisplaySourceId())
      if (!source?.downloadSubtitle)
        throw new Error('当前 Emby 数据源不支持字幕下载。')
      const track = await source.downloadSubtitle({
        itemId: currentSubtitleSearchContext().itemId,
        mediaSourceId: currentMediaSourceId(),
        result,
      })
      if (!track.url)
        throw new Error('Emby 已下载字幕，但没有返回可加载的字幕地址。')
      await addExternalSubtitle(track.url, track.title ?? result.title, track.language, 'provider')
    }
    else {
      const cacheOwner = currentMediaPreferenceIdentity() ?? undefined
      const downloaded = await downloadLocalSubtitle(result, cacheOwner)
      await addExternalSubtitle(downloaded.path, downloaded.title, downloaded.language, 'downloaded')
      activeCachedSubtitlePath = downloaded.path
    }

    persistedSubtitlePreference = currentSubtitlePreference()
    await saveMediaPreferenceNow(undefined, true)

    showTransientPlayerMessage(`已加载字幕：${result.title}`)
    subtitleSearchOpen.value = false
    subtitleSearchResults.value = []
    scheduleChromeHide()
  }
  catch (error) {
    subtitleSearchError.value = toSafeErrorMessage(error, '字幕下载或加载失败。')
  }
  finally {
    subtitleDownloadingId.value = null
  }
}

function currentSubtitleSearchContext(keyword?: string, keywordMode: SubtitleKeywordMode = 'mediaTitle'): SubtitleSearchMediaContext {
  const playbackContext = currentPlaybackContext()
  const detail = playbackContext?.detail
  const queueItem = currentQueueItem.value
  return {
    itemId: activeItemId.value || playbackContext?.itemId || queueItem?.id || '',
    mediaSourceId: currentMediaSourceId(),
    title: keyword?.trim() || currentSubtitleMediaTitle(),
    localFilePath: currentLocalSubtitleFilePath(),
    remoteMediaUrl: currentRemoteSubtitleMediaUrl(),
    remoteMediaHeaders: currentRemoteSubtitleMediaUrl() ? { ...mediaHeaders.value } : undefined,
    mediaFileName: currentSubtitleFileName(),
    originalTitle: keywordMode === 'custom' ? undefined : detail?.originalTitle ?? queueItem?.originalTitle,
    seriesName: keywordMode === 'custom' ? undefined : detail?.seriesName ?? queueItem?.seriesName,
    duration: keywordMode === 'custom'
      ? undefined
      : detail?.duration ?? queueItem?.duration ?? (duration.value > 0 ? duration.value : undefined),
    keywordMode,
    year: keywordMode === 'custom' ? undefined : detail?.year,
    mediaType: keywordMode === 'custom' ? undefined : activeMediaType.value ?? detail?.type ?? queueItem?.type,
    seasonNumber: keywordMode === 'custom' ? undefined : detail?.seasonNumber ?? queueItem?.seasonNumber,
    episodeNumber: keywordMode === 'custom' ? undefined : detail?.episodeNumber ?? queueItem?.episodeNumber,
    imdbId: keywordMode === 'custom' ? undefined : detail?.imdbId,
    tmdbId: keywordMode === 'custom' ? undefined : detail?.tmdbId,
  }
}

function currentLocalSubtitleFilePath(): string | undefined {
  const locator = currentPlaybackContext()?.locator
  if (locator?.kind === 'localPath' && isAbsoluteLocalMediaPath(locator.path))
    return locator.path.trim()
  const value = mediaPath.value.trim()
  return isAbsoluteLocalMediaPath(value) ? value : undefined
}

function isAbsoluteLocalMediaPath(value: string): boolean {
  return Boolean(value)
    && !/^[a-z][a-z0-9+.-]*:\/\//i.test(value)
    && (/^[a-z]:[\\/]/i.test(value) || value.startsWith('\\\\') || value.startsWith('/'))
}

function currentRemoteSubtitleMediaUrl(): string | undefined {
  const value = mediaPath.value.trim()
  return /^https?:\/\//i.test(value) ? value : undefined
}

function currentSubtitleMediaTitle(): string {
  const playbackContext = currentPlaybackContext()
  const queueItem = currentQueueItem.value
  const value = playbackContext?.detail?.name
    || queueItem?.title
    || queueItem?.name
    || mediaTitle.value
    || '未命名影片'
  return value.replace(/\.[a-z0-9]{2,5}$/i, '').trim() || value
}

function currentSubtitleFileName(): string {
  const playbackContext = currentPlaybackContext()
  const candidates = [
    currentLocalSubtitleFilePath(),
    currentQueueItem.value?.path,
    playbackContext?.detail?.path,
    mediaPath.value,
  ]
  for (const candidate of candidates) {
    const fileName = subtitleFileNameFromPath(candidate)
    if (fileName)
      return fileName
  }
  return ''
}

function subtitleFileNameFromPath(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed)
    return ''
  try {
    if (/^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)) {
      const pathname = new URL(trimmed).pathname
      return decodeURIComponent(pathname.split('/').filter(Boolean).at(-1) ?? '')
    }
  }
  catch {
    return ''
  }
  return trimmed.replace(/\\/g, '/').split('/').filter(Boolean).at(-1) ?? ''
}

function showTransientPlayerMessage(message: string) {
  clearResumeMessageTimer()
  resumeMessage.value = message
  resumeMessageTimer = window.setTimeout(() => {
    resumeMessageTimer = undefined
    resumeMessage.value = null
  }, 3500)
}

async function resizeWindowForAspect(mode: VideoAspectMode) {
  const ratio = aspectRatioValue(mode)
  if (!ratio || !appWindow)
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

function isTauriRuntime(): boolean {
  const root = globalThis as {
    readonly __TAURI_INTERNALS__?: unknown
    readonly window?: { readonly __TAURI_INTERNALS__?: unknown }
  }
  return root.__TAURI_INTERNALS__ != null || root.window?.__TAURI_INTERNALS__ != null
}

watch(
  () => [route.query.sourceId, route.query.itemId, route.query.contextId, route.query.mediaSourceId],
  async () => {
    resetDanmakuUiForMediaChange()
    await saveCurrentProgress(true, 'stopped')
    await saveMediaPreferenceNow()
    cancelPendingTrackPreferenceRestore()
    resetPersistedTrackPreferences()
    clearMediaPreferenceSaveTimer()
    activeCachedSubtitlePath = null
    await releaseHeldArrow(false)
    resetHistorySaveState()
    mediaPath.value = ''
    mediaHeaders.value = {}
    pictureSettingsError.value = null
    queueSwitchError.value = null
    syncPlaybackQueueFromRoute()
    syncActiveMediaMetadataFromRoute()
    closePlaybackContextMenu(false)
    closePlaybackDetailPanel(false)
    revealChrome()

    const context = currentPlaybackContext()
    const hasPlaybackTarget = Boolean(
      (queryStringValue(route.query.sourceId) && queryStringValue(route.query.itemId))
      || context?.locator,
    )

    if (hasPlaybackTarget) {
      await syncKnownSubtitleTracks()
      await ensureRenderInitialized()
      if (playbackCleanupStarted)
        return
      try {
        const request = await resolvePlaybackLoadRequest()
        mediaPath.value = request.url
        mediaHeaders.value = { ...(request.headers ?? {}) }
        await load(request.url, { headers: request.headers, title: mediaTitle.value })
        await restoreMediaPlaybackPreference()
        startHistorySaveTimer()
        await resumeSavedProgressIfAvailable()
        syncProviderPlaybackStarted()
        if (playbackCleanupStarted)
          await stopPlaybackSilently()
      }
      catch (error) {
        mediaPath.value = ''
        mediaHeaders.value = {}
        queueSwitchError.value = toSafeErrorMessage(error, '无法解析播放地址。')
      }
    }
    else {
      setKnownSubtitleTracks([])
    }
  },
  { immediate: true },
)

watch(isPlaying, (playing) => {
  if (playbackCleanupStarted)
    return

  revealChrome()
  if (playing) {
    startHistorySaveTimer()
    void saveCurrentProgress(false, 'resumed')
  }
  else {
    void saveCurrentProgress(true, 'paused')
  }
})

watch(danmakuLoading, (loading) => {
  if (loading) {
    chromeManuallyHidden.value = false
    chromeVisible.value = true
    clearHideTimer()
    return
  }
  scheduleChromeHide()
})

watch([shouldShowChrome, hasMedia], () => {
  void updateChromeOcclusion()
})

watch([audioTracks, subtitleTracks, videoReady], () => {
  void restorePendingTrackPreference()
})

watch([videoReady, duration], () => {
  void applyPendingResumeSeekWhenReady()
})

watch([videoReady, duration, mediaTitle, currentQueueItem], ([ready, mediaDuration]) => {
  if (danmakuLoadTimer)
    window.clearTimeout(danmakuLoadTimer)
  if (!ready || mediaDuration <= 0 || !hasMedia.value)
    return
  danmakuLoadTimer = window.setTimeout(() => {
    void loadDanmakuForMedia(currentDanmakuMediaIdentity(), mediaDuration)
  }, 250)
})

watch(currentTime, (time) => {
  const pending = pendingResumeSeek
  if (pending && (!isNativeAndroidPlayer || videoReady.value) && pending.path === mediaPath.value && Math.abs(time - pending.position) <= 5)
    completePendingResumeSeek(pending.path, pending.position)
})

async function handleFileDrop(path: string) {
  resetDanmakuUiForMediaChange()
  await saveCurrentProgress(true, 'stopped')
  await saveMediaPreferenceNow()
  cancelPendingTrackPreferenceRestore()
  resetPersistedTrackPreferences()
  clearMediaPreferenceSaveTimer()
  activeCachedSubtitlePath = null
  await releaseHeldArrow(false)
  resetHistorySaveState()
  mediaPath.value = path
  mediaHeaders.value = {}
  mediaTitle.value = path.split(/[\\/]/).pop() || '本地视频'
  playbackQueue.value = null
  playbackContextId.value = ''
  activeSourceId.value = LOCAL_FILE_SOURCE_ID
  activeItemId.value = ''
  activeLibraryId.value = ''
  activeMediaType.value = 'file'
  activePosterUrl.value = ''
  activeBackdropUrl.value = ''
  activeTitleLogoUrl.value = ''
  pictureSettingsError.value = null
  queueSwitchError.value = null
  setKnownSubtitleTracks([])
  revealChrome()
  await ensureRenderInitialized()
  if (playbackCleanupStarted)
    return
  await load(path, { title: mediaTitle.value })
  await restoreMediaPlaybackPreference()
  startHistorySaveTimer()
  await resumeSavedProgressIfAvailable()
  if (playbackCleanupStarted)
    await stopPlaybackSilently()
}

async function playQueueItemAt(index: number) {
  const queue = playbackQueue.value
  if (!queue || index < 0 || index >= queue.items.length || isQueueSwitching.value)
    return
  if (index === queue.currentIndex)
    return

  const target = queue.items[index]
  await saveMediaPreferenceNow()
  isQueueSwitching.value = true
  queueSwitchError.value = null
  revealChrome()
  try {
    store.loadConfigs()
    await store.syncManager()
    const source = store.getSource(target.sourceId)
    if (!source)
      throw new Error('数据源不可用，请检查设置或重新登录。')

    if (playbackCleanupStarted)
      return

    playbackQueue.value = {
      items: queue.items.map(item => ({ ...item })),
      currentIndex: index,
    }
    await router.replace({
      name: 'player',
      query: createPlaybackRouteQuery({
        sourceId: target.sourceId,
        itemId: target.id,
        contextId: playbackContextId.value || undefined,
      }),
    })
  }
  catch (error) {
    queueSwitchError.value = toSafeErrorMessage(error, '无法切换到队列媒体。')
  }
  finally {
    isQueueSwitching.value = false
    revealChrome()
  }
}

function handlePlayPrevious() {
  const queue = playbackQueue.value
  if (!queue)
    return
  void playQueueItemAt(queue.currentIndex - 1)
}

function handlePlayNext() {
  const queue = playbackQueue.value
  if (!queue)
    return
  void playQueueItemAt(queue.currentIndex + 1)
}

async function handleTogglePause() {
  const willPause = isPlaying.value
  await togglePause()
  if (willPause)
    await saveCurrentProgress(true, 'paused')
}

async function handleSetPlaybackSpeed(speed: number) {
  await setPlaybackSpeed(speed)
  await saveMediaPreferenceNow(undefined, true)
}

async function handleSetSubtitleDelay(delay: number) {
  await setSubtitleDelay(delay)
  scheduleMediaPreferenceSave()
}

async function handleSetSubtitle(trackId: Parameters<typeof setSubtitle>[0]) {
  cancelPendingTrackPreferenceRestore()
  const selected = subtitleTracks.value.find(track => track.id === trackId)
  activeCachedSubtitlePath = selected?.source === 'downloaded' && selected.url ? selected.url : null
  await setSubtitle(trackId)
  persistedSubtitlePreference = currentSubtitlePreference()
  await saveMediaPreferenceNow(undefined, true)
}

async function handleSetAudio(trackId: number) {
  cancelPendingTrackPreferenceRestore()
  await setAudio(trackId)
  persistedAudioPreference = currentAudioPreference()
  await saveMediaPreferenceNow(undefined, true)
}

function nextPlaybackSpeed(): number {
  const currentIndex = PLAYBACK_SPEED_OPTIONS.findIndex(speed => Math.abs(speed - playbackSpeed.value) < 0.001)
  return PLAYBACK_SPEED_OPTIONS[(currentIndex + 1) % PLAYBACK_SPEED_OPTIONS.length]
}

async function cycleSubtitleFromKeyboard() {
  const selectable = subtitleTracks.value.filter(track => track.selectable)
  const choices: Array<Parameters<typeof handleSetSubtitle>[0]> = [null, ...selectable.map(track => track.id)]
  const currentIndex = choices.findIndex(choice => choice === currentSubtitle.value)
  const next = choices[(currentIndex + 1) % choices.length]
  await handleSetSubtitle(next)
  showKeyboardOsd(`字幕 · ${selectedSubtitleTrackLabel()}`)
}

async function cycleAudioFromKeyboard() {
  if (audioTracks.value.length === 0) {
    showKeyboardOsd('音轨 · 暂未检测到可用音轨')
    return
  }
  const currentIndex = audioTracks.value.findIndex(track => track.id === currentAudio.value)
  const next = audioTracks.value[(currentIndex + 1) % audioTracks.value.length]
  await handleSetAudio(next.id)
  showKeyboardOsd(`音轨 · ${selectedAudioTrackLabel()}`)
}

async function toggleMuteFromKeyboard() {
  if (volume.value > 0) {
    keyboardPreviousVolume = volume.value
    await setVolume(0)
    showKeyboardOsd('音量 · 静音')
    return
  }
  const restoredVolume = Math.max(1, Math.min(100, keyboardPreviousVolume || 50))
  await setVolume(restoredVolume)
  showKeyboardOsd(`音量 · ${Math.round(restoredVolume)}%`)
}

function showQueueKeyboardOsd() {
  const queue = playbackQueue.value
  if (!queue || queue.items.length <= 1) {
    showKeyboardOsd('播放队列 · 当前仅有一个项目')
    return
  }
  const item = queue.items[queue.currentIndex]
  showKeyboardOsd(`播放队列 ${queue.currentIndex + 1}/${queue.items.length} · ${safeMenuText(item?.title || item?.name, '当前项目', 48)}`)
}

async function executePlayerShortcutFromKeyboard(target: PlayerShortcutTarget) {
  if (target === 'hideControls') {
    hideChromeFromKeyboard()
    showKeyboardOsd('控制界面已隐藏 · 移动鼠标可恢复')
    return
  }

  if (!shouldShowChrome.value) {
    chromeManuallyHidden.value = true
    chromeVisible.value = false
  }

  try {
    await runKeyboardAction(async () => {
      switch (target) {
        case 'playPrevious': {
          const queue = playbackQueue.value
          if (!queue || !canPlayPrevious.value) {
            showKeyboardOsd('没有上一集')
            return
          }
          const item = queue.items[queue.currentIndex - 1]
          await playQueueItemAt(queue.currentIndex - 1)
          showKeyboardOsd(`上一集 · ${safeMenuText(item?.title || item?.name, '上一集', 48)}`)
          return
        }
        case 'seekBackward':
          await seekRelative(-10)
          showKeyboardOsd('后退 10 秒')
          return
        case 'togglePause':
          await handleTogglePause()
          showKeyboardOsd(isPlaying.value ? '继续播放' : '暂停')
          return
        case 'seekForward':
          await seekRelative(10)
          showKeyboardOsd('前进 10 秒')
          return
        case 'playNext': {
          const queue = playbackQueue.value
          if (!queue || !canPlayNext.value) {
            showKeyboardOsd('没有下一集')
            return
          }
          const item = queue.items[queue.currentIndex + 1]
          await playQueueItemAt(queue.currentIndex + 1)
          showKeyboardOsd(`下一集 · ${safeMenuText(item?.title || item?.name, '下一集', 48)}`)
          return
        }
        case 'toggleMute':
          await toggleMuteFromKeyboard()
          return
        case 'toggleSpeedMenu': {
          const speed = nextPlaybackSpeed()
          await handleSetPlaybackSpeed(speed)
          showKeyboardOsd(`播放速度 · ${Number.isInteger(speed) ? speed.toFixed(1) : speed}x`)
          return
        }
        case 'toggleSubtitleMenu':
          await cycleSubtitleFromKeyboard()
          return
        case 'toggleDanmaku':
          await toggleDanmaku()
          showKeyboardOsd(danmakuSettings.value.enabled ? '弹幕已开启' : '弹幕已关闭')
          return
        case 'toggleDanmakuSettings':
          playerControlsRef.value?.openDanmakuSettingsFromShortcut()
          showKeyboardOsd('弹幕设置')
          return
        case 'toggleAudioMenu':
          await cycleAudioFromKeyboard()
          return
        case 'toggleQueueMenu':
          showQueueKeyboardOsd()
          return
        case 'toggleSettings':
          showKeyboardOsd(`画面 · ${videoAspectLabel(videoAspectMode.value)} · ${videoFitLabel(videoFitMode.value)}`)
          return
        case 'toggleFullscreen':
          await playerControlsRef.value?.toggleFullscreenFromShortcut()
          showKeyboardOsd(isPlayerFullscreen.value ? '进入全屏' : '退出全屏')
      }
    })
  }
  catch (error) {
    showKeyboardOsd(toSafeErrorMessage(error, '快捷键操作失败'))
  }
}

function clampContextMenuPosition(clientX: number, clientY: number): ContextMenuPosition {
  const maxX = Math.max(CONTEXT_MENU_MARGIN, window.innerWidth - CONTEXT_MENU_WIDTH - CONTEXT_MENU_MARGIN)
  const maxY = Math.max(CONTEXT_MENU_MARGIN, window.innerHeight - CONTEXT_MENU_MAX_HEIGHT - CONTEXT_MENU_MARGIN)
  return {
    x: Math.min(Math.max(clientX, CONTEXT_MENU_MARGIN), maxX),
    y: Math.min(Math.max(clientY, CONTEXT_MENU_MARGIN), maxY),
  }
}

function openPlaybackContextMenu(event: MouseEvent) {
  event.preventDefault()
  event.stopPropagation()
  const pointerType = typeof PointerEvent !== 'undefined' && event instanceof PointerEvent ? event.pointerType : 'mouse'
  const isRealMouseContextMenu = pointerType === 'mouse' && event.button === 2
  if (!isRealMouseContextMenu || touchGestureSession !== null || Date.now() < suppressPlayerContextMenuUntil || isNativeAndroidPlayer)
    return
  contextMenuPosition.value = clampContextMenuPosition(event.clientX, event.clientY)
  contextMenuOpen.value = true
  chromeVisible.value = true
  clearHideTimer()
}

function closePlaybackContextMenu(scheduleHide = true) {
  if (!contextMenuOpen.value)
    return

  contextMenuOpen.value = false
  if (scheduleHide)
    scheduleChromeHide()
}

function openPlaybackDetailFromContextMenu() {
  playbackDetailOpen.value = true
  closePlaybackContextMenu(false)
  chromeVisible.value = true
  clearHideTimer()
}

function closePlaybackDetailPanel(scheduleHide = true) {
  if (!playbackDetailOpen.value)
    return

  playbackDetailOpen.value = false
  if (scheduleHide)
    scheduleChromeHide()
}

async function togglePlaybackFromContextMenu() {
  await handleTogglePause()
  closePlaybackContextMenu()
}

async function navigateFromContextMenu(name: 'home' | 'settings') {
  closePlaybackContextMenu(false)
  closePlaybackDetailPanel(false)
  await router.push({ name })
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
  await saveMediaPreferenceNow(undefined, true)
}

async function handleSetVideoFit(mode: VideoFitMode) {
  await setVideoFit(mode)
  scheduleRenderBoundsSync()
  await saveMediaPreferenceNow(undefined, true)
}

async function handleSetVideoBrightness(level: number) {
  await setVideoBrightness(level)
  await saveMediaPreferenceNow(undefined, true)
}

async function handleSetOrientationMode(mode: MpvOrientationMode) {
  await setOrientationMode(mode)
  const label = mode === 'landscape' ? '锁定横屏' : mode === 'portrait' ? '锁定竖屏' : '自动横屏'
  showKeyboardOsd(`屏幕方向 · ${label}`)
}

async function stopPlaybackSilently() {
  try {
    await stop()
  }
  catch {
    // Route cleanup must never expose native/player details or block navigation.
  }
}

function stopPlaybackForRouteExit(): Promise<void> {
  if (playbackStopPromise)
    return playbackStopPromise

  playbackCleanupStarted = true
  playbackStopPromise = (async () => {
    await saveMediaPreferenceNow()
    const progressSave = saveCurrentProgress(true, 'stopped')
    clearHistorySaveTimer()
    await stopPlaybackSilently()
    try {
      await progressSave
    }
    catch {
      // A local or provider progress failure must not keep the native video surface alive or block routing.
    }
  })()
  return playbackStopPromise
}

function handleBeforeUnload() {
  void saveMediaPreferenceNow()
  void saveCurrentProgress(true, 'stopped')
}

function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && danmakuSearchOpen.value) {
    event.preventDefault()
    closeDanmakuSearch()
    return
  }
  if (event.key === 'Escape' && subtitleSearchOpen.value) {
    event.preventDefault()
    closeSubtitleSearch()
    return
  }
  if (event.key === 'Escape' && (contextMenuOpen.value || playbackDetailOpen.value)) {
    event.preventDefault()
    closePlaybackContextMenu(false)
    closePlaybackDetailPanel()
    return
  }

  // Ctrl+Shift+D (or Cmd+Shift+D) surfaces the diagnostics panel from the WebView overlay.
  if ((event.ctrlKey || event.metaKey) && event.shiftKey && (event.key === 'D' || event.key === 'd')) {
    event.preventDefault()
    diagnosticsOpen.value = !diagnosticsOpen.value
    return
  }

  if (!hasMedia.value || shouldIgnorePlaybackShortcut(event))
    return
  const hasModifier = event.ctrlKey || event.metaKey || event.altKey || event.shiftKey
  if (!hasModifier && event.code === 'Space') {
    if (event.target instanceof Element && event.target.closest('button'))
      return
    event.preventDefault()
    if (!event.repeat)
      void executePlayerShortcutFromKeyboard('togglePause')
    return
  }
  if (!hasModifier && (event.code === 'ArrowLeft' || event.code === 'ArrowRight')) {
    event.preventDefault()
    if (!event.repeat)
      beginHeldArrow(event.code, 'keyboard')
    return
  }
  if (!hasModifier && (event.code === 'ArrowUp' || event.code === 'ArrowDown')) {
    event.preventDefault()
    const delta = event.code === 'ArrowUp' ? 5 : -5
    const target = adjustVolumeFromKeyboard(delta)
    showKeyboardOsd(target === 0 ? '音量 · 静音' : `音量 · ${Math.round(target)}%`)
    return
  }

  const shortcutTarget = playerShortcutTargetForEvent(event, playerShortcuts.value)
  if (!shortcutTarget || event.repeat)
    return
  event.preventDefault()
  void executePlayerShortcutFromKeyboard(shortcutTarget)
}

function handleGlobalKeyup(event: KeyboardEvent) {
  if (heldArrowOwner !== 'keyboard' || event.code !== heldArrowKey)
    return
  event.preventDefault()
  void releaseHeldArrow(true, 'keyboard')
}

function shouldIgnorePlaybackShortcut(event: KeyboardEvent): boolean {
  if (event.defaultPrevented || event.isComposing)
    return true
  const target = event.target
  if (!(target instanceof HTMLElement))
    return false
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
}

function beginHeldArrow(key: 'ArrowLeft' | 'ArrowRight', owner: 'keyboard' | 'touch'): boolean {
  if (heldArrowKey)
    return false
  heldArrowKey = key
  heldArrowOwner = owner
  arrowHoldActivated = false
  arrowBasePlaybackSpeed = playbackSpeed.value
  heldArrowTimer = window.setTimeout(() => {
    heldArrowTimer = undefined
    arrowHoldActivated = true
    if (key === 'ArrowRight') {
      const configuredSpeed = loadPlayerInteractionSettings().longPressPlaybackSpeed
      void applyPlaybackSpeed(configuredSpeed)
      showKeyboardOsd(`长按快进 · ${configuredSpeed}x`)
      return
    }
    showKeyboardOsd('连续后退')
    void seekRelative(-ARROW_TAP_SEEK_SECONDS)
    rewindHoldTimer = window.setInterval(() => {
      void seekRelative(-ARROW_TAP_SEEK_SECONDS)
    }, REWIND_HOLD_INTERVAL)
  }, ARROW_HOLD_DELAY)
  return true
}

async function releaseHeldArrow(triggerTap: boolean, owner?: 'keyboard' | 'touch') {
  if (owner && heldArrowOwner !== owner)
    return
  const key = heldArrowKey
  const wasHold = arrowHoldActivated
  if (heldArrowTimer)
    window.clearTimeout(heldArrowTimer)
  if (rewindHoldTimer)
    window.clearInterval(rewindHoldTimer)
  heldArrowTimer = undefined
  rewindHoldTimer = undefined
  heldArrowKey = null
  heldArrowOwner = null
  arrowHoldActivated = false
  if (!key)
    return
  if (key === 'ArrowRight' && wasHold) {
    await applyPlaybackSpeed(arrowBasePlaybackSpeed)
    showKeyboardOsd(`播放速度 · ${Number.isInteger(arrowBasePlaybackSpeed) ? arrowBasePlaybackSpeed.toFixed(1) : arrowBasePlaybackSpeed}x`)
    return
  }
  if (triggerTap && !wasHold) {
    await seekRelative(key === 'ArrowLeft' ? -ARROW_TAP_SEEK_SECONDS : ARROW_TAP_SEEK_SECONDS)
    showKeyboardOsd(key === 'ArrowLeft' ? `后退 ${ARROW_TAP_SEEK_SECONDS} 秒` : `前进 ${ARROW_TAP_SEEK_SECONDS} 秒`)
  }
}

function handlePlayerAreaClick(event: MouseEvent) {
  if (Date.now() < suppressPlayerClickUntil)
    return
  if (!hasMedia.value || event.button !== 0 || contextMenuOpen.value || playbackDetailOpen.value || subtitleSearchOpen.value || danmakuSearchOpen.value)
    return
  const target = event.target
  if (target instanceof Element && target.closest('button, input, select, textarea, a, [role="dialog"], [role="menu"], [data-player-click-ignore]'))
    return
  if (isNativeAndroidPlayer) {
    toggleChromeFromTouch()
    return
  }
  void handleTogglePause()
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

onBeforeRouteLeave(async () => {
  await stopPlaybackForRouteExit()
})

onMounted(() => {
  document.documentElement.classList.add('player-render-surface-active')
  document.body.classList.add('player-render-surface-active')
  syncTransparentRootClass(isTransparentRootActive.value)
  playerChromeStore.setVisible(shouldShowChrome.value)
  window.addEventListener('blur', handleWindowBlur)
  window.addEventListener('focus', handleWindowFocus)
  window.addEventListener('resize', handleWindowResize)
  window.addEventListener('beforeunload', handleBeforeUnload)
  window.addEventListener('keydown', handleGlobalKeydown)
  window.addEventListener('keyup', handleGlobalKeyup)
  window.addEventListener(PLAYER_SHORTCUTS_CHANGED_EVENT, reloadPlayerShortcuts)
  document.documentElement.addEventListener('mouseleave', handleApplicationPointerLeave)
  document.addEventListener('visibilitychange', handleVisibilityChange)
  if (appWindow) {
    void appWindow.onFocusChanged(({ payload: focused }) => {
      if (focused)
        handleWindowFocus()
      else
        handleWindowBlur()
    }).then((unlisten) => {
      if (playerViewDisposed)
        unlisten()
      else
        nativeWindowFocusUnlisten = unlisten
    }).catch(() => undefined)
  }
  void updateChromeOcclusion()
  void ensureRenderInitialized()
  scheduleChromeHide()
})

onBeforeUnmount(() => {
  if (danmakuLoadTimer)
    window.clearTimeout(danmakuLoadTimer)
  playerViewDisposed = true
  nativeWindowFocusUnlisten?.()
  nativeWindowFocusUnlisten = undefined
  void stopPlaybackForRouteExit()
  document.documentElement.classList.remove('player-render-surface-active')
  document.body.classList.remove('player-render-surface-active')
  document.documentElement.classList.remove('player-render-surface-transparent')
  document.body.classList.remove('player-render-surface-transparent')
  playerChromeStore.setVisible(true)
  clearHideTimer()
  clearMediaPreferenceSaveTimer()
  void releaseHeldArrow(false)
  clearResumeSeekTimers()
  clearResumeMessageTimer()
  if (keyboardOsdTimer)
    window.clearTimeout(keyboardOsdTimer)
  keyboardOsdTimer = undefined
  if (touchSingleTapTimer)
    window.clearTimeout(touchSingleTapTimer)
  touchSingleTapTimer = undefined
  if (touchFeedbackTimer)
    window.clearTimeout(touchFeedbackTimer)
  touchFeedbackTimer = undefined
  touchGestureSession = null
  lastTouchTap = null
  pendingTouchLevelUpdate = null
  window.removeEventListener('blur', handleWindowBlur)
  window.removeEventListener('focus', handleWindowFocus)
  window.removeEventListener('resize', handleWindowResize)
  window.removeEventListener('beforeunload', handleBeforeUnload)
  window.removeEventListener('keydown', handleGlobalKeydown)
  window.removeEventListener('keyup', handleGlobalKeyup)
  window.removeEventListener(PLAYER_SHORTCUTS_CHANGED_EVENT, reloadPlayerShortcuts)
  document.documentElement.removeEventListener('mouseleave', handleApplicationPointerLeave)
  document.removeEventListener('visibilitychange', handleVisibilityChange)
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
    playerChromeStore.setVisible(visible)
  },
  { immediate: true },
)
</script>

<template>
  <div
    class="player-view theme-adaptive relative h-screen w-full overflow-hidden"
    :class="[
      { 'cursor-none': !shouldShowChrome },
      { 'player-view--native-mobile': isNativeAndroidPlayer },
      isTransparentRootActive ? 'player-view--transparent' : 'bg-black',
    ]"
    @mousemove="revealChromeFromPointer"
    @mouseleave="scheduleChromeHide"
    @pointerdown="handlePlayerTouchPointerDown"
    @pointermove="handlePlayerTouchPointerMove"
    @pointerup="handlePlayerTouchPointerEnd"
    @pointercancel="handlePlayerTouchPointerEnd($event, true)"
    @click="handlePlayerAreaClick"
    @contextmenu="openPlaybackContextMenu"
  >
    <VideoPlayer
      :is-playing="isPlaying"
      :has-media="hasMedia"
      :video-ready="videoReady"
      :backdrop-url="activeBackdropUrl || currentQueueItem?.backdropUrl || ''"
      :render-status="renderStatus"
      :render-error="renderError"
      :render-diagnostics="renderDiagnostics"
      :playback-diagnostics="playbackDiagnostics"
      :render-strategy="renderStrategy"
      :top-occlusion="topOcclusion"
      :bottom-occlusion="bottomOcclusion"
      :diagnostics-open="diagnosticsOpen"
      :provider-sync-diagnostics="providerSyncDiagnostics"
      @file-drop="handleFileDrop"
      @render-bounds="handleRenderBounds"
      @toggle-diagnostics="toggleDiagnosticsPanel"
      @set-strategy="handleSetStrategy"
    />

    <!-- Danmaku is media content, not Player chrome. Teleport it out of the PlayerView subtree so
         no current or future chrome visibility class/transition can become one of its ancestors. -->
    <Teleport to="body">
      <DanmakuOverlay v-if="hasMedia" :comments="danmakuComments" :settings="danmakuSettings" :current-time="currentTime" :is-playing="isPlaying" :playback-speed="playbackSpeed" />
    </Teleport>

    <div
      v-if="hasMedia && isNativeAndroidPlayer"
      class="android-touch-capture pointer-events-auto absolute inset-0 z-[6]"
      aria-hidden="true"
      @pointerdown.stop="handlePlayerTouchPointerDown"
      @pointermove.stop="handlePlayerTouchPointerMove"
      @pointerup.stop="handlePlayerTouchPointerEnd"
      @pointercancel.stop="handlePlayerTouchPointerEnd($event, true)"
      @click.stop="handlePlayerAreaClick"
    />

    <div
      v-if="hasMedia && !isNativeAndroidPlayer"
      class="pointer-events-auto absolute inset-x-0 top-0 z-5 h-24"
      aria-hidden="true"
      @mouseenter="revealChromeFromPointer"
      @mousemove="revealChromeFromPointer"
    />
    <div
      v-if="hasMedia && !isNativeAndroidPlayer"
      class="pointer-events-auto absolute inset-x-0 bottom-0 z-5 h-32"
      aria-hidden="true"
      @mouseenter="revealChromeFromPointer"
      @mousemove="revealChromeFromPointer"
    />

    <Transition name="keyboard-osd">
      <div
        v-if="keyboardOsdMessage"
        class="keyboard-osd pointer-events-none absolute right-6 top-16 z-30 max-w-[min(24rem,calc(100vw-3rem))] rounded-lg border border-white/14 bg-black/72 px-4 py-2.5 text-sm font-semibold text-white/90 shadow-2xl backdrop-blur-xl"
        role="status"
        aria-live="polite"
      >
        {{ keyboardOsdMessage }}
      </div>
    </Transition>

    <Transition name="touch-gesture-osd">
      <div
        v-if="touchGestureFeedback"
        class="touch-gesture-osd pointer-events-none absolute left-1/2 top-1/2 z-30 flex min-w-36 -translate-x-1/2 -translate-y-1/2 flex-col items-center border border-white/14 bg-black/72 px-5 py-4 text-center text-white shadow-2xl backdrop-blur-xl"
        role="status"
        aria-live="polite"
      >
        <span class="touch-gesture-icon" aria-hidden="true">
          <svg v-if="touchGestureFeedback.kind === 'seek'" viewBox="0 0 24 24"><path d="M8 5v14l11-7L8 5Zm-4 2v10" /></svg>
          <svg v-else-if="touchGestureFeedback.kind === 'brightness'" viewBox="0 0 24 24"><path d="M12 3v2m0 14v2M3 12h2m14 0h2M5.6 5.6 7 7m10 10 1.4 1.4m0-12.8L17 7M7 17l-1.4 1.4M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z" /></svg>
          <svg v-else-if="touchGestureFeedback.kind === 'volume'" viewBox="0 0 24 24"><path d="M4 9h4l5-4v14l-5-4H4V9Zm12 1a3 3 0 0 1 0 4m2-7a7 7 0 0 1 0 10" /></svg>
          <svg v-else viewBox="0 0 24 24"><path d="M8 5v14l11-7L8 5Z" /></svg>
        </span>
        <strong>{{ touchGestureFeedback.title }}</strong>
        <span>{{ touchGestureFeedback.value }}</span>
        <span v-if="touchGestureFeedback.percent != null" class="touch-gesture-meter">
          <i :style="{ width: `${touchGestureFeedback.percent}%` }" />
        </span>
      </div>
    </Transition>

    <Transition name="buffering-indicator">
      <BufferingIndicator
        v-if="hasMedia && isBuffering"
        :speed-bytes-per-second="bufferSpeedBytesPerSecond"
      />
    </Transition>

    <Transition name="player-chrome-top">
      <div
        v-if="!isNativeAndroidPlayer"
        v-show="shouldShowChrome"
        ref="topChromeRef"
        class="player-top-chrome pointer-events-none absolute inset-x-0 top-0 z-10 bg-gradient-to-b from-black via-black/82 to-transparent px-6 pb-8 pt-16"
      >
        <div class="max-w-4xl">
          <p class="text-xs uppercase tracking-[0.24em] text-white/38">
            Now Playing
          </p>
          <img
            v-if="currentTitleLogoUrl"
            :src="currentTitleLogoUrl"
            :alt="mediaTitle"
            class="mt-2 max-h-14 max-w-[min(22rem,72vw)] object-contain object-left drop-shadow-lg"
            loading="eager"
            decoding="async"
            @error="markTitleLogoFailed(currentTitleLogoUrl)"
          >
          <h1 :class="currentTitleLogoUrl ? 'mt-2 truncate text-sm font-semibold text-white/72 drop-shadow-lg' : 'mt-2 truncate text-2xl font-bold text-white drop-shadow-lg'">
            {{ mediaTitle }}
          </h1>
          <p v-if="mediaPath" class="mt-2 truncate text-xs text-white/35">
            {{ displayMediaPath }}
          </p>
          <p v-if="queueSwitchError" class="mt-2 text-xs text-red-100/80">
            {{ queueSwitchError }}
          </p>
          <p v-if="diagnosticsOpen && providerSyncError" class="mt-2 text-xs text-amber-100/80">
            {{ providerSyncError }}
          </p>
          <p v-if="resumeMessage" class="mt-2 text-xs text-white/60">
            {{ resumeMessage }}
          </p>
        </div>
      </div>
    </Transition>

    <!-- Bottom chrome: always in DOM when media loaded so occlusion stays valid.
         Controls fade via opacity transition; the gradient div itself never disappears. -->
    <div
      v-if="hasMedia && !isNativeAndroidPlayer"
      ref="bottomChromeRef"
      data-player-click-ignore
      class="player-bottom-chrome absolute inset-x-0 bottom-0 z-20 bg-gradient-to-t from-black via-black/86 to-transparent px-6 pb-6 pt-10 transition-opacity duration-300"
      :class="shouldShowChrome ? 'pointer-events-auto opacity-100' : 'pointer-events-none opacity-0'"
      @mouseenter="revealChromeFromPointer"
      @mousemove="revealChromeFromPointer"
    >
      <PlayerControls
        ref="playerControlsRef"
        :is-playing="isPlaying"
        :current-time="currentTime"
        :duration="duration"
        :volume="volume"
        :playback-speed="playbackSpeed"
        :subtitle-delay="subtitleDelay"
        :subtitle-tracks="subtitleTracks"
        :audio-tracks="audioTracks"
        :queue-item-count="playbackQueueItemCount"
        :queue-items="playbackQueue?.items ?? []"
        :current-queue-index="playbackQueue?.currentIndex ?? 0"
        :is-queue-switching="isQueueSwitching"
        :can-play-previous="canPlayPrevious"
        :can-play-next="canPlayNext"
        :current-subtitle="currentSubtitle"
        :current-audio="currentAudio"
        :video-aspect-mode="videoAspectMode"
        :video-fit-mode="videoFitMode"
        :video-brightness="videoBrightness"
        :track-error="trackError"
        :picture-settings-error="pictureSettingsError"
        :mobile-layout="false"
        :orientation-supported="orientationSupported"
        :orientation-mode="orientationMode"
        :danmaku-settings="danmakuSettings" :danmaku-loading="danmakuLoading" :danmaku-error="danmakuError" :danmaku-comment-count="danmakuComments.length"
        @play-previous="handlePlayPrevious"
        @toggle-pause="handleTogglePause"
        @play-next="handlePlayNext"
        @select-queue-item="playQueueItemAt"
        @seek="seek"
        @seek-relative="seekRelative"
        @set-volume="setVolume"
        @set-playback-speed="handleSetPlaybackSpeed"
        @set-subtitle-delay="handleSetSubtitleDelay"
        @set-subtitle="handleSetSubtitle"
        @load-local-subtitle="loadLocalSubtitleFile"
        @search-subtitles="openSubtitleSearch"
        @set-audio="handleSetAudio"
        @set-video-aspect="handleSetVideoAspect"
        @set-video-fit="handleSetVideoFit"
        @set-video-brightness="handleSetVideoBrightness"
        @set-orientation-mode="handleSetOrientationMode"
        @fullscreen-changed="handleFullscreenChanged"
        @interaction-change="handleControlsInteraction"
        @toggle-danmaku="toggleDanmaku"
        @update-danmaku-settings="updateDanmakuSettings"
        @reload-danmaku="loadDanmakuForMedia(currentDanmakuMediaIdentity(), duration, true)"
        @search-danmaku="openDanmakuSearch"
        @open-playback-detail="openPlaybackDetailFromContextMenu"
        @navigate-home="navigateFromContextMenu('home')"
        @navigate-settings="navigateFromContextMenu('settings')"
      />
    </div>

    <Transition name="player-chrome-top">
      <MobilePlayerControls
        v-if="hasMedia && isNativeAndroidPlayer"
        v-show="shouldShowChrome"
        ref="playerControlsRef"
        :title="mediaTitle"
        :title-logo-url="currentTitleLogoUrl"
        :is-playing="isPlaying"
        :is-buffering="isBuffering"
        :current-time="currentTime"
        :duration="duration"
        :volume="volume"
        :playback-speed="playbackSpeed"
        :subtitle-delay="subtitleDelay"
        :subtitle-tracks="subtitleTracks"
        :audio-tracks="audioTracks"
        :queue-item-count="playbackQueueItemCount"
        :queue-items="playbackQueue?.items ?? []"
        :current-queue-index="playbackQueue?.currentIndex ?? 0"
        :is-queue-switching="isQueueSwitching"
        :can-play-previous="canPlayPrevious"
        :can-play-next="canPlayNext"
        :current-subtitle="currentSubtitle"
        :current-audio="currentAudio"
        :video-aspect-mode="videoAspectMode"
        :video-fit-mode="videoFitMode"
        :video-brightness="videoBrightness"
        :track-error="trackError"
        :picture-settings-error="pictureSettingsError"
        :orientation-supported="orientationSupported"
        :orientation-mode="orientationMode"
        :danmaku-settings="danmakuSettings" :danmaku-loading="danmakuLoading" :danmaku-error="danmakuError" :danmaku-comment-count="danmakuComments.length"
        @back="handlePlayerBack"
        @play-previous="handlePlayPrevious"
        @toggle-pause="handleTogglePause"
        @play-next="handlePlayNext"
        @select-queue-item="playQueueItemAt"
        @seek="seek"
        @seek-relative="seekRelative"
        @set-volume="setVolume"
        @set-playback-speed="handleSetPlaybackSpeed"
        @set-subtitle-delay="handleSetSubtitleDelay"
        @set-subtitle="handleSetSubtitle"
        @load-local-subtitle="loadLocalSubtitleFile"
        @search-subtitles="openSubtitleSearch"
        @set-audio="handleSetAudio"
        @set-video-aspect="handleSetVideoAspect"
        @set-video-fit="handleSetVideoFit"
        @set-video-brightness="handleSetVideoBrightness"
        @set-orientation-mode="handleSetOrientationMode"
        @interaction-change="handleControlsInteraction"
        @toggle-danmaku="toggleDanmaku"
        @update-danmaku-settings="updateDanmakuSettings"
        @reload-danmaku="loadDanmakuForMedia(currentDanmakuMediaIdentity(), duration, true)"
        @search-danmaku="openDanmakuSearch"
      />
    </Transition>

    <Teleport to="body">
      <DanmakuSearchDialog
        :open="danmakuSearchOpen"
        :media-title="currentDanmakuMediaIdentity().searchTitle"
        :file-name="currentDanmakuMediaIdentity().matchName"
        :initial-episode="currentDanmakuMediaIdentity().episode"
        :results="danmakuSearchResults"
        :has-more="danmakuSearchHasMore"
        :loading="danmakuSearchLoading"
        :selecting-episode-id="danmakuSearchSelectingEpisodeId"
        :error="danmakuSearchError"
        :mobile-layout="isNativeAndroidPlayer"
        @close="closeDanmakuSearch"
        @search="runDanmakuSearch"
        @select="chooseDanmakuSearchEpisode"
      />

      <SubtitleSearchDialog
        :open="subtitleSearchOpen"
        :requires-source-choice="subtitleSearchRequiresSourceChoice"
        :origin="subtitleSearchOrigin"
        :default-language="subtitleSearchDefaultLanguage"
        :media-title="currentSubtitleMediaTitle()"
        :file-name="currentSubtitleFileName()"
        :results="subtitleSearchResults"
        :loading="subtitleSearchLoading"
        :downloading-id="subtitleDownloadingId"
        :error="subtitleSearchError"
        :provider-summary="subtitleSearchProviderSummary"
        :mobile-layout="isNativeAndroidPlayer"
        @close="closeSubtitleSearch"
        @select-origin="selectSubtitleSearchOrigin"
        @back="resetSubtitleSearchOrigin"
        @search="searchSubtitles"
        @download="downloadAndLoadSubtitle"
      />

      <div
        v-if="contextMenuOpen"
        class="fixed inset-0 z-[1080]"
        aria-hidden="false"
        @pointerdown="closePlaybackContextMenu()"
        @contextmenu.prevent="openPlaybackContextMenu"
      >
        <div
          class="player-context-menu theme-adaptive pointer-events-auto fixed w-56 max-w-[calc(100vw-1.5rem)] overflow-hidden rounded-2xl border p-1.5 text-sm shadow-2xl backdrop-blur-2xl"
          :style="{ left: `${contextMenuPosition.x}px`, top: `${contextMenuPosition.y}px`, maxHeight: `min(${CONTEXT_MENU_MAX_HEIGHT}px, calc(100vh - 1.5rem))` }"
          role="menu"
          aria-label="播放菜单"
          tabindex="0"
          @pointerdown.stop
          @contextmenu.prevent.stop
          @keydown.esc.prevent.stop="closePlaybackContextMenu()"
        >
          <div class="border-b border-white/10 px-2.5 py-2">
            <p class="line-clamp-1 text-xs font-semibold text-white/78">
              {{ contextMenuTitle }}
            </p>
            <p class="mt-0.5 truncate text-[11px] text-white/42">
              {{ contextMenuSource }}
            </p>
          </div>

          <div class="grid gap-1 py-1" role="group" aria-label="播放操作">
            <button type="button" class="context-menu-action" role="menuitem" @click="openPlaybackDetailFromContextMenu">
              播放详情
            </button>
            <button type="button" class="context-menu-action" role="menuitem" @click="togglePlaybackFromContextMenu">
              {{ isPlaying ? '暂停播放' : '继续播放' }}
            </button>
          </div>

          <div class="border-t border-white/10 pt-1" role="group" aria-label="页面操作">
            <button type="button" class="context-menu-action" role="menuitem" @click="navigateFromContextMenu('home')">
              返回主页
            </button>
            <button type="button" class="context-menu-action" role="menuitem" @click="navigateFromContextMenu('settings')">
              打开设置
            </button>
          </div>
        </div>
      </div>

      <div
        v-if="playbackDetailOpen"
        class="player-detail-panel theme-adaptive pointer-events-auto fixed left-6 top-24 z-[1070] w-[min(28rem,calc(100vw-3rem))] overflow-hidden rounded-3xl border p-4 text-sm shadow-2xl backdrop-blur-2xl"
        role="dialog"
        aria-label="播放详情"
        @pointerdown.stop
        @contextmenu.prevent.stop
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="detail-panel-dot" aria-hidden="true" />
              <p class="text-[10px] font-semibold uppercase tracking-[0.22em] text-white/42">
                Stats for playback
              </p>
            </div>
            <h2 class="mt-2 line-clamp-2 text-base font-bold leading-6 text-white">
              {{ contextMenuTitle }}
            </h2>
            <p class="mt-1 truncate text-xs text-white/46">
              {{ contextMenuSource }}
            </p>
          </div>
          <button type="button" class="detail-panel-close" aria-label="关闭播放详情" @click="closePlaybackDetailPanel()">
            ×
          </button>
        </div>

        <div class="mt-4 rounded-2xl border border-white/10 bg-white/[0.045] p-3">
          <div class="flex items-center justify-between gap-3 text-xs">
            <span class="font-semibold text-white/72">{{ playbackStatsHeadline }}</span>
            <span class="text-white/42">{{ isPlaying ? 'Playing' : 'Paused' }}</span>
          </div>
          <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-white/10">
            <div class="h-full rounded-full bg-white/72" :style="{ width: `${playbackProgressPercent}%` }" />
          </div>
        </div>

        <dl class="detail-stats-grid mt-3">
          <template v-for="detail in contextMenuDetails" :key="detail.label">
            <dt>{{ detail.label }}</dt>
            <dd>{{ detail.value }}</dd>
          </template>
        </dl>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
:global(html.player-render-surface-active),
:global(body.player-render-surface-active),
:global(body.player-render-surface-active #app),
:global(body.player-render-surface-active .app-window) {
  background: #030305;
}

:global(body.player-render-surface-active main) {
  background: transparent;
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

.player-top-chrome {
  background: var(--player-chrome-top-gradient);
}

.player-bottom-chrome {
  background: var(--player-chrome-bottom-gradient);
}

.keyboard-osd-enter-active,
.keyboard-osd-leave-active {
  transition: opacity 160ms ease, transform 160ms ease;
}

.keyboard-osd-enter-from,
.keyboard-osd-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}

.touch-gesture-osd {
  border-radius: 8px;
}

.touch-gesture-icon {
  display: flex;
  width: 2.35rem;
  height: 2.35rem;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  color: rgba(255, 255, 255, 0.94);
  background: rgba(255, 255, 255, 0.12);
}

.touch-gesture-icon svg {
  width: 1.35rem;
  height: 1.35rem;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.touch-gesture-osd strong {
  margin-top: 0.6rem;
  font-size: 0.86rem;
}

.touch-gesture-osd > span:not(.touch-gesture-icon, .touch-gesture-meter) {
  margin-top: 0.18rem;
  color: rgba(255, 255, 255, 0.62);
  font-size: 0.72rem;
}

.touch-gesture-meter {
  width: 7rem;
  height: 0.3rem;
  margin-top: 0.65rem;
  overflow: hidden;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.14);
}

.touch-gesture-meter i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: rgba(255, 255, 255, 0.9);
}

.touch-gesture-osd-enter-active,
.touch-gesture-osd-leave-active {
  transition: opacity 140ms ease, transform 140ms ease;
}

.touch-gesture-osd-enter-from,
.touch-gesture-osd-leave-to {
  opacity: 0;
  transform: translate(-50%, -46%) scale(0.96);
}

.player-view--transparent {
  background: transparent;
  background-color: transparent;
}

.android-touch-capture {
  background: rgba(0, 0, 0, 0.002);
  touch-action: none;
}

.player-context-menu,
.player-detail-panel {
  border-color: var(--control-border);
  color: var(--color-text);
  background: var(--player-chrome-surface-strong);
  box-shadow:
    var(--player-chrome-shadow);
}

.context-menu-action {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  border: 1px solid transparent;
  border-radius: 16px;
  padding: 0.58rem 0.72rem;
  color: rgba(255, 255, 255, 0.72);
  background: rgba(255, 255, 255, 0.045);
  font-size: 0.78rem;
  font-weight: 700;
  text-align: left;
  transition: background var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), transform var(--duration-fast) var(--ease-out);
}

.context-menu-action:hover,
.context-menu-action:focus-visible {
  border-color: rgba(255, 255, 255, 0.16);
  color: rgba(255, 255, 255, 0.96);
  background: rgba(255, 255, 255, 0.11);
  transform: translateY(-1px);
}

.detail-panel-dot {
  height: 0.45rem;
  width: 0.45rem;
  flex: 0 0 auto;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.76);
  box-shadow: 0 0 18px rgba(255, 255, 255, 0.55);
}

.detail-stats-grid {
  display: grid;
  grid-template-columns: minmax(7.5rem, max-content) minmax(0, 1fr);
  gap: 0.08rem 0.8rem;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 1rem;
  background: rgba(255, 255, 255, 0.035);
  padding: 0.75rem;
  font-size: 0.72rem;
  line-height: 1.45;
}

.detail-stats-grid dt {
  color: rgba(255, 255, 255, 0.38);
}

.detail-stats-grid dd {
  min-width: 0;
  overflow: hidden;
  color: rgba(255, 255, 255, 0.76);
  font-weight: 650;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-panel-close {
  display: inline-flex;
  height: 1.9rem;
  width: 1.9rem;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 999px;
  color: rgba(255, 255, 255, 0.58);
  background: rgba(255, 255, 255, 0.06);
  font-size: 1.2rem;
  line-height: 1;
  transition: background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out);
}

.detail-panel-close:hover,
.detail-panel-close:focus-visible {
  color: rgba(255, 255, 255, 0.92);
  background: rgba(255, 255, 255, 0.14);
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

@media (max-width: 820px), (hover: none) and (pointer: coarse) {
  .player-view {
    cursor: default !important;
  }

  .player-top-chrome {
    padding: max(4.5rem, calc(env(safe-area-inset-top) + 3.5rem)) 1rem 2rem;
  }

  .player-top-chrome h1 {
    max-width: calc(100vw - 5rem);
    font-size: 1rem;
  }

  .player-top-chrome p:first-child,
  .player-top-chrome p:nth-of-type(2) {
    display: none;
  }

  .player-bottom-chrome {
    padding: 2.5rem 0.65rem max(0.65rem, env(safe-area-inset-bottom));
  }

  .keyboard-osd {
    top: max(4rem, calc(env(safe-area-inset-top) + 3.25rem));
    right: 0.75rem;
    max-width: calc(100vw - 1.5rem);
  }

  .context-menu-action {
    min-height: 3rem;
    border-radius: 8px;
  }

  .player-detail-panel {
    right: 0.75rem;
    bottom: max(0.75rem, env(safe-area-inset-bottom));
    left: 0.75rem;
    top: auto;
    width: auto;
    max-height: 78svh;
    overflow-y: auto;
    border-radius: 8px;
  }
}

@media (any-pointer: coarse) {
  .player-view {
    touch-action: none;
  }
}

.player-view--native-mobile {
  cursor: default !important;
  touch-action: none;
}

.player-view--native-mobile .player-top-chrome {
  padding: max(2.5rem, calc(env(safe-area-inset-top) + 1.25rem)) 1rem 1.5rem;
}

.player-view--native-mobile .player-top-chrome h1 {
  max-width: calc(100vw - 5rem);
  font-size: 1rem;
}

.player-view--native-mobile .player-top-chrome p:first-child,
.player-view--native-mobile .player-top-chrome p:nth-of-type(2) {
  display: none;
}

.player-view--native-mobile .player-bottom-chrome {
  padding: 2rem 0.65rem max(0.65rem, env(safe-area-inset-bottom));
}
</style>
