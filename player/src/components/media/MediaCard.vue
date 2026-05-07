<script setup lang="ts">
import type { MediaItem, MediaLibrary } from '@/services/datasource/types'
import { computed } from 'vue'

const props = defineProps<{
  item: MediaItem | MediaLibrary
  kind?: 'poster' | 'library'
  disabled?: boolean
}>()

const emit = defineEmits<{
  select: [item: MediaItem | MediaLibrary]
  play: [item: MediaItem]
}>()

const isMediaItem = computed(() => hasMediaPath(props.item))
const title = computed(() => props.item.name)
const subtitle = computed(() => {
  const item = props.item
  if (!hasMediaPath(item)) {
    const count = item.itemCount == null ? '' : `${item.itemCount} items · `
    return `${count}${item.type}`
  }

  const meta = [item.year, item.type].filter(Boolean)
  return meta.join(' · ')
})
const posterUrl = computed(() => props.item.posterUrl)
const canPlay = computed(() => isMediaItem.value && !props.disabled && props.item.type !== 'folder' && props.item.type !== 'series')

function hasMediaPath(item: MediaItem | MediaLibrary): item is MediaItem {
  return 'path' in item
}

function handleSelect() {
  if (!props.disabled)
    emit('select', props.item)
}

function handlePlay() {
  if (canPlay.value && hasMediaPath(props.item))
    emit('play', props.item)
}
</script>

<template>
  <article
    class="media-card group overflow-hidden rounded-[1.4rem] border transition-all duration-300"
    :class="disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:-translate-y-1 hover:border-white/24'"
    @click="handleSelect"
  >
    <div class="relative aspect-[2/3] overflow-hidden bg-white/5">
      <img
        v-if="posterUrl"
        :src="posterUrl"
        :alt="title"
        class="h-full w-full object-cover transition-transform duration-500 group-hover:scale-105"
      >
      <div v-else class="flex h-full w-full flex-col items-center justify-center gap-3 p-5 text-center">
        <svg width="40" height="40" viewBox="0 0 24 24" fill="none" class="text-white/28">
          <rect x="3" y="3" width="18" height="18" rx="3" stroke="currentColor" stroke-width="1.5" />
          <path d="M8 14l2.5-2.5 2 2L15 11l2 3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <p class="line-clamp-3 text-sm font-semibold text-white/70">
          {{ title }}
        </p>
      </div>

      <div class="absolute inset-0 bg-gradient-to-t from-black/86 via-black/10 to-transparent opacity-80" />

      <button
        v-if="canPlay"
        class="absolute left-1/2 top-1/2 flex h-12 w-12 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-white text-black opacity-0 shadow-2xl transition-all duration-200 hover:scale-110 group-hover:opacity-100"
        aria-label="Play media"
        title="Play media"
        @click.stop="handlePlay"
      >
        <svg width="15" height="15" viewBox="0 0 14 14" fill="currentColor">
          <path d="M3 1l9 6-9 6V1z" />
        </svg>
      </button>

      <div class="absolute inset-x-0 bottom-0 p-4">
        <p class="line-clamp-2 text-sm font-semibold text-white drop-shadow">
          {{ title }}
        </p>
        <p v-if="subtitle" class="mt-1 truncate text-xs text-white/54">
          {{ subtitle }}
        </p>
      </div>
    </div>
  </article>
</template>

<style scoped>
.media-card {
  border-color: var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 40%, transparent);
  box-shadow: var(--shadow-sm);
}
</style>
