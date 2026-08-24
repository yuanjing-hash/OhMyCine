import { describe, expect, it } from 'vitest'
import { buildPTSearchQuery, cookieCloudErrorLabel, cookieCloudSettingsPath, cookieCloudSyncPath, upsertPTGroup, type PTSearchGroup } from './sites'

const group = (siteID: number, page = 1): PTSearchGroup => ({ site_id: siteID, site_name: `site-${siteID}`, status: 'success', page, has_next: false, skipped: 0, items: [{ token: `token-${siteID}-${page}`, title: 'Title', expires_at: '2026-08-24T00:00:00Z' }] })

describe('PT discovery contracts', () => {
  it('builds bounded public search parameters without credential fields', () => {
    const query = buildPTSearchQuery({ keyword: ' 七武士 ', mediaType: 'movie', year: 1954, page: 2, siteID: 7 })
    expect(query.get('keyword')).toBe('七武士')
    expect(query.get('site_id')).toBe('7')
    expect(query.toString()).not.toContain('cookie')
    expect(query.toString()).not.toContain('passkey')
  })

  it('replaces streaming site groups and appends explicit next pages', () => {
    expect(upsertPTGroup([group(1)], group(2))).toHaveLength(2)
    const appended = upsertPTGroup([group(1)], group(1, 2), true)
    expect(appended[0].items.map(item => item.token)).toEqual(['token-1-1', 'token-1-2'])
  })

  it('keeps TMDB identity search and CookieCloud management on explicit server routes', () => {
    const query = buildPTSearchQuery({ keyword: 'Seven Samurai', tmdbID: 346, searchBy: 'tmdb_id' })
    expect(query.get('tmdb_id')).toBe('346')
    expect(query.get('search_by')).toBe('tmdb_id')
    expect(cookieCloudSettingsPath).toBe('/api/v1/settings/sites/cookiecloud')
    expect(cookieCloudSyncPath).toBe('/api/v1/settings/sites/cookiecloud/sync')
  })

  it('renders safe CookieCloud site failure codes as actionable Chinese text', () => {
    expect(cookieCloudErrorLabel('site_authentication_failed')).toBe('PTTime 登录 Cookie 已失效或不完整')
    expect(cookieCloudErrorLabel('site_unavailable')).toBe('PTTime 站点暂时不可用')
    expect(cookieCloudErrorLabel('future_safe_code')).toBe('future_safe_code')
  })
})
