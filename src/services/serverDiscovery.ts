import type { ServerDataSource } from '@/services/datasource/server'

export interface ServerDiscoveryWork {
  provider: 'tmdb' | 'douban'
  providerId: string
  mediaType: 'movie' | 'tv'
  title: string
  originalTitle?: string
  year?: number
  overview?: string
  rating?: number
  posterUrl?: string
  backdropUrl?: string
  tmdbId?: number
}

export interface ServerDiscoveryDetail {
  work: ServerDiscoveryWork
  tagline?: string
  runtimeMinutes?: number
  seasonCount?: number
  episodeCount?: number
  genres: string[]
  directors: string[]
  cast: string[]
}

export interface ServerSearchSite { id: number, name: string, siteType: string, searchable: boolean, reason?: string }
export interface ServerResourceItem { token: string, title: string, subtitle?: string, sizeBytes?: number, seeders?: number, promotion?: string, quality?: string, matchedName?: string }
export interface ServerResourceGroup { siteId: number, siteName: string, siteType: string, status: string, errorCode?: string, items: ServerResourceItem[] }
export interface ServerDownloadOption { id: string, name: string, type?: string }
export interface ServerLibraryOption { id: number, name: string }
export interface ServerProfileOption { id: number, name: string }
export interface ServerCoverageSummary { status: string, present: number, missing: number, total: number }

export async function searchServerDiscovery(source: ServerDataSource, query: string, mediaType: 'all' | 'movie' | 'tv' = 'all'): Promise<ServerDiscoveryWork[]> {
  const data = record(await source.searchDiscoveryMedia(query, mediaType))
  const works = array(data.items).map(parseWork).filter((item): item is ServerDiscoveryWork => item != null)
  await hydrateArtwork(source, works, false)
  return works
}

export async function getServerDiscoveryDetail(source: ServerDataSource, provider: 'tmdb' | 'douban', mediaType: 'movie' | 'tv', providerId: string): Promise<ServerDiscoveryDetail> {
  const data = record(await source.getDiscoveryDetail(provider, mediaType, providerId))
  const work = parseWork(data.work)
  if (!work)
    throw new Error('Server 返回的影视详情无效。')
  await hydrateArtwork(source, [work], true)
  return {
    work,
    tagline: text(data.tagline),
    runtimeMinutes: number(data.runtime_minutes),
    seasonCount: number(data.season_count),
    episodeCount: number(data.episode_count),
    genres: strings(data.genres),
    directors: people(data.directors),
    cast: people(data.cast).slice(0, 12),
  }
}

export async function getServerSearchSites(source: ServerDataSource): Promise<ServerSearchSite[]> {
  const data = record(await source.getDiscoverySearchOptions())
  return array(data.list).flatMap((value): ServerSearchSite[] => {
    const item = record(value)
    const id = number(item.id)
    const name = text(item.name)
    if (!id || !name)
      return []
    return [{ id, name, siteType: text(item.site_type) ?? '', searchable: item.searchable === true, reason: text(item.reason) }]
  })
}

export async function getServerCoverage(source: ServerDataSource, mediaType: 'movie' | 'tv', tmdbId: number): Promise<ServerCoverageSummary> {
  const data = record(await source.getDiscoveryCoverage(mediaType, tmdbId))
  const tv = record(data.tv)
  const counts = record(tv.counts)
  const movie = record(data.movie)
  return mediaType === 'movie'
    ? { status: text(data.status) ?? 'unknown', present: movie.present === true ? 1 : 0, missing: movie.present === true ? 0 : 1, total: 1 }
    : { status: text(data.status) ?? 'unknown', present: number(counts.present) ?? 0, missing: number(counts.missing) ?? 0, total: number(counts.total) ?? 0 }
}

export async function searchServerResources(source: ServerDataSource, input: { mediaType: 'movie' | 'tv', tmdbId?: number, title?: string, direct?: boolean, siteIds: number[] }): Promise<ServerResourceGroup[]> {
  const params = new URLSearchParams({ page: '1' })
  for (const id of input.siteIds)
    params.append('site_ids', String(id))
  let path: string
  if (!input.direct && input.tmdbId) {
    path = `/api/v1/player/discovery/media/${input.mediaType}/${input.tmdbId}/torrent-search?${params}`
  }
  else {
    if (input.tmdbId && !input.title) {
      params.set('search_by', 'tmdb_id')
      params.set('tmdb_id', String(input.tmdbId))
      params.set('media_type', input.mediaType)
      params.set('keyword', String(input.tmdbId))
    }
    else {
      params.set('search_by', 'title')
      params.set('keyword', input.title?.trim() ?? '')
      params.set('media_type', input.mediaType)
    }
    path = `/api/v1/player/discovery/torrent-search?${params}`
  }
  const data = record(await source.searchDiscoveryResources(path))
  return array(data.groups).map(parseGroup).filter((item): item is ServerResourceGroup => item != null)
}

export async function getServerDownloadOptions(source: ServerDataSource) {
  const data = await source.getDiscoveryDownloadOptions()
  const downloaders = array(record(data.downloaders).list).flatMap((value): ServerDownloadOption[] => {
    const item = record(value)
    const id = text(item.id)
    const name = text(item.name)
    return id && name && item.enabled !== false ? [{ id, name, type: text(item.type) }] : []
  })
  const libraries = array(record(data.libraries).list).flatMap((value): ServerLibraryOption[] => {
    const item = record(value)
    const id = number(item.id)
    const name = text(item.name)
    return id && name && item.enabled !== false ? [{ id, name }] : []
  })
  const profiles = array(record(data.profiles).list).flatMap((value): ServerProfileOption[] => {
    const item = record(value)
    const id = number(item.id)
    const name = text(item.name)
    return id && name ? [{ id, name }] : []
  })
  return { downloaders, libraries, profiles }
}

function parseWork(value: unknown): ServerDiscoveryWork | null {
  const item = record(value)
  const provider = item.provider === 'tmdb' || item.provider === 'douban' ? item.provider : null
  const mediaType = item.media_type === 'movie' || item.media_type === 'tv' ? item.media_type : null
  const providerId = text(item.provider_id)
  const title = text(item.title)
  if (!provider || !mediaType || !providerId || !title)
    return null
  return { provider, providerId, mediaType, title, originalTitle: text(item.original_title), year: number(item.year), overview: text(item.overview), rating: number(item.rating), posterUrl: text(item.poster_url), backdropUrl: text(item.backdrop_url), tmdbId: number(item.tmdb_id) }
}

function parseGroup(value: unknown): ServerResourceGroup | null {
  const item = record(value)
  const siteId = number(item.site_id)
  const siteName = text(item.site_name)
  if (!siteId || !siteName)
    return null
  return { siteId, siteName, siteType: text(item.site_type) ?? '', status: text(item.status) ?? 'error', errorCode: text(item.error_code), items: array(item.items).flatMap((raw): ServerResourceItem[] => {
    const result = record(raw)
    const token = text(result.token)
    const title = text(result.title)
    return token && title ? [{ token, title, subtitle: text(result.subtitle), sizeBytes: number(result.size_bytes), seeders: number(result.seeders), promotion: text(result.promotion), quality: text(result.quality), matchedName: text(result.matched_name) }] : []
  }) }
}

async function hydrateArtwork(source: ServerDataSource, works: ServerDiscoveryWork[], includeBackdrop: boolean) {
  await Promise.all(works.map(async (work) => {
    const [poster, backdrop] = await Promise.all([
      work.posterUrl ? source.loadDiscoveryArtwork(work.posterUrl).catch(() => undefined) : undefined,
      includeBackdrop && work.backdropUrl ? source.loadDiscoveryArtwork(work.backdropUrl).catch(() => undefined) : undefined,
    ])
    work.posterUrl = poster
    work.backdropUrl = backdrop
  }))
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}
function array(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}
function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}
function number(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}
function strings(value: unknown): string[] {
  return array(value).map(text).filter((item): item is string => Boolean(item))
}
function people(value: unknown): string[] {
  return array(value).map(item => text(record(item).name)).filter((item): item is string => Boolean(item))
}
