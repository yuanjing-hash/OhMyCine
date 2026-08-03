<script setup lang="ts">
import type { MediaItem } from '@/services/datasource/types'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useDataSourceStore } from '@/stores/datasource'

const emit = defineEmits<{
  select: [item: MediaItem]
  play: [item: MediaItem]
}>()

const MIN_QUERY_LENGTH = 2
const SEARCH_DEBOUNCE_MS = 280

const store = useDataSourceStore()
const root = ref<HTMLElement | null>(null)
const input = ref<HTMLInputElement | null>(null)
const query = ref('')
const results = ref<MediaItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const expanded = ref(false)
let timer: number | undefined
let searchGeneration = 0

const normalizedQuery = computed(() => query.value.trim())
const showResults = computed(() => expanded.value && normalizedQuery.value.length >= MIN_QUERY_LENGTH)

watch(query, () => {
  if (timer)
    window.clearTimeout(timer)

  const keyword = normalizedQuery.value
  error.value = null
  expanded.value = keyword.length >= MIN_QUERY_LENGTH
  if (keyword.length < MIN_QUERY_LENGTH) {
    results.value = []
    loading.value = false
    return
  }

  const generation = ++searchGeneration
  loading.value = true
  timer = window.setTimeout(() => {
    timer = undefined
    void runSearch(keyword, generation)
  }, SEARCH_DEBOUNCE_MS)
})

async function runSearch(keyword: string, generation: number) {
  try {
    const items = await store.searchAllSources(keyword)
    if (generation !== searchGeneration || keyword !== normalizedQuery.value)
      return
    results.value = items
  }
  catch {
    if (generation !== searchGeneration)
      return
    results.value = []
    error.value = '部分媒体库暂时无法搜索，请稍后重试。'
  }
  finally {
    if (generation === searchGeneration)
      loading.value = false
  }
}

function sourceLabel(item: MediaItem): string {
  const config = store.configs.find(source => source.id === item.sourceId)
  return config?.displayName ?? config?.name ?? item.sourceId
}

function secondaryLabel(item: MediaItem): string {
  const values = [
    item.year ? String(item.year) : '',
    mediaTypeLabel(item.type),
    sourceLabel(item),
  ].filter(Boolean)
  return values.join(' · ')
}

function mediaTypeLabel(type: MediaItem['type']): string {
  switch (type) {
    case 'movie': return '电影'
    case 'series': return '剧集'
    case 'season': return '季'
    case 'episode': return '单集'
    case 'folder': return '文件夹'
    default: return '视频'
  }
}

function canPlay(item: MediaItem): boolean {
  return item.type !== 'folder' && item.type !== 'season'
}

function selectItem(item: MediaItem) {
  expanded.value = false
  input.value?.blur()
  emit('select', item)
}

function playItem(item: MediaItem) {
  expanded.value = false
  input.value?.blur()
  emit('play', item)
}

function clearSearch() {
  query.value = ''
  results.value = []
  expanded.value = false
  input.value?.focus()
}

function handleDocumentPointerDown(event: PointerEvent) {
  if (root.value && event.target instanceof Node && !root.value.contains(event.target))
    expanded.value = false
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    expanded.value = false
    input.value?.blur()
  }
}

onMounted(() => document.addEventListener('pointerdown', handleDocumentPointerDown))
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', handleDocumentPointerDown)
  if (timer)
    window.clearTimeout(timer)
})
</script>

<template>
  <div ref="root" class="home-aggregate-search pointer-events-auto absolute left-1/2 top-20 z-30 w-[min(46rem,calc(100%-2rem))] -translate-x-1/2">
    <div class="search-field flex h-14 items-center gap-3 px-4">
      <svg class="shrink-0 text-white/62" width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <circle cx="11" cy="11" r="6.5" stroke="currentColor" stroke-width="1.8" />
        <path d="m16 16 4 4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
      </svg>
      <input
        ref="input"
        v-model="query"
        type="search"
        class="min-w-0 flex-1 bg-transparent text-base text-white outline-none placeholder:text-white/44"
        placeholder="搜索所有媒体库"
        autocomplete="off"
        spellcheck="false"
        aria-label="搜索所有媒体库"
        @focus="expanded = normalizedQuery.length >= MIN_QUERY_LENGTH"
        @keydown="handleKeydown"
      >
      <span v-if="loading" class="search-spinner h-4 w-4 shrink-0 rounded-full border-2 border-white/24 border-t-white/78" aria-label="搜索中" />
      <button v-else-if="query" type="button" class="search-clear flex h-8 w-8 shrink-0 items-center justify-center text-white/58" aria-label="清除搜索" @click="clearSearch">
        <svg width="16" height="16" viewBox="0 0 20 20" fill="none" aria-hidden="true">
          <path d="m5 5 10 10M15 5 5 15" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
        </svg>
      </button>
    </div>

    <Transition name="search-results">
      <div v-if="showResults" class="search-results mt-2 max-h-[min(31rem,calc(100vh-10rem))] overflow-y-auto p-2 cinema-scrollbar">
        <article
          v-for="item in results"
          :key="`${item.sourceId}:${item.id}`"
          class="search-result group flex w-full items-center gap-3 p-2 text-left"
        >
          <button type="button" class="flex min-w-0 flex-1 items-center gap-3 text-left" @click="selectItem(item)">
            <div class="h-16 w-11 shrink-0 overflow-hidden rounded-md bg-white/8">
              <img v-if="item.posterUrl" :src="item.posterUrl" :alt="item.name" class="h-full w-full object-cover" loading="lazy" decoding="async">
              <div v-else class="flex h-full w-full items-center justify-center text-xs font-bold text-white/36">
                {{ item.name.slice(0, 1) }}
              </div>
            </div>
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm font-semibold text-white/92">
                {{ item.name }}
              </p>
              <p class="mt-1 truncate text-xs text-white/48">
                {{ secondaryLabel(item) }}
              </p>
            </div>
          </button>
          <button
            v-if="canPlay(item)"
            type="button"
            class="search-play flex h-9 w-9 shrink-0 items-center justify-center text-white/76"
            :aria-label="`播放 ${item.name}`"
            @click.stop="playItem(item)"
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
              <path d="M4 2.5 13 8l-9 5.5v-11Z" />
            </svg>
          </button>
        </article>

        <div v-if="!loading && results.length === 0" class="px-4 py-8 text-center text-sm text-white/48">
          {{ error ?? '没有找到匹配的媒体' }}
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.search-field,
.search-results {
  border: 1px solid rgba(255, 255, 255, 0.18);
  border-radius: 8px;
  background: rgba(7, 9, 14, 0.54);
  box-shadow: 0 20px 72px rgba(0, 0, 0, 0.34), inset 0 1px 0 rgba(255, 255, 255, 0.1);
  backdrop-filter: blur(36px) saturate(1.5);
  -webkit-backdrop-filter: blur(36px) saturate(1.5);
}

.search-field:focus-within {
  border-color: rgba(255, 255, 255, 0.34);
  background: rgba(7, 9, 14, 0.68);
}

.search-result {
  border-radius: 6px;
  transition: background 160ms ease;
}

.search-result:hover,
.search-result:focus-visible {
  background: rgba(255, 255, 255, 0.1);
  outline: none;
}

.search-play,
.search-clear {
  border-radius: 50%;
  transition: color 160ms ease, background 160ms ease, transform 160ms ease;
}

.search-play:hover,
.search-clear:hover {
  color: white;
  background: rgba(255, 255, 255, 0.14);
  transform: scale(1.05);
}

.search-spinner {
  animation: search-spin 700ms linear infinite;
}

.search-results-enter-active,
.search-results-leave-active {
  transition: opacity 180ms ease, transform 180ms ease;
}

.search-results-enter-from,
.search-results-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}

@keyframes search-spin {
  to { transform: rotate(360deg); }
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .home-aggregate-search {
    top: max(0.85rem, env(safe-area-inset-top));
    width: calc(100% - 1.5rem);
  }

  .search-field {
    height: 3.25rem;
  }

  .search-results {
    max-height: min(28rem, calc(100vh - 7rem));
  }
}
</style>
