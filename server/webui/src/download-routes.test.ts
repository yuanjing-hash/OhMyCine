import { describe, expect, it } from 'vitest'
import { enabledRouteTargets, formatRouteBytes, routeTargetByID, type DownloadRoutePreview } from '@/download-routes'

const preview: DownloadRoutePreview = {
  downloader_id: 'qbit', source_kind: 'torrent', options: [
    { media_library_id: 1, library_name: '本地', storage_name: 'NAS', route_kind: 'same_source_local', route_label: '本地整理', enabled: true, reason_code: '', reason_message: '', requires_managed_staging: false, expected_bytes: null, required_bytes: null, available_bytes: null },
    { media_library_id: 2, library_name: '115', storage_name: '115', route_kind: 'cross_source', route_label: '跨数据源', enabled: false, reason_code: 'staging_space_insufficient', reason_message: '暂存空间不足', requires_managed_staging: true, expected_bytes: 1024, required_bytes: 2048, available_bytes: 1024 },
  ],
}

describe('authoritative download route preview', () => {
  it('keeps disabled targets visible while selecting only executable routes', () => {
    expect(preview.options).toHaveLength(2)
    expect(enabledRouteTargets(preview).map(item => item.media_library_id)).toEqual([1])
    expect(routeTargetByID(preview, 2)?.reason_code).toBe('staging_space_insufficient')
  })

  it('formats safe nullable space facts without inventing zero', () => {
    expect(formatRouteBytes(null)).toBe('未知')
    expect(formatRouteBytes(1536)).toBe('1.5 KiB')
  })
})
