<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import SecretInput from '@/components/SecretInput.vue'
import {
  buildEmbyGatewayUpdatePayload,
  canEnableEmbyGateway,
  connectionListPath,
  consumeEmbyCreatePayload,
  consumeEmbyUpdatePayload,
  isLoopbackURL,
  type EmbyConnectionDraft,
} from '@/connections'
import { credentialLoader } from '@/credentials'
import { playerClientLabel, playerDeviceConfirmation, playerDeviceListPath, playerDeviceRevokePath, playerDeviceTime } from '@/player-devices'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import type { ConnectionSummary, EmbyGatewaySummary, EmbyManagementSummary, ListResponse, MediaLibraryDetail, MediaServerLibrarySummary, MediaServerRefreshTargetSummary, PlayerDeviceSummary } from '@/types/api'

interface SummaryState {
  item: EmbyManagementSummary | null
  loading: boolean
  failed: boolean
}

const auth = useAuthStore()
const connections = ref<ConnectionSummary[]>([])
const summaries = ref<Record<number, SummaryState>>({})
const gateways = ref<Record<number, EmbyGatewaySummary | null>>({})
const devices = ref<PlayerDeviceSummary[]>([])
const devicesLoading = ref(true)
const devicesFailed = ref(false)
const revokingDeviceID = ref<string | null>(null)
const gatewayAliasDrafts = ref<Record<number, string>>({})
const gatewayFailed = ref<Record<number, boolean>>({})
const loading = ref(true)
const creating = ref(false)
const busyCounts = ref<Record<number, number>>({})
const addOpen = ref(false)
const editingID = ref<number | null>(null)
const createDraft = ref<EmbyConnectionDraft>({ provider: 'emby', name: '', endpoint: 'http://127.0.0.1:8096', apiKey: '', enabled: true })
const editDraft = ref<EmbyConnectionDraft>({ provider: 'emby', name: '', endpoint: '', apiKey: '', enabled: true })
const mediaLibraries = ref<MediaLibraryDetail[]>([])
const refreshTargets = ref<MediaServerRefreshTargetSummary[]>([])
const upstreamLibraries = ref<MediaServerLibrarySummary[]>([])
const refreshDraft = ref({ libraryId: 0, connectionId: 0, upstreamLibraryId: '', enabled: true })
const refreshBusy = ref(false)

const players = computed(() => connections.value.filter(item => item.provider === 'emby' || item.provider === 'jellyfin'))
const canCreate = computed(() => auth.can(Permissions.ConnectionsCreate))

async function loadConnections() {
  const [emby, jellyfin] = await Promise.all([
    api<ListResponse<ConnectionSummary>>(connectionListPath('emby')),
    api<ListResponse<ConnectionSummary>>(connectionListPath('jellyfin')),
  ])
  connections.value = [...emby.list, ...jellyfin.list]
}

async function loadDevices(showSuccess = false) {
  devicesLoading.value = true
  devicesFailed.value = false
  try {
    const response = await api<ListResponse<PlayerDeviceSummary>>(playerDeviceListPath)
    devices.value = response.list
    if (showSuccess) notify('Player 设备已刷新', 'success')
  } catch (reason) {
    devicesFailed.value = true
    notify(message(reason), 'error')
  } finally {
    devicesLoading.value = false
  }
}

async function loadCard(connection: ConnectionSummary) {
  if (connection.provider === 'jellyfin') {
    summaries.value[connection.id] = { item: null, loading: false, failed: false }
    gateways.value[connection.id] = null
    gatewayFailed.value[connection.id] = false
    return
  }
  summaries.value[connection.id] = { item: null, loading: connection.enabled, failed: false }
  gatewayFailed.value[connection.id] = false
  const summaryRequest = connection.enabled
    ? api<EmbyManagementSummary>(`/api/v1/connections/${connection.id}/emby-summary`)
    : Promise.resolve(null)
  const [summary, gateway] = await Promise.allSettled([
    summaryRequest,
    api<EmbyGatewaySummary>(`/api/v1/connections/${connection.id}/emby-gateway`),
  ])
  if (summary.status === 'fulfilled') {
    summaries.value[connection.id] = { item: summary.value, loading: false, failed: false }
  } else {
    summaries.value[connection.id] = { item: null, loading: false, failed: true }
  }
  if (gateway.status === 'fulfilled') {
    gateways.value[connection.id] = gateway.value
    gatewayAliasDrafts.value[connection.id] = gateway.value.alias
  }
  else {
    gateways.value[connection.id] = null
    gatewayFailed.value[connection.id] = true
  }
}

async function load() {
  if (!auth.can(Permissions.ConnectionsRead)) return
  loading.value = true
  try {
    await loadConnections()
    await Promise.all([Promise.all(players.value.map(loadCard)), loadRefreshTargets()])
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    loading.value = false
  }
}

function toggleAdd() {
  addOpen.value = !addOpen.value
  createDraft.value.apiKey = ''
  if (!addOpen.value) createDraft.value = { provider: 'emby', name: '', endpoint: 'http://127.0.0.1:8096', apiKey: '', enabled: true }
}

async function createEmby() {
  const payload = consumeEmbyCreatePayload(createDraft.value)
  creating.value = true
  try {
    await api('/api/v1/connections', { method: 'POST', body: JSON.stringify(payload) })
    const provider = createDraft.value.provider
    createDraft.value = { provider, name: '', endpoint: 'http://127.0.0.1:8096', apiKey: '', enabled: true }
    addOpen.value = false
    notify(`${providerLabel(provider)} 已添加，请先测试连接`, 'success')
    await load()
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    creating.value = false
  }
}

function startEdit(connection: ConnectionSummary) {
  editDraft.value.apiKey = ''
  editingID.value = connection.id
  editDraft.value = { provider: connection.provider === 'jellyfin' ? 'jellyfin' : 'emby', name: connection.name, endpoint: connection.endpoint, apiKey: '', enabled: connection.enabled }
}

function cancelEdit() {
  editDraft.value.apiKey = ''
  editingID.value = null
  editDraft.value = { provider: 'emby', name: '', endpoint: '', apiKey: '', enabled: true }
}

async function saveEmby(connection: ConnectionSummary) {
  const payload = consumeEmbyUpdatePayload(editDraft.value, connection.revision)
  beginCardWork(connection.id)
  try {
    await api(`/api/v1/connections/${connection.id}`, { method: 'PATCH', body: JSON.stringify(payload) })
    cancelEdit()
    notify(`${providerLabel(connection.provider)} 连接已保存，请重新测试`, 'success')
    await load()
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    editDraft.value.apiKey = ''
    endCardWork(connection.id)
  }
}

async function testConnection(connection: ConnectionSummary) {
  beginCardWork(connection.id)
  try {
    await api(`/api/v1/connections/${connection.id}/test`, { method: 'POST', body: '{}' })
    notify(`${providerLabel(connection.provider)} 连接测试成功`, 'success')
    await loadConnections()
    const current = players.value.find(item => item.id === connection.id)
    if (current) await loadCard(current)
  } catch (reason) {
    notify(message(reason), 'error')
    try {
      await loadConnections()
      const current = players.value.find(item => item.id === connection.id)
      if (current) await loadCard(current)
    } catch {
      summaries.value[connection.id] = { item: null, loading: false, failed: true }
    }
  } finally {
    endCardWork(connection.id)
  }
}

async function refreshSummary(connection: ConnectionSummary) {
  beginCardWork(connection.id)
  try {
    await loadCard(connection)
    if (summaries.value[connection.id]?.failed) notify('Emby 摘要读取失败，请检查连接和运行日志', 'error')
    else notify('播放器摘要已刷新', 'success')
  } finally {
    endCardWork(connection.id)
  }
}

type GatewayPatch = Partial<Pick<EmbyGatewaySummary, 'enabled' | 'alias' | 'external_player_enabled' | 'fanart_enabled'>>

async function updateGateway(connection: ConnectionSummary, patch: GatewayPatch, successMessage: string) {
  const gateway = gateways.value[connection.id]
  if (!gateway) return
  const settings = { ...gateway, ...patch }
  beginCardWork(connection.id)
  try {
    gateways.value[connection.id] = await api<EmbyGatewaySummary>(`/api/v1/connections/${connection.id}/emby-gateway`, {
      method: 'PATCH',
      body: JSON.stringify(buildEmbyGatewayUpdatePayload(settings.enabled, settings.alias, settings.external_player_enabled, settings.fanart_enabled, gateway.revision)),
    })
    gatewayAliasDrafts.value[connection.id] = gateways.value[connection.id]?.alias || ''
    notify(successMessage, 'success')
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    endCardWork(connection.id)
  }
}

async function saveGatewayAlias(connection: ConnectionSummary) {
  const gateway = gateways.value[connection.id]
  const alias = gatewayAliasDrafts.value[connection.id]?.trim() || ''
  if (!gateway || !alias || alias === gateway.alias) return
  await updateGateway(connection, { alias }, 'Emby 网关别名已保存')
}

async function copyGatewayBaseURL(connectionID: number) {
  const value = gateways.value[connectionID]?.base_url
  if (!value) return
  try {
    await navigator.clipboard.writeText(value)
    notify('Emby 网关 Base URL 已复制', 'success')
  } catch {
    notify('复制失败，请手动选择 Base URL', 'error')
  }
}

async function deleteEmby(connection: ConnectionSummary) {
  if (!window.confirm(`确认删除播放器“${connection.name}”？该操作只删除连接配置，不会删除 Emby 中的媒体。`)) return
  beginCardWork(connection.id)
  try {
    await api(`/api/v1/connections/${connection.id}`, { method: 'DELETE', body: '{}' })
    if (editingID.value === connection.id) cancelEdit()
    notify('播放器连接已删除', 'success')
    await load()
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    endCardWork(connection.id)
  }
}

function healthLabel(connection: ConnectionSummary) {
  if (!connection.enabled) return '已停用'
  if (connection.health.status === 'online') return '在线'
  if (connection.health.status === 'offline') return '异常'
  return '待测试'
}

function healthClass(connection: ConnectionSummary) {
  const value = healthLabel(connection)
  if (value === '在线') return 'status-chip status-chip--ready'
  if (value === '异常') return 'status-chip status-chip--error'
  if (value === '待测试') return 'status-chip status-chip--warning'
  return 'status-chip'
}

function summaryValue(connectionID: number, field: 'library_count' | 'movie_count' | 'series_count' | 'episode_count') {
  const state = summaries.value[connectionID]
  if (state?.loading) return '读取中'
  const value = state?.item?.[field]
  return typeof value === 'number' ? value.toLocaleString() : '未知'
}

function beginCardWork(connectionID: number) {
  busyCounts.value = { ...busyCounts.value, [connectionID]: (busyCounts.value[connectionID] ?? 0) + 1 }
}

function endCardWork(connectionID: number) {
  const next = { ...busyCounts.value }
  const remaining = (next[connectionID] ?? 1) - 1
  if (remaining > 0) next[connectionID] = remaining
  else delete next[connectionID]
  busyCounts.value = next
}

function cardBusy(connectionID: number) {
  return (busyCounts.value[connectionID] ?? 0) > 0
}

function checkedAt(value?: string | null, empty = '尚未查询') {
  return value ? new Date(value).toLocaleString() : empty
}

function message(reason: unknown) {
  return reason instanceof Error ? reason.message : '操作失败'
}

function providerLabel(provider: string) {
  return provider === 'jellyfin' ? 'Jellyfin' : 'Emby'
}

async function loadRefreshTargets() {
  if (!auth.can(Permissions.MediaLibrariesRead)) return
  const [libraries, targets] = await Promise.all([
    api<ListResponse<MediaLibraryDetail>>('/api/v1/media-libraries'),
    api<ListResponse<MediaServerRefreshTargetSummary>>('/api/v1/media-server-refresh-targets'),
  ])
  mediaLibraries.value = libraries.list
  refreshTargets.value = targets.list
}

async function loadUpstreamLibraries() {
  refreshDraft.value.upstreamLibraryId = ''
  upstreamLibraries.value = []
  if (!refreshDraft.value.connectionId) return
  try {
    const response = await api<ListResponse<MediaServerLibrarySummary>>(`/api/v1/connections/${refreshDraft.value.connectionId}/media-server-libraries`)
    upstreamLibraries.value = response.list
  } catch (reason) {
    notify(message(reason), 'error')
  }
}

async function createRefreshTarget() {
  refreshBusy.value = true
  try {
    await api('/api/v1/media-server-refresh-targets', { method: 'POST', body: JSON.stringify({ library_id: refreshDraft.value.libraryId, connection_id: refreshDraft.value.connectionId, upstream_library_id: refreshDraft.value.upstreamLibraryId, enabled: refreshDraft.value.enabled }) })
    notify('媒体服务器刷新绑定已创建', 'success')
    refreshDraft.value = { libraryId: 0, connectionId: 0, upstreamLibraryId: '', enabled: true }
    upstreamLibraries.value = []
    await loadRefreshTargets()
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    refreshBusy.value = false
  }
}

async function toggleRefreshTarget(target: MediaServerRefreshTargetSummary) {
  refreshBusy.value = true
  try {
    await api(`/api/v1/media-server-refresh-targets/${target.id}`, { method: 'PATCH', body: JSON.stringify({ enabled: !target.enabled, revision: target.revision }) })
    await loadRefreshTargets()
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    refreshBusy.value = false
  }
}

async function runRefreshTarget(target: MediaServerRefreshTargetSummary) {
  refreshBusy.value = true
  try {
    await api(`/api/v1/media-server-refresh-targets/${target.id}/refresh`, { method: 'POST', body: '{}' })
    notify('媒体服务器刷新任务已入队', 'success')
    await loadRefreshTargets()
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    refreshBusy.value = false
  }
}

async function deleteRefreshTarget(target: MediaServerRefreshTargetSummary) {
  if (!window.confirm(`确认删除刷新绑定“${target.upstream_library_name}”？`)) return
  refreshBusy.value = true
  try {
    await api(`/api/v1/media-server-refresh-targets/${target.id}`, { method: 'DELETE', body: '{}' })
    await loadRefreshTargets()
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    refreshBusy.value = false
  }
}

function libraryName(id: number) {
  return mediaLibraries.value.find(item => item.id === id)?.name ?? `媒体库 #${id}`
}

function connectionName(id: number) {
  return connections.value.find(item => item.id === id)?.name ?? `连接 #${id}`
}

async function revokeDevice(device: PlayerDeviceSummary) {
  if (!window.confirm(playerDeviceConfirmation(device))) return
  revokingDeviceID.value = device.id
  try {
    await api(playerDeviceRevokePath(device.id), { method: 'DELETE', body: '{}' })
    devices.value = devices.value.filter(item => item.id !== device.id)
    notify('Player 设备配对已撤销', 'success')
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    revokingDeviceID.value = null
  }
}

onMounted(() => {
  void load()
  void loadDevices()
})
</script>

<template>
  <section class="mx-auto max-w-7xl">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="m-0 text-3xl font-800">播放器管理</h1>
        <p class="page-description mb-0 mt-2 max-w-3xl">集中管理 Emby/Jellyfin 连接、媒体库刷新绑定，以及 Emby 签名 STRM 的 302 播放网关。</p>
      </div>
      <button v-if="canCreate" class="btn-primary" @click="toggleAdd">{{ addOpen ? '取消添加' : '添加媒体服务器' }}</button>
    </div>

    <form v-if="addOpen" class="panel mt-6 grid gap-4 md:grid-cols-2" @submit.prevent="createEmby">
      <div class="md:col-span-2"><h2 class="m-0 text-xl">添加媒体服务器</h2><p class="text-subtle mb-0 mt-2 text-sm">API Key 只发送给 Server，并使用 AES-GCM 加密保存。</p></div>
      <div><label class="label">类型</label><select v-model="createDraft.provider" class="input"><option value="emby">Emby</option><option value="jellyfin">Jellyfin</option></select></div>
      <div><label class="label">名称</label><input v-model="createDraft.name" class="input" required maxlength="128" :placeholder="`家庭 ${providerLabel(createDraft.provider)}`" /></div>
      <div><label class="label">服务地址</label><input v-model="createDraft.endpoint" class="input" required type="url" placeholder="http://127.0.0.1:8096" /></div>
      <div class="md:col-span-2"><label class="label">API Key</label><SecretInput v-model="createDraft.apiKey" class="input font-mono" required autocomplete="new-password" spellcheck="false" /></div>
      <label class="text-muted flex items-center gap-3 text-sm"><input v-model="createDraft.enabled" type="checkbox" />添加后启用</label>
      <button class="btn-primary md:col-span-2" :disabled="creating || !createDraft.name.trim() || !createDraft.endpoint.trim() || !createDraft.apiKey.trim()">添加 {{ providerLabel(createDraft.provider) }}</button>
    </form>

    <p v-if="loading" class="text-subtle mt-8">正在读取播放器…</p>
    <section v-else-if="!players.length" class="panel mt-7">
      <h2 class="m-0 text-lg">尚未添加 Emby/Jellyfin</h2>
      <p class="page-description mb-0 mt-2 text-sm">添加并测试连接后，即可绑定上游媒体库并自动刷新。</p>
    </section>

    <div v-else class="mt-7 grid gap-5 lg:grid-cols-2">
      <article v-for="connection in players" :key="connection.id" class="panel">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0"><h2 class="m-0 truncate text-xl">{{ connection.name }}</h2><p class="text-subtle mb-0 mt-1 truncate font-mono text-xs">{{ connection.endpoint }}</p></div>
          <span :class="healthClass(connection)">{{ healthLabel(connection) }}</span>
        </div>

        <p class="text-subtle mb-0 mt-2 text-xs">{{ providerLabel(connection.provider) }}</p>
        <div v-if="connection.provider === 'emby'" class="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div class="semantic-inset p-3"><span class="text-subtle text-xs">媒体库</span><strong class="mt-1 block">{{ summaryValue(connection.id, 'library_count') }}</strong></div>
          <div class="semantic-inset p-3"><span class="text-subtle text-xs">电影</span><strong class="mt-1 block">{{ summaryValue(connection.id, 'movie_count') }}</strong></div>
          <div class="semantic-inset p-3"><span class="text-subtle text-xs">剧集</span><strong class="mt-1 block">{{ summaryValue(connection.id, 'series_count') }}</strong></div>
          <div class="semantic-inset p-3"><span class="text-subtle text-xs">单集</span><strong class="mt-1 block">{{ summaryValue(connection.id, 'episode_count') }}</strong></div>
        </div>

        <div v-if="connection.provider === 'emby'" class="mt-4 grid gap-2 text-xs sm:grid-cols-2">
          <div><span class="text-subtle">Emby 版本：</span><strong>{{ summaries[connection.id]?.item?.version || '未知' }}</strong></div>
          <div><span class="text-subtle">服务器：</span><strong>{{ summaries[connection.id]?.item?.server_name || '未知' }}</strong></div>
          <div><span class="text-subtle">摘要查询：</span>{{ checkedAt(summaries[connection.id]?.item?.checked_at) }}</div>
          <div><span class="text-subtle">连接探测：</span>{{ checkedAt(connection.health.last_checked_at, '尚未测试') }}</div>
        </div>
        <p v-if="summaries[connection.id]?.failed" class="semantic-warning mb-0 mt-4 p-3 text-xs">摘要读取失败，统计值保持“未知”。请重新测试连接并检查运行日志。</p>
        <p v-else-if="summaries[connection.id]?.item?.status === 'partial'" class="semantic-warning mb-0 mt-4 p-3 text-xs">Emby 已连接，但部分聚合统计暂不可用；不可用项目保持“未知”。</p>

        <section v-if="connection.provider === 'emby'" class="semantic-inset mt-5 p-4">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div><h3 class="m-0 text-base">Emby 302 网关</h3><p class="text-subtle mb-0 mt-1 text-xs">与 Web UI 和 STRM 共用 Server 主端口，不单独配置端口。</p></div>
            <span v-if="gateways[connection.id]" :class="gateways[connection.id]?.enabled ? 'status-chip status-chip--ready' : 'status-chip'">{{ gateways[connection.id]?.enabled ? '已启用' : '默认关闭' }}</span>
          </div>
          <p v-if="gatewayFailed[connection.id]" class="semantic-warning mb-0 mt-3 p-3 text-xs">网关配置读取失败。</p>
          <template v-else-if="gateways[connection.id]">
            <div class="mt-4"><label class="label">网关别名</label><div class="flex gap-2"><input v-model="gatewayAliasDrafts[connection.id]" class="input min-w-0 font-mono text-xs" maxlength="32" :disabled="!auth.can(Permissions.ConnectionsUpdate) || cardBusy(connection.id)" placeholder="home-cinema" /><button v-if="auth.can(Permissions.ConnectionsUpdate)" type="button" class="btn-secondary shrink-0" :disabled="cardBusy(connection.id) || !gatewayAliasDrafts[connection.id]?.trim() || gatewayAliasDrafts[connection.id]?.trim() === gateways[connection.id]?.alias" @click="saveGatewayAlias(connection)">保存别名</button></div><p class="text-subtle mb-0 mt-2 text-xs">3-32 位字母、数字或单个连字符；保存后旧地址立即失效。</p></div>
            <div class="mt-4 flex gap-2"><input class="input min-w-0 font-mono text-xs" :value="gateways[connection.id]?.base_url" readonly /><button type="button" class="btn-secondary shrink-0" @click="copyGatewayBaseURL(connection.id)">复制地址</button></div>
            <p v-if="isLoopbackURL(gateways[connection.id]?.base_url || '')" class="semantic-warning mb-0 mt-3 p-3 text-xs">当前为回环地址：同机 Emby 可以使用，其它 NAS/Player 设备无法访问。跨设备请把全局 public_origin 配为 Server 的局域网 IP 或域名后重启。</p>
            <label class="text-muted mt-4 flex items-start gap-3 text-sm">
              <input :checked="gateways[connection.id]?.enabled" type="checkbox" :disabled="cardBusy(connection.id) || !auth.can(Permissions.ConnectionsUpdate) || (!gateways[connection.id]?.enabled && !canEnableEmbyGateway(connection.enabled, connection.health.status))" @change="updateGateway(connection, { enabled: ($event.target as HTMLInputElement).checked }, ($event.target as HTMLInputElement).checked ? 'Emby 302 网关已启用' : 'Emby 302 网关已关闭')" />
              <span><strong class="text-normal block">启用 302 网关</strong><span class="text-subtle text-xs">只有已启用且测试在线的 Emby 才能开启；普通 API、图片和媒体请求保持透明转发。</span></span>
            </label>
            <div class="mt-4 grid gap-3 sm:grid-cols-2">
              <label class="text-muted flex items-start gap-3 text-sm">
                <input :checked="gateways[connection.id]?.external_player_enabled" type="checkbox" :disabled="cardBusy(connection.id) || !auth.can(Permissions.ConnectionsUpdate)" @change="updateGateway(connection, { external_player_enabled: ($event.target as HTMLInputElement).checked }, ($event.target as HTMLInputElement).checked ? '外部播放器入口已启用' : '外部播放器入口已关闭')" />
                <span><strong class="text-normal block">外部播放器</strong><span class="text-subtle text-xs">在 Emby 详情页按设备显示 PotPlayer、VLC、MPV、IINA 等入口，仅传递短时播放票据。</span></span>
              </label>
              <label class="text-muted flex items-start gap-3 text-sm">
                <input :checked="gateways[connection.id]?.fanart_enabled" type="checkbox" :disabled="cardBusy(connection.id) || !auth.can(Permissions.ConnectionsUpdate)" @change="updateGateway(connection, { fanart_enabled: ($event.target as HTMLInputElement).checked }, ($event.target as HTMLInputElement).checked ? '同人图展示已启用' : '同人图展示已关闭')" />
                <span><strong class="text-normal block">显示同人图</strong><span class="text-subtle text-xs">在电影、剧集、人物和视频详情页显示 Emby 已有背景图，可横向浏览并全屏预览。</span></span>
              </label>
            </div>
          </template>
        </section>

        <form v-if="editingID === connection.id" class="semantic-inset mt-5 grid gap-4 p-4 md:grid-cols-2" @submit.prevent="saveEmby(connection)">
          <div><label class="label">名称</label><input v-model="editDraft.name" class="input" required maxlength="128" /></div>
          <div><label class="label">服务地址</label><input v-model="editDraft.endpoint" class="input" required type="url" /></div>
          <div class="md:col-span-2"><label class="label">更换 API Key（可选）</label><SecretInput v-model="editDraft.apiKey" class="input font-mono" :configured="connection.credential_configured" :load-secret="auth.can(Permissions.ConnectionsSecretsExport) ? credentialLoader({ resourceType: 'connection', resourceID: connection.id, field: 'credential' }) : undefined" :reset-key="connection.id" autocomplete="new-password" spellcheck="false" placeholder="留空表示继续使用已保存的 API Key" /><p class="text-subtle mb-0 mt-2 text-xs">星号表示已有 API Key；点击眼睛可临时查看，直接保存不会把回显值当作新凭据提交。</p></div>
          <label class="text-muted flex items-center gap-3 text-sm"><input v-model="editDraft.enabled" type="checkbox" />启用连接</label>
          <div class="text-subtle self-center text-xs">凭据：{{ connection.credential_configured ? '已安全配置' : '未配置' }}</div>
          <div class="md:col-span-2 flex gap-3"><button class="btn-primary" :disabled="cardBusy(connection.id)">保存连接</button><button type="button" class="btn-secondary" @click="cancelEdit">取消</button></div>
        </form>

        <div class="mt-5 flex flex-wrap gap-3">
          <button v-if="auth.can(Permissions.ConnectionsTest)" type="button" class="btn-primary" :disabled="cardBusy(connection.id) || !connection.enabled" @click="testConnection(connection)">测试连接</button>
          <button v-if="connection.provider === 'emby'" type="button" class="btn-secondary" :disabled="cardBusy(connection.id) || !connection.enabled" @click="refreshSummary(connection)">刷新摘要</button>
          <button v-if="auth.can(Permissions.ConnectionsUpdate) && editingID !== connection.id" type="button" class="btn-secondary" :disabled="cardBusy(connection.id)" @click="startEdit(connection)">编辑连接</button>
          <button v-if="auth.can(Permissions.ConnectionsDelete)" type="button" class="btn-danger" :disabled="cardBusy(connection.id)" @click="deleteEmby(connection)">删除</button>
        </div>
      </article>
    </div>

    <section class="panel mt-7">
      <div>
        <h2 class="m-0 text-lg">媒体库自动刷新</h2>
        <p class="page-description mb-0 mt-2 text-sm">媒体真正可用后，Server 会并行通知所有启用的 Emby/Jellyfin 绑定；失败目标不会阻塞 Player 更新。</p>
      </div>

      <form v-if="auth.can(Permissions.ConnectionsUpdate) && auth.can(Permissions.MediaLibrariesUpdate)" class="semantic-inset mt-5 grid gap-4 p-4 md:grid-cols-2" @submit.prevent="createRefreshTarget">
        <div><label class="label">OhMyCine 媒体库</label><select v-model.number="refreshDraft.libraryId" class="input" required><option :value="0" disabled>选择媒体库</option><option v-for="library in mediaLibraries" :key="library.id" :value="library.id">{{ library.name }}</option></select></div>
        <div><label class="label">媒体服务器连接</label><select v-model.number="refreshDraft.connectionId" class="input" required @change="loadUpstreamLibraries"><option :value="0" disabled>选择连接</option><option v-for="connection in players" :key="connection.id" :value="connection.id" :disabled="!connection.enabled">{{ connection.name }} · {{ providerLabel(connection.provider) }}</option></select></div>
        <div class="md:col-span-2"><label class="label">上游媒体库</label><select v-model="refreshDraft.upstreamLibraryId" class="input" required :disabled="!refreshDraft.connectionId"><option value="" disabled>{{ refreshDraft.connectionId ? '选择上游媒体库' : '请先选择连接' }}</option><option v-for="library in upstreamLibraries" :key="library.id" :value="library.id">{{ library.name }}{{ library.content_type ? ` · ${library.content_type}` : '' }}</option></select></div>
        <label class="text-muted flex items-center gap-3 text-sm"><input v-model="refreshDraft.enabled" type="checkbox" />创建后启用自动刷新</label>
        <button class="btn-primary md:col-span-2" :disabled="refreshBusy || !refreshDraft.libraryId || !refreshDraft.connectionId || !refreshDraft.upstreamLibraryId">创建刷新绑定</button>
      </form>

      <p v-if="refreshTargets.length === 0" class="text-subtle mb-0 mt-5 text-sm">尚未配置刷新绑定。没有绑定时 Server 不会伪造刷新成功。</p>
      <div v-else class="mt-5 grid gap-4 lg:grid-cols-2">
        <article v-for="target in refreshTargets" :key="target.id" class="semantic-inset p-4">
          <div class="flex items-start justify-between gap-4"><div><h3 class="m-0 text-base">{{ libraryName(target.library_id) }} → {{ target.upstream_library_name }}</h3><p class="text-subtle mb-0 mt-1 text-xs">{{ connectionName(target.connection_id) }}</p></div><span :class="target.last_status === 'failed' ? 'status-chip status-chip--error' : target.enabled ? 'status-chip status-chip--ready' : 'status-chip'">{{ target.enabled ? target.last_status : '已停用' }}</span></div>
          <dl class="mt-4 grid gap-3 text-sm sm:grid-cols-2"><div><dt class="text-subtle text-xs">待刷新版本</dt><dd class="m-0 mt-1">{{ target.desired_revision }}</dd></div><div><dt class="text-subtle text-xs">成功版本</dt><dd class="m-0 mt-1">{{ target.successful_revision }}</dd></div><div><dt class="text-subtle text-xs">最近尝试</dt><dd class="m-0 mt-1">{{ checkedAt(target.last_attempt_at) }}</dd></div><div><dt class="text-subtle text-xs">最近成功</dt><dd class="m-0 mt-1">{{ checkedAt(target.last_successful_at) }}</dd></div></dl>
          <p v-if="target.last_error_code" class="semantic-warning mb-0 mt-4 p-3 text-xs">刷新失败：{{ target.last_error_code }}</p>
          <div class="mt-4 flex flex-wrap gap-2"><button v-if="auth.can(Permissions.MediaServersRefresh)" type="button" class="btn-primary" :disabled="refreshBusy || !target.enabled" @click="runRefreshTarget(target)">立即刷新</button><button v-if="auth.can(Permissions.ConnectionsUpdate) && auth.can(Permissions.MediaLibrariesUpdate)" type="button" class="btn-secondary" :disabled="refreshBusy" @click="toggleRefreshTarget(target)">{{ target.enabled ? '停用' : '启用' }}</button><button v-if="auth.can(Permissions.ConnectionsUpdate) && auth.can(Permissions.MediaLibrariesUpdate)" type="button" class="btn-danger" :disabled="refreshBusy" @click="deleteRefreshTarget(target)">删除</button></div>
        </article>
      </div>
    </section>

    <section class="panel mt-7">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 class="m-0 text-lg">OhMyCine Player 设备</h2>
          <p class="page-description mb-0 mt-2 text-sm">Player 使用 Server 账号完成安全配对后会自动出现在这里。最近活动表示上次成功访问，并不代表设备此刻在线。</p>
        </div>
        <button type="button" class="btn-secondary" :disabled="devicesLoading" @click="loadDevices(true)">{{ devicesLoading ? '刷新中…' : '刷新设备' }}</button>
      </div>

      <p v-if="devicesLoading && devices.length === 0" class="text-subtle mb-0 mt-5 text-sm">正在读取已配对设备…</p>
      <div v-else-if="devicesFailed && devices.length === 0" class="semantic-warning mt-5 p-4 text-sm">
        <strong class="block">暂时无法读取 Player 设备</strong>
        <span class="text-subtle mt-1 block">Emby 连接不受影响，可以稍后单独刷新此区域。</span>
      </div>
      <p v-else-if="devices.length === 0" class="text-subtle mb-0 mt-5 text-sm">当前账号还没有有效的 Player 配对设备。</p>

      <div v-else class="mt-5 grid gap-4 lg:grid-cols-2">
        <article v-for="device in devices" :key="device.id" class="semantic-inset p-4">
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <h3 class="m-0 truncate text-base">{{ device.name }}</h3>
              <p class="text-subtle mb-0 mt-1 text-xs">{{ playerClientLabel(device.client_kind) }}</p>
            </div>
            <span class="status-chip status-chip--ready">配对有效</span>
          </div>
          <dl class="mt-4 grid gap-3 text-sm sm:grid-cols-2">
            <div><dt class="text-subtle text-xs">首次配对</dt><dd class="m-0 mt-1">{{ playerDeviceTime(device.created_at) }}</dd></div>
            <div><dt class="text-subtle text-xs">最近活动</dt><dd class="m-0 mt-1">{{ playerDeviceTime(device.last_seen_at) }}</dd></div>
            <div><dt class="text-subtle text-xs">闲置到期</dt><dd class="m-0 mt-1">{{ playerDeviceTime(device.idle_expires_at) }}</dd></div>
            <div><dt class="text-subtle text-xs">最长有效期</dt><dd class="m-0 mt-1">{{ playerDeviceTime(device.absolute_expires_at) }}</dd></div>
          </dl>
          <button v-if="auth.can(Permissions.ConnectionsUpdate)" type="button" class="btn-danger mt-4" :disabled="revokingDeviceID !== null" @click="revokeDevice(device)">{{ revokingDeviceID === device.id ? '正在撤销…' : '撤销配对' }}</button>
        </article>
      </div>
    </section>
  </section>
</template>
