import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api, clearCSRFToken, setCSRFToken } from '@/api/client'
import type { PermissionCode } from '@/auth/generated-permissions'
import type { CurrentUser } from '@/types/api'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<CurrentUser | null>(null)
  const setupRequired = ref(false)
  const recoveryRequired = ref(false)
  const initialized = ref(false)
  const loading = ref(false)
  const permissionSet = computed(() => new Set<PermissionCode>(user.value?.permissions ?? []))

  function can(code: PermissionCode) { return permissionSet.value.has(code) }
  function canAny(codes: PermissionCode[]) { return codes.length === 0 || codes.some(can) }

  async function bootstrap(force = false) {
    if (initialized.value && !force) return
    loading.value = true
    try {
      const status = await api<{ setup_required: boolean; recovery_required: boolean }>('/api/v1/setup/status')
      setupRequired.value = status.setup_required
      recoveryRequired.value = status.recovery_required
      user.value = null
      if (!status.setup_required && !status.recovery_required) {
        try { user.value = await api<CurrentUser>('/api/v1/auth/me') } catch { user.value = null }
      }
      initialized.value = true
    } finally { loading.value = false }
  }

  async function login(username: string, password: string) {
    const result = await api<{ user: CurrentUser; csrf_token: string }>('/api/v1/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }, { skipCSRF: true })
    user.value = result.user; setCSRFToken(result.csrf_token); setupRequired.value = false
  }

  async function setup(username: string, displayName: string, password: string) {
    const result = await api<{ user: CurrentUser; csrf_token: string }>('/api/v1/setup/owner', { method: 'POST', body: JSON.stringify({ username, display_name: displayName, password }) }, { skipCSRF: true })
    user.value = result.user; setCSRFToken(result.csrf_token); setupRequired.value = false
  }

  async function logout() {
    try { await api('/api/v1/auth/logout', { method: 'POST', body: '{}' }) } finally { user.value = null; clearCSRFToken() }
  }

  window.addEventListener('omc:unauthorized', () => { user.value = null })
  window.addEventListener('omc:forbidden', () => {
    void api<CurrentUser>('/api/v1/auth/me').then(current => { user.value = current }).catch(() => undefined)
  })
  return { user, setupRequired, recoveryRequired, initialized, loading, can, canAny, bootstrap, login, setup, logout }
})
