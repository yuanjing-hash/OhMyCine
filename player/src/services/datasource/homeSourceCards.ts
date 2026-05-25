import type { RawSourceIndexStatus } from '../scraper/rawSourceIndexScheduler'
import type { DataSourceConfig, DataSourceType } from './types'
import { normalizeProviderPath } from '../scraper/pathUtils'

export type HomeSourceScanState = 'notApplicable' | 'pending' | 'indexing' | 'scanned' | 'partial' | 'failed'
export type HomeSourceStatusTone = 'ready' | 'pending' | 'indexing' | 'scanned' | 'partial' | 'disabled'

export interface HomeSourceScanCacheSummary {
  readonly status: 'completed' | 'partialFailed'
  readonly records?: readonly unknown[]
  readonly candidates?: readonly unknown[]
  readonly fileCount?: number
}

export interface HomeSourceScanCacheLookupInput {
  readonly config: DataSourceConfig
  readonly sourceId: string
  readonly sourceType: DataSourceType
  readonly rootPath: string
}

export interface DeriveHomeSourceCardsOptions {
  readonly scanCacheReader?: (input: HomeSourceScanCacheLookupInput) => HomeSourceScanCacheSummary | null
  readonly scanStatusReader?: (input: HomeSourceScanCacheLookupInput) => RawSourceIndexStatus | null
}

export interface HomeSourceCard {
  readonly id: string
  readonly type: DataSourceType
  readonly title: string
  readonly providerLabel: string
  readonly iconLabel: string
  readonly enabled: boolean
  readonly isOpenable: boolean
  readonly statusLabel: string
  readonly statusTone: HomeSourceStatusTone
  readonly description: string
  readonly actionLabel: string
  readonly scanState: HomeSourceScanState
  readonly rootPath?: string
}

export type HomeLatestEmptyAction
  = | { readonly kind: 'settings' }
    | { readonly kind: 'source', readonly sourceId: string }

export interface HomeLatestEmptyState {
  readonly title: string
  readonly description: string
  readonly actionLabel: string
  readonly action: HomeLatestEmptyAction
}

const SOURCE_TYPE_LABELS: Record<DataSourceType, string> = {
  emby: 'Emby',
  jellyfin: 'Jellyfin',
  alist: 'OpenList/Alist',
  clouddrive2: 'CloudDrive2',
  server: 'OhMyCine Server',
  115: '115',
  123: '123',
  quark: 'Quark',
  local: '本地文件',
}

const SOURCE_TYPE_ICONS: Record<DataSourceType, string> = {
  emby: 'E',
  jellyfin: 'J',
  alist: 'A',
  clouddrive2: 'C',
  server: 'S',
  115: '1',
  123: '2',
  quark: 'Q',
  local: 'L',
}

export function deriveHomeSourceCards(
  configs: readonly DataSourceConfig[],
  options: DeriveHomeSourceCardsOptions = {},
): HomeSourceCard[] {
  return [...configs]
    .sort((left, right) => left.order - right.order)
    .map(config => deriveHomeSourceCard(config, options))
}

export function deriveHomeLatestEmptyState(cards: readonly HomeSourceCard[]): HomeLatestEmptyState {
  if (cards.length === 0) {
    return {
      title: '等待影视库内容',
      description: '添加数据源后，最新入库和推荐内容会在这里横向展示。',
      actionLabel: '添加数据源',
      action: { kind: 'settings' },
    }
  }

  const openableCards = cards.filter(card => card.isOpenable)
  const pendingOpenList = openableCards.find(card => card.type === 'alist' && isOpenListAutoIndexPendingState(card.scanState))
  if (pendingOpenList) {
    const isIndexing = pendingOpenList.scanState === 'indexing'
    return {
      title: isIndexing ? 'OpenList/Alist 后台整理中' : '等待 OpenList/Alist 自动索引',
      description: '本地媒体库会在后台自动整理；期间不影响其他数据源的首页内容。',
      actionLabel: '打开媒体库',
      action: { kind: 'source', sourceId: pendingOpenList.id },
    }
  }

  const firstOpenable = openableCards[0]
  if (firstOpenable) {
    return {
      title: '还没有最新内容',
      description: '打开媒体库浏览已配置的数据源。',
      actionLabel: '打开媒体库',
      action: { kind: 'source', sourceId: firstOpenable.id },
    }
  }

  return {
    title: '数据源已停用',
    description: '在设置中启用数据源后再浏览。',
    actionLabel: '管理数据源',
    action: { kind: 'settings' },
  }
}

function deriveHomeSourceCard(config: DataSourceConfig, options: DeriveHomeSourceCardsOptions): HomeSourceCard {
  const title = config.displayName?.trim() || config.name
  const enabled = config.enabled !== false
  const providerLabel = sourceTypeLabel(config.type)
  const rootPath = config.type === 'alist' ? readRawSourceRootPath(config) : undefined

  if (!enabled) {
    return {
      id: config.id,
      type: config.type,
      title,
      providerLabel,
      iconLabel: sourceTypeIcon(config.type),
      enabled,
      isOpenable: false,
      statusLabel: '已停用',
      statusTone: 'disabled',
      description: '在设置中启用后可打开。',
      actionLabel: '不可用',
      scanState: 'notApplicable',
      rootPath,
    }
  }

  if (config.type === 'alist') {
    const lookupInput: HomeSourceScanCacheLookupInput = {
      config,
      sourceId: config.id,
      sourceType: config.type,
      rootPath: rootPath ?? '/',
    }
    const cache = options.scanCacheReader?.(lookupInput) ?? null
    const scanStatus = options.scanStatusReader?.(lookupInput) ?? null
    const isIndexing = scanStatus?.state === 'running' || scanStatus?.state === 'queued'

    if (!cache) {
      if (isIndexing) {
        return {
          id: config.id,
          type: config.type,
          title,
          providerLabel,
          iconLabel: sourceTypeIcon(config.type),
          enabled,
          isOpenable: true,
          statusLabel: '后台整理中',
          statusTone: 'indexing',
          description: `${rootPathDescription(rootPath)} · 正在自动建立本地索引`,
          actionLabel: '打开媒体库',
          scanState: 'indexing',
          rootPath,
        }
      }

      if (scanStatus?.state === 'failed') {
        return {
          id: config.id,
          type: config.type,
          title,
          providerLabel,
          iconLabel: sourceTypeIcon(config.type),
          enabled,
          isOpenable: true,
          statusLabel: '稍后自动重试',
          statusTone: 'partial',
          description: `${rootPathDescription(rootPath)} · 后台索引未完成，文件夹浏览不受影响`,
          actionLabel: '打开媒体库',
          scanState: 'failed',
          rootPath,
        }
      }

      return {
        id: config.id,
        type: config.type,
        title,
        providerLabel,
        iconLabel: sourceTypeIcon(config.type),
        enabled,
        isOpenable: true,
        statusLabel: '等待自动索引',
        statusTone: 'pending',
        description: `${rootPathDescription(rootPath)} · 软件会在后台整理`,
        actionLabel: '打开媒体库',
        scanState: 'pending',
        rootPath,
      }
    }

    const scanState = cache.status === 'completed' ? 'scanned' : 'partial'
    const statusLabel = isIndexing ? '后台更新中' : cache.status === 'completed' ? '已索引' : '部分索引'
    const itemCount = cache.candidates?.length ?? cache.records?.length ?? cache.fileCount ?? 0
    return {
      id: config.id,
      type: config.type,
      title,
      providerLabel,
      iconLabel: sourceTypeIcon(config.type),
      enabled,
      isOpenable: true,
      statusLabel,
      statusTone: isIndexing ? 'indexing' : scanState === 'scanned' ? 'scanned' : 'partial',
      description: `${rootPathDescription(rootPath)} · ${itemCount > 0 ? `${itemCount} 个候选` : '已有本地缓存'}`,
      actionLabel: '打开媒体库',
      scanState: isIndexing ? 'indexing' : scanState,
      rootPath,
    }
  }

  return {
    id: config.id,
    type: config.type,
    title,
    providerLabel,
    iconLabel: sourceTypeIcon(config.type),
    enabled,
    isOpenable: true,
    statusLabel: '可打开',
    statusTone: 'ready',
    description: '打开媒体库浏览内容。',
    actionLabel: '打开媒体库',
    scanState: 'notApplicable',
  }
}

function readRawSourceRootPath(config: DataSourceConfig): string {
  const rawRootPath = config.extra?.rootPath
  if (typeof rawRootPath !== 'string')
    return '/'

  try {
    return normalizeProviderPath(rawRootPath)
  }
  catch {
    return '/'
  }
}

function rootPathDescription(rootPath: string | undefined): string {
  return `根目录 ${rootPath || '/'}`
}

function isOpenListAutoIndexPendingState(state: HomeSourceScanState): boolean {
  return state === 'pending' || state === 'indexing' || state === 'failed'
}

function sourceTypeLabel(type: DataSourceType): string {
  return SOURCE_TYPE_LABELS[type] ?? type
}

function sourceTypeIcon(type: DataSourceType): string {
  return SOURCE_TYPE_ICONS[type] ?? '?'
}
