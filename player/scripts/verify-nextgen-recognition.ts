import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { parseRawMediaCandidate } from '../src/services/scraper/parser.ts'
import { TmdbScraper } from '../src/services/scraper/tmdb.ts'
import {
  buildRecognitionSearchRequests,
  buildRecognitionTitleVariants,
  decideRecognitionCandidate,
  MAX_TMDB_RECOGNITION_DETAILS,
  MAX_TMDB_RECOGNITION_SEARCHES,
  PLAYER_RECOGNITION_ENGINE_VERSION,
  titleSimilarityScore,
} from '../src/services/scraper/recognition.ts'
import type { RawFileRecord, RawMediaCandidate } from '../src/services/scraper/types.ts'

interface SharedCorpus {
  readonly version: string
  readonly cases: SharedCorpusCase[]
}

interface SharedCorpusCase {
  readonly id: string
  readonly input: {
    readonly package_name: string
    readonly media_type_hint?: 'movie' | 'tv'
    readonly year_hint?: number
    readonly files?: Array<{ readonly relative_path: string, readonly size?: number }>
  }
  readonly candidates: Array<{
    readonly id: number
    readonly media_type: 'movie' | 'tv'
    readonly title: string
    readonly original_title?: string
    readonly alternative_titles?: string[]
    readonly translations?: string[]
    readonly release_year?: number
    readonly season_count?: number
    readonly popularity?: number
  }>
  readonly expected: {
    readonly canonical_title: string
    readonly remote_id?: number
  }
  readonly policy: 'must_match' | 'must_reject'
}

export async function verifyNextgenRecognition(): Promise<Record<string, unknown>> {
  const corpusPath = new URL('../../server/internal/mediarecognition/testdata/corpus.v1.json', import.meta.url)
  const corpus = JSON.parse(await readFile(corpusPath, 'utf8')) as SharedCorpus
  assert.equal(corpus.version, 'provider-neutral-v1')

  let mustMatch = 0
  let mustReject = 0
  for (const fixture of corpus.cases) {
    const candidate = corpusCandidate(fixture)
    const variants = buildRecognitionTitleVariants(candidate)
    const remote = fixture.candidates.map(item => ({
      id: item.id,
      mediaType: item.media_type,
      title: item.title,
      originalTitle: item.original_title,
      alternativeTitles: item.alternative_titles,
      translations: item.translations,
      releaseYear: item.release_year,
      seasonCount: item.season_count,
      popularity: item.popularity,
    }))
    const decision = decideRecognitionCandidate(candidate, variants, remote)
    const reversed = decideRecognitionCandidate(candidate, variants, [...remote].reverse())
    assert.equal(reversed.reason, decision.reason, `${fixture.id}: decision changed when provider order reversed`)
    assert.equal(reversed.match?.id, decision.match?.id, `${fixture.id}: winner changed when provider order reversed`)

    if (fixture.policy === 'must_match') {
      mustMatch += 1
      assert.equal(decision.reason, 'matched', `${fixture.id}: expected a match, got ${decision.reason}`)
      assert.equal(decision.match?.id, fixture.expected.remote_id, `${fixture.id}: unexpected identity`)
      assert.ok(variants.some(variant => titleSimilarityScore(variant.title, fixture.expected.canonical_title) >= 0.9), `${fixture.id}: canonical title missing`)
    }
    else {
      mustReject += 1
      assert.notEqual(decision.reason, 'matched', `${fixture.id}: ambiguous/conflicting case must be rejected`)
    }
  }

  const multilingualCases: Array<{ path: string, title: string, season?: number, episode?: number }> = [
    { path: '/Films/Amélie.2001.mkv', title: 'Amélie' },
    { path: '/Фильмы/Брат.1997.mkv', title: 'Брат' },
    { path: '/أفلام/الفيل الأزرق.2014.mkv', title: 'الفيل الأزرق' },
    { path: '/ภาพยนตร์/ฉลาดเกมส์โกง.2017.mkv', title: 'ฉลาดเกมส์โกง' },
    { path: '/孤独のグルメ/第2期/孤独のグルメ 第3話.mkv', title: '孤独のグルメ', season: 2, episode: 3 },
    { path: '/기생수/2시즌/기생수 제4화.mkv', title: '기생수', season: 2, episode: 4 },
    { path: '/Lupin/Saison 02/Episode 03.mkv', title: 'Lupin', season: 2, episode: 3 },
    { path: '/Dark/Staffel 03/Folge 05.mkv', title: 'Dark', season: 3, episode: 5 },
  ]
  for (const fixture of multilingualCases) {
    const candidate = parseRawMediaCandidate(recordForPath(fixture.path))
    assert.equal(candidate.seriesTitle ?? candidate.title, fixture.title, fixture.path)
    assert.equal(candidate.seasonNumber, fixture.season, `${fixture.path}: season`)
    assert.equal(candidate.episodeNumber, fixture.episode, `${fixture.path}: episode`)
  }

  for (const title of ['第八集', '第2季', '第二十条', '[REC]', '(500) Days of Summer', 'Spider-Man', 'Tinker-Tailor-Soldier-Spy']) {
    const candidate = parseRawMediaCandidate(recordForPath(`/${title}.mkv`))
    assert.ok((candidate.seriesTitle ?? candidate.title).includes(title), `${title}: legal whole title was damaged`)
  }

  const douluo = parseRawMediaCandidate(recordForPath('/斗罗大陆/斗罗大陆 - - 第2集.mp4'))
  assert.equal(douluo.seriesTitle, '斗罗大陆')
  assert.equal(douluo.episodeNumber, 2)
  const numericTitle = parseRawMediaCandidate(recordForPath('/1917.2019.2160p.UHD.BluRay-GRP.mkv'))
  assert.equal(numericTitle.title, '1917')
  assert.equal(numericTitle.year, 2019)

  const sourceFairCandidate = parseRawMediaCandidate(recordForPath('/祖级 Grand/父级 Parent/文件 File.mkv'))
  const sourceFairVariants = buildRecognitionTitleVariants(sourceFairCandidate)
  const firstFallbackIndex = sourceFairVariants.findIndex(item => item.stage === 'fallback')
  assert.ok(firstFallbackIndex > 0)
  assert.ok(sourceFairVariants.findIndex(item => item.source === 'parent' && item.stage === 'canonical') < firstFallbackIndex)
  assert.ok(sourceFairVariants.findIndex(item => item.source === 'grandparent' && item.stage === 'canonical') < firstFallbackIndex)

  const bounded = parseRawMediaCandidate(recordForPath('/Shows/Example (2024)/Saison 01/Example Episode 02.mkv'))
  const requests = buildRecognitionSearchRequests(bounded)
  assert.ok(requests.length <= MAX_TMDB_RECOGNITION_SEARCHES)
  assert.ok(MAX_TMDB_RECOGNITION_DETAILS <= 3)
  assert.ok(requests.some(item => item.year == null), 'yearless fallback is missing')
  assert.ok(requests.some(item => item.year === 2023), 'year -1 fallback is missing')
  assert.ok(requests.some(item => item.year === 2025), 'year +1 fallback is missing')

  const requestBudget = await verifyBoundedTmdbRecall()

  return {
    engineVersion: PLAYER_RECOGNITION_ENGINE_VERSION,
    sharedCorpus: corpus.version,
    mustMatch,
    mustReject,
    multilingualCases: multilingualCases.length,
    maxSearches: MAX_TMDB_RECOGNITION_SEARCHES,
    maxDetails: MAX_TMDB_RECOGNITION_DETAILS,
    requestBudget,
  }
}

async function verifyBoundedTmdbRecall(): Promise<{
  searches: number
  details: number
  rateLimitStopsAt: number
  detailRateLimitStopsAt: number
}> {
  const fetchDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'fetch')
  const windowDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const scraper = new TmdbScraper({ authType: 'apiKey', value: 'fixture-key' }, {
    credentialRef: 'settings:tmdb-credential',
    authType: 'apiKey',
    language: 'zh-CN',
    region: 'CN',
    apiBaseUrl: 'https://api.tmdb.org/3',
    imageBaseUrl: 'https://image.tmdb.org/t/p',
  })
  let searches = 0
  let details = 0
  try {
    Object.defineProperty(globalThis, 'window', { value: globalThis, configurable: true })
    Object.defineProperty(globalThis, 'fetch', {
      configurable: true,
      value: async (input: RequestInfo | URL) => {
        const url = new URL(String(input))
        if (url.pathname.startsWith('/3/search/')) {
          searches += 1
          return new Response(JSON.stringify({
            results: url.pathname.endsWith('/movie')
              ? [{ id: 1995, title: '阿凡达', original_title: 'Avatar', release_date: '2009-12-18', popularity: 100 }]
              : [],
          }), { status: 200 })
        }
        if (url.pathname === '/3/movie/1995') {
          details += 1
          return new Response(JSON.stringify({
            id: 1995,
            title: '阿凡达',
            original_title: 'Avatar',
            release_date: '2009-12-18',
            genres: [],
            production_countries: [],
            alternative_titles: { titles: [{ title: 'Avatar' }] },
            translations: { translations: [] },
            images: { logos: [] },
            external_ids: {},
          }), { status: 200 })
        }
        return new Response('{}', { status: 404 })
      },
    })
    const match = await scraper.searchCandidate(parseRawMediaCandidate(recordForPath('/阿凡达.mkv')))
    assert.equal(match?.metadata.tmdbId, 1995)
    assert.ok(searches <= MAX_TMDB_RECOGNITION_SEARCHES)
    assert.ok(details <= MAX_TMDB_RECOGNITION_DETAILS)

    let rateLimitStopsAt = 0
    Object.defineProperty(globalThis, 'fetch', {
      configurable: true,
      value: async () => {
        rateLimitStopsAt += 1
        return new Response(JSON.stringify({ status_message: 'rate limited' }), { status: 429 })
      },
    })
    await assert.rejects(() => scraper.searchCandidate(parseRawMediaCandidate(recordForPath('/Rate Limit.mkv'))))
    assert.equal(rateLimitStopsAt, 1)

    let detailRateLimitStopsAt = 0
    Object.defineProperty(globalThis, 'fetch', {
      configurable: true,
      value: async (input: RequestInfo | URL) => {
        const url = new URL(String(input))
        if (url.pathname.startsWith('/3/search/')) {
          return new Response(JSON.stringify({
            results: url.pathname.endsWith('/movie')
              ? [
                  { id: 2001, title: 'Detail Rate Limit', release_date: '2024-01-01', popularity: 100 },
                  { id: 2002, title: 'Detail Rate Limit', release_date: '2024-01-01', popularity: 90 },
                ]
              : [],
          }), { status: 200 })
        }
        if (url.pathname.startsWith('/3/movie/')) {
          detailRateLimitStopsAt += 1
          return new Response(JSON.stringify({ status_message: 'rate limited' }), { status: 429 })
        }
        return new Response('{}', { status: 404 })
      },
    })
    await assert.rejects(() => scraper.searchCandidate(parseRawMediaCandidate(recordForPath('/Detail Rate Limit.2024.mkv'))))
    assert.equal(detailRateLimitStopsAt, 1)
    return { searches, details, rateLimitStopsAt, detailRateLimitStopsAt }
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

function corpusCandidate(fixture: SharedCorpusCase): RawMediaCandidate {
  const usableFile = fixture.input.files?.find((item) => {
    const stem = item.relative_path.split('/').at(-1)?.replace(/\.[^.]+$/, '') ?? ''
    return !/^\d+$/.test(stem) && !/^(?:BDMV|STREAM|VIDEO_TS)$/i.test(stem)
  })
  const candidate = parseRawMediaCandidate(usableFile
    ? recordForPath(`/${usableFile.relative_path.replace(/^\/+/, '')}`)
    : recordForPath(`/${fixture.input.package_name}.mkv`))
  const hintedKind = fixture.input.media_type_hint
    ? fixture.input.media_type_hint
    : candidate.kind
  return {
    ...candidate,
    kind: hintedKind === 'tv' && candidate.episodeNumber != null ? 'episode' : hintedKind,
    parseStatus: candidate.parseStatus === 'unresolved' ? 'partial' : candidate.parseStatus,
    year: fixture.input.year_hint ?? candidate.year,
  }
}

function recordForPath(providerPath: string): RawFileRecord {
  const fileName = providerPath.split('/').at(-1) ?? 'video.mkv'
  return {
    id: `fixture:${providerPath}`,
    sourceId: 'fixture',
    sourceType: 'local',
    rootPath: '/',
    providerPath,
    relativePath: providerPath.replace(/^\/+/, ''),
    parentPath: providerPath.slice(0, providerPath.lastIndexOf('/')) || '/',
    fileName,
    extension: fileName.split('.').at(-1) ?? '',
  }
}
