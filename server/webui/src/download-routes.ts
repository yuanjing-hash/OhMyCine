import { api } from '@/api/client'

export type DownloadRouteKind = 'same_source_local' | 'same_source_provider' | 'cross_source' | string

export interface DownloadRouteTargetOption {
  media_library_id: number
  library_name: string
  storage_name: string
  route_kind: DownloadRouteKind
  route_label: string
  enabled: boolean
  reason_code: string
  reason_message: string
  requires_managed_staging: boolean
  expected_bytes?: number | null
  required_bytes?: number | null
  available_bytes?: number | null
}

export interface DownloadRoutePreview {
  downloader_id: string
  source_kind: string
  options: DownloadRouteTargetOption[]
}

export interface DownloadRoutePreviewInput {
  downloader_id: string
  source_kind: string
  site_id?: number
  expected_bytes?: number
}

export async function previewDownloadRoutes(input: DownloadRoutePreviewInput, signal?: AbortSignal) {
  return api<DownloadRoutePreview>('/api/v1/download-routes/preview', {
    method: 'POST',
    body: JSON.stringify(input),
    signal,
  })
}

export function enabledRouteTargets(preview: DownloadRoutePreview | null) {
  return preview?.options.filter(item => item.enabled) ?? []
}

export function routeTargetByID(preview: DownloadRoutePreview | null, mediaLibraryID: number) {
  return preview?.options.find(item => item.media_library_id === mediaLibraryID) ?? null
}

export function formatRouteBytes(value: number | null | undefined) {
  if (value == null) return '未知'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = Math.max(0, value)
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1 }
  return `${size >= 10 || unit === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[unit]}`
}
