import { api } from '@/api/client'
import type { ListResponse, TMDBCandidate } from '@/types/api'

export type MediaReorganizationConflictPolicy = 'ask' | 'overwrite' | 'skip' | 'rename'
export type MediaReorganizationPhase = 'queued' | 'executing' | 'reconciling' | 'completed' | 'failed'

export interface MediaReorganizationPlanItem {
  kind: string
  old_relative_path: string
  new_relative_path: string
  action: 'move' | 'skip' | 'unchanged' | string
}

export interface MediaReorganizationPreview {
  library_id: number
  identity_revision: number
  title: string
  media_type: 'movie' | 'tv'
  items: MediaReorganizationPlanItem[]
  conflict_count: number
  confirmation_token: string
  expires_at: string
}

export interface MediaReorganizationTask {
  id: string
  job_id: string
  library_id: number
  source_identity_revision: number
  target_identity_revision: number
  phase: MediaReorganizationPhase
  total_items: number
  processed_items: number
  last_error_code: string
  created_at: string
  updated_at: string
  finished_at: string | null
}

export interface MediaReorganizationPreviewInput {
  transfer_task_id: string
  tmdb_id: number
  media_type: 'movie' | 'tv'
  conflict_policy: MediaReorganizationConflictPolicy
}

export function searchReorganizationCandidates(downloadTaskID: string, title: string, mediaType: '' | 'movie' | 'tv') {
  const query = new URLSearchParams({ title: title.trim() })
  if (mediaType) query.set('media_type', mediaType)
  return api<ListResponse<TMDBCandidate>>(`/api/v1/downloads/${encodeURIComponent(downloadTaskID)}/tmdb-candidates?${query}`)
}

export function previewMediaReorganization(input: MediaReorganizationPreviewInput) {
  return api<MediaReorganizationPreview>('/api/v1/media/reorganizations/preview', { method: 'POST', body: JSON.stringify(input) })
}

export function confirmMediaReorganization(confirmationToken: string) {
  return api<MediaReorganizationTask>('/api/v1/media/reorganizations/confirm', { method: 'POST', body: JSON.stringify({ confirmation_token: confirmationToken }) })
}

export function getMediaReorganization(id: string) {
  return api<MediaReorganizationTask>(`/api/v1/media/reorganizations/${encodeURIComponent(id)}`)
}

export function mediaReorganizationPhaseLabel(phase: MediaReorganizationPhase): string {
  return ({ queued: '等待执行', executing: '正在移动与重命名', reconciling: '正在重建元数据与投影', completed: '重新整理完成', failed: '重新整理失败' } as const)[phase]
}
