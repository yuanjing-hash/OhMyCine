import type { MediaItem } from '@/services/datasource/types'
import { invoke } from '@tauri-apps/api/core'
import { redactSensitiveText } from '@/services/datasource/errors'

export type PlaybackProgressSource = 'local'
export const PLAYED_STATE_CHANGED_EVENT = 'ohmycine:played-state-changed'

export interface PlaybackProgressIdentity {
  sourceId: string
  mediaIdentity: string
}

export interface PlaybackProgressUpsert extends PlaybackProgressIdentity {
  libraryId?: string
  itemId?: string
  title: string
  streamIdentity?: string
  mediaType?: MediaItem['type']
  posterUrl?: string
  backdropUrl?: string
  titleLogoUrl?: string
  displaySubtitle?: string
  episodeStillUrl?: string
  position: number
  duration?: number
  completed?: boolean
}

export interface PlaybackHistoryEntry extends PlaybackProgressIdentity {
  libraryId?: string | null
  itemId?: string | null
  title: string
  streamIdentity?: string | null
  mediaType?: MediaItem['type'] | null
  posterUrl?: string | null
  backdropUrl?: string | null
  titleLogoUrl?: string | null
  displaySubtitle?: string | null
  episodeStillUrl?: string | null
  position: number
  duration?: number | null
  progress?: number | null
  updatedAt: number
  completed: boolean
  progressSource: PlaybackProgressSource
}

export interface PlaybackHistoryPage {
  list: PlaybackHistoryEntry[]
  total: number
  page: number
  pageSize: number
  hasMore: boolean
}

const RESUME_MIN_POSITION = 30
const COMPLETED_REMAINING_SECONDS = 90
const COMPLETED_PROGRESS_RATIO = 0.92
const SENSITIVE_URL_PATTERN = /[?&](?:api_key|apikey|access_key|accesskeyid|access_token|token|x-emby-token|expires|x-amz-expires|signature|sig|sign|password|passwd|pwd|security-token|x-amz-signature|x-amz-credential|x-amz-security-token|ossaccesskeyid|awsaccesskeyid)=/i

export async function savePlaybackProgress(input: PlaybackProgressUpsert): Promise<PlaybackHistoryEntry | null> {
  const progress = sanitizeProgressInput(input)
  if (!progress)
    return null

  try {
    const saved = await invoke<PlaybackHistoryEntry>('player_upsert_playback_progress', { progress })
    notifyPlaybackHistoryChanged('progress-saved')
    return saved
  }
  catch {
    return null
  }
}

export async function getPlaybackProgress(identity: PlaybackProgressIdentity): Promise<PlaybackHistoryEntry | null> {
  if (!isValidIdentity(identity))
    return null

  try {
    return await invoke<PlaybackHistoryEntry | null>('player_get_playback_progress', { identity })
  }
  catch {
    return null
  }
}

export async function listLocalContinueWatching(limit = 20): Promise<PlaybackHistoryEntry[]> {
  try {
    return await invoke<PlaybackHistoryEntry[]>('player_list_continue_watching', { limit })
  }
  catch {
    return []
  }
}

export async function listPlaybackHistoryPage(page = 1, pageSize = 24): Promise<PlaybackHistoryPage> {
  const safePage = Number.isInteger(page) ? Math.max(1, Math.min(100_000, page)) : 1
  const safePageSize = Number.isInteger(pageSize) ? Math.max(1, Math.min(100, pageSize)) : 24
  try {
    return await invoke<PlaybackHistoryPage>('player_list_playback_history', { page: safePage, pageSize: safePageSize })
  }
  catch {
    return { list: [], total: 0, page: safePage, pageSize: safePageSize, hasMore: false }
  }
}

export async function setPlaybackCompleted(identity: PlaybackProgressIdentity, completed: boolean): Promise<boolean> {
  if (!isValidIdentity(identity))
    return false
  const changed = await invoke<boolean>('player_set_playback_completed', { identity, completed })
  if (changed)
    notifyPlaybackHistoryChanged('completion-changed')
  return changed
}

export async function removeContinueWatching(identity: PlaybackProgressIdentity): Promise<boolean> {
  if (!isValidIdentity(identity))
    return false
  const changed = await invoke<boolean>('player_remove_continue_watching', { identity })
  if (changed)
    notifyPlaybackHistoryChanged('continue-removed')
  return changed
}

export async function getPlaybackCompletionBatch(identities: readonly PlaybackProgressIdentity[]): Promise<PlaybackHistoryEntry[]> {
  const valid = identities.filter(isValidIdentity).slice(0, 500)
  if (!valid.length)
    return []
  try {
    return await invoke<PlaybackHistoryEntry[]>('player_get_playback_completion_batch', { identities: valid })
  }
  catch {
    return []
  }
}

export function areAllKnownPlayableChildrenCompleted(items: readonly MediaItem[], completedKeys: ReadonlySet<string>): boolean {
  const playable = flattenPlayableChildren(items)
  return playable.length > 0 && playable.every(item => item.played === true || completedKeys.has(playbackCompletionKeyForMediaItem(item)))
}

export function playbackCompletionKey(sourceId: string, mediaIdentity: string): string {
  return `${sourceId.trim()}:${mediaIdentity.trim()}`
}

export function playbackProgressIdentityForMediaItem(item: MediaItem): PlaybackProgressIdentity {
  return {
    sourceId: item.offlineOriginSourceId ?? item.sourceId,
    mediaIdentity: item.historyIdentity ?? item.offlineOriginItemId ?? item.id,
  }
}

export function playbackCompletionKeyForMediaItem(item: MediaItem): string {
  const identity = playbackProgressIdentityForMediaItem(item)
  return playbackCompletionKey(identity.sourceId, identity.mediaIdentity)
}

function flattenPlayableChildren(items: readonly MediaItem[]): MediaItem[] {
  return items.flatMap((item) => {
    if (item.type === 'series' || item.type === 'season' || item.type === 'folder')
      return flattenPlayableChildren(item.children ?? [])
    return [item]
  })
}

export async function deletePlaybackHistoryForSource(sourceId: string): Promise<boolean> {
  const normalizedSourceId = sourceId.trim()
  if (!normalizedSourceId)
    return false

  try {
    const changed = await invoke<number>('player_delete_playback_history_for_source', { sourceId: normalizedSourceId })
    if (changed > 0)
      notifyPlaybackHistoryChanged('source-history-deleted')
    return true
  }
  catch {
    return false
  }
}

function notifyPlaybackHistoryChanged(reason: string): void {
  window.dispatchEvent(new CustomEvent(PLAYED_STATE_CHANGED_EVENT, { detail: { source: 'player-history', reason } }))
}

export function shouldResumePlayback(entry: PlaybackHistoryEntry | null | undefined): entry is PlaybackHistoryEntry {
  if (!entry || entry.completed || !Number.isFinite(entry.position))
    return false

  if (entry.position < RESUME_MIN_POSITION)
    return false

  if (typeof entry.duration === 'number' && Number.isFinite(entry.duration) && entry.duration > 0)
    return !isCompletedPosition(entry.position, entry.duration)

  return true
}

export function isCompletedPosition(position: number, duration: number | undefined): boolean {
  if (!Number.isFinite(position) || position < 0 || !duration || !Number.isFinite(duration) || duration <= RESUME_MIN_POSITION)
    return false

  return position >= duration * COMPLETED_PROGRESS_RATIO || duration - position <= COMPLETED_REMAINING_SECONDS
}

export function toContinueWatchingMediaItem(entry: PlaybackHistoryEntry): MediaItem {
  return {
    id: entry.itemId ?? entry.mediaIdentity,
    sourceId: entry.sourceId,
    libraryId: entry.libraryId ?? undefined,
    name: entry.title,
    type: entry.mediaType ?? 'file',
    posterUrl: safeArtworkUrl(entry.posterUrl),
    backdropUrl: safeArtworkUrl(entry.backdropUrl),
    titleLogoUrl: safeArtworkUrl(entry.titleLogoUrl),
    historyIdentity: entry.mediaIdentity,
    displaySubtitle: nonEmpty(entry.displaySubtitle),
    episodeStillUrl: safeArtworkUrl(entry.episodeStillUrl),
    cardLayout: entry.mediaType === 'episode' ? 'poster' : undefined,
    duration: entry.duration ?? undefined,
    path: entry.streamIdentity ?? entry.mediaIdentity,
    resumePosition: entry.position,
    progress: entry.progress ?? undefined,
    progressSource: entry.progressSource,
  }
}

export function createSafeStreamIdentity(sourceId: string, itemId: string | undefined, path: string): string {
  if (sourceId === 'local-file')
    return redactSensitiveText(path)

  if (sourceId && itemId)
    return `source:${sourceId}:${itemId}`

  return redactSensitiveText(path)
}

function sanitizeProgressInput(input: PlaybackProgressUpsert): PlaybackProgressUpsert | null {
  if (!isValidIdentity(input) || !Number.isFinite(input.position) || input.position < 0)
    return null

  const duration = typeof input.duration === 'number' && Number.isFinite(input.duration) && input.duration >= 0
    ? input.duration
    : undefined

  return {
    sourceId: input.sourceId.trim(),
    libraryId: nonEmpty(input.libraryId),
    itemId: nonEmpty(input.itemId),
    mediaIdentity: sanitizeMediaIdentity(input.mediaIdentity),
    title: nonEmpty(input.title) ?? '未命名影片',
    streamIdentity: sanitizeStreamIdentity(input.streamIdentity),
    mediaType: input.mediaType,
    posterUrl: safeArtworkUrl(input.posterUrl),
    backdropUrl: safeArtworkUrl(input.backdropUrl),
    titleLogoUrl: safeArtworkUrl(input.titleLogoUrl),
    displaySubtitle: nonEmpty(input.displaySubtitle),
    episodeStillUrl: safeArtworkUrl(input.episodeStillUrl),
    position: input.position,
    duration,
    completed: input.completed ?? isCompletedPosition(input.position, duration),
  }
}

function isValidIdentity(identity: PlaybackProgressIdentity): boolean {
  return Boolean(nonEmpty(identity.sourceId) && nonEmpty(identity.mediaIdentity))
}

function nonEmpty(value: string | null | undefined): string | undefined {
  const trimmed = value?.trim()
  return trimmed || undefined
}

function sanitizeMediaIdentity(value: string): string {
  return redactSensitiveText(value.trim())
}

function sanitizeStreamIdentity(value: string | undefined): string | undefined {
  const trimmed = nonEmpty(value)
  if (!trimmed)
    return undefined

  return redactSensitiveText(trimmed)
}

function safeArtworkUrl(value: string | null | undefined): string | undefined {
  const trimmed = nonEmpty(value)
  if (!trimmed || SENSITIVE_URL_PATTERN.test(trimmed))
    return undefined

  return /^https?:\/\//i.test(trimmed) ? trimmed : undefined
}
