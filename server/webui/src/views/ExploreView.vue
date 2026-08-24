<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import { compatibleDownloadLibraries, formatBytes } from '@/downloads'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import { discoveryDownloadsPath, ptSearchPath, ptSearchStreamPath, ptSearchURL, upsertPTGroup, type PTSearchGroup, type PTSearchResponse, type PTSearchResult } from '@/sites'
import type { DownloaderSummary, ListResponse, MediaLibraryDetail, StorageSummary } from '@/types/api'

const route = useRoute()
const auth = useAuthStore()
const keyword = ref(typeof route.query.title === 'string' ? route.query.title : '')
const mediaType = ref(typeof route.query.media_type === 'string' ? route.query.media_type : '')
const year = ref<number | undefined>(typeof route.query.year === 'string' ? Number(route.query.year) || undefined : undefined)
const tmdbID = ref<number | undefined>(typeof route.query.tmdb_id === 'string' ? Number(route.query.tmdb_id) || undefined : undefined)
const searchBy = ref<'title' | 'tmdb_id'>(route.query.search_by === 'tmdb_id' ? 'tmdb_id' : 'title')
const selectedTitle = computed(() => typeof route.query.title === 'string' ? route.query.title : '')
const groups = ref<PTSearchGroup[]>([])
const searching = ref(false)
const searchError = ref('')
const searched = ref(false)
const downloaders = ref<DownloaderSummary[]>([])
const libraries = ref<MediaLibraryDetail[]>([])
const storages = ref<StorageSummary[]>([])
const downloadDialog = ref<PTSearchResult | null>(null)
const downloadForm = ref({ downloaderID: '', mediaLibraryID: 0, priority: 0 })
const submitting = ref(false)
let source: EventSource | null = null
let streamTimeout: number | undefined

const enabledDownloaders = computed(() => downloaders.value.filter(item => item.enabled))
const selectedDownloader = computed(() => enabledDownloaders.value.find(item => item.id === downloadForm.value.downloaderID) ?? null)
const compatibleLibraries = computed(() => compatibleDownloadLibraries(libraries.value, storages.value, selectedDownloader.value, false))
const selectedLibrary = computed(() => downloadForm.value.mediaLibraryID === 0 ? compatibleLibraries.value[0] ?? null : compatibleLibraries.value.find(item => item.id === downloadForm.value.mediaLibraryID) ?? null)

function searchInput(siteID?: number, page = 1) {
  return { keyword: keyword.value, mediaType: mediaType.value || undefined, year: year.value, tmdbID: tmdbID.value, searchBy: searchBy.value, page, siteID }
}

function stopStream() {
  source?.close()
  source = null
  if (streamTimeout !== undefined) window.clearTimeout(streamTimeout)
  streamTimeout = undefined
}

async function searchJSON(siteID?: number, page = 1, append = false) {
  try {
    const response = await api<PTSearchResponse>(ptSearchURL(ptSearchPath, searchInput(siteID, page)))
    for (const group of response.groups) groups.value = upsertPTGroup(groups.value, group, append)
    searched.value = true
  } catch (reason) { searchError.value = message(reason) }
  finally { searching.value = false }
}

function search() {
  if (searchBy.value === 'title' && !keyword.value.trim()) { notify('请输入作品或发行标题', 'warning'); return }
  if (searchBy.value === 'tmdb_id' && (!tmdbID.value || !mediaType.value)) { notify('TMDB ID 搜索需要有效 ID 与媒体类型', 'warning'); return }
  stopStream()
  groups.value = []
  searchError.value = ''
  searched.value = false
  searching.value = true
  if (typeof EventSource === 'undefined') { void searchJSON(); return }
  let delivered = false
  const eventSource = new EventSource(ptSearchURL(ptSearchStreamPath, searchInput()))
  source = eventSource
  eventSource.addEventListener('site', event => {
    try {
      const group = JSON.parse((event as MessageEvent<string>).data) as PTSearchGroup
      groups.value = upsertPTGroup(groups.value, group)
      delivered = true
      searched.value = true
    } catch { /* malformed events are ignored; JSON fallback remains available */ }
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
  }, 30_000)
}

async function retrySite(group: PTSearchGroup) {
  searching.value = true
  searchError.value = ''
  await searchJSON(group.site_id, 1)
}

async function nextPage(group: PTSearchGroup) {
  searching.value = true
  await searchJSON(group.site_id, group.page + 1, true)
}

async function loadDownloadOptions() {
  const requests: Promise<void>[] = []
  if (auth.can(Permissions.DownloadersRead)) requests.push(api<ListResponse<DownloaderSummary>>('/api/v1/downloaders').then(response => { downloaders.value = response.list }))
  if (auth.can(Permissions.MediaLibrariesRead)) requests.push(api<ListResponse<MediaLibraryDetail>>('/api/v1/media-libraries').then(response => { libraries.value = response.list }))
  if (auth.can(Permissions.StoragesRead)) requests.push(api<ListResponse<StorageSummary>>('/api/v1/storages').then(response => { storages.value = response.list }))
  await Promise.all(requests)
  if (!enabledDownloaders.value.some(item => item.id === downloadForm.value.downloaderID)) downloadForm.value.downloaderID = enabledDownloaders.value[0]?.id ?? ''
}

async function openDownload(item: PTSearchResult) {
  downloadDialog.value = item
  downloadForm.value = { downloaderID: '', mediaLibraryID: 0, priority: 0 }
  try { await loadDownloadOptions() }
  catch (reason) { notify(message(reason), 'error') }
}

async function submitDownload() {
  const item = downloadDialog.value
  if (!item || !downloadForm.value.downloaderID) { notify('请选择已启用的下载器', 'warning'); return }
  if (!selectedLibrary.value) { notify('没有与所选下载器兼容的媒体库', 'warning'); return }
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
function message(reason: unknown) { return reason instanceof Error ? reason.message : 'PT 搜索暂时不可用' }

onMounted(() => { if (keyword.value.trim() || (searchBy.value === 'tmdb_id' && tmdbID.value)) search() })
onBeforeUnmount(stopStream)
</script>

<template>
  <section class="space-y-5">
    <header><p class="text-xs font-700 uppercase tracking-widest text-[var(--text-subtle)]">Explore</p><h1 class="mt-1 text-2xl font-800">探索与 PT 搜索</h1><p class="page-description mt-1">从推荐作品或关键词查询已启用站点；浏览器只收到 15 分钟有效的不透明结果令牌。</p></header>
    <form class="panel" @submit.prevent="search">
      <div class="mb-4 flex flex-wrap gap-2" role="group" aria-label="搜索方式"><button type="button" class="btn-secondary" :class="{ '!border-[var(--accent)] !bg-[var(--accent-soft)] !text-[var(--accent)]': searchBy === 'title' }" @click="searchBy = 'title'">按标题</button><button type="button" class="btn-secondary" :class="{ '!border-[var(--accent)] !bg-[var(--accent-soft)] !text-[var(--accent)]': searchBy === 'tmdb_id' }" @click="searchBy = 'tmdb_id'">按 TMDB ID</button></div>
      <div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_10rem_8rem_auto] md:items-end">
        <div v-if="searchBy === 'title'"><label class="label" for="discovery-keyword">作品或发行标题</label><input id="discovery-keyword" v-model="keyword" class="input" maxlength="160" placeholder="七武士 / Seven Samurai" required /></div>
        <div v-else><label class="label" for="discovery-tmdb">TMDB ID</label><input id="discovery-tmdb" v-model.number="tmdbID" class="input font-mono" type="number" min="1" placeholder="346" required /></div>
        <div><label class="label" for="discovery-kind">媒体类型</label><select id="discovery-kind" v-model="mediaType" class="input"><option value="">自动</option><option value="movie">电影</option><option value="tv">剧集</option></select></div>
        <div><label class="label" for="discovery-year">年份</label><input id="discovery-year" v-model.number="year" class="input" type="number" min="1880" max="2200" placeholder="可选" /></div>
        <button class="btn-primary" :disabled="searching">{{ searching ? '搜索中…' : '搜索 PT 资源' }}</button>
      </div>
    </form>
    <div v-if="selectedTitle" class="panel"><span class="status-chip">已选作品</span><h2 class="mt-3 text-xl font-750">{{ selectedTitle }}</h2><p class="mt-1 text-sm text-muted">推荐来源只帮助确认作品身份，真实种子搜索仍按站点分别执行。</p></div>
    <div v-if="searchError" class="semantic-error p-4"><strong>搜索失败</strong><p class="mt-1 text-sm">{{ searchError }}</p><button class="btn-secondary mt-3" @click="search">重试全部站点</button></div>
    <div v-if="searching && !groups.length" class="panel py-10 text-center text-muted">正在按站点限速并行搜索，结果会渐进出现…</div>
    <div v-else-if="searched && !groups.length && !searchError" class="panel py-10 text-center text-muted">没有启用的 PT 站点，或当前关键词暂无结果。可以先到“站点管理”添加 PTTime。</div>

    <article v-for="group in groups" :key="group.site_id" class="panel overflow-hidden p-0">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] px-5 py-4"><div class="flex flex-wrap items-center gap-2"><h2 class="m-0 text-lg">{{ group.site_name }}</h2><span :class="group.status === 'success' ? 'status-chip status-chip--ready' : 'status-chip status-chip--warning'">{{ group.status === 'success' ? `${group.items.length} 条结果` : '搜索失败' }}</span><span v-if="group.skipped" class="status-chip">跳过 {{ group.skipped }} 条畸形数据</span></div><button v-if="group.status === 'error'" class="btn-secondary" :disabled="searching" @click="retrySite(group)">只重试此站</button></header>
      <div v-if="group.status === 'error'" class="p-5 text-sm text-muted">该站点暂时不可用（<span class="font-mono">{{ group.error_code || 'site_unavailable' }}</span>），不影响其它站点结果。</div>
      <div v-else-if="!group.items.length" class="p-5 text-sm text-muted">此页没有匹配结果。</div>
      <div v-else class="divide-y divide-[var(--border)]">
        <article v-for="item in group.items" :key="item.token" class="grid gap-4 px-5 py-4 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
          <div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><strong class="break-words">{{ item.title }}</strong><span v-if="item.promotion" class="status-chip status-chip--ready">{{ item.promotion.toUpperCase() }}</span><span v-if="item.quality" class="status-chip">{{ item.quality }}</span></div><p v-if="item.subtitle" class="text-subtle mb-0 mt-1 text-xs">{{ item.subtitle }}</p><div class="text-subtle mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs"><span>{{ formatBytes(item.size_bytes ?? null) }}</span><span>{{ formatTime(item.published_at) }}</span><span>做种 {{ count(item.seeders) }}</span><span>下载 {{ count(item.leechers) }}</span><span>完成 {{ count(item.completed) }}</span></div></div>
          <button class="btn-primary" :disabled="!auth.can(Permissions.DownloadsCreate)" @click="openDownload(item)">选择下载器并入队</button>
        </article>
      </div>
      <footer v-if="group.status === 'success' && group.has_next" class="border-t border-[var(--border)] p-4 text-center"><button class="btn-secondary" :disabled="searching" @click="nextPage(group)">加载此站下一页</button></footer>
    </article>

    <div v-if="downloadDialog" class="modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4" @click.self="!submitting && (downloadDialog = null)">
      <form class="panel w-full max-w-xl" role="dialog" aria-modal="true" aria-labelledby="pt-download-title" @submit.prevent="submitDownload">
        <div class="flex items-start justify-between gap-3"><div><h2 id="pt-download-title" class="m-0 text-xl">创建下载任务</h2><p class="page-description mt-1 line-clamp-2 text-sm">{{ downloadDialog.title }}</p></div><button class="btn-secondary" type="button" :disabled="submitting" @click="downloadDialog = null">关闭</button></div>
        <div class="mt-5 grid gap-4 sm:grid-cols-2">
          <div><label class="label">下载器</label><select v-model="downloadForm.downloaderID" class="input" required><option value="" disabled>请选择</option><option v-for="item in enabledDownloaders" :key="item.id" :value="item.id">{{ item.name }} · {{ item.type === 'pan115_offline' ? '115 离线' : item.type }}</option></select></div>
          <div><label class="label">目标媒体库</label><select v-model.number="downloadForm.mediaLibraryID" class="input" required><option :value="0">自动选择（按媒体库顺序）</option><option v-for="library in compatibleLibraries" :key="library.id" :value="library.id">{{ library.name }} · {{ library.storage_name }}</option></select></div>
          <div class="sm:col-span-2"><label class="label">队列优先级</label><input v-model.number="downloadForm.priority" class="input" type="number" min="-100" max="100" /></div>
        </div>
        <div v-if="selectedLibrary" class="semantic-inset mt-4 grid gap-3 p-4 text-sm sm:grid-cols-2"><div><span class="text-subtle block text-xs">最终媒体库</span><strong>{{ selectedLibrary.name }}</strong></div><div><span class="text-subtle block text-xs">分类与入库</span><strong>{{ selectedLibrary.profile_name }} · {{ selectedLibrary.transfer_mode }} · {{ selectedLibrary.conflict_policy }}</strong></div></div>
        <p v-else class="semantic-warning mt-4 p-3 text-sm">没有与当前下载器兼容且启用的媒体库。请先完成媒体库与下载器配置。</p>
        <p class="text-subtle mt-4 text-xs">确认后 Server 才会凭短期令牌获取种子，并复用现有下载 → 识别 → 整理 → 入库流水线。真实种子地址和 passkey 不会进入页面。</p>
        <div class="mt-5 flex justify-end gap-3"><button class="btn-secondary" type="button" :disabled="submitting" @click="downloadDialog = null">取消</button><button class="btn-primary" :disabled="submitting || !downloadForm.downloaderID || !selectedLibrary">{{ submitting ? '正在获取种子并入队…' : '确认并入队' }}</button></div>
      </form>
    </div>
  </section>
</template>
