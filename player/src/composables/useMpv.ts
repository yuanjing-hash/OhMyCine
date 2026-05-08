import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'
import { onUnmounted, ref } from 'vue'

interface Track {
  index: number
  language: string
  title?: string
  codec?: string
  channels?: number
  isDefault: boolean
}

export type MpvRenderStatus = 'idle' | 'initializing' | 'ready' | 'unsupported' | 'error'

export interface MpvRenderState {
  status: MpvRenderStatus
  backend: 'windowsOpenGl' | 'linuxFuture' | 'macosFuture' | 'mobileFuture' | 'unsupported'
  message: string | null
}

export function useMpv() {
  const isPlaying = ref(false)
  const currentTime = ref(0)
  const duration = ref(0)
  const volume = ref(100)
  const isMuted = ref(false)
  const subtitleTracks = ref<Track[]>([])
  const audioTracks = ref<Track[]>([])
  const currentSubtitle = ref(0)
  const currentAudio = ref(0)
  const renderStatus = ref<MpvRenderStatus>('idle')
  const renderError = ref<string | null>(null)
  const renderBackend = ref<MpvRenderState['backend']>('unsupported')

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

  async function initializeRender() {
    renderStatus.value = 'initializing'
    renderError.value = null

    try {
      const state = await invoke<MpvRenderState>('mpv_render_status')
      renderStatus.value = state.status
      renderBackend.value = state.backend
      renderError.value = state.message
    }
    catch (error: unknown) {
      renderStatus.value = 'error'
      renderError.value = error instanceof Error ? error.message : String(error)
    }
  }

  async function load(path: string) {
    await invoke('mpv_load', { path })
    isPlaying.value = true
  }

  async function togglePause() {
    await invoke(isPlaying.value ? 'mpv_pause' : 'mpv_resume')
    isPlaying.value = !isPlaying.value
  }

  async function seek(position: number) {
    await invoke('mpv_seek', { position })
    currentTime.value = position
  }

  async function seekRelative(offset: number) {
    const next = Math.max(0, Math.min(duration.value || Number.MAX_SAFE_INTEGER, currentTime.value + offset))
    await seek(next)
  }

  async function setVolume(vol: number) {
    const next = Math.max(0, Math.min(100, vol))
    await invoke('mpv_set_property', { prop: 'volume', value: next.toString() })
    volume.value = next
    isMuted.value = next === 0
  }

  async function setSubtitle(index: number) {
    await invoke('mpv_set_property', { prop: 'sid', value: index.toString() })
    currentSubtitle.value = index
  }

  async function setAudio(index: number) {
    await invoke('mpv_set_property', { prop: 'aid', value: index.toString() })
    currentAudio.value = index
  }

  async function stop() {
    await invoke('mpv_pause')
    isPlaying.value = false
  }

  onUnmounted(() => {
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
    subtitleTracks,
    audioTracks,
    currentSubtitle,
    currentAudio,
    renderStatus,
    renderError,
    renderBackend,
    initializeRender,
    load,
    togglePause,
    seek,
    seekRelative,
    setVolume,
    setSubtitle,
    setAudio,
    stop,
  }
}
