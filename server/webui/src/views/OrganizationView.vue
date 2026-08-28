<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { APIError, api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import MediaReorganizationDialog from '@/components/MediaReorganizationDialog.vue'
import TransferDeletionDialog from '@/components/TransferDeletionDialog.vue'
import { controlJob, respondAction } from '@/jobs'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import {
  canDeleteTransferRecord,
  canRetargetTransfer,
  conflictPolicyLabels,
  formatTransferProgress,
  getTransfer,
  listTransfers,
  retargetCompletedImport,
  shouldRefreshTransferEvent,
  transferModeLabels,
  transferIdentityLabel,
  transferPhaseDescription,
  transferRouteLabel,
  transferStatusClass,
  transferStatusLabel,
  type TransferDetail,
  type TransferFilterStatus,
  type TransferListScope,
  type TransferMode,
  type TransferPage,
  type TransferSummary,
} from '@/transfers'
import type { ListResponse, MediaLibraryDetail } from '@/types/api'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const emptyStats = () => ({ processing: 0, waiting_action: 0, failed: 0, completed_today: 0 })

const items = ref<TransferSummary[]>([])
const total = ref(0)
const stats = ref(emptyStats())
const filterLibraries = ref<Array<{ id: number; name: string }>>([])
const filterCategories = ref<string[]>([])
const mediaLibraries = ref<MediaLibraryDetail[]>([])
const loading = ref(true)
const detailLoading = ref(false)
const saving = ref(false)
const error = ref('')
const selected = ref<TransferDetail | null>(null)
const retargeting = ref<TransferSummary | null>(null)
const reorganizing = ref<TransferSummary | null>(null)
const deleting = ref<TransferSummary | null>(null)
const retargetLibraryID = ref(0)
const drawer = ref<HTMLElement | null>(null)
const page = ref(readPositiveQuery('page', 1))
const pageSize = 30
const scope = ref<TransferListScope>(readScopeQuery())
const status = ref<TransferFilterStatus>(readStatusQuery())
const libraryID = ref(readStringQuery('library_id'))
const category = ref(readStringQuery('category'))
const transferMode = ref<'' | TransferMode>(readModeQuery())
const keyword = ref(readStringQuery('keyword'))
let drawerTrigger: HTMLElement | null = null
let refreshTimer: number | undefined
let debounceTimer: number | undefined
let socket: WebSocket | undefined

const canRespond = computed(() => {
  if (!selected.value || !auth.can(Permissions.JobsRespond)) return false
  return auth.can(Permissions.JobsReadAll) || (selected.value.owner_id === auth.user?.id && auth.can(Permissions.JobsReadOwn))
})

function readStringQuery(key: string): string {
  const value = route.query[key]
  return typeof value === 'string' ? value : ''
}

function readPositiveQuery(key: string, fallback: number): number {
  const value = Number(readStringQuery(key))
  return Number.isInteger(value) && value > 0 ? value : fallback
}

function readStatusQuery(): TransferFilterStatus {
  const value = readStringQuery('status')
  return ['', 'processing', 'waiting_action', 'paused', 'failed', 'completed', 'cancelled'].includes(value) ? value as TransferFilterStatus : ''
}

function readScopeQuery(): TransferListScope {
  return readStringQuery('scope') === 'history' ? 'history' : 'active'
}

function readModeQuery(): '' | TransferMode {
  const value = readStringQuery('transfer_mode')
  return ['', 'move', 'copy', 'symlink'].includes(value) ? value as '' | TransferMode : ''
}

function queryParams(): URLSearchParams {
  const query = new URLSearchParams({ scope: scope.value, page: String(page.value), page_size: String(pageSize) })
  if (status.value) query.set('status', status.value)
  if (libraryID.value) query.set('library_id', libraryID.value)
  if (category.value.trim()) query.set('category', category.value.trim())
  if (transferMode.value) query.set('transfer_mode', transferMode.value)
  if (keyword.value.trim()) query.set('keyword', keyword.value.trim())
  return query
}

async function changeScope(next: TransferListScope) {
  scope.value = next
  status.value = ''
  page.value = 1
  await syncRoute('')
  await load()
}

async function syncRoute(taskID = selected.value?.id ?? readStringQuery('task')) {
  const query = Object.fromEntries(queryParams()) as Record<string, string>
  if (taskID) query.task = taskID
  await router.replace({ name: 'organization', query })
}

async function load(showLoading = true, quiet = false) {
  if (showLoading) loading.value = true
  if (!quiet) error.value = ''
  try {
    const result: TransferPage = await listTransfers(queryParams())
    items.value = result.list
    total.value = result.total
    stats.value = result.stats
    filterLibraries.value = result.filter_options.libraries
    filterCategories.value = result.filter_options.categories
    const requested = readStringQuery('task')
    let detailLoaded = false
    if (requested && selected.value?.id !== requested) {
      detailLoaded = await openByID(requested, false)
      if (!detailLoaded) await syncRoute('')
    }
    if (selected.value && !detailLoaded && !(await refreshDetail(selected.value.id))) await syncRoute('')
  } catch (reason) {
    if (!quiet) error.value = message(reason)
  } finally {
    if (showLoading) loading.value = false
  }
}

async function applyFilters() {
  page.value = 1
  await syncRoute()
  await load()
}

async function changePage(next: number) {
  if (next < 1 || (next - 1) * pageSize >= total.value) return
  page.value = next
  await syncRoute()
  await load()
}

async function open(item: TransferSummary) {
  drawerTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  await openByID(item.id, true)
}

async function openByID(id: string, updateRoute: boolean): Promise<boolean> {
  detailLoading.value = true
  await nextTick()
  document.querySelector<HTMLElement>('.organization-drawer .icon-button')?.focus()
  try {
    selected.value = await getTransfer(id)
    if (updateRoute) await syncRoute(id)
    return true
  } catch (reason) {
    notify(message(reason), 'error')
    if (updateRoute) await closeDrawer()
    return false
  } finally {
    detailLoading.value = false
  }
}

async function refreshDetail(id: string): Promise<boolean> {
  try {
    selected.value = await getTransfer(id)
    return true
  } catch {
    selected.value = null
    return false
  }
}

async function closeDrawer() {
  selected.value = null
  await syncRoute('')
  window.setTimeout(() => drawerTrigger?.focus())
}

function canControl(item: TransferSummary): boolean {
  return auth.can(Permissions.JobsControlAll) || (item.owner_id === auth.user?.id && auth.can(Permissions.JobsControlOwn))
}

function canDelete(item: TransferSummary): boolean {
  return canControl(item) && canDeleteTransferRecord(item)
}

function canReorganize(item: TransferSummary): boolean {
  return canControl(item) && item.phase === 'completed' && item.job_status === 'completed' && item.identity_revision > 0
}

function openReorganization(item: TransferSummary) {
  reorganizing.value = item
}

async function reorganizationQueued() {
  await load(false, true)
  if (selected.value) await refreshDetail(selected.value.id)
}

function remove(item: TransferSummary) {
  deleting.value = item
}

async function deletionCompleted() {
  const deletedID = deleting.value?.id
  deleting.value = null
  if (selected.value?.id === deletedID) {
    selected.value = null
    await syncRoute('')
  }
  await load(false, true)
}

async function retry(item: TransferSummary) {
  if (!window.confirm(`确认重试“${item.display_name}”的入库阶段？已完成的下载不会重新执行。`)) return
  saving.value = true
  try {
    await controlJob(item.job_id, 'retry')
    notify('入库任务已重新排队', 'success')
    await load(false, true)
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    saving.value = false
  }
}

function openRetarget(item: TransferSummary) {
  retargeting.value = item
  retargetLibraryID.value = mediaLibraries.value.find(library => library.enabled && library.id !== item.library_id)?.id ?? 0
}

async function confirmRetarget() {
  if (!retargeting.value || !retargetLibraryID.value) return
  saving.value = true
  try {
    await retargetCompletedImport(retargeting.value.download_task_id, retargetLibraryID.value)
    notify('已更新目标媒体库，只重新执行入库阶段', 'success')
    retargeting.value = null
    await load(false, true)
  } catch (reason) {
    notify(message(reason), 'error')
  } finally { saving.value = false }
}

async function respond(response: string) {
  if (!selected.value?.job.action_request) return
  saving.value = true
  try {
    await respondAction(selected.value.job, response)
    notify('冲突处理方式已提交，入库任务会继续执行', 'success')
    await load(false, true)
  } catch (reason) {
    if (reason instanceof APIError && reason.errorCode === 'queue_action_stale') {
      notify('等待操作已经变化，已刷新最新状态', 'warning')
      await refreshDetail(selected.value.id)
    } else {
      notify(message(reason), 'error')
    }
  } finally {
    saving.value = false
  }
}

function scheduleRefresh() {
  if (debounceTimer !== undefined) window.clearTimeout(debounceTimer)
  debounceTimer = window.setTimeout(() => { void load(false, true) }, 400)
}

function startLiveUpdates() {
  refreshTimer = window.setInterval(() => { void load(false, true) }, 15_000)
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  socket = new WebSocket(`${protocol}//${window.location.host}/api/v1/jobs/events/ws`)
  socket.onmessage = event => {
    const visibleJobIDs = new Set(items.value.map(item => item.job_id))
    if (selected.value) visibleJobIDs.add(selected.value.job_id)
    if (shouldRefreshTransferEvent(event.data, visibleJobIDs)) scheduleRefresh()
  }
  socket.onclose = () => { socket = undefined }
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === 'Escape' && selected.value) void closeDrawer()
}

function trapDrawerFocus(event: KeyboardEvent) {
  if (event.key !== 'Tab' || !drawer.value) return
  const focusable = [...drawer.value.querySelectorAll<HTMLElement>('button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])')]
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}

function actionLabel(value: string): string {
  return ({ overwrite: '覆盖', skip: '跳过', rename: '自动改名' } as Record<string, string>)[value] ?? value
}

function resultLabel(value: string): string {
  return ({ planned: '已规划', completed: '已完成', skipped: '已跳过' } as Record<string, string>)[value] ?? value
}

function formatDate(value: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '时间未知' : date.toLocaleString('zh-CN', { hour12: false })
}

function formatSize(value: number): string {
  if (!Number.isFinite(value) || value < 0) return '未知'
  if (value < 1024) return `${value} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let amount = value / 1024
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount.toFixed(1)} ${units[index]}`
}

function message(reason: unknown): string {
  return reason instanceof Error ? reason.message : '操作失败'
}

onMounted(async () => {
  window.addEventListener('keydown', handleEscape)
  if (auth.can(Permissions.MediaLibrariesRead)) {
    try { mediaLibraries.value = (await api<ListResponse<MediaLibraryDetail>>('/api/v1/media-libraries')).list } catch { mediaLibraries.value = [] }
  }
  await load()
  startLiveUpdates()
})

onBeforeUnmount(() => {
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer)
  if (debounceTimer !== undefined) window.clearTimeout(debounceTimer)
  socket?.close()
  window.removeEventListener('keydown', handleEscape)
})
</script>

<template>
  <section class="mx-auto max-w-[96rem]">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="m-0 text-3xl font-800">媒体整理</h1>
        <p class="page-description mt-2 max-w-3xl">查看下载完成后自动产生的识别、命名和入库记录。手动选择文件并整理属于后续文件管理，不在这里创建。</p>
      </div>
      <div class="flex gap-2"><RouterLink class="btn-secondary" to="/automation/downloads">下载管理</RouterLink><button class="btn-secondary" type="button" @click="load()">刷新</button></div>
    </header>

    <nav class="management-tabs mt-6" role="tablist" aria-label="媒体整理记录范围"><button id="organization-tab-active" class="management-tab" :class="scope === 'active' ? 'management-tab--active' : ''" type="button" role="tab" :aria-selected="scope === 'active'" aria-controls="organization-records" :tabindex="scope === 'active' ? 0 : -1" @click="changeScope('active')">进行中</button><button id="organization-tab-history" class="management-tab" :class="scope === 'history' ? 'management-tab--active' : ''" type="button" role="tab" :aria-selected="scope === 'history'" aria-controls="organization-records" :tabindex="scope === 'history' ? 0 : -1" @click="changeScope('history')">历史记录</button></nav>

    <div class="task-summary mt-6">
      <article class="panel"><small>处理中</small><strong>{{ stats.processing }}</strong></article>
      <article class="panel"><small>等待处理</small><strong>{{ stats.waiting_action }}</strong></article>
      <article class="panel"><small>失败</small><strong>{{ stats.failed }}</strong></article>
      <article class="panel"><small>今日完成</small><strong>{{ stats.completed_today }}</strong></article>
    </div>

    <form class="panel organization-filters mt-4" @submit.prevent="applyFilters">
      <label><span class="label">状态</span><select v-model="status" class="input"><option value="">{{ scope === 'history' ? '全部历史状态' : '全部进行中状态' }}</option><template v-if="scope === 'active'"><option value="processing">处理中</option><option value="waiting_action">等待处理</option><option value="paused">已暂停</option></template><template v-else><option value="failed">失败</option><option value="completed">已完成</option><option value="cancelled">已取消</option></template></select></label>
      <label><span class="label">目标媒体库</span><select v-model="libraryID" class="input"><option value="">全部媒体库</option><option v-for="library in filterLibraries" :key="library.id" :value="String(library.id)">{{ library.name }}</option></select></label>
      <label><span class="label">分类</span><input v-model.trim="category" class="input" maxlength="128" list="organization-categories" placeholder="全部分类" /><datalist id="organization-categories"><option v-for="value in filterCategories" :key="value" :value="value" /></datalist></label>
      <label><span class="label">入库方式</span><select v-model="transferMode" class="input"><option value="">全部方式</option><option value="move">移动</option><option value="copy">复制</option><option value="symlink">软链接</option></select></label>
      <label class="organization-keyword"><span class="label">标题</span><input v-model.trim="keyword" class="input" maxlength="256" placeholder="下载标题或识别标题" /></label>
      <button class="btn-primary" type="submit">筛选</button>
    </form>

    <p v-if="error" class="semantic-error mt-4 p-3" role="alert">{{ error }}</p>
    <div id="organization-records" class="panel mt-4 overflow-x-auto p-0" role="tabpanel" :aria-labelledby="`organization-tab-${scope}`">
      <p v-if="loading" class="p-6 text-muted">正在读取媒体整理记录…</p>
      <p v-else-if="items.length === 0" class="p-6 text-muted">{{ scope === 'history' ? '当前没有媒体整理历史记录。' : '当前没有进行中的自动整理任务。' }}</p>
      <table v-else class="semantic-table organization-table w-full">
        <thead><tr><th>媒体</th><th>识别 / 分类</th><th>目标</th><th>阶段</th><th>文件</th><th>更新时间</th><th>操作</th></tr></thead>
        <tbody><tr v-for="item in items" :key="item.id" :class="{ 'task-action-required': item.job_status === 'waiting_user_action' }">
          <td><button class="semantic-link text-left" type="button" @click="open(item)"><strong class="block">{{ item.display_name }}</strong><small class="text-subtle mt-1 block">{{ item.downloader_name }} · {{ item.provider_type }}</small></button></td>
          <td><strong class="block">{{ item.scrape_title || '未识别标题' }}</strong><small class="text-subtle mt-1 block">{{ item.scrape_media_type || '未知类型' }} · {{ item.scrape_category || '未分类' }}<template v-if="item.scrape_tmdb_id"> · TMDB {{ item.scrape_tmdb_id }}</template></small></td>
          <td><strong class="block">{{ item.library_name }}</strong><small class="text-subtle mt-1 block">{{ transferRouteLabel(item.route_kind) }} · {{ transferModeLabels[item.transfer_mode] }} · {{ conflictPolicyLabels[item.conflict_policy] }}</small></td>
          <td><span :class="transferStatusClass(item)">{{ transferStatusLabel(item) }}</span><small v-if="item.phase === 'risk_backoff'" class="semantic-warning-text mt-1 block">{{ transferPhaseDescription(item) }}</small><small v-if="item.last_error_message" class="semantic-danger-text mt-1 block">{{ item.last_error_message }}</small></td>
          <td>{{ formatTransferProgress(item) }}</td>
          <td>{{ formatDate(item.updated_at) }}</td>
          <td><div class="flex flex-wrap gap-2"><button class="btn-secondary" type="button" @click="open(item)">查看详情</button><button v-if="canReorganize(item)" class="btn-secondary" type="button" :disabled="saving" @click="openReorganization(item)">修正识别并重新整理</button><button v-if="canControl(item) && canRetargetTransfer(item)" class="btn-secondary" type="button" :disabled="saving" @click="openRetarget(item)">修改入库目标</button><button v-if="canControl(item) && item.job_status === 'failed'" class="btn-secondary" type="button" :disabled="saving" @click="retry(item)">重试入库</button><button v-if="canDelete(item)" class="btn-danger" type="button" :disabled="saving" @click="remove(item)">删除记录</button></div></td>
        </tr></tbody>
      </table>
      <div v-if="!loading && items.length" class="organization-mobile-list p-3">
        <article v-for="item in items" :key="item.id" class="semantic-inset p-3"><div class="flex items-start justify-between gap-3"><div><strong>{{ item.scrape_title || item.display_name }}</strong><small class="text-subtle mt-1 block">{{ item.library_name }} · {{ transferRouteLabel(item.route_kind) }} · {{ transferModeLabels[item.transfer_mode] }}</small></div><span :class="transferStatusClass(item)">{{ transferStatusLabel(item) }}</span></div><p class="text-subtle mb-0 mt-3 text-xs">{{ item.scrape_category || '未分类' }} · {{ formatTransferProgress(item) }} · {{ formatDate(item.updated_at) }}</p><div class="mt-3 flex flex-wrap gap-2"><button class="btn-secondary" type="button" @click="open(item)">查看详情</button><button v-if="canReorganize(item)" class="btn-secondary" type="button" :disabled="saving" @click="openReorganization(item)">修正识别并重新整理</button><button v-if="canControl(item) && canRetargetTransfer(item)" class="btn-secondary" type="button" :disabled="saving" @click="openRetarget(item)">修改入库目标</button><button v-if="canControl(item) && item.job_status === 'failed'" class="btn-secondary" type="button" :disabled="saving" @click="retry(item)">重试入库</button><button v-if="canDelete(item)" class="btn-danger" type="button" :disabled="saving" @click="remove(item)">删除记录</button></div></article>
      </div>
      <footer v-if="!loading && items.length" class="border-t border-[var(--border)] p-3 text-sm text-muted">显示 {{ items.length }} / {{ total }} 条 <span class="ml-4 inline-flex items-center gap-2"><button class="btn-secondary" type="button" :disabled="page === 1" @click="changePage(page - 1)">上一页</button><span>第 {{ page }} 页</span><button class="btn-secondary" type="button" :disabled="page * pageSize >= total" @click="changePage(page + 1)">下一页</button></span></footer>
    </div>

    <div v-if="retargeting" class="modal-backdrop fixed inset-0 z-60 flex items-center justify-center p-4" @click.self="!saving && (retargeting = null)"><form class="panel w-full max-w-lg" role="dialog" aria-modal="true" aria-labelledby="retarget-title" @submit.prevent="confirmRetarget"><h2 id="retarget-title" class="m-0 text-xl">修改入库目标</h2><p class="page-description mt-2">{{ retargeting.scrape_title || retargeting.display_name }}</p><p class="semantic-warning mt-4 p-3 text-sm">只会更换媒体库、分类规则和命名快照，并重新执行 Transfer → Import；不会重新下载。若已经产生目录规划、云端检查点或部分写入，Server 会拒绝操作。</p><label class="mt-4 block"><span class="label">新的目标媒体库</span><select v-model.number="retargetLibraryID" class="input" required><option :value="0" disabled>请选择</option><option v-for="library in mediaLibraries.filter(item => item.enabled && item.id !== retargeting?.library_id)" :key="library.id" :value="library.id">{{ library.name }} · {{ library.storage_name }} · {{ library.transfer_mode }}</option></select></label><div class="mt-5 flex justify-end gap-3"><button class="btn-secondary" type="button" :disabled="saving" @click="retargeting = null">取消</button><button class="btn-primary" :disabled="saving || !retargetLibraryID">{{ saving ? '正在校验并重排…' : '确认修改并重试入库' }}</button></div></form></div>
    <MediaReorganizationDialog v-if="reorganizing" :open="true" :transfer-task-id="reorganizing.id" :download-task-id="reorganizing.download_task_id" :display-name="reorganizing.display_name" :current-title="reorganizing.scrape_title" :current-media-type="reorganizing.scrape_media_type" @close="reorganizing = null" @queued="reorganizationQueued" />
    <TransferDeletionDialog v-if="deleting" :transfer="deleting" @close="deleting = null" @deleted="deletionCompleted" />

    <div v-if="selected || detailLoading" class="task-drawer-backdrop" @click.self="closeDrawer()">
      <aside ref="drawer" class="task-drawer organization-drawer" role="dialog" aria-modal="true" :aria-label="`${selected?.display_name ?? '媒体整理'}详情`" @keydown="trapDrawerFocus">
        <header><div><small>自动整理任务</small><h2>{{ selected?.scrape_title || selected?.display_name || '正在加载' }}</h2></div><button class="icon-button" type="button" aria-label="关闭详情" @click="closeDrawer()">×</button></header>
        <p v-if="detailLoading && !selected" class="p-5 text-muted">正在读取任务详情…</p>
        <template v-else-if="selected">
          <section v-if="selected.job.action_request" class="semantic-warning m-4 p-4"><strong>{{ selected.job.action_request.prompt }}</strong><dl class="mt-3 grid gap-2 text-sm"><div v-for="(value, key) in selected.job.action_request.preview" :key="key"><dt class="text-subtle">{{ key }}</dt><dd class="m-0">{{ value }}</dd></div></dl><div class="mt-3 flex flex-wrap gap-2"><button v-for="option in selected.job.action_request.options" :key="option" class="btn-primary" type="button" :disabled="saving || !canRespond" @click="respond(option)">{{ actionLabel(option) }}</button></div></section>
          <section class="organization-detail-section"><div class="flex items-start justify-between gap-3"><div><h3>任务概览</h3><p class="text-subtle mt-1 text-sm">{{ selected.display_name }} · {{ selected.downloader_name }}</p></div><span :class="transferStatusClass(selected)">{{ transferStatusLabel(selected) }}</span></div><p v-if="selected.phase === 'risk_backoff'" class="semantic-warning mt-4 p-3 text-sm">{{ transferPhaseDescription(selected) }}</p><dl class="organization-facts"><div><dt>目标媒体库</dt><dd>{{ selected.library_name }}</dd></div><div><dt>入库路线</dt><dd>{{ transferRouteLabel(selected.route_kind) }}</dd></div><div><dt>入库方式</dt><dd>{{ transferModeLabels[selected.transfer_mode] }}</dd></div><div><dt>冲突策略</dt><dd>{{ conflictPolicyLabels[selected.conflict_policy] }}</dd></div><div><dt>文件进度</dt><dd>{{ formatTransferProgress(selected) }}</dd></div><div><dt>暂存清理</dt><dd>{{ selected.cleanup_status === 'deferred' ? '等待做种结束' : selected.cleanup_status === 'completed' ? `已清理 ${selected.cleanup_removed} 项` : selected.cleanup_status === 'failed' ? `清理失败 · ${selected.cleanup_error_code}` : selected.cleanup_status === 'skipped' ? '旧任务不自动清理' : '待处理' }}</dd></div><div><dt>分类规则快照</dt><dd>Profile {{ selected.profile_id }} · r{{ selected.profile_revision }}</dd></div><div><dt>创建时间</dt><dd>{{ formatDate(selected.created_at) }}</dd></div></dl><p v-if="selected.last_error_message" class="semantic-error mt-4 p-3"><strong>{{ selected.last_error_code || '整理失败' }}</strong><span class="mt-1 block">{{ selected.last_error_message }}</span></p><div class="mt-4 flex flex-wrap gap-2"><button v-if="canReorganize(selected)" class="btn-primary" type="button" :disabled="saving" @click="openReorganization(selected)">修正识别并重新整理</button><button v-if="canControl(selected) && selected.job_status === 'failed'" class="btn-primary" type="button" :disabled="saving" @click="retry(selected)">仅重试入库阶段</button><button v-if="canDelete(selected)" class="btn-danger" type="button" :disabled="saving" @click="remove(selected)">删除记录</button></div></section>
          <section class="organization-detail-section"><h3>识别与命名</h3><dl class="organization-facts"><div><dt>身份状态</dt><dd>{{ transferIdentityLabel(selected) }}</dd></div><div><dt>识别标题</dt><dd>{{ selected.scrape_title || '未识别' }}</dd></div><div><dt>媒体类型</dt><dd>{{ selected.scrape_media_type || '未知' }}</dd></div><div><dt>分类</dt><dd>{{ selected.scrape_category || '未分类' }}</dd></div><div><dt>TMDB</dt><dd>{{ selected.scrape_tmdb_id ?? '未匹配' }}</dd></div><div><dt>年份</dt><dd>{{ selected.scrape_year ?? '未知' }}</dd></div><div><dt>置信度</dt><dd>{{ selected.scrape_confidence == null ? '未知' : selected.scrape_confidence.toFixed(2) }}</dd></div></dl><div v-if="selected.plan_summary?.items.length" class="mt-4"><h4>目标相对路径</h4><p v-if="selected.plan_summary.truncated" class="semantic-warning p-2 text-xs">文件较多，仅显示前 {{ selected.plan_summary.items.length }} / {{ selected.plan_summary.total_files }} 项。</p><ul class="organization-plan"><li v-for="item in selected.plan_summary.items" :key="`${item.relative_path}-${item.kind}`"><code>{{ item.relative_path }}</code><span>{{ item.kind === 'video' ? '视频' : '伴随文件' }} · {{ formatSize(item.size) }} · {{ resultLabel(item.result) }}</span></li></ul></div><p v-else class="text-subtle text-sm">历史任务或尚未进入规划阶段，暂无安全命名摘要。</p></section>
          <section class="organization-detail-section"><h3>执行记录</h3><h4>状态时间线</h4><ol class="task-timeline"><li v-for="event in selected.timeline" :key="event.id"><strong>{{ event.event_type }}</strong><span>{{ event.from_status || '开始' }} → {{ event.to_status || '—' }}</span><time>{{ formatDate(event.created_at) }}</time></li></ol><h4 class="mt-6">执行尝试</h4><ol class="task-timeline"><li v-for="attempt in selected.attempts" :key="attempt.id"><strong>第 {{ attempt.attempt_number }} 次 · {{ attempt.status }}</strong><span>{{ attempt.error_message || '无错误' }}</span><time>{{ formatDate(attempt.started_at) }}</time></li></ol></section>
        </template>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.organization-filters { display: grid; grid-template-columns: repeat(4, minmax(9rem, 1fr)); gap: .75rem; align-items: end; }
.organization-keyword { grid-column: span 2; }
.organization-table { min-width: 76rem; }
.organization-mobile-list { display: none; gap: .75rem; }
.organization-detail-section { border-bottom: 1px solid var(--border); padding: 1rem; }
.organization-detail-section:last-child { border-bottom: 0; }
.organization-detail-section h3 { margin: 0; }
.organization-facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; margin: 1rem 0 0; }
.organization-facts div { border-left: 3px solid var(--border-strong); padding-left: .65rem; }
.organization-facts dt { color: var(--text-subtle); font-size: .72rem; }
.organization-facts dd { margin: .2rem 0 0; overflow-wrap: anywhere; font-size: .88rem; font-weight: 650; }
.organization-plan { display: grid; gap: .6rem; margin: .75rem 0 0; padding: 0; list-style: none; }
.organization-plan li { display: grid; gap: .25rem; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface-subtle); padding: .65rem; }
.organization-plan code { overflow-wrap: anywhere; color: var(--text); font-size: .78rem; }
.organization-plan span { color: var(--text-subtle); font-size: .72rem; }
@media (max-width: 900px) { .organization-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); }.organization-keyword { grid-column: span 1; } }
@media (max-width: 767px) { .organization-filters { grid-template-columns: 1fr; }.organization-table { display: none; }.organization-mobile-list { display: grid; }.organization-facts { grid-template-columns: 1fr; } }
</style>
