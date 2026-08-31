import type { MediaActionAdapter, MediaActionCapability, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import type { DataSource } from '@/services/datasource/types'
import { getPlaybackProgress, removeContinueWatching, savePlaybackProgress, setPlaybackCompleted } from '@/services/playbackHistory'

export interface PlayedStateAdapterOptions {
  readonly resolveSource: (sourceId: string) => DataSource | null
}

export function createPlayedStateMediaActionAdapter(options: PlayedStateAdapterOptions): MediaActionAdapter {
  return {
    id: 'played-state',
    priority: 100,
    supports: target => target.kind === 'media',
    resolve: target => resolveCapabilities(options, target),
    execute: (target, action) => executePlayedStateAction(options, target, action),
  }
}

async function resolveCapabilities(options: PlayedStateAdapterOptions, target: MediaActionTarget): Promise<readonly MediaActionCapability[]> {
  if (target.kind !== 'media')
    return []
  const provider = options.resolveSource(target.sourceId)
  const providerOwned = target.sourceType === 'emby' || target.sourceType === 'jellyfin'
  if (!providerOwned && ['series', 'season', 'folder'].includes(target.mediaType))
    return []
  if (providerOwned && !provider?.setPlayedState) {
    return [
      { action: 'markPlayed', availability: 'disabled', disabledReason: '当前媒体服务不支持修改已播放状态。' },
      { action: 'markUnplayed', availability: 'disabled', disabledReason: '当前媒体服务不支持修改已播放状态。' },
    ]
  }

  const localEntry = providerOwned
    ? null
    : await getPlaybackProgress({ sourceId: target.sourceId, mediaIdentity: target.itemId })
  const played = target.played ?? localEntry?.completed ?? false
  const capabilities: MediaActionCapability[] = [played
    ? { action: 'markUnplayed', availability: 'available' }
    : { action: 'markPlayed', availability: 'available' }]
  if (target.context === 'continueWatching' && !played)
    capabilities.push({ action: 'removeFromContinueWatching', availability: 'available' })
  return capabilities
}

async function executePlayedStateAction(options: PlayedStateAdapterOptions, target: MediaActionTarget, action: MediaActionId): Promise<MediaActionExecutionResult> {
  if (target.kind !== 'media')
    throw new Error('该对象不支持播放状态操作。')
  const providerOwned = target.sourceType === 'emby' || target.sourceType === 'jellyfin'
  if (!providerOwned && ['series', 'season', 'folder'].includes(target.mediaType))
    throw new Error('聚合媒体的完成态由全部已知可播放子项计算。')
  const provider = options.resolveSource(target.sourceId)
  const mutation = action === 'markPlayed'
    ? 'played'
    : action === 'markUnplayed'
      ? 'unplayed'
      : action === 'removeFromContinueWatching'
        ? 'removeContinueWatching'
        : null
  if (!mutation)
    throw new Error('该播放状态操作当前不可用。')

  if (providerOwned) {
    if (!provider?.setPlayedState)
      throw new Error('当前媒体服务不支持修改已播放状态。')
    await provider.setPlayedState(target.itemId, mutation)
  }
  else if (mutation === 'removeContinueWatching') {
    await removeContinueWatching({ sourceId: target.sourceId, mediaIdentity: target.itemId })
  }
  else if (mutation === 'played') {
    const updated = await setPlaybackCompleted({ sourceId: target.sourceId, mediaIdentity: target.itemId }, true)
    if (!updated) {
      await savePlaybackProgress({
        sourceId: target.sourceId,
        mediaIdentity: target.itemId,
        itemId: target.itemId,
        libraryId: target.libraryId,
        title: target.display.name,
        mediaType: target.mediaType,
        position: 0,
        completed: true,
      })
    }
  }
  else {
    const updated = await setPlaybackCompleted({ sourceId: target.sourceId, mediaIdentity: target.itemId }, false)
    if (!updated) {
      await savePlaybackProgress({
        sourceId: target.sourceId,
        mediaIdentity: target.itemId,
        itemId: target.itemId,
        libraryId: target.libraryId,
        title: target.display.name,
        mediaType: target.mediaType,
        position: 0,
        completed: false,
      })
    }
  }

  return {
    message: mutation === 'played' ? '已标记为已播放' : mutation === 'unplayed' ? '已标记为未播放' : '已移出继续观看',
    invalidations: [{
      sourceId: target.sourceId,
      itemIds: [target.itemId],
      scopes: ['home', 'source', 'detail', 'search', 'history'],
    }],
  }
}
