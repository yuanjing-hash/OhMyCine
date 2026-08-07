import type { Directive } from 'vue'
import type { PermissionCode } from '@/auth/generated-permissions'
import { useAuthStore } from '@/stores/auth'

export const permissionDirective: Directive<HTMLElement, PermissionCode> = {
  mounted(el, binding) { if (!useAuthStore().can(binding.value)) el.hidden = true },
  updated(el, binding) { el.hidden = !useAuthStore().can(binding.value) },
}
