import type { MediaActionTarget } from './types'
import type { LocalCollectionKind, LocalMediaCollection } from '@/services/mediaCollections'
import { readonly, shallowRef } from 'vue'

export interface CollectionSelectionOption { id: string, name: string, itemCount?: number }
interface Request {
  target: MediaActionTarget
  kind: Exclude<LocalCollectionKind, 'favorite'>
  ownerLabel: string
  load: () => Promise<CollectionSelectionOption[]>
  create: (name: string) => Promise<string>
  resolve: (id: string | null) => void
}
const request = shallowRef<Request | null>(null)
export function requestCollectionSelection(input: Omit<Request, 'resolve'>): Promise<string | null> {
  request.value?.resolve(null)
  return new Promise(resolve => request.value = { ...input, resolve })
}
export function resolveCollectionSelection(id: string | null) {
  const current = request.value
  if (!current)
    return
  request.value = null
  current.resolve(id)
}
export function useCollectionSelectionRuntime() {
  return { request: readonly(request) }
}
export function eligibleCollections(items: readonly LocalMediaCollection[], kind: 'playlist' | 'collection') {
  return items.filter(item => item.kind === kind)
}
