<script setup lang="ts">
import { getCurrentWindow } from '@tauri-apps/api/window'
import { computed, nextTick, onMounted, ref } from 'vue'
import PlayerSettingsPanel from './PlayerSettingsPanel.vue'
import ProgressBar from './ProgressBar.vue'
import VolumeControl from './VolumeControl.vue'

defineProps<{
  isPlaying: boolean
  currentTime: number
  duration: number
  volume: number
}>()

const emit = defineEmits<{
  togglePause: []
  seek: [position: number]
  seekRelative: [offset: number]
  setVolume: [volume: number]
  interactionChange: [active: boolean]
}>()

const appWindow = getCurrentWindow()
const settingsButton = ref<HTMLButtonElement | null>(null)
const pointerInside = ref(false)
const focusInside = ref(false)
const childInteracting = ref(false)
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

function emitInteractionState() {
  emit('interactionChange', pointerInside.value || focusInside.value || childInteracting.value || settingsPanelOpen.value || settingsPanelInteracting.value || fullscreenBusy.value)
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

function toggleSettingsPanel() {
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

function formatTime(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)
  if (h > 0) {
    return `${h}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`
  }
  return `${m}:${s.toString().padStart(2, '0')}`
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

  fullscreenBusy.value = true
  emitInteractionState()
  try {
    const nextFullscreen = !(await appWindow.isFullscreen())
    await appWindow.setFullscreen(nextFullscreen)
    isFullscreen.value = nextFullscreen
    fullscreenError.value = null
  }
  catch {
    try {
      const nextFullscreen = document.fullscreenElement === null
      await toggleBrowserFullscreen(nextFullscreen)
      isFullscreen.value = nextFullscreen
      fullscreenError.value = null
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
  >
    <div class="transport-controls flex shrink-0 items-center gap-2">
      <button
        class="control-button secondary disabled"
        type="button"
        title="上一集（播放列表待接入）"
        aria-label="上一集（播放列表待接入）"
        disabled
      >
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M6 5a1 1 0 0 1 1 1v4.2l8.86-5.01A1.1 1.1 0 0 1 17.5 6.14v11.72a1.1 1.1 0 0 1-1.64.95L7 13.8V18a1 1 0 1 1-2 0V6a1 1 0 0 1 1-1Z" />
        </svg>
      </button>

      <button
        class="control-button secondary"
        type="button"
        title="后退 10 秒"
        aria-label="后退 10 秒"
        @click="emit('seekRelative', -10)"
      >
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M11.4 5.08a1 1 0 0 1 .12 1.41L9.86 8.45H14a6 6 0 1 1-5.66 8H10.6A4 4 0 1 0 14 10.45H9.86l1.66 1.96a1 1 0 1 1-1.52 1.29L6.42 9.47a1 1 0 0 1 0-1.29L10 4.96a1 1 0 0 1 1.4.12Z" />
          <path d="M11.6 13.6h-.95v-.9h2.05V17h-1.1v-3.4Zm3.05-.9h1.15c1.05 0 1.75.86 1.75 2.15 0 1.3-.7 2.15-1.75 2.15h-1.15c-1.06 0-1.76-.86-1.76-2.15 0-1.3.7-2.15 1.76-2.15Zm.1 1c-.45 0-.75.46-.75 1.15 0 .7.3 1.15.75 1.15h.95c.45 0 .75-.46.75-1.15 0-.7-.3-1.15-.75-1.15h-.95Z" />
        </svg>
      </button>

      <button
        class="control-button primary"
        type="button"
        :title="isPlaying ? '暂停' : '播放'"
        :aria-label="isPlaying ? '暂停' : '播放'"
        @click="emit('togglePause')"
      >
        <svg v-if="isPlaying" class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M8 5.75A1.75 1.75 0 0 1 9.75 4h.5A1.75 1.75 0 0 1 12 5.75v12.5A1.75 1.75 0 0 1 10.25 20h-.5A1.75 1.75 0 0 1 8 18.25V5.75Zm6 0A1.75 1.75 0 0 1 15.75 4h.5A1.75 1.75 0 0 1 18 5.75v12.5A1.75 1.75 0 0 1 16.25 20h-.5A1.75 1.75 0 0 1 14 18.25V5.75Z" />
        </svg>
        <svg v-else class="control-icon play-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M8 5.87c0-1.35 1.5-2.16 2.63-1.42l9.2 6.13a1.7 1.7 0 0 1 0 2.84l-9.2 6.13A1.7 1.7 0 0 1 8 18.13V5.87Z" />
        </svg>
      </button>

      <button
        class="control-button secondary"
        type="button"
        title="前进 10 秒"
        aria-label="前进 10 秒"
        @click="emit('seekRelative', 10)"
      >
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12.6 5.08a1 1 0 0 0-.12 1.41l1.66 1.96H10a6 6 0 1 0 5.66 8H13.4A4 4 0 1 1 10 10.45h4.14l-1.66 1.96A1 1 0 1 0 14 13.7l3.58-4.23a1 1 0 0 0 0-1.29L14 4.96a1 1 0 0 0-1.4.12Z" />
          <path d="M6.45 13.6H5.5v-.9h2.05V17h-1.1v-3.4Zm3.05-.9h1.15c1.05 0 1.75.86 1.75 2.15 0 1.3-.7 2.15-1.75 2.15H9.5c-1.06 0-1.76-.86-1.76-2.15 0-1.3.7-2.15 1.76-2.15Zm.1 1c-.45 0-.75.46-.75 1.15 0 .7.3 1.15.75 1.15h.95c.45 0 .75-.46.75-1.15 0-.7-.3-1.15-.75-1.15H9.6Z" />
        </svg>
      </button>

      <button
        class="control-button secondary disabled"
        type="button"
        title="下一集（播放列表待接入）"
        aria-label="下一集（播放列表待接入）"
        disabled
      >
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M18 5a1 1 0 0 0-1 1v4.2L8.14 5.19A1.1 1.1 0 0 0 6.5 6.14v11.72a1.1 1.1 0 0 0 1.64.95L17 13.8V18a1 1 0 1 0 2 0V6a1 1 0 0 0-1-1Z" />
        </svg>
      </button>
    </div>

    <span class="time-label w-16 shrink-0 text-left">
      {{ formatTime(currentTime) }}
    </span>

    <ProgressBar
      class="min-w-0 flex-1"
      :current="currentTime"
      :total="duration"
      @seek="(pos) => emit('seek', pos)"
      @interaction-change="setChildInteracting"
    />

    <span class="time-label w-16 shrink-0 text-right">
      {{ formatTime(duration) }}
    </span>

    <div class="right-controls flex shrink-0 items-center gap-2">
      <VolumeControl
        class="shrink-0"
        :volume="volume"
        @set-volume="(vol) => emit('setVolume', vol)"
        @interaction-change="setChildInteracting"
      />

      <button
        class="control-button action-chip secondary"
        type="button"
        title="倍速（后续接入）"
        aria-label="倍速，后续接入"
        aria-disabled="true"
      >
        <span class="control-text">1.0x</span>
      </button>

      <button
        class="control-button action-chip secondary"
        type="button"
        title="字幕（后续接入）"
        aria-label="字幕，后续接入"
        aria-disabled="true"
      >
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M4.75 5.5h14.5A2.75 2.75 0 0 1 22 8.25v7.5a2.75 2.75 0 0 1-2.75 2.75H4.75A2.75 2.75 0 0 1 2 15.75v-7.5A2.75 2.75 0 0 1 4.75 5.5Zm0 2A.75.75 0 0 0 4 8.25v7.5c0 .41.34.75.75.75h14.5c.41 0 .75-.34.75-.75v-7.5a.75.75 0 0 0-.75-.75H4.75Zm1.5 7.25a1 1 0 0 1 1-1h3.2a1 1 0 1 1 0 2h-3.2a1 1 0 0 1-1-1Zm7.3 0a1 1 0 0 1 1-1h2.2a1 1 0 1 1 0 2h-2.2a1 1 0 0 1-1-1Zm-7.3-3.5a1 1 0 0 1 1-1h1.7a1 1 0 1 1 0 2h-1.7a1 1 0 0 1-1-1Zm5.3 0a1 1 0 0 1 1-1h4.2a1 1 0 1 1 0 2h-4.2a1 1 0 0 1-1-1Z" />
        </svg>
        <span class="control-text">字幕</span>
      </button>

      <button
        class="control-button action-chip secondary"
        type="button"
        title="音轨（后续接入）"
        aria-label="音轨，后续接入"
        aria-disabled="true"
      >
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12.5 4.2a1 1 0 0 1 .5.86v13.88a1 1 0 0 1-1.64.77L7.1 16.2H4.5A2.5 2.5 0 0 1 2 13.7v-3.4a2.5 2.5 0 0 1 2.5-2.5h2.6l4.26-3.5a1 1 0 0 1 1.14-.1Zm4.74 3.1a1 1 0 0 1 1.41 0A6.63 6.63 0 0 1 20.6 12c0 1.84-.75 3.5-1.95 4.7a1 1 0 1 1-1.41-1.42A4.63 4.63 0 0 0 18.6 12c0-1.28-.52-2.44-1.36-3.28a1 1 0 0 1 0-1.42Zm-2.46 2.45a1 1 0 0 1 1.41 0c.58.58.94 1.38.94 2.25s-.36 1.67-.94 2.25a1 1 0 0 1-1.41-1.41c.22-.22.35-.52.35-.84s-.13-.62-.35-.84a1 1 0 0 1 0-1.41Z" />
        </svg>
        <span class="control-text">音轨</span>
      </button>

      <button
        class="control-button action-chip secondary"
        type="button"
        title="播放队列（后续接入）"
        aria-label="播放队列，后续接入"
        aria-disabled="true"
      >
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M4 6.5a1 1 0 0 1 1-1h9a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm0 5.5a1 1 0 0 1 1-1h9a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm0 5.5a1 1 0 0 1 1-1h6.5a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1Zm12.5-4.98c0-.87.96-1.4 1.7-.94l2.88 1.8a1.1 1.1 0 0 1 0 1.86l-2.88 1.8a1.1 1.1 0 0 1-1.7-.94v-3.58Z" />
        </svg>
        <span class="control-text">队列</span>
      </button>

      <button
        ref="settingsButton"
        class="control-button settings-entry-button secondary"
        :class="{ 'is-active': settingsPanelOpen }"
        type="button"
        title="画面设置"
        aria-label="画面设置"
        aria-controls="player-settings-panel"
        :aria-expanded="settingsPanelOpen"
        @click="toggleSettingsPanel"
      >
        <svg class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M4 7.25A3.25 3.25 0 0 1 7.25 4h9.5A3.25 3.25 0 0 1 20 7.25v9.5A3.25 3.25 0 0 1 16.75 20h-9.5A3.25 3.25 0 0 1 4 16.75v-9.5Zm3.25-1.2c-.66 0-1.2.54-1.2 1.2v9.5c0 .66.54 1.2 1.2 1.2h9.5c.66 0 1.2-.54 1.2-1.2v-9.5c0-.66-.54-1.2-1.2-1.2h-9.5Zm1.5 3.2a1 1 0 0 1 1-1h4.5a1 1 0 1 1 0 2h-4.5a1 1 0 0 1-1-1Zm0 3.75a1 1 0 0 1 1-1h2.5a1 1 0 1 1 0 2h-2.5a1 1 0 0 1-1-1Zm6 0a1.25 1.25 0 1 1 2.5 0 1.25 1.25 0 0 1-2.5 0Z" />
        </svg>
        <span class="settings-entry-label">画面</span>
      </button>

      <button
        class="control-button fullscreen-button secondary"
        :class="{ 'is-active': isFullscreen }"
        type="button"
        :title="fullscreenTitle"
        :aria-label="fullscreenTitle"
        :aria-pressed="isFullscreen"
        :disabled="fullscreenBusy"
        @click="toggleFullscreen"
      >
        <svg v-if="isFullscreen" class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M9 4a1 1 0 0 1 1 1v3.25A1.75 1.75 0 0 1 8.25 10H5a1 1 0 0 1 0-2h3V5a1 1 0 0 1 1-1Zm6 0a1 1 0 0 1 1 1v3h3a1 1 0 1 1 0 2h-3.25A1.75 1.75 0 0 1 14 8.25V5a1 1 0 0 1 1-1ZM4 15a1 1 0 0 1 1-1h3.25A1.75 1.75 0 0 1 10 15.75V19a1 1 0 1 1-2 0v-3H5a1 1 0 0 1-1-1Zm10 0.75A1.75 1.75 0 0 1 15.75 14H19a1 1 0 1 1 0 2h-3v3a1 1 0 1 1-2 0v-3.25Z" />
        </svg>
        <svg v-else class="control-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M5 4h4a1 1 0 0 1 0 2H6v3a1 1 0 1 1-2 0V5a1 1 0 0 1 1-1Zm10 1a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v4a1 1 0 1 1-2 0V6h-3a1 1 0 0 1-1-1ZM4 15a1 1 0 1 1 2 0v3h3a1 1 0 1 1 0 2H5a1 1 0 0 1-1-1v-4Zm16-1a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1h-4a1 1 0 1 1 0-2h3v-3a1 1 0 0 1 1-1Z" />
        </svg>
      </button>
    </div>

    <PlayerSettingsPanel
      :open="settingsPanelOpen"
      @close="closeSettingsPanel"
      @interaction-change="setSettingsPanelInteracting"
    />
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

.time-label {
  color: rgba(255, 255, 255, 0.56);
  font-size: 0.75rem;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.02em;
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
