import type { DataSource, MediaItem } from './types'

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
    return items.slice(0, limitPerSource).map(item => ({
      ...item,
      sourceId: item.sourceId || source.id,
    }))
  }))

  const seen = new Set<string>()
  const results: MediaItem[] = []
  for (const result of settled) {
    if (result.status !== 'fulfilled')
      continue
    for (const item of result.value) {
      const key = `${item.sourceId}:${item.id}`
      if (seen.has(key))
        continue
      seen.add(key)
      results.push(item)
      if (results.length >= limit)
        return results
    }
  }
  return results
}
