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

export interface ConnectionSummary {
  id: number; name: string; provider: 'pan115' | 'emby' | string; endpoint: string; enabled: boolean; credential_configured: boolean; recycle_password_configured: boolean
  account: { id: string; name: string; vip: boolean; used_bytes: number | null; total_bytes: number | null }
  health: { status: 'unknown' | 'online' | 'offline'; error_code: string; last_checked_at: string | null }
  revision: number; created_at: string; updated_at: string
}

export interface EmbyGatewaySummary {
  connection_id: number
  public_id: string
  alias: string
  enabled: boolean
  external_player_enabled: boolean
  fanart_enabled: boolean
  base_url: string
  revision: number
}

export interface EmbyManagementSummary {
  connection_id: number
  server_name: string
  version: string
  library_count: number | null
  movie_count: number | null
  series_count: number | null
  episode_count: number | null
  status: 'ready' | 'partial'
  error_code: string
  checked_at: string
}

export interface PlayerDeviceSummary {
  id: string
  name: string
  client_kind: string
  created_at: string
  last_seen_at: string
  idle_expires_at: string
  absolute_expires_at: string
}

export interface StorageCapabilities {
  network_drive: boolean; directory_list: boolean; watch: boolean
  native_offline_download: boolean; temporary_direct_url: boolean; signed_proxy: boolean; small_file_upload?: boolean; change_cursor: boolean
}
export interface StorageProbe { exists: boolean; readable: boolean; available: boolean; free_bytes: number | null; total_bytes: number | null; last_checked_at: string; error_code: string }
export interface StorageSummary {
  id: number; name: string; type: string; root_path: string; root_display_path?: string; connection_id: number | null; enabled: boolean
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
  kind?: 'fixed' | 'removable' | 'network' | 'optical' | 'ramdisk' | 'unknown' | 'filesystem' | 'mount' | 'directory' | 'cloud-directory'
}

export interface DirectoryBreadcrumb { name: string; token: string }

export interface DirectoryListing {
  platform: string
  location: string
  current_token: string
  current_selection_token: string
  parent_token?: string
  next_page_token?: string
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
export type RecognitionRuleMediaType = 'all' | 'movie' | 'tv'
export interface RecognitionRule { enabled: boolean; media_type: RecognitionRuleMediaType; pattern: string; replacement: string }
export interface MediaClassificationProfileSummary {
  id: number; code: string | null; name: string; kind: 'system' | 'custom'; protected: boolean
  schema_version: 1; revision: number; movie_category_count: number; tv_category_count: number; builtin_recognition_pack_count: number; recognition_rule_count: number
  created_at: string; updated_at: string
}
export interface MediaClassificationProfileDetail extends MediaClassificationProfileSummary {
  rules: ClassificationRulesV1; builtin_recognition_packs: Array<'tv-v1' | 'anime-v1'>; recognition_rules: RecognitionRule[]
  movie_directory_template: string; movie_filename_template: string
  tv_directory_template: string; tv_filename_template: string
}

export type MediaLibraryStatus = 'disabled' | 'initializing' | 'attaching_listener' | 'catch_up_reconciliation' | 'listening' | 'initialization_failed'
export interface MediaLibraryDetail {
  id: number; name: string; storage_id: number; storage_name: string; profile_id: number; profile_name: string
  profile_revision: number; relative_root: string; enabled: boolean; recursive: boolean
  full_scan_interval_hours: number; incremental_minutes: number; video_extensions: string[]; ignore_patterns: string[]
	strm_asset_default_extensions: string[]; strm_asset_extra_extensions: string[]; strm_asset_effective_extensions: string[]
  metadata_language: string; metadata_region: string; match_strategy: string
  provider_rate_per_second: number; provider_concurrency: number; metadata_rate_per_second: number; metadata_concurrency: number
  strm_enabled: boolean; strm_local_path: string; signed_proxy_enabled: boolean
  metadata_artifacts_enabled: boolean; upload_sidecars: boolean
  artifact_generation: number; artifact_applied_generation: number; artifact_status: string; artifact_error: string; artifact_updated_at: string | null
  status: MediaLibraryStatus; status_error_code: string; next_retry_at: string | null
  last_scan_at: string | null; last_successful_scan_at: string | null; baseline_generation: number; dirty_generation: number
  reclassification_due: boolean; entry_count: number; created_at: string; updated_at: string
  sort_order: number; transfer_mode: 'move' | 'copy' | 'symlink'; conflict_policy: 'ask' | 'overwrite' | 'skip' | 'rename'
  movie_directory_template: string; movie_filename_template: string
  tv_directory_template: string; tv_filename_template: string
  ingest_enabled: boolean; ingest_downloader_id?: string; ingest_downloader_name: string; ingest_relative_root: string
}
export interface MediaLibraryScanRun {
  id: number; library_id: number; kind: 'initial' | 'catch_up' | 'event' | 'incremental' | 'full' | 'manual' | string
  status: 'running' | 'success' | 'failed'; generation: number; discovered: number; added: number; updated: number; removed: number
  matched: number; unrecognized: number; cache_hits: number; recognition_failed: number
  error_code: string; partial: boolean; started_at: string; finished_at: string | null
}
export interface MediaLibraryEntry {
  id: number; library_id: number; relative_path: string; size: number; modified_at: string
  media_type: 'movie' | 'tv' | 'unknown'; title: string; series_title: string; season: number | null; episode: number | null
  match_status: string; tmdb_id?: number; release_year?: number; match_confidence?: number; recognition_error_code: string
  category_name: string; matched_rule_id: string | null; last_generation: number
  created_at: string; updated_at: string
}
export interface MediaCatalogItem {
  id: string; title: string; kind: 'movie' | 'series'; file_count: number; season_count: number; episode_count: number
  size: number; modified_at: string; category_name: string; match_status: string; tmdb_id?: number; release_year?: number; confidence?: number; recognition_error_code?: string
}
export interface MediaCatalogEpisode {
  id: number; title: string; season: number | null; episode: number | null; relative_path: string; size: number; modified_at: string
}
export interface MediaCatalogSeason { number: number; episodes: MediaCatalogEpisode[] }
export interface MediaCatalogDetail { work: MediaCatalogItem; seasons: MediaCatalogSeason[]; files: MediaCatalogEpisode[] }
export interface MediaRecognitionSummary {
  token: string; status: 'matched' | 'unrecognized'; error_code: string; title: string; media_type: 'movie' | 'tv' | ''
  release_year?: number; tmdb_id?: number; confidence?: number; category_name: string; manual_override: boolean
  file_count: number; source_summary: string; updated_at: string
}
export interface TMDBCandidate {
  id: number; title: string; media_type: 'movie' | 'tv'; original_language: string; release_year?: number; confidence: number
}
export interface PageResponse<T> extends ListResponse<T> { page: number; page_size: number }
export interface MediaLibraryWritePayload {
  name: string; storage_id: number; profile_id: number; relative_root_token?: string; relative_root?: string
  enabled: boolean; recursive: boolean; full_scan_interval_hours: number; incremental_minutes: number
  video_extensions: string[]; strm_asset_extra_extensions: string[]; ignore_patterns: string[]; metadata_language: string; metadata_region: string; match_strategy: string
  provider_rate_per_second: number; provider_concurrency: number; metadata_rate_per_second: number; metadata_concurrency: number
  strm_enabled: boolean; strm_local_root_token?: string; metadata_artifacts_enabled: boolean; upload_sidecars: boolean
  transfer_mode: 'move' | 'copy' | 'symlink'; conflict_policy: 'ask' | 'overwrite' | 'skip' | 'rename'
  movie_directory_template: string; movie_filename_template: string
  tv_directory_template: string; tv_filename_template: string
  ingest_enabled: boolean; ingest_downloader_id?: string; ingest_relative_root?: string; ingest_relative_root_token?: string
}

export interface RuntimeLogEntry {
  timestamp: string; level: 'debug' | 'info' | 'warn' | 'error'; message: string; module: string; component: string
  operation?: string; operation_label?: string; plugin_id?: string; fields: Record<string, unknown>
}
export interface RuntimeLogResult { list: RuntimeLogEntry[]; next_cursor?: string; scanned_bytes: number; malformed: number; partial: boolean }
export interface RuntimeLogFacets { levels: string[]; modules: string[]; components: string[]; operations: string[]; plugin_ids: string[] }
export interface RuntimeLogPolicy { level: 'debug' | 'info' | 'warn' | 'error'; max_file_mib: number; max_backups: number; retention_days: number; max_total_mib: number }
export interface RuntimeLogSettings extends RuntimeLogPolicy { revision: number; health: { degraded: boolean; reason?: string } }

export interface DownloaderCapabilities {
  pause: boolean; resume: boolean; cancel: boolean; delete_data: boolean
  download_speed: boolean; upload_speed: boolean; eta: boolean; seeding: boolean; native_offline: boolean
  output_constraint: 'none' | 'local_staging' | 'provider_storage'
  share_receive: boolean
}
export interface DownloaderSummary {
  id: string; name: string; type: 'fake' | 'qbittorrent' | 'pan115_offline'; base_url: string; enabled: boolean
  storage_id: number | null; storage_name: string; provider_directory_path: string
  username_configured: boolean; password_configured: boolean; capabilities: DownloaderCapabilities
  health: { status: 'unknown' | 'online' | 'offline'; version: string; error_code: string; last_checked_at: string | null }
  created_at: string; updated_at: string
}
export interface DownloadSettings {
  configured: boolean; absolute_path: string; revision: number; updated_at: string
}
export interface MetadataSettings {
  tmdb_configured: boolean; custom_configured: boolean; credential_source: 'custom' | 'deployment' | 'builtin' | 'none'
  credential_kind: 'read_access_token' | 'api_key' | ''
  api_base_url: string; image_base_url: string; revision: number; updated_at: string
}
export interface DownloadTaskSummary {
  id: string; job_id: string; owner_id: number; downloader_id: string | null; downloader_name: string; provider_type: string
  display_name: string; job_status: string; provider_status: string; phase: string; progress: number | null
  bytes_completed: number | null; bytes_total: number | null; download_speed: number | null; upload_speed: number | null
  eta_seconds: number | null; last_sampled_at: string | null; last_error_code: string; last_error_message: string
  created_at: string; updated_at: string; finished_at: string | null
  profile_id: number; profile_revision: number; scrape_status: string; scrape_title: string; scrape_media_type: string
  scrape_category: string; scrape_tmdb_id: number | null; scrape_confidence: number | null; manifest_file_count: number
  target_library_id: number | null; target_library_name: string; transfer_mode: string; conflict_policy: string; transfer_phase: string
  transfer_task_id: string; transfer_job_id: string; transfer_job_status: string
  seeding_task_id: string; seeding_job_id: string; seeding_job_status: string; seeding_phase: string
  lifecycle_scope: 'active' | 'history'
}

export interface SeedingSettings {
  enabled: boolean; minimum_seed_minutes: number; minimum_ratio: number
  completion_mode: 'all' | 'any'; revision: number; updated_at: string
}
export interface SeedingTaskSummary {
  id: string; job_id: string; job_status: string; download_task_id: string; owner_id: number
  downloader_name: string; display_name: string; provider_type: string; transfer_mode: 'copy' | 'symlink'; delete_data: boolean
  cleanup_enabled: boolean; minimum_seed_minutes: number; minimum_ratio: number; completion_mode: 'all' | 'any'
  phase: 'queued' | 'seeding' | 'cleanup' | 'retained' | 'completed' | 'failed'
  ratio: number | null; seeded_seconds: number | null; uploaded_bytes: number | null; last_sampled_at: string | null
  last_error_code: string; created_at: string; updated_at: string; finished_at: string | null
}
