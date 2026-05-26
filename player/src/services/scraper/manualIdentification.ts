import type { RawLocalScanCache, RawLocalScanLogEntry } from './localScanCache'
import type { TmdbMetadata } from './tmdb'
import type { RawCategoryAssignment, RawMediaCandidate, RawScrapedMediaItem } from './types'
import { resolveRawCandidateCategoryAssignment } from './categoryGrouping'
import { classifyScrapeMetadata, loadScrapeClassificationRules } from './classificationRules'
import { createRawSeriesGroupingKey } from './rawSeriesGrouping'
import { extractCandidateTmdbSearchTitles } from './tmdb'

export interface RawManualIdentificationInput {
  readonly targetRecordId: string
  readonly metadata: TmdbMetadata
  readonly matchedSearchTitle?: string
  readonly searchTitles?: readonly string[]
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
    existingItems.set(candidate.record.id, createMatchedScrapedItem(candidate, input.metadata, {
      matchedSearchTitle: input.matchedSearchTitle,
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
    + (item.metadata?.overview ? 0.5 : 0)
}

function createMatchedScrapedItem(
  candidate: RawMediaCandidate,
  metadata: TmdbMetadata,
  options: {
    readonly matchedSearchTitle?: string
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
