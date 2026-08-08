import type { DanmakuComment, DanmakuMatch, DanmakuMode } from './types'

export function parseDanmakuMatches(value: unknown): DanmakuMatch[] {
  if (!value || typeof value !== 'object')
    return []
  const root = value as Record<string, unknown>
  if (root.success === false)
    throw new Error(safeMessage(root.errorMessage, '弹幕匹配失败。'))
  const matches = Array.isArray(root.matches) ? root.matches : []
  return matches.slice(0, 100).flatMap((entry) => {
    if (!entry || typeof entry !== 'object')
      return []
    const item = entry as Record<string, unknown>
    const episodeId = safePositiveInteger(item.episodeId)
    if (!episodeId)
      return []
    return [{
      episodeId,
      animeId: safePositiveInteger(item.animeId) ?? 0,
      animeTitle: safeText(item.animeTitle),
      episodeTitle: safeText(item.episodeTitle),
      shift: safeFinite(item.shift) ?? 0,
    }]
  })
}

export function parseDanmakuComments(value: unknown, shift = 0): DanmakuComment[] {
  if (!value || typeof value !== 'object')
    return []
  const root = value as Record<string, unknown>
  if (root.success === false)
    throw new Error(safeMessage(root.errorMessage, '弹幕加载失败。'))
  const comments = Array.isArray(root.comments) ? root.comments : []
  return comments.slice(0, 50_000).flatMap((entry, index) => {
    if (!entry || typeof entry !== 'object')
      return []
    const item = entry as Record<string, unknown>
    const text = safeText(item.m).trim()
    const parts = typeof item.p === 'string' ? item.p.split(',') : []
    const time = Number.parseFloat(parts[0] ?? '') + shift
    const mode = parseMode(parts[1])
    const color = parseColor(parts[2])
    if (!text || text.length > 500 || !Number.isFinite(time) || time < 0 || !mode)
      return []
    return [{ id: String(item.cid ?? index), time, mode, color, text }]
  }).sort((a, b) => a.time - b.time)
}

function parseMode(value: string | undefined): DanmakuMode | null {
  if (value === '1')
    return 'scroll'
  if (value === '4')
    return 'bottom'
  if (value === '5')
    return 'top'
  return null
}

function parseColor(value: string | undefined): string {
  const color = Number.parseInt(value ?? '', 10)
  return Number.isInteger(color) && color >= 0 && color <= 0xFFFFFF
    ? `#${color.toString(16).padStart(6, '0')}`
    : '#ffffff'
}

function safePositiveInteger(value: unknown): number | null {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
}

function safeFinite(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function safeText(value: unknown): string {
  return typeof value === 'string' ? value.slice(0, 500) : ''
}

function safeMessage(value: unknown, fallback: string): string {
  const message = safeText(value).trim()
  return message || fallback
}
