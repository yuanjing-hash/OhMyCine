import type { MediaLibraryDetail, MediaLibraryStatus, MediaLibraryWritePayload, StorageSummary } from '@/types/api'

export const defaultVideoExtensions = ['.mp4', '.mkv', '.ts', '.iso', '.rmvb', '.avi', '.mov', '.mpeg', '.mpg', '.wmv', '.3gp', '.asf', '.m4v', '.flv', '.m2ts', '.tp', '.f4v']

export interface MediaLibraryDraft extends Omit<MediaLibraryWritePayload, 'video_extensions' | 'ignore_patterns'> {
  source_path: string
  video_extensions_text: string
  ignore_patterns_text: string
  strm_local_path: string
}

export function emptyMediaLibraryDraft(storageID = 0, profileID = 0): MediaLibraryDraft {
  return {
    name: '', storage_id: storageID, profile_id: profileID, relative_root_token: '', relative_root: '/', source_path: '',
    enabled: true, recursive: true, full_scan_interval_hours: 24, incremental_minutes: 15,
    video_extensions_text: defaultVideoExtensions.join(', '), ignore_patterns_text: '', metadata_language: 'zh-CN', metadata_region: 'CN', match_strategy: 'balanced',
    provider_rate_per_second: 100, provider_concurrency: 2, metadata_rate_per_second: 5, metadata_concurrency: 1,
    strm_enabled: false, strm_local_root_token: '', strm_local_path: '',
  }
}

export function draftFromLibrary(library: MediaLibraryDetail): MediaLibraryDraft {
  return {
    name: library.name, storage_id: library.storage_id, profile_id: library.profile_id, relative_root: library.relative_root,
    relative_root_token: '', source_path: library.relative_root, enabled: library.enabled, recursive: library.recursive,
    full_scan_interval_hours: library.full_scan_interval_hours, incremental_minutes: library.incremental_minutes,
    video_extensions_text: library.video_extensions.join(', '), ignore_patterns_text: library.ignore_patterns.join('\n'),
    metadata_language: library.metadata_language, metadata_region: library.metadata_region, match_strategy: library.match_strategy,
    provider_rate_per_second: library.provider_rate_per_second, provider_concurrency: library.provider_concurrency,
    metadata_rate_per_second: library.metadata_rate_per_second, metadata_concurrency: library.metadata_concurrency,
    strm_enabled: library.strm_enabled, strm_local_root_token: '', strm_local_path: '',
  }
}

function splitValues(value: string) {
  return value.split(/[\n,]/).map(item => item.trim()).filter(Boolean)
}

export function supportsSTRM(storage: StorageSummary | undefined): boolean {
  return Boolean(storage && storage.type !== 'local' && storage.capabilities.temporary_direct_url && storage.capabilities.signed_proxy)
}

export function payloadFromDraft(draft: MediaLibraryDraft, storage: StorageSummary | undefined): MediaLibraryWritePayload {
  const payload: MediaLibraryWritePayload = {
    name: draft.name, storage_id: draft.storage_id, profile_id: draft.profile_id, enabled: draft.enabled, recursive: draft.recursive,
    full_scan_interval_hours: draft.full_scan_interval_hours, incremental_minutes: draft.incremental_minutes,
    video_extensions: splitValues(draft.video_extensions_text), ignore_patterns: splitValues(draft.ignore_patterns_text),
    metadata_language: draft.metadata_language, metadata_region: draft.metadata_region, match_strategy: draft.match_strategy,
    provider_rate_per_second: draft.provider_rate_per_second, provider_concurrency: draft.provider_concurrency,
    metadata_rate_per_second: draft.metadata_rate_per_second, metadata_concurrency: draft.metadata_concurrency,
    strm_enabled: supportsSTRM(storage) && draft.strm_enabled,
  }
  if (draft.relative_root_token) payload.relative_root_token = draft.relative_root_token
  else payload.relative_root = draft.relative_root || '/'
  if (payload.strm_enabled && draft.strm_local_root_token) payload.strm_local_root_token = draft.strm_local_root_token
  return payload
}

const statusPresentation: Record<MediaLibraryStatus, { label: string; className: string }> = {
  disabled: { label: '已停用', className: 'status-chip' },
  initializing: { label: '首次扫描中', className: 'status-chip status-chip--warning' },
  attaching_listener: { label: '挂接监听中', className: 'status-chip status-chip--warning' },
  catch_up_reconciliation: { label: '增量对账中', className: 'status-chip status-chip--warning' },
  listening: { label: '监听中', className: 'status-chip status-chip--ready' },
  initialization_failed: { label: '初始化失败', className: 'status-chip status-chip--error' },
}

export function presentLibraryStatus(status: MediaLibraryStatus) { return statusPresentation[status] }
export function isActiveLibraryStatus(status: MediaLibraryStatus) { return ['initializing', 'attaching_listener', 'catch_up_reconciliation'].includes(status) }
