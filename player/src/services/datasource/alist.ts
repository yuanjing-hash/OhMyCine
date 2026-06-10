import type { AlistCredentialValue } from './credentialStore'
import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption } from './types'
import { ofetch } from 'ofetch'
import { createRawSourceHomeSections, getRawScannedMediaDetail, isRawScannedSyntheticId, listRawScannedChildren, loadRawSourceScanCache } from '@/services/scraper'
import { SourceMetadataCache } from './cache'
import { createCredentialRef, readAlistCredential, readRawCredentialBackup, removeCredential, saveAlistCredential, saveRawCredentialBackup } from './credentialStore'
import { redactSensitiveText } from './errors'

const ALIST_REQUEST_TIMEOUT_MS = 15_000

type AlistRequestPayload = Record<string, string | number | boolean | null | undefined>
type AlistCredentialReader = (ref: string) => Promise<AlistCredentialValue | null>
type AlistCredentialSaver = (ref: string, value: AlistCredentialValue) => Promise<void>

export type AlistFetch = <T = unknown>(
  request: Parameters<typeof ofetch>[0],
  options?: Parameters<typeof ofetch>[1],
) => Promise<T>

interface AlistConfigExtra {
  readonly credentialRef?: string
  readonly rootPath: string
}

interface AlistAuthResult {
  readonly token: string
}

export interface AlistDataSourceOptions {
  readonly fetcher?: AlistFetch
  readonly readCredential?: AlistCredentialReader
  readonly saveCredential?: AlistCredentialSaver
}

interface AlistApiEnvelope {
  readonly code?: number
  readonly message?: string
  readonly data?: unknown
}

interface AlistListResponse {
  readonly content?: unknown[]
  readonly total?: number
}

interface AlistFileRecord {
  readonly name: string
  readonly path: string
  readonly parent?: string
  readonly isDir: boolean
  readonly size?: number
  readonly modified?: string
  readonly created?: string
  readonly sign?: string
}

class AlistProviderApiError extends Error {
  constructor(message: string, readonly code?: number) {
    super(message)
    this.name = 'AlistProviderApiError'
  }
}

export interface AlistLoginConfigInput {
  readonly id: string
  readonly url: string
  readonly displayName?: string
  readonly username: string
  readonly password: string
  readonly rootPath?: string
  readonly order?: number
}

export interface AlistLoginConfigResult {
  readonly config: DataSourceConfig
  readonly libraries: MediaLibrary[]
}

export interface AlistSetupSessionInput {
  readonly id: string
  readonly url: string
  readonly displayName?: string
  readonly username: string
  readonly password: string
  readonly order?: number
}

export class AlistDataSource implements DataSource {
  private config: DataSourceConfig | null = null
  private baseUrl = ''
  private token = ''
  private credentialRef = ''
  private rootPath = '/'
  private connected = false
  private authRefreshPromise: Promise<void> | null = null
  private readonly cache = new SourceMetadataCache()
  private readonly fetcher: AlistFetch
  private readonly readCredential: AlistCredentialReader
  private readonly saveCredential: AlistCredentialSaver

  readonly type = 'alist' as const

  constructor(options: AlistDataSourceOptions = {}) {
    this.fetcher = options.fetcher ?? (ofetch as AlistFetch)
    this.readCredential = options.readCredential ?? readAlistCredential
    this.saveCredential = options.saveCredential ?? saveAlistCredential
  }

  get id(): string {
    return this.config?.id ?? ''
  }

  get name(): string {
    return this.config?.displayName ?? this.config?.name ?? 'OpenList/Alist'
  }

  get isConnected(): boolean {
    return this.connected
  }

  async init(config: DataSourceConfig): Promise<void> {
    this.config = sanitizeExportConfig(config)
    this.baseUrl = normalizeBaseUrl(config.url)
    const extra = readAlistExtra(config)
    this.credentialRef = extra.credentialRef ?? ''
    this.rootPath = extra.rootPath
    this.token = await this.resolveStoredToken()
    this.connected = Boolean(this.baseUrl && this.token)
  }

  async test(): Promise<boolean> {
    this.ensureConfigured({ requireToken: false })
    await this.requestFsList(this.rootPath, 1)
    this.connected = true
    return true
  }

  async authenticate(username: string, password: string): Promise<AlistAuthResult> {
    if (!this.config || !this.baseUrl)
      throw new Error('OpenList/Alist source is not configured.')

    const trimmedUsername = username.trim()
    if (!trimmedUsername || !password)
      throw new Error('请输入 OpenList/Alist 账号和密码。')

    try {
      const response = await this.fetcher<unknown>(`${this.baseUrl}/api/auth/login`, {
        method: 'POST',
        timeout: ALIST_REQUEST_TIMEOUT_MS,
        body: {
          username: trimmedUsername,
          password,
        },
      })
      const auth = parseAuthResponse(response)
      this.token = auth.token
      this.connected = true
      return auth
    }
    catch (error) {
      throw new Error(redactSensitiveText(error))
    }
  }

  destroy(): void {
    this.connected = false
  }

  clearCache(): void {
    this.cache.clear()
  }

  async list(path?: string): Promise<MediaItem[]> {
    const rawChildren = this.listRawScannedChildren(path)
    if (rawChildren)
      return rawChildren
    if (path && isRawScannedSyntheticId(path))
      return []

    const normalizedPath = this.resolveLibraryPath(path)
    const records = await this.cache.getOrSet(`list:${normalizedPath}`, () => this.requestFsList(normalizedPath))
    return this.filterRecordsInRoot(records).map(record => this.mapItem(record))
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    this.ensureConfigured({ requireToken: false })
    return [
      {
        id: this.rootPath,
        sourceId: this.id,
        name: this.rootPath === '/' ? '文件目录' : (basename(this.rootPath) ?? this.rootPath),
        type: 'folders',
      },
    ]
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const trimmed = keyword.trim()
    if (!trimmed)
      return []

    try {
      const records = await this.requestFsSearch(trimmed)
      return this.filterRecordsInRoot(records).map(record => this.mapItem(record))
    }
    catch {
      const records = await this.searchByListing(this.rootPath, trimmed)
      return this.filterRecordsInRoot(records).map(record => this.mapItem(record))
    }
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const rawDetail = this.getRawScannedDetail(id)
    if (rawDetail)
      return rawDetail
    if (isRawScannedSyntheticId(id))
      throw new Error('OpenList/Alist 本地扫描详情不可用，请重新扫描或从文件夹视图打开具体文件。')

    const path = this.resolveLibraryPath(id)
    const record = await this.cache.getOrSet(`detail:${path}`, () => this.requestFsGet(path, false))
    this.ensureRecordInRoot(record)
    const item = this.mapItem(record)
    return {
      ...item,
      mediaSources: item.type === 'folder' ? [] : [this.mapMediaSource(record)],
    }
  }

  async getStreamURL(id: string): Promise<string> {
    if (isRawScannedSyntheticId(id))
      throw new Error('OpenList/Alist 剧集合集不能直接播放，请选择具体分集。')

    const path = this.resolveLibraryPath(id)
    const record = await this.requestFsGet(path, true)
    this.ensureRecordInRoot(record)
    if (record.isDir)
      throw new Error('OpenList/Alist 文件夹不能直接播放。')

    return this.buildDownloadUrl(path, record.sign)
  }

  async getHomeSections(): Promise<HomeSection[]> {
    try {
      const cache = this.loadRawScanCache()
      return cache ? createRawSourceHomeSections(cache, this.name) : []
    }
    catch {
      return []
    }
  }

  exportConfig(): DataSourceConfig {
    this.ensureConfigured({ requireToken: false })
    return sanitizeExportConfig(this.config)
  }

  private async requestFsList(path: string, perPage = 200): Promise<AlistFileRecord[]> {
    const data = await this.request('/api/fs/list', {
      path,
      page: 1,
      per_page: perPage,
      refresh: false,
    })
    return parseListRecords(data, path)
  }

  private async requestFsGet(path: string, includeSign: boolean): Promise<AlistFileRecord> {
    const data = await this.request('/api/fs/get', { path })
    const record = parseFileRecord(data, parentPath(path), includeSign)
    if (record.path === '/')
      return { ...record, path }
    return record
  }

  private async requestFsSearch(keyword: string): Promise<AlistFileRecord[]> {
    const data = await this.request('/api/fs/search', {
      parent: this.rootPath,
      keywords: keyword,
      scope: 0,
      page: 1,
      per_page: 100,
    })
    return parseListRecords(data, this.rootPath)
  }

  private async request(path: string, body: AlistRequestPayload): Promise<unknown> {
    await this.ensureAuthenticated()

    try {
      return await this.performRequest(path, body)
    }
    catch (error) {
      if (isAlistAuthenticationFailure(error)) {
        try {
          await this.refreshAuthentication()
          return await this.performRequest(path, body)
        }
        catch (retryError) {
          this.connected = false
          throw new Error(redactSensitiveText(retryError))
        }
      }

      this.connected = false
      throw new Error(redactSensitiveText(error))
    }
  }

  private async performRequest(path: string, body: AlistRequestPayload): Promise<unknown> {
    const response = await this.fetcher<unknown>(`${this.baseUrl}${path}`, {
      method: 'POST',
      timeout: ALIST_REQUEST_TIMEOUT_MS,
      body,
      headers: this.authHeaders(),
    })
    const data = unwrapAlistData(response)
    this.connected = true
    return data
  }

  private authHeaders(): Record<string, string> {
    return {
      Authorization: this.token,
    }
  }

  private async ensureAuthenticated(): Promise<void> {
    this.ensureConfigured({ requireToken: false })
    if (this.token)
      return

    const credential = await this.readStoredCredential()
    if (credential?.token) {
      this.token = credential.token
      this.connected = true
      return
    }

    await this.refreshAuthentication()
  }

  private async refreshAuthentication(): Promise<void> {
    if (this.authRefreshPromise)
      return this.authRefreshPromise

    this.authRefreshPromise = this.refreshAuthenticationOnce()
      .finally(() => {
        this.authRefreshPromise = null
      })
    return this.authRefreshPromise
  }

  private async refreshAuthenticationOnce(): Promise<void> {
    const credential = await this.readStoredCredential()
    if (!credential?.username || !credential.password)
      throw new Error('OpenList/Alist 登录凭证缺失。请在设置的数据源管理中重新编辑并登录。')

    const auth = await this.authenticate(credential.username, credential.password)
    await this.saveCredential(this.credentialRef, {
      token: auth.token,
      username: credential.username,
      password: credential.password,
    })
    this.cache.clear()
  }

  private async resolveStoredToken(): Promise<string> {
    const credential = await this.readStoredCredential()
    return credential?.token ?? ''
  }

  private async readStoredCredential(): Promise<AlistCredentialValue | null> {
    if (!this.credentialRef)
      return null
    return this.readCredential(this.credentialRef)
  }

  private async searchByListing(rootPath: string, keyword: string, maxDepth = 2, maxVisited = 80): Promise<AlistFileRecord[]> {
    const searchRoot = normalizeAlistPath(rootPath)
    const normalizedKeyword = keyword.toLocaleLowerCase()
    const results: AlistFileRecord[] = []
    const queue: Array<{ path: string, depth: number }> = [{ path: searchRoot, depth: 0 }]
    let visited = 0

    while (queue.length > 0 && visited < maxVisited) {
      const current = queue.shift()
      if (!current)
        break
      visited += 1

      const children = (await this.requestFsList(current.path, 100))
        .filter(child => isPathWithinRoot(child.path, searchRoot))
      for (const child of children) {
        if (child.name.toLocaleLowerCase().includes(normalizedKeyword))
          results.push(child)
        if (child.isDir && current.depth < maxDepth)
          queue.push({ path: child.path, depth: current.depth + 1 })
      }
    }

    return results.slice(0, 100)
  }

  private mapItem(record: AlistFileRecord): MediaItem {
    const modified = record.modified ?? record.created
    return {
      id: record.path,
      sourceId: this.id,
      libraryId: this.rootPath,
      name: record.name,
      type: record.isDir ? 'folder' : 'file',
      size: record.isDir ? undefined : record.size,
      modified,
      path: record.path,
    }
  }

  private mapMediaSource(record: AlistFileRecord): MediaSourceOption {
    return {
      id: 'default',
      name: '默认版本',
      container: fileExtension(record.name),
      size: record.size,
      isRemote: true,
    }
  }

  private buildDownloadUrl(path: string, sign: string | undefined): string {
    if (!isPathWithinRoot(path, this.rootPath))
      throw new Error('OpenList/Alist 路径不在已选择的根目录内。')

    const url = new URL(`${this.baseUrl}/d${encodeAlistPath(path)}`)
    if (sign)
      url.searchParams.set('sign', sign)
    return url.toString()
  }

  private ensureConfigured(options: { requireToken?: boolean } = {}): void {
    if (!this.config || !this.baseUrl)
      throw new Error('OpenList/Alist source is not configured.')
    if (options.requireToken !== false && !this.token)
      throw new Error('OpenList/Alist 登录凭证缺失。请在设置的数据源管理中重新编辑并登录。')
  }

  private resolveLibraryPath(path?: string): string {
    const raw = path?.trim()
    if (!raw)
      return this.rootPath

    const normalized = normalizeAlistPath(raw)
    if (normalized === '/' && this.rootPath !== '/')
      return this.rootPath
    if (!isPathWithinRoot(normalized, this.rootPath))
      throw new Error('OpenList/Alist 路径不在已选择的根目录内。')
    return normalized
  }

  private filterRecordsInRoot(records: readonly AlistFileRecord[]): AlistFileRecord[] {
    return records.filter(record => isPathWithinRoot(record.path, this.rootPath))
  }

  private ensureRecordInRoot(record: AlistFileRecord): void {
    if (!isPathWithinRoot(record.path, this.rootPath))
      throw new Error('OpenList/Alist 返回的文件路径不在已选择的根目录内。')
  }

  private loadRawScanCache() {
    return loadRawSourceScanCache(this.id, 'alist', this.rootPath)
  }

  private getRawScannedDetail(id: string): MediaDetail | null {
    try {
      const cache = this.loadRawScanCache()
      return cache ? getRawScannedMediaDetail(cache, id) : null
    }
    catch {
      return null
    }
  }

  private listRawScannedChildren(id: string | undefined): MediaItem[] | null {
    if (!id)
      return null
    try {
      const cache = this.loadRawScanCache()
      return cache ? listRawScannedChildren(cache, id) : null
    }
    catch {
      return null
    }
  }
}

export async function createAuthenticatedAlistSetupSource(input: AlistSetupSessionInput): Promise<AlistDataSource> {
  const displayName = input.displayName?.trim() || 'OpenList/Alist'
  const source = new AlistDataSource()
  try {
    await source.init({
      id: input.id,
      type: 'alist',
      name: displayName,
      displayName,
      order: input.order ?? 0,
      url: input.url.trim(),
      enabled: true,
      extra: {
        rootPath: '/',
      },
    })
    await source.authenticate(input.username, input.password)
    await source.test()
    return source
  }
  catch (error) {
    source.destroy()
    throw error
  }
}

export async function loginAlistAndCreateConfig(input: AlistLoginConfigInput): Promise<AlistLoginConfigResult> {
  const credentialRef = createCredentialRef(input.id, 'alist')
  const displayName = input.displayName?.trim() || 'OpenList/Alist'
  const rootPath = normalizeAlistPath(input.rootPath)
  const config: DataSourceConfig = {
    id: input.id,
    type: 'alist',
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

  const source = new AlistDataSource()
  await source.init(config)
  const previousCredential = await readRawCredentialBackup(credentialRef)
  const auth = await source.authenticate(input.username, input.password)
  try {
    await saveAlistCredential(credentialRef, {
      token: auth.token,
      username: input.username.trim(),
      password: input.password,
    })
  }
  catch (error) {
    throw new Error(redactSensitiveText(error))
  }

  let libraries: MediaLibrary[] = []
  try {
    await source.init(config)
    await source.test()
    libraries = await source.listLibraries()
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

  return {
    config: {
      ...config,
      extra: {
        ...config.extra,
        libraries: libraries.map(library => ({
          id: library.id,
          name: library.name,
          type: library.type,
        })),
      },
    },
    libraries,
  }
}

function normalizeBaseUrl(value: string): string {
  const trimmed = value.trim()
  if (!trimmed)
    throw new Error('请输入 OpenList/Alist URL。')

  try {
    const url = new URL(trimmed)
    if (url.protocol !== 'http:' && url.protocol !== 'https:')
      throw new Error('OpenList/Alist URL 仅支持 http 或 https。')
    if (url.username || url.password || url.search || url.hash)
      throw new Error('OpenList/Alist URL 不能包含账号、密码、查询参数或片段。')

    const pathname = url.pathname.replace(/\/+$/, '')
    return `${url.origin}${pathname === '/' ? '' : pathname}`
  }
  catch (error) {
    if (error instanceof Error && error.message.includes('OpenList/Alist'))
      throw error
    throw new Error('OpenList/Alist URL 格式无效。')
  }
}

function readAlistExtra(config: DataSourceConfig): AlistConfigExtra {
  const extra = config.extra ?? {}
  return {
    credentialRef: typeof extra.credentialRef === 'string' ? extra.credentialRef : undefined,
    rootPath: typeof extra.rootPath === 'string' ? normalizeAlistPath(extra.rootPath) : '/',
  }
}

export function readAlistRootPath(config: DataSourceConfig | null | undefined): string {
  if (!config || config.type !== 'alist')
    return '/'
  try {
    return readAlistExtra(config).rootPath
  }
  catch {
    return '/'
  }
}

export function normalizeAlistRootPath(value: string | undefined): string {
  return normalizeAlistPath(value)
}

function unwrapAlistData(value: unknown): unknown {
  if (!isObject(value))
    return value

  const envelope = value as AlistApiEnvelope
  if (typeof envelope.code === 'number' && envelope.code !== 200 && envelope.code !== 0)
    throw new AlistProviderApiError(envelope.message || 'OpenList/Alist API 返回失败。', envelope.code)

  return Object.prototype.hasOwnProperty.call(value, 'data') ? envelope.data : value
}

function isAlistAuthenticationFailure(error: unknown): boolean {
  const status = statusCodeFromError(error)
  if (status === 401 || status === 403)
    return true

  if (error instanceof AlistProviderApiError && (error.code === 401 || error.code === 403))
    return true

  const text = errorText(error).toLowerCase()
  if (!text)
    return false

  return [
    'unauthorized',
    'forbidden',
    'invalid token',
    'token invalid',
    'expired token',
    'token expired',
    'auth failed',
    'auth error',
    'authorization failed',
    'not login',
    'not logged in',
    '未登录',
    '未登入',
    '登录过期',
    '登录已过期',
    '登录失效',
    '登陆过期',
    '登陆已过期',
    '登陆失效',
    '令牌无效',
    '令牌过期',
    '凭证无效',
    '无权限',
  ].some(phrase => text.includes(phrase))
}

function statusCodeFromError(error: unknown): number | undefined {
  const candidates: unknown[] = []
  if (isObject(error)) {
    candidates.push(error.status, error.statusCode)
    if (isObject(error.response))
      candidates.push(error.response.status, error.response.statusCode)
    if (isObject(error.data))
      candidates.push(error.data.status, error.data.statusCode, error.data.code)
  }
  return candidates.find((value): value is number => typeof value === 'number' && Number.isFinite(value))
}

function errorText(error: unknown): string {
  const parts: string[] = []
  if (error instanceof Error)
    parts.push(error.message)
  if (isObject(error)) {
    if (typeof error.message === 'string')
      parts.push(error.message)
    if (typeof error.statusMessage === 'string')
      parts.push(error.statusMessage)
    if (typeof error.data === 'string')
      parts.push(error.data)
    if (isObject(error.data)) {
      if (typeof error.data.message === 'string')
        parts.push(error.data.message)
      if (typeof error.data.error === 'string')
        parts.push(error.data.error)
    }
  }
  return parts.join(' ')
}

function parseAuthResponse(value: unknown): AlistAuthResult {
  const data = unwrapAlistData(value)
  if (!isObject(data))
    throw new Error('OpenList/Alist 登录响应格式无效。')

  const token = data.token
  if (typeof token !== 'string' || !token.trim())
    throw new Error('OpenList/Alist 登录响应缺少 token。')

  return { token }
}

function parseListRecords(value: unknown, parent: string): AlistFileRecord[] {
  let content: unknown[] = []
  if (Array.isArray(value)) {
    content = value
  }
  else if (isObject(value)) {
    const response = value as AlistListResponse
    if (Array.isArray(response.content))
      content = response.content
  }

  return content
    .map(item => parseOptionalFileRecord(item, parent, false))
    .filter((record): record is AlistFileRecord => record != null)
}

function parseOptionalFileRecord(value: unknown, fallbackParent: string, includeSign: boolean): AlistFileRecord | null {
  try {
    return parseFileRecord(value, fallbackParent, includeSign)
  }
  catch {
    return null
  }
}

function parseFileRecord(value: unknown, fallbackParent: string, includeSign: boolean): AlistFileRecord {
  if (!isObject(value))
    throw new Error('OpenList/Alist 返回了无效的文件条目。')

  const record = value as Record<string, unknown>
  const name = stringValue(record.name) ?? basename(stringValue(record.path)) ?? ''
  const parent = stringValue(record.parent) ?? fallbackParent
  const path = normalizeAlistPath(stringValue(record.path) ?? joinAlistPath(parent, name))

  if (!name && path === '/')
    throw new Error('OpenList/Alist 返回的文件条目缺少名称。')
  if (!name)
    throw new Error('OpenList/Alist 返回的文件条目缺少名称。')

  return {
    name,
    path,
    parent: normalizeAlistPath(parent),
    isDir: booleanValue(record.is_dir ?? record.isDir),
    size: numberValue(record.size),
    modified: stringValue(record.modified),
    created: stringValue(record.created),
    sign: includeSign ? stringValue(record.sign) : undefined,
  }
}

function normalizeAlistPath(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed)
    return '/'
  const withRoot = trimmed.startsWith('/') ? trimmed : `/${trimmed}`
  const normalized = withRoot.replace(/\/+/g, '/')
  const hasUnsafeSegment = normalized
    .split('/')
    .filter(Boolean)
    .some(segment => segment === '.' || segment === '..')
  if (hasUnsafeSegment)
    throw new Error('OpenList/Alist 路径包含不安全的相对段。')
  return normalized === '/' ? '/' : normalized.replace(/\/+$/, '')
}

function parentPath(path: string): string {
  const normalized = normalizeAlistPath(path)
  if (normalized === '/')
    return '/'
  const index = normalized.lastIndexOf('/')
  return index <= 0 ? '/' : normalized.slice(0, index)
}

function isPathWithinRoot(path: string, rootPath: string): boolean {
  const normalizedPath = normalizeAlistPath(path)
  const normalizedRoot = normalizeAlistPath(rootPath)
  return normalizedRoot === '/'
    || normalizedPath === normalizedRoot
    || normalizedPath.startsWith(`${normalizedRoot}/`)
}

function joinAlistPath(parent: string, name: string): string {
  if (!name)
    return normalizeAlistPath(parent)
  return normalizeAlistPath(`${normalizeAlistPath(parent)}/${name}`)
}

function encodeAlistPath(path: string): string {
  return normalizeAlistPath(path)
    .split('/')
    .map(segment => encodeURIComponent(segment))
    .join('/')
}

function basename(path: string | undefined): string | undefined {
  if (!path)
    return undefined
  const normalized = normalizeAlistPath(path)
  return normalized.split('/').filter(Boolean).at(-1)
}

function fileExtension(name: string): string | undefined {
  const match = /\.([a-z0-9]{1,12})$/i.exec(name)
  return match?.[1]?.toLowerCase()
}

function stringValue(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined
}

function numberValue(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function booleanValue(value: unknown): boolean {
  if (typeof value === 'boolean')
    return value
  if (typeof value === 'number' && Number.isFinite(value))
    return value !== 0
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase()
    if (['true', '1', 'yes'].includes(normalized))
      return true
    if (['false', '0', 'no'].includes(normalized))
      return false
  }
  return false
}

function sanitizeExportConfig(config: DataSourceConfig | null): DataSourceConfig {
  if (!config)
    throw new Error('OpenList/Alist source is not configured.')

  const safeExtra = Object.fromEntries(
    Object.entries(config.extra ?? {}).filter(([key]) => !isSensitiveConfigKey(key)),
  )

  return {
    ...config,
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

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
