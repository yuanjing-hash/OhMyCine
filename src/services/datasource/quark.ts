import type { QuarkCredentialValue } from './credentialStore'
import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption, MediaStreamRequest, PlaybackRequest } from './types'
import { invoke } from '@tauri-apps/api/core'
import { createRawSourceHomeSections, getRawScannedMediaDetail, isRawScannedSyntheticId, listRawScannedChildren, loadRawSourceScanCache } from '@/services/scraper'
import { getVideoFileExtension, isPathWithinRoot, isVideoFileName, normalizeProviderPath, providerBasename, providerParentPath } from '@/services/scraper/pathUtils'
import { SourceMetadataCache } from './cache'
import { createCredentialRef, readQuarkCredential, readRawCredentialBackup, removeCredential, saveQuarkCredential, saveRawCredentialBackup } from './credentialStore'
import { redactSensitiveText } from './errors'
import { withSiblingSubtitles } from './siblingSubtitles'

export const QUARK_PROVIDER_URL = 'https://pan.quark.cn'

interface QuarkConfigExtra {
  readonly credentialRef?: string
  readonly rootPath: string
}

interface QuarkNativeFileEntry {
  readonly fid: string
  readonly name: string
  readonly path: string
  readonly isDir: boolean
  readonly size?: number
  readonly modifiedMs?: number
  readonly thumbnail?: string
}

interface QuarkNativeListResponse {
  readonly entries: QuarkNativeFileEntry[]
  readonly updatedCookie?: string
}

interface QuarkNativeStreamResponse {
  readonly url: string
  readonly headers: Record<string, string>
  readonly updatedCookie?: string
}

interface QuarkPathRequest {
  readonly cookie: string
  readonly path: string
}

interface QuarkSearchRequest {
  readonly cookie: string
  readonly keyword: string
  readonly rootPath: string
}

interface QuarkStreamRequest {
  readonly cookie: string
  readonly fid: string
}

export interface QuarkBridge {
  list: (request: QuarkPathRequest) => Promise<unknown>
  search: (request: QuarkSearchRequest) => Promise<unknown>
  getStream: (request: QuarkStreamRequest) => Promise<unknown>
}

export interface QuarkDataSourceOptions {
  readonly bridge?: QuarkBridge
  readonly readCredential?: (ref: string) => Promise<QuarkCredentialValue | null>
  readonly saveCredential?: (ref: string, credential: QuarkCredentialValue) => Promise<void>
}

export interface QuarkCookieConfigInput {
  readonly id: string
  readonly displayName?: string
  readonly cookie: string
  readonly rootPath?: string
  readonly order?: number
}

export interface QuarkCookieConfigResult {
  readonly config: DataSourceConfig
  readonly libraries: MediaLibrary[]
}

export interface QuarkSetupSessionInput {
  readonly id: string
  readonly displayName?: string
  readonly cookie: string
  readonly order?: number
}

export type QuarkAuthPollStatus = 'pending' | 'success' | 'expired' | 'cancelled'

export interface QuarkQrLoginSession {
  readonly sessionId: string
  readonly qrImageUrl: string
  readonly expiresAtMs: number
}

export interface QuarkAccountLoginSession {
  readonly sessionId: string
}

export interface QuarkAuthPollResult {
  readonly status: QuarkAuthPollStatus
  readonly cookie?: string
}

const defaultQuarkBridge: QuarkBridge = {
  list: request => invoke('quark_list', { request }),
  search: request => invoke('quark_search', { request }),
  getStream: request => invoke('quark_get_stream', { request }),
}

export async function startQuarkQrLogin(): Promise<QuarkQrLoginSession> {
  const value = await invoke<unknown>('quark_auth_start_qr')
  if (!isObject(value) || typeof value.sessionId !== 'string' || typeof value.qrImageUrl !== 'string' || typeof value.expiresAtMs !== 'number')
    throw new Error('夸克扫码登录初始化失败。')
  return {
    sessionId: value.sessionId,
    qrImageUrl: value.qrImageUrl,
    expiresAtMs: value.expiresAtMs,
  }
}

export async function pollQuarkQrLogin(sessionId: string): Promise<QuarkAuthPollResult> {
  return parseAuthPollResult(await invoke<unknown>('quark_auth_poll_qr', { sessionId }))
}

export async function startQuarkAccountLogin(): Promise<QuarkAccountLoginSession> {
  const value = await invoke<unknown>('quark_auth_start_account')
  if (!isObject(value) || typeof value.sessionId !== 'string')
    throw new Error('夸克账号登录页面打开失败。')
  return { sessionId: value.sessionId }
}

export async function pollQuarkAccountLogin(sessionId: string): Promise<QuarkAuthPollResult> {
  return parseAuthPollResult(await invoke<unknown>('quark_auth_poll_account', { sessionId }))
}

export async function cancelQuarkLogin(sessionId: string): Promise<void> {
  if (!sessionId.trim())
    return
  await invoke('quark_auth_cancel', { sessionId })
}

export class QuarkDataSource implements DataSource {
  private config: DataSourceConfig | null = null
  private credentialRef = ''
  private credential: QuarkCredentialValue | null = null
  private rootPath = '/'
  private connected = false
  private readonly cache = new SourceMetadataCache()
  private readonly entriesByPath = new Map<string, QuarkNativeFileEntry>()
  private readonly bridge: QuarkBridge
  private readonly readCredential: (ref: string) => Promise<QuarkCredentialValue | null>
  private readonly saveCredential: (ref: string, credential: QuarkCredentialValue) => Promise<void>

  readonly type = 'quark' as const

  constructor(options: QuarkDataSourceOptions = {}) {
    this.bridge = options.bridge ?? defaultQuarkBridge
    this.readCredential = options.readCredential ?? readQuarkCredential
    this.saveCredential = options.saveCredential ?? saveQuarkCredential
  }

  get id(): string {
    return this.config?.id ?? ''
  }

  get name(): string {
    return this.config?.displayName ?? this.config?.name ?? '夸克网盘'
  }

  get isConnected(): boolean {
    return this.connected
  }

  async init(config: DataSourceConfig): Promise<void> {
    this.config = sanitizeExportConfig(config)
    const extra = readQuarkExtra(config)
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
      name: this.rootPath === '/' ? '夸克网盘文件目录' : (providerBasename(this.rootPath) ?? this.rootPath),
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
        cookie: credential.cookie,
        keyword: trimmed,
        rootPath: this.rootPath,
      }))
      await this.applyRotatedCookie(response.updatedCookie)
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
      throw new Error('夸克网盘本地扫描合集不能直接播放，请选择具体文件或分集。')

    const path = this.resolveLibraryPath(id)
    const entry = this.entriesByPath.get(path) ?? await this.cache.getOrSet(`detail:${path}`, async () => {
      const entries = await this.listProviderEntries(providerParentPath(path))
      return entries.find(item => item.path === path) ?? null
    })
    if (!entry)
      throw new Error('夸克网盘文件不存在或已被移动。')
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
      throw new Error('夸克网盘剧集合集不能直接播放，请选择具体分集。')
    const path = this.resolveLibraryPath(request.itemId)
    if (!isVideoFileName(providerBasename(path) ?? ''))
      throw new Error('该夸克网盘文件不是支持的视频格式。')
    const entry = await this.resolveEntry(path)
    return this.resolveStream(entry.fid)
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

  private async listProviderEntries(path: string): Promise<QuarkNativeFileEntry[]> {
    const credential = await this.ensureCredential()
    try {
      const response = parseNativeListResponse(await this.bridge.list({
        cookie: credential.cookie,
        path: this.resolveLibraryPath(path),
      }))
      await this.applyRotatedCookie(response.updatedCookie)
      this.rememberEntries(response.entries)
      this.connected = true
      return response.entries
    }
    catch (error) {
      this.connected = false
      throw new Error(redactSensitiveText(error))
    }
  }

  private async resolveEntry(path: string): Promise<QuarkNativeFileEntry> {
    const cached = this.entriesByPath.get(path)
    if (cached)
      return cached
    const entries = await this.listProviderEntries(providerParentPath(path))
    const entry = entries.find(item => item.path === path)
    if (!entry)
      throw new Error('夸克网盘文件不存在或已被移动。')
    return entry
  }

  private async resolveStream(fid: string): Promise<MediaStreamRequest> {
    const credential = await this.ensureCredential()
    try {
      const response = parseNativeStreamResponse(await this.bridge.getStream({ cookie: credential.cookie, fid }))
      await this.applyRotatedCookie(response.updatedCookie)
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
      async path => (await this.resolveStream((await this.resolveEntry(path)).fid)).url,
    )
  }

  private rememberEntries(entries: readonly QuarkNativeFileEntry[]): void {
    for (const entry of entries)
      this.entriesByPath.set(entry.path, entry)
  }

  private async applyRotatedCookie(updatedCookie: string | undefined): Promise<void> {
    const cookie = updatedCookie?.trim()
    if (!cookie || cookie === this.credential?.cookie)
      return
    const credential = { cookie }
    if (this.credentialRef)
      await this.saveCredential(this.credentialRef, credential)
    this.credential = credential
  }

  private async ensureCredential(): Promise<QuarkCredentialValue> {
    this.ensureConfigured({ requireCredential: false })
    if (this.credential)
      return this.credential
    const credential = await this.readStoredCredential()
    if (!credential)
      throw new Error('夸克网盘 Cookie 缺失，请在设置的数据源管理中重新编辑。')
    this.credential = credential
    this.connected = true
    return credential
  }

  private async readStoredCredential(): Promise<QuarkCredentialValue | null> {
    return this.credentialRef ? this.readCredential(this.credentialRef) : null
  }

  private mapItem(entry: QuarkNativeFileEntry): MediaItem {
    return {
      id: entry.path,
      sourceId: this.id,
      libraryId: this.rootPath,
      name: entry.name,
      type: entry.isDir ? 'folder' : 'file',
      posterUrl: entry.thumbnail,
      size: entry.isDir ? undefined : entry.size,
      modified: modifiedIso(entry.modifiedMs),
      path: entry.path,
    }
  }

  private mapMediaSource(entry: QuarkNativeFileEntry): MediaSourceOption {
    return {
      id: entry.fid,
      name: '夸克网盘原始直链',
      container: getVideoFileExtension(entry.name) ?? undefined,
      size: entry.size,
      isRemote: true,
    }
  }

  private resolveLibraryPath(path?: string): string {
    const raw = path?.trim()
    if (!raw)
      return this.rootPath
    const normalized = normalizeQuarkPath(raw)
    if (normalized === '/' && this.rootPath !== '/')
      return this.rootPath
    if (!isPathWithinRoot(normalized, this.rootPath))
      throw new Error('夸克网盘路径不在已选择的根目录内。')
    return normalized
  }

  private ensureEntryInRoot(entry: QuarkNativeFileEntry): void {
    if (!isPathWithinRoot(entry.path, this.rootPath))
      throw new Error('夸克网盘返回的文件路径不在已选择的根目录内。')
  }

  private ensureConfigured(options: { requireCredential?: boolean } = {}): void {
    if (!this.config)
      throw new Error('夸克网盘数据源未配置。')
    if (options.requireCredential !== false && !this.credential && !this.credentialRef)
      throw new Error('夸克网盘 Cookie 缺失，请在设置的数据源管理中重新编辑。')
  }

  private async loadRawScanCache() {
    return loadRawSourceScanCache(this.id, 'quark', this.rootPath)
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

export async function createAuthenticatedQuarkSetupSource(input: QuarkSetupSessionInput): Promise<QuarkDataSource> {
  const credential = normalizeCookie(input.cookie)
  const displayName = input.displayName?.trim() || '夸克网盘'
  const credentialRef = createCredentialRef(input.id, 'quark')
  const source = new QuarkDataSource({
    readCredential: async () => credential,
    saveCredential: async () => undefined,
  })
  try {
    await source.init(createQuarkConfig(input.id, displayName, credentialRef, '/', input.order ?? 0))
    await source.test()
    return source
  }
  catch (error) {
    source.destroy()
    throw error
  }
}

export async function saveQuarkCookieAndCreateConfig(input: QuarkCookieConfigInput): Promise<QuarkCookieConfigResult> {
  const credential = normalizeCookie(input.cookie)
  const credentialRef = createCredentialRef(input.id, 'quark')
  const displayName = input.displayName?.trim() || '夸克网盘'
  const rootPath = normalizeQuarkPath(input.rootPath)
  const config = createQuarkConfig(input.id, displayName, credentialRef, rootPath, input.order ?? 0)
  const previousCredential = await readRawCredentialBackup(credentialRef)
  try {
    await saveQuarkCredential(credentialRef, credential)
  }
  catch (error) {
    throw new Error(redactSensitiveText(error))
  }

  const source = new QuarkDataSource()
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

export function readQuarkRootPath(config: DataSourceConfig | null | undefined): string {
  if (!config || config.type !== 'quark')
    return '/'
  try {
    return readQuarkExtra(config).rootPath
  }
  catch {
    return '/'
  }
}

export function normalizeQuarkRootPath(value: string | undefined): string {
  return normalizeQuarkPath(value)
}

function createQuarkConfig(id: string, displayName: string, credentialRef: string, rootPath: string, order: number): DataSourceConfig {
  return {
    id,
    type: 'quark',
    name: displayName,
    displayName,
    order,
    url: QUARK_PROVIDER_URL,
    enabled: true,
    extra: {
      credentialRef,
      credentialVersion: Date.now(),
      rootPath,
    },
  }
}

function normalizeCookie(value: string): QuarkCredentialValue {
  const cookie = value.trim()
  if (!cookie)
    throw new Error('请输入夸克网盘 Cookie。')
  return { cookie }
}

function readQuarkExtra(config: DataSourceConfig): QuarkConfigExtra {
  if (config.type !== 'quark')
    throw new Error('夸克网盘数据源类型无效。')
  const extra = config.extra ?? {}
  return {
    credentialRef: typeof extra.credentialRef === 'string' ? extra.credentialRef : undefined,
    rootPath: typeof extra.rootPath === 'string' ? normalizeQuarkPath(extra.rootPath) : '/',
  }
}

function normalizeQuarkPath(value: string | undefined): string {
  return normalizeProviderPath(value)
}

function sanitizeExportConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    throw new Error('夸克网盘数据源未配置。')
  const safeExtra = Object.fromEntries(Object.entries(config.extra ?? {}).filter(([key]) => !isSensitiveConfigKey(key)))
  return { ...config, url: QUARK_PROVIDER_URL, extra: safeExtra }
}

function parseNativeListResponse(value: unknown): QuarkNativeListResponse {
  if (!isObject(value) || !Array.isArray(value.entries))
    throw new Error('夸克网盘返回了无效的文件列表。')
  return {
    entries: value.entries.map(parseNativeFileEntry).filter((entry): entry is QuarkNativeFileEntry => entry != null),
    updatedCookie: typeof value.updatedCookie === 'string' && value.updatedCookie.trim() ? value.updatedCookie.trim() : undefined,
  }
}

function parseNativeFileEntry(value: unknown): QuarkNativeFileEntry | null {
  if (!isObject(value) || typeof value.fid !== 'string' || typeof value.name !== 'string' || typeof value.path !== 'string' || typeof value.isDir !== 'boolean')
    return null
  try {
    const fid = value.fid.trim()
    const name = value.name.trim()
    if (!fid || !name)
      return null
    const thumbnail = typeof value.thumbnail === 'string' ? safeHttpsUrl(value.thumbnail) : undefined
    return {
      fid,
      name,
      path: normalizeQuarkPath(value.path),
      isDir: value.isDir,
      size: finiteNonNegativeNumber(value.size),
      modifiedMs: finiteNumber(value.modifiedMs),
      thumbnail,
    }
  }
  catch {
    return null
  }
}

function parseNativeStreamResponse(value: unknown): QuarkNativeStreamResponse {
  if (!isObject(value) || typeof value.url !== 'string')
    throw new Error('夸克网盘返回了无效的播放地址。')
  const url = new URL(value.url)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password)
    throw new Error('夸克网盘返回了无效的播放地址。')
  const headers = isObject(value.headers)
    ? Object.fromEntries(Object.entries(value.headers).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
    : {}
  return {
    url: url.toString(),
    headers,
    updatedCookie: typeof value.updatedCookie === 'string' && value.updatedCookie.trim() ? value.updatedCookie.trim() : undefined,
  }
}

function parseAuthPollResult(value: unknown): QuarkAuthPollResult {
  if (!isObject(value) || !isQuarkAuthPollStatus(value.status))
    throw new Error('夸克登录状态无效。')
  return {
    status: value.status,
    cookie: typeof value.cookie === 'string' && value.cookie.trim() ? value.cookie.trim() : undefined,
  }
}

function isQuarkAuthPollStatus(value: unknown): value is QuarkAuthPollStatus {
  return value === 'pending' || value === 'success' || value === 'expired' || value === 'cancelled'
}

function safeHttpsUrl(value: string): string | undefined {
  try {
    const url = new URL(value)
    return url.protocol === 'https:' ? url.toString() : undefined
  }
  catch {
    return undefined
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
