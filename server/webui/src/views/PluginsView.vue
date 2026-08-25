<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import QRCode from 'qrcode'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import PluginSettingsForm from '@/components/PluginSettingsForm.vue'
import SecretInput from '@/components/SecretInput.vue'
import { credentialLoader } from '@/credentials'
import {
  buildPluginRepositoryCreatePayload,
  buildPluginRepositoryDeletePayload,
  buildPluginRepositoryOrderPayload,
  buildPluginRepositoryTogglePayload,
  buildPluginInstallConfirmPayload,
  buildPluginInstallPreviewPayload,
  buildPluginConnectionCreatePayload,
  buildPluginConnectionCreatePayloadFromConfig,
  buildPluginConnectionConfigPayload,
  buildPluginConnectionQRCodePayload,
  buildPluginConnectionDeletePayload,
  buildPluginConnectionTogglePayload,
  buildPluginRevisionPayload,
  compatibilityLabel,
  installedPluginsPath,
  pluginMarketplacePath,
  pluginInstallConfirmPath,
  pluginInstallPreviewPath,
  pluginLifecyclePath,
  pluginConnectionPath,
  pluginConnectionAuthPath,
  pluginConnectionsPath,
  pluginLogsPath,
  pluginRepositoryListPath,
  pluginRepositoryPath,
  pluginRepositoryRefreshPath,
  pluginUninstallPath,
  permissionDetails,
  permissionLabel,
  pluginQRCodeAuthScope,
  normalizePluginInstallPreview,
  normalizeInstalledPluginSummary,
  pluginHasMarketplaceUpdate,
  pluginMarketplaceAction,
  selectedMarketplaceSource,
  type InstalledPluginsResponse,
  type InstalledPluginSummary,
  type PluginConnectionSummary,
  type PluginAuthPollSummary,
  type PluginAuthStartSummary,
  type PluginQRCodeAuthState,
  type PluginCredentialMode,
  type PluginInstallPreview,
  type PluginMarketplaceEntry,
  type PluginMarketplaceResponse,
  type PluginRepositorySummary,
  type PluginTab,
} from '@/plugins'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import type { ListResponse } from '@/types/api'

const auth = useAuthStore()
const activeTab = ref<PluginTab>('marketplace')
const repositories = ref<PluginRepositorySummary[]>([])
const marketplace = ref<PluginMarketplaceEntry[]>([])
const installed = ref<InstalledPluginSummary[]>([])
const serverVersion = ref('')
const repositoriesLoading = ref(true)
const marketplaceLoading = ref(true)
const installedLoading = ref(true)
const repositoriesError = ref('')
const marketplaceError = ref('')
const installedError = ref('')
const githubURL = ref('')
const repositoryName = ref('')
const creating = ref(false)
const busyID = ref<number | null>(null)
const reordering = ref(false)
const pluginBusyID = ref('')
const installPreview = ref<PluginInstallPreview | null>(null)
const installConfirmed = ref(false)
const installDialog = ref<HTMLElement | null>(null)
const expandedPluginID = ref('')
const connectionsByPlugin = ref<Record<string, PluginConnectionSummary[]>>({})
const connectionsLoadingID = ref('')
const connectionBusyID = ref('')
const connectionName = ref('')
const connectionConfigText = ref('{\n  "homeRecommendationEnabled": true\n}')
const connectionConfig = ref<Record<string, unknown>>({})
const editingConnectionID = ref('')
const connectionEditConfig = ref<Record<string, unknown>>({})
const connectionEditCredential = ref('')
const connectionCredentialMode = ref<PluginCredentialMode>('none')
const connectionCredentialScope = ref('')
const connectionCredential = ref('')
const connectionAuth = ref<Record<string, PluginQRCodeAuthState>>({})
const authPollTimers = new Map<string, number>()
let installDialogReturnFocus: HTMLElement | null = null

const canManage = computed(() => auth.can(Permissions.PluginsInstall))

async function loadRepositories() {
  repositoriesLoading.value = true
  repositoriesError.value = ''
  try {
    const response = await api<ListResponse<PluginRepositorySummary>>(pluginRepositoryListPath)
    repositories.value = response.list
  } catch (reason) {
    repositoriesError.value = message(reason)
  } finally {
    repositoriesLoading.value = false
  }
}

async function loadMarketplace() {
  marketplaceLoading.value = true
  marketplaceError.value = ''
  try {
    const response = await api<PluginMarketplaceResponse>(pluginMarketplacePath)
    marketplace.value = response.list
    serverVersion.value = response.server_version
  } catch (reason) {
    marketplaceError.value = message(reason)
  } finally {
    marketplaceLoading.value = false
  }
}

async function loadInstalled() {
  installedLoading.value = true
  installedError.value = ''
  try {
    const response = await api<InstalledPluginsResponse>(installedPluginsPath)
    installed.value = Array.isArray(response.list) ? response.list.map(normalizeInstalledPluginSummary) : []
  } catch (reason) {
    installedError.value = message(reason)
  } finally {
    installedLoading.value = false
  }
}

async function loadAll() {
  await Promise.all([loadRepositories(), loadMarketplace(), loadInstalled()])
}

async function loadConnections(plugin: InstalledPluginSummary) {
  connectionsLoadingID.value = plugin.id
  try {
    const response = await api<ListResponse<PluginConnectionSummary>>(pluginConnectionsPath(plugin.id))
    connectionsByPlugin.value = { ...connectionsByPlugin.value, [plugin.id]: response.list }
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    connectionsLoadingID.value = ''
  }
}

async function toggleConnectionPanel(plugin: InstalledPluginSummary) {
  if (expandedPluginID.value === plugin.id) {
    expandedPluginID.value = ''
    return
  }
  expandedPluginID.value = plugin.id
  connectionName.value = `${plugin.name} 连接`
  connectionConfig.value = { ...plugin.config_defaults }
  connectionConfigText.value = JSON.stringify(plugin.config_defaults ?? {}, null, 2)
  const qrAuthScope = pluginQRCodeAuthScope(plugin)
  connectionCredentialMode.value = qrAuthScope ? 'cookie' : 'none'
  connectionCredentialScope.value = qrAuthScope ?? credentialScopes(plugin)[0] ?? ''
  connectionCredential.value = ''
  await loadConnections(plugin)
}

async function createConnection(plugin: InstalledPluginSummary) {
  connectionBusyID.value = `new:${plugin.id}`
  try {
    const payload = plugin.settings_page
      ? buildPluginConnectionCreatePayloadFromConfig({ name: connectionName.value, config: connectionConfig.value, credentialScope: connectionCredentialScope.value, credentialMode: connectionCredentialMode.value, credential: connectionCredential.value })
      : buildPluginConnectionCreatePayload({ name: connectionName.value, configText: connectionConfigText.value, credentialScope: connectionCredentialScope.value, credentialMode: connectionCredentialMode.value, credential: connectionCredential.value })
    const connection = await api<PluginConnectionSummary>(pluginConnectionsPath(plugin.id), { method: 'POST', body: JSON.stringify(payload) })
    connectionCredential.value = ''
    await loadConnections(plugin)
    if (pluginQRCodeAuthScope(plugin)) {
      notify(`插件连接已创建，请使用 ${plugin.name} 客户端扫码登录`, 'success')
      await startConnectionAuth(plugin, connection)
    } else {
      notify('插件连接已创建；Player 将通过 Server 在线媒体库读取此来源', 'success')
    }
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    connectionBusyID.value = ''
  }
}

function beginEditConnection(connection: PluginConnectionSummary) {
  editingConnectionID.value = connection.id
  connectionEditConfig.value = { ...connection.config }
  connectionEditCredential.value = ''
}

function cancelEditConnection() {
  editingConnectionID.value = ''
  connectionEditCredential.value = ''
}

async function saveConnectionConfig(plugin: InstalledPluginSummary, connection: PluginConnectionSummary) {
  connectionBusyID.value = connection.id
  try {
    await api(pluginConnectionPath(plugin.id, connection.id), { method: 'PATCH', body: JSON.stringify(buildPluginConnectionConfigPayload(connection, connectionEditConfig.value, connectionEditCredential.value)) })
    notify('插件设置已保存', 'success')
    cancelEditConnection()
    await loadConnections(plugin)
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    connectionBusyID.value = ''
  }
}

async function setConnectionEnabled(plugin: InstalledPluginSummary, connection: PluginConnectionSummary, enabled: boolean) {
  connectionBusyID.value = connection.id
  try {
    await api(pluginConnectionPath(plugin.id, connection.id), { method: 'PATCH', body: JSON.stringify(buildPluginConnectionTogglePayload(connection, enabled)) })
    notify(enabled ? '插件连接已启用' : '插件连接已停用', 'success')
    await loadConnections(plugin)
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    connectionBusyID.value = ''
  }
}

async function deleteConnection(plugin: InstalledPluginSummary, connection: PluginConnectionSummary) {
  if (!window.confirm(`确认删除插件连接“${connection.name}”？远端账号与内容不会被删除。`)) return
  connectionBusyID.value = connection.id
  try {
    await api(pluginConnectionPath(plugin.id, connection.id), { method: 'DELETE', body: JSON.stringify(buildPluginConnectionDeletePayload(connection)) })
    notify('插件连接已删除', 'success')
    await loadConnections(plugin)
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    connectionBusyID.value = ''
  }
}

async function startConnectionAuth(plugin: InstalledPluginSummary, connection: PluginConnectionSummary) {
  connectionBusyID.value = `auth:${connection.id}`
  try {
    const qrAuthScope = pluginQRCodeAuthScope(plugin)
    if (!qrAuthScope) throw new Error('此插件没有声明可用的扫码登录能力。')
    let authConnection = connection
    if (connection.credential_mode !== 'cookie' || connection.credential_scope !== qrAuthScope) {
      authConnection = await api<PluginConnectionSummary>(pluginConnectionPath(plugin.id, connection.id), {
        method: 'PATCH',
        body: JSON.stringify(buildPluginConnectionQRCodePayload(connection, qrAuthScope)),
      })
      await loadConnections(plugin)
    }
    const response = await api<PluginAuthStartSummary>(pluginConnectionAuthPath(plugin.id, authConnection.id, 'start'), { method: 'POST', body: '{}' })
    const qrDataURL = await QRCode.toDataURL(response.qrCodeUrl, { width: 220, margin: 1, errorCorrectionLevel: 'M' })
    connectionAuth.value = { ...connectionAuth.value, [connection.id]: { loginSession: response.loginSession, qrDataURL, expiresAt: response.expiresAt, state: 'pending' } }
    scheduleAuthPoll(plugin, authConnection, response.pollAfterSeconds)
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    connectionBusyID.value = ''
  }
}

function scheduleAuthPoll(plugin: InstalledPluginSummary, connection: PluginConnectionSummary, delaySeconds: number) {
  const existing = authPollTimers.get(connection.id)
  if (existing !== undefined) window.clearTimeout(existing)
  const timer = window.setTimeout(() => { void pollConnectionAuth(plugin, connection) }, Math.max(1, Math.min(delaySeconds, 30)) * 1000)
  authPollTimers.set(connection.id, timer)
}

async function pollConnectionAuth(plugin: InstalledPluginSummary, connection: PluginConnectionSummary) {
  const current = connectionAuth.value[connection.id]
  if (!current) return
  try {
    const response = await api<PluginAuthPollSummary>(pluginConnectionAuthPath(plugin.id, connection.id, 'poll'), { method: 'POST', body: JSON.stringify({ login_session: current.loginSession }) })
    connectionAuth.value = { ...connectionAuth.value, [connection.id]: { ...current, state: response.state, accountName: response.account?.name } }
    if (response.state === 'pending' || response.state === 'scanned') scheduleAuthPoll(plugin, connection, response.pollAfterSeconds ?? 2)
    else {
      authPollTimers.delete(connection.id)
      if (response.authenticated) {
        notify(`已登录${response.account?.name ? `：${response.account.name}` : ''}`, 'success')
        await loadConnections(plugin)
      } else notify('二维码已过期，请重新生成', 'warning')
    }
  } catch (reason) {
    authPollTimers.delete(connection.id)
    notify(message(reason), 'error')
  }
}

function connectionHealthLabel(connection: PluginConnectionSummary) {
  if (connection.health_status === 'healthy') return '连接正常'
  if (connection.health_status === 'auth_pending') return '等待扫码'
  if (connection.health_status === 'auth_expired') return '登录已过期'
  if (connection.health_status === 'error') return '连接异常'
  return connection.enabled ? '待检测' : '已停用'
}

function credentialScopes(plugin: InstalledPluginSummary) {
  return plugin.permissions.filter(permission => permission.kind === 'credential.use').flatMap(permission => permission.scopes ?? [])
}

onBeforeUnmount(() => {
  for (const timer of authPollTimers.values()) window.clearTimeout(timer)
  authPollTimers.clear()
})

async function createRepository() {
  creating.value = true
  try {
    const created = await api<PluginRepositorySummary>(pluginRepositoryListPath, {
      method: 'POST',
      body: JSON.stringify(buildPluginRepositoryCreatePayload(githubURL.value, repositoryName.value)),
    })
    githubURL.value = ''
    repositoryName.value = ''
    notify('插件仓库已添加，正在读取固定提交上的 Registry', 'success')
    try {
      await api(pluginRepositoryRefreshPath(created.id), { method: 'POST', body: '{}' })
      notify('插件仓库刷新成功', 'success')
    } catch (reason) {
      notify(`${message(reason)}；仓库配置已保留`, 'warning')
    }
    await Promise.all([loadRepositories(), loadMarketplace()])
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    creating.value = false
  }
}

async function toggleRepository(repository: PluginRepositorySummary, enabled: boolean) {
  busyID.value = repository.id
  try {
    await api(pluginRepositoryPath(repository.id), {
      method: 'PATCH',
      body: JSON.stringify(buildPluginRepositoryTogglePayload(repository, enabled)),
    })
    notify(enabled ? '插件仓库已启用' : '插件仓库已停用', 'success')
    await Promise.all([loadRepositories(), loadMarketplace()])
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    busyID.value = null
  }
}

async function refreshRepository(repository: PluginRepositorySummary) {
  busyID.value = repository.id
  try {
    await api(pluginRepositoryRefreshPath(repository.id), { method: 'POST', body: '{}' })
    notify('插件仓库刷新成功', 'success')
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    await Promise.all([loadRepositories(), loadMarketplace()])
    busyID.value = null
  }
}

async function moveRepository(index: number, offset: -1 | 1) {
  const target = index + offset
  if (target < 0 || target >= repositories.value.length) return
  const next = [...repositories.value]
  const current = next[index]
  const replacement = next[target]
  if (!current || !replacement) return
  next[index] = replacement
  next[target] = current
  reordering.value = true
  try {
    const response = await api<ListResponse<PluginRepositorySummary>>(`${pluginRepositoryListPath}/order`, {
      method: 'PUT',
      body: JSON.stringify(buildPluginRepositoryOrderPayload(next)),
    })
    repositories.value = response.list
    notify('插件仓库顺序已保存', 'success')
    await loadMarketplace()
  } catch (reason) {
    notify(message(reason), 'error')
    await loadRepositories()
  } finally {
    reordering.value = false
  }
}

async function deleteRepository(repository: PluginRepositorySummary) {
  if (!window.confirm(`确认删除插件仓库“${repository.name}”？已安装插件不会被删除。`)) return
  busyID.value = repository.id
  try {
    await api(pluginRepositoryPath(repository.id), {
      method: 'DELETE',
      body: JSON.stringify(buildPluginRepositoryDeletePayload(repository)),
    })
    notify('插件仓库已删除，已安装插件未受影响', 'success')
    await Promise.all([loadRepositories(), loadMarketplace()])
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    busyID.value = null
  }
}

async function beginInstallPreview(entry: PluginMarketplaceEntry) {
  const payload = buildPluginInstallPreviewPayload(entry)
  if (!payload) {
    notify('该插件没有可用的仓库来源', 'error')
    return
  }
  pluginBusyID.value = entry.id
  try {
    const preview = await api<PluginInstallPreview>(pluginInstallPreviewPath(entry.id), {
      method: 'POST',
      body: JSON.stringify(payload),
    })
    installPreview.value = normalizePluginInstallPreview(preview)
    installConfirmed.value = false
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    pluginBusyID.value = ''
  }
}

async function confirmInstallation() {
  const preview = installPreview.value
  if (!preview || !installConfirmed.value) return
  pluginBusyID.value = preview.plugin_id
  try {
    await api(pluginInstallConfirmPath(preview), {
      method: 'POST',
      body: JSON.stringify(buildPluginInstallConfirmPayload(preview)),
    })
    notify(preview.operation === 'update' ? '插件升级完成' : '插件安装完成；请在已安装页面启用', 'success')
    closeInstallPreview()
    await Promise.all([loadInstalled(), loadMarketplace()])
    activeTab.value = 'installed'
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    pluginBusyID.value = ''
  }
}

function closeInstallPreview() {
  if (pluginBusyID.value) return
  installPreview.value = null
  installConfirmed.value = false
}

async function setPluginEnabled(plugin: InstalledPluginSummary, enabled: boolean) {
  pluginBusyID.value = plugin.id
  try {
    await api(pluginLifecyclePath(plugin.id, enabled ? 'enable' : 'disable'), {
      method: 'POST',
      body: JSON.stringify(buildPluginRevisionPayload(plugin)),
    })
    notify(enabled ? '插件已启用' : '插件已停用', 'success')
    await Promise.all([loadInstalled(), loadMarketplace()])
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    pluginBusyID.value = ''
  }
}

async function rollbackPlugin(plugin: InstalledPluginSummary) {
  if (!window.confirm(`确认把“${plugin.name}”从 v${plugin.version} 回滚到 v${plugin.previous_version}？`)) return
  pluginBusyID.value = plugin.id
  try {
    await api(pluginLifecyclePath(plugin.id, 'rollback'), {
      method: 'POST',
      body: JSON.stringify(buildPluginRevisionPayload(plugin)),
    })
    notify('插件已回滚', 'success')
    await Promise.all([loadInstalled(), loadMarketplace()])
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    pluginBusyID.value = ''
  }
}

async function uninstallPlugin(plugin: InstalledPluginSummary) {
  if (!window.confirm(`确认卸载插件“${plugin.name}”？插件定义和授权会被删除，此操作不会删除插件连接的远端数据。`)) return
  pluginBusyID.value = plugin.id
  try {
    await api(pluginUninstallPath(plugin.id), {
      method: 'DELETE',
      body: JSON.stringify(buildPluginRevisionPayload(plugin)),
    })
    notify('插件已卸载', 'success')
    await Promise.all([loadInstalled(), loadMarketplace()])
  } catch (reason) {
    notify(message(reason), 'error')
  } finally {
    pluginBusyID.value = ''
  }
}

function updateInstalledPlugin(plugin: InstalledPluginSummary) {
  const entry = marketplace.value.find(candidate => candidate.id === plugin.id)
  if (!entry || entry.version === plugin.version) {
    notify('当前仓库中没有可用的新版本', 'warning')
    return
  }
  void beginInstallPreview(entry)
}

function repositoryState(repository: PluginRepositorySummary) {
  if (!repository.enabled) return { label: '已停用', className: 'status-chip' }
  if (repository.last_error_code) return { label: '刷新异常', className: 'status-chip status-chip--error' }
  if (repository.cache_valid) return { label: '可用', className: 'status-chip status-chip--ready' }
  return { label: '等待刷新', className: 'status-chip status-chip--warning' }
}

function compatibilityClass(entry: PluginMarketplaceEntry) {
  return entry.compatibility === 'compatible' ? 'status-chip status-chip--ready' : 'status-chip status-chip--warning'
}

function installationState(plugin: InstalledPluginSummary) {
  if (plugin.status === 'enabled') return { label: '运行中', className: 'status-chip status-chip--ready' }
  if (plugin.status === 'failed') return { label: '运行异常', className: 'status-chip status-chip--error' }
  return { label: '已停用', className: 'status-chip' }
}

function marketplaceAction(entry: PluginMarketplaceEntry) {
  return pluginMarketplaceAction(entry)
}

function hasMarketplaceUpdate(plugin: InstalledPluginSummary) {
  return pluginHasMarketplaceUpdate(plugin.id, marketplace.value)
}

function handleInstallDialogKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeInstallPreview()
    return
  }
  if (event.key !== 'Tab' || !installDialog.value) return
  const focusable = Array.from(installDialog.value.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'))
  if (focusable.length === 0) {
    event.preventDefault()
    installDialog.value.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last?.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first?.focus()
  }
}

function formatTime(value: string | null) {
  return value ? new Date(value).toLocaleString() : '尚未成功刷新'
}

function shortCommit(value: string) {
  return value ? value.slice(0, 12) : '—'
}

function message(reason: unknown) {
  return reason instanceof Error ? reason.message : '操作失败'
}

watch(installPreview, async (preview) => {
  if (preview) {
    installDialogReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    await nextTick()
    installDialog.value?.focus()
  } else {
    installDialogReturnFocus?.focus()
    installDialogReturnFocus = null
  }
})

onMounted(() => { void loadAll() })
</script>

<template>
  <section class="mx-auto max-w-7xl">
    <div>
      <h1 class="m-0 text-3xl font-800">插件</h1>
      <p class="page-description mb-0 mt-2 max-w-3xl">插件只安装和运行在 Server。GitHub 仓库用于发现版本，Server 会把 Registry 固定到提交 SHA，并在安装前校验 Manifest、包摘要、WASM 沙箱和精确权限。</p>
    </div>

    <nav class="management-tabs mt-6" aria-label="插件页面">
      <button type="button" class="management-tab" :class="{ 'management-tab--active': activeTab === 'installed' }" @click="activeTab = 'installed'">已安装</button>
      <button type="button" class="management-tab" :class="{ 'management-tab--active': activeTab === 'marketplace' }" @click="activeTab = 'marketplace'">插件市场</button>
      <button type="button" class="management-tab" :class="{ 'management-tab--active': activeTab === 'repositories' }" @click="activeTab = 'repositories'">仓库设置</button>
    </nav>

    <section v-if="activeTab === 'installed'" class="mt-6">
      <p v-if="installedLoading" class="text-subtle">正在读取已安装插件…</p>
      <div v-else-if="installedError" class="semantic-error p-4 text-sm">{{ installedError }}</div>
      <div v-else-if="installed.length === 0" class="panel">
        <h2 class="m-0 text-lg">尚未安装插件</h2>
        <p class="page-description mb-0 mt-2 text-sm">前往插件市场选择插件。安装前 Server 会校验来源、摘要、兼容性、WASM 沙箱和权限。</p>
        <button type="button" class="btn-primary mt-4" @click="activeTab = 'marketplace'">前往插件市场</button>
      </div>
      <div v-else class="grid gap-5 lg:grid-cols-2">
        <article v-for="plugin in installed" :key="plugin.id" class="panel">
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <h2 class="m-0 truncate text-xl">{{ plugin.name }}</h2>
              <p class="text-subtle mb-0 mt-1 truncate font-mono text-xs">{{ plugin.id }}</p>
            </div>
            <span :class="installationState(plugin).className">{{ installationState(plugin).label }}</span>
          </div>
          <p class="page-description mb-0 mt-4 text-sm">{{ plugin.description }}</p>
          <dl class="mt-5 grid gap-3 text-sm sm:grid-cols-2">
            <div><dt class="text-subtle text-xs">当前版本</dt><dd class="m-0 mt-1">v{{ plugin.version }}</dd></div>
            <div><dt class="text-subtle text-xs">仓库来源</dt><dd class="m-0 mt-1">{{ plugin.repository_name || '原仓库已移除' }}</dd></div>
            <div><dt class="text-subtle text-xs">运行代次</dt><dd class="m-0 mt-1 font-mono">{{ plugin.runtime_generation }}</dd></div>
            <div><dt class="text-subtle text-xs">可回滚版本</dt><dd class="m-0 mt-1">{{ plugin.previous_version ? `v${plugin.previous_version}` : '—' }}</dd></div>
          </dl>
          <div v-if="plugin.last_runtime_error_code" class="semantic-error mt-4 p-3 text-xs">运行错误：<span class="font-mono">{{ plugin.last_runtime_error_code }}</span></div>
          <div class="mt-4 flex flex-wrap gap-2">
            <span v-for="capability in plugin.capabilities" :key="capability" class="status-chip">{{ capability }}</span>
          </div>
          <div class="semantic-inset mt-4 grid gap-2 p-3 text-xs">
            <strong>已授权权限</strong>
            <div v-for="permission in plugin.permissions" :key="`${permission.kind}:${permissionDetails(permission)}`" class="flex flex-wrap justify-between gap-2">
              <span>{{ permissionLabel(permission.kind) }}</span><span class="text-subtle">{{ permissionDetails(permission) }}</span>
            </div>
            <span v-if="plugin.permissions.length === 0" class="text-subtle">此插件没有 Host 权限。</span>
          </div>
          <div v-if="canManage" class="mt-5 flex flex-wrap gap-2">
            <button v-if="plugin.status === 'enabled'" type="button" class="btn-secondary" :disabled="pluginBusyID !== ''" @click="setPluginEnabled(plugin, false)">停用</button>
            <button v-else type="button" class="btn-primary" :disabled="pluginBusyID !== ''" @click="setPluginEnabled(plugin, true)">启用</button>
            <button type="button" class="btn-secondary" :disabled="pluginBusyID !== '' || plugin.status !== 'enabled'" :aria-expanded="expandedPluginID === plugin.id" @click="toggleConnectionPanel(plugin)">{{ expandedPluginID === plugin.id ? '收起连接' : '连接与在线库' }}</button>
            <button type="button" class="btn-secondary" :disabled="pluginBusyID !== '' || !hasMarketplaceUpdate(plugin)" @click="updateInstalledPlugin(plugin)">{{ hasMarketplaceUpdate(plugin) ? '校验升级包' : '已是仓库最新版' }}</button>
            <button type="button" class="btn-secondary" :disabled="pluginBusyID !== '' || !plugin.previous_version" @click="rollbackPlugin(plugin)">回滚</button>
            <RouterLink class="btn-secondary" :to="pluginLogsPath(plugin.id)">查看日志</RouterLink>
            <button type="button" class="btn-danger" :disabled="pluginBusyID !== ''" @click="uninstallPlugin(plugin)">卸载</button>
          </div>
          <section v-if="expandedPluginID === plugin.id" class="semantic-inset mt-5 grid gap-4 p-4" :aria-label="`${plugin.name} 连接配置`">
            <div>
              <h3 class="m-0 text-base">连接与在线媒体库</h3>
              <p class="text-subtle mb-0 mt-1 text-xs">一个插件可创建多个相互隔离的账号/匿名连接。普通配置可见，Cookie、Token 等凭据仅加密保存在 Server。</p>
            </div>
            <p v-if="connectionsLoadingID === plugin.id" class="text-subtle m-0 text-sm">正在读取连接…</p>
            <div v-else-if="(connectionsByPlugin[plugin.id] ?? []).length" class="grid gap-2">
              <article v-for="connection in connectionsByPlugin[plugin.id]" :key="connection.id" class="panel p-3">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <strong class="text-sm">{{ connection.name }}</strong>
                    <p class="text-subtle m-0 mt-1 text-xs">{{ connection.credential_configured ? `${connection.credential_mode} 凭据已安全配置` : '匿名连接' }}</p>
                  </div>
                  <div class="flex flex-wrap gap-2"><span :class="connection.enabled ? 'status-chip status-chip--ready' : 'status-chip'">{{ connection.enabled ? '已启用' : '已停用' }}</span><span :class="connection.health_status === 'error' || connection.health_status === 'auth_expired' ? 'status-chip status-chip--warning' : 'status-chip'">{{ connectionHealthLabel(connection) }}</span></div>
                </div>
                <p v-if="connection.health_error_code" class="semantic-warning mt-3 p-2 text-xs">健康错误：<span class="font-mono">{{ connection.health_error_code }}</span></p>
                <PluginSettingsForm
                  v-if="plugin.settings_page && editingConnectionID === connection.id"
                  v-model="connectionEditConfig"
                  class="mt-3"
                  :page="plugin.settings_page"
                  :credential-configured="connection.credential_configured"
                  :health-status="connection.health_status"
                  :qr-auth-state="canManage ? connectionAuth[connection.id] : undefined"
                  :qr-auth-action-visible="canManage && Boolean(pluginQRCodeAuthScope(plugin))"
                  :qr-auth-action-disabled="connectionBusyID !== '' || !connection.enabled"
                  @start-auth="startConnectionAuth(plugin, connection)"
                />
                <PluginSettingsForm
                  v-else-if="plugin.settings_page"
                  :model-value="connection.config"
                  class="mt-3"
                  :page="plugin.settings_page"
                  :credential-configured="connection.credential_configured"
                  :health-status="connection.health_status"
                  :qr-auth-state="canManage ? connectionAuth[connection.id] : undefined"
                  :qr-auth-action-visible="canManage && Boolean(pluginQRCodeAuthScope(plugin))"
                  :qr-auth-action-disabled="connectionBusyID !== '' || !connection.enabled"
                  disabled
                  @start-auth="startConnectionAuth(plugin, connection)"
                />
                <div v-if="editingConnectionID === connection.id && connection.credential_mode !== 'none'" class="mt-3">
                  <label class="label">更换凭据（留空保留）</label>
                  <SecretInput
                    v-model="connectionEditCredential"
                    class="input min-h-20 font-mono text-xs"
                    multiline
                    :configured="connection.credential_configured"
                    :load-secret="auth.can(Permissions.ConnectionsSecretsExport) ? credentialLoader({ resourceType: 'plugin_connection', resourceID: connection.id, field: 'credential' }) : undefined"
                    :reset-key="connection.id"
                    autocomplete="off"
                    spellcheck="false"
                  />
                </div>
                <div v-if="canManage" class="mt-3 flex flex-wrap gap-2">
                  <template v-if="plugin.settings_page">
                    <button v-if="editingConnectionID !== connection.id" type="button" class="btn-secondary" :disabled="connectionBusyID !== ''" @click="beginEditConnection(connection)">编辑设置</button>
                    <button v-else type="button" class="btn-primary" :disabled="connectionBusyID !== ''" @click="saveConnectionConfig(plugin, connection)">保存设置</button>
                    <button v-if="editingConnectionID === connection.id" type="button" class="btn-secondary" :disabled="connectionBusyID !== ''" @click="cancelEditConnection">取消</button>
                  </template>
                  <button type="button" class="btn-secondary" :disabled="connectionBusyID !== ''" @click="setConnectionEnabled(plugin, connection, !connection.enabled)">{{ connection.enabled ? '停用' : '启用' }}</button>
                  <button type="button" class="btn-danger" :disabled="connectionBusyID !== ''" @click="deleteConnection(plugin, connection)">删除</button>
                </div>
              </article>
            </div>
            <p v-else class="text-subtle m-0 text-sm">尚未创建连接。无需登录即可使用的插件可直接创建匿名连接。</p>

            <form v-if="canManage" class="grid gap-3" @submit.prevent="createConnection(plugin)">
              <div><label class="label">连接名称</label><input v-model="connectionName" class="input" maxlength="128" required /></div>
              <PluginSettingsForm v-if="plugin.settings_page" v-model="connectionConfig" :page="plugin.settings_page" />
              <div v-else><label class="label">普通配置（JSON）</label><textarea v-model="connectionConfigText" class="input min-h-28 font-mono text-xs" spellcheck="false" /></div>
              <p v-if="pluginQRCodeAuthScope(plugin)" class="semantic-inset m-0 p-3 text-sm">创建连接后会立即显示 {{ plugin.name }} 登录二维码，无需手动填写 Cookie。</p>
              <div v-else class="grid gap-3 sm:grid-cols-2">
                <div><label class="label">认证方式</label><select v-model="connectionCredentialMode" class="input"><option value="none">匿名 / 不使用凭据</option><option v-if="credentialScopes(plugin).length" value="cookie">Cookie</option><option v-if="credentialScopes(plugin).length" value="bearer">Bearer Token</option></select></div>
                <div v-if="connectionCredentialMode !== 'none'"><label class="label">凭据范围</label><select v-model="connectionCredentialScope" class="input" required><option v-for="scope in credentialScopes(plugin)" :key="scope" :value="scope">{{ scope }}</option></select></div>
              </div>
              <div v-if="!pluginQRCodeAuthScope(plugin) && connectionCredentialMode !== 'none'"><label class="label">凭据{{ connectionCredentialMode === 'cookie' ? '（可留空后扫码登录）' : '' }}</label><SecretInput v-model="connectionCredential" class="input min-h-20 font-mono text-xs" multiline :required="connectionCredentialMode === 'bearer'" autocomplete="off" spellcheck="false" /><p class="text-subtle mb-0 mt-1 text-xs">手动填写的凭据保存后不会再次回显明文，日志、普通 API 和 Player 都不会收到它。</p></div>
              <button class="btn-primary" :disabled="connectionBusyID !== '' || !connectionName.trim()">{{ connectionBusyID === `new:${plugin.id}` ? '正在创建…' : pluginQRCodeAuthScope(plugin) ? '创建连接并扫码登录' : '创建连接' }}</button>
            </form>
          </section>
        </article>
      </div>
    </section>

    <section v-else-if="activeTab === 'marketplace'" class="mt-6">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <p class="page-description m-0 text-sm">Server {{ serverVersion || '—' }} · 仅展示已通过 Registry v1 校验的缓存条目</p>
        <button type="button" class="btn-secondary" :disabled="marketplaceLoading" @click="loadMarketplace">{{ marketplaceLoading ? '刷新中…' : '刷新市场视图' }}</button>
      </div>
      <p v-if="marketplaceLoading" class="text-subtle">正在合并已启用仓库…</p>
      <div v-else-if="marketplaceError" class="semantic-error p-4 text-sm">{{ marketplaceError }}</div>
      <div v-else-if="marketplace.length === 0" class="panel">
        <h2 class="m-0 text-lg">插件市场为空</h2>
        <p class="page-description mb-0 mt-2 text-sm">请在“仓库设置”添加 GitHub 仓库主页地址并完成刷新。</p>
        <button type="button" class="btn-primary mt-4" @click="activeTab = 'repositories'">前往仓库设置</button>
      </div>
      <div v-else class="grid gap-5 lg:grid-cols-2">
        <article v-for="entry in marketplace" :key="entry.id" class="panel">
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <h2 class="m-0 truncate text-xl">{{ entry.name }}</h2>
              <p class="text-subtle mb-0 mt-1 truncate font-mono text-xs">{{ entry.id }}</p>
            </div>
            <span :class="compatibilityClass(entry)">{{ compatibilityLabel(entry.compatibility) }}</span>
          </div>
          <p class="page-description mb-0 mt-4 text-sm">{{ entry.description }}</p>
          <div class="mt-4 flex flex-wrap gap-2">
            <span class="status-chip">v{{ entry.version }}</span>
            <span :class="entry.channel === 'stable' ? 'status-chip status-chip--ready' : 'status-chip status-chip--warning'">{{ entry.channel === 'stable' ? 'Stable' : 'Beta' }}</span>
            <span v-for="category in entry.categories" :key="category" class="status-chip">{{ category }}</span>
          </div>
          <dl class="mt-5 grid gap-3 text-sm sm:grid-cols-2">
            <div><dt class="text-subtle text-xs">当前来源</dt><dd class="m-0 mt-1">{{ selectedMarketplaceSource(entry)?.repository_name || '未知' }}</dd></div>
            <div><dt class="text-subtle text-xs">Server 范围</dt><dd class="m-0 mt-1">{{ entry.min_server_version }}{{ entry.max_server_version ? ` — ${entry.max_server_version}` : ' 起' }}</dd></div>
          </dl>
          <div v-if="entry.source_conflict" class="semantic-warning mt-4 p-3 text-xs">发现 {{ entry.sources.length }} 个仓库提供同一插件 ID；当前按仓库顺序选择“{{ selectedMarketplaceSource(entry)?.repository_name }}”，未静默覆盖其它来源。</div>
          <div class="semantic-inset mt-4 p-3 text-xs">
            <strong class="block">权限与安装</strong>
            <span class="text-subtle mt-1 block">点击后先下载并校验独立 Manifest 与插件包，再展示精确权限和升级权限差异；确认前不会安装或替换运行版本。</span>
          </div>
          <div v-if="!entry.permissions_available" class="semantic-warning mt-4 p-3 text-xs">当前 Server 未配置可用的插件隔离目录或 WASM 运行时，暂时不能安装和运行插件。</div>
          <button
            v-if="canManage"
            type="button"
            class="btn-primary mt-4 w-full"
            :disabled="pluginBusyID !== '' || entry.compatibility !== 'compatible' || marketplaceAction(entry).disabled"
            @click="beginInstallPreview(entry)"
          >{{ pluginBusyID === entry.id ? '正在下载并校验…' : marketplaceAction(entry).label }}</button>
        </article>
      </div>
    </section>

    <section v-else class="mt-6">
      <form v-if="canManage" class="panel grid gap-4 md:grid-cols-2" @submit.prevent="createRepository">
        <div class="md:col-span-2">
          <h2 class="m-0 text-lg">添加 GitHub 插件仓库</h2>
          <p class="page-description mb-0 mt-2 text-sm">填写仓库主页地址，例如 https://github.com/owner/repo。Raw 地址、分支路径、查询参数和带凭据地址都会被拒绝。</p>
        </div>
        <div><label class="label">GitHub 仓库地址</label><input v-model="githubURL" class="input font-mono" required type="url" placeholder="https://github.com/owner/repo" autocomplete="off" spellcheck="false" /></div>
        <div><label class="label">显示名称（可选）</label><input v-model="repositoryName" class="input" maxlength="128" placeholder="留空使用 owner/repo" /></div>
        <button class="btn-primary md:col-span-2" :disabled="creating || !githubURL.trim()">{{ creating ? '添加并刷新中…' : '添加仓库' }}</button>
      </form>
      <div v-else class="semantic-warning p-4 text-sm">当前账户只能查看插件仓库，没有添加、刷新、排序或删除权限。</div>

      <p v-if="repositoriesLoading" class="text-subtle mt-6">正在读取插件仓库…</p>
      <div v-else-if="repositoriesError" class="semantic-error mt-6 p-4 text-sm">{{ repositoriesError }}</div>
      <div v-else-if="repositories.length === 0" class="panel mt-6">
        <h2 class="m-0 text-lg">尚未配置仓库</h2>
        <p class="page-description mb-0 mt-2 text-sm">添加后 Server 会通过固定的 api.github.com 流程读取默认分支、提交 SHA 和根目录 Registry。</p>
      </div>
      <div v-else class="mt-6 grid gap-4">
        <article v-for="(repository, index) in repositories" :key="repository.id" class="panel">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2"><h2 class="m-0 text-lg">{{ repository.name }}</h2><span :class="repositoryState(repository).className">{{ repositoryState(repository).label }}</span></div>
              <a class="semantic-link mt-1 block truncate font-mono text-xs" :href="repository.github_url" target="_blank" rel="noopener noreferrer">{{ repository.github_url }}</a>
            </div>
            <div v-if="canManage" class="flex flex-wrap gap-2">
              <button type="button" class="btn-secondary" :disabled="reordering || index === 0" aria-label="上移仓库" @click="moveRepository(index, -1)">上移</button>
              <button type="button" class="btn-secondary" :disabled="reordering || index === repositories.length - 1" aria-label="下移仓库" @click="moveRepository(index, 1)">下移</button>
            </div>
          </div>
          <dl class="mt-5 grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <div><dt class="text-subtle text-xs">固定提交</dt><dd class="m-0 mt-1 font-mono" :title="repository.last_commit_sha">{{ shortCommit(repository.last_commit_sha) }}</dd></div>
            <div><dt class="text-subtle text-xs">最近成功刷新</dt><dd class="m-0 mt-1">{{ formatTime(repository.last_refreshed_at) }}</dd></div>
            <div><dt class="text-subtle text-xs">Registry</dt><dd class="m-0 mt-1">{{ repository.registry_name || '尚无有效缓存' }}</dd></div>
            <div><dt class="text-subtle text-xs">插件条目</dt><dd class="m-0 mt-1">{{ repository.cache_valid ? repository.plugin_count : '—' }}</dd></div>
          </dl>
          <div v-if="repository.last_error_code" class="semantic-warning mt-4 p-3 text-xs">最近刷新错误：<span class="font-mono">{{ repository.last_error_code }}</span>。上次成功缓存和提交 SHA 已保留。</div>
          <div v-if="canManage" class="mt-5 flex flex-wrap items-center gap-3">
            <label class="text-muted flex items-center gap-2 text-sm"><input :checked="repository.enabled" type="checkbox" :disabled="busyID !== null || reordering" @change="toggleRepository(repository, ($event.target as HTMLInputElement).checked)" />启用此仓库</label>
            <button type="button" class="btn-primary" :disabled="busyID !== null || reordering" @click="refreshRepository(repository)">{{ busyID === repository.id ? '处理中…' : '立即刷新' }}</button>
            <button type="button" class="btn-danger" :disabled="busyID !== null || reordering" @click="deleteRepository(repository)">删除仓库</button>
          </div>
        </article>
      </div>
    </section>

    <div v-if="installPreview" class="modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4" @click.self="closeInstallPreview">
      <section ref="installDialog" role="dialog" aria-modal="true" aria-labelledby="plugin-install-title" tabindex="-1" class="panel max-h-[90vh] w-full max-w-2xl overflow-y-auto" @keydown="handleInstallDialogKeydown">
        <div class="flex items-start justify-between gap-4">
          <div>
            <h2 id="plugin-install-title" class="m-0 text-xl">{{ installPreview.operation === 'update' ? '确认升级插件' : '确认安装插件' }}</h2>
            <p class="page-description mb-0 mt-2 text-sm">{{ installPreview.name }} · v{{ installPreview.version }} · {{ installPreview.repository_name }}</p>
          </div>
          <button type="button" class="btn-secondary" :disabled="pluginBusyID !== ''" aria-label="关闭安装确认" @click="closeInstallPreview">关闭</button>
        </div>

        <div v-if="installPreview.permission_diff.added.length" class="semantic-warning mt-5 p-4">
          <strong class="text-sm">{{ installPreview.operation === 'update' ? '本次新增权限' : '安装后授予权限' }}</strong>
          <ul class="mb-0 mt-3 grid gap-2 pl-5 text-sm">
            <li v-for="(permission, index) in installPreview.permission_diff.added" :key="`${permission.kind}-${index}`"><strong>{{ permissionLabel(permission.kind) }}</strong>：{{ permissionDetails(permission) }}</li>
          </ul>
        </div>
        <div v-if="installPreview.permission_diff.removed.length" class="semantic-inset mt-4 p-4">
          <strong class="text-sm">本次移除权限</strong>
          <ul class="mb-0 mt-3 grid gap-2 pl-5 text-sm">
            <li v-for="(permission, index) in installPreview.permission_diff.removed" :key="`${permission.kind}-${index}`"><strong>{{ permissionLabel(permission.kind) }}</strong>：{{ permissionDetails(permission) }}</li>
          </ul>
        </div>
        <div v-if="installPreview.permission_diff.added.length === 0 && installPreview.permission_diff.removed.length === 0" class="semantic-inset mt-5 p-4 text-sm">{{ installPreview.operation === 'update' ? '该升级没有权限变化，原有精确授权将按新包快照重新绑定。' : '该插件不申请 Host 权限。' }}</div>

        <div class="mt-5">
          <strong class="text-sm">插件能力</strong>
          <div class="mt-2 flex flex-wrap gap-2"><span v-for="capability in installPreview.capabilities" :key="capability" class="status-chip">{{ capability }}</span></div>
        </div>
        <p class="semantic-inset mt-5 p-3 text-xs">此确认只对当前已校验包摘要、权限指纹和安装修订有效，并会在 {{ formatTime(installPreview.expires_at) }} 过期。插件在 WASM 沙箱中运行，不直接获得 Server 数据库、系统命令或全局凭据访问权。</p>
        <label class="mt-5 flex items-start gap-3 text-sm">
          <input v-model="installConfirmed" type="checkbox" class="mt-1" />
          <span>我已核对插件来源、版本以及上方权限，并确认执行{{ installPreview.operation === 'update' ? '升级' : '安装' }}。</span>
        </label>
        <div class="mt-5 flex justify-end gap-3">
          <button type="button" class="btn-secondary" :disabled="pluginBusyID !== ''" @click="closeInstallPreview">取消</button>
          <button type="button" class="btn-primary" :disabled="!installConfirmed || pluginBusyID !== ''" @click="confirmInstallation">{{ pluginBusyID ? '处理中…' : installPreview.operation === 'update' ? '确认升级' : '确认安装' }}</button>
        </div>
      </section>
    </div>
  </section>
</template>
