interface Envelope<T> { code: number; message: string; data: T }

export class APIError extends Error {
  constructor(public status: number, public errorCode: string, message: string) { super(message) }
}

let csrfToken = ''

function isEnvelope(value: unknown): value is Envelope<unknown> {
  return typeof value === 'object' && value !== null && typeof Reflect.get(value, 'code') === 'number' && typeof Reflect.get(value, 'message') === 'string' && Reflect.has(value, 'data')
}

export function setCSRFToken(token: string) { csrfToken = token }
export function clearCSRFToken() { csrfToken = '' }

async function ensureCSRF() {
  if (csrfToken) return
  const result = await api<{ csrf_token: string }>('/api/v1/auth/csrf')
  csrfToken = result.csrf_token
}

export async function api<T>(path: string, options: RequestInit = {}, config: { skipCSRF?: boolean } = {}): Promise<T> {
  const method = (options.method ?? 'GET').toUpperCase()
  const mutating = !['GET', 'HEAD', 'OPTIONS'].includes(method)
  if (mutating && !config.skipCSRF) await ensureCSRF()
  const headers = new Headers(options.headers)
  if (mutating) headers.set('Content-Type', 'application/json')
  if (mutating && !config.skipCSRF) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers, credentials: 'include' })
  const payload: unknown = await response.json().catch(() => null)
  if (!isEnvelope(payload)) throw new APIError(response.status, 'INVALID_RESPONSE', '服务器返回了无效响应')
  if (!response.ok || payload.code !== 0) {
    const data = payload.data
    const errorCode = typeof data === 'object' && data !== null && typeof Reflect.get(data, 'error_code') === 'string' ? String(Reflect.get(data, 'error_code')) : 'REQUEST_FAILED'
    if (response.status === 401) { clearCSRFToken(); window.dispatchEvent(new CustomEvent('omc:unauthorized')) }
    if (response.status === 403) window.dispatchEvent(new CustomEvent('omc:forbidden'))
    throw new APIError(response.status, errorCode, payload.message)
  }
  return payload.data as T
}
