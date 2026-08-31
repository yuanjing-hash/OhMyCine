import type { MpvRenderState, VideoAspectMode, VideoFitMode } from '@/composables/useMpv'
import { redactSensitiveText } from '@/services/datasource/errors'

export function safePlayerMenuText(value: unknown, fallback: string, maxLength = 120): string {
  if (value == null)
    return fallback

  const text = redactSensitiveText(value).replace(/\s+/g, ' ').trim()
  if (!text || containsUnsafeDisplayToken(text))
    return fallback

  return text.length > maxLength ? `${text.slice(0, Math.max(0, maxLength - 1))}…` : text
}

export function playerRenderStatusLabel(status: MpvRenderState['status']): string {
  const labels: Record<MpvRenderState['status'], string> = {
    idle: '待播放',
    initializing: '准备中',
    ready: '已就绪',
    unsupported: '暂不可用',
    error: '需要重试',
  }
  return labels[status]
}

export function playerRenderBackendLabel(backend: MpvRenderState['backend']): string {
  const labels: Record<MpvRenderState['backend'], string> = {
    windowsTransparentOverlay: 'Windows 透明叠层',
    windowsOpenGl: 'Windows OpenGL',
    androidSurface: 'Android SurfaceView',
    linuxFuture: 'Linux 预留 backend',
    macosFuture: 'macOS 预留 backend',
    mobileFuture: '移动端预留 backend',
    unsupported: '暂不支持',
  }
  return labels[backend]
}

export function playerVideoAspectLabel(mode: VideoAspectMode): string {
  if (mode === '16:9')
    return '16:9'
  if (mode === '4:3')
    return '4:3'
  if (mode === 'cinema')
    return '2.35:1'
  return '原始比例'
}

export function playerVideoFitLabel(mode: VideoFitMode): string {
  if (mode === 'crop')
    return '填充裁切'
  if (mode === 'cinemaCrop')
    return '影院裁切'
  return '适应窗口'
}

export function compactPlayerTrackLabel(parts: Array<string | number | null | undefined>, fallback: string): string {
  const label = parts
    .filter(part => part != null && String(part).trim().length > 0)
    .map(part => String(part).trim())
    .join(' · ')
  return safePlayerMenuText(label, fallback, 72)
}

export function formatPlaybackTime(totalSeconds: number): string {
  const seconds = Math.max(0, Math.floor(totalSeconds))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = seconds % 60

  if (hours > 0)
    return `${hours}:${minutes.toString().padStart(2, '0')}:${rest.toString().padStart(2, '0')}`

  return `${minutes}:${rest.toString().padStart(2, '0')}`
}

export function videoAspectRatioValue(mode: VideoAspectMode): number | null {
  if (mode === '16:9')
    return 16 / 9
  if (mode === '4:3')
    return 4 / 3
  if (mode === 'cinema')
    return 2.35
  return null
}

function looksLikeMediaFilename(value: string): boolean {
  return /\.(?:3g2|3gp|avi|flv|m2ts|m4v|mkv|mov|mp4|mpeg|mpg|mts|ogm|ogv|rmvb|ts|webm|wmv)(?:$|[\s"')，。])/i.test(value)
}

function containsUnsafeDisplayToken(value: string): boolean {
  const normalized = value.trim()
  return /^https?:\/\//i.test(normalized)
    || /^[a-z]:[\\/]/i.test(normalized)
    || normalized.startsWith('\\\\')
    || normalized.startsWith('/')
    || normalized.startsWith('~/')
    || /\b(?:[a-z]:[\\/]|file:\/\/|https?:\/\/)/i.test(normalized)
    || /(?:^|[\s"'({])\/(?:[^/\s?#"')]+\/)+[^/\s?#"')]+/.test(normalized)
    || /(?:^|[\s"'({])\\\\[^\\/\s]+[\\/][^\\/\s]+/.test(normalized)
    || /\b(?:\d{1,3}\.){3}\d{1,3}\b/.test(normalized)
    || (!looksLikeMediaFilename(normalized) && /\b(?:localhost|(?:[a-z0-9-]+\.)+[a-z]{2,})(?::\d{2,5})?\b/i.test(normalized))
}
