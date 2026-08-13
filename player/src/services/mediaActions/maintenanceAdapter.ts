import type { Router } from 'vue-router'
import type { MediaActionAdapter, MediaActionCapability, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import type { DataSource, DataSourceConfig, DataSourceType } from '@/services/datasource/types'
import { loadRawSourceScanCache } from '@/services/scraper/localScanCache'
import { getRawScannedMediaDetail } from '@/services/scraper/rawHomeMapping'
import { openMediaEditor } from './editorRuntime'
import { getMaintenanceHandler, waitForMaintenanceHandler } from './maintenanceRuntime'

const RAW_TYPES = new Set<DataSourceType>(['alist', 'clouddrive2', 'webdav', 'local', '115', '123', 'quark'])

export function createMaintenanceMediaActionAdapter(router: Router, resolveSource: (id: string) => DataSource | null, resolveConfig?: (id: string) => DataSourceConfig | undefined): MediaActionAdapter {
  return {
    id: 'maintenance',
    priority: 80,
    supports: target => RAW_TYPES.has(target.sourceType as DataSourceType) || target.sourceType === 'emby' || target.sourceType === 'jellyfin',
    resolve: target => capabilities(target, resolveSource(target.sourceId), resolveConfig?.(target.sourceId)),
    execute: (target, action) => execute(router, resolveSource, target, action),
  }
}

async function capabilities(target: MediaActionTarget, source: DataSource | null, config: DataSourceConfig | undefined): Promise<MediaActionCapability[]> {
  if (target.sourceType === 'emby' || target.sourceType === 'jellyfin') {
    if (target.kind !== 'media')
      return []
    const resolved: MediaActionCapability[] = []
    if (source?.updateMetadata)
      resolved.push({ action: 'editMetadata', availability: 'available' })
    if (source?.updateArtworkFromUrl)
      resolved.push({ action: 'editArtwork', availability: 'available' })
    if (source?.searchSubtitles || source?.deleteSubtitle)
      resolved.push({ action: 'editSubtitles', availability: 'available' })
    if (source?.refreshMetadata)
      resolved.push({ action: 'refreshMetadata', availability: 'available' })
    return resolved
  }
  if (!RAW_TYPES.has(target.sourceType as DataSourceType))
    return []
  if (target.kind === 'library')
    return [{ action: 'rescanLibrary', availability: 'available' }]
  if (['folder', 'season'].includes(target.mediaType))
    return []
  const handler = getMaintenanceHandler(target.sourceId)
  if (handler?.canHandle) {
    const actions = ['identify', 'editMetadata', 'editArtwork', 'editSubtitles', 'refreshMetadata'] as const
    const supported = await Promise.all(actions.map(action => handler.canHandle!(target, action)))
    return actions.filter((_, index) => supported[index]).map(action => ({ action, availability: 'available' }))
  }
  else if (!await isScanOwnedTarget(target, config)) {
    return []
  }
  return [{ action: 'identify', availability: 'available' }, { action: 'editMetadata', availability: 'available' }, { action: 'editArtwork', availability: 'available' }, { action: 'editSubtitles', availability: 'available' }, { action: 'refreshMetadata', availability: 'available' }]
}

export async function isScanOwnedTarget(target: MediaActionTarget, config: DataSourceConfig | undefined): Promise<boolean> {
  if (target.kind !== 'media' || !config || !RAW_TYPES.has(config.type))
    return false
  const rootPath = typeof config.extra?.rootPath === 'string' ? config.extra.rootPath : '/'
  const cache = await loadRawSourceScanCache(config.id, config.type as Extract<DataSourceType, 'alist' | 'clouddrive2' | 'webdav' | 'local' | '115' | '123' | 'quark'>, rootPath)
  if (!cache)
    return false
  return cache.candidates.some(candidate => candidate.record.providerPath === target.itemId)
    || getRawScannedMediaDetail(cache, target.itemId) != null
}

async function execute(router: Router, resolveSource: (id: string) => DataSource | null, target: MediaActionTarget, action: MediaActionId): Promise<MediaActionExecutionResult> {
  if (target.kind === 'media' && (action === 'editMetadata' || action === 'editArtwork' || action === 'editSubtitles')) {
    openMediaEditor(target, action)
    return { message: '已打开编辑器' }
  }
  if (target.sourceType === 'emby' || target.sourceType === 'jellyfin') {
    const source = resolveSource(target.sourceId)
    if (target.kind !== 'media' || !source?.refreshMetadata)
      throw new Error('当前媒体服务不支持刷新元数据。')
    await source.refreshMetadata(target.itemId)
  }
  else {
    let handler = getMaintenanceHandler(target.sourceId)
    if (!handler) {
      await router.push({ name: 'source', params: { sourceId: target.sourceId } })
      handler = await waitForMaintenanceHandler(target.sourceId)
    }
    await handler.execute(target, action)
  }
  return { message: action === 'rescanLibrary' ? '媒体库扫描已启动' : action === 'refreshMetadata' ? '元数据刷新已启动' : '已打开本地维护工具', invalidations: [{ sourceId: target.sourceId, scopes: ['source', 'detail', 'home', 'search'] }] }
}
