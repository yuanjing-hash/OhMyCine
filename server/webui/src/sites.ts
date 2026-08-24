export interface SiteHealth {
  status: 'unknown' | 'online' | 'offline'
  error_code: string
  username: string
  checked_at?: string
}

export interface SiteSummary {
  id: number
  name: string
  kind: 'pttime'
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
  failed: number
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
  status: 'success' | 'error'
  error_code?: string
  page: number
  has_next: boolean
  skipped: number
  items: PTSearchResult[]
}

export interface PTSearchResponse { groups: PTSearchGroup[] }

export const sitesPath = '/api/v1/sites'
export const ptSearchPath = '/api/v1/discovery/pt-search'
export const ptSearchStreamPath = '/api/v1/discovery/pt-search/stream'
export const discoveryDownloadsPath = '/api/v1/discovery/downloads'
export const cookieCloudSettingsPath = '/api/v1/settings/sites/cookiecloud'
export const cookieCloudSyncPath = `${cookieCloudSettingsPath}/sync`

export function sitePath(id: number) { return `${sitesPath}/${id}` }
export function siteTestPath(id: number) { return `${sitePath(id)}/test` }

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
