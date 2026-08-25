<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import DirectoryPickerDialog from '@/components/DirectoryPickerDialog.vue'
import SecretInput from '@/components/SecretInput.vue'
import { canBrowseProviderDirectory, connectionListPath } from '@/connections'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import type { ConnectionSummary, ListResponse, StorageSummary } from '@/types/api'

type SourceKind = 'local' | 'pan115'
const auth = useAuthStore()
const connections = ref<ConnectionSummary[]>([])
const storages = ref<StorageSummary[]>([])
const selectedKey = ref('')
const loading = ref(true), saving = ref(false), addOpen = ref(false), pickerOpen = ref(false)
const addKind = ref<SourceKind | null>(null)
const pickerTarget = ref<'create' | 'edit' | 'cloud-create' | 'cloud-edit'>('create')
const localCreate = ref({ name: '', selectedPath: '', pickerToken: '', enabled: true })
const cloudCreate = ref({ name: '', cookie: '', recyclePassword: '', connectionID: 0, selectedPath: '', pickerToken: '', enabled: true })
const localEdit = ref({ name: '', selectedPath: '', pickerToken: '', enabled: true })
const cloudEdit = ref({ name: '', cookie: '', recyclePassword: '', clearRecyclePassword: false, enabled: true })
const sources = computed(() => [
  ...storages.value.map(item => ({ key: `storage:${item.id}`, kind: item.type === 'pan115' ? 'pan115' as const : 'local' as const, name: item.name, subtitle: item.root_display_path || item.root_path })),
  ...connections.value
    .filter(connection => connection.provider === 'pan115' && !storages.value.some(storage => storage.connection_id === connection.id))
    .map(item => ({
      key: `connection:${item.id}`,
      kind: 'pan115' as const,
      name: item.name,
      subtitle: '账号已保存 · 尚未选择云端目录',
    })),
].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN')))
const selectedStorage = computed(() => selectedKey.value.startsWith('storage:') ? storages.value.find(item => item.id === Number(selectedKey.value.slice(8))) ?? null : null)
const selectedConnection = computed(() => selectedKey.value.startsWith('connection:') ? connections.value.find(item => item.id === Number(selectedKey.value.slice(11))) ?? null : null)
const pan115Connections = computed(() => connections.value.filter(item => item.provider === 'pan115'))
const storageConnection = computed(() => connections.value.find(item => item.id === selectedStorage.value?.connection_id) ?? null)
const selectedPan115 = computed(() => selectedConnection.value?.provider === 'pan115' ? selectedConnection.value : null)

watch(selectedStorage, item => { if (item) localEdit.value = { name: item.name, selectedPath: item.root_display_path || item.root_path, pickerToken: '', enabled: item.enabled } }, { immediate: true })
watch(selectedConnection, item => {
  cloudEdit.value.cookie = ''
  if (item?.provider === 'pan115') cloudEdit.value = { name: item.name, cookie: '', recyclePassword: '', clearRecyclePassword: false, enabled: item.enabled }
}, { immediate: true })
watch(storageConnection, item => { if (item && selectedStorage.value?.type === 'pan115') cloudEdit.value = { name: item.name, cookie: '', recyclePassword: '', clearRecyclePassword: false, enabled: item.enabled } }, { immediate: true })

async function load() {
  loading.value = true
  try {
    const [local, cloud] = await Promise.all([
      auth.can(Permissions.StoragesRead) ? api<ListResponse<StorageSummary>>('/api/v1/storages') : Promise.resolve({ list: [], total: 0 }),
      auth.can(Permissions.ConnectionsRead) ? api<ListResponse<ConnectionSummary>>(connectionListPath('pan115')) : Promise.resolve({ list: [], total: 0 }),
    ])
    storages.value = local.list; connections.value = cloud.list
    if (!sources.value.some(item => item.key === selectedKey.value)) selectedKey.value = sources.value[0]?.key ?? ''
  } catch (reason) { notify(message(reason), 'error') } finally { loading.value = false }
}
async function run(action: () => Promise<void>) {
  saving.value = true
  try {
    await action()
    await load()
    return true
  } catch (reason) {
    notify(message(reason), 'error')
    return false
  } finally {
    saving.value = false
  }
}
function clearDraftCredentials() {
  cloudCreate.value.cookie = ''
  cloudCreate.value.recyclePassword = ''
}
function returnToKindPicker() { clearDraftCredentials(); addKind.value = null }
function toggleAdd() {
  addOpen.value = !addOpen.value
  if (!addOpen.value) clearDraftCredentials()
  addKind.value = null
}
function openPicker(target: 'create' | 'edit' | 'cloud-create' | 'cloud-edit') { pickerTarget.value = target; pickerOpen.value = true }
function directorySelected(value: { path: string; token: string }) {
  if (pickerTarget.value === 'cloud-create') { cloudCreate.value.selectedPath = value.path; cloudCreate.value.pickerToken = value.token; return }
  const form = pickerTarget.value === 'create' ? localCreate.value : localEdit.value
  form.selectedPath = value.path; form.pickerToken = value.token
}
async function createLocal() {
  if (!localCreate.value.pickerToken) return notify('请先选择 Server 本地目录', 'warning')
  await run(async () => { const item = await api<StorageSummary>('/api/v1/storages', { method: 'POST', body: JSON.stringify({ name: localCreate.value.name, type: 'local', picker_token: localCreate.value.pickerToken, enabled: localCreate.value.enabled }) }); selectedKey.value = `storage:${item.id}`; localCreate.value = { name: '', selectedPath: '', pickerToken: '', enabled: true }; addOpen.value = false; addKind.value = null; notify('本地数据源已添加', 'success') })
}
async function createPan115() {
  if (!cloudCreate.value.connectionID) {
    const cookie = cloudCreate.value.cookie
    cloudCreate.value.cookie = ''
    const recyclePassword = cloudCreate.value.recyclePassword
    cloudCreate.value.recyclePassword = ''
    const created = await run(async () => { const connection = await api<ConnectionSummary>('/api/v1/connections', { method: 'POST', body: JSON.stringify({ name: cloudCreate.value.name, provider: 'pan115', cookie, recycle_password: recyclePassword || undefined, enabled: true }) }); cloudCreate.value.connectionID = connection.id; notify('账号连接已保存，请选择 115 云端目录', 'success') })
    if (created && canBrowsePan115ForCreate.value) openPicker('cloud-create')
    return
  }
  if (!cloudCreate.value.pickerToken) {
    if (canBrowsePan115ForCreate.value) openPicker('cloud-create')
    else notify('缺少浏览 115 目录所需权限', 'error')
    return
  }
  await run(async () => { const item = await api<StorageSummary>('/api/v1/storages', { method: 'POST', body: JSON.stringify({ name: cloudCreate.value.name, type: 'pan115', connection_id: cloudCreate.value.connectionID, provider_picker_token: cloudCreate.value.pickerToken, enabled: cloudCreate.value.enabled }) }); selectedKey.value = `storage:${item.id}`; cloudCreate.value = { name: '', cookie: '', recyclePassword: '', connectionID: 0, selectedPath: '', pickerToken: '', enabled: true }; addOpen.value = false; addKind.value = null; notify('115 数据源已添加', 'success') })
}
async function saveLocal() {
  if (!selectedStorage.value) return
  const item = selectedStorage.value
  await run(async () => { const payload: Record<string, unknown> = { name: localEdit.value.name, enabled: localEdit.value.enabled }; if (localEdit.value.pickerToken) payload.picker_token = localEdit.value.pickerToken; await api(`/api/v1/storages/${item.id}`, { method: 'PATCH', body: JSON.stringify(payload) }); notify('本地数据源已保存', 'success') })
}
async function saveCloudStorage() {
  if (!selectedStorage.value?.connection_id) return
  const item = selectedStorage.value
  await run(async () => { if ((cloudEdit.value.cookie.trim() || cloudEdit.value.recyclePassword.trim() || cloudEdit.value.clearRecyclePassword) && storageConnection.value && auth.can(Permissions.ConnectionsUpdate)) { const connectionPayload: Record<string, unknown> = { revision: storageConnection.value.revision }; if (cloudEdit.value.cookie.trim()) connectionPayload.cookie = cloudEdit.value.cookie; if (cloudEdit.value.recyclePassword.trim()) connectionPayload.recycle_password = cloudEdit.value.recyclePassword; else if (cloudEdit.value.clearRecyclePassword) connectionPayload.recycle_password = ''; await api(`/api/v1/connections/${storageConnection.value.id}`, { method: 'PATCH', body: JSON.stringify(connectionPayload) }); cloudEdit.value.cookie = ''; cloudEdit.value.recyclePassword = ''; cloudEdit.value.clearRecyclePassword = false }; const payload: Record<string, unknown> = { name: localEdit.value.name, enabled: localEdit.value.enabled, connection_id: item.connection_id }; if (localEdit.value.pickerToken) payload.provider_picker_token = localEdit.value.pickerToken; await api(`/api/v1/storages/${item.id}`, { method: 'PATCH', body: JSON.stringify(payload) }); notify('115 数据源已保存', 'success') })
}
function finishConnection() {
  if (!selectedConnection.value || !canBrowsePan115ForCreate.value) return
  cloudCreate.value = { name: selectedConnection.value.name, cookie: '', recyclePassword: '', connectionID: selectedConnection.value.id, selectedPath: '', pickerToken: '', enabled: selectedConnection.value.enabled }
  addOpen.value = true
  addKind.value = 'pan115'
  openPicker('cloud-create')
}
async function savePan115() {
  if (!selectedPan115.value) return
  const item = selectedPan115.value
  await run(async () => { const payload: Record<string, unknown> = { name: cloudEdit.value.name, enabled: cloudEdit.value.enabled, revision: item.revision }; if (cloudEdit.value.cookie.trim()) payload.cookie = cloudEdit.value.cookie; if (cloudEdit.value.recyclePassword.trim()) payload.recycle_password = cloudEdit.value.recyclePassword; else if (cloudEdit.value.clearRecyclePassword) payload.recycle_password = ''; await api(`/api/v1/connections/${item.id}`, { method: 'PATCH', body: JSON.stringify(payload) }); cloudEdit.value.cookie = ''; cloudEdit.value.recyclePassword = ''; cloudEdit.value.clearRecyclePassword = false; notify('115 数据源已保存', 'success') })
}
async function testSelected() {
  if (selectedStorage.value) await run(async () => { await api(`/api/v1/storages/${selectedStorage.value?.id}/test`, { method: 'POST', body: '{}' }); notify('本地数据源测试成功', 'success') })
  else if (selectedConnection.value) {
    const item = selectedConnection.value
    await run(async () => {
      await api(`/api/v1/connections/${item.id}/test`, { method: 'POST', body: '{}' })
      notify('115 数据源测试成功', 'success')
    })
  }
}
async function deleteSelected() {
  const item = selectedStorage.value ?? selectedConnection.value
  if (!item || !window.confirm(`确认删除数据源“${item.name}”？只删除配置，不会删除真实文件或网盘内容。`)) return
  const path = selectedStorage.value ? `/api/v1/storages/${item.id}` : `/api/v1/connections/${item.id}`
  await run(async () => { await api(path, { method: 'DELETE', body: '{}' }); selectedKey.value = ''; notify('数据源已删除', 'success') })
}
const canAdd = computed(() => auth.can(Permissions.StoragesCreate) || auth.can(Permissions.ConnectionsCreate))
const canEdit = computed(() => selectedStorage.value ? auth.can(Permissions.StoragesUpdate) : auth.can(Permissions.ConnectionsUpdate))
const canTest = computed(() => selectedStorage.value ? auth.can(Permissions.StoragesTest) : auth.can(Permissions.ConnectionsTest))
const canDelete = computed(() => selectedStorage.value ? auth.can(Permissions.StoragesDelete) : auth.can(Permissions.ConnectionsDelete))
const canBrowsePan115ForCreate = computed(() => canBrowseProviderDirectory(
  auth.can(Permissions.ConnectionsRead),
  auth.can(Permissions.StoragesBrowse),
  auth.can(Permissions.StoragesCreate),
))
const canBrowsePan115ForUpdate = computed(() => canBrowseProviderDirectory(
  auth.can(Permissions.ConnectionsRead),
  auth.can(Permissions.StoragesBrowse),
  auth.can(Permissions.StoragesUpdate),
))
function status(key: string) { if (key.startsWith('storage:')) { const item = storages.value.find(v => `storage:${v.id}` === key); return !item?.enabled ? '已停用' : item.probe.error_code ? '异常' : item.probe.readable ? '在线' : '待测试' }; const item = connections.value.find(v => `connection:${v.id}` === key); return !item?.enabled ? '已停用' : item.health.status === 'online' ? '在线' : item.health.status === 'offline' ? '异常' : '待测试' }
function statusClass(key: string) { const value = status(key); return value === '在线' ? 'status-chip status-chip--ready' : value === '异常' ? 'status-chip status-chip--error' : value === '待测试' ? 'status-chip status-chip--warning' : 'status-chip' }
function bytes(value: number | null) { if (value === null) return '未知'; const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']; let amount = value, index = 0; while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }; return `${amount.toFixed(index ? 1 : 0)} ${units[index]}` }
function checkedAt(value?: string | null) { return value ? new Date(value).toLocaleString() : '尚未测试' }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败' }
onMounted(load)
</script>

<template>
  <section class="mx-auto max-w-6xl">
    <div class="flex flex-wrap items-end justify-between gap-4"><div><h1 class="m-0 text-3xl font-800">数据源</h1><p class="page-description mb-0 mt-2 max-w-3xl">统一管理 Server 可以访问的本地目录和网盘账号。创建媒体库时再选择数据源、目录与扫描规则。</p></div><button v-if="canAdd" class="btn-primary" @click="toggleAdd">{{ addOpen ? '取消添加' : '添加数据源' }}</button></div>

    <section v-if="addOpen" class="panel mt-6">
      <template v-if="!addKind"><h2 class="m-0 text-xl">选择数据源类型</h2><p class="text-subtle mt-2 text-sm">以后 OpenList/Alist、CloudDrive2 也从这里添加；媒体服务器请前往“播放器管理”。</p><div class="mt-5 grid gap-4 sm:grid-cols-2"><button v-if="auth.can(Permissions.StoragesCreate)" type="button" class="source-type-card" @click="addKind = 'local'"><span class="source-type-card__icon">L</span><span><strong>本地目录</strong><small>Windows 盘符、NAS 或 Linux 挂载点</small></span></button><button v-if="auth.can(Permissions.ConnectionsCreate)" type="button" class="source-type-card" @click="addKind = 'pan115'"><span class="source-type-card__icon">115</span><span><strong>115 网盘</strong><small>使用 Cookie 连接账号，之后选择网盘目录</small></span></button></div></template>
      <form v-else-if="addKind === 'local'" class="grid gap-4 md:grid-cols-2" @submit.prevent="createLocal"><div class="md:col-span-2 flex items-center gap-3"><button type="button" class="btn-quiet px-2 py-1" @click="returnToKindPicker">← 返回</button><h2 class="m-0 text-xl">添加本地目录</h2></div><div><label class="label">名称</label><input v-model="localCreate.name" class="input" required maxlength="128" placeholder="115 下载盘" /></div><div><label class="label">类型</label><input class="input" value="本地目录" disabled /></div><div class="md:col-span-2"><label class="label">Server 根目录</label><div class="flex gap-2"><input class="input min-w-0 font-mono" :value="localCreate.selectedPath" readonly required placeholder="请选择 Server 可见目录" /><button v-if="auth.can(Permissions.StoragesBrowse)" type="button" class="btn-secondary shrink-0" @click="openPicker('create')">浏览</button></div></div><label class="text-muted flex items-center gap-3 text-sm"><input v-model="localCreate.enabled" type="checkbox" />添加后启用</label><button class="btn-primary md:col-span-2" :disabled="saving || !localCreate.name.trim() || !localCreate.pickerToken">添加数据源</button></form>
      <form v-else-if="addKind === 'pan115'" class="grid gap-4 md:grid-cols-2" @submit.prevent="createPan115">
        <div class="md:col-span-2 flex items-center gap-3"><button type="button" class="btn-quiet px-2 py-1" @click="returnToKindPicker">← 返回</button><h2 class="m-0 text-xl">添加 115 网盘</h2></div>
        <div><label class="label">数据源名称</label><input v-model="cloudCreate.name" class="input" required maxlength="128" placeholder="115 电影" /></div>
        <div><label class="label">115 账号</label><select v-model.number="cloudCreate.connectionID" class="input" :disabled="Boolean(cloudCreate.pickerToken)"><option :value="0">添加新账号</option><option v-for="connection in pan115Connections" :key="connection.id" :value="connection.id">{{ connection.name }}</option></select></div>
        <div v-if="!cloudCreate.connectionID" class="md:col-span-2"><label class="label">115 Cookie</label><SecretInput v-model="cloudCreate.cookie" class="input min-h-28 font-mono text-xs" multiline required autocomplete="off" spellcheck="false" placeholder="UID=...; CID=...; SEID=...; KID=..." /><p class="text-subtle mb-0 mt-2 text-xs">账号凭据使用 AES-GCM 加密；同一账号以后可直接复用并选择其它目录。</p></div>
        <div v-if="!cloudCreate.connectionID" class="md:col-span-2"><label class="label">115 回收站安全码（可选）</label><SecretInput v-model="cloudCreate.recyclePassword" class="input" autocomplete="new-password" maxlength="64" placeholder="用于精确清理 OhMyCine 临时播放副本" /><p class="text-subtle mb-0 mt-2 text-xs">只按 OhMyCine 记录的临时副本 ID 清理，不会默认清空整个回收站。</p></div>
        <div v-else class="md:col-span-2"><label class="label">115 云端根目录</label><div class="flex gap-2"><input class="input min-w-0" :value="cloudCreate.selectedPath" readonly placeholder="尚未选择云端目录" /><button v-if="canBrowsePan115ForCreate" type="button" class="btn-secondary shrink-0" @click="openPicker('cloud-create')">浏览网盘</button></div></div>
        <label class="text-muted flex items-center gap-3 text-sm"><input v-model="cloudCreate.enabled" type="checkbox" />添加后启用</label>
        <button class="btn-primary md:col-span-2" :disabled="saving || !cloudCreate.name.trim() || (!cloudCreate.connectionID && !cloudCreate.cookie.trim())">{{ !cloudCreate.connectionID ? '保存账号并选择目录' : cloudCreate.pickerToken ? '添加数据源' : '选择云端目录' }}</button>
      </form>
    </section>

    <div v-if="loading" class="text-subtle mt-8">正在读取数据源…</div><div v-else-if="!sources.length" class="panel mt-7"><h2 class="m-0 text-lg">尚未添加数据源</h2><p class="page-description mb-0 mt-2 text-sm">点击“添加数据源”，选择本地目录或网盘。</p></div>
    <div v-else class="mt-7 grid gap-5 xl:grid-cols-[minmax(20rem,.75fr)_minmax(30rem,1.25fr)]">
      <div class="panel p-2"><button v-for="source in sources" :key="source.key" class="semantic-list-item mb-1 w-full p-3 text-left" :class="{ 'semantic-list-item--selected': selectedKey === source.key }" @click="selectedKey = source.key"><div class="flex items-center justify-between gap-3"><div class="flex min-w-0 items-center gap-3"><span class="source-kind-badge">{{ source.kind === 'local' ? 'L' : '115' }}</span><strong class="truncate">{{ source.name }}</strong></div><span :class="statusClass(source.key)">{{ status(source.key) }}</span></div><div class="text-subtle mt-2 truncate text-xs" :class="{ 'font-mono': source.kind === 'local' }">{{ source.subtitle }}</div></button></div>
      <form v-if="selectedStorage?.type === 'local'" class="panel" @submit.prevent="saveLocal"><div class="flex items-start justify-between gap-3"><div><h2 class="m-0">{{ selectedStorage.name }}</h2><p class="text-subtle mb-0 mt-1 text-xs">本地目录 · 最后测试 {{ checkedAt(selectedStorage.probe.last_checked_at) }}</p></div><span :class="statusClass(selectedKey)">{{ status(selectedKey) }}</span></div><div class="mt-5 grid gap-4 md:grid-cols-2"><div><label class="label">名称</label><input v-model="localEdit.name" class="input" :disabled="!canEdit" /></div><div><label class="label">类型</label><input class="input" value="本地目录" disabled /></div><div class="md:col-span-2"><label class="label">Server 根目录</label><div class="flex gap-2"><input class="input font-mono" :value="localEdit.selectedPath" readonly /><button v-if="canEdit && auth.can(Permissions.StoragesBrowse)" type="button" class="btn-secondary" @click="openPicker('edit')">更换目录</button></div></div><label class="text-muted flex items-center gap-3 text-sm"><input v-model="localEdit.enabled" type="checkbox" :disabled="!canEdit" />启用数据源</label></div><div class="mt-5 grid gap-3 sm:grid-cols-2"><div class="semantic-inset p-3"><span class="text-subtle text-xs">可用空间</span><strong class="mt-1 block">{{ bytes(selectedStorage.probe.free_bytes) }}</strong></div><div class="semantic-inset p-3"><span class="text-subtle text-xs">总容量</span><strong class="mt-1 block">{{ bytes(selectedStorage.probe.total_bytes) }}</strong></div></div><div class="mt-5 flex gap-3"><button v-if="canEdit" class="btn-secondary" :disabled="saving">保存</button><button v-if="canTest" type="button" class="btn-primary" :disabled="saving || !selectedStorage.enabled" @click="testSelected">测试</button><button v-if="canDelete" type="button" class="btn-danger" :disabled="saving" @click="deleteSelected">删除</button></div></form>
      <form v-else-if="selectedStorage" class="panel" @submit.prevent="saveCloudStorage"><div class="flex items-start justify-between gap-3"><div><h2 class="m-0">{{ selectedStorage.name }}</h2><p class="text-subtle mb-0 mt-1 text-xs">115 网盘 · {{ storageConnection?.name || '账号不可用' }}</p></div><span :class="statusClass(selectedKey)">{{ status(selectedKey) }}</span></div><div class="mt-5 grid gap-4 md:grid-cols-2"><div><label class="label">名称</label><input v-model="localEdit.name" class="input" :disabled="!canEdit" /></div><div><label class="label">账号连接</label><input class="input" :value="storageConnection?.name || '未知'" disabled /></div><div class="md:col-span-2"><label class="label">115 云端根目录</label><div class="flex gap-2"><input class="input" :value="localEdit.selectedPath" readonly /><button v-if="canBrowsePan115ForUpdate && selectedStorage.connection_id" type="button" class="btn-secondary" @click="openPicker('cloud-edit')">更换目录</button></div></div><div class="md:col-span-2"><label class="label">更换账号 Cookie（可选）</label><SecretInput v-model="cloudEdit.cookie" class="input min-h-20 font-mono text-xs" multiline :configured="Boolean(storageConnection?.credential_configured)" :disabled="!canEdit" placeholder="留空表示继续使用已保存的 Cookie" /><p class="text-subtle mb-0 mt-2 text-xs">该账号下的其它数据源也会使用更新后的 Cookie。</p></div><div class="md:col-span-2"><label class="label">115 回收站安全码（可选）</label><SecretInput v-model="cloudEdit.recyclePassword" class="input" :configured="Boolean(storageConnection?.recycle_password_configured)" maxlength="64" :disabled="!canEdit || cloudEdit.clearRecyclePassword" /><label v-if="storageConnection?.recycle_password_configured" class="text-muted mt-2 flex items-center gap-2 text-xs"><input v-model="cloudEdit.clearRecyclePassword" type="checkbox" :disabled="!canEdit" />移除已保存的安全码</label><p class="text-subtle mb-0 mt-2 text-xs">只用于精确永久删除 OhMyCine 临时播放副本，不会清空其它回收站内容。</p></div><label class="text-muted flex items-center gap-3 text-sm"><input v-model="localEdit.enabled" type="checkbox" :disabled="!canEdit" />启用数据源</label></div><div class="mt-5 flex gap-3"><button v-if="canEdit" class="btn-secondary" :disabled="saving">保存</button><button v-if="canTest" type="button" class="btn-primary" :disabled="saving || !selectedStorage.enabled" @click="testSelected">测试</button><button v-if="canDelete" type="button" class="btn-danger" :disabled="saving" @click="deleteSelected">删除</button></div></form>
      <form v-else-if="selectedPan115" class="panel" @submit.prevent="savePan115"><div class="flex items-start justify-between gap-3"><div><h2 class="m-0">{{ selectedPan115.name }}</h2><p class="semantic-warning-text mb-0 mt-1 text-xs">账号已保存，但还没有选择云端根目录</p></div><span :class="statusClass(selectedKey)">{{ status(selectedKey) }}</span></div><div class="mt-5 grid gap-4 md:grid-cols-2"><div><label class="label">账号名称</label><input v-model="cloudEdit.name" class="input" :disabled="!canEdit" /></div><div><label class="label">账号</label><input class="input" :value="selectedPan115.account.name || '待测试'" disabled /></div><div class="md:col-span-2"><label class="label">更换 Cookie（可选）</label><SecretInput v-model="cloudEdit.cookie" class="input min-h-24 font-mono text-xs" multiline :configured="selectedPan115.credential_configured" :disabled="!canEdit" placeholder="留空表示继续使用已保存的 Cookie" /></div><div class="md:col-span-2"><label class="label">115 回收站安全码（可选）</label><SecretInput v-model="cloudEdit.recyclePassword" class="input" :configured="selectedPan115.recycle_password_configured" maxlength="64" :disabled="!canEdit || cloudEdit.clearRecyclePassword" /><label v-if="selectedPan115.recycle_password_configured" class="text-muted mt-2 flex items-center gap-2 text-xs"><input v-model="cloudEdit.clearRecyclePassword" type="checkbox" :disabled="!canEdit" />移除已保存的安全码</label></div></div><div class="mt-5 flex flex-wrap gap-3"><button v-if="canBrowsePan115ForCreate" type="button" class="btn-primary" @click="finishConnection">选择目录并完成数据源</button><button v-if="canEdit" class="btn-secondary" :disabled="saving">保存账号</button><button v-if="canDelete" type="button" class="btn-danger" :disabled="saving" @click="deleteSelected">删除</button></div></form>
    </div>
    <DirectoryPickerDialog :open="pickerOpen" :storage-id="pickerTarget === 'edit' ? selectedStorage?.id : null" :provider-connection-id="pickerTarget === 'cloud-create' ? cloudCreate.connectionID : pickerTarget === 'cloud-edit' ? selectedStorage?.connection_id : null" @close="pickerOpen = false" @select="directorySelected" />
  </section>
</template>
