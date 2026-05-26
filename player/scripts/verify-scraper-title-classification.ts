import assert from 'node:assert/strict'
import { deriveRawCandidateCategoryAssignment, RAW_UNRESOLVED_CATEGORY_NAME, resolveRawCandidateCategoryAssignment, resolveRawScrapedCategoryAssignment } from '../src/services/scraper/categoryGrouping.ts'
import { classifyScrapeMetadata, DEFAULT_SCRAPE_CLASSIFICATION_RULES } from '../src/services/scraper/classificationRules.ts'
import { applyRawManualIdentification, createEffectiveRawScrapeItemMap } from '../src/services/scraper/manualIdentification.ts'
import { recognizePathAwareMedia } from '../src/services/scraper/pathRecognition.ts'
import { cleanMediaTitle, extractMediaSearchTitles, parseRawMediaCandidate } from '../src/services/scraper/parser.ts'
import { createRawSeriesSeasonChildren, getContextSeriesSeasons, getPlayableSeasonChildren, groupRawSeriesEntries } from '../src/services/scraper/rawSeriesGrouping.ts'
import { createRawScanPreview } from '../src/services/scraper/scanner.ts'
import type { MediaItem } from '../src/services/datasource/types.ts'
import type { RawFileRecord, RawScrapedMediaItem, RawTmdbMatchStatus } from '../src/services/scraper/types.ts'

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
  artwork: { posterUrl: matchedSeriesScrape.metadata?.posterUrl, backdropUrl: matchedSeriesScrape.metadata?.backdropUrl },
})
assert.deepEqual(contextSeasonChildren.map(season => season.name), ['Season 01', 'Season 02', 'Season 03'])
assert.deepEqual(contextSeasonChildren.map(season => season.children?.map(episode => episode.seasonNumber)), [[1], [2], [3]])
const contextualSeries = { children: contextSeasonChildren }
const contextualSeasons = getContextSeriesSeasons(contextualSeries)
assert.equal(contextualSeasons.length, 3)
assert.equal(getPlayableSeasonChildren(contextualSeasons[1]).at(0)?.seasonNumber, 2)

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
  mixedSeriesDisplayCategories,
  noRepresentativeDisplayCategories,
  manualCategories,
  noPathHintMatchedCategory: noPathHintMatchedAssignment.categoryName,
  noPathHintMatchedDisplayCategory: noPathHintMatchedDisplayAssignment.categoryName,
  unmatchedDisplayCategories,
  groupedSeriesCount: groupedSeries.length,
  contextSeasonChildren: contextSeasonChildren.map(season => ({
    name: season.name,
    episodeCount: season.children?.length ?? 0,
  })),
  flatEpisodeSeriesTitle: flatEpisodeCandidate.seriesTitle,
  categoryOnlySeasonSearchTitles: categoryOnlyRecognition.searchTitles,
  tokenizedPathRecordCount: tokenizedPathPreview.records.length,
}, null, 2))

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
