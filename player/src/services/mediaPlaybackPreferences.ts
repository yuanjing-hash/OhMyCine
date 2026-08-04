import type { VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import { invoke } from '@tauri-apps/api/core'

export interface MediaPlaybackPreferenceIdentity {
  sourceId: string
  mediaIdentity: string
}

export interface MediaTrackPreference {
  language?: string | null
  title?: string | null
  codec?: string | null
  channels?: number | null
  trackId?: number | null
}

export interface MediaSubtitlePreference {
  kind: 'off' | 'embedded' | 'cachedExternal'
  track?: MediaTrackPreference | null
  cachedPath?: string | null
}

export interface MediaPlaybackPreference extends MediaPlaybackPreferenceIdentity {
  subtitle?: MediaSubtitlePreference | null
  audio?: MediaTrackPreference | null
  subtitleDelay: number
  playbackSpeed: number
  videoBrightness: number
  aspectMode: VideoAspectMode
  fitMode: VideoFitMode
  updatedAt: number
}

export type MediaPlaybackPreferenceUpsert = Omit<MediaPlaybackPreference, 'updatedAt'>

export interface MediaCacheClearResult {
  playbackPreferencesDeleted: number
  rawScanCacheEntriesDeleted: number
}

export async function getMediaPlaybackPreference(identity: MediaPlaybackPreferenceIdentity): Promise<MediaPlaybackPreference | null> {
  try {
    return await invoke<MediaPlaybackPreference | null>('player_get_media_playback_preference', { identity })
  }
  catch {
    return null
  }
}

export async function saveMediaPlaybackPreference(preference: MediaPlaybackPreferenceUpsert): Promise<boolean> {
  try {
    await invoke<void>('player_upsert_media_playback_preference', { preference })
    return true
  }
  catch {
    return false
  }
}

export async function deleteMediaPlaybackPreferencesForSource(sourceId: string): Promise<void> {
  await invoke<number>('player_delete_media_playback_preferences_for_source', { sourceId })
}

export async function clearPlayerMediaCache(): Promise<MediaCacheClearResult> {
  return invoke<MediaCacheClearResult>('player_clear_media_cache')
}
