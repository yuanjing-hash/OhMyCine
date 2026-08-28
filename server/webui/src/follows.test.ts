import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { APIError } from './api/client'
import { canSubmitFollow, cloneFollowSnapshot, compatibleFollowDownloaders, compatibleFollowSites, followDefaultsPath, isFollowRevisionConflict, splitRuleText, type FollowDefaults, type FollowExecutionSnapshot } from './follows'

const snapshot: FollowExecutionSnapshot = { version: 1, seasons: [1], site_ids: [2], downloader_id: 'd', media_library_id: 3, schedule: { kind: 'interval', minutes: 360 }, filters: { resolutions: [], video_codecs: [], qualities: [], include_keywords: [], exclude_keywords: [], release_groups: [], exclude_release_groups: [], min_seeders: 1, max_age_hours: null, min_size_bytes: null, max_size_bytes: null }, max_resources_per_run: 3, download_priority: 0 }
describe('follow contracts', () => {
  it('builds bounded safe defaults paths and rule arrays', () => { expect(followDefaultsPath(100)).toBe('/api/v1/follows/defaults?media_type=tv&tmdb_id=100'); expect(splitRuleText('WEB, WEB，HEVC\n')).toEqual(['WEB', 'HEVC']) })
  it('clones immutable snapshots and validates route bindings', () => { const clone = cloneFollowSnapshot(snapshot); clone.seasons.push(2); expect(snapshot.seasons).toEqual([1]); const defaults: FollowDefaults = { snapshot, sites: [{ id: 2, name: 'Mikan', site_type: 'bt' }], downloaders: [{ id: 'd', name: '115', type: 'pan115_offline', connection_id: 8 }], media_libraries: [{ id: 3, name: '115 TV', storage_type: 'pan115', connection_id: 8 }], subscribed_seasons: [], coverage: {} as never }; expect(canSubmitFollow(defaults, snapshot)).toBe(true); expect(canSubmitFollow(null, snapshot)).toBe(false) })
  it('allows cross-source targets while keeping PT away from 115', () => { const defaults: FollowDefaults = { snapshot, sites: [{ id: 1, name: 'PT', site_type: 'pt' }, { id: 2, name: 'Mikan', site_type: 'bt' }], downloaders: [{ id: 'pan', name: '115', type: 'pan115_offline', connection_id: 8 }, { id: 'qbit', name: 'qBit', type: 'qbittorrent' }], media_libraries: [{ id: 3, name: '115 TV', storage_type: 'pan115', connection_id: 8 }, { id: 4, name: 'Other 115', storage_type: 'pan115', connection_id: 9 }, { id: 5, name: 'Local', storage_type: 'local' }], subscribed_seasons: [], coverage: {} as never }; expect(compatibleFollowDownloaders(defaults, 3).map(item => item.id)).toEqual(['pan', 'qbit']); expect(compatibleFollowDownloaders(defaults, 4).map(item => item.id)).toEqual(['pan', 'qbit']); expect(compatibleFollowSites(defaults, 3, 'pan').map(item => item.id)).toEqual([2]); expect(compatibleFollowSites(defaults, 5, 'qbit').map(item => item.id)).toEqual([1, 2]) })
  it('recognizes revision conflicts without treating other API failures as reload signals', () => { expect(isFollowRevisionConflict(new APIError(409, 'follow_revision_conflict', 'conflict'))).toBe(true); expect(isFollowRevisionConflict(new APIError(409, 'follow_season_conflict', 'conflict'))).toBe(false) })
  it('uses the same Server route preview as manual and discovery downloads', () => {
    const source = readFileSync(new URL('./components/FollowEditorDialog.vue', import.meta.url), 'utf8')
    expect(source).toContain('previewDownloadRoutes')
    expect(source).toContain('<DownloadRouteTargetPicker')
    expect(source).toContain("source_kind: 'url'")
    expect(source).toContain('selectedRoute.value?.enabled === true')
    expect(source).not.toContain('按媒体库顺序自动选择')
    expect(source).not.toContain('compatibleDownloadLibraries')
  })
})
