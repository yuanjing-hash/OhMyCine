import type { RawMediaCandidate, RawScrapedMediaItem } from './types'
import type { MediaItem } from '@/services/datasource/types'
import { normalizeTitleKey } from './parser'

export interface RawSeriesGroupableEntry {
  readonly candidate: RawMediaCandidate
  readonly scraped?: RawScrapedMediaItem
}

export interface RawSeriesEntryGroup<T extends RawSeriesGroupableEntry> {
  key: string
  title: string
  entries: T[]
  representative: T
}

export interface CreateRawSeriesSeasonChildrenInput {
  readonly seriesKey: string
  readonly sourceId: string
  readonly libraryId?: string
  readonly fallbackPath?: string
  readonly episodes: readonly MediaItem[]
  readonly artwork?: Pick<MediaItem, 'posterUrl' | 'backdropUrl' | 'titleLogoUrl'>
}

export function createRawSeriesGroupingKey(
  candidate: RawMediaCandidate,
  scraped?: RawScrapedMediaItem,
): string {
  const rawSeriesTitle = candidate.seriesTitle
    ?? (candidate.kind === 'episode' || candidate.kind === 'tv' ? candidate.title : undefined)
  const rawSeriesKey = rawSeriesTitle ? normalizeTitleKey(rawSeriesTitle) : ''
  if (rawSeriesKey)
    return `raw-series:${rawSeriesKey}`

  const metadata = scraped?.metadata ?? candidate.scrapeMetadata
  if (metadata?.mediaType === 'tv')
    return `tmdb:${metadata.mediaType}:${metadata.tmdbId}`

  return `path:${candidate.record.providerPath}`
}

export function groupRawSeriesEntries<T extends RawSeriesGroupableEntry>(
  entries: readonly T[],
): RawSeriesEntryGroup<T>[] {
  const groups = new Map<string, RawSeriesEntryGroup<T>>()

  for (const entry of entries) {
    const key = createRawSeriesGroupingKey(entry.candidate, entry.scraped)
    const current = groups.get(key)
    if (!current) {
      groups.set(key, {
        key,
        title: displayTitleForSeriesEntry(entry),
        entries: [entry],
        representative: entry,
      })
      continue
    }

    current.entries.push(entry)
    if (representativeScore(entry) > representativeScore(current.representative)) {
      current.representative = entry
      current.title = displayTitleForSeriesEntry(entry)
    }
  }

  return [...groups.values()]
}

export function createRawSeriesSeasonChildren(input: CreateRawSeriesSeasonChildrenInput): MediaItem[] {
  if (!input.episodes.some(episode => episode.seasonNumber != null))
    return []

  const groups = new Map<number, MediaItem[]>()
  for (const episode of input.episodes) {
    const seasonNumber = episode.seasonNumber ?? 0
    const current = groups.get(seasonNumber) ?? []
    current.push(episode)
    groups.set(seasonNumber, current)
  }

  return [...groups.entries()]
    .sort(([left], [right]) => left - right)
    .map(([seasonNumber, episodes]) => createSeasonChild(input, seasonNumber, sortEpisodes(episodes)))
}

export function getContextSeriesSeasons(current: Pick<MediaItem, 'children'>): MediaItem[] {
  return (current.children ?? [])
    .filter(item => (item.type === 'season' || item.type === 'folder') && getPlayableSeasonChildren(item).length > 0)
}

export function getContextFlatEpisodes(input: {
  readonly detail: Pick<MediaItem, 'children'>
  readonly relatedItems: readonly MediaItem[]
}): MediaItem[] {
  const detailChildren = (input.detail.children ?? []).filter(isPlayableSeriesEpisode)
  if (detailChildren.length > 0)
    return detailChildren

  return input.relatedItems.filter(isPlayableSeriesEpisode)
}

export function getPlayableSeasonChildren(season: Pick<MediaItem, 'children'>): MediaItem[] {
  return (season.children ?? []).filter(isPlayableSeriesEpisode)
}

function displayTitleForSeriesEntry(entry: RawSeriesGroupableEntry): string {
  const metadata = entry.scraped?.metadata ?? entry.candidate.scrapeMetadata
  return metadata?.title ?? entry.candidate.seriesTitle ?? entry.candidate.title
}

function representativeScore(entry: RawSeriesGroupableEntry): number {
  const metadata = entry.scraped?.metadata ?? entry.candidate.scrapeMetadata
  return (entry.scraped?.matchStatus === 'matched' ? 4 : 0)
    + (metadata ? 3 : 0)
    + (metadata?.posterUrl ? 2 : 0)
    + (metadata?.titleLogoUrl ? 1 : 0)
    + (metadata?.backdropUrl ? 1 : 0)
}

function createSeasonChild(
  input: CreateRawSeriesSeasonChildrenInput,
  seasonNumber: number,
  episodes: readonly MediaItem[],
): MediaItem {
  const firstEpisode = episodes[0]
  const seasonLabel = seasonNumber > 0 ? `Season ${String(seasonNumber).padStart(2, '0')}` : '未分季'
  return {
    id: `raw-season:${encodeURIComponent(input.seriesKey)}:${seasonNumber}`,
    sourceId: input.sourceId,
    libraryId: firstEpisode?.libraryId ?? input.libraryId,
    name: seasonLabel,
    type: 'season',
    posterUrl: input.artwork?.posterUrl ?? firstEpisode?.posterUrl,
    backdropUrl: input.artwork?.backdropUrl ?? firstEpisode?.backdropUrl,
    titleLogoUrl: input.artwork?.titleLogoUrl ?? firstEpisode?.titleLogoUrl,
    year: firstEpisode?.year,
    rating: firstEpisode?.rating,
    overview: `${episodes.length} 集`,
    path: firstEpisode?.path ?? input.fallbackPath ?? '',
    seasonNumber: seasonNumber > 0 ? seasonNumber : undefined,
    children: [...episodes],
  }
}

function isPlayableSeriesEpisode(item: MediaItem): boolean {
  return item.type === 'episode' || item.type === 'file' || item.type === 'movie'
}

function sortEpisodes(episodes: MediaItem[]): MediaItem[] {
  return episodes.sort((a, b) =>
    (a.seasonNumber ?? 0) - (b.seasonNumber ?? 0)
    || (a.episodeNumber ?? 0) - (b.episodeNumber ?? 0)
    || a.name.localeCompare(b.name, 'zh-Hans-CN'))
}
