import type { ServerDataSource } from '@/services/datasource/server'
import { ServerRequestError } from '@/services/datasource/server'

export interface ServerShareEntry { token: string, path: string, name: string, isDirectory: boolean, size: number }
export interface ServerSharePreview { token: string, entries: ServerShareEntry[], fileCount: number, totalSize: number, expiresAt: string }
export interface ServerShareSelection { previewToken: string, entryTokens: string[] }
export const maxServerShareSelectedFiles = 500

export async function previewServerShare(source: ServerDataSource, resultToken: string, downloaderId: string): Promise<ServerSharePreview> {
  try {
    return parseServerSharePreview(await source.previewDiscoveryShare({ result_token: resultToken, downloader_id: downloaderId }))
  }
  catch (reason) {
    if (reason instanceof ServerRequestError && (reason.status === 404 || reason.status === 405))
      throw new Error('当前 Server 不支持分享文件预览，请先升级 Server。')
    throw reason
  }
}

export function parseServerSharePreview(value: unknown): ServerSharePreview {
  if (!value || typeof value !== 'object')
    throw invalidPreview()
  const data = value as Record<string, unknown>
  if (!token(data.token) || !Array.isArray(data.entries) || data.entries.length > 2000 || !count(data.file_count) || !count(data.total_size)
    || typeof data.expires_at !== 'string' || !Number.isFinite(Date.parse(data.expires_at)) || Date.parse(data.expires_at) <= Date.now()) {
    throw invalidPreview()
  }
  const seen = new Set<string>()
  const entries = data.entries.map((raw): ServerShareEntry => {
    if (!raw || typeof raw !== 'object')
      throw invalidPreview()
    const entry = raw as Record<string, unknown>
    if (!token(entry.token) || seen.has(entry.token) || typeof entry.name !== 'string' || !entry.name || entry.name.length > 4096
      || typeof entry.path !== 'string' || !entry.path || entry.path.length > 8192 || hasControl(entry.path)
      || typeof entry.is_dir !== 'boolean' || !count(entry.size)) {
      throw invalidPreview()
    }
    seen.add(entry.token)
    return { token: entry.token, path: entry.path, name: entry.name, isDirectory: entry.is_dir, size: entry.size }
  })
  const files = entries.filter(entry => !entry.isDirectory)
  if (files.length !== data.file_count || files.reduce((sum, item) => sum + item.size, 0) !== data.total_size)
    throw invalidPreview()
  return { token: data.token, entries, fileCount: data.file_count, totalSize: data.total_size, expiresAt: data.expires_at }
}

export function shareEntryLeaves(preview: ServerSharePreview, entry: ServerShareEntry): ServerShareEntry[] {
  return entry.isDirectory ? preview.entries.filter(item => !item.isDirectory && item.path.startsWith(`${entry.path.replace(/\/$/u, '')}/`)) : [entry]
}
export function toggleServerShareEntry(preview: ServerSharePreview, selected: string[], entry: ServerShareEntry): string[] {
  const files = shareEntryLeaves(preview, entry)
  const next = new Set(selected)
  const remove = files.length > 0 && files.every(item => next.has(item.token))
  for (const file of files) {
    if (remove)
      next.delete(file.token)
    else next.add(file.token)
  }
  return [...next]
}
export function validateServerShareSelection(preview: ServerSharePreview | null, selected: string[], now = Date.now()): ServerShareSelection | null {
  if (!preview || Date.parse(preview.expiresAt) <= now || !selected.length || selected.length > maxServerShareSelectedFiles || new Set(selected).size !== selected.length)
    return null
  const files = new Set(preview.entries.filter(item => !item.isDirectory).map(item => item.token))
  if (selected.some(token => !files.has(token)))
    return null
  return { previewToken: preview.token, entryTokens: [...selected] }
}
function token(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= 8192 && !hasControl(value, true)
}
function count(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}
function hasControl(value: string, spaces = false) {
  return [...value].some(char => char.charCodeAt(0) <= (spaces ? 32 : 31) || char.charCodeAt(0) === 127)
}
function invalidPreview() {
  return new Error('分享目录无效或已过期，请重新读取；不会自动转存整个分享。')
}
