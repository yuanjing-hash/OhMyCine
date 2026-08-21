import { Permissions, type PermissionCode } from '@/auth/generated-permissions'
import { hasAnyPermission } from '@/navigation'

export type DashboardSection = 'status' | 'activity' | 'pipeline' | 'subscriptions' | 'discovery'
export type DashboardCardState = 'live' | 'planned'

export interface DashboardCardDefinition {
  id: string
  title: string
  section: DashboardSection
  state: DashboardCardState
  span: 3 | 4 | 5 | 7 | 12
  permissionsAny: readonly PermissionCode[]
  description: string
  owner: string
}

export const dashboardCards: readonly DashboardCardDefinition[] = [
  { id: 'server-status', title: 'Server 总状态', section: 'status', state: 'live', span: 12, permissionsAny: [Permissions.DashboardRead], description: '来自 GET /api/v1/dashboard 的初始化与恢复基线。', owner: 'runtime / administration' },
  { id: 'media-summary', title: '媒体概览', section: 'status', state: 'planned', span: 4, permissionsAny: [Permissions.DashboardRead], description: '媒体目录 API 尚未实现；不会把未配置显示为 0 部。', owner: 'media catalog / import history' },
  { id: 'storage-summary', title: '存储空间', section: 'status', state: 'planned', span: 4, permissionsAny: [Permissions.DestinationsRead], description: '存储目标和容量 API 尚未实现，未知容量不会显示为 0 B。', owner: 'destination service / storage drivers' },
  { id: 'connection-health', title: '连接健康', section: 'status', state: 'planned', span: 4, permissionsAny: [Permissions.ConnectionsRead], description: '尚未配置真实连接；不会显示虚假的健康 provider。', owner: 'connection service / provider adapters' },
  { id: 'active-tasks', title: '活动任务', section: 'activity', state: 'planned', span: 7, permissionsAny: [Permissions.DownloadsReadOwn, Permissions.DownloadsReadAll, Permissions.FollowsReadOwn, Permissions.FollowsReadAll, Permissions.StrmRunsRead], description: '全局任务中心和下载任务事实已实现；仪表盘跨域聚合仍待接入。', owner: 'task read model over domain services' },
  { id: 'pipeline-status', title: '流水线状态', section: 'activity', state: 'planned', span: 5, permissionsAny: [Permissions.DownloadsReadOwn, Permissions.DownloadsReadAll, Permissions.FollowsReadOwn, Permissions.FollowsReadAll, Permissions.StrmRunsRead], description: 'Discover → Download → Transfer → Import → Notify 各阶段尚无真实聚合状态。', owner: 'pipeline projection' },
  { id: 'recent-imports', title: '近期入库 / 整理', section: 'pipeline', state: 'planned', span: 7, permissionsAny: [Permissions.TransfersReadOwn, Permissions.TransfersReadAll], description: '媒体整理已有独立任务中心；仪表盘近期入库聚合仍待接入，完整记录可前往媒体整理查看。', owner: 'transfer / import history' },
  { id: 'scheduler-jobs', title: '后台任务队列', section: 'pipeline', state: 'planned', span: 5, permissionsAny: [Permissions.SystemAdmin], description: '任务中心已有真实队列 API；仪表盘 scheduler 聚合卡片仍待接入。', owner: 'scheduler service' },
  { id: 'download-summary', title: '下载速率 / 队列', section: 'pipeline', state: 'planned', span: 4, permissionsAny: [Permissions.DownloadsReadOwn, Permissions.DownloadsReadAll], description: '下载管理已有 qBittorrent 实时采样；仪表盘汇总曲线仍待接入。', owner: 'downloader service / adapters' },
  { id: 'subscription-calendar', title: '订阅日历', section: 'subscriptions', state: 'planned', span: 5, permissionsAny: [Permissions.FollowsReadOwn, Permissions.FollowsReadAll], description: 'follow service 尚未实现，当前没有可查询的将播或缺集事件。', owner: 'follow service / scheduler' },
  { id: 'quick-actions', title: '快捷操作', section: 'subscriptions', state: 'planned', span: 3, permissionsAny: [Permissions.ConnectionsCreate, Permissions.FollowsCreate, Permissions.DownloadsCreate, Permissions.StrmRunsCreate, Permissions.MediaServersRefresh], description: '下载提交已在下载管理页落地；其它业务快捷操作等待各自真实 API。', owner: 'each domain write API' },
  { id: 'discovery-hero', title: '发现内容', section: 'discovery', state: 'planned', span: 12, permissionsAny: [Permissions.DiscoveryRead], description: '发现 provider 尚未配置；不会使用占位海报冒充真实推荐。', owner: 'discovery service / metadata providers' },
] as const

export const dashboardSectionPriority: readonly DashboardSection[] = ['status', 'activity', 'pipeline', 'subscriptions', 'discovery']

export function getVisibleDashboardCards(granted: Iterable<PermissionCode>): DashboardCardDefinition[] {
  const permissionSet = granted instanceof Set ? granted : new Set(granted)
  return dashboardCards.filter(card => hasAnyPermission(card.permissionsAny, permissionSet))
}
