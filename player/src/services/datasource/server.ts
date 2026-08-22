import type { ServerCredentialValue } from './credentialStore'
import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaIdentity, MediaItem, MediaLibrary, MediaSourceOption, MediaStreamRequest, PlaybackRequest } from './types'
import { invoke } from '@tauri-apps/api/core'
import { tmdbArtworkUrl } from '@/services/scraper/tmdb'
import { createCredentialRef, readServerCredential, saveServerCredential } from './credentialStore'
import { redactSensitiveText } from './errors'
import { playbackTargetsForItem } from './identityMerge'

interface ServerConfigExtra {
  credentialRef?: string
  deviceId: string
  credentialVersion?: number
  libraries?: Array<{ id: string, name: string, type: MediaLibrary['type'] }>
}

interface ServerNativeRequest {
  baseUrl: string
  method: 'GET' | 'POST' | 'DELETE'
  path: string
  accessToken?: string
  body?: unknown
}

interface ServerNativeResponse {
  status: number
  body: unknown
}

interface ServerLibraryRecord {
  id: number
  name: string
  storage_type: string
  entry_count: number
}

interface ServerIdentityRecord {
  scheme: 'tmdb' | 'server'
  media_type: 'movie' | 'series'
  value: string
}

interface ServerItemRecord {
  id: string
  library_id: number
  title: string
  original_title?: string
  kind: 'movie' | 'series'
  release_year?: number
  overview?: string
  tagline?: string
  rating?: number
  runtime_minutes?: number
  genres?: string[]
  directors?: string[]
  writers?: string[]
  cast?: string[]
  tmdb_id?: number
  imdb_id?: string
  poster_path?: string
  backdrop_path?: string
  still_paths?: string[]
  work_identity: ServerIdentityRecord
  file_count: number
  season_count: number
  episode_count: number
  modified_at: string
  match_status: string
}

interface ServerVersionRecord {
  id: number
  title: string
  season?: number
  episode?: number
  size: number
  modified_at: string
  playable: boolean
  stream_path?: string
  delivery_kind?: 'server_stream' | 'server_redirect'
  exact_identity: string
}

interface ServerDetailRecord {
  item: ServerItemRecord
  versions: ServerVersionRecord[]
}

export interface ServerLoginInput {
  id: string
  url: string
  displayName?: string
  username: string
  password: string
  order?: number
  deviceId?: string
  deviceName?: string
  retainTokenOnValidationFailure?: boolean
}

export interface ServerLoginResult {
  config: DataSourceConfig
  libraries: MediaLibrary[]
}

export interface ServerBridge {
  request: (request: ServerNativeRequest) => Promise<ServerNativeResponse>
}

const defaultServerBridge: ServerBridge = {
  request: request => invoke<ServerNativeResponse>('server_request_json', { request }),
}

const SERVER_PAGE_SIZE = 100
const SERVER_MAX_PAGES = 100
const SERVER_MAX_ITEMS = SERVER_PAGE_SIZE * SERVER_MAX_PAGES

export class ServerDataSource implements DataSource {
  readonly type = 'server' as const
  private config: DataSourceConfig | null = null
  private baseUrl = ''
  private credentialRef = ''
  private credential: ServerCredentialValue | null = null
  private connected = false
  private readonly bridge: ServerBridge
  private readonly readCredential: (ref: string) => Promise<ServerCredentialValue | null>

  constructor(options: { bridge?: ServerBridge, readCredential?: (ref: string) => Promise<ServerCredentialValue | null> } = {}) {
    this.bridge = options.bridge ?? defaultServerBridge
    this.readCredential = options.readCredential ?? readServerCredential
  }

  get id(): string { return this.config?.id ?? '' }
  get name(): string { return this.config?.displayName ?? this.config?.name ?? 'OhMyCine Server' }
  get isConnected(): boolean { return this.connected }

  async init(config: DataSourceConfig): Promise<void> {
    this.config = sanitizeServerConfig(config)
    this.baseUrl = normalizeServerBaseUrl(config.url)
    this.credentialRef = readServerExtra(config).credentialRef ?? ''
    this.credential = this.credentialRef ? await this.readCredential(this.credentialRef) : null
    this.connected = Boolean(this.baseUrl && this.credential)
  }

  async test(): Promise<boolean> {
    await this.request('/api/v1/player/bootstrap')
    this.connected = true
    return true
  }

  destroy(): void {
    this.credential = null
    this.connected = false
  }

  clearCache(): void {}

  async listLibraries(): Promise<MediaLibrary[]> {
    const data = recordData(await this.request('/api/v1/player/media-libraries'))
    const list = Array.isArray(data.list) ? data.list : []
    return list.map(parseLibrary).filter((item): item is MediaLibrary => item != null).map(item => ({ ...item, sourceId: this.id }))
  }

  async list(path = ''): Promise<MediaItem[]> {
    const value = path.trim()
    if (/^\d+$/.test(value))
      return (await this.catalog(value)).map(item => this.mapItem(item))
    const work = parseWorkItemID(value)
    if (work) {
      const detail = await this.detail(work.libraryId, work.workId)
      if (work.season != null)
        return detail.versions.filter(version => version.playable && (version.season ?? 0) === work.season).map(version => this.mapVersion(detail.item, version, work))
      if (detail.item.kind === 'series') {
        const seasons = [...new Set(detail.versions.filter(version => version.playable).map(version => version.season ?? 0))].sort((a, b) => a - b)
        return seasons.map(season => ({
          id: createSeasonItemID(work.libraryId, work.workId, season),
          sourceId: this.id,
          libraryId: work.libraryId,
          name: season === 0 ? '特别篇' : `第 ${season} 季`,
          type: 'season' as const,
          posterUrl: artwork(detail.item.poster_path, 'w500'),
          backdropUrl: artwork(detail.item.backdrop_path, 'w1280'),
          path: '',
          seasonNumber: season,
          seriesName: detail.item.title,
          workIdentity: mapIdentity(detail.item.work_identity),
        }))
      }
      return detail.versions.filter(version => version.playable).map(version => this.mapVersion(detail.item, version, work))
    }
    return []
  }

  async getHomeSections(): Promise<HomeSection[]> {
    const libraries = await this.listLibraries()
    const pages = await Promise.all(libraries.slice(0, 12).map(library => this.catalog(library.id).catch(() => [])))
    const items = pages.flat().map(item => this.mapItem(item)).sort((a, b) => Date.parse(b.modified ?? '') - Date.parse(a.modified ?? ''))
    if (items.length === 0)
      return []
    return [
      { id: `${this.id}:hero`, sourceId: this.id, title: 'Server 精选', type: 'hero', items: items.filter(item => item.backdropUrl).slice(0, 12) },
      { id: `${this.id}:recent`, sourceId: this.id, title: 'Server 最近入库', type: 'recentlyAdded', items: items.slice(0, 24) },
    ].filter(section => section.items.length > 0) as HomeSection[]
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const query = keyword.trim()
    if (!query)
      return []
    const data = recordData(await this.request(`/api/v1/player/search?query=${encodeURIComponent(query)}&page=1&page_size=50`))
    return arrayRecords(data.list).map(parseItem).filter((item): item is ServerItemRecord => item != null).map(item => this.mapItem(item))
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const work = parseWorkItemID(id)
    if (!work)
      throw new Error('Server 媒体标识无效。')
    const detail = await this.detail(work.libraryId, work.workId)
    const item = this.mapItem(detail.item)
    const versions = detail.versions.filter(version => version.playable && (work.season == null || (version.season ?? 0) === work.season))
    const ownSources = versions.map(version => ({
      id: String(version.id),
      name: version.title,
      size: version.size,
      isRemote: true,
      sourceLabel: this.name,
      deliveryKind: version.delivery_kind,
      sourceId: this.id,
      itemId: createEntryItemID(work.libraryId, work.workId, version.id),
      exactIdentity: version.exact_identity,
    } satisfies MediaSourceOption))
    const alternateSources = playbackTargetsForItem(item)
      .filter(target => target.sourceId !== this.id)
      .map((target, index) => ({
        id: `alternate-${index}`,
        name: target.label,
        isRemote: true,
        sourceLabel: target.label,
        sourceId: target.sourceId,
        itemId: target.itemId,
        providerMediaSourceId: target.mediaSourceId,
        exactIdentity: target.exactIdentity,
      } satisfies MediaSourceOption))
    const stillPaths = detail.item.still_paths?.length
      ? detail.item.still_paths
      : detail.item.backdrop_path
        ? [detail.item.backdrop_path]
        : []
    return {
      ...item,
      id,
      type: work.season == null ? item.type : 'season',
      seasonNumber: work.season,
      genres: detail.item.genres,
      directors: detail.item.directors,
      writers: detail.item.writers,
      cast: detail.item.cast,
      imdbId: detail.item.imdb_id,
      tmdbId: detail.item.tmdb_id,
      stills: stillPaths.map(path => artwork(path, 'w1280')).filter((value): value is string => Boolean(value)),
      mediaSources: [...ownSources, ...alternateSources],
      children: versions.map(version => this.mapVersion(detail.item, version, work)),
    }
  }

  async getStreamURL(id: string): Promise<string> {
    return (await this.getStreamRequest({ itemId: id })).url
  }

  async getStreamRequest(request: PlaybackRequest): Promise<MediaStreamRequest> {
    const work = parseWorkItemID(request.itemId)
    let entryID: number | null = null
    if (work) {
      const detail = await this.detail(work.libraryId, work.workId)
      const requestedEntryID = parseEntryID(request.itemId) ?? parseNumericID(request.mediaSourceId)
      const defaultVersion = requestedEntryID
        ? detail.versions.find(version => version.id === requestedEntryID && version.playable)
        : detail.item.kind === 'movie'
          ? detail.versions.find(version => version.playable && (work.season == null || (version.season ?? 0) === work.season))
          : undefined
      entryID = defaultVersion?.id ?? null
    }
    else {
      entryID = parseNumericID(request.mediaSourceId)
    }
    if (!entryID)
      throw new Error('请选择可播放的 Server 媒体版本。')
    const credential = await this.ensureCredential()
    return {
      url: `${this.baseUrl}/api/v1/player/media-entries/${entryID}/stream`,
      headers: { Authorization: `Bearer ${credential.accessToken}` },
      mediaSourceId: String(entryID),
    }
  }

  exportConfig(): DataSourceConfig {
    return sanitizeServerConfig(this.config)
  }

  private async catalog(libraryID: string): Promise<ServerItemRecord[]> {
    return this.pagedItems(`/api/v1/player/media-libraries/${encodeURIComponent(libraryID)}/catalog`)
  }

  private async pagedItems(path: string): Promise<ServerItemRecord[]> {
    const items = new Map<string, ServerItemRecord>()
    for (let page = 1; page <= SERVER_MAX_PAGES; page++) {
      const separator = path.includes('?') ? '&' : '?'
      const data = recordData(await this.request(`${path}${separator}page=${page}&page_size=${SERVER_PAGE_SIZE}`))
      const total = numberValue(data.total)
      if (total != null && total > SERVER_MAX_ITEMS)
        throw new Error(`Server 媒体数量超过单次浏览安全上限（${SERVER_MAX_ITEMS} 项）。`)

      const pageItems = arrayRecords(data.list).map(parseItem).filter((item): item is ServerItemRecord => item != null)
      if (pageItems.length === 0)
        return [...items.values()]

      const previousSize = items.size
      pageItems.forEach(item => items.set(item.id, item))
      if (items.size === previousSize)
        throw new Error('Server 媒体分页未向前推进，已停止浏览以避免无限请求。')
      if (items.size > SERVER_MAX_ITEMS)
        throw new Error(`Server 媒体数量超过单次浏览安全上限（${SERVER_MAX_ITEMS} 项）。`)
      if ((total != null && items.size >= total) || pageItems.length < SERVER_PAGE_SIZE)
        return [...items.values()]
    }
    throw new Error(`Server 媒体分页超过安全上限（${SERVER_MAX_PAGES} 页）。`)
  }

  private async detail(libraryID: string, workID: string): Promise<ServerDetailRecord> {
    const data = recordData(await this.request(`/api/v1/player/media-libraries/${encodeURIComponent(libraryID)}/catalog/${encodeURIComponent(workID)}`))
    const item = parseItem(data.item)
    const versions = arrayRecords(data.versions).map(parseVersion).filter((version): version is ServerVersionRecord => version != null)
    if (!item)
      throw new Error('Server 返回的媒体详情无效。')
    return { item, versions }
  }

  private mapItem(item: ServerItemRecord): MediaItem {
    return {
      id: createWorkItemID(String(item.library_id), item.id),
      sourceId: this.id,
      originType: 'server',
      libraryId: String(item.library_id),
      name: item.title,
      originalTitle: item.original_title,
      type: item.kind,
      posterUrl: artwork(item.poster_path, 'w500'),
      backdropUrl: artwork(item.backdrop_path, 'w1280'),
      year: item.release_year,
      rating: item.rating,
      overview: item.overview,
      tagline: item.tagline,
      duration: item.runtime_minutes ? item.runtime_minutes * 60 : undefined,
      modified: item.modified_at,
      path: '',
      workIdentity: mapIdentity(item.work_identity),
      playbackTargets: [{ sourceId: this.id, itemId: createWorkItemID(String(item.library_id), item.id), label: this.name }],
    }
  }

  private mapVersion(item: ServerItemRecord, version: ServerVersionRecord, work: WorkItemID): MediaItem {
    const id = createEntryItemID(work.libraryId, work.workId, version.id)
    return {
      id,
      sourceId: this.id,
      originType: 'server',
      libraryId: work.libraryId,
      name: version.title,
      originalTitle: item.original_title,
      type: item.kind === 'series' ? 'episode' : 'movie',
      posterUrl: artwork(item.poster_path, 'w500'),
      backdropUrl: artwork(item.backdrop_path, 'w1280'),
      year: item.release_year,
      rating: item.rating,
      overview: item.overview,
      tagline: item.tagline,
      duration: item.runtime_minutes ? item.runtime_minutes * 60 : undefined,
      size: version.size,
      modified: version.modified_at,
      path: '',
      seasonNumber: version.season,
      episodeNumber: version.episode,
      seriesName: item.kind === 'series' ? item.title : undefined,
      workIdentity: mapIdentity(item.work_identity),
      exactIdentity: version.exact_identity,
      playbackTargets: [{ sourceId: this.id, itemId: id, mediaSourceId: String(version.id), label: this.name, exactIdentity: version.exact_identity }],
    }
  }

  private async request(path: string, method: ServerNativeRequest['method'] = 'GET', body?: unknown): Promise<unknown> {
    const credential = path === '/api/v1/player/auth/login' ? null : await this.ensureCredential()
    try {
      const response = await this.bridge.request({ baseUrl: this.baseUrl, method, path, accessToken: credential?.accessToken, body })
      const envelope = isRecord(response.body) ? response.body : {}
      if (response.status < 200 || response.status >= 300 || envelope.code !== 0) {
        const message = typeof envelope.message === 'string' ? envelope.message : `Server 请求失败（HTTP ${response.status}）`
        throw new Error(message)
      }
      return envelope.data
    }
    catch (error) {
      throw new Error(redactSensitiveText(error))
    }
  }

  private async ensureCredential(): Promise<ServerCredentialValue> {
    if (this.credential)
      return this.credential
    if (this.credentialRef)
      this.credential = await this.readCredential(this.credentialRef)
    if (!this.credential)
      throw new Error('OhMyCine Server 登录凭据不存在，请重新连接。')
    return this.credential
  }
}

export async function loginServerAndCreateConfig(
  input: ServerLoginInput,
  bridge: ServerBridge = defaultServerBridge,
  writeCredential: (ref: string, value: ServerCredentialValue) => Promise<void> = saveServerCredential,
): Promise<ServerLoginResult> {
  const baseUrl = normalizeServerBaseUrl(input.url)
  const username = input.username.trim()
  if (!username || !input.password)
    throw new Error('请输入 Server 用户名和密码。')
  const deviceId = input.deviceId?.trim() || crypto.randomUUID()
  const deviceName = input.deviceName?.trim() || defaultDeviceName()
  let response: ServerNativeResponse
  try {
    response = await bridge.request({
      baseUrl,
      method: 'POST',
      path: '/api/v1/player/auth/login',
      body: { username, password: input.password, device_id: deviceId, device_name: deviceName },
    })
  }
  catch (error) {
    throw new Error(redactSensitiveText(error))
  }
  const envelope = isRecord(response.body) ? response.body : {}
  const data = isRecord(envelope.data) ? envelope.data : {}
  const accessToken = typeof data.access_token === 'string' ? data.access_token.trim() : ''
  if (response.status !== 200 || envelope.code !== 0 || !accessToken.startsWith('omc_player_'))
    throw new Error(typeof envelope.message === 'string' ? envelope.message : 'Server 登录失败。')

  const credentialRef = createCredentialRef(input.id, 'server')
  const displayName = input.displayName?.trim() || 'OhMyCine Server'
  const config: DataSourceConfig = {
    id: input.id,
    type: 'server',
    name: displayName,
    displayName,
    order: input.order ?? 0,
    url: baseUrl,
    enabled: true,
    extra: { credentialRef, deviceId, credentialVersion: Date.now() },
  }
  const source = new ServerDataSource({ bridge, readCredential: async () => ({ accessToken }) })
  try {
    let libraries: MediaLibrary[]
    try {
      await source.init(config)
      await source.test()
      libraries = await source.listLibraries()
    }
    catch (error) {
      if (input.retainTokenOnValidationFailure)
        await persistServerCredentialOrRevoke(credentialRef, accessToken, baseUrl, bridge, writeCredential)
      else
        await revokeServerTokenBestEffort(baseUrl, accessToken, bridge)
      throw new Error(redactSensitiveText(error))
    }

    await persistServerCredentialOrRevoke(credentialRef, accessToken, baseUrl, bridge, writeCredential)
    return {
      config: { ...config, extra: { ...config.extra, libraries: libraries.map(library => ({ id: library.id, name: library.name, type: library.type })) } },
      libraries,
    }
  }
  finally {
    source.destroy()
  }
}

export async function logoutServerBestEffort(
  config: DataSourceConfig,
  bridge: ServerBridge = defaultServerBridge,
  readCredential: (ref: string) => Promise<ServerCredentialValue | null> = readServerCredential,
): Promise<void> {
  try {
    const baseUrl = normalizeServerBaseUrl(config.url)
    const credentialRef = readServerExtra(config).credentialRef ?? ''
    const credential = credentialRef ? await readCredential(credentialRef) : null
    if (credential)
      await revokeServerTokenBestEffort(baseUrl, credential.accessToken, bridge)
  }
  catch {
    // Remote revocation is best effort. Local data-source removal must always continue.
  }
}

async function revokeServerTokenBestEffort(baseUrl: string, accessToken: string, bridge: ServerBridge): Promise<void> {
  try {
    await bridge.request({
      baseUrl,
      method: 'POST',
      path: '/api/v1/player/auth/logout',
      accessToken,
    })
  }
  catch {
    // Never expose or persist the token through a cleanup error.
  }
}

async function persistServerCredentialOrRevoke(
  credentialRef: string,
  accessToken: string,
  baseUrl: string,
  bridge: ServerBridge,
  writeCredential: (ref: string, value: ServerCredentialValue) => Promise<void>,
): Promise<void> {
  try {
    await writeCredential(credentialRef, { accessToken })
  }
  catch (error) {
    await revokeServerTokenBestEffort(baseUrl, accessToken, bridge)
    throw new Error(`无法安全保存新的 Server 登录凭据，已撤销本次登录：${redactSensitiveText(error)}`)
  }
}

function parseLibrary(value: unknown): MediaLibrary | null {
  if (!isRecord(value) || typeof value.id !== 'number' || typeof value.name !== 'string')
    return null
  const record = value as unknown as ServerLibraryRecord
  return { id: String(record.id), sourceId: '', name: record.name, type: 'mixed', itemCount: numberValue(record.entry_count) }
}

function parseItem(value: unknown): ServerItemRecord | null {
  if (!isRecord(value) || typeof value.id !== 'string' || typeof value.library_id !== 'number' || typeof value.title !== 'string' || (value.kind !== 'movie' && value.kind !== 'series') || !isRecord(value.work_identity))
    return null
  const identity = value.work_identity
  if ((identity.scheme !== 'tmdb' && identity.scheme !== 'server') || (identity.media_type !== 'movie' && identity.media_type !== 'series') || typeof identity.value !== 'string')
    return null
  return {
    id: value.id,
    library_id: value.library_id,
    title: value.title,
    original_title: optionalString(value.original_title),
    kind: value.kind,
    release_year: numberValue(value.release_year),
    overview: optionalString(value.overview),
    tagline: optionalString(value.tagline),
    rating: boundedNumber(value.rating, 0, 10),
    runtime_minutes: boundedNumber(value.runtime_minutes, 1, 24 * 60),
    genres: stringList(value.genres, 100),
    directors: stringList(value.directors, 100),
    writers: stringList(value.writers, 100),
    cast: stringList(value.cast, 200),
    tmdb_id: boundedNumber(value.tmdb_id, 1, Number.MAX_SAFE_INTEGER),
    imdb_id: optionalIMDbID(value.imdb_id),
    poster_path: optionalImagePath(value.poster_path),
    backdrop_path: optionalImagePath(value.backdrop_path),
    still_paths: imagePathList(value.still_paths, 8),
    work_identity: {
      scheme: identity.scheme,
      media_type: identity.media_type,
      value: identity.value,
    },
    file_count: numberValue(value.file_count) ?? 0,
    season_count: numberValue(value.season_count) ?? 0,
    episode_count: numberValue(value.episode_count) ?? 0,
    modified_at: optionalString(value.modified_at) ?? '',
    match_status: optionalString(value.match_status) ?? '',
  }
}

function parseVersion(value: unknown): ServerVersionRecord | null {
  if (!isRecord(value) || typeof value.id !== 'number' || typeof value.title !== 'string' || typeof value.playable !== 'boolean' || typeof value.exact_identity !== 'string')
    return null
  const deliveryKind = value.delivery_kind === 'server_stream' || value.delivery_kind === 'server_redirect'
    ? value.delivery_kind
    : undefined
  return {
    id: value.id,
    title: value.title,
    season: numberValue(value.season),
    episode: numberValue(value.episode),
    size: numberValue(value.size) ?? 0,
    modified_at: optionalString(value.modified_at) ?? '',
    playable: value.playable,
    stream_path: optionalString(value.stream_path),
    delivery_kind: deliveryKind,
    exact_identity: value.exact_identity,
  }
}

function recordData(value: unknown): Record<string, unknown> {
  if (!isRecord(value))
    throw new Error('Server 返回的数据格式无效。')
  return value
}

function arrayRecords(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}
function numberValue(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}
function artwork(path: string | undefined, size: 'w500' | 'w1280'): string | undefined {
  const safePath = optionalImagePath(path)
  return safePath ? tmdbArtworkUrl(safePath, size) : undefined
}

function optionalString(value: unknown): string | undefined {
  if (typeof value !== 'string')
    return undefined
  const trimmed = value.trim()
  return trimmed || undefined
}

function boundedNumber(value: unknown, minimum: number, maximum: number): number | undefined {
  const parsed = numberValue(value)
  return parsed != null && parsed >= minimum && parsed <= maximum ? parsed : undefined
}

function stringList(value: unknown, limit: number): string[] | undefined {
  if (!Array.isArray(value))
    return undefined
  const result: string[] = []
  const seen = new Set<string>()
  for (const entry of value) {
    const text = optionalString(entry)
    const key = text?.toLocaleLowerCase()
    if (!text || !key || seen.has(key))
      continue
    seen.add(key)
    result.push(text)
    if (result.length === limit)
      break
  }
  return result.length ? result : undefined
}

function optionalIMDbID(value: unknown): string | undefined {
  const candidate = optionalString(value)
  return candidate && /^tt\d{1,30}$/.test(candidate) ? candidate : undefined
}

function optionalImagePath(value: unknown): string | undefined {
  const candidate = optionalString(value)
  return candidate && candidate.startsWith('/') && candidate.length <= 512 && !/[?#\\\r\n]/.test(candidate) && !candidate.includes('..') ? candidate : undefined
}

function imagePathList(value: unknown, limit: number): string[] | undefined {
  if (!Array.isArray(value))
    return undefined
  const result = [...new Set(value.map(optionalImagePath).filter((entry): entry is string => Boolean(entry)))].slice(0, limit)
  return result.length ? result : undefined
}
function mapIdentity(value: ServerIdentityRecord): MediaIdentity {
  return { scheme: value.scheme, mediaType: value.media_type, value: value.value }
}
function createWorkItemID(libraryId: string, workId: string): string {
  return `work|${libraryId}|${workId}`
}
function createSeasonItemID(libraryId: string, workId: string, season: number): string {
  return `season|${libraryId}|${workId}|${season}`
}
function createEntryItemID(libraryId: string, workId: string, entryID: number): string {
  return `entry|${libraryId}|${workId}|${entryID}`
}

interface WorkItemID { libraryId: string, workId: string, season?: number }
function parseWorkItemID(value: string): WorkItemID | null {
  const parts = value.split('|')
  if ((parts[0] !== 'work' && parts[0] !== 'season' && parts[0] !== 'entry') || !/^\d+$/.test(parts[1] ?? '') || !parts[2])
    return null
  const season = parts[0] === 'season' ? Number.parseInt(parts[3] ?? '', 10) : undefined
  return { libraryId: parts[1], workId: parts[2], season: Number.isFinite(season) ? season : undefined }
}
function parseEntryID(value: string | undefined): number | null {
  const parts = value?.split('|') ?? []
  return parts[0] === 'entry' ? parseNumericID(parts[3]) : null
}
function parseNumericID(value: string | undefined): number | null {
  if (!value || !/^\d+$/.test(value))
    return null
  const parsed = Number.parseInt(value, 10)
  return parsed > 0 ? parsed : null
}

function readServerExtra(config: DataSourceConfig): ServerConfigExtra {
  const extra = isRecord(config.extra) ? config.extra : {}
  return {
    credentialRef: typeof extra.credentialRef === 'string' ? extra.credentialRef : undefined,
    deviceId: typeof extra.deviceId === 'string' ? extra.deviceId.trim() : '',
    credentialVersion: typeof extra.credentialVersion === 'number' && Number.isFinite(extra.credentialVersion) ? extra.credentialVersion : undefined,
    libraries: Array.isArray(extra.libraries) ? extra.libraries as ServerConfigExtra['libraries'] : undefined,
  }
}

function sanitizeServerConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    return { id: '', type: 'server', name: 'OhMyCine Server', order: 0, url: '', enabled: false, extra: {} }
  const extra = readServerExtra(config)
  return { ...config, type: 'server', url: normalizeServerBaseUrl(config.url), extra: { credentialRef: extra.credentialRef, deviceId: extra.deviceId, credentialVersion: extra.credentialVersion, libraries: extra.libraries } }
}

export function normalizeServerBaseUrl(value: string): string {
  const raw = value.trim().replace(/\/+$/, '')
  let url: URL
  try {
    url = new URL(raw)
  }
  catch {
    throw new Error('请输入有效的 OhMyCine Server 地址。')
  }
  if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.pathname !== '/' || url.search || url.hash)
    throw new Error('OhMyCine Server 地址只能包含协议、主机和端口。')
  return url.origin
}

function defaultDeviceName(): string {
  const platform = navigator.platform?.trim() || '设备'
  return `OhMyCine Player · ${platform}`.slice(0, 128)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null && !Array.isArray(value)
}
