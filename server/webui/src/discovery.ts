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

export function discoveryDetailPath(work: Pick<DiscoveryWork, 'provider' | 'media_type' | 'provider_id'>) {
  return `/api/v1/discovery/details/${encodeURIComponent(work.provider)}/${encodeURIComponent(work.media_type)}/${encodeURIComponent(work.provider_id)}`
}

export function discoveryDetailRoute(work: Pick<DiscoveryWork, 'provider' | 'media_type' | 'provider_id'>) {
  return `/discovery/details/${encodeURIComponent(work.provider)}/${encodeURIComponent(work.media_type)}/${encodeURIComponent(work.provider_id)}`
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
