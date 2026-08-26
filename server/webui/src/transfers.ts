import { api } from '@/api/client'
import type { Job, JobAttempt, JobEvent } from '@/jobs'

export type TransferMode = 'move' | 'copy' | 'symlink'
export type TransferPhase = 'queued' | 'planning' | 'transferring' | 'reconciling' | 'completed' | 'failed'
export type TransferFilterStatus = '' | 'processing' | 'waiting_action' | 'paused' | 'failed' | 'completed' | 'cancelled'
export type TransferListScope = 'active' | 'history'

export interface TransferStats {
  processing: number
  waiting_action: number
  failed: number
  completed_today: number
}

export interface TransferSummary {
  id: string
  owner_id: number
  download_task_id: string
  job_id: string
  display_name: string
  downloader_name: string
  provider_type: string
  scrape_status: string
  scrape_title: string
  scrape_media_type: string
  scrape_category: string
  scrape_tmdb_id: number | null
  scrape_year: number | null
  scrape_confidence: number | null
  identity_source: 'manual' | 'direct_id' | 'automatic' | 'ai' | 'local_provisional' | ''
  identity_status: 'verified' | 'provisional' | 'local_provisional' | ''
  identity_locked: boolean
  identity_revision: number
  profile_id: number
  profile_revision: number
  library_id: number
  library_name: string
  transfer_mode: TransferMode
  conflict_policy: 'ask' | 'overwrite' | 'skip' | 'rename'
  phase: TransferPhase
  job_status: Job['status']
  processed_files: number
  total_files: number
  last_error_code: string
  last_error_message: string
  cleanup_status: 'pending' | 'deferred' | 'running' | 'completed' | 'failed' | 'skipped'
  cleanup_removed: number
  cleanup_error_code: string
  created_at: string
  updated_at: string
  finished_at: string | null
}

export interface TransferPlanSummaryItem {
  relative_path: string
  kind: 'video' | 'sidecar'
  size: number
  result: 'planned' | 'completed' | 'skipped'
}

export interface TransferPlanSummary {
  items: TransferPlanSummaryItem[]
  total_files: number
  truncated: boolean
}

export interface TransferDetail extends TransferSummary {
  movie_directory_template: string
  movie_filename_template: string
  tv_directory_template: string
  tv_filename_template: string
  plan_summary: TransferPlanSummary | null
  job: Job
  attempts: JobAttempt[]
  timeline: JobEvent[]
}

export interface TransferPage {
  list: TransferSummary[]
  total: number
  page: number
  page_size: number
  stats: TransferStats
  filter_options: {
    libraries: Array<{ id: number; name: string }>
    categories: string[]
  }
}

export const transferPhaseLabels: Record<TransferPhase, string> = {
  queued: '等待整理',
  planning: '规划目录',
  transferring: '正在入库',
  reconciling: '媒体库对账',
  completed: '整理完成',
  failed: '整理失败',
}

export const transferModeLabels: Record<TransferMode, string> = {
  move: '移动',
  copy: '复制',
  symlink: '软链接',
}

export const conflictPolicyLabels: Record<TransferSummary['conflict_policy'], string> = {
  ask: '发生冲突时询问',
  overwrite: '覆盖同名文件',
  skip: '跳过同名文件',
  rename: '自动重命名',
}

export type TransferDeletionScope = 'record_only' | 'record_and_source' | 'record_and_library' | 'record_source_and_library'

export interface TransferDeletionPreview {
  scope: TransferDeletionScope
  source_items: number
  source_bytes: number
  library_items: number
  library_bytes: number
  provider_type: string
  source_storage_type: string
  library_storage_type: string
  source_missing: number
  library_missing: number
  blocked: boolean
  blockers: string[]
  requires_file_delete: boolean
  warnings: string[]
  confirmation_token: string
  expires_at: string
}

export interface TransferDeletionResult {
  deleted: boolean
  scope: TransferDeletionScope
  source_removed: number
  library_removed: number
}

export const transferDeletionLabels: Record<TransferDeletionScope, string> = {
  record_only: '仅删除转移记录',
  record_and_source: '删除转移记录和源文件',
  record_and_library: '删除转移记录和媒体库文件',
  record_source_and_library: '删除转移记录、源文件和媒体库文件',
}

export function transferIdentityLabel(item: TransferSummary): string {
  if (item.identity_locked || item.identity_source === 'manual') return `人工锁定 · r${item.identity_revision}`
  if (item.identity_source === 'ai') return `AI 辅助 · r${item.identity_revision}`
  if (item.identity_status === 'local_provisional') return `本地暂定 · r${item.identity_revision}`
  if (item.identity_status === 'provisional') return `自动暂定 · r${item.identity_revision}`
  if (item.identity_source === 'direct_id') return `TMDB ID 验证 · r${item.identity_revision}`
  return item.identity_revision > 0 ? `自动识别 · r${item.identity_revision}` : '旧任务身份'
}

export function transferStatusClass(item: TransferSummary): string {
  if (item.job_status === 'failed') return 'status-chip status-chip--error'
  if (item.job_status === 'waiting_user_action' || item.job_status === 'retry_wait' || item.job_status === 'paused' || item.job_status === 'cancelled') return 'status-chip status-chip--warning'
  if (item.phase === 'failed' && item.job_status !== 'queued' && item.job_status !== 'running') return 'status-chip status-chip--error'
  if (item.phase === 'completed') return 'status-chip status-chip--ready'
  return 'status-chip status-chip--planned'
}

export function transferStatusLabel(item: TransferSummary): string {
  if (item.job_status === 'waiting_user_action') return '等待冲突处理'
  if (item.job_status === 'retry_wait') return '等待自动重试'
  if (item.job_status === 'paused') return '已暂停'
  if (item.job_status === 'cancelled') return '已取消'
  if (item.job_status === 'failed') return '整理失败'
  if (item.phase === 'failed' && item.job_status === 'queued') return '等待重试'
  if (item.phase === 'failed' && item.job_status === 'running') return '整理处理中'
  if (item.phase === 'failed') return '整理失败'
  return transferPhaseLabels[item.phase] ?? '状态未知'
}

export function formatTransferProgress(item: TransferSummary): string {
  if (item.total_files < 1) return '尚未生成计划'
  return `${item.processed_files} / ${item.total_files}`
}

export const listTransfers = (query: URLSearchParams) => api<TransferPage>(`/api/v1/transfers?${query}`)
export const getTransfer = (id: string) => api<TransferDetail>(`/api/v1/transfers/${encodeURIComponent(id)}`)
export const deleteTransfer = (id: string) => api<{ deleted: boolean }>(`/api/v1/transfers/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const previewTransferDeletion = (id: string, scope: TransferDeletionScope) => api<TransferDeletionPreview>(`/api/v1/transfers/${encodeURIComponent(id)}/deletion-preview`, { method: 'POST', body: JSON.stringify({ scope }) })
export const confirmTransferDeletion = (id: string, token: string) => api<TransferDeletionResult>(`/api/v1/transfers/${encodeURIComponent(id)}/deletion-confirm`, { method: 'POST', body: JSON.stringify({ token }) })
export const retargetCompletedImport = (downloadTaskID: string, mediaLibraryID: number) => api(`/api/v1/downloads/${encodeURIComponent(downloadTaskID)}/import-target`, { method: 'PUT', body: JSON.stringify({ media_library_id: mediaLibraryID }) })

export function canRetargetTransfer(item: TransferSummary): boolean {
  return item.job_status === 'failed' && item.phase === 'failed' && item.processed_files === 0 && item.cleanup_removed === 0 && item.cleanup_status === 'pending'
}

export function canDeleteTransferRecord(item: TransferSummary): boolean {
  return item.job_status === 'failed' || item.job_status === 'cancelled' || item.job_status === 'completed'
}

export function shouldRefreshTransferEvent(raw: unknown, visibleJobIDs: ReadonlySet<string>): boolean {
  if (typeof raw !== 'string') return false
  try {
    const envelope = JSON.parse(raw) as { data?: { job_id?: unknown; job_type?: unknown } }
    if (envelope.data?.job_type === 'transfer') return true
    return typeof envelope.data?.job_id === 'string' && visibleJobIDs.has(envelope.data.job_id)
  } catch {
    return false
  }
}
