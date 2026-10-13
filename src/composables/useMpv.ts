import type { MpvColorDiagnostics, VideoDynamicRangeState } from '@/services/videoColor'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'
import { computed, onUnmounted, ref } from 'vue'
import { loadPlayerInteractionSettings } from '@/services/playerInteractionSettings'
import { describeVideoSourceColor, describeVideoTargetColor, normalizeDolbyVisionProfile, normalizeVideoColorMetadata } from '@/services/videoColor'

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
  lastEvent: string
  lastError?: string | null
  fileLoaded: boolean
  videoFormat?: string | null
  audioCodec?: string | null
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

export function useMpv() {
  const isPlaying = ref(false)
  const currentTime = ref(0)
  const duration = ref(0)
  const volume = ref(100)
  const isMuted = ref(false)
  const videoBrightness = ref(50)
  const displayBrightness = ref(50)
  const displayBrightnessSupported = ref(false)
  const playbackSpeed = ref(DEFAULT_PLAYBACK_SPEED)
  const subtitleDelay = ref(DEFAULT_SUBTITLE_DELAY)
  const embeddedSubtitleTracks = ref<SubtitleTrackOption[]>([])
  const knownSubtitleTracks = ref<SubtitleTrackOption[]>([])
  const subtitleTracks = computed(() => mergeSubtitleTracks(embeddedSubtitleTracks.value, knownSubtitleTracks.value))
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
  let engineReportsBuffering = false
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

  const unlistenPromises = [
    listen<{ time: number }>('mpv:time-update', (event) => {
      currentTime.value = event.payload.time
    }),
    listen<{ duration: number }>('mpv:duration-change', (event) => {
      duration.value = event.payload.duration
      if (event.payload.duration > 0 && !trackRefreshScheduledForCurrentMedia) {
        trackRefreshScheduledForCurrentMedia = true
        scheduleTrackRefresh(150)
      }
    }),
    listen('mpv:paused', () => {
      isPlaying.value = false
    }),
    listen('mpv:resumed', () => {
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
    try {
      const diagnostics = await invoke<MpvPlaybackDiagnostics>('mpv_playback_diagnostics')
      if (disposed || generation !== playbackGeneration)
        return

      const currentColor = playbackActive && !mediaLoadPending && diagnostics.fileLoaded && diagnostics.voConfigured && colorGeneration === videoColorGeneration
      playbackDiagnostics.value = { ...diagnostics, color: currentColor ? diagnostics.color : null }
      if (currentColor) {
        if (diagnostics.color === undefined)
          await refreshSourceColorFromProperties(generation, colorGeneration)
        else
          videoDynamicRange.value = describeVideoSourceColor(diagnostics.color)
      }
      else {
        videoDynamicRange.value = describeVideoSourceColor(null)
      }
      if (disposed || generation !== playbackGeneration)
        return
      if (playbackActive && !mediaLoadPending && diagnostics.state !== 'desktop')
        fileLoaded.value = diagnostics.fileLoaded
      if (playbackActive && !mediaLoadPending && diagnostics.state !== 'desktop' && diagnostics.fileLoaded && (diagnostics.videoFormat || diagnostics.state === 'playing'))
        videoReady.value = true
      if (!mediaLoadPending && (diagnostics.state === 'error' || diagnostics.state === 'ended' || diagnostics.state === 'idle')) {
        isPlaying.value = false
        resetVideoColorState()
        if (diagnostics.state === 'error')
          videoReady.value = false
      }
    }
    catch {
      if (!disposed && generation === playbackGeneration) {
        playbackDiagnostics.value = null
        resetVideoColorState()
      }
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

  async function readMpvProperty(prop: string): Promise<string> {
    try {
      return await invoke<string>('mpv_get_property', { prop })
    }
    catch {
      return ''
    }
  }

  function parseMpvBoolean(value: string): boolean {
    return ['1', 'true', 'yes', 'on'].includes(value.trim().toLowerCase())
  }

  function clearBufferDisplayTimer() {
    if (!bufferDisplayTimer)
      return
    window.clearTimeout(bufferDisplayTimer)
    bufferDisplayTimer = undefined
  }

  function resetBufferingState() {
    engineReportsBuffering = false
    clearBufferDisplayTimer()
    isBuffering.value = false
    bufferSpeedBytesPerSecond.value = 0
  }

  async function refreshBufferingState(generation: number) {
    if (bufferPollInFlight)
      return

    bufferPollInFlight = true
    try {
      const [pausedForCache, cacheSpeed] = await Promise.all([
        readMpvProperty('paused-for-cache'),
        readMpvProperty('cache-speed'),
      ])
      if (generation !== bufferPollingGeneration)
        return

      const buffering = parseMpvBoolean(pausedForCache)
      const speed = Number.parseFloat(cacheSpeed)
      bufferSpeedBytesPerSecond.value = Number.isFinite(speed) && speed > 0 ? speed : 0
      engineReportsBuffering = buffering

      if (!buffering) {
        resetBufferingState()
        return
      }

      if (!isBuffering.value && !bufferDisplayTimer) {
        bufferDisplayTimer = window.setTimeout(() => {
          bufferDisplayTimer = undefined
          if (generation === bufferPollingGeneration && engineReportsBuffering)
            isBuffering.value = true
        }, BUFFER_DISPLAY_DELAY_MS)
      }
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

  async function refreshSourceColorFromProperties(generation: number, colorGeneration: number) {
    const [gamma, primaries, pixelFormat, hardwarePixelFormat, doviProfile] = await Promise.all([
      readMpvProperty('video-params/gamma'),
      readMpvProperty('video-params/primaries'),
      readMpvProperty('video-params/pixelformat'),
      readMpvProperty('video-params/hw-pixelformat'),
      readMpvProperty('current-tracks/video/dolby-vision-profile'),
    ])
    if (disposed || !playbackActive || mediaLoadPending || generation !== playbackGeneration || colorGeneration !== videoColorGeneration)
      return
    videoDynamicRange.value = describeVideoSourceColor({
      sourceGamma: normalizeVideoColorMetadata(gamma),
      sourcePrimaries: normalizeVideoColorMetadata(primaries),
      sourcePixelFormat: normalizeVideoColorMetadata(pixelFormat),
      sourceHardwarePixelFormat: normalizeVideoColorMetadata(hardwarePixelFormat),
      dolbyVisionProfile: normalizeDolbyVisionProfile(doviProfile),
    })
  }

  function setKnownSubtitleTracks(tracks: readonly KnownSubtitleTrackInput[]) {
    knownSubtitleTracks.value = tracks.map(toKnownSubtitleTrack)
    if (selectedKnownSubtitle.value && !knownSubtitleTracks.value.some(track => track.id === selectedKnownSubtitle.value)) {
      selectedKnownSubtitle.value = null
      currentSubtitle.value = null
    }
  }

  async function load(path: string, options: MpvLoadOptions = {}) {
    const generation = ++playbackGeneration
    playbackActive = true
    mediaLoadPending = true
    clearTrackRefreshTimers()
    resetVideoColorState()
    playbackDiagnostics.value = null
    stopBufferingPolling()
    subtitleDelayGeneration += 1
    selectedKnownSubtitle.value = null
    currentTime.value = 0
    duration.value = 0
    trackRefreshScheduledForCurrentMedia = false
    trackStateReady.value = false
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
        await invoke<void>('mpv_load', {
          path,
          headers: toMpvHeaderPayload(options.headers),
          audioPath: options.audioUrl,
          audioHeaders: toMpvHeaderPayload(options.audioHeaders),
          title: options.title,
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
        currentTime.value = 0
        isPlaying.value = true
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
    await invoke<void>(isPlaying.value ? 'mpv_pause' : 'mpv_resume')
    isPlaying.value = !isPlaying.value
  }

  async function seek(position: number, options: { optimistic?: boolean } = {}) {
    await invoke<void>('mpv_seek', { position })
    if (options.optimistic !== false)
      currentTime.value = position
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

  async function setSubtitle(trackId: SubtitleSelectionId | null) {
    if (trackId === null) {
      selectedKnownSubtitle.value = null
      await invoke<void>('mpv_set_property', { prop: 'sid', value: 'no' })
      currentSubtitle.value = null
      return
    }

    const embeddedId = parseEmbeddedSubtitleId(trackId)
    if (embeddedId !== null) {
      selectedKnownSubtitle.value = null
      await invoke<void>('mpv_set_property', { prop: 'sid', value: embeddedId.toString() })
      currentSubtitle.value = trackId
      return
    }

    const track = knownSubtitleTracks.value.find(item => item.id === trackId)
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
      selectedKnownSubtitle.value = trackId
      currentSubtitle.value = trackId
      trackError.value = null
    }
    catch (error: unknown) {
      trackError.value = safeErrorMessage(error, '外部字幕加载失败')
      throw error
    }
  }

  async function addExternalSubtitle(url: string, title: string, language?: string, source: 'downloaded' | 'provider' = 'downloaded') {
    trackError.value = null
    try {
      await invoke<void>('mpv_add_subtitle', {
        url,
        title: title || language || '外部字幕',
        language: language || null,
        headers: [],
      })
      const loadedTrack = toKnownSubtitleTrack({
        id: `loaded:${++loadedExternalSubtitleSequence}`,
        source,
        language: language || null,
        title: title || language || '外部字幕',
        url,
        selectable: true,
      })
      knownSubtitleTracks.value = [
        ...knownSubtitleTracks.value.filter(track => !String(track.id).startsWith('external:loaded:')),
        loadedTrack,
      ]
      selectedKnownSubtitle.value = loadedTrack.id
      currentSubtitle.value = loadedTrack.id
    }
    catch (error) {
      trackError.value = safeErrorMessage(error, '外部字幕加载失败')
      throw error
    }
  }

  async function setAudio(trackId: number) {
    await invoke<void>('mpv_set_property', { prop: 'aid', value: trackId.toString() })
    currentAudio.value = trackId
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
    embeddedSubtitleTracks.value = []
    knownSubtitleTracks.value = []
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
    load,
    togglePause,
    seek,
    seekRelative,
    setVolume,
    setVideoBrightness,
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
  }
}

function toMpvHeaderPayload(headers: Readonly<Record<string, string>> | undefined): MpvHttpHeaderPayload[] | undefined {
  if (!headers)
    return undefined

  const payload = Object.entries(headers)
    .map(([name, value]) => ({ name, value }))
    .filter(header => header.name.trim() && header.value.trim())
  return payload.length > 0 ? payload : undefined
}
