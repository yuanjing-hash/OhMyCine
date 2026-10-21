import type { LocalSubtitleDownloadResult, SubtitleCacheOwner } from './types'
import { invoke } from '@tauri-apps/api/core'

/** The native boundary lists only existing files for this exact source/media. */
export async function listCachedSubtitles(owner: SubtitleCacheOwner): Promise<LocalSubtitleDownloadResult[]> {
  return invoke<LocalSubtitleDownloadResult[]>('subtitle_list_cached', { owner })
}

/** Enrich an already managed file; native validation never accepts another owner. */
export async function rememberCachedSubtitle(owner: SubtitleCacheOwner, subtitle: LocalSubtitleDownloadResult): Promise<LocalSubtitleDownloadResult> {
  return invoke<LocalSubtitleDownloadResult>('subtitle_record_cached', { request: { owner, subtitle } })
}
