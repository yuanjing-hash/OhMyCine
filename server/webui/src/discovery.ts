export type DiscoveryProviderCode = 'tmdb' | 'douban'
export type DiscoveryMediaType = 'movie' | 'tv'
export type DiscoveryCategory = 'movie' | 'tv' | 'anime'

export interface DiscoveryWork {
  provider: DiscoveryProviderCode
  provider_id: string
  media_type: DiscoveryMediaType
  title: string
  original_title?: string
  year?: number
  overview?: string
  rating?: number
  vote_count?: number
  poster_url?: string
  backdrop_url?: string
  tmdb_id?: number
  douban_id?: string
}

export interface DiscoverySectionDefinition { code: string; title: string; media_type?: DiscoveryMediaType; category?: DiscoveryCategory }
export interface DiscoveryProviderSummary { code: DiscoveryProviderCode; sections: DiscoverySectionDefinition[] }
export interface DiscoverySection extends DiscoverySectionDefinition {
  provider: DiscoveryProviderCode
  page: number
  total_pages: number
  items: DiscoveryWork[]
  fetched_at: string
  stale: boolean
  error_code?: string
}
export interface DiscoveryOverview { providers: DiscoveryProviderSummary[]; sections: DiscoverySection[]; updated_at: string }

export type DiscoveryMediaSearchFilter = 'all' | DiscoveryMediaType
export interface DiscoveryMediaSearch {
  query: string
  media_type: DiscoveryMediaSearchFilter
  page: number
  total_pages: number
  items: DiscoveryWork[]
}

export interface DiscoverySearchName { value: string; locale?: string; kind: 'localized' | 'alias' | 'original' | 'english' | 'translation' }
export type MediaCoverageEpisodeStatus = 'present' | 'missing' | 'future' | 'unknown'
export type MediaCoverageStatus = 'present' | 'partial' | 'missing' | 'future_or_incomplete' | 'unknown'
export interface MediaCoverageCounts { total: number; present: number; missing: number; future: number; unknown: number }
export interface MediaCoverageLibrary { id: number; name: string; scan_state: 'complete' | 'partial' | 'unscanned'; last_successful_at?: string; content_revision: number }
export interface MediaCoverageEpisode { episode_number: number; name?: string; air_date?: string; status: MediaCoverageEpisodeStatus; library_ids: number[] }
export interface MediaCoverageSeason { season_number: number; name: string; poster_url?: string; special: boolean; status: MediaCoverageStatus; counts: MediaCoverageCounts; episodes: MediaCoverageEpisode[] }
export interface MediaCoverage {
  media_type: DiscoveryMediaType
  tmdb_id: number
  title: string
  status: MediaCoverageStatus | 'present'
  libraries: MediaCoverageLibrary[]
  freshness: { checked_at: string; library_scan_state: 'complete' | 'partial' | 'unscanned'; tmdb_state: 'complete' | 'partial' }
  movie?: { present: boolean; library_ids: number[] }
  tv?: { counts: MediaCoverageCounts; seasons: MediaCoverageSeason[] }
}

export interface DiscoveryPerson { tmdb_id?: number; name: string; role?: string; character?: string; profile_url?: string }
export interface DiscoveryDetail {
  work: DiscoveryWork
  tagline?: string
  status?: string
  imdb_id?: string
  runtime_minutes?: number
  season_count?: number
  episode_count?: number
  genres: string[]
  countries: string[]
  spoken_languages: string[]
  studios: string[]
  directors: DiscoveryPerson[]
  writers: DiscoveryPerson[]
  cast: DiscoveryPerson[]
  backdrop_urls: string[]
  recommendations: DiscoveryWork[]
  similar: DiscoveryWork[]
  resolved_from_tmdb: boolean
}

export const discoveryRecommendationsPath = '/api/v1/discovery/recommendations'
export const discoveryRefreshPath = '/api/v1/discovery/recommendations/refresh'
export const discoveryMediaSearchPath = '/api/v1/discovery/media-search'

export function discoveryDetailPath(work: Pick<DiscoveryWork, 'provider' | 'media_type' | 'provider_id'>) {
  return `/api/v1/discovery/details/${encodeURIComponent(work.provider)}/${encodeURIComponent(work.media_type)}/${encodeURIComponent(work.provider_id)}`
}

export function discoveryDetailRoute(work: Pick<DiscoveryWork, 'provider' | 'media_type' | 'provider_id'>) {
  return `/discovery/details/${encodeURIComponent(work.provider)}/${encodeURIComponent(work.media_type)}/${encodeURIComponent(work.provider_id)}`
}

export function buildDiscoveryMediaSearchPath(query: string, mediaType: DiscoveryMediaSearchFilter = 'all', page = 1) {
  const params = new URLSearchParams({ query: query.trim(), media_type: mediaType, page: String(Math.max(1, Math.min(500, page))) })
  return `${discoveryMediaSearchPath}?${params}`
}

export function discoveryCoveragePath(mediaType: DiscoveryMediaType, tmdbID: number) {
  return `/api/v1/discovery/media/${encodeURIComponent(mediaType)}/${encodeURIComponent(String(tmdbID))}/coverage`
}

export function mediaIdentitySearchPath(mediaType: DiscoveryMediaType, tmdbID: number, stream = false) {
  return `/api/v1/discovery/media/${encodeURIComponent(mediaType)}/${encodeURIComponent(String(tmdbID))}/torrent-search${stream ? '/stream' : ''}`
}

export function mediaIdentitySearchURL(mediaType: DiscoveryMediaType, tmdbID: number, options: { season?: number; page?: number; siteID?: number } = {}, stream = false) {
  const query = new URLSearchParams({ page: String(Math.max(1, Math.min(20, options.page ?? 1))) })
  if (options.season != null) query.set('season', String(options.season))
  if (options.siteID) query.set('site_id', String(options.siteID))
  return `${mediaIdentitySearchPath(mediaType, tmdbID, stream)}?${query}`
}

export function discoveryResourceRoute(work: DiscoveryWork) {
  const query = discoveryWorkQuery(work)
  query.mode = 'resources'
  return { path: '/discovery/explore', query }
}

export function coverageStatusLabel(status: MediaCoverageStatus | MediaCoverageEpisodeStatus) {
  return ({ present: '已入库', partial: '部分入库', missing: '缺失', future: '未播', future_or_incomplete: '待更新', unknown: '未知 / 待扫描' } as const)[status]
}

export function buildDiscoveryPath(provider: '' | DiscoveryProviderCode = '', page = 1) {
  const query = new URLSearchParams({ page: String(Math.max(1, Math.min(5, page))) })
  if (provider) query.set('provider', provider)
  return `${discoveryRecommendationsPath}?${query}`
}

export function buildRefreshPayload(section: DiscoverySection) {
  return { provider: section.provider, section: section.code, page: section.page }
}

export function discoveryWorkQuery(work: DiscoveryWork) {
  const query: Record<string, string> = {
    title: work.title,
    media_type: work.media_type,
    provider: work.provider,
    provider_id: work.provider_id,
  }
  if (work.year) query.year = String(work.year)
  if (work.tmdb_id) query.tmdb_id = String(work.tmdb_id)
  if (work.douban_id) query.douban_id = work.douban_id
  return query
}

export function providerLabel(code: DiscoveryProviderCode) { return code === 'douban' ? '豆瓣' : 'TMDB' }
