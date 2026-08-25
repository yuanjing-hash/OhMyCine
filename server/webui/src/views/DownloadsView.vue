<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import DirectoryPickerDialog from '@/components/DirectoryPickerDialog.vue'
import SecretInput from '@/components/SecretInput.vue'
import { credentialLoader } from '@/credentials'
import { beginDownloadRetry, compatibleDownloadLibraries, downloadErrorMessage, downloadStatusClass, downloadStatusLabel, formatBytes, formatETA, formatProgress, formatSampleTime, isDownloadHistoryTask, reconcileDownloadRetries, summarizeDownloaderTasks, torrentToBase64, type DownloadManagementSection, type DownloadRetryPresentations, type DownloadSourceMode } from '@/downloads'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import { retargetCompletedImport } from '@/transfers'
import type { DownloaderSummary, DownloadSettings, DownloadTaskSummary, ListResponse, MediaLibraryDetail, SeedingTaskSummary, StorageSummary, TMDBCandidate } from '@/types/api'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const downloaders = ref<DownloaderSummary[]>([])
const tasks = ref<DownloadTaskSummary[]>([])
const seedingTasks = ref<SeedingTaskSummary[]>([])
const downloadSettings = ref<DownloadSettings | null>(null)
const mediaLibraries = ref<MediaLibraryDetail[]>([])
const storages = ref<StorageSummary[]>([])
const loading = ref(true)
const saving = ref(false)
const createOpen = ref(false)
const editingID = ref<string | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const createForm = ref({ name: '', type: 'qbittorrent' as 'qbittorrent' | 'fake' | 'pan115_offline', baseURL: '', username: '', password: '', storageID: 0, directoryToken: '', directoryPath: '', enabled: true })
const editForm = ref({ name: '', baseURL: '', username: '', password: '', storageID: 0, directoryToken: '', directoryPath: '', enabled: true, clearUsername: false, clearPassword: false })
const downloaderPickerOpen = ref(false)
const downloaderPickerTarget = ref<'create' | 'edit'>('create')
const sourceMode = ref<DownloadSourceMode>('url')
const submitForm = ref({ downloaderID: '', mediaLibraryID: 0, displayName: '', priority: 0, sourceURL: '', torrent: null as File | null })
const activeSection = ref<DownloadManagementSection>(readSection())
const activeTotal = ref(0)
const historyTotal = ref(0)
const recognitionTaskID = ref('')
const recognitionCandidates = ref<TMDBCandidate[]>([])
const recognitionSearching = ref(false)
type OptionalNumberInput = number | '' | null
const recognitionForm = ref({ keyword: '', mediaType: '' as '' | 'movie' | 'tv', tmdbID: null as number | null, season: null as OptionalNumberInput, episode: null as OptionalNumberInput })
const retryingTasks = ref<DownloadRetryPresentations>({})
const retargetTask = ref<DownloadTaskSummary | null>(null)
const retargetLibraryID = ref(0)
const editing = computed(() => downloaders.value.find(item => item.id === editingID.value) ?? null)
const enabledDownloaders = computed(() => downloaders.value.filter(item => item.enabled))
const selectedDownloader = computed(() => enabledDownloaders.value.find(item => item.id === submitForm.value.downloaderID) ?? null)
const availableLibraries = computed(() => compatibleDownloadLibraries(mediaLibraries.value, storages.value, selectedDownloader.value, sourceMode.value === 'share'))
const selectedTarget = computed(() => submitForm.value.mediaLibraryID === 0 ? availableLibraries.value[0] ?? null : availableLibraries.value.find(item => item.id === submitForm.value.mediaLibraryID) ?? null)
const pan115Storages = computed(() => storages.value.filter(item => item.enabled && item.type === 'pan115' && item.capabilities.native_offline_download))
const downloaderPickerStorageID = computed(() => downloaderPickerTarget.value === 'create' ? createForm.value.storageID : editForm.value.storageID)
const providerOptions = computed(() => import.meta.env.DEV ? [{ value: 'qbittorrent', label: 'qBittorrent（支持做种）' }, { value: 'pan115_offline', label: '115 网盘原生离线下载（不支持做种）' }, { value: 'fake', label: 'Fake（开发测试）' }] : [{ value: 'qbittorrent', label: 'qBittorrent（支持做种）' }, { value: 'pan115_offline', label: '115 网盘原生离线下载（不支持做种）' }])
const canReadDownloaders = computed(() => auth.can(Permissions.DownloadersRead))
const canReadDownloads = computed(() => auth.canAny([Permissions.DownloadsReadOwn, Permissions.DownloadsReadAll]))
const canReadTransfers = computed(() => auth.canAny([Permissions.TransfersReadOwn, Permissions.TransfersReadAll]))
const activeTasks = computed(() => tasks.value.filter(task => !isDownloadHistoryTask(task)))
const historyTasks = computed(() => tasks.value.filter(isDownloadHistoryTask))
const visibleTasks = computed(() => activeSection.value === 'history' ? historyTasks.value : activeTasks.value)
const visibleSections = computed(() => ([
  { id: 'active', label: '进行中', count: activeTotal.value, visible: canReadDownloads.value },
  { id: 'history', label: '历史记录', count: historyTotal.value, visible: canReadDownloads.value },
  { id: 'create', label: '新建下载', visible: auth.can(Permissions.DownloadsCreate) },
  { id: 'seeding', label: '做种管理', count: seedingTasks.value.filter(item => item.phase !== 'completed').length, visible: canReadDownloads.value },
  { id: 'downloaders', label: '下载器管理', count: downloaders.value.length, visible: canReadDownloaders.value },
] as Array<{ id: DownloadManagementSection; label: string; count?: number; visible: boolean }>).filter(item => item.visible))

function readSection(): DownloadManagementSection {
  const value = route.query.section
  return typeof value === 'string' && ['active', 'history', 'create', 'seeding', 'downloaders'].includes(value) ? value as DownloadManagementSection : 'active'
}

async function selectSection(section: DownloadManagementSection) {
  activeSection.value = section
  await router.replace({ query: { ...route.query, section } })
}

watch(() => route.query.section, () => { activeSection.value = readSection() })
watch(visibleSections, sections => {
  if (!sections.some(item => item.id === activeSection.value)) activeSection.value = sections[0]?.id ?? 'active'
})

watch(enabledDownloaders, items => {
  if (!items.some(item => item.id === submitForm.value.downloaderID)) submitForm.value.downloaderID = items[0]?.id ?? ''
}, { immediate: true })
watch(selectedDownloader, item => {
  if (item?.type === 'pan115_offline' && sourceMode.value === 'torrent') sourceMode.value = 'url'
  if ((item?.type !== 'pan115_offline' || !item.capabilities.share_receive) && sourceMode.value === 'share') sourceMode.value = 'url'
})
watch(availableLibraries, libraries => {
  if (submitForm.value.mediaLibraryID !== 0 && !libraries.some(item => item.id === submitForm.value.mediaLibraryID)) submitForm.value.mediaLibraryID = 0
})
watch(() => recognitionForm.value.mediaType, mediaType => {
  if (mediaType === 'movie') {
    recognitionForm.value.season = null
    recognitionForm.value.episode = null
  }
})

async function load(showLoading = true, quiet = false) {
  if (showLoading) loading.value = true
  try {
    const requests: Promise<void>[] = []
    if (canReadDownloaders.value) requests.push(api<ListResponse<DownloaderSummary>>('/api/v1/downloaders').then(data => { downloaders.value = data.list }))
    if (canReadDownloads.value) requests.push(Promise.all([
      api<ListResponse<DownloadTaskSummary>>('/api/v1/downloads?scope=active&limit=200'),
      api<ListResponse<DownloadTaskSummary>>('/api/v1/downloads?scope=history&limit=200'),
    ]).then(([active, history]) => {
      const nextTasks = [...active.list, ...history.list]
      reconcileRetryingTasks(nextTasks)
      tasks.value = nextTasks
      activeTotal.value = active.total
      historyTotal.value = history.total
    }))
    if (canReadDownloads.value) requests.push(api<ListResponse<SeedingTaskSummary>>('/api/v1/seeding-tasks?limit=100').then(data => { seedingTasks.value = data.list }))
    if (auth.can(Permissions.SettingsRead)) requests.push(api<DownloadSettings>('/api/v1/settings/downloads').then(data => { downloadSettings.value = data }))
    if (auth.can(Permissions.StoragesRead)) requests.push(api<ListResponse<StorageSummary>>('/api/v1/storages').then(data => { storages.value = data.list }))
    if (auth.can(Permissions.MediaLibrariesRead)) requests.push(api<ListResponse<MediaLibraryDetail>>('/api/v1/media-libraries').then(data => { mediaLibraries.value = data.list; if (submitForm.value.mediaLibraryID !== 0 && !mediaLibraries.value.some(item => item.id === submitForm.value.mediaLibraryID)) submitForm.value.mediaLibraryID = 0 }))
    await Promise.all(requests)
    if (editingID.value && !downloaders.value.some(item => item.id === editingID.value)) editingID.value = null
  } catch (reason) { if (!quiet) notify(message(reason), 'error') } finally { if (showLoading) loading.value = false }
}

async function createDownloader() {
  saving.value = true
  try {
    await api<DownloaderSummary>('/api/v1/downloaders', { method: 'POST', body: JSON.stringify({ name: createForm.value.name, type: createForm.value.type, base_url: createForm.value.type === 'qbittorrent' ? createForm.value.baseURL : '', username: createForm.value.type === 'qbittorrent' ? createForm.value.username : '', password: createForm.value.type === 'qbittorrent' ? createForm.value.password : '', storage_id: createForm.value.type === 'pan115_offline' ? createForm.value.storageID : undefined, provider_directory_token: createForm.value.type === 'pan115_offline' ? createForm.value.directoryToken : undefined, enabled: createForm.value.enabled }) })
    createForm.value = { name: '', type: 'qbittorrent', baseURL: '', username: '', password: '', storageID: 0, directoryToken: '', directoryPath: '', enabled: true }
    createOpen.value = false
    notify('下载器连接信息已加密保存，请在卡片上测试连接', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

function startEdit(item: DownloaderSummary) {
  editingID.value = item.id
  editForm.value = { name: item.name, baseURL: item.base_url, username: '', password: '', storageID: item.storage_id ?? 0, directoryToken: '', directoryPath: item.provider_directory_path || '/', enabled: item.enabled, clearUsername: false, clearPassword: false }
}

function resetCreateDirectory() { createForm.value.directoryToken = ''; createForm.value.directoryPath = '' }
function resetEditDirectory() { editForm.value.directoryToken = ''; editForm.value.directoryPath = '' }
function openDownloaderPicker(target: 'create' | 'edit') {
  const storageID = target === 'create' ? createForm.value.storageID : editForm.value.storageID
  if (!storageID) { notify('请先选择一个 115 数据源', 'warning'); return }
  downloaderPickerTarget.value = target
  downloaderPickerOpen.value = true
}
function chooseDownloaderDirectory(value: { path: string; token: string }) {
  if (downloaderPickerTarget.value === 'create') {
    createForm.value.directoryPath = value.path; createForm.value.directoryToken = value.token
  } else {
    editForm.value.directoryPath = value.path; editForm.value.directoryToken = value.token
  }
}

async function saveDownloader() {
  if (!editing.value) return
  const id = editing.value.id
  saving.value = true
  try {
    await updateDownloader(id)
    editForm.value.username = ''; editForm.value.password = ''
    editingID.value = null
    notify('下载器连接配置已保存', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

function updateDownloader(id: string) {
  const providerDirectory = editing.value?.type === 'pan115_offline' && editForm.value.directoryToken
    ? { storage_id: editForm.value.storageID, provider_directory_token: editForm.value.directoryToken }
    : {}
  return api<DownloaderSummary>(`/api/v1/downloaders/${id}`, { method: 'PATCH', body: JSON.stringify({ name: editForm.value.name, base_url: editForm.value.baseURL, username: editForm.value.username || undefined, password: editForm.value.password || undefined, clear_username: editForm.value.clearUsername, clear_password: editForm.value.clearPassword, enabled: editForm.value.enabled, ...providerDirectory }) })
}

async function saveAndTestDownloader() {
  if (!editing.value) return
  const id = editing.value.id
  saving.value = true
  try {
    const saved = await updateDownloader(id)
    const tested = await api<DownloaderSummary>(`/api/v1/downloaders/${id}/test`, { method: 'POST', body: '{}' })
    editForm.value.username = ''; editForm.value.password = ''
    editingID.value = null
    notify(`已保存并连接 ${saved.name}${tested.health.version ? ` · ${tested.health.version}` : ''}`, 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

async function testDownloader(item: DownloaderSummary) {
  saving.value = true
  try {
    const tested = await api<DownloaderSummary>(`/api/v1/downloaders/${item.id}/test`, { method: 'POST', body: '{}' })
    notify(`已连接 ${item.name}${tested.health.version ? ` · ${tested.health.version}` : ''}`, 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

async function deleteDownloader() {
  if (!editing.value || !window.confirm(`确认删除下载器“${editing.value.name}”？不会删除下载文件；存在活跃任务时 Server 会拒绝。`)) return
  const id = editing.value.id
  saving.value = true
  try {
    await api(`/api/v1/downloaders/${id}`, { method: 'DELETE', body: '{}' })
    editingID.value = null
    notify('下载器配置已删除，真实文件未改变', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

function selectTorrent(event: Event) { submitForm.value.torrent = (event.target as HTMLInputElement).files?.[0] ?? null }

async function submitDownload() {
  if (!submitForm.value.downloaderID) { notify('请先添加并启用一个下载器', 'warning'); return }
  const nativeOffline = selectedDownloader.value?.type === 'pan115_offline'
  if (!selectedTarget.value) { notify(nativeOffline ? '请先创建同一 115 账号下可写的移动或复制媒体库' : '请先创建并启用一个本地媒体库', 'warning'); return }
  saving.value = true
  try {
    const payload: Record<string, unknown> = { downloader_id: submitForm.value.downloaderID, display_name: submitForm.value.displayName, priority: submitForm.value.priority }
    payload.media_library_id = submitForm.value.mediaLibraryID
    if (sourceMode.value === 'url' || sourceMode.value === 'share') {
      if (!submitForm.value.sourceURL.trim()) throw new Error('请粘贴磁力链接或 HTTP(S) URL')
      Object.assign(payload, { source_kind: sourceMode.value === 'share' ? '115_share' : 'url', source_url: submitForm.value.sourceURL.trim() })
    } else {
      const file = submitForm.value.torrent
      if (!file || !file.name.toLowerCase().endsWith('.torrent') || file.size < 1 || file.size > 4 * 1024 * 1024) throw new Error('请选择 4 MiB 以内的 .torrent 文件')
      Object.assign(payload, { source_kind: 'torrent', torrent_filename: file.name, torrent_base64: torrentToBase64(new Uint8Array(await file.arrayBuffer())) })
    }
    await api<DownloadTaskSummary>('/api/v1/downloads', { method: 'POST', body: JSON.stringify(payload) })
    submitForm.value.displayName = ''; submitForm.value.sourceURL = ''; submitForm.value.torrent = null
    if (fileInput.value) fileInput.value.value = ''
    notify(sourceMode.value === 'share' ? '115 分享转存已入队，内容会进入媒体库中转目录并自动识别整理' : nativeOffline ? '任务已进入 115 原生离线下载队列，完成后会自动云端整理入库' : '下载任务已进入统一暂存队列', 'success')
    await selectSection('active')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

function canControl(task: DownloadTaskSummary) { return auth.can(Permissions.JobsControlAll) || (task.owner_id === auth.user?.id && auth.can(Permissions.JobsControlOwn)) }
function canDelete(task: DownloadTaskSummary) { return auth.can(Permissions.DownloadsManageAll) || (task.owner_id === auth.user?.id && auth.can(Permissions.JobsControlOwn)) }
function isTaskRetrying(task: DownloadTaskSummary) { return retryingTasks.value[task.id] !== undefined }
function markTaskRetrying(task: DownloadTaskSummary) {
  if (isTaskRetrying(task)) return false
  retryingTasks.value = { ...retryingTasks.value, [task.id]: beginDownloadRetry(task) }
  return true
}
function clearTaskRetrying(taskID: string) {
  if (!retryingTasks.value[taskID]) return
  const next = { ...retryingTasks.value }
  delete next[taskID]
  retryingTasks.value = next
}
function reconcileRetryingTasks(nextTasks: DownloadTaskSummary[]) {
  retryingTasks.value = reconcileDownloadRetries(retryingTasks.value, nextTasks)
}
async function control(task: DownloadTaskSummary, action: 'pause' | 'resume' | 'cancel' | 'retry') {
  if (action === 'cancel' && !window.confirm(`确认取消“${task.display_name}”？这会从下载器删除任务，并删除已经下载的文件和临时数据；操作不可恢复。`)) return
  if (action === 'retry' && !markTaskRetrying(task)) return
  saving.value = true
  try {
    await api(`/api/v1/jobs/${task.job_id}/${action}`, { method: 'POST', body: '{}' })
    notify(action === 'cancel' ? '取消请求已提交；下载器确认删除数据后，本地记录会自动移除' : '任务控制请求已提交', 'success')
  } catch (reason) {
    if (action === 'retry') clearTaskRetrying(task.id)
    notify(message(reason), 'error')
  } finally { saving.value = false; await load(false, true) }
}

function isCompletedRecognitionFailure(task: DownloadTaskSummary) {
  return task.job_status === 'failed' && task.scrape_status === 'completed_unrecognized'
}

async function retryRecognitionImport(task: DownloadTaskSummary) {
  if (!markTaskRetrying(task)) return
  saving.value = true
  try {
    await api(`/api/v1/jobs/${task.job_id}/retry`, { method: 'POST', body: '{}' })
    notify('已使用 115 中现有文件重新识别并入库，不会重复下载', 'success')
  } catch (reason) {
    clearTaskRetrying(task.id)
    notify(message(reason), 'error')
  } finally { saving.value = false; await load(false, true) }
}

function openRecognitionRecovery(task: DownloadTaskSummary) {
  if (recognitionTaskID.value === task.id) {
    recognitionTaskID.value = ''
    recognitionCandidates.value = []
    return
  }
  recognitionTaskID.value = task.id
  recognitionCandidates.value = []
  recognitionForm.value = {
    keyword: task.scrape_title || task.display_name,
    mediaType: task.scrape_media_type === 'movie' || task.scrape_media_type === 'tv' ? task.scrape_media_type : '',
    tmdbID: null,
    season: task.scrape_season,
    episode: task.scrape_episode,
  }
}

async function searchRecognitionCandidates(task: DownloadTaskSummary) {
  const keyword = recognitionForm.value.keyword.trim()
  if (!keyword) { notify('请输入要搜索的影视名称', 'warning'); return }
  recognitionSearching.value = true
  recognitionCandidates.value = []
  try {
    const params = new URLSearchParams({ title: keyword })
    if (recognitionForm.value.mediaType) params.set('media_type', recognitionForm.value.mediaType)
    const data = await api<ListResponse<TMDBCandidate>>(`/api/v1/downloads/${task.id}/tmdb-candidates?${params}`)
    recognitionCandidates.value = data.list
  } catch (reason) { notify(message(reason), 'error') } finally { recognitionSearching.value = false }
}

function selectRecognitionCandidate(candidate: TMDBCandidate) {
  recognitionForm.value.mediaType = candidate.media_type
  recognitionForm.value.tmdbID = candidate.id
}

function optionalPositiveInteger(value: OptionalNumberInput): number | null | undefined {
  if (value === '' || value === null) return null
  return Number.isSafeInteger(value) && value > 0 && value <= 100000 ? value : undefined
}

function optionalSeasonInteger(value: OptionalNumberInput): number | null | undefined {
  if (value === '' || value === null) return null
  return Number.isSafeInteger(value) && value >= 0 && value <= 200 ? value : undefined
}

async function submitRecognitionOverride(task: DownloadTaskSummary) {
  if (!recognitionForm.value.tmdbID || !recognitionForm.value.mediaType) { notify('请选择搜索结果，或填写 TMDB ID 并选择媒体类型', 'warning'); return }
  const payload: Record<string, unknown> = { tmdb_id: recognitionForm.value.tmdbID, media_type: recognitionForm.value.mediaType }
  if (recognitionForm.value.mediaType === 'tv') {
    const season = optionalSeasonInteger(recognitionForm.value.season)
    const episode = optionalPositiveInteger(recognitionForm.value.episode)
    if (season === undefined || episode === undefined) { notify('季号必须是 0 到 200 的整数，集号必须是正整数，也可留空由 Server 自动检测', 'warning'); return }
    if (season !== null) payload.season = season
    if (episode !== null) payload.episode = episode
  }
  if (!markTaskRetrying(task)) return
  saving.value = true
  try {
    await api(`/api/v1/downloads/${task.id}/recognition-override`, { method: 'PUT', body: JSON.stringify(payload) })
    recognitionTaskID.value = ''
    recognitionCandidates.value = []
    notify('TMDB 身份已由 Server 验证，正在使用现有文件继续入库', 'success')
  } catch (reason) {
    clearTaskRetrying(task.id)
    notify(message(reason), 'error')
  } finally { saving.value = false; await load(false, true) }
}

async function retryStage(jobID: string, label: string) {
  if (!jobID) return
  saving.value = true
  try {
    await api(`/api/v1/jobs/${jobID}/retry`, { method: 'POST', body: '{}' })
    notify(`${label}已重新入队`, 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

async function deleteDownload(task: DownloadTaskSummary) {
  const historyOnly = task.job_status === 'completed'
  const prompt = historyOnly
    ? `确认删除“${task.display_name}”的历史记录？\n\n只会清理 OhMyCine 中的下载、整理和做种执行记录，不会操作 qBittorrent，也不会删除暂存或媒体库文件。`
    : `确认彻底删除“${task.display_name}”？Server 会先要求下载器删除任务及其已下载/临时文件，再删除 OhMyCine 本地任务记录；操作不可恢复。`
  if (!window.confirm(prompt)) return
  saving.value = true
  try {
    await api(`/api/v1/downloads/${task.id}`, { method: 'DELETE', body: '{}' })
    tasks.value = tasks.value.filter(item => item.id !== task.id)
    notify(historyOnly ? '下载历史记录已删除，真实文件未改变' : '下载器数据和 OhMyCine 本地任务记录已删除', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

async function stopSeeding(task: SeedingTaskSummary) {
  const consequence = task.delete_data ? '删除 qBittorrent 任务以及统一暂存目录中的源文件' : '只删除 qBittorrent 任务并保留源文件，保证媒体库软链接继续有效'
  if (!window.confirm(`确认立即停止做种？将${consequence}。`)) return
  saving.value = true
  try {
    await api(`/api/v1/seeding-tasks/${task.id}/stop`, { method: 'POST', body: '{}' })
    notify('做种清理已完成', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false; await load(false, true) }
}

function openRetarget(task: DownloadTaskSummary) {
  retargetTask.value = task
  retargetLibraryID.value = mediaLibraries.value.find(library => library.enabled && library.id !== task.target_library_id)?.id ?? 0
}

async function confirmRetarget() {
  if (!retargetTask.value || !retargetLibraryID.value) return
  saving.value = true
  try {
    await retargetCompletedImport(retargetTask.value.id, retargetLibraryID.value)
    notify('已更新入库目标，下载不会重新执行', 'success')
    retargetTask.value = null
  } catch (reason) { notify(message(reason), 'error') }
  finally { saving.value = false; await load(false, true) }
}

function seededTime(seconds: number | null) {
  if (seconds == null) return '未知'
  const hours = Math.floor(seconds / 3600), minutes = Math.floor((seconds % 3600) / 60)
  return hours > 0 ? `${hours} 小时 ${minutes} 分钟` : `${minutes} 分钟`
}
function seedingPhaseLabel(phase: SeedingTaskSummary['phase']) { return ({ queued: '等待采样', seeding: '正在做种', cleanup: '正在清理', retained: '保留做种', completed: '已停止', failed: '异常' } as const)[phase] }
function canStopSeeding(task: SeedingTaskSummary) { return task.phase !== 'completed' && (auth.can(Permissions.DownloadsManageAll) || (task.owner_id === auth.user?.id && auth.can(Permissions.JobsControlOwn))) }

function stats(item: DownloaderSummary) { return summarizeDownloaderTasks(tasks.value, item.id) }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败' }
function healthClass(item: DownloaderSummary) { return item.health.status === 'online' ? 'status-chip status-chip--ready' : item.health.status === 'offline' ? 'status-chip status-chip--error' : 'status-chip' }
function healthLabel(item: DownloaderSummary) { return item.health.status === 'online' ? '在线' : item.health.status === 'offline' ? '离线' : '未测试' }
function healthDetail(item: DownloaderSummary) {
  const labels: Record<string, string> = { downloader_auth_failed: '认证失败', downloader_unavailable: '地址不可达', downloader_response_invalid: '响应不兼容', downloader_request_failed: 'API 请求被拒绝' }
  return labels[item.health.error_code] ?? (item.health.error_code ? '连接异常' : '等待连接测试')
}
function transferModeLabel(mode: string) { return ({ move: '移动', copy: '复制', symlink: '软链接' } as Record<string, string>)[mode] ?? '未配置' }
function transferPhaseLabel(phase: string) {
  return ({ queued: '等待入库', planning: '规划目录', transferring: '正在入库', reconciling: '刷新媒体库', completed: '入库完成', failed: '入库失败' } as Record<string, string>)[phase] ?? '下载完成后入库'
}
function showPan115TransferPacing(task: DownloadTaskSummary) {
  return task.provider_type === 'pan115_offline' && ['planning', 'transferring'].includes(task.transfer_phase)
}

let refreshTimer: number | undefined
onMounted(async () => { await load(); refreshTimer = window.setInterval(() => { void load(false, true) }, 3000) })
onBeforeUnmount(() => { if (refreshTimer !== undefined) window.clearInterval(refreshTimer) })
</script>

<template>
  <section class="mx-auto max-w-7xl">
    <div class="flex flex-wrap items-end justify-between gap-4"><div><h1 class="m-0 text-3xl font-800">下载管理</h1><p class="page-description mt-2 max-w-3xl">上传种子、粘贴磁力或 URL；本地下载进入统一暂存目录，115 原生离线任务进入绑定的云端目录，并统一由任务队列跟踪。</p></div><RouterLink class="btn-secondary" to="/automation/tasks">打开全局任务中心</RouterLink></div>
    <div v-if="downloadSettings && !downloadSettings.configured && selectedDownloader?.type !== 'pan115_offline'" class="semantic-warning mt-5 flex flex-wrap items-center justify-between gap-3 p-3 text-sm"><span>尚未配置统一下载暂存目录，qBittorrent 任务暂时不能入队。</span><RouterLink class="btn-secondary" to="/system/settings">前往设置</RouterLink></div>

    <nav class="management-tabs mt-6" role="tablist" aria-label="下载管理页面">
      <button v-for="section in visibleSections" :id="`download-tab-${section.id}`" :key="section.id" class="management-tab" :class="activeSection === section.id ? 'management-tab--active' : ''" type="button" role="tab" :aria-selected="activeSection === section.id" :aria-controls="`download-panel-${section.id}`" :tabindex="activeSection === section.id ? 0 : -1" @click="selectSection(section.id)">{{ section.label }}<span v-if="section.count !== undefined" class="ml-2 text-xs">{{ section.count }}</span></button>
    </nav>

    <form v-if="activeSection === 'create' && auth.can(Permissions.DownloadsCreate)" id="download-panel-create" class="panel mt-6" role="tabpanel" aria-labelledby="download-tab-create" @submit.prevent="submitDownload">
      <div class="flex flex-wrap items-start justify-between gap-3"><div><h2 class="m-0 text-lg">新建下载任务</h2><p class="text-subtle mb-0 mt-1 text-xs">种子或链接只会进入加密任务来源，不写公开临时目录；115 分享会先转存到媒体库绑定的中转目录。</p></div><div class="flex flex-wrap gap-2" role="radiogroup" aria-label="下载来源"><label class="btn-secondary" :class="{ 'semantic-list-item--selected': sourceMode === 'url' }"><input v-model="sourceMode" class="sr-only" type="radio" value="url" />磁力 / URL</label><label v-if="selectedDownloader?.type !== 'pan115_offline'" class="btn-secondary" :class="{ 'semantic-list-item--selected': sourceMode === 'torrent' }"><input v-model="sourceMode" class="sr-only" type="radio" value="torrent" />上传种子</label><label v-if="selectedDownloader?.type === 'pan115_offline' && selectedDownloader.capabilities.share_receive" class="btn-secondary" :class="{ 'semantic-list-item--selected': sourceMode === 'share' }"><input v-model="sourceMode" class="sr-only" type="radio" value="share" />115 分享链接</label></div></div>
      <div class="mt-5 grid gap-4 md:grid-cols-2"><div><label class="label">下载器</label><select v-model="submitForm.downloaderID" class="input" required><option value="" disabled>请选择</option><option v-for="item in enabledDownloaders" :key="item.id" :value="item.id">{{ item.name }} · {{ item.type === 'pan115_offline' ? '115 离线' : item.type }}</option></select></div><div><label class="label">目标媒体库</label><select v-model.number="submitForm.mediaLibraryID" class="input" required><option :value="0">自动选择（按可用媒体库顺序）</option><option v-for="library in availableLibraries" :key="library.id" :value="library.id">{{ library.name }} · {{ library.storage_name }}</option></select><p v-if="selectedDownloader && availableLibraries.length === 0" class="semantic-warning-text mb-0 mt-2 text-xs">{{ sourceMode === 'share' ? '没有绑定当前下载器并启用分享摄取的 115 媒体库。' : selectedDownloader.type === 'pan115_offline' ? '没有同一 115 账号下可用的移动或复制媒体库。' : '没有可用的本地媒体库。' }}</p></div><div v-if="selectedDownloader?.type === 'pan115_offline'" class="semantic-inset p-3 text-sm md:col-span-2"><span class="text-subtle block text-xs">{{ sourceMode === 'share' ? '媒体库分享中转目录' : '115 离线下载目录' }}</span><strong>{{ sourceMode === 'share' ? `${selectedTarget?.storage_name || '待自动选择'} · ${selectedTarget?.ingest_relative_root || '未配置'}` : `${selectedDownloader.storage_name} · ${selectedDownloader.provider_directory_path || '/'}` }}</strong><span class="text-subtle mt-1 block text-xs">{{ sourceMode === 'share' ? '分享内容会转存到稳定任务子目录，完成后复核文件树并自动识别整理。' : '离线完成后复核文件树，按目标媒体库的分类、命名和冲突策略在同一 115 账号内自动整理。' }}</span></div><div><label class="label">任务名称（可选）</label><input v-model="submitForm.displayName" class="input" maxlength="256" placeholder="留空时使用安全的通用名称" /></div><div><label class="label">队列优先级</label><input v-model.number="submitForm.priority" class="input" type="number" min="-100" max="100" /></div><div v-if="sourceMode === 'url' || sourceMode === 'share'" class="md:col-span-2"><label class="label">{{ sourceMode === 'share' ? '115 分享链接（需包含提取码）' : '磁力链接或 HTTP(S) URL' }}</label><textarea v-model="submitForm.sourceURL" class="input min-h-24 font-mono text-xs" required autocomplete="off" spellcheck="false" :placeholder="sourceMode === 'share' ? 'https://115.com/s/...?password=...' : 'magnet:?xt=... 或 https://...'" /></div><div v-else class="md:col-span-2"><label class="label">种子文件</label><input ref="fileInput" class="input" type="file" accept=".torrent,application/x-bittorrent" required @change="selectTorrent" /></div></div>
      <div v-if="selectedTarget" class="semantic-inset mt-4 grid gap-3 p-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
        <div><span class="text-subtle block text-xs">最终媒体库</span><strong>{{ selectedTarget.name }}</strong></div>
        <div><span class="text-subtle block text-xs">分类规则</span><strong>{{ selectedTarget.profile_name }} · r{{ selectedTarget.profile_revision }}</strong></div>
        <div><span class="text-subtle block text-xs">入库方式</span><strong>{{ selectedDownloader?.type === 'pan115_offline' ? (selectedTarget.transfer_mode === 'move' ? '云端移动' : '云端复制') : (selectedTarget.transfer_mode === 'move' ? '移动' : selectedTarget.transfer_mode === 'copy' ? '复制' : '软链接') }}</strong></div>
        <div><span class="text-subtle block text-xs">冲突策略</span><strong>{{ selectedTarget.conflict_policy === 'ask' ? '询问' : selectedTarget.conflict_policy === 'overwrite' ? '覆盖' : selectedTarget.conflict_policy === 'skip' ? '跳过' : '自动改名' }}</strong></div>
      </div>
      <button class="btn-primary mt-5" :disabled="saving || enabledDownloaders.length === 0 || !selectedTarget || (selectedDownloader?.type !== 'pan115_offline' && downloadSettings?.configured === false)">确认并入队</button>
    </form>

    <section v-if="activeSection === 'active' || activeSection === 'history'" :id="`download-panel-${activeSection}`" class="mt-6" role="tabpanel" :aria-labelledby="`download-tab-${activeSection}`"><div><h2 class="m-0 text-xl">{{ activeSection === 'history' ? '下载历史' : '进行中的下载流水线' }}</h2><p class="text-subtle mb-0 mt-1 text-xs">{{ activeSection === 'history' ? '仅展示已取消或下载、整理和做种后续均已成功收口的记录。' : '下载失败或后续整理、做种仍未完成的任务也会留在这里，避免问题被历史记录掩盖。' }}</p></div>
      <div v-if="loading" class="text-subtle mt-5">正在读取下载状态…</div><div v-else-if="!canReadDownloads" class="panel mt-5 opacity-75">当前账户没有下载任务查看权限。</div><div v-else-if="visibleTasks.length === 0" class="panel mt-5">{{ activeSection === 'history' ? '还没有下载历史记录。' : '当前没有进行中的下载任务。' }}</div>
      <div v-else class="panel mt-5 overflow-x-auto p-0">
        <table class="semantic-table w-full min-w-260 text-left text-sm">
          <thead><tr><th>任务</th><th>状态</th><th>目标 / 入库</th><th>进度</th><th>下载 / 上传</th><th>已完成 / 总量</th><th>ETA</th><th>操作</th></tr></thead>
          <tbody>
            <template v-for="task in visibleTasks" :key="task.id">
              <tr>
                <td><strong class="block">{{ task.display_name }}</strong><span class="text-subtle mt-1 block text-xs">{{ task.downloader_name }} · {{ task.provider_status || '尚未采样' }}</span><span v-if="task.scrape_title" class="text-subtle mt-1 block text-xs">{{ task.scrape_title }} · {{ task.scrape_category || '待分类' }}<template v-if="task.scrape_tmdb_id"> · TMDB {{ task.scrape_tmdb_id }}</template><template v-if="task.scrape_episode !== null"> · S{{ String(task.scrape_season ?? 1).padStart(2, '0') }}E{{ String(task.scrape_episode).padStart(2, '0') }}</template></span><span v-if="isTaskRetrying(task)" class="text-subtle mt-1 block text-xs" role="status">正在重试…</span><span v-else-if="downloadErrorMessage(task)" :class="task.scrape_status === 'fallback_unrecognized' ? 'semantic-warning-text' : 'semantic-danger-text'" class="mt-1 block text-xs">{{ downloadErrorMessage(task) }}</span></td>
                <td><span :class="downloadStatusClass(task, isTaskRetrying(task))">{{ downloadStatusLabel(task, isTaskRetrying(task)) }}</span></td>
                <td class="min-w-36"><strong v-if="task.target_library_id" class="block">{{ task.target_library_name }}</strong><span v-else class="text-subtle block">仅下载</span><span v-if="task.target_library_id" class="text-subtle mt-1 block text-xs">{{ transferModeLabel(task.transfer_mode) }} · {{ transferPhaseLabel(task.transfer_phase) }}</span><span v-if="showPan115TransferPacing(task)" class="text-subtle mt-1 block text-xs">115 风控限速处理中，多文件入库可能需要数分钟</span></td>
                <td class="min-w-36"><progress class="w-full" max="100" :value="task.progress ?? undefined" /><span class="text-subtle mt-1 block text-xs">{{ formatProgress(task.progress) }}</span></td>
                <td>{{ formatBytes(task.download_speed, '/s') }}<span class="text-subtle block text-xs">↑ {{ formatBytes(task.upload_speed, '/s') }}</span></td>
                <td>{{ formatBytes(task.bytes_completed) }}<span class="text-subtle block text-xs">/ {{ formatBytes(task.bytes_total) }}</span></td>
                <td>{{ formatETA(task.eta_seconds) }}</td>
                <td><div v-if="canReadTransfers && task.transfer_task_id || canControl(task) || canDelete(task)" class="flex flex-wrap gap-2"><RouterLink v-if="canReadTransfers && task.transfer_task_id" class="btn-secondary" :to="{ name: 'organization', query: { task: task.transfer_task_id, scope: task.lifecycle_scope === 'history' ? 'history' : 'active' } }">查看整理详情</RouterLink><button v-if="canControl(task) && ['queued','running','retry_wait'].includes(task.job_status)" class="btn-secondary" type="button" :disabled="saving || isTaskRetrying(task)" @click="control(task, 'pause')">暂停</button><button v-if="canControl(task) && task.job_status === 'paused'" class="btn-secondary" type="button" :disabled="saving || isTaskRetrying(task)" @click="control(task, 'resume')">恢复</button><button v-if="canControl(task) && isCompletedRecognitionFailure(task)" class="btn-secondary" type="button" :disabled="saving || isTaskRetrying(task)" @click="retryRecognitionImport(task)">{{ isTaskRetrying(task) ? '正在重试…' : '重新识别并入库' }}</button><button v-else-if="canControl(task) && task.job_status === 'failed'" class="btn-secondary" type="button" :disabled="saving || isTaskRetrying(task)" @click="control(task, 'retry')">{{ isTaskRetrying(task) ? '正在重试…' : '重试下载' }}</button><button v-if="canControl(task) && isCompletedRecognitionFailure(task)" class="btn-secondary" type="button" :disabled="saving || isTaskRetrying(task)" @click="openRecognitionRecovery(task)">人工介入</button><button v-if="canControl(task) && task.transfer_job_status === 'failed'" class="btn-secondary" type="button" :disabled="saving" @click="openRetarget(task)">修改入库目标</button><button v-if="canControl(task) && task.transfer_job_status === 'failed'" class="btn-secondary" type="button" :disabled="saving" @click="retryStage(task.transfer_job_id, '入库任务')">重试入库</button><button v-if="canControl(task) && !['completed','failed','cancelled'].includes(task.job_status)" class="btn-danger" type="button" :disabled="saving || isTaskRetrying(task)" @click="control(task, 'cancel')">取消并删除数据</button><button v-if="canDelete(task) && (['failed','cancelled'].includes(task.job_status) || task.lifecycle_scope === 'history')" class="btn-danger" type="button" :disabled="saving || isTaskRetrying(task)" @click="deleteDownload(task)">{{ task.job_status === 'completed' ? '删除历史记录' : '删除' }}</button></div><span v-else class="text-subtle text-xs">只读</span></td>
              </tr>
              <tr v-if="recognitionTaskID === task.id"><td colspan="8"><div class="semantic-inset grid gap-3 p-4"><div><strong>人工识别恢复</strong><p class="text-subtle mb-0 mt-1 text-xs">仅在自动识别失败后使用。搜索结果或手填 ID 都会由 Server 向 TMDB 重新验证，随后复用已经下载完成的文件继续入库。</p></div><div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_10rem_auto]"><input v-model="recognitionForm.keyword" class="input" maxlength="256" placeholder="输入中文、英文或原名关键词" @keyup.enter="searchRecognitionCandidates(task)" /><select v-model="recognitionForm.mediaType" class="input"><option value="">电影 + 剧集</option><option value="movie">电影</option><option value="tv">剧集</option></select><button class="btn-secondary" type="button" :disabled="recognitionSearching" @click="searchRecognitionCandidates(task)">{{ recognitionSearching ? '搜索中…' : '搜索 TMDB' }}</button></div><div v-if="recognitionCandidates.length" class="grid gap-2 md:grid-cols-2"><button v-for="candidate in recognitionCandidates" :key="`${candidate.media_type}-${candidate.id}`" class="semantic-list-item flex items-center justify-between gap-3 p-3 text-left" :class="{ 'semantic-list-item--selected': recognitionForm.tmdbID === candidate.id && recognitionForm.mediaType === candidate.media_type }" type="button" @click="selectRecognitionCandidate(candidate)"><span><strong>{{ candidate.title }}</strong><small v-if="candidate.original_title && candidate.original_title !== candidate.title" class="text-subtle mt-1 block">{{ candidate.original_title }}</small><small class="text-subtle mt-1 block">{{ candidate.media_type === 'tv' ? '剧集' : '电影' }} · {{ candidate.release_year || '年份未知' }} · TMDB {{ candidate.id }}</small></span><span>{{ Math.round(candidate.confidence * 100) }}%</span></button></div><div v-if="recognitionForm.mediaType === 'tv'" class="grid gap-3 md:grid-cols-2"><div><label class="label" for="recognition-season">季号（可选）</label><input id="recognition-season" v-model.number="recognitionForm.season" class="input" type="number" min="0" max="200" step="1" placeholder="留空自动检测" /></div><div><label class="label" for="recognition-episode">集号（可选）</label><input id="recognition-episode" v-model.number="recognitionForm.episode" class="input" type="number" min="1" max="100000" step="1" placeholder="留空自动检测" /></div><p class="text-subtle mb-0 text-xs md:col-span-2">默认使用 Server 自动检测结果，只在检测错误时修改。单视频任务可指定集号；只填集号时按 S01 处理，特别篇可填写 S00。</p></div><div class="grid gap-3 md:grid-cols-[10rem_12rem_auto]"><select v-model="recognitionForm.mediaType" class="input" aria-label="直接指定媒体类型"><option value="" disabled>选择媒体类型</option><option value="movie">电影</option><option value="tv">剧集</option></select><input v-model.number="recognitionForm.tmdbID" class="input" type="number" min="1" step="1" placeholder="TMDB ID" /><button class="btn-primary" type="button" :disabled="saving || !recognitionForm.tmdbID || !recognitionForm.mediaType" @click="submitRecognitionOverride(task)">验证并继续入库</button></div></div></td></tr>
            </template>
          </tbody>
        </table>
      </div>
    </section>

    <section v-if="activeSection === 'seeding'" id="download-panel-seeding" class="mt-6" role="tabpanel" aria-labelledby="download-tab-seeding"><div><h2 class="m-0 text-xl">做种管理</h2><p class="text-subtle mb-0 mt-1 text-xs">复制和软链接入库任务在这里跟踪。复制清理源文件；软链接永远保留源文件。</p></div>
      <div v-if="seedingTasks.length === 0" class="panel mt-5">还没有做种任务。</div>
      <div v-else class="downloader-grid mt-5"><article v-for="task in seedingTasks" :key="task.id" class="panel"><div class="flex items-start justify-between gap-3"><div><h3 class="m-0 text-base">{{ task.display_name || '未命名下载' }}</h3><p class="text-subtle mb-0 mt-1 text-xs">{{ task.downloader_name }} · {{ task.transfer_mode === 'copy' ? '复制入库' : '软链接入库' }} · {{ task.cleanup_enabled ? '自动清理' : '保留做种' }}</p></div><span :class="task.phase === 'failed' ? 'status-chip status-chip--error' : task.phase === 'completed' ? 'status-chip' : 'status-chip status-chip--ready'">{{ seedingPhaseLabel(task.phase) }}</span></div><dl class="mt-5 grid grid-cols-2 gap-3 text-sm"><div><dt class="text-subtle text-xs">分享率</dt><dd class="m-0 mt-1 font-700">{{ task.ratio == null ? '未知' : task.ratio.toFixed(2) }} / {{ task.minimum_ratio || '关闭' }}</dd></div><div><dt class="text-subtle text-xs">做种时长</dt><dd class="m-0 mt-1 font-700">{{ seededTime(task.seeded_seconds) }} / {{ task.minimum_seed_minutes ? `${task.minimum_seed_minutes} 分钟` : '关闭' }}</dd></div><div><dt class="text-subtle text-xs">已上传</dt><dd class="m-0 mt-1 font-700">{{ formatBytes(task.uploaded_bytes) }}</dd></div><div><dt class="text-subtle text-xs">清理动作</dt><dd class="m-0 mt-1 font-700">{{ task.delete_data ? '删任务和源文件' : '只删任务' }}</dd></div></dl><div v-if="canStopSeeding(task)" class="mt-5 flex flex-wrap gap-2"><button v-if="task.job_status === 'failed'" class="btn-secondary" type="button" :disabled="saving" @click="retryStage(task.job_id, '做种任务')">重试做种</button><button class="btn-danger" type="button" :disabled="saving" @click="stopSeeding(task)">立即停止做种</button></div></article></div>
    </section>

    <section v-if="activeSection === 'downloaders'" id="download-panel-downloaders" class="mt-6" role="tabpanel" aria-labelledby="download-tab-downloaders"><div class="flex flex-wrap items-end justify-between gap-4"><div><h2 class="m-0 text-xl">下载器管理</h2><p class="text-subtle mb-0 mt-1 text-xs">下载器只保存连接能力；统一暂存目录在系统设置中配置。</p></div><button v-if="auth.can(Permissions.DownloadersCreate)" class="btn-primary" @click="createOpen = !createOpen">{{ createOpen ? '取消添加' : '添加下载器' }}</button></div>
      <form v-if="createOpen" class="panel mt-5 grid gap-4 md:grid-cols-2" @submit.prevent="createDownloader"><div><label class="label">名称</label><input v-model="createForm.name" class="input" required maxlength="128" /></div><div><label class="label">下载器类型</label><select v-model="createForm.type" class="input" aria-describedby="downloader-type-help"><option v-for="option in providerOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select><p id="downloader-type-help" class="text-subtle mb-0 mt-2 text-xs">115 使用网盘自带的离线下载能力，不是 BT 客户端，因此不会进入做种管理。</p></div><div v-if="createForm.type === 'qbittorrent'" class="md:col-span-2"><label class="label">qBittorrent Web UI 地址</label><input v-model="createForm.baseURL" class="input" required placeholder="http://127.0.0.1:WebUI端口" /><p class="text-subtle mb-0 mt-2 text-xs">端口以 qBittorrent「选项 → Web UI」中显示的端口为准，不是 BT 监听端口。</p></div><div v-if="createForm.type === 'qbittorrent'"><label class="label">Web UI 用户名</label><input v-model="createForm.username" class="input" autocomplete="username" /></div><div v-if="createForm.type === 'qbittorrent'"><label class="label">Web UI 密码</label><SecretInput v-model="createForm.password" class="input" autocomplete="new-password" /></div><div v-if="createForm.type === 'pan115_offline'" class="md:col-span-2"><div class="semantic-inset mb-4 p-3 text-sm"><strong>115 网盘原生离线下载</strong><span class="text-subtle mt-1 block text-xs">任务由 115 云端完成；不支持暂停、恢复或做种，也不会创建做种任务。</span></div><label class="label">115 数据源</label><select v-model.number="createForm.storageID" class="input" required @change="resetCreateDirectory"><option :value="0" disabled>请选择支持离线下载的 115 数据源</option><option v-for="storage in pan115Storages" :key="storage.id" :value="storage.id">{{ storage.name }} · {{ storage.root_display_path || '/' }}</option></select><label class="label mt-4">数据源内下载目录</label><div class="flex gap-2"><input class="input font-mono" :value="createForm.directoryPath" readonly placeholder="请浏览并选择目录" /><button class="btn-secondary" type="button" :disabled="!createForm.storageID" @click="openDownloaderPicker('create')">浏览</button></div><p v-if="pan115Storages.length === 0" class="semantic-warning mb-0 mt-3 p-3 text-xs">当前没有可用的 115 数据源目录。请先在“数据源”中创建并启用 115 数据源。</p><p v-else class="text-subtle mb-0 mt-2 text-xs">可选择数据源根目录或其任意子目录；目录身份由 Server 校验并保存。</p></div><label class="text-muted flex items-center gap-3 text-sm"><input v-model="createForm.enabled" type="checkbox" />创建后启用</label><button class="btn-primary md:col-span-2" :disabled="saving || (createForm.type === 'pan115_offline' && (!createForm.storageID || !createForm.directoryToken))">保存下载器</button></form>
      <div v-if="!canReadDownloaders" class="panel mt-5 opacity-75">当前账户没有下载器配置查看权限。</div><div v-else-if="downloaders.length === 0" class="panel mt-5">还没有下载器配置。</div>
      <div v-else class="downloader-grid mt-5"><article v-for="item in downloaders" :key="item.id" class="panel downloader-card"><header><div><div class="flex flex-wrap items-center gap-2"><h3 class="m-0 text-lg">{{ item.name }}</h3><span :class="healthClass(item)">{{ healthLabel(item) }}</span><span v-if="item.type === 'pan115_offline'" class="status-chip">不支持做种</span><span v-if="!item.enabled" class="status-chip status-chip--warning">已停用</span></div><p class="text-subtle mb-0 mt-1 text-xs">{{ item.type === 'qbittorrent' ? 'qBittorrent' : item.type === 'pan115_offline' ? '115 网盘原生离线下载' : 'Fake provider' }} · {{ item.health.version || healthDetail(item) }}</p></div><div class="flex gap-2"><button v-if="auth.can(Permissions.DownloadersTest)" class="btn-secondary" type="button" :disabled="saving" @click="testDownloader(item)">测试</button><button v-if="auth.can(Permissions.DownloadersUpdate)" class="btn-secondary" type="button" @click="startEdit(item)">编辑</button></div></header><dl><div><dt>Server 运行中</dt><dd>{{ stats(item).active }}</dd></div><div><dt>Server 跟踪记录</dt><dd>{{ stats(item).total }}</dd></div><div><dt>运行任务平均进度</dt><dd>{{ formatProgress(stats(item).averageProgress) }}</dd></div><div><dt>已采样实时下载</dt><dd>{{ formatBytes(stats(item).downloadSpeed, '/s') }}</dd></div></dl><p class="text-subtle mb-0 mt-3 text-xs">这里只聚合 OhMyCine 正在运行的任务采样，不代表下载器原生界面中的任务总数。</p><div class="semantic-inset mt-4 p-3"><span class="text-subtle block text-xs">{{ item.type === 'pan115_offline' ? '云端目标目录' : '连接地址' }}</span><span class="mt-1 block truncate font-mono text-xs">{{ item.type === 'pan115_offline' ? `${item.storage_name} · ${item.provider_directory_path || '/'}` : item.base_url || '内置测试 provider' }}</span></div><footer class="text-subtle mt-3 flex flex-wrap justify-between gap-2 text-xs"><span>{{ item.health.status === 'offline' ? healthDetail(item) : item.type === 'pan115_offline' ? '复用 115 数据源凭据 · 不进入做种管理' : item.username_configured ? '已配置账户' : '未配置账户' }}</span><span>{{ formatSampleTime(item.health.last_checked_at) }}</span></footer></article></div>

      <form v-if="editing" class="panel mt-5" @submit.prevent="saveDownloader"><div class="flex items-start justify-between gap-3"><div><h3 class="m-0">编辑 {{ editing.name }}</h3><p class="text-subtle mb-0 mt-1 text-xs">只在这里显示连接信息；留空的新凭据不会覆盖已保存凭据。“保存并测试”会先加密保存当前表单，再测试同一份配置。</p></div><button class="btn-secondary" type="button" @click="editingID = null">关闭</button></div><div class="mt-5 grid gap-4 md:grid-cols-2"><div><label class="label">名称</label><input v-model="editForm.name" class="input" /></div><div><label class="label">类型</label><input class="input" :value="editing.type" disabled /></div><div v-if="editing.type === 'qbittorrent'" class="md:col-span-2"><label class="label">qBittorrent Web UI 地址</label><input v-model="editForm.baseURL" class="input" /></div><div v-if="editing.type === 'qbittorrent'"><label class="label">新用户名（留空保留）</label><SecretInput v-model="editForm.username" class="input" :configured="editing.username_configured" :load-secret="auth.can(Permissions.ConnectionsSecretsExport) ? credentialLoader({ resourceType: 'downloader', resourceID: editing.id, field: 'username' }) : undefined" :reset-key="editing.id" autocomplete="off" /><label class="text-muted mt-2 flex items-center gap-2 text-xs"><input v-model="editForm.clearUsername" type="checkbox" />清除用户名</label></div><div v-if="editing.type === 'qbittorrent'"><label class="label">新密码（留空保留）</label><SecretInput v-model="editForm.password" class="input" :configured="editing.password_configured" :load-secret="auth.can(Permissions.ConnectionsSecretsExport) ? credentialLoader({ resourceType: 'downloader', resourceID: editing.id, field: 'password' }) : undefined" :reset-key="editing.id" autocomplete="new-password" /><label class="text-muted mt-2 flex items-center gap-2 text-xs"><input v-model="editForm.clearPassword" type="checkbox" />清除密码</label></div><div v-if="editing.type === 'pan115_offline'" class="md:col-span-2"><label class="label">115 数据源</label><select v-model.number="editForm.storageID" class="input" required @change="resetEditDirectory"><option :value="0" disabled>请选择支持离线下载的 115 数据源</option><option v-for="storage in pan115Storages" :key="storage.id" :value="storage.id">{{ storage.name }} · {{ storage.root_display_path || '/' }}</option></select><label class="label mt-4">数据源内下载目录</label><div class="flex gap-2"><input class="input font-mono" :value="editForm.directoryPath" readonly placeholder="请浏览并选择目录" /><button class="btn-secondary" type="button" :disabled="!editForm.storageID" @click="openDownloaderPicker('edit')">重新选择</button></div></div><label class="text-muted flex items-center gap-3 text-sm"><input v-model="editForm.enabled" type="checkbox" />启用下载器</label></div><div class="mt-5 flex flex-wrap gap-3"><button class="btn-primary" :disabled="saving || (editing.type === 'pan115_offline' && !editForm.directoryPath)">保存配置</button><button v-if="auth.can(Permissions.DownloadersTest)" class="btn-secondary" type="button" :disabled="saving || (editing.type === 'pan115_offline' && !editForm.directoryPath)" @click="saveAndTestDownloader">保存并测试</button><button v-if="auth.can(Permissions.DownloadersDelete)" class="btn-danger" type="button" :disabled="saving" @click="deleteDownloader">删除配置</button></div></form>
    </section>
    <DirectoryPickerDialog :open="downloaderPickerOpen" :storage-id="downloaderPickerStorageID" :restrict-to-storage="true" @close="downloaderPickerOpen = false" @select="chooseDownloaderDirectory" />
    <div v-if="retargetTask" class="modal-backdrop fixed inset-0 z-60 flex items-center justify-center p-4" @click.self="!saving && (retargetTask = null)"><form class="panel w-full max-w-lg" role="dialog" aria-modal="true" aria-labelledby="download-retarget-title" @submit.prevent="confirmRetarget"><h2 id="download-retarget-title" class="m-0 text-xl">修改入库目标</h2><p class="page-description mt-2">{{ retargetTask.scrape_title || retargetTask.display_name }}</p><p class="semantic-warning mt-4 p-3 text-sm">复用已完成文件，只重建目标媒体库、分类与命名快照后继续入库。已发生任何部分写入时会安全拒绝。</p><label class="mt-4 block"><span class="label">新的目标媒体库</span><select v-model.number="retargetLibraryID" class="input" required><option :value="0" disabled>请选择</option><option v-for="library in mediaLibraries.filter(item => item.enabled && item.id !== retargetTask?.target_library_id)" :key="library.id" :value="library.id">{{ library.name }} · {{ library.storage_name }} · {{ library.transfer_mode }}</option></select></label><div class="mt-5 flex justify-end gap-3"><button class="btn-secondary" type="button" :disabled="saving" @click="retargetTask = null">取消</button><button class="btn-primary" :disabled="saving || !retargetLibraryID">{{ saving ? '正在校验并重排…' : '确认修改并重试入库' }}</button></div></form></div>
  </section>
</template>

<style scoped>
.downloader-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 22rem), 1fr)); gap: 1rem; }
.downloader-card { display: flex; min-height: 18rem; flex-direction: column; }
.downloader-card header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
.downloader-card dl { display: grid; grid-template-columns: repeat(2, 1fr); gap: .75rem; margin: 1.2rem 0 0; }
.downloader-card dl div { border-left: 3px solid var(--border-strong); padding-left: .65rem; }
.downloader-card dt { color: var(--text-subtle); font-size: .7rem; }
.downloader-card dd { margin: .25rem 0 0; color: var(--text); font-size: 1rem; font-weight: 750; }
.downloader-card dd small { color: var(--text-subtle); font-size: .7rem; font-weight: 500; }
.downloader-card footer { margin-top: auto; padding-top: .8rem; }
@media (max-width: 520px) { .downloader-card header { align-items: stretch; flex-direction: column; }.downloader-card header > div:last-child { width: 100%; }.downloader-card header button { flex: 1; } }
</style>
