import type { MediaSourceOption } from './types'

export function describeMediaSource(source: MediaSourceOption): string {
  const sourceLabel = source.sourceLabel ? `来自 ${source.sourceLabel}` : undefined
  const deliveryLabel = source.deliveryKind === 'server_stream'
    ? '文件流'
    : source.deliveryKind === 'server_redirect'
      ? '302 直链'
      : undefined
  return [
    source.container?.toUpperCase(),
    source.size ? formatBytes(source.size) : undefined,
    sourceLabel,
    deliveryLabel,
    sourceLabel ? undefined : source.isStrm ? 'STRM' : undefined,
    sourceLabel ? undefined : source.isRemote ? '远程' : undefined,
  ].filter(Boolean).join(' · ') || source.name
}

export function hasMeaningfulMediaSource(source: MediaSourceOption): boolean {
  return Boolean(source.container || source.size || source.bitrate || source.sourceLabel || source.deliveryKind || source.isRemote || source.isStrm || (source.name && !/^default$|^source-\d+$/i.test(source.name)))
}

function formatBytes(value: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}
