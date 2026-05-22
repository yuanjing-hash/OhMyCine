<script setup lang="ts">
import type { DataSource, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { RawLocalScanCache, RawLocalScanLogEntry, RawMediaCandidate } from '@/services/scraper'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import HeroCarousel from '@/components/media/HeroCarousel.vue'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { readAlistRootPath } from '@/services/datasource/alist'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { loadRawSourceScanCache, runRawSourceLocalScan, SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME } from '@/services/scraper'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()

const sourceId = computed(() => route.params.sourceId as string)
const sourceConfig = computed(() =>
  store.configs.find(c => c.id === sourceId.value),
)
const isSourceDisabled = computed(() => sourceConfig.value?.enabled === false)
interface BreadcrumbNode {
  readonly id: string
  readonly name: string
  readonly type: MediaItem['type'] | MediaLibrary['type']
  readonly isSearch?: boolean
}

type SourceViewMode = 'media-library' | 'folders'
type LibraryFilter = 'all' | 'movie' | 'tv' | 'unresolved' | 'uncategorized'

interface ScannedDisplayItem {
  readonly item: MediaItem
  readonly candidate: RawMediaCandidate
  readonly categoryName: string
}

interface ScannedSeriesGroup {
  readonly key: string
  readonly title: string
  readonly items: MediaItem[]
}

interface LibraryFilterChip {
  readonly id: LibraryFilter
  readonly label: string
  readonly count: number
}

const source = ref<DataSource | null>(null)
const libraries = ref<MediaLibrary[]>([])
const items = ref<MediaItem[]>([])
const heroItems = ref<MediaItem[]>([])
const latestItems = ref<MediaItem[]>([])
const continueItems = ref<MediaItem[]>([])
const selectedLibrary = ref<MediaLibrary | null>(null)
const navigationStack = ref<BreadcrumbNode[]>([])
const searchKeyword = ref('')
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)
const viewMode = ref<SourceViewMode>('folders')
const scanCache = ref<RawLocalScanCache | null>(null)
const isScanning = ref(false)
const scanErrorMessage = ref<string | null>(null)
const scanLiveLogs = ref<RawLocalScanLogEntry[]>([])
const libraryFilter = ref<LibraryFilter>('all')

const isAlistSource = computed(() => sourceConfig.value?.type === 'alist')
const alistRootPath = computed(() => isAlistSource.value ? readAlistRootPath(sourceConfig.value) : '/')
const activeViewMode = computed<SourceViewMode>(() => isAlistSource.value ? viewMode.value : 'folders')
const isMediaLibraryView = computed(() => activeViewMode.value === 'media-library')
const isFolderView = computed(() => activeViewMode.value === 'folders')
const displayItems = computed(() => selectedLibrary.value ? items.value : libraries.value)
const currentNode = computed(() => navigationStack.value.at(-1) ?? null)
const pageTitle = computed(() => {
  if (isMediaLibraryView.value)
    return sourceConfig.value?.displayName ?? sourceConfig.value?.name ?? 'OpenList/Alist'
  return currentNode.value?.name ?? selectedLibrary.value?.name ?? (sourceConfig.value?.displayName ?? sourceConfig.value?.name ?? 'Data Source')
})
const sourceTypeLabel = computed(() => sourceConfig.value ? labelForSourceType(sourceConfig.value.type) : 'Data')
const searchPlaceholder = computed(() => `搜索 ${sourceTypeLabel.value} 媒体或文件`)
const breadcrumbLabel = computed(() => `${sourceTypeLabel.value} 浏览路径`)
const rootBackLabel = computed(() => sourceConfig.value?.type === 'alist' ? '返回文件目录' : '返回媒体库')
const sectionTitle = computed(() => {
  if (selectedLibrary.value)
    return sourceConfig.value?.type === 'alist' ? '目录项目' : '媒体项目'
  return sourceConfig.value?.type === 'alist' ? '文件目录' : '媒体库'
})
const sectionDescription = computed(() => {
  if (selectedLibrary.value)
    return '选择视频条目即可进入现有播放加载流程。'
  if (sourceConfig.value?.type === 'alist')
    return '进入 OpenList/Alist 文件目录后，可继续浏览子目录或播放视频文件。'
  return '选择一个媒体库开始浏览。'
})
const emptyTitle = computed(() => selectedLibrary.value ? '此目录暂无可显示项目' : `未找到 ${sourceTypeLabel.value} 入口`)
const emptyDescription = computed(() => {
  if (selectedLibrary.value)
    return `请检查 ${sourceTypeLabel.value} 权限、目录内容或搜索条件。`
  return '请确认设置中的 URL、登录会话和数据源启用状态有效。'
})
const scannedDisplayItems = computed<ScannedDisplayItem[]>(() =>
  (scanCache.value?.candidates ?? []).map(candidate => ({
    candidate,
    item: toScannedMediaItem(candidate),
    categoryName: categoryNameForCandidate(candidate),
  })),
)
const scannedMovies = computed(() => scannedDisplayItems.value.filter(entry => entry.candidate.kind === 'movie'))
const scannedSeriesFiles = computed(() =>
  scannedDisplayItems.value.filter(entry => entry.candidate.kind === 'episode' || entry.candidate.kind === 'tv'),
)
const scannedUnresolved = computed(() => scannedDisplayItems.value.filter(entry => entry.candidate.kind === 'unresolved'))
const libraryFilterChips = computed<LibraryFilterChip[]>(() => [
  { id: 'all', label: '全部', count: scannedDisplayItems.value.length },
  { id: 'movie', label: '电影', count: scannedMovies.value.length },
  { id: 'tv', label: '剧集', count: scannedSeriesFiles.value.length },
  { id: 'unresolved', label: '未识别', count: scannedUnresolved.value.length },
  {
    id: 'uncategorized',
    label: SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME,
    count: scannedMovies.value.length + scannedSeriesFiles.value.length,
  },
])
const visibleScannedMovies = computed(() => scannedMovies.value.filter(matchesLibraryFilter).map(entry => entry.item))
const visibleScannedUnresolved = computed(() => scannedUnresolved.value.filter(matchesLibraryFilter).map(entry => entry.item))
const visibleScannedSeriesGroups = computed<ScannedSeriesGroup[]>(() => {
  const groups = new Map<string, { title: string, items: MediaItem[] }>()
  for (const entry of scannedSeriesFiles.value.filter(matchesLibraryFilter)) {
    const key = entry.candidate.normalizedTitle || entry.candidate.record.providerPath
    const title = entry.candidate.seriesTitle ?? entry.candidate.title
    const current = groups.get(key) ?? { title, items: [] }
    current.items.push(entry.item)
    groups.set(key, current)
  }

  return [...groups.entries()]
    .map(([key, group]) => ({
      key,
      title: group.title,
      items: group.items.sort(compareScannedMediaItems),
    }))
    .sort((a, b) => a.title.localeCompare(b.title, 'zh-Hans-CN'))
})
const visibleScannedQueueItems = computed(() => [
  ...visibleScannedMovies.value,
  ...visibleScannedSeriesGroups.value.flatMap(group => group.items),
  ...visibleScannedUnresolved.value,
])
const hasVisibleScannedSections = computed(() =>
  visibleScannedMovies.value.length > 0
  || visibleScannedSeriesGroups.value.length > 0
  || visibleScannedUnresolved.value.length > 0,
)
const scanStats = computed(() => ({
  total: scannedDisplayItems.value.length,
  movie: scannedMovies.value.length,
  tv: scannedSeriesFiles.value.length,
  unresolved: scannedUnresolved.value.length,
}))
const detectionModeLabel = computed(() => {
  if (!scanCache.value)
    return '未扫描'
  return scanCache.value.detection.mode === 'standard' ? '标准目录' : '非标准目录'
})
const scanFinishedLabel = computed(() => scanCache.value ? formatDateTime(scanCache.value.finishedAt) : '')
const scanStatusLabel = computed(() => {
  if (isScanning.value)
    return '扫描中'
  if (!scanCache.value)
    return '未扫描'
  return scanCache.value.status === 'completed' ? '已完成' : '部分失败'
})
const scanLogEntries = computed(() => {
  const entries = isScanning.value || !scanCache.value ? scanLiveLogs.value : scanCache.value.logs
  return entries.slice(-8)
})

onMounted(async () => {
  store.loadConfigs()
  syncDefaultViewModeForSource()
  await ensureSource()
  loadScanCacheForCurrentSource()
  if (isFolderView.value)
    await loadSourceRoot()
})

watch(sourceId, async () => {
  selectedLibrary.value = null
  navigationStack.value = []
  items.value = []
  libraries.value = []
  scanCache.value = null
  scanLiveLogs.value = []
  libraryFilter.value = 'all'
  syncDefaultViewModeForSource()
  await ensureSource()
  loadScanCacheForCurrentSource()
  if (isFolderView.value)
    await loadSourceRoot()
})

async function ensureSource() {
  source.value = null
  if (!sourceConfig.value)
    return

  if (isSourceDisabled.value) {
    errorMessage.value = '该数据源已停用。请到设置的数据源管理中启用后再浏览。'
    return
  }

  await store.syncManager()
  source.value = store.getSource(sourceId.value)
  if (!source.value) {
    errorMessage.value = store.lastError || '数据源不可用，请检查登录凭证或到设置中重新登录。'
    return
  }

  errorMessage.value = null
}

async function loadSourceRoot() {
  if (!source.value)
    return

  isLoading.value = true
  errorMessage.value = null
  try {
    const [nextLibraries, homeSections] = await Promise.all([
      source.value.listLibraries ? source.value.listLibraries() : Promise.resolve([]),
      source.value.getHomeSections ? source.value.getHomeSections() : Promise.resolve([]),
    ])
    libraries.value = nextLibraries
    heroItems.value = homeSections.find(section => section.type === 'hero')?.items ?? []
    latestItems.value = homeSections.find(section => section.type === 'recentlyAdded')?.items ?? []
    continueItems.value = homeSections.find(section => section.type === 'continueWatching')?.items ?? []
  }
  catch (error) {
    libraries.value = []
    heroItems.value = []
    latestItems.value = []
    continueItems.value = []
    errorMessage.value = toSafeErrorMessage(error, '媒体库加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function switchViewMode(mode: SourceViewMode) {
  if (!isAlistSource.value || viewMode.value === mode)
    return

  viewMode.value = mode
  errorMessage.value = null
  if (mode === 'media-library') {
    backToLibraries()
    loadScanCacheForCurrentSource()
    return
  }

  await loadSourceRoot()
}

async function loadLibrary(library: MediaLibrary) {
  if (!source.value)
    return

  selectedLibrary.value = library
  navigationStack.value = [{ id: library.id, name: library.name, type: library.type }]
  searchKeyword.value = ''
  isLoading.value = true
  errorMessage.value = null
  try {
    items.value = await source.value.list(library.id)
  }
  catch (error) {
    items.value = []
    errorMessage.value = toSafeErrorMessage(error, '媒体条目加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function runSearch() {
  if (!source.value)
    return

  const keyword = searchKeyword.value.trim()
  if (!keyword) {
    if (currentNode.value?.isSearch) {
      backToLibraries()
    }
    else if (selectedLibrary.value) {
      await loadLibrary(selectedLibrary.value)
    }
    return
  }

  isLoading.value = true
  errorMessage.value = null
  try {
    items.value = await source.value.search(keyword)
    selectedLibrary.value = {
      id: 'search',
      sourceId: sourceId.value,
      name: `搜索：${keyword}`,
      type: 'mixed',
    }
    navigationStack.value = [{ id: 'search', name: `搜索：${keyword}`, type: 'mixed', isSearch: true }]
  }
  catch (error) {
    items.value = []
    errorMessage.value = toSafeErrorMessage(error, '搜索失败。')
  }
  finally {
    isLoading.value = false
  }
}

function backToLibraries() {
  selectedLibrary.value = null
  navigationStack.value = []
  items.value = []
  searchKeyword.value = ''
}

async function navigateToCrumb(index: number) {
  const crumb = navigationStack.value[index]
  if (!crumb || crumb.isSearch)
    return

  searchKeyword.value = ''
  navigationStack.value = navigationStack.value.slice(0, index + 1)
  await loadNestedItems(crumb.id)
}

async function handleSelect(item: MediaItem | MediaLibrary) {
  if ('path' in item) {
    if ((item.type === 'folder' && item.duration == null && !item.overview) || item.type === 'season') {
      if (!selectedLibrary.value) {
        selectedLibrary.value = {
          id: item.id,
          sourceId: item.sourceId,
          name: item.name,
          type: 'folders',
        }
      }
      navigationStack.value = [
        ...navigationStack.value,
        { id: item.id, name: item.name, type: item.type },
      ]
      await loadNestedItems(item.id)
      return
    }

    await openDetail(item)
    return
  }

  await loadLibrary(item)
}

async function openDetail(item: MediaItem) {
  const queue = createPlaybackQueue(currentQueueItems(), item.id)
  const contextId = queue
    ? savePlaybackMediaContext({
        sourceId: sourceId.value,
        itemId: item.id,
        title: item.name,
        queue,
      })
    : undefined

  await router.push({
    name: 'media-detail',
    params: {
      sourceId: sourceId.value,
      itemId: item.id,
    },
    query: contextId ? { contextId } : undefined,
  })
}

async function loadNestedItems(parentId: string) {
  if (!source.value)
    return

  isLoading.value = true
  errorMessage.value = null
  try {
    items.value = await source.value.list(parentId)
  }
  catch (error) {
    items.value = []
    errorMessage.value = toSafeErrorMessage(error, '子项目加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function handlePlay(item: MediaItem) {
  if (isContainerItem(item)) {
    await openDetail(item)
    return
  }

  if (!source.value)
    return

  isLoading.value = true
  errorMessage.value = null
  try {
    const streamUrl = await source.value.getStreamURL(item.id)
    const queue = createPlaybackQueue(currentQueueItems(), item.id)
    const playbackContextId = savePlaybackMediaContext({
      sourceId: sourceId.value,
      itemId: item.id,
      title: item.name,
      queue,
    })
    await router.push({
      name: 'player',
      query: {
        title: item.name,
        path: streamUrl,
        sourceId: sourceId.value,
        itemId: item.id,
        libraryId: item.libraryId,
        mediaType: item.type,
        posterUrl: item.posterUrl,
        backdropUrl: item.backdropUrl,
        contextId: playbackContextId,
      },
    })
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '无法获取播放地址。')
  }
  finally {
    isLoading.value = false
  }
}

async function startLocalScan() {
  if (!source.value || !isAlistSource.value)
    return

  isScanning.value = true
  scanErrorMessage.value = null
  scanLiveLogs.value = []
  libraryFilter.value = 'all'
  try {
    scanCache.value = await runRawSourceLocalScan({
      source: source.value,
      sourceId: sourceId.value,
      sourceType: 'alist',
      rootPath: alistRootPath.value,
      onLog: entry => scanLiveLogs.value = [...scanLiveLogs.value.slice(-7), entry],
    })
  }
  catch (error) {
    scanErrorMessage.value = toSafeErrorMessage(error, '扫描失败。文件夹浏览和播放仍可继续使用。')
  }
  finally {
    isScanning.value = false
  }
}

function loadScanCacheForCurrentSource() {
  scanErrorMessage.value = null
  scanLiveLogs.value = []
  if (!isAlistSource.value) {
    scanCache.value = null
    return
  }

  scanCache.value = loadRawSourceScanCache(sourceId.value, 'alist', alistRootPath.value)
}

function syncDefaultViewModeForSource() {
  viewMode.value = isAlistSource.value ? 'media-library' : 'folders'
}

function currentQueueItems(): MediaItem[] {
  return isMediaLibraryView.value ? visibleScannedQueueItems.value : items.value
}

function matchesLibraryFilter(entry: ScannedDisplayItem): boolean {
  switch (libraryFilter.value) {
    case 'movie':
      return entry.candidate.kind === 'movie'
    case 'tv':
      return entry.candidate.kind === 'episode' || entry.candidate.kind === 'tv'
    case 'unresolved':
      return entry.candidate.kind === 'unresolved'
    case 'uncategorized':
      return entry.categoryName === SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME && entry.candidate.kind !== 'unresolved'
    case 'all':
    default:
      return true
  }
}

function toScannedMediaItem(candidate: RawMediaCandidate): MediaItem {
  const title = candidateDisplayTitle(candidate)
  const mediaType: MediaItem['type'] = candidate.kind === 'movie'
    ? 'movie'
    : candidate.kind === 'episode'
      ? 'episode'
      : 'file'

  return {
    id: candidate.record.providerPath,
    sourceId: candidate.record.sourceId,
    libraryId: candidate.record.rootPath,
    name: title,
    type: mediaType,
    year: candidate.year,
    size: candidate.record.size,
    modified: candidate.record.modifiedAt,
    path: candidate.record.providerPath,
    seriesName: candidate.seriesTitle,
    seasonNumber: candidate.seasonNumber,
    episodeNumber: candidate.episodeNumber,
    overview: scannedItemOverview(candidate),
  }
}

function candidateDisplayTitle(candidate: RawMediaCandidate): string {
  if (candidate.kind === 'episode') {
    const episode = candidate.episodeNumber == null ? '' : ` E${String(candidate.episodeNumber).padStart(2, '0')}`
    const season = candidate.seasonNumber == null ? '' : `S${String(candidate.seasonNumber).padStart(2, '0')}`
    return `${candidate.seriesTitle ?? candidate.title} ${season}${episode}`.trim()
  }

  return candidate.title || candidate.record.fileName
}

function scannedItemOverview(candidate: RawMediaCandidate): string {
  const parts = [
    `本地只读扫描：${candidate.parseStatus === 'unresolved' ? '未识别' : '已解析'}`,
    `分类：${categoryNameForCandidate(candidate)}`,
    candidate.categoryHint ? `路径提示：${candidate.categoryHint}` : undefined,
    candidate.signals.length ? `信号：${candidate.signals.join(', ')}` : undefined,
  ].filter((part): part is string => Boolean(part))
  return parts.join(' · ')
}

function categoryNameForCandidate(candidate: RawMediaCandidate): string {
  if (candidate.kind === 'unresolved')
    return '未识别'
  return SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME
}

function compareScannedMediaItems(a: MediaItem, b: MediaItem): number {
  return (a.seasonNumber ?? 0) - (b.seasonNumber ?? 0)
    || (a.episodeNumber ?? 0) - (b.episodeNumber ?? 0)
    || a.name.localeCompare(b.name, 'zh-Hans-CN')
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime()))
    return value
  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function isContainerItem(item: MediaItem): boolean {
  return item.type === 'folder' || item.type === 'series' || item.type === 'season'
}

function labelForSourceType(type: string): string {
  switch (type) {
    case 'emby':
      return 'Emby'
    case 'alist':
      return 'OpenList/Alist'
    case 'jellyfin':
      return 'Jellyfin'
    case 'clouddrive2':
      return 'CloudDrive2'
    case 'local':
      return '本地文件'
    case 'server':
      return 'OhMyCine Server'
    default:
      return type
  }
}
</script>

<template>
  <div class="source-view relative min-h-full">
    <div class="space-y-8 p-6 pl-20 pt-16">
      <div v-if="!sourceConfig" class="flex flex-col items-center justify-center py-24">
        <p class="text-lg text-white/40">
          Data source not found
        </p>
        <button
          class="mt-4 rounded-full bg-white/10 px-6 py-2 text-sm text-white/70 transition-colors hover:bg-white/20"
          @click="router.push('/')"
        >
          Back to Home
        </button>
      </div>

      <template v-else>
        <section v-if="isFolderView && !selectedLibrary && heroItems.length" class="-mx-6 -mt-16 overflow-hidden rounded-b-[2.4rem] md:-ml-20">
          <HeroCarousel :items="heroItems" @play="handlePlay" @detail="openDetail" />
        </section>

        <div class="flex flex-wrap items-center justify-between gap-4">
          <div class="flex items-center gap-4">
            <div class="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/18 text-lg font-bold text-primary">
              <img v-if="sourceConfig.iconUrl" :src="sourceConfig.iconUrl" class="h-8 w-8 rounded" :alt="sourceConfig.name">
              <span v-else>{{ sourceConfig.type[0].toUpperCase() }}</span>
            </div>
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/34">
                {{ sourceTypeLabel }} source
              </p>
              <h1 class="mt-1 text-2xl font-bold text-white">
                {{ pageTitle }}
              </h1>
            </div>
          </div>

          <form v-if="isFolderView" class="flex min-w-72 gap-2" @submit.prevent="runSearch">
            <input
              v-model="searchKeyword"
              class="min-w-0 flex-1 rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none placeholder:text-white/25 focus:border-primary/60"
              :placeholder="searchPlaceholder"
            >
            <button class="rounded-2xl bg-white/10 px-4 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/16">
              搜索
            </button>
          </form>
        </div>

        <div v-if="isAlistSource" class="flex flex-wrap items-center justify-between gap-3">
          <div class="inline-flex rounded-2xl border border-white/10 bg-white/6 p-1">
            <button
              class="rounded-xl px-4 py-2 text-sm font-semibold transition-colors"
              :class="isMediaLibraryView ? 'bg-white text-black' : 'text-white/58 hover:bg-white/10 hover:text-white'"
              @click="switchViewMode('media-library')"
            >
              媒体库
            </button>
            <button
              class="rounded-xl px-4 py-2 text-sm font-semibold transition-colors"
              :class="isFolderView ? 'bg-white text-black' : 'text-white/58 hover:bg-white/10 hover:text-white'"
              @click="switchViewMode('folders')"
            >
              文件夹
            </button>
          </div>
          <p class="text-sm text-white/40">
            当前根目录：{{ alistRootPath }}
          </p>
        </div>

        <div v-if="isFolderView && selectedLibrary" class="flex flex-wrap items-center gap-3">
          <button
            class="rounded-2xl bg-white/8 px-4 py-2 text-sm text-white/70 transition-colors hover:bg-white/14"
            @click="backToLibraries"
          >
            {{ rootBackLabel }}
          </button>
          <nav class="flex flex-wrap items-center gap-2 text-sm text-white/36" :aria-label="breadcrumbLabel">
            <template v-for="(crumb, index) in navigationStack" :key="`${crumb.id}-${index}`">
              <span v-if="index > 0" class="text-white/20">/</span>
              <button
                class="rounded-full px-2 py-1 transition-colors hover:bg-white/8 hover:text-white/70"
                :class="index === navigationStack.length - 1 ? 'text-white/70' : ''"
                @click="navigateToCrumb(index)"
              >
                {{ crumb.name }}
              </button>
            </template>
          </nav>
        </div>

        <div
          v-if="errorMessage"
          class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100"
        >
          {{ errorMessage }}
        </div>

        <section v-if="isMediaLibraryView" class="space-y-6">
          <div class="glass-panel overflow-hidden rounded-[1.75rem] border border-white/10">
            <div class="library-hero px-6 py-6 md:px-8">
              <div class="flex flex-wrap items-start justify-between gap-5">
                <div>
                  <p class="text-xs uppercase tracking-[0.24em] text-white/36">
                    OpenList/Alist local library
                  </p>
                  <h2 class="mt-3 text-3xl font-bold text-white">
                    {{ sourceConfig.displayName ?? sourceConfig.name }}
                  </h2>
                  <p class="mt-3 max-w-2xl text-sm leading-6 text-white/55">
                    扫描范围 {{ alistRootPath }}。扫描只读取目录与文件名，结果保存在本机缓存，不会上传、重命名、移动或删除 OpenList/Alist 文件。
                  </p>
                </div>
                <button
                  class="rounded-2xl bg-primary px-5 py-3 text-sm font-semibold text-black transition-opacity disabled:cursor-wait disabled:opacity-60"
                  :disabled="isScanning || !source"
                  @click="startLocalScan"
                >
                  {{ scanCache ? '重新扫描' : '开始扫描' }}
                </button>
              </div>

              <div class="mt-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
                <div class="rounded-2xl bg-black/22 px-4 py-3">
                  <p class="text-xs text-white/34">
                    状态
                  </p>
                  <p class="mt-1 text-lg font-semibold text-white">
                    {{ scanStatusLabel }}
                  </p>
                </div>
                <div class="rounded-2xl bg-black/22 px-4 py-3">
                  <p class="text-xs text-white/34">
                    结构
                  </p>
                  <p class="mt-1 text-lg font-semibold text-white">
                    {{ detectionModeLabel }}
                  </p>
                </div>
                <div class="rounded-2xl bg-black/22 px-4 py-3">
                  <p class="text-xs text-white/34">
                    视频
                  </p>
                  <p class="mt-1 text-lg font-semibold text-white">
                    {{ scanStats.total }}
                  </p>
                </div>
                <div class="rounded-2xl bg-black/22 px-4 py-3">
                  <p class="text-xs text-white/34">
                    电影 / 剧集
                  </p>
                  <p class="mt-1 text-lg font-semibold text-white">
                    {{ scanStats.movie }} / {{ scanStats.tv }}
                  </p>
                </div>
                <div class="rounded-2xl bg-black/22 px-4 py-3">
                  <p class="text-xs text-white/34">
                    未识别
                  </p>
                  <p class="mt-1 text-lg font-semibold text-white">
                    {{ scanStats.unresolved }}
                  </p>
                </div>
              </div>
            </div>
          </div>

          <div
            v-if="scanErrorMessage"
            class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100"
          >
            {{ scanErrorMessage }}
          </div>

          <div v-if="!scanCache && scanLogEntries.length" class="rounded-2xl border border-white/10 bg-white/6 p-5">
            <h3 class="text-base font-semibold text-white">
              扫描日志
            </h3>
            <div class="mt-3 space-y-2 text-sm leading-6 text-white/54">
              <p v-for="(entry, index) in scanLogEntries" :key="`${entry.timestamp}-${index}`">
                <span
                  class="mr-2 inline-block h-2 w-2 rounded-full"
                  :class="entry.level === 'error' ? 'bg-red-300' : entry.level === 'warning' ? 'bg-yellow-300' : 'bg-primary'"
                />
                {{ entry.path ? `${entry.message} (${entry.path})` : entry.message }}
              </p>
            </div>
          </div>

          <div v-if="!scanCache" class="glass-panel flex min-h-72 flex-col justify-center rounded-[1.75rem] p-8">
            <p class="text-sm font-semibold text-primary">
              尚未生成本地媒体库
            </p>
            <h2 class="mt-3 text-2xl font-bold text-white">
              扫描 {{ alistRootPath }} 后显示电影、剧集和未识别文件
            </h2>
            <p class="mt-3 max-w-2xl text-sm leading-6 text-white/48">
              这一步只通过当前 DataSource 读取目录列表，并把 provider path、文件名、解析候选和结构判断保存在本机。播放时仍通过 OpenList/Alist 的 getStreamURL 流程获取地址。
            </p>
            <div class="mt-6">
              <button
                class="rounded-2xl bg-primary px-5 py-3 text-sm font-semibold text-black transition-opacity disabled:cursor-wait disabled:opacity-60"
                :disabled="isScanning || !source"
                @click="startLocalScan"
              >
                {{ isScanning ? '扫描中…' : '开始扫描' }}
              </button>
            </div>
          </div>

          <template v-else>
            <div class="flex flex-wrap items-center justify-between gap-4">
              <div class="flex flex-wrap gap-2">
                <button
                  v-for="chip in libraryFilterChips"
                  :key="chip.id"
                  class="rounded-full border px-4 py-2 text-sm font-semibold transition-colors"
                  :class="libraryFilter === chip.id ? 'border-primary/70 bg-primary/18 text-primary' : 'border-white/10 bg-white/6 text-white/58 hover:bg-white/10 hover:text-white'"
                  @click="libraryFilter = chip.id"
                >
                  {{ chip.label }} · {{ chip.count }}
                </button>
              </div>
              <p class="text-sm text-white/40">
                上次扫描：{{ scanFinishedLabel }}
              </p>
            </div>

            <div class="grid gap-4 lg:grid-cols-2">
              <div class="rounded-2xl border border-white/10 bg-white/6 p-5">
                <h3 class="text-base font-semibold text-white">
                  结构判断
                </h3>
                <div class="mt-3 space-y-2 text-sm leading-6 text-white/54">
                  <p v-for="reason in scanCache.detection.reasons" :key="reason">
                    {{ reason }}
                  </p>
                </div>
              </div>
              <div class="rounded-2xl border border-white/10 bg-white/6 p-5">
                <h3 class="text-base font-semibold text-white">
                  扫描日志
                </h3>
                <div class="mt-3 space-y-2 text-sm leading-6 text-white/54">
                  <p v-for="(entry, index) in scanLogEntries" :key="`${entry.timestamp}-${index}`">
                    <span
                      class="mr-2 inline-block h-2 w-2 rounded-full"
                      :class="entry.level === 'error' ? 'bg-red-300' : entry.level === 'warning' ? 'bg-yellow-300' : 'bg-primary'"
                    />
                    {{ entry.path ? `${entry.message} (${entry.path})` : entry.message }}
                  </p>
                </div>
              </div>
            </div>

            <div
              v-if="!hasVisibleScannedSections"
              class="glass-panel flex min-h-56 flex-col items-center justify-center rounded-[1.75rem] p-8 text-center"
            >
              <p class="text-base font-semibold text-white">
                当前筛选没有可显示项目
              </p>
              <p class="mt-2 max-w-md text-sm leading-6 text-white/45">
                可以切回“全部”或重新扫描当前 OpenList/Alist 根目录。
              </p>
            </div>

            <section v-if="visibleScannedMovies.length">
              <div class="mb-4">
                <h2 class="text-xl font-bold text-white">
                  电影
                </h2>
                <p class="mt-1 text-sm text-white/36">
                  本地解析出的电影候选，兜底分类为 {{ SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME }}。
                </p>
              </div>
              <MediaGrid :items="visibleScannedMovies" @select="handleSelect" @play="handlePlay" />
            </section>

            <section v-if="visibleScannedSeriesGroups.length" class="space-y-5">
              <div>
                <h2 class="text-xl font-bold text-white">
                  剧集
                </h2>
                <p class="mt-1 text-sm text-white/36">
                  按解析出的剧名聚合，点击分集仍使用 OpenList/Alist 播放流程。
                </p>
              </div>
              <div v-for="group in visibleScannedSeriesGroups" :key="group.key" class="space-y-3">
                <div class="flex flex-wrap items-end justify-between gap-3">
                  <div>
                    <h3 class="text-base font-semibold text-white">
                      {{ group.title }}
                    </h3>
                    <p class="mt-1 text-sm text-white/36">
                      {{ group.items.length }} 个文件 · {{ SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME }}
                    </p>
                  </div>
                </div>
                <MediaGrid :items="group.items" @select="handleSelect" @play="handlePlay" />
              </div>
            </section>

            <section v-if="visibleScannedUnresolved.length">
              <div class="mb-4">
                <h2 class="text-xl font-bold text-white">
                  未识别
                </h2>
                <p class="mt-1 text-sm text-white/36">
                  暂未解析出标题或季集信息的文件，仍可直接播放。
                </p>
              </div>
              <MediaGrid :items="visibleScannedUnresolved" @select="handleSelect" @play="handlePlay" />
            </section>
          </template>
        </section>

        <section v-if="isFolderView && !selectedLibrary && continueItems.length">
          <div class="mb-4 flex items-end justify-between">
            <div>
              <h2 class="text-xl font-bold text-white">
                继续观看
              </h2>
              <p class="mt-1 text-sm text-white/36">
                从 {{ sourceTypeLabel }} 恢复列表继续播放。
              </p>
            </div>
          </div>
          <MediaGrid :items="continueItems" @select="handleSelect" @play="handlePlay" />
        </section>

        <section v-if="isFolderView && !selectedLibrary && latestItems.length">
          <div class="mb-4 flex items-end justify-between">
            <div>
              <h2 class="text-xl font-bold text-white">
                最新影片与剧集
              </h2>
              <p class="mt-1 text-sm text-white/36">
                最近加入 {{ sourceTypeLabel }} 的电影与剧集。
              </p>
            </div>
          </div>
          <MediaGrid :items="latestItems" @select="handleSelect" @play="handlePlay" />
        </section>

        <section v-if="isFolderView">
          <div class="mb-4 flex items-end justify-between">
            <div>
              <h2 class="text-xl font-bold text-white">
                {{ sectionTitle }}
              </h2>
              <p class="mt-1 text-sm text-white/36">
                {{ sectionDescription }}
              </p>
            </div>
          </div>

          <MediaGrid
            :items="displayItems"
            :loading="isLoading"
            :empty-title="emptyTitle"
            :empty-description="emptyDescription"
            @select="handleSelect"
            @play="handlePlay"
          />
        </section>
      </template>
    </div>
  </div>
</template>

<style scoped>
.source-view {
  background: var(--color-bg);
}

.library-hero {
  background:
    linear-gradient(135deg, color-mix(in srgb, var(--color-primary) 18%, transparent), transparent 48%),
    color-mix(in srgb, var(--color-surface) 72%, transparent);
}
</style>
