import type { ServerDataSource, ServerDiscoveryStreamHandle } from '@/services/datasource/server'
import type { ServerSubtitlePolicy } from '@/services/serverSubtitlePolicy'
import { ServerRequestError } from '@/services/datasource/server'
import { parseServerSubtitlePolicy } from '@/services/serverSubtitlePolicy'
import { parseServerWorkItemIdentity } from './datasource/serverIdentity'

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
  sourceKind?: string
  cloudProvider?: string
  contentInspection?: boolean
  payloadKind?: string
  resultType?: string
  origins?: { kind: 'tg' | 'plugin', name: string, postUrl?: string }[]
  acquisitionAvailable?: boolean
  acquisitionReason?: string
  expiresAt?: string
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
  snapshotId?: string
  snapshotExpiresAt?: string
  partial?: boolean
  truncated?: boolean
  completion?: string
  warnings?: { code: string, message: string }[]
  unavailableReason?: string
  complete?: boolean
  revision?: number
  incompleteReason?: string
  queriesCompleted?: number
  queriesFailed?: number
  queriesTotal?: number
}
export interface ServerDownloadOption { id: string, name: string, type?: string }
export interface ServerLibraryOption { id: number, name: string }
export interface ServerProfileOption { id: number, name: string }
export interface ServerRoutePair { downloaderId: string, libraryId: number }
export interface ServerRouteChoice {
  downloaderId: string
  downloaderName: string
  downloaderType: string
  libraryId: number
  libraryName: string
  enabled: boolean
  reasonCode?: string
  reasonMessage?: string
  routeKind?: string
  routeLabel?: string
}
export interface ServerRouteRecommendation {
  sourceKind: 'pt' | 'bt' | '115_share' | 'quark_share'
  recommended: ServerRoutePair | null
  choices: ServerRouteChoice[]
}
export interface ServerFollowRoutePreview {
  available: boolean
  routes: Array<{ siteId: number, siteName: string, sourceKind: string, recommended: ServerRoutePair | null, reasonCode?: string, reasonMessage?: string }>
}
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
  catalogTarget?: { libraryId: number, workToken: string }
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
export function isTerminalServerAcquisition(value: Pick<ServerAcquisitionStatus, 'stage' | 'status'>): boolean {
  return value.stage === 'idle' || ['failed', 'cancelled', 'canceled', 'unavailable'].includes(value.status)
    || (value.stage === 'library' && value.status === 'completed')
}

export function serverAcquisitionLabel(value: Pick<ServerAcquisitionStatus, 'stage' | 'status' | 'lastErrorCode'> | null | undefined): string {
  if (!value || value.stage === 'idle')
    return '尚未入库'
  if (value.stage === 'library' && value.status === 'completed')
    return '已入库'
  if (value.status === 'unavailable')
    return '任务状态暂不可用'
  if (value.status === 'failed')
    return '入库需要处理'
  if (value.status === 'cancelled' || value.status === 'canceled')
    return '入库已取消'
  if (value.status === 'queued')
    return '等待执行'
  if (value.status === 'publishing')
    return value.lastErrorCode === 'acquisition_publication_unverified' ? '入库结果待核对' : '正在发布入库目录'
  return ({ download: '正在下载', organize: '正在整理', transfer: '正在传输', import: '正在刮削入库', subscription: '订阅等待中' } as Record<string, string>)[value.stage] ?? value.stage
}

export interface ServerFollowSnapshot {
  version: number
  routing_policy: 'source_priority'
  seasons: number[]
  site_ids: number[]
  media_library_id: number
  schedule: { kind: string, minutes: number }
  filters: Record<string, unknown> & { subtitles?: ServerSubtitlePolicy }
  max_resources_per_run: number
  download_priority: number
}
export interface ServerFollowDefaults {
  snapshot: ServerFollowSnapshot
  policyDefaultsRevision?: number
  subtitlePolicySupported?: boolean
  seasons: { seasonNumber: number, name: string, missing: number }[]
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
  siteDetail?: ServerSearchSiteDetail
  phase?: ServerSearchPhase
  eventSequence?: number
}
export type ServerSearchPhase = 'preparing_media' | 'queued' | 'preparing_site' | 'restoring_session' | 'checking_session' | 'waiting_rate_limit' | 'querying' | 'processing_results' | 'finished' | 'unknown'
export interface ServerSearchSiteDetail {
  siteId: number
  siteName: string
  phase: ServerSearchPhase
  queryName?: string
  queryIndex?: number
  queriesCompleted?: number
  queriesFailed?: number
  queriesTotal?: number
  resultCount?: number
  elapsedMs?: number
  revision?: number
}
export type ServerResourceStreamEvent
  = | { type: 'progress', progress: ServerSearchProgress }
    | { type: 'site', group: ServerResourceGroup }
    | { type: 'done', progress: ServerSearchProgress }
    | { type: 'error', code?: string, message: string }

export type ServerDiscoveryTypePolicy = 'auto' | 'all' | 'manual'
export const serverDiscoveryTypeOptions = [
  ['115', '115'],
  ['quark', '夸克'],
  ['magnet', '磁力'],
  ['aliyun', '阿里'],
  ['baidu', '百度'],
  ['tianyi', '天翼'],
  ['uc', 'UC'],
  ['xunlei', '迅雷'],
  ['123', '123 网盘'],
  ['pikpak', 'PikPak'],
  ['ed2k', '电驴'],
  ['others', '其他'],
] as const
export function serverResourceTypeLabel(item: ServerResourceItem) {
  return serverDiscoveryTypeOptions.find(option => option[0] === item.resultType)?.[1] ?? item.cloudProvider ?? (item.sourceKind === 'bt' ? '磁力' : '')
}
export interface ServerResourceSearchInput { mediaType: 'movie' | 'tv', tmdbId?: number, title?: string, direct?: boolean, siteIds: number[], page?: number, typePolicy?: ServerDiscoveryTypePolicy, cloudTypes?: string[], snapshotId?: string, refresh?: boolean }

export async function searchServerDiscovery(source: ServerDataSource, query: string, mediaType: 'all' | 'movie' | 'tv' = 'all'): Promise<ServerDiscoveryWork[]> {
  const data = record(await source.searchDiscoveryMedia(query, mediaType))
  return array(data.items).map(item => parseWork(item, source)).filter((item): item is ServerDiscoveryWork => item != null)
}

export async function getServerDiscoveryDetail(source: ServerDataSource, provider: 'tmdb' | 'douban', mediaType: 'movie' | 'tv', providerId: string): Promise<ServerDiscoveryDetail> {
  const data = record(await source.getDiscoveryDetail(provider, mediaType, providerId))
  const work = parseWork(data.work, source)
  if (!work)
    throw new Error('Server 返回的影视详情无效。')
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
  if ((number(snapshot.version) ?? 0) < 2 || snapshot.routing_policy !== 'source_priority')
    throw new Error('当前 Server 不支持按资源来源自动选择下载器，请先升级 Server。')
  const schedule = record(snapshot.schedule)
  const filters = record(snapshot.filters)
  const subtitles = parseServerSubtitlePolicy(filters.subtitles)
  if (filters.subtitles !== undefined && !subtitles)
    throw new Error('Server 返回的字幕策略无效，请刷新后重试。')
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
  const seasons = array(record(record(data.coverage).tv).seasons).flatMap((value) => {
    const item = record(value)
    const seasonNumber = nonnegativeInteger(item.season_number)
    if (seasonNumber == null)
      return []
    return [{ seasonNumber, name: text(item.name) ?? (seasonNumber === 0 ? '特别篇' : `第 ${seasonNumber} 季`), missing: nonnegativeInteger(record(item.counts).missing) ?? 0 }]
  })
  return {
    snapshot: {
      version: number(snapshot.version) ?? 2,
      routing_policy: 'source_priority',
      seasons: array(snapshot.seasons).flatMap(value => typeof value === 'number' ? [value] : []),
      site_ids: array(snapshot.site_ids).flatMap(value => typeof value === 'number' ? [value] : []),
      media_library_id: number(snapshot.media_library_id) ?? 0,
      schedule: { kind: text(schedule.kind) ?? 'interval', minutes: number(schedule.minutes) ?? 360 },
      filters: { ...filters, ...(subtitles ? { subtitles } : {}) },
      max_resources_per_run: number(snapshot.max_resources_per_run) ?? 3,
      download_priority: number(snapshot.download_priority) ?? 0,
    },
    policyDefaultsRevision: number(data.policy_defaults_revision),
    subtitlePolicySupported: subtitles !== undefined,
    seasons: [...new Map(seasons.map(season => [season.seasonNumber, season])).values()].sort((left, right) => left.seasonNumber - right.seasonNumber),
    sites,
    downloaders,
    mediaLibraries,
    subscribedSeasons: array(data.subscribed_seasons).flatMap(value => typeof value === 'number' ? [value] : []),
  }
}

export async function getServerRouteRecommendation(source: ServerDataSource, resultToken: string, libraryId?: number): Promise<ServerRouteRecommendation> {
  let response: unknown
  try {
    response = await source.recommendDiscoveryRoute({ result_token: resultToken, ...(libraryId ? { media_library_id: libraryId } : {}) })
  }
  catch (reason) {
    throw routeCapabilityError(reason)
  }
  const data = record(response)
  const sourceKind = data.source_kind
  if ((sourceKind !== 'pt' && sourceKind !== 'bt' && sourceKind !== '115_share' && sourceKind !== 'quark_share') || !Array.isArray(data.choices))
    throw new Error('Server 返回的入库路由无效，请升级 Server。')
  const choices = data.choices.flatMap((value): ServerRouteChoice[] => {
    const item = record(value)
    const downloaderId = text(item.downloader_id)
    const downloaderName = text(item.downloader_name)
    const libraryId = number(item.media_library_id)
    const libraryName = text(item.library_name)
    if (!downloaderId || !downloaderName || !libraryId || !libraryName)
      return []
    return [{ downloaderId, downloaderName, downloaderType: text(item.downloader_type) ?? '', libraryId, libraryName, enabled: item.enabled === true, reasonCode: text(item.reason_code), reasonMessage: text(item.reason_message), routeKind: text(item.route_kind), routeLabel: text(item.route_label) }]
  })
  const recommended = parseRoutePair(data.recommended)
  if (recommended && !choices.some(choice => choice.enabled && choice.downloaderId === recommended.downloaderId && choice.libraryId === recommended.libraryId))
    throw new Error('Server 返回的推荐路由与可用选项不一致。')
  return { sourceKind, recommended, choices }
}

export async function getServerFollowRoutePreview(source: ServerDataSource, siteIds: number[], libraryId: number): Promise<ServerFollowRoutePreview> {
  let response: unknown
  try {
    response = await source.previewDiscoveryFollowRoutes({ site_ids: siteIds, media_library_id: libraryId })
  }
  catch (reason) {
    throw routeCapabilityError(reason)
  }
  const data = record(response)
  if (!Array.isArray(data.routes) || typeof data.available !== 'boolean')
    throw new Error('Server 返回的订阅路由预览无效，请升级 Server。')
  return {
    available: data.available,
    routes: data.routes.flatMap((value) => {
      const item = record(value)
      const siteId = number(item.site_id)
      const siteName = text(item.site_name)
      if (!siteId || !siteName)
        return []
      return [{ siteId, siteName, sourceKind: text(item.source_kind) ?? '', recommended: parseRoutePair(item.recommended), reasonCode: text(item.reason_code), reasonMessage: text(item.reason_message) }]
    }),
  }
}

function parseRoutePair(value: unknown): ServerRoutePair | null {
  const item = record(value)
  const downloaderId = text(item.downloader_id)
  const libraryId = number(item.media_library_id)
  return downloaderId && libraryId ? { downloaderId, libraryId } : null
}

function routeCapabilityError(reason: unknown): Error {
  if (reason instanceof ServerRequestError && (reason.status === 404 || reason.status === 405))
    return new Error('当前 Server 不支持按资源来源选择下载器，请先升级 Server。')
  return reason instanceof Error ? reason : new Error('Server 路由请求失败。')
}

export async function searchServerResources(source: ServerDataSource, input: ServerResourceSearchInput): Promise<ServerResourceGroup[]> {
  const path = resourceSearchPath(input, false, source.hasCapability('mixed_discovery_v1'))
  const data = record(await source.searchDiscoveryResources(path))
  return array(data.groups).map(parseGroup).filter((item): item is ServerResourceGroup => item != null)
}

export async function streamServerResources(source: ServerDataSource, input: ServerResourceSearchInput, onEvent: (event: ServerResourceStreamEvent) => void): Promise<ServerDiscoveryStreamHandle> {
  return source.streamDiscoveryResources(resourceSearchPath(input, true, source.hasCapability('mixed_discovery_v1')), (raw) => {
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

function resourceSearchPath(input: ServerResourceSearchInput, stream: boolean, mixed: boolean): string {
  const params = new URLSearchParams({ page: String(input.page ?? 1) })
  if (mixed) {
    params.set('client_capabilities', 'mixed_discovery_v1')
    if (input.typePolicy)
      params.set('type_policy', input.typePolicy)
    for (const type of input.cloudTypes ?? []) params.append('cloud_types', type)
    if (input.snapshotId)
      params.set('snapshot_id', input.snapshotId)
    if (input.refresh)
      params.set('refresh', 'true')
  }
  if (stream)
    params.set('progress_version', '2')
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

function parseWork(value: unknown, source: ServerDataSource): ServerDiscoveryWork | null {
  const item = record(value)
  const provider = item.provider === 'tmdb' || item.provider === 'douban' ? item.provider : null
  const mediaType = item.media_type === 'movie' || item.media_type === 'tv' ? item.media_type : null
  const providerId = text(item.provider_id)
  const title = text(item.title)
  if (!provider || !mediaType || !providerId || !title)
    return null
  return { provider, providerId, mediaType, title, originalTitle: text(item.original_title), year: number(item.year), overview: text(item.overview), rating: number(item.rating), posterUrl: source.discoveryArtworkURL(text(item.poster_url) ?? ''), backdropUrl: source.discoveryArtworkURL(text(item.backdrop_url) ?? ''), tmdbId: number(item.tmdb_id) }
}

function parseGroup(value: unknown): ServerResourceGroup | null {
  const item = record(value)
  const siteId = number(item.site_id)
  const siteName = text(item.site_name)
  const invalidSnapshot = ['complete', 'partial'].some(key => item[key] !== undefined && typeof item[key] !== 'boolean')
    || ['revision', 'queries_completed', 'queries_failed', 'queries_total'].some(key => item[key] !== undefined && nonnegativeInteger(item[key]) == null)
  if (!siteId || !Number.isSafeInteger(siteId) || siteId < 1 || !siteName || invalidSnapshot)
    return null
  const warnings = array(item.warnings).slice(0, 16).flatMap((value) => {
    const warning = record(value)
    const code = text(warning.code)
    const message = text(warning.message)
    return code && message ? [{ code, message }] : []
  })
  return { siteId, siteName, siteType: text(item.site_type) ?? '', status: text(item.status) ?? 'error', errorCode: text(item.error_code), page: number(item.page) ?? 1, hasNext: item.has_next === true, skipped: number(item.skipped) ?? 0, complete: optionalBoolean(item.complete), revision: nonnegativeInteger(item.revision), partial: optionalBoolean(item.partial), incompleteReason: text(item.incomplete_reason), queriesCompleted: nonnegativeInteger(item.queries_completed), queriesFailed: nonnegativeInteger(item.queries_failed), queriesTotal: nonnegativeInteger(item.queries_total), snapshotId: text(item.snapshot_id), snapshotExpiresAt: text(item.snapshot_expires_at), truncated: item.truncated === true, completion: text(item.completion), warnings, items: array(item.items).flatMap((raw): ServerResourceItem[] => {
    const result = record(raw)
    const token = text(result.token)
    const title = text(result.title)
    return token && title
      ? [{ token, title, subtitle: text(result.subtitle), sizeBytes: number(result.size_bytes), seeders: number(result.seeders), promotion: text(result.promotion), quality: text(result.quality), matchedName: text(result.matched_name), sourceKind: text(result.source_kind), cloudProvider: text(result.cloud_provider), resultType: text(result.result_type), contentInspection: result.content_inspection === true, payloadKind: text(result.payload_kind), expiresAt: text(result.expires_at), acquisitionAvailable: typeof result.acquisition_available === 'boolean' ? result.acquisition_available : undefined, acquisitionReason: text(result.acquisition_reason), origins: parseOrigins(result) }]
      : []
  }) }
}

function parseOrigins(result: Record<string, unknown>): NonNullable<ServerResourceItem['origins']> {
  const rawOrigins = array(result.origins)
  const origins = rawOrigins.length ? rawOrigins : text(result.channel) ? [{ kind: 'tg', name: result.channel, post_url: result.post_url }] : []
  return origins.slice(0, 16).flatMap((value) => {
    const origin = record(value)
    const name = text(origin.name)
    if ((origin.kind !== 'tg' && origin.kind !== 'plugin') || !name || name.length > 128 || hasDiscoveryControl(name))
      return []
    const rawPost = text(origin.post_url)
    const postUrl = origin.kind === 'tg' && rawPost && /^https:\/\/t\.me\/\w{3,64}\/[1-9]\d{0,19}$/.test(rawPost) ? rawPost : undefined
    return [{ kind: origin.kind, name, postUrl }]
  })
}

export async function getServerResourceLink(source: ServerDataSource, item: ServerResourceItem): Promise<string> {
  if (!source.hasCapability('mixed_discovery_v1'))
    throw new Error('当前 Server 尚不支持混合资源链接查看；原有入库功能仍可使用。')
  const result = record(await source.getDiscoveryResultLink(item.token))
  const value = text(result.url)
  if (!value || value.length > 8192 || hasDiscoveryControl(value, true))
    throw new Error('Server 返回的资源链接无效。')
  try {
    const url = new URL(value)
    if ((url.protocol === 'http:' || url.protocol === 'https:') && url.hostname && !url.username && !url.password)
      return value
    if ((url.protocol === 'magnet:' && url.searchParams.has('xt')) || url.protocol === 'ed2k:')
      return value
  }
  catch { /* Never pass an unsafe scheme to the operating system. */ }
  throw new Error('Server 返回的资源链接无效。')
}

function hasDiscoveryControl(value: string, rejectSpace = false) {
  return Array.from(value).some((character) => {
    const point = character.codePointAt(0) ?? 0
    return point <= (rejectSpace ? 32 : 31) || point === 127
  })
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
    catalogTarget: parseAcquisitionCatalogTarget(data.catalog_target),
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
    siteDetail: parseSiteDetail(value.site_detail),
    phase: text(value.phase) ? parseSearchPhase(value.phase) : undefined,
    eventSequence: nonnegativeInteger(value.event_sequence),
  }
}

function parseSiteDetail(value: unknown): ServerSearchSiteDetail | undefined {
  const item = record(value)
  const siteId = nonnegativeInteger(item.site_id)
  const siteName = text(item.site_name)?.slice(0, 256)
  if (!siteId || !siteName)
    return undefined
  return {
    siteId,
    siteName,
    phase: parseSearchPhase(item.phase),
    queryName: text(item.query_name)?.slice(0, 512),
    queryIndex: nonnegativeInteger(item.query_index),
    queriesCompleted: nonnegativeInteger(item.queries_completed),
    queriesFailed: nonnegativeInteger(item.queries_failed),
    queriesTotal: nonnegativeInteger(item.queries_total),
    resultCount: nonnegativeInteger(item.result_count),
    elapsedMs: nonnegativeInteger(item.elapsed_ms),
    revision: nonnegativeInteger(item.revision),
  }
}

function parseSearchPhase(value: unknown): ServerSearchPhase {
  const phases: ServerSearchPhase[] = ['preparing_media', 'queued', 'preparing_site', 'restoring_session', 'checking_session', 'waiting_rate_limit', 'querying', 'processing_results', 'finished']
  return typeof value === 'string' && phases.includes(value as ServerSearchPhase) ? value as ServerSearchPhase : 'unknown'
}

function optionalBoolean(value: unknown): boolean | undefined {
  return typeof value === 'boolean' ? value : undefined
}

function nonnegativeInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined
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

function parseAcquisitionCatalogTarget(value: unknown): ServerAcquisitionStatus['catalogTarget'] {
  if (value == null)
    return undefined
  const target = record(value)
  const libraryId = number(target.library_id)
  const workToken = text(target.work_token)
  if (!Number.isSafeInteger(libraryId) || !libraryId || libraryId <= 0 || !workToken || !parseServerWorkItemIdentity(`work|${libraryId}|${workToken}`))
    throw new Error('Server 返回的入库媒体目标无效。')
  const canonical = btoa(atob(workToken.replace(/-/g, '+').replace(/_/g, '/'))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  if (canonical !== workToken)
    throw new Error('Server 返回的入库媒体目标无效。')
  return { libraryId, workToken }
}
