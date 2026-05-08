<script setup lang="ts">
import type { MediaItem, MediaLibrary } from '@/services/datasource/types'
import MediaCard from './MediaCard.vue'

defineProps<{
  items: Array<MediaItem | MediaLibrary>
  loading?: boolean
  emptyTitle?: string
  emptyDescription?: string
}>()

const emit = defineEmits<{
  select: [item: MediaItem | MediaLibrary]
  play: [item: MediaItem]
}>()

function hasMediaPath(item: MediaItem | MediaLibrary): item is MediaItem {
  return 'path' in item
}
</script>

<template>
  <div>
    <div v-if="loading" class="pointer-events-none grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5 xl:grid-cols-6" aria-hidden="true">
      <div v-for="i in 12" :key="i" class="aspect-[2/3] animate-pulse rounded-[1.4rem] bg-white/6" />
    </div>

    <div v-else-if="items.length" class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-5" :class="items.some(hasMediaPath) ? 'media-grid-posters' : 'media-grid-libraries'">
      <MediaCard
        v-for="item in items"
        :key="item.id"
        :item="item"
        :kind="hasMediaPath(item) ? 'poster' : 'library'"
        @select="emit('select', $event)"
        @play="emit('play', $event)"
      />
    </div>

    <div v-else class="glass-panel flex min-h-56 flex-col items-center justify-center rounded-[1.75rem] p-8 text-center">
      <p class="text-base font-semibold text-white">
        {{ emptyTitle ?? '暂无内容' }}
      </p>
      <p class="mt-2 max-w-md text-sm leading-6 text-white/45">
        {{ emptyDescription ?? '当前数据源没有返回可显示的媒体项目。' }}
      </p>
    </div>
  </div>
</template>

<style scoped>
.media-grid-posters {
  grid-template-columns: repeat(auto-fill, minmax(9.5rem, 1fr));
}

.media-grid-libraries {
  grid-template-columns: repeat(auto-fill, minmax(17rem, 1fr));
}
</style>
