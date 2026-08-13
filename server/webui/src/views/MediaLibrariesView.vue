<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import DirectoryPickerDialog from '@/components/DirectoryPickerDialog.vue'
import MediaLibrarySettingsFields from '@/components/MediaLibrarySettingsFields.vue'
import { draftFromLibrary, emptyMediaLibraryDraft, isActiveLibraryStatus, payloadFromDraft, presentLibraryStatus, supportsSTRM, type MediaLibraryDraft } from '@/media-libraries'
import { useAuthStore } from '@/stores/auth'
import type { ListResponse, MediaClassificationProfileSummary, MediaLibraryDetail, MediaLibraryEntry, MediaLibraryScanRun, StorageSummary } from '@/types/api'

type DetailTab = 'status' | 'runs' | 'entries' | 'settings'
type PickerTarget = 'source' | 'strm'

const auth = useAuthStore()
const libraries = ref<MediaLibraryDetail[]>([])
const storages = ref<StorageSummary[]>([])
const profiles = ref<MediaClassificationProfileSummary[]>([])
const selectedID = ref<number | null>(null)
const runs = ref<MediaLibraryScanRun[]>([])
const entries = ref<MediaLibraryEntry[]>([])
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
let pollTimer: number | undefined

const selected = computed(() => libraries.value.find(item => item.id === selectedID.value) ?? null)
const activeDraft = computed(() => pickerMode.value === 'create' ? createDraft.value : editDraft.value)
const createStorage = computed(() => storages.value.find(item => item.id === createDraft.value.storage_id))
const editStorage = computed(() => storages.value.find(item => item.id === editDraft.value?.storage_id))
const shouldPoll = computed(() => activeTab.value !== 'settings' && !editDirty.value && (libraries.value.some(item => isActiveLibraryStatus(item.status) || (item.enabled && item.status === 'initialization_failed')) || runs.value.some(run => run.status === 'running')))

function message(reason: unknown) { return reason instanceof Error ? reason.message : '请求失败' }
function dateTime(value: string | null) { return value ? new Date(value).toLocaleString() : '尚无记录' }
function bytes(value: number) { const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; let amount = value; let index = 0; while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ } return `${amount.toFixed(index === 0 ? 0 : 1)} ${units[index]}` }
function episodeLabel(entry: MediaLibraryEntry) { return entry.season === null || entry.episode === null ? '' : `S${String(entry.season).padStart(2, '0')}E${String(entry.episode).padStart(2, '0')}` }
function scanKind(kind: string) { return ({ initial: '首次全量', catch_up: '监听交接对账', event: '文件事件', incremental: '定时增量', full: '周期全量', manual: '手动跟进' } as Record<string, string>)[kind] ?? kind }
function scanStatus(run: MediaLibraryScanRun) { if (run.status === 'failed') return { label: '失败', className: 'status-chip status-chip--error' }; if (run.status === 'running') return { label: '运行中', className: 'status-chip status-chip--warning' }; return { label: run.partial ? '部分完成' : '成功', className: run.partial ? 'status-chip status-chip--warning' : 'status-chip status-chip--ready' } }

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
  const [runData, entryData] = await Promise.all([
    api<ListResponse<MediaLibraryScanRun>>(`/api/v1/media-libraries/${id}/runs?limit=30`),
    api<ListResponse<MediaLibraryEntry>>(`/api/v1/media-libraries/${id}/entries?limit=500`),
  ])
  if (selectedID.value !== id) return
  runs.value = runData.list
  entries.value = entryData.list
}

function schedulePoll() {
  window.clearTimeout(pollTimer)
  if (shouldPoll.value) pollTimer = window.setTimeout(() => void load({ quiet: true }), 2000)
}

function beginCreate() {
  createDraft.value = emptyMediaLibraryDraft(storages.value[0]?.id, profiles.value[0]?.id)
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

function resetSourceSelection(draft: MediaLibraryDraft) { draft.source_path = ''; draft.relative_root_token = ''; draft.relative_root = '/' }
function normalizeSTRM(draft: MediaLibraryDraft, storage: StorageSummary | undefined) { if (!supportsSTRM(storage)) { draft.strm_enabled = false; draft.strm_local_path = ''; draft.strm_local_root_token = '' } }

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

async function run(action: () => Promise<void>) { saving.value = true; error.value = ''; notice.value = ''; try { await action() } catch (reason) { error.value = message(reason) } finally { saving.value = false } }

watch(selectedID, async id => {
  runs.value = []; entries.value = []; activeTab.value = 'status'
  editDirty.value = false
  if (id) { const library = libraries.value.find(item => item.id === id); editDraft.value = library ? draftFromLibrary(library) : null; try { await loadActivity(id) } catch (reason) { error.value = message(reason) } }
  else editDraft.value = null
})
watch(selected, library => { if (library && activeTab.value !== 'settings' && !editDirty.value) editDraft.value = draftFromLibrary(library) })
watch(() => createDraft.value.storage_id, () => { resetSourceSelection(createDraft.value); normalizeSTRM(createDraft.value, createStorage.value) })
watch(() => editDraft.value?.storage_id, () => { if (editDraft.value && editDraft.value.storage_id !== selected.value?.storage_id) resetSourceSelection(editDraft.value); if (editDraft.value) normalizeSTRM(editDraft.value, editStorage.value) })
onMounted(() => void load())
onBeforeUnmount(() => window.clearTimeout(pollTimer))
</script>

<template>
  <section class="mx-auto max-w-7xl">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div><h1 class="m-0 text-3xl font-800">媒体库</h1><p class="page-description mt-2 max-w-3xl">从已注册 Storage 选择受控相对根，使用共享分类 Profile 建立只读索引，并独立运行初始化、监听和最终一致性扫描。</p></div>
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
        <div class="md:col-span-2 xl:col-span-3"><label class="label">来源相对根</label><div class="flex gap-2"><input class="input min-w-0 font-mono" :value="createDraft.source_path" readonly required placeholder="请从来源 Storage 内选择目录；Storage 根会保存为 /" /><button v-if="auth.can(Permissions.StoragesBrowse)" type="button" class="btn-secondary shrink-0" @click="openPicker('create', 'source')">选择目录</button></div><p class="text-subtle mb-0 mt-2 text-xs">提交签名短期令牌，由 Server 校验目录仍位于所选 Storage 内；浏览器不拼接绝对路径。</p></div>
        <label class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.enabled" type="checkbox" />创建后启用并自动初始化</label><label class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.recursive" type="checkbox" />递归扫描子目录</label>
        <template v-if="supportsSTRM(createStorage)"><label class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.strm_enabled" type="checkbox" />启用 signed 302 / STRM</label><div v-if="createDraft.strm_enabled" class="md:col-span-2 xl:col-span-3"><label class="label">本地 STRM 输出目录</label><div class="flex gap-2"><input class="input" :value="createDraft.strm_local_path" readonly required /><button type="button" class="btn-secondary" @click="openPicker('create', 'strm')">选择目录</button></div></div></template>
      </div>
      <details class="semantic-inset mt-5 p-4"><summary class="cursor-pointer font-650">扫描、限速与匹配配置</summary><MediaLibrarySettingsFields v-model="createDraft" class="mt-4" /></details>
      <button class="btn-primary mt-5" :disabled="saving || !createDraft.relative_root_token">创建媒体库</button>
    </form>

    <div v-if="loading" class="panel mt-7">正在加载媒体库…</div>
    <div v-else-if="libraries.length === 0" class="panel mt-7"><h2 class="m-0 text-lg">尚未创建媒体库</h2><p class="page-description mb-0 mt-2 text-sm">先注册可读 Storage 并准备分类 Profile，再选择来源相对根。媒体库扫描只读，不整理或改写媒体文件。</p></div>
    <div v-else class="mt-7 grid gap-5 xl:grid-cols-[minmax(18rem,.68fr)_minmax(0,1.7fr)]">
      <aside class="panel p-2"><button v-for="library in libraries" :key="library.id" type="button" class="semantic-list-item mb-1 w-full p-3 text-left" :class="{ 'semantic-list-item--selected': selectedID === library.id }" @click="selectedID = library.id"><div class="flex items-center justify-between gap-3"><strong class="truncate">{{ library.name }}</strong><span :class="presentLibraryStatus(library.status).className">{{ presentLibraryStatus(library.status).label }}</span></div><div class="text-subtle mt-2 text-xs">{{ library.storage_name }} · {{ library.relative_root }} · {{ library.entry_count }} 条目</div><div v-if="library.reclassification_due" class="semantic-warning-text mt-2 text-xs">分类规则已更新，待重分类</div></button></aside>
      <main v-if="selected" class="min-w-0">
        <section class="panel">
          <div class="flex flex-wrap items-start justify-between gap-4"><div><div class="flex flex-wrap items-center gap-2"><h2 class="m-0">{{ selected.name }}</h2><span :class="presentLibraryStatus(selected.status).className">{{ presentLibraryStatus(selected.status).label }}</span></div><p class="text-subtle mb-0 mt-2 text-sm">{{ selected.storage_name }} · {{ selected.relative_root }} · Profile {{ selected.profile_name }} r{{ selected.profile_revision }}</p></div><div class="flex flex-wrap gap-2"><button v-if="selected.status === 'initialization_failed' && auth.can(Permissions.MediaLibrariesScan)" type="button" class="btn-primary" :disabled="saving" @click="retryNow">立即重试</button><button v-if="selected.enabled && selected.status !== 'initializing' && auth.can(Permissions.MediaLibrariesScan)" type="button" class="btn-secondary" :disabled="saving" @click="scanNow">立即扫描</button><button v-if="auth.can(Permissions.MediaLibrariesDelete)" type="button" class="btn-danger" :disabled="saving" @click="removeLibrary">删除配置</button></div></div>
          <p v-if="selected.status === 'initialization_failed'" class="semantic-error mt-4 p-3 text-sm">初始化失败：{{ selected.status_error_code || 'media_library_scan_failed' }}。失败库不会启动监听；下次自动重试：{{ dateTime(selected.next_retry_at) }}。</p>
          <p v-if="selected.reclassification_due" class="semantic-warning mt-4 p-3 text-sm">所选 Profile 已更新。下一次扫描会重新应用分类，但不会移动、重命名或写回来源文件。<RouterLink class="semantic-link ml-1" to="/system/media-rules">打开规则管理</RouterLink></p>
        </section>

        <div class="management-tabs mt-4" role="tablist" aria-label="媒体库详情"><button v-for="tab in ([['status','状态'],['runs','扫描记录'],['entries','媒体清单'],['settings','配置']] as const)" :id="`library-tab-${tab[0]}`" :key="tab[0]" type="button" class="management-tab" :class="activeTab === tab[0] ? 'management-tab--active' : ''" role="tab" :aria-selected="activeTab === tab[0]" :aria-controls="`library-panel-${tab[0]}`" @click="activeTab = tab[0]">{{ tab[1] }}</button></div>

        <section v-if="activeTab === 'status'" id="library-panel-status" class="panel mt-4" role="tabpanel" aria-labelledby="library-tab-status"><div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"><div class="semantic-inset p-3"><span class="text-subtle text-xs">媒体条目</span><strong class="mt-1 block">{{ selected.entry_count }}</strong></div><div class="semantic-inset p-3"><span class="text-subtle text-xs">基线 / dirty generation</span><strong class="mt-1 block">{{ selected.baseline_generation }} / {{ selected.dirty_generation }}</strong></div><div class="semantic-inset p-3"><span class="text-subtle text-xs">最近成功扫描</span><strong class="mt-1 block text-sm">{{ dateTime(selected.last_successful_scan_at) }}</strong></div><div class="semantic-inset p-3"><span class="text-subtle text-xs">监听方式</span><strong class="mt-1 block">{{ selected.enabled ? 'Storage driver 自动选择' : '未运行' }}</strong></div></div><p class="text-subtle mb-0 mt-4 text-sm">首次全量建立基线后，Server 才挂接监听并立即执行交接增量对账。每个媒体库独立运行，不占用下载、传输或持久任务队列。</p></section>
        <section v-else-if="activeTab === 'runs'" id="library-panel-runs" class="panel mt-4 overflow-x-auto" role="tabpanel" aria-labelledby="library-tab-runs"><table class="semantic-table min-w-190 w-full text-left text-sm"><thead><tr><th>开始时间</th><th>类型</th><th>状态</th><th>发现</th><th>新增 / 更新 / 删除</th><th>Generation</th><th>错误</th></tr></thead><tbody><tr v-for="runItem in runs" :key="runItem.id"><td>{{ dateTime(runItem.started_at) }}</td><td>{{ scanKind(runItem.kind) }}</td><td><span :class="scanStatus(runItem).className">{{ scanStatus(runItem).label }}</span></td><td>{{ runItem.discovered }}</td><td>{{ runItem.added }} / {{ runItem.updated }} / {{ runItem.removed }}</td><td>{{ runItem.generation }}</td><td class="semantic-danger-text">{{ runItem.error_code || '—' }}</td></tr><tr v-if="runs.length === 0"><td colspan="7" class="text-subtle py-8 text-center">尚无扫描记录</td></tr></tbody></table></section>
        <section v-else-if="activeTab === 'entries'" id="library-panel-entries" class="panel mt-4 overflow-x-auto" role="tabpanel" aria-labelledby="library-tab-entries"><p class="text-subtle mt-0 text-sm">仅显示 provider-relative 路径；绝对物理路径不会进入媒体清单、导出或 AI 字段。</p><table class="semantic-table min-w-220 w-full text-left text-sm"><thead><tr><th>标题</th><th>类型</th><th>相对路径</th><th>分类 / 匹配</th><th>大小</th><th>修改时间</th></tr></thead><tbody><tr v-for="entry in entries" :key="entry.id"><td><strong>{{ entry.title }}</strong><small v-if="episodeLabel(entry)" class="text-subtle ml-2">{{ episodeLabel(entry) }}</small></td><td>{{ entry.media_type === 'movie' ? '电影' : entry.media_type === 'tv' ? '剧集' : '未识别' }}</td><td class="max-w-100 truncate font-mono text-xs" :title="entry.relative_path">{{ entry.relative_path }}</td><td>{{ entry.category_name || '未分类' }} · {{ entry.match_status }}</td><td>{{ bytes(entry.size) }}</td><td>{{ dateTime(entry.modified_at) }}</td></tr><tr v-if="entries.length === 0"><td colspan="6" class="text-subtle py-8 text-center">当前没有媒体条目</td></tr></tbody></table></section>
        <form v-else-if="editDraft" id="library-panel-settings" class="panel mt-4" role="tabpanel" aria-labelledby="library-tab-settings" @submit.prevent="saveLibrary"><div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3"><div><label class="label">名称</label><input v-model="editDraft.name" class="input" required maxlength="128" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" /></div><div><label class="label">来源 Storage</label><select v-model.number="editDraft.storage_id" class="input" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)"><option v-for="storage in storages" :key="storage.id" :value="storage.id">{{ storage.name }}</option></select></div><div><label class="label">分类 Profile</label><select v-model.number="editDraft.profile_id" class="input" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)"><option v-for="profile in profiles" :key="profile.id" :value="profile.id">{{ profile.name }} · r{{ profile.revision }}</option></select></div><div class="md:col-span-2 xl:col-span-3"><label class="label">来源相对根</label><div class="flex gap-2"><input class="input font-mono" :value="editDraft.source_path" readonly /><button v-if="auth.can(Permissions.MediaLibrariesUpdate) && auth.can(Permissions.StoragesBrowse)" type="button" class="btn-secondary" @click="openPicker('edit', 'source')">重新选择</button></div><p v-if="!editDraft.source_path" class="semantic-warning-text mb-0 mt-2 text-xs">更换 Storage 后必须通过目录选择器重新选择其范围内的来源根。</p></div><label class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.enabled" type="checkbox" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" />启用媒体库</label><label class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.recursive" type="checkbox" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" />递归扫描</label><template v-if="supportsSTRM(editStorage)"><label class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.strm_enabled" type="checkbox" />启用 signed 302 / STRM</label></template></div><MediaLibrarySettingsFields v-model="editDraft" class="mt-5" :disabled="!auth.can(Permissions.MediaLibrariesUpdate)" /><div class="mt-5 flex flex-wrap gap-2"><button v-if="auth.can(Permissions.MediaLibrariesUpdate)" class="btn-primary" :disabled="saving || !editDraft.source_path">保存配置</button><RouterLink class="btn-secondary" to="/system/media-rules">管理分类规则</RouterLink></div></form>
      </main>
    </div>

    <DirectoryPickerDialog :open="pickerOpen" :storage-id="pickerTarget === 'source' ? activeDraft?.storage_id : null" :restrict-to-storage="pickerTarget === 'source'" @close="pickerOpen = false" @select="directorySelected" />
  </section>
</template>
