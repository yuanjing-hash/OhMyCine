export interface MediaItem {
  id: string
  sourceId: string
  libraryId?: string
  name: string
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

export interface AudioTrack {
  index: number
  language: string
  codec: string
  channels: number
  isDefault: boolean
}

export type DataSourceType = 'emby' | 'jellyfin' | 'alist' | 'clouddrive2' | 'server' | '115' | '123' | 'quark' | 'local'

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

  clearCache?: () => void

  exportConfig: () => DataSourceConfig
}
