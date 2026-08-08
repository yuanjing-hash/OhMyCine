import type { DanmakuComment, DanmakuMatch, DanmakuSettings } from './types'
import { invoke } from '@tauri-apps/api/core'
import { parseDanmakuComments, parseDanmakuMatches } from './parser'
import { danmakuBaseUrl } from './settings'

interface NativeResult { status: number, data: unknown }

export async function matchDanmaku(
  settings: DanmakuSettings,
  fileName: string,
  duration: number,
): Promise<DanmakuMatch[]> {
  const safeName = safeMediaName(fileName)
  const result = await invoke<NativeResult>('danmaku_match', { request: {
    baseUrl: danmakuBaseUrl(settings),
    fileName: safeName,
    videoDuration: Number.isFinite(duration) ? Math.round(Math.max(0, duration)) : 0,
    official: settings.provider === 'official',
  } })
  return parseDanmakuMatches(result.data)
}

export async function fetchDanmakuComments(
  settings: DanmakuSettings,
  match: DanmakuMatch,
): Promise<DanmakuComment[]> {
  const result = await invoke<NativeResult>('danmaku_comments', { request: {
    baseUrl: danmakuBaseUrl(settings),
    episodeId: match.episodeId,
    official: settings.provider === 'official',
  } })
  return parseDanmakuComments(result.data, match.shift)
}

export function safeMediaName(value: string): string {
  const trimmed = value.trim()
  if (!trimmed || trimmed.includes('://'))
    return '未命名影片'
  const normalized = (trimmed.split(/[\\/]/).pop() ?? '').trim().replace(/\s+/g, ' ')
  if (!normalized)
    return '未命名影片'
  return normalized.slice(0, 240)
}
