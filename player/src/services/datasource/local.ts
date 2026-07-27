import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption } from './types'
import { invoke } from '@tauri-apps/api/core'
import { loadRawSourceScanCache } from '@/services/scraper/localScanCache'
import { getVideoFileExtension, isPathWithinRoot, isVideoFileName, normalizeProviderPath } from '@/services/scraper/pathUtils'
import { createRawSourceHomeSections, getRawScannedMediaDetail, isRawScannedSyntheticId, listRawScannedChildren } from '@/services/scraper/rawHomeMapping'
import { SourceMetadataCache } from './cache'

const LOCAL_FILE_URL = 'local://filesystem'

export interface LocalFileEntry {
  readonly name: string
  readonly path: string
  readonly isDir: boolean
  readonly size?: number
  readonly modifiedMs?: number | string
}

export interface LocalFileDataSourceOptions {
  readonly listEntries?: (rootPath: string, path?: string) => Promise<LocalFileEntry[]>
  readonly getMetadata?: (rootPath: string, path: string) => Promise<LocalFileEntry>
  readonly getStreamPath?: (rootPath: string, path: string) => Promise<string>
}

interface LocalConfigExtra {
  readonly rootPath: string
}

export interface LocalFileConfigInput {
  readonly id: string
  readonly displayName?: string
  readonly rootPath: string
  readonly order?: number
}

export class LocalFileDataSource implements DataSource {
  private config: DataSourceConfig | null = null
  private rootPath = ''
  private readonly providerRootPath = '/'
  private connected = false
  private readonly cache = new SourceMetadataCache()
  private readonly listEntries: (rootPath: string, path?: string) => Promise<LocalFileEntry[]>
  private readonly getMetadataEntry: (rootPath: string, path: string) => Promise<LocalFileEntry>
  private readonly getStreamFilePath: (rootPath: string, path: string) => Promise<string>

  readonly type = 'local' as const

  constructor(options: LocalFileDataSourceOptions = {}) {
    this.listEntries = options.listEntries ?? defaultListEntries
    this.getMetadataEntry = options.getMetadata ?? defaultGetMetadata
    this.getStreamFilePath = options.getStreamPath ?? defaultGetStreamPath
  }

  get id(): string {
    return this.config?.id ?? ''
  }

  get name(): string {
    return this.config?.displayName ?? this.config?.name ?? '本地文件'
  }

  get isConnected(): boolean {
    return this.connected
  }

  async init(config: DataSourceConfig): Promise<void> {
    this.config = sanitizeExportConfig(config)
    const extra = readLocalExtra(config)
    this.rootPath = extra.rootPath
    this.connected = Boolean(this.rootPath)
  }

  async test(): Promise<boolean> {
    this.ensureConfigured()
    await this.listEntries(this.rootPath, this.providerRootPath)
    this.connected = true
    return true
  }

  destroy(): void {
    this.connected = false
  }

  clearCache(): void {
    this.cache.clear()
  }

  async list(path?: string): Promise<MediaItem[]> {
    const rawChildren = await this.listRawScannedChildren(path)
    if (rawChildren)
      return rawChildren
    if (path && isRawScannedSyntheticId(path))
      return []

    const providerPath = this.resolveProviderPath(path)
    const entries = await this.cache.getOrSet(`list:${providerPath}`, () => this.listEntries(this.rootPath, providerPath))
    return entries
      .filter(entry => entry.isDir || isVideoFileName(entry.name))
      .map(entry => this.mapItem(entry))
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    this.ensureConfigured()
    return [
      {
        id: this.providerRootPath,
        sourceId: this.id,
        name: localBasename(this.rootPath) || '本地文件夹',
        type: 'folders',
      },
    ]
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const trimmed = keyword.trim()
    if (!trimmed)
      return []

    const normalizedKeyword = trimmed.toLocaleLowerCase()
    const results: MediaItem[] = []
    const queue: Array<{ path: string, depth: number }> = [{ path: this.providerRootPath, depth: 0 }]
    const visited = new Set<string>()
    const maxDepth = 2
    const maxVisited = 80

    while (queue.length > 0 && visited.size < maxVisited) {
      const current = queue.shift()
      if (!current || visited.has(current.path))
        continue
      visited.add(current.path)

      let children: MediaItem[]
      try {
        children = await this.list(current.path)
      }
      catch {
        continue
      }

      for (const child of children) {
        if (child.name.toLocaleLowerCase().includes(normalizedKeyword))
          results.push(child)
        if (child.type === 'folder' && current.depth < maxDepth)
          queue.push({ path: child.path, depth: current.depth + 1 })
      }
    }

    return results.slice(0, 100)
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const rawDetail = await this.getRawScannedDetail(id)
    if (rawDetail)
      return rawDetail
    if (isRawScannedSyntheticId(id))
      throw new Error('本地扫描合集不能直接播放，请选择具体文件或分集。')

    const providerPath = this.resolveProviderPath(id)
    const entry = await this.cache.getOrSet(`detail:${providerPath}`, () => this.getMetadataEntry(this.rootPath, providerPath))
    const item = this.mapItem(entry)
    return {
      ...item,
      mediaSources: item.type === 'folder' || !isVideoFileName(item.name) ? [] : [this.mapMediaSource(item, entry)],
    }
  }

  async getStreamURL(id: string): Promise<string> {
    if (isRawScannedSyntheticId(id))
      throw new Error('本地扫描合集不能直接播放，请选择具体文件或分集。')

    const providerPath = this.resolveProviderPath(id)
    const entry = await this.getMetadataEntry(this.rootPath, providerPath)
    if (entry.isDir)
      throw new Error('本地文件夹不能直接播放。')
    if (!isVideoFileName(entry.name))
      throw new Error('该本地文件不是支持的视频格式。')

    return this.getStreamFilePath(this.rootPath, providerPath)
  }

  async getHomeSections(): Promise<HomeSection[]> {
    try {
      const cache = await this.loadRawScanCache()
      return cache ? createRawSourceHomeSections(cache, this.name) : []
    }
    catch {
      return []
    }
  }

  exportConfig(): DataSourceConfig {
    this.ensureConfigured()
    return sanitizeExportConfig(this.config)
  }

  private mapItem(entry: LocalFileEntry): MediaItem {
    const providerPath = this.resolveProviderPath(entry.path)
    return {
      id: providerPath,
      sourceId: this.id,
      libraryId: this.providerRootPath,
      name: entry.name || localBasename(entry.path) || providerPath,
      type: entry.isDir ? 'folder' : 'file',
      size: entry.isDir ? undefined : entry.size,
      modified: modifiedIso(entry.modifiedMs),
      path: providerPath,
    }
  }

  private mapMediaSource(item: MediaItem, entry: LocalFileEntry): MediaSourceOption {
    return {
      id: 'default',
      name: '本地文件',
      container: getVideoFileExtension(item.name) ?? undefined,
      size: entry.size,
      isRemote: false,
    }
  }

  private resolveProviderPath(path?: string): string {
    const raw = path?.trim()
    if (!raw || raw === '/')
      return this.providerRootPath

    const providerPath = normalizeProviderPath(raw)
    if (!isPathWithinRoot(providerPath, this.providerRootPath))
      throw new Error('本地文件路径不在已选择的根目录内。')
    return providerPath
  }

  private ensureConfigured(): void {
    if (!this.config || !this.rootPath)
      throw new Error('本地文件数据源未配置。')
  }

  private async loadRawScanCache() {
    return loadRawSourceScanCache(this.id, 'local', this.providerRootPath)
  }

  private async getRawScannedDetail(id: string): Promise<MediaDetail | null> {
    try {
      const cache = await this.loadRawScanCache()
      return cache ? getRawScannedMediaDetail(cache, id) : null
    }
    catch {
      return null
    }
  }

  private async listRawScannedChildren(id: string | undefined): Promise<MediaItem[] | null> {
    if (!id || !isRawScannedSyntheticId(id))
      return null
    try {
      const cache = await this.loadRawScanCache()
      return cache ? listRawScannedChildren(cache, id) : null
    }
    catch {
      return null
    }
  }
}

export function createLocalFileDataSourceConfig(input: LocalFileConfigInput): DataSourceConfig {
  const rootPath = normalizeLocalRootPath(input.rootPath)
  const displayName = input.displayName?.trim() || localBasename(rootPath) || '本地文件'
  return {
    id: input.id,
    type: 'local',
    name: displayName,
    displayName,
    order: input.order ?? 0,
    url: LOCAL_FILE_URL,
    enabled: true,
    extra: {
      rootPath,
    },
  }
}

export async function validateLocalFileDataSourceConfig(
  config: DataSourceConfig,
  options: LocalFileDataSourceOptions = {},
): Promise<MediaLibrary[]> {
  const source = new LocalFileDataSource(options)
  try {
    await source.init(config)
    await source.test()
    return await source.listLibraries()
  }
  finally {
    source.destroy()
  }
}

export function readLocalRootPath(config: DataSourceConfig | null | undefined): string {
  if (!config || config.type !== 'local')
    return ''
  try {
    return readLocalExtra(config).rootPath
  }
  catch {
    return ''
  }
}

export function readLocalProviderRootPath(_config: DataSourceConfig | null | undefined): string {
  return '/'
}

export function normalizeLocalRootPath(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed)
    throw new Error('请选择本地文件夹。')
  validateLocalPathText(trimmed)
  return trimTrailingLocalSeparators(trimmed)
}

function readLocalExtra(config: DataSourceConfig): LocalConfigExtra {
  if (config.type !== 'local')
    throw new Error('本地文件数据源类型无效。')

  const rootPath = typeof config.extra?.rootPath === 'string'
    ? normalizeLocalRootPath(config.extra.rootPath)
    : ''
  if (!rootPath)
    throw new Error('本地文件数据源缺少根目录。')

  return { rootPath }
}

async function defaultListEntries(rootPath: string, path?: string): Promise<LocalFileEntry[]> {
  return invoke<LocalFileEntry[]>('local_file_list', { rootPath, path })
}

async function defaultGetMetadata(rootPath: string, path: string): Promise<LocalFileEntry> {
  return invoke<LocalFileEntry>('local_file_metadata', { rootPath, path })
}

async function defaultGetStreamPath(rootPath: string, path: string): Promise<string> {
  return invoke<string>('local_file_stream_path', { rootPath, path })
}

function modifiedIso(value: number | string | undefined): string | undefined {
  const millis = typeof value === 'number'
    ? value
    : typeof value === 'string'
      ? Number(value)
      : Number.NaN
  return Number.isFinite(millis) ? new Date(millis).toISOString() : undefined
}

function localBasename(path: string): string {
  return trimTrailingLocalSeparators(path)
    .replace(/\\/g, '/')
    .split('/')
    .filter(Boolean)
    .at(-1)
    ?? path
}

function trimTrailingLocalSeparators(path: string): string {
  const trimmed = path.trim()
  if (trimmed === '/' || /^[a-z]:[\\/]$/i.test(trimmed))
    return trimmed
  return trimmed.replace(/[\\/]+$/, '')
}

function validateLocalPathText(value: string): void {
  const trimmed = value.trim()
  if (!trimmed || isUrlLikeLocalPath(trimmed))
    throw new Error('本地文件路径格式无效。')

  for (const segment of trimmed.split(/[\\/]/).filter(Boolean)) {
    let current = segment
    for (let attempt = 0; attempt < 2; attempt += 1) {
      if (current === '.' || current === '..')
        throw new Error('本地文件路径不能包含相对段。')
      try {
        const decoded = decodeURIComponent(current)
        if (decoded === current)
          break
        current = decoded
      }
      catch {
        break
      }
    }
    if (current === '.' || current === '..')
      throw new Error('本地文件路径不能包含相对段。')
  }
}

function isUrlLikeLocalPath(value: string): boolean {
  return /^(?:https?|webdav|ftp|sftp|file|blob):/i.test(value)
}

function sanitizeExportConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    throw new Error('本地文件数据源未配置。')

  const safeExtra = Object.fromEntries(
    Object.entries(config.extra ?? {}).filter(([key]) => !isSensitiveConfigKey(key)),
  )

  return {
    ...config,
    url: LOCAL_FILE_URL,
    extra: safeExtra,
  }
}

function isSensitiveConfigKey(key: string): boolean {
  const normalized = key.toLowerCase()
  return ['apikey', 'api_key', 'access_token', 'passwd', 'pwd'].includes(normalized)
    || normalized.includes('token')
    || normalized.includes('password')
    || normalized.includes('username')
    || normalized.includes('authorization')
    || normalized.includes('cookie')
    || normalized.includes('passkey')
}
