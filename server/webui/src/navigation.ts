import { Permissions, type PermissionCode } from '@/auth/generated-permissions'

export type NavigationGroupID = 'discovery' | 'subscriptions' | 'automation' | 'system'

export interface NavigationItem {
  id: string
  label: string
  to: string
  permissionsAny: readonly PermissionCode[]
  planned?: boolean
  description: string
}

export interface NavigationGroup {
  id: NavigationGroupID
  label: string
  items: readonly NavigationItem[]
}

export interface VisibleNavigationGroup extends Omit<NavigationGroup, 'items'> {
  items: NavigationItem[]
}

const discoveryPermissions = [Permissions.DiscoveryRead] as const
const followPermissions = [Permissions.FollowsReadOwn, Permissions.FollowsReadAll] as const
const downloadPermissions = [Permissions.DownloadsReadOwn, Permissions.DownloadsReadAll, Permissions.DownloadersRead] as const
const taskPermissions = [
	Permissions.JobsReadOwn,
	Permissions.JobsReadAll,
] as const
const connectionPermissions = [
  Permissions.StoragesRead,
] as const
export const userManagementPermissions = [Permissions.UsersRead, Permissions.RolesRead] as const

export const dashboardNavigation: NavigationItem = {
  id: 'dashboard',
  label: '仪表盘',
  to: '/',
  permissionsAny: [Permissions.DashboardRead],
  description: 'Server 状态、媒体自动化流水线与需要处理的事项。',
}

export const navigationGroups: readonly NavigationGroup[] = [
  {
    id: 'discovery',
    label: '发现',
    items: [
      { id: 'recommendations', label: '推荐', to: '/discovery/recommendations', permissionsAny: discoveryPermissions, description: '浏览 TMDB 与豆瓣真实推荐栏目，按来源刷新并查看缓存状态。' },
      { id: 'explore', label: '搜索', to: '/discovery/explore', permissionsAny: discoveryPermissions, description: '确认作品后按需聚合搜索已启用的 PT 站点并创建下载。' },
    ],
  },
  {
    id: 'subscriptions',
    label: '订阅',
    items: [
      { id: 'subscriptions', label: '订阅管理', to: '/subscriptions/manage', permissionsAny: followPermissions, planned: true, description: '追更生命周期、缺集检测与筛选能力将在 follow service 落地后接入。' },
      { id: 'workflows', label: '工作流', to: '/subscriptions/workflows', permissionsAny: followPermissions, planned: true, description: '展示订阅调度工作流与逐阶段结果；当前没有可查询的调度 API。' },
      { id: 'calendar', label: '日历', to: '/subscriptions/calendar', permissionsAny: followPermissions, planned: true, description: '未来仅呈现当前账户获权范围内的将播、待搜和失败事件。' },
    ],
  },
  {
    id: 'automation',
    label: '媒体自动化',
    items: [
      { id: 'tasks', label: '任务中心', to: '/automation/tasks', permissionsAny: taskPermissions, description: '查看、筛选和控制获权范围内的持久化自动化任务。' },
      { id: 'downloads', label: '下载管理', to: '/automation/downloads', permissionsAny: downloadPermissions, description: '管理 qBittorrent 下载器，提交磁力、URL 或种子，并查看真实任务 telemetry。' },
      { id: 'organization', label: '媒体整理', to: '/automation/organization', permissionsAny: [Permissions.TransfersReadOwn, Permissions.TransfersReadAll], description: '查看下载完成后自动生成的分类、命名、转移记录，并处理冲突或失败。' },
	  { id: 'strm', label: 'STRM 管理', to: '/automation/strm', permissionsAny: [Permissions.StrmRunsRead], description: '按媒体库刷新 signed STRM 投影，查看运行历史、托管产物并安全清理失效文件。' },
      { id: 'files', label: '文件管理', to: '/automation/files', permissionsAny: [Permissions.SystemAdmin], planned: true, description: '未来文件操作将限定配置根、阻止路径逃逸并执行确认与审计。' },
    ],
  },
  {
    id: 'system',
    label: '系统',
    items: [
      { id: 'connections-storage', label: '数据源', to: '/system/connections', permissionsAny: connectionPermissions, description: '统一管理本地目录与网盘数据源；底层连接凭据和目录边界仍分别保存。' },
      { id: 'players', label: '播放器管理', to: '/system/players', permissionsAny: [Permissions.ConnectionsRead], description: '管理 Emby 连接、聚合运行摘要与签名 STRM 的 302 播放网关。' },
      { id: 'media-libraries', label: '媒体库', to: '/system/media-libraries', permissionsAny: [Permissions.MediaLibrariesRead], description: '管理来源相对根、扫描计划、监听状态、扫描记录和媒体条目。' },
      { id: 'media-rules', label: '规则管理', to: '/system/media-rules', permissionsAny: [Permissions.MediaClassificationProfilesRead], description: '管理媒体分类、识别预处理与电影/剧集命名 Profile。' },
      { id: 'sites', label: '站点管理', to: '/system/sites', permissionsAny: [Permissions.SystemAdmin], description: '安全配置内建 PTTime 站点、连接健康、限速和启停策略。' },
      { id: 'plugins', label: '插件', to: '/system/plugins', permissionsAny: [Permissions.PluginsRead], description: '管理 GitHub 插件仓库、查看固定提交上的市场条目和真实安装状态。' },
      { id: 'user-management', label: '用户管理', to: '/system/users', permissionsAny: userManagementPermissions, description: '账户与角色权限的统一管理工作区。' },
      { id: 'settings', label: '设置', to: '/system/settings', permissionsAny: [Permissions.SettingsRead], description: '配置统一下载暂存目录；更多调度、安全与同步参数将按实际 API 逐步接入。' },
    ],
  },
] as const

export const navigationItems = [dashboardNavigation, ...navigationGroups.flatMap(group => group.items)]

export function hasAnyPermission(required: readonly PermissionCode[], granted: Iterable<PermissionCode>): boolean {
  const permissionSet = granted instanceof Set ? granted : new Set(granted)
  return required.length === 0 || required.some(permission => permissionSet.has(permission))
}

export function buildVisibleNavigation(granted: Iterable<PermissionCode>): VisibleNavigationGroup[] {
  const permissionSet = granted instanceof Set ? granted : new Set(granted)
  return navigationGroups.flatMap(group => {
    const items = group.items.filter(item => hasAnyPermission(item.permissionsAny, permissionSet))
    return items.length > 0 ? [{ ...group, items }] : []
  })
}

export function findNavigationItem(id: string): NavigationItem {
  const item = navigationItems.find(candidate => candidate.id === id)
  if (!item) throw new Error(`Unknown navigation item: ${id}`)
  return item
}

export interface UserManagementTab {
  id: 'accounts' | 'roles'
  label: string
  to: string
  permission: PermissionCode
}

export const userManagementTabs: readonly UserManagementTab[] = [
  { id: 'accounts', label: '账户', to: '/system/users/accounts', permission: Permissions.UsersRead },
  { id: 'roles', label: '角色与权限', to: '/system/users/roles', permission: Permissions.RolesRead },
] as const

export function getVisibleUserManagementTabs(granted: Iterable<PermissionCode>): UserManagementTab[] {
  const permissionSet = granted instanceof Set ? granted : new Set(granted)
  return userManagementTabs.filter(tab => permissionSet.has(tab.permission))
}

export function getFirstVisibleUserManagementPath(granted: Iterable<PermissionCode>): string | null {
  return getVisibleUserManagementTabs(granted)[0]?.to ?? null
}

export const legacyRouteRedirects = {
  '/users': '/system/users/accounts',
  '/roles': '/system/users/roles',
  '/audit': '/logs/audit',
} as const
