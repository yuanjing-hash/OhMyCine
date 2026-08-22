export type OnlineMediaKind = 'movie' | 'series' | 'episode' | 'video' | 'live'

export interface MediaIdentity {
  scheme: string
  value: string
}

export interface MediaWork {
  id: string
  title: string
  kind: OnlineMediaKind
  identity: MediaIdentity
  originalTitle?: string
  overview?: string
  posterUrl?: string
  backdropUrl?: string
  author?: string
  publishedAt?: string
  durationSeconds?: number
  segments?: readonly MediaSegment[]
}

export interface MediaSegment {
  id: string
  title: string
  index: number
  seasonNumber?: number
  episodeNumber?: number
  versions: readonly MediaVersion[]
}

export interface MediaVersion {
  id: string
  label: string
  sourceLabel?: string
  edition?: string
  releaseGroup?: string
  variants: readonly StreamVariant[]
}

export interface StreamVariant {
  id: string
  label: string
  available: boolean
  width?: number
  height?: number
  bitrate?: number
  videoCodec?: string
  audioCodec?: string
  dynamicRange?: string
  unavailableReason?: string
}

export interface NavigationItem {
  id: string
  title: string
  pageType: 'feed' | 'search' | 'user-library'
  iconKey?: string
  routeKey: string
  refreshable?: boolean
}

export interface FeedItem {
  work: MediaWork
  actions?: readonly string[]
}

export interface FeedSection {
  id: string
  title: string
  layout: 'hero' | 'row' | 'poster-grid' | 'video-list'
  items: readonly FeedItem[]
  cursor?: string
  refreshSession?: string
  homeEligible?: boolean
}

export interface PlaybackAsset {
  kind: 'progressive' | 'hls' | 'dash-video' | 'dash-audio'
  urlRef: string
  headersRef?: string
}

export interface PlaybackPlan {
  workId: string
  segmentId: string
  versionId: string
  variantId: string
  variants: readonly StreamVariant[]
  assets: readonly PlaybackAsset[]
  delivery: 'direct' | 'server-gateway' | 'loopback-bridge'
  expiresAt?: string
  refreshToken?: string
  subtitles?: readonly TrackDescriptor[]
  danmaku?: readonly TrackDescriptor[]
}

export interface TrackDescriptor {
  id: string
  label: string
  language?: string
  format?: string
  urlRef: string
}

export interface DownloadAsset {
  id: string
  kind: 'video' | 'audio' | 'subtitle' | 'danmaku'
  urlRef: string
  headersRef?: string
  expectedContentType?: string
  expectedBytes?: number
}

export interface DownloadPlan {
  workId: string
  segmentId: string
  versionId: string
  variantId: string
  suggestedFileName: string
  assets: readonly DownloadAsset[]
  merge?: { kind: 'dash-av', videoAssetId: string, audioAssetId: string }
}
