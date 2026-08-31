import type { UnlistenFn } from '@tauri-apps/api/event'
import type { MediaItemActionTarget } from '@/services/mediaActions'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'

export type DownloadStatus = 'queued' | 'interrupted' | 'resolving' | 'downloading' | 'finalizing' | 'paused' | 'failed' | 'completed' | 'running' | 'cancelling' | 'cancelled'

export interface DownloadSettings {
  concurrentTasks: number
  segmentsPerTask: number
  globalSpeedLimitBytesPerSecond?: number
}

export interface DownloadOnlineIdentity {
  libraryId: string
  workId: string
  segmentId: string
  versionId: string
}

export interface OfflineItemSummary {
  id: string
  sourceId: string
  itemId: string
  mediaSourceId?: string
  variantId?: string
  displayName: string
  mediaType: string
  videoBytes: number
  completedAt: number
  attachmentState: 'pending' | 'syncing' | 'complete' | 'partial'
  seriesName?: string
  seasonNumber?: number
  episodeNumber?: number
}

export interface OfflineDetailSnapshot {
  name: string
  originalTitle?: string
  mediaType: 'movie' | 'series' | 'season' | 'episode' | 'file'
  year?: number
  rating?: number
  overview?: string
  tagline?: string
  duration?: number
  genres: string[]
  directors: string[]
  writers: string[]
  cast: string[]
  imdbId?: string
  tmdbId?: number
  seriesName?: string
  seasonNumber?: number
  episodeNumber?: number
}

export interface OfflineDetailRecord extends OfflineItemSummary {
  snapshot: OfflineDetailSnapshot
  assets: Array<{ id: string, kind: OfflineAttachmentInput['kind'] }>
}

export interface OfflineAttachmentInput {
  kind: 'poster' | 'backdrop' | 'still' | 'subtitle' | 'danmaku'
  dataUrl?: string
  remoteUrl?: string
  headers?: Record<string, string>
  extension?: string
}

export interface OfflineAttachmentSyncResult {
  attachmentState: 'complete' | 'partial'
  saved: number
  failed: number
}

export interface OfflineAssetContent {
  kind: OfflineAttachmentInput['kind']
  dataUrl?: string
  localPath?: string
  text?: string
}

export interface DownloadTask {
  id: string
  sourceId: string
  sourceType: string
  itemId: string
  displayName: string
  mediaType: string
  destinationDirectory: string
  destinationName: string
  status: DownloadStatus
  bytesDownloaded: number
  totalBytes?: number
  retryCount: number
  errorMessage?: string
  parentId?: string
  groupName?: string
  mediaSourceId?: string
  variantId?: string
  libraryId?: string
  onlineIdentity?: DownloadOnlineIdentity
  speedBytesPerSecond: number
  etaSeconds?: number
  activeSegments: number
  attachmentState: 'none' | 'pending' | 'syncing' | 'complete' | 'partial'
  createdAt: number
  updatedAt: number
}

export async function getDefaultDownloadDirectory(): Promise<string> {
  return invoke<string>('player_download_default_directory')
}

export async function setDefaultDownloadDirectory(directory: string): Promise<string> {
  return invoke<string>('player_download_set_default_directory', { directory })
}

export async function pickAndroidDownloadDirectory(persistent: boolean): Promise<string | undefined> {
  const selected = await invoke<string | null>('player_download_pick_directory', { persistent })
  return selected ?? undefined
}

export async function listDownloadTasks(): Promise<DownloadTask[]> {
  return invoke<DownloadTask[]>('player_download_list')
}

export async function listOfflineItems(): Promise<OfflineItemSummary[]> {
  return invoke<OfflineItemSummary[]>('player_download_offline_list')
}

export async function getDownloadSettings(): Promise<DownloadSettings> {
  return invoke<DownloadSettings>('player_download_settings')
}

export async function updateDownloadSettings(value: DownloadSettings): Promise<DownloadSettings> {
  return invoke<DownloadSettings>('player_download_update_settings', { value })
}

export interface DownloadEnqueueOptions {
  destinationDirectory?: string
  parentId?: string
  groupName?: string
  mediaSourceId?: string
  expectedBytes?: number
  variantId?: string
  libraryId?: string
  onlineIdentity?: DownloadOnlineIdentity
  detailSnapshot?: OfflineDetailSnapshot
}

export async function enqueueDownload(target: MediaItemActionTarget, options: DownloadEnqueueOptions = {}): Promise<DownloadTask> {
  return invoke<DownloadTask>('player_download_enqueue', {
    request: {
      sourceId: target.sourceId,
      sourceType: target.sourceType,
      itemId: target.itemId,
      displayName: target.display.name,
      mediaType: target.mediaType,
      expectedBytes: options.expectedBytes,
      destinationDirectory: options.destinationDirectory,
      parentId: options.parentId,
      groupName: options.groupName,
      mediaSourceId: options.mediaSourceId,
      variantId: options.variantId,
      libraryId: options.libraryId ?? target.libraryId,
      onlineIdentity: options.onlineIdentity,
      detailSnapshot: options.detailSnapshot,
    },
  })
}

export async function enqueueDownloadGroup(targets: readonly (MediaItemActionTarget & {
  mediaSourceId?: string
  variantId?: string
  expectedBytes?: number
  onlineIdentity?: DownloadOnlineIdentity
  detailSnapshot?: OfflineDetailSnapshot
})[], groupName: string, destinationDirectory?: string): Promise<DownloadTask[]> {
  if (targets.length === 0)
    return []
  const parentId = randomAggregateId()
  const tasks: DownloadTask[] = []
  try {
    for (const target of targets) {
      tasks.push(await enqueueDownload(target, {
        destinationDirectory,
        parentId,
        groupName,
        mediaSourceId: target.mediaSourceId,
        variantId: target.variantId,
        libraryId: target.libraryId,
        onlineIdentity: target.onlineIdentity,
        expectedBytes: target.expectedBytes,
        detailSnapshot: target.detailSnapshot,
      }))
    }
    return tasks
  }
  catch (error) {
    await Promise.allSettled(tasks.map(task => cancelDownload(task.id)))
    throw error
  }
}

function randomAggregateId(): string {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  return [...bytes].map(value => value.toString(16).padStart(2, '0')).join('')
}

export async function cancelDownload(taskId: string): Promise<void> {
  await invoke('player_download_cancel', { taskId })
}

export async function retryDownload(taskId: string): Promise<DownloadTask> {
  return invoke<DownloadTask>('player_download_retry', { taskId })
}

export async function pauseDownload(taskId: string): Promise<void> {
  await invoke('player_download_pause', { taskId })
}

export async function resumeDownload(taskId: string): Promise<DownloadTask> {
  return invoke<DownloadTask>('player_download_resume', { taskId })
}

export async function removeDownload(taskId: string, deleteFile: boolean): Promise<void> {
  await invoke('player_download_remove', { taskId, deleteFile })
}

export async function resolveCompletedDownload(input: {
  sourceId: string
  itemId: string
  mediaSourceId?: string
  variantId?: string
}): Promise<string | undefined> {
  return (await invoke<string | null>('player_download_resolve_local', input)) ?? undefined
}

export async function getOfflineDetail(sourceId: string, itemId: string, offlineId?: string): Promise<OfflineDetailRecord | undefined> {
  return (await invoke<OfflineDetailRecord | null>('player_download_offline_detail', { sourceId, itemId, offlineId })) ?? undefined
}

export async function syncOfflineAttachments(taskId: string, attachments: OfflineAttachmentInput[], failedKinds: string[] = []): Promise<OfflineAttachmentSyncResult> {
  return invoke<OfflineAttachmentSyncResult>('player_download_sync_attachments', {
    request: { taskId, attachments, failedKinds },
  })
}

export async function resolveOfflineAsset(assetId: string): Promise<OfflineAssetContent | undefined> {
  return (await invoke<OfflineAssetContent | null>('player_download_offline_asset', { assetId })) ?? undefined
}

export function listenDownloadProgress(handler: (task: DownloadTask) => void): Promise<UnlistenFn> {
  return listen<DownloadTask>('player-download:progress', event => handler(event.payload))
}

export function listenDownloadRemoved(handler: (taskId: string) => void): Promise<UnlistenFn> {
  return listen<{ taskId: string }>('player-download:removed', event => handler(event.payload.taskId))
}
