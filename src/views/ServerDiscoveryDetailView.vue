<script setup lang="ts">
import type { ServerCoverageSummary, ServerDiscoveryDetail, ServerDownloadOption, ServerLibraryOption, ServerProfileOption, ServerResourceGroup, ServerResourceItem, ServerSearchSite } from '@/services/serverDiscovery'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ServerDataSource } from '@/services/datasource/server'
import { publishFeedback } from '@/services/mediaActions'
import { getServerCoverage, getServerDiscoveryDetail, getServerDownloadOptions, getServerSearchSites, searchServerResources } from '@/services/serverDiscovery'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const detail = ref<ServerDiscoveryDetail | null>(null)
const coverage = ref<ServerCoverageSummary | null>(null)
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
const downloadingToken = ref('')
const error = ref('')
const sitePickerOpen = ref(false)
const pendingSearchMode = ref<'aggregate' | 'direct'>('aggregate')
const directKind = ref<'title' | 'tmdb'>('title')
const directQuery = ref('')

const sourceId = computed(() => String(route.params.sourceId))
const provider = computed(() => route.params.provider === 'douban' ? 'douban' : 'tmdb')
const mediaType = computed(() => route.params.mediaType === 'tv' ? 'tv' : 'movie')
const providerId = computed(() => String(route.params.providerId))
const searchableSites = computed(() => sites.value.filter(site => site.searchable))
const selectedAll = computed(() => searchableSites.value.length > 0 && searchableSites.value.every(site => selectedSiteIds.value.includes(site.id)))
const resources = computed(() => groups.value.flatMap(group => group.items.map(item => ({ group, item }))))
const coverageLabel = computed(() => {
  if (!coverage.value)
    return '媒体库覆盖未知'
  if (mediaType.value === 'movie')
    return coverage.value.present ? '已入库' : '尚未入库'
  return `已入库 ${coverage.value.present} / ${coverage.value.total} 集 · 缺失 ${coverage.value.missing} 集`
})

async function resolveSource(): Promise<ServerDataSource> {
  store.loadConfigs()
  await store.syncManager()
  const config = store.orderedConfigs.find(item => item.id === sourceId.value && item.type === 'server' && item.enabled !== false)
  const source = store.getSource(sourceId.value)
  if (!config || !(source instanceof ServerDataSource))
    throw new Error('对应 Server 尚未连接，请先在设置中登录。')
  await source.test()
  return source
}

async function load() {
  loading.value = true
  error.value = ''
  groups.value = []
  try {
    const source = await resolveSource()
    detail.value = await getServerDiscoveryDetail(source, provider.value, mediaType.value, providerId.value)
    directQuery.value = detail.value.work.title
    const [siteResult, optionsResult] = await Promise.all([getServerSearchSites(source), getServerDownloadOptions(source)])
    sites.value = siteResult
    selectedSiteIds.value = siteResult.filter(site => site.searchable).map(site => site.id)
    downloaders.value = optionsResult.downloaders
    libraries.value = optionsResult.libraries
    profiles.value = optionsResult.profiles
    downloaderId.value = downloaders.value[0]?.id ?? ''
    libraryId.value = libraries.value[0]?.id
    profileId.value = profiles.value[0]?.id ?? 0
    if (detail.value.work.tmdbId)
      coverage.value = await getServerCoverage(source, mediaType.value, detail.value.work.tmdbId).catch(() => null)
  }
  catch (reason) {
    error.value = message(reason)
  }
  finally {
    loading.value = false
  }
}

function chooseSearch(mode: 'aggregate' | 'direct') {
  pendingSearchMode.value = mode
  sitePickerOpen.value = true
}
function toggleAllSites() {
  selectedSiteIds.value = selectedAll.value ? [] : searchableSites.value.map(site => site.id)
}
function toggleSite(id: number) {
  selectedSiteIds.value = selectedSiteIds.value.includes(id) ? selectedSiteIds.value.filter(value => value !== id) : [...selectedSiteIds.value, id]
}

async function runSearch() {
  if (!detail.value || selectedSiteIds.value.length === 0)
    return
  const tmdbId = detail.value.work.tmdbId
  if (pendingSearchMode.value === 'aggregate' && !tmdbId) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '该作品还没有可验证的 TMDB ID，请使用直接搜索。' })
    return
  }
  searching.value = true
  error.value = ''
  groups.value = []
  sitePickerOpen.value = false
  try {
    const source = await resolveSource()
    const useTMDB = pendingSearchMode.value === 'direct' && directKind.value === 'tmdb'
    groups.value = await searchServerResources(source, {
      mediaType: mediaType.value,
      tmdbId: pendingSearchMode.value === 'aggregate' || useTMDB ? tmdbId : undefined,
      title: pendingSearchMode.value === 'direct' && !useTMDB ? directQuery.value : undefined,
      direct: pendingSearchMode.value === 'direct',
      siteIds: selectedSiteIds.value,
    })
  }
  catch (reason) {
    error.value = message(reason)
  }
  finally {
    searching.value = false
  }
}

async function download(item: ServerResourceItem) {
  if (!downloaderId.value || !profileId.value) {
    publishFeedback({ id: Date.now(), kind: 'error', message: '请先选择下载器和分类规则。' })
    return
  }
  downloadingToken.value = item.token
  try {
    const source = await resolveSource()
    await source.createDiscoveryDownload({ result_token: item.token, downloader_id: downloaderId.value, media_library_id: libraryId.value, profile_id: profileId.value, priority: 0 })
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
onMounted(load)
watch(() => route.fullPath, load)
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
      <article class="discovery-hero glass-panel relative overflow-hidden" :style="detail.work.backdropUrl ? { backgroundImage: `linear-gradient(90deg,rgba(8,10,16,.96),rgba(8,10,16,.58)),url(${detail.work.backdropUrl})` } : undefined">
        <div class="grid gap-6 p-6 md:grid-cols-[12rem_minmax(0,1fr)]">
          <div class="aspect-[2/3] overflow-hidden rounded-lg bg-white/6">
            <img v-if="detail.work.posterUrl" :src="detail.work.posterUrl" :alt="detail.work.title" class="h-full w-full object-cover"><div v-else class="grid h-full place-items-center p-4 text-center text-white/40">
              暂无海报
            </div>
          </div><div class="self-end">
            <div class="flex flex-wrap gap-2 text-xs text-white/55">
              <span class="rounded-full bg-white/8 px-3 py-1">{{ mediaType === 'tv' ? '电视剧' : '电影' }}</span><span class="rounded-full bg-white/8 px-3 py-1">{{ coverageLabel }}</span><span v-if="detail.work.tmdbId" class="rounded-full bg-white/8 px-3 py-1">TMDB {{ detail.work.tmdbId }}</span>
            </div><h1 class="mt-3 text-3xl font-extrabold text-white">
              {{ detail.work.title }}
            </h1><p v-if="detail.work.originalTitle" class="mt-1 text-sm text-white/48">
              {{ detail.work.originalTitle }}
            </p><p class="mt-4 max-w-3xl text-sm leading-6 text-white/62">
              {{ detail.work.overview || '暂无简介。' }}
            </p><p class="mt-3 text-sm text-white/48">
              {{ [detail.work.year, detail.runtimeMinutes ? `${detail.runtimeMinutes} 分钟` : '', ...detail.genres].filter(Boolean).join(' · ') }}
            </p><div class="mt-5 flex flex-wrap gap-3">
              <button class="glass-button bg-white px-5 py-2.5 font-bold text-black" type="button" :disabled="searching" @click="chooseSearch('aggregate')">
                搜索
              </button><button class="glass-button px-5 py-2.5 font-bold" type="button" :disabled="searching" @click="chooseSearch('direct')">
                直接搜索
              </button>
            </div>
          </div>
        </div>
      </article>

      <div class="glass-panel mt-5 grid gap-3 p-4 md:grid-cols-3">
        <label class="text-xs text-white/48">下载器<select v-model="downloaderId" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-sm text-white"><option value="">请选择</option><option v-for="item in downloaders" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">目标媒体库<select v-model="libraryId" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-sm text-white"><option :value="undefined">仅下载，不自动入库</option><option v-for="item in libraries" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label class="text-xs text-white/48">分类规则<select v-model="profileId" class="mt-2 w-full rounded-lg bg-black/35 p-3 text-sm text-white"><option v-for="item in profiles" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
      </div>

      <div v-if="searching" class="glass-panel mt-5 p-10 text-center text-white/55">
        正在按多语言标题聚合所选站点…
      </div>
      <div v-else-if="error" class="mt-5 rounded-xl border border-red-400/20 bg-red-400/10 p-4 text-sm text-red-100">
        {{ error }}
      </div>
      <section v-if="resources.length" class="mt-5 space-y-4">
        <header>
          <h2 class="text-xl font-bold text-white">
            站点资源
          </h2><p class="mt-1 text-sm text-white/45">
            共 {{ resources.length }} 个可提交资源；下载和入库仍由 Server 统一执行。
          </p>
        </header><article v-for="group in groups" :key="group.siteId" class="glass-panel p-4">
          <div class="mb-3 flex items-center justify-between">
            <h3 class="font-bold text-white">
              {{ group.siteName }}
            </h3><span class="text-xs text-white/42">{{ group.items.length }} 个结果</span>
          </div><div class="space-y-2">
            <div v-for="item in group.items" :key="item.token" class="flex flex-col gap-3 rounded-xl border border-white/8 bg-black/18 p-4 md:flex-row md:items-center">
              <div class="min-w-0 flex-1">
                <h4 class="break-words text-sm font-semibold text-white/90">
                  {{ item.title }}
                </h4><p class="mt-2 text-xs text-white/42">
                  {{ [formatBytes(item.sizeBytes), item.seeders != null ? `${item.seeders} 做种` : '', item.promotion, item.quality, item.matchedName ? `命中：${item.matchedName}` : ''].filter(Boolean).join(' · ') }}
                </p>
              </div><button class="glass-button shrink-0 px-4 py-2 text-sm font-bold" :disabled="downloadingToken === item.token" @click="download(item)">
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
              关闭
            </button>
          </header><div v-if="pendingSearchMode === 'direct'" class="mt-4 rounded-xl border border-white/8 bg-black/20 p-4">
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
            <button class="glass-button bg-white px-6 py-2.5 font-bold text-black" :disabled="selectedSiteIds.length === 0 || pendingSearchMode === 'direct' && directKind === 'title' && !directQuery.trim()" @click="runSearch">
              搜索
            </button>
          </footer>
        </section>
      </div>
    </template>
  </section>
</template>

<style scoped>
.discovery-hero { background-position: center; background-size: cover; }
select option { background: #11151d; color: white; }
</style>
