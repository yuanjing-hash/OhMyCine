import type { MediaItem } from './datasource/types'

export interface SeriesEpisodeSearchEntry {
  season: MediaItem | null
  episode: MediaItem
}

export function searchSeriesEpisodes(
  entries: readonly SeriesEpisodeSearchEntry[],
  keyword: string,
): SeriesEpisodeSearchEntry[] {
  const normalizedKeyword = normalizeEpisodeSearchText(keyword)
  if (!normalizedKeyword)
    return []

  return entries.filter(({ episode }) => normalizeEpisodeSearchText(episodeSearchTitle(episode)).includes(normalizedKeyword))
}

export function episodeSearchTitle(episode: MediaItem): string {
  const title = episode.name.trim()
  if (title)
    return title
  return episode.episodeNumber == null ? '未命名单集' : `第 ${episode.episodeNumber} 集`
}

function normalizeEpisodeSearchText(value: string): string {
  return value.normalize('NFKC').trim().toLocaleLowerCase()
}
