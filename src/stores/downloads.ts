import type { MediaItem } from '@/services/datasource/types'
import type { DownloadSettings, DownloadTask, OfflineItemSummary } from '@/services/downloads'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  cancelDownload,
  getDefaultDownloadDirectory,
  getDownloadSettings,
  listDownloadTasks,
  listenDownloadProgress,
  listenDownloadRemoved,
  listOfflineItems,
  pauseDownload,
  removeDownload,
  resumeDownload,
  retryDownload,
  setDefaultDownloadDirectory,
  updateDownloadSettings,
} from '@/services/downloads'
import { synchronizeOfflineAttachments } from '@/services/offlineAttachments'
import { useDataSourceStore } from '@/stores/datasource'

const DEFAULT_SETTINGS: DownloadSettings = {
  concurrentTasks: 1,
  segmentsPerTask: 1,
}

export interface OfflineBadge {
  state: 'complete' | 'partial'
  downloaded: number
  total?: number
  label: string
}

export const useDownloadStore = defineStore('downloads', () => {
  const tasks = ref<DownloadTask[]>([])
  const offlineItems = ref<OfflineItemSummary[]>([])
  const settings = ref<DownloadSettings>({ ...DEFAULT_SETTINGS })
  const defaultDirectory = ref('')
  const ready = ref(false)
  const error = ref('')
  let initialization: Promise<void> | null = null
  let unlistenProgress: (() => void) | undefined
  let unlistenRemoved: (() => void) | undefined
  const syncingAttachments = new Set<string>()

  const activeTasks = computed(() => tasks.value.filter(task => ['queued', 'interrupted', 'resolving', 'downloading', 'finalizing', 'paused'].includes(task.status)))
  const completedTasks = computed(() => tasks.value.filter(task => task.status === 'completed'))
  const failedTasks = computed(() => tasks.value.filter(task => task.status === 'failed'))
  const activeCount = computed(() => activeTasks.value.filter(task => task.status !== 'paused').length)
  const completedIdentityKeys = computed(() => new Set(offlineItems.value.map(item => identityKey(item.sourceId, item.itemId))))

  async function initialize() {
    if (initialization)
      return initialization
    initialization = (async () => {
      try {
        const [loadedTasks, loadedOfflineItems, loadedSettings, directory] = await Promise.allSettled([
          listDownloadTasks(),
          listOfflineItems(),
          getDownloadSettings(),
          getDefaultDownloadDirectory(),
        ])
        if (loadedTasks.status === 'fulfilled')
          tasks.value = loadedTasks.value
        if (loadedOfflineItems.status === 'fulfilled')
          offlineItems.value = loadedOfflineItems.value
        if (loadedSettings.status === 'fulfilled')
          settings.value = loadedSettings.value
        if (directory.status === 'fulfilled')
          defaultDirectory.value = directory.value

        // A damaged optional index or unavailable picker setting must not prevent the
        // queue listeners from attaching; otherwise active tasks would appear frozen.
        if (!unlistenProgress)
          unlistenProgress = await listenDownloadProgress(upsert).catch(() => undefined)
        if (!unlistenRemoved)
          unlistenRemoved = await listenDownloadRemoved(removeFromIndex).catch(() => undefined)
      }
      catch {
        // Browser preview has no Tauri download backend.
      }
      finally {
        ready.value = true
      }
    })()
    return initialization
  }

  function dispose() {
    unlistenProgress?.()
    unlistenRemoved?.()
    unlistenProgress = undefined
    unlistenRemoved = undefined
    initialization = null
  }

  function upsert(task: DownloadTask) {
    const index = tasks.value.findIndex(item => item.id === task.id)
    if (index >= 0)
      tasks.value[index] = task
    else
      tasks.value.unshift(task)
    if (task.status === 'completed') {
      void refreshOfflineItems().catch(() => undefined)
      if (task.attachmentState === 'pending')
        void retryAttachments(task).catch(() => undefined)
    }
  }

  function removeFromIndex(taskId: string) {
    tasks.value = tasks.value.filter(task => task.id !== taskId)
  }

  async function cancel(taskId: string) {
    // Optimistic removal matches the product meaning of cancel: it is not history.
    removeFromIndex(taskId)
    try {
      await cancelDownload(taskId)
    }
    catch (cause) {
      error.value = cause instanceof Error ? cause.message : '取消下载失败。'
      tasks.value = await listDownloadTasks().catch(() => tasks.value)
      throw cause
    }
  }

  async function pause(taskId: string) {
    await pauseDownload(taskId)
  }

  async function resume(taskId: string) {
    upsert(await resumeDownload(taskId))
  }

  async function retry(taskId: string) {
    upsert(await retryDownload(taskId))
  }

  async function retryAttachments(task: DownloadTask) {
    if (syncingAttachments.has(task.id))
      return
    syncingAttachments.add(task.id)
    try {
      const datasource = useDataSourceStore()
      datasource.loadConfigs()
      await datasource.syncManager()
      const source = datasource.getSource(task.sourceId)
      if (!source)
        throw new Error('原媒体来源不可用，暂时无法补全离线附件。')
      const result = await synchronizeOfflineAttachments(task, source)
      const current = tasks.value.find(item => item.id === task.id)
      if (current)
        upsert({ ...current, attachmentState: result.attachmentState })
      await refreshOfflineItems()
    }
    finally {
      syncingAttachments.delete(task.id)
    }
  }

  async function remove(taskId: string, deleteFile: boolean) {
    await removeDownload(taskId, deleteFile)
    removeFromIndex(taskId)
    if (deleteFile) {
      // The backend removes the offline row with the task id. Reflect that ownership
      // immediately so a successful file deletion cannot leave a stale badge merely
      // because the follow-up index read is temporarily unavailable.
      offlineItems.value = offlineItems.value.filter(item => item.id !== taskId)
      useDataSourceStore().pruneOfflineProjection()
      void refreshOfflineItems().catch(() => undefined)
    }
  }

  async function saveSettings(next: DownloadSettings) {
    settings.value = await updateDownloadSettings(next)
  }

  async function saveDirectory(directory: string) {
    defaultDirectory.value = await setDefaultDownloadDirectory(directory)
  }

  function badgeFor(item: Pick<MediaItem, 'sourceId' | 'id' | 'name' | 'type' | 'seriesName' | 'seasonNumber' | 'children'>): OfflineBadge | null {
    return deriveOfflineBadge(item, offlineItems.value, completedIdentityKeys.value)
  }

  async function refreshOfflineItems() {
    offlineItems.value = await listOfflineItems()
    useDataSourceStore().pruneOfflineProjection()
  }

  return {
    tasks,
    offlineItems,
    settings,
    defaultDirectory,
    ready,
    error,
    activeTasks,
    completedTasks,
    failedTasks,
    activeCount,
    initialize,
    dispose,
    cancel,
    pause,
    resume,
    retry,
    retryAttachments,
    remove,
    saveSettings,
    saveDirectory,
    badgeFor,
  }
})

export function deriveOfflineBadge(
  item: Pick<MediaItem, 'sourceId' | 'id' | 'name' | 'type' | 'seriesName' | 'seasonNumber' | 'children'>,
  offlineItems: readonly OfflineItemSummary[],
  completedKeys = new Set(offlineItems.map(entry => identityKey(entry.sourceId, entry.itemId))),
): OfflineBadge | null {
  if (completedKeys.has(identityKey(item.sourceId, item.id)))
    return { state: 'complete', downloaded: 1, total: 1, label: '已下载' }
  if (!['series', 'season', 'folder'].includes(item.type))
    return null
  const descendants = flattenPlayable(item.children ?? [])
  const downloaded = descendants.length
    ? descendants.filter(child => completedKeys.has(identityKey(child.sourceId, child.id))).length
    : offlineItems.filter((entry) => {
      if (entry.sourceId !== item.sourceId || !entry.seriesName)
        return false
      const seriesName = item.seriesName ?? (item.type === 'series' ? item.name : '')
      if (!seriesName || normalizedTitle(entry.seriesName) !== normalizedTitle(seriesName))
        return false
      return item.type !== 'season' || item.seasonNumber == null || entry.seasonNumber === item.seasonNumber
    }).length
  if (!downloaded)
    return null
  if (!descendants.length)
    return { state: 'partial', downloaded, label: `${downloaded} 集已下载` }
  return {
    state: downloaded === descendants.length ? 'complete' : 'partial',
    downloaded,
    total: descendants.length,
    label: downloaded === descendants.length ? '已全部下载' : `${downloaded}/${descendants.length}`,
  }
}

function identityKey(sourceId: string, itemId: string) {
  return `${sourceId}\u0000${itemId}`
}

function normalizedTitle(value: string) {
  return value.normalize('NFKC').trim().toLocaleLowerCase().replace(/[\s·._-]+/g, '')
}

function flattenPlayable(items: readonly MediaItem[]): MediaItem[] {
  return items.flatMap(item => ['series', 'season', 'folder'].includes(item.type) ? flattenPlayable(item.children ?? []) : [item])
}
