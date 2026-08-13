import type { MediaActionAdapter, MediaActionCapability, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import type { DataSource } from '@/services/datasource/types'
import { addLocalCollectionMember, createLocalMediaCollection, listLocalMediaCollections, setLocalFavorite } from '@/services/mediaCollections'
import { requestCollectionSelection } from './collectionRuntime'

export function createCollectionMediaActionAdapter(resolveSource: (id: string) => DataSource | null): MediaActionAdapter {
  return {
    id: 'collections',
    priority: 90,
    supports: target => target.kind === 'media',
    resolve: async target => resolveCapabilities(resolveSource, target),
    execute: (target, action) => execute(resolveSource, target, action),
  }
}
async function resolveCapabilities(resolveSource: (id: string) => DataSource | null, target: MediaActionTarget): Promise<MediaActionCapability[]> {
  if (target.kind !== 'media')
    return []
  if (target.sourceType === 'emby' || target.sourceType === 'jellyfin') {
    const source = resolveSource(target.sourceId)
    let favorite: boolean | undefined
    try {
      if (!source?.getFavoriteState)
        throw new Error('Favorite state query is unavailable.')
      favorite = await source.getFavoriteState(target.itemId)
    }
    catch {
      if (target.favorite === true) {
        favorite = true
      }
      else {
        return [
          { action: 'favorite', availability: 'disabled', disabledReason: '暂时无法从媒体服务确认收藏状态，请稍后重试。' },
          { action: 'addToPlaylist', availability: 'available' },
          { action: 'addToCollection', availability: 'available' },
        ]
      }
    }
    return [
      { action: favorite ? 'unfavorite' : 'favorite', availability: 'available' },
      { action: 'addToPlaylist', availability: 'available' },
      { action: 'addToCollection', availability: 'available' },
    ]
  }
  const collections = await listLocalMediaCollections()
  const favorite = collections.find(c => c.kind === 'favorite')?.members.some(m => m.sourceId === target.sourceId && m.itemId === target.itemId) ?? false
  return [{ action: favorite ? 'unfavorite' : 'favorite', availability: 'available' }, { action: 'addToPlaylist', availability: 'available' }, { action: 'addToCollection', availability: 'available' }]
}
async function execute(resolveSource: (id: string) => DataSource | null, target: MediaActionTarget, action: MediaActionId): Promise<MediaActionExecutionResult> {
  if (target.kind !== 'media')
    throw new Error('该对象不支持集合操作。')
  const providerOwned = target.sourceType === 'emby' || target.sourceType === 'jellyfin'
  if (providerOwned) {
    const source = resolveSource(target.sourceId)
    if (action === 'favorite' || action === 'unfavorite') {
      if (!source?.setFavorite)
        throw new Error('当前媒体服务不支持收藏操作。')
      await source.setFavorite(target.itemId, action === 'favorite')
    }
    else {
      const kind = action === 'addToPlaylist' ? 'playlist' : action === 'addToCollection' ? 'collection' : null
      if (!kind || !source?.listProviderCollections || !source.createProviderCollection || !source.addProviderCollectionMember)
        throw new Error('当前媒体服务不支持该集合操作。')
      const id = await requestCollectionSelection({
        target,
        kind,
        ownerLabel: `${target.display.sourceName ?? '媒体服务'} · 提供方原生`,
        load: () => source.listProviderCollections!(kind),
        create: name => source.createProviderCollection!(name, kind),
      })
      if (!id)
        return { message: '已取消' }
      await source.addProviderCollectionMember(id, target.itemId, kind)
    }
  }
  else if (action === 'favorite' || action === 'unfavorite') {
    await setLocalFavorite(target, action === 'favorite')
  }
  else {
    const kind = action === 'addToPlaylist' ? 'playlist' : action === 'addToCollection' ? 'collection' : null
    if (!kind)
      throw new Error('集合操作不可用。')
    const id = await requestCollectionSelection({
      target,
      kind,
      ownerLabel: 'Player 本地 · 可跨来源',
      load: async () => (await listLocalMediaCollections()).filter(item => item.kind === kind).map(item => ({ id: item.id, name: item.name, itemCount: item.members.length })),
      create: name => createLocalMediaCollection(name, kind),
    })
    if (!id)
      return { message: '已取消' }
    await addLocalCollectionMember(id, target)
  }
  return { message: action === 'favorite' ? '已收藏' : action === 'unfavorite' ? '已取消收藏' : '已添加到集合', invalidations: [{ sourceId: target.sourceId, itemIds: [target.itemId], scopes: ['collections', 'home', 'detail', 'search'] }] }
}
