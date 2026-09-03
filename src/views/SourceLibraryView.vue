<script setup lang="ts">
import type { DataSource, HomeSection, MediaDetail, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { RawFileSourceType, RawLocalScanCache, RawLocalScanLogEntry, RawMediaCandidate, RawScrapedMediaItem, RawSourceIndexStatus, RawSourceIndexTarget, RawSourceScanKind, ScrapeMediaType, TmdbImageCandidate, TmdbImageKind, TmdbMetadata } from '@/services/scraper'
import type { ServerLibraryRefreshDetail } from '@/services/serverMediaChanges'
import type { ScannedCategory, ScannedDisplayItem, ScannedSeriesWork, ScannedWorkItem } from '@/services/sourceLibraryScannedMedia'
import type { PendingServerMediaUpdate } from '@/stores/datasource'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import HeroCarousel from '@/components/media/HeroCarousel.vue'
import MediaGrid from '@/components/media/MediaGrid.vue'
import ServerLibraryUpdateNotice from '@/components/media/ServerLibraryUpdateNotice.vue'
import { requestAppScrollTop } from '@/services/appScroll'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { readLocalRootPath } from '@/services/datasource/local'
import { normalizeWorkLevelSearchResults } from '@/services/datasource/searchAggregation'
import { registerLayoutBackHandler } from '@/services/layoutBackNavigation'
import { clearLayoutContextActions, setLayoutContextActions } from '@/services/layoutContextActions'
import { registerMaintenanceHandler } from '@/services/mediaActions'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { applyRawManualArtworkOverride, applyRawManualIdentification, categoryNameForRawCandidate, createEffectiveRawScrapeItemMap, createRawSeriesGroupingKey, enrichRawScrapedItemsEpisodeMetadata, loadRawSourceScanCache, loadTmdbLocalSettings, RAW_UNRESOLVED_CATEGORY_NAME, rawSourceIndexScheduler, readConfiguredTmdbCredential, readRawSourceRootPath, saveRawSourceScanCache, TmdbScraper, toRawScannedMediaItem } from '@/services/scraper'
import { SERVER_LIBRARY_REFRESH_EVENT } from '@/services/serverMediaChanges'
import { loadSourceBrowseContext, saveSourceBrowseContext, sourceBrowseContextIdFromQuery } from '@/services/sourceBrowseContext'
import { compareHeroScannedItems, compareScannedCategories, createScannedCategory, domainForScannedEntry, findVisibleHomeSection, formatRawIndexStatus, formatRawIndexTime, isContainerItem, labelForSourceType, metadataForCandidate, playableItemsFromWorks } from '@/services/sourceLibraryScannedMedia'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const layoutContextOwner = Symbol('source-library-layout-actions')
let unregisterMaintenanceHandler: (() => void) | undefined
let unregisterLayoutBackHandler: (() => void) | undefined

const sourceId = computed(() => route.params.sourceId as string)
const sourceConfig = computed(() =>
  store.orderedConfigs.find(c => c.id === sourceId.value),
)
const isSourceDisabled = computed(() => sourceConfig.value?.enabled === false)
interface BreadcrumbNode {
  readonly id: string
  readonly name: string
  readonly type: MediaItem['type'] | MediaLibrary['type']
  readonly isSearch?: boolean
}

type SourceViewMode = 'media-library' | 'folders'
type EditableArtworkKind = Extract<TmdbImageKind, 'poster' | 'logo' | 'backdrop'>
type IdentificationTab = 'match' | 'images'

interface IdentificationArtworkCard {
  readonly kind: EditableArtworkKind | 'thumb' | 'banner' | 'disc' | 'art'
  readonly label: string
  readonly description: string
  readonly enabled: boolean
  readonly currentUrl?: string
}

const source = ref<DataSource | null>(null)
const libraries = ref<MediaLibrary[]>([])
const items = ref<MediaItem[]>([])
const heroItems = ref<MediaItem[]>([])
const latestItems = ref<MediaItem[]>([])
const continueItems = ref<MediaItem[]>([])
const rootHomeSections = ref<HomeSection[]>([])
const selectedLibrary = ref<MediaLibrary | null>(null)
const navigationStack = ref<BreadcrumbNode[]>([])
const searchKeyword = ref('')
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)
const viewMode = ref<SourceViewMode>('folders')
const scanCache = ref<RawLocalScanCache | null>(null)
const rawIndexStatus = ref<RawSourceIndexStatus | null>(null)
const incrementalRawIndexStatus = ref<RawSourceIndexStatus | null>(null)
const isScanning = ref(false)
const scanErrorMessage = ref<string | null>(null)
const scanLiveLogs = ref<RawLocalScanLogEntry[]>([])
const isScanManagementOpen = ref(false)
const selectedScannedCategoryId = ref<string | null>(null)
const identificationTarget = ref<ScannedWorkItem | null>(null)
const identificationActiveTab = ref<IdentificationTab>('match')
const identificationQuery = ref('')
const identificationMediaType = ref<ScrapeMediaType>('movie')
const identificationYear = ref('')
const identificationTmdbId = ref('')
const identificationImdbId = ref('')
const identificationTvdbId = ref('')
const identificationResults = ref<TmdbMetadata[]>([])
const isIdentificationDialogOpen = ref(false)
const isIdentificationSearching = ref(false)
const isIdentificationApplying = ref(false)
const identificationErrorMessage = ref<string | null>(null)
const identificationInfoMessage = ref<string | null>(null)
const artworkSearchKind = ref<EditableArtworkKind | null>(null)
const artworkSearchResults = ref<TmdbImageCandidate[]>([])
const isArtworkSearching = ref(false)
const isArtworkApplying = ref(false)
let unsubscribeRawIndexStatus: (() => void) | undefined
let identificationSearchRequestId = 0
let rawIndexGeneration = 0
let sourceRootLoadGeneration = 0
let serverViewRefreshGeneration = 0
let activeSourceBrowseContextId: string | null = null
let isRestoringSourceBrowseContext = false
const isApplyingServerUpdate = ref(false)
const observedServerUpdateVersion = ref(0)

const rawSourceType = computed<RawFileSourceType | null>(() => {
  const type = sourceConfig.value?.type
  return type === 'alist' || type === 'clouddrive2' || type === 'webdav' || type === '123' || type === 'quark' || type === 'local' ? type : null
})
const isRawFileSource = computed(() => rawSourceType.value != null)
const rawSourceRootPath = computed(() => sourceConfig.value && isRawFileSource.value ? readRawSourceRootPath(sourceConfig.value) : '/')
const rawSourceRootLabel = computed(() => sourceConfig.value?.type === 'local' ? readLocalRootPath(sourceConfig.value) : rawSourceRootPath.value)
const activeViewMode = computed<SourceViewMode>(() => isRawFileSource.value ? viewMode.value : 'folders')
const isMediaLibraryView = computed(() => activeViewMode.value === 'media-library')
const isFolderView = computed(() => activeViewMode.value === 'folders')
const displayItems = computed(() => selectedLibrary.value ? items.value : libraries.value)
const supplementalHomeSections = computed(() => rootHomeSections.value.filter(section => !['hero', 'continueWatching', 'recentlyAdded'].includes(section.type)))
const hasHomeLibrarySection = computed(() => supplementalHomeSections.value.some(section => section.purpose === 'libraries'))
const currentNode = computed(() => navigationStack.value.at(-1) ?? null)
const pendingServerViewUpdate = computed<PendingServerMediaUpdate | null>(() => {
  void observedServerUpdateVersion.value
  if (sourceConfig.value?.type !== 'server')
    return null
  const update = store.getPendingServerUpdate(sourceId.value)
  if (!update || currentNode.value?.isSearch)
    return null
  if (update.resyncRequired || !selectedLibrary.value)
    return update
  const libraryId = /^\d+$/.test(selectedLibrary.value.id) ? selectedLibrary.value.id : null
  return libraryId && update.libraryIds.includes(libraryId) ? update : null
})
const sourceTypeLabel = computed(() => sourceConfig.value ? labelForSourceType(sourceConfig.value.type) : 'Data')
const pageTitle = computed(() => {
  if (isMediaLibraryView.value)
    return sourceConfig.value?.displayName ?? sourceConfig.value?.name ?? sourceTypeLabel.value
  return currentNode.value?.name ?? selectedLibrary.value?.name ?? (sourceConfig.value?.displayName ?? sourceConfig.value?.name ?? 'Data Source')
})
const searchPlaceholder = computed(() => `搜索 ${sourceTypeLabel.value} 媒体或文件`)
const breadcrumbLabel = computed(() => `${sourceTypeLabel.value} 浏览路径`)
const rootBackLabel = computed(() => isRawFileSource.value ? '返回文件目录' : '返回媒体库')
const sectionTitle = computed(() => {
  if (selectedLibrary.value)
    return isRawFileSource.value ? '目录项目' : '媒体项目'
  return isRawFileSource.value ? '文件目录' : '媒体库'
})
const sectionDescription = computed(() => {
  if (selectedLibrary.value)
    return '选择视频条目即可进入现有播放加载流程。'
  if (isRawFileSource.value)
    return `进入 ${sourceTypeLabel.value} 文件目录后，可继续浏览子目录或播放视频文件。`
  return '选择一个媒体库开始浏览。'
})
const emptyTitle = computed(() => selectedLibrary.value ? '此目录暂无可显示项目' : `未找到 ${sourceTypeLabel.value} 入口`)
const emptyDescription = computed(() => {
  if (selectedLibrary.value)
    return `请检查 ${sourceTypeLabel.value} 权限、目录内容或搜索条件。`
  return '请确认设置中的 URL、登录会话和数据源启用状态有效。'
})
const scrapedItemsByRecordId = computed(() =>
  createEffectiveRawScrapeItemMap(scanCache.value?.candidates ?? [], scanCache.value?.scrapedItems),
)
const scannedDisplayItems = computed<ScannedDisplayItem[]>(() =>
  (scanCache.value?.candidates ?? []).map((candidate) => {
    const scraped = scrapedItemsByRecordId.value.get(candidate.record.id)
    const domain = domainForScannedEntry(candidate, scraped)
    return {
      candidate,
      scraped,
      domain,
      item: toRawScannedMediaItem(candidate, scraped, domain),
      categoryName: categoryNameForCandidate(candidate, scraped),
    }
  }),
)
const scannedMovies = computed(() => scannedDisplayItems.value.filter(entry => entry.domain === 'movie'))
const scannedSeriesFiles = computed(() =>
  scannedDisplayItems.value.filter(entry => entry.domain === 'tv'),
)
const scannedCategories = computed<ScannedCategory[]>(() => {
  const groups = new Map<string, ScannedDisplayItem[]>()
  for (const entry of scannedDisplayItems.value) {
    const current = groups.get(entry.categoryName) ?? []
    current.push(entry)
    groups.set(entry.categoryName, current)
  }

  return [...groups.entries()]
    .map(([name, entries]) => createScannedCategory({ sourceId: sourceId.value, rootPath: rawSourceRootPath.value, name, entries }))
    .sort(compareScannedCategories)
})
const selectedScannedCategory = computed(() =>
  scannedCategories.value.find(category => category.id === selectedScannedCategoryId.value) ?? null,
)
const scannedCategoryLibraries = computed<MediaLibrary[]>(() => scannedCategories.value.map(category => category.library))
const selectedCategoryWorkItems = computed<MediaItem[]>(() => selectedScannedCategory.value?.works.map(work => work.item) ?? [])
const selectedScannedCategoryDescription = computed(() => {
  if (selectedScannedCategory.value?.name === RAW_UNRESOLVED_CATEGORY_NAME)
    return '未识别条目保留解析出的标题、季集和播放路径；剧集候选仍按作品/季/集聚合，便于后续手动识别。'
  return '电影与剧集都按作品聚合；可直接进入详情或播放。'
})
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
        series.set(work.item.id, { key: work.item.id, title: work.item.name, item: work.item, entries: work.entries, episodes: work.episodes, seasons: work.seasons ?? [] })
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
  movie: scannedMovies.value.filter(entry => !isUnresolvedCategoryEntry(entry)).length,
  tv: scannedSeriesFiles.value.filter(entry => !isUnresolvedCategoryEntry(entry)).length,
  unresolved: scannedDisplayItems.value.filter(isUnresolvedCategoryEntry).length,
}))
const detectionModeLabel = computed(() => {
  if (!scanCache.value)
    return '等待索引'
  return scanCache.value.detection.mode === 'standard' ? '标准目录' : '非标准目录'
})
const fullScanStatusLabel = computed(() => formatRawIndexStatus(rawIndexStatus.value, scanCache.value ? '已完成' : '等待自动索引'))
const incrementalScanStatusLabel = computed(() => formatRawIndexStatus(incrementalRawIndexStatus.value, '等待增量扫描'))
const fullScanLastRunLabel = computed(() => formatRawIndexTime(rawIndexStatus.value))
const incrementalScanLastRunLabel = computed(() => formatRawIndexTime(incrementalRawIndexStatus.value))
const scanStatusLabel = computed(() => {
  if (isScanning.value || rawIndexStatus.value?.state === 'running')
    return '扫描中'
  if (rawIndexStatus.value?.state === 'queued')
    return '准备索引'
  if (rawIndexStatus.value?.state === 'failed')
    return '索引失败'
  if (rawIndexStatus.value?.state === 'completed' && !scanCache.value)
    return '读取索引结果'
  if (rawIndexStatus.value?.state === 'cooldown' && !scanCache.value)
    return '等待重试'
  if (!scanCache.value)
    return '等待自动索引'
  return scanCache.value.status === 'completed' ? '已完成' : '部分索引'
})
const scanLogEntries = computed(() => {
  const entries = isScanning.value || !scanCache.value ? scanLiveLogs.value : scanCache.value.logs
  return entries.slice(-8)
})
const isRawIndexBusy = computed(() =>
  isScanning.value || rawIndexStatus.value?.state === 'queued' || rawIndexStatus.value?.state === 'running',
)
const firstIndexStatusLabel = computed(() => {
  if (rawIndexStatus.value?.state === 'failed')
    return '索引失败'
  if (rawIndexStatus.value?.state === 'completed')
    return '读取索引结果'
  if (rawIndexStatus.value?.state === 'cooldown')
    return '等待下次自动索引'
  if (isRawIndexBusy.value)
    return rawIndexStatus.value?.state === 'queued' ? '准备索引' : '正在索引'
  return '准备索引'
})
const firstIndexTitle = computed(() => {
  if (rawIndexStatus.value?.state === 'failed')
    return '这次索引没跑完'
  if (rawIndexStatus.value?.state === 'completed')
    return '正在加载索引结果'
  if (rawIndexStatus.value?.state === 'cooldown')
    return '正在等待索引窗口'
  if (isRawIndexBusy.value)
    return '正在整理本地媒体库'
  return '即将整理本地媒体库'
})
const firstIndexDescription = computed(() => {
  if (rawIndexStatus.value?.state === 'failed')
    return rawIndexStatus.value.errorMessage ?? '自动索引失败了，但文件夹浏览和播放仍然可用，可以手动重试。'
  if (rawIndexStatus.value?.state === 'completed')
    return '索引已经完成，正在读取本机缓存并生成分类卡片。'
  if (rawIndexStatus.value?.state === 'cooldown')
    return '当前源刚刚尝试过自动索引；如果媒体库仍为空，可以手动立即索引。'
  if (isRawIndexBusy.value)
    return '第一次进入会读取目录、识别电影/剧集并写入本机缓存，完成后分类会自动出现。'
  return '正在启动当前根目录的自动索引，文件夹视图可以同时浏览和播放。'
})
const firstIndexActionLabel = computed(() => {
  if (isRawIndexBusy.value)
    return '索引中…'
  if (rawIndexStatus.value?.state === 'failed')
    return '重试索引'
  return '立即索引'
})
const identificationSourcePath = computed(() =>
  identificationTarget.value?.entries[0]?.candidate.record.providerPath
  ?? identificationTarget.value?.item.path
  ?? '',
)
const identificationCurrentMetadata = computed(() => {
  const target = identificationTarget.value?.entries[0]?.candidate
  if (!target)
    return undefined
  const scraped = scrapedItemsByRecordId.value.get(target.record.id)
  return metadataForCandidate(target, scraped)
})
const identificationArtworkCards = computed<IdentificationArtworkCard[]>(() => {
  const metadata = identificationCurrentMetadata.value
  return [
    {
      kind: 'poster',
      label: '海报',
      description: '用于海报墙和详情页竖版封面。',
      enabled: true,
      currentUrl: metadata?.posterUrl,
    },
    {
      kind: 'logo',
      label: '徽标',
      description: '标题 Logo，详情页与播放页优先显示。',
      enabled: true,
      currentUrl: metadata?.titleLogoUrl,
    },
    {
      kind: 'backdrop',
      label: '背景图',
      description: '用于详情页和播放前的横向背景。',
      enabled: true,
      currentUrl: metadata?.backdropUrl,
    },
    {
      kind: 'thumb',
      label: '缩略图',
      description: '后续支持本地缩略图覆盖。',
      enabled: false,
    },
    {
      kind: 'banner',
      label: '横幅图',
      description: '后续支持横幅图源。',
      enabled: false,
    },
    {
      kind: 'disc',
      label: '光盘封面',
      description: '后续支持光盘封面。',
      enabled: false,
    },
    {
      kind: 'art',
      label: '艺术图',
      description: '后续支持艺术图覆盖。',
      enabled: false,
    },
  ]
})

onMounted(async () => {
  window.addEventListener('keydown', handleGlobalKeydown)
  window.addEventListener(SERVER_LIBRARY_REFRESH_EVENT, handleServerLibraryRefresh)
  unregisterLayoutBackHandler = registerLayoutBackHandler(layoutContextOwner, handleInPageBack)
  unsubscribeRawIndexStatus = rawSourceIndexScheduler.subscribe((status) => {
    if (isCurrentRawIndexStatus(status)) {
      setRawIndexStatus(status)
      void loadScanCacheForCurrentSource({ preserveLiveLogs: status.state === 'running' })
      if (status.state === 'failed')
        scanErrorMessage.value = status.errorMessage ?? '后台索引未完成，文件夹浏览和播放仍可继续使用。'
      else if (status.state === 'running' || status.state === 'completed')
        scanErrorMessage.value = null
    }
  })
  store.loadConfigs()
  registerCurrentMaintenanceHandler()
  syncDefaultViewModeForSource()
  await ensureSource()
  await prepareRawSourceIndex()
  if (isFolderView.value || isRawFileSource.value)
    await loadSourceRoot()
  await restoreSourceBrowseContext()
})

onBeforeUnmount(() => {
  unregisterMaintenanceHandler?.()
  unregisterLayoutBackHandler?.()
  rawIndexGeneration += 1
  sourceRootLoadGeneration += 1
  serverViewRefreshGeneration += 1
  window.removeEventListener('keydown', handleGlobalKeydown)
  window.removeEventListener(SERVER_LIBRARY_REFRESH_EVENT, handleServerLibraryRefresh)
  unsubscribeRawIndexStatus?.()
  unsubscribeRawIndexStatus = undefined
  clearLayoutContextActions(layoutContextOwner)
})

function handleServerLibraryRefresh(event: Event) {
  const detail = (event as CustomEvent<ServerLibraryRefreshDetail>).detail
  if (sourceConfig.value?.type !== 'server' || detail?.sourceId !== sourceId.value)
    return
  // The pending update is held source/library-scoped in Pinia. This typed
  // event only nudges an already-mounted view; it never reloads the current
  // list or touches playback without the user's explicit action.
  observedServerUpdateVersion.value = Math.max(observedServerUpdateVersion.value, detail.version)
}

async function refreshCurrentServerView() {
  const snapshot = pendingServerViewUpdate.value
  const currentSource = source.value
  if (!snapshot || !currentSource || isApplyingServerUpdate.value)
    return

  const scrollRoot = document.querySelector<HTMLElement>('main.cinema-scrollbar')
  const scrollTop = scrollRoot?.scrollTop ?? 0
  const focusedCardId = document.activeElement instanceof HTMLElement
    ? document.activeElement.closest<HTMLElement>('[data-media-card-id]')?.dataset.mediaCardId
    : undefined
  const refreshGeneration = ++serverViewRefreshGeneration
  const loadingSourceId = sourceId.value
  const selectedLibraryId = selectedLibrary.value?.id
  const parentId = currentNode.value?.id ?? selectedLibraryId
  const keyword = searchKeyword.value.trim()
  let refreshed = false
  isApplyingServerUpdate.value = true
  currentSource.clearCache?.()
  store.invalidateSourceRootSnapshot(loadingSourceId)

  try {
    if (!selectedLibrary.value) {
      refreshed = await loadSourceRoot({ force: true })
    }
    else {
      isLoading.value = true
      errorMessage.value = null
      const nextItems = await currentSource.list(parentId ?? selectedLibrary.value.id)
      if (serverViewRefreshGeneration !== refreshGeneration
        || sourceId.value !== loadingSourceId
        || source.value !== currentSource
        || selectedLibrary.value?.id !== selectedLibraryId
        || searchKeyword.value.trim() !== keyword) {
        return
      }
      items.value = nextItems
      refreshed = true
    }
    if (refreshed) {
      const libraryIds = selectedLibraryId && /^\d+$/.test(selectedLibraryId)
        ? [selectedLibraryId]
        : undefined
      store.acknowledgeServerUpdate(snapshot, libraryIds)
    }
  }
  catch (error) {
    if (serverViewRefreshGeneration === refreshGeneration && sourceId.value === loadingSourceId)
      errorMessage.value = toSafeErrorMessage(error, '媒体条目刷新失败。')
  }
  finally {
    if (serverViewRefreshGeneration === refreshGeneration && sourceId.value === loadingSourceId) {
      isLoading.value = false
      isApplyingServerUpdate.value = false
    }
  }

  if (!refreshed)
    return
  await nextTick()
  window.requestAnimationFrame(() => {
    scrollRoot?.scrollTo({ top: scrollTop, left: 0, behavior: 'auto' })
    if (!focusedCardId)
      return
    const card = [...document.querySelectorAll<HTMLElement>('[data-media-card-id]')]
      .find(element => element.dataset.mediaCardId === focusedCardId)
    card?.focus({ preventScroll: true })
  })
}

watch(sourceId, async () => {
  unregisterMaintenanceHandler?.()
  rawIndexGeneration += 1
  sourceRootLoadGeneration += 1
  serverViewRefreshGeneration += 1
  activeSourceBrowseContextId = null
  isRestoringSourceBrowseContext = false
  isApplyingServerUpdate.value = false
  isLoading.value = false
  selectedLibrary.value = null
  navigationStack.value = []
  items.value = []
  libraries.value = []
  scanCache.value = null
  rawIndexStatus.value = null
  isScanning.value = false
  scanErrorMessage.value = null
  scanLiveLogs.value = []
  selectedScannedCategoryId.value = null
  closeIdentificationDialog()
  isScanManagementOpen.value = false
  syncDefaultViewModeForSource()
  registerCurrentMaintenanceHandler()
  await ensureSource()
  await prepareRawSourceIndex()
  if (isFolderView.value || isRawFileSource.value)
    await loadSourceRoot()
  await restoreSourceBrowseContext()
})

function registerCurrentMaintenanceHandler() {
  unregisterMaintenanceHandler = registerMaintenanceHandler(sourceId.value, {
    canHandle: (target, action) => action === 'rescanLibrary' || (action !== 'editSubtitles' && target.kind === 'media' && scannedWorkById.value.has(target.itemId)),
    execute: async (target, action) => {
      if (action === 'rescanLibrary') {
        await startLocalScan('full')
        return
      }
      const work = target.kind === 'media' ? scannedWorkById.value.get(target.itemId) : null
      if (!work)
        throw new Error('当前本地扫描缓存中找不到该媒体，请先重扫媒体库。')
      if (action === 'refreshMetadata') {
        openIdentificationDialog(work)
        await searchIdentificationResults()
        return
      }
      openIdentificationDialog(work)
      if (action === 'editArtwork')
        identificationActiveTab.value = 'images'
    },
  })
}

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

async function loadSourceRoot(options: { force?: boolean } = {}): Promise<boolean> {
  if (!source.value)
    return false

  const loadGeneration = ++sourceRootLoadGeneration
  const loadingSourceId = sourceId.value
  const pendingUpdate = sourceConfig.value?.type === 'server' ? store.getPendingServerUpdate(loadingSourceId) : null
  const cachedSnapshot = store.getSourceRootSnapshot(loadingSourceId)
  if (cachedSnapshot)
    applySourceRootSnapshot(cachedSnapshot)
  if (cachedSnapshot && !options.force && store.isSourceRootSnapshotFresh(loadingSourceId))
    return true

  const showLoading = cachedSnapshot == null
  if (showLoading)
    isLoading.value = true
  errorMessage.value = null
  const libraryRequest = pendingUpdate && source.value.listLibrariesForMediaChangeRefresh
    ? source.value.listLibrariesForMediaChangeRefresh()
    : source.value.listLibraries
      ? source.value.listLibraries()
      : Promise.resolve([])
  const [librariesResult, homeResult] = await Promise.allSettled([
    libraryRequest,
    source.value.getHomeSections ? source.value.getHomeSections() : Promise.resolve([]),
  ] as const)
  if (loadGeneration !== sourceRootLoadGeneration || loadingSourceId !== sourceId.value)
    return false

  if (librariesResult.status === 'rejected')
    errorMessage.value = toSafeErrorMessage(librariesResult.reason, '媒体库加载失败。')

  const nextSnapshot = {
    libraries: librariesResult.status === 'fulfilled' ? librariesResult.value : (cachedSnapshot?.libraries ?? []),
    homeSections: homeResult.status === 'fulfilled' ? homeResult.value : (cachedSnapshot?.homeSections ?? []),
  }
  applySourceRootSnapshot(nextSnapshot)
  if (librariesResult.status === 'fulfilled' || homeResult.status === 'fulfilled')
    store.setSourceRootSnapshot(loadingSourceId, nextSnapshot)
  const succeeded = librariesResult.status === 'fulfilled'
  if (librariesResult.status === 'fulfilled' && pendingUpdate)
    store.acknowledgeServerUpdate(pendingUpdate)
  if (showLoading)
    isLoading.value = false
  return succeeded
}

function applySourceRootSnapshot(snapshot: { libraries: MediaLibrary[], homeSections: HomeSection[] }) {
  libraries.value = snapshot.libraries
  rootHomeSections.value = snapshot.homeSections
  heroItems.value = findVisibleHomeSection(snapshot.homeSections, 'hero')?.items ?? []
  latestItems.value = findVisibleHomeSection(snapshot.homeSections, 'recentlyAdded')?.items ?? []
  continueItems.value = findVisibleHomeSection(snapshot.homeSections, 'continueWatching')?.items ?? []
}

async function openHomeSection(section: HomeSection) {
  const target = section.viewAllRoute
  if (!target)
    return
  if (target.kind === 'history') {
    await router.push({ name: 'history', query: { sourceId: target.sourceId, ...(target.libraryId ? { libraryId: target.libraryId } : {}) } })
    return
  }
  await handleSelect({
    id: target.path,
    sourceId: sourceId.value,
    name: section.title,
    type: 'folder',
    path: '',
  })
}

async function persistSourceBrowseContext(options: { captureScroll?: boolean } = {}) {
  if (isRestoringSourceBrowseContext || route.name !== 'source')
    return

  const contextId = saveSourceBrowseContext({
    sourceId: sourceId.value,
    viewMode: activeViewMode.value,
    selectedLibrary: selectedLibrary.value,
    navigationStack: navigationStack.value,
    selectedScannedCategoryId: selectedScannedCategoryId.value,
    searchKeyword: searchKeyword.value,
    scrollTop: options.captureScroll ? readSourceScrollTop() : 0,
  }, activeSourceBrowseContextId)
  activeSourceBrowseContextId = contextId
  const query = { ...route.query, browseContextId: contextId }
  await router.replace({
    name: 'source',
    params: { sourceId: sourceId.value },
    query,
  })
}

async function restoreSourceBrowseContext() {
  const contextId = sourceBrowseContextIdFromQuery(route.query.browseContextId)
  activeSourceBrowseContextId = contextId
  const context = loadSourceBrowseContext(contextId, sourceId.value)
  if (!context)
    return

  isRestoringSourceBrowseContext = true
  try {
    if (isRawFileSource.value)
      viewMode.value = context.viewMode

    if (activeViewMode.value === 'media-library') {
      const categoryExists = context.selectedScannedCategoryId != null
        && scannedCategories.value.some(category => category.id === context.selectedScannedCategoryId)
      selectedScannedCategoryId.value = categoryExists ? context.selectedScannedCategoryId : null
      restoreSourceScrollTop(context.scrollTop)
      return
    }

    const searchNode = context.navigationStack.find(node => node.isSearch)
    if (searchNode && context.searchKeyword.trim() && source.value) {
      items.value = normalizeWorkLevelSearchResults(await source.value.search(context.searchKeyword.trim()))
      selectedLibrary.value = context.selectedLibrary
      navigationStack.value = context.navigationStack.map(node => ({ ...node }))
      searchKeyword.value = context.searchKeyword
      restoreSourceScrollTop(context.scrollTop)
      return
    }

    if (!context.selectedLibrary) {
      restoreSourceScrollTop(context.scrollTop)
      return
    }

    const library = libraries.value.find(candidate => candidate.id === context.selectedLibrary?.id)
    if (!library)
      throw new Error('The saved library no longer exists.')
    const restoredStack = context.navigationStack
      .filter(node => !node.isSearch && node.id && node.name)
      .slice(0, 64)
    if (restoredStack.length === 0 || restoredStack[0]?.id !== library.id)
      throw new Error('The saved folder path is invalid.')

    selectedLibrary.value = library
    navigationStack.value = restoredStack.map(node => ({ ...node }))
    searchKeyword.value = context.searchKeyword
    if (source.value)
      items.value = await source.value.list(restoredStack.at(-1)?.id ?? library.id)
    restoreSourceScrollTop(context.scrollTop)
  }
  catch {
    selectedLibrary.value = null
    navigationStack.value = []
    selectedScannedCategoryId.value = null
    searchKeyword.value = ''
    items.value = []
    activeSourceBrowseContextId = null
    const query = { ...route.query }
    delete query.browseContextId
    await router.replace({
      name: 'source',
      params: { sourceId: sourceId.value },
      query,
    })
  }
  finally {
    isRestoringSourceBrowseContext = false
  }
}

function readSourceScrollTop(): number {
  const scrollRoot = document.querySelector<HTMLElement>('main.cinema-scrollbar')
  return Math.max(0, Math.round(scrollRoot?.scrollTop ?? 0))
}

function restoreSourceScrollTop(scrollTop: number) {
  if (!Number.isFinite(scrollTop) || scrollTop <= 0)
    return
  void nextTick(() => {
    window.requestAnimationFrame(() => {
      document.querySelector<HTMLElement>('main.cinema-scrollbar')
        ?.scrollTo({ top: scrollTop, left: 0, behavior: 'auto' })
    })
  })
}

async function switchViewMode(mode: SourceViewMode) {
  if (!isRawFileSource.value || viewMode.value === mode)
    return

  viewMode.value = mode
  requestAppScrollTop()
  errorMessage.value = null
  if (mode === 'media-library') {
    backToLibraries()
    await prepareRawSourceIndex()
    if (libraries.value.length === 0)
      await loadSourceRoot()
    await persistSourceBrowseContext()
    return
  }

  selectedScannedCategoryId.value = null
  isScanManagementOpen.value = false
  await loadSourceRoot()
  await persistSourceBrowseContext()
}

async function loadLibrary(library: MediaLibrary) {
  if (!source.value)
    return

  selectedLibrary.value = library
  requestAppScrollTop()
  navigationStack.value = [{ id: library.id, name: library.name, type: library.type }]
  searchKeyword.value = ''
  isLoading.value = true
  errorMessage.value = null
  const pendingUpdate = sourceConfig.value?.type === 'server' ? store.getPendingServerUpdate(sourceId.value) : null
  try {
    items.value = await source.value.list(library.id)
    if (pendingUpdate && /^\d+$/.test(library.id))
      store.acknowledgeServerUpdate(pendingUpdate, [library.id])
  }
  catch (error) {
    items.value = []
    errorMessage.value = toSafeErrorMessage(error, '媒体条目加载失败。')
  }
  finally {
    isLoading.value = false
  }
  await persistSourceBrowseContext()
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
    items.value = normalizeWorkLevelSearchResults(await source.value.search(keyword))
    selectedLibrary.value = {
      id: 'search',
      sourceId: sourceId.value,
      name: `搜索：${keyword}`,
      type: 'mixed',
    }
    navigationStack.value = [{ id: 'search', name: `搜索：${keyword}`, type: 'mixed', isSearch: true }]
    requestAppScrollTop()
    await persistSourceBrowseContext()
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
  requestAppScrollTop()
  void persistSourceBrowseContext()
}

async function handleInPageBack(): Promise<boolean> {
  if (isMediaLibraryView.value && selectedScannedCategoryId.value) {
    backToScannedCategories()
    return true
  }
  if (!selectedLibrary.value)
    return false
  if (navigationStack.value.length > 1) {
    await navigateToCrumb(navigationStack.value.length - 2)
    return true
  }
  backToLibraries()
  return true
}

async function navigateToCrumb(index: number) {
  const crumb = navigationStack.value[index]
  if (!crumb || crumb.isSearch)
    return

  searchKeyword.value = ''
  navigationStack.value = navigationStack.value.slice(0, index + 1)
  await loadNestedItems(crumb.id)
  await persistSourceBrowseContext()
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
      await persistSourceBrowseContext()
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
  await persistSourceBrowseContext({ captureScroll: true })
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

  requestAppScrollTop()
  isLoading.value = true
  errorMessage.value = null
  const pendingUpdate = sourceConfig.value?.type === 'server' ? store.getPendingServerUpdate(sourceId.value) : null
  try {
    items.value = await source.value.list(parentId)
    const libraryId = selectedLibrary.value?.id
    if (pendingUpdate && libraryId && /^\d+$/.test(libraryId))
      store.acknowledgeServerUpdate(pendingUpdate, [libraryId])
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
    await persistSourceBrowseContext({ captureScroll: true })
    const queue = createPlaybackQueue(currentQueueItems(), item.id)
    const playbackContextId = savePlaybackMediaContext({
      sourceId: sourceId.value,
      itemId: item.id,
      title: item.name,
      currentItem: item,
      queue,
    })
    await router.push({
      name: 'player',
      query: createPlaybackRouteQuery({
        sourceId: sourceId.value,
        itemId: item.id,
        contextId: playbackContextId,
      }),
    })
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '无法获取播放地址。')
  }
  finally {
    isLoading.value = false
  }
}

async function prepareRawSourceIndex() {
  await loadScanCacheForCurrentSource()
  await refreshRawIndexStatusForCurrentSource()
  void ensureRawSourceIndexForCurrentSource()
}

async function refreshRawIndexStatusForCurrentSource(): Promise<RawSourceIndexStatus | null> {
  if (!rawSourceType.value) {
    rawIndexStatus.value = null
    incrementalRawIndexStatus.value = null
    isScanning.value = false
    return null
  }

  const statuses = await rawSourceIndexScheduler.getStatuses({
    sourceId: sourceId.value,
    sourceType: rawSourceType.value,
    rootPath: rawSourceRootPath.value,
  })
  if (!isCurrentRawIndexStatus(statuses.full))
    return null

  rawIndexStatus.value = statuses.full
  incrementalRawIndexStatus.value = statuses.incremental
  refreshRawIndexBusyState()
  const failedStatus = statuses.full.state === 'failed'
    ? statuses.full
    : statuses.incremental.state === 'failed' ? statuses.incremental : null
  if (failedStatus)
    scanErrorMessage.value = failedStatus.errorMessage ?? '后台索引未完成，文件夹浏览和播放仍可继续使用。'
  return statuses.full
}

async function ensureRawSourceIndexForCurrentSource() {
  if (scanCache.value || rawIndexStatus.value?.state === 'failed')
    return

  const target = currentRawSourceIndexTarget()
  if (!target)
    return

  if (rawIndexStatus.value?.state === 'running' || rawIndexStatus.value?.state === 'queued') {
    isScanning.value = true
    return
  }

  if (rawIndexStatus.value?.state === 'cooldown')
    return

  const generation = rawIndexGeneration
  rawIndexStatus.value = {
    sourceId: target.sourceId,
    sourceType: target.sourceType,
    rootPath: target.rootPath,
    scanKind: 'full',
    state: 'queued',
  }
  isScanning.value = true
  scanErrorMessage.value = null
  scanLiveLogs.value = []
  selectedScannedCategoryId.value = null
  try {
    const cache = await rawSourceIndexScheduler.forceScan(target, {
      scanKind: 'full',
      onLog: entry => scanLiveLogs.value = [...scanLiveLogs.value.slice(-7), entry],
    })
    if (generation === rawIndexGeneration && isCurrentRawIndexTarget(target))
      scanCache.value = cache
  }
  catch (error) {
    if (generation === rawIndexGeneration && isCurrentRawIndexTarget(target))
      scanErrorMessage.value = toSafeErrorMessage(error, '扫描失败。文件夹浏览和播放仍可继续使用。')
  }
  finally {
    if (generation === rawIndexGeneration && isCurrentRawIndexTarget(target)) {
      isScanning.value = false
      await refreshRawIndexStatusForCurrentSource()
    }
  }
}

async function startLocalScan(scanKind: RawSourceScanKind = 'full') {
  const target = currentRawSourceIndexTarget()
  if (!target)
    return

  const generation = rawIndexGeneration
  setRawIndexStatus({
    sourceId: target.sourceId,
    sourceType: target.sourceType,
    rootPath: target.rootPath,
    scanKind,
    state: 'queued',
  })
  isScanning.value = true
  scanErrorMessage.value = null
  scanLiveLogs.value = []
  selectedScannedCategoryId.value = null
  try {
    const cache = await rawSourceIndexScheduler.forceScan(target, {
      scanKind,
      onLog: entry => scanLiveLogs.value = [...scanLiveLogs.value.slice(-7), entry],
    })
    if (generation === rawIndexGeneration && isCurrentRawIndexTarget(target))
      scanCache.value = cache
  }
  catch (error) {
    if (generation === rawIndexGeneration && isCurrentRawIndexTarget(target))
      scanErrorMessage.value = toSafeErrorMessage(error, '扫描失败。文件夹浏览和播放仍可继续使用。')
  }
  finally {
    if (generation === rawIndexGeneration && isCurrentRawIndexTarget(target)) {
      isScanning.value = false
      await refreshRawIndexStatusForCurrentSource()
    }
  }
}

async function startFullRescrape() {
  isScanManagementOpen.value = true
  await startLocalScan('full')
}

watchEffect(() => {
  if (!isRawFileSource.value || !isMediaLibraryView.value || !scanCache.value) {
    clearLayoutContextActions(layoutContextOwner)
    return
  }

  setLayoutContextActions(layoutContextOwner, [
    {
      id: 'raw-source-rescrape',
      label: isScanning.value ? '重新刮削中…' : '重新刮削',
      description: '全量扫描目录并重新匹配媒体信息',
      icon: 'rescrape',
      disabled: isScanning.value || !source.value,
      execute: startFullRescrape,
    },
    {
      id: 'raw-source-scan-management',
      label: isScanManagementOpen.value ? '收起扫描管理' : '扫描管理',
      description: '查看扫描状态、结构判断和日志',
      icon: 'scan',
      active: isScanManagementOpen.value,
      execute: () => {
        isScanManagementOpen.value = !isScanManagementOpen.value
      },
    },
    {
      id: 'raw-source-folder-view',
      label: '文件夹',
      description: '按数据源原始目录浏览媒体文件',
      icon: 'folder',
      execute: () => switchViewMode('folders'),
    },
  ])
})

async function loadScanCacheForCurrentSource(options: { preserveLiveLogs?: boolean } = {}) {
  scanErrorMessage.value = null
  if (!options.preserveLiveLogs)
    scanLiveLogs.value = []
  if (!rawSourceType.value) {
    scanCache.value = null
    selectedScannedCategoryId.value = null
    return
  }

  scanCache.value = await loadRawSourceScanCache(sourceId.value, rawSourceType.value, rawSourceRootPath.value)
  if (!scanCache.value)
    selectedScannedCategoryId.value = null
}

function currentRawSourceIndexTarget(): RawSourceIndexTarget | null {
  if (!source.value || !rawSourceType.value)
    return null

  return {
    source: source.value,
    sourceId: sourceId.value,
    sourceType: rawSourceType.value,
    rootPath: rawSourceRootPath.value,
  }
}

function setRawIndexStatus(status: RawSourceIndexStatus): void {
  if (status.scanKind === 'incremental')
    incrementalRawIndexStatus.value = status
  else
    rawIndexStatus.value = status
  refreshRawIndexBusyState()
}

function refreshRawIndexBusyState(): void {
  isScanning.value = rawIndexStatus.value?.state === 'running'
    || incrementalRawIndexStatus.value?.state === 'running'
    || rawIndexStatus.value?.state === 'queued'
    || incrementalRawIndexStatus.value?.state === 'queued'
}

function isCurrentRawIndexStatus(status: RawSourceIndexStatus): boolean {
  return status.sourceId === sourceId.value
    && status.sourceType === rawSourceType.value
    && status.rootPath === rawSourceRootPath.value
}

function isCurrentRawIndexTarget(target: Pick<RawSourceIndexTarget, 'sourceId' | 'sourceType' | 'rootPath'>): boolean {
  return target.sourceId === sourceId.value
    && target.sourceType === rawSourceType.value
    && target.rootPath === rawSourceRootPath.value
}

function syncDefaultViewModeForSource() {
  viewMode.value = isRawFileSource.value ? 'media-library' : 'folders'
}

function currentQueueItems(): MediaItem[] {
  if (!isMediaLibraryView.value)
    return items.value
  return selectedScannedCategory.value ? selectedCategoryQueueItems.value : allScannedQueueItems.value
}

function selectScannedCategory(category: ScannedCategory) {
  selectedScannedCategoryId.value = category.id
  requestAppScrollTop()
  void persistSourceBrowseContext()
}

function backToScannedCategories() {
  selectedScannedCategoryId.value = null
  requestAppScrollTop()
  void persistSourceBrowseContext()
}

function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    if (isIdentificationDialogOpen.value)
      closeIdentificationDialog()
  }
}

function openIdentificationDialog(work: ScannedWorkItem) {
  identificationSearchRequestId += 1
  identificationTarget.value = work
  identificationActiveTab.value = 'match'
  identificationQuery.value = defaultIdentificationQuery(work)
  identificationMediaType.value = inferIdentificationMediaType(work)
  identificationYear.value = defaultIdentificationYear(work)
  identificationTmdbId.value = defaultIdentificationTmdbId(work)
  identificationImdbId.value = defaultIdentificationImdbId(work)
  identificationTvdbId.value = defaultIdentificationTvdbId(work)
  identificationResults.value = []
  identificationErrorMessage.value = null
  identificationInfoMessage.value = null
  artworkSearchKind.value = null
  artworkSearchResults.value = []
  isIdentificationSearching.value = false
  isIdentificationApplying.value = false
  isArtworkSearching.value = false
  isArtworkApplying.value = false
  isIdentificationDialogOpen.value = true
}

function closeIdentificationDialog() {
  identificationSearchRequestId += 1
  isIdentificationDialogOpen.value = false
  identificationTarget.value = null
  identificationActiveTab.value = 'match'
  identificationResults.value = []
  identificationErrorMessage.value = null
  identificationInfoMessage.value = null
  artworkSearchKind.value = null
  artworkSearchResults.value = []
  isIdentificationSearching.value = false
  isIdentificationApplying.value = false
  isArtworkSearching.value = false
  isArtworkApplying.value = false
}

async function searchIdentificationResults() {
  const keyword = identificationQuery.value.trim()
  const tmdbId = parsePositiveInteger(identificationTmdbId.value)
  const year = parsePositiveInteger(identificationYear.value)
  if (!keyword && !tmdbId) {
    identificationErrorMessage.value = '请输入标题，或填写 TheMovieDb 标识符后精确查找。'
    return
  }

  isIdentificationSearching.value = true
  identificationErrorMessage.value = null
  identificationInfoMessage.value = externalIdStatusMessage()
  identificationResults.value = []
  const requestId = ++identificationSearchRequestId
  try {
    const credential = await readConfiguredTmdbCredential()
    if (requestId !== identificationSearchRequestId || !isIdentificationDialogOpen.value)
      return

    if (!credential) {
      identificationErrorMessage.value = '当前构建未提供 TMDB 内置凭据，请在刮削与分类设置中填写自定义凭据。'
      return
    }

    const tmdb = new TmdbScraper(credential, loadTmdbLocalSettings())
    const results = tmdbId
      ? [await tmdb.getDetail(identificationMediaType.value, tmdbId)]
      : await tmdb.searchChoices(identificationMediaType.value, keyword, year, 8)
    if (requestId !== identificationSearchRequestId || !isIdentificationDialogOpen.value)
      return

    identificationResults.value = results
    if (tmdbId)
      identificationInfoMessage.value = [externalIdStatusMessage(), `已按 TheMovieDb ID ${tmdbId} 精确获取详情。`].filter(Boolean).join(' ')
    if (identificationResults.value.length === 0)
      identificationErrorMessage.value = '没有找到可用的 TMDB 结果，可以换一个关键词再试。'
  }
  catch (error) {
    if (requestId !== identificationSearchRequestId || !isIdentificationDialogOpen.value)
      return

    identificationErrorMessage.value = toSafeErrorMessage(error, 'TMDB 搜索失败。')
  }
  finally {
    if (requestId === identificationSearchRequestId)
      isIdentificationSearching.value = false
  }
}

async function applyIdentificationResult(metadata: TmdbMetadata) {
  const target = identificationTarget.value
  const targetCandidate = target?.entries[0]?.candidate
  if (!scanCache.value || !targetCandidate)
    return

  isIdentificationApplying.value = true
  identificationErrorMessage.value = null
  try {
    const identifiedCache = applyRawManualIdentification(scanCache.value, {
      targetRecordId: targetCandidate.record.id,
      metadata,
      matchedSearchTitle: identificationQuery.value.trim() || metadata.title,
      searchTitles: [identificationQuery.value, metadata.title, metadata.originalTitle]
        .filter((value): value is string => Boolean(value?.trim())),
    })
    const nextCache = await enrichIdentifiedTvEpisodeMetadata(identifiedCache, targetCandidate.record.id, metadata)

    if (!await saveRawSourceScanCache(nextCache)) {
      identificationErrorMessage.value = '本地扫描缓存写入失败，本次修正未保存。'
      return
    }

    scanCache.value = nextCache
    selectedScannedCategoryId.value = categoryIdFromName(
      nextCache.scrapedItems?.find(item => item.recordId === targetCandidate.record.id)?.categoryName,
    )
    identificationTmdbId.value = String(metadata.tmdbId)
    identificationYear.value = metadata.releaseYear ? String(metadata.releaseYear) : identificationYear.value
    identificationImdbId.value = metadata.imdbId ?? identificationImdbId.value
    identificationTvdbId.value = metadata.tvdbId == null ? identificationTvdbId.value : String(metadata.tvdbId)
    identificationActiveTab.value = 'images'
    identificationInfoMessage.value = '识别结果已写入本地扫描缓存；可以继续编辑海报、徽标和背景图。'
  }
  catch (error) {
    identificationErrorMessage.value = toSafeErrorMessage(error, '识别结果写入失败。')
  }
  finally {
    isIdentificationApplying.value = false
  }
}

async function searchArtworkCandidates(kind: EditableArtworkKind) {
  const tmdbId = parsePositiveInteger(identificationTmdbId.value) ?? identificationCurrentMetadata.value?.tmdbId
  if (!tmdbId) {
    identificationErrorMessage.value = '请先完成识别，或填写 TheMovieDb 标识符后再搜索图片。'
    identificationActiveTab.value = 'match'
    return
  }

  isArtworkSearching.value = true
  artworkSearchKind.value = kind
  artworkSearchResults.value = []
  identificationErrorMessage.value = null
  identificationInfoMessage.value = null
  const requestId = ++identificationSearchRequestId
  try {
    const credential = await readConfiguredTmdbCredential()
    if (requestId !== identificationSearchRequestId || !isIdentificationDialogOpen.value)
      return

    if (!credential) {
      identificationErrorMessage.value = '当前构建未提供 TMDB 内置凭据，请在刮削与分类设置中填写自定义凭据。'
      return
    }

    const tmdb = new TmdbScraper(credential, loadTmdbLocalSettings())
    if (!identificationCurrentMetadata.value) {
      const didApplyMetadata = await ensureIdentificationMetadataForArtwork(tmdb, tmdbId, requestId)
      if (!didApplyMetadata)
        return
    }

    const results = await tmdb.getImageCandidates(identificationMediaType.value, tmdbId, kind)
    if (requestId !== identificationSearchRequestId || !isIdentificationDialogOpen.value)
      return

    artworkSearchResults.value = results
    if (artworkSearchResults.value.length === 0)
      identificationInfoMessage.value = `TMDB 暂无可用${artworkKindLabel(kind)}候选。`
  }
  catch (error) {
    if (requestId !== identificationSearchRequestId || !isIdentificationDialogOpen.value)
      return
    identificationErrorMessage.value = toSafeErrorMessage(error, 'TMDB 图片搜索失败。')
  }
  finally {
    if (requestId === identificationSearchRequestId)
      isArtworkSearching.value = false
  }
}

function searchArtworkFromCard(card: IdentificationArtworkCard) {
  if (!card.enabled || !isEditableArtworkKind(card.kind))
    return
  void searchArtworkCandidates(card.kind)
}

function clearArtworkFromCard(card: IdentificationArtworkCard) {
  if (!card.enabled || !isEditableArtworkKind(card.kind))
    return
  void clearArtworkOverride(card.kind)
}

async function applyArtworkCandidate(image: TmdbImageCandidate) {
  if (!isEditableArtworkKind(image.kind))
    return

  await updateArtworkOverride(image.kind, image.imageUrl, image.filePath)
}

async function clearArtworkOverride(kind: EditableArtworkKind) {
  await updateArtworkOverride(kind, undefined, undefined)
}

async function updateArtworkOverride(kind: EditableArtworkKind, imageUrl: string | undefined, filePath: string | undefined) {
  const targetCandidate = identificationTarget.value?.entries[0]?.candidate
  if (!scanCache.value || !targetCandidate)
    return

  isArtworkApplying.value = true
  identificationErrorMessage.value = null
  identificationInfoMessage.value = null
  try {
    const nextCache = applyRawManualArtworkOverride(scanCache.value, {
      targetRecordId: targetCandidate.record.id,
      kind,
      imageUrl,
      filePath,
    })

    if (!await saveRawSourceScanCache(nextCache)) {
      identificationErrorMessage.value = '本地扫描缓存写入失败，本次图片修改未保存。'
      return
    }

    scanCache.value = nextCache
    identificationInfoMessage.value = `${imageUrl ? '已应用' : '已清除'}${artworkKindLabel(kind)}本地覆盖；不会写回数据源目录。`
  }
  catch (error) {
    identificationErrorMessage.value = toSafeErrorMessage(error, '图片覆盖写入失败。')
  }
  finally {
    isArtworkApplying.value = false
  }
}

async function ensureIdentificationMetadataForArtwork(tmdb: TmdbScraper, tmdbId: number, requestId: number): Promise<boolean> {
  const targetCandidate = identificationTarget.value?.entries[0]?.candidate
  if (!scanCache.value || !targetCandidate)
    return false

  const metadata = await tmdb.getDetail(identificationMediaType.value, tmdbId)
  if (requestId !== identificationSearchRequestId || !isIdentificationDialogOpen.value)
    return false

  const identifiedCache = applyRawManualIdentification(scanCache.value, {
    targetRecordId: targetCandidate.record.id,
    metadata,
    matchedSearchTitle: identificationQuery.value.trim() || metadata.title,
    searchTitles: [identificationQuery.value, metadata.title, metadata.originalTitle]
      .filter((value): value is string => Boolean(value?.trim())),
  })
  const nextCache = await enrichIdentifiedTvEpisodeMetadata(identifiedCache, targetCandidate.record.id, metadata, tmdb)
  if (
    requestId !== identificationSearchRequestId
    || !isIdentificationDialogOpen.value
    || identificationTarget.value?.entries[0]?.candidate.record.id !== targetCandidate.record.id
  ) {
    return false
  }

  if (!await saveRawSourceScanCache(nextCache))
    throw new Error('本地扫描缓存写入失败，本次图片修改未保存。')

  scanCache.value = nextCache
  identificationTmdbId.value = String(metadata.tmdbId)
  identificationYear.value = metadata.releaseYear ? String(metadata.releaseYear) : identificationYear.value
  identificationImdbId.value = metadata.imdbId ?? identificationImdbId.value
  identificationTvdbId.value = metadata.tvdbId == null ? identificationTvdbId.value : String(metadata.tvdbId)
  return true
}

async function enrichIdentifiedTvEpisodeMetadata(
  cache: RawLocalScanCache,
  targetRecordId: string,
  metadata: TmdbMetadata,
  existingTmdb?: TmdbScraper,
): Promise<RawLocalScanCache> {
  if (metadata.mediaType !== 'tv')
    return cache

  const target = cache.candidates.find(candidate => candidate.record.id === targetRecordId)
  if (!target)
    return cache

  const credential = existingTmdb ? undefined : await readConfiguredTmdbCredential()
  const tmdb = existingTmdb ?? (credential ? new TmdbScraper(credential, loadTmdbLocalSettings()) : undefined)
  if (!tmdb)
    return cache

  const targetGroupKey = createRawSeriesGroupingKey(target)
  const targetCandidates = cache.candidates.filter(candidate => createRawSeriesGroupingKey(candidate) === targetGroupKey)
  const enrichedItems = await enrichRawScrapedItemsEpisodeMetadata(targetCandidates, cache.scrapedItems, tmdb, {
    onLog: entry => scanLiveLogs.value = [...scanLiveLogs.value.slice(-7), entry],
  })
  const enrichedByRecordId = new Map(enrichedItems.map(item => [item.recordId, item]))
  return {
    ...cache,
    scrapedItems: (cache.scrapedItems ?? []).map(item => enrichedByRecordId.get(item.recordId) ?? item),
  }
}

async function openScannedSeriesDetail(series: ScannedSeriesWork) {
  await persistSourceBrowseContext({ captureScroll: true })
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
      entries: work.entries,
      episodes: work.episodes,
      seasons: work.seasons ?? [],
    })
    return
  }

  await persistSourceBrowseContext({ captureScroll: true })
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
    children: series.seasons.length > 0 ? series.seasons : series.episodes,
    mediaSources: [],
  }
}

function createScannedMediaDetail(item: MediaItem): MediaDetail {
  return {
    ...item,
    mediaSources: [],
  }
}

function categoryNameForCandidate(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem): string {
  return categoryNameForRawCandidate(candidate, scraped)
}

function isUnresolvedCategoryEntry(entry: ScannedDisplayItem): boolean {
  return entry.categoryName === RAW_UNRESOLVED_CATEGORY_NAME
}

function defaultIdentificationQuery(work: ScannedWorkItem): string {
  const representative = work.entries[0]
  const metadata = representative ? metadataForCandidate(representative.candidate, representative.scraped) : undefined
  return metadata?.title
    ?? representative?.candidate.seriesTitle
    ?? representative?.candidate.title
    ?? work.item.name
}

function defaultIdentificationYear(work: ScannedWorkItem): string {
  const representative = work.entries[0]
  const metadata = representative ? metadataForCandidate(representative.candidate, representative.scraped) : undefined
  return String(metadata?.releaseYear ?? representative?.candidate.year ?? work.item.year ?? '')
}

function defaultIdentificationTmdbId(work: ScannedWorkItem): string {
  const representative = work.entries[0]
  const metadata = representative ? metadataForCandidate(representative.candidate, representative.scraped) : undefined
  return metadata?.tmdbId == null ? '' : String(metadata.tmdbId)
}

function defaultIdentificationImdbId(work: ScannedWorkItem): string {
  const representative = work.entries[0]
  const metadata = representative ? metadataForCandidate(representative.candidate, representative.scraped) : undefined
  return metadata?.imdbId ?? ''
}

function defaultIdentificationTvdbId(work: ScannedWorkItem): string {
  const representative = work.entries[0]
  const metadata = representative ? metadataForCandidate(representative.candidate, representative.scraped) : undefined
  return metadata?.tvdbId == null ? '' : String(metadata.tvdbId)
}

function inferIdentificationMediaType(work: ScannedWorkItem): ScrapeMediaType {
  const representative = work.entries[0]
  const metadata = representative ? metadataForCandidate(representative.candidate, representative.scraped) : undefined
  if (metadata?.mediaType)
    return metadata.mediaType
  if (work.domain === 'tv')
    return 'tv'
  if (work.domain === 'movie')
    return 'movie'
  const candidate = representative?.candidate
  return candidate?.kind === 'episode' || candidate?.kind === 'tv' ? 'tv' : 'movie'
}

function parsePositiveInteger(value: string): number | undefined {
  const normalized = value.trim()
  if (!normalized)
    return undefined
  if (!/^\d+$/.test(normalized))
    return undefined
  const parsed = Number.parseInt(normalized, 10)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
}

function externalIdStatusMessage(): string | null {
  const fields = [
    identificationImdbId.value.trim() ? 'IMDb' : undefined,
    identificationTvdbId.value.trim() ? 'TheTVDB' : undefined,
  ].filter((value): value is string => Boolean(value))

  if (fields.length === 0)
    return null
  return `${fields.join('/')} 标识符已保留为识别条件备注；当前 MVP 不做外部 ID 反查，请使用标题/年份搜索或 TheMovieDb ID 精确查找。`
}

function isEditableArtworkKind(value: string): value is EditableArtworkKind {
  return value === 'poster' || value === 'logo' || value === 'backdrop'
}

function artworkKindLabel(kind: EditableArtworkKind): string {
  if (kind === 'poster')
    return '海报'
  if (kind === 'logo')
    return '徽标'
  return '背景图'
}

function categoryIdFromName(categoryName: string | undefined): string | null {
  return categoryName ? `category:${encodeURIComponent(categoryName)}` : null
}

function metadataYearLabel(metadata: TmdbMetadata): string {
  return metadata.releaseYear ? String(metadata.releaseYear) : '年份未知'
}

function metadataTypeLabel(metadata: TmdbMetadata): string {
  return metadata.mediaType === 'movie' ? '电影' : '剧集'
}
</script>

<template>
  <div class="source-view theme-adaptive relative min-h-full">
    <ServerLibraryUpdateNotice
      :visible="pendingServerViewUpdate != null"
      :busy="isApplyingServerUpdate"
      @refresh="refreshCurrentServerView"
    />
    <div class="source-page-content mobile-nav-safe space-y-8 px-4 pb-6 pt-20 sm:p-6 sm:pl-20 sm:pt-20">
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
        <section v-if="isFolderView && !selectedLibrary && heroItems.length" class="-mx-4 -mt-20 overflow-hidden rounded-b-[2.4rem] sm:-mx-6 md:-ml-20">
          <HeroCarousel :items="heroItems" @play="handlePlay" @detail="openDetail" />
        </section>

        <section v-if="isMediaLibraryView && !selectedScannedCategory && sourceLandingHeroItems.length" class="-mx-4 -mt-20 overflow-hidden rounded-b-[2.4rem] sm:-mx-6 md:-ml-20">
          <HeroCarousel :items="sourceLandingHeroItems" @play="handlePlay" @detail="handleSelect" />
        </section>

        <div v-if="isFolderView" class="flex flex-wrap items-center justify-between gap-4">
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

          <form class="source-mobile-search flex min-w-72 gap-2" @submit.prevent="runSearch">
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

        <div v-if="isRawFileSource && isFolderView" class="flex flex-wrap items-center justify-between gap-3">
          <button
            class="rounded-2xl border border-white/10 bg-white/6 px-4 py-2.5 text-sm font-semibold text-white/72 transition-colors hover:bg-white/12 hover:text-white"
            @click="switchViewMode('media-library')"
          >
            返回媒体库
          </button>
          <p class="text-sm text-white/40">
            当前根目录：{{ rawSourceRootLabel }}
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
          <div
            v-if="scanErrorMessage"
            class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100"
          >
            {{ scanErrorMessage }}
          </div>

          <section
            v-if="isScanManagementOpen"
            id="source-scan-management"
            class="scan-management-panel rounded-2xl border border-white/10 bg-white/6 p-5"
          >
            <div class="scan-management-header flex flex-wrap items-start justify-between gap-4">
              <div class="scan-management-copy min-w-0">
                <h3 class="text-base font-semibold text-white">
                  扫描管理
                </h3>
                <p class="mt-2 max-w-2xl text-sm leading-6 text-white/46">
                  根目录 {{ rawSourceRootLabel }}。扫描只读取目录和文件名，结果保存在本机缓存，不写回数据源目录。
                </p>
              </div>
              <div class="scan-management-actions flex flex-wrap gap-2">
                <button
                  class="rounded-2xl bg-primary px-5 py-3 text-sm font-semibold text-black transition-opacity disabled:cursor-wait disabled:opacity-60"
                  :disabled="isScanning || !source"
                  @click="startFullRescrape"
                >
                  {{ isScanning ? '重新刮削中…' : scanCache ? '重新刮削' : '立即索引' }}
                </button>
                <button
                  class="rounded-2xl border border-white/10 bg-white/8 px-5 py-3 text-sm font-semibold text-white/74 transition-colors hover:bg-white/14 hover:text-white disabled:cursor-wait disabled:opacity-45"
                  :disabled="isScanning || !source"
                  @click="startLocalScan('incremental')"
                >
                  增量扫描
                </button>
              </div>
            </div>

            <div class="scan-management-stats mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-6">
              <div class="scan-stat">
                <p>全量</p>
                <strong>{{ fullScanStatusLabel }}</strong>
                <span>{{ fullScanLastRunLabel }}</span>
              </div>
              <div class="scan-stat">
                <p>增量</p>
                <strong>{{ incrementalScanStatusLabel }}</strong>
                <span>{{ incrementalScanLastRunLabel }}</span>
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
                <p>已分类电影 / 剧集</p>
                <strong>{{ scanStats.movie }} / {{ scanStats.tv }}</strong>
              </div>
              <div class="scan-stat">
                <p>未识别</p>
                <strong>{{ scanStats.unresolved }}</strong>
              </div>
            </div>

            <div v-if="scanCache || scanLogEntries.length" class="scan-management-details mt-5 grid gap-4 lg:grid-cols-2">
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
            <section class="space-y-4">
              <div>
                <div>
                  <h2 class="text-xl font-bold text-white">
                    媒体库
                  </h2>
                </div>
              </div>

              <div class="first-index-panel rounded-[1.75rem] border border-white/10 p-5">
                <div class="first-index-header flex flex-wrap items-start justify-between gap-5">
                  <div class="first-index-summary flex min-w-0 flex-1 gap-4">
                    <div class="first-index-spinner" :class="{ 'is-active': isRawIndexBusy }" aria-hidden="true">
                      <span />
                    </div>
                    <div class="first-index-copy min-w-0">
                      <p class="text-xs font-semibold uppercase tracking-[0.2em] text-primary/80">
                        {{ firstIndexStatusLabel }}
                      </p>
                      <h3 class="mt-2 text-lg font-bold text-white">
                        {{ firstIndexTitle }}
                      </h3>
                      <p class="mt-2 max-w-2xl text-sm leading-6 text-white/52">
                        {{ firstIndexDescription }}
                      </p>
                    </div>
                  </div>
                  <div class="first-index-actions flex flex-wrap gap-2">
                    <button
                      class="rounded-2xl bg-primary px-5 py-3 text-sm font-semibold text-black transition-opacity disabled:cursor-wait disabled:opacity-60"
                      :disabled="isRawIndexBusy || !source"
                      @click="startLocalScan()"
                    >
                      {{ firstIndexActionLabel }}
                    </button>
                    <button
                      class="rounded-2xl border border-white/10 bg-white/6 px-5 py-3 text-sm font-semibold text-white/70 transition-colors hover:bg-white/12 hover:text-white"
                      @click="switchViewMode('folders')"
                    >
                      文件夹
                    </button>
                  </div>
                </div>

                <div class="raw-index-progress mt-5" :class="{ 'is-active': isRawIndexBusy }">
                  <span />
                </div>

                <div class="first-index-stats mt-5 grid gap-3 sm:grid-cols-3">
                  <div class="scan-stat">
                    <p>根目录</p>
                    <strong class="truncate">{{ rawSourceRootLabel }}</strong>
                  </div>
                  <div class="scan-stat">
                    <p>状态</p>
                    <strong>{{ scanStatusLabel }}</strong>
                  </div>
                  <div class="scan-stat">
                    <p>当前缓存</p>
                    <strong>尚未生成</strong>
                  </div>
                </div>

                <div v-if="scanLogEntries.length" class="first-index-log mt-5 rounded-2xl border border-white/8 bg-black/14 p-4">
                  <h4 class="text-sm font-semibold text-white">
                    最近扫描
                  </h4>
                  <div class="mt-3 space-y-2 text-sm leading-6 text-white/52">
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
            </section>
          </template>

          <template v-else-if="!selectedScannedCategory">
            <section class="space-y-4">
              <div>
                <h2 class="text-xl font-bold text-white">
                  媒体库
                </h2>
              </div>

              <MediaGrid
                :items="scannedCategoryLibraries"
                empty-title="当前扫描没有可显示的媒体库"
                empty-description="扫描缓存暂无可展示分类；现有目录访问不受影响。"
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
                可以返回分类页或重新扫描当前根目录。
              </p>
            </div>

            <section v-else class="space-y-4">
              <div>
                <h3 class="text-lg font-bold text-white">
                  作品
                </h3>
                <p class="mt-1 text-sm text-white/36">
                  {{ selectedScannedCategoryDescription }}
                </p>
              </div>
              <MediaGrid
                :items="selectedCategoryWorkItems"
                @select="handleSelect"
                @play="handlePlay"
              />
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

        <section
          v-for="section in supplementalHomeSections"
          v-show="isFolderView && !selectedLibrary"
          :key="section.id"
        >
          <div class="mb-4 flex items-end justify-between gap-4">
            <div>
              <h2 class="text-xl font-bold text-white">
                {{ section.title }}
              </h2>
              <p v-if="section.errorCode" class="mt-1 text-sm text-amber-200/70">
                该板块暂时不可用，其他内容仍可正常浏览。
              </p>
            </div>
            <button
              v-if="section.viewAllRoute && section.items.length"
              type="button"
              class="rounded-xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14 hover:text-white"
              @click="openHomeSection(section)"
            >
              查看全部
            </button>
          </div>
          <MediaGrid
            v-if="section.items.length || section.purpose === 'libraries'"
            :items="section.items"
            :empty-title="section.purpose === 'libraries' ? '当前没有可访问的媒体库' : undefined"
            :empty-description="section.purpose === 'libraries' ? '请检查 Server 媒体库权限或扫库状态。' : undefined"
            @select="handleSelect"
            @play="handlePlay"
          />
        </section>

        <section v-if="isFolderView && (selectedLibrary || !hasHomeLibrarySection)">
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

    <div
      v-if="isIdentificationDialogOpen"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/68 p-6 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-label="识别"
      @click.self="closeIdentificationDialog"
    >
      <section class="identification-dialog theme-adaptive max-h-[90vh] w-full max-w-5xl overflow-hidden rounded-3xl border border-white/10 shadow-2xl">
        <div class="flex items-center gap-3 border-b border-white/8 p-4">
          <button
            class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-2xl bg-white/8 text-white/70 transition-colors hover:bg-white/14 hover:text-white"
            type="button"
            aria-label="关闭识别"
            title="关闭识别"
            @click="closeIdentificationDialog"
          >
            <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
              <path d="M12.5 4.5 7 10l5.5 5.5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          <div class="min-w-0">
            <h3 class="text-xl font-bold text-white">
              识别
            </h3>
            <p class="mt-1 truncate text-xs text-white/40">
              {{ identificationTarget?.item.name || '本地媒体' }}
            </p>
          </div>
        </div>

        <div class="max-h-[calc(90vh-4.5rem)] overflow-y-auto p-5">
          <div class="rounded-2xl border border-white/8 bg-black/18 p-4">
            <p class="text-xs font-semibold uppercase tracking-[0.2em] text-white/34">
              源路径
            </p>
            <p class="mt-2 break-all rounded-xl border border-white/8 bg-white/5 px-3 py-2 text-sm text-white/58">
              {{ identificationSourcePath || '无路径信息' }}
            </p>
          </div>

          <div class="mt-5 flex flex-wrap gap-2">
            <button
              type="button"
              class="rounded-2xl px-4 py-2 text-sm font-semibold transition-colors"
              :class="identificationActiveTab === 'match' ? 'bg-white text-black' : 'bg-white/8 text-white/62 hover:bg-white/14 hover:text-white'"
              @click="identificationActiveTab = 'match'"
            >
              识别信息
            </button>
            <button
              type="button"
              class="rounded-2xl px-4 py-2 text-sm font-semibold transition-colors"
              :class="identificationActiveTab === 'images' ? 'bg-white text-black' : 'bg-white/8 text-white/62 hover:bg-white/14 hover:text-white'"
              @click="identificationActiveTab = 'images'"
            >
              编辑图片
            </button>
          </div>

          <div
            v-if="identificationErrorMessage"
            class="mt-4 rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm text-red-100"
          >
            {{ identificationErrorMessage }}
          </div>
          <div
            v-if="identificationInfoMessage"
            class="mt-4 rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm leading-6 text-white/58"
          >
            {{ identificationInfoMessage }}
          </div>

          <section v-if="identificationActiveTab === 'match'" class="mt-5 space-y-5">
            <form class="space-y-4" @submit.prevent="searchIdentificationResults">
              <div class="grid gap-3 md:grid-cols-[1fr_9rem_10rem]">
                <label class="block">
                  <span class="text-xs font-semibold text-white/42">标题</span>
                  <input
                    v-model="identificationQuery"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none placeholder:text-white/25 focus:border-primary/60"
                    placeholder="片名或剧名"
                  >
                </label>
                <label class="block">
                  <span class="text-xs font-semibold text-white/42">年份</span>
                  <input
                    v-model="identificationYear"
                    inputmode="numeric"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none placeholder:text-white/25 focus:border-primary/60"
                    placeholder="可选"
                  >
                </label>
                <label class="block">
                  <span class="text-xs font-semibold text-white/42">媒体类型</span>
                  <select
                    v-model="identificationMediaType"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none focus:border-primary/60"
                  >
                    <option value="movie">
                      电影
                    </option>
                    <option value="tv">
                      剧集
                    </option>
                  </select>
                </label>
              </div>

              <div class="grid gap-3 md:grid-cols-3">
                <label class="block">
                  <span class="text-xs font-semibold text-white/42">IMDb 标识符</span>
                  <input
                    v-model="identificationImdbId"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none placeholder:text-white/25 focus:border-primary/60"
                    placeholder="tt1234567"
                  >
                </label>
                <label class="block">
                  <span class="text-xs font-semibold text-white/42">TheMovieDb 标识符</span>
                  <input
                    v-model="identificationTmdbId"
                    inputmode="numeric"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none placeholder:text-white/25 focus:border-primary/60"
                    placeholder="填入后精确查找"
                  >
                </label>
                <label class="block">
                  <span class="text-xs font-semibold text-white/42">TheTVDB 标识符</span>
                  <input
                    v-model="identificationTvdbId"
                    inputmode="numeric"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none placeholder:text-white/25 focus:border-primary/60"
                    placeholder="暂不反查"
                  >
                </label>
              </div>

              <div class="flex flex-wrap items-center justify-between gap-3">
                <p class="max-w-2xl text-xs leading-5 text-white/40">
                  TheMovieDb 标识符会直接精确获取详情；标题和年份用于普通 TMDB 搜索。IMDb / TheTVDB 字段本轮仅作为可见识别条件保留，不做反向查询。
                </p>
                <button
                  class="rounded-2xl bg-primary px-6 py-3 text-sm font-semibold text-black transition-opacity disabled:cursor-wait disabled:opacity-60"
                  :disabled="isIdentificationSearching || isIdentificationApplying"
                >
                  {{ isIdentificationSearching ? '搜索中…' : '搜索' }}
                </button>
              </div>
            </form>

            <div v-if="identificationResults.length" class="grid gap-3 md:grid-cols-2">
              <button
                v-for="result in identificationResults"
                :key="`${result.mediaType}-${result.tmdbId}`"
                class="identification-result grid grid-cols-[5rem_1fr] gap-3 rounded-2xl border border-white/8 bg-white/5 p-3 text-left transition-colors hover:border-primary/50 hover:bg-white/9 disabled:cursor-wait disabled:opacity-60"
                :disabled="isIdentificationApplying"
                @click="applyIdentificationResult(result)"
              >
                <div class="aspect-[2/3] overflow-hidden rounded-xl bg-white/6">
                  <img
                    v-if="result.posterUrl"
                    :src="result.posterUrl"
                    :alt="result.title"
                    class="h-full w-full object-cover"
                    loading="lazy"
                    decoding="async"
                  >
                  <div v-else class="flex h-full items-center justify-center px-2 text-center text-xs font-semibold text-white/42">
                    {{ result.title }}
                  </div>
                </div>
                <div class="min-w-0">
                  <img
                    v-if="result.titleLogoUrl"
                    :src="result.titleLogoUrl"
                    :alt="result.title"
                    class="mb-2 max-h-8 max-w-44 object-contain object-left"
                    loading="lazy"
                    decoding="async"
                  >
                  <p class="line-clamp-2 text-sm font-bold text-white">
                    {{ result.title }}
                  </p>
                  <p class="mt-1 text-xs text-white/42">
                    {{ metadataYearLabel(result) }} · {{ metadataTypeLabel(result) }} · TMDB {{ result.tmdbId }}
                  </p>
                  <p v-if="result.rating" class="mt-1 text-xs text-primary">
                    TMDB {{ result.rating.toFixed(1) }}
                  </p>
                  <p class="mt-2 line-clamp-4 text-xs leading-5 text-white/46">
                    {{ result.overview || '暂无简介。' }}
                  </p>
                </div>
              </button>
            </div>

            <p v-else-if="!isIdentificationSearching" class="rounded-2xl border border-white/8 bg-white/5 px-4 py-5 text-sm text-white/42">
              输入识别条件并搜索后，点击候选即可写入本地扫描缓存。
            </p>
          </section>

          <section v-else class="mt-5 space-y-5">
            <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              <article
                v-for="card in identificationArtworkCards"
                :key="card.kind"
                class="rounded-2xl border border-white/8 bg-white/5 p-4"
                :class="card.enabled ? '' : 'opacity-55'"
              >
                <div class="flex items-start gap-3">
                  <div class="flex h-24 w-20 flex-shrink-0 items-center justify-center overflow-hidden rounded-xl bg-black/28">
                    <img
                      v-if="card.currentUrl"
                      :src="card.currentUrl"
                      :alt="card.label"
                      class="h-full w-full object-cover"
                      loading="lazy"
                      decoding="async"
                    >
                    <span v-else class="px-2 text-center text-xs text-white/32">暂无图片</span>
                  </div>
                  <div class="min-w-0 flex-1">
                    <div class="flex items-center justify-between gap-2">
                      <h4 class="text-sm font-bold text-white">
                        {{ card.label }}
                      </h4>
                      <span
                        class="rounded-full border px-2 py-0.5 text-[10px] font-semibold"
                        :class="card.enabled ? 'border-primary/30 bg-primary/12 text-primary' : 'border-white/8 bg-white/5 text-white/32'"
                      >
                        {{ card.enabled ? 'TMDB' : '后续支持' }}
                      </span>
                    </div>
                    <p class="mt-2 line-clamp-2 text-xs leading-5 text-white/42">
                      {{ card.description }}
                    </p>
                    <div class="mt-3 flex flex-wrap gap-2">
                      <button
                        type="button"
                        class="rounded-xl bg-white/10 px-3 py-2 text-xs font-semibold text-white/72 transition-colors hover:bg-white/16 disabled:cursor-not-allowed disabled:opacity-45"
                        :disabled="!card.enabled || isArtworkSearching || isArtworkApplying"
                        @click="searchArtworkFromCard(card)"
                      >
                        {{ isArtworkSearching && artworkSearchKind === card.kind ? '搜索中…' : '搜索' }}
                      </button>
                      <button
                        type="button"
                        class="rounded-xl border border-white/8 bg-white/5 px-3 py-2 text-xs font-semibold text-white/56 transition-colors hover:bg-white/10 disabled:cursor-not-allowed disabled:opacity-45"
                        :disabled="!card.enabled || isArtworkApplying || !card.currentUrl"
                        @click="clearArtworkFromCard(card)"
                      >
                        删除
                      </button>
                    </div>
                  </div>
                </div>
              </article>
            </div>

            <div
              v-if="artworkSearchKind"
              class="rounded-2xl border border-white/8 bg-black/14 p-4"
            >
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h4 class="text-base font-bold text-white">
                    {{ artworkKindLabel(artworkSearchKind) }}候选
                  </h4>
                  <p class="mt-1 text-xs text-white/42">
                    选择后只写入 Player 本地扫描缓存，不写回数据源目录。
                  </p>
                </div>
                <span class="text-xs text-white/34">{{ artworkSearchResults.length }} 张</span>
              </div>

              <div v-if="artworkSearchResults.length" class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <button
                  v-for="image in artworkSearchResults"
                  :key="`${image.kind}-${image.filePath}`"
                  type="button"
                  class="overflow-hidden rounded-2xl border border-white/8 bg-white/5 text-left transition-colors hover:border-primary/50 hover:bg-white/9 disabled:cursor-wait disabled:opacity-60"
                  :disabled="isArtworkApplying"
                  @click="applyArtworkCandidate(image)"
                >
                  <div :class="image.kind === 'backdrop' || image.kind === 'logo' ? 'aspect-video' : 'aspect-[2/3]'" class="bg-black/28">
                    <img :src="image.imageUrl" :alt="artworkKindLabel(image.kind)" class="h-full w-full object-contain" loading="lazy" decoding="async">
                  </div>
                  <div class="p-3 text-xs text-white/48">
                    <p>{{ image.language || '无语言' }}</p>
                    <p v-if="image.width && image.height" class="mt-1">
                      {{ image.width }} × {{ image.height }}
                    </p>
                  </div>
                </button>
              </div>
              <p v-else-if="!isArtworkSearching" class="mt-4 rounded-xl border border-white/8 bg-white/5 px-4 py-5 text-sm text-white/42">
                暂无候选。请先完成识别或填写有效 TheMovieDb 标识符后再搜索。
              </p>
            </div>
          </section>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.source-view {
  background: var(--color-bg);
}

.identification-dialog {
  background: var(--chrome-surface);
  box-shadow: var(--chrome-shadow);
}

.first-index-panel {
  background:
    linear-gradient(135deg, color-mix(in srgb, var(--color-primary) 12%, transparent), transparent 48%),
    color-mix(in srgb, var(--color-surface) 66%, transparent);
  box-shadow:
    inset 0 1px 0 rgb(255 255 255 / 10%),
    0 18px 44px rgb(0 0 0 / 18%);
}

.first-index-spinner {
  position: relative;
  width: 3rem;
  height: 3rem;
  flex: 0 0 auto;
  border-radius: 999px;
  border: 1px solid var(--color-border);
  background: var(--surface-soft);
}

.first-index-spinner span {
  position: absolute;
  inset: 0.45rem;
  border-radius: 999px;
  border: 2px solid var(--color-border-hover);
  border-top-color: var(--color-primary);
}

.first-index-spinner.is-active span {
  animation: raw-index-spin 0.9s linear infinite;
}

.raw-index-progress {
  position: relative;
  height: 0.35rem;
  overflow: hidden;
  border-radius: 999px;
  background: var(--surface-soft);
}

.raw-index-progress span {
  position: absolute;
  inset-block: 0;
  left: 0;
  width: 42%;
  border-radius: inherit;
  background: linear-gradient(90deg, transparent, var(--color-primary), color-mix(in srgb, var(--color-text) 82%, transparent));
  opacity: 0.45;
}

.raw-index-progress.is-active span {
  opacity: 1;
  animation: raw-index-progress 1.35s ease-in-out infinite;
}

.scan-management-panel {
  background:
    linear-gradient(135deg, color-mix(in srgb, var(--color-primary) 8%, transparent), transparent 46%),
    color-mix(in srgb, var(--color-surface) 62%, transparent);
}

.scan-stat {
  border: 1px solid color-mix(in srgb, var(--color-border) 76%, transparent);
  border-radius: 1rem;
  background: var(--surface-soft);
  padding: 0.8rem 1rem;
}

.scan-stat p {
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}

.scan-stat strong {
  display: block;
  margin-top: 0.25rem;
  color: var(--color-text);
  font-size: 1rem;
  font-weight: 700;
}

.scan-stat span {
  display: block;
  margin-top: 0.15rem;
  color: var(--color-text-tertiary);
  font-size: 0.72rem;
}

@keyframes raw-index-spin {
  to {
    transform: rotate(360deg);
  }
}

@keyframes raw-index-progress {
  0% {
    transform: translateX(-110%);
  }

  100% {
    transform: translateX(250%);
  }
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .source-page-content {
    padding-top: max(4.4rem, calc(env(safe-area-inset-top) + 3.4rem));
  }

  .source-page-content > template + *,
  .source-page-content {
    row-gap: 1.35rem;
  }

  .source-mobile-search {
    width: 100%;
    min-width: 0;
  }

  .source-mobile-search input,
  .source-mobile-search button {
    min-height: 2.9rem;
    border-radius: 8px;
  }

  .scan-management-panel,
  .first-index-panel,
  .scan-stat {
    border-radius: 8px;
  }

  .scan-management-panel,
  .first-index-panel {
    padding: 1rem;
  }

  .scan-management-header,
  .first-index-header {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 1rem;
  }

  .scan-management-copy,
  .first-index-copy {
    width: 100%;
    min-width: 0;
  }

  .scan-management-copy p,
  .first-index-copy h3,
  .first-index-copy p {
    max-width: none;
    word-break: normal;
    overflow-wrap: break-word;
  }

  .first-index-summary {
    display: grid;
    grid-template-columns: 2.65rem minmax(0, 1fr);
    width: 100%;
    gap: 0.8rem;
  }

  .first-index-spinner {
    width: 2.65rem;
    height: 2.65rem;
  }

  .scan-management-actions,
  .first-index-actions {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    width: 100%;
    gap: 0.65rem;
  }

  .scan-management-actions button,
  .first-index-actions button {
    width: 100%;
    min-width: 0;
    min-height: 2.8rem;
    border-radius: 8px;
    padding: 0.7rem 0.75rem;
    white-space: normal;
  }

  .scan-management-stats,
  .first-index-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .first-index-stats .scan-stat:first-child {
    grid-column: 1 / -1;
  }

  .scan-stat {
    min-width: 0;
    padding: 0.72rem 0.8rem;
  }

  .scan-stat strong,
  .scan-stat span {
    overflow-wrap: anywhere;
  }

  .scan-management-details {
    grid-template-columns: minmax(0, 1fr);
  }

  .scan-management-details > div,
  .first-index-log {
    border-radius: 8px;
    padding: 0.85rem;
  }

  .scan-management-details > div > div,
  .first-index-log > div {
    max-height: 15rem;
    overflow-y: auto;
    overscroll-behavior: contain;
    padding-right: 0.2rem;
  }
}
</style>
