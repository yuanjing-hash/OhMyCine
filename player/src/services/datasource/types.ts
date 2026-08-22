export interface MediaItem {
  id: string
  sourceId: string
  originType?: DataSourceType
  libraryId?: string
  name: string
  originalTitle?: string
  titleLogoUrl?: string
  type: 'movie' | 'series' | 'season' | 'episode' | 'folder' | 'file'
  posterUrl?: string
  backdropUrl?: string
  year?: number
  rating?: number
  overview?: string
  tagline?: string
  duration?: number
  size?: number
  modified?: string
  path: string
  resumePosition?: number
  progress?: number
  progressSource?: 'local'
  played?: boolean
  favorite?: boolean
  seriesName?: string
  seasonNumber?: number
  episodeNumber?: number
  children?: MediaItem[]
  /** Stable cross-source work identity. It never contains credentials or paths. */
  workIdentity?: MediaIdentity
  /** Exact playable artifact/version identity when the provider can prove it. */
  exactIdentity?: string
  /** Alternate provider routes retained when aggregate cards are merged. */
  playbackTargets?: MediaPlaybackTarget[]
}

export interface MediaIdentity {
  scheme: 'tmdb' | 'emby' | 'server' | 'plugin'
  mediaType: 'movie' | 'series' | 'season' | 'episode' | 'file'
  value: string
}

export interface MediaPlaybackTarget {
  sourceId: string
  itemId: string
  mediaSourceId?: string
  label: string
  exactIdentity?: string
}

export interface MediaLibrary {
  id: string
  sourceId: string
  name: string
  type: 'movies' | 'series' | 'anime' | 'music' | 'mixed' | 'folders'
  posterUrl?: string
  backdropUrl?: string
  itemCount?: number
  providerIdentity?: string
}

export interface HomeSection {
  id: string
  sourceId?: string
  title: string
  type: 'hero' | 'continueWatching' | 'recentlyAdded' | 'recommended' | 'libraryRow'
  items: MediaItem[]
}

export interface FileEntry {
  name: string
  path: string
  modified: string
}

export interface MediaDetail extends MediaItem {
  genres?: string[]
  directors?: string[]
  writers?: string[]
  cast?: string[]
  imdbId?: string
  tmdbId?: number
  resolution?: string
  codec?: string
  audioCodec?: string
  subtitles?: SubtitleTrack[]
  audioTracks?: AudioTrack[]
  mediaSources?: MediaSourceOption[]
  stills?: string[]
  similarItems?: MediaItem[]
  collections?: MediaItem[]
}

export interface MediaSourceOption {
  id: string
  name: string
  container?: string
  size?: number
  bitrate?: number
  isRemote?: boolean
  isStrm?: boolean
  sourceLabel?: string
  deliveryKind?: 'server_stream' | 'server_redirect'
  sourceId?: string
  itemId?: string
  providerMediaSourceId?: string
  exactIdentity?: string
}

export interface SubtitleTrack {
  index: number
  language: string
  title?: string
  codec?: string
  isDefault: boolean
  source?: 'embedded' | 'external'
  url?: string
}

export interface PlaybackSubtitleTrack extends SubtitleTrack {
  /** Transient playback-only request headers. Never persist or expose in route/history state. */
  readonly headers?: Readonly<Record<string, string>>
}

export type SubtitleSearchOrigin = 'emby' | 'local'

export interface SubtitleSearchInput {
  itemId: string
  language: string
  mediaSourceId?: string
  title?: string
  year?: number
  mediaType?: MediaItem['type']
  seasonNumber?: number
  episodeNumber?: number
  imdbId?: string
  tmdbId?: number
}

export interface SubtitleSearchResult {
  id: string
  origin: SubtitleSearchOrigin
  providerName: string
  language: string
  title: string
  format?: string
  author?: string
  comments?: string
  rating?: number
  downloadCount?: number
  isHashMatch?: boolean
  aiTranslated?: boolean
  machineTranslated?: boolean
  forced?: boolean
  hearingImpaired?: boolean
  downloadRef?: string
}

export interface SubtitleDownloadInput {
  itemId: string
  mediaSourceId?: string
  result: SubtitleSearchResult
}

export interface AudioTrack {
  index: number
  language: string
  codec: string
  channels: number
  isDefault: boolean
}

export type DataSourceType = 'emby' | 'jellyfin' | 'alist' | 'clouddrive2' | 'webdav' | 'server' | '115' | '123' | 'quark' | 'local'

export interface DataSourceConfig {
  id: string
  type: DataSourceType
  name: string
  displayName?: string
  iconUrl?: string
  order: number
  url: string
  enabled?: boolean
  extra?: Record<string, unknown>
}

export interface MediaStreamRequest {
  readonly url: string
  readonly headers?: Record<string, string>
  readonly mediaSourceId?: string
  /** Current resolution/bitrate choice inside the selected media version. */
  readonly variantId?: string
  /** Actual choices for the selected version. URLs and provider credentials never belong here. */
  readonly variants?: readonly StreamVariant[]
  /** Exact subtitle inventory for the resolved playback version. */
  readonly subtitles?: readonly PlaybackSubtitleTrack[]
  /** Short-lived provider-native danmaku tracks. URLs must stay behind the source security boundary. */
  readonly danmaku?: readonly PlaybackDanmakuTrack[]
}

export interface PlaybackDanmakuTrack {
  readonly id: string
  readonly label: string
  readonly format?: string
  readonly url: string
  readonly headers?: Readonly<Record<string, string>>
}

export interface ProviderDanmakuComment {
  readonly id: string
  readonly time: number
  readonly mode: 'scroll' | 'top' | 'bottom'
  readonly color: string
  readonly text: string
}

export interface StreamVariant {
  readonly id: string
  readonly label: string
  readonly available: boolean
  readonly width?: number
  readonly height?: number
  readonly bitrate?: number
  readonly videoCodec?: string
  readonly audioCodec?: string
  readonly dynamicRange?: string
  readonly unavailableReason?: string
}

export interface PlaybackRequest {
  readonly itemId: string
  readonly mediaSourceId?: string
  /** Selects only a stream quality inside mediaSourceId; it must not change the episode or version. */
  readonly variantId?: string
}

export type ProviderPlaybackProgressEvent = 'started' | 'progress' | 'paused' | 'resumed' | 'stopped' | 'completed'

export interface ProviderPlaybackProgressInput {
  itemId: string
  mediaSourceId?: string
  playSessionId?: string
  mediaType?: MediaItem['type']
  position: number
  duration?: number
  startPosition?: number
  isPaused: boolean
  completed: boolean
  event: ProviderPlaybackProgressEvent
  playbackRate?: number
}

export interface ProviderPlaybackHistoryRequest {
  cursor?: string
  limit?: number
  /** Optional online-library identity when one Server publishes multiple provider histories. */
  libraryId?: string
}

export interface ProviderPlaybackHistoryPage {
  items: MediaItem[]
  cursor?: string
  hasMore: boolean
}

export interface ProviderPlaybackSyncDiagnostic {
  timestamp: string
  sourceId: string
  event: ProviderPlaybackProgressEvent
  stage: string
  ok: boolean
  endpoint: string
  itemIdPresent: boolean
  mediaSourceIdPresent: boolean
  playSessionIdPresent: boolean
  position: number
  message?: string
}

export interface EditableMediaMetadata {
  name: string
  originalTitle?: string
  overview?: string
  tagline?: string
  year?: number
  rating?: number
  genres?: string[]
}

export type EditableArtworkKind = 'Primary' | 'Backdrop' | 'Logo'

export type PlayedStateMutation = 'played' | 'unplayed' | 'removeContinueWatching'
export interface ProviderCollectionOption { id: string, name: string, kind: 'playlist' | 'collection', itemCount?: number }

export interface DataSource {
  readonly id: string
  readonly name: string
  readonly type: DataSourceType
  readonly isConnected: boolean

  init: (config: DataSourceConfig) => Promise<void>
  test: () => Promise<boolean>
  destroy: () => void

  list: (path?: string) => Promise<MediaItem[]>
  listLibraries?: () => Promise<MediaLibrary[]>
  getHomeSections?: () => Promise<HomeSection[]>
  getFeaturedItems?: () => Promise<MediaItem[]>
  getContinueWatching?: () => Promise<MediaItem[]>
  getRecentlyAdded?: () => Promise<MediaItem[]>
  search: (keyword: string) => Promise<MediaItem[]>
  getDetail: (id: string) => Promise<MediaDetail>

  getStreamURL: (id: string) => Promise<string>
  getStreamRequest?: (request: PlaybackRequest) => Promise<MediaStreamRequest>
  getDanmakuComments?: (track: PlaybackDanmakuTrack) => Promise<ProviderDanmakuComment[]>
  searchSubtitles?: (input: SubtitleSearchInput) => Promise<SubtitleSearchResult[]>
  downloadSubtitle?: (input: SubtitleDownloadInput) => Promise<SubtitleTrack>
  updateMetadata?: (itemId: string, metadata: EditableMediaMetadata) => Promise<void>
  updateArtworkFromUrl?: (itemId: string, kind: EditableArtworkKind, imageUrl: string) => Promise<void>
  deleteArtwork?: (itemId: string, kind: EditableArtworkKind) => Promise<void>
  deleteSubtitle?: (itemId: string, subtitleIndex: number) => Promise<void>
  syncPlaybackProgress?: (progress: ProviderPlaybackProgressInput) => Promise<void>
  listPlaybackHistory?: (request?: ProviderPlaybackHistoryRequest) => Promise<ProviderPlaybackHistoryPage>
  setPlayedState?: (itemId: string, mutation: PlayedStateMutation) => Promise<void>
  setFavorite?: (itemId: string, favorite: boolean) => Promise<void>
  listFavorites?: () => Promise<MediaItem[]>
  getFavoriteState?: (itemId: string) => Promise<boolean>
  listProviderCollections?: (kind: 'playlist' | 'collection') => Promise<ProviderCollectionOption[]>
  createProviderCollection?: (name: string, kind: 'playlist' | 'collection') => Promise<string>
  addProviderCollectionMember?: (collectionId: string, itemId: string, kind: 'playlist' | 'collection') => Promise<void>
  refreshMetadata?: (itemId: string) => Promise<void>
  deleteMedia?: (itemId: string) => Promise<void>
  canDeleteMedia?: (itemId: string) => Promise<boolean>
  getPlaybackSyncDiagnostics?: () => ProviderPlaybackSyncDiagnostic[]

  clearCache?: () => void

  exportConfig: () => DataSourceConfig
}
