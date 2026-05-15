<script setup lang="ts">
import type { VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import { computed, nextTick, ref, watch } from 'vue'

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
  errorMessage: string | null
}>()

const emit = defineEmits<{
  close: []
  interactionChange: [active: boolean]
  setAspectMode: [mode: VideoAspectMode]
  setFitMode: [mode: VideoFitMode]
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
      class="player-settings-panel pointer-events-auto absolute bottom-[calc(100%+1rem)] right-0 z-40 w-[min(25rem,calc(100vw-3rem))] rounded-[28px] p-4 text-white outline-none"
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
            当前：{{ activeAspectLabel }} · {{ activeFitLabel }}。调整视频比例和窗口适配方式。
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
  border: 1px solid rgba(255, 255, 255, 0.18);
  background:
    radial-gradient(120% 120% at 18% 0%, rgba(255, 255, 255, 0.2), transparent 44%),
    radial-gradient(110% 120% at 100% 100%, rgba(74, 158, 255, 0.18), transparent 48%),
    linear-gradient(135deg, rgba(12, 15, 24, 0.78), rgba(8, 10, 16, 0.58));
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.18),
    inset 0 -1px 0 rgba(255, 255, 255, 0.06),
    0 28px 90px rgba(0, 0, 0, 0.5);
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
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: var(--radius-full);
  color: rgba(255, 255, 255, 0.64);
  background: rgba(255, 255, 255, 0.055);
  transition: transform var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out);
}

.panel-icon-button svg {
  width: 18px;
  height: 18px;
  fill: currentColor;
}

.panel-icon-button:hover,
.panel-icon-button:focus-visible {
  border-color: rgba(255, 255, 255, 0.18);
  color: rgba(255, 255, 255, 0.96);
  background: rgba(255, 255, 255, 0.12);
  transform: translateY(-1px);
}

.settings-section {
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.045);
}

.status-pill {
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: var(--radius-full);
  background: rgba(255, 255, 255, 0.05);
  padding: 0.25rem 0.5rem;
  color: rgba(255, 255, 255, 0.36);
  font-size: 0.62rem;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.setting-option {
  min-height: 34px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: var(--radius-full);
  padding: 0 0.75rem;
  color: rgba(255, 255, 255, 0.66);
  background: rgba(255, 255, 255, 0.055);
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  transition: background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out), opacity var(--duration-fast) var(--ease-out);
}

.setting-option:hover:not(:disabled),
.setting-option:focus-visible:not(:disabled) {
  border-color: rgba(255, 255, 255, 0.18);
  color: rgba(255, 255, 255, 0.92);
  background: rgba(255, 255, 255, 0.1);
}

.setting-option.is-active {
  border-color: rgba(255, 255, 255, 0.24);
  color: rgba(255, 255, 255, 0.92);
  background: rgba(255, 255, 255, 0.14);
}

.setting-option:disabled,
.setting-option.is-disabled {
  cursor: not-allowed;
  color: rgba(255, 255, 255, 0.3);
  background: rgba(255, 255, 255, 0.035);
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
</style>
