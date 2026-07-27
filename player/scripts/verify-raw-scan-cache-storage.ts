import assert from 'node:assert/strict'
import { clearRawSourceScanCache, diffRawFileRecords, loadRawSourceScanCache, saveRawSourceScanCache } from '../src/services/scraper/localScanCache.ts'
import type { RawLocalScanCache } from '../src/services/scraper/localScanCache.ts'
import type { RawFileRecord, RawMediaCandidate } from '../src/services/scraper/types.ts'

const restoreLocalStorage = installMockLocalStorage()

try {
  const firstCache = createCache({
    sourceId: 'alist-one',
    rootPath: '/影视库',
    scanId: 'scan-one',
    title: 'First Movie',
    posterUrl: 'https://image.tmdb.org/t/p/w500/first.jpg',
  })
  assert.equal(await saveRawSourceScanCache(firstCache), true)

  const loadedFirst = await loadRawSourceScanCache('alist-one', 'alist', '/影视库')
  assert.equal(loadedFirst?.scanId, 'scan-one')
  assert.equal(loadedFirst?.scrapedItems?.[0]?.metadata?.title, 'First Movie')

  const overwrittenCache = createCache({
    sourceId: 'alist-one',
    rootPath: '/影视库',
    scanId: 'scan-two',
    title: 'Updated Movie',
    posterUrl: 'https://image.tmdb.org/t/p/w500/updated.jpg',
  })
  assert.equal(await saveRawSourceScanCache(overwrittenCache), true)
  assert.equal((await loadRawSourceScanCache('alist-one', 'alist', '/影视库'))?.scanId, 'scan-two')

  const isolatedCache = createCache({
    sourceId: 'alist-one',
    rootPath: '/纪录片',
    scanId: 'scan-documentary',
    title: 'Documentary Movie',
    posterUrl: 'https://image.tmdb.org/t/p/w500/documentary.jpg',
  })
  assert.equal(await saveRawSourceScanCache(isolatedCache), true)
  assert.equal((await loadRawSourceScanCache('alist-one', 'alist', '/影视库'))?.scanId, 'scan-two')
  assert.equal((await loadRawSourceScanCache('alist-one', 'alist', '/纪录片'))?.scanId, 'scan-documentary')
  assert.equal(await loadRawSourceScanCache('alist-two', 'alist', '/影视库'), null)

  const sensitiveCache = createCache({
    sourceId: 'alist-sensitive',
    rootPath: '/影视库',
    scanId: 'scan-sensitive',
    title: 'Sensitive Movie',
    posterUrl: 'https://image.tmdb.org/t/p/w500/sensitive.jpg?token=secret',
  })
  assert.equal(await saveRawSourceScanCache(sensitiveCache), true)
  const loadedSensitive = await loadRawSourceScanCache('alist-sensitive', 'alist', '/影视库')
  const serializedSensitive = JSON.stringify(loadedSensitive)
  assert.equal(loadedSensitive?.scrapedItems?.[0]?.metadata?.posterUrl, undefined)
  assert.equal(serializedSensitive.includes('secret'), false)

  const baseRecord = loadedFirst?.records[0]
  assert.ok(baseRecord)
  const diffSummary = diffRawFileRecords([baseRecord], [
    {
      ...baseRecord,
      size: (baseRecord.size ?? 0) + 1,
    },
    {
      ...baseRecord,
      id: 'alist-one:/影视库/New.Movie.mkv',
      providerPath: '/影视库/New.Movie.mkv',
      relativePath: 'New.Movie.mkv',
      fileName: 'New.Movie.mkv',
    },
  ])
  assert.deepEqual(diffSummary, {
    added: 1,
    removed: 0,
    changed: 1,
    unchanged: 0,
  })

  await clearRawSourceScanCache('alist-one', 'alist', '/影视库')
  assert.equal(await loadRawSourceScanCache('alist-one', 'alist', '/影视库'), null)
  assert.equal((await loadRawSourceScanCache('alist-one', 'alist', '/纪录片'))?.scanId, 'scan-documentary')

  console.log(JSON.stringify({
    loadedFirstTitle: loadedFirst?.scrapedItems?.[0]?.metadata?.title,
    overwrittenScanId: (await loadRawSourceScanCache('alist-one', 'alist', '/纪录片'))?.scanId,
    sensitivePosterPreserved: loadedSensitive?.scrapedItems?.[0]?.metadata?.posterUrl != null,
  }, null, 2))
}
finally {
  restoreLocalStorage()
}

function createCache(input: {
  sourceId: string
  rootPath: string
  scanId: string
  title: string
  posterUrl: string
}): RawLocalScanCache {
  const record: RawFileRecord = {
    id: `${input.sourceId}:${input.rootPath}/Movie.mkv`,
    sourceId: input.sourceId,
    sourceType: 'alist',
    rootPath: input.rootPath,
    providerPath: `${input.rootPath}/Movie.mkv`,
    relativePath: 'Movie.mkv',
    parentPath: input.rootPath,
    fileName: 'Movie.mkv',
    extension: 'mkv',
    size: 1024,
    modifiedAt: '2026-07-08T00:00:00.000Z',
  }
  const candidate: RawMediaCandidate = {
    kind: 'movie',
    parseStatus: 'parsed',
    record,
    title: input.title,
    normalizedTitle: input.title.toLowerCase(),
    year: 2026,
    confidence: 0.9,
    signals: ['verify'],
  }

  return {
    version: 1,
    scanId: input.scanId,
    sourceId: input.sourceId,
    sourceType: 'alist',
    rootPath: input.rootPath,
    status: 'completed',
    startedAt: '2026-07-08T00:00:00.000Z',
    finishedAt: '2026-07-08T00:00:01.000Z',
    folderCount: 1,
    fileCount: 1,
    skippedFileCount: 0,
    errorCount: 0,
    logs: [],
    records: [record],
    detection: {
      mode: 'nonStandard',
      confidence: 0.8,
      reasons: ['verify fixture'],
      samplePaths: [record.providerPath],
      scores: {
        videoCount: 1,
        sampledCount: 1,
        titleYearFolder: 0,
        titleYearFile: 0,
        seasonFolder: 0,
        episodePattern: 0,
        chineseEpisodePattern: 0,
        categoryTitleSeasonHierarchy: 0,
        sameSeriesEpisodeGroups: 0,
        rootLevelVideos: 1,
        mixedFolderAmbiguity: 0,
        standardScore: 0,
        nonStandardScore: 1,
      },
    },
    candidates: [candidate],
    scrapedItems: [
      {
        recordId: record.id,
        providerPath: record.providerPath,
        matchStatus: 'matched',
        searchTitles: [input.title],
        matchedSearchTitle: input.title,
        mediaType: 'movie',
        categoryName: '未分类',
        metadata: {
          tmdbId: 1001,
          mediaType: 'movie',
          title: input.title,
          originalTitle: input.title,
          overview: 'Verify fixture.',
          releaseDate: '2026-07-08',
          releaseYear: 2026,
          rating: 8.1,
          genreIds: [],
          genres: [],
          originalLanguage: 'zh',
          originCountries: [],
          productionCountries: [],
          posterUrl: input.posterUrl,
          scrapedAt: '2026-07-08T00:00:00.000Z',
        },
        categoryAssignment: {
          categoryName: '未分类',
          source: 'kindFallback',
        },
      },
    ],
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
