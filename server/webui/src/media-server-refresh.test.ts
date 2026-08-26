import { describe, expect, it } from 'vitest'
import { canTestMediaServerRefreshTarget } from '@/media-server-refresh'

describe('media-server refresh target permissions', () => {
  it('shows target testing only when both route permissions are present', () => {
    expect(canTestMediaServerRefreshTarget(true, true)).toBe(true)
    expect(canTestMediaServerRefreshTarget(true, false)).toBe(false)
    expect(canTestMediaServerRefreshTarget(false, true)).toBe(false)
  })
})
