import type { TmdbEpisodeMetadata, TmdbMetadata } from './tmdb'
import type {
  RawCategoryAssignment,
  RawFileRecord,
  RawFileSourceType,
  RawMediaCandidate,
  RawProviderScanItem,
  RawScanPreview,
  RawScrapedMediaItem,
} from './types'
import type { DataSource, MediaItem } from '@/services/datasource/types'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { enrichRawMediaCandidates } from './metadataEnrichment'
import { isLikelySensitiveProviderPath, isPathWithinRoot, isVideoFileName, normalizeProviderPath, providerParentPath } from './pathUtils'
import { createRawScanPreview } from './scanner'

export type RawLocalScanStatus = 'completed' | 'partialFailed'
export type RawLocalScanLogLevel = 'info' | 'warning' | 'error'

export interface RawLocalScanLogEntry {
  readonly timestamp: string
  readonly level: RawLocalScanLogLevel
  readonly message: string
  readonly path?: string
}

export interface RawLocalScanCache extends RawScanPreview {
  readonly version: 1
  readonly scanId: string
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
  readonly status: RawLocalScanStatus
  readonly startedAt: string
  readonly finishedAt: string
  readonly folderCount: number
  readonly fileCount: number
  readonly skippedFileCount: number
  readonly errorCount: number
  readonly logs: RawLocalScanLogEntry[]
  readonly scrapedItems?: RawScrapedMediaItem[]
}

export interface RunRawSourceScanInput {
  readonly source: Pick<DataSource, 'list'>
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath?: string
  readonly maxDepth?: number
  readonly maxFolders?: number
  readonly maxEntries?: number
  readonly onLog?: (entry: RawLocalScanLogEntry) => void
}

const RAW_SCAN_CACHE_VERSION = 1
const RAW_SCAN_CACHE_KEY_PREFIX = 'ohmycine-raw-source-scan-cache-v1'
const DEFAULT_MAX_DEPTH = 12
const DEFAULT_MAX_FOLDERS = 600
const DEFAULT_MAX_ENTRIES = 8_000
export async function runRawSourceLocalScan(input: RunRawSourceScanInput): Promise<RawLocalScanCache> {
  const rootPath = normalizeProviderPath(input.rootPath)
  const maxDepth = input.maxDepth ?? DEFAULT_MAX_DEPTH
  const maxFolders = input.maxFolders ?? DEFAULT_MAX_FOLDERS
  const maxEntries = input.maxEntries ?? DEFAULT_MAX_ENTRIES
  const startedAt = new Date().toISOString()
  const logs: RawLocalScanLogEntry[] = []
  const rawItems: RawProviderScanItem[] = []
  const queue: Array<{ path: string, depth: number }> = [{ path: rootPath, depth: 0 }]
  const visited = new Set<string>()
  let folderCount = 0
  let skippedFileCount = 0
  let errorCount = 0
  let stoppedByLimit = false

  const addLog = (level: RawLocalScanLogLevel, message: string, path?: string) => {
    const entry: RawLocalScanLogEntry = {
      timestamp: new Date().toISOString(),
      level,
      message,
      path,
    }
    logs.push(entry)
    input.onLog?.(entry)
  }

  addLog('info', `开始只读扫描，范围：${rootPath}`)

  while (queue.length > 0) {
    if (folderCount >= maxFolders || rawItems.length >= maxEntries) {
      stoppedByLimit = true
      addLog('warning', `扫描达到 MVP 上限：${folderCount} 个目录、${rawItems.length} 个文件。`)
      break
    }

    const current = queue.shift()
    if (!current || visited.has(current.path))
      continue

    const currentPath = current.path
    visited.add(currentPath)
    folderCount += 1

    let children: MediaItem[]
    try {
      children = await input.source.list(currentPath)
    }
    catch (error) {
      errorCount += 1
      const message = toSafeErrorMessage(error, '目录读取失败，已跳过该分支。')
      addLog('error', message, currentPath)
      if (currentPath === rootPath && rawItems.length === 0)
        throw new Error(message)
      continue
    }

    for (const child of children) {
      const childPath = normalizeMediaItemPath(child)
      if (!childPath) {
        errorCount += 1
        addLog('warning', '跳过了路径格式不安全或缺失的条目。', currentPath)
        continue
      }

      if (isDirectoryMediaItem(child)) {
        if (!isPathWithinRoot(childPath, rootPath)) {
          errorCount += 1
          addLog('warning', '跳过了不在当前扫描根目录内的目录。', childPath)
          continue
        }
        if (current.depth >= maxDepth) {
          stoppedByLimit = true
          addLog('warning', `扫描达到深度上限 ${maxDepth}，已跳过更深目录。`, childPath)
          continue
        }
        if (!visited.has(childPath))
          queue.push({ path: childPath, depth: current.depth + 1 })
        continue
      }

      if (!isPathWithinRoot(childPath, rootPath)) {
        errorCount += 1
        addLog('warning', '跳过了不在当前扫描根目录内的文件。', childPath)
        continue
      }

      rawItems.push(toRawProviderScanItem(child, childPath, currentPath))
      if (!isVideoFileName(child.name))
        skippedFileCount += 1

      if (rawItems.length >= maxEntries)
        break
    }
  }

  const preview = createRawScanPreview(rawItems, {
    sourceId: input.sourceId,
    sourceType: input.sourceType,
    rootPath,
  })
  addLog('info', `结构判断：${preview.detection.mode === 'standard' ? '标准目录' : '非标准目录'}，置信度 ${Math.round(preview.detection.confidence * 100)}%。`)
  const scrapedItems = await enrichRawMediaCandidates(preview.candidates, {
    onLog: (entry) => {
      logs.push(entry)
      input.onLog?.(entry)
    },
  })
  const scrapedItemsByRecordId = new Map(scrapedItems.map(item => [item.recordId, item]))
  const candidates = preview.candidates.map((candidate) => {
    const scraped = scrapedItemsByRecordId.get(candidate.record.id)
    return {
      ...candidate,
      scrapeMetadata: scraped?.metadata,
      categoryAssignment: scraped?.categoryAssignment,
    } satisfies RawMediaCandidate
  })
  const finishedAt = new Date().toISOString()
  const status: RawLocalScanStatus = errorCount > 0 || stoppedByLimit ? 'partialFailed' : 'completed'
  const matchedCount = scrapedItems.filter(item => item.matchStatus === 'matched').length

  addLog(
    status === 'completed' ? 'info' : 'warning',
    `扫描完成：识别 ${preview.records.length} 个视频文件，TMDB 命中 ${matchedCount} 个候选，跳过 ${skippedFileCount} 个非视频文件。`,
  )

  let cache: RawLocalScanCache = {
    version: RAW_SCAN_CACHE_VERSION,
    scanId: createScanId(),
    sourceId: input.sourceId,
    sourceType: input.sourceType,
    rootPath,
    status,
    startedAt,
    finishedAt,
    folderCount,
    fileCount: rawItems.length,
    skippedFileCount,
    errorCount,
    logs,
    ...preview,
    candidates,
    scrapedItems,
  }

  if (!saveRawSourceScanCache(cache)) {
    addLog('warning', '本地扫描缓存写入失败，本次结果仅在当前页面临时可用。')
    cache = {
      ...cache,
      status: 'partialFailed',
      logs,
    }
  }
  return cache
}

export function loadRawSourceScanCache(
  sourceId: string,
  sourceType: RawFileSourceType,
  rootPath?: string,
): RawLocalScanCache | null {
  try {
    const raw = localStorage.getItem(rawScanCacheKey(sourceId, sourceType, normalizeProviderPath(rootPath)))
    if (!raw)
      return null
    const value = JSON.parse(raw) as unknown
    return isRawLocalScanCache(value, sourceId, sourceType, normalizeProviderPath(rootPath)) ? value : null
  }
  catch {
    return null
  }
}

export function saveRawSourceScanCache(cache: RawLocalScanCache): boolean {
  try {
    const safeCache = sanitizeRawLocalScanCache(cache)
    localStorage.setItem(rawScanCacheKey(safeCache.sourceId, safeCache.sourceType, safeCache.rootPath), JSON.stringify(safeCache))
    return true
  }
  catch {
    return false
  }
}

export function clearRawSourceScanCache(sourceId: string, sourceType: RawFileSourceType, rootPath?: string): void {
  try {
    localStorage.removeItem(rawScanCacheKey(sourceId, sourceType, normalizeProviderPath(rootPath)))
  }
  catch {
    // Cache clearing is best-effort and must not affect browsing/playback.
  }
}

function toRawProviderScanItem(item: MediaItem, providerPath: string, parentPath: string): RawProviderScanItem {
  return {
    name: item.name,
    path: providerPath,
    providerPath,
    parentPath: item.path ? providerParentPath(providerPath) : parentPath,
    isDirectory: false,
    type: 'file',
    size: item.size,
    modifiedAt: item.modified,
  }
}

function normalizeMediaItemPath(item: MediaItem): string | null {
  const rawPath = item.path || item.id
  if (!rawPath)
    return null
  if (isLikelySensitiveProviderPath(rawPath))
    return null

  try {
    return normalizeProviderPath(rawPath)
  }
  catch {
    return null
  }
}

function isDirectoryMediaItem(item: MediaItem): boolean {
  return item.type === 'folder' || item.type === 'season'
}

function rawScanCacheKey(sourceId: string, sourceType: RawFileSourceType, rootPath: string): string {
  return `${RAW_SCAN_CACHE_KEY_PREFIX}:${encodeURIComponent(sourceType)}:${encodeURIComponent(sourceId)}:${encodeURIComponent(rootPath)}`
}

function createScanId(): string {
  if (globalThis.crypto?.randomUUID)
    return globalThis.crypto.randomUUID()
  return `scan-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

function sanitizeRawLocalScanCache(cache: RawLocalScanCache): RawLocalScanCache {
  const scrapedItems = cache.scrapedItems?.map(sanitizeRawScrapedMediaItem)
  const scrapedItemsByRecordId = new Map(scrapedItems?.map(item => [item.recordId, item]) ?? [])
  const candidates = cache.candidates.map((candidate) => {
    const scraped = scrapedItemsByRecordId.get(candidate.record.id)
    return sanitizeRawMediaCandidate({
      ...candidate,
      scrapeMetadata: scraped?.metadata ?? candidate.scrapeMetadata,
      categoryAssignment: scraped?.categoryAssignment ?? candidate.categoryAssignment,
    })
  })

  return {
    version: RAW_SCAN_CACHE_VERSION,
    scanId: cache.scanId,
    sourceId: cache.sourceId,
    sourceType: cache.sourceType,
    rootPath: cache.rootPath,
    status: cache.status,
    startedAt: cache.startedAt,
    finishedAt: cache.finishedAt,
    folderCount: cache.folderCount,
    fileCount: cache.fileCount,
    skippedFileCount: cache.skippedFileCount,
    errorCount: cache.errorCount,
    logs: cache.logs.map(entry => ({
      timestamp: entry.timestamp,
      level: entry.level,
      message: entry.message,
      path: entry.path,
    })),
    records: cache.records.map(sanitizeRawFileRecord),
    detection: cache.detection,
    candidates,
    scrapedItems,
  }
}

function sanitizeRawMediaCandidate(candidate: RawMediaCandidate): RawMediaCandidate {
  return {
    kind: candidate.kind,
    parseStatus: candidate.parseStatus,
    record: sanitizeRawFileRecord(candidate.record),
    title: candidate.title,
    normalizedTitle: candidate.normalizedTitle,
    year: candidate.year,
    seriesTitle: candidate.seriesTitle,
    seasonNumber: candidate.seasonNumber,
    episodeNumber: candidate.episodeNumber,
    categoryHint: candidate.categoryHint,
    scrapeMetadata: sanitizeTmdbMetadata(candidate.scrapeMetadata),
    categoryAssignment: sanitizeCategoryAssignment(candidate.categoryAssignment),
    confidence: candidate.confidence,
    signals: [...candidate.signals],
  }
}

function sanitizeRawFileRecord(record: RawFileRecord): RawFileRecord {
  return {
    id: record.id,
    sourceId: record.sourceId,
    sourceType: record.sourceType,
    rootPath: record.rootPath,
    providerPath: record.providerPath,
    relativePath: record.relativePath,
    parentPath: record.parentPath,
    fileName: record.fileName,
    extension: record.extension,
    size: record.size,
    modifiedAt: record.modifiedAt,
  }
}

function sanitizeRawScrapedMediaItem(item: RawScrapedMediaItem): RawScrapedMediaItem {
  const categoryAssignment = sanitizeCategoryAssignment(item.categoryAssignment)
  return {
    recordId: item.recordId,
    providerPath: item.providerPath,
    matchStatus: item.matchStatus,
    searchTitles: [...item.searchTitles],
    matchedSearchTitle: item.matchedSearchTitle,
    metadata: sanitizeTmdbMetadata(item.metadata),
    episodeMetadata: sanitizeTmdbEpisodeMetadata(item.episodeMetadata),
    mediaType: item.mediaType,
    categoryName: item.categoryName,
    matchedRuleId: item.matchedRuleId,
    matchedRuleName: item.matchedRuleName,
    categoryAssignment,
    errorMessage: item.errorMessage,
  }
}

function sanitizeTmdbMetadata(metadata: TmdbMetadata | undefined): TmdbMetadata | undefined {
  if (!metadata)
    return undefined

  return {
    tmdbId: metadata.tmdbId,
    mediaType: metadata.mediaType,
    title: metadata.title,
    originalTitle: metadata.originalTitle,
    imdbId: metadata.imdbId,
    tvdbId: metadata.tvdbId,
    overview: metadata.overview,
    releaseDate: metadata.releaseDate,
    releaseYear: metadata.releaseYear,
    rating: metadata.rating,
    genreIds: [...metadata.genreIds],
    genres: [...metadata.genres],
    originalLanguage: metadata.originalLanguage,
    originCountries: [...metadata.originCountries],
    productionCountries: [...metadata.productionCountries],
    posterPath: metadata.posterPath,
    backdropPath: metadata.backdropPath,
    titleLogoPath: metadata.titleLogoPath,
    posterUrl: metadata.posterUrl,
    backdropUrl: metadata.backdropUrl,
    titleLogoUrl: metadata.titleLogoUrl,
    scrapedAt: metadata.scrapedAt,
  }
}

function sanitizeTmdbEpisodeMetadata(metadata: TmdbEpisodeMetadata | undefined): TmdbEpisodeMetadata | undefined {
  if (!metadata)
    return undefined

  return {
    tmdbEpisodeId: metadata.tmdbEpisodeId,
    tvTmdbId: metadata.tvTmdbId,
    seasonNumber: metadata.seasonNumber,
    episodeNumber: metadata.episodeNumber,
    name: metadata.name,
    overview: metadata.overview,
    airDate: metadata.airDate,
    runtime: metadata.runtime,
    rating: metadata.rating,
    stillPath: metadata.stillPath,
    stillUrl: metadata.stillUrl,
    scrapedAt: metadata.scrapedAt,
  }
}

function sanitizeCategoryAssignment(assignment: RawCategoryAssignment | undefined): RawCategoryAssignment | undefined {
  if (!assignment)
    return undefined

  return {
    categoryName: assignment.categoryName,
    source: assignment.source,
    matchedRuleId: assignment.matchedRuleId,
    matchedRuleName: assignment.matchedRuleName,
  }
}

function isRawLocalScanCache(
  value: unknown,
  sourceId: string,
  sourceType: RawFileSourceType,
  rootPath: string,
): value is RawLocalScanCache {
  if (!isRecord(value))
    return false
  return value.version === RAW_SCAN_CACHE_VERSION
    && value.sourceId === sourceId
    && value.sourceType === sourceType
    && value.rootPath === rootPath
    && (value.status === 'completed' || value.status === 'partialFailed')
    && typeof value.startedAt === 'string'
    && typeof value.finishedAt === 'string'
    && typeof value.folderCount === 'number'
    && typeof value.fileCount === 'number'
    && typeof value.skippedFileCount === 'number'
    && typeof value.errorCount === 'number'
    && isRecord(value.detection)
    && Array.isArray(value.records)
    && Array.isArray(value.candidates)
    && Array.isArray(value.logs)
    && (value.scrapedItems == null || Array.isArray(value.scrapedItems))
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
