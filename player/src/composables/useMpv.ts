import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'
import { computed, onUnmounted, ref } from 'vue'

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
export type SubtitleTrackSource = 'embedded' | 'external' | 'detail'

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
  selectable?: boolean
  unavailableReason?: string
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
  backend: 'windowsTransparentOverlay' | 'windowsOpenGl' | 'linuxFuture' | 'macosFuture' | 'mobileFuture' | 'unsupported'
  message: string | null
  diagnostics?: MpvRenderDiagnostics | null
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

interface PlaybackSpeedPreference {
  playbackSpeed: number
}

interface SetPlaybackSpeedPreferencePayload extends Record<string, unknown> {
  speed: number
}

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

async function readSavedPlaybackSpeed(): Promise<number> {
  try {
    const preference = await invoke<PlaybackSpeedPreference>('player_get_playback_speed_preference')
    return normalizePlaybackSpeed(preference.playbackSpeed)
  }
  catch {
    // Preference persistence is a convenience only; keep playback usable with session defaults.
    return DEFAULT_PLAYBACK_SPEED
  }
}

async function savePlaybackSpeedPreference(speed: number): Promise<void> {
  try {
    const payload: SetPlaybackSpeedPreferencePayload = { speed }
    await invoke<void>('player_set_playback_speed_preference', payload)
  }
  catch {
    // Avoid noisy UI for non-sensitive preference persistence failures.
  }
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
  const source = track.source ?? (hasUrl ? 'external' : 'detail')

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
  const playbackSpeed = ref(DEFAULT_PLAYBACK_SPEED)
  const embeddedSubtitleTracks = ref<SubtitleTrackOption[]>([])
  const knownSubtitleTracks = ref<SubtitleTrackOption[]>([])
  const subtitleTracks = computed(() => mergeSubtitleTracks(embeddedSubtitleTracks.value, knownSubtitleTracks.value))
  const audioTracks = ref<Track[]>([])
  const currentSubtitle = ref<SubtitleSelectionId | null>(null)
  const selectedKnownSubtitle = ref<SubtitleSelectionId | null>(null)
  const currentAudio = ref<number | null>(null)
  const videoAspectMode = ref<VideoAspectMode>('default')
  const videoFitMode = ref<VideoFitMode>('fit')
  const renderStatus = ref<MpvRenderStatus>('idle')
  const renderError = ref<string | null>(null)
  const renderBackend = ref<MpvRenderState['backend']>('unsupported')
  const renderDiagnostics = ref<MpvRenderDiagnostics | null>(null)
  const trackError = ref<string | null>(null)
  let renderDiagnosticsTimer: number | undefined
  const trackRefreshTimers = new Set<number>()
  let playbackSpeedPreferenceLoaded = false
  let playbackSpeedPreferenceLoading: Promise<void> | null = null
  let playbackSpeedChangedLocally = false

  function ensurePlaybackSpeedPreferenceLoaded(): Promise<void> {
    if (playbackSpeedPreferenceLoaded)
      return Promise.resolve()
    if (playbackSpeedPreferenceLoading)
      return playbackSpeedPreferenceLoading

    playbackSpeedPreferenceLoading = readSavedPlaybackSpeed()
      .then((savedSpeed) => {
        if (!playbackSpeedChangedLocally)
          playbackSpeed.value = savedSpeed
        playbackSpeedPreferenceLoaded = true
      })
      .finally(() => {
        playbackSpeedPreferenceLoading = null
      })

    return playbackSpeedPreferenceLoading
  }

  void ensurePlaybackSpeedPreferenceLoaded()

  const unlistenPromises = [
    listen<{ time: number }>('mpv:time-update', (event) => {
      currentTime.value = event.payload.time
    }),
    listen<{ duration: number }>('mpv:duration-change', (event) => {
      duration.value = event.payload.duration
    }),
    listen('mpv:paused', () => {
      isPlaying.value = false
    }),
    listen('mpv:resumed', () => {
      isPlaying.value = true
    }),
  ]

  function applyRenderState(state: MpvRenderState) {
    renderStatus.value = state.status
    renderBackend.value = state.backend
    renderError.value = state.message
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

  function startRenderDiagnosticsPolling() {
    if (renderDiagnosticsTimer)
      return

    renderDiagnosticsTimer = window.setInterval(() => {
      if (renderStatus.value === 'ready')
        void refreshRenderStatus()
    }, 1000)
  }

  async function initializeRender() {
    renderStatus.value = 'initializing'
    renderError.value = null

    try {
      const state = await invoke<MpvRenderState>('mpv_init_render_surface')
      applyRenderState(state)
      if (state.status === 'ready')
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
    try {
      const state = await invoke<MpvTrackState>('mpv_track_state')
      applyTrackState(state)
    }
    catch (error: unknown) {
      trackError.value = safeErrorMessage(error, '轨道信息暂不可用')
      embeddedSubtitleTracks.value = []
      audioTracks.value = []
      currentSubtitle.value = selectedKnownSubtitle.value
      currentAudio.value = null
    }
  }

  function scheduleTrackRefresh(delay: number) {
    const timer = window.setTimeout(() => {
      trackRefreshTimers.delete(timer)
      void refreshTrackState()
    }, delay)
    trackRefreshTimers.add(timer)
  }

  function setKnownSubtitleTracks(tracks: readonly KnownSubtitleTrackInput[]) {
    knownSubtitleTracks.value = tracks.map(toKnownSubtitleTrack)
    if (selectedKnownSubtitle.value && !knownSubtitleTracks.value.some(track => track.id === selectedKnownSubtitle.value)) {
      selectedKnownSubtitle.value = null
      currentSubtitle.value = null
    }
  }

  async function load(path: string) {
    selectedKnownSubtitle.value = null
    currentTime.value = 0
    duration.value = 0
    isPlaying.value = false
    await ensurePlaybackSpeedPreferenceLoaded()
    await invoke<void>('mpv_load', { path })
    await invoke<void>('mpv_resume')
    currentTime.value = 0
    isPlaying.value = true
    await applyPlaybackSpeed(playbackSpeed.value)
    await refreshTrackState()
    scheduleTrackRefresh(400)
    scheduleTrackRefresh(1200)
  }

  async function togglePause() {
    await invoke<void>(isPlaying.value ? 'mpv_pause' : 'mpv_resume')
    isPlaying.value = !isPlaying.value
  }

  async function seek(position: number) {
    await invoke<void>('mpv_seek', { position })
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

  async function applyPlaybackSpeed(rate: number) {
    const next = normalizePlaybackSpeed(rate)
    await invoke<void>('mpv_set_property', { prop: 'speed', value: next.toString() })
    playbackSpeed.value = next
  }

  async function setPlaybackSpeed(rate: number) {
    playbackSpeedChangedLocally = true
    await applyPlaybackSpeed(rate)
    void savePlaybackSpeedPreference(playbackSpeed.value)
  }

  async function setSubtitle(trackId: SubtitleSelectionId | null) {
    if (trackId === null) {
      selectedKnownSubtitle.value = null
      await invoke<void>('mpv_set_property', { prop: 'sid', value: 'no' })
      currentSubtitle.value = null
      await refreshTrackState()
      return
    }

    const embeddedId = parseEmbeddedSubtitleId(trackId)
    if (embeddedId !== null) {
      selectedKnownSubtitle.value = null
      await invoke<void>('mpv_set_property', { prop: 'sid', value: embeddedId.toString() })
      currentSubtitle.value = trackId
      await refreshTrackState()
      return
    }

    const track = knownSubtitleTracks.value.find(item => item.id === trackId)
    if (!track || !track.selectable || !track.url) {
      trackError.value = track?.unavailableReason ?? '该字幕暂不可加载。'
      return
    }

    try {
      await invoke<void>('mpv_add_subtitle', {
        url: track.url,
        title: track.title ?? track.language ?? '外部字幕',
        language: track.language ?? null,
      })
      selectedKnownSubtitle.value = trackId
      currentSubtitle.value = trackId
      trackError.value = null
      await refreshTrackState()
    }
    catch (error: unknown) {
      trackError.value = safeErrorMessage(error, '外部字幕加载失败')
    }
  }

  async function setAudio(trackId: number) {
    await invoke<void>('mpv_set_property', { prop: 'aid', value: trackId.toString() })
    currentAudio.value = trackId
    await refreshTrackState()
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
    await invoke<void>('mpv_pause')
    isPlaying.value = false
  }

  onUnmounted(() => {
    if (renderDiagnosticsTimer)
      window.clearInterval(renderDiagnosticsTimer)

    for (const timer of trackRefreshTimers)
      window.clearTimeout(timer)
    trackRefreshTimers.clear()

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
  }
}
