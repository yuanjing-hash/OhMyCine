import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { compatibleDownloadLibraries, downloadStatusLabel, formatBytes, formatETA, formatProgress, isDownloadHistoryTask, summarizeDownloaderTasks, torrentToBase64 } from '@/downloads'
import type { DownloaderSummary, DownloadTaskSummary, MediaLibraryDetail, StorageSummary } from '@/types/api'

const task = { job_status: 'queued' } as DownloadTaskSummary

describe('download presentation', () => {
  it('keeps unknown telemetry unknown instead of rendering zero', () => {
    expect(formatBytes(null)).toBe('未知')
    expect(formatProgress(null)).toBe('未知')
    expect(formatETA(null)).toBe('未知')
  })
  it('formats task facts and source bytes deterministically', () => {
    expect(formatBytes(1536, '/s')).toBe('1.5 KiB/s')
    expect(formatProgress(45.26)).toBe('45.3%')
    expect(formatETA(3661)).toBe('1 小时 2 分钟')
    expect(downloadStatusLabel(task)).toBe('排队中')
    expect(torrentToBase64(new Uint8Array([0, 1, 2]))).toBe('AAEC')
  })
  it('shows the safe preclassification stage while a worker is active', () => {
    expect(downloadStatusLabel({ job_status: 'running', phase: 'metadata' } as DownloadTaskSummary)).toBe('获取 metadata')
    expect(downloadStatusLabel({ job_status: 'running', phase: 'classifying' } as DownloadTaskSummary)).toBe('轻量刮削')
		expect(downloadStatusLabel({ job_status: 'waiting_user_action', phase: 'waiting_user_action' } as DownloadTaskSummary)).toBe('准备自动处理')
  })
  it('aggregates only active tasks for downloader card telemetry', () => {
    const tasks = [
      { downloader_id: 'a', job_status: 'running', progress: 25, download_speed: 100, upload_speed: null },
      { downloader_id: 'a', job_status: 'paused', progress: 75, download_speed: 50, upload_speed: 5 },
      { downloader_id: 'a', job_status: 'completed', progress: 100, download_speed: 0, upload_speed: 0 },
      { downloader_id: 'b', job_status: 'running', progress: 10, download_speed: 999, upload_speed: 1 },
    ] as DownloadTaskSummary[]
    expect(summarizeDownloaderTasks(tasks, 'a')).toEqual({ active: 1, total: 3, downloadSpeed: 100, uploadSpeed: null, averageProgress: 25 })
  })
  it('uses the server lifecycle scope instead of guessing from qBittorrent telemetry', () => {
    expect(isDownloadHistoryTask({ lifecycle_scope: 'history' } as DownloadTaskSummary)).toBe(true)
    expect(isDownloadHistoryTask({ lifecycle_scope: 'active' } as DownloadTaskSummary)).toBe(false)
  })
})

describe('115 offline downloader directory selection', () => {
  it('uses the shared Storage-scoped directory picker instead of a root-only dropdown', () => {
    const source = readFileSync(new URL('./views/DownloadsView.vue', import.meta.url), 'utf8')

    expect(source).toContain("openDownloaderPicker('create')")
    expect(source).toContain("openDownloaderPicker('edit')")
    expect(source).toContain(':restrict-to-storage="true"')
    expect(source).toContain(':storage-id="downloaderPickerStorageID"')
    expect(source).toContain('provider_directory_token')
  })

  it('offers only writable libraries in the downloader output boundary', () => {
    const capabilities = { network_drive: false, directory_list: true, watch: true, native_offline_download: true, temporary_direct_url: false, signed_proxy: false, change_cursor: true }
    const probe = { exists: true, readable: true, available: true, free_bytes: null, total_bytes: null, last_checked_at: '', error_code: '' }
    const storages = [
      { id: 1, name: 'local', type: 'local', connection_id: null, enabled: true, capabilities, probe },
      { id: 2, name: '115 source', type: 'pan115', connection_id: 10, enabled: true, capabilities, probe },
      { id: 3, name: '115 same account', type: 'pan115', connection_id: 10, enabled: true, capabilities, probe },
      { id: 4, name: '115 other account', type: 'pan115', connection_id: 20, enabled: true, capabilities, probe },
    ] as StorageSummary[]
    const libraries = [
      { id: 1, name: 'local', storage_id: 1, enabled: true, transfer_mode: 'move' },
      { id: 2, name: 'same', storage_id: 3, enabled: true, transfer_mode: 'copy', ingest_enabled: true, ingest_downloader_id: 'pan115' },
      { id: 3, name: 'other', storage_id: 4, enabled: true, transfer_mode: 'move' },
      { id: 4, name: 'legacy link', storage_id: 3, enabled: true, transfer_mode: 'symlink' },
    ] as MediaLibraryDetail[]
    const qbit = { type: 'qbittorrent', storage_id: null } as DownloaderSummary
    const pan115 = { id: 'pan115', type: 'pan115_offline', storage_id: 2 } as DownloaderSummary

    expect(compatibleDownloadLibraries(libraries, storages, qbit).map(item => item.id)).toEqual([1])
    expect(compatibleDownloadLibraries(libraries, storages, pan115).map(item => item.id)).toEqual([2])
    expect(compatibleDownloadLibraries(libraries, storages, pan115, true).map(item => item.id)).toEqual([2])
    expect(compatibleDownloadLibraries(libraries, storages, { ...pan115, id: 'other' }, true)).toEqual([])
  })

  it('renders separate native-offline and 115-share source entries', () => {
    const source = readFileSync(new URL('./views/DownloadsView.vue', import.meta.url), 'utf8')
    expect(source).toContain('value="share"')
    expect(source).toContain("source_kind: sourceMode.value === 'share' ? '115_share' : 'url'")
    expect(source).toContain('selectedTarget?.ingest_relative_root')
  })

  it('recovers completed recognition failures without presenting another download', () => {
    const source = readFileSync(new URL('./views/DownloadsView.vue', import.meta.url), 'utf8')

    expect(source).toContain('重新识别并入库')
    expect(source).toContain('不会重复下载')
    expect(source).toContain('/tmdb-candidates?')
    expect(source).toContain('/recognition-override')
    expect(source).toContain('验证并继续入库')
  })
})
