import { getAppSetting, setAppSetting } from '@/services/appSettings'

export interface PlayerInteractionSettings {
  longPressPlaybackSpeed: number
  videoOutput: PlayerVideoOutput
  hardwareDecoder: PlayerHardwareDecoder
  cacheMode: PlayerCacheMode
  demuxerMaxBytesMb: PlayerDemuxerCacheSize
  videoSync: PlayerVideoSync
}

export type PlayerVideoOutput = 'gpu-next' | 'gpu'
export type PlayerHardwareDecoder = 'auto-safe' | 'auto' | 'software'
export type PlayerCacheMode = 'auto' | 'enabled' | 'disabled'
export type PlayerDemuxerCacheSize = 64 | 128 | 256 | 512
export type PlayerVideoSync = 'audio' | 'display-resample' | 'display-vdrop'

export const PLAYBACK_SPEED_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2] as const

const STORAGE_KEY = 'ohmycine-player-interaction-settings-v1'
const DEFAULT_SETTINGS: PlayerInteractionSettings = {
  longPressPlaybackSpeed: 2,
  videoOutput: 'gpu-next',
  hardwareDecoder: 'auto-safe',
  cacheMode: 'auto',
  demuxerMaxBytesMb: 64,
  videoSync: 'audio',
}

export function loadPlayerInteractionSettings(): PlayerInteractionSettings {
  const raw = getAppSetting(STORAGE_KEY)
  if (!raw)
    return { ...DEFAULT_SETTINGS }

  try {
    const parsed = JSON.parse(raw) as Partial<PlayerInteractionSettings>
    return normalizePlayerInteractionSettings(parsed)
  }
  catch {
    return { ...DEFAULT_SETTINGS }
  }
}

export async function savePlayerInteractionSettings(settings: PlayerInteractionSettings): Promise<void> {
  await setAppSetting(STORAGE_KEY, JSON.stringify(normalizePlayerInteractionSettings(settings)))
}

export function normalizePlayerInteractionSettings(settings: Partial<PlayerInteractionSettings>): PlayerInteractionSettings {
  return {
    longPressPlaybackSpeed: normalizeLongPressPlaybackSpeed(settings.longPressPlaybackSpeed),
    videoOutput: settings.videoOutput === 'gpu' ? 'gpu' : 'gpu-next',
    hardwareDecoder: settings.hardwareDecoder === 'auto' || settings.hardwareDecoder === 'software'
      ? settings.hardwareDecoder
      : 'auto-safe',
    cacheMode: settings.cacheMode === 'enabled' || settings.cacheMode === 'disabled'
      ? settings.cacheMode
      : 'auto',
    demuxerMaxBytesMb: normalizeDemuxerCacheSize(settings.demuxerMaxBytesMb),
    videoSync: settings.videoSync === 'display-resample' || settings.videoSync === 'display-vdrop'
      ? settings.videoSync
      : 'audio',
  }
}

export function normalizeLongPressPlaybackSpeed(value: unknown): number {
  const number = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(number))
    return DEFAULT_SETTINGS.longPressPlaybackSpeed
  return Math.round(Math.max(1.25, Math.min(4, number)) * 4) / 4
}

function normalizeDemuxerCacheSize(value: unknown): PlayerDemuxerCacheSize {
  const number = Number(value)
  return number === 128 || number === 256 || number === 512 ? number : 64
}
