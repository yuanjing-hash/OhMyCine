import assert from 'node:assert/strict'
import { AlistDataSource } from '../src/services/datasource/alist.ts'
import { collectHomeSectionsFromSources } from '../src/services/datasource/homeAggregation.ts'
import { loadRawSourceScanCache, saveRawSourceScanCache } from '../src/services/scraper/localScanCache.ts'
import { createRawSourceIndexScheduler } from '../src/services/scraper/rawSourceIndexScheduler.ts'
import { createRawScanPreview } from '../src/services/scraper/scanner.ts'
import type { DataSource, DataSourceConfig, DataSourceType, HomeSection, MediaDetail, MediaItem } from '../src/services/datasource/types.ts'
import type { RawLocalScanCache } from '../src/services/scraper/localScanCache.ts'
import type { RawProviderScanItem, RawScrapedMediaItem } from '../src/services/scraper/types.ts'
import type { TmdbMetadata } from '../src/services/scraper/tmdb.ts'

class MemoryStorage {
  private readonly values = new Map<string, string>()

  getItem(key: string): string | null {
    return this.values.get(key) ?? null
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value)
  }
}

const embyHeroItem: MediaItem = {
  id: 'emby-movie-1',
  sourceId: 'emby-home',
  name: 'Emby Movie',
  type: 'movie',
  path: 'emby-movie-1',
  posterUrl: 'https://example.test/poster.jpg',
  backdropUrl: 'https://example.test/backdrop.jpg',
  titleLogoUrl: 'https://example.test/logo.png',
  overview: 'Emby metadata should remain visible when OpenList fails.',
  modified: '2026-05-23T00:00:00.000Z',
}

const embySource = createFakeSource({
  id: 'emby-home',
  name: '家庭 Emby',
  type: 'emby',
  getHomeSections: async () => [
    {
      id: 'hero-emby-home',
      sourceId: 'emby-home',
      title: '家庭 Emby 精选',
      type: 'hero',
      items: [embyHeroItem],
    },
    {
      id: 'continue-emby-home',
      sourceId: 'emby-home',
      title: '家庭 Emby 继续观看',
      type: 'continueWatching',
      items: [{ ...embyHeroItem, id: 'emby-resume-1', resumePosition: 120 }],
    },
    {
      id: 'recent-emby-home',
      sourceId: 'emby-home',
      title: '家庭 Emby 最新入库',
      type: 'recentlyAdded',
      items: [{ ...embyHeroItem, id: 'emby-recent-1' }],
    },
  ],
})

const failingOpenListSource = createFakeSource({
  id: 'alist-broken',
  name: 'OpenList/Alist',
  type: 'alist',
  getHomeSections: async () => {
    throw new Error('TMDB unavailable or source scan failed')
  },
})

const scheduler = createRawSourceIndexScheduler({
  storage: new MemoryStorage(),
  scanRunner: async () => {
    throw new Error('OpenList background index failed')
  },
})

const indexResults = await scheduler.triggerAutoIndexForTargets([
  {
    sourceId: 'alist-broken',
    sourceType: 'alist',
    rootPath: '/影视库',
    source: failingOpenListSource,
  },
])
const sections = await collectHomeSectionsFromSources([failingOpenListSource, embySource])

assert.equal(indexResults.length, 1)
assert.equal(indexResults[0]?.state, 'failed')
assert.equal(indexResults[0]?.skipped, false)
assert.equal(sections.some(section => section.id.startsWith('error-')), false)
assert.equal(sections.find(section => section.type === 'hero')?.items[0]?.sourceId, 'emby-home')
assert.equal(sections.find(section => section.type === 'continueWatching')?.items[0]?.sourceId, 'emby-home')
assert.equal(sections.find(section => section.type === 'recentlyAdded')?.items[0]?.sourceId, 'emby-home')

const restoreLocalStorage = installMockLocalStorage()
try {
  const openListCache = createOpenListHomeCache('alist-home')
  assert.equal(await saveRawSourceScanCache(openListCache), true)

  const alistSource = new AlistDataSource({
    readCredential: async () => ({ token: 'token', username: 'alice', password: 'stored-password' }),
    saveCredential: async () => {},
  })
  await alistSource.init(createAlistConfig('alist-home'))

  const alistHomeSections = await alistSource.getHomeSections()
  const alistHeroItems = alistHomeSections.find(section => section.type === 'hero')?.items ?? []
  const alistRecentItems = alistHomeSections.find(section => section.type === 'recentlyAdded')?.items ?? []
  const alistSeriesItem = alistRecentItems.find(item => item.type === 'series')
  const alistMovieItem = alistRecentItems.find(item => item.type === 'movie')

  assert.equal(alistHeroItems.some(item => item.sourceId === 'alist-home'), true)
  assert.equal(alistRecentItems.some(item => item.id.includes('Loose.File')), false)
  assert.ok(alistSeriesItem)
  assert.ok(alistMovieItem)

  const seriesDetail = await alistSource.getDetail(alistSeriesItem.id)
  const seriesChildren = await alistSource.list(alistSeriesItem.id)
  const firstSeason = seriesChildren[0]
  assert.equal(seriesDetail.type, 'series')
  assert.equal(firstSeason?.type, 'season')
  assert.equal(firstSeason?.children?.[0]?.id, '/影视库/动漫/Open Show/Season 01/S01E01.mkv')
  assert.equal((await alistSource.list(firstSeason.id))[0]?.id, '/影视库/动漫/Open Show/Season 01/S01E01.mkv')

  const movieDetail = await alistSource.getDetail(alistMovieItem.id)
  assert.equal(movieDetail.tmdbId, 1001)
  assert.equal(movieDetail.mediaSources?.[0]?.isRemote, true)

  const mixedSections = await collectHomeSectionsFromSources([embySource, alistSource])
  const mixedHeroSourceIds = new Set(mixedSections.find(section => section.type === 'hero')?.items.map(item => item.sourceId))
  const mixedRecentSourceIds = new Set(mixedSections.find(section => section.type === 'recentlyAdded')?.items.map(item => item.sourceId))
  assert.equal(mixedHeroSourceIds.has('emby-home'), true)
  assert.equal(mixedHeroSourceIds.has('alist-home'), true)
  assert.equal(mixedRecentSourceIds.has('emby-home'), true)
  assert.equal(mixedRecentSourceIds.has('alist-home'), true)

  assert.equal(await saveRawSourceScanCache(createOnlyUnmatchedOpenListCache('alist-unmatched')), true)
  const unmatchedAlistSource = new AlistDataSource({
    readCredential: async () => ({ token: 'token', username: 'alice', password: 'stored-password' }),
    saveCredential: async () => {},
  })
  await unmatchedAlistSource.init(createAlistConfig('alist-unmatched'))
  assert.deepEqual(await unmatchedAlistSource.getHomeSections(), [])

  assert.equal(await saveRawSourceScanCache(createSensitiveOpenListCache('alist-sensitive')), true)
  const sanitizedSensitiveCache = await loadRawSourceScanCache('alist-sensitive', 'alist', '/影视库')
  const serializedSensitiveCache = JSON.stringify(sanitizedSensitiveCache)
  assert.equal(sanitizedSensitiveCache?.records.length, 0)
  assert.equal(serializedSensitiveCache.includes('token-secret'), false)
  assert.equal(serializedSensitiveCache.includes('sign-secret'), false)
  const sensitiveAlistSource = new AlistDataSource({
    readCredential: async () => ({ token: 'token', username: 'alice', password: 'stored-password' }),
    saveCredential: async () => {},
  })
  await sensitiveAlistSource.init(createAlistConfig('alist-sensitive'))
  assert.deepEqual(await sensitiveAlistSource.getHomeSections(), [])

  console.log(JSON.stringify({
    openListMatchedHeroCount: alistHeroItems.length,
    openListMatchedRecentCount: alistRecentItems.length,
    openListSeriesChildren: seriesChildren.length,
    mixedHeroSourceIds: [...mixedHeroSourceIds],
    mixedRecentSourceIds: [...mixedRecentSourceIds],
    unmatchedOpenListSectionCount: (await unmatchedAlistSource.getHomeSections()).length,
    sensitiveOpenListRecordCount: sanitizedSensitiveCache?.records.length,
  }, null, 2))
}
finally {
  restoreLocalStorage()
}

console.log(JSON.stringify({
  openListIndexState: indexResults[0]?.state,
  sectionCount: sections.length,
  heroSourceId: sections.find(section => section.type === 'hero')?.sourceId,
  continueSourceId: sections.find(section => section.type === 'continueWatching')?.sourceId,
  latestSourceId: sections.find(section => section.type === 'recentlyAdded')?.sourceId,
}, null, 2))

function createAlistConfig(id: string): DataSourceConfig {
  return {
    id,
    type: 'alist',
    name: 'OpenList/Alist',
    displayName: 'OpenList/Alist',
    order: 1,
    url: 'https://openlist.example.test',
    enabled: true,
    extra: {
      credentialRef: `datasource:${id}:alist-credential`,
      rootPath: '/影视库',
    },
  }
}

function createOpenListHomeCache(sourceId: string): RawLocalScanCache {
  const preview = createRawScanPreview([
    createScanItem('/影视库/电影/Open Movie (2026)/Open Movie (2026).mkv', '2026-05-28T09:00:00.000Z'),
    createScanItem('/影视库/动漫/Open Show/Season 01/S01E01.mkv', '2026-05-29T10:00:00.000Z'),
    createScanItem('/影视库/未识别/Loose.File.mkv', '2026-05-30T10:00:00.000Z'),
  ], {
    sourceId,
    sourceType: 'alist',
    rootPath: '/影视库',
  })
  const candidates = preview.candidates
  const candidateByPath = new Map(candidates.map(candidate => [candidate.record.providerPath, candidate]))
  const movieCandidate = candidateByPath.get('/影视库/电影/Open Movie (2026)/Open Movie (2026).mkv')
  const seriesCandidate = candidateByPath.get('/影视库/动漫/Open Show/Season 01/S01E01.mkv')
  const failedCandidate = candidateByPath.get('/影视库/未识别/Loose.File.mkv')
  assert.ok(movieCandidate)
  assert.ok(seriesCandidate)
  assert.ok(failedCandidate)

  const scrapedItems: RawScrapedMediaItem[] = [
    createMatchedScrapedItem(movieCandidate, createTmdbMetadata({
      tmdbId: 1001,
      mediaType: 'movie',
      title: 'Open Movie',
      releaseYear: 2026,
      posterUrl: 'https://image.tmdb.org/t/p/w500/open-movie-poster.jpg',
      backdropUrl: 'https://image.tmdb.org/t/p/w780/open-movie-backdrop.jpg',
      titleLogoUrl: 'https://image.tmdb.org/t/p/w500/open-movie-logo.png',
      overview: 'Matched OpenList movie metadata.',
    })),
    createMatchedScrapedItem(seriesCandidate, createTmdbMetadata({
      tmdbId: 2002,
      mediaType: 'tv',
      title: 'Open Show',
      releaseYear: 2025,
      posterUrl: 'https://image.tmdb.org/t/p/w500/open-show-poster.jpg',
      backdropUrl: 'https://image.tmdb.org/t/p/w780/open-show-backdrop.jpg',
      titleLogoUrl: 'https://image.tmdb.org/t/p/w500/open-show-logo.png',
      overview: 'Matched OpenList series metadata.',
    })),
    {
      recordId: failedCandidate.record.id,
      providerPath: failedCandidate.record.providerPath,
      matchStatus: 'failed',
      searchTitles: ['Loose File'],
      mediaType: 'movie',
      categoryName: '未识别',
      categoryAssignment: { categoryName: '未识别', source: 'kindFallback' },
      errorMessage: 'TMDB unavailable',
    },
  ]

  return createCacheFromPreview(sourceId, preview, scrapedItems)
}

function createOnlyUnmatchedOpenListCache(sourceId: string): RawLocalScanCache {
  const preview = createRawScanPreview([
    createScanItem('/影视库/未识别/Only.Unknown.mkv', '2026-05-30T10:00:00.000Z'),
  ], {
    sourceId,
    sourceType: 'alist',
    rootPath: '/影视库',
  })
  const candidate = preview.candidates[0]
  assert.ok(candidate)

  return createCacheFromPreview(sourceId, preview, [{
    recordId: candidate.record.id,
    providerPath: candidate.record.providerPath,
    matchStatus: 'notFound',
    searchTitles: ['Only Unknown'],
    mediaType: 'movie',
    categoryName: '未识别',
    categoryAssignment: { categoryName: '未识别', source: 'kindFallback' },
  }])
}

function createSensitiveOpenListCache(sourceId: string): RawLocalScanCache {
  const cache = createOpenListHomeCache(sourceId)
  const sensitivePath = 'https://openlist.example.test/d/%E5%BD%B1%E8%A7%86%E5%BA%93/Open.Movie.mkv?sign=sign-secret&token=token-secret'
  const record = cache.records[0]
  const candidate = cache.candidates[0]
  const scraped = cache.scrapedItems?.[0]
  assert.ok(record)
  assert.ok(candidate)
  assert.ok(scraped)

  const unsafeRecord = {
    ...record,
    id: `${sourceId}:${sensitivePath}`,
    providerPath: sensitivePath,
    relativePath: sensitivePath,
    parentPath: sensitivePath,
  }

  return {
    ...cache,
    records: [unsafeRecord],
    detection: {
      ...cache.detection,
      samplePaths: [sensitivePath],
    },
    candidates: [{
      ...candidate,
      record: unsafeRecord,
    }],
    scrapedItems: [{
      ...scraped,
      recordId: unsafeRecord.id,
      providerPath: sensitivePath,
    }],
    logs: [{
      timestamp: '2026-05-30T00:00:00.000Z',
      level: 'warning',
      message: `provider returned tokenized URL ${sensitivePath}`,
      path: sensitivePath,
    }],
  }
}

function createCacheFromPreview(
  sourceId: string,
  preview: ReturnType<typeof createRawScanPreview>,
  scrapedItems: RawScrapedMediaItem[],
): RawLocalScanCache {
  const scrapedByRecordId = new Map(scrapedItems.map(item => [item.recordId, item]))
  return {
    version: 1,
    scanId: `scan-${sourceId}`,
    sourceId,
    sourceType: 'alist',
    rootPath: '/影视库',
    status: 'completed',
    startedAt: '2026-05-30T00:00:00.000Z',
    finishedAt: '2026-05-30T00:01:00.000Z',
    folderCount: 3,
    fileCount: preview.records.length,
    skippedFileCount: 0,
    errorCount: 0,
    logs: [],
    ...preview,
    candidates: preview.candidates.map(candidate => ({
      ...candidate,
      scrapeMetadata: scrapedByRecordId.get(candidate.record.id)?.metadata,
      categoryAssignment: scrapedByRecordId.get(candidate.record.id)?.categoryAssignment,
    })),
    scrapedItems,
  }
}

function createScanItem(providerPath: string, modifiedAt: string): RawProviderScanItem {
  return {
    name: providerPath.split('/').at(-1) ?? 'video.mkv',
    path: providerPath,
    providerPath,
    parentPath: providerPath.slice(0, providerPath.lastIndexOf('/')) || '/',
    type: 'file',
    isDirectory: false,
    size: 1024,
    modifiedAt,
  }
}

function createMatchedScrapedItem(candidate: NonNullable<RawLocalScanCache['candidates'][number]>, metadata: TmdbMetadata): RawScrapedMediaItem {
  const categoryAssignment = {
    categoryName: metadata.mediaType === 'tv' ? '动漫' : '电影',
    source: 'pathHint' as const,
  }
  return {
    recordId: candidate.record.id,
    providerPath: candidate.record.providerPath,
    matchStatus: 'matched',
    searchTitles: [metadata.title],
    matchedSearchTitle: metadata.title,
    metadata,
    mediaType: metadata.mediaType,
    categoryName: categoryAssignment.categoryName,
    categoryAssignment,
  }
}

function createTmdbMetadata(input: {
  tmdbId: number
  mediaType: TmdbMetadata['mediaType']
  title: string
  releaseYear: number
  posterUrl: string
  backdropUrl: string
  titleLogoUrl: string
  overview: string
}): TmdbMetadata {
  return {
    tmdbId: input.tmdbId,
    mediaType: input.mediaType,
    title: input.title,
    overview: input.overview,
    releaseYear: input.releaseYear,
    rating: 8.2,
    genreIds: [],
    genres: [],
    originCountries: [],
    productionCountries: [],
    posterUrl: input.posterUrl,
    backdropUrl: input.backdropUrl,
    titleLogoUrl: input.titleLogoUrl,
    scrapedAt: '2026-05-30T00:00:00.000Z',
  }
}

function installMockLocalStorage(): () => void {
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

  Object.defineProperty(globalThis, 'localStorage', {
    value: storage,
    configurable: true,
  })

  return () => {
    if (descriptor)
      Object.defineProperty(globalThis, 'localStorage', descriptor)
    else
      delete (globalThis as { localStorage?: Storage }).localStorage
  }
}

function createFakeSource(input: {
  id: string
  name: string
  type: DataSourceType
  getHomeSections?: () => Promise<HomeSection[]>
}): DataSource {
  const config: DataSourceConfig = {
    id: input.id,
    type: input.type,
    name: input.name,
    order: 0,
    url: 'https://example.test',
    enabled: true,
  }

  return {
    id: input.id,
    name: input.name,
    type: input.type,
    isConnected: true,
    init: async () => {},
    test: async () => true,
    destroy: () => {},
    list: async () => [],
    listLibraries: async () => [],
    getHomeSections: input.getHomeSections,
    search: async () => [],
    getDetail: async (id: string): Promise<MediaDetail> => ({
      id,
      sourceId: input.id,
      name: input.name,
      type: 'movie',
      path: id,
    }),
    getStreamURL: async (id: string) => id,
    exportConfig: () => config,
  }
}
