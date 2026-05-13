<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'

interface PictureOption {
  readonly label: string
  readonly description: string
  readonly active?: boolean
}

interface PictureSettingsSection {
  readonly id: string
  readonly title: string
  readonly eyebrow: string
  readonly description: string
  readonly options: readonly PictureOption[]
}

const props = defineProps<{
  open: boolean
}>()

const emit = defineEmits<{
  close: []
  interactionChange: [active: boolean]
}>()

const panelRef = ref<HTMLElement | null>(null)
const pointerInside = ref(false)
const focusInside = ref(false)

const pictureSections: readonly PictureSettingsSection[] = [
  {
    id: 'aspect-ratio',
    title: '画面比例',
    eyebrow: 'Aspect',
    description: '控制视频在透明渲染面中的适配方式，实际 mpv 画面比例命令将在后续子任务接入。',
    options: [
      { label: '适应窗口', active: true, description: '保持完整画面并适配当前窗口' },
      { label: '原始比例', description: '按视频原始比例显示' },
      { label: '填充裁切', description: '填满窗口并裁切边缘' },
      { label: '拉伸填满', description: '忽略原始比例填满画面' },
    ],
  },
]

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
      class="player-settings-panel pointer-events-auto absolute bottom-[calc(100%+1rem)] right-0 z-40 w-[min(22rem,calc(100vw-3rem))] rounded-[28px] p-4 text-white outline-none"
      role="dialog"
      aria-label="画面设置"
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
            Picture Settings
          </p>
          <h2 class="mt-2 text-lg font-semibold text-white">
            画面设置
          </h2>
          <p class="mt-1 text-sm leading-5 text-white/52">
            调整画面比例与显示模式；实际画面命令将在后续子任务接入。
          </p>
        </div>
        <button
          type="button"
          class="panel-icon-button"
          title="关闭画面设置"
          aria-label="关闭画面设置"
          @click="closePanel"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M6.7 5.3a1 1 0 0 0-1.4 1.4L10.58 12 5.3 17.3a1 1 0 1 0 1.4 1.4L12 13.42l5.3 5.28a1 1 0 0 0 1.4-1.4L13.42 12l5.28-5.3a1 1 0 0 0-1.4-1.4L12 10.58 6.7 5.3Z" />
          </svg>
        </button>
      </div>

      <div class="mt-4 space-y-3">
        <article
          v-for="section in pictureSections"
          :key="section.id"
          class="settings-section rounded-3xl p-3"
        >
          <div class="flex items-start justify-between gap-3">
            <div>
              <p class="text-[10px] font-semibold uppercase tracking-[0.22em] text-white/35">
                {{ section.eyebrow }}
              </p>
              <h3 class="mt-1 text-sm font-semibold text-white/88">
                {{ section.title }}
              </h3>
            </div>
            <span class="rounded-full border border-white/8 bg-white/5 px-2 py-1 text-[10px] uppercase tracking-[0.18em] text-white/34">
              待接入
            </span>
          </div>
          <p class="mt-2 text-xs leading-5 text-white/48">
            {{ section.description }}
          </p>
          <div class="mt-3 grid grid-cols-2 gap-2">
            <button
              v-for="option in section.options"
              :key="`${section.id}-${option.label}`"
              type="button"
              class="setting-option"
              :class="{ 'is-active': option.active }"
              :title="`${option.label}：${option.description}（后续接入）`"
              :aria-label="`${option.label}，${option.description}，后续接入`"
              aria-disabled="true"
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

.setting-option.is-active {
  border-color: rgba(255, 255, 255, 0.24);
  color: rgba(255, 255, 255, 0.92);
  background: rgba(255, 255, 255, 0.14);
}

.setting-option[aria-disabled="true"] {
  cursor: default;
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
