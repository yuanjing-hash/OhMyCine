<script setup lang="ts">
import type { MpvOrientationMode, SubtitleSelectionId, SubtitleTrackOption, Track, VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import type { PlaybackQueueItem } from '@/services/playbackContext'
import { computed, ref, watch } from 'vue'
import { PLAYBACK_SPEED_OPTIONS } from '@/services/playerInteractionSettings'
import ProgressBar from './ProgressBar.vue'

type MobilePanel = 'more' | 'speed' | 'subtitle' | 'audio' | 'queue' | 'picture' | 'orientation'

const props = defineProps<{
  title: string
  titleLogoUrl?: string
  isPlaying: boolean
  isBuffering: boolean
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
  orientationSupported: boolean
  orientationMode: MpvOrientationMode
}>()

const emit = defineEmits<{
  back: []
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
  interactionChange: [active: boolean]
}>()

const activePanel = ref<MobilePanel | null>(null)
const progressInteracting = ref(false)

const downloadedSubtitleTracks = computed(() => props.subtitleTracks.filter(track => track.source === 'downloaded'))
const mediaSubtitleTracks = computed(() => props.subtitleTracks.filter(track => track.source !== 'downloaded'))
const showAudioControl = computed(() => props.audioTracks.length > 1)
const showQueueControl = computed(() => props.queueItemCount > 1)
const panelTitle = computed(() => {
  switch (activePanel.value) {
    case 'speed': return '播放速度'
    case 'subtitle': return '字幕'
    case 'audio': return '音轨'
    case 'queue': return '播放队列'
    case 'picture': return '画面'
    case 'orientation': return '屏幕方向'
    default: return '播放工具'
  }
})

function formatTime(seconds: number): string {
  const safeSeconds = Number.isFinite(seconds) && seconds > 0 ? seconds : 0
  const hours = Math.floor(safeSeconds / 3600)
  const minutes = Math.floor((safeSeconds % 3600) / 60)
  const secs = Math.floor(safeSeconds % 60)
  return hours > 0
    ? `${hours}:${minutes.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`
    : `${minutes}:${secs.toString().padStart(2, '0')}`
}

function formatSpeed(speed: number): string {
  return `${Number.isInteger(speed) ? speed.toFixed(1) : speed}x`
}

function fullTrackLabel(track: Track | SubtitleTrackOption): string {
  const parts = [track.title, track.language?.toUpperCase(), track.codec, track.channels ? `${track.channels}ch` : null]
    .filter((part): part is string => Boolean(part))
  return parts.length ? parts.join(' · ') : `轨道 ${track.id}`
}

function subtitleSourceLabel(track: SubtitleTrackOption): string {
  const source = track.source === 'downloaded'
    ? '本地下载'
    : track.source === 'embedded'
      ? '视频内嵌'
      : track.source === 'provider'
        ? '媒体源提供'
        : '媒体详情'
  return `${source}${track.isDefault ? ' · 默认' : ''}`
}

function queueItemLabel(item: PlaybackQueueItem): string {
  const episode = [
    typeof item.seasonNumber === 'number' ? `S${item.seasonNumber.toString().padStart(2, '0')}` : '',
    typeof item.episodeNumber === 'number' ? `E${item.episodeNumber.toString().padStart(2, '0')}` : '',
  ].join('')
  return episode || item.type
}

function orientationLabel(): string {
  if (props.orientationMode === 'landscape')
    return '锁定横屏'
  if (props.orientationMode === 'portrait')
    return '锁定竖屏'
  return '自动横屏'
}

function formatSubtitleDelay(delay: number): string {
  if (Math.abs(delay) < 0.05)
    return '同步 0.0 秒'
  return delay > 0 ? `延后 +${delay.toFixed(1)} 秒` : `提前 ${Math.abs(delay).toFixed(1)} 秒`
}

function openPanel(panel: MobilePanel) {
  activePanel.value = panel
}

function closePanel() {
  activePanel.value = null
}

function dismissTransientUi() {
  closePanel()
}

async function toggleFullscreenFromShortcut() {
  // Android playback already owns an immersive full-screen activity surface.
}

function chooseSpeed(speed: number) {
  emit('setPlaybackSpeed', speed)
  closePanel()
}

function chooseSubtitle(trackId: SubtitleSelectionId | null) {
  emit('setSubtitle', trackId)
  closePanel()
}

function chooseAudio(trackId: number) {
  emit('setAudio', trackId)
  closePanel()
}

function chooseQueueItem(index: number) {
  emit('selectQueueItem', index)
  closePanel()
}

function chooseOrientation(mode: MpvOrientationMode) {
  emit('setOrientationMode', mode)
  closePanel()
}

function openSubtitleSearch() {
  closePanel()
  emit('searchSubtitles')
}

function openLocalSubtitle() {
  closePanel()
  emit('loadLocalSubtitle')
}

function setProgressInteracting(active: boolean) {
  progressInteracting.value = active
}

watch([activePanel, progressInteracting], () => {
  emit('interactionChange', activePanel.value !== null || progressInteracting.value)
})

defineExpose({ dismissTransientUi, toggleFullscreenFromShortcut })
</script>

<template>
  <div class="mobile-control-layer pointer-events-none absolute inset-0 z-20" data-player-click-ignore>
    <header class="mobile-player-top pointer-events-auto">
      <div class="mobile-title-group">
        <button type="button" class="mobile-icon-button mobile-back-button" aria-label="返回" @click="emit('back')">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m15 5-7 7 7 7M8 12h11" /></svg>
        </button>
        <img v-if="titleLogoUrl" :src="titleLogoUrl" :alt="title" class="mobile-title-logo">
        <strong v-else class="mobile-title-text">{{ title }}</strong>
      </div>

      <nav class="mobile-top-tools" aria-label="播放工具">
        <button type="button" class="mobile-icon-button" aria-label="画面设置" @click="openPanel('picture')">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16v10H4V7Zm3-3v3m10-3v3M7 17v3m10-3v3" /></svg>
        </button>
        <button v-if="orientationSupported" type="button" class="mobile-icon-button" :class="{ 'is-active': orientationMode !== 'auto' }" :aria-label="orientationLabel()" @click="openPanel('orientation')">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 10V8a4 4 0 1 1 8 0v2h1a2 2 0 0 1 2 2v7H5v-7a2 2 0 0 1 2-2h1Zm2 0h4V8a2 2 0 1 0-4 0v2Z" /></svg>
        </button>
        <button type="button" class="mobile-icon-button" :class="{ 'is-active': currentSubtitle !== null }" aria-label="字幕" @click="openPanel('subtitle')">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6h16a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2Zm2 5h5m3 0h4M6 14h3m3 0h6" /></svg>
        </button>
        <button type="button" class="mobile-icon-button" aria-label="更多播放工具" @click="openPanel('more')">
          <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="5" cy="12" r="1.7" /><circle cx="12" cy="12" r="1.7" /><circle cx="19" cy="12" r="1.7" /></svg>
        </button>
      </nav>
    </header>

    <div class="mobile-transport pointer-events-auto" :class="{ 'is-buffering': isBuffering }" aria-label="播放控制">
      <button type="button" class="transport-skip" aria-label="后退 10 秒" @click="emit('seekRelative', -10)">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7.5 8.5H14a6 6 0 1 1-5.2 9M7.5 8.5 10 6M7.5 8.5 10 11" /><text x="9" y="17">10</text></svg>
      </button>
      <button type="button" class="transport-primary" :aria-label="isPlaying ? '暂停' : '播放'" @click="emit('togglePause')">
        <svg v-if="isPlaying" viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5v14m8-14v14" /></svg>
        <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="m8 5 11 7-11 7V5Z" /></svg>
      </button>
      <button type="button" class="transport-skip" aria-label="前进 10 秒" @click="emit('seekRelative', 10)">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M16.5 8.5H10a6 6 0 1 0 5.2 9m1.3-9L14 6m2.5 2.5L14 11" /><text x="7.5" y="17">10</text></svg>
      </button>
    </div>

    <footer class="mobile-player-bottom pointer-events-auto">
      <div class="mobile-bottom-row">
        <div class="mobile-episode-tools">
          <button type="button" class="mobile-icon-button" :disabled="!canPlayPrevious" aria-label="上一集" @click="emit('playPrevious')">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 5v14m10-13-8 6 8 6V6Z" /></svg>
          </button>
          <button type="button" class="mobile-icon-button" :disabled="!canPlayNext" aria-label="下一集" @click="emit('playNext')">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M17 5v14M7 6l8 6-8 6V6Z" /></svg>
          </button>
        </div>
        <time>{{ formatTime(currentTime) }}</time>
        <ProgressBar class="min-w-0 flex-1" :current="currentTime" :total="duration" @seek="position => emit('seek', position)" @interaction-change="setProgressInteracting" />
        <time>{{ formatTime(duration) }}</time>
        <div class="mobile-media-tools">
          <button type="button" class="mobile-icon-button" aria-label="音量" @click="openPanel('more')">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 9h4l5-4v14l-5-4H4V9Zm12 1a3 3 0 0 1 0 4m2-7a7 7 0 0 1 0 10" /></svg>
          </button>
          <button type="button" class="mobile-text-button" aria-label="播放速度" @click="openPanel('speed')">
            {{ formatSpeed(playbackSpeed) }}
          </button>
          <button v-if="showAudioControl" type="button" class="mobile-icon-button" aria-label="音轨" @click="openPanel('audio')">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 9h4l5-4v14l-5-4H4V9Zm12 1a3 3 0 0 1 0 4m2-7a7 7 0 0 1 0 10" /></svg>
          </button>
          <button v-if="showQueueControl" type="button" class="mobile-icon-button" aria-label="播放队列" @click="openPanel('queue')">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6h11M4 12h11M4 18h8m5-4 4 3-4 3v-6Z" /></svg>
          </button>
        </div>
      </div>
    </footer>

    <Transition name="mobile-player-sheet">
      <div v-if="activePanel" class="mobile-sheet-layer pointer-events-auto" @pointerdown.self="closePanel">
        <aside class="mobile-player-sheet" role="dialog" aria-modal="true" :aria-label="panelTitle">
          <header class="mobile-sheet-header">
            <strong>{{ panelTitle }}</strong>
            <button type="button" class="mobile-icon-button" aria-label="关闭" @click="closePanel">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
            </button>
          </header>

          <div class="mobile-sheet-content cinema-scrollbar">
            <template v-if="activePanel === 'more'">
              <label class="mobile-range-row">
                <span>音量</span>
                <strong>{{ Math.round(volume) }}%</strong>
                <input type="range" min="0" max="100" step="1" :value="volume" @input="emit('setVolume', Number(($event.target as HTMLInputElement).value))">
              </label>
              <button type="button" class="mobile-sheet-action" @click="openPanel('picture')">
                <span>画面比例与填充</span><b>›</b>
              </button>
              <button type="button" class="mobile-sheet-action" @click="openPanel('subtitle')">
                <span>字幕与偏移</span><b>›</b>
              </button>
              <button v-if="showAudioControl" type="button" class="mobile-sheet-action" @click="openPanel('audio')">
                <span>音轨</span><b>›</b>
              </button>
              <button v-if="showQueueControl" type="button" class="mobile-sheet-action" @click="openPanel('queue')">
                <span>播放队列</span><b>›</b>
              </button>
              <button v-if="orientationSupported" type="button" class="mobile-sheet-action" @click="openPanel('orientation')">
                <span>{{ orientationLabel() }}</span><b>›</b>
              </button>
              <button type="button" class="mobile-sheet-action" @click="openSubtitleSearch">
                <span>搜索字幕</span><b>›</b>
              </button>
              <button type="button" class="mobile-sheet-action" @click="openLocalSubtitle">
                <span>载入本地字幕</span><b>›</b>
              </button>
            </template>

            <div v-else-if="activePanel === 'speed'" class="mobile-option-grid">
              <button v-for="speed in PLAYBACK_SPEED_OPTIONS" :key="speed" type="button" class="mobile-choice" :class="{ 'is-selected': Math.abs(playbackSpeed - speed) < 0.001 }" @click="chooseSpeed(speed)">
                {{ formatSpeed(speed) }}
              </button>
            </div>

            <template v-else-if="activePanel === 'subtitle'">
              <p v-if="trackError" class="mobile-sheet-error">
                {{ trackError }}
              </p>
              <button type="button" class="mobile-sheet-action" :class="{ 'is-selected': currentSubtitle === null }" @click="chooseSubtitle(null)">
                <span>关闭字幕</span>
              </button>
              <p v-if="downloadedSubtitleTracks.length" class="mobile-section-label">
                本地下载
              </p>
              <button v-for="track in downloadedSubtitleTracks" :key="track.id" type="button" class="mobile-track-row" :class="{ 'is-selected': currentSubtitle === track.id }" :disabled="!track.selectable" @click="chooseSubtitle(track.id)">
                <span>{{ fullTrackLabel(track) }}</span><small>{{ subtitleSourceLabel(track) }}</small>
              </button>
              <p v-if="mediaSubtitleTracks.length" class="mobile-section-label">
                视频与媒体源
              </p>
              <button v-for="track in mediaSubtitleTracks" :key="track.id" type="button" class="mobile-track-row" :class="{ 'is-selected': currentSubtitle === track.id }" :disabled="!track.selectable" @click="chooseSubtitle(track.id)">
                <span>{{ fullTrackLabel(track) }}</span><small>{{ subtitleSourceLabel(track) }}</small>
              </button>
              <p v-if="!subtitleTracks.length && !trackError" class="mobile-sheet-empty">
                暂未检测到字幕轨道
              </p>
              <label class="mobile-range-row mobile-subtitle-delay">
                <span>字幕偏移</span><strong>{{ formatSubtitleDelay(subtitleDelay) }}</strong>
                <input type="range" min="-30" max="30" step="0.1" :value="subtitleDelay" @input="emit('setSubtitleDelay', Number(($event.target as HTMLInputElement).value))">
              </label>
              <div class="mobile-delay-actions">
                <button type="button" @click="emit('setSubtitleDelay', subtitleDelay - 0.5)">
                  -0.5s
                </button>
                <button type="button" @click="emit('setSubtitleDelay', 0)">
                  重置
                </button>
                <button type="button" @click="emit('setSubtitleDelay', subtitleDelay + 0.5)">
                  +0.5s
                </button>
              </div>
              <button type="button" class="mobile-sheet-action" @click="openSubtitleSearch">
                <span>搜索字幕</span><b>›</b>
              </button>
              <button type="button" class="mobile-sheet-action" @click="openLocalSubtitle">
                <span>载入本地字幕</span><b>›</b>
              </button>
            </template>

            <template v-else-if="activePanel === 'audio'">
              <p v-if="trackError" class="mobile-sheet-error">
                {{ trackError }}
              </p>
              <button v-for="track in audioTracks" :key="track.id" type="button" class="mobile-track-row" :class="{ 'is-selected': currentAudio === track.id }" @click="chooseAudio(track.id)">
                <span>{{ fullTrackLabel(track) }}</span><small>{{ track.isDefault ? '默认轨道' : '音轨' }}</small>
              </button>
            </template>

            <template v-else-if="activePanel === 'queue'">
              <button v-for="(item, index) in queueItems" :key="`${item.sourceId}:${item.id}:${index}`" type="button" class="mobile-queue-row" :class="{ 'is-selected': index === currentQueueIndex }" :disabled="isQueueSwitching && index !== currentQueueIndex" @click="chooseQueueItem(index)">
                <img v-if="item.posterUrl || item.backdropUrl" :src="item.posterUrl || item.backdropUrl" alt="">
                <span><strong>{{ item.title || item.name }}</strong><small>{{ item.overview || queueItemLabel(item) }}</small></span>
              </button>
            </template>

            <template v-else-if="activePanel === 'picture'">
              <label class="mobile-range-row">
                <span>播放器亮度</span>
                <strong>{{ Math.round(videoBrightness) }}%</strong>
                <input type="range" min="0" max="100" step="1" :value="videoBrightness" @input="emit('setVideoBrightness', Number(($event.target as HTMLInputElement).value))">
              </label>
              <p class="mobile-section-label">
                画面比例
              </p>
              <div class="mobile-option-grid">
                <button v-for="option in ([['default', '原始'], ['16:9', '16:9'], ['4:3', '4:3'], ['cinema', '2.35:1']] as const)" :key="option[0]" type="button" class="mobile-choice" :class="{ 'is-selected': videoAspectMode === option[0] }" @click="emit('setVideoAspect', option[0])">
                  {{ option[1] }}
                </button>
              </div>
              <p class="mobile-section-label">
                填充方式
              </p>
              <div class="mobile-option-grid">
                <button v-for="option in ([['fit', '适应'], ['crop', '填满'], ['cinemaCrop', '影院裁切']] as const)" :key="option[0]" type="button" class="mobile-choice" :class="{ 'is-selected': videoFitMode === option[0] }" @click="emit('setVideoFit', option[0])">
                  {{ option[1] }}
                </button>
              </div>
              <p v-if="pictureSettingsError" class="mobile-sheet-error">
                {{ pictureSettingsError }}
              </p>
            </template>

            <template v-else-if="activePanel === 'orientation'">
              <button type="button" class="mobile-sheet-action" :class="{ 'is-selected': orientationMode === 'auto' }" @click="chooseOrientation('auto')">
                <span>自动横屏</span>
              </button>
              <button type="button" class="mobile-sheet-action" :class="{ 'is-selected': orientationMode === 'landscape' }" @click="chooseOrientation('landscape')">
                <span>锁定横屏</span>
              </button>
              <button type="button" class="mobile-sheet-action" :class="{ 'is-selected': orientationMode === 'portrait' }" @click="chooseOrientation('portrait')">
                <span>锁定竖屏</span>
              </button>
            </template>
          </div>
        </aside>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.mobile-control-layer {
  color: white;
}

.mobile-player-top,
.mobile-player-bottom {
  position: absolute;
  right: max(1rem, env(safe-area-inset-right));
  left: max(1rem, env(safe-area-inset-left));
  display: flex;
  align-items: center;
}

.mobile-player-top {
  top: max(0.8rem, env(safe-area-inset-top));
  justify-content: space-between;
  gap: 1rem;
}

.mobile-title-group,
.mobile-top-tools,
.mobile-episode-tools,
.mobile-media-tools {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.55rem;
}

.mobile-title-group {
  flex: 1;
}

.mobile-title-logo {
  width: auto;
  max-width: min(15rem, 36vw);
  height: 2rem;
  object-fit: contain;
  object-position: left center;
}

.mobile-title-text {
  max-width: min(20rem, 42vw);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 1rem;
}

.mobile-top-tools,
.mobile-player-bottom {
  overflow: hidden;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  background: linear-gradient(145deg, rgba(35, 38, 45, 0.7), rgba(10, 12, 17, 0.76));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.13), 0 16px 44px rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(24px) saturate(1.35);
  -webkit-backdrop-filter: blur(24px) saturate(1.35);
}

.mobile-top-tools::before,
.mobile-player-bottom::before,
.mobile-player-sheet::before {
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background: linear-gradient(110deg, rgba(255, 255, 255, 0.08), transparent 34%, transparent 72%, rgba(121, 168, 255, 0.05));
  content: '';
  pointer-events: none;
}

.mobile-top-tools {
  position: relative;
  gap: 0.18rem;
  padding: 0.24rem;
}

.mobile-icon-button,
.mobile-text-button,
.transport-skip,
.transport-primary {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 1px solid transparent;
  color: rgba(255, 255, 255, 0.82);
  background: rgba(255, 255, 255, 0.055);
}

.mobile-icon-button {
  width: 2.4rem;
  height: 2.4rem;
  border-radius: 50%;
}

.mobile-icon-button svg {
  width: 1.15rem;
  height: 1.15rem;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.mobile-icon-button svg circle {
  fill: currentColor;
  stroke: none;
}

.mobile-icon-button.is-active,
.mobile-text-button.is-active,
.mobile-choice.is-selected,
.mobile-sheet-action.is-selected,
.mobile-track-row.is-selected,
.mobile-queue-row.is-selected {
  border-color: rgba(255, 255, 255, 0.28);
  color: white;
  background: rgba(255, 255, 255, 0.16);
}

.mobile-icon-button:disabled {
  opacity: 0.3;
}

.mobile-back-button {
  width: 2.75rem;
  height: 2.75rem;
  border-color: rgba(255, 255, 255, 0.13);
  background: linear-gradient(145deg, rgba(38, 41, 48, 0.72), rgba(10, 12, 17, 0.78));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.14), 0 12px 30px rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(20px) saturate(1.3);
}

.mobile-transport {
  position: absolute;
  top: 50%;
  left: 50%;
  display: flex;
  align-items: center;
  gap: clamp(0.9rem, 3.4vw, 2.5rem);
  transform: translate(-50%, -50%);
  transition: opacity 180ms ease, transform 180ms ease;
}

.mobile-transport.is-buffering {
  pointer-events: none;
  opacity: 0;
  transform: translate(-50%, -50%) scale(0.94);
}

.transport-skip,
.transport-primary {
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 50%;
  background: linear-gradient(145deg, rgba(38, 41, 48, 0.72), rgba(10, 12, 17, 0.78));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.14), 0 12px 32px rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(20px) saturate(1.3);
  -webkit-backdrop-filter: blur(20px) saturate(1.3);
}

.transport-skip {
  width: 3.25rem;
  height: 3.25rem;
}

.transport-primary {
  width: 3.9rem;
  height: 3.9rem;
  border-color: rgba(255, 255, 255, 0.5);
  color: rgba(10, 12, 17, 0.95);
  background: rgba(255, 255, 255, 0.92);
  box-shadow: inset 0 1px 0 white, 0 12px 34px rgba(0, 0, 0, 0.28);
}

.transport-skip svg,
.transport-primary svg {
  width: 1.55rem;
  height: 1.55rem;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.transport-skip text {
  fill: currentColor;
  stroke: none;
  font-size: 6px;
  font-weight: 800;
}

.mobile-player-bottom {
  bottom: max(0.8rem, env(safe-area-inset-bottom));
  padding: 0.42rem 0.65rem;
}

.mobile-bottom-row {
  display: grid;
  width: 100%;
  grid-template-columns: auto 3.2rem minmax(6rem, 1fr) 3.2rem auto;
  align-items: center;
  gap: clamp(0.35rem, 1vw, 0.7rem);
}

.mobile-bottom-row time {
  color: rgba(255, 255, 255, 0.74);
  font-size: 0.66rem;
  font-variant-numeric: tabular-nums;
}

.mobile-bottom-row time:nth-of-type(2) {
  text-align: right;
}

.mobile-bottom-row .mobile-icon-button {
  width: 2.15rem;
  height: 2.15rem;
}

.mobile-episode-tools,
.mobile-media-tools {
  flex-wrap: nowrap;
}

.mobile-text-button {
  min-width: 3rem;
  height: 2.15rem;
  border-radius: 999px;
  padding: 0 0.6rem;
  font-size: 0.68rem;
  font-weight: 800;
}

.mobile-sheet-layer {
  position: fixed;
  inset: 0;
  z-index: 1200;
  display: flex;
  justify-content: flex-end;
  background: rgba(0, 0, 0, 0.32);
}

.mobile-player-sheet {
  position: relative;
  overflow: hidden;
  display: flex;
  width: min(23rem, 86vw);
  height: 100%;
  flex-direction: column;
  border-left: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px 0 0 8px;
  background: linear-gradient(145deg, rgba(38, 41, 48, 0.9), rgba(11, 13, 18, 0.94));
  box-shadow: inset 1px 0 0 rgba(255, 255, 255, 0.08), -18px 0 48px rgba(0, 0, 0, 0.42);
  backdrop-filter: blur(28px) saturate(1.25);
  -webkit-backdrop-filter: blur(28px) saturate(1.25);
  padding: max(0.8rem, env(safe-area-inset-top)) max(0.8rem, env(safe-area-inset-right)) max(0.8rem, env(safe-area-inset-bottom)) 0.8rem;
}

.mobile-sheet-header {
  position: relative;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);
  padding: 0 0 0.65rem 0.3rem;
}

.mobile-sheet-header strong {
  font-size: 1rem;
}

.mobile-sheet-content {
  position: relative;
  z-index: 1;
  min-height: 0;
  flex: 1;
  overflow-y: auto;
  padding: 0.75rem 0.1rem 0;
}

.mobile-sheet-action,
.mobile-track-row,
.mobile-queue-row,
.mobile-choice {
  width: 100%;
  border: 1px solid transparent;
  border-radius: 8px;
  color: rgba(255, 255, 255, 0.78);
  background: rgba(255, 255, 255, 0.045);
}

.mobile-sheet-action {
  display: flex;
  min-height: 3.2rem;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.45rem;
  padding: 0 0.9rem;
  text-align: left;
}

.mobile-sheet-action b {
  color: rgba(255, 255, 255, 0.38);
  font-size: 1.25rem;
}

.mobile-range-row {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 0.65rem;
  margin-bottom: 0.8rem;
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);
  padding: 0.35rem 0.4rem 0.85rem;
  color: rgba(255, 255, 255, 0.72);
  font-size: 0.8rem;
}

.mobile-range-row input {
  grid-column: 1 / -1;
  width: 100%;
}

.mobile-option-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.5rem;
}

.mobile-choice {
  min-height: 3rem;
  padding: 0.65rem;
  font-size: 0.82rem;
  font-weight: 700;
}

.mobile-section-label {
  margin: 0.85rem 0 0.45rem;
  color: rgba(255, 255, 255, 0.42);
  font-size: 0.68rem;
  font-weight: 800;
}

.mobile-track-row {
  display: flex;
  min-height: 3.4rem;
  flex-direction: column;
  justify-content: center;
  gap: 0.2rem;
  margin-bottom: 0.4rem;
  padding: 0.65rem 0.8rem;
  text-align: left;
}

.mobile-track-row small,
.mobile-queue-row small {
  color: rgba(255, 255, 255, 0.42);
  font-size: 0.66rem;
}

.mobile-track-row:disabled {
  opacity: 0.42;
}

.mobile-subtitle-delay {
  margin-top: 0.8rem;
  border-top: 1px solid rgba(255, 255, 255, 0.1);
  padding-top: 0.8rem;
}

.mobile-delay-actions {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 0.45rem;
  margin-bottom: 0.8rem;
}

.mobile-delay-actions button {
  min-height: 2.7rem;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  color: rgba(255, 255, 255, 0.76);
  background: rgba(255, 255, 255, 0.05);
}

.mobile-queue-row {
  display: grid;
  grid-template-columns: 4.5rem minmax(0, 1fr);
  gap: 0.7rem;
  align-items: center;
  margin-bottom: 0.45rem;
  padding: 0.45rem;
  text-align: left;
}

.mobile-queue-row img {
  width: 4.5rem;
  aspect-ratio: 16 / 10;
  border-radius: 6px;
  object-fit: cover;
}

.mobile-queue-row span,
.mobile-queue-row strong,
.mobile-queue-row small {
  display: block;
  min-width: 0;
}

.mobile-queue-row strong,
.mobile-queue-row small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mobile-sheet-error,
.mobile-sheet-empty {
  margin: 0 0 0.65rem;
  border-radius: 8px;
  padding: 0.75rem;
  font-size: 0.72rem;
}

.mobile-sheet-error {
  color: rgba(254, 226, 226, 0.9);
  background: rgba(127, 29, 29, 0.28);
}

.mobile-sheet-empty {
  color: rgba(255, 255, 255, 0.46);
  background: rgba(255, 255, 255, 0.04);
}

.mobile-player-sheet-enter-active,
.mobile-player-sheet-leave-active {
  transition: opacity 180ms ease;
}

.mobile-player-sheet-enter-active .mobile-player-sheet,
.mobile-player-sheet-leave-active .mobile-player-sheet {
  transition: transform 220ms ease;
}

.mobile-player-sheet-enter-from,
.mobile-player-sheet-leave-to {
  opacity: 0;
}

.mobile-player-sheet-enter-from .mobile-player-sheet,
.mobile-player-sheet-leave-to .mobile-player-sheet {
  transform: translateX(100%);
}

@media (orientation: portrait) {
  .mobile-player-top {
    align-items: flex-start;
  }

  .mobile-top-tools {
    gap: 0.2rem;
  }

  .mobile-top-tools .mobile-icon-button {
    width: 2.45rem;
    height: 2.45rem;
  }

  .mobile-transport {
    gap: 0.85rem;
  }

  .mobile-player-bottom {
    right: max(0.45rem, env(safe-area-inset-right));
    left: max(0.45rem, env(safe-area-inset-left));
    padding-right: 0.4rem;
    padding-left: 0.4rem;
  }

  .mobile-bottom-row {
    grid-template-columns: auto 2.65rem minmax(3rem, 1fr) 2.65rem auto;
    gap: 0.18rem;
  }

  .mobile-bottom-row time {
    font-size: 0.58rem;
  }

  .mobile-bottom-row .mobile-icon-button {
    width: 1.85rem;
    height: 1.85rem;
  }

  .mobile-bottom-row .mobile-text-button {
    min-width: 2.5rem;
    height: 1.85rem;
    padding: 0 0.4rem;
    font-size: 0.62rem;
  }

  .mobile-episode-tools,
  .mobile-media-tools {
    gap: 0.15rem;
  }

  .mobile-player-sheet {
    align-self: flex-end;
    width: 100%;
    height: min(70svh, 36rem);
    border-top: 1px solid rgba(255, 255, 255, 0.12);
    border-left: 0;
    border-radius: 8px 8px 0 0;
    padding: 0.8rem max(0.8rem, env(safe-area-inset-right)) max(0.8rem, env(safe-area-inset-bottom)) max(0.8rem, env(safe-area-inset-left));
  }

  .mobile-sheet-layer {
    align-items: flex-end;
  }

  .mobile-player-sheet-enter-from .mobile-player-sheet,
  .mobile-player-sheet-leave-to .mobile-player-sheet {
    transform: translateY(100%);
  }
}
</style>
