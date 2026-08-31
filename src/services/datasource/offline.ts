import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaItem, MediaLibrary, MediaStreamRequest, PlaybackDanmakuTrack, ProviderDanmakuComment } from './types'
import type { OfflineDetailRecord, OfflineItemSummary } from '@/services/downloads'
import { getOfflineDetail, listOfflineItems, resolveCompletedDownload, resolveOfflineAsset } from '@/services/downloads'

export const OFFLINE_SOURCE_ID = '__offline__'
export const OFFLINE_SOURCE_CONFIG: DataSourceConfig = {
  id: OFFLINE_SOURCE_ID,
  type: 'offline',
  name: '离线内容',
  displayName: '离线内容',
  order: -1,
  url: '',
  enabled: true,
}

export class OfflineDataSource implements DataSource {
  readonly id = OFFLINE_SOURCE_ID
  readonly name = '离线内容'
  readonly type = 'offline' as const
  readonly isConnected = true
  private items: OfflineItemSummary[] = []

  async init(_config: DataSourceConfig): Promise<void> {
    await this.refresh()
  }

  async test(): Promise<boolean> {
    return true
  }

  destroy(): void {
    this.items = []
  }

  async list(parentId?: string): Promise<MediaItem[]> {
    await this.refresh()
    const roots = buildOfflineHierarchy(this.items)
    if (!parentId)
      return roots
    return findHierarchyItem(roots, parentId)?.children ?? []
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    await this.refresh()
    return [{
      id: 'offline-library',
      sourceId: this.id,
      name: '已下载',
      type: 'mixed',
      itemCount: this.items.length,
      providerIdentity: 'player:offline',
    }]
  }

  async getHomeSections(): Promise<HomeSection[]> {
    const items = await this.list()
    return items.length > 0
      ? [{ id: 'offline-recent', sourceId: this.id, title: '离线内容', type: 'recentlyAdded', items }]
      : []
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const normalized = keyword.trim().toLocaleLowerCase()
    await this.refresh()
    const items = this.items.map(toMediaItem)
    return normalized ? items.filter(item => item.name.toLocaleLowerCase().includes(normalized)) : items
  }

  async getDetail(id: string): Promise<MediaDetail> {
    await this.refresh()
    const hierarchy = findHierarchyItem(buildOfflineHierarchy(this.items), id)
    if (hierarchy?.type === 'series' || hierarchy?.type === 'season')
      return { ...hierarchy, children: hierarchy.children ?? [] }
    const item = this.items.find(entry => entry.itemId === id || entry.id === id)
    if (!item)
      throw new Error('离线媒体不存在或已被移除。')
    const detail = await getOfflineDetail(item.sourceId, item.itemId, item.id)
    if (!detail)
      throw new Error('离线媒体详情不存在或已被移除。')
    return toOfflineMediaDetail(detail)
  }

  async getStreamURL(id: string): Promise<string> {
    await this.refresh()
    const item = this.items.find(entry => entry.itemId === id || entry.id === id)
    if (!item)
      throw new Error('离线媒体不存在或已被移除。')
    const path = await resolveCompletedDownload({
      sourceId: item.sourceId,
      itemId: item.itemId,
      mediaSourceId: item.mediaSourceId,
      variantId: item.variantId,
    })
    if (!path)
      throw new Error('离线文件不存在或校验失败。')
    return path
  }

  async getStreamRequest(request: { itemId: string, mediaSourceId?: string, variantId?: string }): Promise<MediaStreamRequest> {
    await this.refresh()
    const item = this.items.find(entry => entry.itemId === request.itemId || entry.id === request.itemId)
    if (!item)
      throw new Error('离线媒体不存在或已被移除。')
    const detail = await getOfflineDetail(item.sourceId, item.itemId, item.id)
    const assets = await Promise.all((detail?.assets ?? []).map(async asset => ({ ...asset, content: await resolveOfflineAsset(asset.id) })))
    return {
      url: await this.getStreamURL(item.id),
      mediaSourceId: item.mediaSourceId,
      variantId: item.variantId,
      subtitles: assets.filter(asset => asset.kind === 'subtitle' && asset.content?.localPath).map((asset, index) => ({
        index,
        language: 'Unknown',
        title: '离线字幕',
        codec: asset.content?.localPath?.split('.').pop()?.toLocaleLowerCase(),
        isDefault: index === 0,
        source: 'external' as const,
        url: asset.content!.localPath,
      })),
      danmaku: assets.filter(asset => asset.kind === 'danmaku').map(asset => ({
        id: asset.id,
        label: '离线弹幕',
        format: 'json',
        url: `offline-asset:${asset.id}`,
      })),
    }
  }

  async getDanmakuComments(track: PlaybackDanmakuTrack): Promise<ProviderDanmakuComment[]> {
    const id = track.url.startsWith('offline-asset:') ? track.url.slice('offline-asset:'.length) : ''
    if (!id)
      return []
    const content = await resolveOfflineAsset(id)
    return parseOfflineDanmaku(content?.text)
  }

  clearCache(): void {
    this.items = []
  }

  exportConfig(): DataSourceConfig {
    return { ...OFFLINE_SOURCE_CONFIG }
  }

  private async refresh(): Promise<void> {
    try {
      this.items = await listOfflineItems()
    }
    catch {
      // The browser-only preview has no native offline store. Keep the virtual source
      // available and empty without making unrelated configured sources fail to load.
      this.items = []
    }
  }
}

export async function toOfflineMediaDetail(record: OfflineDetailRecord): Promise<MediaDetail> {
  const detail = toMediaDetail(record)
  const assets = await Promise.all(record.assets.map(async asset => ({ ...asset, content: await resolveOfflineAsset(asset.id) })))
  detail.posterUrl = assets.find(asset => asset.kind === 'poster')?.content?.dataUrl
  detail.backdropUrl = assets.find(asset => asset.kind === 'backdrop')?.content?.dataUrl
  detail.stills = assets.filter(asset => asset.kind === 'still' && asset.content?.dataUrl).map(asset => asset.content!.dataUrl!)
  detail.subtitles = assets.filter(asset => asset.kind === 'subtitle' && asset.content?.localPath).map((asset, index) => ({
    index,
    language: 'Unknown',
    title: '离线字幕',
    codec: asset.content?.localPath?.split('.').pop()?.toLocaleLowerCase(),
    isDefault: index === 0,
    source: 'external',
    url: asset.content!.localPath,
  }))
  return detail
}

function toMediaItem(item: OfflineItemSummary): MediaItem {
  return {
    // The virtual source must own its route identity. Keeping the remote source id here
    // made a cold/offline navigation jump back to a disconnected provider before the
    // local resolver had a chance to validate the completed file.
    id: item.id,
    sourceId: OFFLINE_SOURCE_ID,
    originType: 'offline',
    name: item.displayName,
    type: mediaType(item.mediaType),
    size: item.videoBytes,
    modified: new Date(item.completedAt * 1000).toISOString(),
    path: '',
    seriesName: item.seriesName,
    seasonNumber: item.seasonNumber,
    episodeNumber: item.episodeNumber,
    exactIdentity: `${item.sourceId}:${item.itemId}:${item.mediaSourceId ?? ''}:${item.variantId ?? ''}`,
    offlineOriginSourceId: item.sourceId,
    offlineOriginItemId: item.itemId,
  }
}

function toMediaDetail(record: OfflineDetailRecord): MediaDetail {
  const snapshot = record.snapshot
  return {
    ...toMediaItem(record),
    name: snapshot.name || record.displayName,
    originalTitle: snapshot.originalTitle,
    type: mediaType(snapshot.mediaType),
    year: snapshot.year,
    rating: snapshot.rating,
    overview: snapshot.overview,
    tagline: snapshot.tagline,
    duration: snapshot.duration,
    genres: snapshot.genres,
    directors: snapshot.directors,
    writers: snapshot.writers,
    cast: snapshot.cast,
    imdbId: snapshot.imdbId,
    tmdbId: snapshot.tmdbId,
    seriesName: snapshot.seriesName,
    seasonNumber: snapshot.seasonNumber,
    episodeNumber: snapshot.episodeNumber,
    mediaSources: [{
      id: record.mediaSourceId ?? record.id,
      providerMediaSourceId: record.mediaSourceId,
      name: record.variantId ? `离线 · ${record.variantId}` : '离线文件',
      size: record.videoBytes,
      isRemote: false,
      sourceId: OFFLINE_SOURCE_ID,
      itemId: record.id,
      exactIdentity: `${record.sourceId}:${record.itemId}:${record.mediaSourceId ?? ''}:${record.variantId ?? ''}`,
    }],
  }
}

function mediaType(value: string): MediaItem['type'] {
  return ['movie', 'series', 'season', 'episode', 'file'].includes(value)
    ? value as MediaItem['type']
    : 'file'
}

export function buildOfflineHierarchy(items: readonly OfflineItemSummary[]): MediaItem[] {
  const roots: MediaItem[] = []
  const series = new Map<string, MediaItem>()
  const seasons = new Map<string, MediaItem>()
  for (const record of items) {
    const playable = toMediaItem(record)
    if (!record.seriesName || record.mediaType !== 'episode') {
      roots.push(playable)
      continue
    }
    const seriesKey = `${record.sourceId}\u0000${record.seriesName}`
    let seriesItem = series.get(seriesKey)
    if (!seriesItem) {
      seriesItem = {
        id: `offline-series:${encodeURIComponent(seriesKey)}`,
        sourceId: OFFLINE_SOURCE_ID,
        originType: 'offline',
        name: record.seriesName,
        type: 'series',
        path: '',
        children: [],
        exactIdentity: `offline-series:${seriesKey}`,
      }
      series.set(seriesKey, seriesItem)
      roots.push(seriesItem)
    }
    const seasonNumber = record.seasonNumber ?? 0
    const seasonKey = `${seriesKey}\u0000${seasonNumber}`
    let season = seasons.get(seasonKey)
    if (!season) {
      season = {
        id: `offline-season:${encodeURIComponent(seasonKey)}`,
        sourceId: OFFLINE_SOURCE_ID,
        originType: 'offline',
        name: seasonNumber > 0 ? `第 ${seasonNumber} 季` : '未分季',
        type: 'season',
        path: '',
        seriesName: record.seriesName,
        seasonNumber,
        children: [],
        exactIdentity: `offline-season:${seasonKey}`,
      }
      seasons.set(seasonKey, season)
      seriesItem.children?.push(season)
    }
    season.children?.push(playable)
  }
  for (const season of seasons.values())
    season.children?.sort((left, right) => (left.episodeNumber ?? 0) - (right.episodeNumber ?? 0))
  for (const item of series.values())
    item.children?.sort((left, right) => (left.seasonNumber ?? 0) - (right.seasonNumber ?? 0))
  return roots
}

function findHierarchyItem(items: readonly MediaItem[], id: string): MediaItem | undefined {
  for (const item of items) {
    if (item.id === id)
      return item
    const child = findHierarchyItem(item.children ?? [], id)
    if (child)
      return child
  }
  return undefined
}

function parseOfflineDanmaku(value: string | undefined): ProviderDanmakuComment[] {
  if (!value)
    return []
  try {
    const parsed = JSON.parse(value) as unknown
    if (!Array.isArray(parsed))
      return []
    return parsed.slice(0, 200_000).flatMap((entry): ProviderDanmakuComment[] => {
      if (!entry || typeof entry !== 'object')
        return []
      const item = entry as Record<string, unknown>
      if (typeof item.id !== 'string' || typeof item.time !== 'number' || !Number.isFinite(item.time) || typeof item.text !== 'string')
        return []
      const mode = item.mode === 'top' || item.mode === 'bottom' ? item.mode : 'scroll'
      return [{ id: item.id.slice(0, 128), time: Math.max(0, item.time), mode, color: typeof item.color === 'string' ? item.color.slice(0, 32) : '#ffffff', text: item.text.slice(0, 2_000) }]
    })
  }
  catch {
    return []
  }
}
