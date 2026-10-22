// Safe, display-only facts derived by Server from the real share entries.
// These facts are never a file selection or authorization to acquire a share.
export interface ShareContentGroup {
  title: string
  media_type?: 'movie' | 'tv'
  tmdb_id?: number
  year?: number
  season?: number
  episodes: number[]
  video_count: number
  status: 'matched' | 'unrecognized'
  identity_status?: string
  error_code?: string
  poster_url?: string
}

export interface ShareContentAnalysis {
  status: 'complete' | 'unavailable'
  complete: boolean
  groups: ShareContentGroup[]
  video_count: number
  ignored_count: number
  unresolved_count: number
  message: string
  error_code?: string
  checked_at: string
  expires_at?: string
  downloader_id?: string
}

const record = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined
const text = (value: unknown, limit = 512): string | undefined => typeof value === 'string' && value.length <= limit && ![...value].some(char => char.charCodeAt(0) <= 31 || char.charCodeAt(0) === 127) ? value : undefined
const integer = (value: unknown, minimum = 0, maximum = Number.MAX_SAFE_INTEGER): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum && value <= maximum
const date = (value: unknown): value is string => typeof value === 'string' && value.length <= 64 && Number.isFinite(Date.parse(value))

export function parseShareContentAnalysis(value: unknown, now = Date.now()): ShareContentAnalysis | undefined {
  const data = record(value)
  if (!data || !['complete', 'unavailable'].includes(String(data.status)) || typeof data.complete !== 'boolean'
    || (data.complete && data.status !== 'complete') || !Array.isArray(data.groups) || data.groups.length > 32
    || !integer(data.video_count) || !integer(data.ignored_count) || !integer(data.unresolved_count)
    || text(data.message, 2048) === undefined || !date(data.checked_at)
    || (data.expires_at !== undefined && (!date(data.expires_at) || Date.parse(data.expires_at) <= now))) {
    return
  }
  const groups: ShareContentGroup[] = []
  for (const raw of data.groups) {
    const group = record(raw)
    if (!group || text(group.title) === undefined || !['matched', 'unrecognized'].includes(String(group.status))
      || !integer(group.video_count) || !Array.isArray(group.episodes) || group.episodes.length > 2000
      || group.episodes.some(episode => !integer(episode, 1, 100000))
      || (group.season !== undefined && !integer(group.season, 0, 200))
      || (group.tmdb_id !== undefined && !integer(group.tmdb_id, 1))
      || (group.year !== undefined && !integer(group.year, 1888, 2200))) {
      return
    }
    groups.push({
      title: group.title as string,
      status: group.status as ShareContentGroup['status'],
      media_type: group.media_type === 'movie' || group.media_type === 'tv' ? group.media_type : undefined,
      tmdb_id: group.tmdb_id as number | undefined,
      year: group.year as number | undefined,
      season: group.season as number | undefined,
      episodes: [...new Set(group.episodes as number[])].sort((left, right) => left - right),
      video_count: group.video_count,
      identity_status: text(group.identity_status, 128),
      error_code: text(group.error_code, 128),
      // Only protected Server artwork references can be used by clients.
      poster_url: typeof group.poster_url === 'string' && /^\/api\/v1\/(?:player\/)?discovery\/images\/(?:tmdb|douban)\/[\w-]{1,4096}$/u.test(group.poster_url) ? group.poster_url : undefined,
    })
  }
  return {
    status: data.status as ShareContentAnalysis['status'],
    complete: data.complete,
    groups,
    video_count: data.video_count,
    ignored_count: data.ignored_count,
    unresolved_count: data.unresolved_count,
    message: data.message as string,
    checked_at: data.checked_at,
    expires_at: data.expires_at as string | undefined,
    error_code: text(data.error_code, 128),
    downloader_id: text(data.downloader_id, 256),
  }
}

export function compactEpisodeSet(episodes: readonly number[]): string {
  const values = [...new Set(episodes.filter(value => integer(value, 1, 100000)))].sort((left, right) => left - right)
  const ranges: string[] = []
  for (let index = 0; index < values.length; index++) {
    const start = values[index]!
    let end = start
    while (values[index + 1] === end + 1) {
      index++
      end = values[index]!
    }
    ranges.push(start === end ? String(start) : `${start}–${end}`)
  }
  return ranges.join('、')
}

export function shareContentGroupLabel(group: ShareContentGroup): string {
  const season = group.season !== undefined ? `第 ${group.season} 季` : ''
  const episodes = compactEpisodeSet(group.episodes)
  return [group.title || '未识别作品', season, episodes ? `第 ${episodes} 集（共 ${group.episodes.length} 集）` : group.media_type === 'movie' ? '电影' : '集数待确认', `${group.video_count} 个视频`].filter(Boolean).join(' · ')
}
