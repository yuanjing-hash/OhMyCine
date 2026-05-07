<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{
  current: number
  total: number
}>()

const emit = defineEmits<{
  seek: [position: number]
}>()

const barRef = ref<HTMLDivElement>()
const isDragging = ref(false)

const progress = computed(() => {
  if (!props.total)
    return 0
  return (props.current / props.total) * 100
})

function handleClick(e: MouseEvent) {
  if (!barRef.value || !props.total)
    return
  const rect = barRef.value.getBoundingClientRect()
  const ratio = (e.clientX - rect.left) / rect.width
  emit('seek', ratio * props.total)
}

function handleMouseDown(e: MouseEvent) {
  isDragging.value = true
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
  window.removeEventListener('mousemove', handleMouseMove)
  window.removeEventListener('mouseup', handleMouseUp)
}
</script>

<template>
  <div
    ref="barRef"
    class="group h-6 flex items-center cursor-pointer"
    @mousedown="handleMouseDown"
  >
    <div class="relative w-full h-1 group-hover:h-1.5 bg-surface rounded-full transition-all">
      <div
        class="absolute left-0 top-0 h-full bg-primary rounded-full transition-all"
        :style="{ width: `${progress}%` }"
      />
      <div
        class="absolute top-1/2 -translate-y-1/2 w-3 h-3 bg-primary rounded-full opacity-0 group-hover:opacity-100 transition-opacity"
        :style="{ left: `${progress}%`, transform: `translate(-50%, -50%)` }"
      />
    </div>
  </div>
</template>
