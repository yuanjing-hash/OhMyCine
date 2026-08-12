import { Permissions, type PermissionCode } from '@/auth/generated-permissions'

export type NavigationGroupID = 'discovery' | 'subscriptions' | 'automation' | 'system'

export interface NavigationItem {
  id: string
  label: string
  to: string
  permissionsAny: readonly PermissionCode[]
  planned?: boolean
  eyebrow: string
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
const downloadPermissions = [Permissions.DownloadsReadOwn, Permissions.DownloadsReadAll] as const
const taskPermissions = [
  Permissions.DownloadsReadOwn,
  Permissions.DownloadsReadAll,
  Permissions.FollowsReadOwn,
  Permissions.FollowsReadAll,
  Permissions.StrmRunsRead,
] as const
const connectionPermissions = [
  Permissions.ConnectionsRead,
  Permissions.StoragesRead,
  Permissions.DestinationsRead,
  Permissions.CategoriesRead,
] as const
export const userManagementPermissions = [Permissions.UsersRead, Permissions.RolesRead] as const

export const dashboardNavigation: NavigationItem = {
  id: 'dashboard',
  label: '仪表盘',
  to: '/',
  permissionsAny: [Permissions.DashboardRead],
  eyebrow: 'Overview',
  description: 'Server 状态、媒体自动化流水线与需要处理的事项。',
}

export const navigationGroups: readonly NavigationGroup[] = [
  {
    id: 'discovery',
    label: '发现',
    items: [
      { id: 'recommendations', label: '推荐', to: '/discovery/recommendations', permissionsAny: discoveryPermissions, planned: true, eyebrow: 'Discovery', description: '真实发现 provider 接入后，在这里呈现有来源和更新时间的推荐内容。' },
      { id: 'explore', label: '探索', to: '/discovery/explore', permissionsAny: discoveryPermissions, planned: true, eyebrow: 'Discovery', description: '用于 PT/资源聚合搜索与公开元数据探索；当前业务 API 尚未实现。' },
    ],
  },
  {
    id: 'subscriptions',
    label: '订阅',
    items: [
      { id: 'subscriptions', label: '订阅管理', to: '/subscriptions/manage', permissionsAny: followPermissions, planned: true, eyebrow: 'Subscriptions', description: '追更生命周期、缺集检测与筛选能力将在 follow service 落地后接入。' },
      { id: 'workflows', label: '工作流', to: '/subscriptions/workflows', permissionsAny: followPermissions, planned: true, eyebrow: 'Subscriptions', description: '展示订阅调度工作流与逐阶段结果；当前没有可查询的调度 API。' },
      { id: 'calendar', label: '日历', to: '/subscriptions/calendar', permissionsAny: followPermissions, planned: true, eyebrow: 'Subscriptions', description: '未来仅呈现当前账户获权范围内的将播、待搜和失败事件。' },
    ],
  },
  {
    id: 'automation',
    label: '媒体自动化',
    items: [
      { id: 'tasks', label: '任务中心', to: '/automation/tasks', permissionsAny: taskPermissions, planned: true, eyebrow: 'Automation', description: '聚合下载、转移、STRM、刷新与追更任务；不会成为新的任务写入 owner。' },
      { id: 'downloads', label: '下载管理', to: '/automation/downloads', permissionsAny: downloadPermissions, planned: true, eyebrow: 'Automation', description: '下载器健康、队列和速度需要真实 downloader service；当前不生成演示任务。' },
      { id: 'organization', label: '媒体整理', to: '/automation/organization', permissionsAny: [Permissions.CategoriesRead], planned: true, eyebrow: 'Automation', description: '分类、元数据匹配、命名与转移记录将在相应业务域完成后接入。' },
      { id: 'strm-import', label: 'STRM / 入库', to: '/automation/strm-import', permissionsAny: [Permissions.StrmRunsRead], planned: true, eyebrow: 'Automation', description: 'STRM Run、signed 302、入库和媒体服务器刷新 API 尚未实现。' },
      { id: 'files', label: '文件管理', to: '/automation/files', permissionsAny: [Permissions.SystemAdmin], planned: true, eyebrow: 'Automation', description: '未来文件操作将限定配置根、阻止路径逃逸并执行确认与审计。' },
    ],
  },
  {
    id: 'system',
    label: '系统',
    items: [
      { id: 'connections-storage', label: '连接与存储', to: '/system/connections', permissionsAny: connectionPermissions, eyebrow: 'System', description: '管理本地 Storage 根与连接；Storage Destination 和分类规则仍处于规划阶段。' },
      { id: 'sites', label: '站点管理', to: '/system/sites', permissionsAny: [Permissions.SystemAdmin], planned: true, eyebrow: 'System', description: 'PT 站点与脱敏配置将在独立权限和 API 落地后开放。' },
      { id: 'plugins', label: '插件', to: '/system/plugins', permissionsAny: [Permissions.PluginsRead], planned: true, eyebrow: 'System', description: '插件浏览、权限审阅与手动安装运行时仍在规划中。' },
      { id: 'user-management', label: '用户管理', to: '/system/users', permissionsAny: userManagementPermissions, eyebrow: 'Administration', description: '账户与角色权限的统一管理工作区。' },
      { id: 'settings', label: '设置', to: '/system/settings', permissionsAny: [Permissions.SettingsRead], planned: true, eyebrow: 'System', description: '调度、安全、同步与运行参数需要真实设置 API；本页当前不保存配置。' },
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
