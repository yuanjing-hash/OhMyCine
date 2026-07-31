import { getAppSetting, setAppSetting } from '@/services/appSettings'

export interface PlayerInteractionSettings {
  longPressPlaybackSpeed: number
}

const STORAGE_KEY = 'ohmycine-player-interaction-settings-v1'
const DEFAULT_SETTINGS: PlayerInteractionSettings = {
  longPressPlaybackSpeed: 2,
}

export function loadPlayerInteractionSettings(): PlayerInteractionSettings {
  const raw = getAppSetting(STORAGE_KEY)
  if (!raw)
    return { ...DEFAULT_SETTINGS }

  try {
    const parsed = JSON.parse(raw) as Partial<PlayerInteractionSettings>
    return {
      longPressPlaybackSpeed: normalizeLongPressPlaybackSpeed(parsed.longPressPlaybackSpeed),
    }
  }
  catch {
    return { ...DEFAULT_SETTINGS }
  }
}

export async function savePlayerInteractionSettings(settings: PlayerInteractionSettings): Promise<void> {
  await setAppSetting(STORAGE_KEY, JSON.stringify({
    longPressPlaybackSpeed: normalizeLongPressPlaybackSpeed(settings.longPressPlaybackSpeed),
  }))
}

export function normalizeLongPressPlaybackSpeed(value: unknown): number {
  const number = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(number))
    return DEFAULT_SETTINGS.longPressPlaybackSpeed
  return Math.round(Math.max(1.25, Math.min(4, number)) * 4) / 4
}
