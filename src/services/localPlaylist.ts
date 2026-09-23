import type { PlaybackQueueItemInput } from '@/services/playbackContext'
import { invoke } from '@tauri-apps/api/core'
import { savePlaybackMediaContext } from '@/services/playbackContext'

export const VIDEO_FILE_EXTENSIONS = [
  'mp4',
  'mkv',
  'avi',
  'mov',
  'webm',
  'm4v',
  'flv',
  'wmv',
  'ts',
  'm2ts',
  'rmvb',
  'mpg',
  'mpeg',
  '3gp',
  'ogv',
  'divx',
  'vob',
  'iso',
] as const

export interface LocalPlaylistFile {
  path: string
  name?: string
}

interface LocalFileEntry {
  name: string
  path: string
  isDir: boolean
}

const videoExtensions = new Set<string>(VIDEO_FILE_EXTENSIONS)
const fileNameCollator = new Intl.Collator('zh-Hans-CN', { numeric: true, sensitivity: 'base' })

export function localFileName(path: string): string {
  return path.split(/[\\/]/).pop() || '本地视频'
}

export function isLocalVideoFileName(name: string): boolean {
  const match = /\.([a-z0-9]{1,12})$/i.exec(name.trim())
  return Boolean(match && videoExtensions.has(match[1].toLowerCase()))
}

export function sortLocalPlaylistFiles(files: readonly LocalPlaylistFile[]): LocalPlaylistFile[] {
  return [...files].sort((left, right) => {
    const byName = fileNameCollator.compare(left.name || localFileName(left.path), right.name || localFileName(right.path))
    return byName || left.path.localeCompare(right.path)
  })
}

export async function listLocalFolderVideos(rootPath: string): Promise<LocalPlaylistFile[]> {
  const entries = await invoke<LocalFileEntry[]>('local_file_list', { rootPath, path: '/' })
  const videos = entries.filter(entry => !entry.isDir && isLocalVideoFileName(entry.name))
  const resolved: LocalPlaylistFile[] = []

  // The native command confines each file to the selected directory and returns a
  // canonical desktop path or a read-only Android document URI for playback.
  for (const entry of sortLocalPlaylistFiles(videos.map(item => ({ path: item.path, name: item.name })))) {
    try {
      const path = await invoke<string>('local_file_stream_path', { rootPath, path: entry.path })
      resolved.push({ path, name: entry.name })
    }
    catch {
      // A file can disappear while the platform picker is closing. Keep the
      // remaining playable files in this temporary queue.
    }
  }

  return resolved
}

export async function createLocalPlaylistContext(files: readonly LocalPlaylistFile[]): Promise<{ contextId: string, itemId: string }> {
  const unique = new Map<string, LocalPlaylistFile>()
  for (const file of files) {
    const path = file.path.trim()
    if (!path)
      continue
    const name = file.name?.trim() || localFileName(path)
    if (!isLocalVideoFileName(name))
      continue
    unique.set(path, { path, name })
  }

  const sorted = sortLocalPlaylistFiles([...unique.values()])
  if (sorted.length === 0)
    throw new Error('所选位置没有可播放的视频文件。')

  const items: PlaybackQueueItemInput[] = await Promise.all(sorted.map(async ({ path, name }) => {
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(path))
    const id = `local-file-${[...new Uint8Array(digest)].map(byte => byte.toString(16).padStart(2, '0')).join('')}`
    return {
      id,
      sourceId: 'local-file',
      name: name || '本地视频',
      path,
      type: 'file',
      historyIdentity: id,
    }
  }))
  const first = items[0]
  const contextId = savePlaybackMediaContext({
    sourceId: 'local-file',
    itemId: first.id,
    title: first.name,
    locator: { kind: 'localPath', path: first.path },
    currentItem: first,
    queue: { items, currentIndex: 0 },
  })
  return { contextId, itemId: first.id }
}
