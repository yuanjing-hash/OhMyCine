import type { ScrapeMediaType } from './classificationRules'
import type { RawMediaCandidate } from './types'
import type { TmdbCredentialValue } from '@/services/datasource/credentialStore'
import { readTmdbCredential, removeCredential, saveTmdbCredential } from '@/services/datasource/credentialStore'
import { extractMediaSearchTitles, normalizeTitleKey } from './parser'
import { stripFileExtension } from './pathUtils'

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
  readonly posterUrl?: string
  readonly backdropUrl?: string
  readonly scrapedAt: string
}

export interface TmdbCandidateMatch {
  readonly metadata: TmdbMetadata
  readonly searchTitle: string
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
  return readTmdbCredential(settings.credentialRef)
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

  async getDetail(mediaType: ScrapeMediaType, tmdbId: number): Promise<TmdbMetadata> {
    const data = await this.requestJson(`/${mediaType}/${tmdbId}`, {
      language: this.settings.language,
      append_to_response: 'external_ids',
    })
    return mapTmdbDetail(data, mediaType)
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
    const url = new URL(`${TMDB_BASE_URL}${path}`)
    for (const [key, value] of Object.entries(params)) {
      if (value)
        url.searchParams.set(key, value)
    }
    if (this.credential.authType === 'apiKey')
      url.searchParams.set('api_key', this.credential.value)

    const headers = new Headers()
    headers.set('Accept', 'application/json')
    if (this.credential.authType === 'readAccessToken')
      headers.set('Authorization', `Bearer ${this.credential.value}`)

    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), this.timeoutMs)
    try {
      const response = await fetch(url, { headers, signal: controller.signal })
      if (!response.ok)
        throw new Error(`TMDB request failed with status ${response.status}.`)
      return response.json()
    }
    catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError')
        throw new Error('TMDB 请求超时。')
      throw new Error('TMDB 请求失败。')
    }
    finally {
      window.clearTimeout(timeout)
    }
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

function mapTmdbDetail(value: unknown, mediaType: ScrapeMediaType): TmdbMetadata {
  if (!isRecord(value))
    throw new Error('TMDB detail response is invalid.')

  const tmdbId = numberValue(value.id)
  const title = stringValue(mediaType === 'movie' ? value.title : value.name)
  if (tmdbId == null || !title)
    throw new Error('TMDB detail response is incomplete.')

  const releaseDate = stringValue(mediaType === 'movie' ? value.release_date : value.first_air_date)
  const posterPath = stringValue(value.poster_path)
  const backdropPath = stringValue(value.backdrop_path)

  return {
    tmdbId,
    mediaType,
    title,
    originalTitle: stringValue(mediaType === 'movie' ? value.original_title : value.original_name),
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
    posterUrl: posterPath ? tmdbImageUrl(posterPath, 'w500') : undefined,
    backdropUrl: backdropPath ? tmdbImageUrl(backdropPath, 'w1280') : undefined,
    scrapedAt: new Date().toISOString(),
  }
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
