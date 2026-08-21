import { describe, expect, it } from 'vitest'
import { emptyMediaLibraryDraft, payloadFromDraft, presentLibraryStatus, supportsSTRM } from '@/media-libraries'
import type { StorageSummary } from '@/types/api'

function storage(type: string, direct = false, signed = false): StorageSummary {
  return { id: 1, name: 'source', type: type as StorageSummary['type'], root_path: 'hidden', connection_id: null, enabled: true,
    capabilities: { network_drive: false, directory_list: true, watch: true, native_offline_download: false, temporary_direct_url: direct, signed_proxy: signed, change_cursor: false },
    probe: { exists: true, readable: true, available: true, free_bytes: null, total_bytes: null, last_checked_at: '', error_code: '' }, created_at: '', updated_at: '' }
}

describe('media library form boundary', () => {
  it('submits an opaque relative-root token without reconstructing a path', () => {
    const draft = emptyMediaLibraryDraft(1, 2)
    draft.relative_root_token = 'opaque-selection'
    draft.source_path = 'D:\\Media\\Movies'
    const payload = payloadFromDraft(draft, storage('local'))
    expect(payload.relative_root_token).toBe('opaque-selection')
    expect(payload.relative_root).toBeUndefined()
    expect(payload).toMatchObject({ transfer_mode: 'move', conflict_policy: 'ask', movie_filename_template: '{title} ({year})', tv_filename_template: '{title} - S{season:02}E{episode:02}' })
    expect(JSON.stringify(payload)).not.toContain('D:\\Media')
  })

  it('strips STRM state and its picker token for local sources', () => {
    const draft = emptyMediaLibraryDraft(1, 2)
    draft.strm_enabled = true
    draft.strm_local_root_token = 'must-not-leak'
    expect(supportsSTRM(storage('local', true, true))).toBe(false)
    expect(payloadFromDraft(draft, storage('local'))).toMatchObject({ strm_enabled: false })
    expect(payloadFromDraft(draft, storage('local'))).not.toHaveProperty('strm_local_root_token')
  })

  it('requires both direct URL and signed proxy capabilities for cloud STRM', () => {
    expect(supportsSTRM(storage('cloud', true, false))).toBe(false)
    expect(supportsSTRM(storage('cloud', true, true))).toBe(true)
  })

  it('does not submit symlink mode for a 115 media library', () => {
    const draft = emptyMediaLibraryDraft(1, 2)
    draft.transfer_mode = 'symlink'
    expect(payloadFromDraft(draft, storage('pan115')).transfer_mode).toBe('move')
  })

  it('submits the 115 intake token only while intake is enabled', () => {
    const draft = emptyMediaLibraryDraft(1, 2)
    draft.ingest_enabled = true
    draft.ingest_downloader_id = 'downloader-115'
    draft.ingest_relative_root_token = 'opaque-intake'
    draft.ingest_path = '/中转'
    const payload = payloadFromDraft(draft, storage('pan115'))
    expect(payload).toMatchObject({ ingest_enabled: true, ingest_downloader_id: 'downloader-115', ingest_relative_root_token: 'opaque-intake' })
    expect(JSON.stringify(payload)).not.toContain('ingest_path')

    draft.ingest_enabled = false
    expect(payloadFromDraft(draft, storage('pan115'))).toMatchObject({ ingest_enabled: false })
    expect(payloadFromDraft(draft, storage('pan115'))).not.toHaveProperty('ingest_relative_root_token')
  })

  it('presents initialization failure as an error state', () => {
    expect(presentLibraryStatus('initialization_failed')).toEqual({ label: '初始化失败', className: 'status-chip status-chip--error' })
  })
})
