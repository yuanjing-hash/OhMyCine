import type {
  RawFileSourceType,
  RawProviderScanItem,
  RawScanPreview,
} from './types'
import type { DataSource, MediaItem } from '@/services/datasource/types'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { isPathWithinRoot, isVideoFileName, normalizeProviderPath, providerParentPath } from './pathUtils'
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
  const finishedAt = new Date().toISOString()
  const status: RawLocalScanStatus = errorCount > 0 || stoppedByLimit ? 'partialFailed' : 'completed'

  addLog(
    status === 'completed' ? 'info' : 'warning',
    `扫描完成：识别 ${preview.records.length} 个视频文件，跳过 ${skippedFileCount} 个非视频文件。`,
  )
  addLog('info', `结构判断：${preview.detection.mode === 'standard' ? '标准目录' : '非标准目录'}，置信度 ${Math.round(preview.detection.confidence * 100)}%。`)

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
    localStorage.setItem(rawScanCacheKey(cache.sourceId, cache.sourceType, cache.rootPath), JSON.stringify(cache))
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
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
