<script setup lang="ts">
import type { ServerAcquisitionStatus, ServerCoverageSummary, ServerDiscoveryDetail, ServerDownloadOption, ServerFollowDefaults, ServerLibraryOption, ServerProfileOption, ServerResourceGroup, ServerResourceItem, ServerSearchProgress, ServerSearchSite } from '@/services/serverDiscovery'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MediaDetailHero from '@/components/media/MediaDetailHero.vue'
import { ServerDataSource } from '@/services/datasource/server'
import { publishFeedback } from '@/services/mediaActions'
import { getServerAcquisition, getServerCoverage, getServerDiscoveryDetail, getServerDownloadOptions, getServerFollowDefaults, getServerSearchSites, streamServerResources } from '@/services/serverDiscovery'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const detail = ref<ServerDiscoveryDetail | null>(null)
const coverage = ref<ServerCoverageSummary | null>(null)
const acquisition = ref<ServerAcquisitionStatus | null>(null)
const sites = ref<ServerSearchSite[]>([])
const selectedSiteIds = ref<number[]>([])
const groups = ref<ServerResourceGroup[]>([])
const downloaders = ref<ServerDownloadOption[]>([])
const libraries = ref<ServerLibraryOption[]>([])
const profiles = ref<ServerProfileOption[]>([])
const downloaderId = ref('')
const libraryId = ref<number | undefined>()
const profileId = ref(0)
const loading = ref(true)
const searching = ref(false)
const loadingSites = ref(false)
const loadingTargets = ref(false)
const downloadingToken = ref('')
const error = ref('')
const capabilities = ref(new Set<string>())
const sitePickerOpen = ref(false)
const followOpen = ref(false)
const followLoading = ref(false)
const subscribing = ref(false)
const followDefaults = ref<ServerFollowDefaults | null>(null)
const followForm = ref({ seasons: [] as number[], siteIds: [] as number[], downloaderId: '', libraryId: 0, minutes: 360, maxResources: 3, priority: 0 })
const pendingSearchMode = ref<'aggregate' | 'direct'>('aggregate')
const directKind = ref<'title' | 'tmdb'>('title')
const directQuery = ref('')
const searchProgress = ref<ServerSearchProgress>(emptyProgress())
let searchGeneration = 0
let activeSearch: Awaited<ReturnType<typeof streamServerResources>> | null = null

const sourceId = computed(() => String(route.params.sourceId))
const provider = computed(() => route.params.provider === 'douban' ? 'douban' : 'tmdb')
const mediaType = computed(() => route.params.mediaType === 'tv' ? 'tv' : 'movie')
const providerId = computed(() => String(route.params.providerId))
const searchableSites = computed(() => sites.value.filter(site => site.searchable))
const selectedAll = computed(() => searchableSites.value.length > 0 && searchableSites.value.every(site => selectedSiteIds.value.includes(site.id)))
const resources = computed(() => groups.value.flatMap(group => group.items.map(item => ({ group, item }))))
const progressPercent = computed(() => searchProgress.value.total > 0 ? Math.round(searchProgress.value.completed / searchProgress.value.total * 100) : 0)
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
const acquisitionLabel = computed(() => {
  if (!acquisition.value || acquisition.value.stage === 'idle')
    return '尚未入库'
  const stages: Record<string, string> = { subscription: '已订阅', download: '下载中', transfer: '整理中', import: '入库完成' }
  return `${stages[acquisition.value.stage] ?? acquisition.value.stage} · ${acquisition.value.status}`
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
  loading.value = true
  error.value = ''
  groups.value = []
  try {
    const source = await resolveSource()
    capabilities.value = new Set(await source.refreshCapabilities())
    detail.value = await getServerDiscoveryDetail(source, provider.value, mediaType.value, providerId.value)
    directQuery.value = detail.value.work.title
    if (detail.value.work.tmdbId) {
      const tmdbId = detail.value.work.tmdbId
      const [coverageResult, acquisitionResult] = await Promise.all([getServerCoverage(source, mediaType.value, tmdbId).catch(() => null), getServerAcquisition(source, mediaType.value, tmdbId).catch(() => null)])
      coverage.value = coverageResult
      acquisition.value = acquisitionResult
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
    error.value = '当前 Server 账号没有影视搜索权限。'
    return
  }
  pendingSearchMode.value = mode
  sitePickerOpen.value = true
  if (sites.value.length || loadingSites.value)
    return
  loadingSites.value = true
  try {
    const source = await resolveSource()
    const result = await getServerSearchSites(source)
    sites.value = result
    selectedSiteIds.value = result.filter(site => site.searchable).map(site => site.id)
  }
  catch (reason) {
    error.value = message(reason)
  }
  finally {
    loadingSites.value = false
  }
}
function toggleAllSites() {
  selectedSiteIds.value = selectedAll.value ? [] : searchableSites.value.map(site => site.id)
}

async function openFollow() {
  if (!canSubscribe.value) {
    error.value = '当前 Server 账号没有创建订阅的权限。'
    return
  }
  const tmdbId = detail.value?.work.tmdbId
  if (!tmdbId)
    return
  followOpen.value = true
  followLoading.value = true
  try {
    const source = await resolveSource()
    const defaults = await getServerFollowDefaults(source, tmdbId)
    followDefaults.value = defaults
    followForm.value = {
      seasons: [...defaults.snapshot.seasons],
      siteIds: [...defaults.snapshot.site_ids],
      downloaderId: defaults.snapshot.downloader_id,
      libraryId: defaults.snapshot.media_library_id,
      minutes: defaults.snapshot.schedule.minutes,
      maxResources: defaults.snapshot.max_resources_per_run,
      priority: defaults.snapshot.download_priority,
    }
  }
  catch (reason) {
    error.value = message(reason)
    followOpen.value = false
  }
  finally {
    followLoading.value = false
  }
}
function toggleFollowSite(id: number) {
  followForm.value.siteIds = followForm.value.siteIds.includes(id)
    ? followForm.value.siteIds.filter(value => value !== id)
    : [...followForm.value.siteIds, id]
}
function toggleSeason(season: number) {
  followForm.value.seasons = followForm.value.seasons.includes(season)
    ? followForm.value.seasons.filter(value => value !== season)
    : [...followForm.value.seasons, season].sort((a, b) => a - b)
}
async function createFollow() {
  const work = detail.value?.work
  if (!work?.tmdbId || !followDefaults.value)
    return
  subscribing.value = true
  try {
    const source = await resolveSource()
    const base = followDefaults.value.snapshot
    await source.createDiscoveryFollow({
      tmdb_id: work.tmdbId,
      title: work.title,
      year: work.year,
      snapshot: {
        ...base,
        seasons: followForm.value.seasons,
        site_ids: followForm.value.siteIds,
        downloader_id: followForm.value.downloaderId,
        media_library_id: followForm.value.libraryId,
        schedule: { kind: 'interval', minutes: followForm.value.minutes },
        max_resources_per_run: followForm.value.maxResources,
        download_priority: followForm.value.priority,
      },
    })
    acquisition.value = await getServerAcquisition(source, 'tv', work.tmdbId)
    followOpen.value = false
    publishFeedback({ id: Date.now(), kind: 'success', message: `《${work.title}》已订阅，Server 将自动搜索缺失剧集并按当前设置下载入库。` })
  }
  catch (reason) {
    publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
  finally {
    subscribing.value = false
  }
}
function toggleSite(id: number) {
  selectedSiteIds.value = selectedSiteIds.value.includes(id) ? selectedSiteIds.value.filter(value => value !== id) : [...selectedSiteIds.value, id]
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
    downloaderId.value = downloaders.value[0]?.id ?? ''
    libraryId.value = libraries.value[0]?.id
    profileId.value = profiles.value[0]?.id ?? 0
  }
  finally {
    loadingTargets.value = false
  }
}

async function runSearch(siteIds = selectedSiteIds.value, replaceAll = true) {
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
  if (replaceAll)
    groups.value = []
  else
    groups.value = groups.value.filter(group => !siteIds.includes(group.siteId))
  sitePickerOpen.value = false
  try {
    const source = await resolveSource()
    const useTMDB = pendingSearchMode.value === 'direct' && directKind.value === 'tmdb'
    void ensureDownloadOptions(source).catch(reason => publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) }))
    const handle = await streamServerResources(source, {
      mediaType: mediaType.value,
      tmdbId: pendingSearchMode.value === 'aggregate' || useTMDB ? tmdbId : undefined,
      title: pendingSearchMode.value === 'direct' && !useTMDB ? directQuery.value : undefined,
      direct: pendingSearchMode.value === 'direct',
      siteIds,
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
        return
      }
      error.value = event.message
    })
    activeSearch = handle
    await handle.done
  }
  catch (reason) {
    if (generation === searchGeneration)
      error.value = message(reason)
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

async function download(item: ServerResourceItem) {
  if (!canAcquire.value) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '当前 Server 账号没有下载入库权限。' })
    return
  }
  if (!downloaderId.value || !profileId.value) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '请先选择下载器和分类规则。' })
    return
  }
  downloadingToken.value = item.token
  try {
    const source = await resolveSource()
    await source.createDiscoveryDownload({ result_token: item.token, downloader_id: downloaderId.value, media_library_id: libraryId.value, profile_id: profileId.value, priority: 0 })
    if (detail.value?.work.tmdbId)
      acquisition.value = await getServerAcquisition(source, mediaType.value, detail.value.work.tmdbId).catch(() => acquisition.value)
    publishFeedback({ id: Date.now(), kind: 'success', message: `《${item.title}》已提交 Server，下载完成后将自动识别并入库。` })
  }
  catch (reason) {
    publishFeedback({ id: Date.now(), kind: 'error', message: message(reason) })
  }
  finally {
    downloadingToken.value = ''
  }
}

function formatBytes(value?: number) {
  if (!value)
    return '大小未知'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index++
  }
  return `${size.toFixed(index > 1 ? 1 : 0)} ${units[index]}`
}
function message(reason: unknown) {
  return reason instanceof Error ? reason.message : '操作失败'
}
function emptyProgress(total = 0): ServerSearchProgress {
  return { total, pending: total, running: 0, completed: 0, succeeded: 0, failed: 0, resultCount: 0 }
}
onMounted(load)
watch(() => route.fullPath, async () => {
  await cancelSearch()
  await load()
})
onBeforeUnmount(() => {
  void cancelSearch()
})
</script>

<template>
  <section class="min-h-full pb-16">
    <button class="glass-button mb-5 px-4 py-2 text-sm" type="button" @click="router.back()">
      返回
    </button>
    <div v-if="loading" class="glass-panel p-12 text-center text-white/55">
      正在通过 Server 读取 TMDB 详情…
    </div>
    <div v-else-if="error && !detail" class="glass-panel border border-red-400/20 p-6 text-red-100">
      <h2 class="text-lg font-bold">
        无法打开影视详情
      </h2><p class="mt-2 text-sm">
        {{ error }}
      </p><button class="glass-button mt-4 px-4 py-2" @click="load">
        重试
      </button>
    </div>
    <template v-else-if="detail">
      <MediaDetailHero
        :title="detail.work.title"
        :original-title="detail.work.originalTitle"
        :poster-url="detail.work.posterUrl"
        :backdrop-url="detail.work.backdropUrl"
        :overview="detail.work.overview || '暂无简介。'"
        eyebrow="OhMyCine Server Discovery"
      >
        <template #meta>
          <span class="rounded-full bg-white/8 px-3 py-1">{{ mediaType === 'tv' ? '电视剧' : '电影' }}</span>
          <span v-if="detail.work.rating" class="rounded-full bg-yellow-400/16 px-3 py-1 text-yellow-100">★ {{ detail.work.rating.toFixed(1) }}</span>
          <span v-if="detail.work.year">{{ detail.work.year }}</span>
          <span v-if="detail.runtimeMinutes">{{ detail.runtimeMinutes }} 分钟</span>
          <span v-if="detail.genres.length">{{ detail.genres.slice(0, 4).join(' / ') }}</span>
          <span class="rounded-full bg-white/8 px-3 py-1">{{ coverageLabel }}</span>
          <span class="rounded-full bg-white/8 px-3 py-1">{{ acquisitionLabel }}</span>
          <span v-if="detail.work.tmdbId" class="rounded-full bg-white/8 px-3 py-1">TMDB {{ detail.work.tmdbId }}</span>
        </template>
        <template #actions>
          <button class="rounded-full bg-white px-7 py-3 text-sm font-bold text-black shadow-xl transition-transform hover:scale-105 disabled:opacity-60" type="button" :disabled="searching || !canSearch" @click="chooseSearch('aggregate')">
            搜索
          </button>
          <button class="rounded-full border border-white/16 bg-white/10 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/16 disabled:opacity-50" type="button" :disabled="searching || !canSearch" @click="chooseSearch('direct')">
            直接搜索
          </button>
          <button v-if="mediaType === 'tv'" class="rounded-full border border-white/16 bg-white/10 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/16 disabled:opacity-50" type="button" :disabled="!canSubscribe || Boolean(acquisition?.followSubscriptionId)" @click="openFollow">
            {{ acquisition?.followSubscriptionId ? '已订阅' : '订阅' }}
          </button>
        </template>
      </MediaDetailHero>

      <div v-if="groups.length || searching" class="glass-panel mt-5 grid gap-3 p-4 md:grid-cols-3">
        <label class="text-xs text-white/48">下载器<select v-model="downloaderId" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-sm text-white"><option value="">请选择</option><option v-for="item in downloaders" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">目标媒体库<select v-model="libraryId" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-sm text-white"><option :value="undefined">仅下载，不自动入库</option><option v-for="item in libraries" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">分类规则<select v-model="profileId" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-sm text-white"><option v-for="item in profiles" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
      </div>

      <div v-if="searching" class="glass-panel mt-5 p-5 text-white/65">
        <div class="flex items-center justify-between gap-4 text-sm">
          <span>已完成 {{ searchProgress.completed }} / {{ searchProgress.total }} 个站点</span>
          <span>已发现 {{ searchProgress.resultCount }} 个资源</span>
        </div>
        <div class="mt-3 h-2 overflow-hidden rounded-full bg-white/8">
          <div class="h-full rounded-full bg-white transition-[width] duration-300" :style="{ width: `${progressPercent}%` }" />
        </div>
        <div class="mt-3 flex flex-wrap items-center justify-between gap-3 text-xs text-white/45">
          <span>运行中 {{ searchProgress.running }} · 等待 {{ searchProgress.pending }} · 成功 {{ searchProgress.succeeded }} · 失败 {{ searchProgress.failed }}<template v-if="searchProgress.siteName"> · {{ searchProgress.siteName }}</template></span>
          <button class="glass-button px-3 py-1.5 text-xs" type="button" @click="cancelSearch">
            取消搜索
          </button>
        </div>
      </div>
      <div v-else-if="error" class="mt-5 rounded-xl border border-red-400/20 bg-red-400/10 p-4 text-sm text-red-100">
        {{ error }}
      </div>
      <section v-if="groups.length" class="mt-5 space-y-4">
        <header>
          <h2 class="text-xl font-bold text-white">
            站点资源
          </h2><p class="mt-1 text-sm text-white/45">
            共 {{ resources.length }} 个可提交资源；下载和入库仍由 Server 统一执行。
          </p>
        </header><article v-for="group in groups" :key="group.siteId" class="glass-panel p-4">
          <div class="mb-3 flex items-center justify-between gap-3">
            <h3 class="font-bold text-white">
              {{ group.siteName }}
            </h3><span class="text-xs text-white/42">{{ group.status === 'success' ? `${group.items.length} 个结果` : '搜索失败' }}</span>
            <button v-if="group.status !== 'success' && !searching" class="glass-button px-3 py-1.5 text-xs" type="button" @click="runSearch([group.siteId], false)">
              重试本站
            </button>
          </div><div class="space-y-2">
            <div v-for="item in group.items" :key="item.token" class="flex flex-col gap-3 rounded-xl border border-white/8 bg-black/18 p-4 md:flex-row md:items-center">
              <div class="min-w-0 flex-1">
                <h4 class="break-words text-sm font-semibold text-white/90">
                  {{ item.title }}
                </h4><p class="mt-2 text-xs text-white/42">
                  {{ [formatBytes(item.sizeBytes), item.seeders != null ? `${item.seeders} 做种` : '', item.promotion, item.quality, item.matchedName ? `命中：${item.matchedName}` : ''].filter(Boolean).join(' · ') }}
                </p>
              </div><button class="glass-button shrink-0 px-4 py-2 text-sm font-bold" :disabled="!canAcquire || downloadingToken === item.token" @click="download(item)">
                {{ downloadingToken === item.token ? '提交中…' : '下载并入库' }}
              </button>
            </div>
          </div>
        </article>
      </section>

      <div v-if="sitePickerOpen" class="fixed inset-0 z-[1200] grid place-items-center bg-black/70 p-4" @click.self="sitePickerOpen = false">
        <section class="glass-panel max-h-[85vh] w-full max-w-2xl overflow-y-auto p-5">
          <header class="flex items-start justify-between gap-4">
            <div>
              <h2 class="text-lg font-bold text-white">
                选择搜索站点
              </h2><p class="mt-1 text-sm text-white/45">
                选择后点“搜索”，只请求这些站点；支持一键全选。
              </p>
            </div><button class="glass-button px-3 py-2" @click="sitePickerOpen = false">
              取消
            </button>
          </header><div v-if="loadingSites" class="mt-5 py-8 text-center text-sm text-white/45">
            正在读取可搜索站点…
          </div><div v-else-if="pendingSearchMode === 'direct'" class="mt-4 rounded-xl border border-white/8 bg-black/20 p-4">
            <div class="flex gap-2">
              <button class="glass-button px-3 py-2 text-sm" :class="{ 'bg-white text-black': directKind === 'title' }" @click="directKind = 'title'">
                按标题
              </button><button class="glass-button px-3 py-2 text-sm" :class="{ 'bg-white text-black': directKind === 'tmdb' }" :disabled="!detail.work.tmdbId" @click="directKind = 'tmdb'">
                按 TMDB ID
              </button>
            </div><input v-if="directKind === 'title'" v-model="directQuery" class="mt-3 w-full rounded-lg bg-black/35 p-3 text-white" maxlength="160" placeholder="输入标题">
          </div><div class="mt-4 flex items-center justify-between">
            <button class="glass-button px-4 py-2 text-sm" @click="toggleAllSites">
              {{ selectedAll ? '取消全选' : '快速全选' }}
            </button><span class="text-xs text-white/42">已选 {{ selectedSiteIds.length }} / {{ searchableSites.length }}</span>
          </div><div class="mt-3 grid gap-2 sm:grid-cols-2">
            <button v-for="site in sites" :key="site.id" class="flex items-center justify-between rounded-xl border p-3 text-left" :class="selectedSiteIds.includes(site.id) ? 'border-white/45 bg-white/10' : 'border-white/8 bg-black/18'" :disabled="!site.searchable" @click="toggleSite(site.id)">
              <span><strong class="text-sm text-white/88">{{ site.name }}</strong><small class="mt-1 block text-white/38">{{ site.siteType.toUpperCase() }}<template v-if="site.reason"> · {{ site.reason }}</template></small></span><span>{{ selectedSiteIds.includes(site.id) ? '✓' : '' }}</span>
            </button>
          </div><footer class="mt-5 flex justify-end">
            <button class="glass-button bg-white px-6 py-2.5 font-bold text-black" :disabled="selectedSiteIds.length === 0 || pendingSearchMode === 'direct' && directKind === 'title' && !directQuery.trim()" @click="runSearch()">
              搜索
            </button>
          </footer>
        </section>
      </div>
      <div v-if="followOpen" class="fixed inset-0 z-[1200] grid place-items-center bg-black/70 p-4" @click.self="!subscribing && (followOpen = false)">
        <section class="glass-panel max-h-[88vh] w-full max-w-3xl overflow-y-auto p-5">
          <header class="flex justify-between gap-4">
            <div>
              <h2 class="text-lg font-bold text-white">
                订阅《{{ detail.work.title }}》
              </h2><p class="mt-1 text-sm text-white/45">
                配置会冻结到订阅；以后自动找缺失、搜索、下载并按目标媒体库入库。
              </p>
            </div><button class="glass-button px-3 py-2" :disabled="subscribing" @click="followOpen = false">
              取消
            </button>
          </header>
          <div v-if="followLoading" class="py-12 text-center text-white/45">
            正在读取 Server 订阅选项…
          </div>
          <div v-else-if="followDefaults" class="mt-5 space-y-5">
            <div>
              <label class="text-xs text-white/48">订阅季</label><div class="mt-2 flex flex-wrap gap-2">
                <button v-for="season in Math.max(detail.seasonCount || 1, 1)" :key="season" type="button" class="glass-button px-3 py-2 text-sm" :class="{ 'bg-white text-black': followForm.seasons.includes(season) }" @click="toggleSeason(season)">
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
                <button v-for="site in followDefaults.sites" :key="site.id" type="button" class="rounded-xl border p-3 text-left text-sm" :class="followForm.siteIds.includes(site.id) ? 'border-white/45 bg-white/10' : 'border-white/8'" @click="toggleFollowSite(site.id)">
                  {{ site.name }}<span class="float-right">{{ followForm.siteIds.includes(site.id) ? '✓' : '' }}</span>
                </button>
              </div>
            </div>
            <div class="grid gap-3 md:grid-cols-2">
              <label class="text-xs text-white/48">下载器<select v-model="followForm.downloaderId" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-white"><option v-for="item in followDefaults.downloaders" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">目标媒体库<select v-model="followForm.libraryId" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-white"><option v-for="item in followDefaults.mediaLibraries" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">检查间隔（分钟）<input v-model.number="followForm.minutes" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-white" type="number" min="10" max="10080"></label><label class="text-xs text-white/48">每次最多下载资源数<input v-model.number="followForm.maxResources" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-white" type="number" min="1" max="20"></label>
            </div>
            <footer class="flex justify-end">
              <button class="glass-button bg-white px-6 py-2.5 font-bold text-black" :disabled="subscribing || !followForm.seasons.length || !followForm.siteIds.length || !followForm.downloaderId || !followForm.libraryId" @click="createFollow">
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
.discovery-hero { background-position: center; background-size: cover; }
select option { background: #11151d; color: white; }
</style>
