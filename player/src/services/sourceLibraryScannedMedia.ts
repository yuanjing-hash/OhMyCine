import type { HomeSection, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { RawMediaCandidate, RawScannedMediaDomain, RawScrapedMediaItem, RawSeriesEntryGroup, RawSourceIndexStatus, TmdbMetadata } from '@/services/scraper'
import {
  createRawSeriesGroupingKey,
  createRawSeriesSeasonChildren,
  groupRawSeriesEntries,
  metadataForRawCandidate,
  RAW_MOVIE_CATEGORY_NAME,
  RAW_TV_CATEGORY_NAME,
  RAW_UNRESOLVED_CATEGORY_NAME,
} from '@/services/scraper'

export type ScannedCategoryType = 'movie' | 'tv' | 'unresolved' | 'mixed'
export type ScannedMediaDomain = RawScannedMediaDomain

export interface ScannedDisplayItem {
  readonly item: MediaItem
  readonly candidate: RawMediaCandidate
  readonly scraped?: RawScrapedMediaItem
  readonly categoryName: string
  readonly domain: ScannedMediaDomain
}

export interface ScannedSeriesWork {
  readonly key: string
  readonly title: string
  readonly item: MediaItem
  readonly entries: ScannedDisplayItem[]
  readonly episodes: MediaItem[]
  readonly seasons: MediaItem[]
}

export interface ScannedWorkItem {
  readonly item: MediaItem
  readonly domain: ScannedMediaDomain
  readonly entries: ScannedDisplayItem[]
  readonly episodes?: MediaItem[]
  readonly seasons?: MediaItem[]
}

export interface ScannedCategory {
  readonly id: string
  readonly name: string
  readonly type: ScannedCategoryType
  readonly entries: ScannedDisplayItem[]
  readonly works: ScannedWorkItem[]
  readonly library: MediaLibrary
  readonly previewItems: MediaItem[]
  readonly count: number
  readonly fileCount: number
  readonly movieCount: number
  readonly tvCount: number
  readonly unresolvedCount: number
  readonly seriesCount: number
  readonly subtitle: string
  readonly previewTitles: string[]
}

interface ScannedCategoryInput {
  readonly sourceId: string
  readonly rootPath: string
  readonly name: string
  readonly entries: ScannedDisplayItem[]
}

export function createScannedCategory(input: ScannedCategoryInput): ScannedCategory {
  const { sourceId, rootPath, name, entries } = input
  const movieCount = entries.filter(entry => entry.domain === 'movie').length
  const tvEntries = entries.filter(entry => entry.domain === 'tv')
  const unresolvedCount = entries.filter(entry => entry.domain === 'unresolved').length
  const seriesCount = seriesCountForEntries(tvEntries)
  const type = name === RAW_UNRESOLVED_CATEGORY_NAME ? 'unresolved' : scannedCategoryType(movieCount, tvEntries.length, unresolvedCount)
  const works = createScannedWorkItems(entries, sourceId, rootPath)
  const previewItems = works.map(work => work.item).slice(0, 4)
  const previewArtwork = previewItems.find(item => item.backdropUrl || item.posterUrl)
  const library: MediaLibrary = {
    id: `category:${encodeURIComponent(name)}`,
    sourceId,
    name,
    type: mediaLibraryTypeForCategory(type),
    posterUrl: previewArtwork?.posterUrl,
    backdropUrl: previewArtwork?.backdropUrl ?? previewArtwork?.posterUrl,
    itemCount: works.length,
  }

  return {
    id: library.id,
    name,
    type,
    entries,
    works,
    library,
    previewItems,
    count: works.length,
    fileCount: entries.length,
    movieCount,
    tvCount: tvEntries.length,
    unresolvedCount,
    seriesCount,
    subtitle: scannedCategorySubtitle({ categoryName: name, fileCount: entries.length, movieCount, tvCount: tvEntries.length, unresolvedCount, seriesCount }),
    previewTitles: uniqueDisplayTitles(entries).slice(0, 3),
  }
}

export function playableItemsFromWorks(works: readonly ScannedWorkItem[]): MediaItem[] {
  return works.flatMap((work) => {
    if (work.domain === 'tv')
      return work.episodes ?? []
    return work.item.type === 'series' ? [] : [work.item]
  })
}

export function compareScannedCategories(a: ScannedCategory, b: ScannedCategory): number {
  return scannedCategorySortPriority(a) - scannedCategorySortPriority(b)
    || b.count - a.count
    || a.name.localeCompare(b.name, 'zh-Hans-CN')
}

export function domainForScannedEntry(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem): ScannedMediaDomain {
  const metadata = metadataForCandidate(candidate, scraped)
  if (metadata?.mediaType === 'movie' || scraped?.mediaType === 'movie')
    return 'movie'
  if (metadata?.mediaType === 'tv' || scraped?.mediaType === 'tv')
    return 'tv'
  if (candidate.kind === 'movie')
    return 'movie'
  if (candidate.kind === 'episode' || candidate.kind === 'tv')
    return 'tv'
  return 'unresolved'
}

export function metadataForCandidate(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem): TmdbMetadata | undefined {
  return metadataForRawCandidate(candidate, scraped)
}

export function compareScannedMediaItems(a: MediaItem, b: MediaItem): number {
  return (a.seasonNumber ?? 0) - (b.seasonNumber ?? 0)
    || (a.episodeNumber ?? 0) - (b.episodeNumber ?? 0)
    || a.name.localeCompare(b.name, 'zh-Hans-CN')
}

export function compareHeroScannedItems(a: MediaItem, b: MediaItem): number {
  const artworkScore = (item: MediaItem) => (item.backdropUrl ? 2 : 0) + (item.posterUrl ? 1 : 0) + (item.overview ? 0.5 : 0)
  return artworkScore(b) - artworkScore(a)
    || (b.rating ?? 0) - (a.rating ?? 0)
    || (b.year ?? 0) - (a.year ?? 0)
    || a.name.localeCompare(b.name, 'zh-Hans-CN')
}

export function findVisibleHomeSection(homeSections: readonly HomeSection[], type: 'hero' | 'continueWatching' | 'recentlyAdded'): HomeSection | undefined {
  return homeSections.find(section => section.type === type && section.items.length > 0)
}

export function isContainerItem(item: MediaItem): boolean {
  return item.type === 'folder' || item.type === 'series' || item.type === 'season'
}

export function formatRawIndexStatus(status: RawSourceIndexStatus | null, fallback: string): string {
  switch (status?.state) {
    case 'disabled': return '已停用'
    case 'cooldown': return '等待下次'
    case 'queued': return '准备扫描'
    case 'running': return '扫描中'
    case 'completed': return '已完成'
    case 'failed': return '失败'
    case 'idle':
    default: return fallback
  }
}

export function formatRawIndexTime(status: RawSourceIndexStatus | null): string {
  const value = status?.lastSuccessAt ?? status?.lastAttemptAt ?? status?.lastFailureAt
  if (!value)
    return '暂无记录'
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp))
    return '暂无记录'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(timestamp))
}

export function labelForSourceType(type: string): string {
  const labels: Record<string, string> = {
    emby: 'Emby',
    alist: 'OpenList/Alist',
    jellyfin: 'Jellyfin',
    clouddrive2: 'CloudDrive2',
    webdav: 'WebDAV',
    quark: '夸克网盘',
    123: '123 云盘',
    local: '本地文件',
    server: 'OhMyCine Server',
  }
  return labels[type] ?? type
}

function createScannedWorkItems(entries: readonly ScannedDisplayItem[], sourceId: string, rootPath: string): ScannedWorkItem[] {
  return [
    ...createDedupedFileWorks(entries.filter(entry => entry.domain === 'movie'), 'movie'),
    ...createSeriesWorks(entries.filter(entry => entry.domain === 'tv'), sourceId, rootPath).map(work => ({
      item: work.item,
      domain: 'tv' as const,
      entries: work.entries,
      episodes: work.episodes,
      seasons: work.seasons,
    })),
    ...entries.filter(entry => entry.domain === 'unresolved').map(entry => ({
      item: entry.item,
      domain: 'unresolved' as const,
      entries: [entry],
    })),
  ]
}

function createDedupedFileWorks(entries: readonly ScannedDisplayItem[], domain: Exclude<ScannedMediaDomain, 'tv'>): ScannedWorkItem[] {
  const groups = new Map<string, ScannedDisplayItem>()
  for (const entry of entries) {
    const metadata = metadataForCandidate(entry.candidate, entry.scraped)
    const key = metadata
      ? `tmdb:${metadata.mediaType}:${metadata.tmdbId}`
      : `${entry.candidate.normalizedTitle || entry.item.id}:${entry.candidate.year ?? ''}`
    if (!groups.has(key))
      groups.set(key, entry)
  }

  return [...groups.values()]
    .map(entry => ({ item: entry.item, domain, entries: [entry] }))
    .sort((a, b) => compareScannedMediaItems(a.item, b.item))
}

function createSeriesWorks(entries: readonly ScannedDisplayItem[], sourceId: string, rootPath: string): ScannedSeriesWork[] {
  return groupRawSeriesEntries(entries)
    .map(group => createSeriesWork(group, sourceId, rootPath))
    .sort((a, b) => a.title.localeCompare(b.title, 'zh-Hans-CN'))
}

function createSeriesWork(group: RawSeriesEntryGroup<ScannedDisplayItem>, sourceId: string, rootPath: string): ScannedSeriesWork {
  const episodes = group.entries.map(entry => entry.item).sort(compareScannedMediaItems)
  const representative = group.representative ?? group.entries[0]
  const metadata = representative ? metadataForCandidate(representative.candidate, representative.scraped) : undefined
  const firstEpisode = episodes[0]
  const title = metadata?.title ?? group.title ?? '剧集'
  const seasons = createRawSeriesSeasonChildren({
    seriesKey: group.key,
    sourceId,
    libraryId: representative?.candidate.record.rootPath ?? rootPath,
    fallbackPath: firstEpisode?.path ?? representative?.candidate.record.providerPath,
    episodes,
    artwork: {
      posterUrl: metadata?.posterUrl,
      backdropUrl: metadata?.backdropUrl,
      titleLogoUrl: metadata?.titleLogoUrl,
    },
  })
  const item: MediaItem = {
    id: `raw-series:${encodeURIComponent(group.key)}`,
    sourceId,
    libraryId: representative?.candidate.record.rootPath ?? rootPath,
    name: title,
    type: 'series',
    posterUrl: metadata?.posterUrl ?? firstEpisode?.posterUrl,
    backdropUrl: metadata?.backdropUrl ?? firstEpisode?.backdropUrl,
    titleLogoUrl: metadata?.titleLogoUrl ?? firstEpisode?.titleLogoUrl,
    year: metadata?.releaseYear ?? firstEpisode?.year,
    rating: metadata?.rating ?? firstEpisode?.rating,
    overview: metadata?.overview || `${episodes.length} 个本地识别分集。`,
    path: firstEpisode?.path ?? representative?.candidate.record.providerPath ?? '',
    children: seasons.length > 0 ? seasons : episodes,
  }

  return { key: group.key, title, item, entries: group.entries, episodes, seasons }
}

function scannedCategoryType(movieCount: number, tvCount: number, unresolvedCount: number): ScannedCategoryType {
  const activeTypes = [
    movieCount > 0 ? 'movie' : null,
    tvCount > 0 ? 'tv' : null,
    unresolvedCount > 0 ? 'unresolved' : null,
  ].filter(Boolean)
  return activeTypes.length === 1 ? activeTypes[0] as ScannedCategoryType : 'mixed'
}

function mediaLibraryTypeForCategory(type: ScannedCategoryType): MediaLibrary['type'] {
  if (type === 'movie')
    return 'movies'
  if (type === 'tv')
    return 'series'
  if (type === 'mixed')
    return 'mixed'
  return 'folders'
}

function scannedCategorySubtitle(counts: { categoryName: string, fileCount: number, movieCount: number, tvCount: number, unresolvedCount: number, seriesCount: number }): string {
  if (counts.categoryName === RAW_UNRESOLVED_CATEGORY_NAME) {
    return [
      `${counts.fileCount} 个待识别文件`,
      counts.seriesCount ? `${counts.seriesCount} 组剧集候选` : undefined,
    ].filter((part): part is string => Boolean(part)).join(' · ')
  }

  return [
    counts.movieCount ? `${counts.movieCount} 部影片` : undefined,
    counts.tvCount ? `${counts.seriesCount || counts.tvCount} 部剧集` : undefined,
    counts.unresolvedCount ? `${counts.unresolvedCount} 个未识别` : undefined,
  ].filter((part): part is string => Boolean(part)).join(' · ') || '暂无项目'
}

function seriesCountForEntries(entries: readonly ScannedDisplayItem[]): number {
  return new Set(entries.map(entry => createRawSeriesGroupingKey(entry.candidate, entry.scraped))).size
}

function uniqueDisplayTitles(entries: readonly ScannedDisplayItem[]): string[] {
  const titles: string[] = []
  const seen = new Set<string>()
  for (const entry of entries) {
    const title = metadataForCandidate(entry.candidate, entry.scraped)?.title ?? entry.candidate.seriesTitle ?? entry.candidate.title ?? entry.item.name
    const normalized = title.trim().toLocaleLowerCase()
    if (!normalized || seen.has(normalized))
      continue
    seen.add(normalized)
    titles.push(title)
  }
  return titles
}

function scannedCategorySortPriority(category: ScannedCategory): number {
  if (category.name === RAW_UNRESOLVED_CATEGORY_NAME)
    return 90
  if (category.name === '未分类')
    return 80
  if (category.name === RAW_MOVIE_CATEGORY_NAME)
    return 60
  if (category.name === RAW_TV_CATEGORY_NAME)
    return 61
  return 20
}
