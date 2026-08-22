import { describe, expect, it } from 'vitest'
import { playerClientLabel, playerDeviceConfirmation, playerDeviceListPath, playerDeviceRevokePath, playerDeviceTime } from '@/player-devices'

describe('Player device management boundary', () => {
  it('uses the browser-session management routes instead of Player bearer routes', () => {
    expect(playerDeviceListPath).toBe('/api/v1/player-devices')
    expect(playerDeviceRevokePath('device/with spaces')).toBe('/api/v1/player-devices/device%2Fwith%20spaces')
  })

  it('presents persisted activity without claiming that a device is currently online', () => {
    expect(playerClientLabel('player')).toBe('OhMyCine Player')
    expect(playerClientLabel('future-client')).toBe('OhMyCine 客户端')
    expect(playerDeviceTime('invalid')).toBe('时间未知')
  })

  it('makes the re-login consequence explicit before revocation', () => {
    expect(playerDeviceConfirmation({ name: '客厅 Player' })).toContain('需要重新登录 Server')
  })
})
