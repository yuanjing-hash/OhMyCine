import type {
  AudioTrack,
  DataSource,
  DataSourceConfig,
  HomeSection,
  MediaDetail,
  MediaItem,
  MediaLibrary,
  MediaSourceOption,
  SubtitleTrack,
} from './types'
import { ofetch } from 'ofetch'
import { SourceMetadataCache } from './cache'
import { createCredentialRef, readCredential, readEmbyCredential, readRawCredentialBackup, removeCredential, saveEmbyCredential, saveRawCredentialBackup } from './credentialStore'
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
  'DateLastMediaAdded',
  'SortName',
  'RunTimeTicks',
  'ImageTags',
  'BackdropImageTags',
  'ParentId',
  'ParentBackdropItemId',
  'ParentBackdropImageTags',
  'ParentThumbItemId',
  'ParentThumbImageTag',
  'ParentLogoItemId',
  'ParentLogoImageTag',
  'PrimaryImageAspectRatio',
  'Taglines',
].join(',')

const IMAGE_QUERY = {
  EnableImages: 'true',
  EnableImageTypes: 'Primary,Backdrop,Thumb,Logo',
  ImageTypeLimit: '1',
} as const
const HERO_IMAGE_QUERY = {
  ...IMAGE_QUERY,
  EnableImageTypes: 'Backdrop,Primary,Thumb,Logo',
  ImageTypes: 'Backdrop',
} as const

const FAST_IMAGE_QUALITY = '82'
const POSTER_IMAGE_WIDTH = '420'
const POSTER_IMAGE_HEIGHT = '630'
const BACKDROP_IMAGE_WIDTH = '1600'
const BACKDROP_IMAGE_HEIGHT = '900'
const LOGO_IMAGE_WIDTH = '520'
const LOGO_IMAGE_HEIGHT = '260'

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
  readonly ParentId?: string
  readonly ParentBackdropItemId?: string
  readonly ParentBackdropImageTags?: string[]
  readonly ParentThumbItemId?: string
  readonly ParentThumbImageTag?: string
  readonly ParentLogoItemId?: string
  readonly ParentLogoImageTag?: string
  readonly PrimaryImageAspectRatio?: number
  readonly IndexNumber?: number
  readonly ParentIndexNumber?: number
  readonly ChildCount?: number
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
  readonly Name?: string
  readonly Path?: string
  readonly Container?: string
  readonly Size?: number
  readonly Bitrate?: number
  readonly Protocol?: string
  readonly IsRemote?: boolean
  readonly SupportsDirectStream?: boolean
  readonly SupportsDirectPlay?: boolean
  readonly DirectStreamUrl?: string
  readonly DirectPlayUrl?: string
  readonly TranscodingUrl?: string
  readonly RequiredHttpHeaders?: Record<string, string>
  readonly AddApiKeyToDirectStreamUrl?: boolean
}

interface EmbyPlaybackInfoResponse {
  readonly MediaSources?: unknown[]
}

interface EmbyDetailCachePayload {
  readonly item: EmbyItemRecord
  readonly similarItems: EmbyItemRecord[]
  readonly collections: EmbyItemRecord[]
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
  private readonly cache = new SourceMetadataCache()

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
    this.token = await resolveToken(config, extra)
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

  clearCache(): void {
    this.cache.clear()
  }

  async list(path?: string): Promise<MediaItem[]> {
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

    const records = await this.cache.getOrSet(`list-records:${path}`, () => this.fetchListRecords(path))
    return records.map(child => this.mapItem(child, path))
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    const records = await this.cache.getOrSet('library-records', () => this.fetchLibraryRecords())
    return records.map(item => this.mapLibrary(item))
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
    const records = await this.cache.getOrSet('featured-records', () => this.fetchFeaturedItemRecords())
    return records.map(item => this.mapItem(item))
  }

  async getContinueWatching(): Promise<MediaItem[]> {
    const records = await this.cache.getOrSet('continue-watching-records', () => this.fetchContinueWatchingRecords())
    return records.map(item => this.mapItem(item))
  }

  async getRecentlyAdded(): Promise<MediaItem[]> {
    const records = await this.cache.getOrSet('recently-added-records', () => this.fetchRecentlyAddedRecords())
    return records.map(item => this.mapItem(item))
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
      ...IMAGE_QUERY,
      Limit: '50',
    })

    return parseItemsResponse(response).map(item => this.mapItem(item))
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const payload = await this.cache.getOrSet(`detail:${id}`, () => this.fetchDetailPayload(id))
    return this.mapDetail(payload)
  }

  async getStreamURL(id: string): Promise<string> {
    this.ensureConfigured()
    const item = await this.getItem(id)
    const mediaSources = await this.getPlayableMediaSources(id, item)
    if (mediaSources.length === 0)
      throw new Error('Emby 未返回可播放的媒体源。')

    const resolvedUrl = mediaSources
      .map(source => this.resolveMediaSourceUrl(source))
      .find((url): url is string => Boolean(url))
    if (resolvedUrl)
      return resolvedUrl

    const staticStreamSource = mediaSources.find(source => canUseStaticEmbyStreamFallback(source))
    if (staticStreamSource)
      return this.buildStaticStreamUrl(id, staticStreamSource.Id)

    throw new Error('该 Emby 条目指向 STRM/远程媒体或内部插件重定向，但 Emby 未暴露可由 Player 直接播放的流地址。请在 Emby/插件侧启用可访问的直链、DirectStream/Transcoding，或使用对应云盘直连数据源。')
  }

  exportConfig(): DataSourceConfig {
    this.ensureConfigured()
    return sanitizeExportConfig(this.config)
  }

  private async fetchListRecords(path: string): Promise<EmbyItemRecord[]> {
    this.ensureConfigured()

    const item = await this.getItemIfAvailable(path)
    if (item?.Type === 'Series')
      return this.listSeasonRecords(path)

    if (item?.Type === 'Season')
      return this.listEpisodeRecords(path)

    const response = await this.request('/Users/{UserId}/Items', {
      ParentId: path,
      Recursive: 'false',
      IncludeItemTypes: 'Movie,Series,Season,Episode,Folder,CollectionFolder,BoxSet',
      Fields: ITEM_FIELDS,
      ...IMAGE_QUERY,
      SortBy: 'SortName',
      SortOrder: 'Ascending',
      Limit: '200',
    })

    return parseItemsResponse(response)
  }

  private async fetchLibraryRecords(): Promise<EmbyItemRecord[]> {
    this.ensureConfigured()
    const response = await this.request('/Users/{UserId}/Views')
    return parseLibraryResponse(response)
  }

  private async fetchFeaturedItemRecords(): Promise<EmbyItemRecord[]> {
    const response = await this.request('/Users/{UserId}/Items', {
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series',
      Fields: ITEM_FIELDS,
      SortBy: 'Random',
      ...HERO_IMAGE_QUERY,
      Limit: '20',
    })
    return parseItemsResponse(response)
  }

  private async fetchContinueWatchingRecords(): Promise<EmbyItemRecord[]> {
    const response = await this.request('/Users/{UserId}/Items/Resume', {
      MediaTypes: 'Video',
      Fields: ITEM_FIELDS,
      ...IMAGE_QUERY,
      Limit: '12',
    })
    return parseItemsResponse(response)
  }

  private async fetchRecentlyAddedRecords(): Promise<EmbyItemRecord[]> {
    const response = await this.request('/Users/{UserId}/Items', {
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series',
      Fields: ITEM_FIELDS,
      ...IMAGE_QUERY,
      SortBy: 'DateCreated',
      SortOrder: 'Descending',
      Limit: '18',
    })
    return parseItemsResponse(response)
  }

  private async fetchDetailPayload(id: string): Promise<EmbyDetailCachePayload> {
    const item = await this.getItem(id)
    const [similarItems, collections] = await Promise.all([
      this.getSimilarItemRecords(id),
      this.getCollectionRecords(id),
    ])

    return { item, similarItems, collections }
  }

  private mapDetail(payload: EmbyDetailCachePayload): MediaDetail {
    const item = payload.item
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
      mediaSources: mapMediaSources(item.MediaSources),
      stills: this.stillUrls(item),
      similarItems: payload.similarItems.map(relatedItem => this.mapItem(relatedItem)),
      collections: payload.collections.map(collection => this.mapItem(collection)),
    }
  }

  private async getItem(id: string): Promise<EmbyItemRecord> {
    const response = await this.request(`/Users/{UserId}/Items/${encodeURIComponent(id)}`, {
      Fields: ITEM_FIELDS,
      ...IMAGE_QUERY,
    })
    return parseItemRecord(response)
  }

  private async getItemIfAvailable(id: string): Promise<EmbyItemRecord | null> {
    try {
      return await this.getItem(id)
    }
    catch {
      return null
    }
  }

  private async listSeasonRecords(seriesId: string): Promise<EmbyItemRecord[]> {
    const response = await this.request(`/Shows/${encodeURIComponent(seriesId)}/Seasons`, {
      UserId: this.userId,
      Fields: ITEM_FIELDS,
      ...IMAGE_QUERY,
    })
    return parseItemsResponse(response)
  }

  private async listEpisodeRecords(parentId: string): Promise<EmbyItemRecord[]> {
    const response = await this.request('/Users/{UserId}/Items', {
      ParentId: parentId,
      Recursive: 'false',
      IncludeItemTypes: 'Episode,Folder',
      Fields: ITEM_FIELDS,
      ...IMAGE_QUERY,
      SortBy: 'ParentIndexNumber,IndexNumber,SortName',
      SortOrder: 'Ascending',
      Limit: '200',
    })
    return parseItemsResponse(response)
  }

  private async getPlayableMediaSources(id: string, item: EmbyItemRecord): Promise<EmbyMediaSourceRecord[]> {
    try {
      const response = await this.request(`/Items/${encodeURIComponent(id)}/PlaybackInfo`, {
        UserId: this.userId,
        EnableDirectPlay: 'true',
        EnableDirectStream: 'true',
        EnableTranscoding: 'true',
        AllowVideoStreamCopy: 'true',
        AllowAudioStreamCopy: 'true',
        IsPlayback: 'true',
      }, 'POST')
      const playbackSources = parsePlaybackMediaSources(response)
      if (playbackSources.length > 0)
        return playbackSources
    }
    catch {
      // Fall back to detail MediaSources. Errors are intentionally hidden so tokenized URLs are not surfaced.
    }

    return (item.MediaSources ?? []).filter(isEmbyMediaSourceRecord)
  }

  private resolveMediaSourceUrl(source: EmbyMediaSourceRecord): string | undefined {
    const embyUrl = this.resolveEmbyPlaybackUrl(source.DirectStreamUrl, source.AddApiKeyToDirectStreamUrl)
      ?? this.resolveEmbyPlaybackUrl(source.DirectPlayUrl, source.AddApiKeyToDirectStreamUrl)
      ?? this.resolveEmbyPlaybackUrl(source.TranscodingUrl, true)
    if (embyUrl)
      return embyUrl

    if (hasRequiredHeaders(source))
      return undefined

    if (source.Protocol === 'Http' || source.IsRemote)
      return extractPlayableRemoteUrl(source.Path, this.baseUrl)

    return undefined
  }

  private resolveEmbyPlaybackUrl(value: string | undefined, addApiKey?: boolean): string | undefined {
    if (!value)
      return undefined

    const trimmed = value.trim()
    if (trimmed.startsWith('/'))
      return this.withOptionalApiKey(`${this.baseUrl}${trimmed}`, addApiKey)

    if (!/^https?:\/\//i.test(trimmed))
      return undefined

    if (!isSameOrigin(trimmed, this.baseUrl) || isInternalPluginRedirectUrl(trimmed))
      return undefined

    return this.withOptionalApiKey(trimmed, addApiKey)
  }

  private withOptionalApiKey(value: string, addApiKey?: boolean): string {
    if (!addApiKey || /[?&]api_key=/i.test(value))
      return value

    try {
      const url = new URL(value)
      url.searchParams.set('api_key', this.token)
      return url.toString()
    }
    catch {
      return value
    }
  }

  private buildStaticStreamUrl(id: string, mediaSourceId?: string): string {
    const params = new URLSearchParams({
      Static: 'true',
      api_key: this.token,
    })
    if (mediaSourceId)
      params.set('MediaSourceId', mediaSourceId)
    return `${this.baseUrl}/Videos/${encodeURIComponent(id)}/stream?${params.toString()}`
  }

  private async getSimilarItemRecords(id: string): Promise<EmbyItemRecord[]> {
    try {
      const response = await this.request(`/Items/${encodeURIComponent(id)}/Similar`, {
        UserId: this.userId,
        Limit: '12',
        Fields: ITEM_FIELDS,
        ...IMAGE_QUERY,
      })
      return parseItemsResponse(response)
    }
    catch {
      return []
    }
  }

  private async getCollectionRecords(id: string): Promise<EmbyItemRecord[]> {
    try {
      const response = await this.request('/Users/{UserId}/Items', {
        Recursive: 'true',
        IncludeItemTypes: 'BoxSet',
        Fields: ITEM_FIELDS,
        ...IMAGE_QUERY,
        Limit: '12',
        AdjacentTo: id,
      })
      return parseItemsResponse(response)
    }
    catch {
      return []
    }
  }

  private async request(path: string, query: Record<string, string> = {}, method: 'GET' | 'POST' = 'GET'): Promise<unknown> {
    this.ensureConfigured()
    const resolvedPath = path.replace('{UserId}', encodeURIComponent(this.userId))
    const url = `${this.baseUrl}${resolvedPath}`

    try {
      return await ofetch<unknown>(url, {
        method,
        query: method === 'GET' ? query : undefined,
        body: method === 'POST' ? query : undefined,
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
      posterUrl: this.posterUrl(item),
      backdropUrl: this.backdropUrl(item),
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
      posterUrl: this.posterUrl(item),
      backdropUrl: this.backdropUrl(item),
      titleLogoUrl: this.logoUrl(item),
      year: item.ProductionYear,
      rating: item.CommunityRating ?? normalizeCriticRating(item.CriticRating),
      overview: item.Overview,
      tagline: item.Taglines?.[0],
      duration: ticksToSeconds(item.RunTimeTicks),
      size: item.Size ?? mediaSourceSize,
      modified: item.DateCreated ?? item.DateLastMediaAdded,
      path: item.Id,
      seasonNumber: item.Type === 'Season' ? item.IndexNumber : item.Type === 'Episode' ? item.ParentIndexNumber : undefined,
      episodeNumber: item.Type === 'Episode' ? item.IndexNumber : undefined,
    }
  }

  private posterUrl(item: EmbyItemRecord): string | undefined {
    return this.imageUrl(item.Id, 'Primary', item.ImageTags?.Primary, {
      width: POSTER_IMAGE_WIDTH,
      height: POSTER_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.Id, 'Thumb', item.ImageTags?.Thumb, {
      width: POSTER_IMAGE_WIDTH,
      height: POSTER_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.ParentThumbItemId, 'Thumb', item.ParentThumbImageTag, {
      width: POSTER_IMAGE_WIDTH,
      height: POSTER_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.ParentBackdropItemId, 'Backdrop/0', item.ParentBackdropImageTags?.[0], {
      width: POSTER_IMAGE_WIDTH,
      height: POSTER_IMAGE_HEIGHT,
    })
  }

  private backdropUrl(item: EmbyItemRecord, index = 0): string | undefined {
    return this.imageUrl(item.Id, `Backdrop/${index}`, item.BackdropImageTags?.[index], {
      width: BACKDROP_IMAGE_WIDTH,
      height: BACKDROP_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.ParentBackdropItemId, 'Backdrop/0', item.ParentBackdropImageTags?.[0], {
      width: BACKDROP_IMAGE_WIDTH,
      height: BACKDROP_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.Id, 'Thumb', item.ImageTags?.Thumb, {
      width: BACKDROP_IMAGE_WIDTH,
      height: BACKDROP_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.ParentThumbItemId, 'Thumb', item.ParentThumbImageTag, {
      width: BACKDROP_IMAGE_WIDTH,
      height: BACKDROP_IMAGE_HEIGHT,
    })
  }

  private logoUrl(item: EmbyItemRecord): string | undefined {
    return this.imageUrl(item.Id, 'Logo', item.ImageTags?.Logo, {
      width: LOGO_IMAGE_WIDTH,
      height: LOGO_IMAGE_HEIGHT,
      cropWhitespace: true,
    })
    ?? this.imageUrl(item.ParentLogoItemId, 'Logo', item.ParentLogoImageTag, {
      width: LOGO_IMAGE_WIDTH,
      height: LOGO_IMAGE_HEIGHT,
      cropWhitespace: true,
    })
  }

  private imageUrl(itemId: string | undefined, type: string, tag: string | undefined, size: { width?: string, height?: string, cropWhitespace?: boolean } = {}): string | undefined {
    if (!this.token || !itemId || !tag)
      return undefined

    const params = new URLSearchParams({
      api_key: this.token,
      quality: FAST_IMAGE_QUALITY,
      tag,
    })
    if (size.width)
      params.set('maxWidth', size.width)
    if (size.height)
      params.set('maxHeight', size.height)
    if (size.cropWhitespace)
      params.set('CropWhitespace', 'true')

    return `${this.baseUrl}/Items/${encodeURIComponent(itemId)}/Images/${type}?${params.toString()}`
  }

  private stillUrls(item: EmbyItemRecord): string[] {
    const ownStills = (item.BackdropImageTags ?? [])
      .slice(0, 8)
      .map((tag, index) => this.imageUrl(item.Id, `Backdrop/${index}`, tag, {
        width: BACKDROP_IMAGE_WIDTH,
        height: BACKDROP_IMAGE_HEIGHT,
      }))
      .filter((url): url is string => Boolean(url))

    if (ownStills.length > 0)
      return ownStills

    const parentBackdrop = this.backdropUrl(item)
    return parentBackdrop ? [parentBackdrop] : []
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
  const previousCredential = await readRawCredentialBackup(credentialRef)
  const auth = await source.authenticate(input.username, input.password)
  try {
    await saveEmbyCredential(credentialRef, {
      accessToken: auth.accessToken,
      username: input.username.trim(),
      password: input.password,
    })
  }
  catch (error) {
    throw new Error(redactSensitiveText(error))
  }

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

  let libraries: MediaLibrary[] = []
  try {
    await source.init(safeConfig)
    libraries = await source.listLibraries()
  }
  catch (error) {
    if (previousCredential)
      await saveRawCredentialBackup(credentialRef, previousCredential)
    else
      await removeCredential(credentialRef)
    throw new Error(redactSensitiveText(error))
  }
  finally {
    source.destroy()
  }

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

async function resolveToken(config: DataSourceConfig, extra: EmbyConfigExtra): Promise<string> {
  if (extra.credentialRef) {
    const credential = await readEmbyCredential(extra.credentialRef)
    if (credential?.accessToken)
      return credential.accessToken
    const token = await readCredential(extra.credentialRef)
    if (token)
      return token
  }

  void config
  return ''
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

function parsePlaybackMediaSources(value: unknown): EmbyMediaSourceRecord[] {
  if (!isObject(value))
    return []
  const response = value as EmbyPlaybackInfoResponse
  if (!Array.isArray(response.MediaSources))
    return []
  return response.MediaSources.filter(isEmbyMediaSourceRecord)
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

function isEmbyMediaSourceRecord(value: unknown): value is EmbyMediaSourceRecord {
  if (!isObject(value))
    return false
  const record = value as Record<string, unknown>
  return typeof record.Id === 'string' || typeof record.Path === 'string' || typeof record.Name === 'string'
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
    case 'Season':
      return 'season'
    case 'Folder':
    case 'CollectionFolder':
    case 'BoxSet':
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

function mapMediaSources(sources: readonly EmbyMediaSourceRecord[] | undefined): MediaSourceOption[] {
  return (sources ?? [])
    .filter(isEmbyMediaSourceRecord)
    .map((source, index) => ({
      id: source.Id ?? `source-${index}`,
      name: source.Name ?? safeMediaSourceLabel(source, index),
      container: source.Container,
      size: source.Size,
      bitrate: source.Bitrate,
      isRemote: source.IsRemote || source.Protocol === 'Http' || isHttpUrl(source.Path),
      isStrm: isStrmPath(source.Path) || isStrmPath(source.Name),
    }))
}

function isRemoteOrStrmSource(source: EmbyMediaSourceRecord): boolean {
  return Boolean(source.IsRemote)
    || source.Protocol === 'Http'
    || isHttpUrl(source.Path)
    || isStrmPath(source.Path)
    || isStrmPath(source.Name)
}

function canUseStaticEmbyStreamFallback(source: EmbyMediaSourceRecord): boolean {
  return !isRemoteOrStrmSource(source)
    && !hasRequiredHeaders(source)
    && !isInternalPluginRedirectUrl(source.Path)
}

function extractPlayableRemoteUrl(value: string | undefined, embyBaseUrl?: string): string | undefined {
  if (typeof value !== 'string')
    return undefined
  const trimmed = value.trim()
  if (!/^https?:\/\//i.test(trimmed) || isStrmPath(trimmed) || isInternalPluginRedirectUrl(trimmed))
    return undefined
  if (embyBaseUrl && isSameOrigin(trimmed, embyBaseUrl))
    return undefined

  try {
    const url = new URL(trimmed)
    url.username = ''
    url.password = ''
    return url.toString()
  }
  catch {
    return undefined
  }
}

function isHttpUrl(value: string | undefined): boolean {
  return typeof value === 'string' && /^https?:\/\//i.test(value.trim())
}

function hasRequiredHeaders(source: EmbyMediaSourceRecord): boolean {
  return source.RequiredHttpHeaders != null && Object.keys(source.RequiredHttpHeaders).length > 0
}

function isSameOrigin(value: string, baseUrl: string): boolean {
  try {
    const url = new URL(value)
    const base = new URL(baseUrl)
    return url.origin === base.origin
  }
  catch {
    return false
  }
}

function isInternalPluginRedirectUrl(value: string | undefined): boolean {
  if (!value)
    return false

  try {
    const url = new URL(value)
    const pathname = url.pathname.toLowerCase()
    return pathname.includes('/api/v1/plugin/')
      || pathname.includes('/plugin/')
      || pathname.includes('/redirect_url')
      || pathname.includes('/redirect-url')
  }
  catch {
    return /\/api\/v1\/plugin\/|\/plugin\/|redirect_url|redirect-url/i.test(value)
  }
}

function isStrmPath(value: string | undefined): boolean {
  return typeof value === 'string' && /\.strm(?:$|[?#])/i.test(value)
}

function safeMediaSourceLabel(source: EmbyMediaSourceRecord, index: number): string {
  const details = [source.Container, source.Bitrate ? `${Math.round(source.Bitrate / 1000)} kbps` : undefined]
    .filter(Boolean)
    .join(' · ')
  return details ? `版本 ${index + 1} · ${details}` : `版本 ${index + 1}`
}

function sanitizeExportConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    throw new Error('Emby source is not configured.')

  const safeExtra = Object.fromEntries(
    Object.entries(config.extra ?? {}).filter(([key]) => !isSensitiveConfigKey(key)),
  )

  return {
    ...config,
    extra: safeExtra,
  }
}

function isSensitiveConfigKey(key: string): boolean {
  return ['apiKey', 'token', 'accessToken', 'password', 'username'].includes(key)
}
