<script setup lang="ts">
import type { DataSource, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { RawLocalScanCache, RawLocalScanLogEntry, RawMediaCandidate, RawScrapedMediaItem } from '@/services/scraper'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import HeroCarousel from '@/components/media/HeroCarousel.vue'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { readAlistRootPath } from '@/services/datasource/alist'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { deriveRawCandidateCategoryName, loadRawSourceScanCache, RAW_MOVIE_CATEGORY_NAME, RAW_TV_CATEGORY_NAME, RAW_UNRESOLVED_CATEGORY_NAME, runRawSourceLocalScan } from '@/services/scraper'
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

interface ScannedSeriesGroup {
  readonly key: string
  readonly title: string
  readonly items: MediaItem[]
}

interface ScannedCategory {
  readonly id: string
  readonly name: string
  readonly type: ScannedCategoryType
  readonly entries: ScannedDisplayItem[]
  readonly previewItems: MediaItem[]
  readonly count: number
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
const selectedCategoryEntries = computed(() => selectedScannedCategory.value?.entries ?? [])
const selectedCategoryMovies = computed(() =>
  selectedCategoryEntries.value.filter(entry => entry.domain === 'movie').map(entry => entry.item),
)
const selectedCategoryUnresolved = computed(() =>
  selectedCategoryEntries.value.filter(entry => entry.domain === 'unresolved').map(entry => entry.item),
)
const selectedCategorySeriesGroups = computed<ScannedSeriesGroup[]>(() => createSeriesGroups(
  selectedCategoryEntries.value.filter(entry => entry.domain === 'tv'),
))
const selectedCategoryQueueItems = computed(() => [
  ...selectedCategoryMovies.value,
  ...selectedCategorySeriesGroups.value.flatMap(group => group.items),
  ...selectedCategoryUnresolved.value,
])
const allScannedQueueItems = computed(() => [
  ...scannedMovies.value.map(entry => entry.item),
  ...createSeriesGroups(scannedSeriesFiles.value).flatMap(group => group.items),
  ...scannedUnresolved.value.map(entry => entry.item),
])
const hasSelectedCategorySections = computed(() =>
  selectedCategoryMovies.value.length > 0
  || selectedCategorySeriesGroups.value.length > 0
  || selectedCategoryUnresolved.value.length > 0,
)
const scanStats = computed(() => ({
  total: scannedDisplayItems.value.length,
  movie: scannedMovies.value.length,
  tv: scannedSeriesFiles.value.length,
  unresolved: scannedUnresolved.value.length,
}))
const mediaLibrarySummary = computed(() => {
  if (!scanCache.value)
    return `扫描 ${alistRootPath.value} 后生成电影、剧集和未识别文件分类。`

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
  selectedScannedCategoryId.value = null
  isScanManagementOpen.value = false
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
  selectedScannedCategoryId.value = null
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

function createScannedCategory(name: string, entries: ScannedDisplayItem[]): ScannedCategory {
  const movieCount = entries.filter(entry => entry.domain === 'movie').length
  const tvEntries = entries.filter(entry => entry.domain === 'tv')
  const unresolvedCount = entries.filter(entry => entry.domain === 'unresolved').length
  const seriesCount = seriesCountForEntries(tvEntries)
  const type = scannedCategoryType(movieCount, tvEntries.length, unresolvedCount)

  return {
    id: `category:${encodeURIComponent(name)}`,
    name,
    type,
    entries,
    previewItems: uniquePreviewItems(entries).slice(0, 4),
    count: entries.length,
    movieCount,
    tvCount: tvEntries.length,
    unresolvedCount,
    seriesCount,
    subtitle: scannedCategorySubtitle({ movieCount, tvCount: tvEntries.length, unresolvedCount, seriesCount }),
    previewTitles: uniqueDisplayTitles(entries).slice(0, 3),
  }
}

function createSeriesGroups(entries: ScannedDisplayItem[]): ScannedSeriesGroup[] {
  const groups = new Map<string, { title: string, items: MediaItem[] }>()
  for (const entry of entries) {
    const metadata = metadataForCandidate(entry.candidate, entry.scraped)
    const key = metadata
      ? `tmdb:${metadata.mediaType}:${metadata.tmdbId}`
      : entry.candidate.normalizedTitle || entry.candidate.record.providerPath
    const title = metadata?.title ?? entry.candidate.seriesTitle ?? entry.candidate.title
    const current = groups.get(key) ?? { title, items: [] }
    current.items.push(entry.item)
    groups.set(key, current)
  }

  return [...groups.entries()]
    .map(([key, group]) => ({
      key,
      title: group.title || '剧集',
      items: group.items.sort(compareScannedMediaItems),
    }))
    .sort((a, b) => a.title.localeCompare(b.title, 'zh-Hans-CN'))
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

function uniquePreviewItems(entries: readonly ScannedDisplayItem[]): MediaItem[] {
  const items: MediaItem[] = []
  const seen = new Set<string>()
  for (const entry of entries) {
    const metadata = metadataForCandidate(entry.candidate, entry.scraped)
    const key = metadata
      ? `tmdb:${metadata.mediaType}:${metadata.tmdbId}`
      : entry.candidate.normalizedTitle || entry.item.id
    if (!key || seen.has(key))
      continue
    seen.add(key)
    items.push(entry.item)
  }
  return items
}

function previewInitial(item: MediaItem): string {
  return item.name.trim().slice(0, 1).toLocaleUpperCase() || '#'
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

function categoryToneClass(category: ScannedCategory): string {
  switch (category.type) {
    case 'movie':
      return 'category-card--movie'
    case 'tv':
      return 'category-card--tv'
    case 'unresolved':
      return 'category-card--unresolved'
    default:
      return 'category-card--mixed'
  }
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
              <button
                class="rounded-2xl border border-white/10 bg-white/6 px-4 py-2.5 text-sm font-semibold text-white/72 transition-colors hover:bg-white/12 hover:text-white"
                @click="isScanManagementOpen = !isScanManagementOpen"
              >
                {{ isScanManagementOpen ? '收起扫描管理' : '扫描管理' }}
              </button>
            </div>
          </div>

          <div
            v-if="scanErrorMessage"
            class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100"
          >
            {{ scanErrorMessage }}
          </div>

          <div
            v-if="isScanning && !isScanManagementOpen"
            class="rounded-2xl border border-primary/25 bg-primary/10 px-5 py-4 text-sm text-primary"
          >
            正在扫描 {{ alistRootPath }}，完成后会自动刷新本地媒体库。
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
                {{ isScanning ? '扫描中…' : scanCache ? '重新扫描' : '开始扫描' }}
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

          <div v-if="!scanCache" class="empty-library-state flex min-h-72 flex-col justify-center rounded-[1.75rem] border border-white/10 p-8">
            <p class="text-sm font-semibold text-primary">
              尚未生成本地媒体库
            </p>
            <h2 class="mt-3 max-w-3xl text-2xl font-bold text-white">
              扫描后会先展示分类入口，再进入电影、剧集和未识别文件海报墙
            </h2>
            <p class="mt-3 max-w-2xl text-sm leading-6 text-white/48">
              当前根目录：{{ alistRootPath }}。播放仍通过现有 DataSource.getStreamURL() 获取地址，文件夹视图可随时作为兜底浏览入口。
            </p>
            <div class="mt-6 flex flex-wrap gap-3">
              <button
                class="rounded-2xl bg-primary px-5 py-3 text-sm font-semibold text-black transition-opacity disabled:cursor-wait disabled:opacity-60"
                :disabled="isScanning || !source"
                @click="startLocalScan"
              >
                {{ isScanning ? '扫描中…' : '开始扫描' }}
              </button>
              <button
                class="rounded-2xl border border-white/10 bg-white/6 px-5 py-3 text-sm font-semibold text-white/72 transition-colors hover:bg-white/12 hover:text-white"
                @click="switchViewMode('folders')"
              >
                打开文件夹视图
              </button>
            </div>
          </div>

          <template v-else-if="!selectedScannedCategory">
            <section class="space-y-4">
              <div class="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h2 class="text-xl font-bold text-white">
                    媒体分类
                  </h2>
                  <p class="mt-1 text-sm text-white/38">
                    优先使用 TMDB 元数据和刮削分类规则；路径目录只作为解析提示和兜底信息。
                  </p>
                </div>
                <p class="text-sm text-white/38">
                  {{ scanStats.total }} 个视频文件
                </p>
              </div>

              <div v-if="scannedCategories.length" class="category-grid">
                <button
                  v-for="category in scannedCategories"
                  :key="category.id"
                  type="button"
                  class="category-card group text-left"
                  :class="categoryToneClass(category)"
                  @click="selectScannedCategory(category)"
                >
                  <div class="category-card-visual">
                    <div class="category-card-posters" aria-hidden="true">
                      <div
                        v-for="(item, index) in category.previewItems"
                        :key="`${category.id}-${item.id}`"
                        class="category-card-poster"
                        :style="{ '--poster-index': index }"
                      >
                        <img
                          v-if="item.posterUrl || item.backdropUrl"
                          :src="item.posterUrl || item.backdropUrl"
                          :alt="item.name"
                          loading="lazy"
                          decoding="async"
                        >
                        <span v-else>{{ previewInitial(item) }}</span>
                      </div>
                    </div>
                    <span class="category-card-count">{{ category.count }}</span>
                    <span class="category-card-kind">{{ category.type === 'movie' ? 'Movie' : category.type === 'tv' ? 'Series' : category.type === 'unresolved' ? 'Unknown' : 'Mixed' }}</span>
                  </div>
                  <div class="p-4">
                    <p class="line-clamp-1 text-base font-semibold text-white">
                      {{ category.name }}
                    </p>
                    <p class="mt-1 text-sm text-white/48">
                      {{ category.subtitle }}
                    </p>
                    <p v-if="category.previewTitles.length" class="mt-3 line-clamp-2 text-xs leading-5 text-white/34">
                      {{ category.previewTitles.join(' / ') }}
                    </p>
                  </div>
                </button>
              </div>

              <div v-else class="glass-panel flex min-h-56 flex-col items-center justify-center rounded-[1.75rem] p-8 text-center">
                <p class="text-base font-semibold text-white">
                  当前扫描没有可显示的视频
                </p>
                <p class="mt-2 max-w-md text-sm leading-6 text-white/45">
                  可以重新扫描当前根目录，或切换到文件夹视图继续浏览。
                </p>
              </div>
            </section>
          </template>

          <template v-else>
            <div class="flex flex-wrap items-center gap-4">
              <button
                class="rounded-2xl bg-white/8 px-4 py-2 text-sm text-white/70 transition-colors hover:bg-white/14"
                @click="backToScannedCategories"
              >
                返回分类
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

            <section v-if="selectedCategoryMovies.length">
              <div class="mb-4">
                <h3 class="text-lg font-bold text-white">
                  电影
                </h3>
              </div>
              <MediaGrid :items="selectedCategoryMovies" @select="handleSelect" @play="handlePlay" />
            </section>

            <section v-if="selectedCategorySeriesGroups.length" class="space-y-5">
              <div>
                <h3 class="text-lg font-bold text-white">
                  剧集
                </h3>
                <p class="mt-1 text-sm text-white/36">
                  按解析出的剧名聚合，点击分集仍使用 OpenList/Alist 播放流程。
                </p>
              </div>
              <div v-for="group in selectedCategorySeriesGroups" :key="group.key" class="space-y-3">
                <div class="flex flex-wrap items-end justify-between gap-3">
                  <div>
                    <h4 class="text-base font-semibold text-white">
                      {{ group.title }}
                    </h4>
                    <p class="mt-1 text-sm text-white/36">
                      {{ group.items.length }} 个文件
                    </p>
                  </div>
                </div>
                <MediaGrid :items="group.items" @select="handleSelect" @play="handlePlay" />
              </div>
            </section>

            <section v-if="selectedCategoryUnresolved.length">
              <div class="mb-4">
                <h3 class="text-lg font-bold text-white">
                  未识别
                </h3>
                <p class="mt-1 text-sm text-white/36">
                  暂未解析出标题或季集信息的文件，仍可直接播放。
                </p>
              </div>
              <MediaGrid :items="selectedCategoryUnresolved" @select="handleSelect" @play="handlePlay" />
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

.scan-management-panel,
.empty-library-state {
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

.category-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(15.5rem, 1fr));
  gap: 1rem;
}

.category-card {
  overflow: hidden;
  border: 1px solid var(--color-border);
  border-radius: 1.4rem;
  background: color-mix(in srgb, var(--color-surface) 54%, transparent);
  box-shadow: var(--shadow-sm);
  transition: transform var(--duration-normal), border-color var(--duration-normal), background var(--duration-normal);
}

.category-card:hover {
  transform: translateY(-0.2rem);
  border-color: rgb(255 255 255 / 24%);
  background: color-mix(in srgb, var(--color-surface-hover) 60%, transparent);
}

.category-card-visual {
  position: relative;
  aspect-ratio: 16 / 9;
  overflow: hidden;
}

.category-card-visual::before {
  position: absolute;
  inset: 0;
  content: '';
  background:
    linear-gradient(135deg, rgb(255 255 255 / 20%), transparent 42%),
    radial-gradient(circle at 78% 24%, rgb(255 255 255 / 20%), transparent 24%),
    linear-gradient(0deg, rgb(0 0 0 / 44%), transparent 64%);
}

.category-card--movie .category-card-visual {
  background: linear-gradient(135deg, rgb(34 197 94 / 38%), rgb(14 165 233 / 22%)), var(--color-surface);
}

.category-card--tv .category-card-visual {
  background: linear-gradient(135deg, rgb(245 158 11 / 34%), rgb(236 72 153 / 22%)), var(--color-surface);
}

.category-card--unresolved .category-card-visual {
  background: linear-gradient(135deg, rgb(148 163 184 / 26%), rgb(255 255 255 / 10%)), var(--color-surface);
}

.category-card--mixed .category-card-visual {
  background: linear-gradient(135deg, rgb(20 184 166 / 30%), rgb(168 85 247 / 24%)), var(--color-surface);
}

.category-card-posters {
  position: absolute;
  inset: 1.2rem auto 1rem 1rem;
  display: flex;
  width: min(68%, 12rem);
  align-items: center;
}

.category-card-poster {
  position: absolute;
  left: 0;
  display: flex;
  aspect-ratio: 2 / 3;
  height: min(7.8rem, 82%);
  transform: translateX(calc(var(--poster-index) * 2.1rem)) rotate(calc((var(--poster-index) - 1.5) * 2deg));
  transform-origin: bottom center;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border: 1px solid rgb(255 255 255 / 20%);
  border-radius: 0.7rem;
  background:
    linear-gradient(160deg, rgb(255 255 255 / 18%), transparent 36%),
    rgb(0 0 0 / 24%);
  box-shadow: 0 1rem 1.8rem rgb(0 0 0 / 30%);
}

.category-card-poster img {
  height: 100%;
  width: 100%;
  object-fit: cover;
}

.category-card-poster span {
  color: rgb(255 255 255 / 72%);
  font-size: 1.5rem;
  font-weight: 800;
}

.category-card-count {
  position: absolute;
  right: 1rem;
  bottom: 0.75rem;
  color: white;
  font-size: 2rem;
  font-weight: 800;
  line-height: 1;
}

.category-card-kind {
  position: absolute;
  left: 1rem;
  top: 0.85rem;
  color: rgb(255 255 255 / 62%);
  font-size: 0.72rem;
  font-weight: 700;
  text-transform: uppercase;
}
</style>
