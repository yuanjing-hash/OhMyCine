<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import DownloadRouteTargetPicker from '@/components/DownloadRouteTargetPicker.vue'
import { previewDownloadRoutes, routeTargetByID, type DownloadRoutePreview } from '@/download-routes'
import { formatBytes } from '@/downloads'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import { buildDiscoveryMediaSearchPath, discoveryDetailRoute, mediaIdentitySearchURL, type DiscoveryMediaSearch, type DiscoveryMediaSearchFilter, type DiscoveryMediaType, type DiscoverySearchName, type DiscoveryWork } from '@/discovery'
import { discoveryDownloadsPath, discoverySearchOptionsPath, filterAndSortTorrentResults, ptRecognitionEpisodeLabel, ptRecognitionErrorLabel, ptRecognitionSpecLabels, readTorrentSearchSession, readTorrentSearchSiteSelection, saveTorrentSearchSession, saveTorrentSearchSiteSelection, sitesPath, torrentRecognitionCandidatesPath, torrentRecognitionOverridePath, torrentRecognitionPath, torrentSearchPath, torrentSearchStreamPath, torrentSearchURL, upsertTorrentGroup, type PTRecognitionCandidate, type SearchSiteOption, type SiteSummary, type TorrentRecognitionResult, type TorrentResultDirection, type TorrentSearchGroup, type TorrentSearchResponse, type TorrentSearchResult, type TorrentSearchSession } from '@/sites'
import type { DownloaderSummary, ListResponse, MediaLibraryDetail } from '@/types/api'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const mode = ref<'media' | 'resources'>(route.query.mode === 'resources' || typeof route.query.site_id === 'string' ? 'resources' : 'media')
const mediaQuery = ref(typeof route.query.query === 'string' ? route.query.query : '')
const mediaFilter = ref<DiscoveryMediaSearchFilter>(route.query.media_type === 'movie' || route.query.media_type === 'tv' ? route.query.media_type : 'all')
const mediaResults = ref<DiscoveryWork[]>([])
const mediaPage = ref(1)
const mediaTotalPages = ref(1)
const mediaSearching = ref(false)
const mediaSearched = ref(false)
const mediaError = ref('')
const keyword = ref(typeof route.query.title === 'string' ? route.query.title : '')
const mediaType = ref(typeof route.query.media_type === 'string' ? route.query.media_type : '')
const year = ref<number | undefined>(typeof route.query.year === 'string' ? Number(route.query.year) || undefined : undefined)
const tmdbID = ref<number | undefined>(typeof route.query.tmdb_id === 'string' ? Number(route.query.tmdb_id) || undefined : undefined)
const searchBy = ref<'title' | 'tmdb_id'>(route.query.search_by === 'tmdb_id' ? 'tmdb_id' : 'title')
const identityNames = ref<DiscoverySearchName[]>([])
const selectedTitle = computed(() => typeof route.query.title === 'string' ? route.query.title : '')
const lockedSiteID = computed(() => {
  const value = typeof route.query.site_id === 'string' ? Number(route.query.site_id) : 0
  return Number.isInteger(value) && value > 0 ? value : undefined
})
const lockedSite = ref<SiteSummary | null>(null)
const groups = ref<TorrentSearchGroup[]>([])
const searching = ref(false)
const searchError = ref('')
const searched = ref(false)
const siteSelectorOpen = ref(false)
const siteOptionsLoading = ref(false)
const siteOptionsError = ref('')
const siteOptions = ref<SearchSiteOption[]>([])
const selectedSiteIDs = ref<number[]>([])
const activeSearchSiteIDs = ref<number[]>([])
const downloaders = ref<DownloaderSummary[]>([])
const libraries = ref<MediaLibraryDetail[]>([])
const downloadDialog = ref<TorrentSearchResult | null>(null)
const downloadForm = ref({ downloaderID: '', mediaLibraryID: 0, priority: 0 })
const downloadSiteID = ref<number | undefined>()
const routePreview = ref<DownloadRoutePreview | null>(null)
const routePreviewLoading = ref(false)
let routePreviewRequest: AbortController | null = null
const submitting = ref(false)
const recognitions = ref<Record<string, TorrentRecognitionResult>>({})
const recognitionErrors = ref<Record<string, string>>({})
const recognizingTokens = ref<string[]>([])
const manualDialog = ref<TorrentSearchResult | null>(null)
const manualForm = ref<{ keyword: string; mediaType: '' | 'movie' | 'tv'; year?: number }>({ keyword: '', mediaType: '' })
const manualCandidates = ref<PTRecognitionCandidate[]>([])
const selectedManualCandidate = ref<PTRecognitionCandidate | null>(null)
const manualSearching = ref(false)
const manualSaving = ref(false)
let source: EventSource | null = null
let streamTimeout: number | undefined
let mediaSearchRequest: AbortController | null = null

const enabledDownloaders = computed(() => downloaders.value.filter(item => item.enabled))
const selectedRoute = computed(() => routeTargetByID(routePreview.value, downloadForm.value.mediaLibraryID))
const selectedLibrary = computed(() => libraries.value.find(item => item.id === selectedRoute.value?.media_library_id) ?? null)
const selectableSiteOptions = computed(() => siteOptions.value.filter(item => item.searchable))
const activeChannel = ref<'all' | number>('all')
const enabledSiteTypes = ref<Array<'pt' | 'bt'>>(['pt', 'bt'])
const resolutionFilter = ref('')
const promotionFilter = ref('')
const minimumSeeders = ref<number | undefined>()
const resultSort = ref<'seeders' | 'published' | 'size'>('seeders')
const resultDirection = ref<TorrentResultDirection>('desc')

const orderedGroups = computed(() => [...groups.value].sort((left, right) => left.site_id - right.site_id))
const activeGroup = computed(() => typeof activeChannel.value === 'number' ? groups.value.find(group => group.site_id === activeChannel.value) ?? null : null)
const resolutionOptions = computed(() => [...new Set(groups.value.flatMap(group => group.items.map(item => item.specifications?.resolution || item.quality || '').filter(Boolean)))].sort())
const visibleResults = computed(() => filterAndSortTorrentResults(groups.value, {
  activeChannel: activeChannel.value,
  enabledSiteTypes: enabledSiteTypes.value,
  resolution: resolutionFilter.value,
  promotion: promotionFilter.value,
  minimumSeeders: minimumSeeders.value,
  sort: resultSort.value,
  direction: resultDirection.value,
}))
const trustedIdentity = computed(() => mode.value === 'resources' && route.query.identity === 'tmdb' && (mediaType.value === 'movie' || mediaType.value === 'tv') && tmdbID.value != null && tmdbID.value > 0
  ? { mediaType: mediaType.value as DiscoveryMediaType, tmdbID: tmdbID.value }
  : null)

async function searchMedia(page = 1) {
  const query = mediaQuery.value.trim()
  if (!query) { notify('请输入电影或剧集名称', 'warning'); return }
  mediaSearchRequest?.abort()
  const controller = new AbortController()
  mediaSearchRequest = controller
  mediaSearching.value = true
  mediaError.value = ''
  try {
    const result = await api<DiscoveryMediaSearch>(buildDiscoveryMediaSearchPath(query, mediaFilter.value, page), { signal: controller.signal })
    mediaResults.value = result.items
    mediaPage.value = result.page
    mediaTotalPages.value = result.total_pages
    mediaSearched.value = true
    await router.replace({ path: '/discovery/explore', query: { query, media_type: mediaFilter.value === 'all' ? undefined : mediaFilter.value } })
  } catch (reason) {
    if (!controller.signal.aborted) mediaError.value = message(reason)
  } finally {
    if (mediaSearchRequest === controller) {
      mediaSearchRequest = null
      mediaSearching.value = false
    }
  }
}

function openMedia(work: DiscoveryWork) { void router.push(discoveryDetailRoute(work)) }
function switchMode(value: 'media' | 'resources') {
  if (value === 'media') {
    stopStream()
    searching.value = false
  } else {
    mediaSearchRequest?.abort()
    mediaSearchRequest = null
    mediaSearching.value = false
  }
  mode.value = value
  if (value === 'media') void router.replace({ path: '/discovery/explore' })
  else void router.replace({ path: '/discovery/explore', query: { mode: 'resources' } })
}

function searchInput(siteID?: number, page = 1) {
  const fixedSiteID = lockedSiteID.value ?? siteID
  return { keyword: keyword.value, mediaType: mediaType.value || undefined, year: year.value, tmdbID: tmdbID.value, searchBy: searchBy.value, page, siteID: fixedSiteID, siteIDs: fixedSiteID ? undefined : activeSearchSiteIDs.value }
}

function stopStream() {
  source?.close()
  source = null
  if (streamTimeout !== undefined) window.clearTimeout(streamTimeout)
  streamTimeout = undefined
}

async function searchJSON(siteID?: number, page = 1) {
  try {
    const identity = trustedIdentity.value
    const path = identity
      ? mediaIdentitySearchURL(identity.mediaType, identity.tmdbID, { page, siteID: lockedSiteID.value ?? siteID, siteIDs: lockedSiteID.value || siteID ? undefined : activeSearchSiteIDs.value })
      : torrentSearchURL(torrentSearchPath, searchInput(siteID, page))
    const response = await api<TorrentSearchResponse & { query_names?: DiscoverySearchName[] }>(path)
    identityNames.value = response.query_names ?? identityNames.value
    for (const group of response.groups) groups.value = upsertTorrentGroup(groups.value, group)
    searched.value = true
  } catch (reason) { searchError.value = message(reason) }
  finally { searching.value = false }
}

async function openSiteSelector() {
  siteSelectorOpen.value = true
  siteOptionsLoading.value = true
  siteOptionsError.value = ''
  try {
    const response = await api<ListResponse<SearchSiteOption>>(discoverySearchOptionsPath)
    siteOptions.value = response.list
    selectedSiteIDs.value = readTorrentSearchSiteSelection(typeof localStorage === 'undefined' ? undefined : localStorage, response.list)
  } catch (reason) {
    siteOptions.value = []
    selectedSiteIDs.value = []
    siteOptionsError.value = message(reason)
  } finally { siteOptionsLoading.value = false }
}

function selectAllSites() { selectedSiteIDs.value = selectableSiteOptions.value.map(item => item.id) }
function clearSelectedSites() { selectedSiteIDs.value = [] }

function confirmSiteSelection() {
  const allowed = new Set(selectableSiteOptions.value.map(item => item.id))
  const selected = [...new Set(selectedSiteIDs.value.filter(id => allowed.has(id)))]
  if (selected.length === 0) { notify('请至少选择一个可搜索站点', 'warning'); return }
  saveTorrentSearchSiteSelection(typeof localStorage === 'undefined' ? undefined : localStorage, selected)
  siteSelectorOpen.value = false
  executeSearch(selected)
}

function search() {
  if (!trustedIdentity.value && searchBy.value === 'title' && !keyword.value.trim()) { notify('请输入作品或发行标题', 'warning'); return }
  if (!trustedIdentity.value && searchBy.value === 'tmdb_id' && (!tmdbID.value || !mediaType.value)) { notify('TMDB ID 搜索需要有效 ID 与媒体类型', 'warning'); return }
  if (!lockedSiteID.value) { void openSiteSelector(); return }
  executeSearch([lockedSiteID.value])
}

function executeSearch(siteIDs: number[]) {
  activeSearchSiteIDs.value = [...siteIDs]
  stopStream()
  groups.value = []
  activeChannel.value = 'all'
  recognitions.value = {}
  recognitionErrors.value = {}
  identityNames.value = []
  searchError.value = ''
  searched.value = false
  searching.value = true
  if (lockedSiteID.value) {
    activeChannel.value = lockedSiteID.value
    void searchJSON(lockedSiteID.value)
    return
  }
  if (typeof EventSource === 'undefined') { void searchJSON(); return }
  let delivered = false
  const identity = trustedIdentity.value
  const eventSource = new EventSource(identity
    ? mediaIdentitySearchURL(identity.mediaType, identity.tmdbID, { siteID: lockedSiteID.value, siteIDs: lockedSiteID.value ? undefined : activeSearchSiteIDs.value }, true)
    : torrentSearchURL(torrentSearchStreamPath, searchInput()))
  source = eventSource
  eventSource.addEventListener('site', event => {
    try {
      const group = JSON.parse((event as MessageEvent<string>).data) as TorrentSearchGroup
      groups.value = upsertTorrentGroup(groups.value, group)
      delivered = true
      searched.value = true
    } catch { /* malformed events are ignored; JSON fallback remains available */ }
  })
  eventSource.addEventListener('media', event => {
    try { identityNames.value = (JSON.parse((event as MessageEvent<string>).data) as { query_names?: DiscoverySearchName[] }).query_names ?? [] }
    catch { /* safe metadata is optional */ }
  })
  eventSource.addEventListener('done', () => {
    stopStream()
    searching.value = false
    searched.value = true
  })
  eventSource.onerror = () => {
    stopStream()
    if (!delivered) void searchJSON()
    else { searching.value = false; notify('部分站点流式结果已返回；连接提前结束，可单独重试失败站点', 'warning') }
  }
  streamTimeout = window.setTimeout(() => {
    stopStream()
    if (!delivered) void searchJSON()
    else searching.value = false
  }, identity ? 120_000 : 30_000)
}

async function retrySite(group: TorrentSearchGroup) {
  searching.value = true
  searchError.value = ''
  await searchJSON(group.site_id, 1)
}

async function nextPage(group: TorrentSearchGroup) {
  searching.value = true
  await searchJSON(group.site_id, group.page + 1)
}

async function previousPage(group: TorrentSearchGroup) {
  if (group.page <= 1) return
  searching.value = true
  await searchJSON(group.site_id, group.page - 1)
}

async function loadDownloadOptions() {
  const requests: Promise<void>[] = []
  if (auth.can(Permissions.DownloadersRead)) requests.push(api<ListResponse<DownloaderSummary>>('/api/v1/downloaders').then(response => { downloaders.value = response.list }))
  if (auth.can(Permissions.MediaLibrariesRead)) requests.push(api<ListResponse<MediaLibraryDetail>>('/api/v1/media-libraries').then(response => { libraries.value = response.list }))
  await Promise.all(requests)
  if (!enabledDownloaders.value.some(item => item.id === downloadForm.value.downloaderID)) downloadForm.value.downloaderID = enabledDownloaders.value[0]?.id ?? ''
}

async function openDownload(item: TorrentSearchResult) {
  downloadDialog.value = item
  downloadSiteID.value = groups.value.find(group => group.items.some(candidate => candidate.token === item.token))?.site_id
  downloadForm.value = { downloaderID: '', mediaLibraryID: 0, priority: 0 }
  try { await loadDownloadOptions(); await loadRoutePreview() }
  catch (reason) { notify(message(reason), 'error') }
}

async function loadRoutePreview() {
  routePreviewRequest?.abort()
  routePreviewRequest = null
  routePreview.value = null
  downloadForm.value.mediaLibraryID = 0
  if (!downloadForm.value.downloaderID || !downloadDialog.value) return
  const controller = new AbortController()
  routePreviewRequest = controller
  routePreviewLoading.value = true
  try {
    const preview = await previewDownloadRoutes({
      downloader_id: downloadForm.value.downloaderID,
      source_kind: 'torrent',
      site_id: downloadSiteID.value,
      expected_bytes: downloadDialog.value.size_bytes ?? undefined,
    }, controller.signal)
    if (!controller.signal.aborted) routePreview.value = preview
  } catch (reason) {
    if (!controller.signal.aborted) notify(message(reason), 'error')
  } finally {
    if (routePreviewRequest === controller) { routePreviewRequest = null; routePreviewLoading.value = false }
  }
}

watch(() => downloadForm.value.downloaderID, () => void loadRoutePreview())

async function recognizeResult(item: TorrentSearchResult) {
  if (recognizingTokens.value.includes(item.token)) return
  recognizingTokens.value = [...recognizingTokens.value, item.token]
  const errors = { ...recognitionErrors.value }
  delete errors[item.token]
  recognitionErrors.value = errors
  try {
    const result = await api<TorrentRecognitionResult>(torrentRecognitionPath, { method: 'POST', body: JSON.stringify({ result_token: item.token }) })
    recognitions.value = { ...recognitions.value, [item.token]: result }
  } catch (reason) {
    recognitionErrors.value = { ...recognitionErrors.value, [item.token]: message(reason) }
  } finally {
    recognizingTokens.value = recognizingTokens.value.filter(token => token !== item.token)
  }
}

async function submitDownload() {
  const item = downloadDialog.value
  if (!item || !downloadForm.value.downloaderID) { notify('请选择已启用的下载器', 'warning'); return }
  if (!selectedRoute.value?.enabled || !selectedLibrary.value) { notify('请选择一条 Server 已确认可执行的入库路线', 'warning'); return }
  submitting.value = true
  try {
    const result = await api<{ id: string }>(discoveryDownloadsPath, { method: 'POST', body: JSON.stringify({ result_token: item.token, downloader_id: downloadForm.value.downloaderID, media_library_id: downloadForm.value.mediaLibraryID, priority: downloadForm.value.priority }) })
    notify(`下载任务已进入统一队列：${result.id}`, 'success')
    downloadDialog.value = null
  } catch (reason) { notify(message(reason), 'error') }
  finally { submitting.value = false }
}

function formatTime(value?: string) { return value ? new Date(value).toLocaleString() : '未知' }
function count(value?: number) { return value == null ? '—' : String(value) }
function mediaTypeLabel(value?: string) { return value === 'movie' ? '电影' : value === 'tv' ? '剧集' : '类型待定' }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '种子搜索暂时不可用' }

function currentSearchInput() {
  return { keyword: keyword.value, mediaType: mediaType.value, year: year.value, tmdbID: tmdbID.value, searchBy: searchBy.value, siteID: lockedSiteID.value, siteIDs: lockedSiteID.value || activeSearchSiteIDs.value.length === 0 ? undefined : activeSearchSiteIDs.value }
}

async function openManualRecognition(item: TorrentSearchResult) {
  const recognized = recognitions.value[item.token]
  manualDialog.value = item
  manualForm.value = {
    keyword: recognized?.title?.trim() || item.title,
    mediaType: recognized?.media_type || '',
    year: recognized?.year,
  }
  manualCandidates.value = []
  selectedManualCandidate.value = null
  await searchManualCandidates()
}

async function searchManualCandidates() {
  const item = manualDialog.value
  if (!item || !manualForm.value.keyword.trim()) { notify('请输入用于 TMDB 搜索的作品名', 'warning'); return }
  manualSearching.value = true
  selectedManualCandidate.value = null
  try {
    const result = await api<ListResponse<PTRecognitionCandidate>>(torrentRecognitionCandidatesPath, {
      method: 'POST',
      body: JSON.stringify({ result_token: item.token, title: manualForm.value.keyword, media_type: manualForm.value.mediaType, year: manualForm.value.year || undefined }),
    })
    manualCandidates.value = result.list
  } catch (reason) {
    manualCandidates.value = []
    notify(message(reason), 'error')
  } finally { manualSearching.value = false }
}

async function confirmManualRecognition() {
  const item = manualDialog.value
  const candidate = selectedManualCandidate.value
  if (!item || !candidate) { notify('请先选择一个 TMDB 作品', 'warning'); return }
  manualSaving.value = true
  try {
    const result = await api<TorrentRecognitionResult>(torrentRecognitionOverridePath, {
      method: 'PUT',
      body: JSON.stringify({ result_token: item.token, tmdb_id: candidate.id, media_type: candidate.media_type }),
    })
    recognitions.value = { ...recognitions.value, [item.token]: result }
    manualDialog.value = null
    notify(`已确认：${result.title}，创建下载任务时将沿用此身份`, 'success')
  } catch (reason) { notify(message(reason), 'error') }
  finally { manualSaving.value = false }
}

function sameSearchInput(left: ReturnType<typeof currentSearchInput>, right: TorrentSearchSession['input']) {
  const sameScope = !left.siteIDs?.length || JSON.stringify(left.siteIDs) === JSON.stringify(right.siteIDs)
  return left.keyword.trim() === right.keyword.trim() && left.mediaType === right.mediaType && left.year === right.year && left.tmdbID === right.tmdbID && left.searchBy === right.searchBy && left.siteID === right.siteID && sameScope
}

watch([groups, recognitions, searched], () => {
  saveTorrentSearchSession(typeof sessionStorage === 'undefined' ? undefined : sessionStorage, {
    input: currentSearchInput(), groups: groups.value, recognitions: recognitions.value, searched: searched.value, savedAt: Date.now(),
  })
}, { deep: true })

onMounted(async () => {
  if (mode.value === 'media') {
    if (mediaQuery.value.trim()) await searchMedia()
    return
  }
  if (lockedSiteID.value && auth.can(Permissions.SystemAdmin)) {
    try {
      const response = await api<ListResponse<SiteSummary>>(sitesPath)
      lockedSite.value = response.list.find(item => item.id === lockedSiteID.value) ?? null
    } catch { /* Search API remains the authority if the management summary cannot load. */ }
  }
  const cached = readTorrentSearchSession(typeof sessionStorage === 'undefined' ? undefined : sessionStorage)
  const hasRouteSearch = typeof route.query.title === 'string' || typeof route.query.tmdb_id === 'string'
  if (cached && (!hasRouteSearch || sameSearchInput(currentSearchInput(), cached.input))) {
    keyword.value = cached.input.keyword
    mediaType.value = cached.input.mediaType
    year.value = cached.input.year
    tmdbID.value = cached.input.tmdbID
    searchBy.value = cached.input.searchBy
    activeSearchSiteIDs.value = cached.input.siteIDs ?? []
    groups.value = cached.groups
    recognitions.value = cached.recognitions
    searched.value = cached.searched
    if (lockedSiteID.value) activeChannel.value = lockedSiteID.value
    return
  }
  if (trustedIdentity.value || keyword.value.trim() || (searchBy.value === 'tmdb_id' && tmdbID.value)) search()
})
onBeforeUnmount(() => {
  stopStream()
  mediaSearchRequest?.abort()
  routePreviewRequest?.abort()
})
</script>

<template>
  <section class="space-y-5">
    <header><p class="text-xs font-700 uppercase tracking-widest text-[var(--text-subtle)]">Search</p><h1 v-if="mode === 'media'" class="mt-1 text-2xl font-800">搜索</h1><h1 v-else class="mt-1 text-2xl font-800">直接搜索</h1><p class="page-description mt-1">{{ mode === 'media' ? '先从 TMDB 海报确认作品，再进入统一详情、媒体库覆盖率和多语言资源聚合。' : lockedSiteID ? '当前处于单站搜索模式；请求、重试和翻页不会回退到其他站点。' : '按原始关键词、标题或 TMDB ID 直接查询 PT/BT 资源。' }}</p></header>
    <nav class="management-tabs overflow-x-auto" role="tablist" aria-label="搜索模式"><button class="management-tab shrink-0" :class="mode === 'media' ? 'management-tab--active' : ''" type="button" role="tab" :aria-selected="mode === 'media'" @click="switchMode('media')">搜索</button><button class="management-tab shrink-0" :class="mode === 'resources' ? 'management-tab--active' : ''" type="button" role="tab" :aria-selected="mode === 'resources'" @click="switchMode('resources')">直接搜索</button></nav>

    <template v-if="mode === 'media'">
      <form class="panel" @submit.prevent="searchMedia(1)"><div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_10rem_auto] md:items-end"><div><label class="label" for="media-search-query">电影或剧集名称</label><input id="media-search-query" v-model="mediaQuery" class="input" maxlength="256" placeholder="三体 / Three-Body" required /></div><div><label class="label" for="media-search-type">媒体类型</label><select id="media-search-type" v-model="mediaFilter" class="input"><option value="all">电影 + 剧集</option><option value="movie">电影</option><option value="tv">剧集</option></select></div><button class="btn-primary" :disabled="mediaSearching">{{ mediaSearching ? '搜索中…' : '搜索' }}</button></div></form>
      <div v-if="mediaError" class="semantic-error p-4"><strong>影视搜索失败</strong><p class="mt-1 text-sm">{{ mediaError }}</p><button class="btn-secondary mt-3" @click="searchMedia(mediaPage)">重试</button></div>
      <div v-if="mediaSearching" class="panel py-10 text-center text-muted">正在通过 TMDB 搜索电影和剧集海报…</div>
      <div v-else-if="mediaSearched && !mediaResults.length && !mediaError" class="panel py-10 text-center text-muted">没有找到匹配作品。可以尝试原名、英文名，或切换到直接搜索。</div>
      <div v-if="mediaResults.length" class="grid gap-4 sm:grid-cols-2 md:grid-cols-3 xl:grid-cols-5"><button v-for="work in mediaResults" :key="`${work.media_type}:${work.tmdb_id}`" class="discovery-poster text-left" @click="openMedia(work)"><div class="discovery-poster__image"><img v-if="work.poster_url" :src="work.poster_url" :alt="`${work.title} 海报`" loading="lazy" /><span v-else>暂无海报</span></div><strong :title="work.title">{{ work.title }}</strong><small>{{ work.media_type === 'tv' ? '剧集' : '电影' }} · {{ work.year || '年份未知' }}<template v-if="work.rating != null"> · {{ work.rating.toFixed(1) }}</template></small><small v-if="work.original_title && work.original_title !== work.title" :title="work.original_title">{{ work.original_title }}</small></button></div>
      <footer v-if="mediaResults.length" class="panel flex items-center justify-center gap-3"><button class="btn-secondary" :disabled="mediaSearching || mediaPage <= 1" @click="searchMedia(mediaPage - 1)">上一页</button><span class="text-sm">第 {{ mediaPage }} / {{ mediaTotalPages }} 页</span><button class="btn-secondary" :disabled="mediaSearching || mediaPage >= mediaTotalPages" @click="searchMedia(mediaPage + 1)">下一页</button></footer>
    </template>

    <template v-else>
      <div v-if="lockedSiteID" class="semantic-inset flex flex-wrap items-center justify-between gap-3 p-4"><div><span class="status-chip">仅搜索此站</span><strong class="ml-2">{{ lockedSite?.name || `站点 #${lockedSiteID}` }}</strong></div><RouterLink class="btn-secondary" to="/system/sites">返回站点管理</RouterLink></div>
      <form class="panel" @submit.prevent="search">
        <div class="mb-4 flex flex-wrap gap-2" role="group" aria-label="搜索方式"><button type="button" class="btn-secondary" :class="{ '!border-[var(--accent)] !bg-[var(--accent-soft)] !text-[var(--accent)]': searchBy === 'title' }" @click="searchBy = 'title'">按标题</button><button type="button" class="btn-secondary" :class="{ '!border-[var(--accent)] !bg-[var(--accent-soft)] !text-[var(--accent)]': searchBy === 'tmdb_id' }" @click="searchBy = 'tmdb_id'">按 TMDB ID</button></div>
        <div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_10rem_8rem_auto] md:items-end">
          <div v-if="searchBy === 'title'"><label class="label" for="discovery-keyword">作品或发行标题</label><input id="discovery-keyword" v-model="keyword" class="input" maxlength="160" placeholder="七武士 / Seven Samurai" required /></div>
          <div v-else><label class="label" for="discovery-tmdb">TMDB ID</label><input id="discovery-tmdb" v-model.number="tmdbID" class="input font-mono" type="number" min="1" placeholder="346" required /></div>
          <div><label class="label" for="discovery-kind">媒体类型</label><select id="discovery-kind" v-model="mediaType" class="input"><option value="">自动</option><option value="movie">电影</option><option value="tv">剧集</option></select></div>
          <div><label class="label" for="discovery-year">年份</label><input id="discovery-year" v-model.number="year" class="input" type="number" min="1880" max="2200" placeholder="可选" /></div>
          <button class="btn-primary" :disabled="searching">{{ searching ? '搜索中…' : '搜索' }}</button>
        </div>
      </form>
      <div v-if="selectedTitle" class="panel"><span class="status-chip">已确认作品身份</span><h2 class="mt-3 text-xl font-750">{{ selectedTitle }}</h2><p class="mt-1 text-sm text-muted">Server 会重新验证 TMDB 身份，并按受限的中文名、地区别名、原名和英文名聚合搜索。</p><div v-if="identityNames.length" class="mt-3 flex flex-wrap gap-2"><span v-for="name in identityNames" :key="`${name.kind}:${name.locale}:${name.value}`" class="status-chip">{{ name.value }}<template v-if="name.locale"> · {{ name.locale }}</template></span></div></div>
      <div v-if="searchError" class="semantic-error p-4"><strong>搜索失败</strong><p class="mt-1 text-sm">{{ searchError }}</p><button class="btn-secondary mt-3" @click="search">{{ lockedSiteID ? '重试此站' : '重新选择站点' }}</button></div>
      <div v-if="searching && !groups.length" class="panel py-10 text-center text-muted">正在按站点限速并行搜索，结果会渐进出现…</div>
      <div v-else-if="searched && !groups.length && !searchError" class="panel py-10 text-center text-muted">没有启用的 PT/BT 站点，或当前关键词暂无结果。可以先到“站点管理”添加站点。</div>

      <template v-if="groups.length">
        <nav v-if="!lockedSiteID" class="management-tabs overflow-x-auto" role="tablist" aria-label="搜索渠道">
          <button class="management-tab shrink-0" :class="activeChannel === 'all' ? 'management-tab--active' : ''" type="button" role="tab" :aria-selected="activeChannel === 'all'" @click="activeChannel = 'all'">全部渠道</button>
          <button v-for="group in orderedGroups" :key="group.site_id" class="management-tab shrink-0" :class="activeChannel === group.site_id ? 'management-tab--active' : ''" type="button" role="tab" :aria-selected="activeChannel === group.site_id" @click="activeChannel = group.site_id">{{ group.site_name }} <small>{{ group.site_type.toUpperCase() }}</small></button>
        </nav>

        <form class="panel grid gap-3 md:grid-cols-2 xl:grid-cols-[auto_auto_11rem_11rem_9rem_10rem_9rem_auto] xl:items-end" @submit.prevent>
          <fieldset class="flex gap-3"><legend class="label">站点类型</legend><label class="text-sm"><input v-model="enabledSiteTypes" type="checkbox" value="pt" /> PT</label><label class="text-sm"><input v-model="enabledSiteTypes" type="checkbox" value="bt" /> BT</label></fieldset>
          <label><span class="label">分辨率</span><select v-model="resolutionFilter" class="input"><option value="">全部</option><option v-for="value in resolutionOptions" :key="value" :value="value">{{ value }}</option></select></label>
          <label><span class="label">优惠</span><select v-model="promotionFilter" class="input"><option value="">全部</option><option value="free">FREE</option><option value="2xfree">2X FREE</option><option value="2x">2X</option></select></label>
          <label><span class="label">最低做种</span><input v-model.number="minimumSeeders" class="input" type="number" min="0" placeholder="不限" /></label>
          <label><span class="label">排序字段</span><select v-model="resultSort" class="input"><option value="seeders">做种数</option><option value="published">发布时间</option><option value="size">体积</option></select></label>
          <label><span class="label">排序方向</span><select v-model="resultDirection" class="input"><option value="desc">降序</option><option value="asc">升序</option></select></label>
          <p class="text-subtle mb-0 text-xs xl:col-span-2">不同筛选项之间为 AND；同类标签按任一匹配。排序只针对已经取回的当前页结果。</p>
        </form>

        <div v-if="activeGroup?.status === 'error'" class="semantic-warning flex flex-wrap items-center justify-between gap-3 p-4"><span>{{ activeGroup.site_name }} 暂时不可用（{{ activeGroup.error_code || 'site_unavailable' }}）。</span><button class="btn-secondary" :disabled="searching" @click="retrySite(activeGroup)">重试此站</button></div>
        <div v-if="!visibleResults.length" class="panel py-10 text-center text-muted">当前渠道和筛选条件下没有结果。</div>
        <div v-else class="grid gap-4 xl:grid-cols-2">
          <article v-for="entry in visibleResults" :key="entry.item.token" class="panel flex min-h-72 flex-col">
            <div class="flex min-w-0 flex-1 gap-4">
              <div class="flex h-32 w-22 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-[var(--surface-subtle)] text-center text-xs text-subtle">
                <img v-if="recognitions[entry.item.token]?.poster_url" :src="recognitions[entry.item.token].poster_url" :alt="`${recognitions[entry.item.token].title} 海报`" class="h-full w-full object-cover" loading="lazy" />
                <span v-else>{{ entry.group.site_name }}<br />{{ entry.group.site_type.toUpperCase() }}</span>
              </div>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap gap-1.5"><span class="status-chip">{{ entry.group.site_name }}</span><span class="status-chip">{{ entry.group.site_type.toUpperCase() }}</span><span v-if="entry.item.matched_name" class="status-chip status-chip--ready">命中 {{ entry.item.matched_name }}</span><span v-if="entry.item.promotion" class="status-chip status-chip--ready">{{ entry.item.promotion.toUpperCase() }}</span><span v-for="label in ptRecognitionSpecLabels(entry.item.specifications || {})" :key="label" class="status-chip">{{ label }}</span><span v-if="entry.item.quality" class="status-chip">{{ entry.item.quality }}</span><span v-for="tag in entry.item.tags || []" :key="tag" class="status-chip">{{ tag }}</span></div>
                <h2 class="mt-3 break-words text-base font-750">{{ entry.item.title }}</h2>
                <p v-if="entry.item.subtitle" class="text-subtle mb-0 mt-1 line-clamp-2 text-xs">{{ entry.item.subtitle }}</p>
                <div class="text-subtle mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs"><span>{{ formatBytes(entry.item.size_bytes ?? null) }}</span><span>{{ formatTime(entry.item.published_at) }}</span><strong>做种 {{ count(entry.item.seeders) }}</strong><span>下载 {{ count(entry.item.leechers) }}</span><span>完成 {{ count(entry.item.completed) }}</span></div>
                <div v-if="recognitions[entry.item.token]" class="semantic-inset mt-3 p-3 text-sm"><div class="flex flex-wrap items-center gap-2"><strong>{{ recognitions[entry.item.token].title }}</strong><span :class="recognitions[entry.item.token].status === 'matched' ? 'status-chip status-chip--ready' : 'status-chip status-chip--warning'">{{ recognitions[entry.item.token].manual_override ? '已人工确认' : recognitions[entry.item.token].status === 'matched' ? '标题预识别成功' : '标题预识别未命中' }}</span><span class="status-chip">{{ mediaTypeLabel(recognitions[entry.item.token].media_type) }}</span><span v-if="recognitions[entry.item.token].year" class="status-chip">{{ recognitions[entry.item.token].year }}</span></div><p v-if="ptRecognitionEpisodeLabel(recognitions[entry.item.token])" class="mb-0 mt-2">{{ ptRecognitionEpisodeLabel(recognitions[entry.item.token]) }}</p><p v-if="recognitions[entry.item.token].error_code" class="text-subtle mb-0 mt-2 text-xs">{{ ptRecognitionErrorLabel(recognitions[entry.item.token].error_code) }}</p></div>
                <p v-if="recognitionErrors[entry.item.token]" class="semantic-warning mb-0 mt-3 p-3 text-xs">{{ recognitionErrors[entry.item.token] }}</p>
              </div>
            </div>
            <footer class="mt-4 flex flex-wrap justify-end gap-2 border-t border-[var(--border)] pt-4"><button class="btn-secondary" :disabled="recognizingTokens.includes(entry.item.token)" @click="recognizeResult(entry.item)">{{ recognizingTokens.includes(entry.item.token) ? '检测中…' : '检测' }}</button><button class="btn-secondary" :disabled="!auth.can(Permissions.DownloadsCreate)" @click="openManualRecognition(entry.item)">手动检测</button><button class="btn-primary" :disabled="!auth.can(Permissions.DownloadsCreate)" @click="openDownload(entry.item)">入库</button></footer>
          </article>
        </div>
        <footer v-if="activeGroup?.status === 'success'" class="panel flex items-center justify-center gap-3"><button class="btn-secondary" :disabled="searching || activeGroup.page <= 1" @click="previousPage(activeGroup)">上一页</button><span class="text-sm">{{ activeGroup.site_name }} · 第 {{ activeGroup.page }} 页</span><button class="btn-secondary" :disabled="searching || !activeGroup.has_next" @click="nextPage(activeGroup)">下一页</button></footer>
        <p v-else-if="activeChannel === 'all'" class="text-subtle text-center text-xs">“全部渠道”聚合显示各站点当前页；切换到单个站点后可独立翻页。</p>
      </template>

    </template>

    <div v-if="siteSelectorOpen" class="modal-backdrop fixed inset-0 z-60 flex items-center justify-center p-4" @click.self="siteSelectorOpen = false" @keydown.esc="siteSelectorOpen = false">
      <section class="panel max-h-[90vh] w-full max-w-2xl overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="search-site-selector-title">
        <div class="flex items-start justify-between gap-3"><div><h2 id="search-site-selector-title" class="m-0 text-xl">选择搜索站点</h2><p class="page-description mt-1 text-sm">本次搜索、流式回退和多语言聚合只会访问这里选中的站点。</p></div><button class="icon-button" type="button" aria-label="关闭站点选择" @click="siteSelectorOpen = false">×</button></div>
        <div class="mt-4 flex flex-wrap items-center justify-between gap-3"><span class="text-sm">已选 {{ selectedSiteIDs.length }} / {{ selectableSiteOptions.length }} 个可用站点</span><div class="flex gap-2"><button class="btn-secondary" type="button" :disabled="siteOptionsLoading || !selectableSiteOptions.length" @click="selectAllSites">全选</button><button class="btn-secondary" type="button" :disabled="siteOptionsLoading || !selectedSiteIDs.length" @click="clearSelectedSites">取消全选</button></div></div>
        <div v-if="siteOptionsLoading" class="semantic-inset mt-4 p-5 text-center text-sm text-muted">正在读取可搜索站点…</div>
        <div v-else-if="siteOptionsError" class="semantic-error mt-4 p-4"><strong>站点列表读取失败</strong><p class="mb-0 mt-1 text-sm">{{ siteOptionsError }}</p><button class="btn-secondary mt-3" type="button" @click="openSiteSelector">重试</button></div>
        <div v-else-if="!siteOptions.length" class="semantic-warning mt-4 p-4 text-sm">当前没有已启用且支持搜索的 PT/BT 站点，请先到站点管理添加并测试。</div>
        <div v-else class="mt-4 grid gap-2 sm:grid-cols-2">
          <label v-for="site in siteOptions" :key="site.id" class="semantic-list-item flex items-start gap-3 p-3" :class="{ 'opacity-60': !site.searchable }">
            <input v-model="selectedSiteIDs" type="checkbox" :value="site.id" :disabled="!site.searchable" />
            <span class="min-w-0"><strong class="block break-words">{{ site.name }}</strong><small class="text-subtle mt-1 block">{{ site.site_type.toUpperCase() }} · {{ site.health_status || 'unknown' }}</small><small v-if="site.reason" class="semantic-danger-text mt-1 block">{{ site.reason }}</small></span>
          </label>
        </div>
        <p v-if="!siteOptionsLoading && !siteOptionsError && selectedSiteIDs.length === 0" class="semantic-warning mt-4 p-3 text-sm" role="alert">至少选择一个可搜索站点后才能开始搜索。</p>
        <div class="mt-5 flex justify-end gap-3"><button class="btn-secondary" type="button" @click="siteSelectorOpen = false">取消</button><button class="btn-primary" type="button" :disabled="siteOptionsLoading || !!siteOptionsError || selectedSiteIDs.length === 0" @click="confirmSiteSelection">搜索</button></div>
      </section>
    </div>

    <div v-if="manualDialog" class="modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4" @click.self="!manualSaving && (manualDialog = null)">
      <form class="panel max-h-[90vh] w-full max-w-3xl overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="manual-recognition-title" @submit.prevent="searchManualCandidates">
        <div class="flex items-start justify-between gap-3"><div><h2 id="manual-recognition-title" class="m-0 text-xl">手动识别作品</h2><p class="page-description mt-1 line-clamp-2 text-sm">{{ manualDialog.title }}</p></div><button class="btn-secondary" type="button" :disabled="manualSaving" @click="manualDialog = null">关闭</button></div>
        <p class="semantic-inset mt-4 p-3 text-sm">自动识别失败也可以在这里修改关键词。候选只用于选择 TMDB 身份；Server 会重新读取 TMDB 详情并把验证结果绑定到当前短期搜索令牌。</p>
        <div class="mt-4 grid gap-3 md:grid-cols-[minmax(0,1fr)_9rem_8rem_auto] md:items-end">
          <div><label class="label" for="manual-recognition-keyword">作品关键词</label><input id="manual-recognition-keyword" v-model="manualForm.keyword" class="input" maxlength="256" placeholder="中文、英文、原名或拼音" required /></div>
          <div><label class="label" for="manual-recognition-type">媒体类型</label><select id="manual-recognition-type" v-model="manualForm.mediaType" class="input"><option value="">电影 + 剧集</option><option value="movie">电影</option><option value="tv">剧集</option></select></div>
          <div><label class="label" for="manual-recognition-year">年份</label><input id="manual-recognition-year" v-model.number="manualForm.year" class="input" type="number" min="1888" max="2200" placeholder="可选" /></div>
          <button class="btn-secondary" :disabled="manualSearching">{{ manualSearching ? '搜索中…' : '搜索 TMDB' }}</button>
        </div>
        <div v-if="manualCandidates.length" class="mt-4 grid gap-3 sm:grid-cols-2">
          <button v-for="candidate in manualCandidates" :key="`${candidate.media_type}-${candidate.id}`" class="semantic-list-item flex min-h-32 gap-3 p-3 text-left" :class="{ 'semantic-list-item--selected': selectedManualCandidate?.id === candidate.id && selectedManualCandidate?.media_type === candidate.media_type }" type="button" @click="selectedManualCandidate = candidate">
            <img v-if="candidate.poster_url" :src="candidate.poster_url" :alt="`${candidate.title} 海报`" class="h-28 w-19 shrink-0 rounded object-cover" loading="lazy" />
            <div class="min-w-0"><strong class="break-words">{{ candidate.title }}</strong><small v-if="candidate.original_title && candidate.original_title !== candidate.title" class="text-subtle mt-1 block break-words">{{ candidate.original_title }}</small><div class="mt-2 flex flex-wrap gap-1.5"><span class="status-chip">{{ mediaTypeLabel(candidate.media_type) }}</span><span v-if="candidate.release_year" class="status-chip">{{ candidate.release_year }}</span><span v-if="candidate.original_language" class="status-chip">{{ candidate.original_language.toUpperCase() }}</span></div><small class="text-subtle mt-2 block">TMDB {{ candidate.id }} · 搜索相似度 {{ Math.round(candidate.confidence * 100) }}%</small></div>
          </button>
        </div>
        <p v-else-if="!manualSearching" class="text-subtle mt-4 text-sm">修改关键词并搜索，然后选择正确作品。没有候选不会改变当前识别结果。</p>
        <div class="mt-5 flex justify-end gap-3"><button class="btn-secondary" type="button" :disabled="manualSaving" @click="manualDialog = null">取消</button><button class="btn-primary" type="button" :disabled="manualSaving || !selectedManualCandidate" @click="confirmManualRecognition">{{ manualSaving ? '正在验证身份…' : '确认此身份' }}</button></div>
      </form>
    </div>

    <div v-if="downloadDialog" class="modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4" @click.self="!submitting && (downloadDialog = null)">
      <form class="panel w-full max-w-xl" role="dialog" aria-modal="true" aria-labelledby="pt-download-title" @submit.prevent="submitDownload">
        <div class="flex items-start justify-between gap-3"><div><h2 id="pt-download-title" class="m-0 text-xl">创建下载任务</h2><p class="page-description mt-1 line-clamp-2 text-sm">{{ downloadDialog.title }}</p></div><button class="btn-secondary" type="button" :disabled="submitting" @click="downloadDialog = null">关闭</button></div>
        <div class="mt-5 grid gap-4 sm:grid-cols-2">
          <div><label class="label">下载器</label><select v-model="downloadForm.downloaderID" class="input" required><option value="" disabled>请选择</option><option v-for="item in enabledDownloaders" :key="item.id" :value="item.id">{{ item.name }} · {{ item.type === 'pan115_offline' ? '115 离线' : item.type }}</option></select></div>
          <DownloadRouteTargetPicker v-model="downloadForm.mediaLibraryID" :preview="routePreview" :loading="routePreviewLoading" />
          <div class="sm:col-span-2"><label class="label">队列优先级</label><input v-model.number="downloadForm.priority" class="input" type="number" min="-100" max="100" /></div>
        </div>
        <div v-if="selectedLibrary && selectedRoute" class="semantic-inset mt-4 grid gap-3 p-4 text-sm sm:grid-cols-2"><div><span class="text-subtle block text-xs">最终媒体库</span><strong>{{ selectedLibrary.name }}</strong></div><div><span class="text-subtle block text-xs">分类与入库</span><strong>{{ selectedRoute.route_label }} · {{ selectedLibrary.profile_name }} · {{ selectedLibrary.transfer_mode }}</strong></div></div>
        <p class="text-subtle mt-4 text-xs">确认后 Server 才会凭短期令牌获取种子，并复用现有下载 → 识别 → 整理 → 入库流水线。真实种子地址和 passkey 不会进入页面。</p>
        <div class="mt-5 flex justify-end gap-3"><button class="btn-secondary" type="button" :disabled="submitting" @click="downloadDialog = null">取消</button><button class="btn-primary" :disabled="submitting || routePreviewLoading || !downloadForm.downloaderID || !selectedRoute?.enabled || !selectedLibrary">{{ submitting ? '正在获取种子并入队…' : '确认并入队' }}</button></div>
      </form>
    </div>
  </section>
</template>
