import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { buildPTSearchQuery, cookieCloudErrorLabel, cookieCloudSettingsPath, cookieCloudSyncPath, discoverySearchOptionsPath, filterAndSortTorrentResults, ptRecognitionEngineVersion, ptRecognitionEpisodeLabel, ptRecognitionErrorLabel, ptRecognitionPath, ptRecognitionSpecLabels, readTorrentSearchSession, readTorrentSearchSiteSelection, saveTorrentSearchSession, saveTorrentSearchSiteSelection, siteCatalogPath, siteResolvePath, torrentRecognitionCandidatesPath, torrentRecognitionOverridePath, torrentRecognitionPath, torrentSearchPath, torrentSearchSessionKey, torrentSearchStreamPath, upsertPTGroup, type PTSearchGroup, type SearchSiteOption } from './sites'

const group = (siteID: number, page = 1): PTSearchGroup => ({ site_id: siteID, site_name: `site-${siteID}`, site_type: 'pt', status: 'success', page, has_next: false, skipped: 0, items: [{ token: `token-${siteID}-${page}`, title: 'Title', expires_at: '2026-08-24T00:00:00Z' }] })

describe('PT discovery contracts', () => {
  it('builds bounded public search parameters without credential fields', () => {
    const query = buildPTSearchQuery({ keyword: ' 七武士 ', mediaType: 'movie', year: 1954, page: 2, siteID: 7 })
    expect(query.get('keyword')).toBe('七武士')
    expect(query.get('site_id')).toBe('7')
    expect(query.toString()).not.toContain('cookie')
    expect(query.toString()).not.toContain('passkey')
  })

  it('replaces one site with its requested page without disturbing other site pages', () => {
    expect(upsertPTGroup([group(1)], group(2))).toHaveLength(2)
    const replaced = upsertPTGroup([group(1), group(2)], group(1, 2))
    expect(replaced.map(item => [item.site_id, item.page, item.items[0]?.token])).toEqual([
      [1, 2, 'token-1-2'],
      [2, 1, 'token-2-1'],
    ])
  })

  it('filters current-site cards with AND rules and defaults to seeder-descending order', () => {
    const pt = group(1, 2)
    pt.items = [
      { token: 'low', title: 'Low', seeders: 4, completed: 9, promotion: 'free', quality: '1080p', specifications: { resolution: '1080p' }, expires_at: '2026-08-25T00:10:00Z' },
      { token: 'high', title: 'High', seeders: 80, completed: 20, promotion: 'free', quality: '2160p', specifications: { resolution: '2160p' }, expires_at: '2026-08-25T00:10:00Z' },
      { token: 'mid', title: 'Mid', seeders: 20, completed: 15, promotion: 'free', quality: '2160p', specifications: { resolution: '2160p' }, expires_at: '2026-08-25T00:10:00Z' },
    ]
    const bt = { ...group(2), site_type: 'bt' as const, items: [{ token: 'bt', title: 'BT', seeders: 200, quality: '2160p', specifications: { resolution: '2160p' }, expires_at: '2026-08-25T00:10:00Z' }] }
    const values = filterAndSortTorrentResults([pt, bt], { activeChannel: 1, enabledSiteTypes: ['pt'], resolution: '2160p', promotion: 'free', minimumSeeders: 10, sort: 'seeders', direction: 'desc' })
    expect(values.map(entry => entry.item.token)).toEqual(['high', 'mid'])
  })

  it('binds repeated multi-site scope without allowing ambiguous widening', () => {
    const query = buildPTSearchQuery({ keyword: '七武士', siteIDs: [3, 1, 3] })
    expect(query.getAll('site_ids')).toEqual(['3', '1'])
    expect(query.has('site_id')).toBe(false)
    expect(() => buildPTSearchQuery({ keyword: '七武士', siteID: 1, siteIDs: [2] })).toThrow()
    expect(() => buildPTSearchQuery({ keyword: '七武士', siteIDs: [] })).toThrow()
  })

  it('selects all on first use and does not silently add new sites later', () => {
    const values = new Map<string, string>()
    const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) } }
    const options: SearchSiteOption[] = [
      { id: 1, name: 'PT', site_type: 'pt', health_status: 'online', searchable: true },
      { id: 2, name: 'BT', site_type: 'bt', health_status: 'offline', searchable: false, reason: 'unavailable' },
    ]
    expect(readTorrentSearchSiteSelection(storage, options)).toEqual([1])
    saveTorrentSearchSiteSelection(storage, [1])
    const expanded = [...options, { id: 3, name: 'New BT', site_type: 'bt' as const, health_status: 'online', searchable: true }]
    expect(readTorrentSearchSiteSelection(storage, expanded)).toEqual([1])
  })

  it('normalizes null result items at the wire boundary', () => {
    const nullItems = { ...group(1), items: null } as unknown as PTSearchGroup
    const normalized = upsertPTGroup([], nullItems)
    expect(normalized[0]?.items).toEqual([])
    expect(() => filterAndSortTorrentResults(normalized, { activeChannel: 'all', enabledSiteTypes: ['pt'], sort: 'seeders', direction: 'desc' })).not.toThrow()
  })

  it.each([
    { sort: 'seeders' as const, descending: ['large', 'middle', 'small'], ascending: ['small', 'middle', 'large'] },
    { sort: 'size' as const, descending: ['large', 'middle', 'small'], ascending: ['small', 'middle', 'large'] },
    { sort: 'published' as const, descending: ['large', 'middle', 'small'], ascending: ['small', 'middle', 'large'] },
  ])('sorts $sort in either direction', ({ sort, descending, ascending }) => {
    const values = group(1)
    values.items = [
      { token: 'middle', title: 'Middle', seeders: 20, size_bytes: 20, published_at: '2026-08-20T00:00:00Z', expires_at: '2026-08-25T00:10:00Z' },
      { token: 'small', title: 'Small', seeders: 10, size_bytes: 10, published_at: '2026-08-10T00:00:00Z', expires_at: '2026-08-25T00:10:00Z' },
      { token: 'large', title: 'Large', seeders: 30, size_bytes: 30, published_at: '2026-08-23T00:00:00Z', expires_at: '2026-08-25T00:10:00Z' },
    ]
    const filters = { activeChannel: 'all' as const, enabledSiteTypes: ['pt'] as const, sort }
    expect(filterAndSortTorrentResults([values], { ...filters, direction: 'desc' }).map(entry => entry.item.token)).toEqual(descending)
    expect(filterAndSortTorrentResults([values], { ...filters, direction: 'asc' }).map(entry => entry.item.token)).toEqual(ascending)
  })

  it('keeps deterministic tie breakers independent of sort direction', () => {
    const first = group(2)
    first.items = [{ token: 'z', title: 'Same', seeders: 10, completed: 5, published_at: '2026-08-20T00:00:00Z', expires_at: '2026-08-25T00:10:00Z' }]
    const second = group(1)
    second.items = [{ token: 'a', title: 'Same', seeders: 10, completed: 5, published_at: '2026-08-20T00:00:00Z', expires_at: '2026-08-25T00:10:00Z' }]
    const filters = { activeChannel: 'all' as const, enabledSiteTypes: ['pt'] as const, sort: 'seeders' as const }
    expect(filterAndSortTorrentResults([first, second], { ...filters, direction: 'desc' }).map(entry => entry.item.token)).toEqual(['a', 'z'])
    expect(filterAndSortTorrentResults([first, second], { ...filters, direction: 'asc' }).map(entry => entry.item.token)).toEqual(['a', 'z'])
  })

  it('keeps missing or invalid sort values after known values in either direction', () => {
    const values = group(1)
    values.items = [
      { token: 'missing', title: 'Missing', expires_at: '2026-08-25T00:10:00Z' },
      { token: 'invalid', title: 'Invalid', published_at: 'not-a-date', expires_at: '2026-08-25T00:10:00Z' },
      { token: 'known', title: 'Known', seeders: 0, size_bytes: 0, published_at: '2026-08-20T00:00:00Z', expires_at: '2026-08-25T00:10:00Z' },
    ]
    for (const sort of ['seeders', 'size', 'published'] as const) {
      for (const direction of ['asc', 'desc'] as const) {
        const tokens = filterAndSortTorrentResults([values], { activeChannel: 'all', enabledSiteTypes: ['pt'], sort, direction }).map(entry => entry.item.token)
        expect(tokens[0]).toBe('known')
      }
    }
  })

  it('keeps TMDB identity search and CookieCloud management on explicit server routes', () => {
    const query = buildPTSearchQuery({ keyword: 'Seven Samurai', tmdbID: 346, searchBy: 'tmdb_id' })
    expect(query.get('tmdb_id')).toBe('346')
    expect(query.get('search_by')).toBe('tmdb_id')
    expect(cookieCloudSettingsPath).toBe('/api/v1/settings/sites/cookiecloud')
    expect(cookieCloudSyncPath).toBe('/api/v1/settings/sites/cookiecloud/sync')
    expect(siteCatalogPath).toBe('/api/v1/sites/catalog')
    expect(siteResolvePath).toBe('/api/v1/sites/resolve')
    expect(ptRecognitionPath).toBe('/api/v1/discovery/pt-results/recognize')
    expect(torrentSearchPath).toBe('/api/v1/discovery/torrent-search')
    expect(torrentSearchStreamPath).toBe('/api/v1/discovery/torrent-search/stream')
    expect(torrentRecognitionPath).toBe('/api/v1/discovery/torrent-results/recognize')
    expect(torrentRecognitionCandidatesPath).toBe('/api/v1/discovery/torrent-results/tmdb-candidates')
    expect(torrentRecognitionOverridePath).toBe('/api/v1/discovery/torrent-results/recognition-override')
    expect(discoverySearchOptionsPath).toBe('/api/v1/discovery/search-options')
  })

  it('binds cached results to the fixed single-site scope', () => {
    const values = new Map<string, string>()
    const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) } }
    const now = Date.parse('2026-08-25T00:00:00Z')
    const scoped = group(7)
    scoped.items[0].expires_at = '2026-08-25T00:10:00Z'
    saveTorrentSearchSession(storage, { input: { keyword: 'anime', mediaType: 'tv', searchBy: 'title', siteID: 7 }, groups: [scoped], recognitions: {}, searched: true, savedAt: now })
    expect(readTorrentSearchSession(storage, now)?.input.siteID).toBe(7)
  })

  it('drops legacy cached searches without an explicit site scope', () => {
    const values = new Map<string, string>()
    const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) } }
    const now = Date.parse('2026-08-25T00:00:00Z')
    const legacy = group(7)
    legacy.items[0].expires_at = '2026-08-25T00:10:00Z'
    saveTorrentSearchSession(storage, { input: { keyword: 'anime', mediaType: 'tv', searchBy: 'title' }, groups: [legacy], recognitions: {}, searched: true, savedAt: now })
    expect(readTorrentSearchSession(storage, now)).toBeNull()
    expect(values.has(torrentSearchSessionKey)).toBe(false)
  })

  it('uses address-driven BT adding and a fixed single-site search route', () => {
    const sitesView = readFileSync(new URL('./views/SitesView.vue', import.meta.url), 'utf8')
    const exploreView = readFileSync(new URL('./views/ExploreView.vue', import.meta.url), 'utf8')
    expect(sitesView).toContain("form.value.kind = 'auto_bt'")
    expect(sitesView).toContain('siteResolvePath')
    expect(sitesView).toContain("query: { site_id: String(site.id) }")
    expect(sitesView).toContain("const publicRenderedKinds = new Set(['1337x', 'extto'])")
    expect(sitesView).toContain('OMC_CLOAKBROWSER_COMPANION_URL')
    expect(sitesView).toContain('OhMyCine 不附带、下载或重新分发其浏览器二进制')
    expect(sitesView).toContain('不会把 PT Cookie 或 passkey 交给 FlareSolverr')
    expect(sitesView).not.toContain('把目标站点 URL 和该站 Cookie 发送给此服务')
    expect(sitesView).not.toContain('选择 Nyaa 等内建公开索引')
    expect(exploreView).toContain('lockedSiteID')
    expect(exploreView).toContain('当前处于单站搜索模式')
    expect(exploreView).toContain('void searchJSON(lockedSiteID.value)')
  })

  it('presents shared recognition specifications without accepting a raw title or torrent URL', () => {
    expect(ptRecognitionSpecLabels({ resolution: '2160p', source: 'UHD BluRay REMUX', video_codec: 'H.265/HEVC', audio_codec: 'DTS-HD', hdr: 'HDR10 / Dolby Vision', release_group: 'GROUP' }))
      .toEqual(['2160p', 'UHD BluRay REMUX', 'H.265/HEVC', 'DTS-HD', 'HDR10 / Dolby Vision', '压制组 GROUP'])
    expect(ptRecognitionPath).not.toContain('torrent')
    expect(ptRecognitionErrorLabel('tmdb_no_match')).toContain('未找到可信匹配')
    expect(ptRecognitionErrorLabel('tmdb_credential_unavailable')).toContain('仅显示本地解析结果')
    expect(ptRecognitionEpisodeLabel({ engine_version: ptRecognitionEngineVersion, status: 'matched', title: '奥美迦奥特曼', media_type: 'tv', episodes: { season: 1, episode_min: 9, episode_max: 9, count: 1 }, specifications: {} })).toBe('第 1 季 · 第 9 集')
    expect(ptRecognitionEpisodeLabel({ engine_version: ptRecognitionEngineVersion, status: 'matched', title: '迪迦奥特曼', media_type: 'tv', episodes: { season: 1, episode_min: 1, episode_max: 52, count: 52 }, specifications: {} })).toBe('第 1 季 · 第 1–52 集 · 多集（共 52 集）')
    expect(ptRecognitionEpisodeLabel({ engine_version: ptRecognitionEngineVersion, status: 'matched', title: '爱情公寓', media_type: 'tv', episodes: { season: 3, season_year: 2012 }, specifications: {} })).toBe('第 3 季（2012）')
    expect(ptRecognitionEpisodeLabel({ engine_version: ptRecognitionEngineVersion, status: 'matched', title: '外传', media_type: 'movie', episodes: { episode_min: 1 }, specifications: {} })).toBe('')
  })

  it('renders safe CookieCloud site failure codes as actionable Chinese text', () => {
    expect(cookieCloudErrorLabel('site_authentication_failed')).toBe('站点登录 Cookie 已失效或不完整')
    expect(cookieCloudErrorLabel('site_unavailable')).toBe('站点暂时不可用')
    expect(cookieCloudErrorLabel('future_safe_code')).toBe('future_safe_code')
  })

  it('keeps manual recognition explicit and binds only a verified TMDB identity before download', () => {
    const source = readFileSync(new URL('./views/ExploreView.vue', import.meta.url), 'utf8')
    expect(source).toContain('>Search</p>')
    expect(source).toContain('>直接搜索</h1>')
    expect(source).toContain('v-model="resultDirection"')
    expect(source).toContain('<option value="desc">降序</option><option value="asc">升序</option>')
    expect(source).toContain('手动识别')
    expect(source).toContain('自动识别失败也可以在这里修改关键词')
    expect(source).toContain('torrentRecognitionCandidatesPath')
    expect(source).toContain('torrentRecognitionOverridePath')
    expect(source).toContain('result_token: item.token')
    expect(source).toContain('tmdb_id: candidate.id')
    expect(source).toContain('media_type: candidate.media_type')
    expect(source).not.toContain('torrent_id')
    expect(source).toContain("'检测'")
    expect(source).toContain('>手动检测</button>')
    expect(source).toContain('>入库</button>')
  })

  it('keeps the latest unexpired search in session storage without retaining stale claims', () => {
    const values = new Map<string, string>()
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { values.set(key, value) },
      removeItem: (key: string) => { values.delete(key) },
    }
    const now = Date.parse('2026-08-25T00:00:00Z')
    const fresh = group(1)
    fresh.items[0].expires_at = '2026-08-25T00:10:00Z'
    const stale = group(2)
    stale.items[0].expires_at = '2026-08-24T23:59:00Z'
    saveTorrentSearchSession(storage, {
      input: { keyword: '迪迦奥特曼', mediaType: 'tv', searchBy: 'title', siteIDs: [1, 2] },
      groups: [fresh, stale],
      recognitions: { [fresh.items[0].token]: { engine_version: ptRecognitionEngineVersion, status: 'matched', manual_override: true, title: '迪迦奥特曼', media_type: 'tv', episodes: { episode_min: 1, episode_max: 52, count: 52 }, specifications: {} } },
      searched: true,
      savedAt: now,
    })
    const restored = readTorrentSearchSession(storage, now)
    expect(restored?.input.keyword).toBe('迪迦奥特曼')
    expect(restored?.groups[0].items).toHaveLength(1)
    expect(restored?.groups[1].items).toHaveLength(0)
    expect(restored?.recognitions[fresh.items[0].token]?.status).toBe('matched')
    expect(restored?.recognitions[fresh.items[0].token]?.manual_override).toBe(true)
    expect(restored?.recognitions[fresh.items[0].token]?.episodes?.episode_max).toBe(52)
    expect(values.has(torrentSearchSessionKey)).toBe(true)
  })

  it('keeps cached search results but drops recognition output from an older engine', () => {
    const values = new Map<string, string>()
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { values.set(key, value) },
      removeItem: (key: string) => { values.delete(key) },
    }
    const now = Date.parse('2026-08-25T00:00:00Z')
    const fresh = group(1)
    fresh.items[0].expires_at = '2026-08-25T00:10:00Z'
    values.set(torrentSearchSessionKey, JSON.stringify({
      input: { keyword: '迪迦奥特曼', mediaType: 'tv', searchBy: 'title', siteIDs: [1] },
      groups: [fresh],
      recognitions: { [fresh.items[0].token]: { engine_version: 'older-engine', status: 'unrecognized', error_code: 'tmdb_no_match', title: 'Ultraman Tiga', specifications: {} } },
      searched: true,
      savedAt: now,
    }))
    const restored = readTorrentSearchSession(storage, now)
    expect(restored?.groups[0].items).toHaveLength(1)
    expect(restored?.recognitions).toEqual({})
  })

  it('caps restored search results across all groups', () => {
    const values = new Map<string, string>()
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { values.set(key, value) },
      removeItem: (key: string) => { values.delete(key) },
    }
    const now = Date.parse('2026-08-25T00:00:00Z')
    const groups = [1, 2].map(siteID => ({
      ...group(siteID),
      items: Array.from({ length: 200 }, (_, index) => ({ token: `${siteID}-${index}`, title: `Title ${index}`, expires_at: '2026-08-25T00:10:00Z' })),
    }))
    values.set(torrentSearchSessionKey, JSON.stringify({
      input: { keyword: 'test', mediaType: '', searchBy: 'title', siteIDs: [1, 2] }, groups, recognitions: {}, searched: true, savedAt: now,
    }))
    const restored = readTorrentSearchSession(storage, now)
    expect(restored?.groups.reduce((total, item) => total + item.items.length, 0)).toBe(300)
  })
})
