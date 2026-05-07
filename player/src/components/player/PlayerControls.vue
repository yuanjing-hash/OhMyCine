<script setup lang="ts">
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
}>()

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
  <div class="glass-card px-4 py-2 flex items-center gap-3">
    <button
      class="w-8 h-8 flex items-center justify-center rounded-full hover:bg-surface-hover text-text-secondary"
      title="后退 10 秒"
      @click="emit('seekRelative', -10)"
    >
      <div class="i-carbon-rewind-10 text-lg" />
    </button>

    <button
      class="w-8 h-8 flex items-center justify-center rounded-full hover:bg-surface-hover text-text"
      @click="emit('togglePause')"
    >
      <div :class="isPlaying ? 'i-carbon-pause-filled' : 'i-carbon-play-filled'" />
    </button>

    <button
      class="w-8 h-8 flex items-center justify-center rounded-full hover:bg-surface-hover text-text-secondary"
      title="前进 10 秒"
      @click="emit('seekRelative', 10)"
    >
      <div class="i-carbon-forward-10 text-lg" />
    </button>

    <span class="text-xs text-text-secondary tabular-nums w-16">
      {{ formatTime(currentTime) }}
    </span>

    <ProgressBar
      class="flex-1"
      :current="currentTime"
      :total="duration"
      @seek="(pos) => emit('seek', pos)"
    />

    <span class="text-xs text-text-secondary tabular-nums w-16 text-right">
      {{ formatTime(duration) }}
    </span>

    <VolumeControl
      :volume="volume"
      @set-volume="(vol) => emit('setVolume', vol)"
    />
  </div>
</template>
