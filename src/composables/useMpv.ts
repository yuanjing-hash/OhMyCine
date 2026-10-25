import type { HttpReadProfile } from '@/services/datasource/types'
import type { LocalSubtitleDownloadResult } from '@/services/subtitle/types'
import type { MpvColorDiagnostics, VideoDynamicRangeState } from '@/services/videoColor'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'
import { computed, onUnmounted, ref } from 'vue'
import { loadPlayerInteractionSettings } from '@/services/playerInteractionSettings'
import { describeVideoSourceColor, describeVideoTargetColor } from '@/services/videoColor'

export type { MpvColorDiagnostics, VideoDynamicRangeState } from '@/services/videoColor'

export interface Track {
  id: number
  kind: 'sub' | 'audio'
  language?: string | null
  title?: string | null
  codec?: string | null
  channels?: number | null
  isDefault: boolean
  selected: boolean
}

export type SubtitleSelectionId = `embedded:${number}` | `external:${string}`
export type SubtitleTrackSource = 'embedded' | 'downloaded' | 'provider' | 'detail'

export interface SubtitleTrackOption {
  id: SubtitleSelectionId
  kind: 'sub'
  source: SubtitleTrackSource
  language?: string | null
  title?: string | null
  codec?: string | null
  channels?: number | null
  isDefault: boolean
  selected: boolean
  selectable: boolean
  mpvId?: number
  url?: string
  headers?: Readonly<Record<string, string>>
  unavailableReason?: string
}

export interface KnownSubtitleTrackInput {
  id: string | number
  source?: SubtitleTrackSource
  language?: string | null
  title?: string | null
  codec?: string | null
  isDefault?: boolean
  url?: string
  headers?: Readonly<Record<string, string>>
  selectable?: boolean
  unavailableReason?: string
}

export interface MpvLoadOptions {
  readonly headers?: Record<string, string>
  readonly audioUrl?: string
  readonly audioHeaders?: Record<string, string>
  readonly title?: string
  readonly httpReadProfile?: HttpReadProfile
}

interface MpvHttpHeaderPayload {
  name: string
  value: string
}

interface MpvTrackState {
  tracks: Track[]
  currentSubtitle?: number | null
  currentAudio?: number | null
}

export type VideoAspectMode = 'default' | '16:9' | '4:3' | 'cinema'
export type VideoFitMode = 'fit' | 'crop' | 'cinemaCrop'

export type MpvRenderStatus = 'idle' | 'initializing' | 'ready' | 'unsupported' | 'error'

export type MpvZOrderStrategy = 'transparentOverlay' | 'ownedTopLevel' | 'bottomTransparentHole' | 'topDisabledFallback'
export type MpvOrientationMode = 'auto' | 'landscape' | 'portrait'

interface MpvOrientationState {
  supported: boolean
  mode: MpvOrientationMode
}

interface MpvDisplayBrightnessState {
  supported: boolean
  level: number
}

export interface MpvRenderDiagnostics {
  ownerHwndAttached: boolean
  mpvHwndCreated: boolean
  mpvHwndShown: boolean
  overlayWindowTransparent: boolean
  webviewBackgroundTransparentApplied: boolean
  zOrderUnderlayApplied: boolean
  geometryFollowing: boolean
  taskbarIgnored: boolean
  fullscreenState: string
  lastSyncResult: string
  mpvWidAccepted: boolean
  mpvInitialized: boolean
  lastBounds: string | null
  scale: number
  syncs: number
  logFile: string | null
}

export interface MpvRenderState {
  status: MpvRenderStatus
  backend: 'windowsTransparentOverlay' | 'windowsOpenGl' | 'androidSurface' | 'linuxFuture' | 'macosFuture' | 'mobileFuture' | 'unsupported'
  message: string | null
  diagnostics?: MpvRenderDiagnostics | null
}

export interface MpvPlaybackDiagnostics {
  state: 'idle' | 'loading' | 'playing' | 'ended' | 'error' | 'desktop' | string
  endReason: MpvEndReason | null
  position: number
  duration: number
  lastEvent: string
  lastError?: string | null
  fileLoaded: boolean
  videoFormat?: string | null
  audioCodec?: string | null
  videoBitrate: number | null
  audioBitrate: number | null
  voConfigured: boolean
  hardwareDecoder?: string | null
  videoOutput: string
  videoOutputFallbackUsed: boolean
  playbackTransport: string
  fsrStatus?: string
  fsrReason?: string | null
  color?: MpvColorDiagnostics | null
  logs: string[]
}

export type MpvEndReason = 'eof' | 'stop' | 'quit' | 'error' | 'redirect'
export type MpvSeekPrecision = 'exact' | 'keyframes'

interface MpvBufferState {
  position: number
  seeking: boolean
  pausedForCache: boolean
  cacheBufferingState: number | null
  cacheSpeed: number | null
  voConfigured: boolean
  fileLoaded: boolean
  seekSerial: number
  restartedSeekSerial: number
  seekTarget: number | null
}

interface MpvSeekReceipt {
  seekSerial: number
  completed: boolean
}

interface SeekRequest {
  sequence: number
  generation: number
  position: number
  precision: MpvSeekPrecision
  resolve: () => void
  reject: (error: unknown) => void
}

interface SeekSettlement {
  sequence: number
  position: number
  precision: MpvSeekPrecision
  acknowledged: boolean
  baselineSerial: number | null
}

export interface RenderSurfaceBounds {
  x: number
  y: number
  width: number
  height: number
  scaleFactor: number
  topOcclusion?: number
  bottomOcclusion?: number
}

const DEFAULT_PLAYBACK_SPEED = 1
const MIN_PLAYBACK_SPEED = 0.25
const MAX_PLAYBACK_SPEED = 4
const DEFAULT_SUBTITLE_DELAY = 0
const MIN_SUBTITLE_DELAY = -30
const MAX_SUBTITLE_DELAY = 30
const BUFFER_POLL_INTERVAL_MS = 400
const BUFFER_DISPLAY_DELAY_MS = 500

const ASPECT_PROPERTY_VALUE: Record<VideoAspectMode, string> = {
  'default': '-1',
  '16:9': '16:9',
  '4:3': '4:3',
  'cinema': '2.35:1',
}

const FIT_PROPERTY_VALUE: Record<VideoFitMode, string> = {
  fit: '0',
  crop: '1',
  cinemaCrop: '0.5',
}

function normalizePlaybackSpeed(speed: number): number {
  if (!Number.isFinite(speed))
    return DEFAULT_PLAYBACK_SPEED
  return Math.max(MIN_PLAYBACK_SPEED, Math.min(MAX_PLAYBACK_SPEED, speed))
}

function normalizeSubtitleDelay(delay: number): number {
  if (!Number.isFinite(delay))
    return DEFAULT_SUBTITLE_DELAY
  const bounded = Math.max(MIN_SUBTITLE_DELAY, Math.min(MAX_SUBTITLE_DELAY, delay))
  return Math.round(bounded * 10) / 10
}

async function applyEngineSettings(): Promise<void> {
  const settings = loadPlayerInteractionSettings()
  await invoke<void>('mpv_apply_engine_settings', {
    settings: {
      videoOutput: settings.videoOutput,
      hardwareDecoder: settings.hardwareDecoder,
      cacheMode: settings.cacheMode,
      demuxerMaxBytesMb: settings.demuxerMaxBytesMb,
      videoSync: settings.videoSync,
      backgroundPlaybackEnabled: settings.androidBackgroundPlaybackEnabled,
      fsrMode: settings.fsrMode,
      fsrSharpness: settings.fsrSharpness,
      fsrDenoise: settings.fsrDenoise,
      fsrTarget: settings.fsrTarget,
    },
  })
}

function safeErrorMessage(error: unknown, fallback: string): string {
  if (typeof error === 'string' && error.trim())
    return error
  if (error instanceof Error && error.message.trim())
    return error.message
  return fallback
}

function embeddedSubtitleId(id: number): SubtitleSelectionId {
  return `embedded:${id}`
}

function knownSubtitleId(id: string | number): SubtitleSelectionId {
  return `external:${String(id)}`
}

function parseEmbeddedSubtitleId(id: SubtitleSelectionId): number | null {
  if (!id.startsWith('embedded:'))
    return null
  const value = Number.parseInt(id.slice('embedded:'.length), 10)
  return Number.isFinite(value) ? value : null
}

function toEmbeddedSubtitleTrack(track: Track): SubtitleTrackOption {
  return {
    ...track,
    id: embeddedSubtitleId(track.id),
    kind: 'sub',
    source: 'embedded',
    mpvId: track.id,
    selectable: true,
  }
}

function toKnownSubtitleTrack(track: KnownSubtitleTrackInput): SubtitleTrackOption {
  const hasUrl = typeof track.url === 'string' && track.url.length > 0
  const selectable = track.selectable ?? hasUrl
  const source = track.source ?? (hasUrl ? 'provider' : 'detail')

  return {
    id: knownSubtitleId(track.id),
    kind: 'sub',
    source,
    language: track.language,
    title: track.title,
    codec: track.codec,
    channels: null,
    isDefault: track.isDefault ?? false,
    selected: false,
    selectable,
    url: track.url,
    headers: track.headers ? { ...track.headers } : undefined,
    unavailableReason: selectable ? undefined : track.unavailableReason ?? '该字幕由媒体详情提供，但当前数据源未提供可直接加载的字幕地址。',
  }
}

function mergeSubtitleTracks(embeddedTracks: readonly SubtitleTrackOption[], knownTracks: readonly SubtitleTrackOption[]): SubtitleTrackOption[] {
  const seenKnown = new Set<string>()
  const uniqueKnown = knownTracks.filter((track) => {
    const key = [track.source, track.title ?? '', track.language ?? '', track.codec ?? '', track.url ?? '', track.id].join('|')
    if (seenKnown.has(key))
      return false
    seenKnown.add(key)
    return true
  })

  return [...embeddedTracks, ...uniqueKnown]
}

function downloadedSubtitleKey(path: string): string {
  // Windows may return a canonical long path while the saved preference still uses
  // the same normal absolute path. Other platforms keep case-sensitive identity.
  if (/^(?:[a-z]:[\\/]|\\\\)/i.test(path))
    return path.replace(/^\\\\\?\\UNC\\/i, '\\\\').replace(/^\\\\\?\\/, '').replace(/\//g, '\\').toLowerCase()
  return path
}

export function useMpv() {
  const isPlaying = ref(false)
  const currentTime = ref(0)
  const duration = ref(0)
  const playbackEndReason = ref<MpvEndReason | null>(null)
  const lastConfirmedTime = ref(0)
  const lastConfirmedDuration = ref(0)
  const volume = ref(100)
  const isMuted = ref(false)
  const videoBrightness = ref(50)
  const displayBrightness = ref(50)
  const displayBrightnessSupported = ref(false)
  const playbackSpeed = ref(DEFAULT_PLAYBACK_SPEED)
  const subtitleDelay = ref(DEFAULT_SUBTITLE_DELAY)
  const embeddedSubtitleTracks = ref<SubtitleTrackOption[]>([])
  const knownSubtitleTracks = ref<SubtitleTrackOption[]>([])
  const downloadedSubtitleTracks = ref<SubtitleTrackOption[]>([])
  const sessionExternalSubtitleTracks = ref<SubtitleTrackOption[]>([])
  const subtitleTracks = computed(() => mergeSubtitleTracks(embeddedSubtitleTracks.value, [
    ...downloadedSubtitleTracks.value,
    ...knownSubtitleTracks.value,
    ...sessionExternalSubtitleTracks.value,
  ]))
  const audioTracks = ref<Track[]>([])
  const currentSubtitle = ref<SubtitleSelectionId | null>(null)
  const selectedKnownSubtitle = ref<SubtitleSelectionId | null>(null)
  const currentAudio = ref<number | null>(null)
  const trackStateReady = ref(false)
  const videoAspectMode = ref<VideoAspectMode>('default')
  const videoFitMode = ref<VideoFitMode>('fit')
  const renderStatus = ref<MpvRenderStatus>('idle')
  const renderError = ref<string | null>(null)
  const renderBackend = ref<MpvRenderState['backend']>('unsupported')
  const renderDiagnostics = ref<MpvRenderDiagnostics | null>(null)
  const playbackDiagnostics = ref<MpvPlaybackDiagnostics | null>(null)
  const videoReady = ref(false)
  const fileLoaded = ref(false)
  const isBuffering = ref(false)
  const nativeSeeking = ref(false)
  const nativeCachePaused = ref(false)
  const seekPending = ref(false)
  const isPlaybackWaiting = computed(() => seekPending.value || nativeSeeking.value || nativeCachePaused.value)
  const bufferingKind = computed(() => nativeCachePaused.value ? 'buffering' as const : 'seeking' as const)
  const bufferPercent = ref<number | null>(null)
  const bufferSpeedBytesPerSecond = ref(0)
  const orientationSupported = ref(false)
  const orientationMode = ref<MpvOrientationMode>('auto')
  const videoDynamicRange = ref<VideoDynamicRangeState>(describeVideoSourceColor(null))
  const videoTargetDynamicRange = computed(() => describeVideoTargetColor(playbackDiagnostics.value?.color))
  const trackError = ref<string | null>(null)
  let renderDiagnosticsTimer: number | undefined
  let bufferPollingTimer: number | undefined
  let bufferDisplayTimer: number | undefined
  let bufferPollInFlight = false
  let bufferPollingGeneration = 0
  const trackRefreshTimers = new Set<number>()
  let subtitleDelayCommand = Promise.resolve()
  let subtitleDelayGeneration = 0
  let loadedExternalSubtitleSequence = 0
  let trackRefreshScheduledForCurrentMedia = false
  let playbackGeneration = 0
  let videoColorGeneration = 0
  let playbackActive = false
  let mediaLoadPending = false
  let disposed = false
  let diagnosticsRefreshPromise: Promise<void> | null = null
  let playbackTransitionPromise = Promise.resolve()
  let seekSequence = 0
  let pendingSeekRequest: SeekRequest | null = null
  let seekSettlement: SeekSettlement | null = null
  let seekDispatchInFlight = false
  let pauseIntentSequence = 0

  const unlistenPromises = [
    listen<{ time: number }>('mpv:time-update', (event) => {
      if (disposed || !playbackActive || mediaLoadPending || !Number.isFinite(event.payload.time) || event.payload.time < 0
        || playbackEndReason.value === 'error' || playbackEndReason.value === 'stop' || playbackEndReason.value === 'quit') {
        return
      }
      if (!isPlaybackWaiting.value) {
        currentTime.value = event.payload.time
        lastConfirmedTime.value = event.payload.time
      }
    }),
    listen<{ duration: number }>('mpv:duration-change', (event) => {
      if (disposed || !playbackActive || mediaLoadPending || !Number.isFinite(event.payload.duration) || event.payload.duration <= 0)
        return
      duration.value = event.payload.duration
      lastConfirmedDuration.value = event.payload.duration
      if (event.payload.duration > 0 && !trackRefreshScheduledForCurrentMedia) {
        trackRefreshScheduledForCurrentMedia = true
        scheduleTrackRefresh(150)
      }
    }),
    listen('mpv:paused', () => {
      isPlaying.value = false
    }),
    listen('mpv:resumed', () => {
      if (playbackActive && !playbackEndReason.value)
        isPlaying.value = true
    }),
    listen('mpv:video-ready', () => {
      if (disposed || !playbackActive)
        return
      videoReady.value = true
      resetVideoColorState()
      void refreshPlaybackDiagnostics()
    }),
    listen('mpv:file-loaded', () => {
      if (disposed || !playbackActive)
        return
      fileLoaded.value = true
    }),
  ]

  function applyRenderState(state: MpvRenderState) {
    renderStatus.value = state.status
    renderBackend.value = state.backend
    renderError.value = state.status === 'error' ? state.message : null
    renderDiagnostics.value = state.diagnostics ?? null
  }

  function applyTrackState(state: MpvTrackState) {
    const tracks = state.tracks.filter(track => track.kind === 'sub' || track.kind === 'audio')
    const embeddedTracks = tracks
      .filter(track => track.kind === 'sub')
      .map(toEmbeddedSubtitleTrack)
    embeddedSubtitleTracks.value = embeddedTracks
    audioTracks.value = tracks.filter(track => track.kind === 'audio')

    const selectedEmbedded = embeddedTracks.find(track => track.mpvId === state.currentSubtitle)
    currentSubtitle.value = selectedKnownSubtitle.value ?? selectedEmbedded?.id ?? null
    currentAudio.value = state.currentAudio ?? null
    trackStateReady.value = true
    trackError.value = null
  }

  async function refreshRenderStatus() {
    try {
      const state = await invoke<MpvRenderState>('mpv_render_status')
      applyRenderState(state)
    }
    catch (error: unknown) {
      renderStatus.value = 'error'
      renderError.value = safeErrorMessage(error, '内嵌渲染状态暂不可用')
      renderDiagnostics.value = null
    }
  }

  async function refreshPlaybackDiagnostics() {
    if (disposed)
      return
    if (diagnosticsRefreshPromise)
      return diagnosticsRefreshPromise

    const generation = playbackGeneration
    const colorGeneration = videoColorGeneration
    const refresh = readPlaybackDiagnostics(generation, colorGeneration)
    diagnosticsRefreshPromise = refresh
    try {
      await refresh
    }
    finally {
      if (diagnosticsRefreshPromise === refresh)
        diagnosticsRefreshPromise = null
    }
  }

  async function readPlaybackDiagnostics(generation: number, colorGeneration: number) {
    const loadWasPending = mediaLoadPending
    const seekAtRead = seekSequence
    const seekAcknowledgedAtRead = seekSettlement?.sequence === seekAtRead && seekSettlement.acknowledged
    try {
      const diagnostics = await invoke<MpvPlaybackDiagnostics>('mpv_playback_diagnostics')
      if (disposed || generation !== playbackGeneration || loadWasPending || seekAtRead !== seekSequence)
        return

      const currentColor = playbackActive && !mediaLoadPending && colorGeneration === videoColorGeneration
      playbackDiagnostics.value = {
        ...diagnostics,
        color: currentColor ? diagnostics.color : null,
        videoBitrate: playbackActive ? positiveRate(diagnostics.videoBitrate) : null,
        audioBitrate: playbackActive ? positiveRate(diagnostics.audioBitrate) : null,
      }
      if (currentColor)
        videoDynamicRange.value = describeVideoSourceColor(diagnostics.color)
      else
        videoDynamicRange.value = describeVideoSourceColor(null)
      if (disposed || generation !== playbackGeneration)
        return
      if (playbackActive && !mediaLoadPending) {
        const endedDuringSeek = diagnostics.endReason === 'eof' && isPlaybackWaiting.value
        // A keep-open EOF from before the new seek is not its outcome. A fresh
        // post-ack terminal EOF with no loaded file has actually closed this
        // media, and cannot remain waiting for a restart that will never arrive.
        const seekClosedAtEof = endedDuringSeek && diagnostics.state === 'ended'
          && !diagnostics.fileLoaded && seekAcknowledgedAtRead
        playbackEndReason.value = diagnostics.state === 'error' ? 'error' : endedDuringSeek && !seekClosedAtEof ? null : diagnostics.endReason
        if (!isPlaybackWaiting.value && Number.isFinite(diagnostics.position) && diagnostics.position >= 0) {
          lastConfirmedTime.value = diagnostics.position
          currentTime.value = diagnostics.position
        }
        if (Number.isFinite(diagnostics.duration) && diagnostics.duration > 0) {
          lastConfirmedDuration.value = diagnostics.duration
          duration.value = diagnostics.duration
        }
        if (seekClosedAtEof) {
          cancelPendingSeek()
          stopBufferingPolling()
        }
      }
      if (playbackActive && !mediaLoadPending && diagnostics.state !== 'desktop')
        fileLoaded.value = diagnostics.fileLoaded
      if (playbackActive && !mediaLoadPending && diagnostics.state !== 'desktop' && diagnostics.fileLoaded && (diagnostics.videoFormat || diagnostics.state === 'playing'))
        videoReady.value = true
      if (!mediaLoadPending && (diagnostics.state === 'error' || diagnostics.state === 'ended' || diagnostics.state === 'idle')) {
        if (diagnostics.state !== 'ended' || !isPlaybackWaiting.value)
          isPlaying.value = false
        if (diagnostics.state === 'error')
          videoReady.value = false
        if (diagnostics.state === 'error' || diagnostics.state === 'idle'
          || (diagnostics.state === 'ended' && diagnostics.endReason !== 'eof')) {
          cancelPendingSeek()
          stopBufferingPolling()
        }
      }
    }
    catch {
      // A transient diagnostic read failure cannot erase confirmed media/end facts.
    }
  }

  function startRenderDiagnosticsPolling() {
    if (renderDiagnosticsTimer)
      return

    renderDiagnosticsTimer = window.setInterval(() => {
      if (renderStatus.value === 'initializing' || renderStatus.value === 'ready') {
        void refreshRenderStatus()
        void refreshPlaybackDiagnostics()
      }
    }, 1000)
  }

  async function initializeRender() {
    renderStatus.value = 'initializing'
    renderError.value = null

    try {
      await applyEngineSettings()
      const state = await invoke<MpvRenderState>('mpv_init_render_surface')
      applyRenderState(state)
      if (state.backend === 'androidSurface')
        await refreshPlaybackDiagnostics()
      if (state.status === 'initializing' || state.status === 'ready')
        startRenderDiagnosticsPolling()
    }
    catch (error: unknown) {
      renderStatus.value = 'error'
      renderError.value = safeErrorMessage(error, '内嵌渲染初始化失败')
      renderDiagnostics.value = null
    }
  }

  async function updateRenderSurfaceBounds(bounds: RenderSurfaceBounds) {
    try {
      const state = await invoke<MpvRenderState>('mpv_update_render_surface_bounds', { bounds })
      applyRenderState(state)
    }
    catch (error: unknown) {
      renderStatus.value = 'error'
      renderError.value = safeErrorMessage(error, '视频窗口位置同步失败')
      renderDiagnostics.value = null
    }
  }

  async function setRenderStrategy(strategy: MpvZOrderStrategy) {
    try {
      const state = await invoke<MpvRenderState>('mpv_set_render_strategy', { strategy })
      applyRenderState(state)
    }
    catch (error: unknown) {
      renderStatus.value = 'error'
      renderError.value = safeErrorMessage(error, '视频渲染策略切换失败')
      renderDiagnostics.value = null
    }
  }

  async function refreshTrackState() {
    const generation = playbackGeneration
    try {
      const state = await invoke<MpvTrackState>('mpv_track_state')
      if (disposed || generation !== playbackGeneration)
        return
      applyTrackState(state)
    }
    catch (error: unknown) {
      if (disposed || generation !== playbackGeneration)
        return
      trackError.value = safeErrorMessage(error, '轨道信息暂不可用')
      trackStateReady.value = false
      embeddedSubtitleTracks.value = []
      audioTracks.value = []
      currentSubtitle.value = selectedKnownSubtitle.value
      currentAudio.value = null
    }
  }

  async function refreshOrientationState() {
    try {
      const state = await invoke<MpvOrientationState>('mpv_orientation_state')
      orientationSupported.value = state.supported
      orientationMode.value = state.mode
    }
    catch {
      orientationSupported.value = false
      orientationMode.value = 'auto'
    }
  }

  async function setOrientationMode(mode: MpvOrientationMode) {
    const state = await invoke<MpvOrientationState>('mpv_set_orientation', { mode })
    orientationSupported.value = state.supported
    orientationMode.value = state.mode
  }

  async function refreshDisplayBrightness() {
    try {
      const state = await invoke<MpvDisplayBrightnessState>('mpv_display_brightness_state')
      displayBrightnessSupported.value = state.supported
      if (state.supported)
        displayBrightness.value = Math.max(0, Math.min(100, state.level))
    }
    catch {
      displayBrightnessSupported.value = false
    }
  }

  async function setDisplayBrightness(level: number) {
    const next = Math.max(0, Math.min(100, level))
    const state = await invoke<MpvDisplayBrightnessState>('mpv_set_display_brightness', { level: next })
    displayBrightnessSupported.value = state.supported
    if (!state.supported)
      throw new Error('当前显示器不支持系统亮度调节。')
    displayBrightness.value = Math.max(0, Math.min(100, state.level))
  }

  function scheduleTrackRefresh(delay: number) {
    const generation = playbackGeneration
    const timer = window.setTimeout(() => {
      trackRefreshTimers.delete(timer)
      if (disposed || !playbackActive || generation !== playbackGeneration)
        return
      void refreshTrackState()
      void refreshPlaybackDiagnostics()
    }, delay)
    trackRefreshTimers.add(timer)
  }

  function clearBufferDisplayTimer() {
    if (!bufferDisplayTimer)
      return
    window.clearTimeout(bufferDisplayTimer)
    bufferDisplayTimer = undefined
  }

  function resetBufferingState() {
    nativeSeeking.value = false
    nativeCachePaused.value = false
    clearBufferDisplayTimer()
    isBuffering.value = false
    bufferPercent.value = null
    bufferSpeedBytesPerSecond.value = 0
  }

  function updateWaitingPresentation() {
    if (!isPlaybackWaiting.value) {
      clearBufferDisplayTimer()
      isBuffering.value = false
      return
    }
    if (!isBuffering.value && !bufferDisplayTimer) {
      const generation = bufferPollingGeneration
      bufferDisplayTimer = window.setTimeout(() => {
        bufferDisplayTimer = undefined
        if (generation === bufferPollingGeneration && isPlaybackWaiting.value)
          isBuffering.value = true
      }, BUFFER_DISPLAY_DELAY_MS)
    }
  }

  async function refreshBufferingState(generation: number) {
    if (bufferPollInFlight)
      return

    const playback = playbackGeneration
    const sequence = seekSequence
    const acknowledgedBeforeSample = seekSettlement?.sequence === sequence && seekSettlement.acknowledged
    bufferPollInFlight = true
    try {
      const state = await invoke<MpvBufferState>('mpv_buffer_state')
      if (disposed || generation !== bufferPollingGeneration || playback !== playbackGeneration
        || sequence !== seekSequence || !playbackActive || mediaLoadPending) {
        return
      }

      nativeSeeking.value = state.seeking
      nativeCachePaused.value = state.pausedForCache
      bufferSpeedBytesPerSecond.value = positiveRate(state.cacheSpeed) ?? 0
      bufferPercent.value = state.cacheBufferingState != null && Number.isFinite(state.cacheBufferingState)
        ? Math.max(0, Math.min(100, state.cacheBufferingState))
        : null
      const settlement = seekSettlement
      // Readiness is a post-ack native sample, not the seek command's return. A
      // keyframe preview may land before its requested time; the released exact
      // target must match, including a no-op seek that emits no restart event.
      const tolerance = Math.max(0.75, playbackSpeed.value * (BUFFER_POLL_INTERVAL_MS / 1000 + 0.25))
      if (settlement?.sequence === sequence && acknowledgedBeforeSample && settlement.baselineSerial != null
        && state.seekSerial > settlement.baselineSerial && state.restartedSeekSerial === state.seekSerial
        && (settlement.precision === 'keyframes' || state.seekTarget == null || Math.abs(state.seekTarget - settlement.position) <= 0.001)
        && !state.seeking && !state.pausedForCache && state.fileLoaded && state.voConfigured
        && Number.isFinite(state.position) && state.position >= 0
        // An observed target and its restart stay valid after playback moves
        // forward. A fixed upper position window can otherwise wait forever
        // after a delayed UI/native sample has missed the presented target.
        && (settlement.precision === 'keyframes' || state.seekTarget != null
          || state.position >= settlement.position - tolerance
          || playbackDiagnostics.value?.endReason === 'eof')) {
        seekSettlement = null
        seekPending.value = false
        currentTime.value = state.position
        lastConfirmedTime.value = state.position
      }
      updateWaitingPresentation()
    }
    catch {
      // An unavailable sample is not evidence that an outstanding seek settled.
    }
    finally {
      bufferPollInFlight = false
    }
  }

  function startBufferingPolling() {
    stopBufferingPolling()
    const generation = ++bufferPollingGeneration
    void refreshBufferingState(generation)
    bufferPollingTimer = window.setInterval(() => {
      void refreshBufferingState(generation)
    }, BUFFER_POLL_INTERVAL_MS)
  }

  function stopBufferingPolling() {
    bufferPollingGeneration += 1
    if (bufferPollingTimer) {
      window.clearInterval(bufferPollingTimer)
      bufferPollingTimer = undefined
    }
    resetBufferingState()
  }

  function resetVideoColorState() {
    videoColorGeneration += 1
    videoDynamicRange.value = describeVideoSourceColor(null)
    if (playbackDiagnostics.value)
      playbackDiagnostics.value = { ...playbackDiagnostics.value, color: null }
  }

  function clearTrackRefreshTimers() {
    for (const timer of trackRefreshTimers)
      window.clearTimeout(timer)
    trackRefreshTimers.clear()
  }

  function isCurrentPlaybackGeneration(generation: number): boolean {
    return !disposed && generation === playbackGeneration
  }

  function queuePlaybackTransition(operation: () => Promise<void>): Promise<void> {
    // Native load can await proxy/surface preparation. Keep a newer load or stop behind
    // that call so a cancelled request cannot finish after its replacement or stop.
    const transition = playbackTransitionPromise.then(operation, operation)
    playbackTransitionPromise = transition.catch(() => undefined)
    return transition
  }

  function setKnownSubtitleTracks(tracks: readonly KnownSubtitleTrackInput[]) {
    knownSubtitleTracks.value = tracks.map(toKnownSubtitleTrack)
    if (selectedKnownSubtitle.value && !subtitleTracks.value.some(track => track.id === selectedKnownSubtitle.value)) {
      selectedKnownSubtitle.value = null
      currentSubtitle.value = null
    }
  }

  function setDownloadedSubtitleTracks(subtitles: readonly Pick<LocalSubtitleDownloadResult, 'path' | 'title' | 'language'>[]) {
    const tracks = new Map(downloadedSubtitleTracks.value
      .flatMap(track => track.url ? [[downloadedSubtitleKey(track.url), track] as const] : []))
    for (const subtitle of subtitles) {
      if (!subtitle.path.trim())
        continue
      const key = downloadedSubtitleKey(subtitle.path)
      const previous = tracks.get(key)
      tracks.set(key, toKnownSubtitleTrack({
        id: previous?.id.slice('external:'.length) ?? `loaded:${++loadedExternalSubtitleSequence}`,
        source: 'downloaded',
        title: subtitle.title || previous?.title || '已下载字幕',
        language: subtitle.language || previous?.language || null,
        url: subtitle.path,
        selectable: true,
      }))
    }
    // A cache-list read may have started before the latest download. Merge those
    // choices, and leave selecting/loading any one of them to explicit intent.
    downloadedSubtitleTracks.value = [...tracks.values()]
  }

  async function load(path: string, options: MpvLoadOptions = {}) {
    const generation = ++playbackGeneration
    pauseIntentSequence += 1
    cancelPendingSeek()
    playbackActive = true
    mediaLoadPending = true
    clearTrackRefreshTimers()
    resetVideoColorState()
    playbackDiagnostics.value = null
    stopBufferingPolling()
    subtitleDelayGeneration += 1
    selectedKnownSubtitle.value = null
    downloadedSubtitleTracks.value = []
    sessionExternalSubtitleTracks.value = []
    currentTime.value = 0
    duration.value = 0
    lastConfirmedTime.value = 0
    lastConfirmedDuration.value = 0
    playbackEndReason.value = null
    trackRefreshScheduledForCurrentMedia = false
    trackStateReady.value = false
    embeddedSubtitleTracks.value = []
    audioTracks.value = []
    currentSubtitle.value = null
    currentAudio.value = null
    isPlaying.value = false
    videoReady.value = false
    fileLoaded.value = false
    playbackSpeed.value = DEFAULT_PLAYBACK_SPEED
    videoBrightness.value = 50
    subtitleDelay.value = DEFAULT_SUBTITLE_DELAY
    videoAspectMode.value = 'default'
    videoFitMode.value = 'fit'
    await queuePlaybackTransition(async () => {
      if (!isCurrentPlaybackGeneration(generation))
        return
      try {
        await subtitleDelayCommand.catch(() => undefined)
        if (!isCurrentPlaybackGeneration(generation))
          return
        await applyEngineSettings()
        if (!isCurrentPlaybackGeneration(generation))
          return
        // sid is sticky in mpv; each incoming file starts in Auto before its own intent restores.
        await invoke<void>('mpv_set_property', { prop: 'sid', value: 'auto' })
        if (!isCurrentPlaybackGeneration(generation))
          return
        await invoke<void>('mpv_load', {
          path,
          headers: toMpvHeaderPayload(options.headers),
          audioPath: options.audioUrl,
          audioHeaders: toMpvHeaderPayload(options.audioHeaders),
          title: options.title,
          httpReadProfile: options.httpReadProfile,
        })
        if (!isCurrentPlaybackGeneration(generation))
          return
        mediaLoadPending = false
        startBufferingPolling()
        await invoke<void>('mpv_resume')
        if (!isCurrentPlaybackGeneration(generation))
          return
        if (renderBackend.value === 'androidSurface') {
          await refreshPlaybackDiagnostics()
          if (!isCurrentPlaybackGeneration(generation))
            return
        }
        await refreshOrientationState()
        if (!isCurrentPlaybackGeneration(generation))
          return
        await refreshDisplayBrightness()
        if (!isCurrentPlaybackGeneration(generation))
          return
        isPlaying.value = !playbackEndReason.value
        await applyPlaybackSpeed(playbackSpeed.value)
        if (!isCurrentPlaybackGeneration(generation))
          return
        await applySubtitleDelay(DEFAULT_SUBTITLE_DELAY)
        if (!isCurrentPlaybackGeneration(generation))
          return
        await invoke<void>('mpv_set_property', { prop: 'video-aspect-override', value: ASPECT_PROPERTY_VALUE.default })
        if (!isCurrentPlaybackGeneration(generation))
          return
        await invoke<void>('mpv_set_property', { prop: 'panscan', value: FIT_PROPERTY_VALUE.fit })
        if (!isCurrentPlaybackGeneration(generation))
          return
        await invoke<void>('mpv_set_property', { prop: 'brightness', value: '0' })
        if (!isCurrentPlaybackGeneration(generation))
          return
        await refreshPlaybackDiagnostics()
        if (isCurrentPlaybackGeneration(generation))
          scheduleTrackRefresh(2500)
      }
      catch (error) {
        if (!isCurrentPlaybackGeneration(generation))
          return
        mediaLoadPending = false
        playbackActive = false
        resetVideoColorState()
        stopBufferingPolling()
        throw error
      }
    })
  }

  async function togglePause() {
    const generation = playbackGeneration
    const intent = ++pauseIntentSequence
    const playing = !isPlaying.value
    isPlaying.value = playing
    await queuePlaybackTransition(async () => {
      if (!isCurrentPlaybackGeneration(generation) || intent !== pauseIntentSequence)
        return
      await invoke<void>(playing ? 'mpv_resume' : 'mpv_pause')
    })
  }

  function seek(position: number, options: { optimistic?: boolean, precision?: MpvSeekPrecision } = {}): Promise<void> {
    if (!Number.isFinite(position) || position < 0)
      return Promise.reject(new Error('播放位置无效。'))
    if (disposed || !playbackActive || mediaLoadPending)
      return Promise.resolve()
    const target = Math.max(0, Math.min(duration.value || Number.MAX_SAFE_INTEGER, position))
    const sequence = ++seekSequence
    const precision = options.precision ?? 'exact'
    pendingSeekRequest?.resolve()
    seekSettlement = { sequence, position: target, precision, acknowledged: false, baselineSerial: null }
    seekPending.value = true
    if (options.optimistic !== false)
      currentTime.value = target
    updateWaitingPresentation()
    const submitted = new Promise<void>((resolve, reject) => {
      pendingSeekRequest = { sequence, generation: playbackGeneration, position: target, precision, resolve, reject }
    })
    void dispatchPendingSeeks()
    return submitted
  }

  async function dispatchPendingSeeks() {
    if (seekDispatchInFlight)
      return
    seekDispatchInFlight = true
    try {
      while (pendingSeekRequest) {
        const request = pendingSeekRequest
        pendingSeekRequest = null
        try {
          await queuePlaybackTransition(async () => {
            // Check inside the load/stop executor. A check after await alone
            // cannot retract an obsolete seek already applied to a new source.
            if (!isCurrentPlaybackGeneration(request.generation) || request.sequence !== seekSequence)
              return
            const receipt = await invoke<MpvSeekReceipt>('mpv_seek', { position: request.position, precision: request.precision })
            if (isCurrentPlaybackGeneration(request.generation) && seekSettlement?.sequence === request.sequence) {
              if (receipt.completed) {
                // Native skipped a genuine ready-position no-op; no seek/restart
                // event is required for a command that was never submitted.
                seekSettlement = null
                seekPending.value = false
                updateWaitingPresentation()
              }
              else {
                seekSettlement.acknowledged = true
                seekSettlement.baselineSerial = receipt.seekSerial
                void refreshBufferingState(bufferPollingGeneration)
              }
            }
          })
          request.resolve()
        }
        catch (error) {
          if (isCurrentPlaybackGeneration(request.generation) && seekSettlement?.sequence === request.sequence) {
            seekSettlement = null
            seekPending.value = false
            updateWaitingPresentation()
          }
          request.reject(error)
        }
      }
    }
    finally {
      seekDispatchInFlight = false
    }
  }

  function cancelPendingSeek() {
    seekSequence += 1
    pendingSeekRequest?.resolve()
    pendingSeekRequest = null
    seekSettlement = null
    seekPending.value = false
    updateWaitingPresentation()
  }

  async function seekRelative(offset: number) {
    const next = Math.max(0, Math.min(duration.value || Number.MAX_SAFE_INTEGER, currentTime.value + offset))
    await seek(next)
  }

  async function setVolume(vol: number) {
    const next = Math.max(0, Math.min(100, vol))
    await invoke<void>('mpv_set_property', { prop: 'volume', value: next.toString() })
    volume.value = next
    isMuted.value = next === 0
  }

  async function setVideoBrightness(level: number) {
    const next = Math.max(0, Math.min(100, level))
    const mpvBrightness = Math.round((next - 50) * 2)
    await invoke<void>('mpv_set_property', { prop: 'brightness', value: mpvBrightness.toString() })
    videoBrightness.value = next
  }

  async function applyPlaybackSpeed(rate: number) {
    const next = normalizePlaybackSpeed(rate)
    await invoke<void>('mpv_set_property', { prop: 'speed', value: next.toString() })
    playbackSpeed.value = next
  }

  async function setPlaybackSpeed(rate: number) {
    await applyPlaybackSpeed(rate)
  }

  async function applySubtitleDelay(delay: number) {
    const next = normalizeSubtitleDelay(delay)
    subtitleDelay.value = next
    await invoke<void>('mpv_set_property', { prop: 'sub-delay', value: next.toString() })
  }

  async function setSubtitleDelay(delay: number) {
    const next = normalizeSubtitleDelay(delay)
    const generation = subtitleDelayGeneration
    subtitleDelay.value = next
    subtitleDelayCommand = subtitleDelayCommand
      .catch(() => undefined)
      .then(() => generation === subtitleDelayGeneration
        ? invoke<void>('mpv_set_property', { prop: 'sub-delay', value: next.toString() })
        : undefined)
    await subtitleDelayCommand
  }

  async function setSubtitleAutomatic() {
    const generation = playbackGeneration
    await queuePlaybackTransition(async () => {
      if (!isCurrentPlaybackGeneration(generation))
        return
      await invoke<void>('mpv_set_property', { prop: 'sid', value: 'auto' })
      if (!isCurrentPlaybackGeneration(generation))
        return
      selectedKnownSubtitle.value = null
      currentSubtitle.value = embeddedSubtitleTracks.value.find(track => track.selected)?.id ?? null
      trackError.value = null
    })
  }

  async function setSubtitle(trackId: SubtitleSelectionId | null) {
    const generation = playbackGeneration
    await queuePlaybackTransition(async () => {
      if (isCurrentPlaybackGeneration(generation))
        await applySubtitleSelection(trackId, generation)
    })
  }

  async function applySubtitleSelection(trackId: SubtitleSelectionId | null, generation: number) {
    if (trackId === null) {
      await invoke<void>('mpv_set_property', { prop: 'sid', value: 'no' })
      if (!isCurrentPlaybackGeneration(generation))
        return
      selectedKnownSubtitle.value = null
      currentSubtitle.value = null
      return
    }

    const embeddedId = parseEmbeddedSubtitleId(trackId)
    if (embeddedId !== null) {
      await invoke<void>('mpv_set_property', { prop: 'sid', value: embeddedId.toString() })
      if (!isCurrentPlaybackGeneration(generation))
        return
      selectedKnownSubtitle.value = null
      currentSubtitle.value = trackId
      return
    }

    const track = subtitleTracks.value.find(item => item.id === trackId)
    if (!track || !track.selectable || !track.url) {
      const message = track?.unavailableReason ?? '该字幕暂不可加载。'
      trackError.value = message
      throw new Error(message)
    }

    try {
      await invoke<void>('mpv_add_subtitle', {
        url: track.url,
        title: track.title ?? track.language ?? '外部字幕',
        language: track.language ?? null,
        headers: toMpvHeaderPayload(track.headers),
      })
      if (!isCurrentPlaybackGeneration(generation))
        return
      selectedKnownSubtitle.value = trackId
      currentSubtitle.value = trackId
      trackError.value = null
    }
    catch (error: unknown) {
      trackError.value = safeErrorMessage(error, '外部字幕加载失败')
      throw error
    }
  }

  async function addExternalSubtitle(url: string, title: string, language?: string, source: 'downloaded' | 'provider' = 'downloaded', headers?: Readonly<Record<string, string>>) {
    const generation = playbackGeneration
    await queuePlaybackTransition(async () => {
      if (isCurrentPlaybackGeneration(generation))
        await applyExternalSubtitle(url, title, language, source, generation, headers)
    })
  }

  async function applyExternalSubtitle(url: string, title: string, language: string | undefined, source: 'downloaded' | 'provider', generation: number, headers?: Readonly<Record<string, string>>) {
    trackError.value = null
    try {
      await invoke<void>('mpv_add_subtitle', {
        url,
        title: title || language || '外部字幕',
        language: language || null,
        headers: toMpvHeaderPayload(headers),
      })
      if (!isCurrentPlaybackGeneration(generation))
        return
      let loadedTrack: SubtitleTrackOption
      if (source === 'downloaded') {
        setDownloadedSubtitleTracks([{ path: url, title, language: language || '' }])
        const downloaded = downloadedSubtitleTracks.value.find(track => track.url
          && downloadedSubtitleKey(track.url) === downloadedSubtitleKey(url))
        if (!downloaded)
          throw new Error('字幕缓存路径无效。')
        loadedTrack = downloaded
      }
      else {
        const previous = sessionExternalSubtitleTracks.value.find(track => track.url === url)
        loadedTrack = toKnownSubtitleTrack({
          id: previous?.id.slice('external:'.length) ?? `loaded:${++loadedExternalSubtitleSequence}`,
          source,
          language: language || null,
          title: title || language || '外部字幕',
          url,
          headers,
          selectable: true,
        })
        sessionExternalSubtitleTracks.value = [
          ...sessionExternalSubtitleTracks.value.filter(track => track.id !== loadedTrack.id),
          loadedTrack,
        ]
      }
      selectedKnownSubtitle.value = loadedTrack.id
      currentSubtitle.value = loadedTrack.id
    }
    catch (error) {
      trackError.value = safeErrorMessage(error, '外部字幕加载失败')
      throw error
    }
  }

  async function setAudio(trackId: number) {
    const generation = playbackGeneration
    await queuePlaybackTransition(async () => {
      if (!isCurrentPlaybackGeneration(generation))
        return
      await invoke<void>('mpv_set_property', { prop: 'aid', value: trackId.toString() })
      if (isCurrentPlaybackGeneration(generation))
        currentAudio.value = trackId
    })
  }

  async function setVideoAspect(mode: VideoAspectMode) {
    await invoke<void>('mpv_set_property', { prop: 'video-aspect-override', value: ASPECT_PROPERTY_VALUE[mode] })
    videoAspectMode.value = mode
  }

  async function setVideoFit(mode: VideoFitMode) {
    await invoke<void>('mpv_set_property', { prop: 'panscan', value: FIT_PROPERTY_VALUE[mode] })
    videoFitMode.value = mode
  }

  async function stop() {
    const generation = ++playbackGeneration
    pauseIntentSequence += 1
    cancelPendingSeek()
    playbackActive = false
    mediaLoadPending = false
    clearTrackRefreshTimers()
    resetVideoColorState()
    playbackDiagnostics.value = null
    stopBufferingPolling()
    await queuePlaybackTransition(() => invoke<void>('mpv_stop'))
    if (!isCurrentPlaybackGeneration(generation))
      return
    isPlaying.value = false
    currentTime.value = 0
    duration.value = 0
    lastConfirmedTime.value = 0
    lastConfirmedDuration.value = 0
    playbackEndReason.value = null
    embeddedSubtitleTracks.value = []
    knownSubtitleTracks.value = []
    downloadedSubtitleTracks.value = []
    sessionExternalSubtitleTracks.value = []
    audioTracks.value = []
    currentSubtitle.value = null
    selectedKnownSubtitle.value = null
    currentAudio.value = null
    trackStateReady.value = false
    videoReady.value = false
    fileLoaded.value = false
  }

  onUnmounted(() => {
    disposed = true
    playbackGeneration += 1
    cancelPendingSeek()
    resetVideoColorState()
    stopBufferingPolling()
    if (renderDiagnosticsTimer)
      window.clearInterval(renderDiagnosticsTimer)

    clearTrackRefreshTimers()

    for (const promise of unlistenPromises) {
      promise.then(unlisten => unlisten())
    }
  })

  return {
    isPlaying,
    currentTime,
    duration,
    playbackEndReason,
    lastConfirmedTime,
    lastConfirmedDuration,
    volume,
    isMuted,
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
    fileLoaded,
    isBuffering,
    isPlaybackWaiting,
    bufferingKind,
    bufferPercent,
    bufferSpeedBytesPerSecond,
    orientationSupported,
    orientationMode,
    videoDynamicRange,
    videoTargetDynamicRange,
    trackError,
    applyEngineSettings,
    refreshPlaybackDiagnostics,
    initializeRender,
    updateRenderSurfaceBounds,
    setRenderStrategy,
    refreshTrackState,
    refreshOrientationState,
    setOrientationMode,
    refreshDisplayBrightness,
    setDisplayBrightness,
    setKnownSubtitleTracks,
    setDownloadedSubtitleTracks,
    load,
    togglePause,
    seek,
    cancelPendingSeek,
    seekRelative,
    setVolume,
    setVideoBrightness,
    applyPlaybackSpeed,
    setPlaybackSpeed,
    applySubtitleDelay,
    setSubtitleDelay,
    setSubtitle,
    setSubtitleAutomatic,
    addExternalSubtitle,
    setAudio,
    setVideoAspect,
    setVideoFit,
    stop,
  }
}

function positiveRate(value: number | null | undefined): number | null {
  return value != null && Number.isFinite(value) && value > 0 ? value : null
}

function toMpvHeaderPayload(headers: Readonly<Record<string, string>> | undefined): MpvHttpHeaderPayload[] | undefined {
  if (!headers)
    return undefined

  const payload = Object.entries(headers)
    .map(([name, value]) => ({ name, value }))
    .filter(header => header.name.trim() && header.value.trim())
  return payload.length > 0 ? payload : undefined
}
