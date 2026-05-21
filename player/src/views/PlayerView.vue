<script setup lang="ts">
import type { KnownSubtitleTrackInput, MpvRenderState, MpvZOrderStrategy, RenderSurfaceBounds, VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import type { SubtitleTrack as DataSourceSubtitleTrack, MediaItem, ProviderPlaybackProgressEvent, ProviderPlaybackSyncDiagnostic } from '@/services/datasource/types'
import type { PlaybackQueueState } from '@/services/playbackContext'
import type { PlaybackHistoryEntry, PlaybackProgressUpsert } from '@/services/playbackHistory'
import { LogicalSize } from '@tauri-apps/api/dpi'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import PlayerControls from '@/components/player/PlayerControls.vue'
import VideoPlayer from '@/components/player/VideoPlayer.vue'
import { useMpv } from '@/composables/useMpv'
import { redactSensitiveText, toSafeErrorMessage } from '@/services/datasource/errors'
import { getPlaybackMediaContext } from '@/services/playbackContext'
import { createSafeStreamIdentity, getPlaybackProgress, isCompletedPosition, savePlaybackProgress, shouldResumePlayback } from '@/services/playbackHistory'
import { useDataSourceStore } from '@/stores/datasource'

const AUTO_HIDE_DELAY = 2800
const HISTORY_SAVE_INTERVAL = 10000
const HISTORY_MIN_SAVE_POSITION = 1
const HISTORY_MIN_RESUME_POSITION = 30
const HOME_REFRESH_AFTER_PLAYBACK_DELAY = 1200
const LOCAL_FILE_SOURCE_ID = 'local-file'
const CONTEXT_MENU_WIDTH = 224
const CONTEXT_MENU_MAX_HEIGHT = 360
const CONTEXT_MENU_MARGIN = 12

interface ContextMenuPosition {
  x: number
  y: number
}

interface PlaybackContextMenuDetail {
  label: string
  value: string
}

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const appWindow = getCurrentWindow()
const mediaTitle = ref('未命名影片')
const mediaPath = ref('')
const activeSourceId = ref('')
const activeItemId = ref('')
const activeLibraryId = ref('')
const activeMediaType = ref<MediaItem['type'] | undefined>()
const activePosterUrl = ref('')
const activeBackdropUrl = ref('')
const playbackQueue = ref<PlaybackQueueState | null>(null)
const playbackContextId = ref('')
const queueSwitchError = ref<string | null>(null)
const isQueueSwitching = ref(false)
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
const contextMenuOpen = ref(false)
const playbackDetailOpen = ref(false)
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
let renderInitPromise: Promise<void> | null = null
let boundsUpdateInFlight = false
let pendingRenderBounds: RenderSurfaceBounds | null = null
let playbackCleanupStarted = false
let historySaveTimer: number | undefined
let resumeMessageTimer: number | undefined
let homeRefreshTimer: number | undefined
let lastSavedPosition = -1
let playbackStartPosition = 0
const resumeSeekTimers = new Set<number>()

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
  renderBackend,
  renderDiagnostics,
  videoDynamicRange,
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
const currentQueueItem = computed(() => {
  const queue = playbackQueue.value
  return queue ? queue.items[queue.currentIndex] : null
})
const playbackQueueItemCount = computed(() => playbackQueue.value?.items.length ?? (hasMedia.value ? 1 : 0))
const canPlayPrevious = computed(() => Boolean(playbackQueue.value && playbackQueue.value.currentIndex > 0 && !isQueueSwitching.value))
const canPlayNext = computed(() => Boolean(playbackQueue.value && playbackQueue.value.currentIndex < playbackQueue.value.items.length - 1 && !isQueueSwitching.value))
const shouldShowChrome = computed(() => chromeVisible.value || !hasMedia.value || !isPlaying.value || controlsInteracting.value || contextMenuOpen.value || playbackDetailOpen.value)
const isTransparentRootActive = computed(() => hasMedia.value && renderStatus.value === 'ready')
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
  return hasMedia.value && isPlaying.value && !controlsInteracting.value && !contextMenuOpen.value && !playbackDetailOpen.value && isWindowFocused.value
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
  return playbackContextId.value ? getPlaybackMediaContext(playbackContextId.value) : null
}

function looksLikeMediaFilename(value: string): boolean {
  return /\.(?:3g2|3gp|avi|flv|m2ts|m4v|mkv|mov|mp4|mpeg|mpg|mts|ogm|ogv|rmvb|ts|webm|wmv)(?:$|[\s"')，。])/i.test(value)
}

function containsUnsafeDisplayToken(value: string): boolean {
  const normalized = value.trim()
  return /^https?:\/\//i.test(normalized)
    || /^[a-z]:[\\/]/i.test(normalized)
    || normalized.startsWith('\\\\')
    || normalized.startsWith('/')
    || normalized.startsWith('~/')
    || /\b(?:[a-z]:[\\/]|file:\/\/|https?:\/\/)/i.test(normalized)
    || /(?:^|[\s"'({])\/(?:[^/\s?#"')]+\/)+[^/\s?#"')]+/.test(normalized)
    || /(?:^|[\s"'({])\\\\[^\\/\s]+[\\/][^\\/\s]+/.test(normalized)
    || /\b(?:\d{1,3}\.){3}\d{1,3}\b/.test(normalized)
    || (!looksLikeMediaFilename(normalized) && /\b(?:localhost|(?:[a-z0-9-]+\.)+[a-z]{2,})(?::\d{2,5})?\b/i.test(normalized))
}

function truncateMenuText(value: string, maxLength: number): string {
  return value.length > maxLength ? `${value.slice(0, Math.max(0, maxLength - 1))}…` : value
}

function safeMenuText(value: unknown, fallback: string, maxLength = 120): string {
  if (value == null)
    return fallback

  const text = redactSensitiveText(value).replace(/\s+/g, ' ').trim()
  if (!text || containsUnsafeDisplayToken(text))
    return fallback

  return truncateMenuText(text, maxLength)
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

function renderStatusLabel(status: MpvRenderState['status']): string {
  switch (status) {
    case 'initializing':
      return '准备中'
    case 'ready':
      return '已就绪'
    case 'unsupported':
      return '暂不可用'
    case 'error':
      return '需要重试'
    case 'idle':
    default:
      return '待播放'
  }
}

function renderBackendLabel(backend: MpvRenderState['backend']): string {
  switch (backend) {
    case 'windowsTransparentOverlay':
      return 'Windows 透明叠层'
    case 'windowsOpenGl':
      return 'Windows OpenGL'
    case 'linuxFuture':
      return 'Linux 预留 backend'
    case 'macosFuture':
      return 'macOS 预留 backend'
    case 'mobileFuture':
      return '移动端预留 backend'
    case 'unsupported':
    default:
      return '暂不支持'
  }
}

function videoAspectLabel(mode: VideoAspectMode): string {
  switch (mode) {
    case '16:9':
      return '16:9'
    case '4:3':
      return '4:3'
    case 'cinema':
      return '2.35:1'
    case 'default':
    default:
      return '原始比例'
  }
}

function videoFitLabel(mode: VideoFitMode): string {
  switch (mode) {
    case 'crop':
      return '填充裁切'
    case 'cinemaCrop':
      return '影院裁切'
    case 'fit':
    default:
      return '适应窗口'
  }
}

function compactTrackLabel(parts: Array<string | number | null | undefined>, fallback: string): string {
  const label = parts
    .filter(part => part != null && String(part).trim().length > 0)
    .map(part => String(part).trim())
    .join(' · ')
  return safeMenuText(label, fallback, 72)
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

  const source = track.source === 'embedded' ? '内封' : track.source === 'external' ? '外挂' : '详情'
  return compactTrackLabel([source, track.language, track.title, track.codec], String(track.id))
}

function playbackQueuePositionLabel(): string {
  const queue = playbackQueue.value
  if (!queue)
    return hasMedia.value ? '单个媒体' : '无媒体'

  return `${queue.currentIndex + 1} / ${queue.items.length}`
}

function syncActiveMediaMetadataFromRoute() {
  activeSourceId.value = queryStringValue(route.query.sourceId)
  activeItemId.value = queryStringValue(route.query.itemId)
  activeLibraryId.value = queryStringValue(route.query.libraryId)
  activeMediaType.value = queryMediaType()
  activePosterUrl.value = queryStringValue(route.query.posterUrl)
  activeBackdropUrl.value = queryStringValue(route.query.backdropUrl)
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

function currentHistoryPayload(): PlaybackProgressUpsert | null {
  const identity = currentHistoryIdentity()
  if (!identity)
    return null

  const context = currentPlaybackContext()
  const queueItem = currentQueueItem.value
  const itemId = activeItemId.value || context?.itemId || queueItem?.id || undefined
  const libraryId = activeLibraryId.value || queueItem?.libraryId
  const mediaType = activeMediaType.value ?? queueItem?.type
  const position = Math.max(0, currentTime.value)
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
    position,
    duration: mediaDuration,
    completed: isCompletedPosition(position, mediaDuration),
  }
}

function currentMediaSourceId(): string | undefined {
  const routeMediaSourceId = queryStringValue(route.query.mediaSourceId)
  return routeMediaSourceId || currentPlaybackContext()?.mediaSourceId || undefined
}

function queryNumberValue(value: unknown): number | undefined {
  const raw = typeof value === 'string' || typeof value === 'number' ? Number.parseFloat(String(value)) : undefined
  return typeof raw === 'number' && Number.isFinite(raw) && raw >= 0 ? raw : undefined
}

function routeResumePosition(): number | undefined {
  return queryNumberValue(route.query.resumePosition)
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
    void store.loadHomeSections()
  }, HOME_REFRESH_AFTER_PLAYBACK_DELAY)
}

function shouldSaveLocalProgress(payload: PlaybackProgressUpsert, force: boolean, event: ProviderPlaybackProgressEvent): boolean {
  if (payload.completed)
    return payload.position >= HISTORY_MIN_SAVE_POSITION

  if (force && isLowPositionTerminalEvent(payload, event))
    return false

  return payload.position >= HISTORY_MIN_SAVE_POSITION
}

function queryMediaType(): MediaItem['type'] | undefined {
  const value = queryStringValue(route.query.mediaType)
  return isMediaType(value) ? value : undefined
}

function isMediaType(value: string): value is MediaItem['type'] {
  return ['movie', 'series', 'season', 'episode', 'folder', 'file'].includes(value)
}

async function saveCurrentProgress(force = false, event: ProviderPlaybackProgressEvent = 'progress') {
  const payload = currentHistoryPayload()
  if (!payload)
    return

  const providerEvent = providerEventForPayload(payload, event)
  if (!force && (!isPlaying.value || payload.position < HISTORY_MIN_SAVE_POSITION || Math.abs(payload.position - lastSavedPosition) < HISTORY_MIN_SAVE_POSITION)) {
    if (event !== 'progress' && !isLowPositionTerminalEvent(payload, event))
      void syncProviderProgress(payload, providerEvent)
    return
  }

  if (!shouldSaveLocalProgress(payload, force, event))
    return

  const saved = await savePlaybackProgress(payload)
  if (saved)
    lastSavedPosition = saved.position

  const providerSync = syncProviderProgress(payload, providerEvent)
  if (shouldRefreshHomeAfterProgressEvent(event, providerEvent))
    void providerSync.finally(scheduleHomeSectionsRefreshAfterPlayback)
  else
    void providerSync
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
  const position = shouldResumePlayback(saved)
    ? saved.position
    : shouldResumePosition(fallbackPosition, fallbackDuration)
      ? fallbackPosition
      : undefined

  if (position == null) {
    playbackStartPosition = 0
    resumeMessage.value = null
    return
  }

  playbackStartPosition = position
  await seekResumePosition(position)
  resumeMessage.value = `已从 ${formatPlaybackTime(position)} 继续播放`
  clearResumeMessageTimer()
  resumeMessageTimer = window.setTimeout(() => {
    resumeMessageTimer = undefined
    resumeMessage.value = null
  }, 3600)
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
    await seek(position)
  }
  catch {
    // Resume seek is retried after media metadata settles; failures must not break playback startup.
  }
}

function scheduleResumeSeek(position: number, delay: number, force: boolean) {
  const path = mediaPath.value
  const timer = window.setTimeout(() => {
    resumeSeekTimers.delete(timer)
    if (playbackCleanupStarted || !mediaPath.value || mediaPath.value !== path)
      return
    if (force || Math.abs(currentTime.value - position) > 5)
      void seekResumePositionSilently(position)
  }, delay)
  resumeSeekTimers.add(timer)
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
  clearResumeSeekTimers()
  clearResumeMessageTimer()
  lastSavedPosition = -1
  playbackStartPosition = 0
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

function formatPlaybackTime(totalSeconds: number): string {
  const seconds = Math.max(0, Math.floor(totalSeconds))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = seconds % 60

  if (hours > 0)
    return `${hours}:${minutes.toString().padStart(2, '0')}:${rest.toString().padStart(2, '0')}`

  return `${minutes}:${rest.toString().padStart(2, '0')}`
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
    await saveCurrentProgress(true, 'stopped')
    const nextPath = typeof path === 'string' ? path : ''
    resetHistorySaveState()
    mediaPath.value = nextPath
    mediaTitle.value = typeof route.query.title === 'string' ? route.query.title : '未命名影片'
    pictureSettingsError.value = null
    queueSwitchError.value = null
    syncPlaybackQueueFromRoute()
    syncActiveMediaMetadataFromRoute()
    closePlaybackContextMenu(false)
    closePlaybackDetailPanel(false)
    revealChrome()

    if (nextPath) {
      await syncKnownSubtitleTracks()
      await ensureRenderInitialized()
      if (playbackCleanupStarted)
        return
      await load(nextPath)
      startHistorySaveTimer()
      await resumeSavedProgressIfAvailable()
      syncProviderPlaybackStarted()
      if (playbackCleanupStarted)
        await stopPlaybackSilently()
    }
    else {
      setKnownSubtitleTracks([])
    }
  },
  { immediate: true },
)

watch(isPlaying, (playing) => {
  revealChrome()
  if (playing) {
    startHistorySaveTimer()
    void saveCurrentProgress(false, 'resumed')
  }
  else {
    void saveCurrentProgress(true, 'paused')
  }
})

watch([shouldShowChrome, hasMedia], () => {
  void updateChromeOcclusion()
})

async function handleFileDrop(path: string) {
  await saveCurrentProgress(true, 'stopped')
  resetHistorySaveState()
  mediaPath.value = path
  mediaTitle.value = path.split(/[\\/]/).pop() || '本地视频'
  playbackQueue.value = null
  playbackContextId.value = ''
  activeSourceId.value = LOCAL_FILE_SOURCE_ID
  activeItemId.value = ''
  activeLibraryId.value = ''
  activeMediaType.value = 'file'
  activePosterUrl.value = ''
  activeBackdropUrl.value = ''
  pictureSettingsError.value = null
  queueSwitchError.value = null
  setKnownSubtitleTracks([])
  revealChrome()
  await ensureRenderInitialized()
  if (playbackCleanupStarted)
    return
  await load(path)
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
  isQueueSwitching.value = true
  queueSwitchError.value = null
  revealChrome()
  try {
    store.loadConfigs()
    await store.syncManager()
    const source = store.getSource(target.sourceId)
    if (!source)
      throw new Error('数据源不可用，请检查设置或重新登录。')

    const streamUrl = await source.getStreamURL(target.id)
    if (playbackCleanupStarted)
      return

    playbackQueue.value = {
      items: queue.items.map(item => ({ ...item })),
      currentIndex: index,
    }
    await router.replace({
      name: 'player',
      query: {
        ...route.query,
        title: target.title,
        path: streamUrl,
        sourceId: target.sourceId,
        itemId: target.id,
        libraryId: target.libraryId,
        mediaType: target.type,
        posterUrl: target.posterUrl,
        backdropUrl: target.backdropUrl,
        contextId: playbackContextId.value || undefined,
        mediaSourceId: undefined,
        audioIndex: undefined,
        subtitleIndex: undefined,
      },
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
  await saveCurrentProgress(true, 'stopped')
  clearHistorySaveTimer()
  if (!hasMedia.value && !isPlaying.value)
    return

  await stopPlaybackSilently()
}

function handleBeforeUnload() {
  void saveCurrentProgress(true, 'stopped')
}

function handleGlobalKeydown(event: KeyboardEvent) {
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
  window.addEventListener('beforeunload', handleBeforeUnload)
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
  clearResumeSeekTimers()
  clearResumeMessageTimer()
  window.removeEventListener('blur', handleWindowBlur)
  window.removeEventListener('focus', handleWindowFocus)
  window.removeEventListener('resize', handleWindowResize)
  window.removeEventListener('beforeunload', handleBeforeUnload)
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
    @contextmenu="openPlaybackContextMenu"
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
      :provider-sync-diagnostics="providerSyncDiagnostics"
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
        :queue-items="playbackQueue?.items ?? []"
        :current-queue-index="playbackQueue?.currentIndex ?? 0"
        :is-queue-switching="isQueueSwitching"
        :can-play-previous="canPlayPrevious"
        :can-play-next="canPlayNext"
        :current-subtitle="currentSubtitle"
        :current-audio="currentAudio"
        :video-aspect-mode="videoAspectMode"
        :video-fit-mode="videoFitMode"
        :track-error="trackError"
        :picture-settings-error="pictureSettingsError"
        @play-previous="handlePlayPrevious"
        @toggle-pause="handleTogglePause"
        @play-next="handlePlayNext"
        @select-queue-item="playQueueItemAt"
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

    <Teleport to="body">
      <div
        v-if="contextMenuOpen"
        class="fixed inset-0 z-[1080]"
        aria-hidden="false"
        @pointerdown="closePlaybackContextMenu()"
        @contextmenu.prevent="openPlaybackContextMenu"
      >
        <div
          class="player-context-menu pointer-events-auto fixed w-56 max-w-[calc(100vw-1.5rem)] overflow-hidden rounded-2xl border border-white/14 bg-black/76 p-1.5 text-sm text-white/82 shadow-2xl backdrop-blur-2xl"
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
        class="player-detail-panel pointer-events-auto fixed left-6 top-24 z-[1070] w-[min(28rem,calc(100vw-3rem))] overflow-hidden rounded-3xl border border-white/14 bg-black/68 p-4 text-sm text-white/78 shadow-2xl backdrop-blur-2xl"
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

.player-context-menu,
.player-detail-panel {
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.16),
    0 24px 80px rgba(0, 0, 0, 0.52);
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
</style>
