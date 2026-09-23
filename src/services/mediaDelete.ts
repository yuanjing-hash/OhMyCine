import type { DataSource, HomeSection, MediaDetail, MediaItem } from '@/services/datasource/types'
import { getAppSetting, setAppSetting } from '@/services/appSettings'

const TOMBSTONE_KEY = 'ohmycine-media-tombstones-v1'

export async function hideMediaItem(sourceId: string, itemId: string): Promise<void> {
  await addMediaTombstones(sourceId, [itemId])
}

export function withMediaTombstoneFiltering(source: DataSource): DataSource {
  return new Proxy(source, {
    get(target, property, receiver) {
      const method = Reflect.get(target, property, receiver) as unknown
      if (property === 'list' || property === 'search' || property === 'getFeaturedItems' || property === 'getContinueWatching' || property === 'getRecentlyAdded') {
        if (typeof method !== 'function')
          return method
        return async (...args: unknown[]) => filterMediaItems(source.id, await Reflect.apply(method, target, args) as MediaItem[])
      }
      if (property === 'getHomeSections') {
        if (typeof method !== 'function')
          return method
        return async (...args: unknown[]) => filterHomeSections(source.id, await Reflect.apply(method, target, args) as HomeSection[])
      }
      if (property === 'getDetail') {
        return async (...args: unknown[]) => {
          const detail = await Reflect.apply(Reflect.get(target, property, receiver), target, args) as MediaDetail
          if (isMediaTombstoned(source.id, detail.id))
            throw new Error('此条目已从 Player 媒体库移除。')
          return filterTombstonedMediaDetail(detail, item => isMediaTombstoned(item.sourceId, item.id))
        }
      }
      return typeof method === 'function' ? method.bind(target) : method
    },
  })
}

export function isMediaTombstoned(sourceId: string, itemId: string): boolean {
  return loadTombstones().has(`${sourceId}\0${itemId}`)
}

export async function pruneMediaTombstonesForSources(sourceIds: readonly string[]): Promise<void> {
  const ids = new Set(sourceIds)
  if (ids.size === 0)
    return
  const tombstones = [...loadTombstones()]
  const retained = tombstones.filter(value => !ids.has(value.split('\0', 1)[0]))
  if (retained.length !== tombstones.length)
    await setAppSetting(TOMBSTONE_KEY, JSON.stringify(retained))
}

async function addMediaTombstones(sourceId: string, itemIds: readonly string[]) {
  const tombstones = loadTombstones()
  for (const itemId of itemIds)
    tombstones.add(`${sourceId}\0${itemId}`)
  await setAppSetting(TOMBSTONE_KEY, JSON.stringify([...tombstones]))
}

function loadTombstones(): Set<string> {
  try {
    const value = JSON.parse(getAppSetting(TOMBSTONE_KEY) ?? '[]') as unknown
    return new Set(Array.isArray(value) ? value.filter(item => typeof item === 'string') : [])
  }
  catch {
    return new Set()
  }
}

function filterMediaItems(sourceId: string, items: MediaItem[]): MediaItem[] {
  return items.filter(item => !isMediaTombstoned(sourceId, item.id))
}

function filterHomeSections(sourceId: string, sections: HomeSection[]): HomeSection[] {
  return sections.map(section => ({ ...section, items: filterMediaItems(sourceId, section.items) })).filter(section => section.items.length)
}

export function filterTombstonedMediaDetail(detail: MediaDetail, isHidden: (item: MediaItem) => boolean): MediaDetail {
  const filterNested = (items: MediaItem[] | undefined): MediaItem[] | undefined => items
    ?.filter(item => !isHidden(item))
    .map(item => ({ ...item, children: filterNested(item.children) }))
  return {
    ...detail,
    children: filterNested(detail.children),
    similarItems: filterNested(detail.similarItems),
    collections: filterNested(detail.collections),
  }
}
