import type { Pan123CredentialValue } from './credentialStore'
import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption, MediaStreamRequest, PlaybackRequest } from './types'
import { invoke } from '@tauri-apps/api/core'
import { createRawSourceHomeSections, getRawScannedMediaDetail, isRawScannedSyntheticId, listRawScannedChildren, loadRawSourceScanCache } from '@/services/scraper'
import { getVideoFileExtension, isPathWithinRoot, isVideoFileName, normalizeProviderPath, providerBasename, providerParentPath } from '@/services/scraper/pathUtils'
import { SourceMetadataCache } from './cache'
import { createCredentialRef, readPan123Credential, readRawCredentialBackup, removeCredential, savePan123Credential, saveRawCredentialBackup } from './credentialStore'
import { redactSensitiveText } from './errors'
import { withSiblingSubtitles } from './siblingSubtitles'

export const PAN123_PROVIDER_URL = 'https://yun.123pan.com'

interface Pan123ConfigExtra {
  readonly credentialRef?: string
  readonly rootPath: string
}

interface Pan123NativeFileEntry {
  readonly fileId: string
  readonly name: string
  readonly path: string
  readonly isDir: boolean
  readonly size?: number
  readonly modifiedMs?: number
  readonly etag: string
  readonly s3KeyFlag: string
}

interface Pan123NativeListResponse {
  readonly entries: Pan123NativeFileEntry[]
  readonly updatedAccessToken?: string
}

interface Pan123NativeStreamResponse {
  readonly url: string
  readonly headers: Record<string, string>
  readonly updatedAccessToken?: string
}

interface Pan123AuthFields {
  readonly accessToken: string
  readonly username: string
  readonly password: string
}

interface Pan123PathRequest extends Pan123AuthFields {
  readonly path: string
  readonly rootPath: string
}

interface Pan123SearchRequest extends Pan123AuthFields {
  readonly keyword: string
  readonly rootPath: string
}

interface Pan123StreamRequest extends Pan123AuthFields {
  readonly rootPath: string
  readonly path: string
  readonly fileId: string
  readonly fileName: string
  readonly etag: string
  readonly s3KeyFlag: string
  readonly size: number
}

export interface Pan123Bridge {
  login: (request: { username: string, password: string }) => Promise<unknown>
  list: (request: Pan123PathRequest) => Promise<unknown>
  search: (request: Pan123SearchRequest) => Promise<unknown>
  getStream: (request: Pan123StreamRequest) => Promise<unknown>
}

export interface Pan123DataSourceOptions {
  readonly bridge?: Pan123Bridge
  readonly readCredential?: (ref: string) => Promise<Pan123CredentialValue | null>
  readonly saveCredential?: (ref: string, credential: Pan123CredentialValue) => Promise<void>
}

export interface Pan123ConfigInput {
  readonly id: string
  readonly displayName?: string
  readonly username?: string
  readonly password?: string
  readonly apiToken?: string
  readonly rootPath?: string
  readonly order?: number
}

export interface Pan123ConfigResult {
  readonly config: DataSourceConfig
  readonly libraries: MediaLibrary[]
}

const defaultPan123Bridge: Pan123Bridge = {
  login: request => invoke('pan123_login', { request }),
  list: request => invoke('pan123_list', { request }),
  search: request => invoke('pan123_search', { request }),
  getStream: request => invoke('pan123_get_stream', { request }),
}

export class Pan123DataSource implements DataSource {
  private config: DataSourceConfig | null = null
  private credentialRef = ''
  private credential: Pan123CredentialValue | null = null
  private rootPath = '/'
  private connected = false
  private readonly cache = new SourceMetadataCache()
  private readonly entriesByPath = new Map<string, Pan123NativeFileEntry>()
  private readonly bridge: Pan123Bridge
  private readonly readCredential: (ref: string) => Promise<Pan123CredentialValue | null>
  private readonly saveCredential: (ref: string, credential: Pan123CredentialValue) => Promise<void>

  readonly type = '123' as const

  constructor(options: Pan123DataSourceOptions = {}) {
    this.bridge = options.bridge ?? defaultPan123Bridge
    this.readCredential = options.readCredential ?? readPan123Credential
    this.saveCredential = options.saveCredential ?? savePan123Credential
  }

  get id(): string {
    return this.config?.id ?? ''
  }

  get name(): string {
    return this.config?.displayName ?? this.config?.name ?? '123 云盘'
  }

  get isConnected(): boolean {
    return this.connected
  }

  async init(config: DataSourceConfig): Promise<void> {
    this.config = sanitizeExportConfig(config)
    const extra = readPan123Extra(config)
    this.credentialRef = extra.credentialRef ?? ''
    this.rootPath = extra.rootPath
    this.credential = await this.readStoredCredential()
    this.connected = Boolean(this.credential)
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
    this.entriesByPath.clear()
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
      name: this.rootPath === '/' ? '123 云盘文件目录' : (providerBasename(this.rootPath) ?? this.rootPath),
      type: 'folders',
    }]
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const trimmed = keyword.trim()
    if (!trimmed)
      return []
    const credential = await this.ensureCredential()
    try {
      const response = parseNativeListResponse(await this.bridge.search({
        ...credentialRequestFields(credential),
        keyword: trimmed,
        rootPath: this.rootPath,
      }))
      await this.applyUpdatedAccessToken(response.updatedAccessToken)
      this.rememberEntries(response.entries)
      this.connected = true
      return response.entries
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
      return this.withSiblingSubtitles(rawDetail)
    if (isRawScannedSyntheticId(id))
      throw new Error('123 云盘本地扫描合集不能直接播放，请选择具体文件或分集。')

    const path = this.resolveLibraryPath(id)
    const entry = this.entriesByPath.get(path) ?? await this.cache.getOrSet(`detail:${path}`, async () => {
      const entries = await this.listProviderEntries(providerParentPath(path))
      return entries.find(item => item.path === path) ?? null
    })
    if (!entry)
      throw new Error('123 云盘文件不存在或已被移动。')
    this.ensureEntryInRoot(entry)
    const item = this.mapItem(entry)
    return this.withSiblingSubtitles({
      ...item,
      mediaSources: item.type === 'folder' || !isVideoFileName(item.name) ? [] : [this.mapMediaSource(entry)],
    })
  }

  async getStreamURL(id: string): Promise<string> {
    return (await this.getStreamRequest({ itemId: id })).url
  }

  async getStreamRequest(request: PlaybackRequest): Promise<MediaStreamRequest> {
    if (isRawScannedSyntheticId(request.itemId))
      throw new Error('123 云盘剧集合集不能直接播放，请选择具体分集。')
    const path = this.resolveLibraryPath(request.itemId)
    if (!isVideoFileName(providerBasename(path) ?? ''))
      throw new Error('该 123 云盘文件不是支持的视频格式。')
    return this.resolveStream(await this.resolveEntry(path))
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

  private async listProviderEntries(path: string): Promise<Pan123NativeFileEntry[]> {
    const credential = await this.ensureCredential()
    try {
      const response = parseNativeListResponse(await this.bridge.list({
        ...credentialRequestFields(credential),
        path: this.resolveLibraryPath(path),
        rootPath: this.rootPath,
      }))
      await this.applyUpdatedAccessToken(response.updatedAccessToken)
      this.rememberEntries(response.entries)
      this.connected = true
      return response.entries
    }
    catch (error) {
      this.connected = false
      throw new Error(redactSensitiveText(error))
    }
  }

  private async resolveEntry(path: string): Promise<Pan123NativeFileEntry> {
    const cached = this.entriesByPath.get(path)
    if (cached)
      return cached
    const entries = await this.listProviderEntries(providerParentPath(path))
    const entry = entries.find(item => item.path === path)
    if (!entry)
      throw new Error('123 云盘文件不存在或已被移动。')
    return entry
  }

  private async resolveStream(entry: Pan123NativeFileEntry): Promise<MediaStreamRequest> {
    const credential = await this.ensureCredential()
    try {
      const response = parseNativeStreamResponse(await this.bridge.getStream({
        ...credentialRequestFields(credential),
        rootPath: this.rootPath,
        path: entry.path,
        fileId: entry.fileId,
        fileName: entry.name,
        etag: entry.etag,
        s3KeyFlag: entry.s3KeyFlag,
        size: entry.size ?? 0,
      }))
      await this.applyUpdatedAccessToken(response.updatedAccessToken)
      this.connected = true
      return { url: response.url, headers: response.headers }
    }
    catch (error) {
      this.connected = false
      throw new Error(redactSensitiveText(error))
    }
  }

  private async withSiblingSubtitles(detail: MediaDetail): Promise<MediaDetail> {
    return withSiblingSubtitles(
      detail,
      parentPath => this.listProviderEntries(parentPath),
      async path => (await this.resolveStream(await this.resolveEntry(path))).url,
    )
  }

  private rememberEntries(entries: readonly Pan123NativeFileEntry[]): void {
    for (const entry of entries)
      this.entriesByPath.set(entry.path, entry)
  }

  private async applyUpdatedAccessToken(updatedAccessToken: string | undefined): Promise<void> {
    const accessToken = updatedAccessToken?.trim()
    if (!accessToken || accessToken === this.credential?.accessToken)
      return
    const credential: Pan123CredentialValue = {
      accessToken,
      username: this.credential?.username,
      password: this.credential?.password,
    }
    if (this.credentialRef)
      await this.saveCredential(this.credentialRef, credential)
    this.credential = credential
  }

  private async ensureCredential(): Promise<Pan123CredentialValue> {
    this.ensureConfigured({ requireCredential: false })
    if (this.credential)
      return this.credential
    const credential = await this.readStoredCredential()
    if (!credential)
      throw new Error('123 云盘登录凭据缺失，请在设置的数据源管理中重新登录。')
    this.credential = credential
    this.connected = true
    return credential
  }

  private async readStoredCredential(): Promise<Pan123CredentialValue | null> {
    return this.credentialRef ? this.readCredential(this.credentialRef) : null
  }

  private mapItem(entry: Pan123NativeFileEntry): MediaItem {
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

  private mapMediaSource(entry: Pan123NativeFileEntry): MediaSourceOption {
    return {
      id: entry.fileId,
      name: '123 云盘原始直链',
      container: getVideoFileExtension(entry.name) ?? undefined,
      size: entry.size,
      isRemote: true,
    }
  }

  private resolveLibraryPath(path?: string): string {
    const raw = path?.trim()
    if (!raw)
      return this.rootPath
    const normalized = normalizePan123Path(raw)
    if (normalized === '/' && this.rootPath !== '/')
      return this.rootPath
    if (!isPathWithinRoot(normalized, this.rootPath))
      throw new Error('123 云盘路径不在已选择的根目录内。')
    return normalized
  }

  private ensureEntryInRoot(entry: Pan123NativeFileEntry): void {
    if (!isPathWithinRoot(entry.path, this.rootPath))
      throw new Error('123 云盘返回的文件路径不在已选择的根目录内。')
  }

  private ensureConfigured(options: { requireCredential?: boolean } = {}): void {
    if (!this.config)
      throw new Error('123 云盘数据源未配置。')
    if (options.requireCredential !== false && !this.credential && !this.credentialRef)
      throw new Error('123 云盘登录凭据缺失，请在设置的数据源管理中重新登录。')
  }

  private async loadRawScanCache() {
    return loadRawSourceScanCache(this.id, '123', this.rootPath)
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

export async function createAuthenticatedPan123SetupSource(input: Pan123ConfigInput): Promise<Pan123DataSource> {
  const credential = await authenticatePan123(input, defaultPan123Bridge)
  const displayName = input.displayName?.trim() || '123 云盘'
  const credentialRef = createCredentialRef(input.id, '123')
  const source = new Pan123DataSource({
    readCredential: async () => credential,
    saveCredential: async () => undefined,
  })
  try {
    await source.init(createPan123Config(input.id, displayName, credentialRef, '/', input.order ?? 0))
    await source.test()
    return source
  }
  catch (error) {
    source.destroy()
    throw error
  }
}

export async function loginPan123AndCreateConfig(input: Pan123ConfigInput): Promise<Pan123ConfigResult> {
  const credential = await authenticatePan123(input, defaultPan123Bridge)
  const credentialRef = createCredentialRef(input.id, '123')
  const displayName = input.displayName?.trim() || '123 云盘'
  const rootPath = normalizePan123Path(input.rootPath)
  const config = createPan123Config(input.id, displayName, credentialRef, rootPath, input.order ?? 0)
  const previousCredential = await readRawCredentialBackup(credentialRef)
  try {
    await savePan123Credential(credentialRef, credential)
  }
  catch (error) {
    throw new Error(redactSensitiveText(error))
  }

  const source = new Pan123DataSource()
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

export function readPan123RootPath(config: DataSourceConfig | null | undefined): string {
  if (!config || config.type !== '123')
    return '/'
  try {
    return readPan123Extra(config).rootPath
  }
  catch {
    return '/'
  }
}

export function normalizePan123RootPath(value: string | undefined): string {
  return normalizePan123Path(value)
}

async function authenticatePan123(input: Pan123ConfigInput, bridge: Pan123Bridge): Promise<Pan123CredentialValue> {
  const apiToken = input.apiToken?.trim()
  if (apiToken)
    return { accessToken: apiToken }
  const username = input.username?.trim() ?? ''
  const password = input.password ?? ''
  if (!username || !password)
    throw new Error('请输入 123 云盘手机号或邮箱与密码，或者导入访问令牌。')
  const response = await bridge.login({ username, password })
  if (!isObject(response) || typeof response.accessToken !== 'string' || !response.accessToken.trim())
    throw new Error('123 云盘登录没有返回有效访问令牌。')
  return { accessToken: response.accessToken.trim(), username, password }
}

function createPan123Config(id: string, displayName: string, credentialRef: string, rootPath: string, order: number): DataSourceConfig {
  return {
    id,
    type: '123',
    name: displayName,
    displayName,
    order,
    url: PAN123_PROVIDER_URL,
    enabled: true,
    extra: {
      credentialRef,
      credentialVersion: Date.now(),
      rootPath,
    },
  }
}

function readPan123Extra(config: DataSourceConfig): Pan123ConfigExtra {
  if (config.type !== '123')
    throw new Error('123 云盘数据源类型无效。')
  const extra = config.extra ?? {}
  return {
    credentialRef: typeof extra.credentialRef === 'string' ? extra.credentialRef : undefined,
    rootPath: typeof extra.rootPath === 'string' ? normalizePan123Path(extra.rootPath) : '/',
  }
}

function normalizePan123Path(value: string | undefined): string {
  return normalizeProviderPath(value)
}

function credentialRequestFields(credential: Pan123CredentialValue): Pan123AuthFields {
  return {
    accessToken: credential.accessToken,
    username: credential.username ?? '',
    password: credential.password ?? '',
  }
}

function sanitizeExportConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    throw new Error('123 云盘数据源未配置。')
  const safeExtra = Object.fromEntries(Object.entries(config.extra ?? {}).filter(([key]) => !isSensitiveConfigKey(key)))
  return { ...config, url: PAN123_PROVIDER_URL, extra: safeExtra }
}

function parseNativeListResponse(value: unknown): Pan123NativeListResponse {
  if (!isObject(value) || !Array.isArray(value.entries))
    throw new Error('123 云盘返回了无效的文件列表。')
  return {
    entries: value.entries.map(parseNativeFileEntry).filter((entry): entry is Pan123NativeFileEntry => entry != null),
    updatedAccessToken: optionalString(value.updatedAccessToken),
  }
}

function parseNativeFileEntry(value: unknown): Pan123NativeFileEntry | null {
  if (!isObject(value) || typeof value.fileId !== 'string' || typeof value.name !== 'string' || typeof value.path !== 'string' || typeof value.isDir !== 'boolean')
    return null
  try {
    const fileId = value.fileId.trim()
    const name = value.name.trim()
    if (!fileId || !name)
      return null
    return {
      fileId,
      name,
      path: normalizePan123Path(value.path),
      isDir: value.isDir,
      size: finiteNonNegativeNumber(value.size),
      modifiedMs: finiteNumber(value.modifiedMs),
      etag: typeof value.etag === 'string' ? value.etag : '',
      s3KeyFlag: typeof value.s3KeyFlag === 'string' ? value.s3KeyFlag : '',
    }
  }
  catch {
    return null
  }
}

function parseNativeStreamResponse(value: unknown): Pan123NativeStreamResponse {
  if (!isObject(value) || typeof value.url !== 'string')
    throw new Error('123 云盘返回了无效的播放地址。')
  const url = new URL(value.url)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password)
    throw new Error('123 云盘返回了无效的播放地址。')
  const headers = isObject(value.headers)
    ? Object.fromEntries(Object.entries(value.headers).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
    : {}
  return {
    url: url.toString(),
    headers,
    updatedAccessToken: optionalString(value.updatedAccessToken),
  }
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

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function isSensitiveConfigKey(key: string): boolean {
  const normalized = key.toLowerCase()
  return normalized.includes('token')
    || normalized.includes('password')
    || normalized.includes('username')
    || normalized.includes('authorization')
    || normalized.includes('cookie')
    || normalized.includes('passkey')
    || ['apikey', 'api_key', 'access_token', 'passwd', 'pwd'].includes(normalized)
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
