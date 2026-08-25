export type PluginTab = 'installed' | 'marketplace' | 'repositories'

export interface PluginRepositorySummary {
  id: number
  name: string
  github_url: string
  enabled: boolean
  priority: number
  revision: number
  last_commit_sha: string
  last_refreshed_at: string | null
  last_error_code: string
  registry_name: string
  plugin_count: number
  cache_valid: boolean
  created_at: string
  updated_at: string
}

export interface PluginMarketplaceSource {
  repository_id: number
  repository_name: string
  repository_url: string
  priority: number
  version: string
  channel: 'stable' | 'beta'
  selected: boolean
}

export interface PluginMarketplaceEntry {
  id: string
  name: string
  description: string
  version: string
  channel: 'stable' | 'beta'
  categories: string[]
  icon_url?: string
  min_server_version: string
  max_server_version?: string
  release_notes?: string
  compatibility: 'compatible' | 'server_too_old' | 'server_too_new'
  source_conflict: boolean
  sources: PluginMarketplaceSource[]
  permissions_available: boolean
  install_status: 'available' | 'incompatible' | 'installed' | 'update_available'
}

export interface PluginMarketplaceResponse {
  list: PluginMarketplaceEntry[]
  total: number
  server_version: string
}

export interface InstalledPluginsResponse {
  list: InstalledPluginSummary[]
  total: number
  runtime_status: 'available' | 'unavailable'
}

export type PluginInstallationStatus = 'disabled' | 'enabled' | 'failed'

export interface PluginPermission {
  kind: 'network.http' | 'credential.use' | 'storage.private' | 'event.subscribe' | 'download.plan'
  domains?: string[]
  scopes?: string[]
  topics?: string[]
  maxBytes?: number
}

export interface PluginPermissionDiff {
  added: PluginPermission[]
  removed: PluginPermission[]
  unchanged: PluginPermission[]
}

export interface PluginInstallPreview {
  id: string
  plugin_id: string
  name: string
  version: string
  operation: 'install' | 'update'
  repository_id: number
  repository_name: string
  capabilities: string[]
  permissions: PluginPermission[]
  permission_diff: PluginPermissionDiff
  permission_fingerprint: string
  installation_revision: number
  expires_at: string
}

export interface InstalledPluginSummary {
  id: string
  name: string
  description: string
  version: string
  previous_version?: string
  repository_id: number | null
  repository_name: string
  status: PluginInstallationStatus
  revision: number
  runtime_generation: number
  last_runtime_error_code: string
  capabilities: string[]
  permissions: PluginPermission[]
  config_schema: Record<string, unknown>
  config_defaults: Record<string, unknown>
  settings_page?: PluginSettingsPage
  installed_at: string
  updated_at: string
}

export interface PluginSettingsPage {
  version: 1
  tabs: PluginSettingsTab[]
}

export interface PluginSettingsTab {
  id: string
  title: string
  sections: PluginSettingsSection[]
}

export interface PluginSettingsSection {
  id: string
  title: string
  description?: string
  fields: PluginSettingsField[]
}

export interface PluginSettingsField {
  type: 'switch' | 'text' | 'number' | 'select' | 'notice' | 'credential-status'
  key?: string
  label: string
  description?: string
  placeholder?: string
  options?: Array<{ label: string, value: string }>
  minimum?: number
  maximum?: number
}

export type PluginCredentialMode = 'none' | 'cookie' | 'bearer'

export interface PluginConnectionSummary {
  id: string
  plugin_id: string
  name: string
  config: Record<string, unknown>
  credential_scope: string
  credential_mode: PluginCredentialMode
  credential_configured: boolean
  enabled: boolean
  health_status: 'unknown' | 'auth_pending' | 'auth_expired' | 'healthy' | 'error' | string
  health_error_code?: string
  health_checked_at?: string | null
  revision: number
  created_at: string
  updated_at: string
}

export interface PluginAuthStartSummary {
  loginSession: string
  qrCodeUrl: string
  expiresAt: string
  pollAfterSeconds: number
}

export interface PluginAuthPollSummary {
  state: 'pending' | 'scanned' | 'confirmed' | 'expired'
  authenticated: boolean
  account?: { id: string, name: string, avatarUrl?: string }
  pollAfterSeconds?: number
}

export interface PluginQRCodeAuthState {
  loginSession: string
  qrDataURL: string
  expiresAt: string
  state: PluginAuthPollSummary['state']
  accountName?: string
}

export const pluginRepositoryListPath = '/api/v1/plugin-repositories'
export const pluginMarketplacePath = '/api/v1/plugins/marketplace'
export const installedPluginsPath = '/api/v1/plugins/installed'

export function pluginRepositoryPath(id: number) {
  return `${pluginRepositoryListPath}/${id}`
}

export function pluginRepositoryRefreshPath(id: number) {
  return `${pluginRepositoryPath(id)}/refresh`
}

export function pluginInstallPreviewPath(pluginID: string) {
  return `/api/v1/plugins/${encodeURIComponent(pluginID)}/installation-preview`
}

export function pluginInstallConfirmPath(preview: Pick<PluginInstallPreview, 'plugin_id' | 'operation'>) {
  return `/api/v1/plugins/${encodeURIComponent(preview.plugin_id)}/${preview.operation === 'update' ? 'update' : 'install'}`
}

export function pluginLifecyclePath(pluginID: string, operation: 'enable' | 'disable' | 'rollback') {
  return `/api/v1/plugins/${encodeURIComponent(pluginID)}/${operation}`
}

export function pluginUninstallPath(pluginID: string) {
  return `/api/v1/plugins/${encodeURIComponent(pluginID)}`
}

export function pluginConnectionsPath(pluginID: string) {
  return `/api/v1/plugins/${encodeURIComponent(pluginID)}/connections`
}

export function pluginConnectionPath(pluginID: string, connectionID: string) {
  return `${pluginConnectionsPath(pluginID)}/${encodeURIComponent(connectionID)}`
}

export function pluginConnectionAuthPath(pluginID: string, connectionID: string, operation: 'start' | 'poll') {
  return `${pluginConnectionPath(pluginID, connectionID)}/auth/${operation}`
}

export function pluginLogsPath(pluginID: string) {
  return `/logs/runtime?plugin_id=${encodeURIComponent(pluginID)}`
}

export function buildPluginRepositoryCreatePayload(githubURL: string, name = '') {
  return { github_url: githubURL.trim(), name: name.trim(), enabled: true }
}

export function buildPluginRepositoryTogglePayload(repository: PluginRepositorySummary, enabled: boolean) {
  return { enabled, revision: repository.revision }
}

export function buildPluginRepositoryDeletePayload(repository: PluginRepositorySummary) {
  return { revision: repository.revision }
}

export function buildPluginRepositoryOrderPayload(repositories: readonly PluginRepositorySummary[]) {
  return { order: repositories.map(repository => ({ id: repository.id, revision: repository.revision })) }
}

export function buildPluginInstallPreviewPayload(entry: PluginMarketplaceEntry) {
  const source = selectedMarketplaceSource(entry)
  if (!source) return null
  return { repository_id: source.repository_id, version: source.version }
}

export function normalizePluginInstallPreview(preview: PluginInstallPreview) {
  const permissionDiff = preview.permission_diff as PluginPermissionDiff | null | undefined
  return {
    ...preview,
    capabilities: Array.isArray(preview.capabilities) ? preview.capabilities : [],
    permissions: Array.isArray(preview.permissions) ? preview.permissions : [],
    permission_diff: {
      added: Array.isArray(permissionDiff?.added) ? permissionDiff.added : [],
      removed: Array.isArray(permissionDiff?.removed) ? permissionDiff.removed : [],
      unchanged: Array.isArray(permissionDiff?.unchanged) ? permissionDiff.unchanged : [],
    },
  } satisfies PluginInstallPreview
}

export function normalizeInstalledPluginSummary(plugin: InstalledPluginSummary) {
  return {
    ...plugin,
    capabilities: Array.isArray(plugin.capabilities) ? plugin.capabilities : [],
    permissions: Array.isArray(plugin.permissions) ? plugin.permissions : [],
    config_schema: plugin.config_schema && typeof plugin.config_schema === 'object' && !Array.isArray(plugin.config_schema) ? plugin.config_schema : {},
    config_defaults: plugin.config_defaults && typeof plugin.config_defaults === 'object' && !Array.isArray(plugin.config_defaults) ? plugin.config_defaults : {},
  } satisfies InstalledPluginSummary
}

export function buildPluginInstallConfirmPayload(preview: PluginInstallPreview) {
  return {
    preview_id: preview.id,
    permission_fingerprint: preview.permission_fingerprint,
    revision: preview.installation_revision,
  }
}

export function buildPluginRevisionPayload(plugin: InstalledPluginSummary) {
  return { revision: plugin.revision }
}

export function buildPluginConnectionCreatePayload(input: {
  name: string
  configText: string
  credentialScope: string
  credentialMode: PluginCredentialMode
  credential: string
}) {
  const config = JSON.parse(input.configText || '{}') as unknown
  if (!config || typeof config !== 'object' || Array.isArray(config))
    throw new Error('插件连接配置必须是 JSON 对象。')
  return {
    name: input.name.trim(),
    config,
    credential_scope: input.credentialMode === 'none' ? '' : input.credentialScope,
    credential_mode: input.credentialMode,
    credential: input.credentialMode === 'none' ? '' : input.credential,
    enabled: true,
  }
}

export function buildPluginConnectionCreatePayloadFromConfig(input: {
  name: string
  config: Record<string, unknown>
  credentialScope: string
  credentialMode: PluginCredentialMode
  credential: string
}) {
  return {
    name: input.name.trim(),
    config: input.config,
    credential_scope: input.credentialMode === 'none' ? '' : input.credentialScope,
    credential_mode: input.credentialMode,
    credential: input.credentialMode === 'none' ? '' : input.credential,
    enabled: true,
  }
}

export function buildPluginConnectionConfigPayload(connection: PluginConnectionSummary, config: Record<string, unknown>, credential = '') {
  return { config, revision: connection.revision, ...(credential ? { credential } : {}) }
}

export function pluginQRCodeAuthScope(plugin: Pick<InstalledPluginSummary, 'capabilities' | 'permissions' | 'settings_page'>) {
  const exposesCredentialStatus = plugin.settings_page?.tabs.some(tab =>
    tab.sections.some(section => section.fields.some(field => field.type === 'credential-status')),
  ) ?? false
  if (!exposesCredentialStatus || !plugin.capabilities.includes('site.interaction')) return null
  const scopes = new Set(
    plugin.permissions
      .filter(permission => permission.kind === 'credential.use')
      .flatMap(permission => permission.scopes ?? [])
      .map(scope => scope.trim())
      .filter(scope => scope.length > 0),
  )
  if (scopes.size !== 1) return null
  return scopes.values().next().value ?? null
}

export function buildPluginConnectionQRCodePayload(connection: PluginConnectionSummary, credentialScope: string) {
  return { credential_scope: credentialScope, credential_mode: 'cookie' as const, revision: connection.revision }
}

export function buildPluginConnectionTogglePayload(connection: PluginConnectionSummary, enabled: boolean) {
  return { enabled, revision: connection.revision }
}

export function buildPluginConnectionDeletePayload(connection: PluginConnectionSummary) {
  return { revision: connection.revision }
}

export function selectedMarketplaceSource(entry: PluginMarketplaceEntry) {
  return entry.sources.find(source => source.selected) ?? entry.sources[0] ?? null
}

export function pluginMarketplaceAction(entry: PluginMarketplaceEntry) {
  if (!entry.permissions_available) return { label: '插件运行时不可用', disabled: true }
  if (entry.compatibility !== 'compatible' || entry.install_status === 'incompatible') return { label: '与当前 Server 不兼容', disabled: true }
  if (entry.install_status === 'installed') return { label: '当前版本已安装', disabled: true }
  if (entry.install_status === 'update_available') return { label: `校验升级包 v${entry.version}`, disabled: false }
  return { label: '校验安装包', disabled: false }
}

export function pluginHasMarketplaceUpdate(pluginID: string, entries: readonly PluginMarketplaceEntry[]) {
  return entries.some(entry => entry.id === pluginID && entry.install_status === 'update_available' && entry.permissions_available)
}

export function compatibilityLabel(value: PluginMarketplaceEntry['compatibility']) {
  if (value === 'compatible') return '兼容当前 Server'
  if (value === 'server_too_old') return '需要更新 Server'
  return '插件尚未兼容此 Server'
}

export function permissionDetails(permission: PluginPermission) {
  if (permission.kind === 'network.http') return permission.domains?.join('、') || '未声明域名'
  if (permission.kind === 'credential.use') return permission.scopes?.join('、') || '未声明凭据范围'
  if (permission.kind === 'event.subscribe') return permission.topics?.join('、') || '未声明事件'
  if (permission.kind === 'storage.private') return `最多 ${formatBytes(permission.maxBytes ?? 0)}`
  return '允许向 Server 提交受校验的下载方案'
}

export function permissionLabel(kind: PluginPermission['kind']) {
  if (kind === 'network.http') return '访问网络'
  if (kind === 'credential.use') return '使用插件凭据'
  if (kind === 'storage.private') return '插件私有存储'
  if (kind === 'event.subscribe') return '订阅系统事件'
  return '创建下载方案'
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KiB`
  return `${Math.round(value / (1024 * 1024))} MiB`
}
