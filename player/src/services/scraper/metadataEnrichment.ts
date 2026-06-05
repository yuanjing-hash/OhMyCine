import type { RawLocalScanLogEntry } from './localScanCache'
import type { TmdbEpisodeMetadata, TmdbMetadata } from './tmdb'
import type { RawMediaCandidate, RawScrapedMediaItem, RawTmdbMatchStatus } from './types'
import { createRawUnresolvedCategoryAssignment, resolveRawCandidateCategoryAssignment } from './categoryGrouping'
import { classifyScrapeMetadata, loadScrapeClassificationRules } from './classificationRules'
import { normalizeTitleKey } from './parser'
import { extractCandidateTmdbSearchTitles, loadTmdbLocalSettings, readConfiguredTmdbCredential, TmdbScraper } from './tmdb'

export interface EnrichRawMediaCandidatesOptions {
  readonly maxTmdbGroups?: number
  readonly onLog?: (entry: RawLocalScanLogEntry) => void
}

const DEFAULT_MAX_TMDB_GROUPS = 160

export async function enrichRawMediaCandidates(
  candidates: readonly RawMediaCandidate[],
  options: EnrichRawMediaCandidatesOptions = {},
): Promise<RawScrapedMediaItem[]> {
  const credential = await readConfiguredTmdbCredential()
  if (!credential) {
    options.onLog?.(createLog('warning', '未配置 TMDB token/key，本次扫描仅保留可播放候选并统一归入未识别分类。'))
    return candidates.map(candidate => fallbackScrapedItem(candidate, 'notConfigured'))
  }

  const settings = loadTmdbLocalSettings()
  const tmdb = new TmdbScraper(credential, settings)
  const rules = loadScrapeClassificationRules()
  const groups = createCandidateGroups(candidates)
  const scrapedItems = new Map<string, RawScrapedMediaItem>()
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
      const episodeMetadataByKey = new Map<string, TmdbEpisodeMetadata | null>()
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
        const episodeMetadata = await resolveEpisodeMetadataForCandidate(tmdb, match.metadata, candidate, episodeMetadataByKey)
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

  return candidates.map(candidate =>
    scrapedItems.get(candidate.record.id) ?? fallbackScrapedItem(candidate, 'failed', '本地刮削结果缺失。'))
}

async function resolveEpisodeMetadataForCandidate(
  tmdb: TmdbScraper,
  metadata: TmdbMetadata,
  candidate: RawMediaCandidate,
  episodeMetadataByKey: Map<string, TmdbEpisodeMetadata | null>,
): Promise<TmdbEpisodeMetadata | undefined> {
  if (metadata.mediaType !== 'tv' || candidate.seasonNumber == null || candidate.episodeNumber == null)
    return undefined

  const key = `${metadata.tmdbId}:${candidate.seasonNumber}:${candidate.episodeNumber}`
  if (episodeMetadataByKey.has(key))
    return episodeMetadataByKey.get(key) ?? undefined

  const episodeMetadata = await tmdb.getEpisodeDetail(metadata.tmdbId, candidate.seasonNumber, candidate.episodeNumber)
  episodeMetadataByKey.set(key, episodeMetadata)
  return episodeMetadata
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
