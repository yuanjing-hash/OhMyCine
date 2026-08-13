import type { Router } from 'vue-router'
import type { MediaActionAdapter, MediaActionCapability, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import { savePlaybackMediaContext } from '@/services/playbackContext'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'

const PLAYABLE_TYPES = new Set(['movie', 'episode', 'file'])

export function createNavigationMediaActionAdapter(router: Router): MediaActionAdapter {
  return {
    id: 'player-navigation',
    priority: -100,
    supports: () => true,
    resolve: resolveNavigationCapabilities,
    execute: async (target, action) => executeNavigationAction(router, target, action),
  }
}

function resolveNavigationCapabilities(target: MediaActionTarget): readonly MediaActionCapability[] {
  if (target.kind === 'library')
    return [{ action: 'openLibrary', availability: 'available' }]

  const capabilities: MediaActionCapability[] = []
  if (PLAYABLE_TYPES.has(target.mediaType))
    capabilities.push({ action: 'play', availability: 'available' })
  if (target.mediaType !== 'folder')
    capabilities.push({ action: 'viewDetails', availability: 'available' })
  return capabilities
}

async function executeNavigationAction(router: Router, target: MediaActionTarget, action: MediaActionId): Promise<MediaActionExecutionResult> {
  if (target.kind === 'library' && action === 'openLibrary') {
    await router.push({ name: 'source', params: { sourceId: target.sourceId } })
    return { message: `已打开${target.display.name}` }
  }

  if (target.kind !== 'media')
    throw new Error('该对象不支持此操作。')

  if (action === 'viewDetails') {
    await router.push({ name: 'media-detail', params: { sourceId: target.sourceId, itemId: target.itemId } })
    return { message: `已打开${target.display.name}` }
  }

  if (action === 'play' && PLAYABLE_TYPES.has(target.mediaType)) {
    const contextId = savePlaybackMediaContext({
      sourceId: target.sourceId,
      itemId: target.itemId,
      title: target.display.name,
    })
    await router.push({
      name: 'player',
      query: createPlaybackRouteQuery({ sourceId: target.sourceId, itemId: target.itemId, contextId }),
    })
    return { message: `正在播放${target.display.name}` }
  }

  throw new Error('该操作当前不可用。')
}
