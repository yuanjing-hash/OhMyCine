import type { HomeSection, MediaDetail, MediaIdentity, MediaItem, MediaLibrary, MediaSourceOption, MediaStreamRequest, PlaybackSubtitleTrack, ProviderDanmakuComment, ProviderPlaybackHistoryPage, SiteActionDescriptor, SiteActionKey, StreamVariant } from './types'

export interface OnlineLibrarySummary {
  id: string
  pluginId: string
  connectionId: string
  name: string
  providerLabel: string
  capabilities: string[]
  available: boolean
  errorCode?: string
  homeContributions: string[]
  artworkUrl?: string
}

export interface OnlineNavigationItem {
  id: string
  title: string
  kind: 'branch' | 'feed' | 'search' | 'user-library'
  nodeToken?: string
  routeKey?: string
  refreshable: boolean
}

export interface OnlineMediaWork {
  id: string
  title: string
  kind: 'movie' | 'series' | 'episode' | 'video' | 'live'
  identity: { scheme: string, value: string }
  originalTitle?: string
  overview?: string
  posterUrl?: string
  backdropUrl?: string
  author?: string
  publishedAt?: string
  durationSeconds?: number
  segments: OnlineMediaSegment[]
}

export interface OnlineMediaSegment {
  id: string
  title: string
  index: number
  seasonNumber?: number
  episodeNumber?: number
  versions: OnlineMediaVersion[]
}

export interface OnlineMediaVersion {
  id: string
  label: string
  sourceLabel?: string
  edition?: string
  releaseGroup?: string
  variants: StreamVariant[]
}

export interface OnlineFeedSection {
  id: string
  title: string
  layout: 'hero' | 'row' | 'poster-grid' | 'video-list'
  items: Array<{ work: OnlineMediaWork, actions: SiteActionDescriptor[] }>
  cursor?: string
  refreshSession?: string
  homeEligible: boolean
  refreshable: boolean
}

export interface OnlinePlaybackPlan {
  workId: string
  segmentId: string
  versionId: string
  variantId: string
  variants: StreamVariant[]
  assets: Array<{ kind: 'progressive' | 'hls' | 'dash-video' | 'dash-audio', urlRef: string, headersRef?: string }>
  delivery: 'direct' | 'server-gateway' | 'loopback-bridge'
  expiresAt?: string
  subtitles: Array<{ id: string, label: string, language?: string, format?: string, urlRef: string }>
  danmaku: Array<{ id: string, label: string, language?: string, format?: string, urlRef: string }>
}

export interface OnlineHomeContribution {
  id: string
  libraryId: string
  pluginId: string
  providerLabel: string
  routeKey: string
  title: string
  layout: OnlineFeedSection['layout']
  refreshable: boolean
  sections: OnlineFeedSection[]
  errorCode?: string
}

interface OnlineHistoryRecord {
  libraryId: string
  work: OnlineMediaWork
  segmentId?: string
  versionId?: string
  positionSeconds?: number
  durationSeconds?: number
  updatedAt?: string
}

export type OnlineItemID
  = | { kind: 'library', libraryId: string }
    | { kind: 'node', libraryId: string, nodeToken: string }
    | { kind: 'feed', libraryId: string, routeKey: string }
    | { kind: 'work', libraryId: string, workId: string }
    | { kind: 'version', libraryId: string, workId: string, segmentId: string, versionId: string }

const MAX_LIST_ITEMS = 200
const MAX_TEXT_LENGTH = 2048

export function parseOnlineLibraryList(value: unknown): OnlineLibrarySummary[] {
  const data = record(value)
  const list = Array.isArray(data.list) ? data.list : []
  return list.slice(0, 100).map(parseOnlineLibrary).filter((item): item is OnlineLibrarySummary => item != null)
}

export function parseOnlineNavigationList(value: unknown): OnlineNavigationItem[] {
  const envelope = record(value)
  const data = Array.isArray(value) ? value : Array.isArray(envelope.nodes) ? envelope.nodes : envelope.list
  if (!Array.isArray(data))
    return []
  return data.slice(0, 100).map(parseNavigation).filter((item): item is OnlineNavigationItem => item != null)
}

export function parseOnlineFeedSections(value: unknown): OnlineFeedSection[] {
  const data = record(value)
  const sections = Array.isArray(data.sections) ? data.sections : Array.isArray(value) ? value : []
  return sections.slice(0, 50).map(parseFeedSection).filter((item): item is OnlineFeedSection => item != null)
}

export function parseOnlineHomeContributions(value: unknown): OnlineHomeContribution[] {
  const data = record(value)
  const list = Array.isArray(data.list) ? data.list : []
  return list.slice(0, 100).flatMap((raw): OnlineHomeContribution[] => {
    const item = record(raw)
    const id = requiredText(item.id, 512)
    const libraryId = requiredText(item.libraryId ?? item.library_id, 512)
    const pluginId = requiredText(item.pluginId ?? item.plugin_id, 256)
    const providerLabel = requiredText(item.providerLabel ?? item.provider_label, 256)
    const routeKey = requiredText(item.routeKey ?? item.route_key, 256)
    const title = requiredText(item.title, 512)
    const layout = oneOf(item.layout, ['hero', 'row', 'poster-grid', 'video-list'] as const)
    if (!id || !libraryId || !pluginId || !providerLabel || !routeKey || !title || !layout)
      return []
    return [{
      id,
      libraryId,
      pluginId,
      providerLabel,
      routeKey,
      title,
      layout,
      refreshable: item.refreshable === true,
      sections: parseOnlineFeedSections(item.sections),
      errorCode: optionalText(item.errorCode ?? item.error_code, 128),
    }]
  })
}

export function onlineContributionErrorToHomeSection(sourceId: string, item: OnlineHomeContribution): HomeSection {
  return {
    id: `online:${item.libraryId}:error:${item.routeKey}`,
    sourceId,
    title: item.title,
    type: 'recommended',
    items: [],
    providerIdentity: `online-library:${item.libraryId}`,
    sourceLabel: item.providerLabel,
    refreshKey: item.refreshable ? joinOnlineID('online-refresh', item.libraryId, item.routeKey) : undefined,
    refreshable: item.refreshable,
    layout: item.layout,
    errorCode: item.errorCode ?? 'plugin_online_library_unavailable',
  }
}

export function parseOnlineWork(value: unknown): OnlineMediaWork | null {
  const item = record(value)
  const id = requiredText(item.id, 512)
  const title = requiredText(item.title, 512)
  const kind = oneOf(item.kind, ['movie', 'series', 'episode', 'video', 'live'] as const)
  const identity = record(item.identity)
  const identityScheme = requiredText(identity.scheme, 64)
  const identityValue = requiredText(identity.value, 512)
  if (!id || !title || !kind || !identityScheme || !identityValue)
    return null
  const segments = Array.isArray(item.segments)
    ? item.segments.slice(0, MAX_LIST_ITEMS).map(parseSegment).filter((entry): entry is OnlineMediaSegment => entry != null)
    : []
  return {
    id,
    title,
    kind,
    identity: { scheme: identityScheme, value: identityValue },
    originalTitle: optionalText(item.originalTitle, 512),
    overview: optionalText(item.overview, 20_000),
    posterUrl: safeArtworkURL(item.posterUrl),
    backdropUrl: safeArtworkURL(item.backdropUrl),
    author: optionalText(item.author, 512),
    publishedAt: optionalText(item.publishedAt, 128),
    durationSeconds: boundedNumber(item.durationSeconds, 0, 365 * 24 * 60 * 60),
    segments,
  }
}

export function parseOnlinePlaybackPlan(value: unknown): OnlinePlaybackPlan | null {
  const item = record(value)
  const workId = requiredText(item.workId, 512)
  const segmentId = requiredText(item.segmentId, 512)
  const versionId = requiredText(item.versionId, 512)
  const variantId = requiredText(item.variantId, 512)
  const delivery = oneOf(item.delivery, ['direct', 'server-gateway', 'loopback-bridge'] as const)
  if (!workId || !segmentId || !versionId || !variantId || !delivery)
    return null
  const variants = Array.isArray(item.variants)
    ? item.variants.slice(0, 50).map(parseVariant).filter((entry): entry is StreamVariant => entry != null)
    : []
  const assets = Array.isArray(item.assets)
    ? item.assets.slice(0, 8).map(parsePlaybackAsset).filter((entry): entry is OnlinePlaybackPlan['assets'][number] => entry != null)
    : []
  const subtitles = Array.isArray(item.subtitles)
    ? item.subtitles.slice(0, 100).map(parseTrack).filter((entry): entry is OnlinePlaybackPlan['subtitles'][number] => entry != null)
    : []
  const danmaku = Array.isArray(item.danmaku)
    ? item.danmaku.slice(0, 8).map(parseTrack).filter((entry): entry is OnlinePlaybackPlan['danmaku'][number] => entry != null)
    : []
  if (assets.length === 0)
    return null
  return { workId, segmentId, versionId, variantId, variants, assets, delivery, expiresAt: optionalText(item.expiresAt, 128), subtitles, danmaku }
}

export function parseProviderDanmakuComments(value: unknown): ProviderDanmakuComment[] {
  const data = record(value)
  const comments = Array.isArray(data.comments) ? data.comments : []
  return comments.slice(0, 50_000).flatMap((raw): ProviderDanmakuComment[] => {
    const item = record(raw)
    const id = requiredText(item.id, 512)
    const time = boundedNumber(item.time, 0, 365 * 24 * 60 * 60)
    const mode = oneOf(item.mode, ['scroll', 'top', 'bottom'] as const)
    const text = requiredText(item.text, 500)
    const color = optionalText(item.color, 16)
    if (!id || time == null || !mode || !text)
      return []
    return [{ id, time, mode, color: color && /^#[0-9a-f]{6}$/i.test(color) ? color : '#ffffff', text }]
  }).sort((left, right) => left.time - right.time)
}

export function parseOnlineHistoryPage(sourceId: string, value: unknown): ProviderPlaybackHistoryPage {
  const data = record(value)
  const list = Array.isArray(data.list) ? data.list : []
  const items = list.slice(0, 100).flatMap((raw): MediaItem[] => {
    const item = parseOnlineHistoryRecord(raw)
    if (!item)
      return []
    const root = onlineWorkToMediaItem(sourceId, item.libraryId, item.work)
    const segment = item.segmentId ? item.work.segments.find(candidate => candidate.id === item.segmentId) : undefined
    const version = segment && item.versionId ? segment.versions.find(candidate => candidate.id === item.versionId) : undefined
    const target = segment && version ? onlineVersionToMediaItem(sourceId, item.libraryId, item.work, segment, version) : root
    return [{
      ...target,
      duration: item.durationSeconds ?? target.duration,
      resumePosition: item.positionSeconds,
      progress: item.durationSeconds && item.positionSeconds != null ? Math.min(1, item.positionSeconds / item.durationSeconds) : undefined,
      modified: item.updatedAt ?? target.modified,
    }]
  })
  return {
    items,
    cursor: optionalText(data.cursor, MAX_TEXT_LENGTH),
    hasMore: data.hasMore === true || data.has_more === true,
  }
}

export function onlineLibraryToMediaLibrary(sourceId: string, item: OnlineLibrarySummary, serverBaseUrl: string): MediaLibrary {
  const artworkUrl = resolveLibraryArtworkURL(serverBaseUrl, item.artworkUrl)
  return {
    id: createOnlineLibraryID(item.id),
    sourceId,
    name: item.name,
    type: 'mixed',
    posterUrl: artworkUrl,
    backdropUrl: artworkUrl,
    providerIdentity: `plugin:${item.pluginId}:${item.id}`,
  }
}

export function onlineNavigationToMediaItem(sourceId: string, libraryId: string, item: OnlineNavigationItem): MediaItem {
  return {
    id: item.kind === 'branch' && item.nodeToken
      ? createOnlineNodeID(libraryId, item.nodeToken)
      : createOnlineFeedID(libraryId, item.routeKey ?? item.id),
    sourceId,
    originType: 'server',
    libraryId: createOnlineLibraryID(libraryId),
    name: item.title,
    type: 'folder',
    path: '',
  }
}

export function onlineWorkToMediaItem(sourceId: string, libraryId: string, item: OnlineMediaWork): MediaItem {
  const id = createOnlineWorkID(libraryId, item.id)
  return {
    id,
    sourceId,
    originType: 'server',
    libraryId: createOnlineLibraryID(libraryId),
    name: item.title,
    originalTitle: item.originalTitle,
    type: mapOnlineKind(item.kind),
    posterUrl: item.posterUrl,
    backdropUrl: item.backdropUrl,
    overview: item.overview,
    duration: item.durationSeconds,
    modified: item.publishedAt,
    path: '',
    workIdentity: onlineIdentity(item),
    playbackTargets: [{ sourceId, itemId: id, label: 'Server 在线媒体库' }],
  }
}

export function onlineWorkToDetail(sourceId: string, libraryId: string, item: OnlineMediaWork): MediaDetail {
  const root = onlineWorkToMediaItem(sourceId, libraryId, item)
  const children = item.segments.flatMap(segment => segment.versions.map(version => onlineVersionToMediaItem(sourceId, libraryId, item, segment, version)))
  const mediaSources = item.segments.flatMap(segment => segment.versions.map(version => ({
    id: version.id,
    name: version.label,
    isRemote: true,
    sourceLabel: version.sourceLabel ?? 'Server 在线媒体库',
    deliveryKind: 'server_stream',
    sourceId,
    itemId: createOnlineVersionID(libraryId, item.id, segment.id, version.id),
    providerMediaSourceId: version.id,
    exactIdentity: `plugin:${item.identity.scheme}:${item.identity.value}:${segment.id}:${version.id}`,
  } satisfies MediaSourceOption)))
  return { ...root, mediaSources, children }
}

export function onlineSectionsToHomeSections(
  sourceId: string,
  libraryId: string,
  sections: readonly OnlineFeedSection[],
  routeKey?: string,
  providerLabel = 'Server 在线媒体库',
): HomeSection[] {
  return sections.flatMap((section) => {
    const items = section.items.map(item => ({
      ...onlineWorkToMediaItem(sourceId, libraryId, item.work),
      siteActions: parseSiteActions(item.actions),
    }))
    if (items.length === 0)
      return []
    const type: HomeSection['type'] = section.layout === 'hero' ? 'hero' : section.homeEligible ? 'recommended' : 'libraryRow'
    return [{
      id: `online:${libraryId}:${section.id}`,
      sourceId,
      title: section.title,
      type,
      items,
      providerIdentity: `online-library:${libraryId}`,
      sourceLabel: providerLabel,
      refreshKey: section.refreshable && routeKey ? joinOnlineID('online-refresh', libraryId, routeKey) : undefined,
      refreshable: section.refreshable,
      layout: section.layout,
    }]
  })
}

function parseSiteActions(value: unknown): SiteActionDescriptor[] {
  if (!Array.isArray(value))
    return []
  const allowed = new Set<SiteActionKey>([
    'like.add',
    'like.remove',
    'favorite.add',
    'favorite.remove',
    'watch-later.add',
    'watch-later.remove',
    'follow.add',
    'follow.remove',
    'history.remove',
  ])
  const seen = new Set<SiteActionKey>()
  return value.slice(0, 32).flatMap((raw): SiteActionDescriptor[] => {
    const item = typeof raw === 'string' ? { id: raw, label: raw } : record(raw)
    const id = requiredText(item.id, 64) as SiteActionKey | null
    const label = requiredText(item.label, 128)
    if (!id || !label || !allowed.has(id) || seen.has(id))
      return []
    seen.add(id)
    return [{
      id,
      label,
      state: typeof item.state === 'boolean' ? item.state : undefined,
      requiresConfirmation: item.requiresConfirmation === true || item.requires_confirmation === true,
      destructive: item.destructive === true,
    }]
  })
}

export function onlinePlaybackToStreamRequest(
  baseUrl: string,
  accessToken: string,
  plan: OnlinePlaybackPlan,
): MediaStreamRequest {
  const primary = plan.assets.find(asset => asset.kind === 'progressive' || asset.kind === 'hls')
    ?? plan.assets.find(asset => asset.kind === 'dash-video')
  if (!primary)
    throw new Error('在线媒体播放方案缺少可播放视频资源。')
  const url = resolveServerGatewayURL(baseUrl, primary.urlRef)
  const audio = primary.kind === 'dash-video'
    ? plan.assets.find(asset => asset.kind === 'dash-audio')
    : undefined
  const subtitles = plan.subtitles.flatMap((track, index): PlaybackSubtitleTrack[] => {
    try {
      return [{
        index,
        language: track.language ?? 'und',
        title: track.label,
        codec: track.format,
        isDefault: index === 0,
        source: 'external',
        url: resolveServerGatewayURL(baseUrl, track.urlRef),
        headers: { Authorization: `Bearer ${accessToken}` },
      }]
    }
    catch {
      return []
    }
  })
  const danmaku = plan.danmaku.flatMap((track) => {
    try {
      return [{
        id: track.id,
        label: track.label,
        format: track.format,
        url: resolveServerGatewayURL(baseUrl, track.urlRef),
        headers: { Authorization: `Bearer ${accessToken}` },
      }]
    }
    catch {
      return []
    }
  })
  return {
    url,
    headers: { Authorization: `Bearer ${accessToken}` },
    audioUrl: audio ? resolveServerGatewayURL(baseUrl, audio.urlRef) : undefined,
    audioHeaders: audio ? { Authorization: `Bearer ${accessToken}` } : undefined,
    mediaSourceId: plan.versionId,
    variantId: plan.variantId,
    variants: plan.variants,
    subtitles,
    danmaku,
  }
}

export function createOnlineLibraryID(libraryId: string): string {
  return joinOnlineID('online-library', libraryId)
}

export function createOnlineFeedID(libraryId: string, routeKey: string): string {
  return joinOnlineID('online-feed', libraryId, routeKey)
}

export function createOnlineNodeID(libraryId: string, nodeToken: string): string {
  return joinOnlineID('online-node', libraryId, nodeToken)
}

export function createOnlineWorkID(libraryId: string, workId: string): string {
  return joinOnlineID('online-work', libraryId, workId)
}

export function createOnlineVersionID(libraryId: string, workId: string, segmentId: string, versionId: string): string {
  return joinOnlineID('online-version', libraryId, workId, segmentId, versionId)
}

export function parseOnlineItemID(value: string): OnlineItemID | null {
  const [kind, ...encoded] = value.split('|')
  try {
    const values = encoded.map(entry => decodeURIComponent(entry))
    if (kind === 'online-library' && values.length === 1 && values[0])
      return { kind: 'library', libraryId: values[0] }
    if (kind === 'online-node' && values.length === 2 && values.every(Boolean))
      return { kind: 'node', libraryId: values[0], nodeToken: values[1] }
    if (kind === 'online-feed' && values.length === 2 && values.every(Boolean))
      return { kind: 'feed', libraryId: values[0], routeKey: values[1] }
    if (kind === 'online-work' && values.length === 2 && values.every(Boolean))
      return { kind: 'work', libraryId: values[0], workId: values[1] }
    if (kind === 'online-version' && values.length === 4 && values.every(Boolean))
      return { kind: 'version', libraryId: values[0], workId: values[1], segmentId: values[2], versionId: values[3] }
  }
  catch {
    return null
  }
  return null
}

function onlineVersionToMediaItem(sourceId: string, libraryId: string, work: OnlineMediaWork, segment: OnlineMediaSegment, version: OnlineMediaVersion): MediaItem {
  const id = createOnlineVersionID(libraryId, work.id, segment.id, version.id)
  return {
    ...onlineWorkToMediaItem(sourceId, libraryId, work),
    id,
    name: segment.title || work.title,
    type: work.kind === 'series' || work.kind === 'episode' ? 'episode' : 'movie',
    seasonNumber: segment.seasonNumber,
    episodeNumber: segment.episodeNumber ?? segment.index,
    seriesName: work.kind === 'series' ? work.title : undefined,
    exactIdentity: `plugin:${work.identity.scheme}:${work.identity.value}:${segment.id}:${version.id}`,
    playbackTargets: [{ sourceId, itemId: id, mediaSourceId: version.id, label: version.sourceLabel ?? 'Server 在线媒体库' }],
  }
}

function parseOnlineLibrary(value: unknown): OnlineLibrarySummary | null {
  const item = record(value)
  const id = requiredText(item.id, 128)
  const pluginId = requiredText(item.pluginId ?? item.plugin_id, 128)
  const connectionId = requiredText(item.connectionId ?? item.connection_id, 128)
  const name = requiredText(item.name, 256)
  if (!id || !pluginId || !connectionId || !name)
    return null
  return {
    id,
    pluginId,
    connectionId,
    name,
    providerLabel: optionalText(item.providerLabel ?? item.provider_label, 256) ?? name,
    capabilities: stringList(item.capabilities, 100),
    available: item.available !== false,
    errorCode: optionalText(item.errorCode ?? item.error_code, 128),
    homeContributions: stringList(item.homeContributions ?? item.home_contributions, 50),
    artworkUrl: optionalText(item.artworkUrl ?? item.artwork_url, MAX_TEXT_LENGTH),
  }
}

function resolveLibraryArtworkURL(baseUrl: string, value: unknown): string | undefined {
  const candidate = optionalText(value, MAX_TEXT_LENGTH)
  if (!candidate)
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

function parseOnlineHistoryRecord(value: unknown): OnlineHistoryRecord | null {
  const item = record(value)
  const libraryId = requiredText(item.libraryId ?? item.library_id, 128)
  const work = parseOnlineWork(item.work)
  if (!libraryId || !work)
    return null
  return {
    libraryId,
    work,
    segmentId: optionalText(item.segmentId ?? item.segment_id, 512),
    versionId: optionalText(item.versionId ?? item.version_id, 512),
    positionSeconds: boundedNumber(item.positionSeconds ?? item.position_seconds, 0, 365 * 24 * 60 * 60),
    durationSeconds: boundedNumber(item.durationSeconds ?? item.duration_seconds, 0, 365 * 24 * 60 * 60),
    updatedAt: optionalText(item.updatedAt ?? item.updated_at, 128),
  }
}

function parseNavigation(value: unknown): OnlineNavigationItem | null {
  const item = record(value)
  const id = requiredText(item.id, 128)
  const title = requiredText(item.title, 256)
  const legacyPageType = oneOf(item.pageType ?? item.page_type, ['feed', 'search', 'user-library'] as const)
  const kind = oneOf(item.kind, ['branch', 'feed', 'search', 'user-library'] as const) ?? legacyPageType
  const nodeToken = optionalText(item.nodeToken ?? item.node_token, 4096)
  const routeKey = optionalText(item.routeKey ?? item.route_key, 256)
  if (!id || !title || !kind || (kind === 'branch' ? !nodeToken : !routeKey))
    return null
  return { id, title, kind, nodeToken, routeKey, refreshable: item.refreshable === true }
}

function parseFeedSection(value: unknown): OnlineFeedSection | null {
  const item = record(value)
  const id = requiredText(item.id, 256)
  const title = requiredText(item.title, 512)
  const layout = oneOf(item.layout, ['hero', 'row', 'poster-grid', 'video-list'] as const)
  if (!id || !title || !layout)
    return null
  const items = Array.isArray(item.items)
    ? item.items.slice(0, MAX_LIST_ITEMS).flatMap((raw) => {
        const entry = record(raw)
        const work = parseOnlineWork(entry.work)
        return work ? [{ work, actions: parseSiteActions(entry.actions) }] : []
      })
    : []
  return {
    id,
    title,
    layout,
    items,
    cursor: optionalText(item.cursor, MAX_TEXT_LENGTH),
    refreshSession: optionalText(item.refreshSession ?? item.refresh_session, MAX_TEXT_LENGTH),
    homeEligible: item.homeEligible === true || item.home_eligible === true,
    refreshable: item.refreshable === true,
  }
}

function parseSegment(value: unknown): OnlineMediaSegment | null {
  const item = record(value)
  const id = requiredText(item.id, 512)
  const title = requiredText(item.title, 512)
  const index = boundedNumber(item.index, 0, 1_000_000)
  if (!id || !title || index == null)
    return null
  const versions = Array.isArray(item.versions)
    ? item.versions.slice(0, 50).map(parseVersion).filter((entry): entry is OnlineMediaVersion => entry != null)
    : []
  return {
    id,
    title,
    index,
    seasonNumber: boundedNumber(item.seasonNumber, 0, 100_000),
    episodeNumber: boundedNumber(item.episodeNumber, 0, 1_000_000),
    versions,
  }
}

function parseVersion(value: unknown): OnlineMediaVersion | null {
  const item = record(value)
  const id = requiredText(item.id, 512)
  const label = requiredText(item.label, 512)
  if (!id || !label)
    return null
  const variants = Array.isArray(item.variants)
    ? item.variants.slice(0, 50).map(parseVariant).filter((entry): entry is StreamVariant => entry != null)
    : []
  return {
    id,
    label,
    sourceLabel: optionalText(item.sourceLabel, 512),
    edition: optionalText(item.edition, 256),
    releaseGroup: optionalText(item.releaseGroup, 256),
    variants,
  }
}

function parseVariant(value: unknown): StreamVariant | null {
  const item = record(value)
  const id = requiredText(item.id, 512)
  const label = requiredText(item.label, 256)
  if (!id || !label)
    return null
  return {
    id,
    label,
    available: item.available !== false,
    width: boundedNumber(item.width, 1, 100_000),
    height: boundedNumber(item.height, 1, 100_000),
    bitrate: boundedNumber(item.bitrate, 1, Number.MAX_SAFE_INTEGER),
    videoCodec: optionalText(item.videoCodec, 128),
    audioCodec: optionalText(item.audioCodec, 128),
    dynamicRange: optionalText(item.dynamicRange, 128),
    unavailableReason: optionalText(item.unavailableReason, 512),
  }
}

function parsePlaybackAsset(value: unknown): OnlinePlaybackPlan['assets'][number] | null {
  const item = record(value)
  const kind = oneOf(item.kind, ['progressive', 'hls', 'dash-video', 'dash-audio'] as const)
  const urlRef = requiredText(item.urlRef, MAX_TEXT_LENGTH)
  return kind && urlRef ? { kind, urlRef, headersRef: optionalText(item.headersRef, 512) } : null
}

function parseTrack(value: unknown): OnlinePlaybackPlan['subtitles'][number] | null {
  const item = record(value)
  const id = requiredText(item.id, 512)
  const label = requiredText(item.label, 512)
  const urlRef = requiredText(item.urlRef, MAX_TEXT_LENGTH)
  return id && label && urlRef
    ? { id, label, language: optionalText(item.language, 64), format: optionalText(item.format, 64), urlRef }
    : null
}

function onlineIdentity(item: OnlineMediaWork): MediaIdentity {
  return { scheme: 'plugin', mediaType: mapOnlineKind(item.kind) === 'series' ? 'series' : 'movie', value: `${item.identity.scheme}:${item.identity.value}` }
}

function mapOnlineKind(kind: OnlineMediaWork['kind']): MediaItem['type'] {
  if (kind === 'series')
    return 'series'
  if (kind === 'episode')
    return 'episode'
  return 'movie'
}

function resolveServerGatewayURL(baseUrl: string, ref: string): string {
  const url = new URL(ref, `${baseUrl}/`)
  if (!['http:', 'https:'].includes(url.protocol) || url.origin !== new URL(baseUrl).origin || url.username || url.password)
    throw new Error('在线媒体播放地址未通过 Server 安全网关。')
  return url.toString()
}

function joinOnlineID(kind: string, ...parts: string[]): string {
  return [kind, ...parts.map(part => encodeURIComponent(part))].join('|')
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value != null && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function requiredText(value: unknown, limit: number): string | null {
  return optionalText(value, limit) ?? null
}

function optionalText(value: unknown, limit: number): string | undefined {
  if (typeof value !== 'string')
    return undefined
  const text = value.trim()
  return text && text.length <= limit && !hasControlCharacter(text) ? text : undefined
}

function hasControlCharacter(value: string): boolean {
  return [...value].some((character) => {
    const code = character.charCodeAt(0)
    return code < 32 && code !== 9 && code !== 10 && code !== 13
  })
}

function safeArtworkURL(value: unknown): string | undefined {
  const text = optionalText(value, MAX_TEXT_LENGTH)
  if (!text)
    return undefined
  try {
    const url = new URL(text)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.toString() : undefined
  }
  catch {
    return undefined
  }
}

function boundedNumber(value: unknown, minimum: number, maximum: number): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= minimum && value <= maximum ? value : undefined
}

function stringList(value: unknown, limit: number): string[] {
  if (!Array.isArray(value))
    return []
  return [...new Set(value.map(entry => optionalText(entry, 256)).filter((entry): entry is string => Boolean(entry)))].slice(0, limit)
}

function oneOf<const T extends readonly string[]>(value: unknown, choices: T): T[number] | null {
  return typeof value === 'string' && choices.includes(value) ? value as T[number] : null
}
