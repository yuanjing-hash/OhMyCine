import type { DataSource, MediaDetail, MediaItem, MediaSourceOption } from '@/services/datasource/types'
import type { MediaItemActionTarget } from '@/services/mediaActions/types'

const MAX_DOWNLOAD_FILES = 2_000
const MAX_DOWNLOAD_DEPTH = 8

export interface DownloadFilePlan extends MediaItemActionTarget {
  readonly mediaSourceId?: string
  readonly expectedBytes?: number
}

export interface DownloadPlan {
  readonly aggregate: boolean
  readonly displayName: string
  readonly files: readonly DownloadFilePlan[]
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
  for (const [index, version] of meaningfulVersions.entries())
    output.push(createFilePlan(item, root, version, meaningfulVersions.length > 1 ? index + 1 : undefined))
}

function createFilePlan(
  item: MediaItem,
  root: MediaItemActionTarget,
  version?: MediaSourceOption,
  versionNumber?: number,
): DownloadFilePlan {
  const suffix = versionNumber == null ? '' : ` · 版本 ${versionNumber}${version?.name ? ` (${version.name})` : ''}`
  return {
    kind: 'media',
    sourceId: item.sourceId,
    sourceType: root.sourceType,
    itemId: item.id,
    libraryId: item.libraryId ?? root.libraryId,
    mediaType: item.type,
    display: { name: `${item.name}${suffix}`, sourceName: root.display.sourceName },
    mediaSourceId: version?.id,
    expectedBytes: version?.size ?? item.size,
  }
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
