<script setup lang="ts">
import type { MediaItem } from '@/services/datasource/types'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'

const props = defineProps<{
  items: MediaItem[]
  interval?: number
  actionLabel?: (item: MediaItem) => string
}>()

const emit = defineEmits<{
  play: [item: MediaItem]
  detail: [item: MediaItem]
}>()

const currentIndex = ref(0)
const isPaused = ref(false)
let timer: ReturnType<typeof setInterval> | null = null
let resumeTimer: ReturnType<typeof setTimeout> | null = null

const currentItem = () => props.items[currentIndex.value] ?? null
const currentActionLabel = computed(() => {
  const item = currentItem()
  return item ? props.actionLabel?.(item) ?? (isPlayable(item) ? '播放' : '查看详情') : ''
})

function next() {
  if (props.items.length === 0)
    return
  currentIndex.value = (currentIndex.value + 1) % props.items.length
}

function prev() {
  if (props.items.length === 0)
    return
  currentIndex.value = (currentIndex.value - 1 + props.items.length) % props.items.length
}

function goTo(index: number) {
  currentIndex.value = index
  resetTimer()
}

function resetTimer() {
  if (timer)
    clearInterval(timer)
  if (!isPaused.value && props.items.length > 1)
    timer = setInterval(next, props.interval ?? 6000)
}

function onUserNav() {
  isPaused.value = true
  if (timer)
    clearInterval(timer)
  if (resumeTimer)
    clearTimeout(resumeTimer)
  // Resume after 15s of inactivity
  resumeTimer = setTimeout(() => {
    isPaused.value = false
    resumeTimer = null
    resetTimer()
  }, 15000)
}

function isPlayable(item: MediaItem | null): boolean {
  return item != null && item.type !== 'folder' && item.type !== 'season'
}

function handlePrimaryAction() {
  const item = currentItem()
  if (!item)
    return

  if (isPlayable(item))
    emit('play', item)
  else
    emit('detail', item)
}

watch(() => props.items.length, () => {
  currentIndex.value = 0
  resetTimer()
})

onMounted(resetTimer)
onUnmounted(() => {
  if (timer)
    clearInterval(timer)
  if (resumeTimer)
    clearTimeout(resumeTimer)
})
</script>

<template>
  <div class="relative min-h-[560px] overflow-hidden bg-black">
    <!-- Background -->
    <div
      v-if="currentItem()"
      :key="currentItem()!.id"
      class="absolute inset-0 h-full w-full transition-all duration-700 ease-out"
    >
      <img
        v-if="currentItem()!.backdropUrl"
        :src="currentItem()!.backdropUrl"
        :alt="currentItem()!.name"
        class="h-full w-full object-cover object-center"
        loading="eager"
        decoding="async"
      >
      <div v-else class="h-full w-full bg-[linear-gradient(135deg,#0c1a2e,#1a3a5c,#0a0a1a)]" />
    </div>
    <div class="absolute inset-0 bg-gradient-to-r from-black/88 via-black/42 to-black/18" />
    <div class="absolute inset-0 bg-gradient-to-t from-black/72 via-black/12 to-black/32" />

    <!-- Content -->
    <div v-if="currentItem()" class="relative flex min-h-[560px] items-end p-10">
      <div class="max-w-2xl">
        <!-- Tagline -->
        <p v-if="currentItem()!.tagline" class="mb-3 text-sm font-medium uppercase tracking-widest text-primary-light/80">
          {{ currentItem()!.tagline }}
        </p>

        <!-- Title -->
        <h2 class="text-5xl font-bold leading-tight text-white drop-shadow-[0_4px_24px_rgba(0,0,0,0.5)]">
          {{ currentItem()!.name }}
        </h2>

        <!-- Meta -->
        <div class="mt-3 flex items-center gap-3 text-sm text-white/70">
          <span v-if="currentItem()!.year">{{ currentItem()!.year }}</span>
          <span v-if="currentItem()!.rating" class="flex items-center gap-1">
            <span class="text-yellow-400">★</span>
            {{ currentItem()!.rating }}
          </span>
          <span v-if="currentItem()!.duration">{{ Math.floor(currentItem()!.duration! / 60) }} min</span>
        </div>

        <!-- Overview -->
        <p class="mt-4 max-w-xl text-base leading-relaxed text-white/65 line-clamp-3">
          {{ currentItem()!.overview }}
        </p>

        <!-- Actions -->
        <div class="mt-6 flex items-center gap-3">
          <button
            class="flex items-center gap-2 rounded-full bg-white px-6 py-3 text-sm font-semibold text-black shadow-lg transition-transform hover:scale-105"
            :aria-label="currentItem() ? `${currentActionLabel} ${currentItem()!.name}` : currentActionLabel"
            @click="handlePrimaryAction"
          >
            <svg v-if="isPlayable(currentItem())" width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
              <path d="M4 2l10 6-10 6V2z" />
            </svg>
            <svg v-else width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M5.5 3.5h5v9h-5a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2Z" stroke="currentColor" stroke-width="1.4" />
              <path d="M7 6h2.2M7 8h2.2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
            </svg>
            {{ currentActionLabel }}
          </button>
          <button
            class="glass flex items-center gap-2 rounded-full px-6 py-3 text-sm font-medium text-white transition-colors hover:bg-white/10"
            :aria-label="currentItem() ? `查看 ${currentItem()!.name} 详情` : '查看详情'"
            @click="currentItem() && emit('detail', currentItem()!)"
          >
            详情
          </button>
        </div>
      </div>
    </div>

    <!-- Navigation dots -->
    <div v-if="items.length > 1" class="absolute bottom-4 right-8 flex items-center gap-2">
      <button
        class="flex h-8 w-8 items-center justify-center rounded-full text-white/50 transition-colors hover:text-white hover:bg-white/10"
        aria-label="上一张推荐"
        title="上一张推荐"
        @click="prev(); onUserNav()"
      >
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <path d="M9 2L4 7l5 5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
      <button
        v-for="(_, i) in items"
        :key="i"
        class="h-2 rounded-full transition-all duration-300"
        :class="i === currentIndex ? 'w-6 bg-white' : 'w-2 bg-white/30 hover:bg-white/50'"
        :aria-label="`切换到第 ${i + 1} 张推荐`"
        @click="goTo(i); onUserNav()"
      />
      <button
        class="flex h-8 w-8 items-center justify-center rounded-full text-white/50 transition-colors hover:text-white hover:bg-white/10"
        aria-label="下一张推荐"
        title="下一张推荐"
        @click="next(); onUserNav()"
      >
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <path d="M5 2l5 5-5 5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
    </div>
  </div>
</template>
