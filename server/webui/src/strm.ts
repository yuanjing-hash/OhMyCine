import { api } from '@/api/client'

export interface STRMRun {
	id: string; library_id: number; generation: number; status: string; expected_count: number; written_count: number; updated_count: number
	removed_count: number; skipped_count: number; failed_count: number; retry_count: number; error_code: string
	cleanup_status: string; cleanup_error_code: string; cleanup_at: string | null
	started_at: string | null; finished_at: string | null; created_at: string; updated_at: string
}
export interface STRMLibraryOverview {
	id: number; name: string; storage_id: number; artifact_generation: number; artifact_applied_generation: number
	artifact_status: string; artifact_error: string; artifact_updated_at: string | null
	artifact_cleanup_removed: number; artifact_cleanup_error: string; artifact_cleanup_at: string | null; latest_run: STRMRun | null
}
export interface STRMArtifact {
	id: number; run_id: string; library_id: number; kind: string; target_kind: string; relative_path: string
	managed: boolean; active: boolean; status: string; error_code: string; created_at: string; updated_at: string
}
export interface Page<T> { list: T[]; total: number; page: number; page_size: number }
export interface CleanupPreview { count: number; kind_counts: Record<string, number>; paths: string[]; generation: number; confirmation_token: string; expires_at: string }

export const listSTRMLibraries = () => api<STRMLibraryOverview[]>('/api/v1/strm/libraries')
export const listSTRMRuns = (libraryID: number, page = 1) => api<Page<STRMRun>>(`/api/v1/strm/runs?library_id=${libraryID}&page=${page}&page_size=30`)
export const listSTRMArtifacts = (libraryID: number, page = 1) => api<Page<STRMArtifact>>(`/api/v1/strm/artifacts?library_id=${libraryID}&page=${page}&page_size=30`)
export const reconcileSTRM = (libraryID: number, mode: 'incremental' | 'full') => api(`/api/v1/strm/libraries/${libraryID}/reconcile`, { method: 'POST', body: JSON.stringify({ mode }) })
export const retrySTRMRun = (runID: string) => api(`/api/v1/strm/runs/${encodeURIComponent(runID)}/retry`, { method: 'POST', body: '{}' })
export const previewSTRMCleanup = (libraryID: number) => api<CleanupPreview>(`/api/v1/strm/libraries/${libraryID}/cleanup/preview`, { method: 'POST', body: '{}' })
export const executeSTRMCleanup = (libraryID: number, confirmationToken: string) => api<{ removed: number }>(`/api/v1/strm/libraries/${libraryID}/cleanup/execute`, { method: 'POST', body: JSON.stringify({ confirmation_token: confirmationToken }) })
