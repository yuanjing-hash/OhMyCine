import type { MediaActionTarget } from '@/services/mediaActions'
import { invoke } from '@tauri-apps/api/core'

export const COLLECTIONS_CHANGED_EVENT = 'ohmycine:collections-changed'

export type LocalCollectionKind = 'favorite' | 'playlist' | 'collection'
export interface LocalCollectionMember { sourceId: string, itemId: string, title: string, mediaType: string, posterUrl?: string | null, backdropUrl?: string | null, position: number, missing?: boolean }
export interface LocalMediaCollection { id: string, name: string, kind: LocalCollectionKind, members: LocalCollectionMember[] }

export function listLocalMediaCollections(): Promise<LocalMediaCollection[]> {
  return invoke('player_list_media_collections')
}
export function createLocalMediaCollection(name: string, kind: Exclude<LocalCollectionKind, 'favorite'>): Promise<string> {
  return invoke('player_create_media_collection', { name, kind })
}
export function deleteLocalMediaCollection(id: string): Promise<boolean> {
  return invoke('player_delete_media_collection', { id })
}
export function setLocalFavorite(target: MediaActionTarget, favorite: boolean): Promise<boolean> {
  return invoke('player_set_local_favorite', { member: member(target), favorite })
}
export function addLocalCollectionMember(collectionId: string, target: MediaActionTarget): Promise<boolean> {
  return invoke('player_add_media_collection_member', { collectionId, member: member(target) })
}
export function removeLocalCollectionMember(collectionId: string, sourceId: string, itemId: string): Promise<boolean> {
  return invoke('player_remove_media_collection_member', { collectionId, sourceId, itemId })
}
export function annotateMissingCollectionSources(collections: readonly LocalMediaCollection[], availableSourceIds: ReadonlySet<string>): LocalMediaCollection[] {
  return collections.map(collection => ({ ...collection, members: collection.members.map(member => ({ ...member, missing: !availableSourceIds.has(member.sourceId) })) }))
}

function member(target: MediaActionTarget) {
  if (target.kind !== 'media')
    throw new Error('媒体库对象不能加入本地媒体集合。')
  return { sourceId: target.sourceId, itemId: target.itemId, title: target.display.name, mediaType: target.mediaType }
}
