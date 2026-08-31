import type { DataSource, MediaItem } from './types'
import { mergeMediaItemsByIdentity } from './identityMerge'

const TOP_LEVEL_SEARCH_TYPES = new Set<MediaItem['type']>(['movie', 'series', 'folder', 'file'])

export function normalizeWorkLevelSearchResults(items: readonly MediaItem[]): MediaItem[] {
  return items.filter(item => TOP_LEVEL_SEARCH_TYPES.has(item.type))
}

export async function searchAcrossDataSources(
  sources: readonly DataSource[],
  keyword: string,
  options: { limitPerSource?: number, limit?: number } = {},
): Promise<MediaItem[]> {
  const normalizedKeyword = keyword.trim()
  if (!normalizedKeyword)
    return []

  const limitPerSource = Math.max(1, options.limitPerSource ?? 18)
  const limit = Math.max(1, options.limit ?? 60)
  const settled = await Promise.allSettled(sources.map(async (source) => {
    const items = await source.search(normalizedKeyword)
    return normalizeWorkLevelSearchResults(items).slice(0, limitPerSource).map(item => ({
      ...item,
      sourceId: item.sourceId || source.id,
    }))
  }))

  const results: MediaItem[] = []
  for (const result of settled) {
    if (result.status !== 'fulfilled')
      continue
    for (const item of result.value) {
      results.push(item)
    }
  }
  return mergeMediaItemsByIdentity(results).slice(0, limit)
}
