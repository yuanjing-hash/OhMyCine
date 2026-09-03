<script setup lang="ts">
import type { MpvPlaybackDiagnostics, VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import type { PlayerFrameInterpolationSettings, PlayerFsrSettings } from '@/services/playerInteractionSettings'
import { computed, nextTick, ref, watch } from 'vue'
import FrameInterpolationSettingsContent from './FrameInterpolationSettingsContent.vue'
import FsrSettingsContent from './FsrSettingsContent.vue'

interface PictureOption<T extends string> {
  readonly value: T
  readonly label: string
  readonly description: string
  readonly disabled?: boolean
}

const props = defineProps<{
  open: boolean
  aspectMode: VideoAspectMode
  fitMode: VideoFitMode
  videoBrightness: number
  errorMessage: string | null
  fsrSettings: PlayerFsrSettings
  fsrError: string | null
  frameInterpolationSettings: PlayerFrameInterpolationSettings
  frameInterpolationDiagnostics: MpvPlaybackDiagnostics | null
  frameInterpolationError: string | null
}>()

const emit = defineEmits<{
  close: []
  interactionChange: [active: boolean]
  setAspectMode: [mode: VideoAspectMode]
  setFitMode: [mode: VideoFitMode]
  setVideoBrightness: [level: number]
  updateFsrSettings: [patch: Partial<PlayerFsrSettings>]
  updateFrameInterpolationSettings: [patch: Partial<PlayerFrameInterpolationSettings>]
}>()

const panelRef = ref<HTMLElement | null>(null)
const pointerInside = ref(false)
const focusInside = ref(false)

const aspectOptions: readonly PictureOption<VideoAspectMode>[] = [
  { value: 'default', label: '原始比例', description: '使用影片自身比例，自动适配窗口' },
  { value: '16:9', label: '16:9', description: '按 16:9 显示画面比例' },
  { value: '4:3', label: '4:3', description: '按 4:3 显示画面比例' },
  { value: 'cinema', label: '2.35:1', description: '按影院宽银幕比例显示画面' },
]

const fitOptions: readonly PictureOption<VideoFitMode | 'stretch'>[] = [
  { value: 'fit', label: '适应窗口', description: '完整显示画面，不主动裁切边缘' },
  { value: 'cinemaCrop', label: '轻微裁切', description: '轻微放大画面，减少上下或左右黑边' },
  { value: 'crop', label: '填充裁切', description: '尽量填满窗口，必要时裁切边缘' },
  { value: 'stretch', label: '拉伸填满', description: '忽略比例拉伸可能导致画面和交互区域不一致，暂不启用', disabled: true },
]

const activeFitLabel = computed(() => fitOptions.find(option => option.value === props.fitMode)?.label ?? '适应窗口')
const activeAspectLabel = computed(() => aspectOptions.find(option => option.value === props.aspectMode)?.label ?? '原始比例')

function emitInteractionState() {
  emit('interactionChange', props.open || pointerInside.value || focusInside.value)
}

function setPointerInside(next: boolean) {
  pointerInside.value = next
  emitInteractionState()
}

function handleFocusIn() {
  focusInside.value = true
  emitInteractionState()
}

function handleFocusOut(event: FocusEvent) {
  const nextTarget = event.relatedTarget instanceof Node ? event.relatedTarget : null
  if (nextTarget && panelRef.value?.contains(nextTarget))
    return

  focusInside.value = false
  emitInteractionState()
}

function closePanel() {
  emit('close')
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape')
    return

  event.preventDefault()
  closePanel()
}

function selectAspect(mode: VideoAspectMode) {
  emit('setAspectMode', mode)
}

function selectFit(value: VideoFitMode | 'stretch') {
  if (value === 'stretch')
    return
  emit('setFitMode', value)
}

watch(
  () => props.open,
  async (open) => {
    if (!open) {
      pointerInside.value = false
      focusInside.value = false
      emitInteractionState()
      return
    }

    emitInteractionState()
    await nextTick()
    panelRef.value?.focus()
  },
  { immediate: true },
)
</script>

<template>
  <Transition name="player-settings-panel">
    <section
      v-if="open"
      id="player-settings-panel"
      ref="panelRef"
      class="player-settings-panel pointer-events-auto absolute bottom-[calc(100%+1rem)] right-0 z-40 max-h-[min(80vh,48rem)] w-[min(25rem,calc(100vw-3rem))] overflow-y-auto rounded-[28px] p-4 text-white outline-none"
      role="dialog"
      aria-label="播放器设置"
      aria-modal="false"
      tabindex="-1"
      @mouseenter="setPointerInside(true)"
      @mouseleave="setPointerInside(false)"
      @focusin="handleFocusIn"
      @focusout="handleFocusOut"
      @keydown="handleKeydown"
      @pointerdown="emitInteractionState"
    >
      <div class="flex items-start justify-between gap-4">
        <div>
          <p class="text-[11px] font-semibold uppercase tracking-[0.24em] text-white/42">
            Player Settings
          </p>
          <h2 class="mt-2 text-lg font-semibold text-white">
            设置
          </h2>
          <p class="mt-1 text-sm leading-5 text-white/52">
            当前：{{ activeAspectLabel }} · {{ activeFitLabel }}。调整画面、插帧与 FSR 超分设置。
          </p>
        </div>
        <button
          type="button"
          class="panel-icon-button"
          title="关闭设置"
          aria-label="关闭设置"
          @click="closePanel"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M6.7 5.3a1 1 0 0 0-1.4 1.4L10.58 12 5.3 17.3a1 1 0 1 0 1.4 1.4L12 13.42l5.3 5.28a1 1 0 0 0 1.4-1.4L13.42 12l5.28-5.3a1 1 0 0 0-1.4-1.4L12 10.58 6.7 5.3Z" />
          </svg>
        </button>
      </div>

      <p v-if="errorMessage" class="mt-4 rounded-2xl border border-amber-300/18 bg-amber-300/10 px-3 py-2 text-xs leading-5 text-amber-100/86">
        {{ errorMessage }}
      </p>

      <div class="mt-4 space-y-3">
        <article class="settings-section rounded-3xl p-3">
          <div class="mb-3 flex items-start justify-between gap-3">
            <div>
              <p class="text-[10px] font-semibold tracking-[0.18em] text-white/35">
                Hardware HDR Frame Generation
              </p>
              <h3 class="mt-1 text-sm font-semibold text-white/88">
                视频插帧
              </h3>
            </div>
            <span class="status-pill">{{ frameInterpolationDiagnostics?.frameInterpolationEffectiveState || 'disabled' }}</span>
          </div>
          <FrameInterpolationSettingsContent
            :settings="frameInterpolationSettings"
            :diagnostics="frameInterpolationDiagnostics"
            :error="frameInterpolationError"
            @update="emit('updateFrameInterpolationSettings', $event)"
          />
        </article>

        <article class="settings-section rounded-3xl p-3">
          <div class="mb-3 flex items-start justify-between gap-3">
            <div>
              <p class="text-[10px] font-semibold tracking-[0.18em] text-white/35">
                FidelityFX Super Resolution 1
              </p>
              <h3 class="mt-1 text-sm font-semibold text-white/88">
                FSR 超分与锐化
              </h3>
            </div>
            <span class="status-pill">{{ fsrSettings.fsrMode }}</span>
          </div>
          <FsrSettingsContent :settings="fsrSettings" :error="fsrError" @update="emit('updateFsrSettings', $event)" />
        </article>

        <article class="settings-section rounded-3xl p-3">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-[10px] font-semibold tracking-[0.18em] text-white/35">
                播放器亮度
              </p>
              <h3 class="mt-1 text-sm font-semibold text-white/88">
                画面亮度
              </h3>
            </div>
            <span class="status-pill">{{ Math.round(videoBrightness) }}%</span>
          </div>
          <input
            class="mt-3 w-full"
            type="range"
            min="0"
            max="100"
            step="1"
            :value="videoBrightness"
            aria-label="播放器画面亮度"
            @input="emit('setVideoBrightness', Number(($event.target as HTMLInputElement).value))"
          >
          <p class="mt-2 text-xs leading-5 text-white/48">
            只调整 mpv 视频画面，不改变设备或显示器亮度。
          </p>
        </article>

        <article class="settings-section rounded-3xl p-3">
          <div class="flex items-start justify-between gap-3">
            <div>
              <p class="text-[10px] font-semibold tracking-[0.18em] text-white/35">
                画面比例
              </p>
              <h3 class="mt-1 text-sm font-semibold text-white/88">
                画面比例
              </h3>
            </div>
            <span class="status-pill">
              当前 {{ activeAspectLabel }}
            </span>
          </div>
          <p class="mt-2 text-xs leading-5 text-white/48">
            调整视频显示比例；选择“原始比例”会使用影片自身比例。
          </p>
          <div class="mt-3 grid grid-cols-2 gap-2">
            <button
              v-for="option in aspectOptions"
              :key="option.value"
              type="button"
              class="setting-option"
              :class="{ 'is-active': props.aspectMode === option.value }"
              :title="`${option.label}：${option.description}`"
              :aria-label="`${option.label}，${option.description}`"
              :aria-pressed="props.aspectMode === option.value"
              @click="selectAspect(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
        </article>

        <article class="settings-section rounded-3xl p-3">
          <div class="flex items-start justify-between gap-3">
            <div>
              <p class="text-[10px] font-semibold tracking-[0.18em] text-white/35">
                画面适配
              </p>
              <h3 class="mt-1 text-sm font-semibold text-white/88">
                画面适配
              </h3>
            </div>
            <span class="status-pill">
              当前 {{ activeFitLabel }}
            </span>
          </div>
          <p class="mt-2 text-xs leading-5 text-white/48">
            控制画面适应窗口或填满窗口的方式；拉伸填满暂不启用。
          </p>
          <div class="mt-3 grid grid-cols-2 gap-2">
            <button
              v-for="option in fitOptions"
              :key="option.value"
              type="button"
              class="setting-option"
              :class="{ 'is-active': props.fitMode === option.value, 'is-disabled': option.disabled }"
              :title="`${option.label}：${option.description}`"
              :aria-label="`${option.label}，${option.description}`"
              :aria-pressed="props.fitMode === option.value"
              :aria-disabled="option.disabled ? 'true' : undefined"
              :disabled="option.disabled"
              @click="selectFit(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
        </article>
      </div>
    </section>
  </Transition>
</template>

<style scoped>
.player-settings-panel {
  border: 1px solid var(--control-border);
  background: var(--player-chrome-surface-strong);
  box-shadow: var(--chrome-shadow);
  backdrop-filter: blur(56px) saturate(1.85) contrast(1.04);
  -webkit-backdrop-filter: blur(56px) saturate(1.85) contrast(1.04);
}

.panel-icon-button {
  display: flex;
  width: 36px;
  height: 36px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--control-border);
  border-radius: var(--radius-full);
  color: var(--color-text-secondary);
  background: var(--surface-soft);
  transition: transform var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out);
}

.panel-icon-button svg {
  width: 18px;
  height: 18px;
  fill: currentColor;
}

.panel-icon-button:hover,
.panel-icon-button:focus-visible {
  border-color: var(--control-border-hover);
  color: var(--color-text);
  background: var(--surface-soft-hover);
  transform: translateY(-1px);
}

.settings-section {
  border: 1px solid var(--color-border);
  background: var(--surface-soft);
}

.status-pill {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-full);
  background: var(--surface-soft);
  padding: 0.25rem 0.5rem;
  color: var(--color-text-tertiary);
  font-size: 0.62rem;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.setting-option {
  min-height: 34px;
  border: 1px solid var(--control-border);
  border-radius: var(--radius-full);
  padding: 0 0.75rem;
  color: var(--color-text-secondary);
  background: var(--surface-soft);
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  transition: background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out), opacity var(--duration-fast) var(--ease-out);
}

.setting-option:hover:not(:disabled),
.setting-option:focus-visible:not(:disabled) {
  border-color: var(--control-border-hover);
  color: var(--color-text);
  background: var(--control-bg-hover);
}

.setting-option.is-active {
  border-color: var(--control-border-hover);
  color: var(--color-text);
  background: var(--surface-soft-hover);
}

.setting-option:disabled,
.setting-option.is-disabled {
  cursor: not-allowed;
  color: var(--color-text-tertiary);
  background: var(--surface-soft);
  opacity: 0.72;
}

.player-settings-panel-enter-active,
.player-settings-panel-leave-active {
  transition: opacity 180ms var(--ease-out), transform 180ms var(--ease-out);
}

.player-settings-panel-enter-from,
.player-settings-panel-leave-to {
  opacity: 0;
  transform: translateY(10px) scale(0.98);
}

@media (max-width: 820px), (hover: none) and (pointer: coarse) {
  .player-settings-panel {
    position: fixed;
    z-index: 1250;
    right: 0.75rem;
    bottom: max(0.75rem, env(safe-area-inset-bottom));
    left: 0.75rem;
    width: auto;
    max-height: min(78svh, 42rem);
    overflow-y: auto;
    border-radius: 8px;
    padding: 0.9rem;
  }

  .settings-section,
  .setting-option {
    border-radius: 8px;
  }

  .setting-option {
    min-height: 2.75rem;
  }
}
</style>
