<script setup lang="ts">
import type { DataSource, HomeSection, MediaDetail, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { RawLocalScanCache, RawLocalScanLogEntry, RawMediaCandidate, RawScrapedMediaItem } from '@/services/scraper'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import HeroCarousel from '@/components/media/HeroCarousel.vue'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { readAlistRootPath } from '@/services/datasource/alist'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { deriveRawCandidateCategoryName, loadRawSourceScanCache, RAW_MOVIE_CATEGORY_NAME, RAW_TV_CATEGORY_NAME, RAW_UNRESOLVED_CATEGORY_NAME, rawSourceIndexScheduler } from '@/services/scraper'
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
type ScannedCategoryType = 'movie' | 'tv' | 'unresolved' | 'mixed'
type ScannedMediaDomain = 'movie' | 'tv' | 'unresolved'

interface ScannedDisplayItem {
  readonly item: MediaItem
  readonly candidate: RawMediaCandidate
  readonly scraped?: RawScrapedMediaItem
  readonly categoryName: string
  readonly domain: ScannedMediaDomain
}

interface ScannedSeriesWork {
  readonly key: string
  readonly title: string
  readonly item: MediaItem
  readonly episodes: MediaItem[]
}

interface ScannedWorkItem {
  readonly item: MediaItem
  readonly domain: ScannedMediaDomain
  readonly episodes?: MediaItem[]
}

interface ScannedCategory {
  readonly id: string
  readonly name: string
  readonly type: ScannedCategoryType
  readonly entries: ScannedDisplayItem[]
  readonly works: ScannedWorkItem[]
  readonly library: MediaLibrary
  readonly previewItems: MediaItem[]
  readonly count: number
  readonly fileCount: number
  readonly movieCount: number
  readonly tvCount: number
  readonly unresolvedCount: number
  readonly seriesCount: number
  readonly subtitle: string
  readonly previewTitles: string[]
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
const isScanManagementOpen = ref(false)
const selectedScannedCategoryId = ref<string | null>(null)
let unsubscribeRawIndexStatus: (() => void) | undefined

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
const scrapedItemsByRecordId = computed(() =>
  new Map((scanCache.value?.scrapedItems ?? []).map(item => [item.recordId, item])),
)
const scannedDisplayItems = computed<ScannedDisplayItem[]>(() =>
  (scanCache.value?.candidates ?? []).map((candidate) => {
    const scraped = scrapedItemsByRecordId.value.get(candidate.record.id)
    const domain = domainForScannedEntry(candidate, scraped)
    return {
      candidate,
      scraped,
      domain,
      item: toScannedMediaItem(candidate, scraped, domain),
      categoryName: categoryNameForCandidate(candidate, scraped),
    }
  }),
)
const scannedMovies = computed(() => scannedDisplayItems.value.filter(entry => entry.domain === 'movie'))
const scannedSeriesFiles = computed(() =>
  scannedDisplayItems.value.filter(entry => entry.domain === 'tv'),
)
const scannedUnresolved = computed(() => scannedDisplayItems.value.filter(entry => entry.domain === 'unresolved'))
const scannedCategories = computed<ScannedCategory[]>(() => {
  const groups = new Map<string, ScannedDisplayItem[]>()
  for (const entry of scannedDisplayItems.value) {
    const current = groups.get(entry.categoryName) ?? []
    current.push(entry)
    groups.set(entry.categoryName, current)
  }

  return [...groups.entries()]
    .map(([name, entries]) => createScannedCategory(name, entries))
    .sort(compareScannedCategories)
})
const selectedScannedCategory = computed(() =>
  scannedCategories.value.find(category => category.id === selectedScannedCategoryId.value) ?? null,
)
const scannedCategoryLibraries = computed<MediaLibrary[]>(() => scannedCategories.value.map(category => category.library))
const selectedCategoryWorkItems = computed<MediaItem[]>(() => selectedScannedCategory.value?.works.map(work => work.item) ?? [])
const selectedCategoryQueueItems = computed(() => [
  ...playableItemsFromWorks(selectedScannedCategory.value?.works ?? []),
])
const allScannedQueueItems = computed(() => playableItemsFromWorks(scannedCategories.value.flatMap(category => category.works)))
const hasSelectedCategorySections = computed(() => selectedCategoryWorkItems.value.length > 0)
const scannedWorkById = computed(() => {
  const works = new Map<string, ScannedWorkItem>()
  for (const category of scannedCategories.value) {
    for (const work of category.works)
      works.set(work.item.id, work)
  }
  return works
})
const scannedSeriesWorkById = computed(() => {
  const series = new Map<string, ScannedSeriesWork>()
  for (const category of scannedCategories.value) {
    for (const work of category.works) {
      if (work.domain === 'tv' && work.episodes?.length)
        series.set(work.item.id, { key: work.item.id, title: work.item.name, item: work.item, episodes: work.episodes })
    }
  }
  return series
})
const mediaLibraryHeroItems = computed(() =>
  scannedCategories.value
    .flatMap(category => category.works.map(work => work.item))
    .filter(item => item.backdropUrl || item.posterUrl)
    .sort(compareHeroScannedItems)
    .slice(0, 8),
)
const sourceLandingHeroItems = computed(() =>
  mediaLibraryHeroItems.value.length > 0 ? mediaLibraryHeroItems.value : heroItems.value,
)
const scanStats = computed(() => ({
  total: scannedDisplayItems.value.length,
  movie: scannedMovies.value.length,
  tv: scannedSeriesFiles.value.length,
  unresolved: scannedUnresolved.value.length,
}))
const mediaLibrarySummary = computed(() => {
  if (!scanCache.value)
    return `后台索引会自动整理 ${alistRootPath.value}，完成后生成电影、剧集和未识别文件分类。`

  const parts = [
    `${scannedCategories.value.length} 个分类`,
    `${scanStats.value.movie} 部影片`,
    `${seriesCountForEntries(scannedSeriesFiles.value)} 部剧集`,
    scanStats.value.unresolved ? `${scanStats.value.unresolved} 个未识别` : undefined,
  ].filter((part): part is string => Boolean(part))
  return parts.join(' · ')
})
const detectionModeLabel = computed(() => {
  if (!scanCache.value)
    return '等待索引'
  return scanCache.value.detection.mode === 'standard' ? '标准目录' : '非标准目录'
})
const scanFinishedLabel = computed(() => scanCache.value ? formatDateTime(scanCache.value.finishedAt) : '')
const scanStatusLabel = computed(() => {
  if (isScanning.value)
    return '扫描中'
  if (!scanCache.value)
    return '等待自动索引'
  return scanCache.value.status === 'completed' ? '已完成' : '部分索引'
})
const scanLogEntries = computed(() => {
  const entries = isScanning.value || !scanCache.value ? scanLiveLogs.value : scanCache.value.logs
  return entries.slice(-8)
})

onMounted(async () => {
  unsubscribeRawIndexStatus = rawSourceIndexScheduler.subscribe((status) => {
    if (status.sourceId === sourceId.value && status.sourceType === 'alist') {
      isScanning.value = status.state === 'running'
      loadScanCacheForCurrentSource({ preserveLiveLogs: status.state === 'running' })
      if (status.state === 'failed')
        scanErrorMessage.value = status.errorMessage ?? '后台索引未完成，文件夹浏览和播放仍可继续使用。'
    }
  })
  store.loadConfigs()
  syncDefaultViewModeForSource()
  await ensureSource()
  loadScanCacheForCurrentSource()
  if (isFolderView.value || isAlistSource.value)
    await loadSourceRoot()
})

onBeforeUnmount(() => {
  unsubscribeRawIndexStatus?.()
  unsubscribeRawIndexStatus = undefined
})

watch(sourceId, async () => {
  selectedLibrary.value = null
  navigationStack.value = []
  items.value = []
  libraries.value = []
  scanCache.value = null
  scanLiveLogs.value = []
  selectedScannedCategoryId.value = null
  isScanManagementOpen.value = false
  syncDefaultViewModeForSource()
  await ensureSource()
  loadScanCacheForCurrentSource()
  if (isFolderView.value || isAlistSource.value)
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
  const [librariesResult, homeResult] = await Promise.allSettled([
    source.value.listLibraries ? source.value.listLibraries() : Promise.resolve([]),
    source.value.getHomeSections ? source.value.getHomeSections() : Promise.resolve([]),
  ] as const)

  if (librariesResult.status === 'fulfilled') {
    libraries.value = librariesResult.value
  }
  else {
    libraries.value = []
    errorMessage.value = toSafeErrorMessage(librariesResult.reason, '媒体库加载失败。')
  }

  const homeSections = homeResult.status === 'fulfilled' ? homeResult.value : []
  heroItems.value = findVisibleHomeSection(homeSections, 'hero')?.items ?? []
  latestItems.value = findVisibleHomeSection(homeSections, 'recentlyAdded')?.items ?? []
  continueItems.value = findVisibleHomeSection(homeSections, 'continueWatching')?.items ?? []
  isLoading.value = false
}

async function switchViewMode(mode: SourceViewMode) {
  if (!isAlistSource.value || viewMode.value === mode)
    return

  viewMode.value = mode
  errorMessage.value = null
  if (mode === 'media-library') {
    backToLibraries()
    loadScanCacheForCurrentSource()
    if (libraries.value.length === 0)
      await loadSourceRoot()
    return
  }

  selectedScannedCategoryId.value = null
  isScanManagementOpen.value = false
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
    const scannedWork = scannedWorkById.value.get(item.id)
    if (scannedWork) {
      await openScannedWorkDetail(scannedWork)
      return
    }

    const scannedSeries = scannedSeriesWorkById.value.get(item.id)
    if (scannedSeries) {
      await openScannedSeriesDetail(scannedSeries)
      return
    }

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

  if (isMediaLibraryView.value) {
    const category = scannedCategories.value.find(category => category.id === item.id)
    if (category) {
      selectScannedCategory(category)
      return
    }

    await switchViewMode('folders')
  }

  await loadLibrary(item)
}

async function openDetail(item: MediaItem) {
  const queue = createPlaybackQueue(currentQueueItems(), item.id)
  const contextualDetail = scannedWorkById.value.has(item.id) && item.type !== 'series'
    ? createScannedMediaDetail(item)
    : undefined
  const contextId = queue || contextualDetail
    ? savePlaybackMediaContext({
        sourceId: sourceId.value,
        itemId: item.id,
        title: item.name,
        queue,
        detail: contextualDetail,
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
  const scannedSeries = scannedSeriesWorkById.value.get(item.id)
  if (scannedSeries) {
    const firstEpisode = scannedSeries.episodes[0]
    if (firstEpisode)
      await handlePlay(firstEpisode)
    return
  }

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
  selectedScannedCategoryId.value = null
  try {
    scanCache.value = await rawSourceIndexScheduler.forceScan({
      source: source.value,
      sourceId: sourceId.value,
      sourceType: 'alist',
      rootPath: alistRootPath.value,
    }, {
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

function loadScanCacheForCurrentSource(options: { preserveLiveLogs?: boolean } = {}) {
  scanErrorMessage.value = null
  if (!options.preserveLiveLogs)
    scanLiveLogs.value = []
  if (!isAlistSource.value) {
    scanCache.value = null
    selectedScannedCategoryId.value = null
    return
  }

  scanCache.value = loadRawSourceScanCache(sourceId.value, 'alist', alistRootPath.value)
  if (!scanCache.value)
    selectedScannedCategoryId.value = null
}

function syncDefaultViewModeForSource() {
  viewMode.value = isAlistSource.value ? 'media-library' : 'folders'
}

function currentQueueItems(): MediaItem[] {
  if (!isMediaLibraryView.value)
    return items.value
  return selectedScannedCategory.value ? selectedCategoryQueueItems.value : allScannedQueueItems.value
}

function selectScannedCategory(category: ScannedCategory) {
  selectedScannedCategoryId.value = category.id
}

function backToScannedCategories() {
  selectedScannedCategoryId.value = null
}

async function openScannedSeriesDetail(series: ScannedSeriesWork) {
  const firstEpisode = series.episodes[0]
  const queue = firstEpisode ? createPlaybackQueue(series.episodes, firstEpisode.id) : undefined
  const contextId = savePlaybackMediaContext({
    sourceId: sourceId.value,
    itemId: series.item.id,
    title: series.item.name,
    queue,
    detail: createScannedSeriesDetail(series),
    relatedItems: series.episodes,
  })

  await router.push({
    name: 'media-detail',
    params: {
      sourceId: sourceId.value,
      itemId: series.item.id,
    },
    query: { contextId },
  })
}

async function openScannedWorkDetail(work: ScannedWorkItem) {
  if (work.domain === 'tv' && work.episodes?.length) {
    await openScannedSeriesDetail({
      key: work.item.id,
      title: work.item.name,
      item: work.item,
      episodes: work.episodes,
    })
    return
  }

  const queue = createPlaybackQueue(currentQueueItems(), work.item.id)
  const contextId = savePlaybackMediaContext({
    sourceId: sourceId.value,
    itemId: work.item.id,
    title: work.item.name,
    queue,
    detail: createScannedMediaDetail(work.item),
  })

  await router.push({
    name: 'media-detail',
    params: {
      sourceId: sourceId.value,
      itemId: work.item.id,
    },
    query: { contextId },
  })
}

function createScannedSeriesDetail(series: ScannedSeriesWork): MediaDetail {
  return {
    ...series.item,
    type: 'series',
    children: series.episodes,
    mediaSources: [],
  }
}

function createScannedMediaDetail(item: MediaItem): MediaDetail {
  return {
    ...item,
    mediaSources: [],
  }
}

function createScannedCategory(name: string, entries: ScannedDisplayItem[]): ScannedCategory {
  const movieCount = entries.filter(entry => entry.domain === 'movie').length
  const tvEntries = entries.filter(entry => entry.domain === 'tv')
  const unresolvedCount = entries.filter(entry => entry.domain === 'unresolved').length
  const seriesCount = seriesCountForEntries(tvEntries)
  const type = scannedCategoryType(movieCount, tvEntries.length, unresolvedCount)
  const works = createScannedWorkItems(entries)
  const previewItems = works.map(work => work.item).slice(0, 4)
  const previewArtwork = previewItems.find(item => item.backdropUrl || item.posterUrl)
  const library: MediaLibrary = {
    id: `category:${encodeURIComponent(name)}`,
    sourceId: sourceId.value,
    name,
    type: mediaLibraryTypeForCategory(type),
    posterUrl: previewArtwork?.posterUrl,
    backdropUrl: previewArtwork?.backdropUrl ?? previewArtwork?.posterUrl,
    itemCount: works.length,
  }

  return {
    id: library.id,
    name,
    type,
    entries,
    works,
    library,
    previewItems,
    count: works.length,
    fileCount: entries.length,
    movieCount,
    tvCount: tvEntries.length,
    unresolvedCount,
    seriesCount,
    subtitle: scannedCategorySubtitle({ movieCount, tvCount: tvEntries.length, unresolvedCount, seriesCount }),
    previewTitles: uniqueDisplayTitles(entries).slice(0, 3),
  }
}

function createScannedWorkItems(entries: readonly ScannedDisplayItem[]): ScannedWorkItem[] {
  return [
    ...createDedupedFileWorks(entries.filter(entry => entry.domain === 'movie'), 'movie'),
    ...createSeriesWorks(entries.filter(entry => entry.domain === 'tv')).map(work => ({
      item: work.item,
      domain: 'tv' as const,
      episodes: work.episodes,
    })),
    ...entries.filter(entry => entry.domain === 'unresolved').map(entry => ({
      item: entry.item,
      domain: 'unresolved' as const,
    })),
  ]
}

function createDedupedFileWorks(entries: readonly ScannedDisplayItem[], domain: Exclude<ScannedMediaDomain, 'tv'>): ScannedWorkItem[] {
  const groups = new Map<string, ScannedDisplayItem>()
  for (const entry of entries) {
    const metadata = metadataForCandidate(entry.candidate, entry.scraped)
    const key = metadata
      ? `tmdb:${metadata.mediaType}:${metadata.tmdbId}`
      : `${entry.candidate.normalizedTitle || entry.item.id}:${entry.candidate.year ?? ''}`
    if (!groups.has(key))
      groups.set(key, entry)
  }

  return [...groups.values()]
    .map(entry => ({ item: entry.item, domain }))
    .sort((a, b) => compareScannedMediaItems(a.item, b.item))
}

function createSeriesWorks(entries: readonly ScannedDisplayItem[]): ScannedSeriesWork[] {
  const groups = new Map<string, { title: string, entries: ScannedDisplayItem[], episodes: MediaItem[] }>()
  for (const entry of entries) {
    const metadata = metadataForCandidate(entry.candidate, entry.scraped)
    const key = metadata
      ? `tmdb:${metadata.mediaType}:${metadata.tmdbId}`
      : entry.candidate.normalizedTitle || entry.candidate.record.providerPath
    const title = metadata?.title ?? entry.candidate.seriesTitle ?? entry.candidate.title
    const current = groups.get(key) ?? { title, entries: [], episodes: [] }
    current.entries.push(entry)
    current.episodes.push(entry.item)
    groups.set(key, current)
  }

  return [...groups.entries()]
    .map(([key, group]) => createSeriesWork(key, group))
    .sort((a, b) => a.title.localeCompare(b.title, 'zh-Hans-CN'))
}

function createSeriesWork(key: string, group: { title: string, entries: ScannedDisplayItem[], episodes: MediaItem[] }): ScannedSeriesWork {
  const episodes = group.episodes.sort(compareScannedMediaItems)
  const representative = group.entries.find(entry => metadataForCandidate(entry.candidate, entry.scraped)?.posterUrl || metadataForCandidate(entry.candidate, entry.scraped)?.backdropUrl) ?? group.entries[0]
  const metadata = representative ? metadataForCandidate(representative.candidate, representative.scraped) : undefined
  const firstEpisode = episodes[0]
  const title = metadata?.title ?? group.title ?? '剧集'
  const item: MediaItem = {
    id: `raw-series:${encodeURIComponent(key)}`,
    sourceId: sourceId.value,
    libraryId: representative?.candidate.record.rootPath ?? alistRootPath.value,
    name: title,
    type: 'series',
    posterUrl: metadata?.posterUrl ?? firstEpisode?.posterUrl,
    backdropUrl: metadata?.backdropUrl ?? firstEpisode?.backdropUrl,
    year: metadata?.releaseYear ?? firstEpisode?.year,
    rating: metadata?.rating ?? firstEpisode?.rating,
    overview: metadata?.overview || `${episodes.length} 个本地识别分集。`,
    path: firstEpisode?.path ?? representative?.candidate.record.providerPath ?? '',
    children: episodes,
  }

  return {
    key,
    title,
    item,
    episodes,
  }
}

function scannedCategoryType(movieCount: number, tvCount: number, unresolvedCount: number): ScannedCategoryType {
  const activeTypes = [
    movieCount > 0 ? 'movie' : null,
    tvCount > 0 ? 'tv' : null,
    unresolvedCount > 0 ? 'unresolved' : null,
  ].filter(Boolean)

  if (activeTypes.length !== 1)
    return 'mixed'
  return activeTypes[0] as ScannedCategoryType
}

function mediaLibraryTypeForCategory(type: ScannedCategoryType): MediaLibrary['type'] {
  switch (type) {
    case 'movie':
      return 'movies'
    case 'tv':
      return 'series'
    case 'mixed':
      return 'mixed'
    case 'unresolved':
    default:
      return 'folders'
  }
}

function playableItemsFromWorks(works: readonly ScannedWorkItem[]): MediaItem[] {
  return works.flatMap((work) => {
    if (work.domain === 'tv')
      return work.episodes ?? []
    return work.item.type === 'series' ? [] : [work.item]
  })
}

function scannedCategorySubtitle(counts: {
  movieCount: number
  tvCount: number
  unresolvedCount: number
  seriesCount: number
}): string {
  const parts = [
    counts.movieCount ? `${counts.movieCount} 部影片` : undefined,
    counts.tvCount ? `${counts.seriesCount || counts.tvCount} 部剧集` : undefined,
    counts.unresolvedCount ? `${counts.unresolvedCount} 个未识别` : undefined,
  ].filter((part): part is string => Boolean(part))

  return parts.join(' · ') || '暂无项目'
}

function seriesCountForEntries(entries: readonly ScannedDisplayItem[]): number {
  return new Set(entries.map((entry) => {
    const metadata = metadataForCandidate(entry.candidate, entry.scraped)
    return metadata
      ? `tmdb:${metadata.mediaType}:${metadata.tmdbId}`
      : entry.candidate.normalizedTitle
        || entry.candidate.seriesTitle
        || entry.candidate.title
        || entry.candidate.record.providerPath
  })).size
}

function uniqueDisplayTitles(entries: readonly ScannedDisplayItem[]): string[] {
  const titles: string[] = []
  const seen = new Set<string>()
  for (const entry of entries) {
    const title = metadataForCandidate(entry.candidate, entry.scraped)?.title ?? entry.candidate.seriesTitle ?? entry.candidate.title ?? entry.item.name
    const normalized = title.trim().toLocaleLowerCase()
    if (!normalized || seen.has(normalized))
      continue
    seen.add(normalized)
    titles.push(title)
  }
  return titles
}

function compareScannedCategories(a: ScannedCategory, b: ScannedCategory): number {
  return scannedCategorySortPriority(a) - scannedCategorySortPriority(b)
    || b.count - a.count
    || a.name.localeCompare(b.name, 'zh-Hans-CN')
}

function scannedCategorySortPriority(category: ScannedCategory): number {
  if (category.name === RAW_UNRESOLVED_CATEGORY_NAME)
    return 90
  if (category.name === '未分类')
    return 80
  if (category.name === RAW_MOVIE_CATEGORY_NAME)
    return 60
  if (category.name === RAW_TV_CATEGORY_NAME)
    return 61
  return 20
}

function toScannedMediaItem(
  candidate: RawMediaCandidate,
  scraped: RawScrapedMediaItem | undefined,
  domain: ScannedMediaDomain,
): MediaItem {
  const title = candidateDisplayTitle(candidate, scraped)
  const mediaType: MediaItem['type'] = domain === 'movie'
    ? 'movie'
    : domain === 'tv'
      ? 'episode'
      : 'file'
  const metadata = metadataForCandidate(candidate, scraped)

  return {
    id: candidate.record.providerPath,
    sourceId: candidate.record.sourceId,
    libraryId: candidate.record.rootPath,
    name: title,
    type: mediaType,
    posterUrl: metadata?.posterUrl,
    backdropUrl: metadata?.backdropUrl,
    year: metadata?.releaseYear ?? candidate.year,
    rating: metadata?.rating,
    size: candidate.record.size,
    modified: candidate.record.modifiedAt,
    path: candidate.record.providerPath,
    seriesName: domain === 'tv' ? metadata?.title ?? candidate.seriesTitle : candidate.seriesTitle,
    seasonNumber: candidate.seasonNumber,
    episodeNumber: candidate.episodeNumber,
    overview: metadata?.overview || scannedItemOverview(candidate, scraped),
  }
}

function candidateDisplayTitle(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem): string {
  const metadataTitle = metadataForCandidate(candidate, scraped)?.title
  if (candidate.kind === 'episode') {
    const episode = candidate.episodeNumber == null ? '' : ` E${String(candidate.episodeNumber).padStart(2, '0')}`
    const season = candidate.seasonNumber == null ? '' : `S${String(candidate.seasonNumber).padStart(2, '0')}`
    return `${metadataTitle ?? candidate.seriesTitle ?? candidate.title} ${season}${episode}`.trim()
  }

  return (metadataTitle ?? candidate.title) || candidate.record.fileName
}

function scannedItemOverview(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem): string {
  const parts = [
    scraped?.matchStatus === 'matched' ? 'TMDB：已匹配' : `本地只读扫描：${candidate.parseStatus === 'unresolved' ? '未识别' : '已解析'}`,
    `分类：${categoryNameForCandidate(candidate, scraped)}`,
    scraped?.matchedRuleName ? `规则：${scraped.matchedRuleName}` : undefined,
    scraped?.matchedSearchTitle ? `搜索标题：${scraped.matchedSearchTitle}` : undefined,
    scraped?.matchStatus !== 'matched' && candidate.categoryHint ? `路径提示：${candidate.categoryHint}` : undefined,
    candidate.signals.length ? `信号：${candidate.signals.join(', ')}` : undefined,
  ].filter((part): part is string => Boolean(part))
  return parts.join(' · ')
}

function categoryNameForCandidate(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem): string {
  if (scraped?.matchStatus === 'matched')
    return scraped.categoryName
  if (candidate.categoryAssignment?.source === 'metadataRule')
    return candidate.categoryAssignment.categoryName
  return deriveRawCandidateCategoryName(candidate)
}

function domainForScannedEntry(
  candidate: RawMediaCandidate,
  scraped?: RawScrapedMediaItem,
): ScannedMediaDomain {
  const metadata = metadataForCandidate(candidate, scraped)
  if (metadata?.mediaType === 'movie' || scraped?.mediaType === 'movie')
    return 'movie'
  if (metadata?.mediaType === 'tv' || scraped?.mediaType === 'tv')
    return 'tv'
  if (candidate.kind === 'movie')
    return 'movie'
  if (candidate.kind === 'episode' || candidate.kind === 'tv')
    return 'tv'
  return 'unresolved'
}

function metadataForCandidate(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem) {
  return scraped?.metadata ?? candidate.scrapeMetadata
}

function compareScannedMediaItems(a: MediaItem, b: MediaItem): number {
  return (a.seasonNumber ?? 0) - (b.seasonNumber ?? 0)
    || (a.episodeNumber ?? 0) - (b.episodeNumber ?? 0)
    || a.name.localeCompare(b.name, 'zh-Hans-CN')
}

function compareHeroScannedItems(a: MediaItem, b: MediaItem): number {
  const artworkScore = (item: MediaItem) => (item.backdropUrl ? 2 : 0) + (item.posterUrl ? 1 : 0) + (item.overview ? 0.5 : 0)
  return artworkScore(b) - artworkScore(a)
    || (b.rating ?? 0) - (a.rating ?? 0)
    || (b.year ?? 0) - (a.year ?? 0)
    || a.name.localeCompare(b.name, 'zh-Hans-CN')
}

function findVisibleHomeSection(homeSections: readonly HomeSection[], type: 'hero' | 'continueWatching' | 'recentlyAdded'): HomeSection | undefined {
  return homeSections.find(section => section.type === type && section.items.length > 0)
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

        <section v-if="isMediaLibraryView && !selectedScannedCategory && sourceLandingHeroItems.length" class="-mx-6 -mt-16 overflow-hidden rounded-b-[2.4rem] md:-ml-20">
          <HeroCarousel :items="sourceLandingHeroItems" @play="handlePlay" @detail="handleSelect" />
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

          <div v-if="isMediaLibraryView" class="flex flex-wrap items-center gap-3">
            <button
              class="rounded-2xl border border-white/10 bg-white/6 px-4 py-2.5 text-sm font-semibold text-white/72 transition-colors hover:bg-white/12 hover:text-white"
              @click="isScanManagementOpen = !isScanManagementOpen"
            >
              {{ isScanManagementOpen ? '收起扫描管理' : '扫描管理' }}
            </button>
            <button
              class="rounded-2xl border border-white/10 bg-white/6 px-4 py-2.5 text-sm font-semibold text-white/72 transition-colors hover:bg-white/12 hover:text-white"
              @click="switchViewMode('folders')"
            >
              文件夹
            </button>
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

        <div v-if="isAlistSource && isFolderView" class="flex flex-wrap items-center justify-between gap-3">
          <button
            class="rounded-2xl border border-white/10 bg-white/6 px-4 py-2.5 text-sm font-semibold text-white/72 transition-colors hover:bg-white/12 hover:text-white"
            @click="switchViewMode('media-library')"
          >
            返回媒体库
          </button>
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
          <div class="flex flex-wrap items-end justify-between gap-4">
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/36">
                OpenList/Alist 媒体库
              </p>
              <h2 class="mt-2 text-2xl font-bold text-white">
                {{ sourceConfig.displayName ?? sourceConfig.name }}
              </h2>
              <p class="mt-2 text-sm leading-6 text-white/45">
                {{ mediaLibrarySummary }}
              </p>
            </div>

            <div class="flex flex-wrap items-center gap-3">
              <p v-if="scanCache" class="text-sm text-white/38">
                上次扫描：{{ scanFinishedLabel }}
              </p>
            </div>
          </div>

          <div
            v-if="scanErrorMessage"
            class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100"
          >
            {{ scanErrorMessage }}
          </div>

          <section
            v-if="isScanManagementOpen"
            class="scan-management-panel rounded-2xl border border-white/10 bg-white/6 p-5"
          >
            <div class="flex flex-wrap items-start justify-between gap-4">
              <div>
                <h3 class="text-base font-semibold text-white">
                  扫描管理
                </h3>
                <p class="mt-2 max-w-2xl text-sm leading-6 text-white/46">
                  根目录 {{ alistRootPath }}。扫描只读取目录和文件名，结果保存在本机缓存，不写回 OpenList/Alist。
                </p>
              </div>
              <button
                class="rounded-2xl bg-primary px-5 py-3 text-sm font-semibold text-black transition-opacity disabled:cursor-wait disabled:opacity-60"
                :disabled="isScanning || !source"
                @click="startLocalScan"
              >
                {{ isScanning ? '索引中…' : scanCache ? '立即重扫' : '立即索引' }}
              </button>
            </div>

            <div class="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
              <div class="scan-stat">
                <p>状态</p>
                <strong>{{ scanStatusLabel }}</strong>
              </div>
              <div class="scan-stat">
                <p>结构</p>
                <strong>{{ detectionModeLabel }}</strong>
              </div>
              <div class="scan-stat">
                <p>视频</p>
                <strong>{{ scanStats.total }}</strong>
              </div>
              <div class="scan-stat">
                <p>电影 / 剧集</p>
                <strong>{{ scanStats.movie }} / {{ scanStats.tv }}</strong>
              </div>
              <div class="scan-stat">
                <p>未识别</p>
                <strong>{{ scanStats.unresolved }}</strong>
              </div>
            </div>

            <div v-if="scanCache || scanLogEntries.length" class="mt-5 grid gap-4 lg:grid-cols-2">
              <div v-if="scanCache" class="rounded-2xl border border-white/8 bg-black/14 p-4">
                <h4 class="text-sm font-semibold text-white">
                  结构判断
                </h4>
                <div class="mt-3 space-y-2 text-sm leading-6 text-white/52">
                  <p v-for="reason in scanCache.detection.reasons" :key="reason">
                    {{ reason }}
                  </p>
                </div>
              </div>
              <div class="rounded-2xl border border-white/8 bg-black/14 p-4">
                <h4 class="text-sm font-semibold text-white">
                  扫描日志
                </h4>
                <div v-if="scanLogEntries.length" class="mt-3 space-y-2 text-sm leading-6 text-white/52">
                  <p v-for="(entry, index) in scanLogEntries" :key="`${entry.timestamp}-${index}`">
                    <span
                      class="mr-2 inline-block h-2 w-2 rounded-full"
                      :class="entry.level === 'error' ? 'bg-red-300' : entry.level === 'warning' ? 'bg-yellow-300' : 'bg-primary'"
                    />
                    {{ entry.path ? `${entry.message} (${entry.path})` : entry.message }}
                  </p>
                </div>
                <p v-else class="mt-3 text-sm text-white/38">
                  暂无扫描日志。
                </p>
              </div>
            </div>
          </section>

          <template v-if="!scanCache">
            <div class="rounded-2xl border border-white/10 bg-white/6 px-5 py-4">
              <div class="flex flex-wrap items-center justify-between gap-4">
                <div>
                  <p class="text-sm font-semibold text-white">
                    本地海报墙正在等待后台整理
                  </p>
                  <p class="mt-1 text-sm leading-6 text-white/42">
                    软件会自动在后台建立本地索引；当前仍可先使用文件夹入口浏览和播放。扫描管理仅作为高级立即重扫入口。
                  </p>
                </div>
                <div class="flex flex-wrap gap-3">
                  <button
                    class="rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14 hover:text-white"
                    @click="isScanManagementOpen = true"
                  >
                    打开扫描管理
                  </button>
                  <button
                    class="rounded-2xl border border-white/10 bg-white/6 px-4 py-2 text-sm font-semibold text-white/72 transition-colors hover:bg-white/12 hover:text-white"
                    @click="switchViewMode('folders')"
                  >
                    文件夹视图
                  </button>
                </div>
              </div>
            </div>

            <section class="space-y-4">
              <div class="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h2 class="text-xl font-bold text-white">
                    文件夹入口
                  </h2>
                  <p class="mt-1 text-sm text-white/38">
                    OpenList/Alist 根目录保持可浏览可播放；本地刮削失败或未配置 TMDB 时不影响这里。
                  </p>
                </div>
              </div>

              <MediaGrid
                :items="libraries"
                :loading="isLoading"
                empty-title="当前数据源没有返回文件夹入口"
                empty-description="可以检查 OpenList/Alist 登录状态、根目录配置，或直接切换到文件夹视图重试。"
                @select="handleSelect"
                @play="handlePlay"
              />
            </section>
          </template>

          <template v-else-if="!selectedScannedCategory">
            <section class="space-y-4">
              <div class="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h2 class="text-xl font-bold text-white">
                    媒体库
                  </h2>
                  <p class="mt-1 text-sm text-white/38">
                    逻辑媒体库来自 TMDB 分类规则；路径目录只用于标题、季集和兜底识别。
                  </p>
                </div>
                <p class="text-sm text-white/38">
                  {{ scannedCategoryLibraries.length }} 个库 · {{ scanStats.total }} 个视频文件
                </p>
              </div>

              <MediaGrid
                :items="scannedCategoryLibraries"
                empty-title="当前扫描没有可显示的媒体库"
                empty-description="可以重新扫描当前根目录，或切换到文件夹视图继续浏览。"
                @select="handleSelect"
                @play="handlePlay"
              />
            </section>
          </template>

          <template v-else>
            <div class="flex flex-wrap items-center gap-4">
              <button
                class="rounded-2xl bg-white/8 px-4 py-2 text-sm text-white/70 transition-colors hover:bg-white/14"
                @click="backToScannedCategories"
              >
                返回媒体库
              </button>
              <div>
                <p class="text-xs uppercase tracking-[0.2em] text-white/32">
                  {{ selectedScannedCategory.subtitle }}
                </p>
                <h2 class="mt-1 text-2xl font-bold text-white">
                  {{ selectedScannedCategory.name }}
                </h2>
              </div>
            </div>

            <div
              v-if="!hasSelectedCategorySections"
              class="glass-panel flex min-h-56 flex-col items-center justify-center rounded-[1.75rem] p-8 text-center"
            >
              <p class="text-base font-semibold text-white">
                这个分类暂时没有可显示项目
              </p>
              <p class="mt-2 max-w-md text-sm leading-6 text-white/45">
                可以返回分类页或重新扫描当前 OpenList/Alist 根目录。
              </p>
            </div>

            <section v-else class="space-y-4">
              <div>
                <h3 class="text-lg font-bold text-white">
                  作品
                </h3>
                <p class="mt-1 text-sm text-white/36">
                  电影与剧集都按作品聚合；未识别文件保留为可播放文件卡。
                </p>
              </div>
              <MediaGrid :items="selectedCategoryWorkItems" @select="handleSelect" @play="handlePlay" />
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

.scan-management-panel {
  background:
    linear-gradient(135deg, color-mix(in srgb, var(--color-primary) 8%, transparent), transparent 46%),
    color-mix(in srgb, var(--color-surface) 62%, transparent);
}

.scan-stat {
  border: 1px solid color-mix(in srgb, var(--color-border) 76%, transparent);
  border-radius: 1rem;
  background: rgb(0 0 0 / 16%);
  padding: 0.8rem 1rem;
}

.scan-stat p {
  color: rgb(255 255 255 / 34%);
  font-size: 0.75rem;
}

.scan-stat strong {
  display: block;
  margin-top: 0.25rem;
  color: white;
  font-size: 1rem;
  font-weight: 700;
}
</style>
