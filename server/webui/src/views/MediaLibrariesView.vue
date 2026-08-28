<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import DirectoryPickerDialog from '@/components/DirectoryPickerDialog.vue'
import MediaReorganizationDialog from '@/components/MediaReorganizationDialog.vue'
import MediaLibrarySettingsFields from '@/components/MediaLibrarySettingsFields.vue'
import { clearDefaultIngestLibrary, draftFromLibrary, emptyMediaLibraryDraft, isActiveLibraryStatus, mediaLibrarySourceDisplayPath, payloadFromDraft, presentLibraryStatus, setDefaultIngestLibrary, supportsSidecarUpload, supportsSTRM, type MediaLibraryDraft } from '@/media-libraries'
import { mediaCatalogDetailEndpoint, mediaCatalogEndpoint, mediaCatalogPageCount, mediaCatalogPageSizes, mediaCatalogVisibleRange, type MediaCatalogMatchFilter, type MediaCatalogPageSize, type MediaCatalogTypeFilter } from '@/media-catalog'
import { useAuthStore } from '@/stores/auth'
import type { ListResponse, MediaCatalogDetail, MediaCatalogItem, MediaCatalogManagedTransfer, MediaClassificationProfileSummary, MediaLibraryDetail, MediaLibraryScanRun, MediaRecognitionSummary, PageResponse, StorageSummary, TMDBCandidate } from '@/types/api'

type DetailTab = 'status' | 'runs' | 'entries' | 'settings'
type PickerTarget = 'source' | 'strm'
type CatalogMatchView = MediaCatalogMatchFilter | 'manual'

const auth = useAuthStore()
const libraries = ref<MediaLibraryDetail[]>([])
const storages = ref<StorageSummary[]>([])
const profiles = ref<MediaClassificationProfileSummary[]>([])
const selectedID = ref<number | null>(null)
const runs = ref<MediaLibraryScanRun[]>([])
const catalog = ref<MediaCatalogItem[]>([])
const catalogTotal = ref(0)
const catalogPage = ref(1)
const catalogPageSize = ref<MediaCatalogPageSize>(50)
const catalogSearch = ref('')
const catalogType = ref<MediaCatalogTypeFilter>('')
const catalogMatch = ref<CatalogMatchView>('')
const catalogLoading = ref(false)
const catalogError = ref('')
const recognitions = ref<MediaRecognitionSummary[]>([])
const recognitionLoading = ref(false)
const candidateToken = ref('')
const candidates = ref<TMDBCandidate[]>([])
const expandedWorkIDs = ref<string[]>([])
const catalogDetails = ref<Record<string, MediaCatalogDetail>>({})
const detailLoadingIDs = ref<string[]>([])
const reorganizationTarget = ref<{ work: MediaCatalogItem; transfer: MediaCatalogManagedTransfer } | null>(null)
const createOpen = ref(false)
const createDraft = ref<MediaLibraryDraft>(emptyMediaLibraryDraft())
const editDraft = ref<MediaLibraryDraft | null>(null)
const activeTab = ref<DetailTab>('status')
const pickerOpen = ref(false)
const pickerTarget = ref<PickerTarget>('source')
const pickerMode = ref<'create' | 'edit'>('create')
const loading = ref(true)
const refreshing = ref(false)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const editDirty = ref(false)
const draggedLibraryID = ref<number | null>(null)
let pollTimer: number | undefined
let runsRequest: AbortController | null = null
let catalogRequest: AbortController | null = null
const detailRequests = new Map<string, AbortController>()

const selected = computed(() => libraries.value.find(item => item.id === selectedID.value) ?? null)
const selectedStorage = computed(() => storages.value.find(item => item.id === selected.value?.storage_id))
const activeDraft = computed(() => pickerMode.value === 'create' ? createDraft.value : editDraft.value)
const createStorage = computed(() => storages.value.find(item => item.id === createDraft.value.storage_id))
const editStorage = computed(() => storages.value.find(item => item.id === editDraft.value?.storage_id))
const selectedSourceDisplay = computed(() => selected.value ? mediaLibrarySourceDisplayPath(selected.value, storages.value.find(item => item.id === selected.value?.storage_id)) : '')
const shouldPoll = computed(() => activeTab.value !== 'settings' && !editDirty.value && (libraries.value.some(item => isActiveLibraryStatus(item.status) || (item.enabled && item.status === 'initialization_failed')) || runs.value.some(run => run.status === 'running')))
const catalogPages = computed(() => mediaCatalogPageCount(catalogTotal.value, catalogPageSize.value))
const catalogRange = computed(() => mediaCatalogVisibleRange(catalogPage.value, catalogPageSize.value, catalogTotal.value))

function message(reason: unknown) { return reason instanceof Error ? reason.message : '请求失败' }
function dateTime(value: string | null) { return value ? new Date(value).toLocaleString() : '尚无记录' }
function bytes(value: number) { const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; let amount = value; let index = 0; while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ } return `${amount.toFixed(index === 0 ? 0 : 1)} ${units[index]}` }
function episodeLabel(entry: { season: number | null; episode: number | null }) { return entry.episode === null ? '' : `${entry.season === null ? '' : `S${String(entry.season).padStart(2, '0')}`}E${String(entry.episode).padStart(2, '0')}` }
function seasonLabel(number: number) { return number === 0 ? '未分季' : `第 ${number} 季` }
function scanKind(kind: string) { return ({ initial: '首次全量', catch_up: '监听交接对账', event: '文件事件', incremental: '定时增量', full: '周期全量', manual: '手动跟进' } as Record<string, string>)[kind] ?? kind }
function scanStatus(run: MediaLibraryScanRun) { if (run.status === 'failed') return { label: '失败', className: 'status-chip status-chip--error' }; if (run.status === 'running') return { label: '运行中', className: 'status-chip status-chip--warning' }; return { label: run.partial ? '部分完成' : '成功', className: run.partial ? 'status-chip status-chip--warning' : 'status-chip status-chip--ready' } }

async function persistOrder(next: MediaLibraryDetail[]) {
  const previous = libraries.value
  libraries.value = next
  try {
    const data = await api<ListResponse<MediaLibraryDetail>>('/api/v1/media-libraries/order', { method: 'PUT', body: JSON.stringify({ ids: next.map(item => item.id) }) })
    libraries.value = data.list
    notice.value = '媒体库默认选择顺序已保存。'
  } catch (reason) {
    libraries.value = previous
    error.value = message(reason)
  }
}

function moveLibrary(id: number, direction: -1 | 1) {
  const index = libraries.value.findIndex(item => item.id === id)
  const target = index + direction
  if (index < 0 || target < 0 || target >= libraries.value.length) return
  const next = [...libraries.value]
  ;[next[index], next[target]] = [next[target]!, next[index]!]
  void persistOrder(next)
}

function dropLibrary(targetID: number) {
  const sourceID = draggedLibraryID.value
  draggedLibraryID.value = null
  if (sourceID === null || sourceID === targetID) return
  const sourceIndex = libraries.value.findIndex(item => item.id === sourceID)
  const targetIndex = libraries.value.findIndex(item => item.id === targetID)
  if (sourceIndex < 0 || targetIndex < 0) return
  const next = [...libraries.value]
  const [moved] = next.splice(sourceIndex, 1)
  next.splice(targetIndex, 0, moved!)
  void persistOrder(next)
}

async function load(options: { quiet?: boolean; preferred?: number } = {}) {
  if (refreshing.value) return
  refreshing.value = true
  if (!options.quiet) error.value = ''
  try {
    const [libraryData, storageData, profileData] = await Promise.all([
      api<ListResponse<MediaLibraryDetail>>('/api/v1/media-libraries'),
      auth.can(Permissions.StoragesRead) ? api<ListResponse<StorageSummary>>('/api/v1/storages') : Promise.resolve({ list: [], total: 0 }),
      auth.can(Permissions.MediaClassificationProfilesRead) ? api<ListResponse<MediaClassificationProfileSummary>>('/api/v1/media-classification-profiles') : Promise.resolve({ list: [], total: 0 }),
    ])
    libraries.value = libraryData.list
    storages.value = storageData.list.filter(item => item.enabled)
    profiles.value = profileData.list
    const preferred = options.preferred ?? selectedID.value
    selectedID.value = libraries.value.some(item => item.id === preferred) ? preferred : libraries.value[0]?.id ?? null
    if (selected.value) await loadActivity(selected.value.id)
    schedulePoll()
  } catch (reason) {
    if (!options.quiet) error.value = message(reason)
  } finally { refreshing.value = false; loading.value = false }
}

async function loadActivity(id: number) {
	await Promise.all([loadRuns(id), activeTab.value === 'entries' ? loadCatalog(id) : Promise.resolve()])
}

async function loadRuns(id: number) {
  runsRequest?.abort()
  const controller = new AbortController()
  runsRequest = controller
  try {
    const data = await api<ListResponse<MediaLibraryScanRun>>(`/api/v1/media-libraries/${id}/runs?limit=30`, { signal: controller.signal })
    if (!controller.signal.aborted && selectedID.value === id) runs.value = data.list
  } catch (reason) {
    if (!controller.signal.aborted) throw reason
  } finally {
    if (runsRequest === controller) runsRequest = null
  }
}

async function loadCatalog(id = selectedID.value) {
  if (!id) return
  catalogRequest?.abort()
  const controller = new AbortController()
  catalogRequest = controller
  catalogLoading.value = true
  catalogError.value = ''
  try {
    if (catalogMatch.value === 'unrecognized' || catalogMatch.value === 'manual') {
      await loadRecognitions(id, controller)
      return
    }
    const data = await api<PageResponse<MediaCatalogItem>>(mediaCatalogEndpoint(id, { page: catalogPage.value, pageSize: catalogPageSize.value, query: catalogSearch.value, mediaType: catalogType.value, matchStatus: catalogMatch.value === 'matched' ? 'matched' : '' }), { signal: controller.signal })
    if (controller.signal.aborted || selectedID.value !== id) return
    catalog.value = data.list
    catalogTotal.value = data.total
    catalogPage.value = data.page
    expandedWorkIDs.value = expandedWorkIDs.value.filter(workID => data.list.some(item => item.id === workID))
  } catch (reason) {
    if (!controller.signal.aborted) catalogError.value = message(reason)
  } finally {
    if (catalogRequest === controller) { catalogRequest = null; catalogLoading.value = false }
  }
}

async function loadRecognitions(id: number, controller = new AbortController()) {
  recognitionLoading.value = true
  const params = new URLSearchParams({ status: catalogMatch.value === 'manual' ? 'matched' : 'unrecognized', page: String(catalogPage.value), page_size: String(catalogPageSize.value) })
  if (catalogMatch.value === 'manual') params.set('manual_only', 'true')
  if (catalogSearch.value.trim()) params.set('query', catalogSearch.value.trim())
  if (catalogType.value) params.set('media_type', catalogType.value)
  try {
    const data = await api<PageResponse<MediaRecognitionSummary>>(`/api/v1/media-libraries/${id}/recognitions?${params}`, { signal: controller.signal })
    if (!controller.signal.aborted && selectedID.value === id) { recognitions.value = data.list; catalogTotal.value = data.total; catalogPage.value = data.page }
  } finally { recognitionLoading.value = false }
}

async function retryRecognition(item: MediaRecognitionSummary) {
  if (!selectedID.value) return
  await run(async () => { await api(`/api/v1/media-libraries/${selectedID.value}/recognitions/${encodeURIComponent(item.token)}/retry`, { method: 'POST', body: '{}' }); notice.value = '已重新识别该项目。'; await loadCatalog() })
}

async function findCandidates(item: MediaRecognitionSummary) {
  if (!selectedID.value) return
  candidateToken.value = item.token; candidates.value = []; catalogError.value = ''
  const params = new URLSearchParams({ title: item.title || item.source_summary, media_type: item.media_type || 'movie' })
  if (item.release_year) params.set('year', String(item.release_year))
  try { const data = await api<ListResponse<TMDBCandidate>>(`/api/v1/media-libraries/${selectedID.value}/recognitions/${encodeURIComponent(item.token)}/tmdb-candidates?${params}`); candidates.value = data.list }
  catch (reason) { catalogError.value = message(reason) }
}

async function chooseCandidate(item: MediaRecognitionSummary, candidate: TMDBCandidate) {
  if (!selectedID.value) return
  await run(async () => { await api(`/api/v1/media-libraries/${selectedID.value}/recognitions/${encodeURIComponent(item.token)}/override`, { method: 'PUT', body: JSON.stringify({ tmdb_id: candidate.id, media_type: candidate.media_type }) }); candidateToken.value = ''; candidates.value = []; notice.value = '人工匹配已保存并重新分类。'; await loadCatalog() })
}

async function clearRecognitionOverride(item: MediaRecognitionSummary) {
  if (!selectedID.value) return
  await run(async () => { await api(`/api/v1/media-libraries/${selectedID.value}/recognitions/${encodeURIComponent(item.token)}/override`, { method: 'DELETE', body: '{}' }); candidateToken.value = ''; candidates.value = []; notice.value = '人工匹配已清除并恢复自动识别。'; await loadCatalog() })
}

async function applyCatalogFilters() {
  catalogPage.value = 1
  await loadCatalog()
}

async function changeCatalogPage(page: number) {
  if (page < 1 || page > catalogPages.value || page === catalogPage.value) return
  catalogPage.value = page
  await loadCatalog()
}

async function toggleCatalog(item: MediaCatalogItem) {
  if (expandedWorkIDs.value.includes(item.id)) {
    expandedWorkIDs.value = expandedWorkIDs.value.filter(id => id !== item.id)
    return
  }
  expandedWorkIDs.value = [...expandedWorkIDs.value, item.id]
  if (catalogDetails.value[item.id] || !selectedID.value) return
  const libraryID = selectedID.value
  detailRequests.get(item.id)?.abort()
  const controller = new AbortController()
  detailRequests.set(item.id, controller)
  detailLoadingIDs.value = [...detailLoadingIDs.value, item.id]
  try {
    const detail = await api<MediaCatalogDetail>(mediaCatalogDetailEndpoint(libraryID, item.id), { signal: controller.signal })
    if (!controller.signal.aborted && selectedID.value === libraryID) catalogDetails.value[item.id] = detail
  } catch (reason) {
    if (!controller.signal.aborted) catalogError.value = message(reason)
  } finally {
    if (detailRequests.get(item.id) === controller) detailRequests.delete(item.id)
    detailLoadingIDs.value = detailLoadingIDs.value.filter(id => id !== item.id)
  }
}

function openCatalogReorganization(work: MediaCatalogItem, transfer: MediaCatalogManagedTransfer) {
  reorganizationTarget.value = { work, transfer }
}

async function catalogReorganizationQueued() {
  if (!selectedID.value) return
  catalogDetails.value = {}
  expandedWorkIDs.value = []
  await loadCatalog(selectedID.value)
}

function resetCatalog() {
  catalogRequest?.abort()
  catalogRequest = null
  for (const controller of detailRequests.values()) controller.abort()
  detailRequests.clear()
  catalog.value = []
  catalogTotal.value = 0
  catalogPage.value = 1
  catalogSearch.value = ''
  catalogType.value = ''
  catalogMatch.value = ''
  catalogError.value = ''
  recognitions.value = []; candidateToken.value = ''; candidates.value = []
  expandedWorkIDs.value = []
  catalogDetails.value = {}
  detailLoadingIDs.value = []
}

function schedulePoll() {
  window.clearTimeout(pollTimer)
  if (shouldPoll.value) pollTimer = window.setTimeout(() => void load({ quiet: true }), 2000)
}

function beginCreate() {
  createDraft.value = emptyMediaLibraryDraft(storages.value[0]?.id, profiles.value[0]?.id)
  resetSourceSelection(createDraft.value, storages.value[0])
  createOpen.value = true; error.value = ''; notice.value = ''
}

function openPicker(mode: 'create' | 'edit', target: PickerTarget) {
  pickerMode.value = mode; pickerTarget.value = target; pickerOpen.value = true
}

function directorySelected(value: { path: string; token: string }) {
  const draft = activeDraft.value
  if (!draft) return
  if (pickerTarget.value === 'source') { draft.source_path = value.path; draft.relative_root_token = value.token; if (pickerMode.value === 'edit') editDirty.value = true }
  else { draft.strm_local_path = value.path; draft.strm_local_root_token = value.token }
  pickerOpen.value = false
}

function resetSourceSelection(draft: MediaLibraryDraft, storage?: StorageSummary) {
  draft.relative_root_token = ''
  draft.relative_root = '/'
  draft.source_path = ''
  if (storage?.type === 'pan115' && draft.transfer_mode === 'symlink') draft.transfer_mode = 'move'
}
function normalizeSTRM(draft: MediaLibraryDraft, storage: StorageSummary | undefined) {
  if (!supportsSTRM(storage)) { draft.strm_enabled = false; draft.strm_local_path = ''; draft.strm_local_root_token = '' }
  if (storage?.type === 'local') draft.upload_sidecars = false
  if (draft.strm_enabled || !supportsSidecarUpload(storage)) draft.upload_sidecars = false
}

async function createLibrary() {
  await run(async () => {
    const created = await api<MediaLibraryDetail>('/api/v1/media-libraries', { method: 'POST', body: JSON.stringify(payloadFromDraft(createDraft.value, createStorage.value)) })
    createOpen.value = false
    notice.value = created.enabled ? '媒体库已创建，首次全量扫描会自动开始。' : '媒体库配置已保存；启用后会自动执行首次全量扫描。'
    await load({ preferred: created.id })
  })
}

async function saveLibrary() {
  if (!selected.value || !editDraft.value) return
  const id = selected.value.id
  await run(async () => {
    await api<MediaLibraryDetail>(`/api/v1/media-libraries/${id}`, { method: 'PUT', body: JSON.stringify(payloadFromDraft(editDraft.value!, editStorage.value)) })
	editDirty.value = false
    notice.value = '媒体库配置已保存。启停、扫描计划和监听状态将按新配置生效。'
    await load({ preferred: id })
  })
}

async function scanNow() {
  if (!selected.value) return
  const id = selected.value.id
  await run(async () => { await api<MediaLibraryScanRun>(`/api/v1/media-libraries/${id}/scan`, { method: 'POST', body: '{}' }); notice.value = '手动跟进扫描已完成。'; await load({ preferred: id }) })
}

async function retryNow() {
  if (!selected.value) return
  const id = selected.value.id
  await run(async () => { await api(`/api/v1/media-libraries/${id}/retry`, { method: 'POST', body: '{}' }); notice.value = '已唤醒该媒体库的初始化重试。'; await load({ preferred: id }) })
}

async function removeLibrary() {
  if (!selected.value || !window.confirm(`确认删除媒体库“${selected.value.name}”？只会删除配置、索引和扫描记录，不会删除来源媒体文件。`)) return
  const id = selected.value.id
  await run(async () => { await api(`/api/v1/media-libraries/${id}`, { method: 'DELETE', body: '{}' }); selectedID.value = null; notice.value = '媒体库配置和索引已删除；来源媒体文件未改变。'; await load() })
}

async function setAutoListenDefault() {
  if (!selected.value) return
  await run(async () => {
    const result = await setDefaultIngestLibrary(selected.value!.id)
    notice.value = `已将“${result.media_library_name}”设为该 115 账号的自动监听默认入库库。`
    await load({ preferred: selected.value!.id })
  })
}

async function clearAutoListenDefault() {
  if (!selected.value?.connection_id || !window.confirm('确认取消该 115 账号的自动监听默认入库库？\n\n如果仍有下载器开启生活事件监听，Server 会拒绝此操作。')) return
  const id = selected.value.id
  await run(async () => {
    await clearDefaultIngestLibrary(selected.value!.connection_id!)
    notice.value = '已取消自动监听默认入库库。'
    await load({ preferred: id })
  })
}

async function run(action: () => Promise<void>) { saving.value = true; error.value = ''; notice.value = ''; try { await action() } catch (reason) { error.value = message(reason) } finally { saving.value = false } }

watch(selectedID, async id => {
	runs.value = []; resetCatalog(); activeTab.value = 'status'
  editDirty.value = false
  if (id) { const library = libraries.value.find(item => item.id === id); editDraft.value = library ? draftFromLibrary(library, storages.value.find(item => item.id === library.storage_id)) : null; try { await loadActivity(id) } catch (reason) { error.value = message(reason) } }
  else editDraft.value = null
})
watch(selected, library => { if (library && activeTab.value !== 'settings' && !editDirty.value) editDraft.value = draftFromLibrary(library, storages.value.find(item => item.id === library.storage_id)) })
watch(activeTab, tab => { if (tab === 'entries' && selectedID.value) void loadCatalog(selectedID.value) })
watch(() => createDraft.value.storage_id, () => { resetSourceSelection(createDraft.value, createStorage.value); normalizeSTRM(createDraft.value, createStorage.value) })
watch(() => editDraft.value?.storage_id, () => { if (editDraft.value && editDraft.value.storage_id !== selected.value?.storage_id) resetSourceSelection(editDraft.value, editStorage.value); if (editDraft.value) normalizeSTRM(editDraft.value, editStorage.value) })
onMounted(() => void load())
onBeforeUnmount(() => { window.clearTimeout(pollTimer); runsRequest?.abort(); resetCatalog() })
</script>

<template>
  <section class="mx-auto max-w-7xl">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div><h1 class="m-0 text-3xl font-800">媒体库</h1><p class="page-description mt-2 max-w-3xl">从已注册 Storage 选择受控相对根，建立只读扫描索引，并配置下载完成后的分类、命名和入库策略。</p></div>
      <div class="flex flex-wrap gap-2"><button class="btn-secondary" :disabled="refreshing" @click="load()">刷新</button><button v-if="auth.can(Permissions.MediaLibrariesCreate)" class="btn-primary" :disabled="!storages.length || !profiles.length || !auth.can(Permissions.StoragesBrowse)" @click="beginCreate">创建媒体库</button></div>
    </div>
    <p v-if="error" role="alert" class="semantic-error mt-5 p-3 text-sm">{{ error }}</p><p v-if="notice" role="status" class="semantic-success mt-5 p-3 text-sm">{{ notice }}</p>
    <p v-if="auth.can(Permissions.MediaLibrariesCreate) && (!auth.can(Permissions.StoragesRead) || !auth.can(Permissions.StoragesBrowse) || !auth.can(Permissions.MediaClassificationProfilesRead))" class="semantic-warning mt-5 p-3 text-sm">创建媒体库还需要 storages.read、storages.browse 与 media_classification_profiles.read，以安全选择来源和分类 Profile。</p>

    <form v-if="createOpen" class="panel mt-6" @submit.prevent="createLibrary">
      <div class="flex items-start justify-between gap-4"><div><h2 class="m-0 text-xl">创建媒体库</h2><p class="text-subtle mb-0 mt-1 text-sm">启用后自动进入首次全量扫描；无需额外点击扫描。</p></div><button type="button" class="btn-secondary" @click="createOpen = false">取消</button></div>
      <div class="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <div><label class="label" for="library-create-name">名称</label><input id="library-create-name" v-model="createDraft.name" class="input" required maxlength="128" placeholder="本地媒体库" /></div>
        <div><label class="label" for="library-create-storage">来源 Storage</label><select id="library-create-storage" v-model.number="createDraft.storage_id" class="input" required><option v-for="storage in storages" :key="storage.id" :value="storage.id">{{ storage.name }}（{{ storage.type }}）</option></select></div>
        <div><label class="label" for="library-create-profile">分类 Profile</label><select id="library-create-profile" v-model.number="createDraft.profile_id" class="input" required><option v-for="profile in profiles" :key="profile.id" :value="profile.id">{{ profile.name }} · r{{ profile.revision }}</option></select></div>
        <div class="md:col-span-2 xl:col-span-3"><label class="label">来源相对根</label><div class="flex gap-2"><input class="input min-w-0 font-mono" :value="createDraft.source_path" readonly required placeholder="请从来源 Storage 内选择目录；Storage 根会保存为 /" /><button v-if="createStorage && auth.can(Permissions.StoragesBrowse)" type="button" class="btn-secondary shrink-0" @click="openPicker('create', 'source')">选择目录</button></div><p class="text-subtle mb-0 mt-2 text-xs">目录选择令牌绑定当前用户、Connection、Storage 和 Storage 根；115 扫描从所选目录的稳定 file_id 开始。</p></div>
        <label class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.enabled" type="checkbox" />创建后启用并自动初始化</label><label class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.recursive" type="checkbox" />递归扫描子目录</label>
        <label class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.metadata_artifacts_enabled" type="checkbox" />生成 NFO / 图片元数据</label>
        <template v-if="supportsSTRM(createStorage)"><label class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.strm_enabled" type="checkbox" @change="normalizeSTRM(createDraft, createStorage)" />启用 signed 302 / STRM</label><div v-if="createDraft.strm_enabled" class="md:col-span-2 xl:col-span-3"><label class="label">本地 STRM 输出目录</label><div class="flex gap-2"><input class="input" :value="createDraft.strm_local_path" readonly required /><button type="button" class="btn-secondary" @click="openPicker('create', 'strm')">选择目录</button></div><p class="text-subtle mb-0 mt-2 text-xs">视频生成 STRM；NFO、海报、字幕和图片按同一目录结构生成在这里，不上传到网盘。</p></div></template>
        <label v-if="supportsSidecarUpload(createStorage) && !createDraft.strm_enabled" class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.upload_sidecars" type="checkbox" :disabled="!createDraft.metadata_artifacts_enabled" />将 NFO / JPG 上传到云端媒体旁</label>
      </div>
      <details class="semantic-inset mt-5 p-4"><summary class="cursor-pointer font-650">扫描、限速与匹配配置</summary><MediaLibrarySettingsFields v-model="createDraft" class="mt-4" :storage-type="createStorage?.type" /></details>
      <button class="btn-primary mt-5" :disabled="saving || !createStorage || !createDraft.relative_root_token || (createDraft.strm_enabled && !createDraft.strm_local_path)">创建媒体库</button>
    </form>

    <div v-if="loading" class="panel mt-7">正在加载媒体库…</div>
    <div v-else-if="libraries.length === 0" class="panel mt-7"><h2 class="m-0 text-lg">尚未创建媒体库</h2><p class="page-description mb-0 mt-2 text-sm">先注册可读 Storage 并准备分类 Profile，再选择来源相对根。扫描和监听始终只读；只有已确认目标的独立入库任务会按这里的策略写入。</p></div>
    <div v-else class="mt-7 grid gap-5 xl:grid-cols-[minmax(18rem,.68fr)_minmax(0,1.7fr)]">
      <aside class="panel p-2">
        <div
          v-for="(library, index) in libraries"
          :key="library.id"
          class="semantic-list-item mb-1 flex items-stretch gap-1 p-1"
          :class="{ 'semantic-list-item--selected': selectedID === library.id }"
          draggable="true"
          @dragstart="draggedLibraryID = library.id"
          @dragend="draggedLibraryID = null"
          @dragover.prevent
          @drop.prevent="dropLibrary(library.id)"
        >
          <button class="btn-quiet shrink-0 cursor-grab px-2" type="button" aria-label="拖动媒体库排序" title="拖动排序">⋮⋮</button>
          <button type="button" class="min-w-0 flex-1 p-2 text-left" @click="selectedID = library.id">
            <div class="flex items-center justify-between gap-3"><strong class="truncate">{{ library.name }}</strong><span :class="presentLibraryStatus(library.status).className">{{ presentLibraryStatus(library.status).label }}</span></div>
            <div class="text-subtle mt-2 text-xs">第 {{ index + 1 }} 顺位 · {{ library.storage_name }} · {{ mediaLibrarySourceDisplayPath(library, storages.find(item => item.id === library.storage_id)) }}（相对根 {{ library.relative_root }}） · {{ library.entry_count }} 条目</div>
            <div class="text-subtle mt-1 text-xs">{{ library.transfer_mode === 'move' ? '移动' : library.transfer_mode === 'copy' ? '复制' : '软链接' }} · {{ library.profile_name }}</div>
            <div v-if="library.reclassification_due" class="semantic-warning-text mt-2 text-xs">分类规则已更新，待重分类</div>
          </button>
          <div v-if="auth.can(Permissions.MediaLibrariesUpdate)" class="flex shrink-0 flex-col justify-center gap-1">
            <button class="btn-quiet px-2" type="button" :disabled="index === 0 || saving" aria-label="上移媒体库" @click="moveLibrary(library.id, -1)">↑</button>
            <button class="btn-quiet px-2" type="button" :disabled="index === libraries.length - 1 || saving" aria-label="下移媒体库" @click="moveLibrary(library.id, 1)">↓</button>
          </div>
        </div>
      </aside>
      <main v-if="selected" class="min-w-0">
        <section class="panel">
          <div class="flex flex-wrap items-start justify-between gap-4"><div><div class="flex flex-wrap items-center gap-2"><h2 class="m-0">{{ selected.name }}</h2><span :class="presentLibraryStatus(selected.status).className">{{ presentLibraryStatus(selected.status).label }}</span></div><p class="text-subtle mb-0 mt-2 text-sm">{{ selected.storage_name }} · {{ selectedSourceDisplay }}（相对根 {{ selected.relative_root }}） · Profile {{ selected.profile_name }} r{{ selected.profile_revision }}</p></div><div class="flex flex-wrap gap-2"><button v-if="selected.status === 'initialization_failed' && auth.can(Permissions.MediaLibrariesScan)" type="button" class="btn-primary" :disabled="saving" @click="retryNow">立即重试</button><button v-if="selected.enabled && selected.status !== 'initializing' && auth.can(Permissions.MediaLibrariesScan)" type="button" class="btn-secondary" :disabled="saving" @click="scanNow">立即扫描</button><button v-if="auth.can(Permissions.MediaLibrariesDelete)" type="button" class="btn-danger" :disabled="saving" @click="removeLibrary">删除配置</button></div></div>
          <p v-if="selected.status === 'initialization_failed'" class="semantic-error mt-4 p-3 text-sm">初始化失败：{{ selected.status_error_code || 'media_library_scan_failed' }}。失败库不会启动监听；下次自动重试：{{ dateTime(selected.next_retry_at) }}。</p>
          <p v-if="selected.reclassification_due" class="semantic-warning mt-4 p-3 text-sm">所选 Profile 已更新。下一次扫描会重新应用分类，但不会移动、重命名或写回来源文件。<RouterLink class="semantic-link ml-1" to="/system/media-rules">打开规则管理</RouterLink></p>
        </section>

        <section v-if="selectedStorage?.type === 'pan115'" class="semantic-inset mt-4 p-4">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div><div class="flex flex-wrap items-center gap-2"><strong>自动监听默认入库库</strong><span v-if="selected.auto_listen_default" class="status-chip status-chip--ready">当前默认</span></div><p class="text-subtle mb-0 mt-1 text-xs">同一 115 账号只能有一个。它只接收 115 App 手工放入下载目录的内容；离线下载、转存、站点下载和追更仍使用各自任务的目标。</p></div>
            <div v-if="auth.can(Permissions.MediaLibrariesUpdate)" class="flex gap-2"><button v-if="!selected.auto_listen_default" type="button" class="btn-secondary" :disabled="saving || !selected.enabled" @click="setAutoListenDefault">设为默认</button><button v-else type="button" class="btn-danger" :disabled="saving" @click="clearAutoListenDefault">取消默认</button></div>
          </div>
        </section>

        <div class="management-tabs mt-4" role="tablist" aria-label="媒体库详情"><button v-for="tab in ([['status','状态'],['runs','扫描记录'],['entries','媒体清单'],['settings','配置']] as const)" :id="`library-tab-${tab[0]}`" :key="tab[0]" type="button" class="management-tab" :class="activeTab === tab[0] ? 'management-tab--active' : ''" role="tab" :aria-selected="activeTab === tab[0]" :aria-controls="`library-panel-${tab[0]}`" @click="activeTab = tab[0]">{{ tab[1] }}</button></div>

        <section v-if="activeTab === 'status'" id="library-panel-status" class="panel mt-4" role="tabpanel" aria-labelledby="library-tab-status"><div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"><div class="semantic-inset p-3"><span class="text-subtle text-xs">媒体条目</span><strong class="mt-1 block">{{ selected.entry_count }}</strong></div><div class="semantic-inset p-3"><span class="text-subtle text-xs">基线 / dirty generation</span><strong class="mt-1 block">{{ selected.baseline_generation }} / {{ selected.dirty_generation }}</strong></div><div class="semantic-inset p-3"><span class="text-subtle text-xs">最近成功扫描</span><strong class="mt-1 block text-sm">{{ dateTime(selected.last_successful_scan_at) }}</strong></div><div class="semantic-inset p-3"><span class="text-subtle text-xs">监听方式</span><strong class="mt-1 block">{{ selected.enabled ? 'Storage driver 自动选择' : '未运行' }}</strong></div></div><p class="text-subtle mb-0 mt-4 text-sm">首次全量建立基线后，Server 才挂接监听并立即执行交接增量对账。每个媒体库独立运行，不占用下载、传输或持久任务队列。</p></section>
        <section v-else-if="activeTab === 'runs'" id="library-panel-runs" class="panel mt-4 overflow-x-auto" role="tabpanel" aria-labelledby="library-tab-runs"><table class="semantic-table min-w-220 w-full text-left text-sm"><thead><tr><th>开始时间</th><th>类型</th><th>状态</th><th>发现</th><th>识别 / 未识别 / 缓存</th><th>新增 / 更新 / 删除</th><th>Generation</th><th>错误</th></tr></thead><tbody><tr v-for="runItem in runs" :key="runItem.id"><td>{{ dateTime(runItem.started_at) }}</td><td>{{ scanKind(runItem.kind) }}</td><td><span :class="scanStatus(runItem).className">{{ scanStatus(runItem).label }}</span></td><td>{{ runItem.discovered }}</td><td>{{ runItem.matched }} / {{ runItem.unrecognized }} / {{ runItem.cache_hits }}<span v-if="runItem.recognition_failed" class="semantic-danger-text"> · 失败 {{ runItem.recognition_failed }}</span></td><td>{{ runItem.added }} / {{ runItem.updated }} / {{ runItem.removed }}</td><td>{{ runItem.generation }}</td><td class="semantic-danger-text">{{ runItem.error_code || '—' }}</td></tr><tr v-if="runs.length === 0"><td colspan="8" class="text-subtle py-8 text-center">尚无扫描记录</td></tr></tbody></table></section>
        <section v-else-if="activeTab === 'entries'" id="library-panel-entries" class="panel mt-4" role="tabpanel" aria-labelledby="library-tab-entries">
          <div class="management-tabs mb-4" role="tablist" aria-label="媒体识别状态">
            <button v-for="filter in ([['','全部'],['matched','已识别'],['unrecognized','未识别'],['manual','人工匹配']] as const)" :key="filter[0]" type="button" class="management-tab" :class="catalogMatch === filter[0] ? 'management-tab--active' : ''" @click="catalogMatch = filter[0]; applyCatalogFilters()">{{ filter[1] }}</button>
          </div>
          <form class="flex flex-wrap items-end gap-3" @submit.prevent="applyCatalogFilters">
            <div class="min-w-52 flex-1"><label class="label" for="media-catalog-search">搜索标题</label><input id="media-catalog-search" v-model="catalogSearch" class="input" maxlength="200" placeholder="输入电影或剧集标题" /></div>
            <div><label class="label" for="media-catalog-type">类型</label><select id="media-catalog-type" v-model="catalogType" class="input" @change="applyCatalogFilters"><option value="">全部</option><option value="movie">电影</option><option value="series">剧集</option></select></div>
            <div><label class="label" for="media-catalog-page-size">每页</label><select id="media-catalog-page-size" v-model.number="catalogPageSize" class="input" @change="applyCatalogFilters"><option v-for="size in mediaCatalogPageSizes" :key="size" :value="size">{{ size }}</option></select></div>
            <button type="submit" class="btn-secondary" :disabled="catalogLoading">搜索</button>
          </form>
          <p v-if="catalogError" role="alert" class="semantic-error mt-4 p-3 text-sm">{{ catalogError }}</p>
          <div v-if="catalogMatch !== 'unrecognized' && catalogMatch !== 'manual'" class="mt-4 overflow-x-auto">
            <table class="semantic-table min-w-180 w-full text-left text-sm">
              <thead><tr><th class="w-12"></th><th>作品</th><th>类型</th><th>季 / 集 / 文件</th><th>分类 / 匹配</th><th>大小</th><th>修改时间</th></tr></thead>
              <tbody>
                <template v-for="work in catalog" :key="work.id">
                  <tr>
                    <td><button type="button" class="btn-quiet px-2" :aria-label="expandedWorkIDs.includes(work.id) ? `收起${work.title}` : `展开${work.title}`" @click="toggleCatalog(work)">{{ expandedWorkIDs.includes(work.id) ? '−' : '+' }}</button></td>
                    <td><strong>{{ work.title }}</strong></td><td>{{ work.kind === 'movie' ? '电影' : '剧集' }}</td>
                    <td>{{ work.kind === 'series' ? `${work.season_count} 季 / ${work.episode_count} 集 / ${work.file_count} 文件` : `${work.file_count} 文件` }}</td>
                    <td>{{ work.category_name || '未分类' }} · {{ work.match_status }}</td><td>{{ bytes(work.size) }}</td><td>{{ dateTime(work.modified_at) }}</td>
                  </tr>
                  <tr v-if="expandedWorkIDs.includes(work.id)"><td colspan="7" class="p-0">
                    <div class="semantic-inset m-2 p-3">
                      <p v-if="detailLoadingIDs.includes(work.id)" class="text-subtle m-0 text-sm">正在读取季度与分集…</p>
                      <div v-if="catalogDetails[work.id]?.reorganizable_transfers.length" class="mb-4 flex flex-wrap items-center gap-2"><span class="text-subtle text-xs">OhMyCine 托管入库记录：</span><button v-for="transfer in catalogDetails[work.id].reorganizable_transfers" :key="transfer.transfer_task_id" type="button" class="btn-secondary" @click="openCatalogReorganization(work, transfer)">修正并整理 {{ transfer.file_count }} 个文件</button></div>
                      <div v-for="season in catalogDetails[work.id]?.seasons ?? []" :key="season.number" class="mb-3 last:mb-0">
                        <strong class="text-sm">{{ seasonLabel(season.number) }}</strong>
                        <div class="mt-2 grid gap-1">
                          <div v-for="episode in season.episodes" :key="episode.id" class="grid gap-2 rounded-1 px-2 py-1.5 text-xs md:grid-cols-[7rem_minmax(0,1fr)_auto]">
                            <span>{{ episodeLabel(episode) || '未识别集号' }}</span><span class="truncate font-mono" :title="episode.relative_path">{{ episode.relative_path }}</span><span>{{ bytes(episode.size) }}</span>
                          </div>
                        </div>
                      </div>
                      <div v-if="catalogDetails[work.id]?.files.length" class="grid gap-1"><div v-for="file in catalogDetails[work.id].files" :key="file.id" class="grid gap-2 rounded-1 px-2 py-1.5 text-xs md:grid-cols-[minmax(0,1fr)_auto]"><span class="truncate font-mono" :title="file.relative_path">{{ file.relative_path }}</span><span>{{ bytes(file.size) }}</span></div></div>
                    </div>
                  </td></tr>
                </template>
                <tr v-if="catalogLoading && catalog.length === 0"><td colspan="7" class="text-subtle py-8 text-center">正在加载作品目录…</td></tr>
                <tr v-else-if="catalog.length === 0"><td colspan="7" class="text-subtle py-8 text-center">当前筛选下没有作品</td></tr>
              </tbody>
            </table>
          </div>
          <div v-else class="mt-4 overflow-x-auto">
            <table class="semantic-table min-w-190 w-full text-left text-sm">
              <thead><tr><th>识别标题 / 来源摘要</th><th>类型</th><th>原因</th><th>文件</th><th>更新时间</th><th>操作</th></tr></thead>
              <tbody>
                <template v-for="item in recognitions" :key="item.token">
                  <tr><td><strong>{{ item.title || '标题未解析' }}</strong><small class="text-subtle mt-1 block font-mono">{{ item.source_summary }}</small></td><td>{{ item.media_type === 'tv' ? '剧集' : item.media_type === 'movie' ? '电影' : '未知' }}</td><td :class="item.status === 'unrecognized' ? 'semantic-warning-text' : ''">{{ item.status === 'matched' ? `TMDB ${item.tmdb_id}` : item.error_code || 'tmdb_no_match' }}</td><td>{{ item.file_count }}</td><td>{{ dateTime(item.updated_at) }}</td><td><div class="flex flex-wrap gap-2"><button v-if="item.status === 'unrecognized' && auth.can(Permissions.MediaLibrariesScan)" type="button" class="btn-secondary" :disabled="saving" @click="retryRecognition(item)">重试</button><button v-if="item.status === 'unrecognized'" type="button" class="btn-secondary" :disabled="saving" @click="findCandidates(item)">匹配 TMDB</button><button v-if="item.manual_override && auth.can(Permissions.MediaLibrariesScan)" type="button" class="btn-danger" :disabled="saving" @click="clearRecognitionOverride(item)">清除人工匹配</button></div></td></tr>
                  <tr v-if="candidateToken === item.token"><td colspan="6"><div class="semantic-inset p-3"><p v-if="candidates.length === 0" class="text-subtle m-0">没有可用候选，尝试修改规则后重新扫描。</p><div v-else class="grid gap-2"><button v-for="candidate in candidates" :key="`${candidate.media_type}-${candidate.id}`" type="button" class="semantic-list-item flex items-center justify-between gap-3 p-3 text-left" :disabled="saving || !auth.can(Permissions.MediaLibrariesScan)" @click="chooseCandidate(item, candidate)"><span><strong>{{ candidate.title }}</strong><small class="text-subtle ml-2">{{ candidate.release_year || '年份未知' }} · TMDB {{ candidate.id }}</small></span><span>{{ Math.round(candidate.confidence * 100) }}%</span></button></div></div></td></tr>
                </template>
                <tr v-if="recognitionLoading && recognitions.length === 0"><td colspan="6" class="text-subtle py-8 text-center">正在加载识别项目…</td></tr><tr v-else-if="recognitions.length === 0"><td colspan="6" class="text-subtle py-8 text-center">当前筛选下没有项目</td></tr>
              </tbody>
            </table>
          </div>
          <footer class="semantic-divider mt-4 flex flex-wrap items-center justify-between gap-3 border-t pt-4 text-sm">
            <span class="text-subtle">显示 {{ catalogRange.start }}–{{ catalogRange.end }}，共 {{ catalogTotal }} 个作品</span>
            <div class="flex items-center gap-2"><button type="button" class="btn-secondary" :disabled="catalogLoading || catalogPage <= 1" @click="changeCatalogPage(catalogPage - 1)">上一页</button><span>第 {{ catalogPage }} / {{ catalogPages }} 页</span><button type="button" class="btn-secondary" :disabled="catalogLoading || catalogPage >= catalogPages" @click="changeCatalogPage(catalogPage + 1)">下一页</button></div>
          </footer>
        </section>
        <form v-else-if="editDraft" id="library-panel-settings" class="panel mt-4" role="tabpanel" aria-labelledby="library-tab-settings" @submit.prevent="saveLibrary"><div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3"><div><label class="label">名称</label><input v-model="editDraft.name" class="input" required maxlength="128" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" /></div><div><label class="label">来源 Storage</label><select v-model.number="editDraft.storage_id" class="input" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)"><option v-for="storage in storages" :key="storage.id" :value="storage.id">{{ storage.name }}</option></select></div><div><label class="label">分类 Profile</label><select v-model.number="editDraft.profile_id" class="input" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)"><option v-for="profile in profiles" :key="profile.id" :value="profile.id">{{ profile.name }} · r{{ profile.revision }}</option></select></div><div class="md:col-span-2 xl:col-span-3"><label class="label">来源目录</label><div class="flex gap-2"><input class="input font-mono" :value="editDraft.source_path" readonly /><button v-if="editStorage && auth.can(Permissions.MediaLibrariesUpdate) && auth.can(Permissions.StoragesBrowse)" type="button" class="btn-secondary" @click="openPicker('edit', 'source')">重新选择</button></div><p v-if="editDraft.source_path" class="text-subtle mb-0 mt-2 text-xs">实际可读位置如上；数据库保存 Storage 相对根 {{ editDraft.relative_root || '/' }}，其中 / 表示该 Storage 根目录。</p><p v-else class="semantic-warning-text mb-0 mt-2 text-xs">更换 Storage 后必须通过目录选择器重新选择其范围内的来源根。</p></div><label class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.enabled" type="checkbox" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" />启用媒体库</label><label class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.recursive" type="checkbox" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" />递归扫描</label><label class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.metadata_artifacts_enabled" type="checkbox" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" />生成 NFO / 图片元数据</label><template v-if="supportsSTRM(editStorage)"><label class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.strm_enabled" type="checkbox" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" @change="normalizeSTRM(editDraft!, editStorage)" />启用 signed 302 / STRM</label><div v-if="editDraft.strm_enabled" class="md:col-span-2 xl:col-span-3"><label class="label">本地 STRM 输出目录</label><div class="flex gap-2"><input class="input" :value="editDraft.strm_local_path" readonly required /><button type="button" class="btn-secondary" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" @click="openPicker('edit', 'strm')">重新选择</button></div></div></template><label v-if="supportsSidecarUpload(editStorage) && !editDraft.strm_enabled" class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.upload_sidecars" type="checkbox" :disabled="!auth.can(Permissions.MediaLibrariesUpdate) || !editDraft.metadata_artifacts_enabled" />将 NFO / JPG 上传到云端媒体旁</label></div><MediaLibrarySettingsFields v-model="editDraft" class="mt-5" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" :storage-type="editStorage?.type" /><div class="mt-5 flex flex-wrap gap-2"><button v-if="auth.can(Permissions.MediaLibrariesUpdate)" class="btn-primary" :disabled="saving || !editDraft.source_path || (editDraft.strm_enabled && !editDraft.strm_local_path)">保存配置</button><RouterLink class="btn-secondary" to="/system/media-rules">管理分类规则</RouterLink></div></form>
      </main>
    </div>

    <DirectoryPickerDialog :open="pickerOpen" :storage-id="pickerTarget === 'source' ? activeDraft?.storage_id : null" :restrict-to-storage="pickerTarget === 'source'" @close="pickerOpen = false" @select="directorySelected" />
    <MediaReorganizationDialog v-if="reorganizationTarget" :open="true" :transfer-task-id="reorganizationTarget.transfer.transfer_task_id" :download-task-id="reorganizationTarget.transfer.download_task_id" :display-name="reorganizationTarget.work.title" :current-title="reorganizationTarget.work.title" :current-media-type="reorganizationTarget.work.kind === 'movie' ? 'movie' : 'tv'" @close="reorganizationTarget = null" @queued="catalogReorganizationQueued" />
  </section>
</template>
