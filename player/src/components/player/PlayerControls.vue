<script setup lang="ts">
import { ref } from 'vue'
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

const pointerInside = ref(false)
const focusInside = ref(false)
const childInteracting = ref(false)

function emitInteractionState() {
  emit('interactionChange', pointerInside.value || focusInside.value || childInteracting.value)
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

function formatTime(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)
  if (h > 0) {
    return `${h}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`
  }
  return `${m}:${s.toString().padStart(2, '0')}`
}
</script>

<template>
  <div
    class="player-controls-glass pointer-events-auto mx-auto flex max-w-6xl items-center gap-3 rounded-[28px] px-5 py-3"
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
      class="min-w-36 flex-1"
      :current="currentTime"
      :total="duration"
      @seek="(pos) => emit('seek', pos)"
      @interaction-change="setChildInteracting"
    />

    <span class="time-label w-16 shrink-0 text-right">
      {{ formatTime(duration) }}
    </span>

    <VolumeControl
      :volume="volume"
      @set-volume="(vol) => emit('setVolume', vol)"
      @interaction-change="setChildInteracting"
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
</style>
