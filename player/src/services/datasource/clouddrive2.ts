import type { CloudDrive2CredentialValue } from './credentialStore'
import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption, MediaStreamRequest } from './types'
import { ofetch } from 'ofetch'
import { createRawSourceHomeSections, getRawScannedMediaDetail, isRawScannedSyntheticId, listRawScannedChildren, loadRawSourceScanCache } from '@/services/scraper'
import { getVideoFileExtension, isPathWithinRoot, isVideoFileName, normalizeProviderPath, providerBasename, splitProviderPath } from '@/services/scraper/pathUtils'
import { SourceMetadataCache } from './cache'
import { createCredentialRef, readCloudDrive2Credential, readRawCredentialBackup, removeCredential, saveCloudDrive2Credential, saveRawCredentialBackup } from './credentialStore'
import { redactSensitiveText } from './errors'

const CLOUDDRIVE2_REQUEST_TIMEOUT_MS = 15_000

type CloudDrive2CredentialReader = (ref: string) => Promise<CloudDrive2CredentialValue | null>

export type CloudDrive2Fetch = <T = unknown>(
  request: Parameters<typeof ofetch>[0],
  options?: Parameters<typeof ofetch>[1],
) => Promise<T>

interface CloudDrive2ConfigExtra {
  readonly credentialRef?: string
  readonly rootPath: string
}

interface CloudDrive2FileRecord {
  readonly name: string
  readonly path: string
  readonly isDir: boolean
  readonly size?: number
  readonly modified?: string
}

interface NodeBufferLike {
  from: (input: string, encoding: 'utf8') => { toString: (encoding: 'base64') => string }
}

export interface CloudDrive2DataSourceOptions {
  readonly fetcher?: CloudDrive2Fetch
  readonly readCredential?: CloudDrive2CredentialReader
}

export interface CloudDrive2LoginConfigInput {
  readonly id: string
  readonly url: string
  readonly displayName?: string
  readonly username: string
  readonly password: string
  readonly rootPath?: string
  readonly order?: number
}

export interface CloudDrive2LoginConfigResult {
  readonly config: DataSourceConfig
  readonly libraries: MediaLibrary[]
}

export interface CloudDrive2SetupSessionInput {
  readonly id: string
  readonly url: string
  readonly displayName?: string
  readonly username: string
  readonly password: string
  readonly order?: number
}

export class CloudDrive2DataSource implements DataSource {
  private config: DataSourceConfig | null = null
  private baseUrl = ''
  private credentialRef = ''
  private credential: CloudDrive2CredentialValue | null = null
  private rootPath = '/'
  private connected = false
  private readonly cache = new SourceMetadataCache()
  private readonly fetcher: CloudDrive2Fetch
  private readonly readCredential: CloudDrive2CredentialReader

  readonly type = 'clouddrive2' as const

  constructor(options: CloudDrive2DataSourceOptions = {}) {
    this.fetcher = options.fetcher ?? (ofetch as CloudDrive2Fetch)
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
    await this.requestPropfind(this.rootPath, 1)
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
    const records = await this.cache.getOrSet(`list:${providerPath}`, () => this.requestPropfind(providerPath, 1))
    return this.filterRecordsInRoot(records)
      .filter(record => record.path !== providerPath)
      .filter(record => record.isDir || isVideoFileName(record.name))
      .map(record => this.mapItem(record))
  }

  async listLibraries(): Promise<MediaLibrary[]> {
    this.ensureConfigured()
    return [
      {
        id: this.rootPath,
        sourceId: this.id,
        name: this.rootPath === '/' ? 'WebDAV 文件目录' : (providerBasename(this.rootPath) ?? this.rootPath),
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
    const queue: Array<{ path: string, depth: number }> = [{ path: this.rootPath, depth: 0 }]
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
      throw new Error('CloudDrive2 本地扫描合集不能直接播放，请选择具体文件或分集。')

    const providerPath = this.resolveLibraryPath(id)
    const record = await this.cache.getOrSet(`detail:${providerPath}`, async () => {
      const records = await this.requestPropfind(providerPath, 0)
      return records.find(item => item.path === providerPath) ?? createFallbackRecord(providerPath)
    })
    this.ensureRecordInRoot(record)
    const item = this.mapItem(record)
    return {
      ...item,
      mediaSources: item.type === 'folder' || !isVideoFileName(item.name) ? [] : [this.mapMediaSource(record)],
    }
  }

  async getStreamURL(id: string): Promise<string> {
    const path = await this.resolvePlayablePath(id)
    return this.buildWebDavUrl(path, false)
  }

  async getStreamRequest(id: string): Promise<MediaStreamRequest> {
    const url = await this.getStreamURL(id)
    return {
      url,
      headers: await this.authHeaders(),
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

  private async resolvePlayablePath(id: string): Promise<string> {
    if (isRawScannedSyntheticId(id))
      throw new Error('CloudDrive2 剧集合集不能直接播放，请选择具体分集。')

    const path = this.resolveLibraryPath(id)
    const records = await this.requestPropfind(path, 0)
    const record = records.find(item => item.path === path) ?? createFallbackRecord(path)
    this.ensureRecordInRoot(record)
    if (record.isDir)
      throw new Error('CloudDrive2 文件夹不能直接播放。')
    if (!isVideoFileName(record.name))
      throw new Error('该 CloudDrive2 文件不是支持的视频格式。')

    return path
  }

  private async requestPropfind(path: string, depth: 0 | 1): Promise<CloudDrive2FileRecord[]> {
    this.ensureConfigured()
    const providerPath = this.resolveLibraryPath(path)
    try {
      const response = await this.fetcher<string>(this.buildWebDavUrl(providerPath, depth === 1), {
        method: 'PROPFIND',
        timeout: CLOUDDRIVE2_REQUEST_TIMEOUT_MS,
        responseType: 'text',
        body: webDavPropfindBody(),
        headers: {
          ...(await this.authHeaders()),
          'Depth': String(depth),
          'Content-Type': 'application/xml; charset=utf-8',
        },
      })
      const records = parsePropfindRecords(String(response), this.baseUrl)
      this.connected = true
      return records
    }
    catch (error) {
      this.connected = false
      throw new Error(redactSensitiveText(error))
    }
  }

  private async authHeaders(): Promise<Record<string, string>> {
    const credential = await this.ensureCredential()
    return {
      Authorization: `Basic ${base64Utf8(`${credential.username}:${credential.password}`)}`,
    }
  }

  private async ensureCredential(): Promise<CloudDrive2CredentialValue> {
    this.ensureConfigured({ requireCredential: false })
    if (this.credential)
      return this.credential

    const credential = await this.readStoredCredential()
    if (!credential)
      throw new Error('CloudDrive2 登录凭证缺失。请在设置的数据源管理中重新编辑并登录。')

    this.credential = credential
    this.connected = true
    return credential
  }

  private async readStoredCredential(): Promise<CloudDrive2CredentialValue | null> {
    if (!this.credentialRef)
      return null
    return this.readCredential(this.credentialRef)
  }

  private mapItem(record: CloudDrive2FileRecord): MediaItem {
    return {
      id: record.path,
      sourceId: this.id,
      libraryId: this.rootPath,
      name: record.name,
      type: record.isDir ? 'folder' : 'file',
      size: record.isDir ? undefined : record.size,
      modified: record.modified,
      path: record.path,
    }
  }

  private mapMediaSource(record: CloudDrive2FileRecord): MediaSourceOption {
    return {
      id: 'default',
      name: 'WebDAV',
      container: getVideoFileExtension(record.name) ?? undefined,
      size: record.size,
      isRemote: true,
    }
  }

  private buildWebDavUrl(path: string, directory: boolean): string {
    if (!isPathWithinRoot(path, this.rootPath))
      throw new Error('CloudDrive2 路径不在已选择的根目录内。')
    return buildWebDavUrl(this.baseUrl, path, directory)
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

  private filterRecordsInRoot(records: readonly CloudDrive2FileRecord[]): CloudDrive2FileRecord[] {
    return records.filter(record => isPathWithinRoot(record.path, this.rootPath))
  }

  private ensureRecordInRoot(record: CloudDrive2FileRecord): void {
    if (!isPathWithinRoot(record.path, this.rootPath))
      throw new Error('CloudDrive2 返回的文件路径不在已选择的根目录内。')
  }

  private ensureConfigured(options: { requireCredential?: boolean } = {}): void {
    if (!this.config || !this.baseUrl)
      throw new Error('CloudDrive2 数据源未配置。')
    if (options.requireCredential !== false && !this.credential && !this.credentialRef)
      throw new Error('CloudDrive2 登录凭证缺失。请在设置的数据源管理中重新编辑并登录。')
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
  const credential = normalizeLoginCredential(input.username, input.password)
  const displayName = input.displayName?.trim() || 'CloudDrive2'
  const credentialRef = createCredentialRef(input.id, 'clouddrive2')
  const source = new CloudDrive2DataSource({
    readCredential: async () => credential,
  })
  try {
    await source.init({
      id: input.id,
      type: 'clouddrive2',
      name: displayName,
      displayName,
      order: input.order ?? 0,
      url: input.url.trim(),
      enabled: true,
      extra: {
        credentialRef,
        rootPath: '/',
      },
    })
    await source.test()
    return source
  }
  catch (error) {
    source.destroy()
    throw error
  }
}

export async function loginCloudDrive2AndCreateConfig(input: CloudDrive2LoginConfigInput): Promise<CloudDrive2LoginConfigResult> {
  const credential = normalizeLoginCredential(input.username, input.password)
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

function normalizeLoginCredential(username: string, password: string): CloudDrive2CredentialValue {
  const trimmedUsername = username.trim()
  if (!trimmedUsername || !password)
    throw new Error('请输入 CloudDrive2 WebDAV 账号和密码。')
  return {
    username: trimmedUsername,
    password,
  }
}

function normalizeCloudDrive2BaseUrl(value: string): string {
  const trimmed = value.trim()
  if (!trimmed)
    throw new Error('请输入 CloudDrive2 WebDAV URL。')

  try {
    const url = new URL(trimmed)
    if (url.protocol !== 'http:' && url.protocol !== 'https:')
      throw new Error('CloudDrive2 WebDAV URL 仅支持 http 或 https。')
    if (url.username || url.password || url.search || url.hash)
      throw new Error('CloudDrive2 WebDAV URL 不能包含账号、密码、查询参数或片段。')

    const pathname = url.pathname.replace(/\/+$/, '')
    return `${url.origin}${pathname === '/' ? '' : pathname}`
  }
  catch (error) {
    if (error instanceof Error && error.message.includes('CloudDrive2'))
      throw error
    throw new Error('CloudDrive2 WebDAV URL 格式无效。')
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

  const safeExtra = Object.fromEntries(
    Object.entries(config.extra ?? {}).filter(([key]) => !isSensitiveConfigKey(key)),
  )

  return {
    ...config,
    url: normalizeCloudDrive2BaseUrl(config.url),
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

function webDavPropfindBody(): string {
  return [
    '<?xml version="1.0" encoding="utf-8"?>',
    '<d:propfind xmlns:d="DAV:">',
    '<d:prop>',
    '<d:displayname />',
    '<d:resourcetype />',
    '<d:getcontentlength />',
    '<d:getlastmodified />',
    '</d:prop>',
    '</d:propfind>',
  ].join('')
}

function parsePropfindRecords(xml: string, baseUrl: string): CloudDrive2FileRecord[] {
  return matchXmlBlocks(xml, 'response')
    .filter(isSuccessfulResponseBlock)
    .map((block): CloudDrive2FileRecord | null => {
      const href = xmlTagText(block, 'href')
      if (!href)
        return null
      const path = providerPathFromHref(href, baseUrl)
      const displayName = xmlTagText(block, 'displayname')
      const isDir = xmlTagExists(block, 'collection') || href.trim().endsWith('/')
      const size = isDir ? undefined : numberValue(xmlTagText(block, 'getcontentlength'))
      const modified = modifiedIso(xmlTagText(block, 'getlastmodified'))
      return {
        name: displayName?.trim() || providerBasename(path) || '文件目录',
        path,
        isDir,
        size,
        modified,
      }
    })
    .filter((record): record is CloudDrive2FileRecord => record != null)
}

function matchXmlBlocks(xml: string, localName: string): string[] {
  const pattern = new RegExp(`<(?:[\\w.-]+:)?${localName}\\b[\\s\\S]*?<\\/(?:[\\w.-]+:)?${localName}>`, 'gi')
  return xml.match(pattern) ?? []
}

function xmlTagText(xml: string, localName: string): string | undefined {
  const pattern = new RegExp(`<(?:[\\w.-]+:)?${localName}\\b[^>]*>([\\s\\S]*?)<\\/(?:[\\w.-]+:)?${localName}>`, 'i')
  const match = pattern.exec(xml)
  return match ? decodeXmlText(match[1].trim()) : undefined
}

function xmlTagExists(xml: string, localName: string): boolean {
  const pattern = new RegExp(`<(?:[\\w.-]+:)?${localName}\\b`, 'i')
  return pattern.test(xml)
}

function isSuccessfulResponseBlock(block: string): boolean {
  const statusTexts = matchXmlBlocks(block, 'status').map(item => xmlTagText(item, 'status') ?? '')
  if (statusTexts.length === 0)
    return true
  return statusTexts.some((status) => {
    const code = /\s(\d{3})\s/.exec(status)?.[1]
    return code ? code.startsWith('2') : false
  })
}

function decodeXmlText(value: string): string {
  return value
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, '\'')
    .replace(/&amp;/g, '&')
    .replace(/&#(\d+);/g, (_, code: string) => String.fromCodePoint(Number(code)))
    .replace(/&#x([0-9a-f]+);/gi, (_, code: string) => String.fromCodePoint(Number.parseInt(code, 16)))
}

function providerPathFromHref(href: string, baseUrl: string): string {
  const base = new URL(baseUrl)
  const url = new URL(href, `${baseUrl}/`)
  const basePath = base.pathname.replace(/\/+$/, '')
  let pathname = url.pathname
  if (basePath && (pathname === basePath || pathname.startsWith(`${basePath}/`)))
    pathname = pathname.slice(basePath.length) || '/'
  return normalizeProviderPath(decodeProviderPathname(pathname))
}

function decodeProviderPathname(pathname: string): string {
  return pathname
    .split('/')
    .map((segment) => {
      try {
        return decodeURIComponent(segment)
      }
      catch {
        return segment
      }
    })
    .join('/')
}

function buildWebDavUrl(baseUrl: string, path: string, directory: boolean): string {
  const url = new URL(baseUrl)
  const basePath = url.pathname.replace(/\/+$/, '')
  const encodedSegments = splitProviderPath(path).map(segment => encodeURIComponent(segment))
  const nextPath = [basePath, ...encodedSegments].filter(Boolean).join('/')
  url.pathname = `/${nextPath}`.replace(/\/+/g, '/')
  if (directory && !url.pathname.endsWith('/'))
    url.pathname = `${url.pathname}/`
  url.search = ''
  url.hash = ''
  return url.toString()
}

function createFallbackRecord(path: string): CloudDrive2FileRecord {
  return {
    name: providerBasename(path) ?? path,
    path,
    isDir: false,
  }
}

function modifiedIso(value: string | undefined): string | undefined {
  if (!value)
    return undefined
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? new Date(timestamp).toISOString() : undefined
}

function numberValue(value: string | undefined): number | undefined {
  if (!value)
    return undefined
  const numeric = Number(value)
  return Number.isFinite(numeric) ? numeric : undefined
}

function base64Utf8(value: string): string {
  const encoder = globalThis.TextEncoder ? new TextEncoder() : null
  if (typeof globalThis.btoa === 'function' && encoder) {
    const bytes = encoder.encode(value)
    let binary = ''
    for (const byte of bytes)
      binary += String.fromCharCode(byte)
    return globalThis.btoa(binary)
  }

  const buffer = Reflect.get(globalThis, 'Buffer') as unknown
  if (isNodeBufferLike(buffer))
    return buffer.from(value, 'utf8').toString('base64')

  throw new Error('当前运行环境不支持 Basic Auth 编码。')
}

function isNodeBufferLike(value: unknown): value is NodeBufferLike {
  return typeof value === 'object'
    && value != null
    && typeof Reflect.get(value, 'from') === 'function'
}
