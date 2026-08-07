/// <reference types="vite/client" />

import type { PermissionCode } from '@/auth/generated-permissions'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    permissionsAny?: PermissionCode[]
    title?: string
  }
}
