<script setup lang="ts">
import type { PlayerHardwareDecoder } from '@/services/playerInteractionSettings'
import { computed } from 'vue'
import { PLAYER_HARDWARE_DECODER_OPTIONS } from '@/services/playerInteractionSettings'

const props = defineProps<{
  hardwareDecoder: PlayerHardwareDecoder
  pending: boolean
  error: string | null
}>()

const emit = defineEmits<{
  select: [mode: PlayerHardwareDecoder]
}>()

const selectedLabel = computed(() => PLAYER_HARDWARE_DECODER_OPTIONS.find(option => option.value === props.hardwareDecoder)?.label)
</script>

<template>
  <div class="space-y-3" :aria-busy="pending">
    <p class="text-xs leading-5 text-white/58" role="status">
      {{ pending ? '正在应用解码设置…' : `当前选择：${selectedLabel}` }}
    </p>
    <div class="grid gap-2" role="group" aria-label="视频解码方式">
      <button
        v-for="option in PLAYER_HARDWARE_DECODER_OPTIONS"
        :key="option.value"
        type="button"
        class="decoder-option rounded-2xl border px-3 py-2.5 text-left transition-colors"
        :class="hardwareDecoder === option.value ? 'border-white/30 bg-white/14' : 'border-white/10 bg-white/4'"
        :aria-pressed="hardwareDecoder === option.value"
        :disabled="pending"
        @click="emit('select', option.value)"
      >
        <strong class="block text-sm font-semibold text-white/90">{{ option.label }}</strong>
        <span class="mt-1 block text-xs leading-5 text-white/50">{{ option.description }}</span>
      </button>
    </div>
    <p v-if="error" class="rounded-xl border border-amber-300/18 bg-amber-300/10 px-3 py-2 text-xs leading-5 text-amber-100/86" role="alert">
      {{ error }}
    </p>
    <p class="text-xs leading-5 text-white/48">
      在当前影片中应用，也用于后续播放。实际使用的解码器以播放诊断为准。
    </p>
  </div>
</template>

<style scoped>
.decoder-option:hover:not(:disabled),
.decoder-option:focus-visible:not(:disabled) {
  border-color: var(--control-border-hover);
  background: var(--surface-soft-hover);
}

.decoder-option:focus-visible {
  outline: 2px solid var(--control-border-hover);
  outline-offset: 2px;
}

.decoder-option:disabled {
  cursor: wait;
  opacity: 0.6;
}
</style>
