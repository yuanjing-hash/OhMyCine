import type { UnlistenFn } from '@tauri-apps/api/event'
import type { MediaItemActionTarget } from '@/services/mediaActions'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'

export type DownloadStatus = 'queued' | 'running' | 'cancelling' | 'paused' | 'cancelled' | 'failed' | 'completed'

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
  createdAt: number
  updatedAt: number
}

export async function getDefaultDownloadDirectory(): Promise<string> {
  return invoke<string>('player_download_default_directory')
}

export async function setDefaultDownloadDirectory(directory: string): Promise<string> {
  return invoke<string>('player_download_set_default_directory', { directory })
}

export async function listDownloadTasks(): Promise<DownloadTask[]> {
  return invoke<DownloadTask[]>('player_download_list')
}

export async function enqueueDownload(target: MediaItemActionTarget, destinationDirectory?: string): Promise<DownloadTask> {
  return invoke<DownloadTask>('player_download_enqueue', {
    request: {
      sourceId: target.sourceId,
      sourceType: target.sourceType,
      itemId: target.itemId,
      displayName: target.display.name,
      mediaType: target.mediaType,
      expectedBytes: undefined,
      destinationDirectory,
    },
  })
}

export async function cancelDownload(taskId: string): Promise<void> {
  await invoke('player_download_cancel', { taskId })
}

export async function retryDownload(taskId: string): Promise<DownloadTask> {
  return invoke<DownloadTask>('player_download_retry', { taskId })
}

export function listenDownloadProgress(handler: (task: DownloadTask) => void): Promise<UnlistenFn> {
  return listen<DownloadTask>('player-download:progress', event => handler(event.payload))
}
