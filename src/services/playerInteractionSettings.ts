import { getAppSetting, setAppSetting } from '@/services/appSettings'

export interface PlayerInteractionSettings {
  longPressPlaybackSpeed: number
  mobileEpisodeLayout: MobileEpisodeLayout
  androidBackgroundPlaybackEnabled: boolean
  videoOutput: PlayerVideoOutput
  hardwareDecoder: PlayerHardwareDecoder
  cacheMode: PlayerCacheMode
  demuxerMaxBytesMb: PlayerDemuxerCacheSize
  videoSync: PlayerVideoSync
  fsrMode: PlayerFsrMode
  fsrSharpness: number
  fsrDenoise: boolean
  fsrTarget: PlayerFsrTarget
}

export type PlayerVideoOutput = 'gpu-next' | 'gpu'
export type PlayerHardwareDecoder = 'auto-safe' | 'auto' | 'software'
export type PlayerCacheMode = 'auto' | 'enabled' | 'disabled'
export type PlayerDemuxerCacheSize = 64 | 128 | 256 | 512
export type PlayerVideoSync = 'audio' | 'display-resample' | 'display-vdrop'
export type PlayerFsrMode = 'off' | 'auto' | 'force'
export type PlayerFsrTarget = 'auto' | '1080p' | '1440p' | '2160p'
export type PlayerFsrSettings = Pick<PlayerInteractionSettings, 'fsrMode' | 'fsrSharpness' | 'fsrDenoise' | 'fsrTarget'>
export type MobileEpisodeLayout = 'vertical' | 'horizontal'

export const PLAYBACK_SPEED_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2] as const

const STORAGE_KEY = 'ohmycine-player-interaction-settings-v1'
const DEFAULT_SETTINGS: PlayerInteractionSettings = {
  longPressPlaybackSpeed: 2,
  mobileEpisodeLayout: 'horizontal',
  androidBackgroundPlaybackEnabled: true,
  videoOutput: 'gpu-next',
  hardwareDecoder: 'auto-safe',
  cacheMode: 'auto',
  demuxerMaxBytesMb: 64,
  videoSync: 'audio',
  fsrMode: 'auto',
  fsrSharpness: 35,
  fsrDenoise: true,
  fsrTarget: 'auto',
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

export function normalizePlayerInteractionSettings(settings: Partial<PlayerInteractionSettings> & { mobileQueueLayout?: MobileEpisodeLayout }): PlayerInteractionSettings {
  return {
    longPressPlaybackSpeed: normalizeLongPressPlaybackSpeed(settings.longPressPlaybackSpeed),
    mobileEpisodeLayout: settings.mobileEpisodeLayout === 'vertical' || settings.mobileQueueLayout === 'vertical' ? 'vertical' : 'horizontal',
    androidBackgroundPlaybackEnabled: settings.androidBackgroundPlaybackEnabled !== false,
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
    fsrMode: settings.fsrMode === 'off' || settings.fsrMode === 'force' ? settings.fsrMode : 'auto',
    fsrSharpness: normalizeFsrSharpness(settings.fsrSharpness),
    fsrDenoise: settings.fsrDenoise !== false,
    fsrTarget: settings.fsrTarget === '1080p' || settings.fsrTarget === '1440p' || settings.fsrTarget === '2160p'
      ? settings.fsrTarget
      : 'auto',
  }
}

function normalizeFsrSharpness(value: unknown): number {
  const number = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(number))
    return DEFAULT_SETTINGS.fsrSharpness
  return Math.round(Math.max(0, Math.min(100, number)))
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
