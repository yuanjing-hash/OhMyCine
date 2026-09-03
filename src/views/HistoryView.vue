<script setup lang="ts">
import type { MediaItem, MediaLibrary } from '@/services/datasource/types'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { savePlaybackMediaContext } from '@/services/playbackContext'
import { listPlaybackHistoryPage, toContinueWatchingMediaItem } from '@/services/playbackHistory'
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
const activeSourceId = ref('local')
const activeLibraryId = ref<string | undefined>()
const remoteCursor = ref<string | undefined>()
const remoteHasMore = ref(false)
const errorMessage = ref('')
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))
const remoteSources = ref<Array<{ key: string, sourceId: string, libraryId?: string, label: string }>>([])

onMounted(async () => {
  store.loadConfigs()
  await store.syncManager().catch(() => undefined)
  await loadRemoteSources()
  await loadPage(1)
})

async function loadRemoteSources() {
  const next: Array<{ key: string, sourceId: string, libraryId?: string, label: string }> = []
  for (const config of store.orderedConfigs.filter(item => item.type === 'server' && item.enabled !== false)) {
    const source = store.getSource(config.id)
    next.push({ key: config.id, sourceId: config.id, label: `${config.displayName ?? config.name} 历史` })
    if (source?.listLibraries) {
      try {
        const libraries = await source.listLibraries()
        for (const library of libraries.filter(item => item.providerIdentity?.startsWith('plugin:'))) {
          next.push({ key: `${config.id}:${library.id}`, sourceId: config.id, libraryId: library.id, label: `${library.name} 历史` })
        }
      }
      catch {
        // Keep the Server-level history entry so older Servers fail softly.
      }
    }
  }
  remoteSources.value = next
}

async function loadPage(nextPage: number) {
  activeSourceId.value = 'local'
  activeLibraryId.value = undefined
  errorMessage.value = ''
  loading.value = true
  const result = await listPlaybackHistoryPage(nextPage, PAGE_SIZE)
  page.value = result.page
  total.value = result.total
  hasMore.value = result.hasMore
  items.value = result.list.map(toContinueWatchingMediaItem)
  loading.value = false
}

async function selectRemoteSource(sourceId: string, libraryId?: string) {
  activeSourceId.value = sourceId
  activeLibraryId.value = libraryId
  remoteCursor.value = undefined
  remoteHasMore.value = false
  items.value = []
  errorMessage.value = ''
  await loadRemotePage(false)
}

async function loadRemotePage(append: boolean) {
  const source = store.getSource(activeSourceId.value)
  if (!source?.listPlaybackHistory) {
    errorMessage.value = '当前 Server 版本或在线插件没有提供历史记录能力。'
    return
  }
  loading.value = true
  try {
    const result = await source.listPlaybackHistory({ cursor: append ? remoteCursor.value : undefined, limit: PAGE_SIZE, libraryId: activeLibraryId.value })
    items.value = append ? [...items.value, ...result.items] : result.items
    remoteCursor.value = result.cursor
    remoteHasMore.value = result.hasMore
  }
  catch {
    errorMessage.value = '在线来源历史加载失败，本机历史仍可正常使用。'
  }
  finally {
    loading.value = false
  }
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
            本机记录按最近观看时间稳定分页；在线来源历史会按来源能力接入同一页面。
          </p>
        </div>
        <p v-if="activeSourceId === 'local'" class="m-0 text-sm text-white/42">
          共 {{ total }} 条 · 第 {{ page }} / {{ totalPages }} 页
        </p>
      </header>

      <nav class="mb-6 flex flex-wrap gap-2" aria-label="历史记录来源">
        <button type="button" class="rounded-xl px-4 py-2 text-sm" :class="activeSourceId === 'local' ? 'bg-white/16 text-white' : 'bg-white/6 text-white/55'" @click="loadPage(1)">
          Player 本机
        </button>
        <button v-for="source in remoteSources" :key="source.key" type="button" class="rounded-xl px-4 py-2 text-sm" :class="activeSourceId === source.sourceId && activeLibraryId === source.libraryId ? 'bg-white/16 text-white' : 'bg-white/6 text-white/55'" @click="selectRemoteSource(source.sourceId, source.libraryId)">
          {{ source.label }}
        </button>
      </nav>

      <p v-if="errorMessage" class="mb-5 rounded-2xl border border-amber-400/18 bg-amber-400/7 p-4 text-sm text-amber-100">
        {{ errorMessage }}
      </p>

      <MediaGrid :items="items" :loading="loading" empty-title="还没有观看历史" empty-description="开始播放后，进度会先安全保存在本机。" @select="playItem" @play="playItem" />

      <nav v-if="activeSourceId === 'local' && totalPages > 1" class="mt-8 flex items-center justify-center gap-3" aria-label="观看历史分页">
        <button type="button" class="rounded-xl bg-white/8 px-5 py-2.5 text-sm text-white disabled:opacity-35" :disabled="loading || page <= 1" @click="loadPage(page - 1)">
          上一页
        </button>
        <span class="text-sm text-white/45">{{ page }} / {{ totalPages }}</span>
        <button type="button" class="rounded-xl bg-white/8 px-5 py-2.5 text-sm text-white disabled:opacity-35" :disabled="loading || !hasMore" @click="loadPage(page + 1)">
          下一页
        </button>
      </nav>
      <div v-else-if="activeSourceId !== 'local' && remoteHasMore" class="mt-8 flex justify-center">
        <button type="button" class="rounded-xl bg-white/8 px-6 py-2.5 text-sm text-white disabled:opacity-35" :disabled="loading" @click="loadRemotePage(true)">
          {{ loading ? '加载中…' : '加载更多' }}
        </button>
      </div>
    </div>
  </main>
</template>
