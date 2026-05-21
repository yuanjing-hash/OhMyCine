import type { DataSource, DataSourceConfig, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption } from './types'
import { ofetch } from 'ofetch'
import { SourceMetadataCache } from './cache'
import { createCredentialRef, readAlistCredential, readRawCredentialBackup, removeCredential, saveAlistCredential, saveRawCredentialBackup } from './credentialStore'
import { redactSensitiveText } from './errors'

const ALIST_REQUEST_TIMEOUT_MS = 15_000

type AlistRequestPayload = Record<string, string | number | boolean | null | undefined>

interface AlistConfigExtra {
  readonly credentialRef?: string
}

interface AlistAuthResult {
  readonly token: string
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

export interface AlistLoginConfigInput {
  readonly id: string
  readonly url: string
  readonly displayName?: string
  readonly username: string
  readonly password: string
  readonly order?: number
}

export interface AlistLoginConfigResult {
  readonly config: DataSourceConfig
  readonly libraries: MediaLibrary[]
}

export class AlistDataSource implements DataSource {
  private config: DataSourceConfig | null = null
  private baseUrl = ''
  private token = ''
  private connected = false
  private readonly cache = new SourceMetadataCache()

  readonly type = 'alist' as const

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
    this.token = await resolveToken(extra)
    this.connected = Boolean(this.baseUrl && this.token)
  }

  async test(): Promise<boolean> {
    this.ensureConfigured()
    await this.requestFsList('/', 1)
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
      const response = await ofetch<unknown>(`${this.baseUrl}/api/auth/login`, {
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
    const normalizedPath = normalizeAlistPath(path)
    const records = await this.cache.getOrSet(`list:${normalizedPath}`, () => this.requestFsList(normalizedPath))
    return records.map(record => this.mapItem(record))
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    this.ensureConfigured()
    return [
      {
        id: '/',
        sourceId: this.id,
        name: '文件目录',
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
      return records.map(record => this.mapItem(record))
    }
    catch {
      const records = await this.searchByListing('/', trimmed)
      return records.map(record => this.mapItem(record))
    }
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const path = normalizeAlistPath(id)
    const record = await this.cache.getOrSet(`detail:${path}`, () => this.requestFsGet(path, false))
    const item = this.mapItem(record)
    return {
      ...item,
      mediaSources: item.type === 'folder' ? [] : [this.mapMediaSource(record)],
    }
  }

  async getStreamURL(id: string): Promise<string> {
    const path = normalizeAlistPath(id)
    const record = await this.requestFsGet(path, true)
    if (record.isDir)
      throw new Error('OpenList/Alist 文件夹不能直接播放。')

    return this.buildDownloadUrl(path, record.sign)
  }

  exportConfig(): DataSourceConfig {
    this.ensureConfigured()
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
      parent: '/',
      keywords: keyword,
      scope: 0,
      page: 1,
      per_page: 100,
    })
    return parseListRecords(data, '/')
  }

  private async request(path: string, body: AlistRequestPayload): Promise<unknown> {
    this.ensureConfigured()

    try {
      const response = await ofetch<unknown>(`${this.baseUrl}${path}`, {
        method: 'POST',
        timeout: ALIST_REQUEST_TIMEOUT_MS,
        body,
        headers: this.authHeaders(),
      })
      return unwrapAlistData(response)
    }
    catch (error) {
      throw new Error(redactSensitiveText(error))
    }
  }

  private authHeaders(): Record<string, string> {
    return {
      Authorization: this.token,
    }
  }

  private async searchByListing(rootPath: string, keyword: string, maxDepth = 2, maxVisited = 80): Promise<AlistFileRecord[]> {
    const normalizedKeyword = keyword.toLocaleLowerCase()
    const results: AlistFileRecord[] = []
    const queue: Array<{ path: string, depth: number }> = [{ path: rootPath, depth: 0 }]
    let visited = 0

    while (queue.length > 0 && visited < maxVisited) {
      const current = queue.shift()
      if (!current)
        break
      visited += 1

      const children = await this.requestFsList(current.path, 100)
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
      libraryId: '/',
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
    const url = new URL(`${this.baseUrl}/d${encodeAlistPath(path)}`)
    if (sign)
      url.searchParams.set('sign', sign)
    return url.toString()
  }

  private ensureConfigured(): void {
    if (!this.config || !this.baseUrl)
      throw new Error('OpenList/Alist source is not configured.')
    if (!this.token)
      throw new Error('OpenList/Alist 登录凭证缺失。请在设置的数据源管理中重新编辑并登录。')
  }
}

export async function loginAlistAndCreateConfig(input: AlistLoginConfigInput): Promise<AlistLoginConfigResult> {
  const credentialRef = createCredentialRef(input.id, 'alist')
  const displayName = input.displayName?.trim() || 'OpenList/Alist'
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
  }
}

async function resolveToken(extra: AlistConfigExtra): Promise<string> {
  if (!extra.credentialRef)
    return ''

  const credential = await readAlistCredential(extra.credentialRef)
  return credential?.token ?? ''
}

function unwrapAlistData(value: unknown): unknown {
  if (!isObject(value))
    return value

  const envelope = value as AlistApiEnvelope
  if (typeof envelope.code === 'number' && envelope.code !== 200 && envelope.code !== 0)
    throw new Error(envelope.message || 'OpenList/Alist API 返回失败。')

  return Object.prototype.hasOwnProperty.call(value, 'data') ? envelope.data : value
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
  if (!trimmed || trimmed === '.')
    return '/'
  const withRoot = trimmed.startsWith('/') ? trimmed : `/${trimmed}`
  const normalized = withRoot.replace(/\/+/g, '/')
  const hasUnsafeSegment = normalized
    .split('/')
    .filter(Boolean)
    .some(segment => segment === '.' || segment === '..')
  if (hasUnsafeSegment)
    throw new Error('OpenList/Alist 路径包含不安全的相对段。')
  return normalized
}

function parentPath(path: string): string {
  const normalized = normalizeAlistPath(path)
  if (normalized === '/')
    return '/'
  const index = normalized.lastIndexOf('/')
  return index <= 0 ? '/' : normalized.slice(0, index)
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
