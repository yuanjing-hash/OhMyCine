import type { RecognitionRemoteCandidate } from '../src/services/scraper/recognition.ts'
import type { RawFileRecord, RawMediaCandidate } from '../src/services/scraper/types.ts'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { argv } from 'node:process'
import { pathToFileURL } from 'node:url'
import { parseRawMediaCandidate } from '../src/services/scraper/parser.ts'
import {
  buildRecognitionSearchRequests,
  buildRecognitionTitleVariants,
  decideRecognitionCandidate,
  MAX_TMDB_RECOGNITION_DETAILS,
  MAX_TMDB_RECOGNITION_SEARCHES,
  PLAYER_RECOGNITION_ENGINE_VERSION,
  titleSimilarityScore,
} from '../src/services/scraper/recognition.ts'
import { TmdbScraper } from '../src/services/scraper/tmdb.ts'

interface SharedCorpus {
  readonly version: string
  readonly cases: SharedCorpusCase[]
}

interface SharedCorpusCase {
  readonly id: string
  readonly input: {
    readonly package_name: string
    readonly source_kind?: string
    readonly media_type_hint?: 'movie' | 'tv'
    readonly year_hint?: number
    readonly files?: Array<{ readonly relative_path: string, readonly size?: number }>
  }
  readonly candidates: Array<{
    readonly id: number
    readonly media_type: 'movie' | 'tv'
    readonly title: string
    readonly original_title?: string
    readonly original_language?: string
    readonly alternative_titles?: string[]
    readonly translations?: string[]
    readonly release_year?: number
    readonly season_count?: number
    readonly episode_count?: number
    readonly popularity?: number
    readonly vote_count?: number
    readonly has_poster?: boolean
  }>
  readonly expected: {
    readonly canonical_title: string
    readonly remote_id?: number
  }
  readonly policy: 'must_match' | 'must_reject'
}

export async function verifyNextgenRecognition(): Promise<Record<string, unknown>> {
  const corpusPath = new URL('./fixtures/media-recognition-corpus.v1.json', import.meta.url)
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
      originalLanguage: item.original_language,
      alternativeTitles: item.alternative_titles,
      translations: item.translations,
      releaseYear: item.release_year,
      seasonCount: item.season_count,
      episodeCount: item.episode_count,
      popularity: item.popularity,
      voteCount: item.vote_count,
      hasPoster: item.has_poster,
    }))
    const decision = decideRecognitionCandidate(candidate, variants, remote)
    const reversed = decideRecognitionCandidate(candidate, variants, [...remote].reverse())
    assert.equal(reversed.reason, decision.reason, `${fixture.id}: decision changed when provider order reversed`)
    assert.equal(reversed.match?.id, decision.match?.id, `${fixture.id}: winner changed when provider order reversed`)

    if (fixture.policy === 'must_match') {
      mustMatch += 1
      assert.equal(decision.reason, 'matched', `${fixture.id}: expected a match, got ${decision.reason}`)
      assert.equal(decision.match?.id, fixture.expected.remote_id, `${fixture.id}: unexpected identity`)
      assert.ok(hasCanonicalTitleCoverage(variants, fixture.expected.canonical_title), `${fixture.id}: canonical title missing`)
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

  const authorityCases = verifyAuthorityTieBreak()
  const requestBudget = await verifyBoundedTmdbRecall()
  const tmdbAuthorityMapping = await verifyTmdbAuthorityMapping()

  return {
    engineVersion: PLAYER_RECOGNITION_ENGINE_VERSION,
    sharedCorpus: corpus.version,
    mustMatch,
    mustReject,
    multilingualCases: multilingualCases.length,
    authorityCases,
    maxSearches: MAX_TMDB_RECOGNITION_SEARCHES,
    maxDetails: MAX_TMDB_RECOGNITION_DETAILS,
    requestBudget,
    tmdbAuthorityMapping,
  }
}

function verifyAuthorityTieBreak(): number {
  const aliases = Array.from({ length: 13 }, (_, index) => `柯南别名 ${index + 1}`)
  const translations = Array.from({ length: 19 }, (_, index) => `Conan translation ${index + 1}`)
  const correct: RecognitionRemoteCandidate = {
    id: 30983,
    mediaType: 'tv',
    title: '名侦探柯南',
    originalTitle: '名探偵コナン',
    originalLanguage: 'ja',
    alternativeTitles: aliases,
    translations,
    releaseYear: 1996,
    seasonCount: 1,
    episodeCount: 1212,
    popularity: 70.8752,
    voteCount: 781,
    hasPoster: true,
  }
  const emptyShell: RecognitionRemoteCandidate = {
    id: 318691,
    mediaType: 'tv',
    title: '名侦探柯南',
    originalTitle: '名侦探柯南',
    originalLanguage: 'zh',
    popularity: 0.741,
  }
  const releases = [
    '[银色子弹字幕组][名侦探柯南][第1200集 快递失窃频发中][WEBRIP][简日双语MP4][1080P]',
    '[银色子弹字幕组][名侦探柯南][第1201集 我就是犯人][WEBRIP][简日双语MP4][1080P]',
    '[银色子弹字幕组][名侦探柯南][第1204集 谁绑架了柯南和梓?][WEBRIP][简日双语MP4][1080P]',
    '[银色子弹字幕组][名侦探柯南][第1206集 摔落的男人][WEBRIP][简日双语MP4][1080P]',
  ]
  let cases = 0
  for (const release of releases) {
    const candidate = parseRawMediaCandidate(recordForPath(`/${release}.mkv`))
    assert.equal(candidate.kind, 'episode', `${release}: expected episode structure`)
    for (const remotes of [[correct, emptyShell], [emptyShell, correct]]) {
      const decision = decideRecognitionCandidate(candidate, buildRecognitionTitleVariants(candidate), remotes)
      assert.equal(decision.reason, 'matched', `${release}: real TMDB shape remained ambiguous`)
      assert.equal(decision.match?.id, correct.id, `${release}: empty shell won`)
      assert.ok((decision.ranked[0]?.authority ?? 0) > (decision.ranked[1]?.authority ?? 0), `${release}: authority evidence was lost`)
      cases += 1
    }
  }

  const sharedCandidate = parseRawMediaCandidate(recordForPath('/Shared Series S01E120.mkv'))
  const sharedVariants = buildRecognitionTitleVariants(sharedCandidate)
  const complete = (id: number): RecognitionRemoteCandidate => ({
    id,
    mediaType: 'tv',
    title: 'Shared Series',
    originalTitle: '共有シリーズ',
    originalLanguage: 'ja',
    alternativeTitles: Array.from({ length: 8 }, (_, index) => `Shared alias ${index + 1}`),
    translations: Array.from({ length: 8 }, (_, index) => `Shared translation ${index + 1}`),
    releaseYear: 1998,
    seasonCount: 1,
    episodeCount: 200,
    popularity: 25,
    voteCount: 500,
    hasPoster: true,
  })
  const sharedShell: RecognitionRemoteCandidate = { id: 22, mediaType: 'tv', title: 'Shared Series' }
  const unrelated = { ...complete(23), title: 'Unrelated Popular Series', originalTitle: 'Unrelated Popular Series', popularity: 1_000_000, voteCount: 1_000_000_000 }
  for (const candidates of [
    [complete(21), sharedShell, unrelated],
    [unrelated, sharedShell, complete(21)],
    [sharedShell, complete(21), unrelated],
  ]) {
    const decision = decideRecognitionCandidate(sharedCandidate, sharedVariants, candidates)
    assert.equal(decision.reason, 'matched', 'generic three-candidate authority tie-break failed')
    assert.equal(decision.match?.id, 21, 'candidate order changed the generic authority winner')
    cases += 1
  }

  const equalAuthority = decideRecognitionCandidate(sharedCandidate, sharedVariants, [complete(31), complete(32)])
  assert.equal(equalAuthority.reason, 'candidate_conflict', 'equally complete exact identities must remain ambiguous')
  cases += 1

  const emptyData = decideRecognitionCandidate(sharedCandidate, sharedVariants, [
    { id: 41, mediaType: 'tv', title: 'Shared Series' },
    { id: 42, mediaType: 'tv', title: 'Shared Series' },
  ])
  assert.equal(emptyData.reason, 'candidate_conflict', 'missing authority data must remain neutral')
  cases += 1

  const popularityOnly = decideRecognitionCandidate(sharedCandidate, sharedVariants, [
    { id: 51, mediaType: 'tv', title: 'Shared Series', popularity: 1_000_000, voteCount: 1_000_000_000 },
    { id: 52, mediaType: 'tv', title: 'Shared Series' },
  ])
  assert.equal(popularityOnly.reason, 'candidate_conflict', 'popularity and votes alone established identity')
  cases += 1

  const wrongTitle = decideRecognitionCandidate(sharedCandidate, sharedVariants, [
    { ...complete(61), title: 'Completely Different', originalTitle: 'Completely Different', popularity: 1_000_000, voteCount: 1_000_000_000 },
    { id: 62, mediaType: 'tv', title: 'Shared Series', episodeCount: 200 },
  ])
  assert.equal(wrongTitle.match?.id, 62, 'authority overrode title identity')
  cases += 1

  const wrongType = decideRecognitionCandidate(sharedCandidate, sharedVariants, [
    { ...complete(71), mediaType: 'movie' },
    { id: 72, mediaType: 'tv', title: 'Shared Series', episodeCount: 200 },
  ])
  assert.equal(wrongType.match?.id, 72, 'authority overrode a strong media type')
  cases += 1

  const yearCandidate = parseRawMediaCandidate(recordForPath('/The Office.2005.S01E01.mkv'))
  const yearVariants = buildRecognitionTitleVariants(yearCandidate)
  const wrongYear = decideRecognitionCandidate(yearCandidate, yearVariants, [
    { ...complete(81), title: 'The Office', originalTitle: 'The Office', releaseYear: 1995 },
    { id: 82, mediaType: 'tv', title: 'The Office', releaseYear: 2005, episodeCount: 20 },
  ])
  assert.equal(wrongYear.match?.id, 82, 'authority overrode a strong release year')
  cases += 1

  const wrongEpisode = decideRecognitionCandidate(sharedCandidate, sharedVariants, [
    { ...complete(91), episodeCount: 24 },
    { id: 92, mediaType: 'tv', title: 'Shared Series', episodeCount: 200 },
  ])
  assert.equal(wrongEpisode.match?.id, 92, 'authority overrode a known episode-range conflict')
  cases += 1

  const unknownEpisodeCount = decideRecognitionCandidate(sharedCandidate, sharedVariants, [
    { id: 101, mediaType: 'tv', title: 'Shared Series', episodeCount: 200 },
    { id: 102, mediaType: 'tv', title: 'Shared Series' },
  ])
  assert.equal(unknownEpisodeCount.reason, 'candidate_conflict', 'missing episode count was treated as a conflict')
  cases += 1

  const saturatedTitle = 'A Very Long Shared Series Identity With Saturated Evidence'
  const saturatedCandidate = parseRawMediaCandidate(recordForPath(`/${saturatedTitle}.2024.S01E120.mkv`))
  const saturatedDecision = decideRecognitionCandidate(saturatedCandidate, buildRecognitionTitleVariants(saturatedCandidate), [
    {
      id: 1,
      mediaType: 'tv',
      title: 'A Very Long Shared Series Identitx With Saturated Evidence',
      releaseYear: 2024,
      seasonCount: 1,
      episodeCount: 200,
      popularity: 1_000,
    },
    {
      id: 2,
      mediaType: 'tv',
      title: saturatedTitle,
      releaseYear: 2024,
      seasonCount: 1,
      episodeCount: 200,
      popularity: 1_000,
    },
  ])
  assert.equal(saturatedDecision.reason, 'matched', 'an approximate candidate lowered a unique exact title after score saturation')
  assert.equal(saturatedDecision.match?.id, 2, 'score saturation hid the exact title identity')
  cases += 1

  const overlongAlias = 'x'.repeat(400)
  const bounded = decideRecognitionCandidate(sharedCandidate, sharedVariants, [{
    id: 111,
    mediaType: 'tv',
    title: 'Shared Series',
    originalLanguage: 'JA'.repeat(20),
    alternativeTitles: Array.from({ length: 80 }, () => overlongAlias),
    translations: Array.from({ length: 80 }, () => overlongAlias),
    releaseYear: Number.NaN,
    seasonCount: -1,
    episodeCount: 1_000_001,
    popularity: Number.POSITIVE_INFINITY,
    voteCount: -1,
    hasPoster: false,
  }]).ranked[0]?.candidate
  assert.ok(bounded)
  assert.equal(bounded.originalLanguage?.length, 16)
  assert.equal(bounded.alternativeTitles?.length, 32)
  assert.equal(bounded.alternativeTitles?.[0]?.length, 256)
  assert.equal(bounded.translations?.length, 32)
  assert.equal(bounded.releaseYear, undefined)
  assert.equal(bounded.seasonCount, undefined)
  assert.equal(bounded.episodeCount, undefined)
  assert.equal(bounded.popularity, undefined)
  assert.equal(bounded.voteCount, undefined)
  cases += 1
  return cases
}

async function verifyTmdbAuthorityMapping(): Promise<{ searches: number, details: number }> {
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
  let failAuthoritativeDetail = false
  try {
    Object.defineProperty(globalThis, 'window', { value: globalThis, configurable: true })
    Object.defineProperty(globalThis, 'fetch', {
      configurable: true,
      value: async (input: RequestInfo | URL) => {
        const url = new URL(String(input))
        if (url.pathname.startsWith('/3/search/')) {
          searches += 1
          return new Response(JSON.stringify({
            results: url.pathname.endsWith('/tv')
              ? [
                  {
                    id: 30983,
                    name: '名侦探柯南',
                    original_name: '名探偵コナン',
                    original_language: 'ja',
                    first_air_date: '1996-01-08',
                    popularity: 70.8752,
                    vote_count: 781,
                    poster_path: '/conan.jpg',
                  },
                  {
                    id: 318691,
                    name: '名侦探柯南',
                    original_name: '名侦探柯南',
                    original_language: 'zh',
                    popularity: 0.741,
                  },
                ]
              : [],
          }), { status: 200 })
        }
        if (url.pathname === '/3/tv/30983') {
          details += 1
          if (failAuthoritativeDetail)
            return new Response(JSON.stringify({ status_message: 'temporary upstream failure' }), { status: 500 })
          return new Response(JSON.stringify({
            id: 30983,
            name: '名侦探柯南',
            original_name: '名探偵コナン',
            original_language: 'ja',
            first_air_date: '1996-01-08',
            number_of_seasons: 1,
            number_of_episodes: 1212,
            popularity: 70.8752,
            vote_count: 781,
            poster_path: '/conan.jpg',
            genres: [],
            origin_country: ['JP'],
            production_countries: [],
            alternative_titles: { results: Array.from({ length: 8 }, (_, index) => ({ title: `柯南别名 ${index + 1}` })) },
            translations: { translations: Array.from({ length: 8 }, (_, index) => ({ data: { name: `Conan translation ${index + 1}` } })) },
            images: { logos: [] },
            external_ids: {},
          }), { status: 200 })
        }
        if (url.pathname === '/3/tv/318691') {
          details += 1
          return new Response(JSON.stringify({
            id: 318691,
            name: '名侦探柯南',
            original_name: '名侦探柯南',
            original_language: 'zh',
            popularity: 0.741,
            genres: [],
            origin_country: [],
            production_countries: [],
            alternative_titles: { results: [] },
            translations: { translations: [] },
            images: { logos: [] },
            external_ids: {},
          }), { status: 200 })
        }
        return new Response('{}', { status: 404 })
      },
    })
    const release = '[银色子弹字幕组][名侦探柯南][第1206集 摔落的男人][WEBRIP][简日双语MP4][1080P]'
    const match = await scraper.searchCandidate(parseRawMediaCandidate(recordForPath(`/${release}.mkv`)))
    assert.equal(match?.metadata.tmdbId, 30983, 'TMDB search/detail authority fields did not reach the ranker')
    assert.equal(match?.metadata.originalLanguage, 'ja')
    assert.equal(match?.metadata.episodeCount, 1212)
    assert.equal(match?.metadata.voteCount, 781)
    assert.equal(match?.metadata.posterPath, '/conan.jpg')
    assert.ok(searches <= MAX_TMDB_RECOGNITION_SEARCHES)
    assert.ok(details <= MAX_TMDB_RECOGNITION_DETAILS)

    searches = 0
    details = 0
    failAuthoritativeDetail = true
    const degraded = await scraper.searchCandidate(parseRawMediaCandidate(recordForPath(`/${release}.mkv`)))
    assert.equal(degraded, null, 'a detail failure removed the authoritative summary and allowed the empty shell to win')
    assert.ok(searches <= MAX_TMDB_RECOGNITION_SEARCHES)
    assert.ok(details <= MAX_TMDB_RECOGNITION_DETAILS)
    return { searches, details }
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

function hasCanonicalTitleCoverage(
  variants: readonly { readonly title: string }[],
  canonicalTitle: string,
): boolean {
  if (variants.some(variant => titleSimilarityScore(variant.title, canonicalTitle) >= 0.9))
    return true

  // Provider-neutral package names can carry bilingual aliases separated by a
  // slash, while a real filesystem record cannot contain `/` in one path
  // component. Player therefore validates that every alias is represented by
  // a high-confidence search variant instead of requiring an impossible
  // filesystem title containing the separator.
  const aliases = canonicalTitle.split(/\s*[/／]\s*/u).filter(Boolean)
  return aliases.length > 1 && aliases.some(alias =>
    variants.some(variant => titleSimilarityScore(variant.title, alias) >= 0.9),
  )
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
    : /\bm\s*0*\d{1,3}\b/iu.test(fixture.input.package_name) && /\bmovie\b/iu.test(fixture.input.package_name)
      ? 'movie'
      : fixture.input.source_kind === 'download' && candidate.episodeNumber == null
        ? 'unresolved'
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

if (argv[1] && import.meta.url === pathToFileURL(argv[1]).href)
  console.log(JSON.stringify(await verifyNextgenRecognition(), null, 2))
