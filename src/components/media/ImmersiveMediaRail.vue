<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

defineProps<{
  label: string
}>()

const rail = ref<HTMLElement | null>(null)
const canScrollBackward = ref(false)
const canScrollForward = ref(false)
let resizeObserver: ResizeObserver | undefined

function updateScrollEdges() {
  const element = rail.value
  if (!element)
    return
  const maxScrollLeft = Math.max(0, element.scrollWidth - element.clientWidth)
  canScrollBackward.value = element.scrollLeft > 2
  canScrollForward.value = element.scrollLeft < maxScrollLeft - 2
}

function scrollByPage(direction: -1 | 1) {
  const element = rail.value
  if (!element)
    return
  element.scrollBy({
    left: direction * Math.max(240, element.clientWidth * 0.78),
    behavior: 'smooth',
  })
}

function handleRailKeydown(event: KeyboardEvent) {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight')
    return
  event.preventDefault()
  scrollByPage(event.key === 'ArrowLeft' ? -1 : 1)
}

onMounted(() => {
  void nextTick(updateScrollEdges)
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(updateScrollEdges)
    if (rail.value)
      resizeObserver.observe(rail.value)
  }
  window.addEventListener('resize', updateScrollEdges)
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  window.removeEventListener('resize', updateScrollEdges)
})
</script>

<template>
  <div class="immersive-rail-shell group/rail relative min-w-0">
    <div
      class="immersive-rail-fade pointer-events-none absolute inset-y-0 left-0 z-10 w-14 bg-gradient-to-r from-[var(--color-bg)] via-[color-mix(in_srgb,var(--color-bg)_72%,transparent)] to-transparent transition-opacity"
      :class="canScrollBackward ? 'opacity-100' : 'opacity-0'"
      aria-hidden="true"
    />
    <div
      class="immersive-rail-fade pointer-events-none absolute inset-y-0 right-0 z-10 w-20 bg-gradient-to-l from-[var(--color-bg)] via-[color-mix(in_srgb,var(--color-bg)_72%,transparent)] to-transparent transition-opacity"
      :class="canScrollForward ? 'opacity-100' : 'opacity-0'"
      aria-hidden="true"
    />

    <button
      type="button"
      class="immersive-rail-button left-3"
      :class="canScrollBackward ? 'is-visible' : ''"
      :disabled="!canScrollBackward"
      :aria-label="`${label}向左滚动`"
      @click="scrollByPage(-1)"
    >
      <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="M12.5 4.5 7 10l5.5 5.5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
    </button>
    <button
      type="button"
      class="immersive-rail-button right-3"
      :class="canScrollForward ? 'is-visible' : ''"
      :disabled="!canScrollForward"
      :aria-label="`${label}向右滚动`"
      @click="scrollByPage(1)"
    >
      <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m7.5 4.5 5.5 5.5-5.5 5.5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
    </button>

    <div
      ref="rail"
      class="immersive-rail overflow-x-auto"
      :aria-label="label"
      tabindex="0"
      @scroll.passive="updateScrollEdges"
      @keydown="handleRailKeydown"
    >
      <slot />
    </div>
  </div>
</template>

<style scoped>
.immersive-rail {
  -ms-overflow-style: none;
  scrollbar-width: none;
  overscroll-behavior-x: contain;
  scroll-behavior: smooth;
}

.immersive-rail::-webkit-scrollbar {
  display: none;
}

.immersive-rail:focus-visible {
  border-radius: 1.5rem;
  outline: 2px solid rgba(255, 255, 255, 0.28);
  outline-offset: 4px;
}

.immersive-rail-button {
  position: absolute;
  top: 50%;
  z-index: 20;
  display: inline-flex;
  width: 3rem;
  height: 3rem;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--glass-border-hover);
  border-radius: 999px;
  color: var(--color-text);
  background: color-mix(in srgb, var(--color-bg) 54%, transparent);
  box-shadow: var(--glass-shadow-elevated);
  opacity: 0;
  backdrop-filter: blur(24px) saturate(1.2);
  -webkit-backdrop-filter: blur(24px) saturate(1.2);
  transform: translateY(-50%) scale(0.92);
  transition: opacity var(--duration-fast) var(--ease-out), transform var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out);
}

.immersive-rail-button.is-visible:focus-visible,
.group\/rail:hover .immersive-rail-button.is-visible {
  opacity: 1;
  transform: translateY(-50%) scale(1);
}

.immersive-rail-button:hover:not(:disabled) {
  background: var(--glass-bg-active);
  transform: translateY(-50%) scale(1.06);
}

.immersive-rail-button:disabled {
  pointer-events: none;
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .immersive-rail-button,
  .immersive-rail-fade {
    display: none;
  }

  .immersive-rail {
    -webkit-overflow-scrolling: touch;
    scroll-snap-type: x proximity;
  }
}
</style>
