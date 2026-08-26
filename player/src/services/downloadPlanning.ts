import type { DataSource, MediaDetail, MediaItem, MediaSourceOption } from '@/services/datasource/types'
import type { OfflineDetailSnapshot } from '@/services/downloads'
import type { MediaItemActionTarget } from '@/services/mediaActions/types'

const MAX_DOWNLOAD_FILES = 2_000
const MAX_DOWNLOAD_DEPTH = 8

export interface DownloadFilePlan extends MediaItemActionTarget {
  readonly mediaSourceId?: string
  readonly variantId?: string
  readonly expectedBytes?: number
  readonly detailSnapshot: OfflineDetailSnapshot
}

export interface DownloadPlan {
  readonly aggregate: boolean
  readonly displayName: string
  readonly files: readonly DownloadFilePlan[]
}

export interface DownloadPlanSummary {
  readonly fileCount: number
  readonly knownBytes: number
  readonly unknownSizeFiles: number
  readonly usesExplicitSelection: boolean
}

export function summarizeDownloadPlan(plan: DownloadPlan): DownloadPlanSummary {
  return {
    fileCount: plan.files.length,
    knownBytes: plan.files.reduce((total, file) => total + (file.expectedBytes ?? 0), 0),
    unknownSizeFiles: plan.files.filter(file => file.expectedBytes == null).length,
    usesExplicitSelection: plan.files.some(file => Boolean(file.mediaSourceId || file.variantId)),
  }
}

export async function planMediaDownload(source: DataSource, target: MediaItemActionTarget): Promise<DownloadPlan> {
  const detail = await source.getDetail(target.itemId)
  const files: DownloadFilePlan[] = []
  const visited = new Set<string>()
  await expandItem(source, detail, target, files, visited, 0)
  if (files.length === 0)
    throw new Error('没有找到可下载的媒体文件。')
  return {
    aggregate: files.length > 1 || isAggregateType(target.mediaType),
    displayName: target.display.name,
    files,
  }
}

async function expandItem(
  source: DataSource,
  item: MediaItem | MediaDetail,
  root: MediaItemActionTarget,
  output: DownloadFilePlan[],
  visited: Set<string>,
  depth: number,
): Promise<void> {
  if (depth > MAX_DOWNLOAD_DEPTH)
    throw new Error('媒体层级过深，已停止展开下载。')
  const visitKey = `${item.sourceId}:${item.id}`
  if (visited.has(visitKey))
    return
  visited.add(visitKey)

  if (!isAggregateType(item.type)) {
    appendPlayableVersions(item, root, output)
    enforceFileLimit(output.length)
    return
  }

  const inlineChildren = item.children ?? []
  const children = inlineChildren.length > 0 ? inlineChildren : await source.list(item.id)
  for (const child of children) {
    const childDetail = await source.getDetail(child.id).catch(() => child)
    await expandItem(source, childDetail, root, output, visited, depth + 1)
  }
}

function appendPlayableVersions(item: MediaItem | MediaDetail, root: MediaItemActionTarget, output: DownloadFilePlan[]) {
  const mediaSources = 'mediaSources' in item ? item.mediaSources ?? [] : []
  const meaningfulVersions = mediaSources.filter(hasDownloadableVersion)
  if (meaningfulVersions.length === 0) {
    output.push(createFilePlan(item, root))
    return
  }
  // A media card represents one playable item. Downloading every available version here
  // silently multiplies disk usage, so the ordinary action selects the provider's primary
  // version. Explicit version/quality selection is handled by the playback/detail action.
  const selectedVersion = item.id === root.itemId && root.mediaSourceId
    ? meaningfulVersions.find(version => (version.providerMediaSourceId ?? version.id) === root.mediaSourceId)
    : undefined
  if (item.id === root.itemId && root.mediaSourceId && !selectedVersion)
    throw new Error('所选媒体版本已经不可用，请刷新详情后重新选择。')
  output.push(createFilePlan(item, root, selectedVersion ?? meaningfulVersions[0]))
}

function createFilePlan(
  item: MediaItem,
  root: MediaItemActionTarget,
  version?: MediaSourceOption,
): DownloadFilePlan {
  return {
    kind: 'media',
    sourceId: item.sourceId,
    sourceType: root.sourceType,
    itemId: item.id,
    libraryId: item.libraryId ?? root.libraryId,
    mediaType: item.type,
    display: { name: item.name, sourceName: root.display.sourceName },
    mediaSourceId: version?.providerMediaSourceId ?? version?.id,
    variantId: item.id === root.itemId ? root.variantId : undefined,
    expectedBytes: version?.size ?? item.size,
    detailSnapshot: toOfflineDetailSnapshot(item),
  }
}

function toOfflineDetailSnapshot(item: MediaItem | MediaDetail): OfflineDetailSnapshot {
  const detail = item as MediaDetail
  const mediaType = item.type === 'folder' ? 'file' : item.type
  return {
    name: boundedText(item.name, 512) || '离线媒体',
    originalTitle: optionalBoundedText(item.originalTitle, 512),
    mediaType,
    year: finiteInteger(item.year, 0, 9999),
    rating: finiteNumber(item.rating, 0, 10),
    overview: optionalBoundedText(item.overview, 16_384),
    tagline: optionalBoundedText(item.tagline, 512),
    duration: finiteInteger(item.duration, 0, 31_536_000),
    genres: boundedList(detail.genres),
    directors: boundedList(detail.directors),
    writers: boundedList(detail.writers),
    cast: boundedList(detail.cast),
    imdbId: optionalBoundedText(detail.imdbId, 512),
    tmdbId: finiteInteger(detail.tmdbId, 1, Number.MAX_SAFE_INTEGER),
    seriesName: optionalBoundedText(item.seriesName, 512),
    seasonNumber: finiteInteger(item.seasonNumber, 0, 100_000),
    episodeNumber: finiteInteger(item.episodeNumber, 0, 100_000),
  }
}

function boundedList(values: readonly string[] | undefined): string[] {
  return (values ?? []).map(value => boundedText(value, 256)).filter(Boolean).slice(0, 128)
}

function optionalBoundedText(value: string | undefined, max: number): string | undefined {
  const bounded = boundedText(value ?? '', max)
  return bounded || undefined
}

function boundedText(value: string, max: number): string {
  return value.replaceAll('\0', '').trim().slice(0, max)
}

function finiteInteger(value: number | undefined, min: number, max: number): number | undefined {
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.max(min, Math.min(max, Math.trunc(value)))
    : undefined
}

function finiteNumber(value: number | undefined, min: number, max: number): number | undefined {
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.max(min, Math.min(max, value))
    : undefined
}

function hasDownloadableVersion(version: MediaSourceOption): boolean {
  return Boolean(version.id.trim())
}

function isAggregateType(type: MediaItem['type']): boolean {
  return type === 'folder' || type === 'series' || type === 'season'
}

function enforceFileLimit(count: number) {
  if (count > MAX_DOWNLOAD_FILES)
    throw new Error(`单次聚合下载最多支持 ${MAX_DOWNLOAD_FILES} 个文件。`)
}
