import assert from 'node:assert/strict'
import { deriveRawCandidateCategoryAssignment } from '../src/services/scraper/categoryGrouping.ts'
import { classifyScrapeMetadata, DEFAULT_SCRAPE_CLASSIFICATION_RULES } from '../src/services/scraper/classificationRules.ts'
import { cleanMediaTitle, extractMediaSearchTitles, parseRawMediaCandidate } from '../src/services/scraper/parser.ts'
import type { RawFileRecord } from '../src/services/scraper/types.ts'

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
assert.equal(workFolderCandidate.seriesTitle, '机械之声的传奇 The Legend of Vox Machina')
assert.equal(workFolderAssignment.categoryName, '剧集')
assert.equal(workFolderAssignment.source, 'kindFallback')

console.log(JSON.stringify({
  cleanedTitle,
  searchTitles,
  tvAnimationCategory: tvAnimation.categoryName,
  zhMovieCategory: zhMovie.categoryName,
  legacyFallbackCategory: legacyFallback.categoryName,
  workFolderCategory: workFolderAssignment.categoryName,
  workFolderCategorySource: workFolderAssignment.source,
}, null, 2))
