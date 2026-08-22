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
  buildPluginConnectionDeletePayload,
  buildPluginConnectionTogglePayload,
  permissionDetails,
  pluginHasMarketplaceUpdate,
  pluginMarketplaceAction,
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
    expect(pluginMarketplaceAction({ ...base, version: '1.1.0', install_status: 'update_available' })).toEqual({ label: '升级到 v1.1.0', disabled: false })
    expect(pluginHasMarketplaceUpdate(base.id, [{ ...base, install_status: 'update_available' }])).toBe(true)
  })

  it('disables installation when the server plugin runtime is unavailable', () => {
    const entry = {
      id: 'org.example.video', version: '1.0.0', compatibility: 'compatible', permissions_available: false, install_status: 'available',
    } as PluginMarketplaceEntry
    expect(pluginMarketplaceAction(entry)).toEqual({ label: '插件运行时不可用', disabled: true })
    expect(pluginHasMarketplaceUpdate(entry.id, [{ ...entry, install_status: 'update_available' }])).toBe(false)
  })
})
