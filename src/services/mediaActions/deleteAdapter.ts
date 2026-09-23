import type { MediaActionAdapter, MediaActionConfirmationResult, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import { hideMediaItem } from '@/services/mediaDelete'

export function createDeleteMediaActionAdapter(): MediaActionAdapter {
  return {
    id: 'media-delete',
    priority: 100,
    supports: target => target.kind === 'media',
    resolve: target => target.kind === 'media'
      ? [{
          action: 'deleteMedia',
          availability: 'available',
          danger: 'destructive',
          confirmation: {
            title: '从 Player 媒体库移除',
            message: '只在本机隐藏此条目，不会修改媒体服务器上的文件。',
            confirmLabel: '确认移除',
            cancelLabel: '取消',
            danger: 'destructive',
            requiredText: target.mediaType === 'series' || target.mediaType === 'season' ? target.display.name : undefined,
          },
        }]
      : [],
    execute: executeDelete,
  }
}

async function executeDelete(target: MediaActionTarget, action: MediaActionId, confirmation?: MediaActionConfirmationResult): Promise<MediaActionExecutionResult> {
  if (target.kind !== 'media' || action !== 'deleteMedia' || !confirmation?.confirmed)
    throw new Error('删除确认已失效。')
  await hideMediaItem(target.sourceId, target.itemId)
  return {
    message: '已从 Player 媒体库移除',
    invalidations: [{ sourceId: target.sourceId, itemIds: [target.itemId], scopes: ['home', 'source', 'detail', 'search', 'history'] }],
  }
}
