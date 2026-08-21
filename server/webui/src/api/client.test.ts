import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { APIError, api, clearCSRFToken, setCSRFToken } from '@/api/client'

function envelope(status: number, code: number, data: unknown, message = 'success') {
  return new Response(JSON.stringify({ code, message, data }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

let dispatchEvent: ReturnType<typeof vi.fn>

beforeEach(() => {
  dispatchEvent = vi.fn()
  vi.stubGlobal('window', { dispatchEvent })
})

afterEach(() => {
  clearCSRFToken()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('API CSRF recovery', () => {
  it('refreshes an expired CSRF token and replays a mutation once', async () => {
    setCSRFToken('expired-token')
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(envelope(403, 40301, { error_code: 'CSRF_INVALID' }, 'CSRF 校验失败'))
      .mockResolvedValueOnce(envelope(200, 0, { csrf_token: 'fresh-token' }))
      .mockResolvedValueOnce(envelope(200, 0, { retried: true }))

    await expect(api<{ retried: boolean }>('/api/v1/jobs/job-1/retry', { method: 'POST', body: '{}' })).resolves.toEqual({ retried: true })

    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(new Headers(fetchMock.mock.calls[0]![1]?.headers).get('X-CSRF-Token')).toBe('expired-token')
    expect(fetchMock.mock.calls[1]![0]).toBe('/api/v1/auth/csrf')
    expect(new Headers(fetchMock.mock.calls[2]![1]?.headers).get('X-CSRF-Token')).toBe('fresh-token')
    expect(dispatchEvent).not.toHaveBeenCalled()
  })

  it('does not replay a non-CSRF forbidden response', async () => {
    setCSRFToken('valid-token')
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(envelope(403, 40302, { error_code: 'PERMISSION_DENIED' }, '权限不足'))

    await expect(api('/api/v1/jobs/job-1/retry', { method: 'POST', body: '{}' }))
      .rejects.toEqual(expect.objectContaining<Partial<APIError>>({ status: 403, errorCode: 'PERMISSION_DENIED' }))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(dispatchEvent).toHaveBeenCalledTimes(1)
  })

  it('shares one CSRF refresh between concurrent failed mutations', async () => {
    setCSRFToken('expired-token')
    let refreshCalls = 0
    const mutationCalls = new Map<string, number>()
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input)
      if (path === '/api/v1/auth/csrf') {
        refreshCalls++
        return envelope(200, 0, { csrf_token: 'fresh-token' })
      }
      const calls = (mutationCalls.get(path) ?? 0) + 1
      mutationCalls.set(path, calls)
      const token = new Headers(init?.headers).get('X-CSRF-Token')
      return token === 'fresh-token'
        ? envelope(200, 0, { path })
        : envelope(403, 40301, { error_code: 'CSRF_INVALID' }, 'CSRF 校验失败')
    })

    await Promise.all([
      api('/api/v1/jobs/job-1/retry', { method: 'POST', body: '{}' }),
      api('/api/v1/jobs/job-2/retry', { method: 'POST', body: '{}' }),
    ])

    expect(refreshCalls).toBe(1)
    expect(mutationCalls).toEqual(new Map([['/api/v1/jobs/job-1/retry', 2], ['/api/v1/jobs/job-2/retry', 2]]))
  })
})
