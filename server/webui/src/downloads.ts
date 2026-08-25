import type { DownloaderSummary, DownloadTaskSummary, MediaLibraryDetail, StorageSummary } from '@/types/api'

export type DownloadSourceMode = 'url' | 'torrent' | 'share'
export type DownloadManagementSection = 'active' | 'history' | 'create' | 'seeding' | 'downloaders'
export interface DownloaderTaskStats { active: number; total: number; downloadSpeed: number | null; uploadSpeed: number | null; averageProgress: number | null }
export interface DownloadRetryPresentationState { errorFingerprint: string; taskUpdatedAt: string; observedActive: boolean }
export type DownloadRetryPresentations = Record<string, DownloadRetryPresentationState>

export function formatBytes(value: number | null, suffix = ''): string {
  if (value === null || !Number.isFinite(value) || value < 0) return '未知'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value; let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount.toFixed(index === 0 ? 0 : 1)} ${units[index]}${suffix}`
}

export function formatProgress(value: number | null): string {
  return value === null || !Number.isFinite(value) ? '未知' : `${Math.max(0, Math.min(100, value)).toFixed(1)}%`
}

export function formatETA(value: number | null): string {
  if (value === null || !Number.isFinite(value) || value < 0) return '未知'
  if (value < 60) return `${Math.round(value)} 秒`
  if (value < 3600) return `${Math.ceil(value / 60)} 分钟`
  return `${Math.floor(value / 3600)} 小时 ${Math.ceil((value % 3600) / 60)} 分钟`
}

export function downloadStatusLabel(task: DownloadTaskSummary, retrying = false): string {
	if (retrying) return '正在重试…'
	const phases: Record<string, string> = { submitting: '提交下载器', metadata: '获取 metadata', classifying: '轻量刮削', waiting_user_action: '准备自动归入未识别', categorized: '已指派分类', downloading: '正式下载', verifying: '完成后复核' }
	if (task.job_status === 'running' && phases[task.phase]) return phases[task.phase]
  const labels: Record<string, string> = { queued: '排队中', running: '下载中', waiting_user_action: '准备自动处理', retry_wait: '等待重试', paused: '已暂停', completed: '已完成', failed: '失败', cancelled: '已取消' }
  return labels[task.job_status] ?? (task.job_status || '未知')
}

export function downloadStatusClass(task: DownloadTaskSummary, retrying = false): string {
  if (retrying) return 'status-chip status-chip--planned'
  if (task.job_status === 'completed') return 'status-chip status-chip--ready'
  if (task.job_status === 'failed' || task.job_status === 'cancelled') return 'status-chip status-chip--error'
  if (task.job_status === 'retry_wait' || task.job_status === 'waiting_user_action' || task.job_status === 'paused') return 'status-chip status-chip--warning'
  return 'status-chip status-chip--planned'
}

export function downloadErrorMessage(task: DownloadTaskSummary, retrying = false): string {
  return retrying || task.job_status === 'cancelled' ? '' : task.last_error_message
}

function retryErrorFingerprint(task: DownloadTaskSummary): string {
  return `${task.last_error_code}\u0000${task.last_error_message}`
}

export function beginDownloadRetry(task: DownloadTaskSummary): DownloadRetryPresentationState {
  return { errorFingerprint: retryErrorFingerprint(task), taskUpdatedAt: task.updated_at, observedActive: false }
}

export function reconcileDownloadRetries(states: DownloadRetryPresentations, tasks: DownloadTaskSummary[]): DownloadRetryPresentations {
  const byID = new Map(tasks.map(task => [task.id, task]))
  const next: DownloadRetryPresentations = {}
  for (const [taskID, previous] of Object.entries(states)) {
    const task = byID.get(taskID)
    if (!task || task.lifecycle_scope === 'history' || ['completed', 'cancelled'].includes(task.job_status)) continue
    if (task.job_status !== 'failed') {
      next[taskID] = { ...previous, observedActive: true }
      continue
    }
    const newFailure = previous.observedActive || task.updated_at !== previous.taskUpdatedAt || retryErrorFingerprint(task) !== previous.errorFingerprint
    if (!newFailure) next[taskID] = previous
  }
  return next
}

export function torrentToBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let offset = 0; offset < bytes.length; offset += chunk) binary += String.fromCharCode(...bytes.subarray(offset, offset + chunk))
  return btoa(binary)
}

export function summarizeDownloaderTasks(tasks: DownloadTaskSummary[], downloaderID: string): DownloaderTaskStats {
  const own = tasks.filter(task => task.downloader_id === downloaderID)
  const active = own.filter(task => task.job_status === 'running')
  const knownProgress = active.flatMap(task => task.progress === null ? [] : [task.progress])
  const knownDownload = active.flatMap(task => task.download_speed === null ? [] : [task.download_speed])
  const knownUpload = active.flatMap(task => task.upload_speed === null ? [] : [task.upload_speed])
  return {
    active: active.length,
    total: own.length,
    downloadSpeed: knownDownload.length ? knownDownload.reduce((sum, value) => sum + value, 0) : null,
    uploadSpeed: knownUpload.length ? knownUpload.reduce((sum, value) => sum + value, 0) : null,
    averageProgress: knownProgress.length ? knownProgress.reduce((sum, value) => sum + value, 0) / knownProgress.length : null,
  }
}

export function isDownloadHistoryTask(task: DownloadTaskSummary): boolean {
  return task.lifecycle_scope === 'history'
}

export function compatibleDownloadLibraries(
  libraries: MediaLibraryDetail[],
  storages: StorageSummary[],
  downloader: DownloaderSummary | null,
  requireIngest = false,
): MediaLibraryDetail[] {
  if (!downloader) return []
  const storageByID = new Map(storages.map(storage => [storage.id, storage]))
  const enabled = libraries.filter(library => library.enabled && storageByID.get(library.storage_id)?.enabled)
  if (downloader.type !== 'pan115_offline') {
    return enabled.filter(library => storageByID.get(library.storage_id)?.type === 'local')
  }
  const source = downloader.storage_id == null ? undefined : storageByID.get(downloader.storage_id)
  if (source?.type !== 'pan115' || source.connection_id == null) return []
  return enabled.filter(library => {
    const target = storageByID.get(library.storage_id)
    return target?.type === 'pan115'
      && target.connection_id === source.connection_id
      && library.transfer_mode !== 'symlink'
      && (!requireIngest || (library.ingest_enabled && library.ingest_downloader_id === downloader.id))
  })
}

export function formatSampleTime(value: string | null): string {
  if (!value) return '尚未检查'
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? '时间未知' : time.toLocaleString('zh-CN', { hour12: false })
}
