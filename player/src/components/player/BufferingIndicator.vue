<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  speedBytesPerSecond: number
}>()

const formattedSpeed = computed(() => {
  const speed = Number.isFinite(props.speedBytesPerSecond)
    ? Math.max(0, props.speedBytesPerSecond)
    : 0

  if (speed >= 1024 * 1024)
    return `${(speed / (1024 * 1024)).toFixed(2)} MB/s`
  if (speed >= 1024)
    return `${(speed / 1024).toFixed(1)} KB/s`
  return speed > 0 ? `${Math.round(speed)} B/s` : '正在连接媒体源'
})
</script>

<template>
  <div class="buffering-indicator pointer-events-none" role="status" aria-live="polite">
    <span class="buffering-spinner" aria-hidden="true" />
    <strong>正在缓冲</strong>
    <span>{{ formattedSpeed }}</span>
  </div>
</template>

<style scoped>
.buffering-indicator {
  position: absolute;
  top: 50%;
  left: 50%;
  z-index: 28;
  display: flex;
  min-width: 8.5rem;
  transform: translate(-50%, -50%);
  flex-direction: column;
  align-items: center;
  gap: 0.4rem;
  border: 1px solid var(--control-border);
  border-radius: 8px;
  background: var(--player-chrome-surface-strong);
  box-shadow: var(--player-chrome-shadow);
  padding: 0.9rem 1.15rem;
  color: var(--color-text);
  text-align: center;
  backdrop-filter: blur(20px) saturate(1.25);
  -webkit-backdrop-filter: blur(20px) saturate(1.25);
}

.buffering-spinner {
  width: 1.7rem;
  height: 1.7rem;
  border: 2px solid var(--control-border-hover);
  border-top-color: var(--color-text);
  border-radius: 50%;
  animation: buffering-spin 820ms linear infinite;
}

.buffering-indicator strong {
  font-size: 0.82rem;
  font-weight: 700;
}

.buffering-indicator > span:last-child {
  color: var(--color-text-secondary);
  font-size: 0.7rem;
  font-variant-numeric: tabular-nums;
}

@keyframes buffering-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .buffering-indicator {
    min-width: 7.6rem;
    gap: 0.3rem;
    padding: 0.75rem 0.95rem;
  }

  .buffering-spinner {
    width: 1.5rem;
    height: 1.5rem;
  }
}
</style>
