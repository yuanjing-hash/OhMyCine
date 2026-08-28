import type { DownloadTaskSummary } from '@/types/api'

export type DownloadSourceMode = 'url' | 'torrent' | 'share'
export type DownloadManagementSection = 'active' | 'history' | 'create' | 'seeding' | 'downloaders'
export interface DownloaderTaskStats { active: number; total: number; downloadSpeed: number | null; uploadSpeed: number | null; averageProgress: number | null }
export interface DownloadRetryPresentationState { errorFingerprint: string; taskUpdatedAt: string; observedActive: boolean }
export type DownloadRetryPresentations = Record<string, DownloadRetryPresentationState>

const activeProviderStatuses = new Set(['allocating', 'checkingdl', 'checkingresumedata', 'downloading', 'forceddl', 'metadl', 'queueddl', 'stalleddl'])

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
	if (task.job_status === 'running' && normalizedProviderStatus(task.provider_status) === 'stalleddl') return '等待连接/暂无速度'
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
  if (retrying || task.job_status === 'cancelled') return ''
  // Preclassification deliberately keeps a safe TMDB warning visible while
  // the provider continues downloading. Only a stale terminal downloader
  // failure should be suppressed by authoritative active telemetry.
  if (task.scrape_status === 'fallback_unrecognized' && task.last_error_code.startsWith('tmdb_')) return task.last_error_message
  if ((task.job_status === 'queued' || task.job_status === 'running') && isActiveProviderStatus(task.provider_status)) return ''
  return task.last_error_message
}

function normalizedProviderStatus(value: string): string {
  return (value || '').trim().toLowerCase().replace(/[\s_-]+/g, '')
}

function isActiveProviderStatus(value: string): boolean {
  return activeProviderStatuses.has(normalizedProviderStatus(value))
}

export function downloadProviderStatusLabel(value: string): string {
  const labels: Record<string, string> = {
    allocating: '正在分配空间',
    checkingdl: '正在校验',
    checkingresumedata: '正在校验恢复数据',
    downloading: '正在下载',
    forceddl: '强制下载',
    metadl: '正在获取元数据',
    queueddl: '等待下载队列',
    stalleddl: '等待连接/暂无速度',
  }
  const normalized = normalizedProviderStatus(value)
  return labels[normalized] ?? (value || '尚未采样')
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
    if (!task) continue
    const ordering = compareTaskUpdate(task.updated_at, previous.taskUpdatedAt)
    if (ordering < 0) {
      next[taskID] = previous
      continue
    }
    if (task.lifecycle_scope === 'history' || ['completed', 'cancelled'].includes(task.job_status)) continue
    if (task.job_status !== 'failed') {
      next[taskID] = { ...previous, taskUpdatedAt: ordering > 0 ? task.updated_at : previous.taskUpdatedAt, observedActive: true }
      continue
    }
    const newFailure = ordering > 0 || retryErrorFingerprint(task) !== previous.errorFingerprint
    if (!newFailure) next[taskID] = previous
  }
  return next
}

function compareTaskUpdate(value: string, baseline: string): number {
  const current = Date.parse(value)
  const previous = Date.parse(baseline)
  if (Number.isFinite(current) && Number.isFinite(previous)) return Math.sign(current - previous)
  return value === baseline ? 0 : value > baseline ? 1 : -1
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

export function canCancelDownloadPipeline(task: DownloadTaskSummary): boolean {
  return task.lifecycle_scope === 'active' && task.job_status !== 'cancelled'
}

export function formatSampleTime(value: string | null): string {
  if (!value) return '尚未检查'
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? '时间未知' : time.toLocaleString('zh-CN', { hour12: false })
}
