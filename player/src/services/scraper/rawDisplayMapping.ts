import type { TmdbEpisodeMetadata, TmdbMetadata } from './tmdb'
import type { RawMediaCandidate, RawScrapedMediaItem } from './types'
import type { MediaItem } from '@/services/datasource/types'
import { resolveRawScrapedCategoryAssignment } from './categoryGrouping'
import { isMatchingTmdbEpisodeMetadata } from './tmdb'

export type RawScannedMediaDomain = 'movie' | 'tv' | 'unresolved'

export function toRawScannedMediaItem(
  candidate: RawMediaCandidate,
  scraped: RawScrapedMediaItem | undefined,
  domain: RawScannedMediaDomain,
): MediaItem {
  const metadata = metadataForRawCandidate(candidate, scraped)
  const episodeMetadata = episodeMetadataForRawCandidate(candidate, scraped, domain)
  const mediaType: MediaItem['type'] = domain === 'movie'
    ? 'movie'
    : domain === 'tv'
      ? 'episode'
      : 'file'
  const episodeStillUrl = episodeMetadata?.stillUrl

  return {
    id: candidate.record.providerPath,
    sourceId: candidate.record.sourceId,
    libraryId: candidate.record.rootPath,
    name: rawCandidateDisplayTitle(candidate, scraped, domain),
    type: mediaType,
    posterUrl: episodeStillUrl ?? metadata?.posterUrl,
    backdropUrl: episodeStillUrl ?? metadata?.backdropUrl,
    titleLogoUrl: metadata?.titleLogoUrl,
    year: yearFromDate(episodeMetadata?.airDate) ?? metadata?.releaseYear ?? candidate.year,
    rating: episodeMetadata?.rating ?? metadata?.rating,
    duration: runtimeMinutesToSeconds(episodeMetadata?.runtime),
    size: candidate.record.size,
    modified: candidate.record.modifiedAt,
    path: candidate.record.providerPath,
    seriesName: domain === 'tv' ? metadata?.title ?? candidate.seriesTitle : candidate.seriesTitle,
    seasonNumber: candidate.seasonNumber,
    episodeNumber: candidate.episodeNumber,
    overview: episodeMetadata?.overview || metadata?.overview || rawScannedItemOverview(candidate, scraped),
  }
}

export function metadataForRawCandidate(
  candidate: RawMediaCandidate,
  scraped?: RawScrapedMediaItem,
): TmdbMetadata | undefined {
  return scraped?.metadata ?? candidate.scrapeMetadata
}

export function episodeMetadataForRawCandidate(
  candidate: RawMediaCandidate,
  scraped: RawScrapedMediaItem | undefined,
  domain: RawScannedMediaDomain,
): TmdbEpisodeMetadata | undefined {
  if (domain !== 'tv' || candidate.seasonNumber == null || candidate.episodeNumber == null)
    return undefined
  const metadata = metadataForRawCandidate(candidate, scraped)
  return isMatchingTmdbEpisodeMetadata(
    scraped?.episodeMetadata,
    metadata?.tmdbId,
    candidate.seasonNumber,
    candidate.episodeNumber,
  )
    ? scraped?.episodeMetadata
    : undefined
}

export function rawCandidateDisplayTitle(
  candidate: RawMediaCandidate,
  scraped: RawScrapedMediaItem | undefined,
  domain: RawScannedMediaDomain,
): string {
  const metadataTitle = metadataForRawCandidate(candidate, scraped)?.title
  const episodeName = episodeMetadataForRawCandidate(candidate, scraped, domain)?.name
  if (candidate.kind === 'episode' || domain === 'tv') {
    const episodeLabel = rawEpisodeNumberLabel(candidate)
    if (episodeName)
      return episodeLabel ? `${episodeLabel} · ${episodeName}` : episodeName
    return `${metadataTitle ?? candidate.seriesTitle ?? candidate.title} ${episodeLabel}`.trim()
  }

  return (metadataTitle ?? candidate.title) || candidate.record.fileName
}

export function rawScannedItemOverview(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem): string {
  const parts = [
    scraped?.matchStatus === 'matched' ? 'TMDB：已匹配' : `本地只读扫描：${candidate.parseStatus === 'unresolved' ? '未识别' : '已解析'}`,
    `分类：${categoryNameForRawCandidate(candidate, scraped)}`,
    resolveRawScrapedCategoryAssignment(candidate, scraped).source === 'metadataRule' && scraped?.matchedRuleName ? `规则：${scraped.matchedRuleName}` : undefined,
    scraped?.matchedSearchTitle ? `搜索标题：${scraped.matchedSearchTitle}` : undefined,
    scraped?.matchStatus !== 'matched' && candidate.categoryHint ? `路径提示：${candidate.categoryHint}` : undefined,
    candidate.signals.length ? `信号：${candidate.signals.join(', ')}` : undefined,
  ].filter((part): part is string => Boolean(part))
  return parts.join(' · ')
}

export function categoryNameForRawCandidate(candidate: RawMediaCandidate, scraped?: RawScrapedMediaItem): string {
  return resolveRawScrapedCategoryAssignment(candidate, scraped).categoryName
}

function rawEpisodeNumberLabel(candidate: RawMediaCandidate): string {
  const season = candidate.seasonNumber == null ? '' : `S${String(candidate.seasonNumber).padStart(2, '0')}`
  const episode = candidate.episodeNumber == null ? '' : `E${String(candidate.episodeNumber).padStart(2, '0')}`
  return `${season}${episode}`
}

function runtimeMinutesToSeconds(runtime: number | undefined): number | undefined {
  return runtime != null && runtime > 0 ? runtime * 60 : undefined
}

function yearFromDate(value: string | undefined): number | undefined {
  const year = value ? Number(value.slice(0, 4)) : undefined
  return Number.isInteger(year) ? year : undefined
}
