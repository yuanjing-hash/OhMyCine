<script setup lang="ts">
import type { SubtitleSelectionId, SubtitleTrackOption, Track, VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import PlayerSettingsPanel from './PlayerSettingsPanel.vue'
import ProgressBar from './ProgressBar.vue'
import VolumeControl from './VolumeControl.vue'

type ControlMenu = 'speed' | 'subtitle' | 'audio'

const props = defineProps<{
  isPlaying: boolean
  currentTime: number
  duration: number
  volume: number
  playbackSpeed: number
  subtitleTracks: readonly SubtitleTrackOption[]
  audioTracks: readonly Track[]
  queueItemCount: number
  canPlayPrevious: boolean
  canPlayNext: boolean
  currentSubtitle: SubtitleSelectionId | null
  currentAudio: number | null
  videoAspectMode: VideoAspectMode
  videoFitMode: VideoFitMode
  trackError: string | null
  pictureSettingsError: string | null
}>()

const emit = defineEmits<{
  playPrevious: []
  togglePause: []
  playNext: []
  seek: [position: number]
  seekRelative: [offset: number]
  setVolume: [volume: number]
  setPlaybackSpeed: [speed: number]
  setSubtitle: [trackId: SubtitleSelectionId | null]
  setAudio: [trackId: number]
  setVideoAspect: [mode: VideoAspectMode]
  setVideoFit: [mode: VideoFitMode]
  refreshTracks: []
  fullscreenChanged: [fullscreen: boolean]
  interactionChange: [active: boolean]
}>()

const PLAYBACK_SPEED_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2] as const

const appWindow = getCurrentWindow()
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
  if (activeMenu.value === 'subtitle' || activeMenu.value === 'audio')
    emit('refreshTracks')
  emitInteractionState()
}

function closeMenus() {
  activeMenu.value = null
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

function chooseSubtitle(trackId: SubtitleSelectionId | null) {
  emit('setSubtitle', trackId)
  closeMenus()
}

function chooseAudio(trackId: number) {
  emit('setAudio', trackId)
  closeMenus()
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
  const sourceLabel = track.source === 'embedded' ? '内嵌轨道' : track.source === 'external' ? '外部字幕' : '媒体详情'
  const status = track.isDefault ? ' · 默认' : ''
  if (!track.selectable)
    return `${sourceLabel} · 暂不可加载`
  return `${sourceLabel}${status}`
}

async function syncFullscreenState() {
  try {
    isFullscreen.value = await appWindow.isFullscreen()
    fullscreenError.value = null
  }
  catch {
    isFullscreen.value = document.fullscreenElement !== null
  }
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

async function toggleFullscreen() {
  if (fullscreenBusy.value)
    return

  closeMenus()
  fullscreenBusy.value = true
  emitInteractionState()
  try {
    const nextFullscreen = !(await appWindow.isFullscreen())
    await appWindow.setFullscreen(nextFullscreen)
    isFullscreen.value = nextFullscreen
    fullscreenError.value = null
    emit('fullscreenChanged', nextFullscreen)
  }
  catch {
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
    emitInteractionState()
  }
}

function handleKeydown(event: KeyboardEvent) {
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

onMounted(() => {
  void syncFullscreenState()
})
</script>

<template>
  <div
    class="player-controls-glass pointer-events-auto relative mx-auto flex w-full max-w-7xl min-w-0 items-center gap-3 overflow-visible rounded-[28px] px-5 py-3"
    @mouseenter="setPointerInside(true)"
    @mouseleave="setPointerInside(false)"
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

    <span class="time-label w-16 shrink-0 text-left">{{ formatTime(currentTime) }}</span>

    <ProgressBar class="min-w-0 flex-1" :current="currentTime" :total="duration" @seek="(pos) => emit('seek', pos)" @interaction-change="setChildInteracting" />

    <span class="time-label w-16 shrink-0 text-right">{{ formatTime(duration) }}</span>

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
            <template v-if="subtitleTracks.length">
              <button v-for="track in subtitleTracks" :key="track.id" type="button" class="menu-option menu-option--stacked" :class="{ 'is-selected': currentSubtitle === track.id }" role="menuitemradio" :aria-checked="currentSubtitle === track.id" :aria-disabled="track.selectable ? undefined : 'true'" :disabled="!track.selectable" :title="track.unavailableReason ?? fullTrackLabel(track)" @click="chooseSubtitle(track.id)">
                <span>{{ fullTrackLabel(track) }}</span>
                <small>{{ subtitleSourceLabel(track) }}</small>
                <small v-if="track.unavailableReason">{{ track.unavailableReason }}</small>
              </button>
            </template>
            <p v-else-if="!trackError" class="menu-empty">
              暂未检测到字幕轨道，且媒体详情未提供可显示的字幕信息
            </p>
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

      <button v-if="showQueueControl" class="control-button action-chip secondary" type="button" title="播放队列（后续接入）" aria-label="播放队列，后续接入" aria-disabled="true">
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6.5a1 1 0 0 1 1-1h9a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm0 5.5a1 1 0 0 1 1-1h9a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm0 5.5a1 1 0 0 1 1-1h6.5a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm12.5-4.98c0-.87.96-1.4 1.7-.94l2.88 1.8a1.1 1.1 0 0 1 0 1.86l-2.88 1.8a1.1 1.1 0 0 1-1.7-.94v-3.58Z" /></svg>
        <span class="control-text">队列</span>
      </button>

      <button ref="settingsButton" class="control-button settings-entry-button secondary" :class="{ 'is-active': settingsPanelOpen }" type="button" title="设置" aria-label="设置" aria-controls="player-settings-panel" :aria-expanded="settingsPanelOpen" @click="toggleSettingsPanel">
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M19.43 12.98c.04-.32.07-.65.07-.98s-.02-.66-.07-.98l2.11-1.65a.5.5 0 0 0 .12-.64l-2-3.46a.5.5 0 0 0-.6-.22l-2.49 1a7.34 7.34 0 0 0-1.69-.98L14.5 2.42A.5.5 0 0 0 14 2h-4a.5.5 0 0 0-.5.42L9.12 5.07c-.61.23-1.18.56-1.69.98l-2.49-1a.5.5 0 0 0-.6.22l-2 3.46a.5.5 0 0 0 .12.64l2.11 1.65c-.04.32-.07.65-.07.98s.02.66.07.98l-2.11 1.65a.5.5 0 0 0-.12.64l2 3.46c.13.22.39.31.62.22l2.47-1a7.34 7.34 0 0 0 1.69.98l.38 2.65c.04.24.25.42.5.42h4c.25 0 .46-.18.5-.42l.38-2.65c.61-.23 1.18-.56 1.69-.98l2.47 1c.23.09.49 0 .62-.22l2-3.46a.5.5 0 0 0-.12-.64l-2.11-1.65ZM12 15.5A3.5 3.5 0 1 1 12 8a3.5 3.5 0 0 1 0 7.5Z" /></svg>
        <span class="settings-entry-label">设置</span>
      </button>

      <button class="control-button fullscreen-button secondary" :class="{ 'is-active': isFullscreen }" type="button" :title="fullscreenTitle" :aria-label="fullscreenTitle" :aria-pressed="isFullscreen" :disabled="fullscreenBusy" @click="toggleFullscreen">
        <svg v-if="isFullscreen" class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M9 4a1 1 0 0 1 1 1v3.25A1.75 1.75 0 0 1 8.25 10H5a1 1 0 0 1 0-2h3V5a1 1 0 0 1 1-1Zm6 0a1 1 0 0 1 1 1v3h3a1 1 0 1 1 0 2h-3.25A1.75 1.75 0 0 1 14 8.25V5a1 1 0 0 1 1-1ZM4 15a1 1 0 0 1 1-1h3.25A1.75 1.75 0 0 1 10 15.75V19a1 1 0 1 1-2 0v-3H5a1 1 0 0 1-1-1Zm10 0.75A1.75 1.75 0 0 1 15.75 14H19a1 1 0 1 1 0 2h-3v3a1 1 0 1 1-2 0v-3.25Z" /></svg>
        <svg v-else class="control-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h4a1 1 0 0 1 0 2H6v3a1 1 0 1 1-2 0V5a1 1 0 0 1 1-1Zm10 1a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v4a1 1 0 1 1-2 0V6h-3a1 1 0 0 1-1-1ZM4 15a1 1 0 1 1 2 0v3h3a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1v-4Zm16-1a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1h-4a1 1 0 1 1 0-2h3v-3a1 1 0 0 1 1-1Z" /></svg>
      </button>
    </div>

    <PlayerSettingsPanel :open="settingsPanelOpen" :aspect-mode="videoAspectMode" :fit-mode="videoFitMode" :error-message="pictureSettingsError" @close="closeSettingsPanel" @interaction-change="setSettingsPanelInteracting" @set-aspect-mode="(mode) => emit('setVideoAspect', mode)" @set-fit-mode="(mode) => emit('setVideoFit', mode)" />
  </div>
</template>

<style scoped>
.player-controls-glass {
  border: 1px solid rgba(255, 255, 255, 0.18);
  background:
    radial-gradient(120% 160% at 14% 0%, rgba(255, 255, 255, 0.2), transparent 42%),
    radial-gradient(80% 140% at 100% 100%, rgba(74, 158, 255, 0.16), transparent 46%),
    linear-gradient(135deg, rgba(255, 255, 255, 0.13), rgba(255, 255, 255, 0.055));
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.2),
    inset 0 -1px 0 rgba(255, 255, 255, 0.06),
    0 22px 70px rgba(0, 0, 0, 0.48);
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
  color: rgba(255, 255, 255, 0.8);
  background: rgba(255, 255, 255, 0.035);
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
  border-color: rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.09);
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
  border-color: rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.12);
  color: rgba(255, 255, 255, 0.98);
  transform: translateY(-1px);
}

.control-button:active:not(:disabled) {
  transform: translateY(0) scale(0.96);
}

.control-button.is-active:not(:disabled) {
  border-color: rgba(255, 255, 255, 0.24);
  background: rgba(255, 255, 255, 0.16);
  color: rgba(255, 255, 255, 0.98);
  box-shadow: 0 10px 26px rgba(74, 158, 255, 0.18);
}

.control-button[aria-disabled="true"] {
  cursor: default;
  color: rgba(255, 255, 255, 0.52);
}

.control-button:disabled {
  cursor: not-allowed;
  border-color: rgba(255, 255, 255, 0.06);
  background: rgba(255, 255, 255, 0.035);
  color: rgba(255, 255, 255, 0.28);
  opacity: 0.72;
  box-shadow: none;
}

.control-button.primary {
  background: rgba(255, 255, 255, 0.92);
  color: rgba(8, 10, 16, 0.96);
  box-shadow: 0 12px 28px rgba(255, 255, 255, 0.12);
}

.control-button.primary:hover:not(:disabled) {
  background: #fff;
  color: rgba(8, 10, 16, 1);
}

.control-popover {
  position: absolute;
  right: 0;
  bottom: calc(100% + 0.8rem);
  z-index: 45;
  min-width: 9rem;
  max-width: min(18rem, calc(100vw - 3rem));
  border: 1px solid rgba(255, 255, 255, 0.16);
  border-radius: 22px;
  background:
    radial-gradient(120% 120% at 20% 0%, rgba(255, 255, 255, 0.18), transparent 44%),
    linear-gradient(135deg, rgba(12, 15, 24, 0.82), rgba(8, 10, 16, 0.66));
  box-shadow: 0 22px 60px rgba(0, 0, 0, 0.42), inset 0 1px 0 rgba(255, 255, 255, 0.16);
  padding: 0.45rem;
  backdrop-filter: blur(42px) saturate(1.8);
  -webkit-backdrop-filter: blur(42px) saturate(1.8);
}

.track-popover {
  min-width: 15rem;
}

.speed-popover {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.35rem;
}

.menu-option {
  width: 100%;
  border: 1px solid transparent;
  border-radius: 16px;
  padding: 0.56rem 0.7rem;
  color: rgba(255, 255, 255, 0.68);
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
  border-color: rgba(255, 255, 255, 0.14);
  color: rgba(255, 255, 255, 0.96);
  background: rgba(255, 255, 255, 0.1);
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

.menu-empty {
  margin: 0;
  padding: 0.7rem;
  color: rgba(255, 255, 255, 0.44);
  font-size: 0.72rem;
  line-height: 1.45;
}

.time-label {
  color: rgba(255, 255, 255, 0.56);
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
  .transport-controls .control-button.disabled,
  .time-label,
  .action-chip[aria-label^="音轨"],
  .action-chip[aria-label^="播放队列"] {
    display: none;
  }
}
</style>
