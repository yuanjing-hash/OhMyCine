import type { PermissionCode } from '@/auth/generated-permissions'

export interface CurrentUser {
  id: number
  username: string
  display_name: string
  status: 'active' | 'disabled'
  is_owner: boolean
  roles: string[]
  permissions: PermissionCode[]
}

export interface RoleSummary {
  id: number
  code: string
  name: string
  description: string
  kind: 'system' | 'custom'
  protected: boolean
  active: boolean
  permissions: PermissionCode[]
  user_count: number
}

export interface UserSummary {
  id: number
  username: string
  display_name: string
  status: 'active' | 'disabled'
  is_owner: boolean
  roles: RoleSummary[]
  last_login_at: string | null
  created_at: string
}

export interface PermissionDefinition {
  code: PermissionCode
  module: string
  name: string
  description: string
  risk: 'normal' | 'sensitive' | 'destructive'
  deprecated_at: string | null
}

export interface ListResponse<T> { list: T[]; total: number }

export interface StorageCapabilities {
  network_drive: boolean; directory_list: boolean; watch: boolean
  native_offline_download: boolean; temporary_direct_url: boolean; signed_proxy: boolean; change_cursor: boolean
}
export interface StorageProbe { exists: boolean; readable: boolean; available: boolean; free_bytes: number | null; total_bytes: number | null; last_checked_at: string; error_code: string }
export interface StorageSummary {
  id: number; name: string; type: string; root_path: string; connection_id: number | null; enabled: boolean
  capabilities: StorageCapabilities; probe: StorageProbe; created_at: string; updated_at: string
}

export interface DirectoryItem {
  name: string
  location: string
  token?: string
  selection_token?: string
  selectable: boolean
  enterable: boolean
  unavailable_reason?: 'link_not_allowed' | 'root_unavailable'
  kind?: 'fixed' | 'removable' | 'network' | 'optical' | 'ramdisk' | 'unknown' | 'filesystem' | 'mount' | 'directory'
}

export interface DirectoryBreadcrumb { name: string; token: string }

export interface DirectoryListing {
  platform: string
  location: string
  current_token: string
  current_selection_token: string
  parent_token?: string
  breadcrumbs: DirectoryBreadcrumb[]
  items: DirectoryItem[]
  truncated: boolean
}

export type MediaType = 'movie' | 'tv'
export interface RuleSetCondition<T> { include: T[]; exclude: T[] }
export interface ClassificationConditions {
  genre_ids: RuleSetCondition<number>
  original_languages: RuleSetCondition<string>
  production_countries?: RuleSetCondition<string>
  origin_countries?: RuleSetCondition<string>
  release_year: { from?: number; to?: number } | null
}
export interface ClassificationCategory { id: string; name: string; conditions: ClassificationConditions }
export interface ClassificationGroup { media_type: MediaType; categories: ClassificationCategory[]; fallback_category_name: string }
export interface ClassificationRulesV1 { version: 1; groups: ClassificationGroup[] }
export interface MediaClassificationProfileSummary {
  id: number; code: string | null; name: string; kind: 'system' | 'custom'; protected: boolean
  schema_version: 1; revision: number; movie_category_count: number; tv_category_count: number
  created_at: string; updated_at: string
}
export interface MediaClassificationProfileDetail extends MediaClassificationProfileSummary { rules: ClassificationRulesV1 }

export type MediaLibraryStatus = 'disabled' | 'initializing' | 'attaching_listener' | 'catch_up_reconciliation' | 'listening' | 'initialization_failed'
export interface MediaLibraryDetail {
  id: number; name: string; storage_id: number; storage_name: string; profile_id: number; profile_name: string
  profile_revision: number; relative_root: string; enabled: boolean; recursive: boolean
  full_scan_interval_hours: number; incremental_minutes: number; video_extensions: string[]; ignore_patterns: string[]
  metadata_language: string; metadata_region: string; match_strategy: string
  provider_rate_per_second: number; provider_concurrency: number; metadata_rate_per_second: number; metadata_concurrency: number
  strm_enabled: boolean; status: MediaLibraryStatus; status_error_code: string; next_retry_at: string | null
  last_scan_at: string | null; last_successful_scan_at: string | null; baseline_generation: number; dirty_generation: number
  reclassification_due: boolean; entry_count: number; created_at: string; updated_at: string
}
export interface MediaLibraryScanRun {
  id: number; library_id: number; kind: 'initial' | 'catch_up' | 'event' | 'incremental' | 'full' | 'manual' | string
  status: 'running' | 'success' | 'failed'; generation: number; discovered: number; added: number; updated: number; removed: number
  error_code: string; partial: boolean; started_at: string; finished_at: string | null
}
export interface MediaLibraryEntry {
  id: number; library_id: number; relative_path: string; provider_id: string; size: number; modified_at: string
  media_type: 'movie' | 'tv' | 'unknown'; title: string; season: number | null; episode: number | null
  match_status: string; category_name: string; matched_rule_id: string | null; last_generation: number
  created_at: string; updated_at: string
}
export interface MediaLibraryWritePayload {
  name: string; storage_id: number; profile_id: number; relative_root_token?: string; relative_root?: string
  enabled: boolean; recursive: boolean; full_scan_interval_hours: number; incremental_minutes: number
  video_extensions: string[]; ignore_patterns: string[]; metadata_language: string; metadata_region: string; match_strategy: string
  provider_rate_per_second: number; provider_concurrency: number; metadata_rate_per_second: number; metadata_concurrency: number
  strm_enabled: boolean; strm_local_root_token?: string
}

export interface RuntimeLogEntry {
  timestamp: string; level: 'debug' | 'info' | 'warn' | 'error'; message: string; module: string; component: string
  plugin_id?: string; fields: Record<string, unknown>
}
export interface RuntimeLogResult { list: RuntimeLogEntry[]; next_cursor?: string; scanned_bytes: number; malformed: number; partial: boolean }
export interface RuntimeLogFacets { levels: string[]; modules: string[]; components: string[]; plugin_ids: string[] }
export interface RuntimeLogPolicy { level: 'debug' | 'info' | 'warn' | 'error'; max_file_mib: number; max_backups: number; retention_days: number; max_total_mib: number }
export interface RuntimeLogSettings extends RuntimeLogPolicy { revision: number; health: { degraded: boolean; reason?: string } }
