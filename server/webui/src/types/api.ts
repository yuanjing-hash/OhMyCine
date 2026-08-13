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
  id: number; name: string; type: 'local'; root_path: string; connection_id: number | null; enabled: boolean
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
