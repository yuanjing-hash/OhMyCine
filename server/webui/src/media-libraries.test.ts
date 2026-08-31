import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { defaultVideoExtensions, draftFromLibrary, emptyMediaLibraryDraft, isMediaLibraryDraftValid, mediaLibraryDraftFingerprint, mediaLibrarySourceDisplayPath, payloadFromDraft, presentLibraryStatus, supportsSTRM } from '@/media-libraries'
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

  it('omits legacy media-library intake writes in favor of downloader life-event listening', () => {
    const draft = emptyMediaLibraryDraft(1, 2)
    const payload = payloadFromDraft(draft, storage('pan115'))
    expect(payload).not.toHaveProperty('ingest_enabled')
    expect(payload).not.toHaveProperty('ingest_downloader_id')
    expect(payload).not.toHaveProperty('ingest_relative_root_token')
    expect(JSON.stringify(payload)).not.toContain('ingest_path')
  })

  it('manages one 115 default library without exposing a second intake directory', () => {
    const source = readFileSync(new URL('./views/MediaLibrariesView.vue', import.meta.url), 'utf8')
    expect(source).toContain('setDefaultIngestLibrary')
    expect(source).toContain('clearDefaultIngestLibrary')
    expect(source).toContain('自动监听默认入库库')
    expect(source).not.toContain('自动摄取的中转目录')
    expect(source).not.toContain('ingest_downloader_id')
  })

  it('presents initialization failure as an error state', () => {
    expect(presentLibraryStatus('initialization_failed')).toEqual({ label: '初始化失败', className: 'status-chip status-chip--error' })
  })

  it('tracks every editable persistent setting without treating picker tokens as configuration', () => {
    const target = { ...storage('pan115', true, true), capabilities: { ...storage('pan115', true, true).capabilities, small_file_upload: true } }
    const original = emptyMediaLibraryDraft(1, 2)
    original.name = '媒体库'
    original.source_path = '/媒体'
    const baseline = mediaLibraryDraftFingerprint(original, target)
    const mutations: Array<(draft: typeof original) => void> = [
      draft => { draft.name = '新名称' },
      draft => { draft.storage_id = 2 },
      draft => { draft.profile_id = 3 },
      draft => { draft.source_path = '/媒体/电视剧' },
      draft => { draft.enabled = false },
      draft => { draft.recursive = false },
      draft => { draft.full_scan_interval_hours = 48 },
      draft => { draft.incremental_minutes = 30 },
      draft => { draft.strm_asset_extra_extensions_text = 'png' },
      draft => { draft.ignore_patterns_text = 'sample' },
      draft => { draft.metadata_language = 'en-US' },
      draft => { draft.metadata_region = 'US' },
      draft => { draft.match_strategy = 'strict' },
      draft => { draft.provider_rate_per_second = 50 },
      draft => { draft.provider_concurrency = 3 },
      draft => { draft.metadata_rate_per_second = 4 },
      draft => { draft.metadata_concurrency = 2 },
      draft => { draft.metadata_artifacts_enabled = false },
      draft => { draft.upload_sidecars = true },
      draft => { draft.transfer_mode = 'copy' },
      draft => { draft.conflict_policy = 'skip' },
      draft => { draft.movie_directory_template = '电影/{title}' },
      draft => { draft.movie_filename_template = '{title}' },
      draft => { draft.tv_directory_template = '电视剧/{title}' },
      draft => { draft.tv_filename_template = '{title}-E{episode:02}' },
    ]
    for (const mutate of mutations) {
      const changed = { ...original }
      mutate(changed)
      expect(mediaLibraryDraftFingerprint(changed, target)).not.toBe(baseline)
    }

    const withTokens = { ...original, relative_root_token: 'opaque-source', strm_local_root_token: 'opaque-strm' }
    expect(mediaLibraryDraftFingerprint(withTokens, target)).toBe(baseline)

    const strm = { ...original, strm_enabled: true, strm_local_path: 'D:\\STRM' }
    const strmBaseline = mediaLibraryDraftFingerprint(strm, target)
    expect(strmBaseline).not.toBe(baseline)
    expect(mediaLibraryDraftFingerprint({ ...strm, strm_local_path: 'E:\\STRM' }, target)).not.toBe(strmBaseline)
  })

  it('disables saving for incomplete or out-of-range drafts', () => {
    const target = storage('pan115', true, true)
    const draft = emptyMediaLibraryDraft(1, 2)
    draft.name = '媒体库'
    draft.source_path = '/媒体'
    expect(isMediaLibraryDraftValid(draft, target)).toBe(true)
    expect(isMediaLibraryDraftValid({ ...draft, name: ' ' }, target)).toBe(false)
    expect(isMediaLibraryDraftValid({ ...draft, provider_concurrency: 0 }, target)).toBe(false)
    expect(isMediaLibraryDraftValid({ ...draft, strm_enabled: true, strm_local_path: '' }, target)).toBe(false)
  })

  it('rehydrates an authoritative saved detail without reusing consumed picker tokens', () => {
    const target = storage('pan115', true, true)
    const draft = emptyMediaLibraryDraft(1, 2)
    draft.relative_root_token = 'consumed-source-token'
    draft.strm_local_root_token = 'consumed-strm-token'
    const rehydrated = draftFromLibrary({
      ...draft,
      id: 9,
      storage_name: 'source',
      profile_name: 'profile',
      profile_revision: 1,
      auto_listen_default: false,
      video_extensions: defaultVideoExtensions,
      strm_asset_default_extensions: [],
      strm_asset_extra_extensions: [],
      strm_asset_effective_extensions: [],
      ignore_patterns: [],
      strm_local_path: 'D:\\STRM',
    } as never, target)
    expect(rehydrated.relative_root_token).toBe('')
    expect(rehydrated.strm_local_root_token).toBe('')
    expect(payloadFromDraft(rehydrated, target)).not.toHaveProperty('relative_root_token')
    expect(payloadFromDraft(rehydrated, target)).not.toHaveProperty('strm_local_root_token')
  })

  it('shows save lifecycle feedback beside the dirty-aware action', () => {
    const source = readFileSync(new URL('./views/MediaLibrariesView.vue', import.meta.url), 'utf8')
    expect(source).toContain('mediaLibraryDraftFingerprint')
    expect(source).toContain('replaceEditDraft(saved)')
    expect(source).toContain("state: 'saving', message: '正在保存媒体库配置…'")
    expect(source).toContain("state: 'success', message: '保存成功，新的媒体库配置已生效。'")
    expect(source).toContain(':disabled="saving || !editDirty || !editFormValid"')
    expect(source).toContain('aria-live="polite"')
  })

  it('shows persisted storage root and child roots as readable Windows locations', () => {
    const local = { ...storage('local'), root_path: 'D:\\Downloads\\115\\媒体', root_display_path: 'D:\\Downloads\\115\\媒体' }
    const rootLibrary = { relative_root: '/' } as Parameters<typeof mediaLibrarySourceDisplayPath>[0]
    const childLibrary = { relative_root: '/电视剧/国产' } as Parameters<typeof mediaLibrarySourceDisplayPath>[0]
    expect(mediaLibrarySourceDisplayPath(rootLibrary, local)).toBe('D:\\Downloads\\115\\媒体')
    expect(mediaLibrarySourceDisplayPath(childLibrary, local)).toBe('D:\\Downloads\\115\\媒体\\电视剧\\国产')
    expect(draftFromLibrary({
      ...rootLibrary,
      name: 'root',
      video_extensions: [],
      strm_asset_extra_extensions: [],
      ignore_patterns: [],
    } as never, local).source_path).toBe('D:\\Downloads\\115\\媒体')
  })
})
