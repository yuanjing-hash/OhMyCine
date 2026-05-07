<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{
  volume: number
}>()

const emit = defineEmits<{
  setVolume: [volume: number]
  interactionChange: [active: boolean]
}>()

const isMuted = ref(false)
const prevVolume = ref(100)

const displayVolume = computed(() => isMuted.value ? 0 : props.volume)

function toggleMute() {
  if (isMuted.value) {
    isMuted.value = false
    emit('setVolume', prevVolume.value)
  }
  else {
    prevVolume.value = props.volume
    isMuted.value = true
    emit('setVolume', 0)
  }
}

function handleVolumeChange(e: Event) {
  const target = e.target as HTMLInputElement
  const vol = Math.max(0, Math.min(100, Number(target.value)))
  isMuted.value = false
  prevVolume.value = vol || prevVolume.value
  emit('setVolume', vol)
}

function finishSliderInteraction() {
  emit('interactionChange', false)
}
</script>

<template>
  <div
    class="volume-control flex items-center gap-2"
    @mouseenter="emit('interactionChange', true)"
    @mouseleave="emit('interactionChange', false)"
    @focusin="emit('interactionChange', true)"
    @focusout="emit('interactionChange', false)"
  >
    <button
      class="volume-button"
      type="button"
      :title="displayVolume === 0 ? '取消静音' : '静音'"
      :aria-label="displayVolume === 0 ? '取消静音' : '静音'"
      @click="toggleMute"
    >
      <div
        :class="displayVolume === 0
          ? 'i-carbon-volume-mute'
          : displayVolume < 50
            ? 'i-carbon-volume-down'
            : 'i-carbon-volume-up'"
        class="text-base"
      />
    </button>
    <input
      type="range"
      min="0"
      max="100"
      :value="displayVolume"
      class="volume-slider h-1 w-24 cursor-pointer"
      aria-label="音量"
      @pointerdown="emit('interactionChange', true)"
      @pointerup="finishSliderInteraction"
      @pointercancel="finishSliderInteraction"
      @blur="finishSliderInteraction"
      @input="handleVolumeChange"
    >
  </div>
</template>

<style scoped>
.volume-button {
  display: flex;
  width: 32px;
  height: 32px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-full);
  color: rgba(255, 255, 255, 0.7);
  transition: background var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out), transform var(--duration-fast) var(--ease-out);
}

.volume-button:hover {
  background: rgba(255, 255, 255, 0.12);
  color: rgba(255, 255, 255, 0.96);
  transform: translateY(-1px);
}

.volume-slider {
  appearance: none;
  border-radius: var(--radius-full);
  background: linear-gradient(90deg, var(--color-primary), rgba(168, 85, 247, 0.72));
  outline: none;
}

.volume-slider::-webkit-slider-thumb {
  width: 12px;
  height: 12px;
  appearance: none;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 0 0 4px rgba(74, 158, 255, 0.16), 0 6px 14px rgba(0, 0, 0, 0.35);
}

.volume-slider::-moz-range-thumb {
  width: 12px;
  height: 12px;
  border: 0;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 0 0 4px rgba(74, 158, 255, 0.16), 0 6px 14px rgba(0, 0, 0, 0.35);
}
</style>
