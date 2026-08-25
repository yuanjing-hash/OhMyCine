export interface SiteHealth {
  status: 'unknown' | 'online' | 'offline'
  error_code: string
  username: string
  checked_at?: string
}

export interface SiteSummary {
  id: number
  name: string
  kind: string
  site_type: 'pt' | 'bt'
  credential_kind: 'cookie' | 'api_key' | 'none'
  base_url: string
  user_agent: string
  enabled: boolean
  priority: number
  timeout_seconds: number
  rate_limit_per_minute: number
  browser_emulation: boolean
  browser_service_url: string
  credential_configured: boolean
  health: SiteHealth
  revision: number
  created_at: string
  updated_at: string
}

export interface SiteCatalogItem {
  key: string
  name: string
  engine: string
  base_urls: string[]
  auto_discover: boolean
  site_type: 'pt' | 'bt'
  credential_kind: 'cookie' | 'api_key' | 'none'
}

export type CookieCloudMode = 'disabled' | 'remote' | 'local'

export interface CookieCloudSettings {
  mode: CookieCloudMode
  base_url: string
  auto_sync_minutes: number
  credential_configured: boolean
  local_upload_path?: string
  last_sync_status: string
  last_sync_error_code: string
  last_sync_at?: string
  revision: number
}

export interface CookieCloudSyncResult {
  status: string
  created: number
  updated: number
  skipped: number
  skipped_unsupported_domains: number
  skipped_missing_login_cookies: number
  failed: number
  issues?: CookieCloudSyncIssue[]
}

export interface CookieCloudSyncIssue {
  action: 'create' | 'update'
  site_id?: number
  kind: string
  error_code: string
}

export interface PTSearchResult {
  token: string
  title: string
  subtitle?: string
  size_bytes?: number
  published_at?: string
  seeders?: number
  leechers?: number
  completed?: number
  promotion?: string
  quality?: string
  tags?: string[]
  expires_at: string
}

export interface PTSearchGroup {
  site_id: number
  site_name: string
  site_type: 'pt' | 'bt'
  status: 'success' | 'error'
  error_code?: string
  page: number
  has_next: boolean
  skipped: number
  items: PTSearchResult[]
}

export interface PTRecognitionSpecifications {
  resolution?: string
  source?: string
  video_codec?: string
  audio_codec?: string
  hdr?: string
  release_group?: string
}

export interface PTRecognitionResult {
  status: 'matched' | 'unrecognized'
  error_code?: string
  title: string
  original_title?: string
  media_type?: 'movie' | 'tv'
  year?: number
  tmdb_id?: number
  poster_url?: string
  specifications: PTRecognitionSpecifications
}

export interface PTSearchResponse { groups: PTSearchGroup[] }

export const sitesPath = '/api/v1/sites'
export const siteCatalogPath = `${sitesPath}/catalog`
export const ptSearchPath = '/api/v1/discovery/pt-search'
export const ptSearchStreamPath = '/api/v1/discovery/pt-search/stream'
export const ptRecognitionPath = '/api/v1/discovery/pt-results/recognize'
export const torrentSearchPath = '/api/v1/discovery/torrent-search'
export const torrentSearchStreamPath = '/api/v1/discovery/torrent-search/stream'
export const torrentRecognitionPath = '/api/v1/discovery/torrent-results/recognize'
export const discoveryDownloadsPath = '/api/v1/discovery/downloads'
export const cookieCloudSettingsPath = '/api/v1/settings/sites/cookiecloud'
export const cookieCloudSyncPath = `${cookieCloudSettingsPath}/sync`

export function sitePath(id: number) { return `${sitesPath}/${id}` }
export function siteTestPath(id: number) { return `${sitePath(id)}/test` }

export function cookieCloudErrorLabel(code: string) {
  return ({
    site_authentication_failed: '站点登录 Cookie 已失效或不完整',
    site_unavailable: '站点暂时不可用',
    site_rate_limited: '站点请求受到限速',
    site_response_invalid: '站点返回内容无法识别',
    site_credential_invalid: '已保存的站点凭据不可用',
    CONFLICT: '站点配置已变化，请重新同步',
  } as Record<string, string>)[code] || code || '未知错误'
}

export function buildPTSearchQuery(input: { keyword: string; mediaType?: string; year?: number; tmdbID?: number; searchBy?: 'title' | 'tmdb_id'; page?: number; siteID?: number }) {
  const query = new URLSearchParams({ keyword: input.keyword.trim(), page: String(input.page ?? 1) })
  if (input.mediaType) query.set('media_type', input.mediaType)
  if (input.year) query.set('year', String(input.year))
  if (input.tmdbID) query.set('tmdb_id', String(input.tmdbID))
  if (input.searchBy) query.set('search_by', input.searchBy)
  if (input.siteID) query.set('site_id', String(input.siteID))
  return query
}

export function ptSearchURL(base: string, input: Parameters<typeof buildPTSearchQuery>[0]) {
  return `${base}?${buildPTSearchQuery(input)}`
}

export function ptRecognitionSpecLabels(value: PTRecognitionSpecifications) {
  return [value.resolution, value.source, value.video_codec, value.audio_codec, value.hdr, value.release_group ? `压制组 ${value.release_group}` : ''].filter((item): item is string => Boolean(item))
}

export function ptRecognitionErrorLabel(code?: string) {
  return ({
    tmdb_credential_unavailable: 'TMDB 尚未配置，当前仅显示本地解析结果',
    tmdb_unavailable: 'TMDB 暂时不可用，当前仅显示本地解析结果',
    tmdb_network_unavailable: 'TMDB 网络不可用，当前仅显示本地解析结果',
    tmdb_no_match: 'TMDB 未找到可信匹配，当前仅显示本地解析结果',
    tmdb_low_confidence: 'TMDB 候选可信度不足，未自动认定作品',
    tmdb_candidate_conflict: '存在多个相近候选，未自动认定作品',
    tmdb_auth_failed: 'TMDB 凭据认证失败，当前仅显示本地解析结果',
    tmdb_invalid_response: 'TMDB 返回内容异常，当前仅显示本地解析结果',
    tmdb_request_failed: 'TMDB 请求失败，当前仅显示本地解析结果',
    tmdb_invalid_request: '发行标题无法安全解析',
  } as Record<string, string>)[code || ''] || '暂未识别到可信作品'
}

export function upsertPTGroup(groups: PTSearchGroup[], incoming: PTSearchGroup, append = false) {
  const index = groups.findIndex(group => group.site_id === incoming.site_id)
  if (index < 0) return [...groups, incoming]
  const next = [...groups]
  const previous = next[index]
  next[index] = append && incoming.status === 'success'
    ? { ...incoming, items: [...previous.items, ...incoming.items.filter(item => !previous.items.some(existing => existing.token === item.token))] }
    : incoming
  return next
}

export type TorrentSearchResult = PTSearchResult
export type TorrentSearchGroup = PTSearchGroup
export type TorrentSearchResponse = PTSearchResponse
export type TorrentRecognitionResult = PTRecognitionResult
export const buildTorrentSearchQuery = buildPTSearchQuery
export const torrentSearchURL = ptSearchURL
export const upsertTorrentGroup = upsertPTGroup

export const torrentSearchSessionKey = 'omc:server:torrent-search:v1'
const torrentSearchSessionMaxBytes = 512 * 1024
const torrentSearchSessionMaxAgeMs = 30 * 60 * 1000

export interface TorrentSearchSession {
  input: { keyword: string; mediaType: string; year?: number; tmdbID?: number; searchBy: 'title' | 'tmdb_id' }
  groups: TorrentSearchGroup[]
  recognitions: Record<string, TorrentRecognitionResult>
  searched: boolean
  savedAt: number
}

type SearchSessionStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

export function saveTorrentSearchSession(storage: SearchSessionStorage | undefined, state: TorrentSearchSession) {
  if (!storage) return
  try {
    const groups: TorrentSearchGroup[] = []
    let itemCount = 0
    for (const group of state.groups.slice(0, 24)) {
      const remaining = Math.max(0, 300 - itemCount)
      const items = group.items.slice(0, remaining)
      itemCount += items.length
      groups.push({ ...group, items })
      if (itemCount >= 300) break
    }
    const allowedTokens = new Set(groups.flatMap(group => group.items.map(item => item.token)))
    const recognitions = Object.fromEntries(Object.entries(state.recognitions).filter(([token]) => allowedTokens.has(token)))
    const encoded = JSON.stringify({ ...state, groups, recognitions })
    if (encoded.length > torrentSearchSessionMaxBytes) {
      storage.removeItem(torrentSearchSessionKey)
      return
    }
    storage.setItem(torrentSearchSessionKey, encoded)
  } catch {
    // Search remains usable when browser storage is blocked or full.
  }
}

export function readTorrentSearchSession(storage: SearchSessionStorage | undefined, now = Date.now()): TorrentSearchSession | null {
  if (!storage) return null
  try {
    const raw = storage.getItem(torrentSearchSessionKey)
    if (!raw || raw.length > torrentSearchSessionMaxBytes) return null
    const value = JSON.parse(raw) as Partial<TorrentSearchSession>
    if (!value.input || !Array.isArray(value.groups) || !value.recognitions || typeof value.savedAt !== 'number' || now - value.savedAt < 0 || now - value.savedAt > torrentSearchSessionMaxAgeMs) {
      storage.removeItem(torrentSearchSessionKey)
      return null
    }
    if (value.input.searchBy !== 'title' && value.input.searchBy !== 'tmdb_id') return null
    let remainingItems = 300
    const groups = value.groups
      .slice(0, 24)
      .map((group) => {
        const items = remainingItems > 0 && Array.isArray(group.items)
          ? group.items.filter(item => typeof item?.token === 'string' && typeof item.title === 'string' && typeof item.expires_at === 'string' && Date.parse(item.expires_at) > now).slice(0, remainingItems)
          : []
        remainingItems -= items.length
        return { ...group, items }
      })
    const tokens = new Set(groups.flatMap(group => group.items.map(item => item.token)))
    const recognitions = Object.fromEntries(Object.entries(value.recognitions).filter(([token]) => tokens.has(token)))
    return {
      input: {
        keyword: typeof value.input.keyword === 'string' ? value.input.keyword.slice(0, 160) : '',
        mediaType: typeof value.input.mediaType === 'string' ? value.input.mediaType : '',
        year: typeof value.input.year === 'number' ? value.input.year : undefined,
        tmdbID: typeof value.input.tmdbID === 'number' ? value.input.tmdbID : undefined,
        searchBy: value.input.searchBy,
      },
      groups,
      recognitions,
      searched: Boolean(value.searched),
      savedAt: value.savedAt,
    }
  } catch {
    return null
  }
}
