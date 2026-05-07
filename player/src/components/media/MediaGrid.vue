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
</script>

<template>
  <div>
    <div v-if="loading" class="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5 xl:grid-cols-6">
      <div v-for="i in 12" :key="i" class="aspect-[2/3] animate-pulse rounded-[1.4rem] bg-white/6" />
    </div>

    <div v-else-if="items.length" class="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5 xl:grid-cols-6">
      <MediaCard
        v-for="item in items"
        :key="item.id"
        :item="item"
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
