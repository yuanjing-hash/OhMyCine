<script setup lang="ts">
import type { ServerAcquisitionStatus, ServerCoverageSummary, ServerDiscoveryDetail, ServerDiscoveryTypePolicy, ServerFollowDefaults, ServerFollowRoutePreview, ServerLibraryOption, ServerResourceItem, ServerResourceSearchInput, ServerRouteRecommendation, ServerSearchSite } from '@/services/serverDiscovery'
import type { ServerShareSelection } from '@/services/serverSharePreview'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MediaDetailHero from '@/components/media/MediaDetailHero.vue'
import DiscoveryAcquisitionDialog from '@/components/server/DiscoveryAcquisitionDialog.vue'
import DiscoverySearchWorkspace from '@/components/server/DiscoverySearchWorkspace.vue'
import DiscoverySitePickerDialog from '@/components/server/DiscoverySitePickerDialog.vue'
import { ServerDataSource } from '@/services/datasource/server'
import { registerLayoutBackHandler } from '@/services/layoutBackNavigation'
import { publishFeedback } from '@/services/mediaActions'
import { getServerAcquisition, getServerCoverage, getServerDiscoveryDetail, getServerFollowDefaults, getServerFollowRoutePreview, getServerResourceLink, getServerRouteRecommendation, getServerSearchSites, streamServerResources } from '@/services/serverDiscovery'
import { beginResourceSearch, emptyResourceSearch, finishResourceSearch, reduceResourceSearch, resourceGroupRetryPage, ResourceSearchController } from '@/services/serverDiscoverySearch'
import { previewServerShare } from '@/services/serverSharePreview'
import { useAcquisitionWorkspaceStore } from '@/stores/acquisitionWorkspace'
import { useDataSourceStore } from '@/stores/datasource'
import { useSearchWorkspaceStore } from '@/stores/searchWorkspace'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const globalSearch = useSearchWorkspaceStore()
const acquisitionWorkspace = useAcquisitionWorkspaceStore()
const detail = ref<ServerDiscoveryDetail | null>(null)
const coverage = ref<ServerCoverageSummary | null>(null)
const acquisition = ref<ServerAcquisitionStatus | null>(null)
const sites = ref<ServerSearchSite[]>([])
const selectedSiteIds = ref<number[]>([])
const searchState = ref(emptyResourceSearch())
const activeSiteId = ref<number>()
const libraries = ref<ServerLibraryOption[]>([])
const routeRecommendation = ref<ServerRouteRecommendation | null>(null)
const routeError = ref('')
const loading = ref(true)
const loadingSites = ref(false)
const loadingTargets = ref(false)
const submitting = ref(false)
const busyToken = ref('')
const error = ref('')
const capabilities = ref(new Set<string>())
const discoveryTypePolicy = ref<'default' | ServerDiscoveryTypePolicy>('default')
const discoveryCloudTypes = ref<string[]>(['115', 'quark', 'magnet'])
const copyingToken = ref('')
let activeSearchScope: number[] = []
let copyGeneration = 0
const sitePickerOpen = ref(false)
const resultWorkspaceOpen = ref(false)
const acquisitionDialogOpen = ref(false)
const selectedResource = ref<ServerResourceItem | null>(null)
const followOpen = ref(false)
const followLoading = ref(false)
const subscribing = ref(false)
const followDefaults = ref<ServerFollowDefaults | null>(null)
const followRoutePreview = ref<ServerFollowRoutePreview | null>(null)
const followRouteLoading = ref(false)
const followRouteError = ref('')
const followForm = ref({ seasons: [] as number[], siteIds: [] as number[], libraryId: 0, minutes: 360, maxResources: 3, priority: 0 })
const pendingSearchMode = ref<'aggregate' | 'direct'>('aggregate')
const directKind = ref<'title' | 'tmdb'>('title')
const directQuery = ref('')
const searchController = new ResourceSearchController()
const backOwner = Symbol('server-discovery-detail')
let unregisterBack: (() => void) | undefined
let detailGeneration = 0
let siteGeneration = 0
let routeGeneration = 0
let followRouteGeneration = 0
let followGeneration = 0
let statusGeneration = 0
let statusTimer: number | undefined
let submittedSearchInput: ServerResourceSearchInput | undefined

const sourceId = computed(() => String(route.params.sourceId))
const provider = computed(() => route.params.provider === 'douban' ? 'douban' : 'tmdb')
const mediaType = computed(() => route.params.mediaType === 'tv' ? 'tv' : 'movie')
const providerId = computed(() => String(route.params.providerId))
const searchableSites = computed(() => sites.value.filter(site => site.searchable))
const selectedAll = computed(() => searchableSites.value.length > 0 && searchableSites.value.every(site => selectedSiteIds.value.includes(site.id)))
const canSearch = computed(() => capabilities.value.has('discovery_search'))
const canAcquire = computed(() => capabilities.value.has('acquisition_create'))
const canSubscribe = computed(() => capabilities.value.has('subscription_create'))
const mixedSupported = computed(() => capabilities.value.has('mixed_discovery_v1'))
const followRoutesAvailable = computed(() => !followRouteLoading.value && !followRouteError.value
  && followRoutePreview.value?.available === true
  && followRoutePreview.value.routes.length === followForm.value.siteIds.length
  && followForm.value.siteIds.every(id => followRoutePreview.value?.routes.filter(item => item.siteId === id && item.recommended?.libraryId === followForm.value.libraryId).length === 1))
const followBlockedReason = computed(() => {
  if (!canSubscribe.value)
    return '当前账号没有创建订阅的权限。'
  if (followLoading.value)
    return '正在读取订阅选项，请稍候。'
  const defaults = followDefaults.value
  if (!defaults?.seasons.length)
    return 'Server 尚未返回可订阅季，请稍后重新打开订阅。'
  if (!followForm.value.seasons.length)
    return '请选择至少一季后再确认订阅。'
  if (followForm.value.seasons.some(season => !defaults.seasons.some(item => item.seasonNumber === season) || defaults.subscribedSeasons.includes(season)))
    return '所选季已订阅或已不可用，请重新选择。'
  if (!followForm.value.siteIds.length)
    return '请选择至少一个搜索站点。'
  if (!defaults.mediaLibraries.some(item => item.id === followForm.value.libraryId))
    return '请选择目标媒体库。'
  if (!Number.isInteger(followForm.value.minutes) || followForm.value.minutes < 10 || followForm.value.minutes > 10080)
    return '检查间隔须为 10 至 10080 分钟的整数。'
  if (!Number.isInteger(followForm.value.maxResources) || followForm.value.maxResources < 1 || followForm.value.maxResources > 20)
    return '每次下载资源数须为 1 至 20 的整数。'
  if (followRouteLoading.value)
    return '正在检查下载路由，请稍候。'
  if (followRouteError.value)
    return followRouteError.value
  if (!followRoutesAvailable.value)
    return '部分站点没有可用的下载路由，请调整站点或目标媒体库。'
  return ''
})
const coverageLabel = computed(() => {
  if (!coverage.value)
    return '媒体库覆盖未知'
  if (mediaType.value === 'movie')
    return coverage.value.present ? '已入库' : '尚未入库'
  return `已入库 ${coverage.value.present} / ${coverage.value.total} 集 · 缺失 ${coverage.value.missing} 集`
})
const acquisitionLabel = computed(() => describeAcquisition(acquisition.value))
const acquisitionProgress = computed(() => {
  const current = acquisition.value
  if (current?.totalFiles)
    return Math.min(100, Math.round(current.processedFiles / current.totalFiles * 100))
  return current?.progress == null ? undefined : Math.min(100, Math.max(0, Math.round(current.progress)))
})

async function resolveSource(id = sourceId.value): Promise<ServerDataSource> {
  store.loadConfigs()
  await store.syncManager()
  const config = store.orderedConfigs.find(item => item.id === id && item.type === 'server' && item.enabled !== false)
  const source = store.getSource(id)
  if (!config || !(source instanceof ServerDataSource))
    throw new Error('对应 Server 尚未连接，请先在设置中登录。')
  return source
}

async function load() {
  const generation = ++detailGeneration
  const origin = { id: sourceId.value, provider: provider.value, mediaType: mediaType.value, item: providerId.value, route: route.fullPath } as const
  let source: ServerDataSource | undefined
  const current = () => generation === detailGeneration && origin.route === route.fullPath && origin.id === sourceId.value && (!source || store.getSource(origin.id) === source)
  stopStatusPolling()
  siteGeneration++
  loadingSites.value = false
  sitePickerOpen.value = false
  resultWorkspaceOpen.value = false
  sites.value = []
  selectedSiteIds.value = []
  activeSearchScope = []
  loading.value = true
  error.value = ''
  detail.value = null
  coverage.value = null
  acquisition.value = null
  capabilities.value = new Set()
  searchState.value = emptyResourceSearch()
  submittedSearchInput = undefined
  activeSiteId.value = undefined
  try {
    source = await resolveSource(origin.id)
    if (!current())
      return
    const nextCapabilities = new Set(await source.refreshCapabilities())
    if (!current())
      return
    const nextDetail = await getServerDiscoveryDetail(source, origin.provider, origin.mediaType, origin.item)
    if (!current())
      return
    capabilities.value = nextCapabilities
    detail.value = nextDetail
    directQuery.value = nextDetail.work.title
    if (nextDetail.work.tmdbId) {
      const tmdbId = nextDetail.work.tmdbId
      const [coverageResult, acquisitionResult] = await Promise.all([
        getServerCoverage(source, origin.mediaType, tmdbId).catch(() => null),
        getServerAcquisition(source, origin.mediaType, tmdbId).catch(() => null),
      ])
      if (!current())
        return
      coverage.value = coverageResult
      acquisition.value = acquisitionResult
      scheduleStatusRefresh()
    }
  }
  catch (reason) {
    if (current())
      error.value = message(reason)
  }
  finally {
    if (current())
      loading.value = false
  }
}

async function chooseSearch(mode: 'aggregate' | 'direct') {
  if (!canSearch.value) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '当前 Server 账号没有影视搜索权限。' })
    return
  }
  pendingSearchMode.value = mode
  sitePickerOpen.value = true
  if (loadingSites.value)
    return
  const generation = ++siteGeneration
  const origin = sourceId.value
  const ownerRoute = route.fullPath
  let source: ServerDataSource | undefined
  const current = () => generation === siteGeneration && origin === sourceId.value && ownerRoute === route.fullPath && (!source || store.getSource(origin) === source)
  loadingSites.value = true
  try {
    source = await resolveSource(origin)
    if (!current())
      return
    const result = await getServerSearchSites(source)
    if (!current())
      return
    const first = sites.value.length === 0
    sites.value = result
    const available = result.filter(site => site.searchable).map(site => site.id)
    selectedSiteIds.value = first ? available : selectedSiteIds.value.filter(id => available.includes(id))
    searchState.value.groups = searchState.value.groups.map(group => ({ ...group, unavailableReason: result.find(site => site.id === group.siteId)?.searchable ? undefined : result.find(site => site.id === group.siteId)?.reason || '来源已停用或不再可访问，历史结果保留。' }))
  }
  catch (reason) {
    if (current())
      publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
  finally {
    if (current())
      loadingSites.value = false
  }
}

function toggleAllSites() {
  selectedSiteIds.value = selectedAll.value ? [] : searchableSites.value.map(site => site.id)
}

function toggleSite(id: number) {
  selectedSiteIds.value = selectedSiteIds.value.includes(id) ? selectedSiteIds.value.filter(value => value !== id) : [...selectedSiteIds.value, id]
}

async function runSearch(siteIds = selectedSiteIds.value, replaceAll = true, page = 1, intent: 'search' | 'page' = 'search') {
  if (!detail.value || siteIds.length === 0)
    return
  if (replaceAll && discoveryTypePolicy.value === 'manual' && !discoveryCloudTypes.value.length) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '请至少选择一种盘搜结果类型。' })
    return
  }
  const tmdbId = detail.value.work.tmdbId
  const useTMDB = pendingSearchMode.value === 'direct' && directKind.value === 'tmdb'
  const draftInput: ServerResourceSearchInput = {
    mediaType: mediaType.value,
    tmdbId: pendingSearchMode.value === 'aggregate' || useTMDB ? tmdbId : undefined,
    title: pendingSearchMode.value === 'direct' && !useTMDB ? directQuery.value : undefined,
    direct: pendingSearchMode.value === 'direct',
    siteIds: [...siteIds],
    page,
    typePolicy: discoveryTypePolicy.value === 'default' ? undefined : discoveryTypePolicy.value,
    cloudTypes: discoveryTypePolicy.value === 'manual' ? [...discoveryCloudTypes.value] : undefined,
  }
  const input: ServerResourceSearchInput = {
    ...(!replaceAll && submittedSearchInput ? submittedSearchInput : draftInput),
    siteIds: [...siteIds],
    page,
    snapshotId: intent === 'page' && siteIds.length === 1 ? searchState.value.groups.find(group => group.siteId === siteIds[0])?.snapshotId : undefined,
    refresh: intent === 'search' && page === 1,
  }
  if (!input.direct && !input.tmdbId) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '该作品还没有可验证的 TMDB ID，请使用直接搜索。' })
    return
  }
  if (replaceAll) {
    activeSearchScope = [...siteIds]
    submittedSearchInput = { ...input, siteIds: [...input.siteIds] }
  }
  await searchController.run(async (onEvent, isCurrent) => {
    const source = await resolveSource()
    if (!isCurrent())
      return
    return streamServerResources(source, input, onEvent)
  }, {
    start: () => {
      error.value = ''
      searchState.value = beginResourceSearch(searchState.value, { sites: sites.value, siteIds: input.siteIds, replaceAll, page })
      if (replaceAll)
        activeSiteId.value = undefined
      sitePickerOpen.value = false
      resultWorkspaceOpen.value = true
    },
    event: (event) => {
      searchState.value = reduceResourceSearch(searchState.value, event, sites.value)
      if (event.type === 'site') {
        if (activeSiteId.value == null && searchState.value.groups.some(group => group.siteId === event.group.siteId && group.items.length > 0))
          activeSiteId.value = event.group.siteId
        return
      }
      if (event.type === 'error') {
        error.value = event.message
        publishFeedback({ id: Date.now(), kind: 'error', message: event.message })
      }
    },
    complete: () => {
      // Valid done/error events already finalized the reducer. A rejected or stale
      // terminal envelope must not be turned into a successful empty search here.
      searchState.value = finishResourceSearch(searchState.value, 'failed', '未收到有效的搜索完成确认，已保留收到的结果。')
    },
    error: (reason) => {
      error.value = message(reason)
      searchState.value = finishResourceSearch(searchState.value, 'failed', error.value)
      publishFeedback({ id: Date.now(), kind: 'error', message: error.value })
    },
  })
}

async function cancelSearch() {
  searchState.value = finishResourceSearch(searchState.value, 'cancelled')
  await searchController.cancel()
}

async function retrySite(siteId: number) {
  const group = searchState.value.groups.find(group => group.siteId === siteId)
  await runSearch([siteId], false, group?.snapshotId ? 1 : resourceGroupRetryPage(searchState.value, siteId))
}

async function changeSitePage(siteId: number, page: number) {
  activeSiteId.value = siteId
  await runSearch([siteId], false, page, 'page')
}

async function openAcquisition(item: ServerResourceItem) {
  const group = searchState.value.groups.find(group => group.items.some(value => value.token === item.token))
  if (item.acquisitionAvailable === false || group?.unavailableReason) {
    publishFeedback({ id: Date.now(), kind: 'error', message: group?.unavailableReason || item.acquisitionReason || '当前类型只支持查看链接，尚不支持自动入库。' })
    return
  }
  if (!canAcquire.value) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '当前 Server 账号没有下载入库权限。' })
    return
  }
  selectedResource.value = item
  routeRecommendation.value = null
  routeError.value = ''
  libraries.value = []
  acquisitionDialogOpen.value = true
  await loadRouteRecommendation()
}

async function copyResourceLink(item: ServerResourceItem) {
  const generation = ++copyGeneration
  const origin = sourceId.value
  copyingToken.value = item.token
  try {
    const source = await resolveSource()
    const url = await getServerResourceLink(source, item)
    if (generation !== copyGeneration || origin !== sourceId.value || store.getSource(origin) !== source)
      return
    await navigator.clipboard.writeText(url)
    if (generation === copyGeneration && origin === sourceId.value)
      publishFeedback({ id: Date.now(), kind: 'success', message: '资源链接已复制。' })
  }
  catch (reason) {
    if (generation === copyGeneration && origin === sourceId.value)
      publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
  finally {
    if (generation === copyGeneration)
      copyingToken.value = ''
  }
}

function refreshResourceTypes() {
  void runSearch(activeSearchScope.length ? activeSearchScope : selectedSiteIds.value)
}

async function loadRouteRecommendation(libraryId?: number) {
  const resource = selectedResource.value
  if (!resource)
    return
  const generation = ++routeGeneration
  loadingTargets.value = true
  routeError.value = ''
  routeRecommendation.value = null
  try {
    const recommendation = await getServerRouteRecommendation(await resolveSource(), resource.token, libraryId)
    if (generation !== routeGeneration)
      return
    routeRecommendation.value = recommendation
    if (!libraryId) {
      const byId = new Map<number, ServerLibraryOption>()
      for (const choice of recommendation.choices)
        byId.set(choice.libraryId, { id: choice.libraryId, name: choice.libraryName })
      libraries.value = [...byId.values()]
    }
  }
  catch (reason) {
    if (generation === routeGeneration)
      routeError.value = message(reason)
  }
  finally {
    if (generation === routeGeneration)
      loadingTargets.value = false
  }
}

async function loadAcquisitionSharePreview(downloaderId: string) {
  const resource = selectedResource.value
  const origin = sourceId.value
  const generation = routeGeneration
  if (!resource || !acquisitionDialogOpen.value)
    throw new Error('入库资源已变化，请重新打开。')
  const source = await resolveSource()
  const current = () => origin === sourceId.value && generation === routeGeneration && selectedResource.value?.token === resource.token && acquisitionDialogOpen.value && store.getSource(origin) === source
  if (!current())
    throw new Error('入库资源或 Server 已变化，请重新打开。')
  const preview = await previewServerShare(source, resource.token, downloaderId)
  if (!current())
    throw new Error('入库资源或 Server 已变化，请重新打开。')
  return preview
}

async function submitAcquisition(options: { downloaderId: string, libraryId: number, shareSelection?: ServerShareSelection }) {
  const resource = selectedResource.value
  const work = detail.value?.work
  if (submitting.value || !resource || !work?.tmdbId)
    return
  const origin = sourceId.value
  const generation = routeGeneration
  const submittedMediaType = mediaType.value
  const current = () => origin === sourceId.value && generation === routeGeneration && selectedResource.value?.token === resource.token && acquisitionDialogOpen.value
  if ((routeRecommendation.value?.sourceKind === 'quark_share' || routeRecommendation.value?.sourceKind === '115_share') && !options.shareSelection) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '请先预览分享并选择需要转存的文件。' })
    return
  }
  submitting.value = true
  busyToken.value = resource.token
  try {
    const source = await resolveSource()
    if (!current() || store.getSource(origin) !== source)
      return
    await source.createDiscoveryDownload({
      result_token: resource.token,
      downloader_id: options.downloaderId,
      media_library_id: options.libraryId,
      priority: 0,
      expected_tmdb_id: work.tmdbId,
      expected_media_type: submittedMediaType,
      ...(options.shareSelection ? { preview_token: options.shareSelection.previewToken, selected_entry_tokens: options.shareSelection.entryTokens } : {}),
    })
    if (!current() || store.getSource(origin) !== source)
      return
    const result = await getServerAcquisition(source, submittedMediaType, work.tmdbId)
    if (!current() || store.getSource(origin) !== source)
      return
    acquisition.value = result
    acquisitionDialogOpen.value = false
    scheduleStatusRefresh(true)
    publishFeedback({ id: Date.now(), kind: 'success', message: `《${work.title}》已进入 Server 入库流程，可从右侧“入库任务”持续查看。` })
  }
  catch (reason) {
    if (current())
      publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
  finally {
    submitting.value = false
    busyToken.value = ''
  }
}

function scheduleStatusRefresh(immediate = false) {
  stopStatusPolling(false)
  if (!acquisition.value || isTerminalAcquisition(acquisition.value))
    return
  const generation = statusGeneration
  statusTimer = window.setTimeout(async () => {
    if (generation !== statusGeneration || !detail.value?.work.tmdbId)
      return
    try {
      acquisition.value = await getServerAcquisition(await resolveSource(), mediaType.value, detail.value.work.tmdbId)
    }
    finally {
      if (generation === statusGeneration)
        scheduleStatusRefresh()
    }
  }, immediate ? 200 : 2200)
}

function stopStatusPolling(invalidate = true) {
  if (invalidate)
    statusGeneration++
  if (statusTimer != null) {
    window.clearTimeout(statusTimer)
    statusTimer = undefined
  }
}

function isTerminalAcquisition(value: ServerAcquisitionStatus) {
  return ['completed', 'failed', 'cancelled', 'canceled'].includes(value.status) || value.stage === 'library'
}

function describeAcquisition(value: ServerAcquisitionStatus | null) {
  if (!value || value.stage === 'idle')
    return '尚未入库'
  const labels: Record<string, string> = { subscription: '已订阅', download: '下载中', organize: '整理中', transfer: '传输中', import: '刮削入库中', library: '已入库' }
  if (value.status === 'queued')
    return '等待入库'
  if (value.status === 'failed')
    return '入库需要处理'
  return labels[value.stage] ?? value.stage
}

async function openFollow() {
  if (!canSubscribe.value) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '当前 Server 账号没有创建订阅的权限。' })
    return
  }
  const tmdbId = detail.value?.work.tmdbId
  if (!tmdbId)
    return
  const generation = ++followGeneration
  const origin = sourceId.value
  const ownerRoute = route.fullPath
  let source: ServerDataSource | undefined
  const current = () => generation === followGeneration && followOpen.value && origin === sourceId.value && ownerRoute === route.fullPath && (!source || store.getSource(origin) === source)
  followOpen.value = true
  followLoading.value = true
  followDefaults.value = null
  followRoutePreview.value = null
  followRouteError.value = ''
  try {
    source = await resolveSource(origin)
    if (!current())
      return
    const defaults = await getServerFollowDefaults(source, tmdbId)
    if (!current())
      return
    followDefaults.value = defaults
    const ordinary = defaults.seasons.filter(item => item.seasonNumber > 0 && !defaults.subscribedSeasons.includes(item.seasonNumber))
    const selected = defaults.snapshot.seasons.filter(season => ordinary.some(item => item.seasonNumber === season))
    const preferred = ordinary.find(item => item.missing > 0) ?? ordinary[0]
    const seasons = selected.length ? selected : preferred ? [preferred.seasonNumber] : []
    followForm.value = { seasons, siteIds: [...defaults.snapshot.site_ids], libraryId: defaults.snapshot.media_library_id, minutes: defaults.snapshot.schedule.minutes, maxResources: defaults.snapshot.max_resources_per_run, priority: defaults.snapshot.download_priority }
  }
  catch (reason) {
    if (current()) {
      publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
      followOpen.value = false
    }
  }
  finally {
    if (current())
      followLoading.value = false
  }
}

async function refreshFollowRoutePreview() {
  const generation = ++followRouteGeneration
  const owner = followGeneration
  const origin = sourceId.value
  const ownerRoute = route.fullPath
  let source: ServerDataSource | undefined
  const current = () => generation === followRouteGeneration && owner === followGeneration && followOpen.value && origin === sourceId.value && ownerRoute === route.fullPath && (!source || store.getSource(origin) === source)
  const siteIds = [...followForm.value.siteIds]
  const libraryId = followForm.value.libraryId
  if (!followOpen.value || !followDefaults.value || !siteIds.length || !libraryId) {
    followRoutePreview.value = null
    followRouteError.value = ''
    followRouteLoading.value = false
    return
  }
  followRouteLoading.value = true
  followRouteError.value = ''
  followRoutePreview.value = null
  try {
    source = await resolveSource(origin)
    if (!current())
      return
    const preview = await getServerFollowRoutePreview(source, siteIds, libraryId)
    if (current())
      followRoutePreview.value = preview
  }
  catch (reason) {
    if (current())
      followRouteError.value = message(reason)
  }
  finally {
    if (current())
      followRouteLoading.value = false
  }
}

watch(() => [followOpen.value, followDefaults.value, followForm.value.siteIds.join(','), followForm.value.libraryId], () => {
  void refreshFollowRoutePreview()
})

watch(followOpen, (open) => {
  if (!open) {
    followGeneration++
    followRouteGeneration++
    followLoading.value = false
    followRouteLoading.value = false
    subscribing.value = false
  }
}, { flush: 'sync' })

function toggleFollowSite(id: number) {
  followForm.value.siteIds = followForm.value.siteIds.includes(id) ? followForm.value.siteIds.filter(value => value !== id) : [...followForm.value.siteIds, id]
}

function toggleSeason(season: number) {
  if (!followDefaults.value?.seasons.some(item => item.seasonNumber === season) || followDefaults.value.subscribedSeasons.includes(season))
    return
  followForm.value.seasons = followForm.value.seasons.includes(season) ? followForm.value.seasons.filter(value => value !== season) : [...followForm.value.seasons, season].sort((a, b) => a - b)
}

async function createFollow() {
  const work = detail.value?.work
  if (!followOpen.value || subscribing.value || !work?.tmdbId || !followDefaults.value || followBlockedReason.value)
    return
  const owner = followGeneration
  const origin = sourceId.value
  const ownerRoute = route.fullPath
  let source: ServerDataSource | undefined
  const current = () => owner === followGeneration && followOpen.value && origin === sourceId.value && ownerRoute === route.fullPath && (!source || store.getSource(origin) === source)
  const base = followDefaults.value.snapshot
  const payload = { tmdb_id: work.tmdbId, title: work.title, year: work.year, snapshot: { ...base, seasons: [...followForm.value.seasons], site_ids: [...followForm.value.siteIds], media_library_id: followForm.value.libraryId, schedule: { kind: 'interval', minutes: followForm.value.minutes }, max_resources_per_run: followForm.value.maxResources, download_priority: followForm.value.priority } }
  subscribing.value = true
  try {
    source = await resolveSource(origin)
    if (!current())
      return
    await source.createDiscoveryFollow(payload)
    if (!current())
      return
    const nextAcquisition = await getServerAcquisition(source, 'tv', work.tmdbId)
    if (!current())
      return
    acquisition.value = nextAcquisition
    followOpen.value = false
    scheduleStatusRefresh(true)
    publishFeedback({ id: Date.now(), kind: 'success', message: `《${work.title}》已订阅，Server 将自动搜索缺失剧集并入库。` })
  }
  catch (reason) {
    if (current())
      publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
  finally {
    if (current())
      subscribing.value = false
  }
}

async function handleLayoutBack() {
  if (acquisitionDialogOpen.value) {
    acquisitionDialogOpen.value = false
    return true
  }
  if (sitePickerOpen.value) {
    sitePickerOpen.value = false
    return true
  }
  if (resultWorkspaceOpen.value) {
    resultWorkspaceOpen.value = false
    return true
  }
  if (followOpen.value) {
    followOpen.value = false
    return true
  }
  const origin = typeof route.query.origin === 'string' ? route.query.origin : ''
  const returnTo = safeReturnPath(route.query.return_to)
  await router.replace(returnTo)
  await nextTick()
  if (origin === 'global-search')
    globalSearch.show()
  else if (origin === 'acquisitions')
    acquisitionWorkspace.show()
  return true
}

function safeReturnPath(value: unknown): string {
  if (typeof value !== 'string' || value.length > 2048 || !value.startsWith('/') || value.startsWith('//'))
    return '/'
  return value
}

function message(reason: unknown) {
  return reason instanceof Error ? reason.message : '操作失败'
}

onMounted(() => {
  unregisterBack = registerLayoutBackHandler(backOwner, handleLayoutBack)
  void load()
})
watch(() => route.fullPath, async () => {
  detailGeneration++
  siteGeneration++
  copyGeneration++
  routeGeneration++
  acquisitionDialogOpen.value = false
  followOpen.value = false
  await cancelSearch()
  await load()
})
onBeforeUnmount(() => {
  detailGeneration++
  siteGeneration++
  copyGeneration++
  routeGeneration++
  acquisitionDialogOpen.value = false
  followGeneration++
  followRouteGeneration++
  unregisterBack?.()
  stopStatusPolling()
  void cancelSearch()
})
</script>

<template>
  <section class="min-h-full pb-16">
    <div v-if="loading" class="grid min-h-[70vh] place-items-center text-sm text-white/48">
      正在通过 Server 读取 TMDB 详情…
    </div>
    <div v-else-if="error && !detail" class="mx-auto mt-28 max-w-xl rounded-3xl border border-red-400/20 bg-red-400/8 p-7 text-red-100">
      <h2 class="text-lg font-bold">
        无法打开影视详情
      </h2><p class="mt-2 text-sm">
        {{ error }}
      </p><button class="glass-button mt-4 rounded-full px-4 py-2" @click="load">
        重试
      </button>
    </div>
    <template v-else-if="detail">
      <MediaDetailHero :source-id="sourceId" :title="detail.work.title" :original-title="detail.work.originalTitle" :poster-url="detail.work.posterUrl" :backdrop-url="detail.work.backdropUrl" :overview="detail.work.overview || '暂无简介。'" eyebrow="OhMyCine Server Discovery">
        <template #meta>
          <span class="rounded-full bg-white/8 px-3 py-1">{{ mediaType === 'tv' ? '电视剧' : '电影' }}</span>
          <span v-if="detail.work.rating" class="rounded-full bg-yellow-400/16 px-3 py-1 text-yellow-100">★ {{ detail.work.rating.toFixed(1) }}</span>
          <span v-if="detail.work.year">{{ detail.work.year }}</span><span v-if="detail.runtimeMinutes">{{ detail.runtimeMinutes }} 分钟</span><span v-if="detail.genres.length">{{ detail.genres.slice(0, 4).join(' / ') }}</span>
          <span class="rounded-full bg-white/8 px-3 py-1">{{ coverageLabel }}</span><span class="rounded-full bg-white/8 px-3 py-1">{{ acquisitionLabel }}</span><span v-if="detail.work.tmdbId" class="rounded-full bg-white/8 px-3 py-1">TMDB {{ detail.work.tmdbId }}</span>
        </template>
        <template #actions>
          <button class="rounded-full bg-white px-7 py-3 text-sm font-bold text-black shadow-xl transition-transform hover:scale-105 disabled:opacity-50" type="button" :disabled="!canSearch" @click="chooseSearch('aggregate')">
            搜索
          </button>
          <button class="rounded-full border border-white/16 bg-white/10 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/16 disabled:opacity-50" type="button" :disabled="!canSearch" @click="chooseSearch('direct')">
            直接搜索
          </button>
          <button v-if="mediaType === 'tv'" class="rounded-full border border-white/16 bg-white/10 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/16 disabled:opacity-50" type="button" :disabled="!canSubscribe || Boolean(acquisition?.followSubscriptionId)" @click="openFollow">
            {{ acquisition?.followSubscriptionId ? '已订阅' : '订阅' }}
          </button>
          <button v-if="acquisition && acquisition.stage !== 'idle'" class="rounded-full border border-white/16 bg-white/10 px-5 py-3 text-sm font-semibold text-white hover:bg-white/16" type="button" @click="acquisitionWorkspace.show()">
            查看入库任务
          </button>
        </template>
      </MediaDetailHero>

      <section v-if="acquisition && acquisition.stage !== 'idle'" class="acquisition-strip glass-panel mx-auto mt-5 flex w-[min(72rem,calc(100%-3rem))] flex-wrap items-center gap-4 rounded-3xl px-5 py-4">
        <div class="status-orb" :class="{ failed: acquisition.status === 'failed', completed: isTerminalAcquisition(acquisition) && acquisition.status !== 'failed' }" />
        <div class="min-w-0 flex-1">
          <p class="text-xs font-bold tracking-[.12em] text-white/38 uppercase">
            当前入库状态
          </p><h2 class="mt-1 text-sm font-semibold text-white/88">
            {{ acquisitionLabel }}
          </h2><p class="mt-1 text-xs text-white/40">
            {{ acquisition.totalFiles ? `${acquisition.processedFiles} / ${acquisition.totalFiles} 个文件` : acquisition.progress != null ? `${Math.round(acquisition.progress)}%` : acquisition.status }}<template v-if="acquisition.lastErrorCode">
              · {{ acquisition.lastErrorCode }}
            </template>
          </p>
        </div>
        <div v-if="acquisitionProgress != null" class="h-1.5 w-40 overflow-hidden rounded-full bg-white/8">
          <div class="h-full rounded-full bg-white/80" :style="{ width: `${acquisitionProgress}%` }" />
        </div>
      </section>

      <DiscoverySitePickerDialog :open="sitePickerOpen" :sites="sites" :selected-site-ids="selectedSiteIds" :loading="loadingSites" :mode="pendingSearchMode" :direct-kind="directKind" :direct-query="directQuery" :tmdb-id="detail.work.tmdbId" @close="sitePickerOpen = false" @search="runSearch()" @toggle-site="toggleSite" @toggle-all="toggleAllSites" @update-direct-kind="directKind = $event" @update-direct-query="directQuery = $event" />
      <DiscoverySearchWorkspace :open="resultWorkspaceOpen" :title="detail.work.title" :state="searchState" :active-site-id="activeSiteId" :can-acquire="canAcquire" :busy-token="busyToken" :mixed-supported="mixedSupported" :type-policy="discoveryTypePolicy" :cloud-types="discoveryCloudTypes" :copying-token="copyingToken" @close="resultWorkspaceOpen = false" @cancel="cancelSearch" @retry="retrySite" @select-site="activeSiteId = $event" @select-resource="openAcquisition" @page="changeSitePage" @copy-resource="copyResourceLink" @update-type-policy="discoveryTypePolicy = $event" @update-cloud-types="discoveryCloudTypes = $event" @refresh="refreshResourceTypes" />
      <DiscoveryAcquisitionDialog :key="sourceId" :open="acquisitionDialogOpen" :resource="selectedResource" :libraries="libraries" :recommendation="routeRecommendation" :route-error="routeError" :loading="loadingTargets" :submitting="submitting" :load-share-preview="loadAcquisitionSharePreview" @close="acquisitionDialogOpen = false" @select-library="loadRouteRecommendation($event)" @retry="loadRouteRecommendation($event)" @confirm="submitAcquisition" />

      <div v-if="followOpen" class="follow-layer fixed inset-0 z-[1210] grid place-items-center p-4" @click.self="!subscribing && (followOpen = false)">
        <section class="follow-card glass-panel max-h-[88vh] w-full max-w-3xl overflow-y-auto rounded-[28px] p-6 cinema-scrollbar">
          <header class="flex justify-between gap-4">
            <div>
              <p class="text-[11px] font-bold tracking-[.2em] text-white/35 uppercase">
                自动追更
              </p><h2 class="mt-1 text-xl font-bold text-white">
                订阅《{{ detail.work.title }}》
              </h2><p class="mt-1 text-sm text-white/45">
                配置会冻结到订阅，以后自动找缺失、下载并入库。
              </p>
            </div><button class="glass-button h-10 rounded-full px-4" :disabled="subscribing" @click="followOpen = false">
              取消
            </button>
          </header>
          <div v-if="followLoading" class="py-14 text-center text-white/45">
            正在读取 Server 订阅选项…
          </div>
          <div v-else-if="followDefaults" class="mt-6 space-y-5">
            <div>
              <label class="text-xs text-white/48">订阅季</label>
              <p class="mt-1 text-xs text-white/45">
                已选 {{ followForm.seasons.length }} 季。可调整下方选择；特别篇须单独勾选，已订阅的季不可重复添加。
              </p>
              <div class="mt-2 flex flex-wrap gap-2">
                <button v-for="season in followDefaults.seasons" :key="season.seasonNumber" type="button" class="glass-button rounded-full px-3 py-2 text-sm disabled:opacity-45" :aria-pressed="followForm.seasons.includes(season.seasonNumber)" :disabled="followDefaults.subscribedSeasons.includes(season.seasonNumber)" :class="{ 'bg-white text-black': followForm.seasons.includes(season.seasonNumber) }" @click="toggleSeason(season.seasonNumber)">
                  {{ season.name }}<span v-if="followDefaults.subscribedSeasons.includes(season.seasonNumber)"> · 已订阅</span>
                </button>
              </div>
            </div>
            <div>
              <div class="flex items-center justify-between">
                <label class="text-xs text-white/48">搜索站点</label><button type="button" class="text-xs text-white/60" @click="followForm.siteIds = followForm.siteIds.length === followDefaults.sites.length ? [] : followDefaults.sites.map(item => item.id)">
                  {{ followForm.siteIds.length === followDefaults.sites.length ? '取消全选' : '快速全选' }}
                </button>
              </div><div class="mt-2 grid gap-2 sm:grid-cols-2">
                <button v-for="site in followDefaults.sites" :key="site.id" type="button" class="rounded-2xl border p-3 text-left text-sm" :class="followForm.siteIds.includes(site.id) ? 'border-white/45 bg-white/10' : 'border-white/8 bg-white/[.025]'" @click="toggleFollowSite(site.id)">
                  {{ site.name }}<span class="float-right">{{ followForm.siteIds.includes(site.id) ? '✓' : '' }}</span>
                </button>
              </div>
            </div>
            <div class="grid gap-3 md:grid-cols-2">
              <label class="text-xs text-white/48">目标媒体库<select v-model="followForm.libraryId" class="follow-input mt-2 w-full rounded-2xl p-3 text-white"><option :value="0" disabled>请选择媒体库</option><option v-for="item in followDefaults.mediaLibraries" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">检查间隔（分钟）<input v-model.number="followForm.minutes" class="follow-input mt-2 w-full rounded-2xl p-3 text-white" type="number" min="10" max="10080"></label><label class="text-xs text-white/48">每次最多下载资源数<input v-model.number="followForm.maxResources" class="follow-input mt-2 w-full rounded-2xl p-3 text-white" type="number" min="1" max="20"></label>
            </div>
            <div class="rounded-2xl border border-white/10 bg-white/[.035] p-4 text-sm">
              <p class="font-semibold text-white/88">
                按资源来源自动选择下载器
              </p>
              <p class="mt-1 text-xs leading-5 text-white/48">
                115 和夸克分享分别使用对应网盘的转存，PT 使用排序第一的 qBittorrent，公开 BT 按下载器顺序选择。Server 会针对每条结果和目标媒体库再次验证。
              </p>
              <p v-if="followRouteLoading" class="mt-3 text-xs text-white/55">
                正在预览站点路由…
              </p>
              <p v-else-if="followRouteError" class="mt-3 text-xs text-red-200">
                {{ followRouteError }}
              </p>
              <div v-else-if="followRoutePreview" class="mt-3 space-y-2">
                <div v-for="item in followRoutePreview.routes" :key="item.siteId" class="flex justify-between gap-4 rounded-xl bg-white/[.035] px-3 py-2 text-xs">
                  <span class="text-white/70">{{ item.siteName }}</span>
                  <span :class="item.recommended ? 'text-emerald-200' : 'text-amber-200'">{{ item.recommended ? followDefaults.downloaders.find(downloader => downloader.id === item.recommended?.downloaderId)?.name || '推荐下载器' : item.reasonMessage || '当前目标库不可用' }}</span>
                </div>
              </div>
              <p v-else class="mt-3 text-xs text-white/45">
                请选择搜索站点和目标媒体库。
              </p>
            </div>
            <footer class="flex flex-wrap items-center justify-end gap-3">
              <p v-if="followBlockedReason" id="follow-submit-status" class="min-w-0 flex-1 text-sm text-amber-200" role="status">
                {{ followBlockedReason }}
              </p>
              <button class="rounded-full bg-white px-6 py-2.5 font-bold text-black disabled:opacity-50" :disabled="subscribing || Boolean(followBlockedReason)" :aria-describedby="followBlockedReason ? 'follow-submit-status' : undefined" @click="createFollow">
                {{ subscribing ? '正在创建…' : '确认订阅' }}
              </button>
            </footer>
          </div>
        </section>
      </div>
    </template>
  </section>
</template>

<style scoped>
.acquisition-strip{border:1px solid var(--chrome-border);background:color-mix(in srgb,var(--chrome-surface-translucent) 88%,transparent)}.status-orb{width:.7rem;height:.7rem;border-radius:50%;background:var(--color-primary);box-shadow:0 0 0 6px color-mix(in srgb,var(--color-primary) 14%,transparent)}.status-orb.completed{background:#5fd39a;box-shadow:0 0 0 6px rgba(95,211,154,.12)}.status-orb.failed{background:#ff7187;box-shadow:0 0 0 6px rgba(255,113,135,.12)}.follow-layer{background:color-mix(in srgb,var(--color-bg) 72%,transparent);backdrop-filter:blur(18px)}.follow-card{border:1px solid var(--chrome-border);background:color-mix(in srgb,var(--chrome-surface-translucent) 96%,transparent);box-shadow:var(--chrome-shadow),inset 0 1px rgba(255,255,255,.1)}.follow-input{border:1px solid var(--control-border);background:var(--control-bg)}select option{background:#11151d;color:white}
</style>
