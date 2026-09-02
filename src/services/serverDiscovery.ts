import type { ServerDataSource, ServerDiscoveryStreamHandle } from '@/services/datasource/server'

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

export interface ServerSearchSite {
  id: number
  name: string
  siteType: string
  searchable: boolean
  reason?: string
}
export interface ServerResourceItem {
  token: string
  title: string
  subtitle?: string
  sizeBytes?: number
  seeders?: number
  promotion?: string
  quality?: string
  matchedName?: string
}
export interface ServerResourceGroup {
  siteId: number
  siteName: string
  siteType: string
  status: string
  errorCode?: string
  page: number
  hasNext: boolean
  skipped: number
  items: ServerResourceItem[]
}
export interface ServerDownloadOption { id: string, name: string, type?: string }
export interface ServerLibraryOption { id: number, name: string }
export interface ServerProfileOption { id: number, name: string }
export interface ServerCoverageSummary { status: string, present: number, missing: number, total: number }
export interface ServerAcquisitionStatus {
  id?: string
  title?: string
  mediaType: 'movie' | 'tv'
  tmdbId: number
  stage: string
  status: string
  downloadTaskId?: string
  followSubscriptionId?: string
  targetLibraryId?: number
  transferTaskId?: string
  progress?: number
  bytesCompleted?: number
  bytesTotal?: number
  downloadSpeed?: number
  etaSeconds?: number
  processedFiles: number
  totalFiles: number
  lastErrorCode?: string
  revision: number
  updatedAt?: string
}
export interface ServerAcquisitionPage { list: ServerAcquisitionStatus[], total: number, page: number, pageSize: number }
export interface ServerFollowSnapshot {
  version: number
  seasons: number[]
  site_ids: number[]
  downloader_id: string
  media_library_id: number
  schedule: { kind: string, minutes: number }
  filters: Record<string, unknown>
  max_resources_per_run: number
  download_priority: number
}
export interface ServerFollowDefaults {
  snapshot: ServerFollowSnapshot
  sites: { id: number, name: string, site_type: string }[]
  downloaders: ServerDownloadOption[]
  mediaLibraries: ServerLibraryOption[]
  subscribedSeasons: number[]
}
export interface ServerSearchProgress {
  total: number
  pending: number
  running: number
  completed: number
  succeeded: number
  failed: number
  resultCount: number
  siteId?: number
  siteName?: string
  siteStatus?: string
  errorCode?: string
}
export type ServerResourceStreamEvent
  = | { type: 'progress', progress: ServerSearchProgress }
    | { type: 'site', group: ServerResourceGroup }
    | { type: 'done', progress: ServerSearchProgress }
    | { type: 'error', code?: string, message: string }

export interface ServerResourceSearchInput { mediaType: 'movie' | 'tv', tmdbId?: number, title?: string, direct?: boolean, siteIds: number[], page?: number }

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

export async function getServerAcquisition(source: ServerDataSource, mediaType: 'movie' | 'tv', tmdbId: number): Promise<ServerAcquisitionStatus> {
  const data = record(await source.getDiscoveryAcquisition(mediaType, tmdbId))
  return parseAcquisition(data, mediaType, tmdbId)
}

export async function getServerAcquisitions(source: ServerDataSource, page = 1, pageSize = 30): Promise<ServerAcquisitionPage> {
  const data = record(await source.getDiscoveryAcquisitions(page, pageSize))
  return {
    list: array(data.list).map(value => parseAcquisition(record(value))).filter(item => Boolean(item.id)),
    total: number(data.total) ?? 0,
    page: number(data.page) ?? page,
    pageSize: number(data.page_size) ?? pageSize,
  }
}

export async function getServerFollowDefaults(source: ServerDataSource, tmdbId: number): Promise<ServerFollowDefaults> {
  const data = record(await source.getDiscoveryFollowDefaults(tmdbId))
  const snapshot = record(data.snapshot)
  const schedule = record(snapshot.schedule)
  const sites = array(data.sites).flatMap((value) => {
    const item = record(value)
    const id = number(item.id)
    const name = text(item.name)
    return id && name ? [{ id, name, site_type: text(item.site_type) ?? '' }] : []
  })
  const downloaders = array(data.downloaders).flatMap((value) => {
    const item = record(value)
    const id = text(item.id)
    const name = text(item.name)
    return id && name ? [{ id, name, type: text(item.type) }] : []
  })
  const mediaLibraries = array(data.media_libraries).flatMap((value) => {
    const item = record(value)
    const id = number(item.id)
    const name = text(item.name)
    return id && name ? [{ id, name }] : []
  })
  return {
    snapshot: {
      version: number(snapshot.version) ?? 1,
      seasons: array(snapshot.seasons).flatMap(value => typeof value === 'number' ? [value] : []),
      site_ids: array(snapshot.site_ids).flatMap(value => typeof value === 'number' ? [value] : []),
      downloader_id: text(snapshot.downloader_id) ?? '',
      media_library_id: number(snapshot.media_library_id) ?? 0,
      schedule: { kind: text(schedule.kind) ?? 'interval', minutes: number(schedule.minutes) ?? 360 },
      filters: record(snapshot.filters),
      max_resources_per_run: number(snapshot.max_resources_per_run) ?? 3,
      download_priority: number(snapshot.download_priority) ?? 0,
    },
    sites,
    downloaders,
    mediaLibraries,
    subscribedSeasons: array(data.subscribed_seasons).flatMap(value => typeof value === 'number' ? [value] : []),
  }
}

export async function searchServerResources(source: ServerDataSource, input: ServerResourceSearchInput): Promise<ServerResourceGroup[]> {
  const path = resourceSearchPath(input, false)
  const data = record(await source.searchDiscoveryResources(path))
  return array(data.groups).map(parseGroup).filter((item): item is ServerResourceGroup => item != null)
}

export async function streamServerResources(source: ServerDataSource, input: ServerResourceSearchInput, onEvent: (event: ServerResourceStreamEvent) => void): Promise<ServerDiscoveryStreamHandle> {
  return source.streamDiscoveryResources(resourceSearchPath(input, true), (raw) => {
    const data = record(raw.data)
    if (raw.event === 'site') {
      const group = parseGroup(data)
      if (group)
        onEvent({ type: 'site', group })
      return
    }
    if (raw.event === 'progress' || raw.event === 'done') {
      const progress = parseProgress(data)
      onEvent({ type: raw.event, progress })
      return
    }
    if (raw.event === 'error')
      onEvent({ type: 'error', code: text(data.code), message: text(data.message) ?? 'Server 资源搜索失败。' })
  })
}

function resourceSearchPath(input: ServerResourceSearchInput, stream: boolean): string {
  const params = new URLSearchParams({ page: String(input.page ?? 1) })
  for (const id of input.siteIds)
    params.append('site_ids', String(id))
  let path: string
  if (!input.direct && input.tmdbId) {
    path = `/api/v1/player/discovery/media/${input.mediaType}/${input.tmdbId}/torrent-search${stream ? '/stream' : ''}?${params}`
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
    path = `/api/v1/player/discovery/torrent-search${stream ? '/stream' : ''}?${params}`
  }
  return path
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
  return { siteId, siteName, siteType: text(item.site_type) ?? '', status: text(item.status) ?? 'error', errorCode: text(item.error_code), page: number(item.page) ?? 1, hasNext: item.has_next === true, skipped: number(item.skipped) ?? 0, items: array(item.items).flatMap((raw): ServerResourceItem[] => {
    const result = record(raw)
    const token = text(result.token)
    const title = text(result.title)
    return token && title ? [{ token, title, subtitle: text(result.subtitle), sizeBytes: number(result.size_bytes), seeders: number(result.seeders), promotion: text(result.promotion), quality: text(result.quality), matchedName: text(result.matched_name) }] : []
  }) }
}

function parseAcquisition(data: Record<string, unknown>, fallbackMediaType: 'movie' | 'tv' = 'movie', fallbackTMDBId = 0): ServerAcquisitionStatus {
  return {
    id: text(data.id),
    title: text(data.title),
    mediaType: data.media_type === 'tv' ? 'tv' : fallbackMediaType,
    tmdbId: number(data.tmdb_id) ?? fallbackTMDBId,
    stage: text(data.stage) ?? 'idle',
    status: text(data.status) ?? 'idle',
    downloadTaskId: text(data.download_task_id),
    followSubscriptionId: text(data.follow_subscription_id),
    targetLibraryId: number(data.target_library_id),
    transferTaskId: text(data.transfer_task_id),
    progress: number(data.progress),
    bytesCompleted: number(data.bytes_completed),
    bytesTotal: number(data.bytes_total),
    downloadSpeed: number(data.download_speed),
    etaSeconds: number(data.eta_seconds),
    processedFiles: number(data.processed_files) ?? 0,
    totalFiles: number(data.total_files) ?? 0,
    lastErrorCode: text(data.last_error_code),
    revision: number(data.revision) ?? 0,
    updatedAt: text(data.updated_at),
  }
}

function parseProgress(value: Record<string, unknown>): ServerSearchProgress {
  return {
    total: number(value.total) ?? 0,
    pending: number(value.pending) ?? 0,
    running: number(value.running) ?? 0,
    completed: number(value.completed) ?? 0,
    succeeded: number(value.succeeded) ?? 0,
    failed: number(value.failed) ?? 0,
    resultCount: number(value.result_count) ?? 0,
    siteId: number(value.site_id),
    siteName: text(value.site_name),
    siteStatus: text(value.site_status),
    errorCode: text(value.error_code),
  }
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
