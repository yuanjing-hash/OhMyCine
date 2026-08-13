export interface MediaItem {
  id: string
  sourceId: string
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
}

export interface MediaLibrary {
  id: string
  sourceId: string
  name: string
  type: 'movies' | 'series' | 'anime' | 'music' | 'mixed' | 'folders'
  posterUrl?: string
  backdropUrl?: string
  itemCount?: number
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
}

export interface PlaybackRequest {
  readonly itemId: string
  readonly mediaSourceId?: string
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
  searchSubtitles?: (input: SubtitleSearchInput) => Promise<SubtitleSearchResult[]>
  downloadSubtitle?: (input: SubtitleDownloadInput) => Promise<SubtitleTrack>
  syncPlaybackProgress?: (progress: ProviderPlaybackProgressInput) => Promise<void>
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
