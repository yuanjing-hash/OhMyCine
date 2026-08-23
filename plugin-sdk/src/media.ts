export type OnlineMediaKind = 'movie' | 'series' | 'episode' | 'video' | 'live' | 'creator' | 'collection'

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

/** A credential-free stable identity plus a short-lived opaque Host asset. */
export interface LibraryArtworkCandidate {
  id: string
  assetRef: string
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
  resolution?: string
  sourceMedium?: string
  releaseKind?: string
  dynamicRange?: string
  videoCodec?: string
  audioCodec?: string
  audioLanguages?: readonly string[]
  sizeBytes?: number
  delivery?: 'local-file' | 'cloud-direct' | 'strm' | 'server-stream' | 'online'
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
  frameRate?: number
  container?: string
  hdr?: boolean
  dolbyVision?: boolean
  dolbyAtmos?: boolean
  unavailableReason?: string
}

export interface FlatNavigationItem {
  id: string
  title: string
  pageType: 'feed' | 'search' | 'user-library'
  iconKey?: string
  routeKey: string
  refreshable?: boolean
}

export interface HierarchicalNavigationNode {
  id: string
  title: string
  kind: 'branch' | 'feed' | 'search' | 'user-library'
  nodeKey?: string
  routeKey?: string
  hasChildren?: boolean
  iconKey?: string
  refreshable?: boolean
}

export interface HierarchicalNavigationResponse {
  version: 2
  mode: 'hierarchical'
  nodes: readonly HierarchicalNavigationNode[]
}

export interface FeedItem {
  work: MediaWork
  actions?: readonly (string | SiteActionDescriptor)[]
}

export interface SiteActionDescriptor {
  id: string
  label: string
  state?: boolean
  requiresConfirmation?: boolean
  destructive?: boolean
}

export interface FeedSection {
  id: string
  title: string
  layout: 'hero' | 'row' | 'poster-grid' | 'video-list'
  items: readonly FeedItem[]
  cursor?: string
  refreshSession?: string
  homeEligible?: boolean
  refreshable?: boolean
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
  selectionToken?: string
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

export interface ProviderMetadataSnapshot {
  version: 1
  workId: string
  segmentId: string
  kind: 'movie' | 'series' | 'episode' | 'video'
  title: string
  originalTitle?: string
  overview?: string
  author?: string
  publishedAt?: string
  durationSeconds?: number
  seasonNumber?: number
  episodeNumber?: number
  genres?: readonly string[]
  tags?: readonly string[]
  uniqueIds: Readonly<Record<string, string>>
  artwork?: readonly { kind: 'poster' | 'fanart', assetRef: string }[]
}
