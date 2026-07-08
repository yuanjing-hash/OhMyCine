import assert from 'node:assert/strict'
import { deriveRawCandidateCategoryAssignment, RAW_UNRESOLVED_CATEGORY_NAME, resolveRawCandidateCategoryAssignment, resolveRawScrapedCategoryAssignment } from '../src/services/scraper/categoryGrouping.ts'
import { classifyScrapeMetadata, DEFAULT_SCRAPE_CLASSIFICATION_RULES } from '../src/services/scraper/classificationRules.ts'
import { loadRawSourceScanCache, saveRawSourceScanCache } from '../src/services/scraper/localScanCache.ts'
import { applyRawManualArtworkOverride, applyRawManualIdentification, createEffectiveRawScrapeItemMap } from '../src/services/scraper/manualIdentification.ts'
import { enrichRawMediaCandidates, enrichRawScrapedItemsEpisodeMetadata } from '../src/services/scraper/metadataEnrichment.ts'
import { recognizePathAwareMedia } from '../src/services/scraper/pathRecognition.ts'
import { cleanMediaTitle, extractMediaSearchTitles, parseRawMediaCandidate } from '../src/services/scraper/parser.ts'
import { toRawScannedMediaItem } from '../src/services/scraper/rawDisplayMapping.ts'
import { createRawSeriesSeasonChildren, getContextSeriesSeasons, getPlayableSeasonChildren, groupRawSeriesEntries } from '../src/services/scraper/rawSeriesGrouping.ts'
import { createRawScanPreview } from '../src/services/scraper/scanner.ts'
import { TmdbScraper } from '../src/services/scraper/tmdb.ts'
import { createPlaybackQueue, getPlaybackMediaContext, savePlaybackMediaContext } from '../src/services/playbackContext.ts'
import type { MediaItem } from '../src/services/datasource/types.ts'
import type { RawLocalScanCache } from '../src/services/scraper/localScanCache.ts'
import type { RawTmdbMetadataClient } from '../src/services/scraper/metadataEnrichment.ts'
import type { TmdbCandidateMatch, TmdbEpisodeMetadata } from '../src/services/scraper/tmdb.ts'
import type { RawFileRecord, RawMediaCandidate, RawScrapedMediaItem, RawTmdbMatchStatus } from '../src/services/scraper/types.ts'

const noisyTitle = '机械之声的传奇 The Legend of Vox Machina AMZN GrassTV 1080P 简繁字幕'
const cleanedTitle = cleanMediaTitle(noisyTitle)
const searchTitles = extractMediaSearchTitles(noisyTitle)

assert.equal(cleanedTitle, '机械之声的传奇 The Legend of Vox Machina')
assert.deepEqual(searchTitles.slice(0, 2), ['机械之声的传奇', 'The Legend of Vox Machina'])
assert.equal(searchTitles.includes('简繁字幕'), false)
assert.equal(searchTitles.some(title => title.includes('字幕')), false)
assert.equal(cleanMediaTitle('It Chapter Two 1080P'), 'It Chapter Two')

const tvAnimation = classifyScrapeMetadata({
  mediaType: 'tv',
  genreIds: [16],
  originalLanguage: 'en',
  originCountries: ['US'],
  releaseYear: 2022,
}, DEFAULT_SCRAPE_CLASSIFICATION_RULES)
assert.equal(tvAnimation.categoryName, '动漫')

const zhMovie = classifyScrapeMetadata({
  mediaType: 'movie',
  genreIds: [18],
  originalLanguage: 'zh',
  productionCountries: ['CN'],
  releaseYear: 2024,
}, DEFAULT_SCRAPE_CLASSIFICATION_RULES)
assert.equal(zhMovie.categoryName, '华语电影')

const legacyFallback = classifyScrapeMetadata({
  mediaType: 'movie',
  genreIds: [],
  originalLanguage: 'en',
  productionCountries: ['US'],
}, {
  version: 1,
  groups: [{
    mediaType: 'movie',
    categories: [],
    fallbackCategoryName: '外语电影',
  }],
})
assert.equal(legacyFallback.categoryName, '未分类')

const workFolderRecord: RawFileRecord = {
  id: 'alist:/机械之声的传奇 The Legend of Vox Machina AMZN GrassTV/Season 01/S01E01.mkv',
  sourceId: 'alist',
  sourceType: 'alist',
  rootPath: '/',
  providerPath: '/机械之声的传奇 The Legend of Vox Machina AMZN GrassTV/Season 01/S01E01.mkv',
  relativePath: '机械之声的传奇 The Legend of Vox Machina AMZN GrassTV/Season 01/S01E01.mkv',
  parentPath: '/机械之声的传奇 The Legend of Vox Machina AMZN GrassTV/Season 01',
  fileName: 'S01E01.1080P.AMZN.GrassTV.简繁字幕.mkv',
  extension: 'mkv',
}
const workFolderCandidate = parseRawMediaCandidate(workFolderRecord)
const workFolderAssignment = deriveRawCandidateCategoryAssignment(workFolderCandidate)
const workFolderRecognition = recognizePathAwareMedia(workFolderRecord)
const workFolderPreview = createRawScanPreview([{
  name: workFolderRecord.fileName,
  path: workFolderRecord.providerPath,
  parentPath: workFolderRecord.parentPath,
  type: 'file',
}], {
  sourceId: 'alist',
  sourceType: 'alist',
  rootPath: '/',
})
assert.equal(workFolderCandidate.seriesTitle, '机械之声的传奇 The Legend of Vox Machina')
assert.equal(workFolderCandidate.seasonNumber, 1)
assert.equal(workFolderCandidate.episodeNumber, 1)
assert.equal(workFolderRecognition.parentSegment, 'Season 01')
assert.equal(workFolderRecognition.grandparentSegment, '机械之声的传奇 The Legend of Vox Machina AMZN GrassTV')
assert.equal(workFolderRecognition.parentIsSeason, true)
assert.equal(workFolderRecognition.fileIsEpisodeOnly, true)
assert.equal(workFolderRecognition.searchTitles.includes('The Legend of Vox Machina'), true)
assert.equal(workFolderAssignment.categoryName, '剧集')
assert.equal(workFolderAssignment.source, 'kindFallback')
assert.equal(workFolderPreview.candidates[0]?.categoryHint, undefined)
assert.notEqual(workFolderAssignment.categoryName, '机械之声的传奇 The Legend of Vox Machina')
assert.notEqual(workFolderAssignment.categoryName, '机械之声的传奇 The Legend of Vox Machina AMZN GrassTV')

const standardSeriesRecords = [
  createRecord('/动漫/机械之声的传奇 The Legend of Vox Machina/Season01/S01E01.mkv'),
  createRecord('/动漫/机械之声的传奇 The Legend of Vox Machina/Season 02/S02E01.mkv'),
  createRecord('/动漫/机械之声的传奇 The Legend of Vox Machina/S03/S03E01.mkv'),
]
const standardSeriesCandidates = standardSeriesRecords.map(record => parseRawMediaCandidate(record))
assert.deepEqual(standardSeriesCandidates.map(candidate => candidate.seriesTitle), [
  '机械之声的传奇 The Legend of Vox Machina',
  '机械之声的传奇 The Legend of Vox Machina',
  '机械之声的传奇 The Legend of Vox Machina',
])
assert.deepEqual(standardSeriesCandidates.map(candidate => candidate.categoryHint), ['动漫', '动漫', '动漫'])
assert.deepEqual(standardSeriesCandidates.map(candidate => candidate.seasonNumber), [1, 2, 3])
assert.deepEqual(standardSeriesCandidates.map(candidate => deriveRawCandidateCategoryAssignment(candidate).source), ['pathHint', 'pathHint', 'pathHint'])

const seansonTypoCandidate = parseRawMediaCandidate(createRecord('/动漫/机械之声的传奇 The Legend of Vox Machina/Seanson 04/S04E01.mkv'))
assert.equal(seansonTypoCandidate.seasonNumber, 4)

const metadataRuleAssignment = {
  categoryName: '欧美剧',
  source: 'metadataRule' as const,
  matchedRuleName: '欧美剧',
}
const pathHintMatchedAssignment = resolveRawCandidateCategoryAssignment(standardSeriesCandidates[0], metadataRuleAssignment)
assert.equal(pathHintMatchedAssignment.categoryName, '动漫')
assert.equal(pathHintMatchedAssignment.source, 'pathHint')

const flatEpisodeRecord: RawFileRecord = {
  id: 'alist:/未整理/The.Legend.of.Vox.Machina.S01E02.1080p.AMZN.GrassTV.mkv',
  sourceId: 'alist',
  sourceType: 'alist',
  rootPath: '/',
  providerPath: '/未整理/The.Legend.of.Vox.Machina.S01E02.1080p.AMZN.GrassTV.mkv',
  relativePath: '未整理/The.Legend.of.Vox.Machina.S01E02.1080p.AMZN.GrassTV.mkv',
  parentPath: '/未整理',
  fileName: 'The.Legend.of.Vox.Machina.S01E02.1080p.AMZN.GrassTV.mkv',
  extension: 'mkv',
}
const flatEpisodeCandidate = parseRawMediaCandidate(flatEpisodeRecord)
assert.equal(flatEpisodeCandidate.seriesTitle, 'The Legend of Vox Machina')
assert.equal(flatEpisodeCandidate.episodeNumber, 2)
assert.equal(flatEpisodeCandidate.categoryHint, undefined)
const noPathHintMatchedAssignment = resolveRawCandidateCategoryAssignment(flatEpisodeCandidate, metadataRuleAssignment)
assert.equal(noPathHintMatchedAssignment.categoryName, '欧美剧')
assert.equal(noPathHintMatchedAssignment.source, 'metadataRule')
const noPathHintMatchedScrape: RawScrapedMediaItem = {
  recordId: flatEpisodeCandidate.record.id,
  providerPath: flatEpisodeCandidate.record.providerPath,
  matchStatus: 'matched',
  searchTitles: ['The Legend of Vox Machina'],
  matchedSearchTitle: 'The Legend of Vox Machina',
  mediaType: 'tv',
  categoryName: '欧美剧',
  categoryAssignment: metadataRuleAssignment,
}
const noPathHintMatchedDisplayAssignment = resolveRawScrapedCategoryAssignment(flatEpisodeCandidate, noPathHintMatchedScrape)
assert.equal(noPathHintMatchedDisplayAssignment.categoryName, '欧美剧')
assert.equal(noPathHintMatchedDisplayAssignment.source, 'metadataRule')

const matchedSeriesScrape: RawScrapedMediaItem = {
  recordId: standardSeriesCandidates[0].record.id,
  providerPath: standardSeriesCandidates[0].record.providerPath,
  matchStatus: 'matched',
  searchTitles: ['机械之声的传奇', 'The Legend of Vox Machina'],
  matchedSearchTitle: 'The Legend of Vox Machina',
  metadata: {
    tmdbId: 135934,
    mediaType: 'tv',
    title: '机械之声的传奇',
    originalTitle: 'The Legend of Vox Machina',
    overview: 'Matched metadata wins for the merged series work.',
    releaseYear: 2022,
    rating: 8.2,
    genreIds: [16],
    genres: ['动画'],
    originalLanguage: 'en',
    originCountries: ['US'],
    productionCountries: ['US'],
    posterUrl: 'https://image.tmdb.org/t/p/w500/poster.jpg',
    backdropUrl: 'https://image.tmdb.org/t/p/w780/backdrop.jpg',
    titleLogoUrl: 'https://image.tmdb.org/t/p/w500/logo.png',
    scrapedAt: '2026-05-25T00:00:00.000Z',
  },
  mediaType: 'tv',
  categoryName: '动漫',
  categoryAssignment: {
    categoryName: '动漫',
    source: 'pathHint',
  },
}
const pathHintMatchedDisplayAssignment = resolveRawScrapedCategoryAssignment(standardSeriesCandidates[0], matchedSeriesScrape)
assert.equal(pathHintMatchedDisplayAssignment.categoryName, '动漫')
assert.equal(pathHintMatchedDisplayAssignment.source, 'pathHint')
const missingScrapeDisplayAssignment = resolveRawScrapedCategoryAssignment(standardSeriesCandidates[0], undefined)
assert.equal(missingScrapeDisplayAssignment.categoryName, RAW_UNRESOLVED_CATEGORY_NAME)
assert.equal(missingScrapeDisplayAssignment.source, 'kindFallback')

const matchedEpisodeMetadata: TmdbEpisodeMetadata = {
  tmdbEpisodeId: 3376714,
  tvTmdbId: 135934,
  seasonNumber: 1,
  episodeNumber: 1,
  name: 'The Terror of Tal\'Dorei - Part 1',
  overview: 'Episode-specific overview wins for S01E01.',
  airDate: '2022-01-28',
  runtime: 24,
  rating: 8.4,
  stillPath: '/episode-still.jpg',
  stillUrl: 'https://image.tmdb.org/t/p/w780/episode-still.jpg',
  scrapedAt: '2026-05-25T00:01:00.000Z',
}
const matchedEpisodeScrape: RawScrapedMediaItem = {
  ...matchedSeriesScrape,
  episodeMetadata: matchedEpisodeMetadata,
}
const episodeDisplayItem = toRawScannedMediaItem(standardSeriesCandidates[0], matchedEpisodeScrape, 'tv')
assert.equal(episodeDisplayItem.name, 'S01E01 · The Terror of Tal\'Dorei - Part 1')
assert.equal(episodeDisplayItem.posterUrl, matchedEpisodeMetadata.stillUrl)
assert.equal(episodeDisplayItem.backdropUrl, matchedEpisodeMetadata.stillUrl)
assert.equal(episodeDisplayItem.overview, matchedEpisodeMetadata.overview)
assert.equal(episodeDisplayItem.duration, 24 * 60)
assert.equal(episodeDisplayItem.rating, matchedEpisodeMetadata.rating)

const fallbackEpisodeDisplayItem = toRawScannedMediaItem(standardSeriesCandidates[1], {
  ...matchedSeriesScrape,
  recordId: standardSeriesCandidates[1].record.id,
  providerPath: standardSeriesCandidates[1].record.providerPath,
  episodeMetadata: undefined,
}, 'tv')
assert.equal(fallbackEpisodeDisplayItem.name, '机械之声的传奇 S02E01')
assert.equal(fallbackEpisodeDisplayItem.backdropUrl, matchedSeriesScrape.metadata?.backdropUrl)
assert.equal(fallbackEpisodeDisplayItem.overview, matchedSeriesScrape.metadata?.overview)

const staleEpisodeMetadata: TmdbEpisodeMetadata = {
  ...matchedEpisodeMetadata,
  tvTmdbId: 999999,
  name: 'Stale episode title from another TMDB series',
  overview: 'This stale overview must not be displayed.',
  stillUrl: 'https://image.tmdb.org/t/p/w780/stale-episode-still.jpg',
}
const staleEpisodeDisplayItem = toRawScannedMediaItem(standardSeriesCandidates[0], {
  ...matchedSeriesScrape,
  episodeMetadata: staleEpisodeMetadata,
}, 'tv')
assert.equal(staleEpisodeDisplayItem.name, '机械之声的传奇 S01E01')
assert.equal(staleEpisodeDisplayItem.posterUrl, matchedSeriesScrape.metadata?.posterUrl)
assert.equal(staleEpisodeDisplayItem.overview, matchedSeriesScrape.metadata?.overview)

const unmatchedStatuses: RawTmdbMatchStatus[] = ['notFound', 'notConfigured', 'failed', 'skipped']
const unmatchedDisplayCategories = unmatchedStatuses.map((matchStatus) => {
  const displayAssignment = resolveRawScrapedCategoryAssignment(standardSeriesCandidates[0], {
    ...matchedSeriesScrape,
    matchStatus,
    matchedSearchTitle: undefined,
    metadata: undefined,
    categoryName: '动漫',
    categoryAssignment: {
      categoryName: '动漫',
      source: 'pathHint',
    },
  })
  assert.equal(displayAssignment.categoryName, RAW_UNRESOLVED_CATEGORY_NAME)
  assert.equal(displayAssignment.source, 'kindFallback')
  return displayAssignment.categoryName
})
assert.deepEqual(unmatchedDisplayCategories, unmatchedStatuses.map(() => RAW_UNRESOLVED_CATEGORY_NAME))

const mixedSeriesEffectiveScrapes = createEffectiveRawScrapeItemMap(standardSeriesCandidates, [
  matchedSeriesScrape,
  {
    ...matchedSeriesScrape,
    recordId: standardSeriesCandidates[1].record.id,
    providerPath: standardSeriesCandidates[1].record.providerPath,
    matchStatus: 'notFound',
    matchedSearchTitle: undefined,
    metadata: undefined,
    episodeMetadata: matchedEpisodeMetadata,
    categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
    categoryAssignment: {
      categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
      source: 'kindFallback',
    },
  },
])
const mixedSeriesDisplayCategories = standardSeriesCandidates.map(candidate =>
  resolveRawScrapedCategoryAssignment(candidate, mixedSeriesEffectiveScrapes.get(candidate.record.id)).categoryName)
assert.deepEqual([...new Set(mixedSeriesDisplayCategories)], ['动漫'])
assert.equal(mixedSeriesDisplayCategories.includes(RAW_UNRESOLVED_CATEGORY_NAME), false)
assert.equal(mixedSeriesEffectiveScrapes.get(standardSeriesCandidates[1].record.id)?.metadata?.title, '机械之声的传奇')
assert.equal(mixedSeriesEffectiveScrapes.get(standardSeriesCandidates[1].record.id)?.episodeMetadata, undefined)
assert.equal(mixedSeriesEffectiveScrapes.get(standardSeriesCandidates[2].record.id)?.matchStatus, 'matched')

const noRepresentativeEffectiveScrapes = createEffectiveRawScrapeItemMap(standardSeriesCandidates, [{
  ...matchedSeriesScrape,
  recordId: standardSeriesCandidates[0].record.id,
  providerPath: standardSeriesCandidates[0].record.providerPath,
  matchStatus: 'notFound',
  matchedSearchTitle: undefined,
  metadata: undefined,
  categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
  categoryAssignment: {
    categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
    source: 'kindFallback',
  },
}])
const noRepresentativeDisplayCategories = standardSeriesCandidates.map(candidate =>
  resolveRawScrapedCategoryAssignment(candidate, noRepresentativeEffectiveScrapes.get(candidate.record.id)).categoryName)
assert.deepEqual([...new Set(noRepresentativeDisplayCategories)], [RAW_UNRESOLVED_CATEGORY_NAME])

const matchedSeriesMetadata = matchedSeriesScrape.metadata
assert.ok(matchedSeriesMetadata)
const manuallyIdentifiedCache = applyRawManualIdentification({
  version: 1,
  scanId: 'scan-manual-override',
  sourceId: 'alist',
  sourceType: 'alist',
  rootPath: '/',
  status: 'completed',
  startedAt: '2026-05-25T00:00:00.000Z',
  finishedAt: '2026-05-25T00:00:00.000Z',
  folderCount: 4,
  fileCount: standardSeriesCandidates.length,
  skippedFileCount: 0,
  errorCount: 0,
  logs: [],
  records: standardSeriesRecords,
  detection: {
    mode: 'standard',
    confidence: 0.9,
    reasons: ['fixture'],
    samplePaths: standardSeriesRecords.map(record => record.providerPath),
    scores: {
      videoCount: standardSeriesRecords.length,
      sampledCount: standardSeriesRecords.length,
      titleYearFolder: 0,
      titleYearFile: 0,
      seasonFolder: 3,
      episodePattern: 3,
      chineseEpisodePattern: 0,
      categoryTitleSeasonHierarchy: 3,
      sameSeriesEpisodeGroups: 1,
      rootLevelVideos: 0,
      mixedFolderAmbiguity: 0,
      standardScore: 0.9,
      nonStandardScore: 0.1,
    },
  },
  candidates: standardSeriesCandidates,
  scrapedItems: [{
    ...matchedSeriesScrape,
    recordId: standardSeriesCandidates[0].record.id,
    providerPath: standardSeriesCandidates[0].record.providerPath,
    matchStatus: 'notFound',
    matchedSearchTitle: undefined,
    metadata: undefined,
    categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
    categoryAssignment: {
      categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
      source: 'kindFallback',
    },
  }],
}, {
  targetRecordId: standardSeriesCandidates[1].record.id,
  metadata: matchedSeriesMetadata,
  matchedSearchTitle: 'The Legend of Vox Machina',
  searchTitles: ['The Legend of Vox Machina'],
})
const manualScrapesByRecordId = createEffectiveRawScrapeItemMap(manuallyIdentifiedCache.candidates, manuallyIdentifiedCache.scrapedItems)
const manualCategories = manuallyIdentifiedCache.candidates.map(candidate =>
  resolveRawScrapedCategoryAssignment(candidate, manualScrapesByRecordId.get(candidate.record.id)).categoryName)
assert.equal(manuallyIdentifiedCache.scrapedItems?.length, standardSeriesCandidates.length)
assert.deepEqual([...new Set(manualCategories)], ['动漫'])
assert.equal(manualCategories.includes(RAW_UNRESOLVED_CATEGORY_NAME), false)
assert.equal(manuallyIdentifiedCache.scrapedItems?.[0]?.metadata?.titleLogoUrl, 'https://image.tmdb.org/t/p/w500/logo.png')

const seasonTwoEpisodeMetadata: TmdbEpisodeMetadata = {
  ...matchedEpisodeMetadata,
  tmdbEpisodeId: 3376720,
  seasonNumber: 2,
  episodeNumber: 1,
  name: 'Season two episode title',
  overview: 'Season two episode-specific overview must win.',
  runtime: 25,
  rating: 8.5,
  stillUrl: 'https://image.tmdb.org/t/p/w780/season-two-still.jpg',
}
const seasonThreeEpisodeMetadata: TmdbEpisodeMetadata = {
  ...matchedEpisodeMetadata,
  tmdbEpisodeId: 3376730,
  seasonNumber: 3,
  episodeNumber: 1,
  name: 'Season three episode title',
  overview: 'Season three episode-specific overview must win.',
  runtime: 26,
  rating: 8.6,
  stillUrl: 'https://image.tmdb.org/t/p/w780/season-three-still.jpg',
}
const staleManualEpisodeCache = applyRawManualIdentification(createStandardSeriesCache([{
  ...matchedSeriesScrape,
  recordId: standardSeriesCandidates[1].record.id,
  providerPath: standardSeriesCandidates[1].record.providerPath,
  matchStatus: 'notFound',
  matchedSearchTitle: undefined,
  metadata: undefined,
  episodeMetadata: matchedEpisodeMetadata,
  categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
  categoryAssignment: {
    categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
    source: 'kindFallback',
  },
}]), {
  targetRecordId: standardSeriesCandidates[1].record.id,
  metadata: matchedSeriesMetadata,
  matchedSearchTitle: 'The Legend of Vox Machina',
})
const retainedManualEpisodeCache = applyRawManualIdentification(createStandardSeriesCache([{
  ...matchedSeriesScrape,
  recordId: standardSeriesCandidates[1].record.id,
  providerPath: standardSeriesCandidates[1].record.providerPath,
  matchStatus: 'notFound',
  matchedSearchTitle: undefined,
  metadata: undefined,
  episodeMetadata: seasonTwoEpisodeMetadata,
  categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
  categoryAssignment: {
    categoryName: RAW_UNRESOLVED_CATEGORY_NAME,
    source: 'kindFallback',
  },
}]), {
  targetRecordId: standardSeriesCandidates[1].record.id,
  metadata: matchedSeriesMetadata,
  matchedSearchTitle: 'The Legend of Vox Machina',
})
assert.equal(staleManualEpisodeCache.scrapedItems?.find(item => item.recordId === standardSeriesCandidates[1].record.id)?.episodeMetadata, undefined)
assert.equal(
  retainedManualEpisodeCache.scrapedItems?.find(item => item.recordId === standardSeriesCandidates[1].record.id)?.episodeMetadata?.name,
  seasonTwoEpisodeMetadata.name,
)

const multiSeasonEpisodeRequests: string[] = []
const multiSeasonTmdbClient = {
  async searchCandidate(): Promise<TmdbCandidateMatch | null> {
    return {
      metadata: matchedSeriesMetadata,
      searchTitle: 'The Legend of Vox Machina',
    }
  },
  async getEpisodeDetail(tvTmdbId: number, seasonNumber: number, episodeNumber: number): Promise<TmdbEpisodeMetadata> {
    multiSeasonEpisodeRequests.push(`${tvTmdbId}:${seasonNumber}:${episodeNumber}`)
    const metadata = [
      matchedEpisodeMetadata,
      seasonTwoEpisodeMetadata,
      seasonThreeEpisodeMetadata,
    ].find(item => item.tvTmdbId === tvTmdbId && item.seasonNumber === seasonNumber && item.episodeNumber === episodeNumber)
    if (!metadata)
      throw new Error(`Missing fixture for S${seasonNumber}E${episodeNumber}.`)
    return metadata
  },
} satisfies RawTmdbMetadataClient
const multiSeasonEnrichedScrapes = await enrichRawMediaCandidates(standardSeriesCandidates, {
  tmdbClient: multiSeasonTmdbClient,
})
const multiSeasonScrapesByRecordId = new Map(multiSeasonEnrichedScrapes.map(item => [item.recordId, item]))
const multiSeasonDisplayItems = standardSeriesCandidates.map(candidate =>
  toRawScannedMediaItem(candidate, multiSeasonScrapesByRecordId.get(candidate.record.id), 'tv'))
assert.deepEqual(multiSeasonEpisodeRequests, [
  '135934:1:1',
  '135934:2:1',
  '135934:3:1',
])
assert.deepEqual(multiSeasonDisplayItems.map(item => item.name), [
  'S01E01 · The Terror of Tal\'Dorei - Part 1',
  'S02E01 · Season two episode title',
  'S03E01 · Season three episode title',
])
assert.deepEqual(multiSeasonDisplayItems.map(item => item.backdropUrl), [
  matchedEpisodeMetadata.stillUrl,
  seasonTwoEpisodeMetadata.stillUrl,
  seasonThreeEpisodeMetadata.stillUrl,
])
assert.deepEqual(multiSeasonDisplayItems.map(item => item.overview), [
  matchedEpisodeMetadata.overview,
  seasonTwoEpisodeMetadata.overview,
  seasonThreeEpisodeMetadata.overview,
])
assert.deepEqual(multiSeasonDisplayItems.map(item => item.duration), [24 * 60, 25 * 60, 26 * 60])
assert.notEqual(multiSeasonDisplayItems[1]?.overview, matchedSeriesScrape.metadata?.overview)
assert.notEqual(multiSeasonDisplayItems[2]?.overview, matchedSeriesScrape.metadata?.overview)

const splitYearSeriesCandidates = [
  createRecord('/动漫/机械之声的传奇 The Legend of Vox Machina (2022)/Season 01/S01E01.mkv'),
  createRecord('/动漫/机械之声的传奇 The Legend of Vox Machina (2023)/Season 02/S02E01.mkv'),
  createRecord('/动漫/机械之声的传奇 The Legend of Vox Machina (2024)/Season 03/S03E01.mkv'),
].map(record => parseRawMediaCandidate(record))
const splitYearSearchYears: Array<number | undefined> = []
const splitYearEpisodeRequests: string[] = []
const splitYearTmdbClient = {
  async searchCandidate(candidate: RawMediaCandidate): Promise<TmdbCandidateMatch | null> {
    splitYearSearchYears.push(candidate.year)
    return candidate.year === 2022
      ? {
          metadata: matchedSeriesMetadata,
          searchTitle: 'The Legend of Vox Machina',
        }
      : null
  },
  async getEpisodeDetail(tvTmdbId: number, seasonNumber: number, episodeNumber: number): Promise<TmdbEpisodeMetadata> {
    splitYearEpisodeRequests.push(`${tvTmdbId}:${seasonNumber}:${episodeNumber}`)
    const metadata = [
      matchedEpisodeMetadata,
      seasonTwoEpisodeMetadata,
      seasonThreeEpisodeMetadata,
    ].find(item => item.tvTmdbId === tvTmdbId && item.seasonNumber === seasonNumber && item.episodeNumber === episodeNumber)
    if (!metadata)
      throw new Error(`Missing split-year fixture for S${seasonNumber}E${episodeNumber}.`)
    return metadata
  },
} satisfies RawTmdbMetadataClient
const splitYearScrapes = await enrichRawMediaCandidates(splitYearSeriesCandidates, {
  tmdbClient: splitYearTmdbClient,
})
const splitYearScrapesByRecordId = new Map(splitYearScrapes.map(item => [item.recordId, item]))
const splitYearDisplayItems = splitYearSeriesCandidates.map(candidate =>
  toRawScannedMediaItem(candidate, splitYearScrapesByRecordId.get(candidate.record.id), 'tv'))
assert.deepEqual(splitYearSearchYears, [2022, 2023, 2024])
assert.deepEqual(splitYearScrapes.map(item => item.matchStatus), ['matched', 'matched', 'matched'])
assert.deepEqual(splitYearEpisodeRequests, [
  '135934:1:1',
  '135934:2:1',
  '135934:3:1',
])
assert.equal(splitYearScrapesByRecordId.get(splitYearSeriesCandidates[1].record.id)?.matchedSearchTitle, 'The Legend of Vox Machina')
assert.equal(splitYearDisplayItems[1]?.overview, seasonTwoEpisodeMetadata.overview)
assert.equal(splitYearDisplayItems[2]?.posterUrl, seasonThreeEpisodeMetadata.stillUrl)
assert.notEqual(splitYearDisplayItems[1]?.overview, matchedSeriesScrape.metadata?.overview)
assert.notEqual(splitYearDisplayItems[2]?.overview, matchedSeriesScrape.metadata?.overview)

const existingRequestCount = multiSeasonEpisodeRequests.length
const manualBackfilledScrapes = await enrichRawScrapedItemsEpisodeMetadata(
  standardSeriesCandidates,
  manuallyIdentifiedCache.scrapedItems,
  multiSeasonTmdbClient,
)
const manualBackfilledByRecordId = new Map(manualBackfilledScrapes.map(item => [item.recordId, item]))
assert.equal(manualBackfilledByRecordId.get(standardSeriesCandidates[1].record.id)?.episodeMetadata?.name, seasonTwoEpisodeMetadata.name)
assert.equal(manualBackfilledByRecordId.get(standardSeriesCandidates[2].record.id)?.episodeMetadata?.stillUrl, seasonThreeEpisodeMetadata.stillUrl)
assert.deepEqual(multiSeasonEpisodeRequests.slice(existingRequestCount), [
  '135934:1:1',
  '135934:2:1',
  '135934:3:1',
])

const episodeMetadataCache: RawLocalScanCache = {
  version: 1,
  scanId: 'scan-episode-metadata',
  sourceId: 'alist',
  sourceType: 'alist',
  rootPath: '/',
  status: 'completed',
  startedAt: '2026-05-25T00:00:00.000Z',
  finishedAt: '2026-05-25T00:00:01.000Z',
  folderCount: 2,
  fileCount: 1,
  skippedFileCount: 0,
  errorCount: 0,
  logs: [],
  records: [standardSeriesCandidates[0].record],
  detection: createFixtureDetection([standardSeriesCandidates[0].record]),
  candidates: [standardSeriesCandidates[0]],
  scrapedItems: [matchedEpisodeScrape],
}
await withMockLocalStorage(async () => {
  assert.equal(await saveRawSourceScanCache(episodeMetadataCache), true)
  const loadedCache = await loadRawSourceScanCache('alist', 'alist', '/')
  assert.equal(loadedCache?.scrapedItems?.[0]?.episodeMetadata?.stillUrl, matchedEpisodeMetadata.stillUrl)
  assert.equal(loadedCache?.scrapedItems?.[0]?.episodeMetadata?.overview, matchedEpisodeMetadata.overview)
})
await withMockLocalStorage(async () => {
  assert.equal(await saveRawSourceScanCache({
    ...episodeMetadataCache,
    scanId: 'scan-stale-episode-metadata',
    scrapedItems: [{
      ...matchedEpisodeScrape,
      episodeMetadata: seasonTwoEpisodeMetadata,
    }],
  }), true)
  const loadedCache = await loadRawSourceScanCache('alist', 'alist', '/')
  assert.equal(loadedCache?.scrapedItems?.[0]?.episodeMetadata, undefined)
})

const artworkOverrideCache = applyRawManualArtworkOverride(manuallyIdentifiedCache, {
  targetRecordId: standardSeriesCandidates[0].record.id,
  kind: 'logo',
  imageUrl: 'https://image.tmdb.org/t/p/w500/new-logo.png',
  filePath: '/new-logo.png',
})
const artworkOverrideScrapes = createEffectiveRawScrapeItemMap(artworkOverrideCache.candidates, artworkOverrideCache.scrapedItems)
assert.equal(artworkOverrideScrapes.get(standardSeriesCandidates[0].record.id)?.metadata?.titleLogoUrl, 'https://image.tmdb.org/t/p/w500/new-logo.png')
assert.equal(artworkOverrideScrapes.get(standardSeriesCandidates[1].record.id)?.metadata?.titleLogoUrl, 'https://image.tmdb.org/t/p/w500/new-logo.png')

const posterBackdropOverrideCache = applyRawManualArtworkOverride(
  applyRawManualArtworkOverride(artworkOverrideCache, {
    targetRecordId: standardSeriesCandidates[0].record.id,
    kind: 'poster',
    imageUrl: 'https://image.tmdb.org/t/p/w500/new-poster.jpg',
    filePath: '/new-poster.jpg',
  }),
  {
    targetRecordId: standardSeriesCandidates[0].record.id,
    kind: 'backdrop',
    imageUrl: 'https://image.tmdb.org/t/p/w780/new-backdrop.jpg',
    filePath: '/new-backdrop.jpg',
  },
)
const posterBackdropOverrideScrapes = createEffectiveRawScrapeItemMap(posterBackdropOverrideCache.candidates, posterBackdropOverrideCache.scrapedItems)
assert.equal(posterBackdropOverrideScrapes.get(standardSeriesCandidates[0].record.id)?.metadata?.posterUrl, 'https://image.tmdb.org/t/p/w500/new-poster.jpg')
assert.equal(posterBackdropOverrideScrapes.get(standardSeriesCandidates[0].record.id)?.metadata?.backdropUrl, 'https://image.tmdb.org/t/p/w780/new-backdrop.jpg')

const clearedLogoCache = applyRawManualArtworkOverride(artworkOverrideCache, {
  targetRecordId: standardSeriesCandidates[0].record.id,
  kind: 'logo',
})
const clearedLogoScrapes = createEffectiveRawScrapeItemMap(clearedLogoCache.candidates, clearedLogoCache.scrapedItems)
assert.equal(clearedLogoScrapes.get(standardSeriesCandidates[0].record.id)?.metadata?.titleLogoUrl, undefined)

const groupedSeries = groupRawSeriesEntries([
  { candidate: standardSeriesCandidates[0], scraped: matchedSeriesScrape },
  { candidate: standardSeriesCandidates[1] },
])
assert.equal(groupedSeries.length, 1)
assert.equal(groupedSeries[0]?.representative.scraped?.matchStatus, 'matched')
assert.equal(groupedSeries[0]?.title, '机械之声的传奇')
const groupedEpisodeItems = standardSeriesCandidates.map(candidateToMediaItem)
const contextSeasonChildren = createRawSeriesSeasonChildren({
  seriesKey: groupedSeries[0]?.key ?? 'missing',
  sourceId: 'alist',
  libraryId: '/',
  episodes: groupedEpisodeItems,
  artwork: { posterUrl: matchedSeriesScrape.metadata?.posterUrl, backdropUrl: matchedSeriesScrape.metadata?.backdropUrl, titleLogoUrl: matchedSeriesScrape.metadata?.titleLogoUrl },
})
assert.deepEqual(contextSeasonChildren.map(season => season.name), ['Season 01', 'Season 02', 'Season 03'])
assert.deepEqual(contextSeasonChildren.map(season => season.children?.map(episode => episode.seasonNumber)), [[1], [2], [3]])
assert.equal(contextSeasonChildren[0]?.titleLogoUrl, 'https://image.tmdb.org/t/p/w500/logo.png')
const contextualSeries = { children: contextSeasonChildren }
const contextualSeasons = getContextSeriesSeasons(contextualSeries)
assert.equal(contextualSeasons.length, 3)
assert.equal(getPlayableSeasonChildren(contextualSeasons[1]).at(0)?.seasonNumber, 2)

const playbackQueue = createPlaybackQueue([{
  ...groupedEpisodeItems[0]!,
  titleLogoUrl: 'https://image.tmdb.org/t/p/w500/episode-logo.png',
}], groupedEpisodeItems[0]!.id)
assert.ok(playbackQueue)
const playbackContextId = savePlaybackMediaContext({
  sourceId: 'alist',
  itemId: groupedEpisodeItems[0]!.id,
  title: groupedEpisodeItems[0]!.name,
  queue: playbackQueue,
})
assert.equal(getPlaybackMediaContext(playbackContextId)?.queue?.items[0]?.titleLogoUrl, 'https://image.tmdb.org/t/p/w500/episode-logo.png')

const categoryOnlySeasonRecord: RawFileRecord = {
  id: 'alist:/动漫/Season 01/S01E01.mkv',
  sourceId: 'alist',
  sourceType: 'alist',
  rootPath: '/',
  providerPath: '/动漫/Season 01/S01E01.mkv',
  relativePath: '动漫/Season 01/S01E01.mkv',
  parentPath: '/动漫/Season 01',
  fileName: 'S01E01.mkv',
  extension: 'mkv',
}
const categoryOnlySeasonCandidate = parseRawMediaCandidate(categoryOnlySeasonRecord)
const categoryOnlyRecognition = recognizePathAwareMedia(categoryOnlySeasonRecord)
assert.equal(categoryOnlySeasonCandidate.seriesTitle, undefined)
assert.equal(categoryOnlySeasonCandidate.seasonNumber, 1)
assert.equal(categoryOnlySeasonCandidate.episodeNumber, 1)
assert.equal(categoryOnlyRecognition.searchTitles.includes('动漫'), false)

const tokenizedPathPreview = createRawScanPreview([
  {
    name: 'Movie.mkv',
    path: '/Movies/Movie.mkv?X-Amz-Signature=secret&X-Amz-Credential=credential',
    type: 'file',
  },
  {
    name: 'Remote.mkv',
    path: 'https://cdn.example.test/Remote.mkv',
    type: 'file',
  },
], {
  sourceId: 'alist',
  sourceType: 'alist',
  rootPath: '/',
})
assert.equal(tokenizedPathPreview.records.length, 0)

await verifyTmdbEpisodeDetailMapping()

console.log(JSON.stringify({
  cleanedTitle,
  searchTitles,
  tvAnimationCategory: tvAnimation.categoryName,
  zhMovieCategory: zhMovie.categoryName,
  legacyFallbackCategory: legacyFallback.categoryName,
  workFolderCategory: workFolderAssignment.categoryName,
  workFolderCategorySource: workFolderAssignment.source,
  workFolderSeriesTitle: workFolderCandidate.seriesTitle,
  workFolderSeason: workFolderCandidate.seasonNumber,
  workFolderEpisode: workFolderCandidate.episodeNumber,
  workFolderSearchTitles: workFolderRecognition.searchTitles,
  standardSeriesCategoryHint: standardSeriesCandidates[0].categoryHint,
  standardSeriesSeasons: standardSeriesCandidates.map(candidate => candidate.seasonNumber),
  pathHintMatchedCategory: pathHintMatchedAssignment.categoryName,
  pathHintMatchedDisplayCategory: pathHintMatchedDisplayAssignment.categoryName,
  missingScrapeDisplayCategory: missingScrapeDisplayAssignment.categoryName,
  episodeDisplayStillUrl: episodeDisplayItem.backdropUrl,
  episodeDisplayOverview: episodeDisplayItem.overview,
  fallbackEpisodeBackdropUrl: fallbackEpisodeDisplayItem.backdropUrl,
  staleEpisodeDisplayBackdropUrl: staleEpisodeDisplayItem.backdropUrl,
  mixedSeriesDisplayCategories,
  noRepresentativeDisplayCategories,
  manualCategories,
  cachedEpisodeStillUrl: matchedEpisodeMetadata.stillUrl,
  retainedManualEpisodeTitle: retainedManualEpisodeCache.scrapedItems?.find(item => item.recordId === standardSeriesCandidates[1].record.id)?.episodeMetadata?.name,
  manualTitleLogoUrl: manuallyIdentifiedCache.scrapedItems?.[0]?.metadata?.titleLogoUrl,
  artworkLogoOverride: artworkOverrideScrapes.get(standardSeriesCandidates[0].record.id)?.metadata?.titleLogoUrl,
  posterOverride: posterBackdropOverrideScrapes.get(standardSeriesCandidates[0].record.id)?.metadata?.posterUrl,
  backdropOverride: posterBackdropOverrideScrapes.get(standardSeriesCandidates[0].record.id)?.metadata?.backdropUrl,
  clearedLogoUrl: clearedLogoScrapes.get(standardSeriesCandidates[0].record.id)?.metadata?.titleLogoUrl ?? null,
  noPathHintMatchedCategory: noPathHintMatchedAssignment.categoryName,
  noPathHintMatchedDisplayCategory: noPathHintMatchedDisplayAssignment.categoryName,
  unmatchedDisplayCategories,
  groupedSeriesCount: groupedSeries.length,
  contextSeasonChildren: contextSeasonChildren.map(season => ({
    name: season.name,
    episodeCount: season.children?.length ?? 0,
    titleLogoUrl: season.titleLogoUrl,
  })),
  playbackQueueTitleLogoUrl: getPlaybackMediaContext(playbackContextId)?.queue?.items[0]?.titleLogoUrl,
  flatEpisodeSeriesTitle: flatEpisodeCandidate.seriesTitle,
  categoryOnlySeasonSearchTitles: categoryOnlyRecognition.searchTitles,
  tokenizedPathRecordCount: tokenizedPathPreview.records.length,
}, null, 2))

async function verifyTmdbEpisodeDetailMapping(): Promise<void> {
  const fetchDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'fetch')
  const windowDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'window')
  let requestedUrl = ''
  const tmdb = new TmdbScraper({
    authType: 'apiKey',
    value: 'test-key',
  }, {
    credentialRef: 'settings:tmdb-credential',
    authType: 'apiKey',
    language: 'zh-CN',
    region: 'CN',
  }, 1_000)

  try {
    Object.defineProperty(globalThis, 'window', {
      value: globalThis,
      configurable: true,
    })

    const episodeFetch: typeof fetch = async (input: RequestInfo | URL) => {
      requestedUrl = String(input)
      return new Response(JSON.stringify({
        id: 3376714,
        name: 'The Terror of Tal\'Dorei - Part 1',
        overview: 'TMDB episode overview.',
        still_path: '/tmdb-still.jpg',
        air_date: '2022-01-28',
        runtime: 24,
        vote_average: 8.4,
        season_number: 1,
        episode_number: 1,
      }), { status: 200 })
    }
    Object.defineProperty(globalThis, 'fetch', {
      value: episodeFetch,
      configurable: true,
    })

    const episode = await tmdb.getEpisodeDetail(135934, 1, 1)
    assert.equal(requestedUrl.includes('/tv/135934/season/1/episode/1'), true)
    assert.equal(requestedUrl.includes('language=zh-CN'), true)
    assert.equal(episode.name, 'The Terror of Tal\'Dorei - Part 1')
    assert.equal(episode.overview, 'TMDB episode overview.')
    assert.equal(episode.stillPath, '/tmdb-still.jpg')
    assert.equal(episode.stillUrl, 'https://image.tmdb.org/t/p/w780/tmdb-still.jpg')
    assert.equal(episode.runtime, 24)
    assert.equal(episode.rating, 8.4)

    const invalidFetch: typeof fetch = async () => new Response(JSON.stringify({ name: 'Missing ID' }), { status: 200 })
    Object.defineProperty(globalThis, 'fetch', {
      value: invalidFetch,
      configurable: true,
    })
    await assert.rejects(
      () => tmdb.getEpisodeDetail(135934, 1, 2),
      /TMDB episode response is incomplete/,
    )
  }
  finally {
    if (fetchDescriptor)
      Object.defineProperty(globalThis, 'fetch', fetchDescriptor)
    if (windowDescriptor)
      Object.defineProperty(globalThis, 'window', windowDescriptor)
    else
      delete (globalThis as { window?: unknown }).window
  }
}

async function withMockLocalStorage(callback: () => Promise<void>): Promise<void> {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const values = new Map<string, string>()
  const storage: Storage = {
    get length() {
      return values.size
    },
    clear() {
      values.clear()
    },
    getItem(key: string) {
      return values.get(key) ?? null
    },
    key(index: number) {
      return [...values.keys()][index] ?? null
    },
    removeItem(key: string) {
      values.delete(key)
    },
    setItem(key: string, value: string) {
      values.set(key, value)
    },
  }

  try {
    Object.defineProperty(globalThis, 'localStorage', {
      value: storage,
      configurable: true,
    })
    await callback()
  }
  finally {
    if (descriptor)
      Object.defineProperty(globalThis, 'localStorage', descriptor)
    else
      delete (globalThis as { localStorage?: Storage }).localStorage
  }
}

function createFixtureDetection(records: readonly RawFileRecord[]): RawLocalScanCache['detection'] {
  return {
    mode: 'standard',
    confidence: 0.9,
    reasons: ['fixture'],
    samplePaths: records.map(record => record.providerPath),
    scores: {
      videoCount: records.length,
      sampledCount: records.length,
      titleYearFolder: 0,
      titleYearFile: 0,
      seasonFolder: records.length,
      episodePattern: records.length,
      chineseEpisodePattern: 0,
      categoryTitleSeasonHierarchy: records.length,
      sameSeriesEpisodeGroups: records.length > 1 ? 1 : 0,
      rootLevelVideos: 0,
      mixedFolderAmbiguity: 0,
      standardScore: 0.9,
      nonStandardScore: 0.1,
    },
  }
}

function createStandardSeriesCache(scrapedItems: readonly RawScrapedMediaItem[]): RawLocalScanCache {
  return {
    version: 1,
    scanId: 'scan-standard-series-fixture',
    sourceId: 'alist',
    sourceType: 'alist',
    rootPath: '/',
    status: 'completed',
    startedAt: '2026-05-25T00:00:00.000Z',
    finishedAt: '2026-05-25T00:00:00.000Z',
    folderCount: 4,
    fileCount: standardSeriesCandidates.length,
    skippedFileCount: 0,
    errorCount: 0,
    logs: [],
    records: standardSeriesRecords,
    detection: createFixtureDetection(standardSeriesRecords),
    candidates: standardSeriesCandidates,
    scrapedItems: [...scrapedItems],
  }
}

function createRecord(providerPath: string): RawFileRecord {
  const fileName = providerPath.split('/').at(-1) ?? 'video.mkv'
  const parentPath = providerPath.slice(0, providerPath.lastIndexOf('/')) || '/'
  return {
    id: `alist:${providerPath}`,
    sourceId: 'alist',
    sourceType: 'alist',
    rootPath: '/',
    providerPath,
    relativePath: providerPath.replace(/^\/+/, ''),
    parentPath,
    fileName,
    extension: 'mkv',
  }
}

function candidateToMediaItem(candidate: typeof standardSeriesCandidates[number]): MediaItem {
  return {
    id: candidate.record.providerPath,
    sourceId: candidate.record.sourceId,
    libraryId: candidate.record.rootPath,
    name: candidate.title,
    type: 'episode',
    path: candidate.record.providerPath,
    seriesName: candidate.seriesTitle,
    seasonNumber: candidate.seasonNumber,
    episodeNumber: candidate.episodeNumber,
  }
}
