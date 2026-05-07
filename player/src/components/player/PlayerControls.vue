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
    <button
      class="control-button secondary"
      type="button"
      title="后退 10 秒"
      aria-label="后退 10 秒"
      @click="emit('seekRelative', -10)"
    >
      <div class="i-carbon-rewind-10 text-xl" />
    </button>

    <button
      class="control-button primary"
      type="button"
      :title="isPlaying ? '暂停' : '播放'"
      :aria-label="isPlaying ? '暂停' : '播放'"
      @click="emit('togglePause')"
    >
      <div :class="isPlaying ? 'i-carbon-pause-filled' : 'i-carbon-play-filled'" class="text-xl" />
    </button>

    <button
      class="control-button secondary"
      type="button"
      title="前进 10 秒"
      aria-label="前进 10 秒"
      @click="emit('seekRelative', 10)"
    >
      <div class="i-carbon-forward-10 text-xl" />
    </button>

    <span class="time-label w-16 text-left">
      {{ formatTime(currentTime) }}
    </span>

    <ProgressBar
      class="flex-1"
      :current="currentTime"
      :total="duration"
      @seek="(pos) => emit('seek', pos)"
      @interaction-change="setChildInteracting"
    />

    <span class="time-label w-16 text-right">
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

.control-button {
  display: flex;
  width: 40px;
  height: 40px;
  align-items: center;
  justify-content: center;
  border: 1px solid transparent;
  border-radius: var(--radius-full);
  color: rgba(255, 255, 255, 0.76);
  transition: transform var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out), box-shadow var(--duration-fast) var(--ease-out);
}

.control-button:hover {
  border-color: rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.12);
  color: rgba(255, 255, 255, 0.98);
  transform: translateY(-1px);
}

.control-button:active {
  transform: translateY(0) scale(0.96);
}

.control-button.primary {
  background: rgba(255, 255, 255, 0.92);
  color: rgba(8, 10, 16, 0.96);
  box-shadow: 0 12px 28px rgba(255, 255, 255, 0.12);
}

.control-button.primary:hover {
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
