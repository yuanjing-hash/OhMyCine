import type { RawLocalScanCache, RawLocalScanLogEntry } from './localScanCache'
import type { TmdbEpisodeMetadata, TmdbImageKind, TmdbMetadata } from './tmdb'
import type { RawCategoryAssignment, RawMediaCandidate, RawScrapedMediaItem } from './types'
import { resolveRawCandidateCategoryAssignment } from './categoryGrouping'
import { classifyScrapeMetadata, loadScrapeClassificationRules } from './classificationRules'
import { createRawSeriesGroupingKey } from './rawSeriesGrouping'
import { extractCandidateTmdbSearchTitles, isMatchingTmdbEpisodeMetadata } from './tmdb'

export interface RawManualIdentificationInput {
  readonly targetRecordId: string
  readonly metadata: TmdbMetadata
  readonly matchedSearchTitle?: string
  readonly searchTitles?: readonly string[]
}

export interface RawManualArtworkOverrideInput {
  readonly targetRecordId: string
  readonly kind: Extract<TmdbImageKind, 'poster' | 'logo' | 'backdrop'>
  readonly imageUrl?: string
  readonly filePath?: string
}

interface RawSeriesCandidateGroup {
  readonly key: string
  readonly candidates: RawMediaCandidate[]
}

export function createEffectiveRawScrapeItemMap(
  candidates: readonly RawMediaCandidate[],
  scrapedItems: readonly RawScrapedMediaItem[] | undefined,
): Map<string, RawScrapedMediaItem> {
  const scrapedByRecordId = new Map((scrapedItems ?? []).map(item => [item.recordId, item]))

  for (const group of createRawSeriesCandidateGroups(candidates)) {
    const representative = findMatchedRepresentative(group.candidates, scrapedByRecordId)
    if (!representative?.metadata)
      continue

    for (const candidate of group.candidates) {
      const existing = scrapedByRecordId.get(candidate.record.id)
      if (existing?.matchStatus === 'matched')
        continue

      scrapedByRecordId.set(candidate.record.id, createMatchedScrapedItem(candidate, representative.metadata, {
        matchedSearchTitle: representative.matchedSearchTitle,
        episodeMetadata: matchingEpisodeMetadataForCandidate(existing?.episodeMetadata, representative.metadata, candidate),
        searchTitles: uniqueSearchTitles([
          ...(existing?.searchTitles ?? []),
          ...representative.searchTitles,
          ...extractCandidateTmdbSearchTitles(candidate),
        ]),
      }))
    }
  }

  return scrapedByRecordId
}

export function applyRawManualIdentification(
  cache: RawLocalScanCache,
  input: RawManualIdentificationInput,
): RawLocalScanCache {
  const target = cache.candidates.find(candidate => candidate.record.id === input.targetRecordId)
  if (!target)
    throw new Error('未找到需要识别的本地候选。')

  const targetGroupKey = createRawSeriesGroupingKey(target)
  const targetCandidates = cache.candidates.filter(candidate => createRawSeriesGroupingKey(candidate) === targetGroupKey)
  const existingItems = new Map((cache.scrapedItems ?? []).map(item => [item.recordId, item]))
  const searchTitles = uniqueSearchTitles([
    ...(input.searchTitles ?? []),
    input.matchedSearchTitle,
    ...extractCandidateTmdbSearchTitles(target),
  ])

  for (const candidate of targetCandidates) {
    const existing = existingItems.get(candidate.record.id)
    existingItems.set(candidate.record.id, createMatchedScrapedItem(candidate, input.metadata, {
      matchedSearchTitle: input.matchedSearchTitle,
      episodeMetadata: matchingEpisodeMetadataForCandidate(existing?.episodeMetadata, input.metadata, candidate),
      searchTitles,
    }))
  }

  const scrapedItems = cache.candidates
    .map(candidate => existingItems.get(candidate.record.id))
    .filter((item): item is RawScrapedMediaItem => item != null)
  const scrapedItemsByRecordId = new Map(scrapedItems.map(item => [item.recordId, item]))
  const candidates = cache.candidates.map((candidate) => {
    const scraped = scrapedItemsByRecordId.get(candidate.record.id)
    return {
      ...candidate,
      scrapeMetadata: scraped?.metadata ?? candidate.scrapeMetadata,
      categoryAssignment: scraped?.categoryAssignment ?? candidate.categoryAssignment,
    } satisfies RawMediaCandidate
  })

  return {
    ...cache,
    candidates,
    scrapedItems,
    logs: [
      ...cache.logs,
      createManualIdentificationLog(target, input.metadata, targetCandidates.length),
    ],
  }
}

export function applyRawManualArtworkOverride(
  cache: RawLocalScanCache,
  input: RawManualArtworkOverrideInput,
): RawLocalScanCache {
  const target = cache.candidates.find(candidate => candidate.record.id === input.targetRecordId)
  if (!target)
    throw new Error('未找到需要编辑图片的本地候选。')

  const targetGroupKey = createRawSeriesGroupingKey(target)
  const targetCandidates = cache.candidates.filter(candidate => createRawSeriesGroupingKey(candidate) === targetGroupKey)
  const existingItems = new Map((cache.scrapedItems ?? []).map(item => [item.recordId, item]))
  const baseMetadata = findArtworkBaseMetadata(targetCandidates, existingItems)
  if (!baseMetadata)
    throw new Error('请先完成识别或填写 TMDB ID 获取详情后再编辑图片。')

  for (const candidate of targetCandidates) {
    const existing = existingItems.get(candidate.record.id)
    const metadata = patchArtworkMetadata(existing?.metadata ?? candidate.scrapeMetadata ?? baseMetadata, input)
    existingItems.set(candidate.record.id, existing?.matchStatus === 'matched'
      ? {
          ...existing,
          metadata,
        }
      : createMatchedScrapedItem(candidate, metadata, {
          matchedSearchTitle: existing?.matchedSearchTitle,
          searchTitles: uniqueSearchTitles([
            ...(existing?.searchTitles ?? []),
            ...extractCandidateTmdbSearchTitles(candidate),
          ]),
        }))
  }

  const scrapedItems = cache.candidates
    .map(candidate => existingItems.get(candidate.record.id))
    .filter((item): item is RawScrapedMediaItem => item != null)
  const scrapedItemsByRecordId = new Map(scrapedItems.map(item => [item.recordId, item]))
  const candidates = cache.candidates.map((candidate) => {
    const scraped = scrapedItemsByRecordId.get(candidate.record.id)
    return {
      ...candidate,
      scrapeMetadata: scraped?.metadata ?? candidate.scrapeMetadata,
      categoryAssignment: scraped?.categoryAssignment ?? candidate.categoryAssignment,
    } satisfies RawMediaCandidate
  })

  return {
    ...cache,
    candidates,
    scrapedItems,
    logs: [
      ...cache.logs,
      createManualArtworkLog(target, input, targetCandidates.length),
    ],
  }
}

function createRawSeriesCandidateGroups(candidates: readonly RawMediaCandidate[]): RawSeriesCandidateGroup[] {
  const groups = new Map<string, RawMediaCandidate[]>()
  for (const candidate of candidates) {
    const key = createRawSeriesGroupingKey(candidate)
    const current = groups.get(key) ?? []
    current.push(candidate)
    groups.set(key, current)
  }

  return [...groups.entries()].map(([key, groupCandidates]) => ({
    key,
    candidates: groupCandidates,
  }))
}

function findMatchedRepresentative(
  candidates: readonly RawMediaCandidate[],
  scrapedByRecordId: ReadonlyMap<string, RawScrapedMediaItem>,
): RawScrapedMediaItem | undefined {
  return candidates
    .map(candidate => scrapedByRecordId.get(candidate.record.id))
    .filter((item): item is RawScrapedMediaItem => item?.matchStatus === 'matched' && item.metadata != null)
    .sort((left, right) => matchedRepresentativeScore(right) - matchedRepresentativeScore(left))[0]
}

function matchedRepresentativeScore(item: RawScrapedMediaItem): number {
  return (item.metadata?.posterUrl ? 2 : 0)
    + (item.metadata?.backdropUrl ? 1 : 0)
    + (item.metadata?.titleLogoUrl ? 1 : 0)
    + (item.metadata?.overview ? 0.5 : 0)
}

function findArtworkBaseMetadata(
  candidates: readonly RawMediaCandidate[],
  scrapedByRecordId: ReadonlyMap<string, RawScrapedMediaItem>,
): TmdbMetadata | undefined {
  return candidates
    .map(candidate => scrapedByRecordId.get(candidate.record.id)?.metadata ?? candidate.scrapeMetadata)
    .filter((metadata): metadata is TmdbMetadata => metadata != null)
    .sort((left, right) => artworkMetadataScore(right) - artworkMetadataScore(left))[0]
}

function artworkMetadataScore(metadata: TmdbMetadata): number {
  return (metadata.posterUrl ? 2 : 0)
    + (metadata.titleLogoUrl ? 1 : 0)
    + (metadata.backdropUrl ? 1 : 0)
    + (metadata.overview ? 0.5 : 0)
}

function createMatchedScrapedItem(
  candidate: RawMediaCandidate,
  metadata: TmdbMetadata,
  options: {
    readonly matchedSearchTitle?: string
    readonly episodeMetadata?: TmdbEpisodeMetadata
    readonly searchTitles?: readonly string[]
  },
): RawScrapedMediaItem {
  const metadataAssignment = createMetadataCategoryAssignment(metadata)
  const categoryAssignment = resolveRawCandidateCategoryAssignment(candidate, metadataAssignment)
  const appliedMetadataRule = categoryAssignment.source === 'metadataRule' ? metadataAssignment : undefined

  return {
    recordId: candidate.record.id,
    providerPath: candidate.record.providerPath,
    matchStatus: 'matched',
    searchTitles: uniqueSearchTitles([
      ...(options.searchTitles ?? []),
      ...extractCandidateTmdbSearchTitles(candidate),
    ]),
    matchedSearchTitle: options.matchedSearchTitle,
    metadata,
    episodeMetadata: options.episodeMetadata,
    mediaType: metadata.mediaType,
    categoryName: categoryAssignment.categoryName,
    matchedRuleId: appliedMetadataRule?.matchedRuleId,
    matchedRuleName: appliedMetadataRule?.matchedRuleName,
    categoryAssignment,
  }
}

function createMetadataCategoryAssignment(metadata: TmdbMetadata): RawCategoryAssignment {
  const classification = classifyScrapeMetadata({
    mediaType: metadata.mediaType,
    genreIds: metadata.genreIds,
    originalLanguage: metadata.originalLanguage,
    productionCountries: metadata.productionCountries,
    originCountries: metadata.originCountries,
    releaseYear: metadata.releaseYear,
  }, loadScrapeClassificationRules())

  return {
    ...classification,
    source: 'metadataRule',
  }
}

function matchingEpisodeMetadataForCandidate(
  episodeMetadata: TmdbEpisodeMetadata | undefined,
  metadata: TmdbMetadata,
  candidate: RawMediaCandidate,
): TmdbEpisodeMetadata | undefined {
  if (metadata.mediaType !== 'tv')
    return undefined

  return isMatchingTmdbEpisodeMetadata(
    episodeMetadata,
    metadata.tmdbId,
    candidate.seasonNumber,
    candidate.episodeNumber,
  )
    ? episodeMetadata
    : undefined
}

function patchArtworkMetadata(
  metadata: TmdbMetadata,
  input: RawManualArtworkOverrideInput,
): TmdbMetadata {
  if (input.kind === 'poster') {
    return {
      ...metadata,
      posterPath: input.imageUrl ? input.filePath : undefined,
      posterUrl: input.imageUrl,
      scrapedAt: new Date().toISOString(),
    }
  }

  if (input.kind === 'logo') {
    return {
      ...metadata,
      titleLogoPath: input.imageUrl ? input.filePath : undefined,
      titleLogoUrl: input.imageUrl,
      scrapedAt: new Date().toISOString(),
    }
  }

  return {
    ...metadata,
    backdropPath: input.imageUrl ? input.filePath : undefined,
    backdropUrl: input.imageUrl,
    scrapedAt: new Date().toISOString(),
  }
}

function createManualIdentificationLog(
  target: RawMediaCandidate,
  metadata: TmdbMetadata,
  updatedCount: number,
): RawLocalScanLogEntry {
  return {
    timestamp: new Date().toISOString(),
    level: 'info',
    message: `手动识别：${target.seriesTitle ?? target.title} → ${metadata.title}，已更新 ${updatedCount} 个同作品候选。`,
    path: target.record.providerPath,
  }
}

function createManualArtworkLog(
  target: RawMediaCandidate,
  input: RawManualArtworkOverrideInput,
  updatedCount: number,
): RawLocalScanLogEntry {
  const action = input.imageUrl ? '更新' : '清除'
  const label = input.kind === 'poster' ? '海报' : input.kind === 'logo' ? '徽标' : '背景图'
  return {
    timestamp: new Date().toISOString(),
    level: 'info',
    message: `编辑图片：${action}${label}，已更新 ${updatedCount} 个同作品候选。`,
    path: target.record.providerPath,
  }
}

function uniqueSearchTitles(values: readonly (string | undefined)[]): string[] {
  const titles: string[] = []
  const seen = new Set<string>()
  for (const value of values) {
    const title = value?.trim()
    if (!title)
      continue
    const key = title.toLocaleLowerCase()
    if (seen.has(key))
      continue
    seen.add(key)
    titles.push(title)
  }
  return titles
}
