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
  parentId?: string
  groupName?: string
  mediaSourceId?: string
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

export interface DownloadEnqueueOptions {
  destinationDirectory?: string
  parentId?: string
  groupName?: string
  mediaSourceId?: string
  expectedBytes?: number
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
    },
  })
}

export async function enqueueDownloadGroup(targets: readonly (MediaItemActionTarget & { mediaSourceId?: string, expectedBytes?: number })[], groupName: string, destinationDirectory?: string): Promise<DownloadTask[]> {
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
        expectedBytes: target.expectedBytes,
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

export function listenDownloadProgress(handler: (task: DownloadTask) => void): Promise<UnlistenFn> {
  return listen<DownloadTask>('player-download:progress', event => handler(event.payload))
}
