import { api, APIError } from '@/api/client'
import type { MediaCoverage } from '@/discovery'

export interface FollowSchedule { kind: 'interval'; minutes: number }
export interface FollowFilters {
  resolutions: string[]; video_codecs: string[]; qualities: string[]
  include_keywords: string[]; exclude_keywords: string[]
  release_groups: string[]; exclude_release_groups: string[]
  min_seeders: number; max_age_hours: number | null; min_size_bytes: number | null; max_size_bytes: number | null
}
export interface FollowExecutionSnapshot {
  version: 1; seasons: number[]; site_ids: number[]; downloader_id: string; media_library_id: number
  schedule: FollowSchedule; filters: FollowFilters; max_resources_per_run: number; download_priority: number
}
export interface FollowOption { id: string; name: string }
export interface FollowSiteOption { id: number; name: string }
export interface FollowLibraryOption { id: number; name: string }
export interface FollowDefaults {
  snapshot: FollowExecutionSnapshot; sites: FollowSiteOption[]; downloaders: FollowOption[]
  media_libraries: FollowLibraryOption[]; subscribed_seasons: number[]; coverage: MediaCoverage
}
export type FollowStatus = 'active' | 'paused' | 'completed' | 'blocked'
export interface FollowSummary {
  id: string; owner_id: number; media_type: 'tv'; tmdb_id: number; title: string; year?: number; poster_ref?: string
  status: FollowStatus; revision: number; snapshot: FollowExecutionSnapshot
  progress_target: number; progress_present: number; progress_missing: number
  last_run_id?: string; last_run_at?: string; next_run_at?: string
  last_error_code: string; last_error_message: string; created_at: string; updated_at: string
}
export interface FollowPage { list: FollowSummary[]; total: number; page: number; page_size: number }
export interface FollowRunSummary {
  id: string; job_id: string; trigger: 'scheduled' | 'manual' | 'created'
  status: 'queued' | 'running' | 'no_match' | 'submitted' | 'completed' | 'failed' | 'cancelled' | 'stale'
  subscription_revision: number; searched_names_count: number; candidates: number; selected: number
  filter_summary: Record<string, number>; error_code: string; error_message: string
  started_at?: string; finished_at?: string; created_at: string
}

export function followDefaultsPath(tmdbID: number) { return `/api/v1/follows/defaults?media_type=tv&tmdb_id=${encodeURIComponent(String(tmdbID))}` }
export function followPath(id: string) { return `/api/v1/follows/${encodeURIComponent(id)}` }
export function splitRuleText(value: string) { return [...new Set(value.split(/[,，\n]/).map(item => item.trim()).filter(Boolean))].slice(0, 16) }
export function cloneFollowSnapshot(snapshot: FollowExecutionSnapshot): FollowExecutionSnapshot { return JSON.parse(JSON.stringify(snapshot)) as FollowExecutionSnapshot }
export function canSubmitFollow(defaults: FollowDefaults | null, snapshot: FollowExecutionSnapshot | null) { return Boolean(defaults && snapshot && snapshot.seasons.length && snapshot.site_ids.length && snapshot.downloader_id && snapshot.media_library_id) }
export function followStatusLabel(status: FollowStatus) { return ({ active: '追更中', paused: '已暂停', completed: '当前已补齐', blocked: '需要处理' } as const)[status] }
export function followRunStatusLabel(status: FollowRunSummary['status']) { return ({ queued: '等待执行', running: '正在搜索', no_match: '暂无匹配', submitted: '已提交下载', completed: '本次已补齐', failed: '执行受阻', cancelled: '已取消', stale: '已失效' } as const)[status] }
export function isFollowRevisionConflict(reason: unknown) { return reason instanceof APIError && reason.errorCode === 'follow_revision_conflict' }

export async function loadFollowDefaults(tmdbID: number) { return api<FollowDefaults>(followDefaultsPath(tmdbID)) }
export async function createFollow(input: { tmdb_id: number; title: string; year?: number; poster_ref?: string; snapshot: FollowExecutionSnapshot }) { return api<FollowSummary>('/api/v1/follows', { method: 'POST', body: JSON.stringify(input) }) }
export async function loadFollow(id: string) { return api<FollowSummary>(followPath(id)) }
export async function updateFollow(id: string, revision: number, snapshot: FollowExecutionSnapshot) { return api<FollowSummary>(followPath(id), { method: 'PUT', body: JSON.stringify({ revision, snapshot }) }) }
export async function followAction(id: string, action: 'pause' | 'resume' | 'search') { return api<FollowSummary | { job_id: string }>(`${followPath(id)}/${action}`, { method: 'POST', body: '{}' }) }
export async function deleteFollow(id: string) { return api<{ deleted: boolean }>(followPath(id), { method: 'DELETE' }) }
