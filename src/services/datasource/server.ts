import type { ServerCredentialValue } from './credentialStore'
import type { DataSource, DataSourceConfig, DataSourceMediaChange, HomeSection, MediaAcquisitionState, MediaDetail, MediaIdentity, MediaItem, MediaLibrary, MediaSourceOption, MediaStreamRequest, PlaybackDanmakuTrack, PlaybackRequest, ProviderCollectionOption, ProviderDanmakuComment, ProviderPlaybackHistoryPage, ProviderPlaybackHistoryRequest, ProviderPlaybackProgressInput, SiteActionKey } from './types'
import { Channel, invoke } from '@tauri-apps/api/core'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { tmdbArtworkUrl } from '@/services/scraper/tmdb'
import { getServerAcquisition, getServerDiscoveryDetail } from '@/services/serverDiscovery'
import { createCredentialRef, readServerCredential, saveServerCredential } from './credentialStore'
import { redactSensitiveText } from './errors'
import { playbackTargetsForItem } from './identityMerge'
import {
  onlineContributionErrorToHomeSection,
  onlineLibraryToMediaLibrary,
  onlineNavigationToMediaItem,
  onlinePlaybackToStreamRequest,
  onlineSectionsToHomeSections,
  onlineWorkToDetail,
  onlineWorkToMediaItem,
  parseOnlineFeedSections,
  parseOnlineHistoryPage,
  parseOnlineHomeContributions,
  parseOnlineItemID,
  parseOnlineLibraryList,
  parseOnlineNavigationList,
  parseOnlinePlaybackPlan,
  parseOnlineWork,
  parseProviderDanmakuComments,
} from './serverOnline'

interface ServerConfigExtra {
  credentialRef?: string
  deviceId: string
  credentialVersion?: number
  libraries?: Array<{ id: string, name: string, type: MediaLibrary['type'] }>
  capabilities?: string[]
}

interface ServerNativeRequest {
  baseUrl: string
  method: 'GET' | 'POST' | 'PUT' | 'DELETE'
  path: string
  accessToken?: string
  body?: unknown
}

interface ServerNativeResponse {
  status: number
  body: unknown
}

export interface ServerDiscoveryStreamEvent {
  event: 'media' | 'progress' | 'site' | 'done' | 'error'
  data: unknown
}

export interface ServerDiscoveryStreamHandle {
  requestId: string
  done: Promise<void>
  cancel: () => Promise<void>
}

interface ServerLibraryRecord {
  id: number
  name: string
  storage_type: string
  entry_count: number
  work_count?: number
  artwork_url?: string
  artwork_revision?: string
  artwork_source?: 'generated' | 'provider' | 'custom' | 'fallback'
}

interface ServerCategoryRecord {
  id: string
  name: string
  media_type: 'movie' | 'series'
  item_count: number
  artwork_url?: string
  artwork_revision?: string
  artwork_source?: 'generated' | 'provider' | 'custom' | 'fallback'
}

interface ServerIdentityRecord {
  scheme: 'tmdb' | 'server'
  media_type: 'movie' | 'series'
  value: string
}

interface ServerPersonRecord {
  tmdb_id?: number
  name: string
  role?: string
  character?: string
  profile_path?: string
}

interface ServerItemRecord {
  id: string
  item_token?: string
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
  people?: ServerPersonRecord[]
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
  history_identity?: string
}

interface ServerVersionRecord {
  id: number
  item_token?: string
  title: string
  display_title?: string
  display_subtitle?: string
  series_title?: string
  episode_title?: string
  season?: number
  episode?: number
  overview?: string
  still_path?: string
  poster_path?: string
  backdrop_path?: string
  episode_still_path?: string
  air_date?: string
  runtime_minutes?: number
  rating?: number
  size: number
  modified_at: string
  playable: boolean
  stream_path?: string
  delivery_kind?: 'server_stream' | 'server_redirect'
  exact_identity: string
  history_identity?: string
}

interface ServerDetailRecord {
  item: ServerItemRecord
  versions: ServerVersionRecord[]
}

interface ServerAcquisitionRecord {
  id: string
  title?: string
  mediaType: 'movie' | 'tv'
  tmdbId: number
  stage: string
  status: string
  targetLibraryId?: number
  progress?: number
  processedFiles: number
  totalFiles: number
  lastErrorCode?: string
  updatedAt?: string
}

interface ServerCollectionRecord {
  id: string
  name: string
  kind: 'collection' | 'playlist'
  source: 'tmdb' | 'manual'
  itemCount: number
  posterPath?: string
  backdropPath?: string
}

interface ServerOverviewSectionRecord {
  status: 'ok' | 'unavailable'
  list: unknown[]
  hasMore: boolean
  errorCode?: string
}

interface ServerOverviewRecord {
  version: 'v1'
  sections: {
    featured: ServerOverviewSectionRecord
    continueWatching: ServerOverviewSectionRecord
    recentlyAdded: ServerOverviewSectionRecord
    favorites: ServerOverviewSectionRecord
    automaticCollections: ServerOverviewSectionRecord
    manualCollections: ServerOverviewSectionRecord
    recentHistory: ServerOverviewSectionRecord
    mediaLibraries: ServerOverviewSectionRecord
  }
}

interface ServerMediaChangePage {
  cursor: string
  resyncRequired: boolean
  libraryIds: string[]
  libraryRevisions: Record<string, number>
  changeCount: number
}

class ServerRequestError extends Error {
  constructor(message: string, readonly status: number, readonly code?: string) {
    super(message)
    this.name = 'ServerRequestError'
  }
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
  stream?: (request: ServerNativeRequest, requestId: string, onEvent: (event: ServerDiscoveryStreamEvent) => void) => Promise<void>
  cancelStream?: (requestId: string) => Promise<void>
}

const defaultServerBridge: ServerBridge = {
  request: request => invoke<ServerNativeResponse>('server_request_json', { request }),
  stream: (request, requestId, onEvent) => {
    const onEventChannel = new Channel<ServerDiscoveryStreamEvent>()
    onEventChannel.onmessage = onEvent
    return invoke<void>('server_stream_sse', { request, requestId, onEvent: onEventChannel })
  },
  cancelStream: requestId => invoke<void>('server_cancel_sse', { requestId }),
}

const SERVER_PAGE_SIZE = 100
const SERVER_MAX_PAGES = 100
const SERVER_MAX_ITEMS = SERVER_PAGE_SIZE * SERVER_MAX_PAGES
const SERVER_ONLINE_PROGRESS_CONTEXT_LIMIT = 256

interface ServerOnlineProgressContext {
  libraryId: string
  workId: string
  segmentId: string
  versionId: string
}

export class ServerDataSource implements DataSource {
  readonly type = 'server' as const
  private config: DataSourceConfig | null = null
  private baseUrl = ''
  private credentialRef = ''
  private credential: ServerCredentialValue | null = null
  private connected = false
  private capabilities = new Set<string>()
  private readonly onlineProgressContexts = new Map<string, ServerOnlineProgressContext>()
  private readonly bridge: ServerBridge
  private readonly readCredential: (ref: string) => Promise<ServerCredentialValue | null>
  private mediaChangeWatchGeneration = 0

  constructor(options: { bridge?: ServerBridge, readCredential?: (ref: string) => Promise<ServerCredentialValue | null> } = {}) {
    this.bridge = options.bridge ?? defaultServerBridge
    this.readCredential = options.readCredential ?? readServerCredential
  }

  get id(): string { return this.config?.id ?? '' }
  get name(): string { return this.config?.displayName ?? this.config?.name ?? 'OhMyCine Server' }
  get isConnected(): boolean { return this.connected }
  get capabilityCodes(): string[] { return [...this.capabilities].sort() }

  hasCapability(capability: string): boolean {
    return this.capabilities.has(capability)
  }

  async init(config: DataSourceConfig): Promise<void> {
    this.config = sanitizeServerConfig(config)
    this.baseUrl = normalizeServerBaseUrl(config.url)
    this.credentialRef = readServerExtra(config).credentialRef ?? ''
    this.credential = this.credentialRef ? await this.readCredential(this.credentialRef) : null
    this.capabilities = new Set(readServerExtra(config).capabilities ?? [])
    this.connected = Boolean(this.baseUrl && this.credential)
  }

  async test(): Promise<boolean> {
    await this.refreshCapabilities()
    this.connected = true
    return true
  }

  async refreshCapabilities(): Promise<string[]> {
    const data = recordData(await this.request('/api/v1/player/bootstrap'))
    if (!Array.isArray(data.capabilities) || data.capabilities.length > 64)
      throw new Error('Server 返回了无效的账户能力。')
    const capabilities = data.capabilities.flatMap((value): string[] => typeof value === 'string' && /^[a-z][a-z0-9_]{0,63}$/.test(value) ? [value] : [])
    if (capabilities.length !== data.capabilities.length)
      throw new Error('Server 返回了无效的账户能力。')
    this.capabilities = new Set(capabilities)
    return this.capabilityCodes
  }

  async syncPlaybackHistory(payload: ServerPlaybackHistorySyncRequest): Promise<ServerPlaybackHistorySyncResponse> {
    return parsePlaybackHistorySyncResponse(await this.request('/api/v1/player/history/sync', 'POST', payload))
  }

  async searchDiscoveryMedia(query: string, mediaType: 'all' | 'movie' | 'tv' = 'all'): Promise<unknown> {
    const params = new URLSearchParams({ query: query.trim(), media_type: mediaType, page: '1' })
    return this.request(`/api/v1/player/discovery/media-search?${params}`)
  }

  async getDiscoveryDetail(provider: 'tmdb' | 'douban', mediaType: 'movie' | 'tv', providerID: string): Promise<unknown> {
    return this.request(`/api/v1/player/discovery/details/${encodeURIComponent(provider)}/${encodeURIComponent(mediaType)}/${encodeURIComponent(providerID)}`)
  }

  async getDiscoverySearchOptions(): Promise<unknown> {
    return this.request('/api/v1/player/discovery/search-options')
  }

  async getDiscoveryCoverage(mediaType: 'movie' | 'tv', tmdbId: number): Promise<unknown> {
    return this.request(`/api/v1/player/discovery/media/${mediaType}/${tmdbId}/coverage`)
  }

  async getDiscoveryAcquisition(mediaType: 'movie' | 'tv', tmdbId: number): Promise<unknown> {
    return this.request(`/api/v1/player/discovery/media/${mediaType}/${tmdbId}/acquisition`)
  }

  async getDiscoveryAcquisitions(page = 1, pageSize = 30): Promise<unknown> {
    const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
    return this.request(`/api/v1/player/discovery/acquisitions?${params}`)
  }

  async searchDiscoveryResources(path: string): Promise<unknown> {
    if (!path.startsWith('/api/v1/player/discovery/torrent-search?') && !/^\/api\/v1\/player\/discovery\/media\/(?:movie|tv)\/\d+\/torrent-search\?/.test(path))
      throw new Error('Server 资源搜索路径无效。')
    return this.request(path)
  }

  async streamDiscoveryResources(path: string, onEvent: (event: ServerDiscoveryStreamEvent) => void): Promise<ServerDiscoveryStreamHandle> {
    if (!path.startsWith('/api/v1/player/discovery/torrent-search/stream?') && !/^\/api\/v1\/player\/discovery\/media\/(?:movie|tv)\/\d+\/torrent-search\/stream\?/.test(path))
      throw new Error('Server 流式资源搜索路径无效。')
    if (!this.bridge.stream || !this.bridge.cancelStream)
      throw new Error('当前 Player 环境不支持 Server 流式搜索。')
    const credential = await this.ensureCredential()
    const requestId = crypto.randomUUID()
    const request: ServerNativeRequest = { baseUrl: this.baseUrl, method: 'GET', path, accessToken: credential.accessToken }
    const done = this.bridge.stream(request, requestId, onEvent).catch((error) => {
      throw new Error(redactSensitiveText(error))
    })
    return {
      requestId,
      done,
      cancel: async () => {
        try {
          await this.bridge.cancelStream?.(requestId)
        }
        catch {
          // Cancellation is best effort; search generations still reject late events.
        }
      },
    }
  }

  async getDiscoveryDownloadOptions(): Promise<{ downloaders: unknown, libraries: unknown, profiles: unknown }> {
    const [downloaders, libraries, profiles] = await Promise.all([
      this.request('/api/v1/player/downloaders'),
      this.request('/api/v1/player/media-libraries'),
      this.request('/api/v1/player/classification-profiles'),
    ])
    return { downloaders, libraries, profiles }
  }

  async createDiscoveryDownload(payload: { result_token: string, downloader_id: string, media_library_id?: number, profile_id: number, priority: number, expected_tmdb_id?: number, expected_media_type?: 'movie' | 'tv' }): Promise<unknown> {
    return this.request('/api/v1/player/discovery/downloads', 'POST', payload)
  }

  async getDiscoveryFollowDefaults(tmdbId: number): Promise<unknown> {
    return this.request(`/api/v1/player/discovery/follows/defaults?media_type=tv&tmdb_id=${tmdbId}`)
  }

  async createDiscoveryFollow(payload: unknown): Promise<unknown> {
    return this.request('/api/v1/player/discovery/follows', 'POST', payload)
  }

  async loadDiscoveryArtwork(reference: string): Promise<string | undefined> {
    const route = reference.trim()
    if (!/^\/api\/v1\/discovery\/images\/(?:tmdb|douban)\/[\w-]{1,4096}$/.test(route))
      return undefined
    const credential = await this.ensureCredential()
    const path = route.replace('/api/v1/discovery/images/', '/api/v1/player/discovery/images/')
    const response = await invoke<{ mimeType: string, dataBase64: string }>('server_request_blob', { request: { baseUrl: this.baseUrl, method: 'GET', path, accessToken: credential.accessToken } })
    if (!/^image\/(?:jpeg|png|webp|avif)$/.test(response.mimeType) || !response.dataBase64)
      return undefined
    return `data:${response.mimeType};base64,${response.dataBase64}`
  }

  destroy(): void {
    this.mediaChangeWatchGeneration += 1
    this.credential = null
    this.connected = false
    this.capabilities.clear()
    this.onlineProgressContexts.clear()
  }

  clearCache(): void {}

  watchMediaChanges(listener: (change: DataSourceMediaChange) => void): () => void {
    const generation = ++this.mediaChangeWatchGeneration
    void this.runMediaChangeWatch(generation, listener)
    return () => {
      if (this.mediaChangeWatchGeneration === generation)
        this.mediaChangeWatchGeneration += 1
    }
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    return this.loadLibraries(false)
  }

  async listLibrariesForMediaChangeRefresh(): Promise<MediaLibrary[]> {
    return this.loadLibraries(true)
  }

  private async loadLibraries(requirePhysical: boolean): Promise<MediaLibrary[]> {
    const [physicalResult, onlineResult] = await Promise.allSettled([
      this.request('/api/v1/player/media-libraries'),
      this.request('/api/v1/player/online-libraries'),
    ])
    if (physicalResult.status === 'rejected' && (requirePhysical || onlineResult.status === 'rejected'))
      throw physicalResult.reason
    const physicalData = physicalResult.status === 'fulfilled' ? recordData(physicalResult.value) : {}
    const physicalList = Array.isArray(physicalData.list) ? physicalData.list : []
    const physical = physicalList.map(item => parseLibrary(item, this.baseUrl)).filter((item): item is MediaLibrary => item != null).map(item => ({ ...item, sourceId: this.id }))
    const online = onlineResult.status === 'fulfilled'
      ? parseOnlineLibraryList(onlineResult.value).filter(item => item.available).map(item => onlineLibraryToMediaLibrary(this.id, item, this.baseUrl))
      : []
    return [...physical, ...online]
  }

  async list(path = ''): Promise<MediaItem[]> {
    const value = path.trim()
    const overview = parseServerOverviewPath(value)
    if (overview?.kind === 'favorites')
      return this.listFavorites()
    if (overview?.kind === 'history')
      return (await this.listPlaybackHistory({ limit: 100 })).items
    if (overview?.kind === 'automaticCollections' || overview?.kind === 'manualCollections') {
      const source = overview.kind === 'automaticCollections' ? 'tmdb' : 'manual'
      return (await this.collections()).filter(item => item.source === source).map(item => this.mapCollection(item))
    }
    const collection = parseServerCollectionID(value)
    if (collection) {
      const data = recordData(await this.request(`/api/v1/player/collections/${encodeURIComponent(collection.collectionId)}/items`))
      return arrayRecords(data.list).map(parseItem).filter((item): item is ServerItemRecord => item != null).map(item => this.mapItem(item))
    }
    const online = parseOnlineItemID(value)
    if (online?.kind === 'library') {
      const navigation = parseOnlineNavigationList(await this.request(`/api/v1/player/online-libraries/${encodeURIComponent(online.libraryId)}/navigation`), this.baseUrl)
      return navigation.map(item => onlineNavigationToMediaItem(this.id, online.libraryId, item))
    }
    if (online?.kind === 'node') {
      const navigation = parseOnlineNavigationList(await this.request(`/api/v1/player/online-libraries/${encodeURIComponent(online.libraryId)}/navigation/${encodeURIComponent(online.nodeToken)}/children`), this.baseUrl)
      return navigation.map(item => onlineNavigationToMediaItem(this.id, online.libraryId, item))
    }
    if (online?.kind === 'feed') {
      const sections = parseOnlineFeedSections(await this.request(`/api/v1/player/online-libraries/${encodeURIComponent(online.libraryId)}/feeds/${encodeURIComponent(online.routeKey)}`))
      return sections.flatMap(section => section.items.map(item => ({
        ...onlineWorkToMediaItem(this.id, online.libraryId, item.work),
        siteActions: item.actions,
      })))
    }
    if (online?.kind === 'work' || online?.kind === 'version') {
      const work = await this.onlineWork(online.libraryId, online.workId)
      return onlineWorkToDetail(this.id, online.libraryId, work).children ?? []
    }
    if (/^\d+$/.test(value)) {
      const [categories, acquisitions] = await Promise.all([
        this.categories(value),
        this.acquisitionItems(value).catch(() => []),
      ])
      const result = categories.map(category => this.mapCategory(value, category))
      if (acquisitions.length)
        result.unshift(this.mapAcquisitionCategory(value, acquisitions.length))
      return result
    }
    const acquisitionCategory = parseServerAcquisitionCategoryID(value)
    if (acquisitionCategory)
      return this.acquisitionItems(acquisitionCategory.libraryId)
    const category = parseServerCategoryID(value)
    if (category)
      return (await this.catalog(category.libraryId, category.name, category.mediaType)).map(item => this.mapItem(item))
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
    const [physicalSections, onlineLibraries, contributionResponse] = await Promise.all([
      this.loadPhysicalHomeSections(),
      this.onlineLibraries().catch(() => []),
      this.request('/api/v1/player/home-contributions').catch(() => null),
    ])
    const onlineSections = contributionResponse == null
      ? (await Promise.all(onlineLibraries.filter(item => item.available).slice(0, 8).flatMap(library =>
          library.homeContributions.slice(0, 4).map(async (routeKey) => {
            try {
              const sections = parseOnlineFeedSections(await this.request(`/api/v1/player/online-libraries/${encodeURIComponent(library.id)}/feeds/${encodeURIComponent(routeKey)}`))
              return onlineSectionsToHomeSections(this.id, library.id, sections.filter(section => section.homeEligible), routeKey, library.providerLabel)
            }
            catch {
              return []
            }
          }),
        ))).flat()
      : parseOnlineHomeContributions(contributionResponse).flatMap((contribution) => {
          if (contribution.errorCode)
            return [onlineContributionErrorToHomeSection(this.id, contribution)]
          const eligible = contribution.sections.filter(section => section.homeEligible)
          return onlineSectionsToHomeSections(
            this.id,
            contribution.libraryId,
            eligible.length > 0 ? eligible : contribution.sections,
            contribution.routeKey,
            contribution.providerLabel,
          )
        })
    const onlineLibraryItems = onlineLibraries.filter(item => item.available).map(item => mediaLibraryToRootItem(onlineLibraryToMediaLibrary(this.id, item, this.baseUrl)))
    const sections = physicalSections.map(section => section.purpose === 'libraries'
      ? { ...section, items: [...section.items, ...onlineLibraryItems] }
      : section)
    return [...sections, ...onlineSections]
  }

  private async loadPhysicalHomeSections(): Promise<HomeSection[]> {
    if (this.hasCapability('media_overview_v1')) {
      try {
        const overview = parseServerOverview(await this.request('/api/v1/player/overview'))
        if (overview)
          return this.mapPhysicalOverview(overview)
      }
      catch {
        // A capability can be stale during rolling upgrades. Fall back to the
        // version-independent endpoint set without taking down the source.
      }
    }
    return this.loadLegacyPhysicalHomeSections()
  }

  private mapPhysicalOverview(overview: ServerOverviewRecord): HomeSection[] {
    const mappedItems = (section: ServerOverviewSectionRecord) => section.list
      .map(parseItem)
      .filter((item): item is ServerItemRecord => item != null)
      .map(item => this.mapItem(item))
    const mappedHistory = (section: ServerOverviewSectionRecord) => mapServerHistoryItems(this.id, section.list)
    const mappedCollections = (section: ServerOverviewSectionRecord) => section.list
      .map(parseCollectionRecord)
      .filter((item): item is ServerCollectionRecord => item != null)
      .map(item => this.mapCollection(item))
    const mappedLibraries = overview.sections.mediaLibraries.list
      .map(item => parseLibrary(item, this.baseUrl))
      .filter((item): item is MediaLibrary => item != null)
      .map(item => mediaLibraryToRootItem({ ...item, sourceId: this.id }))
    const sections = [
      overviewHomeSection(this.id, 'hero', 'Server 精选', 'hero', mappedItems(overview.sections.featured), overview.sections.featured),
      overviewHomeSection(this.id, 'continue', '继续观看', 'continueWatching', mappedHistory(overview.sections.continueWatching), overview.sections.continueWatching, { purpose: 'history', viewAllRoute: { kind: 'history', sourceId: this.id } }),
      overviewHomeSection(this.id, 'recent', 'Server 最近入库', 'recentlyAdded', mappedItems(overview.sections.recentlyAdded), overview.sections.recentlyAdded),
      overviewHomeSection(this.id, 'favorites', '我的收藏', 'libraryRow', mappedItems(overview.sections.favorites).map(item => ({ ...item, favorite: true })), overview.sections.favorites, { purpose: 'favorites', viewAllRoute: { kind: 'sourcePath', path: createServerOverviewPath('favorites') } }),
      overviewHomeSection(this.id, 'automatic-collections', '自动合集', 'libraryRow', mappedCollections(overview.sections.automaticCollections), overview.sections.automaticCollections, { purpose: 'automaticCollections', collectionSource: 'automatic', viewAllRoute: { kind: 'sourcePath', path: createServerOverviewPath('automaticCollections') } }),
      overviewHomeSection(this.id, 'manual-collections', '我的合集', 'libraryRow', mappedCollections(overview.sections.manualCollections), overview.sections.manualCollections, { purpose: 'manualCollections', collectionSource: 'manual', viewAllRoute: { kind: 'sourcePath', path: createServerOverviewPath('manualCollections') } }),
      overviewHomeSection(this.id, 'libraries', '媒体库', 'libraryRow', mappedLibraries, overview.sections.mediaLibraries, { purpose: 'libraries' }),
    ] satisfies HomeSection[]
    return sections.filter(section => section.purpose === 'libraries' || section.purpose === 'history' || section.items.length > 0 || Boolean(section.errorCode && section.purpose))
  }

  private async loadLegacyPhysicalHomeSections(): Promise<HomeSection[]> {
    const physicalData = recordData(await this.request('/api/v1/player/media-libraries').catch(() => null))
    const physicalLibraries = arrayRecords(physicalData.list)
      .map(item => parseLibrary(item, this.baseUrl))
      .filter((item): item is MediaLibrary => item != null)
      .map(item => ({ ...item, sourceId: this.id }))
    const [pages, historyResult, favoritesResult, collectionsResult] = await Promise.all([
      Promise.all(physicalLibraries.slice(0, 12).map(library => this.catalog(library.id).catch(() => []))),
      this.listPlaybackHistory({ limit: 24 }).catch(() => ({ items: [], hasMore: false } as ProviderPlaybackHistoryPage)),
      this.listFavorites().catch(() => []),
      this.collections().catch(() => []),
    ])
    const items = pages.flat().map(item => this.mapItem(item)).sort((a, b) => Date.parse(b.modified ?? '') - Date.parse(a.modified ?? ''))
    const continueItems = historyResult.items.filter(item => item.played !== true && (item.resumePosition ?? 0) > 0 && (item.progress ?? 0) < 0.92).slice(0, 12)
    const automaticCollections = collectionsResult.filter(item => item.source === 'tmdb').map(item => this.mapCollection(item)).slice(0, 12)
    const manualCollections = collectionsResult.filter(item => item.source === 'manual').map(item => this.mapCollection(item)).slice(0, 12)
    return ([
      { id: `${this.id}:hero`, sourceId: this.id, title: 'Server 精选', type: 'hero', items: items.filter(item => item.backdropUrl).slice(0, 12) },
      { id: `${this.id}:continue`, sourceId: this.id, title: '继续观看', type: 'continueWatching', purpose: 'history', items: continueItems, viewAllRoute: { kind: 'history', sourceId: this.id } },
      { id: `${this.id}:recent`, sourceId: this.id, title: 'Server 最近入库', type: 'recentlyAdded', items: items.slice(0, 24) },
      { id: `${this.id}:favorites`, sourceId: this.id, title: '我的收藏', type: 'libraryRow', purpose: 'favorites', items: favoritesResult.slice(0, 12), viewAllRoute: { kind: 'sourcePath', path: createServerOverviewPath('favorites') } },
      { id: `${this.id}:automatic-collections`, sourceId: this.id, title: '自动合集', type: 'libraryRow', purpose: 'automaticCollections', collectionSource: 'automatic', items: automaticCollections, viewAllRoute: { kind: 'sourcePath', path: createServerOverviewPath('automaticCollections') } },
      { id: `${this.id}:manual-collections`, sourceId: this.id, title: '我的合集', type: 'libraryRow', purpose: 'manualCollections', collectionSource: 'manual', items: manualCollections, viewAllRoute: { kind: 'sourcePath', path: createServerOverviewPath('manualCollections') } },
      { id: `${this.id}:libraries`, sourceId: this.id, title: '媒体库', type: 'libraryRow', purpose: 'libraries', items: physicalLibraries.map(library => mediaLibraryToRootItem(library)) },
    ] satisfies HomeSection[]).filter(section => section.purpose === 'libraries' || section.purpose === 'history' || section.items.length > 0)
  }

  async refreshHomeSection(refreshKey: string): Promise<HomeSection[]> {
    const [kind, libraryValue, routeValue] = refreshKey.split('|')
    if (kind !== 'online-refresh' || !libraryValue || !routeValue)
      throw new Error('在线栏目刷新标识无效。')
    let libraryId: string
    let routeKey: string
    try {
      libraryId = decodeURIComponent(libraryValue)
      routeKey = decodeURIComponent(routeValue)
    }
    catch {
      throw new Error('在线栏目刷新标识无效。')
    }
    const [sections, libraries] = await Promise.all([
      this.request(
        `/api/v1/player/online-libraries/${encodeURIComponent(libraryId)}/feeds/${encodeURIComponent(routeKey)}/refresh`,
        'POST',
        {},
      ),
      this.onlineLibraries().catch(() => []),
    ])
    const parsed = parseOnlineFeedSections(sections)
    const providerLabel = libraries.find(item => item.id === libraryId)?.providerLabel
    return onlineSectionsToHomeSections(this.id, libraryId, parsed.filter(section => section.homeEligible), routeKey, providerLabel)
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const query = keyword.trim()
    if (!query)
      return []
    const [physical, onlineLibraries] = await Promise.all([
      this.request(`/api/v1/player/search?query=${encodeURIComponent(query)}&page=1&page_size=50`).catch(() => null),
      this.onlineLibraries().catch(() => []),
    ])
    const physicalData = physical == null ? {} : recordData(physical)
    const physicalItems = arrayRecords(physicalData.list).map(parseItem).filter((item): item is ServerItemRecord => item != null).map(item => this.mapItem(item))
    const onlineItems = (await Promise.all(onlineLibraries.filter(item => item.available && item.capabilities.includes('site.search')).slice(0, 8).map(async (library) => {
      try {
        const sections = parseOnlineFeedSections(await this.request(`/api/v1/player/online-libraries/${encodeURIComponent(library.id)}/search?q=${encodeURIComponent(query)}`))
        return sections.flatMap(section => section.items.map(item => onlineWorkToMediaItem(this.id, library.id, item.work)))
      }
      catch {
        return []
      }
    }))).flat()
    return [...physicalItems, ...onlineItems]
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const online = parseOnlineItemID(id)
    if (online?.kind === 'work' || online?.kind === 'version')
      return onlineWorkToDetail(this.id, online.libraryId, await this.onlineWork(online.libraryId, online.workId))
    const acquisition = parseServerAcquisitionItemID(id)
    if (acquisition) {
      const [detail, status] = await Promise.all([
        getServerDiscoveryDetail(this, 'tmdb', acquisition.mediaType, String(acquisition.tmdbId)),
        getServerAcquisition(this, acquisition.mediaType, acquisition.tmdbId),
      ])
      return {
        id,
        sourceId: this.id,
        originType: 'server',
        libraryId: acquisition.libraryId,
        name: detail.work.title,
        originalTitle: detail.work.originalTitle,
        type: acquisition.mediaType === 'tv' ? 'series' : 'movie',
        posterUrl: detail.work.posterUrl,
        backdropUrl: detail.work.backdropUrl,
        year: detail.work.year,
        rating: detail.work.rating,
        overview: detail.work.overview,
        tagline: detail.tagline,
        duration: detail.runtimeMinutes ? detail.runtimeMinutes * 60 : undefined,
        genres: detail.genres,
        directors: detail.directors,
        cast: detail.cast,
        tmdbId: acquisition.tmdbId,
        path: '',
        workIdentity: { scheme: 'tmdb', mediaType: acquisition.mediaType === 'tv' ? 'series' : 'movie', value: String(acquisition.tmdbId) },
        acquisition: acquisitionState(status),
        mediaSources: [],
        children: [],
      }
    }
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
      people: detail.item.people?.map(person => ({
        id: person.tmdb_id ? String(person.tmdb_id) : undefined,
        name: person.name,
        role: person.role,
        character: person.character,
        imageUrl: artwork(person.profile_path, 'w500'),
      })),
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

  async enqueueOnlineDownload(request: PlaybackRequest & { readonly mediaLibraryId?: number }): Promise<void> {
    const online = parseOnlineItemID(request.itemId)
    if (online?.kind !== 'work' && online?.kind !== 'version')
      throw new Error('当前 Server 媒体不是可下载的在线插件内容。')
    const work = await this.onlineWork(online.libraryId, online.workId)
    const candidates = work.segments.flatMap(segment => segment.versions.map(version => ({ segment, version })))
    const selected = online.kind === 'version'
      ? candidates.find(item => item.segment.id === online.segmentId && item.version.id === online.versionId)
      : candidates.find(item => item.version.id === request.mediaSourceId)
        ?? candidates.find(item => versionAvailable(item.version, request.variantId))
    if (!selected)
      throw new Error('请选择可下载的媒体版本。')
    const variantId = request.variantId ?? selected.version.variants.find(variant => variant.available)?.id
    await this.request(
      `/api/v1/player/online-libraries/${encodeURIComponent(online.libraryId)}/items/${encodeURIComponent(online.workId)}/download`,
      'POST',
      {
        segmentId: selected.segment.id,
        versionId: selected.version.id,
        ...(variantId ? { variantId } : {}),
        mediaLibraryId: request.mediaLibraryId ?? 0,
        displayName: work.title,
      },
    )
  }

  async performSiteAction(itemId: string, action: SiteActionKey, value?: boolean, confirmed = false): Promise<void> {
    const online = parseOnlineItemID(itemId)
    if (online?.kind !== 'work' && online?.kind !== 'version')
      throw new Error('当前媒体不支持站点操作。')
    await this.request(
      `/api/v1/player/online-libraries/${encodeURIComponent(online.libraryId)}/items/${encodeURIComponent(online.workId)}/actions/${encodeURIComponent(action)}`,
      'POST',
      {
        ...(online.kind === 'version' ? { segmentId: online.segmentId, versionId: online.versionId } : {}),
        ...(value != null ? { value } : {}),
        confirmed,
        idempotencyKey: crypto.randomUUID(),
      },
    )
  }

  async getStreamRequest(request: PlaybackRequest): Promise<MediaStreamRequest> {
    const online = parseOnlineItemID(request.itemId)
    if (online?.kind === 'work' || online?.kind === 'version') {
      const work = await this.onlineWork(online.libraryId, online.workId)
      const requestedVersion = online.kind === 'version'
        ? work.segments.flatMap(segment => segment.versions.map(version => ({ segment, version }))).find(item => item.segment.id === online.segmentId && item.version.id === online.versionId)
        : work.segments.flatMap(segment => segment.versions.map(version => ({ segment, version }))).find(item => versionAvailable(item.version, request.variantId))
      if (!requestedVersion)
        throw new Error('请选择可播放的在线媒体版本。')
      const variantId = request.variantId
        ?? requestedVersion.version.variants.find(variant => variant.available)?.id
      const body = {
        segmentId: requestedVersion.segment.id,
        versionId: requestedVersion.version.id,
        ...(variantId ? { variantId } : {}),
      }
      const plan = parseOnlinePlaybackPlan(await this.request(
        `/api/v1/player/online-libraries/${encodeURIComponent(online.libraryId)}/items/${encodeURIComponent(online.workId)}/playback`,
        'POST',
        body,
      ))
      if (!plan)
        throw new Error('Server 返回的在线媒体播放方案无效。')
      const credential = await this.ensureCredential()
      this.rememberOnlineProgressContext(request.itemId, plan.versionId, {
        libraryId: online.libraryId,
        workId: online.workId,
        segmentId: plan.segmentId,
        versionId: plan.versionId,
      })
      return onlinePlaybackToStreamRequest(this.baseUrl, credential.accessToken, plan)
    }
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

  async listPlaybackHistory(request: ProviderPlaybackHistoryRequest = {}): Promise<ProviderPlaybackHistoryPage> {
    if (request.libraryId) {
      const onlineLibrary = parseOnlineItemID(request.libraryId)
      if (onlineLibrary?.kind !== 'library')
        throw new Error('在线媒体库历史来源无效。')
      const parameters = new URLSearchParams()
      parameters.set('library_id', onlineLibrary.libraryId)
      if (request.cursor)
        parameters.set('cursor', request.cursor)
      parameters.set('page_size', String(Math.max(1, Math.min(100, request.limit ?? 24))))
      return parseOnlineHistoryPage(this.id, await this.request(`/api/v1/player/online-history?${parameters.toString()}`))
    }
    const page = request.cursor && /^\d{1,6}$/.test(request.cursor) ? Math.max(1, Number.parseInt(request.cursor, 10)) : 1
    const pageSize = Math.max(1, Math.min(100, request.limit ?? 24))
    return parseServerHistoryPage(this.id, await this.request(`/api/v1/player/history?page=${page}&page_size=${pageSize}&source_kind=server`), page)
  }

  async setFavorite(itemId: string, favorite: boolean): Promise<void> {
    const stateID = createPlayerMediaStateItemID(itemId)
    await this.request(`/api/v1/player/favorites/${encodeURIComponent(stateID)}`, 'PUT', { favorite })
  }

  async listFavorites(): Promise<MediaItem[]> {
    const data = recordData(await this.request('/api/v1/player/favorites'))
    return arrayRecords(data.list).map(parseItem).filter((item): item is ServerItemRecord => item != null).map(item => ({ ...this.mapItem(item), favorite: true }))
  }

  async getFavoriteState(itemId: string): Promise<boolean> {
    const stateID = createPlayerMediaStateItemID(itemId)
    const data = recordData(await this.request(`/api/v1/player/favorites/${encodeURIComponent(stateID)}`))
    return data.favorite === true
  }

  async listProviderCollections(kind: 'playlist' | 'collection'): Promise<ProviderCollectionOption[]> {
    return (await this.collections(kind))
      .filter(item => item.source === 'manual')
      .map(item => ({ id: item.id, name: item.name, kind: item.kind, itemCount: item.itemCount }))
  }

  async createProviderCollection(name: string, kind: 'playlist' | 'collection'): Promise<string> {
    const data = recordData(await this.request('/api/v1/player/collections', 'POST', { name, kind }))
    const id = optionalString(data.id)
    if (!id)
      throw new Error('Server 返回的合集标识无效。')
    return id
  }

  async addProviderCollectionMember(collectionId: string, itemId: string, kind: 'playlist' | 'collection'): Promise<void> {
    if (!collectionId || (kind !== 'collection' && kind !== 'playlist'))
      throw new Error('Server 合集参数无效。')
    const stateID = createPlayerMediaStateItemID(itemId)
    await this.request(`/api/v1/player/collections/${encodeURIComponent(collectionId)}/items`, 'POST', { item_id: stateID })
  }

  async syncPlaybackProgress(progress: ProviderPlaybackProgressInput): Promise<void> {
    const online = parseOnlineItemID(progress.itemId)
    const context = online?.kind === 'version'
      ? { libraryId: online.libraryId, workId: online.workId, segmentId: online.segmentId, versionId: online.versionId }
      : this.onlineProgressContexts.get(onlineProgressContextKey(progress.itemId, progress.mediaSourceId))
    if (!context)
      return
    await this.request(
      `/api/v1/player/online-libraries/${encodeURIComponent(context.libraryId)}/items/${encodeURIComponent(context.workId)}/progress`,
      'POST',
      {
        segmentId: context.segmentId,
        versionId: context.versionId,
        event: progress.event,
        positionSeconds: Math.max(0, progress.position),
        ...(progress.duration != null ? { durationSeconds: Math.max(0, progress.duration) } : {}),
        idempotencyKey: crypto.randomUUID(),
        occurredAt: new Date().toISOString(),
      },
    )
  }

  async getDanmakuComments(track: PlaybackDanmakuTrack): Promise<ProviderDanmakuComment[]> {
    const url = new URL(track.url)
    if (url.origin !== new URL(this.baseUrl).origin || !url.pathname.startsWith('/api/v1/player/online-assets/') || url.username || url.password)
      throw new Error('在线弹幕轨道未通过 Server 安全网关。')
    const credential = await this.ensureCredential()
    try {
      const response = await this.bridge.request({
        baseUrl: this.baseUrl,
        method: 'GET',
        path: `${url.pathname}${url.search}`,
        accessToken: credential.accessToken,
      })
      if (response.status < 200 || response.status >= 300)
        throw new Error(`在线弹幕加载失败（HTTP ${response.status}）`)
      return parseProviderDanmakuComments(response.body)
    }
    catch (error) {
      throw new Error(redactSensitiveText(error))
    }
  }

  exportConfig(): DataSourceConfig {
    return sanitizeServerConfig(this.config)
  }

  private async catalog(libraryID: string, category?: string, mediaType?: 'movie' | 'series'): Promise<ServerItemRecord[]> {
    const query = category ? `?category=${encodeURIComponent(category)}&media_type=${encodeURIComponent(mediaType ?? '')}` : ''
    return this.pagedItems(`/api/v1/player/media-libraries/${encodeURIComponent(libraryID)}/catalog${query}`)
  }

  private async collections(kind: 'playlist' | 'collection' = 'collection'): Promise<ServerCollectionRecord[]> {
    const data = recordData(await this.request(`/api/v1/player/collections?kind=${encodeURIComponent(kind)}`))
    return arrayRecords(data.list).map(parseCollectionRecord).filter((item): item is ServerCollectionRecord => item?.kind === kind)
  }

  private mapCollection(collection: ServerCollectionRecord): MediaItem {
    return {
      id: createServerCollectionID(collection.id),
      sourceId: this.id,
      originType: 'server',
      name: collection.name,
      type: 'folder',
      posterUrl: artwork(collection.posterPath, 'w500'),
      backdropUrl: artwork(collection.backdropPath ?? collection.posterPath, 'w1280'),
      displaySubtitle: `${collection.itemCount} 部影片`,
      path: '',
    }
  }

  private async categories(libraryID: string): Promise<ServerCategoryRecord[]> {
    const data = recordData(await this.request(`/api/v1/player/media-libraries/${encodeURIComponent(libraryID)}/categories`))
    return arrayRecords(data.list).flatMap((value): ServerCategoryRecord[] => {
      if (!isRecord(value))
        return []
      const id = optionalString(value.id)
      const name = optionalString(value.name)
      const mediaType = value.media_type === 'movie' || value.media_type === 'series' ? value.media_type : null
      if (!id || !name || !mediaType)
        return []
      return [{
        id,
        name,
        media_type: mediaType,
        item_count: numberValue(value.item_count) ?? 0,
        artwork_url: optionalString(value.artwork_url),
        artwork_revision: optionalString(value.artwork_revision),
        artwork_source: parseArtworkSource(value.artwork_source),
      }]
    })
  }

  private mapCategory(libraryID: string, category: ServerCategoryRecord): MediaItem {
    return {
      id: createServerCategoryID(libraryID, category.media_type, category.name),
      sourceId: this.id,
      originType: 'server',
      libraryId: libraryID,
      name: category.name,
      type: 'folder',
      posterUrl: resolveServerArtworkURL(this.baseUrl, category.artwork_url),
      backdropUrl: resolveServerArtworkURL(this.baseUrl, category.artwork_url),
      artworkRevision: category.artwork_revision,
      artworkSource: category.artwork_source,
      path: '',
    }
  }

  private mapAcquisitionCategory(libraryID: string, count: number): MediaItem {
    return {
      id: createServerAcquisitionCategoryID(libraryID),
      sourceId: this.id,
      originType: 'server',
      libraryId: libraryID,
      name: '正在入库',
      type: 'folder',
      overview: `${count} 个任务正在下载、整理或等待处理`,
      artworkSource: 'fallback',
      path: '',
    }
  }

  private async acquisitionItems(libraryID: string): Promise<MediaItem[]> {
    const numericLibraryID = Number.parseInt(libraryID, 10)
    if (!Number.isSafeInteger(numericLibraryID) || numericLibraryID <= 0)
      return []
    const records = new Map<string, ServerAcquisitionRecord>()
    for (let page = 1; page <= 4; page++) {
      const data = recordData(await this.getDiscoveryAcquisitions(page, 100))
      const list = arrayRecords(data.list).map(parseAcquisitionRecord).filter((item): item is ServerAcquisitionRecord => item != null)
      for (const item of list) {
        if (item.targetLibraryId === numericLibraryID && !isTerminalAcquisitionRecord(item))
          records.set(`${item.mediaType}:${item.tmdbId}`, item)
      }
      const total = numberValue(data.total) ?? list.length
      if (page * 100 >= total || list.length < 100)
        break
    }
    return [...records.values()]
      .sort((left, right) => (Date.parse(right.updatedAt ?? '') || 0) - (Date.parse(left.updatedAt ?? '') || 0))
      .map(item => ({
        id: createServerAcquisitionItemID(libraryID, item.mediaType, item.tmdbId),
        sourceId: this.id,
        originType: 'server' as const,
        libraryId: libraryID,
        name: item.title || `TMDB ${item.tmdbId}`,
        type: item.mediaType === 'tv' ? 'series' as const : 'movie' as const,
        modified: item.updatedAt,
        path: '',
        workIdentity: { scheme: 'tmdb' as const, mediaType: item.mediaType === 'tv' ? 'series' as const : 'movie' as const, value: String(item.tmdbId) },
        acquisition: acquisitionState(item),
      }))
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

  private async onlineLibraries() {
    return parseOnlineLibraryList(await this.request('/api/v1/player/online-libraries'))
  }

  private async onlineWork(libraryID: string, workID: string) {
    const work = parseOnlineWork(await this.request(`/api/v1/player/online-libraries/${encodeURIComponent(libraryID)}/items/${encodeURIComponent(workID)}`))
    if (!work)
      throw new Error('Server 返回的在线媒体详情无效。')
    return work
  }

  private mapItem(item: ServerItemRecord): MediaItem {
    const itemID = item.item_token ?? createWorkItemID(String(item.library_id), item.id)
    return {
      id: itemID,
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
      historyIdentity: item.kind === 'movie' ? item.history_identity ?? createMovieHistoryIdentity(String(item.library_id), item.id) : undefined,
      workIdentity: mapIdentity(item.work_identity),
      playbackTargets: [{ sourceId: this.id, itemId: itemID, label: this.name }],
    }
  }

  private mapVersion(item: ServerItemRecord, version: ServerVersionRecord, work: WorkItemID): MediaItem {
    const id = version.item_token ?? createEntryItemID(work.libraryId, work.workId, version.id)
    const isEpisode = item.kind === 'series'
    const episodeArtwork = isEpisode ? artwork(version.episode_still_path ?? version.still_path, 'w1280') : undefined
    const runtimeMinutes = isEpisode ? version.runtime_minutes : item.runtime_minutes
    return {
      id,
      sourceId: this.id,
      originType: 'server',
      libraryId: work.libraryId,
      name: version.title,
      originalTitle: isEpisode ? undefined : item.original_title,
      type: isEpisode ? 'episode' : 'movie',
      posterUrl: artwork(version.poster_path ?? item.poster_path, 'w500'),
      backdropUrl: artwork(version.backdrop_path ?? item.backdrop_path, 'w1280'),
      episodeStillUrl: episodeArtwork,
      year: item.release_year,
      rating: isEpisode ? version.rating : item.rating,
      overview: isEpisode ? version.overview : item.overview,
      tagline: isEpisode ? undefined : item.tagline,
      duration: runtimeMinutes ? runtimeMinutes * 60 : undefined,
      size: version.size,
      modified: version.modified_at,
      path: '',
      seasonNumber: version.season,
      episodeNumber: version.episode,
      seriesName: item.kind === 'series' ? version.series_title ?? item.title : undefined,
      historyIdentity: version.history_identity
        ?? (isEpisode && version.season != null && version.episode != null
          ? createEpisodeHistoryIdentity(work.libraryId, work.workId, version.season, version.episode)
          : !isEpisode ? item.history_identity ?? createMovieHistoryIdentity(work.libraryId, work.workId) : undefined),
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
        throw new ServerRequestError(redactSensitiveText(message), response.status, typeof envelope.code === 'string' ? envelope.code : undefined)
      }
      return envelope.data
    }
    catch (error) {
      if (error instanceof ServerRequestError)
        throw error
      throw new Error(redactSensitiveText(error))
    }
  }

  private async runMediaChangeWatch(generation: number, listener: (change: DataSourceMediaChange) => void): Promise<void> {
    const cursorKey = `ohmycine:server-media-change-cursor:${this.id}:${encodeURIComponent(this.baseUrl)}`
    let cursor = safeServerChangeCursor(getAppSetting(cursorKey))
    let retryDelay = 1_000
    while (this.connected && this.mediaChangeWatchGeneration === generation) {
      try {
        const page = parseServerMediaChangePage(await this.request(`/api/v1/player/media-changes?cursor=${encodeURIComponent(cursor)}&wait_seconds=12`))
        if (this.mediaChangeWatchGeneration !== generation)
          return
        if (!page.resyncRequired && compareServerChangeCursors(page.cursor, cursor) < 0)
          throw new Error('Server 媒体变更游标发生了无效回退。')
        if (page.changeCount > 0 && page.cursor === cursor)
          throw new Error('Server 媒体变更响应未推进游标。')
        if (page.resyncRequired || page.libraryIds.length > 0) {
          listener({ sourceId: this.id, libraryIds: page.libraryIds, libraryRevisions: page.libraryRevisions, resyncRequired: page.resyncRequired })
        }
        if (this.mediaChangeWatchGeneration !== generation)
          return
        if (page.cursor !== cursor) {
          cursor = page.cursor
          await setAppSetting(cursorKey, cursor)
        }
        retryDelay = 1_000
      }
      catch (error) {
        if (error instanceof ServerRequestError && (error.status === 401 || error.status === 403)) {
          this.connected = false
          return
        }
        if (error instanceof ServerRequestError && error.status === 404)
          return
        await waitForMediaChangeRetry(withMediaChangeJitter(retryDelay), () => this.connected && this.mediaChangeWatchGeneration === generation)
        retryDelay = Math.min(retryDelay * 2, 15_000)
      }
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

  private rememberOnlineProgressContext(itemId: string, mediaSourceId: string, context: ServerOnlineProgressContext): void {
    const key = onlineProgressContextKey(itemId, mediaSourceId)
    this.onlineProgressContexts.delete(key)
    this.onlineProgressContexts.set(key, context)
    while (this.onlineProgressContexts.size > SERVER_ONLINE_PROGRESS_CONTEXT_LIMIT) {
      const oldest = this.onlineProgressContexts.keys().next().value
      if (typeof oldest !== 'string')
        break
      this.onlineProgressContexts.delete(oldest)
    }
  }
}

export interface ServerPlaybackHistoryChange {
  sync_key: string
  source_kind: string
  source_name?: string
  source_locator?: string
  source_id: string
  library_id?: string
  item_id?: string
  media_identity: string
  history_identity?: string
  item_token?: string
  title: string
  display_title?: string
  display_subtitle?: string
  series_title?: string
  episode_title?: string
  season_number?: number
  episode_number?: number
  stream_identity?: string
  media_type?: string
  poster_url?: string
  backdrop_url?: string
  title_logo_url?: string
  episode_still_url?: string
  poster_path?: string
  backdrop_path?: string
  episode_still_path?: string
  position: number
  duration?: number
  completed: boolean
  deleted?: boolean
  updated_at: number
  revision?: number
}

export interface ServerPlaybackHistorySyncRequest { cursor: number, changes: ServerPlaybackHistoryChange[] }
export interface ServerPlaybackHistoryRejection { sync_key?: string, code: string }
export interface ServerPlaybackHistorySyncResponse { cursor: number, changes: ServerPlaybackHistoryChange[], rejected: ServerPlaybackHistoryRejection[] }

export function parsePlaybackHistorySyncResponse(value: unknown): ServerPlaybackHistorySyncResponse {
  const data = recordData(value)
  if (!Number.isSafeInteger(data.cursor) || Number(data.cursor) < 0 || !Array.isArray(data.changes) || data.changes.length > 500)
    throw new Error('Server 播放历史同步响应无效。')
  const changes = data.changes.map(parsePlaybackHistoryChange)
  const rawRejected = data.rejected ?? []
  if (!Array.isArray(rawRejected) || rawRejected.length > 500)
    throw new Error('Server 播放历史同步响应无效。')
  const rejected = rawRejected.map((raw): ServerPlaybackHistoryRejection => {
    if (!isRecord(raw) || typeof raw.code !== 'string' || !/^[A-Z][A-Z0-9_]{0,63}$/.test(raw.code)
      || (raw.sync_key !== undefined && (typeof raw.sync_key !== 'string' || !/^[a-f0-9]{64}$/.test(raw.sync_key)))) {
      throw new Error('Server 播放历史同步响应无效。')
    }
    return { code: raw.code, ...(typeof raw.sync_key === 'string' ? { sync_key: raw.sync_key } : {}) }
  })
  return { cursor: Number(data.cursor), changes, rejected }
}

function parsePlaybackHistoryChange(raw: unknown): ServerPlaybackHistoryChange {
  if (!isRecord(raw) || typeof raw.sync_key !== 'string' || !/^[a-f0-9]{64}$/.test(raw.sync_key)
    || typeof raw.source_kind !== 'string' || !raw.source_kind || typeof raw.source_id !== 'string' || !raw.source_id
    || typeof raw.media_identity !== 'string' || !raw.media_identity || typeof raw.title !== 'string' || !raw.title
    || typeof raw.position !== 'number' || !Number.isFinite(raw.position) || raw.position < 0
    || typeof raw.completed !== 'boolean'
    || typeof raw.updated_at !== 'number' || !Number.isSafeInteger(raw.updated_at) || raw.updated_at <= 0
    || (raw.duration !== undefined && (typeof raw.duration !== 'number' || !Number.isFinite(raw.duration) || raw.duration < 0))
    || (raw.deleted !== undefined && typeof raw.deleted !== 'boolean')
    || (raw.revision !== undefined && (!Number.isSafeInteger(raw.revision) || Number(raw.revision) < 0))
    || (raw.season_number !== undefined && !Number.isSafeInteger(raw.season_number))
    || (raw.episode_number !== undefined && !Number.isSafeInteger(raw.episode_number))) {
    throw new Error('Server 播放历史同步响应无效。')
  }
  const optionalStrings = [
    'source_name',
    'source_locator',
    'library_id',
    'item_id',
    'history_identity',
    'item_token',
    'display_title',
    'display_subtitle',
    'series_title',
    'episode_title',
    'stream_identity',
    'media_type',
    'poster_url',
    'backdrop_url',
    'title_logo_url',
    'episode_still_url',
    'poster_path',
    'backdrop_path',
    'episode_still_path',
  ]
  if (optionalStrings.some(key => raw[key] !== undefined && typeof raw[key] !== 'string'))
    throw new Error('Server 播放历史同步响应无效。')
  return raw as unknown as ServerPlaybackHistoryChange
}

function safeServerChangeCursor(value: string | null): string {
  return value && isServerChangeCursor(value) ? value : '0'
}

function parseServerMediaChangePage(value: unknown): ServerMediaChangePage {
  if (!isRecord(value)
    || typeof value.cursor !== 'string'
    || !isServerChangeCursor(value.cursor)
    || typeof value.resync_required !== 'boolean'
    || !Array.isArray(value.changes)
    || value.changes.length > 256) {
    throw new Error('Server 媒体变更响应无效。')
  }
  const libraryRevisions: Record<string, number> = {}
  for (const item of value.changes) {
    if (!isRecord(item))
      throw new Error('Server 媒体变更响应无效。')
    const libraryId = item.library_id
    const revision = item.content_revision
    if (!Number.isSafeInteger(libraryId) || Number(libraryId) <= 0
      || !Number.isSafeInteger(revision) || Number(revision) <= 0
      || typeof item.kind !== 'string'
      || !['catalog', 'metadata', 'removal'].includes(item.kind)
      || typeof item.changed_at !== 'string'
      || item.changed_at.length === 0
      || item.changed_at.length > 64
      || !Number.isFinite(Date.parse(item.changed_at))) {
      throw new Error('Server 媒体变更响应无效。')
    }
    const key = String(Number(libraryId))
    libraryRevisions[key] = Math.max(libraryRevisions[key] ?? 0, Number(revision))
  }
  return {
    cursor: value.cursor,
    resyncRequired: value.resync_required,
    libraryIds: Object.keys(libraryRevisions),
    libraryRevisions,
    changeCount: value.changes.length,
  }
}

function isServerChangeCursor(value: string): boolean {
  if (!/^\d{1,20}$/.test(value))
    return false
  try {
    return BigInt(value) <= 18_446_744_073_709_551_615n
  }
  catch {
    return false
  }
}

function compareServerChangeCursors(left: string, right: string): number {
  const leftValue = BigInt(left)
  const rightValue = BigInt(right)
  return leftValue < rightValue ? -1 : leftValue > rightValue ? 1 : 0
}

function withMediaChangeJitter(delay: number): number {
  return Math.max(250, Math.round(delay * (0.85 + Math.random() * 0.3)))
}

function waitForMediaChangeRetry(delay: number, isActive: () => boolean): Promise<void> {
  return new Promise((resolve) => {
    const startedAt = Date.now()
    const check = () => {
      if (!isActive() || Date.now() - startedAt >= delay) {
        resolve()
        return
      }
      globalThis.setTimeout(check, Math.min(250, delay - (Date.now() - startedAt)))
    }
    check()
  })
}

function onlineProgressContextKey(itemId: string, mediaSourceId: string | undefined): string {
  return `${itemId}\u0000${mediaSourceId ?? ''}`
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
      config: { ...config, extra: { ...config.extra, capabilities: source.capabilityCodes, libraries: libraries.map(library => ({ id: library.id, name: library.name, type: library.type })) } },
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

function parseLibrary(value: unknown, baseUrl: string): MediaLibrary | null {
  if (!isRecord(value) || typeof value.id !== 'number' || typeof value.name !== 'string')
    return null
  const record = value as unknown as ServerLibraryRecord
  const artworkUrl = resolveServerArtworkURL(baseUrl, record.artwork_url)
  return {
    id: String(record.id),
    sourceId: '',
    name: record.name,
    type: 'mixed',
    posterUrl: artworkUrl,
    backdropUrl: artworkUrl,
    artworkRevision: optionalString(record.artwork_revision),
    artworkSource: parseArtworkSource(record.artwork_source),
    itemCount: numberValue(record.work_count) ?? numberValue(record.entry_count),
  }
}

function parseCollectionRecord(value: unknown): ServerCollectionRecord | null {
  if (!isRecord(value) || (value.source !== 'tmdb' && value.source !== 'manual'))
    return null
  const id = optionalString(value.id)
  const name = optionalString(value.name)
  const kind = value.kind === 'collection' || value.kind === 'playlist' ? value.kind : null
  if (!id || !name || !kind)
    return null
  return {
    id,
    name,
    kind,
    source: value.source,
    itemCount: boundedNumber(value.item_count, 0, SERVER_MAX_ITEMS) ?? 0,
    posterPath: optionalImagePath(value.poster_path),
    backdropPath: optionalImagePath(value.backdrop_path),
  }
}

function parseServerOverview(value: unknown): ServerOverviewRecord | null {
  if (!isRecord(value) || value.version !== 'v1' || !isRecord(value.sections))
    return null
  const sections = value.sections
  const featured = parseServerOverviewSection(sections.featured)
  const continueWatching = parseServerOverviewSection(sections.continue_watching)
  const recentlyAdded = parseServerOverviewSection(sections.recently_added)
  const favorites = parseServerOverviewSection(sections.favorites)
  const automaticCollections = parseServerOverviewSection(sections.automatic_collections)
  const manualCollections = parseServerOverviewSection(sections.manual_collections)
  const recentHistory = parseServerOverviewSection(sections.recent_history)
  const mediaLibraries = parseServerOverviewSection(sections.media_libraries)
  if (!featured || !continueWatching || !recentlyAdded || !favorites || !automaticCollections || !manualCollections || !recentHistory || !mediaLibraries)
    return null
  return {
    version: 'v1',
    sections: { featured, continueWatching, recentlyAdded, favorites, automaticCollections, manualCollections, recentHistory, mediaLibraries },
  }
}

function parseServerOverviewSection(value: unknown): ServerOverviewSectionRecord | null {
  if (!isRecord(value) || (value.status !== 'ok' && value.status !== 'unavailable') || !Array.isArray(value.list) || value.list.length > 100 || typeof value.has_more !== 'boolean')
    return null
  const errorCode = optionalString(value.error_code)
  return {
    status: value.status,
    list: value.list,
    hasMore: value.has_more,
    errorCode: errorCode && /^[A-Z][A-Z0-9_]{0,63}$/.test(errorCode) ? errorCode : undefined,
  }
}

function overviewHomeSection(
  sourceId: string,
  id: string,
  title: string,
  type: HomeSection['type'],
  items: MediaItem[],
  section: ServerOverviewSectionRecord,
  extra: Pick<HomeSection, 'purpose' | 'collectionSource' | 'viewAllRoute'> = {},
): HomeSection {
  return {
    id: `${sourceId}:${id}`,
    sourceId,
    title,
    type,
    items,
    ...extra,
    errorCode: section.status === 'unavailable' ? section.errorCode ?? 'UNAVAILABLE' : undefined,
  }
}

function parseArtworkSource(value: unknown): MediaLibrary['artworkSource'] {
  return ['generated', 'provider', 'custom', 'fallback'].includes(String(value)) ? value as MediaLibrary['artworkSource'] : undefined
}

function createServerCategoryID(libraryId: string, mediaType: 'movie' | 'series', name: string): string {
  return ['server-category', libraryId, mediaType, name].map((value, index) => index === 0 ? value : encodeURIComponent(value)).join('|')
}

type ServerOverviewPathKind = 'favorites' | 'automaticCollections' | 'manualCollections' | 'history'

function createServerOverviewPath(kind: ServerOverviewPathKind): string {
  return `server-overview|${kind}`
}

function parseServerOverviewPath(value: string): { kind: ServerOverviewPathKind } | null {
  const [prefix, kind, ...rest] = value.split('|')
  return prefix === 'server-overview'
    && rest.length === 0
    && (kind === 'favorites' || kind === 'automaticCollections' || kind === 'manualCollections' || kind === 'history')
    ? { kind }
    : null
}

function createServerCollectionID(collectionId: string): string {
  return `server-collection|${encodeURIComponent(collectionId)}`
}

function parseServerCollectionID(value: string): { collectionId: string } | null {
  const [prefix, rawCollectionId, ...rest] = value.split('|')
  if (prefix !== 'server-collection' || !rawCollectionId || rest.length > 0)
    return null
  try {
    const collectionId = decodeURIComponent(rawCollectionId)
    return /^[0-9a-f-]{36}$/i.test(collectionId) ? { collectionId } : null
  }
  catch {
    return null
  }
}

function mediaLibraryToRootItem(library: MediaLibrary): MediaItem {
  return {
    id: library.id,
    sourceId: library.sourceId,
    libraryId: library.id,
    name: library.name,
    type: 'folder',
    posterUrl: library.posterUrl,
    backdropUrl: library.backdropUrl,
    artworkRevision: library.artworkRevision,
    artworkSource: library.artworkSource,
    displaySubtitle: library.itemCount == null ? '媒体库' : `${library.itemCount} 项内容`,
    path: '',
  }
}

function createServerAcquisitionCategoryID(libraryId: string): string {
  return `server-acquisition-category|${encodeURIComponent(libraryId)}`
}

function parseServerAcquisitionCategoryID(value: string): { libraryId: string } | null {
  const [kind, rawLibraryId, ...rest] = value.split('|')
  if (kind !== 'server-acquisition-category' || !rawLibraryId || rest.length > 0)
    return null
  try {
    const libraryId = decodeURIComponent(rawLibraryId)
    return /^\d+$/.test(libraryId) ? { libraryId } : null
  }
  catch {
    return null
  }
}

function createServerAcquisitionItemID(libraryId: string, mediaType: 'movie' | 'tv', tmdbId: number): string {
  return `server-acquisition|${encodeURIComponent(libraryId)}|${mediaType}|${tmdbId}`
}

function parseServerAcquisitionItemID(value: string): { libraryId: string, mediaType: 'movie' | 'tv', tmdbId: number } | null {
  const [kind, rawLibraryId, rawMediaType, rawTMDBId, ...rest] = value.split('|')
  if (kind !== 'server-acquisition' || !rawLibraryId || (rawMediaType !== 'movie' && rawMediaType !== 'tv') || !rawTMDBId || rest.length > 0)
    return null
  try {
    const libraryId = decodeURIComponent(rawLibraryId)
    const tmdbId = Number.parseInt(rawTMDBId, 10)
    return /^\d+$/.test(libraryId) && /^\d+$/.test(rawTMDBId) && Number.isSafeInteger(tmdbId) && tmdbId > 0
      ? { libraryId, mediaType: rawMediaType, tmdbId }
      : null
  }
  catch {
    return null
  }
}

function parseServerCategoryID(value: string): { libraryId: string, mediaType: 'movie' | 'series', name: string } | null {
  const [kind, rawLibraryId, rawMediaType, rawName, ...rest] = value.split('|')
  if (kind !== 'server-category' || !rawLibraryId || !rawMediaType || !rawName || rest.length > 0)
    return null
  try {
    const libraryId = decodeURIComponent(rawLibraryId)
    const mediaType = decodeURIComponent(rawMediaType)
    const name = decodeURIComponent(rawName)
    if (!/^\d+$/.test(libraryId) || (mediaType !== 'movie' && mediaType !== 'series') || !name)
      return null
    return { libraryId, mediaType, name }
  }
  catch {
    return null
  }
}

function parseItem(value: unknown): ServerItemRecord | null {
  if (!isRecord(value) || typeof value.id !== 'string' || typeof value.library_id !== 'number' || typeof value.title !== 'string' || (value.kind !== 'movie' && value.kind !== 'series') || !isRecord(value.work_identity))
    return null
  const identity = value.work_identity
  if ((identity.scheme !== 'tmdb' && identity.scheme !== 'server') || (identity.media_type !== 'movie' && identity.media_type !== 'series') || typeof identity.value !== 'string')
    return null
  return {
    id: value.id,
    item_token: optionalString(value.item_token),
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
    people: personList(value.people, 100),
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
    history_identity: optionalHistoryIdentity(value.history_identity),
  }
}

function parseAcquisitionRecord(value: unknown): ServerAcquisitionRecord | null {
  if (!isRecord(value) || typeof value.id !== 'string' || !value.id.trim() || (value.media_type !== 'movie' && value.media_type !== 'tv'))
    return null
  const tmdbId = numberValue(value.tmdb_id)
  const stage = optionalString(value.stage)
  const status = optionalString(value.status)
  if (!tmdbId || !Number.isSafeInteger(tmdbId) || !stage || !status)
    return null
  return {
    id: value.id,
    title: optionalString(value.title),
    mediaType: value.media_type,
    tmdbId,
    stage,
    status,
    targetLibraryId: numberValue(value.target_library_id),
    progress: numberValue(value.progress),
    processedFiles: numberValue(value.processed_files) ?? 0,
    totalFiles: numberValue(value.total_files) ?? 0,
    lastErrorCode: optionalString(value.last_error_code),
    updatedAt: optionalString(value.updated_at),
  }
}

function isTerminalAcquisitionRecord(value: ServerAcquisitionRecord): boolean {
  return value.stage === 'idle' || value.stage === 'library' || ['completed', 'cancelled', 'canceled'].includes(value.status)
}

function acquisitionState(value: { stage: string, status: string, progress?: number, processedFiles: number, totalFiles: number, lastErrorCode?: string, updatedAt?: string }): MediaAcquisitionState {
  return {
    stage: value.stage,
    status: value.status,
    progress: value.progress,
    processedFiles: value.processedFiles,
    totalFiles: value.totalFiles,
    lastErrorCode: value.lastErrorCode,
    updatedAt: value.updatedAt,
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
    item_token: optionalString(value.item_token),
    title: value.title,
    display_title: optionalString(value.display_title),
    display_subtitle: optionalString(value.display_subtitle),
    series_title: optionalString(value.series_title),
    episode_title: optionalString(value.episode_title),
    season: numberValue(value.season),
    episode: numberValue(value.episode),
    overview: optionalString(value.overview),
    still_path: optionalImagePath(value.still_path),
    poster_path: optionalImagePath(value.poster_path),
    backdrop_path: optionalImagePath(value.backdrop_path),
    episode_still_path: optionalImagePath(value.episode_still_path),
    air_date: optionalString(value.air_date),
    runtime_minutes: boundedNumber(value.runtime_minutes, 1, 24 * 60),
    rating: boundedNumber(value.rating, 0, 10),
    size: numberValue(value.size) ?? 0,
    modified_at: optionalString(value.modified_at) ?? '',
    playable: value.playable,
    stream_path: optionalString(value.stream_path),
    delivery_kind: deliveryKind,
    exact_identity: value.exact_identity,
    history_identity: optionalHistoryIdentity(value.history_identity),
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

function resolveServerArtworkURL(baseUrl: string, value: unknown): string | undefined {
  const candidate = optionalString(value)
  if (!candidate || candidate.length > 2048)
    return undefined
  try {
    const server = new URL(baseUrl)
    const resolved = new URL(candidate, `${server.origin}/`)
    if (resolved.origin !== server.origin || resolved.username || resolved.password || !resolved.pathname.startsWith('/api/v1/assets/'))
      return undefined
    return resolved.toString()
  }
  catch {
    return undefined
  }
}

function optionalString(value: unknown): string | undefined {
  if (typeof value !== 'string')
    return undefined
  const trimmed = value.trim()
  return trimmed || undefined
}

function optionalIDString(value: unknown): string | undefined {
  if (typeof value === 'number' && Number.isSafeInteger(value) && value > 0)
    return String(value)
  return optionalString(value)
}

function optionalHistoryIdentity(value: unknown): string | undefined {
  const candidate = optionalString(value)
  return candidate && /^server:v1:(?:movie|episode):\S{1,1024}$/.test(candidate) ? candidate : undefined
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

function personList(value: unknown, limit: number): ServerPersonRecord[] | undefined {
  if (!Array.isArray(value))
    return undefined
  const result: ServerPersonRecord[] = []
  for (const entry of value) {
    if (!isRecord(entry))
      continue
    const name = optionalString(entry.name)
    if (!name)
      continue
    result.push({
      tmdb_id: boundedNumber(entry.tmdb_id, 1, Number.MAX_SAFE_INTEGER),
      name,
      role: optionalString(entry.role),
      character: optionalString(entry.character),
      profile_path: optionalImagePath(entry.profile_path),
    })
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
function versionAvailable(version: { variants: readonly { id: string, available: boolean }[] }, requestedVariantId?: string): boolean {
  if (requestedVariantId)
    return version.variants.some(variant => variant.id === requestedVariantId && variant.available)
  return version.variants.length === 0 || version.variants.some(variant => variant.available)
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

function createMovieHistoryIdentity(libraryId: string, workId: string): string {
  return `server:v1:movie:${libraryId}:${workId}`
}

function createEpisodeHistoryIdentity(libraryId: string, workId: string, season: number, episode: number): string {
  return `server:v1:episode:${libraryId}:${workId}:${season}:${episode}`
}

function createPlayerMediaStateItemID(itemId: string): string {
  const work = parseWorkItemID(itemId)
  if (!work)
    throw new Error('Server 媒体标识无效。')
  return `${work.libraryId}:${work.workId}`
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

function parseServerHistoryPage(sourceId: string, value: unknown, page: number): ProviderPlaybackHistoryPage {
  const data = recordData(value)
  const items = mapServerHistoryItems(sourceId, arrayRecords(data.list).slice(0, 100))
  const hasMore = data.has_more === true
  return { items, cursor: hasMore ? String(page + 1) : undefined, hasMore }
}

function mapServerHistoryItems(sourceId: string, values: readonly unknown[]): MediaItem[] {
  const items: MediaItem[] = []
  const seenIdentities = new Set<string>()
  for (const value of values) {
    for (const item of mapServerHistoryItem(sourceId, value)) {
      if (!item.historyIdentity || seenIdentities.has(item.historyIdentity))
        continue
      seenIdentities.add(item.historyIdentity)
      items.push(item)
    }
  }
  return items
}

/** Shared Server history projection used by the history page and Server overview sections. */
export function mapServerHistoryItem(sourceId: string, value: unknown): MediaItem[] {
  if (!isRecord(value))
    return []
  const title = optionalString(value.display_title) ?? optionalString(value.series_title) ?? optionalString(value.title)
  const historyIdentity = optionalHistoryIdentity(value.history_identity) ?? optionalString(value.media_identity)
  const itemId = optionalString(value.item_token) ?? optionalString(value.item_id) ?? historyIdentity
  const libraryId = optionalIDString(value.library_id)
  const position = boundedNumber(value.position, 0, Number.MAX_SAFE_INTEGER) ?? 0
  const duration = boundedNumber(value.duration, 0, Number.MAX_SAFE_INTEGER)
  const updatedAt = boundedNumber(value.updated_at, 1, Number.MAX_SAFE_INTEGER)
  if (!title || !itemId || !historyIdentity || !updatedAt)
    return []
  const type = ['movie', 'series', 'season', 'episode', 'folder', 'file'].includes(String(value.media_type))
    ? value.media_type as MediaItem['type']
    : 'file'
  const seasonNumber = boundedNumber(value.season_number, 0, 10_000)
  const episodeNumber = boundedNumber(value.episode_number, 0, 100_000)
  const episodeTitle = optionalString(value.episode_title)
  const displaySubtitle = optionalString(value.display_subtitle) ?? episodeDisplaySubtitle(seasonNumber, episodeNumber, episodeTitle)
  return [{
    id: itemId,
    sourceId,
    originType: 'server',
    libraryId,
    name: title,
    type,
    historyIdentity,
    displaySubtitle,
    posterUrl: safeHistoryArtworkURL(value.poster_url) ?? artwork(optionalImagePath(value.poster_path), 'w500'),
    backdropUrl: safeHistoryArtworkURL(value.backdrop_url) ?? artwork(optionalImagePath(value.backdrop_path), 'w1280'),
    episodeStillUrl: safeHistoryArtworkURL(value.episode_still_url) ?? artwork(optionalImagePath(value.episode_still_path), 'w1280'),
    titleLogoUrl: safeHistoryArtworkURL(value.title_logo_url),
    duration,
    path: optionalString(value.stream_identity) ?? itemId,
    resumePosition: position,
    progress: duration && duration > 0 ? Math.min(1, position / duration) : undefined,
    played: value.completed === true,
    seriesName: optionalString(value.series_title),
    seasonNumber,
    episodeNumber,
    cardLayout: type === 'episode' ? 'poster' : undefined,
    modified: new Date(updatedAt).toISOString(),
  }]
}

function episodeDisplaySubtitle(season: number | undefined, episode: number | undefined, title: string | undefined): string | undefined {
  if (season == null || episode == null)
    return title
  const code = `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`
  return title ? `${code} · ${title}` : code
}

function safeHistoryArtworkURL(value: unknown): string | undefined {
  const candidate = optionalString(value)
  if (!candidate || candidate.length > 2048)
    return undefined
  try {
    const parsed = new URL(candidate)
    if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password)
      return undefined
    for (const key of parsed.searchParams.keys()) {
      if (['token', 'key', 'auth', 'signature', 'sig', 'expires'].some(fragment => key.toLowerCase().includes(fragment)))
        return undefined
    }
    return parsed.toString()
  }
  catch {
    return undefined
  }
}

function readServerExtra(config: DataSourceConfig): ServerConfigExtra {
  const extra = isRecord(config.extra) ? config.extra : {}
  return {
    credentialRef: typeof extra.credentialRef === 'string' ? extra.credentialRef : undefined,
    deviceId: typeof extra.deviceId === 'string' ? extra.deviceId.trim() : '',
    credentialVersion: typeof extra.credentialVersion === 'number' && Number.isFinite(extra.credentialVersion) ? extra.credentialVersion : undefined,
    libraries: Array.isArray(extra.libraries) ? extra.libraries as ServerConfigExtra['libraries'] : undefined,
    capabilities: Array.isArray(extra.capabilities) ? extra.capabilities.filter((value): value is string => typeof value === 'string' && /^[a-z][a-z0-9_]{0,63}$/.test(value)).slice(0, 64) : undefined,
  }
}

function sanitizeServerConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    return { id: '', type: 'server', name: 'OhMyCine Server', order: 0, url: '', enabled: false, extra: {} }
  const extra = readServerExtra(config)
  return { ...config, type: 'server', url: normalizeServerBaseUrl(config.url), extra: { credentialRef: extra.credentialRef, deviceId: extra.deviceId, credentialVersion: extra.credentialVersion, libraries: extra.libraries, capabilities: extra.capabilities } }
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
