<script setup lang="ts">
import type { MediaItem, MediaLibrary } from '@/services/datasource/types'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { savePlaybackMediaContext } from '@/services/playbackContext'
import { listPlaybackHistoryPage, PLAYED_STATE_CHANGED_EVENT, toContinueWatchingMediaItem } from '@/services/playbackHistory'
import { syncPlaybackHistory } from '@/services/playbackHistorySync'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { useDataSourceStore } from '@/stores/datasource'

const PAGE_SIZE = 24
const router = useRouter()
const store = useDataSourceStore()
const page = ref(1)
const total = ref(0)
const hasMore = ref(false)
const loading = ref(true)
const items = ref<MediaItem[]>([])
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))

onMounted(async () => {
  store.loadConfigs()
  await store.syncManager().catch(() => undefined)
  await syncPlaybackHistory(store).catch(() => undefined)
  await loadPage(1)
  window.addEventListener(PLAYED_STATE_CHANGED_EVENT, handleHistoryChanged)
})

onBeforeUnmount(() => {
  window.removeEventListener(PLAYED_STATE_CHANGED_EVENT, handleHistoryChanged)
})

function handleHistoryChanged() {
  void loadPage(page.value)
}

async function loadPage(nextPage: number) {
  loading.value = true
  const result = await listPlaybackHistoryPage(nextPage, PAGE_SIZE)
  page.value = result.page
  total.value = result.total
  hasMore.value = result.hasMore
  items.value = result.list.map(toContinueWatchingMediaItem)
  loading.value = false
}

async function playItem(item: MediaItem | MediaLibrary) {
  if (!('path' in item))
    return
  const contextId = savePlaybackMediaContext({
    sourceId: item.sourceId,
    itemId: item.id,
    title: item.name,
    currentItem: item,
  })
  await router.push({
    name: 'player',
    query: createPlaybackRouteQuery({ sourceId: item.sourceId, itemId: item.id, contextId }),
  })
}
</script>

<template>
  <main class="mobile-nav-safe min-h-full px-5 pb-24 pt-20 sm:px-20">
    <div class="mx-auto max-w-7xl">
      <header class="mb-8 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs uppercase tracking-[.28em] text-white/38">
            History
          </p>
          <h1 class="mt-2 text-3xl font-bold text-white">
            观看历史
          </h1>
          <p class="mt-2 text-sm text-white/48">
            同一 Server 账号下的可用数据源记录会自动合并，并按最近观看时间排列。
          </p>
        </div>
        <p class="m-0 text-sm text-white/42">
          共 {{ total }} 条 · 第 {{ page }} / {{ totalPages }} 页
        </p>
      </header>

      <MediaGrid :items="items" :loading="loading" empty-title="还没有观看历史" empty-description="开始播放后会先保存在本机；连接 Server 后将自动同步到同一账号的其他设备。" @select="playItem" @play="playItem" />

      <nav v-if="totalPages > 1" class="mt-8 flex items-center justify-center gap-3" aria-label="观看历史分页">
        <button type="button" class="rounded-xl bg-white/8 px-5 py-2.5 text-sm text-white disabled:opacity-35" :disabled="loading || page <= 1" @click="loadPage(page - 1)">
          上一页
        </button>
        <span class="text-sm text-white/45">{{ page }} / {{ totalPages }}</span>
        <button type="button" class="rounded-xl bg-white/8 px-5 py-2.5 text-sm text-white disabled:opacity-35" :disabled="loading || !hasMore" @click="loadPage(page + 1)">
          下一页
        </button>
      </nav>
    </div>
  </main>
</template>
