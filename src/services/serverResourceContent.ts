import type { ServerDataSource } from '@/services/datasource/server'
import type { ServerResourceItem } from '@/services/serverDiscovery'
import type { ShareContentAnalysis } from '@/services/serverShareContent'
import { parseShareContentAnalysis } from '@/services/serverShareContent'

export interface ServerContentPreviewInput {
  result_token: string
  downloader_id?: string
  preview_token?: string
  offset?: number
  limit?: number
}
export interface ServerContentSelectionInput {
  result_token: string
  preview_token: string
  selected_entry_tokens: string[]
}
export interface ServerContentFile {
  token: string
  path: string
  name: string
  size: number
  isDirectory: boolean
  kind: 'directory' | 'video' | 'subtitle' | 'archive' | 'other'
  season?: number
  episodes: number[]
  workStatus: string
  reason?: string
  subtitles: Array<{
    language: string
    script?: string
    source: string
  }>
}
export interface ServerContentPreview {
  version: 1
  token: string
  status: string
  complete: boolean
  files: ServerContentFile[]
  analysis?: ShareContentAnalysis
  totalCount: number
  displayedCount: number
  nextOffset?: number
  totalSize: number
  checkedAt: string
  expiresAt: string
  errorCode?: string
  message: string
}
export interface ServerContentSelection {
  selectionToken: string
  previewToken: string
  entryTokens: string[]
  fileCount: number
  videoCount: number
  selectedBytes: number
}
const rec = (value: unknown): Record<string, unknown> => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
const clean = (value: unknown, max = 8192): value is string => typeof value === 'string' && value.length <= max && ![...value].some(char => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)
const count = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
const token = (value: unknown): value is string => clean(value) && !!value && !/\s/u.test(value)
const date = (value: unknown): value is string => clean(value, 64) && Number.isFinite(Date.parse(value))
const invalid = () => new Error('实际文件清单无效或已过期，请重新读取；不会自动改为下载整个资源。')
export function parseServerResourceContent(value: unknown, now = Date.now()): ServerContentPreview {
  const data = rec(value)
  if (data.version !== 1 || !clean(data.status, 64) || typeof data.complete !== 'boolean' || !Array.isArray(data.files) || data.files.length > 200
    || !count(data.total_count) || !count(data.displayed_count) || data.displayed_count !== data.files.length || data.displayed_count > data.total_count
    || !count(data.total_size) || !date(data.checked_at) || !date(data.expires_at) || Date.parse(data.expires_at) <= now || !clean(data.message, 2048)
    || (data.complete && !token(data.token)) || (data.next_offset !== undefined && (!count(data.next_offset) || data.next_offset > data.total_count))) {
    throw invalid()
  }
  const seen = new Set<string>()
  const files = data.files.map((raw): ServerContentFile => {
    const file = rec(raw)
    if (!clean(file.name, 4096) || !file.name || !clean(file.path) || !file.path || file.path.startsWith('/') || /^[A-Z]:/iu.test(file.path) || file.path.includes('\\') || file.path.split('/').some(part => part === '..' || part === '.')
      || !count(file.size) || typeof file.is_dir !== 'boolean' || !['directory', 'video', 'subtitle', 'archive', 'other'].includes(String(file.file_kind))
      || !Array.isArray(file.episodes) || file.episodes.length > 2000 || file.episodes.some(ep => !count(ep) || ep < 1 || ep > 100000)
      || (file.season !== undefined && (!count(file.season) || file.season > 200)) || !clean(file.work_status, 128)
      || (file.token !== undefined && !token(file.token)) || (file.token && seen.has(String(file.token)))) {
      throw invalid()
    }
    if (file.token)
      seen.add(file.token as string)
    if (file.subtitles !== undefined && (!Array.isArray(file.subtitles) || file.subtitles.length > 32))
      throw invalid()
    const subtitles = (file.subtitles as unknown[] ?? []).map((raw) => {
      const item = rec(raw)
      if (!clean(item.language, 64) || !clean(item.source, 128) || (item.script !== undefined && !clean(item.script, 64)))
        throw invalid()
      return { language: item.language, source: item.source, script: item.script as string | undefined }
    })
    return { token: file.token as string ?? '', path: file.path, name: file.name, size: file.size, isDirectory: file.is_dir, kind: file.file_kind as ServerContentFile['kind'], season: file.season as number | undefined, episodes: [...new Set(file.episodes as number[])].sort((a, b) => a - b), workStatus: file.work_status, reason: clean(file.unresolved_reason, 512) ? file.unresolved_reason : undefined, subtitles }
  })
  const analysis = parseShareContentAnalysis(data.analysis, now)
  if (data.analysis !== undefined && !analysis)
    throw invalid()
  return { version: 1, token: token(data.token) ? data.token : '', status: data.status, complete: data.complete, files, analysis, totalCount: data.total_count, displayedCount: data.displayed_count, nextOffset: data.next_offset as number | undefined, totalSize: data.total_size, checkedAt: data.checked_at, expiresAt: data.expires_at, message: data.message, errorCode: clean(data.error_code, 128) ? data.error_code : undefined }
}
export function parseServerContentSelection(value: unknown, expected: ServerContentSelectionInput): ServerContentSelection {
  const data = rec(value)
  if (!token(data.selection_token) || data.preview_token !== expected.preview_token || !Array.isArray(data.selected_entry_tokens)
    || data.selected_entry_tokens.length !== expected.selected_entry_tokens.length || data.selected_entry_tokens.some(value => !token(value) || !expected.selected_entry_tokens.includes(value))
    || new Set(data.selected_entry_tokens).size !== expected.selected_entry_tokens.length || !count(data.file_count) || data.file_count !== expected.selected_entry_tokens.length
    || !count(data.video_count) || !count(data.selected_bytes) || !count(data.total_size) || data.selected_bytes !== data.total_size) {
    throw invalid()
  }
  return { selectionToken: data.selection_token, previewToken: data.preview_token as string, entryTokens: [...data.selected_entry_tokens] as string[], fileCount: data.file_count, videoCount: data.video_count, selectedBytes: data.selected_bytes }
}
export async function previewServerResourceContent(source: ServerDataSource, input: ServerContentPreviewInput): Promise<ServerContentPreview> {
  return parseServerResourceContent(await source.previewDiscoveryContent(input))
}
export async function selectServerResourceContent(source: ServerDataSource, input: ServerContentSelectionInput): Promise<ServerContentSelection> {
  return parseServerContentSelection(await source.selectDiscoveryContent(input), input)
}

export function serverResourceAllowsWhole(item: ServerResourceItem | null, sourceKind?: string): boolean {
  return item?.contentInspection === true && (sourceKind === 'bt' || sourceKind === 'pt') && !item.cloudProvider && item.sourceKind !== '115_share' && item.sourceKind !== 'quark_share' && item.sourceKind !== 'external_link'
}
