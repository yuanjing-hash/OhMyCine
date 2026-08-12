<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import { useAuthStore } from '@/stores/auth'
import type { ListResponse, StorageSummary } from '@/types/api'

const auth = useAuthStore()
const storages = ref<StorageSummary[]>([]); const selectedID = ref<number | null>(null)
const loading = ref(true); const saving = ref(false); const error = ref(''); const notice = ref(''); const createOpen = ref(false)
const createForm = ref({ name: '', rootPath: '', enabled: true }); const editForm = ref({ name: '', rootPath: '', enabled: true })
const selected = computed(() => storages.value.find(storage => storage.id === selectedID.value) ?? null)
watch(selected, storage => { editForm.value = { name: storage?.name ?? '', rootPath: storage?.root_path ?? '', enabled: storage?.enabled ?? true } }, { immediate: true })

async function load() {
  if (!auth.can(Permissions.StoragesRead)) { loading.value = false; storages.value = []; return }
  loading.value = true; error.value = ''
  try {
    const data = await api<ListResponse<StorageSummary>>('/api/v1/storages'); storages.value = data.list
    if (!storages.value.some(storage => storage.id === selectedID.value)) selectedID.value = storages.value[0]?.id ?? null
  } catch (reason) { error.value = message(reason) } finally { loading.value = false }
}
async function createStorage() { await run(async () => {
  const created = await api<StorageSummary>('/api/v1/storages', { method: 'POST', body: JSON.stringify({ name: createForm.value.name, type: 'local', root_path: createForm.value.rootPath, enabled: createForm.value.enabled }) })
  selectedID.value = created.id; createForm.value = { name: '', rootPath: '', enabled: true }; createOpen.value = false
  notice.value = '本地 Storage 已注册；没有扫描或修改目录内容'
}) }
async function saveStorage() { if (!selected.value) return; await run(async () => { await api('/api/v1/storages/' + selected.value?.id, { method: 'PATCH', body: JSON.stringify({ name: editForm.value.name, root_path: editForm.value.rootPath, enabled: editForm.value.enabled }) }); notice.value = 'Storage 配置已保存' }) }
async function testStorage() { if (!selected.value) return; await run(async () => { await api('/api/v1/storages/' + selected.value?.id + '/test', { method: 'POST', body: '{}' }); notice.value = '只读探测已完成' }) }
async function deleteStorage() {
  if (!selected.value || !window.confirm('确认删除 Storage“' + selected.value.name + '”？只会删除 Server 配置，真实目录和文件不会改变。')) return
  const id = selected.value.id; await run(async () => { await api('/api/v1/storages/' + id, { method: 'DELETE', body: '{}' }); selectedID.value = null; notice.value = 'Storage 配置已删除；真实文件未改动' })
}
async function run(action: () => Promise<void>) { saving.value = true; error.value = ''; try { await action(); await load() } catch (reason) { error.value = message(reason) } finally { saving.value = false } }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败' }
function bytes(value: number | null) { if (value === null) return '未知'; const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; let amount = value; let index = 0; while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index += 1 }; return amount.toFixed(index === 0 ? 0 : 1) + ' ' + units[index] }
function probeLabel(storage: StorageSummary) { if (!storage.enabled) return '已停用'; if (storage.probe.error_code) return '需要处理'; if (storage.probe.readable && storage.probe.available) return '在线可读'; return '未探测' }
function probeClass(storage: StorageSummary) { if (!storage.enabled) return 'status-chip'; return storage.probe.error_code ? 'status-chip status-chip--error' : storage.probe.readable ? 'status-chip status-chip--ready' : 'status-chip status-chip--warning' }
onMounted(load)
</script>

<template>
  <section class="mx-auto max-w-6xl">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div><p class="mb-2 text-xs font-700 uppercase tracking-[.22em] text-cyan-300">Storage foundation</p><h1 class="m-0 text-3xl font-800">连接与存储</h1><p class="mt-2 max-w-3xl text-slate-400">Storage 注册可安全访问的提供方根；Storage Destination 决定最终放置位置，当前仍在规划中。</p></div>
      <button v-if="auth.can(Permissions.StoragesCreate)" class="btn-primary" @click="createOpen = !createOpen">{{ createOpen ? '取消添加' : '添加本地 Storage' }}</button>
    </div>
    <p v-if="error" role="alert" class="mt-5 rounded-3 bg-red-400/10 p-3 text-sm text-red-200">{{ error }}</p><p v-if="notice" class="mt-5 rounded-3 bg-emerald-400/10 p-3 text-sm text-emerald-200">{{ notice }}</p>
    <form v-if="createOpen" class="panel mt-6 grid gap-4 md:grid-cols-2" @submit.prevent="createStorage">
      <div><label class="label">名称</label><input v-model="createForm.name" class="input" required maxlength="128" placeholder="115 下载盘" /></div>
      <div><label class="label">类型</label><input class="input" value="本地目录（local）" disabled /></div>
      <div class="md:col-span-2"><label class="label">Windows 绝对根路径</label><input v-model="createForm.rootPath" class="input font-mono" required placeholder="D:\Media 或 \\NAS\Media" /><p class="mb-0 mt-2 text-xs text-slate-500">必须是已存在的真实目录；拒绝相对路径、文件和 Reparse Point。添加时只做安全的根校验与只读探测。</p></div>
      <label class="flex items-center gap-3 text-sm text-slate-300"><input v-model="createForm.enabled" type="checkbox" />创建后启用</label><button class="btn-primary md:col-span-2" :disabled="saving">注册 Storage</button>
    </form>
    <section v-if="!auth.can(Permissions.StoragesRead)" class="panel mt-8 opacity-75"><span class="status-chip status-chip--planned">规划中</span><h2 class="mb-0 mt-3 text-lg">Connections / Storage Destinations</h2><p class="mb-0 mt-2 text-sm text-slate-500">当前权限只允许进入未来连接或目标工作区，不包含本地 Storage 配置；页面不会请求或泄露 Storage 根路径。</p></section>
    <div v-else-if="loading" class="mt-8 text-slate-500">正在读取 Storage 配置…</div>
    <div v-else-if="storages.length === 0" class="panel mt-8"><h2 class="m-0 text-lg">尚未注册 Storage</h2><p class="mb-0 mt-2 text-sm text-slate-400">有创建权限的管理员可注册一个本地绝对目录。Server 不会因此创建媒体库或扫描媒体。</p></div>
    <div v-else class="mt-7 grid gap-5 xl:grid-cols-[minmax(20rem,.75fr)_minmax(30rem,1.25fr)]">
      <div class="panel p-2"><button v-for="storage in storages" :key="storage.id" class="mb-1 w-full rounded-3 p-3 text-left transition hover:bg-white/7" :class="selectedID === storage.id ? 'bg-white/10' : ''" @click="selectedID = storage.id"><div class="flex items-center justify-between gap-3"><strong>{{ storage.name }}</strong><span :class="probeClass(storage)">{{ probeLabel(storage) }}</span></div><div class="mt-2 truncate font-mono text-xs text-slate-500">{{ storage.root_path }}</div><div class="mt-2 text-xs text-slate-400">可用 {{ bytes(storage.probe.free_bytes) }} / 总计 {{ bytes(storage.probe.total_bytes) }}</div></button></div>
      <section v-if="selected" class="space-y-5">
        <form class="panel" @submit.prevent="saveStorage">
          <div class="flex flex-wrap items-start justify-between gap-3"><div><h2 class="m-0">{{ selected.name }}</h2><p class="mt-1 text-xs text-slate-500">最后探测 {{ new Date(selected.probe.last_checked_at).toLocaleString() }}</p></div><span :class="probeClass(selected)">{{ probeLabel(selected) }}</span></div>
          <div class="mt-5 grid gap-4 md:grid-cols-2"><div><label class="label">名称</label><input v-model="editForm.name" class="input" maxlength="128" :disabled="!auth.can(Permissions.StoragesUpdate)" /></div><div><label class="label">类型</label><input class="input" :value="selected.type" disabled /></div><div class="md:col-span-2"><label class="label">根路径</label><input v-model="editForm.rootPath" class="input font-mono" :disabled="!auth.can(Permissions.StoragesUpdate)" /></div><label class="flex items-center gap-3 text-sm text-slate-300"><input v-model="editForm.enabled" type="checkbox" :disabled="!auth.can(Permissions.StoragesUpdate)" />启用 Storage</label></div>
          <div class="mt-5 flex flex-wrap gap-3"><button v-if="auth.can(Permissions.StoragesUpdate)" class="btn-secondary" :disabled="saving">保存配置</button><button v-if="auth.can(Permissions.StoragesTest)" type="button" class="btn-secondary" :disabled="saving" @click="testStorage">只读测试</button><button v-if="auth.can(Permissions.StoragesDelete)" type="button" class="btn-danger" :disabled="saving" @click="deleteStorage">删除配置</button></div>
        </form>
        <section class="panel"><h2 class="m-0 text-lg">只读能力摘要</h2><div class="mt-4 grid gap-3 sm:grid-cols-2"><div class="rounded-3 bg-white/4 p-3"><span class="text-xs text-slate-500">目录枚举</span><strong class="mt-1 block">{{ selected.capabilities.directory_list ? '支持' : '不支持' }}</strong></div><div class="rounded-3 bg-white/4 p-3"><span class="text-xs text-slate-500">文件系统 Watch</span><strong class="mt-1 block">{{ selected.capabilities.watch ? '支持' : '当前不提供' }}</strong></div><div class="rounded-3 bg-white/4 p-3"><span class="text-xs text-slate-500">可用空间</span><strong class="mt-1 block">{{ bytes(selected.probe.free_bytes) }}</strong></div><div class="rounded-3 bg-white/4 p-3"><span class="text-xs text-slate-500">总容量</span><strong class="mt-1 block">{{ bytes(selected.probe.total_bytes) }}</strong></div></div><p class="mb-0 mt-4 text-xs leading-6 text-slate-500">本地 Storage 不提供原生离线下载、临时直链、signed 302 或 change cursor；这些能力不能在界面中手动开启。</p></section>
        <section class="panel opacity-75"><span class="status-chip status-chip--planned">规划中</span><h2 class="mb-0 mt-3 text-lg">Connections / Storage Destinations</h2><p class="mb-0 mt-2 text-sm text-slate-500">外部连接和最终放置目标将在后续独立任务中实现，本页不会伪造其配置或状态。</p></section>
      </section>
    </div>
  </section>
</template>
