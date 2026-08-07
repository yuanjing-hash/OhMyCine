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
