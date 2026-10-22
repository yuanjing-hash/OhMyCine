import type { SubtitleKeywordMode } from './types'
import type { MediaItem } from '@/services/datasource/types'

type SubtitleMediaItem = Pick<MediaItem, 'name' | 'type' | 'seriesName' | 'seasonNumber' | 'episodeNumber'>

export interface EpisodeSubtitleSearchKeywords {
  number: string
  title: string
}

/** Use provider facts verbatim; an episode's display title is not its series title. */
export function subtitleMediaSearchTitle(item: SubtitleMediaItem): string {
  if (item.type !== 'episode')
    return item.name
  if (!item.seriesName?.trim())
    return ''
  const season = orderLabel(item.seasonNumber, 'S')
  const episode = orderLabel(item.episodeNumber, 'E')
  return [item.seriesName, `${season}${episode}`].filter(Boolean).join(' ')
}

export function subtitleEpisodeSearchKeywords(item: SubtitleMediaItem): EpisodeSubtitleSearchKeywords {
  const series = item.type === 'episode' ? item.seriesName?.trim() : ''
  if (!series)
    return { number: '', title: '' }

  const episode = orderLabel(item.episodeNumber, '')
  // First-season numbering is common in release names. Keep the actual season
  // separately in numeric-mode requests; explicit later/special seasons stay visible.
  const season = item.seasonNumber != null && item.seasonNumber !== 1 ? orderLabel(item.seasonNumber, 'S') : ''
  const number = episode ? `${series} ${season ? `${season}E` : ''}${episode}` : ''
  const title = item.name.trim()
  return { number, title: title && title !== series ? `${series} ${title}` : '' }
}

/** Explicit episode-title and custom queries must not be narrowed back to numeric/ID searches. */
export function subtitleKeywordUsesMediaIdentity(mode: SubtitleKeywordMode | undefined): boolean {
  return mode !== 'episodeTitle' && mode !== 'custom'
}

function orderLabel(value: number | undefined, prefix: string): string {
  return value != null && Number.isInteger(value) && value >= 0
    ? `${prefix}${String(value).padStart(2, '0')}`
    : ''
}
