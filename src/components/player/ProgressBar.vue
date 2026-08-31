<script setup lang="ts">
import { computed, ref } from 'vue'

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
let pendingTouch: { pointerId: number, x: number, y: number } | null = null

const progress = computed(() => {
  if (!props.total)
    return 0
  return Math.max(0, Math.min(100, (props.current / props.total) * 100))
})

function updatePosition(event: PointerEvent) {
  if (!barRef.value || !props.total)
    return
  const rect = barRef.value.getBoundingClientRect()
  const ratio = Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width))
  emit('seek', ratio * props.total)
}

function handlePointerDown(event: PointerEvent) {
  if (event.pointerType === 'touch') {
    pendingTouch = { pointerId: event.pointerId, x: event.clientX, y: event.clientY }
    barRef.value?.setPointerCapture(event.pointerId)
    return
  }
  isDragging.value = true
  barRef.value?.setPointerCapture(event.pointerId)
  emit('interactionChange', true)
  updatePosition(event)
}

function handlePointerMove(event: PointerEvent) {
  if (pendingTouch?.pointerId === event.pointerId) {
    const deltaX = event.clientX - pendingTouch.x
    const deltaY = event.clientY - pendingTouch.y
    if (Math.abs(deltaY) > Math.abs(deltaX) && Math.abs(deltaY) >= 12) {
      cancelTouchIntent(event.pointerId)
      return
    }
    if (Math.abs(deltaX) >= 12 && Math.abs(deltaX) > Math.abs(deltaY) * 1.2) {
      pendingTouch = null
      isDragging.value = true
      emit('interactionChange', true)
      updatePosition(event)
    }
    return
  }
  if (isDragging.value)
    updatePosition(event)
}

function handlePointerEnd(event: PointerEvent) {
  if (pendingTouch?.pointerId === event.pointerId) {
    cancelTouchIntent(event.pointerId)
    return
  }
  if (!isDragging.value)
    return
  if (barRef.value?.hasPointerCapture(event.pointerId))
    barRef.value.releasePointerCapture(event.pointerId)
  isDragging.value = false
  emit('interactionChange', false)
}

function cancelTouchIntent(pointerId: number) {
  pendingTouch = null
  if (barRef.value?.hasPointerCapture(pointerId))
    barRef.value.releasePointerCapture(pointerId)
}
</script>

<template>
  <div
    ref="barRef"
    class="progress-hit-area group flex h-7 cursor-pointer items-center"
    @mouseenter="emit('interactionChange', true)"
    @mouseleave="!isDragging && emit('interactionChange', false)"
    @pointerdown.prevent="handlePointerDown"
    @pointermove.prevent="handlePointerMove"
    @pointerup="handlePointerEnd"
    @pointercancel="handlePointerEnd"
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
  background: var(--color-divider);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.34);
}

.progress-fill {
  background: linear-gradient(90deg, var(--color-primary), var(--color-accent));
  box-shadow: 0 0 18px rgba(74, 158, 255, 0.35);
}

.progress-thumb {
  background: var(--color-text);
  box-shadow: 0 0 0 4px rgba(74, 158, 255, 0.18), 0 8px 18px rgba(0, 0, 0, 0.4);
}

.progress-hit-area {
  touch-action: none;
}

@media (max-width: 820px), (hover: none) and (pointer: coarse) {
  .progress-track {
    height: 0.38rem;
  }

  .progress-thumb {
    width: 1rem;
    height: 1rem;
    opacity: 1;
  }
}
</style>
