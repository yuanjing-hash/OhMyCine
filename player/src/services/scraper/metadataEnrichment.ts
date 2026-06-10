import type { ScrapeClassificationRules } from './classificationRules'
import type { RawLocalScanLogEntry } from './localScanCache'
import type { TmdbCandidateMatch, TmdbEpisodeMetadata, TmdbMetadata } from './tmdb'
import type { RawMediaCandidate, RawScrapedMediaItem, RawTmdbMatchStatus } from './types'
import { createRawUnresolvedCategoryAssignment, resolveRawCandidateCategoryAssignment } from './categoryGrouping'
import { classifyScrapeMetadata, loadScrapeClassificationRules } from './classificationRules'
import { normalizeTitleKey } from './parser'
import { createRawSeriesGroupingKey } from './rawSeriesGrouping'
import { extractCandidateTmdbSearchTitles, isMatchingTmdbEpisodeMetadata, loadTmdbLocalSettings, readConfiguredTmdbCredential, TmdbScraper } from './tmdb'

export interface EnrichRawMediaCandidatesOptions {
  readonly maxTmdbGroups?: number
  readonly onLog?: (entry: RawLocalScanLogEntry) => void
  readonly tmdbClient?: RawTmdbMetadataClient
}

const DEFAULT_MAX_TMDB_GROUPS = 160

export interface RawTmdbMetadataClient {
  readonly searchCandidate: (candidate: RawMediaCandidate) => Promise<TmdbCandidateMatch | null>
  readonly getEpisodeDetail: (tvTmdbId: number, seasonNumber: number, episodeNumber: number) => Promise<TmdbEpisodeMetadata>
}

export async function enrichRawMediaCandidates(
  candidates: readonly RawMediaCandidate[],
  options: EnrichRawMediaCandidatesOptions = {},
): Promise<RawScrapedMediaItem[]> {
  const tmdb = options.tmdbClient ?? await createConfiguredTmdbClient()
  if (!tmdb) {
    options.onLog?.(createLog('warning', '未配置 TMDB token/key，本次扫描仅保留可播放候选并统一归入未识别分类。'))
    return candidates.map(candidate => fallbackScrapedItem(candidate, 'notConfigured'))
  }

  const rules = loadScrapeClassificationRules()
  const groups = createCandidateGroups(candidates)
  const scrapedItems = new Map<string, RawScrapedMediaItem>()
  const episodeMetadataByKey = new Map<string, TmdbEpisodeMetadata | null>()
  let processedGroupCount = 0
  let matchedGroupCount = 0
  let authFailureLogged = false
  let episodeFailureLogged = false
  const maxTmdbGroups = options.maxTmdbGroups ?? DEFAULT_MAX_TMDB_GROUPS

  for (const group of groups) {
    processedGroupCount += 1
    if (processedGroupCount > maxTmdbGroups) {
      for (const candidate of group.candidates)
        scrapedItems.set(candidate.record.id, fallbackScrapedItem(candidate, 'skipped', 'TMDB 匹配达到本地扫描上限。'))
      continue
    }

    try {
      const match = await tmdb.searchCandidate(group.representative)
      if (!match) {
        for (const candidate of group.candidates)
          scrapedItems.set(candidate.record.id, fallbackScrapedItem(candidate, 'notFound'))
        continue
      }

      matchedGroupCount += 1
      const classification = classifyScrapeMetadata({
        mediaType: match.metadata.mediaType,
        genreIds: match.metadata.genreIds,
        originalLanguage: match.metadata.originalLanguage,
        productionCountries: match.metadata.productionCountries,
        originCountries: match.metadata.originCountries,
        releaseYear: match.metadata.releaseYear,
      }, rules)
      const metadataAssignment = {
        ...classification,
        source: 'metadataRule' as const,
      }

      for (const candidate of group.candidates) {
        const categoryAssignment = resolveRawCandidateCategoryAssignment(candidate, metadataAssignment)
        const appliedMetadataRule = categoryAssignment.source === 'metadataRule' ? classification : undefined
        const episodeMetadata = await resolveEpisodeMetadataForCandidate({
          tmdb,
          metadata: match.metadata,
          candidate,
          episodeMetadataByKey,
        })
          .catch((error) => {
            if (!episodeFailureLogged) {
              episodeFailureLogged = true
              options.onLog?.(createLog('warning', tmdbEpisodeErrorMessage(error)))
            }
            return undefined
          })
        scrapedItems.set(candidate.record.id, {
          recordId: candidate.record.id,
          providerPath: candidate.record.providerPath,
          matchStatus: 'matched',
          searchTitles: group.searchTitles,
          matchedSearchTitle: match.searchTitle,
          metadata: match.metadata,
          episodeMetadata,
          mediaType: match.metadata.mediaType,
          categoryName: categoryAssignment.categoryName,
          matchedRuleId: appliedMetadataRule?.matchedRuleId,
          matchedRuleName: appliedMetadataRule?.matchedRuleName,
          categoryAssignment,
        })
      }
    }
    catch (error) {
      const errorMessage = tmdbMatchErrorMessage(error)
      if (!authFailureLogged && isTmdbAuthFailureMessage(errorMessage)) {
        authFailureLogged = true
        options.onLog?.(createLog('warning', errorMessage))
      }
      for (const candidate of group.candidates)
        scrapedItems.set(candidate.record.id, fallbackScrapedItem(candidate, 'failed', errorMessage))
    }
  }

  if (processedGroupCount > maxTmdbGroups) {
    options.onLog?.(createLog(
      'warning',
      `TMDB 匹配达到 MVP 上限：已处理 ${maxTmdbGroups} 组作品，其余候选保留为本地兜底分类。`,
    ))
  }

  options.onLog?.(createLog(
    'info',
    `TMDB 补全完成：匹配 ${matchedGroupCount}/${Math.min(groups.length, maxTmdbGroups)} 组作品。`,
  ))

  const completedScrapedItems = await enrichRawScrapedItemsEpisodeMetadata(
    candidates,
    backfillSeriesScrapedItems(candidates, scrapedItems, rules),
    tmdb,
    options,
  )
  const completedScrapedItemsByRecordId = new Map(completedScrapedItems.map(item => [item.recordId, item]))

  return candidates.map(candidate =>
    completedScrapedItemsByRecordId.get(candidate.record.id) ?? fallbackScrapedItem(candidate, 'failed', '本地刮削结果缺失。'))
}

export async function enrichRawScrapedItemsEpisodeMetadata(
  candidates: readonly RawMediaCandidate[],
  scrapedItems: readonly RawScrapedMediaItem[] | undefined,
  tmdb: Pick<RawTmdbMetadataClient, 'getEpisodeDetail'>,
  options: Pick<EnrichRawMediaCandidatesOptions, 'onLog'> = {},
): Promise<RawScrapedMediaItem[]> {
  const scrapedByRecordId = new Map((scrapedItems ?? []).map(item => [item.recordId, item]))
  const episodeMetadataByKey = new Map<string, TmdbEpisodeMetadata | null>()
  let episodeFailureLogged = false
  const nextItems: RawScrapedMediaItem[] = []

  for (const candidate of candidates) {
    const scraped = scrapedByRecordId.get(candidate.record.id)
    if (!scraped)
      continue
    if (scraped.matchStatus !== 'matched' || !scraped.metadata || scraped.metadata.mediaType !== 'tv') {
      nextItems.push({
        ...scraped,
        episodeMetadata: undefined,
      })
      continue
    }

    const existingEpisodeMetadata = isMatchingTmdbEpisodeMetadata(
      scraped.episodeMetadata,
      scraped.metadata.tmdbId,
      candidate.seasonNumber,
      candidate.episodeNumber,
    )
      ? scraped.episodeMetadata
      : undefined
    if (existingEpisodeMetadata) {
      nextItems.push({
        ...scraped,
        episodeMetadata: existingEpisodeMetadata,
      })
      continue
    }

    const episodeMetadata = await resolveEpisodeMetadataForCandidate({
      tmdb,
      metadata: scraped.metadata,
      candidate,
      episodeMetadataByKey,
    }).catch((error) => {
      if (!episodeFailureLogged) {
        episodeFailureLogged = true
        options.onLog?.(createLog('warning', tmdbEpisodeErrorMessage(error)))
      }
      return undefined
    })

    nextItems.push({
      ...scraped,
      episodeMetadata,
    })
  }

  return nextItems
}

async function createConfiguredTmdbClient(): Promise<RawTmdbMetadataClient | null> {
  const credential = await readConfiguredTmdbCredential()
  return credential ? new TmdbScraper(credential, loadTmdbLocalSettings()) : null
}

async function resolveEpisodeMetadataForCandidate(
  input: {
    readonly tmdb: Pick<RawTmdbMetadataClient, 'getEpisodeDetail'>
    readonly metadata: TmdbMetadata
    readonly candidate: RawMediaCandidate
    readonly episodeMetadataByKey: Map<string, TmdbEpisodeMetadata | null>
  },
): Promise<TmdbEpisodeMetadata | undefined> {
  const { tmdb, metadata, candidate, episodeMetadataByKey } = input
  if (metadata.mediaType !== 'tv' || candidate.seasonNumber == null || candidate.episodeNumber == null)
    return undefined

  const key = `${metadata.tmdbId}:${candidate.seasonNumber}:${candidate.episodeNumber}`
  if (episodeMetadataByKey.has(key))
    return episodeMetadataByKey.get(key) ?? undefined

  const episodeMetadata = await tmdb.getEpisodeDetail(metadata.tmdbId, candidate.seasonNumber, candidate.episodeNumber)
  const matchingEpisodeMetadata = isMatchingTmdbEpisodeMetadata(
    episodeMetadata,
    metadata.tmdbId,
    candidate.seasonNumber,
    candidate.episodeNumber,
  )
    ? episodeMetadata
    : undefined
  episodeMetadataByKey.set(key, matchingEpisodeMetadata ?? null)
  return matchingEpisodeMetadata
}

function tmdbMatchErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim())
    return `${error.message.trim()} 已保留本地可播放候选。`
  return 'TMDB 请求失败，已保留本地可播放候选。'
}

function tmdbEpisodeErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim())
    return `${error.message.trim()} 已保留剧集匹配和本地可播放候选。`
  return 'TMDB 分集请求失败，已保留剧集匹配和本地可播放候选。'
}

function isTmdbAuthFailureMessage(value: string): boolean {
  return value.includes('验证失败') && value.includes('切换类型')
}

function createCandidateGroups(candidates: readonly RawMediaCandidate[]): Array<{
  representative: RawMediaCandidate
  candidates: RawMediaCandidate[]
  searchTitles: string[]
}> {
  const groups = new Map<string, { representative: RawMediaCandidate, candidates: RawMediaCandidate[], searchTitles: string[] }>()

  for (const candidate of candidates) {
    const searchTitles = extractCandidateTmdbSearchTitles(candidate)
    const primaryTitle = searchTitles[0] ?? candidate.normalizedTitle ?? candidate.title
    const mediaScope = candidate.kind === 'episode' || candidate.kind === 'tv' ? 'tv' : candidate.kind
    const key = `${mediaScope}:${candidate.year ?? ''}:${normalizeTitleKey(primaryTitle)}`
    const current = groups.get(key) ?? {
      representative: candidate,
      candidates: [],
      searchTitles,
    }
    current.candidates.push(candidate)
    if (current.searchTitles.length === 0 && searchTitles.length > 0)
      current.searchTitles = searchTitles
    groups.set(key, current)
  }

  return [...groups.values()]
}

function fallbackScrapedItem(
  candidate: RawMediaCandidate,
  matchStatus: RawTmdbMatchStatus,
  errorMessage?: string,
): RawScrapedMediaItem {
  const categoryAssignment = createRawUnresolvedCategoryAssignment()
  return {
    recordId: candidate.record.id,
    providerPath: candidate.record.providerPath,
    matchStatus,
    searchTitles: extractCandidateTmdbSearchTitles(candidate),
    mediaType: fallbackMediaType(candidate),
    categoryName: categoryAssignment.categoryName,
    categoryAssignment,
    errorMessage,
  }
}

function backfillSeriesScrapedItems(
  candidates: readonly RawMediaCandidate[],
  scrapedItems: ReadonlyMap<string, RawScrapedMediaItem>,
  rules: ScrapeClassificationRules,
): RawScrapedMediaItem[] {
  const nextItems = new Map(scrapedItems)

  for (const group of groupSeriesCandidates(candidates)) {
    const representative = findMatchedSeriesRepresentative(group, nextItems)
    if (!representative?.metadata)
      continue

    for (const candidate of group) {
      const existing = nextItems.get(candidate.record.id)
      if (existing?.matchStatus === 'matched' && existing.metadata)
        continue

      nextItems.set(candidate.record.id, createMatchedScrapedItemFromMetadata(candidate, representative, rules))
    }
  }

  return candidates
    .map(candidate => nextItems.get(candidate.record.id))
    .filter((item): item is RawScrapedMediaItem => item != null)
}

function groupSeriesCandidates(candidates: readonly RawMediaCandidate[]): RawMediaCandidate[][] {
  const groups = new Map<string, RawMediaCandidate[]>()
  for (const candidate of candidates) {
    if (candidate.kind !== 'episode' && candidate.kind !== 'tv')
      continue
    const key = createRawSeriesGroupingKey(candidate)
    const current = groups.get(key) ?? []
    current.push(candidate)
    groups.set(key, current)
  }
  return [...groups.values()]
}

function findMatchedSeriesRepresentative(
  candidates: readonly RawMediaCandidate[],
  scrapedItems: ReadonlyMap<string, RawScrapedMediaItem>,
): (RawScrapedMediaItem & { metadata: TmdbMetadata }) | undefined {
  return candidates
    .map(candidate => scrapedItems.get(candidate.record.id))
    .filter((item): item is RawScrapedMediaItem & { metadata: TmdbMetadata } => item?.matchStatus === 'matched' && item.metadata?.mediaType === 'tv')
    .sort((left, right) => matchedRepresentativeScore(right) - matchedRepresentativeScore(left))[0]
}

function matchedRepresentativeScore(item: RawScrapedMediaItem): number {
  return (item.episodeMetadata ? 4 : 0)
    + (item.metadata?.posterUrl ? 2 : 0)
    + (item.metadata?.backdropUrl ? 1 : 0)
    + (item.metadata?.titleLogoUrl ? 1 : 0)
    + (item.metadata?.overview ? 0.5 : 0)
}

function createMatchedScrapedItemFromMetadata(
  candidate: RawMediaCandidate,
  representative: RawScrapedMediaItem & { metadata: TmdbMetadata },
  rules: ScrapeClassificationRules,
): RawScrapedMediaItem {
  const metadataAssignment = createMetadataCategoryAssignment(representative.metadata, rules)
  const categoryAssignment = resolveRawCandidateCategoryAssignment(candidate, metadataAssignment)
  const appliedMetadataRule = categoryAssignment.source === 'metadataRule' ? metadataAssignment : undefined

  return {
    recordId: candidate.record.id,
    providerPath: candidate.record.providerPath,
    matchStatus: 'matched',
    searchTitles: uniqueSearchTitles([
      ...representative.searchTitles,
      ...extractCandidateTmdbSearchTitles(candidate),
    ]),
    matchedSearchTitle: representative.matchedSearchTitle,
    metadata: representative.metadata,
    mediaType: representative.metadata.mediaType,
    categoryName: categoryAssignment.categoryName,
    matchedRuleId: appliedMetadataRule?.matchedRuleId,
    matchedRuleName: appliedMetadataRule?.matchedRuleName,
    categoryAssignment,
  }
}

function createMetadataCategoryAssignment(metadata: TmdbMetadata, rules: ScrapeClassificationRules) {
  return {
    ...classifyScrapeMetadata({
      mediaType: metadata.mediaType,
      genreIds: metadata.genreIds,
      originalLanguage: metadata.originalLanguage,
      productionCountries: metadata.productionCountries,
      originCountries: metadata.originCountries,
      releaseYear: metadata.releaseYear,
    }, rules),
    source: 'metadataRule' as const,
  }
}

function uniqueSearchTitles(values: readonly string[]): string[] {
  const result: string[] = []
  const seen = new Set<string>()
  for (const value of values) {
    const normalized = normalizeTitleKey(value)
    if (!normalized || seen.has(normalized))
      continue
    seen.add(normalized)
    result.push(value)
  }
  return result
}

function fallbackMediaType(candidate: RawMediaCandidate): 'movie' | 'tv' | undefined {
  if (candidate.kind === 'movie')
    return 'movie'
  if (candidate.kind === 'episode' || candidate.kind === 'tv')
    return 'tv'
  return undefined
}

function createLog(level: RawLocalScanLogEntry['level'], message: string): RawLocalScanLogEntry {
  return {
    timestamp: new Date().toISOString(),
    level,
    message,
  }
}
