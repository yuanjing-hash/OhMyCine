import type { ScrapeMediaType } from './classificationRules'
import type { RawMediaCandidate } from './types'
import type { TmdbCredentialValue } from '@/services/datasource/credentialStore'
import { readTmdbCredential, removeCredential, saveTmdbCredential } from '@/services/datasource/credentialStore'
import { extractMediaSearchTitles, normalizeTitleKey } from './parser'
import { stripFileExtension } from './pathUtils'
import { buildTmdbRequestDescriptor, tmdbHttpFailureMessage } from './tmdbAuth'

export type TmdbAuthType = TmdbCredentialValue['authType']

export interface TmdbLocalSettings {
  readonly credentialRef: string
  readonly authType: TmdbAuthType
  readonly language: string
  readonly region: string
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
}

const TMDB_BASE_URL = 'https://api.themoviedb.org/3'
const TMDB_IMAGE_BASE_URL = 'https://image.tmdb.org/t/p'
const DEFAULT_TMDB_TIMEOUT_MS = 10_000

export function loadTmdbLocalSettings(): TmdbLocalSettings {
  try {
    const raw = localStorage.getItem(TMDB_SETTINGS_STORAGE_KEY)
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
  localStorage.setItem(TMDB_SETTINGS_STORAGE_KEY, JSON.stringify(sanitized))
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
  const credential = await readTmdbCredential(settings.credentialRef)
  if (!credential || credential.authType !== settings.authType)
    return null
  return credential
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
    return mapTmdbDetail(data, mediaType, this.settings.language)
  }

  async getImageCandidates(mediaType: ScrapeMediaType, tmdbId: number, kind: TmdbImageKind): Promise<TmdbImageCandidate[]> {
    const data = await this.requestJson(`/${mediaType}/${tmdbId}/images`, {
      include_image_language: preferredImageLanguageParam(this.settings.language),
    })
    return mapTmdbImageCandidates(data, kind, this.settings.language)
  }

  async getEpisodeDetail(tvTmdbId: number, seasonNumber: number, episodeNumber: number): Promise<TmdbEpisodeMetadata> {
    if (!isPositiveInteger(tvTmdbId) || !isPositiveInteger(seasonNumber) || !isPositiveInteger(episodeNumber))
      throw new Error('TMDB episode request is invalid.')

    const data = await this.requestJson(`/tv/${tvTmdbId}/season/${seasonNumber}/episode/${episodeNumber}`, {
      language: this.settings.language,
    })
    return mapTmdbEpisodeDetail(data, tvTmdbId, seasonNumber, episodeNumber)
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
    const request = buildTmdbRequestDescriptor({
      baseUrl: TMDB_BASE_URL,
      path,
      params,
      credential: this.credential,
    })

    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), this.timeoutMs)
    try {
      const response = await fetch(request.url, { headers: request.headers, signal: controller.signal })
      if (!response.ok)
        throw new TmdbHttpError(tmdbHttpFailureMessage(this.credential.authType, response.status, await safeReadResponseText(response)))
      return response.json()
    }
    catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError')
        throw new Error('TMDB 请求超时。')
      if (error instanceof TmdbHttpError)
        throw error
      throw new Error('TMDB 请求失败。')
    }
    finally {
      window.clearTimeout(timeout)
    }
  }
}

class TmdbHttpError extends Error {}

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

  if (year) {
    const exactYear = acceptable.find(result => result.year === year)
    if (exactYear)
      return exactYear
    const nearbyYear = acceptable.find(result => result.year != null && Math.abs(result.year - year) <= 1)
    if (nearbyYear)
      return nearbyYear
  }

  return [...acceptable].sort((left, right) => (right.popularity ?? 0) - (left.popularity ?? 0))[0] ?? null
}

function rankSearchResults(
  results: readonly TmdbSearchResult[],
  query: string,
  year?: number,
): TmdbSearchResult[] {
  return [...results].sort((left, right) => searchResultScore(right, query, year) - searchResultScore(left, query, year))
}

function searchResultScore(result: TmdbSearchResult, query: string, year?: number): number {
  const titleMatch = isAcceptableTitleMatch(query, result.title, result.originalTitle) ? 100 : 0
  const exactYear = year != null && result.year === year ? 40 : 0
  const nearbyYear = year != null && result.year != null && Math.abs(result.year - year) <= 1 ? 20 : 0
  return titleMatch + exactYear + nearbyYear + (result.popularity ?? 0)
}

function isAcceptableTitleMatch(query: string, title: string, originalTitle?: string): boolean {
  const queryKey = compactTitleKey(query)
  const titleKeys = [title, originalTitle].map(compactTitleKey).filter(Boolean)
  if (!queryKey || titleKeys.length === 0)
    return false
  if (titleKeys.some(key => key === queryKey || key.includes(queryKey) || queryKey.includes(key)))
    return true

  const queryTokens = tokenizeTitle(query)
  if (queryTokens.length === 0)
    return false

  return titleKeys.some((key) => {
    const titleTokens = tokenizeTitle(key)
    if (titleTokens.length === 0)
      return false
    const overlap = queryTokens.filter(token => titleTokens.includes(token)).length
    return overlap / Math.max(queryTokens.length, titleTokens.length) >= 0.55
  })
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

function mapTmdbDetail(value: unknown, mediaType: ScrapeMediaType, language: string): TmdbMetadata {
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
    posterUrl: posterPath ? tmdbImageUrl(posterPath, 'w500') : undefined,
    backdropUrl: backdropPath ? tmdbImageUrl(backdropPath, 'w1280') : undefined,
    titleLogoUrl: titleLogoPath ? tmdbImageUrl(titleLogoPath, 'w500') : undefined,
    scrapedAt: new Date().toISOString(),
  }
}

function mapTmdbEpisodeDetail(
  value: unknown,
  tvTmdbId: number,
  requestedSeasonNumber: number,
  requestedEpisodeNumber: number,
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
    stillUrl: stillPath ? tmdbImageUrl(stillPath, 'w780') : undefined,
    scrapedAt: new Date().toISOString(),
  }
}

function selectPreferredLogoPath(images: unknown, language: string): string | undefined {
  if (!isRecord(images))
    return undefined

  const logos = imageRecords(images.logos)
  return rankImageRecords(logos, preferredLogoLanguages(language))[0]?.filePath
}

function mapTmdbImageCandidates(value: unknown, kind: TmdbImageKind, language: string): TmdbImageCandidate[] {
  if (!isRecord(value))
    return []

  const records = imageRecords(kind === 'poster' ? value.posters : kind === 'logo' ? value.logos : value.backdrops)
  const languagePriority = kind === 'logo' ? preferredLogoLanguages(language) : preferredImageLanguages(language)
  return rankImageRecords(records, languagePriority)
    .slice(0, 24)
    .map(record => ({
      kind,
      filePath: record.filePath,
      imageUrl: tmdbImageUrl(record.filePath, kind === 'backdrop' ? 'w780' : 'w500'),
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

function tmdbImageUrl(path: string, size: 'w500' | 'w780' | 'w1280'): string {
  return `${TMDB_IMAGE_BASE_URL}/${size}${path.startsWith('/') ? path : `/${path}`}`
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
