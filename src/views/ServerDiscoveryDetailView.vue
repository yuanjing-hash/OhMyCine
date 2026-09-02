<script setup lang="ts">
import type { ServerAcquisitionStatus, ServerCoverageSummary, ServerDiscoveryDetail, ServerDownloadOption, ServerFollowDefaults, ServerLibraryOption, ServerProfileOption, ServerResourceGroup, ServerResourceItem, ServerSearchProgress, ServerSearchSite } from '@/services/serverDiscovery'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MediaDetailHero from '@/components/media/MediaDetailHero.vue'
import DiscoveryAcquisitionDialog from '@/components/server/DiscoveryAcquisitionDialog.vue'
import DiscoverySearchWorkspace from '@/components/server/DiscoverySearchWorkspace.vue'
import DiscoverySitePickerDialog from '@/components/server/DiscoverySitePickerDialog.vue'
import { ServerDataSource } from '@/services/datasource/server'
import { registerLayoutBackHandler } from '@/services/layoutBackNavigation'
import { publishFeedback } from '@/services/mediaActions'
import { getServerAcquisition, getServerCoverage, getServerDiscoveryDetail, getServerDownloadOptions, getServerFollowDefaults, getServerSearchSites, streamServerResources } from '@/services/serverDiscovery'
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
const groups = ref<ServerResourceGroup[]>([])
const activeSiteId = ref<number>()
const downloaders = ref<ServerDownloadOption[]>([])
const libraries = ref<ServerLibraryOption[]>([])
const profiles = ref<ServerProfileOption[]>([])
const loading = ref(true)
const searching = ref(false)
const loadingSites = ref(false)
const loadingTargets = ref(false)
const submitting = ref(false)
const busyToken = ref('')
const error = ref('')
const capabilities = ref(new Set<string>())
const sitePickerOpen = ref(false)
const resultWorkspaceOpen = ref(false)
const acquisitionDialogOpen = ref(false)
const selectedResource = ref<ServerResourceItem | null>(null)
const followOpen = ref(false)
const followLoading = ref(false)
const subscribing = ref(false)
const followDefaults = ref<ServerFollowDefaults | null>(null)
const followForm = ref({ seasons: [] as number[], siteIds: [] as number[], downloaderId: '', libraryId: 0, minutes: 360, maxResources: 3, priority: 0 })
const pendingSearchMode = ref<'aggregate' | 'direct'>('aggregate')
const directKind = ref<'title' | 'tmdb'>('title')
const directQuery = ref('')
const searchProgress = ref<ServerSearchProgress>(emptyProgress())
const backOwner = Symbol('server-discovery-detail')
let unregisterBack: (() => void) | undefined
let searchGeneration = 0
let statusGeneration = 0
let statusTimer: number | undefined
let activeSearch: Awaited<ReturnType<typeof streamServerResources>> | null = null

const sourceId = computed(() => String(route.params.sourceId))
const provider = computed(() => route.params.provider === 'douban' ? 'douban' : 'tmdb')
const mediaType = computed(() => route.params.mediaType === 'tv' ? 'tv' : 'movie')
const providerId = computed(() => String(route.params.providerId))
const searchableSites = computed(() => sites.value.filter(site => site.searchable))
const selectedAll = computed(() => searchableSites.value.length > 0 && searchableSites.value.every(site => selectedSiteIds.value.includes(site.id)))
const canSearch = computed(() => capabilities.value.has('discovery_search'))
const canAcquire = computed(() => capabilities.value.has('acquisition_create'))
const canSubscribe = computed(() => capabilities.value.has('subscription_create'))
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

async function resolveSource(): Promise<ServerDataSource> {
  store.loadConfigs()
  await store.syncManager()
  const config = store.orderedConfigs.find(item => item.id === sourceId.value && item.type === 'server' && item.enabled !== false)
  const source = store.getSource(sourceId.value)
  if (!config || !(source instanceof ServerDataSource))
    throw new Error('对应 Server 尚未连接，请先在设置中登录。')
  return source
}

async function load() {
  stopStatusPolling()
  loading.value = true
  error.value = ''
  groups.value = []
  activeSiteId.value = undefined
  try {
    const source = await resolveSource()
    capabilities.value = new Set(await source.refreshCapabilities())
    detail.value = await getServerDiscoveryDetail(source, provider.value, mediaType.value, providerId.value)
    directQuery.value = detail.value.work.title
    if (detail.value.work.tmdbId) {
      const tmdbId = detail.value.work.tmdbId
      const [coverageResult, acquisitionResult] = await Promise.all([
        getServerCoverage(source, mediaType.value, tmdbId).catch(() => null),
        getServerAcquisition(source, mediaType.value, tmdbId).catch(() => null),
      ])
      coverage.value = coverageResult
      acquisition.value = acquisitionResult
      scheduleStatusRefresh()
    }
  }
  catch (reason) {
    error.value = message(reason)
  }
  finally {
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
  if (sites.value.length || loadingSites.value)
    return
  loadingSites.value = true
  try {
    const result = await getServerSearchSites(await resolveSource())
    sites.value = result
    selectedSiteIds.value = result.filter(site => site.searchable).map(site => site.id)
  }
  catch (reason) {
    publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
  finally {
    loadingSites.value = false
  }
}

function toggleAllSites() {
  selectedSiteIds.value = selectedAll.value ? [] : searchableSites.value.map(site => site.id)
}

function toggleSite(id: number) {
  selectedSiteIds.value = selectedSiteIds.value.includes(id) ? selectedSiteIds.value.filter(value => value !== id) : [...selectedSiteIds.value, id]
}

async function runSearch(siteIds = selectedSiteIds.value, replaceAll = true, page = 1) {
  if (!detail.value || siteIds.length === 0)
    return
  const tmdbId = detail.value.work.tmdbId
  if (pendingSearchMode.value === 'aggregate' && !tmdbId) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '该作品还没有可验证的 TMDB ID，请使用直接搜索。' })
    return
  }
  const generation = ++searchGeneration
  await activeSearch?.cancel()
  activeSearch = null
  searching.value = true
  error.value = ''
  searchProgress.value = emptyProgress(siteIds.length)
  if (replaceAll) {
    groups.value = []
    activeSiteId.value = undefined
  }
  else {
    groups.value = groups.value.filter(group => !siteIds.includes(group.siteId))
  }
  sitePickerOpen.value = false
  resultWorkspaceOpen.value = true
  try {
    const source = await resolveSource()
    const useTMDB = pendingSearchMode.value === 'direct' && directKind.value === 'tmdb'
    const handle = await streamServerResources(source, {
      mediaType: mediaType.value,
      tmdbId: pendingSearchMode.value === 'aggregate' || useTMDB ? tmdbId : undefined,
      title: pendingSearchMode.value === 'direct' && !useTMDB ? directQuery.value : undefined,
      direct: pendingSearchMode.value === 'direct',
      siteIds,
      page,
    }, (event) => {
      if (generation !== searchGeneration)
        return
      if (event.type === 'progress' || event.type === 'done') {
        searchProgress.value = event.progress
        return
      }
      if (event.type === 'site') {
        groups.value = [...groups.value.filter(group => group.siteId !== event.group.siteId), event.group]
          .sort((left, right) => siteOrder(left.siteId) - siteOrder(right.siteId) || left.siteId - right.siteId)
        if (event.group.items.length > 0 && !groups.value.some(group => group.siteId === activeSiteId.value && group.items.length > 0))
          activeSiteId.value = event.group.siteId
        return
      }
      error.value = event.message
      publishFeedback({ id: Date.now(), kind: 'error', message: event.message })
    })
    activeSearch = handle
    await handle.done
  }
  catch (reason) {
    if (generation === searchGeneration) {
      error.value = message(reason)
      publishFeedback({ id: Date.now(), kind: 'error', message: error.value })
    }
  }
  finally {
    if (generation === searchGeneration) {
      searching.value = false
      activeSearch = null
    }
  }
}

async function cancelSearch() {
  searchGeneration++
  const handle = activeSearch
  activeSearch = null
  searching.value = false
  await handle?.cancel()
}

function siteOrder(siteId: number) {
  const index = sites.value.findIndex(site => site.id === siteId)
  return index < 0 ? Number.MAX_SAFE_INTEGER : index
}

async function changeSitePage(siteId: number, page: number) {
  activeSiteId.value = siteId
  await runSearch([siteId], false, page)
}

async function openAcquisition(item: ServerResourceItem) {
  if (!canAcquire.value) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '当前 Server 账号没有下载入库权限。' })
    return
  }
  selectedResource.value = item
  acquisitionDialogOpen.value = true
  try {
    await ensureDownloadOptions(await resolveSource())
  }
  catch (reason) {
    publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
}

async function ensureDownloadOptions(source: ServerDataSource) {
  if (downloaders.value.length || loadingTargets.value)
    return
  loadingTargets.value = true
  try {
    const options = await getServerDownloadOptions(source)
    downloaders.value = options.downloaders
    libraries.value = options.libraries
    profiles.value = options.profiles
  }
  finally {
    loadingTargets.value = false
  }
}

async function submitAcquisition(options: { downloaderId: string, libraryId: number, profileId: number }) {
  const resource = selectedResource.value
  const work = detail.value?.work
  if (!resource || !work?.tmdbId)
    return
  submitting.value = true
  busyToken.value = resource.token
  try {
    const source = await resolveSource()
    await source.createDiscoveryDownload({
      result_token: resource.token,
      downloader_id: options.downloaderId,
      media_library_id: options.libraryId,
      profile_id: options.profileId,
      priority: 0,
      expected_tmdb_id: work.tmdbId,
      expected_media_type: mediaType.value,
    })
    acquisition.value = await getServerAcquisition(source, mediaType.value, work.tmdbId)
    acquisitionDialogOpen.value = false
    scheduleStatusRefresh(true)
    publishFeedback({ id: Date.now(), kind: 'success', message: `《${work.title}》已进入 Server 入库流程，可从右侧“入库任务”持续查看。` })
  }
  catch (reason) {
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
  followOpen.value = true
  followLoading.value = true
  try {
    const defaults = await getServerFollowDefaults(await resolveSource(), tmdbId)
    followDefaults.value = defaults
    followForm.value = { seasons: [...defaults.snapshot.seasons], siteIds: [...defaults.snapshot.site_ids], downloaderId: defaults.snapshot.downloader_id, libraryId: defaults.snapshot.media_library_id, minutes: defaults.snapshot.schedule.minutes, maxResources: defaults.snapshot.max_resources_per_run, priority: defaults.snapshot.download_priority }
  }
  catch (reason) {
    publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
    followOpen.value = false
  }
  finally {
    followLoading.value = false
  }
}

function toggleFollowSite(id: number) {
  followForm.value.siteIds = followForm.value.siteIds.includes(id) ? followForm.value.siteIds.filter(value => value !== id) : [...followForm.value.siteIds, id]
}

function toggleSeason(season: number) {
  followForm.value.seasons = followForm.value.seasons.includes(season) ? followForm.value.seasons.filter(value => value !== season) : [...followForm.value.seasons, season].sort((a, b) => a - b)
}

async function createFollow() {
  const work = detail.value?.work
  if (!work?.tmdbId || !followDefaults.value)
    return
  subscribing.value = true
  try {
    const source = await resolveSource()
    const base = followDefaults.value.snapshot
    await source.createDiscoveryFollow({ tmdb_id: work.tmdbId, title: work.title, year: work.year, snapshot: { ...base, seasons: followForm.value.seasons, site_ids: followForm.value.siteIds, downloader_id: followForm.value.downloaderId, media_library_id: followForm.value.libraryId, schedule: { kind: 'interval', minutes: followForm.value.minutes }, max_resources_per_run: followForm.value.maxResources, download_priority: followForm.value.priority } })
    acquisition.value = await getServerAcquisition(source, 'tv', work.tmdbId)
    followOpen.value = false
    scheduleStatusRefresh(true)
    publishFeedback({ id: Date.now(), kind: 'success', message: `《${work.title}》已订阅，Server 将自动搜索缺失剧集并入库。` })
  }
  catch (reason) {
    publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
  finally {
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

function emptyProgress(total = 0): ServerSearchProgress {
  return { total, pending: total, running: 0, completed: 0, succeeded: 0, failed: 0, resultCount: 0 }
}

onMounted(() => {
  unregisterBack = registerLayoutBackHandler(backOwner, handleLayoutBack)
  void load()
})
watch(() => route.fullPath, async () => {
  await cancelSearch()
  await load()
})
onBeforeUnmount(() => {
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
      <MediaDetailHero :title="detail.work.title" :original-title="detail.work.originalTitle" :poster-url="detail.work.posterUrl" :backdrop-url="detail.work.backdropUrl" :overview="detail.work.overview || '暂无简介。'" eyebrow="OhMyCine Server Discovery">
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
      <DiscoverySearchWorkspace :open="resultWorkspaceOpen" :title="detail.work.title" :groups="groups" :active-site-id="activeSiteId" :searching="searching" :progress="searchProgress" :can-acquire="canAcquire" :busy-token="busyToken" @close="resultWorkspaceOpen = false" @cancel="cancelSearch" @retry="runSearch([$event], false)" @select-site="activeSiteId = $event" @select-resource="openAcquisition" @page="changeSitePage" />
      <DiscoveryAcquisitionDialog :open="acquisitionDialogOpen" :resource="selectedResource" :downloaders="downloaders" :libraries="libraries" :profiles="profiles" :loading="loadingTargets" :submitting="submitting" @close="acquisitionDialogOpen = false" @confirm="submitAcquisition" />

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
              <label class="text-xs text-white/48">订阅季</label><div class="mt-2 flex flex-wrap gap-2">
                <button v-for="season in Math.max(detail.seasonCount || 1, 1)" :key="season" type="button" class="glass-button rounded-full px-3 py-2 text-sm" :class="{ 'bg-white text-black': followForm.seasons.includes(season) }" @click="toggleSeason(season)">
                  第 {{ season }} 季
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
              <label class="text-xs text-white/48">下载器<select v-model="followForm.downloaderId" class="follow-input mt-2 w-full rounded-2xl p-3 text-white"><option v-for="item in followDefaults.downloaders" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">目标媒体库<select v-model="followForm.libraryId" class="follow-input mt-2 w-full rounded-2xl p-3 text-white"><option v-for="item in followDefaults.mediaLibraries" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">检查间隔（分钟）<input v-model.number="followForm.minutes" class="follow-input mt-2 w-full rounded-2xl p-3 text-white" type="number" min="10" max="10080"></label><label class="text-xs text-white/48">每次最多下载资源数<input v-model.number="followForm.maxResources" class="follow-input mt-2 w-full rounded-2xl p-3 text-white" type="number" min="1" max="20"></label>
            </div>
            <footer class="flex justify-end">
              <button class="rounded-full bg-white px-6 py-2.5 font-bold text-black" :disabled="subscribing || !followForm.seasons.length || !followForm.siteIds.length || !followForm.downloaderId || !followForm.libraryId" @click="createFollow">
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
