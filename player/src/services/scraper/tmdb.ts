import type { ScrapeMediaType } from './classificationRules'
import type { RawMediaCandidate } from './types'
import type { TmdbCredentialValue } from '@/services/datasource/credentialStore'
import { invoke } from '@tauri-apps/api/core'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { readTmdbCredential, removeCredential, saveTmdbCredential } from '@/services/datasource/credentialStore'
import { extractMediaSearchTitles, normalizeTitleKey } from './parser'
import { stripFileExtension } from './pathUtils'
import { buildTmdbRequestDescriptor, resolveEffectiveTmdbCredential, tmdbHttpFailureMessage } from './tmdbAuth'

export type TmdbAuthType = TmdbCredentialValue['authType']

export interface TmdbLocalSettings {
  readonly credentialRef: string
  readonly authType: TmdbAuthType
  readonly language: string
  readonly region: string
  readonly apiBaseUrl: string
  readonly imageBaseUrl: string
}

export interface TmdbMetadata {
  readonly tmdbId: number
  readonly mediaType: ScrapeMediaType
  readonly title: string
  readonly originalTitle?: string
  readonly imdbId?: string
  readonly tvdbId?: number
  readonly overview?: string
  readonly releaseDate?: string
  readonly releaseYear?: number
  readonly rating?: number
  readonly genreIds: number[]
  readonly genres: string[]
  readonly originalLanguage?: string
  readonly originCountries: string[]
  readonly productionCountries: string[]
  readonly posterPath?: string
  readonly backdropPath?: string
  readonly titleLogoPath?: string
  readonly posterUrl?: string
  readonly backdropUrl?: string
  readonly titleLogoUrl?: string
  readonly scrapedAt: string
}

export interface TmdbEpisodeMetadata {
  readonly tmdbEpisodeId: number
  readonly tvTmdbId: number
  readonly seasonNumber: number
  readonly episodeNumber: number
  readonly name?: string
  readonly overview?: string
  readonly airDate?: string
  readonly runtime?: number
  readonly rating?: number
  readonly stillPath?: string
  readonly stillUrl?: string
  readonly scrapedAt: string
}

export function isMatchingTmdbEpisodeMetadata(
  metadata: TmdbEpisodeMetadata | undefined,
  tvTmdbId: number | undefined,
  seasonNumber: number | undefined,
  episodeNumber: number | undefined,
): metadata is TmdbEpisodeMetadata {
  return metadata != null
    && metadata.tvTmdbId === tvTmdbId
    && metadata.seasonNumber === seasonNumber
    && metadata.episodeNumber === episodeNumber
}

export interface TmdbCandidateMatch {
  readonly metadata: TmdbMetadata
  readonly searchTitle: string
}

export type TmdbImageKind = 'poster' | 'logo' | 'backdrop'

export interface TmdbImageCandidate {
  readonly kind: TmdbImageKind
  readonly filePath: string
  readonly imageUrl: string
  readonly language?: string
  readonly width?: number
  readonly height?: number
  readonly aspectRatio?: number
  readonly voteAverage?: number
  readonly voteCount?: number
}

interface TmdbSearchResult {
  readonly id: number
  readonly mediaType: ScrapeMediaType
  readonly title: string
  readonly originalTitle?: string
  readonly date?: string
  readonly year?: number
  readonly popularity?: number
}

const TMDB_SETTINGS_STORAGE_KEY = 'ohmycine-tmdb-settings-v1'
export const TMDB_CREDENTIAL_REF = 'settings:tmdb-credential'

const DEFAULT_TMDB_SETTINGS: TmdbLocalSettings = {
  credentialRef: TMDB_CREDENTIAL_REF,
  authType: 'readAccessToken',
  language: 'zh-CN',
  region: 'CN',
  apiBaseUrl: 'https://api.tmdb.org/3',
  imageBaseUrl: 'https://image.tmdb.org/t/p',
}

export const TMDB_API_BASE_URLS = [
  'https://api.tmdb.org/3',
  'https://api.themoviedb.org/3',
] as const
export const DEFAULT_TMDB_API_BASE_URL = DEFAULT_TMDB_SETTINGS.apiBaseUrl
export const DEFAULT_TMDB_IMAGE_BASE_URL = DEFAULT_TMDB_SETTINGS.imageBaseUrl
const DEFAULT_TMDB_TIMEOUT_MS = 10_000
const BUILT_IN_TMDB_READ_ACCESS_TOKEN = typeof __OHMYCINE_BUILTIN_TMDB_READ_ACCESS_TOKEN__ === 'string'
  ? __OHMYCINE_BUILTIN_TMDB_READ_ACCESS_TOKEN__
  : ''

export function loadTmdbLocalSettings(): TmdbLocalSettings {
  try {
    const raw = getAppSetting(TMDB_SETTINGS_STORAGE_KEY)
    if (!raw)
      return { ...DEFAULT_TMDB_SETTINGS }
    return sanitizeTmdbLocalSettings(JSON.parse(raw) as unknown)
  }
  catch {
    return { ...DEFAULT_TMDB_SETTINGS }
  }
}

export function saveTmdbLocalSettings(settings: Partial<TmdbLocalSettings>): TmdbLocalSettings {
  const sanitized = sanitizeTmdbLocalSettings({
    ...loadTmdbLocalSettings(),
    ...settings,
    credentialRef: TMDB_CREDENTIAL_REF,
  })
  void setAppSetting(TMDB_SETTINGS_STORAGE_KEY, JSON.stringify(sanitized))
  return sanitized
}

export async function saveConfiguredTmdbCredential(
  authType: TmdbAuthType,
  value: string,
): Promise<TmdbLocalSettings> {
  const settings = saveTmdbLocalSettings({ authType })
  await saveTmdbCredential(settings.credentialRef, { authType, value })
  return settings
}

export async function readConfiguredTmdbCredential(): Promise<TmdbCredentialValue | null> {
  const settings = loadTmdbLocalSettings()
  return readEffectiveTmdbCredentialFor(settings.authType)
}

export async function readEffectiveTmdbCredentialFor(authType: TmdbAuthType): Promise<TmdbCredentialValue | null> {
  const credential = await readTmdbCredential(TMDB_CREDENTIAL_REF)
  const matchingUserCredential = credential?.authType === authType ? credential : null
  return resolveEffectiveTmdbCredential(matchingUserCredential, BUILT_IN_TMDB_READ_ACCESS_TOKEN)
}

export function hasBuiltInTmdbCredential(): boolean {
  return resolveEffectiveTmdbCredential(null, BUILT_IN_TMDB_READ_ACCESS_TOKEN) != null
}

export async function readStoredTmdbCredential(): Promise<TmdbCredentialValue | null> {
  return readTmdbCredential(TMDB_CREDENTIAL_REF)
}

export async function hasConfiguredTmdbCredential(): Promise<boolean> {
  return (await readConfiguredTmdbCredential()) != null
}

export async function clearConfiguredTmdbCredential(): Promise<void> {
  await removeCredential(loadTmdbLocalSettings().credentialRef)
}

export function extractCandidateTmdbSearchTitles(candidate: RawMediaCandidate): string[] {
  const values = [
    candidate.seriesTitle,
    candidate.title,
    stripFileExtension(candidate.record.fileName),
  ].filter((value): value is string => Boolean(value?.trim()))

  const titles: string[] = []
  for (const value of values) {
    for (const title of extractMediaSearchTitles(value)) {
      const normalized = normalizeTitleKey(title)
      if (!normalized || titles.some(existing => normalizeTitleKey(existing) === normalized))
        continue
      titles.push(title)
    }
  }

  return titles
}

export class TmdbScraper {
  private readonly credential: TmdbCredentialValue
  private readonly settings: TmdbLocalSettings
  private readonly timeoutMs: number

  constructor(credential: TmdbCredentialValue, settings: TmdbLocalSettings = loadTmdbLocalSettings(), timeoutMs = DEFAULT_TMDB_TIMEOUT_MS) {
    this.credential = credential
    this.settings = settings
    this.timeoutMs = timeoutMs
  }

  async searchCandidate(candidate: RawMediaCandidate): Promise<TmdbCandidateMatch | null> {
    const searchTitles = extractCandidateTmdbSearchTitles(candidate)
    if (searchTitles.length === 0)
      return null

    for (const mediaType of preferredMediaTypes(candidate)) {
      for (const searchTitle of searchTitles) {
        const metadata = await this.search(mediaType, searchTitle, candidate.year)
        if (metadata)
          return { metadata, searchTitle }
      }
    }

    return null
  }

  async search(mediaType: ScrapeMediaType, title: string, year?: number): Promise<TmdbMetadata | null> {
    const searchResults = await this.searchResults(mediaType, title, year)
    const best = selectBestSearchResult(searchResults, title, year)
    if (!best)
      return null
    return this.getDetail(best.mediaType, best.id)
  }

  async searchChoices(mediaType: ScrapeMediaType, title: string, year?: number, limit = 8): Promise<TmdbMetadata[]> {
    const searchResults = await this.searchResults(mediaType, title, year)
    const ranked = rankSearchResults(searchResults, title, year).slice(0, Math.max(1, limit))
    const settled = await Promise.allSettled(ranked.map(result => this.getDetail(result.mediaType, result.id)))
    return settled
      .filter((result): result is PromiseFulfilledResult<TmdbMetadata> => result.status === 'fulfilled')
      .map(result => result.value)
  }

  async getDetail(mediaType: ScrapeMediaType, tmdbId: number): Promise<TmdbMetadata> {
    const data = await this.requestJson(`/${mediaType}/${tmdbId}`, {
      language: this.settings.language,
      append_to_response: 'external_ids,images',
      include_image_language: preferredImageLanguageParam(this.settings.language),
    })
    return mapTmdbDetail(data, mediaType, this.settings.language, this.settings.imageBaseUrl)
  }

  async getImageCandidates(mediaType: ScrapeMediaType, tmdbId: number, kind: TmdbImageKind): Promise<TmdbImageCandidate[]> {
    const data = await this.requestJson(`/${mediaType}/${tmdbId}/images`, {
      include_image_language: preferredImageLanguageParam(this.settings.language),
    })
    return mapTmdbImageCandidates(data, kind, this.settings.language, this.settings.imageBaseUrl)
  }

  async getEpisodeDetail(tvTmdbId: number, seasonNumber: number, episodeNumber: number): Promise<TmdbEpisodeMetadata> {
    if (!isPositiveInteger(tvTmdbId) || !isPositiveInteger(seasonNumber) || !isPositiveInteger(episodeNumber))
      throw new Error('TMDB episode request is invalid.')

    const data = await this.requestJson(`/tv/${tvTmdbId}/season/${seasonNumber}/episode/${episodeNumber}`, {
      language: this.settings.language,
    })
    return mapTmdbEpisodeDetail(data, tvTmdbId, seasonNumber, episodeNumber, this.settings.imageBaseUrl)
  }

  private async searchResults(mediaType: ScrapeMediaType, title: string, year?: number): Promise<TmdbSearchResult[]> {
    const params: Record<string, string> = {
      query: title,
      language: this.settings.language,
      include_adult: 'false',
    }
    if (this.settings.region)
      params.region = this.settings.region
    if (year)
      params[mediaType === 'movie' ? 'year' : 'first_air_date_year'] = String(year)

    const data = await this.requestJson(`/search/${mediaType}`, params)
    if (!isRecord(data) || !Array.isArray(data.results))
      return []

    return data.results
      .map(item => mapTmdbSearchResult(item, mediaType))
      .filter((item): item is TmdbSearchResult => item != null)
  }

  private async requestJson(path: string, params: Record<string, string>): Promise<unknown> {
    return requestTmdbJsonWithFallback({
      path,
      params,
      credential: this.credential,
      timeoutMs: this.timeoutMs,
      baseUrls: tmdbApiBaseUrlsForSettings(this.settings),
    })
  }
}

class TmdbHttpError extends Error {}

interface TmdbJsonRequestInput {
  readonly path: string
  readonly params: Record<string, string>
  readonly credential: TmdbCredentialValue
  readonly timeoutMs: number
  readonly baseUrls?: readonly string[]
  readonly fetcher?: typeof fetch
}

interface TmdbNativeRequestResult {
  readonly ok: boolean
  readonly networkError: boolean
  readonly status?: number
  readonly data?: unknown
  readonly responseText?: string
}

export interface TmdbApiRouteTestInput {
  readonly apiBaseUrl: string
  readonly credential: TmdbCredentialValue
  readonly timeoutMs?: number
}

export interface TmdbImageRouteTestInput {
  readonly imageBaseUrl: string
  readonly timeoutMs?: number
}

export interface TmdbApiRouteTestResult {
  readonly apiBaseUrl: string
}

export interface TmdbImageRouteTestResult {
  readonly imageBaseUrl: string
}

export async function requestTmdbJsonWithFallback(input: TmdbJsonRequestInput): Promise<unknown> {
  const baseUrls = input.baseUrls?.length ? input.baseUrls : TMDB_API_BASE_URLS
  if (!input.fetcher && isTauriRuntime())
    return requestTmdbJsonWithNativeFallback(input, baseUrls)

  const fetcher = input.fetcher ?? fetch
  let lastNetworkError: Error | null = null

  for (const baseUrl of baseUrls) {
    const request = buildTmdbRequestDescriptor({
      baseUrl,
      path: input.path,
      params: input.params,
      credential: input.credential,
    })
    const controller = new AbortController()
    const timeout = globalThis.setTimeout(() => controller.abort(), input.timeoutMs)
    try {
      const response = await fetcher(request.url, { headers: request.headers, signal: controller.signal })
      if (!response.ok)
        throw new TmdbHttpError(tmdbHttpFailureMessage(input.credential.authType, response.status, await safeReadResponseText(response)))
      return response.json()
    }
    catch (error) {
      if (error instanceof TmdbHttpError)
        throw error
      lastNetworkError = isAbortError(error)
        ? new Error('TMDB 请求超时。')
        : new Error('TMDB 请求失败。')
    }
    finally {
      globalThis.clearTimeout(timeout)
    }
  }

  throw lastNetworkError ?? new Error('TMDB 请求失败。')
}

export async function testTmdbApiRoute(input: TmdbApiRouteTestInput): Promise<TmdbApiRouteTestResult> {
  const apiBaseUrl = normalizeTmdbBaseUrl(input.apiBaseUrl, 'api')
  const timeoutMs = input.timeoutMs ?? DEFAULT_TMDB_TIMEOUT_MS
  const detail = await requestTmdbJsonWithFallback({
    path: '/movie/550',
    params: { language: 'zh-CN' },
    credential: input.credential,
    timeoutMs,
    baseUrls: [apiBaseUrl],
  })
  if (!isRecord(detail) || numberValue(detail.id) !== 550)
    throw new Error('TMDB API 地址返回的数据不符合预期。')
  return { apiBaseUrl }
}

export async function testTmdbImageRoute(input: TmdbImageRouteTestInput): Promise<TmdbImageRouteTestResult> {
  const imageBaseUrl = normalizeTmdbBaseUrl(input.imageBaseUrl, 'image')
  const timeoutMs = input.timeoutMs ?? DEFAULT_TMDB_TIMEOUT_MS
  const imageUrl = tmdbArtworkUrl('/pB8BM7pdSp6B6Ih7QZ4DrQ3PmJK.jpg', 'w92', imageBaseUrl)
  await testTmdbImageUrl(imageUrl, timeoutMs)
  return { imageBaseUrl }
}

export function normalizeTmdbBaseUrl(value: string, kind: 'api' | 'image'): string {
  const label = kind === 'api' ? 'TMDB API 地址' : 'TMDB 图片地址'
  let url: URL
  try {
    url = new URL(value.trim())
  }
  catch {
    throw new Error(`${label}无效。`)
  }
  if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.search || url.hash)
    throw new Error(`${label}必须是 HTTPS 地址，且不能包含账号、密码、查询参数或片段。`)

  const normalizedPath = url.pathname.replace(/\/+$/, '')
  url.pathname = normalizedPath || '/'
  return url.toString().replace(/\/$/, '')
}

function tmdbApiBaseUrlsForSettings(settings: TmdbLocalSettings): readonly string[] {
  return settings.apiBaseUrl === DEFAULT_TMDB_API_BASE_URL
    ? TMDB_API_BASE_URLS
    : [settings.apiBaseUrl]
}

async function requestTmdbJsonWithNativeFallback(
  input: TmdbJsonRequestInput,
  baseUrls: readonly string[],
): Promise<unknown> {
  let lastNetworkError: Error | null = null
  for (const baseUrl of baseUrls) {
    const result = await invoke<TmdbNativeRequestResult>('tmdb_request_json', {
      request: {
        baseUrl,
        path: input.path,
        params: Object.entries(input.params).map(([key, value]) => ({ key, value })),
        authType: input.credential.authType,
        credential: input.credential.value,
        timeoutMs: input.timeoutMs,
      },
    })
    if (result.ok)
      return result.data
    if (!result.networkError) {
      throw new TmdbHttpError(tmdbHttpFailureMessage(
        input.credential.authType,
        result.status ?? 0,
        result.responseText ?? '',
      ))
    }
    lastNetworkError = new Error('TMDB 请求失败。')
  }
  throw lastNetworkError ?? new Error('TMDB 请求失败。')
}

async function testTmdbImageUrl(url: string, timeoutMs: number): Promise<void> {
  if (isTauriRuntime()) {
    await invoke('tmdb_test_image', { request: { url, timeoutMs } })
    return
  }

  const controller = new AbortController()
  const timeout = globalThis.setTimeout(() => controller.abort(), timeoutMs)
  try {
    const response = await fetch(url, { headers: { Accept: 'image/avif,image/webp,image/png,image/jpeg' }, signal: controller.signal })
    if (!response.ok)
      throw new Error(`TMDB 图片地址测试失败（HTTP ${response.status}）。`)
    if (!response.headers.get('content-type')?.toLocaleLowerCase().startsWith('image/'))
      throw new Error('TMDB 图片地址返回的不是图片。')
  }
  catch (error) {
    if (error instanceof Error && error.message.startsWith('TMDB 图片'))
      throw error
    throw new Error(isAbortError(error) ? 'TMDB 图片地址测试超时。' : 'TMDB 图片地址连接失败。')
  }
  finally {
    globalThis.clearTimeout(timeout)
  }
}

function isTauriRuntime(): boolean {
  const root = globalThis as typeof globalThis & { __TAURI_INTERNALS__?: unknown }
  return root.__TAURI_INTERNALS__ != null
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

async function safeReadResponseText(response: Response): Promise<string> {
  try {
    return (await response.text()).slice(0, 500)
  }
  catch {
    return ''
  }
}

function preferredMediaTypes(candidate: RawMediaCandidate): ScrapeMediaType[] {
  if (candidate.kind === 'episode' || candidate.kind === 'tv')
    return ['tv', 'movie']
  if (candidate.kind === 'movie')
    return ['movie', 'tv']
  return ['movie', 'tv']
}

function selectBestSearchResult(
  results: readonly TmdbSearchResult[],
  query: string,
  year?: number,
): TmdbSearchResult | null {
  const acceptable = results.filter(result => isAcceptableTitleMatch(query, result.title, result.originalTitle))
  if (acceptable.length === 0)
    return null

  return rankSearchResults(acceptable, query, year)[0] ?? null
}

function rankSearchResults(
  results: readonly TmdbSearchResult[],
  query: string,
  year?: number,
): TmdbSearchResult[] {
  return [...results].sort((left, right) =>
    searchResultScore(right, query, year) - searchResultScore(left, query, year)
    || (right.popularity ?? 0) - (left.popularity ?? 0)
    || left.title.localeCompare(right.title, 'zh-Hans-CN'))
}

function searchResultScore(result: TmdbSearchResult, query: string, year?: number): number {
  const titleMatch = titleMatchQuality(query, result.title, result.originalTitle)
  const exactYear = year != null && result.year === year ? 1_000_000 : 0
  const nearbyYear = year != null && result.year != null && Math.abs(result.year - year) <= 1 ? 500_000 : 0
  return exactYear + nearbyYear + titleMatch * 1_000 + (result.popularity ?? 0)
}

function isAcceptableTitleMatch(query: string, title: string, originalTitle?: string): boolean {
  return titleMatchQuality(query, title, originalTitle) > 0
}

function titleMatchQuality(query: string, title: string, originalTitle?: string): number {
  const queryKey = compactTitleKey(query)
  const queryNormalized = normalizeTitleKey(query)
  const titleValues = [title, originalTitle].filter((value): value is string => Boolean(value?.trim()))
  if (!queryKey || titleValues.length === 0)
    return 0

  let bestQuality = 0
  for (const titleValue of titleValues) {
    const titleKey = compactTitleKey(titleValue)
    if (!titleKey)
      continue

    if (normalizeTitleKey(titleValue) === queryNormalized) {
      bestQuality = Math.max(bestQuality, 1000)
      continue
    }
    if (titleKey === queryKey) {
      bestQuality = Math.max(bestQuality, 950)
      continue
    }
    if (titleKey.startsWith(queryKey) || queryKey.startsWith(titleKey)) {
      bestQuality = Math.max(bestQuality, 650)
      continue
    }
    if (titleKey.includes(queryKey) || queryKey.includes(titleKey))
      bestQuality = Math.max(bestQuality, 550)
  }

  const queryTokens = tokenizeTitle(query)
  if (queryTokens.length === 0)
    return bestQuality

  for (const titleValue of titleValues) {
    const titleTokens = tokenizeTitle(titleValue)
    if (titleTokens.length === 0)
      continue
    const overlap = queryTokens.filter(token => titleTokens.includes(token)).length
    const overlapRatio = overlap / Math.max(queryTokens.length, titleTokens.length)
    if (overlapRatio >= 0.55)
      bestQuality = Math.max(bestQuality, 400 + Math.round(overlapRatio * 100))
  }

  return bestQuality
}

function compactTitleKey(value: string | undefined): string {
  if (!value)
    return ''
  return normalizeTitleKey(value).replace(/[^a-z0-9\u4E00-\u9FFF]+/g, '')
}

function tokenizeTitle(value: string): string[] {
  return normalizeTitleKey(value)
    .split(/[^a-z0-9\u4E00-\u9FFF]+/g)
    .map(token => token.trim())
    .filter(token => token.length >= 2)
}

function mapTmdbSearchResult(value: unknown, mediaType: ScrapeMediaType): TmdbSearchResult | null {
  if (!isRecord(value))
    return null
  const id = numberValue(value.id)
  if (id == null)
    return null

  const title = stringValue(mediaType === 'movie' ? value.title : value.name)
  if (!title)
    return null

  const date = stringValue(mediaType === 'movie' ? value.release_date : value.first_air_date)
  return {
    id,
    mediaType,
    title,
    originalTitle: stringValue(mediaType === 'movie' ? value.original_title : value.original_name),
    date,
    year: yearFromDate(date),
    popularity: numberValue(value.popularity),
  }
}

function mapTmdbDetail(value: unknown, mediaType: ScrapeMediaType, language: string, imageBaseUrl: string): TmdbMetadata {
  if (!isRecord(value))
    throw new Error('TMDB detail response is invalid.')

  const tmdbId = numberValue(value.id)
  const title = stringValue(mediaType === 'movie' ? value.title : value.name)
  if (tmdbId == null || !title)
    throw new Error('TMDB detail response is incomplete.')

  const releaseDate = stringValue(mediaType === 'movie' ? value.release_date : value.first_air_date)
  const posterPath = stringValue(value.poster_path)
  const backdropPath = stringValue(value.backdrop_path)
  const titleLogoPath = selectPreferredLogoPath(value.images, language)
  const externalIds = isRecord(value.external_ids) ? value.external_ids : undefined

  return {
    tmdbId,
    mediaType,
    title,
    originalTitle: stringValue(mediaType === 'movie' ? value.original_title : value.original_name),
    imdbId: stringValue(externalIds?.imdb_id),
    tvdbId: numberValue(externalIds?.tvdb_id),
    overview: stringValue(value.overview),
    releaseDate,
    releaseYear: yearFromDate(releaseDate),
    rating: numberValue(value.vote_average),
    genreIds: genreIdsFromDetail(value.genres),
    genres: genreNamesFromDetail(value.genres),
    originalLanguage: stringValue(value.original_language),
    originCountries: stringArray(value.origin_country),
    productionCountries: productionCountryCodes(value.production_countries),
    posterPath,
    backdropPath,
    titleLogoPath,
    posterUrl: posterPath ? tmdbArtworkUrl(posterPath, 'w500', imageBaseUrl) : undefined,
    backdropUrl: backdropPath ? tmdbArtworkUrl(backdropPath, 'w1280', imageBaseUrl) : undefined,
    titleLogoUrl: titleLogoPath ? tmdbArtworkUrl(titleLogoPath, 'w500', imageBaseUrl) : undefined,
    scrapedAt: new Date().toISOString(),
  }
}

function mapTmdbEpisodeDetail(
  value: unknown,
  tvTmdbId: number,
  requestedSeasonNumber: number,
  requestedEpisodeNumber: number,
  imageBaseUrl: string,
): TmdbEpisodeMetadata {
  if (!isRecord(value))
    throw new Error('TMDB episode response is invalid.')

  const tmdbEpisodeId = numberValue(value.id)
  if (tmdbEpisodeId == null)
    throw new Error('TMDB episode response is incomplete.')

  const stillPath = stringValue(value.still_path)
  return {
    tmdbEpisodeId,
    tvTmdbId,
    seasonNumber: positiveIntegerValue(value.season_number) ?? requestedSeasonNumber,
    episodeNumber: positiveIntegerValue(value.episode_number) ?? requestedEpisodeNumber,
    name: stringValue(value.name),
    overview: stringValue(value.overview),
    airDate: stringValue(value.air_date),
    runtime: nonNegativeNumberValue(value.runtime),
    rating: numberValue(value.vote_average),
    stillPath,
    stillUrl: stillPath ? tmdbArtworkUrl(stillPath, 'w780', imageBaseUrl) : undefined,
    scrapedAt: new Date().toISOString(),
  }
}

function selectPreferredLogoPath(images: unknown, language: string): string | undefined {
  if (!isRecord(images))
    return undefined

  const logos = imageRecords(images.logos)
  return rankImageRecords(logos, preferredLogoLanguages(language))[0]?.filePath
}

function mapTmdbImageCandidates(value: unknown, kind: TmdbImageKind, language: string, imageBaseUrl: string): TmdbImageCandidate[] {
  if (!isRecord(value))
    return []

  const records = imageRecords(kind === 'poster' ? value.posters : kind === 'logo' ? value.logos : value.backdrops)
  const languagePriority = kind === 'logo' ? preferredLogoLanguages(language) : preferredImageLanguages(language)
  return rankImageRecords(records, languagePriority)
    .slice(0, 24)
    .map(record => ({
      kind,
      filePath: record.filePath,
      imageUrl: tmdbArtworkUrl(record.filePath, kind === 'backdrop' ? 'w780' : 'w500', imageBaseUrl),
      language: record.language,
      width: record.width,
      height: record.height,
      aspectRatio: record.aspectRatio,
      voteAverage: record.voteAverage,
      voteCount: record.voteCount,
    }))
}

interface TmdbImageRecord {
  readonly filePath: string
  readonly language?: string
  readonly width?: number
  readonly height?: number
  readonly aspectRatio?: number
  readonly voteAverage?: number
  readonly voteCount?: number
}

function imageRecords(value: unknown): TmdbImageRecord[] {
  if (!Array.isArray(value))
    return []

  return value
    .map((item): TmdbImageRecord | null => {
      if (!isRecord(item))
        return null
      const filePath = stringValue(item.file_path)
      if (!filePath)
        return null
      return {
        filePath,
        language: normalizeImageLanguage(item.iso_639_1),
        width: numberValue(item.width),
        height: numberValue(item.height),
        aspectRatio: numberValue(item.aspect_ratio),
        voteAverage: numberValue(item.vote_average),
        voteCount: numberValue(item.vote_count),
      }
    })
    .filter((item): item is TmdbImageRecord => item != null)
}

function rankImageRecords(records: readonly TmdbImageRecord[], languagePriority: readonly (string | undefined)[]): TmdbImageRecord[] {
  return [...records].sort((left, right) =>
    imageLanguageScore(right.language, languagePriority) - imageLanguageScore(left.language, languagePriority)
    || (right.voteAverage ?? 0) - (left.voteAverage ?? 0)
    || (right.voteCount ?? 0) - (left.voteCount ?? 0)
    || (right.width ?? 0) - (left.width ?? 0))
}

function imageLanguageScore(language: string | undefined, priority: readonly (string | undefined)[]): number {
  const index = priority.findIndex(item => item === language)
  return index >= 0 ? priority.length - index : 0
}

function preferredLogoLanguages(language: string): Array<string | undefined> {
  return uniqueLanguages([primaryLanguage(language), 'zh', 'en', undefined])
}

function preferredImageLanguages(language: string): Array<string | undefined> {
  return uniqueLanguages([primaryLanguage(language), 'zh', 'en', undefined])
}

function preferredImageLanguageParam(language: string): string {
  return preferredImageLanguages(language).map(value => value ?? 'null').join(',')
}

function primaryLanguage(language: string): string | undefined {
  const primary = language.split('-')[0]?.trim().toLocaleLowerCase()
  return primary || undefined
}

function uniqueLanguages(values: readonly (string | undefined)[]): Array<string | undefined> {
  const result: Array<string | undefined> = []
  for (const value of values) {
    if (result.includes(value))
      continue
    result.push(value)
  }
  return result
}

function normalizeImageLanguage(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim().toLocaleLowerCase() : undefined
}

export function tmdbArtworkUrl(
  path: string,
  size: 'w92' | 'w500' | 'w780' | 'w1280',
  imageBaseUrl = loadTmdbLocalSettings().imageBaseUrl,
): string {
  return `${imageBaseUrl}/${size}${path.startsWith('/') ? path : `/${path}`}`
}

function genreIdsFromDetail(value: unknown): number[] {
  if (!Array.isArray(value))
    return []
  return value
    .map(item => isRecord(item) ? numberValue(item.id) : undefined)
    .filter((item): item is number => item != null)
}

function genreNamesFromDetail(value: unknown): string[] {
  if (!Array.isArray(value))
    return []
  return value
    .map(item => isRecord(item) ? stringValue(item.name) : undefined)
    .filter((item): item is string => item != null)
}

function productionCountryCodes(value: unknown): string[] {
  if (!Array.isArray(value))
    return []
  return value
    .map(item => isRecord(item) ? stringValue(item.iso_3166_1) : undefined)
    .filter((item): item is string => item != null)
}

function stringArray(value: unknown): string[] {
  if (!Array.isArray(value))
    return []
  return value.filter((item): item is string => typeof item === 'string' && item.trim().length > 0)
}

function yearFromDate(value: string | undefined): number | undefined {
  const year = value ? Number(value.slice(0, 4)) : undefined
  return Number.isInteger(year) ? year : undefined
}

function sanitizeTmdbLocalSettings(value: unknown): TmdbLocalSettings {
  if (!isRecord(value))
    return { ...DEFAULT_TMDB_SETTINGS }

  return {
    credentialRef: TMDB_CREDENTIAL_REF,
    authType: value.authType === 'apiKey' ? 'apiKey' : 'readAccessToken',
    language: sanitizeLocale(stringValue(value.language), DEFAULT_TMDB_SETTINGS.language),
    region: sanitizeRegion(stringValue(value.region), DEFAULT_TMDB_SETTINGS.region),
    apiBaseUrl: sanitizeStoredTmdbBaseUrl(stringValue(value.apiBaseUrl), 'api'),
    imageBaseUrl: sanitizeStoredTmdbBaseUrl(stringValue(value.imageBaseUrl), 'image'),
  }
}

function sanitizeStoredTmdbBaseUrl(value: string | undefined, kind: 'api' | 'image'): string {
  try {
    return normalizeTmdbBaseUrl(value ?? (kind === 'api' ? DEFAULT_TMDB_API_BASE_URL : DEFAULT_TMDB_IMAGE_BASE_URL), kind)
  }
  catch {
    return kind === 'api' ? DEFAULT_TMDB_API_BASE_URL : DEFAULT_TMDB_IMAGE_BASE_URL
  }
}

function sanitizeLocale(value: string | undefined, fallback: string): string {
  return value && /^[a-z]{2}(?:-[A-Z]{2})?$/.test(value) ? value : fallback
}

function sanitizeRegion(value: string | undefined, fallback: string): string {
  return value && /^[A-Z]{2}$/.test(value) ? value : fallback
}

function stringValue(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function numberValue(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function nonNegativeNumberValue(value: unknown): number | undefined {
  const number = numberValue(value)
  return number != null && number >= 0 ? number : undefined
}

function positiveIntegerValue(value: unknown): number | undefined {
  const number = numberValue(value)
  return number != null && Number.isInteger(number) && number > 0 ? number : undefined
}

function isPositiveInteger(value: number): boolean {
  return Number.isInteger(value) && value > 0
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
