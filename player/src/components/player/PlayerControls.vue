<script setup lang="ts">
import type { MpvOrientationMode, SubtitleSelectionId, SubtitleTrackOption, Track, VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import type { PlaybackQueueItem } from '@/services/playbackContext'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { PLAYBACK_SPEED_OPTIONS } from '@/services/playerInteractionSettings'
import { transitionWindowFullscreen } from '@/services/windowFullscreen'
import PlayerSettingsPanel from './PlayerSettingsPanel.vue'
import ProgressBar from './ProgressBar.vue'
import VolumeControl from './VolumeControl.vue'

type ControlMenu = 'speed' | 'subtitle' | 'audio' | 'queue' | 'orientation'

const props = defineProps<{
  isPlaying: boolean
  currentTime: number
  duration: number
  volume: number
  playbackSpeed: number
  subtitleDelay: number
  subtitleTracks: readonly SubtitleTrackOption[]
  audioTracks: readonly Track[]
  queueItemCount: number
  queueItems: readonly PlaybackQueueItem[]
  currentQueueIndex: number
  isQueueSwitching: boolean
  canPlayPrevious: boolean
  canPlayNext: boolean
  currentSubtitle: SubtitleSelectionId | null
  currentAudio: number | null
  videoAspectMode: VideoAspectMode
  videoFitMode: VideoFitMode
  videoBrightness: number
  trackError: string | null
  pictureSettingsError: string | null
  mobileLayout: boolean
  orientationSupported: boolean
  orientationMode: MpvOrientationMode
}>()

const emit = defineEmits<{
  playPrevious: []
  togglePause: []
  playNext: []
  selectQueueItem: [index: number]
  seek: [position: number]
  seekRelative: [offset: number]
  setVolume: [volume: number]
  setPlaybackSpeed: [speed: number]
  setSubtitleDelay: [delay: number]
  setSubtitle: [trackId: SubtitleSelectionId | null]
  loadLocalSubtitle: []
  searchSubtitles: []
  setAudio: [trackId: number]
  setVideoAspect: [mode: VideoAspectMode]
  setVideoFit: [mode: VideoFitMode]
  setVideoBrightness: [level: number]
  setOrientationMode: [mode: MpvOrientationMode]
  fullscreenChanged: [fullscreen: boolean]
  interactionChange: [active: boolean]
}>()

const appWindow = isTauriRuntime() ? getCurrentWindow() : null
const controlsRoot = ref<HTMLElement | null>(null)
const settingsButton = ref<HTMLButtonElement | null>(null)
const pointerInside = ref(false)
const focusInside = ref(false)
const childInteracting = ref(false)
const activeMenu = ref<ControlMenu | null>(null)
const settingsPanelOpen = ref(false)
const settingsPanelInteracting = ref(false)
const isFullscreen = ref(false)
const fullscreenBusy = ref(false)
const fullscreenError = ref<string | null>(null)
let restoreMaximizedOnExit = false
let disposed = false
let pointerLeaveTimer: number | undefined
let pointerOwnsFocus = false
const windowEventUnlisteners: Array<() => void> = []

const fullscreenTitle = computed(() => {
  if (fullscreenError.value)
    return `全屏切换暂不可用：${fullscreenError.value}`
  return isFullscreen.value ? '退出全屏' : '进入全屏'
})

const speedLabel = computed(() => `${formatSpeed(props.playbackSpeed)}x`)
const subtitleLabel = computed(() => {
  if (props.currentSubtitle === null)
    return '字幕关'
  const track = props.subtitleTracks.find(item => item.id === props.currentSubtitle)
  return track ? compactTrackLabel(track, '字幕') : '字幕'
})
const audioLabel = computed(() => {
  const track = props.audioTracks.find(item => item.id === props.currentAudio)
  return track ? compactTrackLabel(track, '音轨') : '音轨'
})
const showAudioControl = computed(() => props.audioTracks.length > 1)
const showQueueControl = computed(() => props.queueItemCount > 1)
const queueLabel = computed(() => `${Math.max(0, props.currentQueueIndex + 1)}/${props.queueItemCount}`)
const orientationLabel = computed(() => {
  if (props.orientationMode === 'landscape')
    return '锁定横屏'
  if (props.orientationMode === 'portrait')
    return '锁定竖屏'
  return '自动横屏'
})
const downloadedSubtitleTracks = computed(() => props.subtitleTracks.filter(track => track.source === 'downloaded'))
const mediaSubtitleTracks = computed(() => props.subtitleTracks.filter(track => track.source !== 'downloaded'))

function isInteracting() {
  return pointerInside.value
    || focusInside.value
    || childInteracting.value
    || activeMenu.value !== null
    || settingsPanelOpen.value
    || settingsPanelInteracting.value
    || fullscreenBusy.value
}

function emitInteractionState() {
  emit('interactionChange', isInteracting())
}

function setPointerInside(next: boolean) {
  pointerInside.value = next
  emitInteractionState()
}

function clearPointerLeaveTimer() {
  if (!pointerLeaveTimer)
    return
  window.clearTimeout(pointerLeaveTimer)
  pointerLeaveTimer = undefined
}

function handlePointerEnter() {
  clearPointerLeaveTimer()
  setPointerInside(true)
}

function handlePointerLeave() {
  setPointerInside(false)
  clearPointerLeaveTimer()
  pointerLeaveTimer = window.setTimeout(() => {
    pointerLeaveTimer = undefined
    if (pointerInside.value)
      return
    if (childInteracting.value)
      return
    activeMenu.value = null
    settingsPanelOpen.value = false
    settingsPanelInteracting.value = false
    if (pointerOwnsFocus) {
      focusInside.value = false
      const activeElement = document.activeElement
      if (activeElement instanceof HTMLElement && controlsRoot.value?.contains(activeElement))
        activeElement.blur()
    }
    pointerOwnsFocus = false
    emitInteractionState()
  }, 160)
}

function markPointerInteraction() {
  pointerOwnsFocus = true
}

function setFocusInside(next: boolean) {
  focusInside.value = next
  emitInteractionState()
}

function setChildInteracting(next: boolean) {
  childInteracting.value = next
  emitInteractionState()
}

function setSettingsPanelInteracting(next: boolean) {
  settingsPanelInteracting.value = next
  emitInteractionState()
}

function toggleMenu(menu: ControlMenu) {
  settingsPanelOpen.value = false
  settingsPanelInteracting.value = false
  activeMenu.value = activeMenu.value === menu ? null : menu
  emitInteractionState()
}

function closeMenus() {
  activeMenu.value = null
  emitInteractionState()
}

function dismissTransientUi() {
  clearPointerLeaveTimer()
  pointerInside.value = false
  focusInside.value = false
  childInteracting.value = false
  activeMenu.value = null
  settingsPanelOpen.value = false
  settingsPanelInteracting.value = false
  pointerOwnsFocus = false
  const activeElement = document.activeElement
  if (activeElement instanceof HTMLElement && controlsRoot.value?.contains(activeElement))
    activeElement.blur()
  emitInteractionState()
}

function toggleSettingsPanel() {
  activeMenu.value = null
  settingsPanelOpen.value = !settingsPanelOpen.value
  emitInteractionState()
}

async function closeSettingsPanel() {
  settingsPanelOpen.value = false
  settingsPanelInteracting.value = false
  emitInteractionState()
  await nextTick()
  settingsButton.value?.focus()
}

function chooseSpeed(speed: number) {
  emit('setPlaybackSpeed', speed)
  closeMenus()
}

function chooseOrientation(mode: MpvOrientationMode) {
  emit('setOrientationMode', mode)
  activeMenu.value = null
}

function chooseSubtitle(trackId: SubtitleSelectionId | null) {
  emit('setSubtitle', trackId)
  closeMenus()
}

function adjustSubtitleDelay(delta: number) {
  emit('setSubtitleDelay', props.subtitleDelay + delta)
}

function updateSubtitleDelay(event: Event) {
  emit('setSubtitleDelay', Number.parseFloat((event.target as HTMLInputElement).value))
}

function formatSubtitleDelay(delay: number): string {
  if (Math.abs(delay) < 0.05)
    return '同步 0.0 秒'
  return delay > 0 ? `延后 +${delay.toFixed(1)} 秒` : `提前 ${Math.abs(delay).toFixed(1)} 秒`
}

function openSubtitleSearch() {
  closeMenus()
  emit('searchSubtitles')
}

function openLocalSubtitle() {
  closeMenus()
  emit('loadLocalSubtitle')
}

function chooseAudio(trackId: number) {
  emit('setAudio', trackId)
  closeMenus()
}

function chooseQueueItem(index: number) {
  emit('selectQueueItem', index)
  closeMenus()
}

function queueItemSubtitle(item: PlaybackQueueItem): string {
  const episodeParts = [
    typeof item.seasonNumber === 'number' ? `S${item.seasonNumber.toString().padStart(2, '0')}` : undefined,
    typeof item.episodeNumber === 'number' ? `E${item.episodeNumber.toString().padStart(2, '0')}` : undefined,
  ].filter(Boolean)
  const meta = [episodeParts.join(''), formatDuration(item.duration)].filter(Boolean)
  return meta.join(' · ') || mediaTypeLabel(item.type)
}

function mediaTypeLabel(type: PlaybackQueueItem['type']): string {
  switch (type) {
    case 'episode':
      return '剧集'
    case 'movie':
      return '电影'
    case 'file':
      return '文件'
    default:
      return '队列项目'
  }
}

function formatDuration(seconds: number | undefined): string | undefined {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0)
    return undefined
  return formatTime(seconds)
}

function formatTime(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)
  if (h > 0)
    return `${h}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`
  return `${m}:${s.toString().padStart(2, '0')}`
}

function formatSpeed(speed: number): string {
  return Number.isInteger(speed) ? speed.toFixed(1) : speed.toString()
}

function compactTrackLabel(track: Track | SubtitleTrackOption, fallback: string): string {
  return track.language?.toUpperCase() || track.title || `${fallback} ${track.id}`
}

function fullTrackLabel(track: Track | SubtitleTrackOption): string {
  const parts = [track.title, track.language?.toUpperCase(), track.codec, track.channels ? `${track.channels}ch` : null]
    .filter((part): part is string => Boolean(part))
  return parts.length ? parts.join(' · ') : `轨道 ${track.id}`
}

function subtitleSourceLabel(track: SubtitleTrackOption): string {
  const sourceLabel = track.source === 'downloaded'
    ? '本地下载'
    : track.source === 'embedded'
      ? '视频内嵌'
      : track.source === 'provider'
        ? '媒体源提供'
        : '媒体详情'
  const status = track.isDefault ? ' · 默认' : ''
  if (!track.selectable)
    return `${sourceLabel} · 暂不可加载`
  return `${sourceLabel}${status}`
}

async function syncFullscreenState() {
  if (!appWindow) {
    const nextFullscreen = document.fullscreenElement !== null
    const previousFullscreen = isFullscreen.value
    isFullscreen.value = nextFullscreen
    if (previousFullscreen !== nextFullscreen)
      emit('fullscreenChanged', nextFullscreen)
    return
  }
  try {
    const nextFullscreen = await appWindow.isFullscreen()
    const previousFullscreen = isFullscreen.value
    if (fullscreenBusy.value) {
      isFullscreen.value = nextFullscreen
      return
    }
    if (previousFullscreen && !nextFullscreen && restoreMaximizedOnExit) {
      restoreMaximizedOnExit = false
      await appWindow.maximize()
    }
    isFullscreen.value = nextFullscreen
    fullscreenError.value = null
    if (previousFullscreen !== nextFullscreen)
      emit('fullscreenChanged', nextFullscreen)
  }
  catch {
    const nextFullscreen = document.fullscreenElement !== null
    const previousFullscreen = isFullscreen.value
    isFullscreen.value = nextFullscreen
    if (previousFullscreen !== nextFullscreen)
      emit('fullscreenChanged', nextFullscreen)
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

function isTauriRuntime(): boolean {
  const root = globalThis as { readonly __TAURI_INTERNALS__?: unknown }
  return root.__TAURI_INTERNALS__ != null
}

async function toggleBrowserFullscreen(nextFullscreen: boolean) {
  if (nextFullscreen) {
    if (!document.fullscreenElement)
      await document.documentElement.requestFullscreen()
    return
  }

  if (document.fullscreenElement)
    await document.exitFullscreen()
}

async function toggleFullscreen(silent = false) {
  if (fullscreenBusy.value)
    return

  if (!silent)
    closeMenus()
  fullscreenBusy.value = true
  if (!silent)
    emitInteractionState()
  try {
    if (!appWindow) {
      const nextFullscreen = document.fullscreenElement === null
      await toggleBrowserFullscreen(nextFullscreen)
      isFullscreen.value = nextFullscreen
      fullscreenError.value = null
      emit('fullscreenChanged', nextFullscreen)
      return
    }
    const nextFullscreen = !(await appWindow.isFullscreen())
    const result = await transitionWindowFullscreen(
      appWindow,
      nextFullscreen,
      restoreMaximizedOnExit,
    )
    restoreMaximizedOnExit = result.restoreMaximizedOnExit
    isFullscreen.value = result.fullscreen
    fullscreenError.value = null
    emit('fullscreenChanged', result.fullscreen)
  }
  catch (error) {
    if (isTauriRuntime()) {
      fullscreenError.value = error instanceof Error ? error.message : '窗口全屏状态切换失败'
      return
    }
    try {
      const nextFullscreen = document.fullscreenElement === null
      await toggleBrowserFullscreen(nextFullscreen)
      isFullscreen.value = nextFullscreen
      fullscreenError.value = null
      emit('fullscreenChanged', nextFullscreen)
    }
    catch {
      fullscreenError.value = '当前运行环境不支持窗口全屏切换'
    }
  }
  finally {
    fullscreenBusy.value = false
    if (!silent)
      emitInteractionState()
  }
}

function handleFullscreenButtonClick() {
  void toggleFullscreen(false)
}

async function toggleFullscreenFromShortcut() {
  await toggleFullscreen(true)
}

function handleKeydown(event: KeyboardEvent) {
  pointerOwnsFocus = false
  if (event.key !== 'Escape')
    return

  if (activeMenu.value) {
    event.preventDefault()
    closeMenus()
  }
  else if (settingsPanelOpen.value) {
    event.preventDefault()
    void closeSettingsPanel()
  }
}

watch(showAudioControl, (visible) => {
  if (!visible && activeMenu.value === 'audio')
    closeMenus()
})

watch(showQueueControl, (visible) => {
  if (!visible && activeMenu.value === 'queue')
    closeMenus()
})

onMounted(() => {
  void syncFullscreenState()
  if (appWindow) {
    trackWindowListener(appWindow.onResized(syncFullscreenState))
    trackWindowListener(appWindow.onFocusChanged(syncFullscreenState))
  }
  document.addEventListener('fullscreenchange', syncFullscreenState)
})

onBeforeUnmount(() => {
  disposed = true
  clearPointerLeaveTimer()
  for (const unlisten of windowEventUnlisteners)
    unlisten()
  windowEventUnlisteners.length = 0
  document.removeEventListener('fullscreenchange', syncFullscreenState)
})

defineExpose({ dismissTransientUi, toggleFullscreenFromShortcut })
</script>

<template>
  <div
    ref="controlsRoot"
    data-player-click-ignore
    class="player-controls-glass pointer-events-auto relative mx-auto flex w-full max-w-7xl min-w-0 items-center gap-3 overflow-visible rounded-[28px] px-5 py-3"
    :class="{ 'mobile-layout': mobileLayout }"
    @mouseenter="handlePointerEnter"
    @mouseleave="handlePointerLeave"
    @pointerdown.capture="markPointerInteraction"
    @focusin="setFocusInside(true)"
    @focusout="setFocusInside(false)"
    @keydown="handleKeydown"
  >
    <div class="transport-controls flex shrink-0 items-center gap-2">
      <button class="control-button secondary" :class="{ disabled: !canPlayPrevious }" type="button" :title="canPlayPrevious ? '上一集' : '没有上一集'" :aria-label="canPlayPrevious ? '上一集' : '没有上一集'" :disabled="!canPlayPrevious" @click="emit('playPrevious')">
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 5a1 1 0 0 1 1 1v4.2l8.86-5.01A1.1 1.1 0 0 1 17.5 6.14v11.72a1.1 1.1 0 0 1-1.64.95L7 13.8V18a1 1 0 1 1-2 0V6a1 1 0 0 1 1-1Z" /></svg>
      </button>

      <button class="control-button secondary" type="button" title="后退 10 秒" aria-label="后退 10 秒" @click="emit('seekRelative', -10)">
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M11.4 5.08a1 1 0 0 1 .12 1.41L9.86 8.45H14a6 6 0 1 1-5.66 8H10.6A4 4 0 1 0 14 10.45H9.86l1.66 1.96a1 1 0 1 1-1.52 1.29L6.42 9.47a1 1 0 0 1 0-1.29L10 4.96a1 1 0 0 1 1.4.12Z" />
          <path d="M11.6 13.6h-.95v-.9h2.05V17h-1.1v-3.4Zm3.05-.9h1.15c1.05 0 1.75.86 1.75 2.15 0 1.3-.7 2.15-1.75 2.15h-1.15c-1.06 0-1.76-.86-1.76-2.15 0-1.3.7-2.15 1.76-2.15Zm.1 1c-.45 0-.75.46-.75 1.15 0 .7.3 1.15.75 1.15h.95c.45 0 .75-.46.75-1.15 0-.7-.3-1.15-.75-1.15h-.95Z" />
        </svg>
      </button>

      <button class="control-button primary" type="button" :title="isPlaying ? '暂停' : '播放'" :aria-label="isPlaying ? '暂停' : '播放'" @click="emit('togglePause')">
        <svg v-if="isPlaying" class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5.75A1.75 1.75 0 0 1 9.75 4h.5A1.75 1.75 0 0 1 12 5.75v12.5A1.75 1.75 0 0 1 10.25 20h-.5A1.75 1.75 0 0 1 8 18.25V5.75Zm6 0A1.75 1.75 0 0 1 15.75 4h.5A1.75 1.75 0 0 1 18 5.75v12.5A1.75 1.75 0 0 1 16.25 20h-.5A1.75 1.75 0 0 1 14 18.25V5.75Z" /></svg>
        <svg v-else class="control-icon play-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5.87c0-1.35 1.5-2.16 2.63-1.42l9.2 6.13a1.7 1.7 0 0 1 0 2.84l-9.2 6.13A1.7 1.7 0 0 1 8 18.13V5.87Z" /></svg>
      </button>

      <button class="control-button secondary" type="button" title="前进 10 秒" aria-label="前进 10 秒" @click="emit('seekRelative', 10)">
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12.6 5.08a1 1 0 0 0-.12 1.41l1.66 1.96H10a6 6 0 1 0 5.66 8H13.4A4 4 0 1 1 10 10.45h4.14l-1.66 1.96A1 1 0 1 0 14 13.7l3.58-4.23a1 1 0 0 0 0-1.29L14 4.96a1 1 0 0 0-1.4.12Z" />
          <path d="M6.45 13.6H5.5v-.9h2.05V17h-1.1v-3.4Zm3.05-.9h1.15c1.05 0 1.75.86 1.75 2.15 0 1.3-.7 2.15-1.75 2.15H9.5c-1.06 0-1.76-.86-1.76-2.15 0-1.3.7-2.15 1.76-2.15Zm.1 1c-.45 0-.75.46-.75 1.15 0 .7.3 1.15.75 1.15h.95c.45 0 .75-.46.75-1.15 0-.7-.3-1.15-.75-1.15H9.6Z" />
        </svg>
      </button>

      <button class="control-button secondary" :class="{ disabled: !canPlayNext }" type="button" :title="canPlayNext ? '下一集' : '没有下一集'" :aria-label="canPlayNext ? '下一集' : '没有下一集'" :disabled="!canPlayNext" @click="emit('playNext')">
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M18 5a1 1 0 0 0-1 1v4.2L8.14 5.19A1.1 1.1 0 0 0 6.5 6.14v11.72a1.1 1.1 0 0 0 1.64.95L17 13.8V18a1 1 0 1 0 2 0V6a1 1 0 0 0-1-1Z" /></svg>
      </button>
    </div>

    <span class="time-label current-time-label w-16 shrink-0 text-left">{{ formatTime(currentTime) }}</span>

    <ProgressBar class="player-progress-bar min-w-0 flex-1" :current="currentTime" :total="duration" @seek="(pos) => emit('seek', pos)" @interaction-change="setChildInteracting" />

    <span class="time-label duration-time-label w-16 shrink-0 text-right">{{ formatTime(duration) }}</span>

    <div class="right-controls flex shrink-0 items-center gap-2">
      <VolumeControl class="shrink-0" :volume="volume" @set-volume="(vol) => emit('setVolume', vol)" @interaction-change="setChildInteracting" />

      <div class="control-menu-anchor">
        <button class="control-button action-chip secondary" :class="{ 'is-active': activeMenu === 'speed' }" type="button" title="倍速" aria-label="倍速" aria-haspopup="menu" :aria-expanded="activeMenu === 'speed'" @click="toggleMenu('speed')">
          <span class="control-text">{{ speedLabel }}</span>
        </button>
        <Transition name="control-menu">
          <div v-if="activeMenu === 'speed'" class="control-popover speed-popover" role="menu" aria-label="选择播放速度">
            <button v-for="speed in PLAYBACK_SPEED_OPTIONS" :key="speed" type="button" class="menu-option" :class="{ 'is-selected': Math.abs(playbackSpeed - speed) < 0.001 }" role="menuitemradio" :aria-checked="Math.abs(playbackSpeed - speed) < 0.001" @click="chooseSpeed(speed)">
              {{ formatSpeed(speed) }}x
            </button>
          </div>
        </Transition>
      </div>

      <div class="control-menu-anchor">
        <button class="control-button action-chip secondary" :class="{ 'is-active': activeMenu === 'subtitle' || currentSubtitle !== null }" type="button" title="字幕" aria-label="字幕" aria-haspopup="menu" :aria-expanded="activeMenu === 'subtitle'" @click="toggleMenu('subtitle')">
          <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M4.75 5.5h14.5A2.75 2.75 0 0 1 22 8.25v7.5a2.75 2.75 0 0 1-2.75 2.75H4.75A2.75 2.75 0 0 1 2 15.75v-7.5A2.75 2.75 0 0 1 4.75 5.5Zm0 2A.75.75 0 0 0 4 8.25v7.5c0 .41.34.75.75.75h14.5c.41 0 .75-.34.75-.75v-7.5a.75.75 0 0 0-.75-.75H4.75Zm1.5 7.25a1 1 0 0 1 1-1h3.2a1 1 0 1 1 0 2h-3.2a1 1 0 0 1-1-1Zm7.3 0a1 1 0 0 1 1-1h2.2a1 1 0 1 1 0 2h-2.2a1 1 0 0 1-1-1Zm-7.3-3.5a1 1 0 0 1 1-1h1.7a1 1 0 1 1 0 2h-1.7a1 1 0 0 1-1-1Zm5.3 0a1 1 0 0 1 1-1h4.2a1 1 0 1 1 0 2h-4.2a1 1 0 0 1-1-1Z" /></svg>
          <span class="control-text">{{ subtitleLabel }}</span>
        </button>
        <Transition name="control-menu">
          <div v-if="activeMenu === 'subtitle'" class="control-popover track-popover" role="menu" aria-label="选择字幕">
            <p v-if="trackError" class="menu-empty">
              {{ trackError }}
            </p>
            <button type="button" class="menu-option" :class="{ 'is-selected': currentSubtitle === null }" role="menuitemradio" :aria-checked="currentSubtitle === null" @click="chooseSubtitle(null)">
              关闭字幕
            </button>
            <template v-if="downloadedSubtitleTracks.length">
              <p class="subtitle-group-label">
                本地下载
              </p>
              <button v-for="track in downloadedSubtitleTracks" :key="track.id" type="button" class="menu-option menu-option--stacked" :class="{ 'is-selected': currentSubtitle === track.id }" role="menuitemradio" :aria-checked="currentSubtitle === track.id" :aria-disabled="track.selectable ? undefined : 'true'" :disabled="!track.selectable" :title="track.unavailableReason ?? fullTrackLabel(track)" @click="chooseSubtitle(track.id)">
                <span>{{ fullTrackLabel(track) }}</span>
                <small>{{ subtitleSourceLabel(track) }}</small>
                <small v-if="track.unavailableReason">{{ track.unavailableReason }}</small>
              </button>
            </template>
            <div v-if="downloadedSubtitleTracks.length && mediaSubtitleTracks.length" class="subtitle-group-divider" aria-hidden="true" />
            <template v-if="mediaSubtitleTracks.length">
              <p class="subtitle-group-label">
                视频与媒体源
              </p>
              <button v-for="track in mediaSubtitleTracks" :key="track.id" type="button" class="menu-option menu-option--stacked" :class="{ 'is-selected': currentSubtitle === track.id }" role="menuitemradio" :aria-checked="currentSubtitle === track.id" :aria-disabled="track.selectable ? undefined : 'true'" :disabled="!track.selectable" :title="track.unavailableReason ?? fullTrackLabel(track)" @click="chooseSubtitle(track.id)">
                <span>{{ fullTrackLabel(track) }}</span>
                <small>{{ subtitleSourceLabel(track) }}</small>
                <small v-if="track.unavailableReason">{{ track.unavailableReason }}</small>
              </button>
            </template>
            <p v-if="!subtitleTracks.length && !trackError" class="menu-empty">
              暂未检测到字幕轨道，且媒体详情未提供可显示的字幕信息
            </p>
            <div class="subtitle-delay-control" role="group" aria-label="字幕偏移">
              <div class="subtitle-delay-header">
                <span>字幕偏移</span>
                <output>{{ formatSubtitleDelay(subtitleDelay) }}</output>
              </div>
              <input
                class="subtitle-delay-slider"
                type="range"
                min="-30"
                max="30"
                step="0.1"
                :value="subtitleDelay"
                aria-label="字幕偏移秒数，负数提前，正数延后"
                @input="updateSubtitleDelay"
              >
              <div class="subtitle-delay-actions">
                <button type="button" title="字幕提前 0.5 秒" aria-label="字幕提前 0.5 秒" @click="adjustSubtitleDelay(-0.5)">
                  -0.5s
                </button>
                <button type="button" title="重置字幕偏移" @click="emit('setSubtitleDelay', 0)">
                  重置
                </button>
                <button type="button" title="字幕延后 0.5 秒" aria-label="字幕延后 0.5 秒" @click="adjustSubtitleDelay(0.5)">
                  +0.5s
                </button>
              </div>
            </div>
            <button type="button" class="menu-option menu-option--search" role="menuitem" @click="openSubtitleSearch">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m20.2 20.2-4.35-4.35m1.4-5.1a6.5 6.5 0 1 1-13 0 6.5 6.5 0 0 1 13 0Z" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg>
              搜索字幕
            </button>
            <button type="button" class="menu-option menu-option--search" role="menuitem" @click="openLocalSubtitle">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4.5 6.5h5l1.6 2h8.4v9A2.5 2.5 0 0 1 17 20H7a2.5 2.5 0 0 1-2.5-2.5v-11Zm7.5 5v5m0-5-2 2m2-2 2 2" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
              载入本地字幕
            </button>
          </div>
        </Transition>
      </div>

      <div v-if="showAudioControl" class="control-menu-anchor">
        <button class="control-button action-chip secondary" :class="{ 'is-active': activeMenu === 'audio' }" type="button" title="音轨" aria-label="音轨" aria-haspopup="menu" :aria-expanded="activeMenu === 'audio'" @click="toggleMenu('audio')">
          <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M12.5 4.2a1 1 0 0 1 .5.86v13.88a1 1 0 0 1-1.64.77L7.1 16.2H4.5A2.5 2.5 0 0 1 2 13.7v-3.4a2.5 2.5 0 0 1 2.5-2.5h2.6l4.26-3.5a1 1 0 0 1 1.14-.1Zm4.74 3.1a1 1 0 0 1 1.41 0A6.63 6.63 0 0 1 20.6 12c0 1.84-.75 3.5-1.95 4.7a1 1 0 1 1-1.41-1.42A4.63 4.63 0 0 0 18.6 12c0-1.28-.52-2.44-1.36-3.28a1 1 0 0 1 0-1.42Zm-2.46 2.45a1 1 0 0 1 1.41 0c.58.58.94 1.38.94 2.25s-.36 1.67-.94 2.25a1 1 0 0 1-1.41-1.41c.22-.22.35-.52.35-.84s-.13-.62-.35-.84a1 1 0 0 1 0-1.41Z" /></svg>
          <span class="control-text">{{ audioLabel }}</span>
        </button>
        <Transition name="control-menu">
          <div v-if="activeMenu === 'audio'" class="control-popover track-popover" role="menu" aria-label="选择音轨">
            <p v-if="trackError" class="menu-empty">
              {{ trackError }}
            </p>
            <template v-if="audioTracks.length">
              <button v-for="track in audioTracks" :key="track.id" type="button" class="menu-option menu-option--stacked" :class="{ 'is-selected': currentAudio === track.id }" role="menuitemradio" :aria-checked="currentAudio === track.id" @click="chooseAudio(track.id)">
                <span>{{ fullTrackLabel(track) }}</span>
                <small v-if="track.isDefault">默认轨道</small>
              </button>
            </template>
            <p v-else-if="!trackError" class="menu-empty">
              暂未检测到音轨
            </p>
          </div>
        </Transition>
      </div>

      <div v-if="showQueueControl" class="control-menu-anchor">
        <button class="control-button action-chip secondary" :class="{ 'is-active': activeMenu === 'queue' }" type="button" title="播放队列" aria-label="播放队列" aria-haspopup="menu" :aria-expanded="activeMenu === 'queue'" @click="toggleMenu('queue')">
          <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6.5a1 1 0 0 1 1-1h9a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm0 5.5a1 1 0 0 1 1-1h9a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm0 5.5a1 1 0 0 1 1-1h6.5a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm12.5-4.98c0-.87.96-1.4 1.7-.94l2.88 1.8a1.1 1.1 0 0 1 0 1.86l-2.88 1.8a1.1 1.1 0 0 1-1.7-.94v-3.58Z" /></svg>
          <span class="control-text">队列 {{ queueLabel }}</span>
        </button>
        <Transition name="control-menu">
          <div v-if="activeMenu === 'queue'" class="control-popover queue-popover" role="menu" aria-label="播放队列">
            <div class="queue-popover-header">
              <span>播放队列</span>
              <small>{{ queueItemCount }} 项</small>
            </div>
            <div class="queue-list" role="list">
              <button v-for="(item, index) in queueItems" :key="`${item.sourceId}:${item.id}:${index}`" type="button" class="queue-option" :class="{ 'is-current': index === currentQueueIndex }" role="menuitem" :aria-current="index === currentQueueIndex ? 'true' : undefined" :disabled="isQueueSwitching && index !== currentQueueIndex" @click="chooseQueueItem(index)">
                <span class="queue-thumb" aria-hidden="true">
                  <img v-if="item.posterUrl || item.backdropUrl" :src="item.posterUrl || item.backdropUrl" alt="" loading="lazy">
                  <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 4h13A2.5 2.5 0 0 1 21 6.5v11a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17.5v-11A2.5 2.5 0 0 1 5.5 4Zm1 3A1.5 1.5 0 1 0 8 8.5 1.5 1.5 0 0 0 6.5 7Zm-1 10.5h13a.5.5 0 0 0 .5-.5v-2.6l-3.15-3.15a1 1 0 0 0-1.42 0l-2.1 2.1-.78-.78a1 1 0 0 0-1.42 0L5 16.7v.3a.5.5 0 0 0 .5.5Z" /></svg>
                </span>
                <span class="queue-copy">
                  <span class="queue-title">{{ item.title || item.name }}</span>
                  <small>{{ item.overview || queueItemSubtitle(item) }}</small>
                </span>
                <span v-if="index === currentQueueIndex" class="queue-current-badge">正在播放</span>
              </button>
            </div>
          </div>
        </Transition>
      </div>

      <button ref="settingsButton" class="control-button settings-entry-button secondary" :class="{ 'is-active': settingsPanelOpen }" type="button" title="设置" aria-label="设置" aria-controls="player-settings-panel" :aria-expanded="settingsPanelOpen" @click="toggleSettingsPanel">
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M19.43 12.98c.04-.32.07-.65.07-.98s-.02-.66-.07-.98l2.11-1.65a.5.5 0 0 0 .12-.64l-2-3.46a.5.5 0 0 0-.6-.22l-2.49 1a7.34 7.34 0 0 0-1.69-.98L14.5 2.42A.5.5 0 0 0 14 2h-4a.5.5 0 0 0-.5.42L9.12 5.07c-.61.23-1.18.56-1.69.98l-2.49-1a.5.5 0 0 0-.6.22l-2 3.46a.5.5 0 0 0 .12.64l2.11 1.65c-.04.32-.07.65-.07.98s.02.66.07.98l-2.11 1.65a.5.5 0 0 0-.12.64l2 3.46c.13.22.39.31.62.22l2.47-1a7.34 7.34 0 0 0 1.69.98l.38 2.65c.04.24.25.42.5.42h4c.25 0 .46-.18.5-.42l.38-2.65c.61-.23 1.18-.56 1.69-.98l2.47 1c.23.09.49 0 .62-.22l2-3.46a.5.5 0 0 0-.12-.64l-2.11-1.65ZM12 15.5A3.5 3.5 0 1 1 12 8a3.5 3.5 0 0 1 0 7.5Z" /></svg>
        <span class="settings-entry-label">设置</span>
      </button>

      <div v-if="mobileLayout && orientationSupported" class="control-menu-anchor">
        <button class="control-button secondary" :class="{ 'is-active': activeMenu === 'orientation' || orientationMode !== 'auto' }" type="button" :title="orientationLabel" :aria-label="orientationLabel" aria-haspopup="menu" :aria-expanded="activeMenu === 'orientation'" @click="toggleMenu('orientation')">
          <svg v-if="orientationMode === 'auto'" class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M7.5 10V8a4.5 4.5 0 0 1 8.7-1.65 1 1 0 1 1-1.86.73A2.5 2.5 0 0 0 9.5 8v2H17a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-6a2 2 0 0 1 2-2h.5Zm-.5 2v6h10v-6H7Z" />
          </svg>
          <svg v-else class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M8 10V8a4 4 0 1 1 8 0v2h1a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-6a2 2 0 0 1 2-2h1Zm2 0h4V8a2 2 0 1 0-4 0v2Zm-3 2v6h10v-6H7Z" />
          </svg>
        </button>
        <Transition name="control-menu">
          <div v-if="activeMenu === 'orientation'" class="control-popover orientation-popover" role="menu" aria-label="屏幕方向">
            <button type="button" class="menu-option" :class="{ 'is-selected': orientationMode === 'auto' }" role="menuitemradio" :aria-checked="orientationMode === 'auto'" @click="chooseOrientation('auto')">
              自动横屏
            </button>
            <button type="button" class="menu-option" :class="{ 'is-selected': orientationMode === 'landscape' }" role="menuitemradio" :aria-checked="orientationMode === 'landscape'" @click="chooseOrientation('landscape')">
              锁定横屏
            </button>
            <button type="button" class="menu-option" :class="{ 'is-selected': orientationMode === 'portrait' }" role="menuitemradio" :aria-checked="orientationMode === 'portrait'" @click="chooseOrientation('portrait')">
              锁定竖屏
            </button>
          </div>
        </Transition>
      </div>

      <button class="control-button fullscreen-button secondary" :class="{ 'is-active': isFullscreen }" type="button" :title="fullscreenTitle" :aria-label="fullscreenTitle" :aria-pressed="isFullscreen" :disabled="fullscreenBusy" @click="handleFullscreenButtonClick">
        <svg v-if="isFullscreen" class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M9 4a1 1 0 0 1 1 1v3.25A1.75 1.75 0 0 1 8.25 10H5a1 1 0 0 1 0-2h3V5a1 1 0 0 1 1-1Zm6 0a1 1 0 0 1 1 1v3h3a1 1 0 1 1 0 2h-3.25A1.75 1.75 0 0 1 14 8.25V5a1 1 0 0 1 1-1ZM4 15a1 1 0 0 1 1-1h3.25A1.75 1.75 0 0 1 10 15.75V19a1 1 0 1 1-2 0v-3H5a1 1 0 0 1-1-1Zm10 0.75A1.75 1.75 0 0 1 15.75 14H19a1 1 0 1 1 0 2h-3v3a1 1 0 1 1-2 0v-3.25Z" /></svg>
        <svg v-else class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h4a1 1 0 0 1 0 2H6v3a1 1 0 1 1-2 0V5a1 1 0 0 1 1-1Zm10 1a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v4a1 1 0 1 1-2 0V6h-3a1 1 0 0 1-1-1ZM4 15a1 1 0 1 1 2 0v3h3a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1v-4Zm16-1a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1h-4a1 1 0 1 1 0-2h3v-3a1 1 0 0 1 1-1Z" /></svg>
      </button>
    </div>

    <PlayerSettingsPanel :open="settingsPanelOpen" :aspect-mode="videoAspectMode" :fit-mode="videoFitMode" :video-brightness="videoBrightness" :error-message="pictureSettingsError" @close="closeSettingsPanel" @interaction-change="setSettingsPanelInteracting" @set-aspect-mode="(mode) => emit('setVideoAspect', mode)" @set-fit-mode="(mode) => emit('setVideoFit', mode)" @set-video-brightness="(level) => emit('setVideoBrightness', level)" />
  </div>
</template>

<style scoped>
.player-controls-glass {
  border: 1px solid var(--control-border);
  background: var(--player-chrome-surface);
  box-shadow: var(--player-chrome-shadow);
  backdrop-filter: blur(52px) saturate(1.9) contrast(1.05);
  -webkit-backdrop-filter: blur(52px) saturate(1.9) contrast(1.05);
}

.transport-controls {
  min-width: max-content;
}

.control-menu-anchor {
  position: relative;
  display: flex;
}

.control-button {
  display: flex;
  flex: 0 0 auto;
  width: 40px;
  height: 40px;
  align-items: center;
  justify-content: center;
  border: 1px solid transparent;
  border-radius: var(--radius-full);
  color: var(--color-text-secondary);
  background: var(--surface-soft);
  transition: transform var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out), box-shadow var(--duration-fast) var(--ease-out), opacity var(--duration-fast) var(--ease-out);
}

.action-chip {
  width: auto;
  min-width: 48px;
  gap: 0.35rem;
  padding: 0 0.72rem;
}

.settings-entry-button {
  width: auto;
  min-width: 72px;
  gap: 0.4rem;
  padding: 0 0.85rem;
}

.fullscreen-button {
  border-color: var(--control-border);
  background: var(--control-bg);
}

.settings-entry-label,
.control-text {
  max-width: 5.5rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.78rem;
  font-weight: 700;
  letter-spacing: 0.04em;
}

.control-icon {
  width: 20px;
  height: 20px;
  display: block;
  fill: currentColor;
  pointer-events: none;
}

.play-icon {
  margin-left: 2px;
}

.control-button:hover:not(:disabled) {
  border-color: var(--control-border-hover);
  background: var(--control-bg-hover);
  color: var(--color-text);
  transform: translateY(-1px);
}

.control-button:active:not(:disabled) {
  transform: translateY(0) scale(0.96);
}

.control-button.is-active:not(:disabled) {
  border-color: var(--control-border-hover);
  background: var(--surface-soft-hover);
  color: var(--color-text);
  box-shadow: 0 10px 26px rgba(74, 158, 255, 0.18);
}

.control-button[aria-disabled="true"] {
  cursor: default;
  color: var(--color-text-tertiary);
}

.control-button:disabled {
  cursor: not-allowed;
  border-color: var(--color-border);
  background: var(--surface-soft);
  color: var(--color-text-tertiary);
  opacity: 0.72;
  box-shadow: none;
}

.control-button.primary {
  background: var(--player-primary-bg);
  color: var(--player-primary-text);
  box-shadow: var(--player-chrome-shadow);
}

.control-button.primary:hover:not(:disabled) {
  background: var(--color-text);
  color: var(--color-text-inverse);
}

.control-popover {
  position: absolute;
  right: 0;
  bottom: calc(100% + 0.8rem);
  z-index: 45;
  min-width: 9rem;
  max-width: min(18rem, calc(100vw - 3rem));
  border: 1px solid var(--control-border);
  border-radius: 22px;
  background: var(--player-chrome-surface-strong);
  box-shadow: var(--chrome-shadow);
  padding: 0.45rem;
  backdrop-filter: blur(42px) saturate(1.8);
  -webkit-backdrop-filter: blur(42px) saturate(1.8);
}

.track-popover {
  min-width: 15rem;
  max-height: min(34rem, 72vh);
  overflow-y: auto;
}

.queue-popover {
  width: min(25rem, calc(100vw - 3rem));
  max-width: min(25rem, calc(100vw - 3rem));
  padding: 0.65rem;
}

.queue-popover-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.2rem 0.35rem 0.55rem;
  color: var(--color-text);
  font-size: 0.78rem;
  font-weight: 800;
  letter-spacing: 0.08em;
}

.queue-popover-header small {
  color: var(--color-text-tertiary);
  font-size: 0.66rem;
  font-weight: 700;
}

.queue-list {
  display: flex;
  max-height: min(22rem, 50vh);
  flex-direction: column;
  gap: 0.42rem;
  overflow-y: auto;
  padding-right: 0.1rem;
}

.queue-option {
  display: grid;
  width: 100%;
  grid-template-columns: 3.6rem minmax(0, 1fr) auto;
  gap: 0.72rem;
  align-items: center;
  border: 1px solid transparent;
  border-radius: 18px;
  padding: 0.5rem;
  color: var(--color-text-secondary);
  background: var(--surface-soft);
  text-align: left;
  transition: background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out), transform var(--duration-fast) var(--ease-out);
}

.queue-option:hover:not(:disabled),
.queue-option:focus-visible,
.queue-option.is-current {
  border-color: var(--control-border-hover);
  color: var(--color-text);
  background: var(--surface-soft-hover);
}

.queue-option:hover:not(:disabled) {
  transform: translateY(-1px);
}

.queue-option:disabled {
  cursor: wait;
  opacity: 0.62;
}

.queue-thumb {
  display: flex;
  aspect-ratio: 16 / 10;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 14px;
  color: var(--color-text-tertiary);
  background: var(--surface-soft);
}

.queue-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.queue-thumb svg {
  width: 1.4rem;
  height: 1.4rem;
  fill: currentColor;
}

.queue-copy {
  min-width: 0;
}

.queue-title {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.78rem;
  font-weight: 800;
  letter-spacing: 0.02em;
}

.queue-copy small {
  display: -webkit-box;
  margin-top: 0.2rem;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  color: var(--color-text-tertiary);
  font-size: 0.66rem;
  font-weight: 600;
  line-height: 1.35;
}

.queue-current-badge {
  border-radius: var(--radius-full);
  padding: 0.24rem 0.44rem;
  color: var(--player-primary-text);
  background: var(--player-primary-bg);
  font-size: 0.62rem;
  font-weight: 800;
  white-space: nowrap;
}

.speed-popover {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.35rem;
}

.orientation-popover {
  min-width: 10rem;
}

.menu-option {
  width: 100%;
  border: 1px solid transparent;
  border-radius: 16px;
  padding: 0.56rem 0.7rem;
  color: var(--color-text-secondary);
  background: transparent;
  text-align: left;
  font-size: 0.76rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  transition: background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out);
}

.menu-option:hover,
.menu-option:focus-visible,
.menu-option.is-selected {
  border-color: var(--control-border-hover);
  color: var(--color-text);
  background: var(--surface-soft-hover);
}

.menu-option:disabled {
  cursor: not-allowed;
  color: rgba(255, 255, 255, 0.34);
  background: rgba(255, 255, 255, 0.025);
  opacity: 0.78;
}

.menu-option:disabled:hover,
.menu-option:disabled:focus-visible {
  border-color: transparent;
  color: rgba(255, 255, 255, 0.34);
  background: rgba(255, 255, 255, 0.025);
}

.menu-option--stacked {
  display: flex;
  flex-direction: column;
  gap: 0.18rem;
}

.menu-option--stacked small {
  color: rgba(255, 255, 255, 0.38);
  font-size: 0.62rem;
  font-weight: 600;
  letter-spacing: 0.08em;
}

.subtitle-group-label {
  margin: 0.35rem 0.45rem 0.25rem;
  color: rgba(255, 255, 255, 0.42);
  font-size: 0.68rem;
  font-weight: 800;
}

.subtitle-group-divider {
  height: 1px;
  margin: 0.55rem 0.35rem 0.35rem;
  background: rgba(255, 255, 255, 0.12);
}

.menu-option--search {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  margin-top: 0.35rem;
  border-top-color: rgba(255, 255, 255, 0.1);
  color: rgba(255, 255, 255, 0.82);
}

.menu-option--search svg {
  width: 1rem;
  height: 1rem;
  flex: 0 0 auto;
}

.subtitle-delay-control {
  margin-top: 0.35rem;
  border-top: 1px solid rgba(255, 255, 255, 0.1);
  padding: 0.75rem 0.7rem 0.45rem;
}

.subtitle-delay-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  color: rgba(255, 255, 255, 0.82);
  font-size: 0.72rem;
  font-weight: 700;
}

.subtitle-delay-header output {
  color: rgba(255, 255, 255, 0.58);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.subtitle-delay-slider {
  width: 100%;
  margin: 0.7rem 0 0.55rem;
  accent-color: rgba(255, 255, 255, 0.92);
}

.subtitle-delay-actions {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.4rem;
}

.subtitle-delay-actions button {
  min-height: 2rem;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 10px;
  color: rgba(255, 255, 255, 0.72);
  background: rgba(255, 255, 255, 0.06);
  font-size: 0.68rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  transition: border-color var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out);
}

.subtitle-delay-actions button:hover,
.subtitle-delay-actions button:focus-visible {
  border-color: rgba(255, 255, 255, 0.2);
  color: rgba(255, 255, 255, 0.96);
  background: rgba(255, 255, 255, 0.12);
}

.menu-empty {
  margin: 0;
  padding: 0.7rem;
  color: var(--color-text-tertiary);
  font-size: 0.72rem;
  line-height: 1.45;
}

.time-label {
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.02em;
}

.control-menu-enter-active,
.control-menu-leave-active {
  transition: opacity 160ms var(--ease-out), transform 160ms var(--ease-out);
}

.control-menu-enter-from,
.control-menu-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.98);
}

@media (max-width: 1080px) {
  .action-chip .control-text,
  .settings-entry-label {
    display: none;
  }

  .action-chip,
  .settings-entry-button {
    min-width: 40px;
    padding: 0;
  }
}

@media (max-width: 820px) {
  .player-controls-glass {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: 0.35rem 0.65rem;
    border-radius: 8px;
    padding: 0.6rem;
  }

  .current-time-label {
    grid-column: 1;
    grid-row: 1;
  }

  .player-progress-bar {
    grid-column: 2;
    grid-row: 1;
  }

  .duration-time-label {
    grid-column: 3;
    grid-row: 1;
  }

  .time-label {
    display: block;
    width: auto;
    font-size: 0.65rem;
  }

  .transport-controls {
    grid-column: 1 / -1;
    grid-row: 2;
    justify-content: center;
    gap: clamp(0.35rem, 3vw, 0.8rem);
  }

  .transport-controls .control-button {
    width: 44px;
    height: 44px;
  }

  .transport-controls .control-button.primary {
    width: 50px;
    height: 50px;
  }

  .right-controls {
    grid-column: 1 / -1;
    grid-row: 3;
    width: 100%;
    justify-content: flex-start;
    overflow-x: auto;
    border-top: 1px solid rgba(255, 255, 255, 0.1);
    padding-top: 0.45rem;
    scrollbar-width: none;
  }

  .right-controls::-webkit-scrollbar {
    display: none;
  }

  .right-controls .control-button,
  .right-controls .action-chip,
  .right-controls .settings-entry-button {
    width: 44px;
    min-width: 44px;
    height: 44px;
    padding: 0;
  }

  .control-popover {
    position: fixed;
    z-index: 1250;
    right: 0.75rem;
    bottom: max(0.75rem, env(safe-area-inset-bottom));
    left: 0.75rem;
    width: auto;
    min-width: 0;
    max-width: none;
    max-height: min(70svh, 38rem);
    border-radius: 8px;
    padding: 0.65rem;
  }

  .track-popover,
  .queue-popover {
    width: auto;
    min-width: 0;
    max-width: none;
  }

  .menu-option,
  .queue-option {
    min-height: 3rem;
    border-radius: 8px;
  }

  .speed-popover {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

.player-controls-glass.mobile-layout {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  gap: 0.3rem 0.55rem;
  border-radius: 8px;
  padding: 0.55rem;
}

.mobile-layout .current-time-label {
  grid-column: 1;
  grid-row: 1;
}

.mobile-layout .player-progress-bar {
  grid-column: 2;
  grid-row: 1;
}

.mobile-layout .duration-time-label {
  grid-column: 3;
  grid-row: 1;
}

.mobile-layout .time-label {
  display: block;
  width: auto;
  font-size: 0.65rem;
}

.mobile-layout .transport-controls {
  grid-column: 1 / -1;
  grid-row: 2;
  justify-content: center;
  gap: clamp(0.35rem, 3vw, 0.8rem);
}

.mobile-layout .transport-controls .control-button {
  width: 44px;
  height: 44px;
}

.mobile-layout .transport-controls .control-button.primary {
  width: 52px;
  height: 52px;
}

.mobile-layout .right-controls {
  grid-column: 1 / -1;
  grid-row: 3;
  width: 100%;
  justify-content: center;
  overflow-x: auto;
  border-top: 1px solid rgba(255, 255, 255, 0.1);
  padding-top: 0.4rem;
  scrollbar-width: none;
}

.mobile-layout .right-controls::-webkit-scrollbar {
  display: none;
}

.mobile-layout .right-controls .control-button,
.mobile-layout .right-controls .action-chip,
.mobile-layout .right-controls .settings-entry-button {
  width: 44px;
  min-width: 44px;
  height: 44px;
  padding: 0;
}

.mobile-layout .control-text,
.mobile-layout .settings-entry-label {
  display: none;
}

.mobile-layout :deep(.volume-slider) {
  display: none;
}

.mobile-layout .control-popover {
  position: fixed;
  z-index: 1250;
  right: 0.75rem;
  bottom: max(0.75rem, env(safe-area-inset-bottom));
  left: 0.75rem;
  width: auto;
  min-width: 0;
  max-width: none;
  max-height: min(70svh, 38rem);
  border-radius: 8px;
  padding: 0.65rem;
}

.mobile-layout .track-popover,
.mobile-layout .queue-popover {
  width: auto;
  min-width: 0;
  max-width: none;
}

.mobile-layout .menu-option,
.mobile-layout .queue-option {
  min-height: 3rem;
  border-radius: 8px;
}

.mobile-layout .speed-popover {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
</style>
