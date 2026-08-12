import type { DanmakuComment, DanmakuMatch, DanmakuMatchResponse, DanmakuSearchResponse, DanmakuSettings } from './types'
import { invoke } from '@tauri-apps/api/core'
import { parseDanmakuComments, parseDanmakuMatches, parseDanmakuSearch } from './parser'
import { danmakuBaseUrl } from './settings'

interface NativeResult { status: number, data: unknown }

export async function matchDanmaku(
  settings: DanmakuSettings,
  fileName: string,
  duration: number,
): Promise<DanmakuMatchResponse> {
  const safeName = safeMediaName(fileName)
  const result = await invoke<NativeResult>('danmaku_match', { request: {
    baseUrl: danmakuBaseUrl(settings),
    fileName: safeName,
    videoDuration: Number.isFinite(duration) ? Math.round(Math.max(0, duration)) : 0,
    official: settings.provider === 'official',
  } })
  return parseDanmakuMatches(result.data)
}

export async function searchDanmaku(
  settings: DanmakuSettings,
  anime: string,
  episode?: string,
): Promise<DanmakuSearchResponse> {
  const keyword = safeSearchKeyword(anime)
  const result = await invoke<NativeResult>('danmaku_search', { request: {
    baseUrl: danmakuBaseUrl(settings),
    anime: keyword,
    episode: episode?.trim() || null,
    official: settings.provider === 'official',
  } })
  return parseDanmakuSearch(result.data)
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
  return normalized.replace(/\.(?:3g2|3gp|asf|avi|flv|m2ts|m4v|mkv|mov|mp4|mpeg|mpg|mts|ogm|ogv|rm|rmvb|ts|vob|webm|wmv)$/i, '').trim().slice(0, 240) || '未命名影片'
}

export function safeSearchKeyword(value: string): string {
  const keyword = value.replace(/\s+/g, ' ').trim()
  const containsControlCharacter = [...keyword].some((character) => {
    const code = character.charCodeAt(0)
    return code <= 0x1F || code === 0x7F
  })
  if ([...keyword].length < 2 || keyword.length > 240 || keyword.includes('://') || keyword.includes('\\') || keyword.startsWith('/') || /^[A-Z]:/i.test(keyword) || containsControlCharacter)
    throw new Error('弹幕搜索关键词至少需要两个字符。')
  return keyword.slice(0, 160)
}

export function inferDanmakuEpisode(value: string): string {
  const safeName = safeMediaName(value)
  const match = safeName.match(/(?:S\d{1,3}[ ._-]*)?EP?[ ._-]*0*(\d{1,4})\b/i)
    ?? safeName.match(/第\s*0*(\d{1,4})\s*[集话]/)
  return match?.[1] || ''
}
