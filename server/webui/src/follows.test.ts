import { describe, expect, it } from 'vitest'
import { APIError } from './api/client'
import { canSubmitFollow, cloneFollowSnapshot, followDefaultsPath, isFollowRevisionConflict, splitRuleText, type FollowExecutionSnapshot } from './follows'

const snapshot: FollowExecutionSnapshot = { version: 1, seasons: [1], site_ids: [2], downloader_id: 'd', media_library_id: 3, schedule: { kind: 'interval', minutes: 360 }, filters: { resolutions: [], video_codecs: [], qualities: [], include_keywords: [], exclude_keywords: [], release_groups: [], exclude_release_groups: [], min_seeders: 1, max_age_hours: null, min_size_bytes: null, max_size_bytes: null }, max_resources_per_run: 3, download_priority: 0 }
describe('follow contracts', () => {
  it('builds bounded safe defaults paths and rule arrays', () => { expect(followDefaultsPath(100)).toBe('/api/v1/follows/defaults?media_type=tv&tmdb_id=100'); expect(splitRuleText('WEB, WEB，HEVC\n')).toEqual(['WEB', 'HEVC']) })
  it('clones immutable snapshots and validates required bindings', () => { const clone = cloneFollowSnapshot(snapshot); clone.seasons.push(2); expect(snapshot.seasons).toEqual([1]); expect(canSubmitFollow({ snapshot, sites: [], downloaders: [], media_libraries: [], subscribed_seasons: [], coverage: {} as never }, snapshot)).toBe(true); expect(canSubmitFollow(null, snapshot)).toBe(false) })
  it('recognizes revision conflicts without treating other API failures as reload signals', () => { expect(isFollowRevisionConflict(new APIError(409, 'follow_revision_conflict', 'conflict'))).toBe(true); expect(isFollowRevisionConflict(new APIError(409, 'follow_season_conflict', 'conflict'))).toBe(false) })
})
