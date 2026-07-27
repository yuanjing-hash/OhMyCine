import type { CloudDrive2CredentialValue } from './credentialStore'
import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption, MediaStreamRequest } from './types'
import { invoke } from '@tauri-apps/api/core'
import { createRawSourceHomeSections, getRawScannedMediaDetail, isRawScannedSyntheticId, listRawScannedChildren, loadRawSourceScanCache } from '@/services/scraper'
import { getVideoFileExtension, isPathWithinRoot, isVideoFileName, normalizeProviderPath, providerBasename } from '@/services/scraper/pathUtils'
import { SourceMetadataCache } from './cache'
import { createCredentialRef, readCloudDrive2Credential, readRawCredentialBackup, removeCredential, saveCloudDrive2Credential, saveRawCredentialBackup } from './credentialStore'
import { redactSensitiveText } from './errors'

interface CloudDrive2ConfigExtra {
  readonly credentialRef?: string
  readonly rootPath: string
}

interface CloudDrive2NativeFileEntry {
  readonly name: string
  readonly path: string
  readonly isDir: boolean
  readonly size?: number
  readonly modifiedMs?: number
}

interface CloudDrive2NativeRequest {
  readonly baseUrl: string
  readonly apiToken: string
  readonly path: string
}

interface CloudDrive2NativeSearchRequest extends CloudDrive2NativeRequest {
  readonly keyword: string
}

export interface CloudDrive2Bridge {
  list: (request: CloudDrive2NativeRequest) => Promise<unknown>
  search: (request: CloudDrive2NativeSearchRequest) => Promise<unknown>
  getStream: (request: CloudDrive2NativeRequest) => Promise<unknown>
}

export interface CloudDrive2DataSourceOptions {
  readonly bridge?: CloudDrive2Bridge
  readonly readCredential?: (ref: string) => Promise<CloudDrive2CredentialValue | null>
}

export interface CloudDrive2TokenConfigInput {
  readonly id: string
  readonly url: string
  readonly displayName?: string
  readonly apiToken: string
  readonly rootPath?: string
  readonly order?: number
}

export interface CloudDrive2TokenConfigResult {
  readonly config: DataSourceConfig
  readonly libraries: MediaLibrary[]
}

export interface CloudDrive2SetupSessionInput {
  readonly id: string
  readonly url: string
  readonly displayName?: string
  readonly apiToken: string
  readonly order?: number
}

const defaultCloudDrive2Bridge: CloudDrive2Bridge = {
  list: request => invoke('clouddrive2_list', { request }),
  search: request => invoke('clouddrive2_search', { request }),
  getStream: request => invoke('clouddrive2_get_stream', { request }),
}

export class CloudDrive2DataSource implements DataSource {
  private config: DataSourceConfig | null = null
  private baseUrl = ''
  private credentialRef = ''
  private credential: CloudDrive2CredentialValue | null = null
  private rootPath = '/'
  private connected = false
  private readonly cache = new SourceMetadataCache()
  private readonly bridge: CloudDrive2Bridge
  private readonly readCredential: (ref: string) => Promise<CloudDrive2CredentialValue | null>

  readonly type = 'clouddrive2' as const

  constructor(options: CloudDrive2DataSourceOptions = {}) {
    this.bridge = options.bridge ?? defaultCloudDrive2Bridge
    this.readCredential = options.readCredential ?? readCloudDrive2Credential
  }

  get id(): string {
    return this.config?.id ?? ''
  }

  get name(): string {
    return this.config?.displayName ?? this.config?.name ?? 'CloudDrive2'
  }

  get isConnected(): boolean {
    return this.connected
  }

  async init(config: DataSourceConfig): Promise<void> {
    this.config = sanitizeExportConfig(config)
    this.baseUrl = normalizeCloudDrive2BaseUrl(config.url)
    const extra = readCloudDrive2Extra(config)
    this.credentialRef = extra.credentialRef ?? ''
    this.rootPath = extra.rootPath
    this.credential = await this.readStoredCredential()
    this.connected = Boolean(this.baseUrl && this.credential)
  }

  async test(): Promise<boolean> {
    this.ensureConfigured()
    await this.listProviderEntries(this.rootPath)
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

    const providerPath = this.resolveLibraryPath(path)
    const entries = await this.cache.getOrSet(`list:${providerPath}`, () => this.listProviderEntries(providerPath))
    return entries
      .filter(entry => isPathWithinRoot(entry.path, this.rootPath))
      .filter(entry => entry.path !== providerPath)
      .filter(entry => entry.isDir || isVideoFileName(entry.name))
      .map(entry => this.mapItem(entry))
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    this.ensureConfigured()
    return [{
      id: this.rootPath,
      sourceId: this.id,
      name: this.rootPath === '/' ? 'CloudDrive2 文件目录' : (providerBasename(this.rootPath) ?? this.rootPath),
      type: 'folders',
    }]
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const trimmed = keyword.trim()
    if (!trimmed)
      return []
    const credential = await this.ensureCredential()
    try {
      const response = await this.bridge.search({
        baseUrl: this.baseUrl,
        apiToken: credential.apiToken,
        path: this.rootPath,
        keyword: trimmed,
      })
      this.connected = true
      return parseNativeFileEntries(response)
        .filter(entry => isPathWithinRoot(entry.path, this.rootPath))
        .filter(entry => entry.isDir || isVideoFileName(entry.name))
        .map(entry => this.mapItem(entry))
        .slice(0, 100)
    }
    catch (error) {
      this.connected = false
      throw new Error(redactSensitiveText(error))
    }
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const rawDetail = await this.getRawScannedDetail(id)
    if (rawDetail)
      return rawDetail
    if (isRawScannedSyntheticId(id))
      throw new Error('CloudDrive2 本地扫描合集不能直接播放，请选择具体文件或分集。')

    const path = this.resolveLibraryPath(id)
    const entry = await this.cache.getOrSet(`detail:${path}`, async () => {
      if (path === '/')
        return createFallbackEntry(path, true)
      const entries = await this.listProviderEntries(parentProviderPath(path))
      return entries.find(item => item.path === path) ?? createFallbackEntry(path, false)
    })
    this.ensureEntryInRoot(entry)
    const item = this.mapItem(entry)
    return {
      ...item,
      mediaSources: item.type === 'folder' || !isVideoFileName(item.name) ? [] : [this.mapMediaSource(entry)],
    }
  }

  async getStreamURL(id: string): Promise<string> {
    return (await this.getStreamRequest(id)).url
  }

  async getStreamRequest(id: string): Promise<MediaStreamRequest> {
    if (isRawScannedSyntheticId(id))
      throw new Error('CloudDrive2 剧集合集不能直接播放，请选择具体分集。')
    const path = this.resolveLibraryPath(id)
    if (!isVideoFileName(providerBasename(path) ?? ''))
      throw new Error('该 CloudDrive2 文件不是支持的视频格式。')
    const credential = await this.ensureCredential()
    try {
      const response = parseNativeStreamResponse(await this.bridge.getStream({
        baseUrl: this.baseUrl,
        apiToken: credential.apiToken,
        path,
      }))
      this.connected = true
      return response
    }
    catch (error) {
      this.connected = false
      throw new Error(redactSensitiveText(error))
    }
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
    this.ensureConfigured({ requireCredential: false })
    return sanitizeExportConfig(this.config)
  }

  private async listProviderEntries(path: string): Promise<CloudDrive2NativeFileEntry[]> {
    const credential = await this.ensureCredential()
    try {
      const response = await this.bridge.list({
        baseUrl: this.baseUrl,
        apiToken: credential.apiToken,
        path: this.resolveLibraryPath(path),
      })
      this.connected = true
      return parseNativeFileEntries(response)
    }
    catch (error) {
      this.connected = false
      throw new Error(redactSensitiveText(error))
    }
  }

  private async ensureCredential(): Promise<CloudDrive2CredentialValue> {
    this.ensureConfigured({ requireCredential: false })
    if (this.credential)
      return this.credential
    const credential = await this.readStoredCredential()
    if (!credential)
      throw new Error('CloudDrive2 API Token 缺失。请在设置的数据源管理中重新编辑。')
    this.credential = credential
    this.connected = true
    return credential
  }

  private async readStoredCredential(): Promise<CloudDrive2CredentialValue | null> {
    if (!this.credentialRef)
      return null
    return this.readCredential(this.credentialRef)
  }

  private mapItem(entry: CloudDrive2NativeFileEntry): MediaItem {
    return {
      id: entry.path,
      sourceId: this.id,
      libraryId: this.rootPath,
      name: entry.name,
      type: entry.isDir ? 'folder' : 'file',
      size: entry.isDir ? undefined : entry.size,
      modified: modifiedIso(entry.modifiedMs),
      path: entry.path,
    }
  }

  private mapMediaSource(entry: CloudDrive2NativeFileEntry): MediaSourceOption {
    return {
      id: 'default',
      name: 'CloudDrive2 原生直链',
      container: getVideoFileExtension(entry.name) ?? undefined,
      size: entry.size,
      isRemote: true,
    }
  }

  private resolveLibraryPath(path?: string): string {
    const raw = path?.trim()
    if (!raw)
      return this.rootPath
    const normalized = normalizeCloudDrive2Path(raw)
    if (normalized === '/' && this.rootPath !== '/')
      return this.rootPath
    if (!isPathWithinRoot(normalized, this.rootPath))
      throw new Error('CloudDrive2 路径不在已选择的根目录内。')
    return normalized
  }

  private ensureEntryInRoot(entry: CloudDrive2NativeFileEntry): void {
    if (!isPathWithinRoot(entry.path, this.rootPath))
      throw new Error('CloudDrive2 返回的文件路径不在已选择的根目录内。')
  }

  private ensureConfigured(options: { requireCredential?: boolean } = {}): void {
    if (!this.config || !this.baseUrl)
      throw new Error('CloudDrive2 数据源未配置。')
    if (options.requireCredential !== false && !this.credential && !this.credentialRef)
      throw new Error('CloudDrive2 API Token 缺失。请在设置的数据源管理中重新编辑。')
  }

  private async loadRawScanCache() {
    return loadRawSourceScanCache(this.id, 'clouddrive2', this.rootPath)
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

export async function createAuthenticatedCloudDrive2SetupSource(input: CloudDrive2SetupSessionInput): Promise<CloudDrive2DataSource> {
  const credential = normalizeApiToken(input.apiToken)
  const displayName = input.displayName?.trim() || 'CloudDrive2'
  const credentialRef = createCredentialRef(input.id, 'clouddrive2')
  const source = new CloudDrive2DataSource({ readCredential: async () => credential })
  try {
    await source.init({
      id: input.id,
      type: 'clouddrive2',
      name: displayName,
      displayName,
      order: input.order ?? 0,
      url: input.url.trim(),
      enabled: true,
      extra: { credentialRef, rootPath: '/' },
    })
    await source.test()
    return source
  }
  catch (error) {
    source.destroy()
    throw error
  }
}

export async function saveCloudDrive2TokenAndCreateConfig(input: CloudDrive2TokenConfigInput): Promise<CloudDrive2TokenConfigResult> {
  const credential = normalizeApiToken(input.apiToken)
  const credentialRef = createCredentialRef(input.id, 'clouddrive2')
  const displayName = input.displayName?.trim() || 'CloudDrive2'
  const rootPath = normalizeCloudDrive2Path(input.rootPath)
  const config: DataSourceConfig = {
    id: input.id,
    type: 'clouddrive2',
    name: displayName,
    displayName,
    order: input.order ?? 0,
    url: input.url.trim(),
    enabled: true,
    extra: {
      credentialRef,
      credentialVersion: Date.now(),
      rootPath,
    },
  }
  const previousCredential = await readRawCredentialBackup(credentialRef)
  try {
    await saveCloudDrive2Credential(credentialRef, credential)
  }
  catch (error) {
    throw new Error(redactSensitiveText(error))
  }

  const source = new CloudDrive2DataSource()
  try {
    await source.init(config)
    await source.test()
    const libraries = await source.listLibraries()
    return {
      config: {
        ...config,
        extra: {
          ...config.extra,
          libraries: libraries.map(library => ({ id: library.id, name: library.name, type: library.type })),
        },
      },
      libraries,
    }
  }
  catch (error) {
    if (previousCredential)
      await saveRawCredentialBackup(credentialRef, previousCredential)
    else
      await removeCredential(credentialRef)
    throw new Error(redactSensitiveText(error))
  }
  finally {
    source.destroy()
  }
}

export function readCloudDrive2RootPath(config: DataSourceConfig | null | undefined): string {
  if (!config || config.type !== 'clouddrive2')
    return '/'
  try {
    return readCloudDrive2Extra(config).rootPath
  }
  catch {
    return '/'
  }
}

export function normalizeCloudDrive2RootPath(value: string | undefined): string {
  return normalizeCloudDrive2Path(value)
}

function normalizeApiToken(value: string): CloudDrive2CredentialValue {
  const apiToken = value.trim()
  if (!apiToken)
    throw new Error('请输入 CloudDrive2 API Token。')
  return { apiToken }
}

function normalizeCloudDrive2BaseUrl(value: string): string {
  const trimmed = value.trim()
  if (!trimmed)
    throw new Error('请输入 CloudDrive2 gRPC 服务地址。')
  try {
    const url = new URL(trimmed)
    if (url.protocol !== 'http:' && url.protocol !== 'https:')
      throw new Error('CloudDrive2 gRPC 服务地址仅支持 http 或 https。')
    if (url.username || url.password || url.search || url.hash || (url.pathname !== '/' && url.pathname !== ''))
      throw new Error('CloudDrive2 gRPC 服务地址不能包含路径、账号、密码、查询参数或片段。')
    return url.origin
  }
  catch (error) {
    if (error instanceof Error && error.message.includes('CloudDrive2'))
      throw error
    throw new Error('CloudDrive2 gRPC 服务地址格式无效。')
  }
}

function readCloudDrive2Extra(config: DataSourceConfig): CloudDrive2ConfigExtra {
  if (config.type !== 'clouddrive2')
    throw new Error('CloudDrive2 数据源类型无效。')
  const extra = config.extra ?? {}
  return {
    credentialRef: typeof extra.credentialRef === 'string' ? extra.credentialRef : undefined,
    rootPath: typeof extra.rootPath === 'string' ? normalizeCloudDrive2Path(extra.rootPath) : '/',
  }
}

function normalizeCloudDrive2Path(value: string | undefined): string {
  return normalizeProviderPath(value)
}

function sanitizeExportConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    throw new Error('CloudDrive2 数据源未配置。')
  const safeExtra = Object.fromEntries(Object.entries(config.extra ?? {}).filter(([key]) => !isSensitiveConfigKey(key)))
  return { ...config, url: normalizeCloudDrive2BaseUrl(config.url), extra: safeExtra }
}

function parseNativeFileEntries(value: unknown): CloudDrive2NativeFileEntry[] {
  if (!Array.isArray(value))
    throw new Error('CloudDrive2 返回了无效的文件列表。')
  return value.map(parseNativeFileEntry).filter((entry): entry is CloudDrive2NativeFileEntry => entry != null)
}

function parseNativeFileEntry(value: unknown): CloudDrive2NativeFileEntry | null {
  if (!isObject(value) || typeof value.name !== 'string' || typeof value.path !== 'string' || typeof value.isDir !== 'boolean')
    return null
  try {
    const path = normalizeCloudDrive2Path(value.path)
    const name = value.name.trim()
    if (!name)
      return null
    return {
      name,
      path,
      isDir: value.isDir,
      size: finiteNonNegativeNumber(value.size),
      modifiedMs: finiteNumber(value.modifiedMs),
    }
  }
  catch {
    return null
  }
}

function parseNativeStreamResponse(value: unknown): MediaStreamRequest {
  if (!isObject(value) || typeof value.url !== 'string')
    throw new Error('CloudDrive2 返回了无效的播放地址。')
  const url = new URL(value.url)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password)
    throw new Error('CloudDrive2 返回了无效的播放地址。')
  const headers = isObject(value.headers)
    ? Object.fromEntries(Object.entries(value.headers).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
    : {}
  return { url: url.toString(), headers }
}

function createFallbackEntry(path: string, isDir: boolean): CloudDrive2NativeFileEntry {
  return { name: providerBasename(path) ?? 'CloudDrive2', path, isDir }
}

function parentProviderPath(path: string): string {
  const normalized = normalizeCloudDrive2Path(path)
  if (normalized === '/')
    return '/'
  const index = normalized.lastIndexOf('/')
  return index <= 0 ? '/' : normalized.slice(0, index)
}

function modifiedIso(value: number | undefined): string | undefined {
  if (value == null)
    return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

function finiteNonNegativeNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined
}

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
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

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
