import type {
  AudioTrack,
  DataSource,
  DataSourceConfig,
  HomeSection,
  MediaDetail,
  MediaItem,
  MediaLibrary,
  MediaSourceOption,
  ProviderPlaybackProgressInput,
  ProviderPlaybackSyncDiagnostic,
  SubtitleTrack,
} from './types'
import { invoke } from '@tauri-apps/api/core'
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
  'SeriesName',
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
const EMBY_TICKS_PER_SECOND = 10_000_000
const EMBY_UNIX_EPOCH_TICKS = 621_355_968_000_000_000
const EMBY_MAX_STREAMING_BITRATE = 120_000_000
const PLAYBACK_INFO_FAILURE_COOLDOWN_MS = 60_000

type EmbyPlaybackAuthMode = 'default' | 'official-compatible'
type EmbyPlaybackInfoShape = 'simple-body' | 'query-body-lite' | 'query-only' | 'query-device-profile' | 'device-profile-body' | 'auto-open-live-stream' | 'official-simple-body'
type EmbyRequestPayload = Record<string, string | number | boolean | null | undefined>
type EmbyRequestBodyValue = string | number | boolean | null | EmbyRequestBodyObject | readonly EmbyRequestBodyValue[]
interface EmbyRequestBodyObject {
  readonly [key: string]: EmbyRequestBodyValue | undefined
}
type EmbyRequestBody = EmbyRequestBodyObject

type EmbyNativeJsonValue = EmbyRequestBodyValue

interface EmbyNativePlaybackJsonRequest {
  readonly baseUrl: string
  readonly path: string
  readonly query?: Record<string, string | number | boolean | null>
  readonly body?: EmbyNativeJsonValue
  readonly token: string
  readonly userId: string
  readonly deviceId: string
  readonly authMode: EmbyPlaybackAuthMode
}

interface EmbyNativePlaybackJsonResponse {
  readonly status: number
  readonly body: unknown
}

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
  readonly SeriesName?: string
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
  readonly DisplayTitle?: string
  readonly Title?: string
  readonly Codec?: string
  readonly Channels?: number
  readonly IsDefault?: boolean
  readonly IsExternal?: boolean
  readonly DeliveryUrl?: string
  readonly Format?: string
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
  readonly PlaySessionId?: string
}

interface EmbyPlaybackInfo {
  readonly mediaSources: EmbyMediaSourceRecord[]
  readonly playSessionId?: string
  readonly authMode: EmbyPlaybackAuthMode
  readonly requestShape: EmbyPlaybackInfoShape
}

interface EmbyPlaybackInfoVariant {
  readonly shape: EmbyPlaybackInfoShape
  readonly authMode: EmbyPlaybackAuthMode
  readonly query: EmbyRequestPayload
  readonly body?: EmbyRequestBody
}

interface EmbyPlaybackInfoFailure {
  readonly expiresAt: number
  readonly message: string
}

interface EmbyPlaybackInfoOptions {
  readonly requirePlaySession: boolean
}

interface EmbyPlaybackSessionMetadata {
  readonly mediaSourceId?: string
  readonly playSessionId?: string
  readonly startPosition?: number
  readonly startedAt?: number
  readonly started?: boolean
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
  private readonly playbackSessions = new Map<string, EmbyPlaybackSessionMetadata>()
  private readonly playbackInfoFailures = new Map<string, EmbyPlaybackInfoFailure>()
  private readonly playbackSyncDiagnostics: ProviderPlaybackSyncDiagnostic[] = []

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
      this.getHomeItemsSafely('continueWatching', () => this.getContinueWatching()),
      this.getHomeItemsSafely('recentlyAdded', () => this.getRecentlyAdded()),
      this.getHomeItemsSafely('featured', () => this.getFeaturedItems()),
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

  private async getHomeItemsSafely(section: 'continueWatching' | 'recentlyAdded' | 'featured', loader: () => Promise<MediaItem[]>): Promise<MediaItem[]> {
    try {
      return await loader()
    }
    catch (error) {
      this.rememberHomeDiagnostic(section, error)
      return []
    }
  }

  async getContinueWatching(): Promise<MediaItem[]> {
    const records = await this.fetchContinueWatchingRecords()
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

    const embyPlaybackSource = mediaSources.find(source => Boolean(this.resolveMediaSourceUrl(source, false)))
    if (embyPlaybackSource) {
      this.rememberPlaybackSession(id, embyPlaybackSource.Id)
      const resolvedUrl = this.resolveMediaSourceUrl(embyPlaybackSource, false)
      if (resolvedUrl)
        return resolvedUrl
    }

    const staticStreamSource = mediaSources.find(source => typeof source.Id === 'string') ?? mediaSources[0]
    if (staticStreamSource) {
      this.rememberPlaybackSession(id, staticStreamSource.Id)
      return this.buildStaticStreamUrl(id, staticStreamSource.Id)
    }

    const remotePlaybackSource = mediaSources.find(source => Boolean(this.resolveMediaSourceUrl(source, true)))
    if (remotePlaybackSource) {
      this.rememberPlaybackSession(id, remotePlaybackSource.Id)
      const resolvedUrl = this.resolveMediaSourceUrl(remotePlaybackSource, true)
      if (resolvedUrl)
        return resolvedUrl
    }

    throw new Error('Emby 未暴露可由 Player 直接播放的流地址。请检查该条目的播放权限或 Emby/插件直链配置。')
  }

  async syncPlaybackProgress(progress: ProviderPlaybackProgressInput): Promise<void> {
    this.ensureConfigured()
    const itemId = progress.itemId.trim()
    if (!itemId || !Number.isFinite(progress.position) || progress.position < 0) {
      this.rememberPlaybackSyncDiagnostic(progress, 'input', 'skipped', false, '缺少有效 itemId 或 position。')
      return
    }

    const session = await this.ensurePlaybackSession(itemId, progress)
    if (!session.playSessionId) {
      this.rememberPlaybackSyncDiagnostic(progress, 'session-metadata', '/Items/{Id}/PlaybackInfo', false, missingPlaySessionMessage(session), session)
      if (progress.event === 'stopped' || progress.event === 'completed')
        this.playbackSessions.delete(itemId)
      return
    }

    const shouldStartSession = !session.started && progress.event !== 'stopped' && progress.event !== 'completed'
    const startError = shouldStartSession
      ? await this.syncSessionPlaybackProgress(itemId, session, { ...progress, event: 'started', isPaused: false })
      : null

    if (shouldStartSession && !startError)
      this.rememberPlaybackSession(itemId, session.mediaSourceId, session.playSessionId, session.startPosition, session.startedAt, true)

    if (progress.event === 'started') {
      throwPlaybackSyncDiagnostic('started', startError)
      return
    }

    const sessionError = await this.syncSessionPlaybackProgress(itemId, session, progress)

    if (progress.completed)
      await this.markPlayed(itemId)

    if (progress.event === 'stopped' || progress.event === 'completed')
      this.playbackSessions.delete(itemId)

    throwPlaybackSyncDiagnostic(progress.event, startError ?? sessionError)
  }

  private async syncSessionPlaybackProgress(itemId: string, session: EmbyPlaybackSessionMetadata, progress: ProviderPlaybackProgressInput): Promise<unknown | null> {
    const reportPosition = playbackReportPosition(progress)
    const endpoint = sessionEndpointForEvent(progress.event)
    if (!session.playSessionId) {
      this.rememberPlaybackSyncDiagnostic(progress, 'sessions', endpoint, false, '缺少 PlaySessionId，已跳过 Emby session endpoint 以避免 400。', session)
      return null
    }

    const body = compactPlaybackBody({
      ItemId: itemId,
      MediaSourceId: session.mediaSourceId,
      PlaySessionId: session.playSessionId,
      PositionTicks: secondsToTicks(reportPosition),
      RunTimeTicks: secondsToTicks(progress.duration),
      PlaybackStartTimeTicks: dateToTicks(session.startedAt),
      CanSeek: true,
      IsPaused: progress.isPaused,
      IsMuted: false,
      PlayMethod: 'DirectPlay',
      EventName: progressEventName(progress.event),
      PlaybackRate: normalizePlaybackRate(progress.playbackRate),
      RepeatMode: 'RepeatNone',
      VolumeLevel: 100,
    })

    try {
      await this.postPlaybackJson(endpoint, body, 'default')
      this.rememberPlaybackSyncDiagnostic(progress, 'sessions', endpoint, true, 'auth=default', session)
      return null
    }
    catch (error) {
      this.rememberPlaybackSyncDiagnostic(progress, 'sessions', endpoint, false, redactSensitiveText(error), session)
      return error
    }
  }

  getPlaybackSyncDiagnostics(): ProviderPlaybackSyncDiagnostic[] {
    return [...this.playbackSyncDiagnostics]
  }

  private rememberHomeDiagnostic(section: string, error: unknown): void {
    this.playbackSyncDiagnostics.unshift({
      timestamp: new Date().toISOString(),
      sourceId: this.id,
      event: 'progress',
      stage: 'home-refresh',
      ok: false,
      endpoint: section,
      itemIdPresent: false,
      mediaSourceIdPresent: false,
      playSessionIdPresent: false,
      position: 0,
      message: `首页 ${section} 刷新失败，已保留其它可用分区：${redactSensitiveText(error)}`,
    })
    this.playbackSyncDiagnostics.splice(12)
  }

  private rememberPlaybackSyncDiagnostic(progress: ProviderPlaybackProgressInput, stage: string, endpoint: string, ok: boolean, message?: string, session?: EmbyPlaybackSessionMetadata): void {
    this.playbackSyncDiagnostics.unshift({
      timestamp: new Date().toISOString(),
      sourceId: this.id,
      event: progress.event,
      stage,
      ok,
      endpoint,
      itemIdPresent: progress.itemId.trim().length > 0,
      mediaSourceIdPresent: Boolean(progress.mediaSourceId || session?.mediaSourceId),
      playSessionIdPresent: Boolean(progress.playSessionId || session?.playSessionId),
      position: playbackDiagnosticPosition(progress),
      message: message ? redactSensitiveText(message) : undefined,
    })
    this.playbackSyncDiagnostics.splice(12)
  }

  private rememberPlaybackInfoDiagnostic(playbackInfo: EmbyPlaybackInfo, ok: boolean, errorMessage?: string, diagnosticPosition = 0): void {
    const mediaSourceCount = playbackInfo.mediaSources.length
    const messageParts = [
      `shape=${playbackInfo.requestShape}`,
      `auth=${playbackInfo.authMode}`,
      `mediaSources=${mediaSourceCount > 0 ? 'yes' : 'no'}`,
      `mediaSourceCount=${mediaSourceCount}`,
      `playSession=${playbackInfo.playSessionId ? 'yes' : 'no'}`,
    ]
    if (!playbackInfo.playSessionId && mediaSourceCount > 0)
      messageParts.push('Emby 未返回 PlaySessionId；将继续尝试安全请求形状，若仍缺失则跳过会 400 的 session 上报。')
    if (errorMessage)
      messageParts.push(errorMessage)

    this.playbackSyncDiagnostics.unshift({
      timestamp: new Date().toISOString(),
      sourceId: this.id,
      event: 'started',
      stage: 'playback-info',
      ok,
      endpoint: '/Items/{Id}/PlaybackInfo',
      itemIdPresent: true,
      mediaSourceIdPresent: mediaSourceCount > 0,
      playSessionIdPresent: Boolean(playbackInfo.playSessionId),
      position: Number.isFinite(diagnosticPosition) ? Math.max(0, diagnosticPosition) : 0,
      message: redactSensitiveText(messageParts.join(' · ')),
    })
    this.playbackSyncDiagnostics.splice(12)
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
    const mediaSourceId = item.MediaSources?.find(source => typeof source.Id === 'string')?.Id

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
      subtitles: streams.filter(stream => stream.Type === 'Subtitle').map(stream => this.mapSubtitleTrack(item.Id, stream, mediaSourceId)),
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
      const playbackInfo = await this.fetchPlaybackInfo(id)
      const mediaSourceId = playbackInfo.mediaSources.find(source => typeof source.Id === 'string')?.Id
      this.rememberPlaybackSession(id, mediaSourceId, playbackInfo.playSessionId)
      if (playbackInfo.mediaSources.length > 0)
        return playbackInfo.mediaSources
    }
    catch {
      // Fall back to detail MediaSources. Errors are intentionally hidden so tokenized URLs are not surfaced.
    }

    return (item.MediaSources ?? []).filter(isEmbyMediaSourceRecord)
  }

  private async fetchPlaybackInfo(id: string, session?: EmbyPlaybackSessionMetadata, startPosition?: number, options: EmbyPlaybackInfoOptions = { requirePlaySession: false }): Promise<EmbyPlaybackInfo> {
    const endpoint = `/Items/${encodeURIComponent(id)}/PlaybackInfo`
    const diagnosticPosition = typeof startPosition === 'number' ? startPosition : 0
    const safeVariants = createPlaybackInfoVariants(this.userId, session?.mediaSourceId, secondsToTicks(startPosition))
    let bestInfo: EmbyPlaybackInfo | null = null
    let lastError: unknown = null
    let defaultResponseWithoutPlaySession = false

    for (const variant of safeVariants) {
      try {
        const playbackInfo = await this.fetchPlaybackInfoVariant(endpoint, variant, diagnosticPosition)
        if (playbackInfo.playSessionId) {
          this.playbackInfoFailures.delete(id)
          return playbackInfo
        }
        if (playbackInfo.mediaSources.length > 0) {
          defaultResponseWithoutPlaySession = true
          if (!bestInfo || bestInfo.mediaSources.length === 0)
            bestInfo = playbackInfo
          if (!options.requirePlaySession) {
            this.rememberPlaybackInfoFailure(id, 'PlaybackInfo 已返回 MediaSourceId 但缺少 PlaySessionId；播放可继续，Emby active/cloud history 同步稍后再尝试。')
            return playbackInfo
          }
        }
      }
      catch (error) {
        lastError = error
      }
    }

    if (options.requirePlaySession && defaultResponseWithoutPlaySession) {
      const officialVariant = createOfficialPlaybackInfoVariant(this.userId, session?.mediaSourceId, secondsToTicks(startPosition))
      try {
        const playbackInfo = await this.fetchPlaybackInfoVariant(endpoint, officialVariant, diagnosticPosition)
        if (playbackInfo.playSessionId) {
          this.playbackInfoFailures.delete(id)
          return playbackInfo
        }
        if (playbackInfo.mediaSources.length > 0 && (!bestInfo || bestInfo.mediaSources.length === 0))
          bestInfo = playbackInfo
      }
      catch (error) {
        lastError = error
      }
    }

    if (bestInfo) {
      this.rememberPlaybackInfoFailure(id, 'PlaybackInfo 安全请求形状均未返回 PlaySessionId；此 Emby 当前无法同步 active/cloud history，已跳过会 400 的 session 上报。')
      return bestInfo
    }

    const message = lastError ? redactSensitiveText(lastError) : 'PlaybackInfo 所有安全请求形状均失败。'
    this.rememberPlaybackInfoFailure(id, message)
    throw new Error(message)
  }

  private async fetchPlaybackInfoVariant(endpoint: string, variant: EmbyPlaybackInfoVariant, diagnosticPosition: number): Promise<EmbyPlaybackInfo> {
    try {
      const response = await this.postPlaybackJson(endpoint, variant.body, variant.authMode, variant.query)
      const playbackInfo = parsePlaybackInfo(response, variant.authMode, variant.shape)
      this.rememberPlaybackInfoDiagnostic(playbackInfo, true, undefined, diagnosticPosition)
      return playbackInfo
    }
    catch (error) {
      const fallbackInfo: EmbyPlaybackInfo = { mediaSources: [], authMode: variant.authMode, requestShape: variant.shape }
      this.rememberPlaybackInfoDiagnostic(fallbackInfo, false, redactSensitiveText(error), diagnosticPosition)
      throw error
    }
  }

  private async ensurePlaybackSession(itemId: string, progress: ProviderPlaybackProgressInput): Promise<EmbyPlaybackSessionMetadata> {
    const startPosition = playbackStartPosition(progress)
    const current = this.playbackSessions.get(itemId)
    if (progress.mediaSourceId || progress.playSessionId)
      this.rememberPlaybackSession(itemId, progress.mediaSourceId, progress.playSessionId, current?.startPosition ?? startPosition, current?.startedAt, current?.started)

    const cached = this.playbackSessions.get(itemId)
    const needsRefresh = progress.event === 'started' || !cached?.mediaSourceId || !cached.playSessionId

    if (!needsRefresh && cached)
      return cached

    const recentFailure = this.recentPlaybackInfoFailure(itemId)
    const shouldRetryAfterFailure = progress.event === 'started' || progress.event === 'resumed' || Boolean(progress.playSessionId)
    if (recentFailure && !shouldRetryAfterFailure) {
      this.rememberPlaybackSyncDiagnostic(progress, 'session-metadata', '/Items/{Id}/PlaybackInfo', false, `PlaybackInfo 最近失败，暂缓重复重试以避免进度上报刷屏：${recentFailure.message}`, cached)
      return cached ?? {
        mediaSourceId: progress.mediaSourceId,
        playSessionId: progress.playSessionId,
        startPosition,
        startedAt: Date.now(),
        started: false,
      }
    }

    try {
      const playbackInfo = await this.fetchPlaybackInfo(itemId, cached, startPosition, { requirePlaySession: true })
      const resolvedMediaSourceId = progress.mediaSourceId ?? cached?.mediaSourceId ?? playbackInfo.mediaSources.find(source => typeof source.Id === 'string')?.Id
      this.rememberPlaybackSession(itemId, resolvedMediaSourceId, progress.playSessionId ?? playbackInfo.playSessionId ?? cached?.playSessionId, cached?.startPosition ?? startPosition, cached?.startedAt, cached?.started)
    }
    catch {
      // Progress sync is best-effort. Keep cached/route metadata if Emby does not return playback info now.
    }

    return this.playbackSessions.get(itemId) ?? {
      mediaSourceId: progress.mediaSourceId,
      playSessionId: progress.playSessionId,
      startPosition,
      startedAt: Date.now(),
      started: false,
    }
  }

  private rememberPlaybackInfoFailure(itemId: string, message: string): void {
    const key = itemId.trim()
    if (!key)
      return

    this.playbackInfoFailures.set(key, {
      expiresAt: Date.now() + PLAYBACK_INFO_FAILURE_COOLDOWN_MS,
      message: redactSensitiveText(message),
    })
  }

  private recentPlaybackInfoFailure(itemId: string): EmbyPlaybackInfoFailure | null {
    const key = itemId.trim()
    const failure = this.playbackInfoFailures.get(key)
    if (!failure)
      return null

    if (failure.expiresAt <= Date.now()) {
      this.playbackInfoFailures.delete(key)
      return null
    }

    return failure
  }

  private rememberPlaybackSession(itemId: string, mediaSourceId?: string, playSessionId?: string, startPosition?: number, startedAt?: number, started?: boolean): void {
    const key = itemId.trim()
    if (!key)
      return

    const current = this.playbackSessions.get(key)
    this.playbackSessions.set(key, {
      mediaSourceId: mediaSourceId ?? current?.mediaSourceId,
      playSessionId: playSessionId ?? current?.playSessionId,
      startPosition: startPosition ?? current?.startPosition,
      startedAt: startedAt ?? current?.startedAt ?? Date.now(),
      started: started ?? current?.started ?? false,
    })
  }

  private async markPlayed(itemId: string): Promise<void> {
    try {
      await this.request(`/Users/{UserId}/PlayedItems/${encodeURIComponent(itemId)}`, {}, 'POST')
    }
    catch {
      // The stopped/progress report is still useful; explicit watched marking is best-effort.
    }
  }

  private resolveMediaSourceUrl(source: EmbyMediaSourceRecord, includeRemotePath: boolean): string | undefined {
    const embyUrl = this.resolveEmbyPlaybackUrl(source.DirectStreamUrl, source.AddApiKeyToDirectStreamUrl)
      ?? this.resolveEmbyPlaybackUrl(source.DirectPlayUrl, source.AddApiKeyToDirectStreamUrl)
      ?? this.resolveEmbyPlaybackUrl(source.TranscodingUrl, true)
    if (embyUrl)
      return embyUrl

    if (!includeRemotePath || hasRequiredHeaders(source))
      return undefined

    if (source.Protocol === 'Http' || source.IsRemote)
      return extractPlayableRemoteUrl(source.Path, this.baseUrl)

    return undefined
  }

  private resolveEmbyPlaybackUrl(value: string | undefined, addApiKey?: boolean): string | undefined {
    if (!value)
      return undefined

    const trimmed = value.trim()
    const tokenRequired = addApiKey ?? true
    if (trimmed.startsWith('/'))
      return this.withOptionalApiKey(`${this.baseUrl}${trimmed}`, tokenRequired)

    if (!/^https?:\/\//i.test(trimmed))
      return undefined

    if (isSameOrigin(trimmed, this.baseUrl))
      return this.withOptionalApiKey(trimmed, tokenRequired)

    return sanitizeRemotePlaybackUrl(trimmed)
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

  private mapSubtitleTrack(itemId: string, stream: EmbyMediaStreamRecord, mediaSourceId?: string): SubtitleTrack {
    return {
      index: stream.Index ?? 0,
      language: stream.DisplayLanguage ?? stream.Language ?? 'Unknown',
      title: stream.Title ?? stream.DisplayTitle,
      codec: stream.Codec ?? stream.Format,
      isDefault: stream.IsDefault ?? false,
      source: stream.IsExternal ? 'external' : 'embedded',
      url: this.subtitleStreamUrl(itemId, stream, mediaSourceId),
    }
  }

  private subtitleStreamUrl(itemId: string, stream: EmbyMediaStreamRecord, mediaSourceId?: string): string | undefined {
    if (stream.DeliveryUrl) {
      if (stream.DeliveryUrl.startsWith('/'))
        return this.withOptionalApiKey(`${this.baseUrl}${stream.DeliveryUrl}`, true)
      if (isSameOrigin(stream.DeliveryUrl, this.baseUrl))
        return this.withOptionalApiKey(stream.DeliveryUrl, true)
      return sanitizeRemotePlaybackUrl(stream.DeliveryUrl)
    }

    if (typeof stream.Index !== 'number')
      return undefined

    const extension = safeSubtitleExtension(stream.Codec ?? stream.Format)
    const mediaSourceSegment = mediaSourceId ? `${encodeURIComponent(mediaSourceId)}/` : ''
    const params = new URLSearchParams({
      api_key: this.token,
    })
    return `${this.baseUrl}/Videos/${encodeURIComponent(itemId)}/${mediaSourceSegment}Subtitles/${stream.Index}/Stream.${extension}?${params.toString()}`
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

  private async request(path: string, query: EmbyRequestPayload = {}, method: 'GET' | 'POST' = 'GET'): Promise<unknown> {
    this.ensureConfigured()
    const resolvedPath = path.replace('{UserId}', encodeURIComponent(this.userId))
    const url = `${this.baseUrl}${resolvedPath}`

    try {
      return await ofetch<unknown>(url, {
        method,
        query: method === 'GET' ? query : undefined,
        body: method === 'POST' ? query : undefined,
        headers: this.authHeaders(),
      })
    }
    catch (error) {
      throw new Error(redactSensitiveText(error))
    }
  }

  private async postPlaybackJson(path: string, body: EmbyRequestBody | undefined, authMode: EmbyPlaybackAuthMode = 'default', query: EmbyRequestPayload = {}): Promise<unknown> {
    this.ensureConfigured()
    const resolvedPath = path.replace('{UserId}', encodeURIComponent(this.userId))

    try {
      const response = await invoke<EmbyNativePlaybackJsonResponse>('emby_post_playback_json', {
        request: {
          baseUrl: this.baseUrl,
          path: resolvedPath,
          query: toNativePlaybackQuery(query),
          body,
          token: this.token,
          userId: this.userId,
          deviceId: this.deviceId,
          authMode,
        } satisfies EmbyNativePlaybackJsonRequest,
      })
      return response.body
    }
    catch (error) {
      throw new Error(redactSensitiveText(error))
    }
  }

  private authHeaders(): Record<string, string> {
    return {
      'X-Emby-Token': this.token,
      'X-Emby-Authorization': authorizationHeader(this.deviceId, this.token),
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
    const resumePosition = ticksToSeconds(item.UserData?.PlaybackPositionTicks)
    const progress = typeof item.UserData?.PlayedPercentage === 'number'
      ? Math.max(0, Math.min(1, item.UserData.PlayedPercentage / 100))
      : undefined

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
      resumePosition,
      progress,
      seriesName: item.Type === 'Episode' ? nonEmptyString(item.SeriesName) : undefined,
      seasonNumber: item.Type === 'Season' ? item.IndexNumber : item.Type === 'Episode' ? item.ParentIndexNumber : undefined,
      episodeNumber: item.Type === 'Episode' ? item.IndexNumber : undefined,
    }
  }

  private posterUrl(item: EmbyItemRecord): string | undefined {
    if (item.Type === 'Episode')
      return this.episodePosterUrl(item)

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

  private episodePosterUrl(item: EmbyItemRecord): string | undefined {
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
    if (item.Type === 'Episode')
      return this.episodeBackdropUrl(item, index)

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

  private episodeBackdropUrl(item: EmbyItemRecord, index = 0): string | undefined {
    return this.imageUrl(item.Id, 'Thumb', item.ImageTags?.Thumb, {
      width: BACKDROP_IMAGE_WIDTH,
      height: BACKDROP_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.Id, `Backdrop/${index}`, item.BackdropImageTags?.[index], {
      width: BACKDROP_IMAGE_WIDTH,
      height: BACKDROP_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.Id, 'Primary', item.ImageTags?.Primary, {
      width: BACKDROP_IMAGE_WIDTH,
      height: BACKDROP_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.ParentThumbItemId, 'Thumb', item.ParentThumbImageTag, {
      width: BACKDROP_IMAGE_WIDTH,
      height: BACKDROP_IMAGE_HEIGHT,
    })
    ?? this.imageUrl(item.ParentBackdropItemId, 'Backdrop/0', item.ParentBackdropImageTags?.[0], {
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
      credentialVersion: Date.now(),
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

function authorizationHeader(deviceId: string, token?: string, userId?: string): string {
  const userSegment = userId ? `, UserId="${userId}"` : ''
  const tokenSegment = token ? `, Token="${token}"` : ''
  return `MediaBrowser Client="OhMyCine Player", Device="Desktop", DeviceId="${deviceId}", Version="0.1.0"${userSegment}${tokenSegment}`
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

function parsePlaybackInfo(value: unknown, authMode: EmbyPlaybackAuthMode, requestShape: EmbyPlaybackInfoShape): EmbyPlaybackInfo {
  if (!isObject(value))
    return { mediaSources: [], authMode, requestShape }

  const response = value as EmbyPlaybackInfoResponse
  return {
    mediaSources: Array.isArray(response.MediaSources) ? response.MediaSources.filter(isEmbyMediaSourceRecord) : [],
    playSessionId: typeof response.PlaySessionId === 'string' && response.PlaySessionId.trim() ? response.PlaySessionId : undefined,
    authMode,
    requestShape,
  }
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
  return typeof ticks === 'number' ? Math.round(ticks / EMBY_TICKS_PER_SECOND) : undefined
}

function secondsToTicks(seconds: number | undefined): number | undefined {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 0)
    return undefined
  return Math.round(seconds * EMBY_TICKS_PER_SECOND)
}

function dateToTicks(milliseconds: number | undefined): number | undefined {
  if (typeof milliseconds !== 'number' || !Number.isFinite(milliseconds) || milliseconds <= 0)
    return undefined
  return Math.round(milliseconds * 10_000 + EMBY_UNIX_EPOCH_TICKS)
}

function playbackStartPosition(progress: ProviderPlaybackProgressInput): number | undefined {
  if (typeof progress.startPosition === 'number' && Number.isFinite(progress.startPosition) && progress.startPosition >= 0)
    return progress.startPosition

  return progress.event === 'started' ? progress.position : undefined
}

function playbackReportPosition(progress: ProviderPlaybackProgressInput): number {
  return progress.event === 'started' ? playbackStartPosition(progress) ?? progress.position : progress.position
}

function playbackDiagnosticPosition(progress: ProviderPlaybackProgressInput): number {
  const position = playbackReportPosition(progress)
  return Number.isFinite(position) ? Math.max(0, position) : 0
}

function createPlaybackInfoVariants(userId: string, mediaSourceId: string | undefined, startTimeTicks: number | undefined): EmbyPlaybackInfoVariant[] {
  const basePayload = playbackInfoPayload(userId, mediaSourceId, startTimeTicks, false)
  const autoOpenPayload = playbackInfoPayload(userId, mediaSourceId, startTimeTicks, true)

  return [
    {
      shape: 'simple-body',
      authMode: 'default',
      query: {},
      body: compactPlaybackRequestBody(basePayload),
    },
    {
      shape: 'query-body-lite',
      authMode: 'default',
      query: basePayload,
      body: compactPlaybackRequestBody({
        UserId: userId,
        IsPlayback: 'true',
      }),
    },
    {
      shape: 'query-only',
      authMode: 'default',
      query: basePayload,
    },
    {
      shape: 'query-device-profile',
      authMode: 'default',
      query: basePayload,
      body: compactPlaybackRequestBody({
        DeviceProfile: createPlaybackDeviceProfile(),
      }),
    },
    {
      shape: 'device-profile-body',
      authMode: 'default',
      query: compactPlaybackBody({ UserId: userId }),
      body: compactPlaybackRequestBody({
        ...basePayload,
        DeviceProfile: createPlaybackDeviceProfile(),
      }),
    },
    {
      shape: 'auto-open-live-stream',
      authMode: 'default',
      query: {},
      body: compactPlaybackRequestBody(autoOpenPayload),
    },
  ]
}

function createOfficialPlaybackInfoVariant(userId: string, mediaSourceId: string | undefined, startTimeTicks: number | undefined): EmbyPlaybackInfoVariant {
  return {
    shape: 'official-simple-body',
    authMode: 'official-compatible',
    query: {},
    body: compactPlaybackRequestBody(playbackInfoPayload(userId, mediaSourceId, startTimeTicks, false)),
  }
}

function playbackInfoPayload(userId: string, mediaSourceId: string | undefined, startTimeTicks: number | undefined, autoOpenLiveStream: boolean): EmbyRequestPayload {
  return compactPlaybackBody({
    UserId: userId,
    StartTimeTicks: startTimeTicks,
    MediaSourceId: mediaSourceId,
    MaxStreamingBitrate: EMBY_MAX_STREAMING_BITRATE,
    EnableDirectPlay: 'true',
    EnableDirectStream: 'true',
    EnableTranscoding: 'true',
    AllowVideoStreamCopy: 'true',
    AllowAudioStreamCopy: 'true',
    DirectPlayProtocols: 'File,Http,Rtmp,Rtsp',
    AutoOpenLiveStream: autoOpenLiveStream ? 'true' : 'false',
    IsPlayback: 'true',
  })
}

function compactPlaybackBody(body: EmbyRequestPayload): EmbyRequestPayload {
  return Object.fromEntries(
    Object.entries(body).filter(([, value]) => value !== undefined && value !== null && value !== ''),
  ) as EmbyRequestPayload
}

function toNativePlaybackQuery(query: EmbyRequestPayload): Record<string, string | number | boolean | null> | undefined {
  const entries = Object.entries(query).filter(([, value]) => value !== undefined && value !== null && value !== '') as Array<[string, string | number | boolean | null]>
  return entries.length > 0 ? Object.fromEntries(entries) : undefined
}

function compactPlaybackRequestBody(body: EmbyRequestBody): EmbyRequestBody {
  return Object.fromEntries(
    Object.entries(body).filter(([, value]) => value !== undefined && value !== null && value !== ''),
  ) as EmbyRequestBody
}

function createPlaybackDeviceProfile(): EmbyRequestBody {
  return {
    Name: 'OhMyCine Player',
    Id: 'ohmycine-player',
    MaxStreamingBitrate: EMBY_MAX_STREAMING_BITRATE,
    MaxStaticBitrate: EMBY_MAX_STREAMING_BITRATE,
    MusicStreamingTranscodingBitrate: 384000,
    DirectPlayProfiles: [
      {
        Type: 'Video',
        Container: 'mp4,m4v,mov,mkv,webm,ts,m2ts,avi,flv,wmv,asf,mpg,mpeg,3gp,ogm,ogv,iso',
      },
      {
        Type: 'Audio',
        Container: 'mp3,aac,m4a,flac,ogg,oga,opus,wav,wma,alac',
      },
    ],
    TranscodingProfiles: [
      {
        Type: 'Video',
        Container: 'ts',
        Protocol: 'hls',
        Context: 'Streaming',
        VideoCodec: 'h264,hevc',
        AudioCodec: 'aac,mp3,ac3,eac3,opus,flac',
        MaxAudioChannels: '8',
        MinSegments: '2',
        BreakOnNonKeyFrames: true,
      },
      {
        Type: 'Video',
        Container: 'mp4',
        Protocol: 'http',
        Context: 'Streaming',
        VideoCodec: 'h264,hevc',
        AudioCodec: 'aac,mp3,ac3,eac3,opus,flac',
        MaxAudioChannels: '8',
      },
      {
        Type: 'Audio',
        Container: 'mp3',
        Protocol: 'http',
        Context: 'Streaming',
        AudioCodec: 'mp3',
      },
    ],
    ContainerProfiles: [],
    CodecProfiles: [],
    ResponseProfiles: [],
    SubtitleProfiles: [
      { Format: 'srt', Method: 'External' },
      { Format: 'ass', Method: 'External' },
      { Format: 'ssa', Method: 'External' },
      { Format: 'vtt', Method: 'External' },
      { Format: 'subrip', Method: 'Embed' },
      { Format: 'ass', Method: 'Embed' },
      { Format: 'ssa', Method: 'Embed' },
    ],
  }
}

function normalizePlaybackRate(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 1
}

function throwPlaybackSyncDiagnostic(event: ProviderPlaybackProgressInput['event'], error: unknown | null): void {
  if (!error)
    return

  throw new Error(`Emby 播放状态同步失败（${event}）：${redactSensitiveText(error)}`)
}

function missingPlaySessionMessage(session: EmbyPlaybackSessionMetadata): string {
  const mediaSourceMessage = session.mediaSourceId ? 'MediaSourceId=yes' : 'MediaSourceId=no'
  return `${mediaSourceMessage} · PlaySessionId=no · PlaybackInfo 安全请求形状仍缺少 PlaySessionId，当前 Emby 服务器无法同步 active/cloud history；已跳过会 400 的 session 上报。`
}

function sessionEndpointForEvent(event: ProviderPlaybackProgressInput['event']): string {
  if (event === 'started')
    return '/Sessions/Playing'
  if (event === 'stopped' || event === 'completed')
    return '/Sessions/Playing/Stopped'
  return '/Sessions/Playing/Progress'
}

function progressEventName(event: ProviderPlaybackProgressInput['event']): string {
  switch (event) {
    case 'paused':
      return 'Pause'
    case 'resumed':
      return 'Unpause'
    case 'started':
      return 'StateChange'
    case 'stopped':
    case 'completed':
      return 'StateChange'
    case 'progress':
    default:
      return 'TimeUpdate'
  }
}

function nonEmptyString(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  return trimmed || undefined
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

function safeSubtitleExtension(value: string | undefined): string {
  const normalized = value?.toLowerCase().replace(/[^a-z0-9]/g, '')
  switch (normalized) {
    case 'ass':
    case 'ssa':
    case 'vtt':
    case 'srt':
      return normalized
    case 'webvtt':
      return 'vtt'
    default:
      return 'srt'
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

function extractPlayableRemoteUrl(value: string | undefined, embyBaseUrl?: string): string | undefined {
  if (typeof value !== 'string')
    return undefined
  const trimmed = value.trim()
  if (!/^https?:\/\//i.test(trimmed) || isStrmPath(trimmed) || isInternalPluginRedirectUrl(trimmed))
    return undefined
  if (embyBaseUrl && isSameOrigin(trimmed, embyBaseUrl))
    return undefined

  return sanitizeRemotePlaybackUrl(trimmed)
}

function sanitizeRemotePlaybackUrl(value: string): string | undefined {
  try {
    const url = new URL(value)
    if (url.protocol !== 'http:' && url.protocol !== 'https:')
      return undefined
    if (url.username || url.password)
      return undefined
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
  const normalized = key.toLowerCase()
  return ['apikey', 'api_key', 'access_token', 'passwd', 'pwd'].includes(normalized)
    || normalized.includes('token')
    || normalized.includes('password')
    || normalized.includes('username')
    || normalized.includes('authorization')
    || normalized.includes('cookie')
    || normalized.includes('passkey')
}
