import type { RawLocalScanCache } from './localScanCache'
import type { RawSeriesEntryGroup } from './rawSeriesGrouping'
import type { TmdbMetadata } from './tmdb'
import type { RawMediaCandidate, RawScrapedMediaItem } from './types'
import type { HomeSection, MediaDetail, MediaItem, MediaSourceOption } from '@/services/datasource/types'
import { getVideoFileExtension } from './pathUtils'
import { toRawScannedMediaItem } from './rawDisplayMapping'
import { createRawSeriesSeasonChildren, groupRawSeriesEntries } from './rawSeriesGrouping'

const RAW_SERIES_ID_PREFIX = 'raw-series:'
const RAW_SEASON_ID_PREFIX = 'raw-season:'
const RAW_HOME_HERO_LIMIT = 8
const RAW_HOME_RECENT_LIMIT = 18

interface RawMatchedDisplayEntry {
  readonly candidate: RawMediaCandidate
  readonly scraped: RawScrapedMediaItem
  readonly metadata: TmdbMetadata
  readonly item: MediaItem
  readonly domain: 'movie' | 'tv'
}

interface RawMatchedWork {
  readonly item: MediaItem
  readonly domain: 'movie' | 'tv'
  readonly metadata: TmdbMetadata
  readonly entries: RawMatchedDisplayEntry[]
  readonly episodes?: MediaItem[]
  readonly seasons?: MediaItem[]
}

interface RawMatchedCatalog {
  readonly works: RawMatchedWork[]
  readonly detailsById: Map<string, MediaDetail>
  readonly childrenById: Map<string, MediaItem[]>
}

export function createRawSourceHomeSections(cache: RawLocalScanCache, sourceName: string): HomeSection[] {
  const catalog = createRawMatchedCatalog(cache)
  if (catalog.works.length === 0)
    return []

  const works = catalog.works.map(work => work.item)
  const heroItems = works
    .filter(isRawHeroCandidate)
    .sort(compareRawHeroItems)
    .slice(0, RAW_HOME_HERO_LIMIT)
  const recentlyAddedItems = [...works]
    .sort(compareRawRecentlyAddedItems)
    .slice(0, RAW_HOME_RECENT_LIMIT)

  const sections: HomeSection[] = [
    {
      id: `raw-hero-${cache.sourceId}`,
      sourceId: cache.sourceId,
      title: `${sourceName} 刮削精选`,
      type: 'hero',
      items: heroItems,
    },
    {
      id: `raw-recent-${cache.sourceId}`,
      sourceId: cache.sourceId,
      title: `${sourceName} 最新入库`,
      type: 'recentlyAdded',
      items: recentlyAddedItems,
    },
  ]

  return sections.filter(section => section.items.length > 0)
}

export function getRawScannedMediaDetail(cache: RawLocalScanCache, id: string): MediaDetail | null {
  return createRawMatchedCatalog(cache).detailsById.get(id) ?? null
}

export function listRawScannedChildren(cache: RawLocalScanCache, id: string): MediaItem[] | null {
  const children = createRawMatchedCatalog(cache).childrenById.get(id)
  return children ? children.map(cloneMediaItem) : null
}

export function isRawScannedSyntheticId(id: string): boolean {
  return id.startsWith(RAW_SERIES_ID_PREFIX) || id.startsWith(RAW_SEASON_ID_PREFIX)
}

function createRawMatchedCatalog(cache: RawLocalScanCache): RawMatchedCatalog {
  const entries = createRawMatchedDisplayEntries(cache)
  const works = [
    ...createMovieWorks(entries.filter(entry => entry.domain === 'movie')),
    ...createSeriesWorks(cache, entries.filter(entry => entry.domain === 'tv')),
  ]
  const detailsById = new Map<string, MediaDetail>()
  const childrenById = new Map<string, MediaItem[]>()

  for (const entry of entries) {
    detailsById.set(entry.item.id, createRawPlayableDetail(entry.item, entry.metadata))
  }

  for (const work of works) {
    if (work.domain === 'tv') {
      const children = work.seasons && work.seasons.length > 0 ? work.seasons : work.episodes ?? []
      childrenById.set(work.item.id, children.map(cloneMediaItem))
      for (const season of work.seasons ?? [])
        childrenById.set(season.id, (season.children ?? []).map(cloneMediaItem))
      detailsById.set(work.item.id, createRawSeriesDetail(work))
      continue
    }

    detailsById.set(work.item.id, createRawPlayableDetail(work.item, work.metadata))
  }

  return { works, detailsById, childrenById }
}

function createRawMatchedDisplayEntries(cache: RawLocalScanCache): RawMatchedDisplayEntry[] {
  const scrapedByRecordId = new Map((cache.scrapedItems ?? []).map(item => [item.recordId, item]))

  return cache.candidates
    .map((candidate): RawMatchedDisplayEntry | null => {
      const scraped = scrapedByRecordId.get(candidate.record.id)
      if (!isHomeEligibleScrapedItem(scraped))
        return null

      const domain = scraped.metadata.mediaType === 'tv' ? 'tv' : 'movie'
      return {
        candidate,
        scraped,
        metadata: scraped.metadata,
        item: toRawScannedMediaItem(candidate, scraped, domain),
        domain,
      }
    })
    .filter((entry): entry is RawMatchedDisplayEntry => entry != null)
}

function isHomeEligibleScrapedItem(scraped: RawScrapedMediaItem | undefined): scraped is RawScrapedMediaItem & { metadata: TmdbMetadata } {
  return scraped?.matchStatus === 'matched'
    && scraped.metadata != null
    && (scraped.metadata.mediaType === 'movie' || scraped.metadata.mediaType === 'tv')
}

function createMovieWorks(entries: readonly RawMatchedDisplayEntry[]): RawMatchedWork[] {
  const groups = new Map<string, RawMatchedDisplayEntry>()

  for (const entry of entries) {
    const key = `tmdb:${entry.metadata.mediaType}:${entry.metadata.tmdbId}`
    const current = groups.get(key)
    if (!current || rawEntryScore(entry) > rawEntryScore(current))
      groups.set(key, entry)
  }

  return [...groups.values()]
    .map(entry => ({
      item: entry.item,
      domain: 'movie' as const,
      metadata: entry.metadata,
      entries: [entry],
    }))
    .sort((left, right) => compareRawRecentlyAddedItems(left.item, right.item))
}

function createSeriesWorks(cache: RawLocalScanCache, entries: readonly RawMatchedDisplayEntry[]): RawMatchedWork[] {
  return groupRawSeriesEntries(entries)
    .map(group => createSeriesWork(cache, group))
    .sort((left, right) => compareRawRecentlyAddedItems(left.item, right.item))
}

function createSeriesWork(
  cache: RawLocalScanCache,
  group: RawSeriesEntryGroup<RawMatchedDisplayEntry>,
): RawMatchedWork {
  const episodes = group.entries
    .map(entry => entry.item)
    .sort(compareRawEpisodeItems)
  const representative = group.representative ?? group.entries[0]
  const metadata = representative.metadata
  const firstEpisode = episodes[0]
  const seasons = createRawSeriesSeasonChildren({
    seriesKey: group.key,
    sourceId: cache.sourceId,
    libraryId: representative.candidate.record.rootPath,
    fallbackPath: firstEpisode?.path ?? representative.candidate.record.providerPath,
    episodes,
    artwork: {
      posterUrl: metadata.posterUrl,
      backdropUrl: metadata.backdropUrl,
      titleLogoUrl: metadata.titleLogoUrl,
    },
  })
  const item: MediaItem = {
    id: `${RAW_SERIES_ID_PREFIX}${encodeURIComponent(group.key)}`,
    sourceId: cache.sourceId,
    libraryId: representative.candidate.record.rootPath,
    name: metadata.title,
    type: 'series',
    posterUrl: metadata.posterUrl ?? firstEpisode?.posterUrl,
    backdropUrl: metadata.backdropUrl ?? firstEpisode?.backdropUrl,
    titleLogoUrl: metadata.titleLogoUrl ?? firstEpisode?.titleLogoUrl,
    year: metadata.releaseYear ?? firstEpisode?.year,
    rating: metadata.rating ?? firstEpisode?.rating,
    overview: metadata.overview || `${episodes.length} 个本地识别分集。`,
    modified: latestModified(episodes),
    path: firstEpisode?.path ?? representative.candidate.record.providerPath,
    children: seasons.length > 0 ? seasons : episodes,
  }

  return {
    item,
    domain: 'tv',
    metadata,
    entries: group.entries,
    episodes,
    seasons,
  }
}

function createRawSeriesDetail(work: RawMatchedWork): MediaDetail {
  return {
    ...cloneMediaItem(work.item),
    type: 'series',
    genres: work.metadata.genres,
    imdbId: work.metadata.imdbId,
    tmdbId: work.metadata.tmdbId,
    mediaSources: [],
    children: (work.seasons && work.seasons.length > 0 ? work.seasons : work.episodes ?? []).map(cloneMediaItem),
    stills: work.item.backdropUrl ? [work.item.backdropUrl] : [],
  }
}

function createRawPlayableDetail(item: MediaItem, metadata: TmdbMetadata): MediaDetail {
  return {
    ...cloneMediaItem(item),
    genres: metadata.genres,
    imdbId: metadata.imdbId,
    tmdbId: metadata.tmdbId,
    mediaSources: [createRawMediaSource(item)],
    stills: [item.backdropUrl, item.posterUrl].filter((url): url is string => Boolean(url)),
  }
}

function createRawMediaSource(item: MediaItem): MediaSourceOption {
  return {
    id: 'default',
    name: '默认版本',
    container: getVideoFileExtension(item.path) ?? undefined,
    size: item.size,
    isRemote: true,
  }
}

function isRawHeroCandidate(item: MediaItem): boolean {
  return Boolean(nonEmpty(item.backdropUrl) && nonEmpty(item.titleLogoUrl) && nonEmpty(item.overview))
}

function compareRawHeroItems(left: MediaItem, right: MediaItem): number {
  return rawHeroScore(right) - rawHeroScore(left)
    || compareRawRecentlyAddedItems(left, right)
}

function rawHeroScore(item: MediaItem): number {
  return (item.backdropUrl ? 4 : 0)
    + (item.titleLogoUrl ? 3 : 0)
    + (item.overview ? 2 : 0)
    + (item.posterUrl ? 1 : 0)
    + (item.rating ?? 0) / 10
}

function compareRawRecentlyAddedItems(left: MediaItem, right: MediaItem): number {
  return rawItemTimestamp(right) - rawItemTimestamp(left)
    || (right.year ?? 0) - (left.year ?? 0)
    || right.name.localeCompare(left.name, 'zh-Hans-CN')
}

function compareRawEpisodeItems(left: MediaItem, right: MediaItem): number {
  return (left.seasonNumber ?? 0) - (right.seasonNumber ?? 0)
    || (left.episodeNumber ?? 0) - (right.episodeNumber ?? 0)
    || left.name.localeCompare(right.name, 'zh-Hans-CN')
}

function rawEntryScore(entry: RawMatchedDisplayEntry): number {
  return rawHeroScore(entry.item) + rawItemTimestamp(entry.item) / 1_000_000_000_000
}

function rawItemTimestamp(item: MediaItem): number {
  const modifiedTime = item.modified ? Date.parse(item.modified) : Number.NaN
  if (Number.isFinite(modifiedTime))
    return modifiedTime
  return item.year ? Date.UTC(item.year, 0, 1) : 0
}

function latestModified(items: readonly MediaItem[]): string | undefined {
  return items
    .map(item => item.modified)
    .filter((value): value is string => Boolean(value))
    .sort((left, right) => Date.parse(right) - Date.parse(left))[0]
}

function cloneMediaItem(item: MediaItem): MediaItem {
  return {
    ...item,
    children: item.children?.map(cloneMediaItem),
  }
}

function nonEmpty(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  return trimmed || undefined
}
