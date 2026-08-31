import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('115 recycle cleanup form contracts', () => {
  const source = readFileSync(new URL('./views/StorageView.vue', import.meta.url), 'utf8')

  it('keeps create credentials until validation, confirmation, and the request succeed', () => {
    const start = source.indexOf('async function createPan115()')
    const end = source.indexOf('async function saveLocal()', start)
    const create = source.slice(start, end)
    const request = create.indexOf("await api<ConnectionSummary>('/api/v1/connections'")
    const clear = create.indexOf('clearDraftCredentials()', request)

    expect(request).toBeGreaterThan(0)
    expect(clear).toBeGreaterThan(request)
    expect(create.slice(0, request)).not.toContain("cloudCreate.value.cookie = ''")
    expect(create.slice(0, request)).not.toContain("cloudCreate.value.recyclePassword = ''")
  })

  it('shows only stable cleanup status data and exposes the last error code', () => {
    expect(source).toContain('recycle_cleanup_last_status')
    expect(source).toContain('recycle_cleanup_last_run_at')
    expect(source).toContain('recycle_cleanup_next_run_at')
    expect(source).toContain('recycle_cleanup_last_error_code')
  })
})
