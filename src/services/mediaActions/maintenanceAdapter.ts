import type { Router } from 'vue-router'
import type { MediaActionAdapter, MediaActionCapability, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import type { DataSource, DataSourceConfig } from '@/services/datasource/types'
import { openMediaEditor } from './editorRuntime'

export function createMaintenanceMediaActionAdapter(
  _router: Router,
  resolveSource: (id: string) => DataSource | null,
  _resolveConfig?: (id: string) => DataSourceConfig | undefined,
): MediaActionAdapter {
  return {
    id: 'maintenance',
    priority: 80,
    supports: target => target.sourceType === 'emby' || target.sourceType === 'jellyfin',
    resolve: target => capabilities(target, resolveSource(target.sourceId)),
    execute: (target, action) => execute(resolveSource, target, action),
  }
}

function capabilities(target: MediaActionTarget, source: DataSource | null): MediaActionCapability[] {
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

async function execute(
  resolveSource: (id: string) => DataSource | null,
  target: MediaActionTarget,
  action: MediaActionId,
): Promise<MediaActionExecutionResult> {
  if (target.kind !== 'media')
    throw new Error('此对象不支持媒体维护。')
  if (action === 'editMetadata' || action === 'editArtwork' || action === 'editSubtitles') {
    openMediaEditor(target, action)
    return { message: '已打开编辑器' }
  }
  const source = resolveSource(target.sourceId)
  if (action !== 'refreshMetadata' || !source?.refreshMetadata)
    throw new Error('当前媒体服务不支持刷新元数据。')
  await source.refreshMetadata(target.itemId)
  return {
    message: '元数据刷新已启动',
    invalidations: [{ sourceId: target.sourceId, scopes: ['source', 'detail', 'home', 'search'] }],
  }
}
