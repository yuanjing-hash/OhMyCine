import { describe, expect, it } from 'vitest'
import {
  buildPluginRepositoryCreatePayload,
  buildPluginRepositoryDeletePayload,
  buildPluginRepositoryOrderPayload,
  buildPluginRepositoryTogglePayload,
  compatibilityLabel,
  buildPluginInstallConfirmPayload,
  buildPluginInstallPreviewPayload,
  buildPluginRevisionPayload,
  buildPluginConnectionCreatePayload,
  buildPluginConnectionCreatePayloadFromConfig,
  buildPluginConnectionConfigPayload,
  buildPluginConnectionQRCodePayload,
  buildPluginConnectionDeletePayload,
  buildPluginConnectionTogglePayload,
  permissionDetails,
  normalizePluginInstallPreview,
  normalizeInstalledPluginSummary,
  pluginHasMarketplaceUpdate,
  pluginMarketplaceAction,
  pluginQRCodeAuthScope,
  pluginInstallConfirmPath,
  pluginInstallPreviewPath,
  pluginLifecyclePath,
  pluginConnectionPath,
  pluginConnectionsPath,
  pluginUninstallPath,
  pluginRepositoryRefreshPath,
  selectedMarketplaceSource,
  type PluginMarketplaceEntry,
  type PluginRepositorySummary,
  type PluginInstallPreview,
  type InstalledPluginSummary,
  type PluginConnectionSummary,
} from '@/plugins'

const repository = (id: number, revision: number): PluginRepositorySummary => ({
  id,
  name: `Repository ${id}`,
  github_url: `https://github.com/owner/repository-${id}`,
  enabled: true,
  priority: id * 1000,
  revision,
  last_commit_sha: '',
  last_refreshed_at: null,
  last_error_code: '',
  registry_name: '',
  plugin_count: 0,
  cache_valid: false,
  created_at: '2026-08-22T00:00:00Z',
  updated_at: '2026-08-22T00:00:00Z',
})

describe('plugin connection contracts', () => {
  const connection = { id: 'connection id', revision: 4 } as PluginConnectionSummary

  it('keeps secrets outside the ordinary config object', () => {
    expect(buildPluginConnectionCreatePayload({
      name: '  Bilibili  ',
      configText: '{"homeRecommendationEnabled":true}',
      credentialScope: 'bilibili.session',
      credentialMode: 'cookie',
      credential: 'SESSDATA=secret',
    })).toEqual({
      name: 'Bilibili',
      config: { homeRecommendationEnabled: true },
      credential_scope: 'bilibili.session',
      credential_mode: 'cookie',
      credential: 'SESSDATA=secret',
      enabled: true,
    })
  })

  it('uses encoded connection routes and revision-bound mutations', () => {
    expect(pluginConnectionsPath('org.example/video')).toBe('/api/v1/plugins/org.example%2Fvideo/connections')
    expect(pluginConnectionPath('org.example/video', connection.id)).toBe('/api/v1/plugins/org.example%2Fvideo/connections/connection%20id')
    expect(buildPluginConnectionTogglePayload(connection, false)).toEqual({ enabled: false, revision: 4 })
    expect(buildPluginConnectionDeletePayload(connection)).toEqual({ revision: 4 })
  })

  it('preserves declarative settings defaults in create and revision-bound update payloads', () => {
    const plugin = {
      id: 'org.ohmycine.bilibili',
      config_defaults: { homeRecommendationEnabled: true, defaultQuality: '1080p', downloadSubtitle: true },
      settings_page: {
        version: 1,
        tabs: [{
          id: 'general',
          title: '常规',
          sections: [{ id: 'download', title: '下载', fields: [{ type: 'select', key: 'defaultQuality', label: '默认清晰度', options: [{ label: '1080P', value: '1080p' }] }] }],
        }],
      },
    } satisfies Pick<InstalledPluginSummary, 'id' | 'config_defaults' | 'settings_page'>
    expect(plugin.settings_page?.tabs[0]?.sections[0]?.fields[0]).toMatchObject({ type: 'select', key: 'defaultQuality' })
    expect(buildPluginConnectionCreatePayloadFromConfig({
      name: '  我的 Bilibili  ',
      config: { ...plugin.config_defaults },
      credentialScope: 'bilibili.session',
      credentialMode: 'none',
      credential: 'must-not-be-sent',
    })).toEqual({
      name: '我的 Bilibili',
      config: plugin.config_defaults,
      credential_scope: '',
      credential_mode: 'none',
      credential: '',
      enabled: true,
    })
    expect(buildPluginConnectionConfigPayload(connection, { ...plugin.config_defaults, defaultQuality: '720p' })).toEqual({
      config: { homeRecommendationEnabled: true, defaultQuality: '720p', downloadSubtitle: true },
      revision: 4,
    })
  })

  it('recognizes Host-owned QR login and migrates anonymous connections to encrypted cookie mode', () => {
    const plugin = {
      capabilities: ['site.interaction'],
      permissions: [{ kind: 'credential.use', scopes: ['bilibili.session'] }],
      settings_page: {
        version: 1,
        tabs: [{
          id: 'account',
          title: '账号',
          sections: [{ id: 'login', title: '登录', fields: [{ type: 'credential-status', label: '账号状态' }] }],
        }],
      },
    } satisfies Pick<InstalledPluginSummary, 'capabilities' | 'permissions' | 'settings_page'>

    expect(pluginQRCodeAuthScope(plugin)).toBe('bilibili.session')
    expect(buildPluginConnectionQRCodePayload(connection, 'bilibili.session')).toEqual({
      credential_scope: 'bilibili.session',
      credential_mode: 'cookie',
      revision: 4,
    })
    expect(pluginQRCodeAuthScope({ ...plugin, settings_page: undefined })).toBeNull()
    expect(pluginQRCodeAuthScope({
      ...plugin,
      permissions: [{ kind: 'credential.use', scopes: ['bilibili.session', 'bilibili.creator'] }],
    })).toBeNull()
  })
})

describe('plugin repository API contracts', () => {
  it('normalizes create input and keeps GitHub URL as the only source input', () => {
    expect(buildPluginRepositoryCreatePayload('  https://github.com/owner/plugins  ', ' Official ')).toEqual({
      github_url: 'https://github.com/owner/plugins',
      name: 'Official',
      enabled: true,
    })
  })

  it('binds every mutation to the current repository revision', () => {
    const item = repository(7, 12)
    expect(buildPluginRepositoryTogglePayload(item, false)).toEqual({ enabled: false, revision: 12 })
    expect(buildPluginRepositoryDeletePayload(item)).toEqual({ revision: 12 })
    expect(pluginRepositoryRefreshPath(item.id)).toBe('/api/v1/plugin-repositories/7/refresh')
  })

  it('preserves visible order and revisions in reorder payloads', () => {
    expect(buildPluginRepositoryOrderPayload([repository(2, 4), repository(1, 8)])).toEqual({
      order: [{ id: 2, revision: 4 }, { id: 1, revision: 8 }],
    })
  })
})

describe('plugin marketplace presentation', () => {
  it('uses the explicitly selected source and exposes compatibility labels', () => {
    const entry = {
      compatibility: 'compatible',
      sources: [
        { repository_id: 1, repository_name: 'A', repository_url: 'https://github.com/a/plugins', priority: 1000, version: '0.1.0', channel: 'stable', selected: false },
        { repository_id: 2, repository_name: 'B', repository_url: 'https://github.com/b/plugins', priority: 500, version: '0.2.0', channel: 'stable', selected: true },
      ],
    } as PluginMarketplaceEntry
    expect(selectedMarketplaceSource(entry)?.repository_name).toBe('B')
    expect(compatibilityLabel(entry.compatibility)).toBe('兼容当前 Server')
    expect(compatibilityLabel('server_too_old')).toBe('需要更新 Server')
  })

  it('binds installation preview and confirmation to the selected immutable source', () => {
    const entry = {
      id: 'org.example.video',
      version: '1.2.3',
      sources: [
        { repository_id: 9, repository_name: 'Official', repository_url: 'https://github.com/example/plugins', priority: 1, version: '1.2.3', channel: 'stable', selected: true },
      ],
    } as PluginMarketplaceEntry
    const preview = {
      id: 'preview-id', plugin_id: entry.id, operation: 'update', permission_fingerprint: 'digest', installation_revision: 4,
    } as PluginInstallPreview
    expect(buildPluginInstallPreviewPayload(entry)).toEqual({ repository_id: 9, version: '1.2.3' })
    expect(buildPluginInstallConfirmPayload(preview)).toEqual({ preview_id: 'preview-id', permission_fingerprint: 'digest', revision: 4 })
    expect(pluginInstallPreviewPath(entry.id)).toBe('/api/v1/plugins/org.example.video/installation-preview')
    expect(pluginInstallConfirmPath(preview)).toBe('/api/v1/plugins/org.example.video/update')
  })

  it('normalizes empty permission collections returned by older servers', () => {
    const preview = {
      id: 'preview-id',
      plugin_id: 'org.example.video',
      name: 'Example',
      version: '1.0.0',
      operation: 'install',
      repository_id: 9,
      repository_name: 'Official',
      capabilities: ['site.navigation'],
      permissions: null,
      permission_diff: { added: [{ kind: 'download.plan' }], removed: null, unchanged: null },
      permission_fingerprint: 'digest',
      installation_revision: 0,
      expires_at: '2026-08-23T00:00:00Z',
    } as unknown as PluginInstallPreview

    expect(normalizePluginInstallPreview(preview)).toMatchObject({
      permissions: [],
      permission_diff: {
        added: [{ kind: 'download.plan' }],
        removed: [],
        unchanged: [],
      },
    })
  })

  it('normalizes nullable installed-plugin collections returned by older servers', () => {
    const plugin = {
      id: 'org.example.video',
      capabilities: null,
      permissions: null,
      config_schema: null,
      config_defaults: null,
    } as unknown as InstalledPluginSummary
    expect(normalizeInstalledPluginSummary(plugin)).toMatchObject({
      capabilities: [],
      permissions: [],
      config_schema: {},
      config_defaults: {},
    })
  })

  it('binds lifecycle actions to the current installation revision', () => {
    const plugin = { id: 'org.example.video', revision: 7 } as InstalledPluginSummary
    expect(buildPluginRevisionPayload(plugin)).toEqual({ revision: 7 })
    expect(pluginLifecyclePath(plugin.id, 'disable')).toBe('/api/v1/plugins/org.example.video/disable')
    expect(pluginUninstallPath(plugin.id)).toBe('/api/v1/plugins/org.example.video')
  })

  it('renders bounded human-readable permission details', () => {
    expect(permissionDetails({ kind: 'network.http', domains: ['api.example.com'] })).toBe('api.example.com')
    expect(permissionDetails({ kind: 'storage.private', maxBytes: 8 * 1024 * 1024 })).toBe('最多 8 MiB')
  })

  it('uses backend install status instead of guessing updates from unequal versions', () => {
    const base = {
      id: 'org.example.video', version: '0.9.0', compatibility: 'compatible', permissions_available: true,
    } as PluginMarketplaceEntry
    expect(pluginMarketplaceAction({ ...base, install_status: 'installed' })).toEqual({ label: '当前版本已安装', disabled: true })
    expect(pluginHasMarketplaceUpdate(base.id, [{ ...base, install_status: 'installed' }])).toBe(false)
    expect(pluginMarketplaceAction({ ...base, version: '1.1.0', install_status: 'update_available' })).toEqual({ label: '校验升级包 v1.1.0', disabled: false })
    expect(pluginHasMarketplaceUpdate(base.id, [{ ...base, install_status: 'update_available' }])).toBe(true)
  })

  it('describes preview actions as package validation instead of completed installation', () => {
    const entry = {
      id: 'org.example.video', version: '1.0.0', compatibility: 'compatible', permissions_available: true, install_status: 'available',
    } as PluginMarketplaceEntry
    expect(pluginMarketplaceAction(entry)).toEqual({ label: '校验安装包', disabled: false })
  })

  it('disables installation when the server plugin runtime is unavailable', () => {
    const entry = {
      id: 'org.example.video', version: '1.0.0', compatibility: 'compatible', permissions_available: false, install_status: 'available',
    } as PluginMarketplaceEntry
    expect(pluginMarketplaceAction(entry)).toEqual({ label: '插件运行时不可用', disabled: true })
    expect(pluginHasMarketplaceUpdate(entry.id, [{ ...entry, install_status: 'update_available' }])).toBe(false)
  })
})
