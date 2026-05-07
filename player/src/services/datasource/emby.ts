import type {
  AudioTrack,
  DataSource,
  DataSourceConfig,
  HomeSection,
  MediaDetail,
  MediaItem,
  MediaLibrary,
  SubtitleTrack,
} from './types'
import { ofetch } from 'ofetch'
import { createCredentialRef, readSessionCredential, saveSessionCredential } from './credentialStore'
import { redactSensitiveText } from './errors'

const ITEM_FIELDS = [
  'Overview',
  'Genres',
  'People',
  'ProviderIds',
  'MediaSources',
  'MediaStreams',
  'Path',
  'DateCreated',
  'SortName',
  'RunTimeTicks',
  'ImageTags',
  'BackdropImageTags',
].join(',')

interface EmbyConfigExtra {
  readonly userId?: string
  readonly credentialRef?: string
  readonly deviceId?: string
}

interface EmbyItemsResponse {
  readonly Items?: unknown[]
  readonly TotalRecordCount?: number
}

interface EmbyLibraryResponse {
  readonly Items?: unknown[]
}

interface EmbyAuthResult {
  readonly accessToken: string
  readonly userId: string
  readonly serverName?: string
}

interface EmbyItemRecord {
  readonly Id: string
  readonly Name: string
  readonly Type?: string
  readonly CollectionType?: string
  readonly ProductionYear?: number
  readonly CommunityRating?: number
  readonly CriticRating?: number
  readonly Overview?: string
  readonly Taglines?: string[]
  readonly RunTimeTicks?: number
  readonly Size?: number
  readonly DateCreated?: string
  readonly DateLastMediaAdded?: string
  readonly Path?: string
  readonly ImageTags?: Record<string, string>
  readonly BackdropImageTags?: string[]
  readonly Genres?: string[]
  readonly People?: EmbyPersonRecord[]
  readonly ProviderIds?: Record<string, string>
  readonly MediaStreams?: EmbyMediaStreamRecord[]
  readonly MediaSources?: EmbyMediaSourceRecord[]
  readonly UserData?: {
    readonly PlayedPercentage?: number
    readonly PlaybackPositionTicks?: number
  }
}

interface EmbyPersonRecord {
  readonly Name?: string
  readonly Type?: string
}

interface EmbyMediaStreamRecord {
  readonly Index?: number
  readonly Type?: string
  readonly Language?: string
  readonly DisplayLanguage?: string
  readonly Title?: string
  readonly Codec?: string
  readonly Channels?: number
  readonly IsDefault?: boolean
  readonly Width?: number
  readonly Height?: number
}

interface EmbyMediaSourceRecord {
  readonly Id?: string
  readonly Size?: number
}

export interface EmbyLoginConfigInput {
  readonly id: string
  readonly url: string
  readonly displayName?: string
  readonly username: string
  readonly password: string
  readonly order?: number
}

export interface EmbyLoginConfigResult {
  readonly config: DataSourceConfig
  readonly libraries: MediaLibrary[]
}

export class EmbyDataSource implements DataSource {
  private config: DataSourceConfig | null = null
  private baseUrl = ''
  private token = ''
  private userId = ''
  private deviceId = ''
  private connected = false

  readonly type = 'emby' as const

  get id(): string {
    return this.config?.id ?? ''
  }

  get name(): string {
    return this.config?.displayName ?? this.config?.name ?? 'Emby'
  }

  get isConnected(): boolean {
    return this.connected
  }

  async init(config: DataSourceConfig): Promise<void> {
    this.config = sanitizeExportConfig(config)
    this.baseUrl = normalizeBaseUrl(config.url)
    const extra = readEmbyExtra(config)
    this.userId = extra.userId ?? ''
    this.deviceId = extra.deviceId ?? createDeviceId(config.id)
    this.token = resolveToken(config, extra)
    this.connected = Boolean(this.baseUrl && this.token && this.userId)
  }

  async test(): Promise<boolean> {
    this.ensureConfigured()
    await this.request('/System/Info')
    this.connected = true
    return true
  }

  async authenticate(username: string, password: string): Promise<EmbyAuthResult> {
    if (!this.config || !this.baseUrl)
      throw new Error('Emby source is not configured.')

    const trimmedUsername = username.trim()
    if (!trimmedUsername || !password)
      throw new Error('请输入 Emby 账号和密码。')

    try {
      const response = await ofetch<unknown>(`${this.baseUrl}/Users/AuthenticateByName`, {
        method: 'POST',
        body: {
          Username: trimmedUsername,
          Pw: password,
        },
        headers: {
          'X-Emby-Authorization': authorizationHeader(this.deviceId),
        },
      })
      const auth = parseAuthResponse(response)
      this.token = auth.accessToken
      this.userId = auth.userId
      this.connected = true
      return auth
    }
    catch (error) {
      throw new Error(redactSensitiveText(error))
    }
  }

  destroy(): void {
    this.connected = false
  }

  async list(path?: string): Promise<MediaItem[]> {
    this.ensureConfigured()

    if (!path) {
      const libraries = await this.listLibraries()
      return libraries.map(library => ({
        id: library.id,
        sourceId: library.sourceId,
        libraryId: library.id,
        name: library.name,
        type: 'folder',
        posterUrl: library.posterUrl,
        backdropUrl: library.backdropUrl,
        path: library.id,
      }))
    }

    const response = await this.request('/Users/{UserId}/Items', {
      ParentId: path,
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series,Episode,Folder',
      Fields: ITEM_FIELDS,
      SortBy: 'SortName',
      SortOrder: 'Ascending',
      Limit: '100',
    })

    return parseItemsResponse(response).map(item => this.mapItem(item, path))
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    this.ensureConfigured()
    const response = await this.request('/Users/{UserId}/Views')
    return parseLibraryResponse(response).map(item => this.mapLibrary(item))
  }

  async getHomeSections(): Promise<HomeSection[]> {
    const [continueWatching, recentlyAdded, featured] = await Promise.all([
      this.getContinueWatching(),
      this.getRecentlyAdded(),
      this.getFeaturedItems(),
    ])

    return [
      {
        id: `hero-${this.id}`,
        sourceId: this.id,
        title: `${this.name} 精选`,
        type: 'hero',
        items: featured,
      },
      {
        id: `continue-${this.id}`,
        sourceId: this.id,
        title: `${this.name} 继续观看`,
        type: 'continueWatching',
        items: continueWatching,
      },
      {
        id: `recent-${this.id}`,
        sourceId: this.id,
        title: `${this.name} 最新入库`,
        type: 'recentlyAdded',
        items: recentlyAdded,
      },
    ]
  }

  async getFeaturedItems(): Promise<MediaItem[]> {
    const response = await this.request('/Users/{UserId}/Items', {
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series,Episode',
      Fields: ITEM_FIELDS,
      SortBy: 'DateCreated',
      SortOrder: 'Descending',
      Limit: '8',
    })
    return parseItemsResponse(response).map(item => this.mapItem(item))
  }

  async getContinueWatching(): Promise<MediaItem[]> {
    const response = await this.request('/Users/{UserId}/Items/Resume', {
      MediaTypes: 'Video',
      Fields: ITEM_FIELDS,
      Limit: '12',
    })
    return parseItemsResponse(response).map(item => this.mapItem(item))
  }

  async getRecentlyAdded(): Promise<MediaItem[]> {
    const response = await this.request('/Users/{UserId}/Items', {
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series,Episode',
      Fields: ITEM_FIELDS,
      SortBy: 'DateCreated',
      SortOrder: 'Descending',
      Limit: '12',
    })
    return parseItemsResponse(response).map(item => this.mapItem(item))
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const trimmed = keyword.trim()
    if (!trimmed)
      return []

    const response = await this.request('/Users/{UserId}/Items', {
      SearchTerm: trimmed,
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series,Episode',
      Fields: ITEM_FIELDS,
      Limit: '50',
    })

    return parseItemsResponse(response).map(item => this.mapItem(item))
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const response = await this.request(`/Users/{UserId}/Items/${encodeURIComponent(id)}`, {
      Fields: ITEM_FIELDS,
    })
    const item = parseItemRecord(response)
    const base = this.mapItem(item)
    const streams = item.MediaStreams ?? []
    const video = streams.find(stream => stream.Type === 'Video')
    const audio = streams.find(stream => stream.Type === 'Audio')

    return {
      ...base,
      genres: item.Genres ?? [],
      directors: namesByPersonType(item.People, 'Director'),
      cast: namesByPersonType(item.People, 'Actor'),
      imdbId: item.ProviderIds?.Imdb,
      tmdbId: parseTmdbId(item.ProviderIds?.Tmdb),
      resolution: video?.Width && video.Height ? `${video.Width}x${video.Height}` : undefined,
      codec: video?.Codec,
      audioCodec: audio?.Codec,
      subtitles: streams.filter(stream => stream.Type === 'Subtitle').map(mapSubtitleTrack),
      audioTracks: streams.filter(stream => stream.Type === 'Audio').map(mapAudioTrack),
    }
  }

  async getStreamURL(id: string): Promise<string> {
    this.ensureConfigured()
    const params = new URLSearchParams({
      Static: 'true',
      api_key: this.token,
    })
    return `${this.baseUrl}/Videos/${encodeURIComponent(id)}/stream?${params.toString()}`
  }

  exportConfig(): DataSourceConfig {
    this.ensureConfigured()
    return sanitizeExportConfig(this.config)
  }

  private async request(path: string, query: Record<string, string> = {}): Promise<unknown> {
    this.ensureConfigured()
    const resolvedPath = path.replace('{UserId}', encodeURIComponent(this.userId))
    const url = `${this.baseUrl}${resolvedPath}`

    try {
      return await ofetch<unknown>(url, {
        query,
        headers: {
          'X-Emby-Token': this.token,
          'X-Emby-Authorization': authorizationHeader(this.deviceId, this.token),
        },
      })
    }
    catch (error) {
      throw new Error(redactSensitiveText(error))
    }
  }

  private mapLibrary(item: EmbyItemRecord): MediaLibrary {
    return {
      id: item.Id,
      sourceId: this.id,
      name: item.Name,
      type: mapLibraryType(item.CollectionType),
      posterUrl: this.imageUrl(item.Id, 'Primary', item.ImageTags?.Primary),
      backdropUrl: this.imageUrl(item.Id, 'Backdrop/0', item.BackdropImageTags?.[0]),
    }
  }

  private mapItem(item: EmbyItemRecord, libraryId?: string): MediaItem {
    const sourceId = this.id
    const mediaSourceSize = item.MediaSources?.find(source => typeof source.Size === 'number')?.Size

    return {
      id: item.Id,
      sourceId,
      libraryId,
      name: item.Name,
      type: mapMediaType(item.Type),
      posterUrl: this.imageUrl(item.Id, 'Primary', item.ImageTags?.Primary),
      backdropUrl: this.imageUrl(item.Id, 'Backdrop/0', item.BackdropImageTags?.[0]),
      titleLogoUrl: this.imageUrl(item.Id, 'Logo', item.ImageTags?.Logo),
      year: item.ProductionYear,
      rating: item.CommunityRating ?? normalizeCriticRating(item.CriticRating),
      overview: item.Overview,
      tagline: item.Taglines?.[0],
      duration: ticksToSeconds(item.RunTimeTicks),
      size: item.Size ?? mediaSourceSize,
      modified: item.DateCreated ?? item.DateLastMediaAdded,
      path: item.Id,
    }
  }

  private imageUrl(itemId: string, type: string, tag?: string): string | undefined {
    if (!this.token)
      return undefined

    const params = new URLSearchParams({ api_key: this.token })
    if (tag)
      params.set('tag', tag)

    return `${this.baseUrl}/Items/${encodeURIComponent(itemId)}/Images/${type}?${params.toString()}`
  }

  private ensureConfigured(): void {
    if (!this.config || !this.baseUrl)
      throw new Error('Emby source is not configured.')
    if (!this.token)
      throw new Error('Emby 登录凭证缺失。请在设置的数据源管理中重新编辑并登录。')
    if (!this.userId)
      throw new Error('Emby 用户信息缺失。请在设置的数据源管理中重新编辑并登录。')
  }
}

export async function loginEmbyAndCreateConfig(input: EmbyLoginConfigInput): Promise<EmbyLoginConfigResult> {
  const credentialRef = createCredentialRef(input.id)
  const displayName = input.displayName?.trim() || 'Emby'
  const config: DataSourceConfig = {
    id: input.id,
    type: 'emby',
    name: displayName,
    displayName,
    order: input.order ?? 0,
    url: input.url.trim(),
    enabled: true,
    extra: {
      credentialRef,
      deviceId: createDeviceId(input.id),
    },
  }

  const source = new EmbyDataSource()
  await source.init(config)
  const auth = await source.authenticate(input.username, input.password)
  saveSessionCredential(credentialRef, auth.accessToken)

  const safeConfig: DataSourceConfig = {
    ...config,
    name: displayName === 'Emby' && auth.serverName ? auth.serverName : displayName,
    displayName: displayName === 'Emby' && auth.serverName ? auth.serverName : displayName,
    extra: {
      ...config.extra,
      userId: auth.userId,
      serverName: auth.serverName,
      libraries: [],
    },
  }

  await source.init(safeConfig)
  const libraries = await source.listLibraries()
  source.destroy()

  return {
    config: {
      ...safeConfig,
      extra: {
        ...safeConfig.extra,
        libraries: libraries.map(library => ({
          id: library.id,
          name: library.name,
          type: library.type,
          itemCount: library.itemCount,
        })),
      },
    },
    libraries,
  }
}

function normalizeBaseUrl(url: string): string {
  return url.trim().replace(/\/+$/, '')
}

function readEmbyExtra(config: DataSourceConfig): EmbyConfigExtra {
  const extra = config.extra ?? {}
  return {
    userId: typeof extra.userId === 'string' ? extra.userId : undefined,
    credentialRef: typeof extra.credentialRef === 'string' ? extra.credentialRef : undefined,
    deviceId: typeof extra.deviceId === 'string' ? extra.deviceId : undefined,
  }
}

function resolveToken(config: DataSourceConfig, extra: EmbyConfigExtra): string {
  if (extra.credentialRef) {
    const token = readSessionCredential(extra.credentialRef)
    if (token)
      return token
  }

  return typeof config.apiKey === 'string' ? config.apiKey : ''
}

function createDeviceId(sourceId: string): string {
  return `ohmycine-${sourceId}`
}

function authorizationHeader(deviceId: string, token?: string): string {
  const tokenSegment = token ? `, Token="${token}"` : ''
  return `MediaBrowser Client="OhMyCine Player", Device="Desktop", DeviceId="${deviceId}", Version="0.1.0"${tokenSegment}`
}

function parseItemsResponse(value: unknown): EmbyItemRecord[] {
  if (!isObject(value))
    return []
  const response = value as EmbyItemsResponse
  if (!Array.isArray(response.Items))
    return []
  return response.Items.filter(isEmbyItemRecord)
}

function parseLibraryResponse(value: unknown): EmbyItemRecord[] {
  if (!isObject(value))
    return []
  const response = value as EmbyLibraryResponse
  if (!Array.isArray(response.Items))
    return []
  return response.Items.filter(isEmbyItemRecord)
}

function parseItemRecord(value: unknown): EmbyItemRecord {
  if (!isEmbyItemRecord(value))
    throw new Error('Emby returned an invalid item response.')
  return value
}

function parseAuthResponse(value: unknown): EmbyAuthResult {
  if (!isObject(value))
    throw new Error('Emby returned an invalid authentication response.')

  const record = value as Record<string, unknown>
  const user = isObject(record.User) ? record.User : null
  const accessToken = record.AccessToken
  const userId = user?.Id
  const serverName = record.ServerName

  if (typeof accessToken !== 'string' || accessToken.length === 0 || typeof userId !== 'string' || userId.length === 0)
    throw new Error('Emby 登录响应缺少访问令牌或用户信息。')

  return {
    accessToken,
    userId,
    serverName: typeof serverName === 'string' ? serverName : undefined,
  }
}

function isEmbyItemRecord(value: unknown): value is EmbyItemRecord {
  if (!isObject(value))
    return false
  const record = value as Record<string, unknown>
  return typeof record.Id === 'string' && typeof record.Name === 'string'
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}

function mapMediaType(type: string | undefined): MediaItem['type'] {
  switch (type) {
    case 'Movie':
      return 'movie'
    case 'Series':
      return 'series'
    case 'Episode':
      return 'episode'
    case 'Folder':
    case 'CollectionFolder':
      return 'folder'
    default:
      return 'file'
  }
}

function mapLibraryType(type: string | undefined): MediaLibrary['type'] {
  switch (type) {
    case 'movies':
      return 'movies'
    case 'tvshows':
      return 'series'
    case 'music':
      return 'music'
    case 'boxsets':
      return 'mixed'
    default:
      return 'folders'
  }
}

function ticksToSeconds(ticks: number | undefined): number | undefined {
  return typeof ticks === 'number' ? Math.round(ticks / 10_000_000) : undefined
}

function normalizeCriticRating(rating: number | undefined): number | undefined {
  return typeof rating === 'number' ? Math.round(rating / 10) / 10 : undefined
}

function parseTmdbId(value: string | undefined): number | undefined {
  if (!value)
    return undefined
  const parsed = Number.parseInt(value, 10)
  return Number.isFinite(parsed) ? parsed : undefined
}

function namesByPersonType(people: readonly EmbyPersonRecord[] | undefined, type: string): string[] {
  return (people ?? [])
    .filter(person => person.Type === type && typeof person.Name === 'string')
    .map(person => person.Name as string)
}

function mapSubtitleTrack(stream: EmbyMediaStreamRecord): SubtitleTrack {
  return {
    index: stream.Index ?? 0,
    language: stream.DisplayLanguage ?? stream.Language ?? 'Unknown',
    title: stream.Title,
    isDefault: stream.IsDefault ?? false,
  }
}

function mapAudioTrack(stream: EmbyMediaStreamRecord): AudioTrack {
  return {
    index: stream.Index ?? 0,
    language: stream.DisplayLanguage ?? stream.Language ?? 'Unknown',
    codec: stream.Codec ?? 'unknown',
    channels: stream.Channels ?? 0,
    isDefault: stream.IsDefault ?? false,
  }
}

function sanitizeExportConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    throw new Error('Emby source is not configured.')

  const { apiKey: _apiKey, username: _username, password: _password, ...safe } = config
  void _apiKey
  void _username
  void _password
  return safe
}
