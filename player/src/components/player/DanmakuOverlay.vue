<script setup lang="ts">
import type { DanmakuComment, DanmakuSettings } from '@/services/danmaku/types'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{
  comments: readonly DanmakuComment[]
  settings: DanmakuSettings
  currentTime: number
  isPlaying: boolean
}>()

const canvas = ref<HTMLCanvasElement | null>(null)
let frame = 0
let observer: ResizeObserver | null = null

function draw() {
  frame = requestAnimationFrame(draw)
  const element = canvas.value
  const context = element?.getContext('2d')
  if (!element || !context)
    return
  const width = element.clientWidth
  const height = element.clientHeight
  const dpr = Math.min(window.devicePixelRatio || 1, 2)
  if (element.width !== Math.round(width * dpr) || element.height !== Math.round(height * dpr)) {
    element.width = Math.round(width * dpr)
    element.height = Math.round(height * dpr)
  }
  context.setTransform(dpr, 0, 0, dpr, 0, 0)
  context.clearRect(0, 0, width, height)
  if (!props.settings.enabled || width <= 0 || height <= 0)
    return

  const fontSize = Math.max(16, Math.min(36, width / 55)) * props.settings.fontScale
  const lineHeight = fontSize * 1.45
  const laneCount = Math.max(1, Math.floor((height * props.settings.displayArea) / lineHeight))
  const densityLanes = Math.max(1, Math.floor(laneCount * props.settings.density))
  context.font = `${props.settings.bold ? 700 : 500} ${fontSize}px system-ui, sans-serif`
  context.textBaseline = 'middle'
  context.lineJoin = 'round'
  context.globalAlpha = props.settings.opacity

  const now = props.currentTime
  const scrollLifetime = 8 / props.settings.speed
  const fixedLifetime = 4
  const keywords = props.settings.blockKeywords.map(item => item.toLocaleLowerCase())
  for (const comment of props.comments) {
    const elapsed = now - comment.time
    const lifetime = comment.mode === 'scroll' ? scrollLifetime : fixedLifetime
    if (elapsed < 0 || elapsed > lifetime || !modeVisible(comment) || blocked(comment.text, keywords))
      continue
    const lane = hash(comment.id) % densityLanes
    const y = comment.mode === 'bottom'
      ? height - (lane + 1) * lineHeight
      : (lane + 0.8) * lineHeight
    const textWidth = context.measureText(comment.text).width
    const x = comment.mode === 'scroll'
      ? width - (elapsed / lifetime) * (width + textWidth)
      : (width - textWidth) / 2
    context.strokeStyle = 'rgba(0,0,0,.9)'
    context.lineWidth = Math.max(2, fontSize * 0.1)
    context.strokeText(comment.text, x, y)
    context.fillStyle = comment.color
    context.fillText(comment.text, x, y)
  }
  context.globalAlpha = 1
}

function modeVisible(comment: DanmakuComment): boolean {
  if (comment.mode === 'scroll')
    return props.settings.showScroll
  if (comment.mode === 'top')
    return props.settings.showTop
  return props.settings.showBottom
}

function blocked(text: string, keywords: string[]): boolean {
  const normalized = text.toLocaleLowerCase()
  return keywords.some(keyword => normalized.includes(keyword))
}

function hash(value: string): number {
  let result = 0
  for (let i = 0; i < value.length; i++)
    result = ((result << 5) - result + value.charCodeAt(i)) | 0
  return Math.abs(result)
}

onMounted(() => {
  observer = new ResizeObserver(() => undefined)
  if (canvas.value)
    observer.observe(canvas.value)
  draw()
})
onBeforeUnmount(() => {
  cancelAnimationFrame(frame)
  observer?.disconnect()
})
watch(() => props.comments, () => undefined)
</script>

<template>
  <canvas ref="canvas" class="pointer-events-none absolute inset-0 z-[8] h-full w-full" aria-hidden="true" />
</template>
