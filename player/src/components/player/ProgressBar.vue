<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'

const props = defineProps<{
  current: number
  total: number
}>()

const emit = defineEmits<{
  seek: [position: number]
  interactionChange: [active: boolean]
}>()

const barRef = ref<HTMLDivElement>()
const isDragging = ref(false)

const progress = computed(() => {
  if (!props.total)
    return 0
  return Math.max(0, Math.min(100, (props.current / props.total) * 100))
})

function handleClick(e: MouseEvent) {
  if (!barRef.value || !props.total)
    return
  const rect = barRef.value.getBoundingClientRect()
  const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width))
  emit('seek', ratio * props.total)
}

function handleMouseDown(e: MouseEvent) {
  isDragging.value = true
  emit('interactionChange', true)
  handleClick(e)
  window.addEventListener('mousemove', handleMouseMove)
  window.addEventListener('mouseup', handleMouseUp)
}

function handleMouseMove(e: MouseEvent) {
  if (isDragging.value) {
    handleClick(e)
  }
}

function handleMouseUp() {
  isDragging.value = false
  emit('interactionChange', false)
  window.removeEventListener('mousemove', handleMouseMove)
  window.removeEventListener('mouseup', handleMouseUp)
}

onBeforeUnmount(() => {
  window.removeEventListener('mousemove', handleMouseMove)
  window.removeEventListener('mouseup', handleMouseUp)
})
</script>

<template>
  <div
    ref="barRef"
    class="progress-hit-area group flex h-7 cursor-pointer items-center"
    @mouseenter="emit('interactionChange', true)"
    @mouseleave="!isDragging && emit('interactionChange', false)"
    @mousedown="handleMouseDown"
  >
    <div class="progress-track relative h-1.5 w-full overflow-visible rounded-full transition-all group-hover:h-2">
      <div
        class="progress-fill absolute left-0 top-0 h-full rounded-full transition-[width]"
        :style="{ width: `${progress}%` }"
      />
      <div
        class="progress-thumb absolute top-1/2 h-3.5 w-3.5 rounded-full opacity-0 shadow-lg transition-opacity group-hover:opacity-100"
        :style="{ left: `${progress}%`, transform: `translate(-50%, -50%)` }"
      />
    </div>
  </div>
</template>

<style scoped>
.progress-track {
  background: rgba(255, 255, 255, 0.14);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.34);
}

.progress-fill {
  background: linear-gradient(90deg, var(--color-primary), var(--color-accent));
  box-shadow: 0 0 18px rgba(74, 158, 255, 0.35);
}

.progress-thumb {
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 0 0 4px rgba(74, 158, 255, 0.18), 0 8px 18px rgba(0, 0, 0, 0.4);
}
</style>
