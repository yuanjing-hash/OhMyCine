import type { PlayerDeviceSummary } from '@/types/api'

export const playerDeviceListPath = '/api/v1/player-devices'

export function playerDeviceRevokePath(deviceID: string): string {
  return `${playerDeviceListPath}/${encodeURIComponent(deviceID)}`
}

export function playerClientLabel(clientKind: string): string {
  return clientKind.trim().toLowerCase() === 'player' ? 'OhMyCine Player' : 'OhMyCine 客户端'
}

export function playerDeviceTime(value: string): string {
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? '时间未知' : time.toLocaleString('zh-CN', { hour12: false })
}

export function playerDeviceConfirmation(device: Pick<PlayerDeviceSummary, 'name'>): string {
  return `确认撤销设备“${device.name}”的安全配对？撤销后，该 Player 需要重新登录 Server。`
}
