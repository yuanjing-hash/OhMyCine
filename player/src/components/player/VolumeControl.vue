<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{
  volume: number
}>()

const emit = defineEmits<{
  setVolume: [volume: number]
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
  const vol = Number(target.value)
  isMuted.value = false
  emit('setVolume', vol)
}
</script>

<template>
  <div class="flex items-center gap-1">
    <button
      class="w-6 h-6 flex items-center justify-center rounded hover:bg-surface-hover text-text-secondary"
      @click="toggleMute"
    >
      <div
        :class="displayVolume === 0
          ? 'i-carbon-volume-mute'
          : displayVolume < 50
            ? 'i-carbon-volume-down'
            : 'i-carbon-volume-up'"
        class="text-sm"
      />
    </button>
    <input
      type="range"
      min="0"
      max="100"
      :value="displayVolume"
      class="w-20 h-1 accent-primary cursor-pointer"
      @input="handleVolumeChange"
    >
  </div>
</template>
