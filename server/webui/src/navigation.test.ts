import { describe, expect, it } from 'vitest'
import { Permissions } from '@/auth/generated-permissions'
import {
  buildVisibleNavigation,
  getFirstVisibleUserManagementPath,
  getVisibleUserManagementTabs,
  legacyRouteRedirects,
  findNavigationItem,
} from '@/navigation'

describe('grouped administration navigation', () => {
  it('uses canonical queue permissions for the real task center', () => {
    const item = findNavigationItem('tasks')
    expect(item.planned).toBeUndefined()
    expect(item.permissionsAny).toEqual([Permissions.JobsReadOwn, Permissions.JobsReadAll])
  })
  it('exposes the real download workspace to download or downloader readers', () => {
    const item = findNavigationItem('downloads')
    expect(item.planned).toBeUndefined()
    expect(buildVisibleNavigation([Permissions.DownloadersRead])[0]?.items.map(candidate => candidate.id)).toEqual(['downloads'])
    expect(buildVisibleNavigation([Permissions.DownloadsReadOwn])[0]?.items.map(candidate => candidate.id)).toEqual(['downloads'])
  })
  it('exposes media organization only to transfer readers', () => {
    const item = findNavigationItem('organization')
    expect(item.planned).toBeUndefined()
    expect(item.permissionsAny).toEqual([Permissions.TransfersReadOwn, Permissions.TransfersReadAll])
    expect(buildVisibleNavigation([Permissions.TransfersReadOwn])[0]?.items.map(candidate => candidate.id)).toEqual(['organization'])
    expect(buildVisibleNavigation([Permissions.CategoriesRead]).flatMap(group => group.items).map(candidate => candidate.id)).not.toContain('organization')
  })
  it('exposes the real settings workspace for settings.read', () => {
    const item = findNavigationItem('settings')
    expect(item.planned).toBeUndefined()
    expect(buildVisibleNavigation([Permissions.SettingsRead])[0]?.items.map(candidate => candidate.id)).toEqual(['settings'])
  })
  it('exposes plugin repository management as a real workspace', () => {
    const item = findNavigationItem('plugins')
    expect(item.planned).toBeUndefined()
    expect(item.permissionsAny).toEqual([Permissions.PluginsRead])
  })
	it('exposes STRM management as a real workspace', () => {
		const item = findNavigationItem('strm')
		expect(item.planned).toBeUndefined()
		expect(item.permissionsAny).toEqual([Permissions.StrmRunsRead])
		expect(buildVisibleNavigation([Permissions.StrmRunsRead])[0]?.items.map(candidate => candidate.id)).toEqual(['strm'])
	})
  it('omits every empty group and keeps the dashboard standalone', () => {
    expect(buildVisibleNavigation([Permissions.DashboardRead])).toEqual([])
  })

  it('shows only children allowed by generated permission codes', () => {
    const groups = buildVisibleNavigation([Permissions.DiscoveryRead, Permissions.PluginsRead])

    expect(groups.map(group => group.id)).toEqual(['discovery', 'system'])
    expect(groups[0]?.items.map(item => item.id)).toEqual(['recommendations', 'explore'])
    expect(groups[1]?.items.map(item => item.id)).toEqual(['plugins'])
  })

  it('exposes the real storage workspace to storages.read', () => {
    const groups = buildVisibleNavigation([Permissions.StoragesRead])
    expect(groups.map(group => group.id)).toEqual(['system'])
    expect(groups[0]?.items.map(item => item.id)).toEqual(['connections-storage'])
    expect(groups[0]?.items[0]?.planned).toBeUndefined()
  })

  it('exposes player management to connection readers', () => {
    const groups = buildVisibleNavigation([Permissions.ConnectionsRead])
    expect(groups.map(group => group.id)).toEqual(['system'])
    expect(groups[0]?.items.map(item => item.id)).toEqual(['players'])
    expect(findNavigationItem('players').planned).toBeUndefined()
  })

  it('exposes rule management only to the canonical profile read permission', () => {
    expect(buildVisibleNavigation([Permissions.MediaClassificationProfilesRead])[0]?.items.map(item => item.id)).toEqual(['media-rules'])
    expect(buildVisibleNavigation([Permissions.CategoriesRead]).flatMap(group => group.items).map(item => item.id)).not.toContain('media-rules')
  })

  it('exposes the media library workspace only to its generated read permission', () => {
    const groups = buildVisibleNavigation([Permissions.MediaLibrariesRead])
    expect(groups.flatMap(group => group.items).map(item => item.id)).toEqual(['library-catalog', 'media-libraries'])
    expect(findNavigationItem('media-libraries').label).toBe('媒体库管理')
    expect(buildVisibleNavigation([Permissions.StoragesRead]).flatMap(group => group.items).map(item => item.id)).not.toContain('media-libraries')
  })

  it('shows User Management when either account or role reads are granted', () => {
    const accounts = buildVisibleNavigation([Permissions.UsersRead])
    const roles = buildVisibleNavigation([Permissions.RolesRead])

    expect(accounts[0]?.items.map(item => item.id)).toEqual(['user-management'])
    expect(roles[0]?.items.map(item => item.id)).toEqual(['user-management'])
  })
})

describe('User Management hierarchy', () => {
  it('hides the workspace tabs and has no redirect without either read permission', () => {
    expect(getVisibleUserManagementTabs([])).toEqual([])
    expect(getFirstVisibleUserManagementPath([])).toBeNull()
  })

  it('exposes only the accounts tab for users.read', () => {
    expect(getVisibleUserManagementTabs([Permissions.UsersRead]).map(tab => tab.id)).toEqual(['accounts'])
    expect(getFirstVisibleUserManagementPath([Permissions.UsersRead])).toBe('/system/users/accounts')
  })

  it('routes a roles-only actor directly to the roles tab', () => {
    expect(getVisibleUserManagementTabs([Permissions.RolesRead]).map(tab => tab.id)).toEqual(['roles'])
    expect(getFirstVisibleUserManagementPath([Permissions.RolesRead])).toBe('/system/users/roles')
  })

  it('exposes both tabs in canonical order and defaults to accounts when both reads are granted', () => {
    const permissions = [Permissions.UsersRead, Permissions.RolesRead]

    expect(getVisibleUserManagementTabs(permissions).map(tab => tab.id)).toEqual(['accounts', 'roles'])
    expect(getFirstVisibleUserManagementPath(permissions)).toBe('/system/users/accounts')
  })
})

describe('legacy deep links', () => {
  it('keeps explicit compatibility redirects', () => {
    expect(legacyRouteRedirects).toEqual({
      '/users': '/system/users/accounts',
      '/roles': '/system/users/roles',
      '/audit': '/logs/audit',
    })
  })
})
