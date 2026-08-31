import type { DanmakuSettings } from './types'
import { getAppSetting, setAppSetting } from '@/services/appSettings'

export const DANMAKU_SETTINGS_KEY = 'ohmycine-danmaku-settings-v1'
export const DANMAKU_SETTINGS_CHANGED_EVENT = 'ohmycine:danmaku-settings-changed'
export const OFFICIAL_DANMAKU_BASE_URL = 'https://api.dandanplay.net'

export const DEFAULT_DANMAKU_SETTINGS: DanmakuSettings = {
  enabled: true,
  provider: 'official',
  customBaseUrl: '',
  opacity: 0.85,
  fontScale: 1,
  speed: 1,
  displayArea: 0.75,
  density: 0.7,
  showScroll: true,
  showTop: true,
  showBottom: true,
  bold: true,
  blockKeywords: [],
}

export function loadDanmakuSettings(): DanmakuSettings {
  const raw = getAppSetting(DANMAKU_SETTINGS_KEY)
  if (!raw)
    return { ...DEFAULT_DANMAKU_SETTINGS }
  try {
    return sanitizeDanmakuSettings(JSON.parse(raw) as Partial<DanmakuSettings>)
  }
  catch {
    return { ...DEFAULT_DANMAKU_SETTINGS }
  }
}

export async function saveDanmakuSettings(value: DanmakuSettings): Promise<DanmakuSettings> {
  const settings = sanitizeDanmakuSettings(value)
  await setAppSetting(DANMAKU_SETTINGS_KEY, JSON.stringify(settings))
  window.dispatchEvent(new CustomEvent(DANMAKU_SETTINGS_CHANGED_EVENT, { detail: settings }))
  return settings
}

export function danmakuBaseUrl(settings: DanmakuSettings): string {
  return settings.provider === 'official'
    ? OFFICIAL_DANMAKU_BASE_URL
    : settings.customBaseUrl.trim().replace(/\/+$/, '')
}

export function sanitizeDanmakuSettings(value: Partial<DanmakuSettings>): DanmakuSettings {
  const keywords = Array.isArray(value.blockKeywords)
    ? value.blockKeywords.filter(item => typeof item === 'string').map(item => item.trim()).filter(Boolean).slice(0, 100)
    : []
  const enabled = value.enabled !== false
  const showTop = value.showTop !== false
  const showBottom = value.showBottom !== false
  const requestedShowScroll = value.showScroll !== false
  const showScroll = requestedShowScroll || (enabled && !showTop && !showBottom)
  return {
    enabled,
    provider: value.provider === 'custom' ? 'custom' : 'official',
    customBaseUrl: typeof value.customBaseUrl === 'string' ? value.customBaseUrl.trim().slice(0, 2048) : '',
    opacity: clamp(value.opacity, 0.1, 1, DEFAULT_DANMAKU_SETTINGS.opacity),
    fontScale: clamp(value.fontScale, 0.7, 1.6, DEFAULT_DANMAKU_SETTINGS.fontScale),
    speed: clamp(value.speed, 0.5, 2, DEFAULT_DANMAKU_SETTINGS.speed),
    displayArea: nearest(value.displayArea, [0.25, 0.5, 0.75, 1], DEFAULT_DANMAKU_SETTINGS.displayArea),
    density: clamp(value.density, 0.2, 1, DEFAULT_DANMAKU_SETTINGS.density),
    showScroll,
    showTop,
    showBottom,
    bold: value.bold !== false,
    blockKeywords: [...new Set(keywords)],
  }
}

function clamp(value: unknown, min: number, max: number, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? Math.min(max, Math.max(min, value)) : fallback
}

function nearest(value: unknown, options: number[], fallback: number): number {
  return typeof value === 'number' && options.includes(value) ? value : fallback
}
