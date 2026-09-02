<script setup lang="ts">
import type { MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { ServerDiscoveryWork } from '@/services/serverDiscovery'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ServerDataSource } from '@/services/datasource/server'
import { artworkCacheKey } from '@/services/imageCache'
import { beginMediaActionLongPress, cancelMediaActionLongPress, createMediaActionTarget, endMediaActionLongPress, handleMediaActionKeyboard, moveMediaActionLongPress, openMediaActionContextMenu, suppressMediaActionClick } from '@/services/mediaActions'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { getPlaybackCompletionBatch, playbackCompletionKey, PLAYED_STATE_CHANGED_EVENT } from '@/services/playbackHistory'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { searchServerDiscovery } from '@/services/serverDiscovery'
import { useDataSourceStore } from '@/stores/datasource'
import { useSearchWorkspaceStore } from '@/stores/searchWorkspace'
import CachedImage from './CachedImage.vue'

const SEARCH_DEBOUNCE_MS = 260
const ALL_FILTER = 'all'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const workspace = useSearchWorkspaceStore()
const inputRef = ref<HTMLInputElement | null>(null)
const query = ref('')
const results = ref<MediaItem[]>([])
const discoveryResults = ref<Array<{ sourceId: string, sourceName: string, work: ServerDiscoveryWork }>>([])
const libraries = ref<MediaLibrary[]>([])
const selectedSourceId = ref(ALL_FILTER)
const selectedLibraryId = ref(ALL_FILTER)
const selectedType = ref(ALL_FILTER)
const loading = ref(false)
const loadingDiscovery = ref(false)
const loadingLibraries = ref(false)
const error = ref<string | null>(null)
const checkingServerCapabilities = ref(false)
const serverCapabilityRevision = ref(0)
const completedItemKeys = ref<Set<string>>(new Set())
let searchTimer: number | undefined
let searchGeneration = 0

const enabledSources = computed(() => store.orderedConfigs.filter(config => config.enabled !== false))
const enabledServerSources = computed(() => {
  // ServerDataSource owns the validated capability set; the revision makes
  // successful bootstrap refreshes observable without duplicating that state.
  void serverCapabilityRevision.value
  const sources = enabledSources.value.flatMap((config) => {
    if (selectedSourceId.value !== ALL_FILTER && selectedSourceId.value !== config.id)
      return []
    const source = store.getSource(config.id)
    return source instanceof ServerDataSource ? [{ config, source }] : []
  })
  return sources
})
const discoveryServerSources = computed(() => enabledServerSources.value.filter(({ source }) => source.hasCapability('discovery_search')))
const serverDiscoveryHint = computed(() => {
  if (checkingServerCapabilities.value)
    return '检查权限中…'
  if (!enabledServerSources.value.length)
    return '需连接 Server'
  if (!discoveryServerSources.value.length)
    return '账号无搜索权限'
  return '继续'
})
const normalizedQuery = computed(() => query.value.trim())
const sourceLibraries = computed(() => libraries.value.filter(library =>
  selectedSourceId.value === ALL_FILTER || library.sourceId === selectedSourceId.value,
))
const suggestionItems = computed(() => filterItems(uniqueItems(
  store.homeSections.flatMap(section => section.items).filter(item => item.sourceId !== 'placeholder'),
)).slice(0, 12))
const suggestedKeywords = computed(() => filterItems(uniqueItems(
  store.homeSections.flatMap(section => section.items).filter(item => item.sourceId !== 'placeholder'),
)).map(item => item.name).filter(Boolean).slice(0, 14))
const filteredResults = computed(() => filterItems(results.value))
const hasActiveQuery = computed(() => normalizedQuery.value.length > 0)
const statusLabel = computed(() => {
  if (loading.value)
    return '正在聚合媒体库结果'
  if (!hasActiveQuery.value)
    return `${suggestionItems.value.length} 个馆藏推荐`
  return `${filteredResults.value.length + discoveryResults.value.length} 个结果`
})

watch(() => workspace.open, async (open) => {
  if (!open)
    return
  store.loadConfigs()
  void store.loadHomeSections({ background: store.homeSections.length > 0 })
  void loadLibraries()
  void refreshServerCapabilities()
  void refreshSearchPlayedStates()
  await nextTick()
  window.setTimeout(() => inputRef.value?.focus(), 80)
})

async function refreshServerCapabilities() {
  checkingServerCapabilities.value = true
  try {
    await store.syncManager()
    await Promise.allSettled(enabledServerSources.value.map(({ source }) => source.refreshCapabilities()))
  }
  finally {
    serverCapabilityRevision.value += 1
    checkingServerCapabilities.value = false
  }
}

watch(() => route.fullPath, () => workspace.hide())

watch(query, () => {
  if (searchTimer)
    window.clearTimeout(searchTimer)
  error.value = null
  loadingDiscovery.value = false
  const keyword = normalizedQuery.value
  if (!keyword) {
    results.value = []
    discoveryResults.value = []
    loading.value = false
    searchGeneration += 1
    return
  }

  const generation = ++searchGeneration
  loading.value = true
  searchTimer = window.setTimeout(() => {
    searchTimer = undefined
    void runSearch(keyword, generation)
  }, SEARCH_DEBOUNCE_MS)
})

watch(selectedSourceId, () => {
  if (selectedLibraryId.value !== ALL_FILTER && !sourceLibraries.value.some(library => libraryFilterKey(library) === selectedLibraryId.value))
    selectedLibraryId.value = ALL_FILTER
  if (hasActiveQuery.value)
    restartSearch()
})

function filterItems(items: readonly MediaItem[]): MediaItem[] {
  return items.filter((item) => {
    if (selectedSourceId.value !== ALL_FILTER && item.sourceId !== selectedSourceId.value)
      return false
    if (selectedLibraryId.value !== ALL_FILTER && `${item.sourceId}:${item.libraryId ?? ''}` !== selectedLibraryId.value)
      return false
    if (selectedType.value !== ALL_FILTER && normalizedMediaType(item.type) !== selectedType.value)
      return false
    return true
  })
}

function uniqueItems(items: readonly MediaItem[]): MediaItem[] {
  const seen = new Set<string>()
  return items.filter((item) => {
    const key = `${item.sourceId}:${item.id}`
    if (seen.has(key))
      return false
    seen.add(key)
    return true
  })
}

async function loadLibraries() {
  loadingLibraries.value = true
  const cached = enabledSources.value.flatMap(config => store.getSourceRootSnapshot(config.id)?.libraries ?? [])
  libraries.value = uniqueLibraries(cached)
  try {
    await store.syncManager()
    const settled = await Promise.allSettled(enabledSources.value.map(async (config) => {
      const source = store.getSource(config.id)
      return source?.listLibraries ? source.listLibraries() : []
    }))
    libraries.value = uniqueLibraries([
      ...libraries.value,
      ...settled.flatMap(result => result.status === 'fulfilled' ? result.value : []),
    ])
  }
  finally {
    loadingLibraries.value = false
  }
}

function uniqueLibraries(items: readonly MediaLibrary[]): MediaLibrary[] {
  return [...new Map(items.map(item => [`${item.sourceId}:${item.id}`, item])).values()]
}

async function runSearch(keyword: string, generation: number) {
  try {
    const sourceIds = selectedSourceId.value === ALL_FILTER ? undefined : [selectedSourceId.value]
    const items = await store.searchAllSources(keyword, 100, sourceIds)
    if (generation !== searchGeneration || keyword !== normalizedQuery.value)
      return
    results.value = uniqueItems(items)
    void refreshSearchPlayedStates()
  }
  catch {
    if (generation !== searchGeneration)
      return
    results.value = []
    discoveryResults.value = []
    error.value = '部分媒体源暂时无法搜索，请稍后重试。'
  }
  finally {
    if (generation === searchGeneration)
      loading.value = false
  }
}

async function searchServerMore() {
  const keyword = normalizedQuery.value
  const discoveryType = selectedType.value === 'movie' ? 'movie' : selectedType.value === 'series' ? 'tv' : selectedType.value === 'other' ? null : 'all'
  if (!keyword || !discoveryType)
    return
  loadingDiscovery.value = true
  error.value = null
  const generation = searchGeneration
  try {
    await store.syncManager()
    await Promise.allSettled(enabledServerSources.value.map(({ source }) => source.refreshCapabilities()))
    serverCapabilityRevision.value += 1
    const serverSources = discoveryServerSources.value
    if (!enabledServerSources.value.length)
      throw new Error('请先连接并启用 OhMyCine Server。')
    if (!serverSources.length)
      throw new Error('当前 Server 账号没有影视搜索权限，请让管理员分配“发现搜索”能力。')
    const settled = await Promise.allSettled(serverSources.map(async ({ config, source }) => (await searchServerDiscovery(source, keyword, discoveryType)).map(work => ({ sourceId: config.id, sourceName: config.displayName ?? config.name, work }))))
    if (generation !== searchGeneration || keyword !== normalizedQuery.value)
      return
    discoveryResults.value = settled.flatMap(result => result.status === 'fulfilled' ? result.value : [])
    if (!discoveryResults.value.length && settled.every(result => result.status === 'rejected'))
      throw new Error('Server 暂时无法搜索更多影视，请检查连接或账号权限。')
  }
  catch (reason) {
    if (generation === searchGeneration)
      error.value = reason instanceof Error ? reason.message : 'Server 搜索失败。'
  }
  finally {
    if (generation === searchGeneration)
      loadingDiscovery.value = false
  }
}

function restartSearch() {
  const keyword = normalizedQuery.value
  if (!keyword)
    return
  const generation = ++searchGeneration
  loading.value = true
  void runSearch(keyword, generation)
}

function chooseType(type: string) {
  selectedType.value = type
  discoveryResults.value = []
}

function chooseLibrary(libraryId: string) {
  selectedLibraryId.value = libraryId
}

function libraryFilterKey(library: MediaLibrary): string {
  return `${library.sourceId}:${library.id}`
}

function searchSuggestion(keyword: string) {
  query.value = keyword
}

function clearSearch() {
  query.value = ''
  inputRef.value?.focus()
}

function sourceLabel(item: MediaItem): string {
  const source = store.configs.find(config => config.id === item.sourceId)
  return source?.displayName ?? source?.name ?? item.sourceId
}

function searchActionTarget(item: MediaItem) {
  const source = store.configs.find(config => config.id === item.sourceId)
  return createMediaActionTarget({ ...item, played: isSearchItemPlayed(item) }, source?.type, source?.displayName ?? source?.name)
}

function isSearchItemPlayed(item: MediaItem): boolean {
  const sourceType = store.configs.find(config => config.id === item.sourceId)?.type
  if (sourceType === 'emby' || sourceType === 'jellyfin')
    return item.played === true
  return item.played === true || completedItemKeys.value.has(playbackCompletionKey(item.sourceId, item.id))
}

async function refreshSearchPlayedStates() {
  const items = uniqueItems([...suggestionItems.value, ...filteredResults.value])
  const entries = await getPlaybackCompletionBatch(items.map(item => ({ sourceId: item.sourceId, mediaIdentity: item.id })))
  completedItemKeys.value = new Set(entries.filter(entry => entry.completed).map(entry => playbackCompletionKey(entry.sourceId, entry.mediaIdentity)))
}

function beginSearchActionLongPress(item: MediaItem, event: PointerEvent) {
  beginMediaActionLongPress(searchActionTarget(item), event)
}

function openSearchActionMenu(item: MediaItem, event: MouseEvent) {
  openMediaActionContextMenu(searchActionTarget(item), event)
}

function handleSearchItemClick(item: MediaItem, event: MouseEvent) {
  if (!suppressMediaActionClick(event))
    openItem(item)
}

function handleSearchItemKey(item: MediaItem, event: KeyboardEvent) {
  handleMediaActionKeyboard(searchActionTarget(item), event, () => openItem(item))
}

function mediaMeta(item: MediaItem): string {
  return [item.year, mediaTypeLabel(item.type), sourceLabel(item)].filter(Boolean).join(' · ')
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

function normalizedMediaType(type: MediaItem['type']): string {
  if (type === 'movie')
    return 'movie'
  if (type === 'series' || type === 'season' || type === 'episode')
    return 'series'
  return 'other'
}

function canPlay(item: MediaItem): boolean {
  return !['folder', 'series', 'season'].includes(item.type)
}

function openItem(item: MediaItem) {
  workspace.hide()
  void router.push({ name: 'media-detail', params: { sourceId: item.sourceId, itemId: item.id } })
}

function openDiscoveryItem(item: { sourceId: string, work: ServerDiscoveryWork }) {
  workspace.hide()
  void router.push({ name: 'server-discovery-detail', params: { sourceId: item.sourceId, provider: item.work.provider, mediaType: item.work.mediaType, providerId: item.work.providerId } })
}

async function playItem(item: MediaItem) {
  if (!canPlay(item)) {
    openItem(item)
    return
  }
  const contextId = savePlaybackMediaContext({
    sourceId: item.sourceId,
    itemId: item.id,
    title: item.name,
    currentItem: item,
    queue: createPlaybackQueue(filteredResults.value.filter(canPlay), item.id),
  })
  workspace.hide()
  await router.push({
    name: 'player',
    query: createPlaybackRouteQuery({
      sourceId: item.sourceId,
      itemId: item.id,
      contextId,
    }),
  })
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape')
    workspace.hide()
}

onMounted(() => window.addEventListener(PLAYED_STATE_CHANGED_EVENT, refreshSearchPlayedStates))
onBeforeUnmount(() => {
  window.removeEventListener(PLAYED_STATE_CHANGED_EVENT, refreshSearchPlayedStates)
  if (searchTimer)
    window.clearTimeout(searchTimer)
})
</script>

<template>
  <Teleport to="body">
    <Transition name="search-workspace">
      <div v-if="workspace.open" class="search-workspace-layer fixed inset-0 z-[1050]" @keydown="handleKeydown">
        <button class="search-workspace-scrim absolute inset-0" type="button" aria-label="关闭搜索" @click="workspace.hide" />
        <section class="search-workspace theme-adaptive absolute flex flex-col overflow-hidden" role="dialog" aria-modal="true" aria-label="聚合搜索">
          <header class="search-workspace-header flex items-center gap-3 px-5 py-4 sm:px-7">
            <button class="mobile-close flex h-10 w-10 shrink-0 items-center justify-center" type="button" aria-label="关闭搜索" @click="workspace.hide">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="m15 18-6-6 6-6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </button>
            <div class="search-input flex min-w-0 flex-1 items-center gap-3 px-4">
              <svg class="shrink-0" width="21" height="21" viewBox="0 0 24 24" fill="none" aria-hidden="true"><circle cx="11" cy="11" r="6.5" stroke="currentColor" stroke-width="1.8" /><path d="m16 16 4 4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg>
              <input ref="inputRef" v-model="query" type="text" class="min-w-0 flex-1 bg-transparent text-base outline-none" placeholder="搜索所有媒体库" autocomplete="off" spellcheck="false">
              <span v-if="loading" class="search-spinner h-4 w-4 shrink-0 rounded-full border-2" aria-label="搜索中" />
              <button v-else-if="query" class="clear-button flex h-8 w-8 shrink-0 items-center justify-center" type="button" aria-label="清除搜索" @click="clearSearch">
                <svg width="15" height="15" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m5 5 10 10M15 5 5 15" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg>
              </button>
            </div>
            <button class="desktop-close flex h-10 shrink-0 items-center justify-center px-3 text-sm font-semibold" type="button" aria-label="取消搜索" @click="workspace.hide">
              取消
            </button>
          </header>

          <div class="search-filter-rail cinema-scrollbar flex shrink-0 items-center gap-2 overflow-x-auto px-5 pb-3 sm:px-7">
            <button class="filter-chip" :class="{ active: selectedSourceId === ALL_FILTER }" type="button" @click="selectedSourceId = ALL_FILTER">
              全部来源
            </button>
            <button v-for="source in enabledSources" :key="source.id" class="filter-chip" :class="{ active: selectedSourceId === source.id }" type="button" @click="selectedSourceId = source.id">
              {{ source.displayName ?? source.name }}
            </button>
            <span class="filter-divider" />
            <button class="filter-chip" :class="{ active: selectedType === ALL_FILTER }" type="button" @click="chooseType(ALL_FILTER)">
              全部类型
            </button>
            <button class="filter-chip" :class="{ active: selectedType === 'movie' }" type="button" @click="chooseType('movie')">
              电影
            </button>
            <button class="filter-chip" :class="{ active: selectedType === 'series' }" type="button" @click="chooseType('series')">
              剧集
            </button>
            <button class="filter-chip" :class="{ active: selectedType === 'other' }" type="button" @click="chooseType('other')">
              其他视频
            </button>
          </div>

          <div v-if="sourceLibraries.length || loadingLibraries" class="search-filter-rail cinema-scrollbar flex shrink-0 items-center gap-2 overflow-x-auto border-t px-5 py-3 sm:px-7">
            <span class="filter-caption">媒体库</span>
            <button class="filter-chip filter-chip--quiet" :class="{ active: selectedLibraryId === ALL_FILTER }" type="button" @click="chooseLibrary(ALL_FILTER)">
              全部
            </button>
            <button v-for="library in sourceLibraries" :key="libraryFilterKey(library)" class="filter-chip filter-chip--quiet" :class="{ active: selectedLibraryId === libraryFilterKey(library) }" type="button" @click="chooseLibrary(libraryFilterKey(library))">
              {{ library.name }}
            </button>
            <span v-if="loadingLibraries" class="text-xs text-white/36">同步中</span>
          </div>

          <main class="cinema-scrollbar min-h-0 flex-1 overflow-y-auto px-5 pb-8 pt-5 sm:px-7">
            <div class="mb-4 flex items-end justify-between gap-4">
              <div>
                <p class="text-xs font-semibold uppercase text-white/36">
                  {{ hasActiveQuery ? 'Search Results' : 'Discover' }}
                </p>
                <h2 class="mt-1 text-xl font-bold text-white">
                  {{ hasActiveQuery ? `“${normalizedQuery}”` : '从你的馆藏开始' }}
                </h2>
              </div>
              <span class="shrink-0 text-xs text-white/42">{{ statusLabel }}</span>
            </div>

            <p v-if="error" class="search-message border border-red-400/20 bg-red-400/10 text-red-100">
              {{ error }}
            </p>

            <template v-if="!hasActiveQuery">
              <div v-if="suggestedKeywords.length" class="keyword-grid mb-6">
                <button v-for="keyword in suggestedKeywords" :key="keyword" type="button" class="keyword-card flex min-w-0 items-center gap-3 text-left" @click="searchSuggestion(keyword)">
                  <svg class="shrink-0 text-white/42" width="17" height="17" viewBox="0 0 24 24" fill="none" aria-hidden="true"><circle cx="11" cy="11" r="6.5" stroke="currentColor" stroke-width="1.7" /><path d="m16 16 4 4" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" /></svg>
                  <span class="truncate text-sm font-semibold text-white/76">{{ keyword }}</span>
                </button>
              </div>
              <div class="poster-grid">
                <article
                  v-for="item in suggestionItems"
                  :key="`${item.sourceId}:${item.id}`"
                  class="result-card group"
                  data-media-action-target
                  tabindex="0"
                  @pointerdown="beginSearchActionLongPress(item, $event)"
                  @pointermove="moveMediaActionLongPress"
                  @pointerup="endMediaActionLongPress"
                  @pointercancel="cancelMediaActionLongPress($event.pointerId)"
                  @pointerleave="cancelMediaActionLongPress($event.pointerId)"
                  @click="handleSearchItemClick(item, $event)"
                  @contextmenu="openSearchActionMenu(item, $event)"
                  @keydown="handleSearchItemKey(item, $event)"
                >
                  <div class="result-poster relative overflow-hidden">
                    <CachedImage :cache-key="artworkCacheKey(item.sourceId, item.id, 'poster')" :src="item.posterUrl ?? item.backdropUrl" :alt="item.name" class="h-full w-full object-cover" loading="lazy" decoding="async">
                      <template #fallback>
                        <div class="flex h-full items-center justify-center px-3 text-center text-sm font-bold text-white/42">
                          {{ item.name }}
                        </div>
                      </template>
                    </CachedImage>
                    <button v-if="canPlay(item)" class="result-play absolute bottom-2 right-2 flex h-9 w-9 items-center justify-center" type="button" :aria-label="`播放 ${item.name}`" @click.stop="playItem(item)">
                      <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M4 2.5 13 8l-9 5.5v-11Z" /></svg>
                    </button>
                    <span v-if="isSearchItemPlayed(item)" class="search-played-badge" aria-label="已播放">✓</span>
                  </div>
                  <h3 class="mt-2 truncate text-sm font-semibold text-white/88">
                    {{ item.name }}
                  </h3>
                  <p class="mt-1 truncate text-xs text-white/40">
                    {{ mediaMeta(item) }}
                  </p>
                </article>
              </div>
            </template>

            <template v-else>
              <section v-if="discoveryResults.length" class="mb-6">
                <div class="mb-3 flex items-center justify-between gap-3">
                  <div>
                    <p class="text-xs font-semibold uppercase text-white/36">
                      TMDB
                    </p><h3 class="mt-1 text-base font-bold text-white/88">
                      Server 在线影视
                    </h3>
                  </div><span class="text-xs text-white/38">点进海报后可搜索站点并控制入库</span>
                </div>
                <div class="poster-grid">
                  <button v-for="item in discoveryResults" :key="`${item.sourceId}:${item.work.provider}:${item.work.providerId}`" class="result-card text-left" type="button" @click="openDiscoveryItem(item)">
                    <div class="result-poster overflow-hidden">
                      <img v-if="item.work.posterUrl" :src="item.work.posterUrl" :alt="item.work.title" class="h-full w-full object-cover"><div v-else class="flex h-full items-center justify-center px-3 text-center text-sm font-bold text-white/42">
                        {{ item.work.title }}
                      </div>
                    </div>
                    <h3 class="mt-2 truncate text-sm font-semibold text-white/88">
                      {{ item.work.title }}
                    </h3>
                    <p class="mt-1 truncate text-xs text-white/40">
                      {{ item.work.year || '年份未知' }} · {{ item.work.mediaType === 'tv' ? '剧集' : '电影' }} · {{ item.sourceName }}
                    </p>
                  </button>
                </div>
              </section>
              <div v-if="filteredResults.length" class="result-grid">
                <article
                  v-for="item in filteredResults"
                  :key="`${item.sourceId}:${item.id}`"
                  class="search-result-row flex min-w-0 items-center gap-3"
                  data-media-action-target
                  tabindex="0"
                  @pointerdown="beginSearchActionLongPress(item, $event)"
                  @pointermove="moveMediaActionLongPress"
                  @pointerup="endMediaActionLongPress"
                  @pointercancel="cancelMediaActionLongPress($event.pointerId)"
                  @pointerleave="cancelMediaActionLongPress($event.pointerId)"
                  @click="handleSearchItemClick(item, $event)"
                  @contextmenu="openSearchActionMenu(item, $event)"
                  @keydown="handleSearchItemKey(item, $event)"
                >
                  <div class="h-20 w-14 shrink-0 overflow-hidden bg-white/6">
                    <CachedImage :cache-key="artworkCacheKey(item.sourceId, item.id, 'poster')" :src="item.posterUrl ?? item.backdropUrl" :alt="item.name" class="h-full w-full object-cover" loading="lazy" decoding="async">
                      <template #fallback>
                        <div class="flex h-full items-center justify-center px-2 text-center text-xs font-bold text-white/36">
                          {{ item.name.slice(0, 2) }}
                        </div>
                      </template>
                    </CachedImage>
                  </div>
                  <div class="min-w-0 flex-1">
                    <h3 class="truncate text-sm font-bold text-white/90">
                      {{ item.name }}
                    </h3>
                    <p class="mt-1 truncate text-xs text-white/42">
                      {{ mediaMeta(item) }}
                    </p>
                    <p v-if="item.overview" class="mt-2 line-clamp-2 text-xs leading-5 text-white/34">
                      {{ item.overview }}
                    </p>
                  </div>
                  <button v-if="canPlay(item)" class="result-play flex h-10 w-10 shrink-0 items-center justify-center" type="button" :aria-label="`播放 ${item.name}`" @click.stop="playItem(item)">
                    <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M4 2.5 13 8l-9 5.5v-11Z" /></svg>
                  </button>
                  <span v-if="isSearchItemPlayed(item)" class="search-played-badge search-played-badge--row" aria-label="已播放">✓</span>
                </article>
              </div>
              <button v-if="!discoveryResults.length && selectedType !== 'other'" class="server-more mt-5 flex w-full items-center justify-between gap-4 rounded-xl border border-white/10 bg-white/5 px-5 py-4 text-left transition hover:bg-white/9 disabled:cursor-not-allowed disabled:opacity-55" type="button" :disabled="loadingDiscovery || checkingServerCapabilities || !discoveryServerSources.length" @click="searchServerMore">
                <span><strong class="block text-sm text-white/88">从 Server 搜索更多并入库</strong><small class="mt-1 block text-xs text-white/42">仅在点击后搜索 TMDB 海报；进入详情后可选站点、订阅或下载入库。</small></span>
                <span class="shrink-0 text-sm text-white/58">{{ loadingDiscovery ? '搜索中…' : serverDiscoveryHint }}</span>
              </button>
              <p v-else-if="!loading && !error && !discoveryResults.length" class="search-message text-white/44">
                没有找到符合当前筛选的媒体。
              </p>
            </template>
          </main>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.search-workspace-scrim { background: var(--chrome-scrim); backdrop-filter: blur(12px); }
.search-workspace { top: 4.75rem; left: 50%; width: min(72rem, calc(100% - 3rem)); max-height: calc(100vh - 6.25rem); transform: translateX(-50%); border: 1px solid var(--chrome-border); border-radius: 8px; color: var(--color-text); background: var(--chrome-surface-translucent); box-shadow: var(--chrome-shadow), inset 0 1px color-mix(in srgb, var(--color-text) 10%, transparent); backdrop-filter: blur(42px) saturate(1.45); }
.search-workspace-header { border-bottom: 1px solid var(--color-divider); }
.search-input { height: 3rem; border: 1px solid var(--control-border); border-radius: 8px; color: var(--control-text); background: var(--control-bg); box-shadow: var(--control-shadow); }
.search-input:focus-within { border-color: var(--control-border-hover); background: var(--control-bg-hover); box-shadow: 0 0 0 3px var(--control-focus-ring), var(--control-shadow); }
.search-input input { color: var(--control-text); }
.search-input input::placeholder { color: var(--control-placeholder); }
.mobile-close { display: none; }
.desktop-close,.mobile-close,.clear-button { border-radius: 50%; color: var(--color-text-secondary); background: var(--surface-soft); transition: background 160ms ease, color 160ms ease, transform 160ms ease; }
.desktop-close:hover,.clear-button:hover { color: var(--color-text); background: var(--surface-soft-hover); transform: scale(1.04); }
.result-play { border-radius: 50%; color: #fff; background: rgba(8,11,17,.7); box-shadow: 0 8px 24px rgba(0,0,0,.28); transition: background 160ms ease, color 160ms ease, transform 160ms ease; }
.result-play:hover { color: #fff; background: rgba(8,11,17,.86); transform: scale(1.04); }
.search-played-badge { position: absolute; right: .45rem; bottom: .45rem; display: grid; width: 1.45rem; height: 1.45rem; place-items: center; border-radius: 50%; color: #fff; background: rgba(34,197,94,.9); font-size: .68rem; font-weight: 900; }
.search-played-badge--row { position: static; flex: 0 0 auto; }
.search-filter-rail { scrollbar-width: none; }
.search-filter-rail::-webkit-scrollbar { display: none; }
.search-filter-rail.border-t { border-color: var(--color-divider); }
.filter-chip { min-height: 2.15rem; flex: 0 0 auto; border: 1px solid var(--color-border); border-radius: 999px; padding: 0 .85rem; color: var(--color-text-secondary); background: var(--surface-soft); font-size: .75rem; font-weight: 650; transition: 160ms ease; }
.filter-chip:hover { color: var(--color-text); background: var(--surface-soft-hover); }
.filter-chip.active { color: #fff; border-color: color-mix(in srgb, var(--color-primary) 58%, transparent); background: color-mix(in srgb, var(--color-primary) 72%, transparent); box-shadow: inset 0 1px rgba(255,255,255,.16); }
.filter-chip--quiet.active { color: var(--color-text); border-color: var(--control-border-hover); background: var(--surface-soft-hover); }
.filter-divider { width: 1px; height: 1.4rem; flex: 0 0 auto; background: var(--color-divider); }
.filter-caption { flex: 0 0 auto; color: var(--color-text-tertiary); font-size: .7rem; font-weight: 700; }
.keyword-grid { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)); gap: .6rem; }
.keyword-card { min-height: 3.3rem; border: 1px solid var(--color-border); border-radius: 8px; padding: 0 .9rem; background: var(--surface-soft); transition: 160ms ease; }
.keyword-card:hover { border-color: var(--control-border-hover); background: var(--surface-soft-hover); transform: translateY(-1px); }
.poster-grid { display: grid; grid-template-columns: repeat(6,minmax(0,1fr)); gap: 1rem; }
.result-card { min-width: 0; cursor: pointer; }
.result-poster { aspect-ratio: 2 / 3; border: 1px solid var(--color-border); border-radius: 8px; background: var(--surface-soft); box-shadow: var(--glass-shadow); transition: 180ms ease; }
.result-card:hover .result-poster { border-color: var(--control-border-hover); transform: translateY(-3px); box-shadow: var(--glass-shadow-elevated); }
.result-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: .7rem; }
.search-result-row { min-height: 6rem; cursor: pointer; border: 1px solid var(--color-border); border-radius: 8px; padding: .55rem; background: var(--surface-soft); transition: 160ms ease; }
.search-result-row:hover { border-color: var(--control-border-hover); background: var(--surface-soft-hover); }
.search-result-row>div:first-child { border-radius: 6px; }
.search-message { border-radius: 8px; padding: 1.25rem; text-align: center; font-size: .875rem; }
.search-spinner { border-color: var(--color-border); border-top-color: var(--color-text); animation: search-spin 700ms linear infinite; }
.search-workspace-enter-active,.search-workspace-leave-active { transition: opacity 180ms ease; }
.search-workspace-enter-active .search-workspace,.search-workspace-leave-active .search-workspace { transition: opacity 180ms ease, transform 180ms ease; }
.search-workspace-enter-from,.search-workspace-leave-to { opacity: 0; }
.search-workspace-enter-from .search-workspace,.search-workspace-leave-to .search-workspace { opacity: 0; transform: translate(-50%,-10px) scale(.99); }
@keyframes search-spin { to { transform: rotate(360deg); } }

@media (max-width: 900px) { .poster-grid { grid-template-columns: repeat(4,minmax(0,1fr)); } .keyword-grid { grid-template-columns: repeat(3,minmax(0,1fr)); } }
@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .search-workspace-scrim { display: none; }
  .search-workspace { inset: 0; width: 100%; max-height: none; transform: none; border: 0; border-radius: 0; padding-top: env(safe-area-inset-top); padding-bottom: env(safe-area-inset-bottom); background: var(--chrome-surface); box-shadow: none; }
  .search-workspace-header { padding: .75rem .9rem; }
  .mobile-close { display: flex; }
  .desktop-close { display: none; }
  .search-input { height: 2.8rem; }
  .search-filter-rail { padding-right: .9rem; padding-left: .9rem; }
  .keyword-grid { grid-template-columns: repeat(2,minmax(0,1fr)); gap: .55rem; }
  .keyword-card { min-height: 3.15rem; padding: 0 .75rem; }
  .poster-grid { grid-template-columns: repeat(3,minmax(0,1fr)); gap: .7rem; }
  .result-grid { grid-template-columns: 1fr; }
  .search-result-row { min-height: 5.5rem; }
  main { padding-right: .9rem; padding-left: .9rem; }
  .search-workspace-enter-from .search-workspace,.search-workspace-leave-to .search-workspace { transform: translateY(16px); }
}
</style>
