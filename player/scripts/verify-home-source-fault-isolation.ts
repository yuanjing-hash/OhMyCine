import assert from 'node:assert/strict'
import { collectHomeSectionsFromSources } from '../src/services/datasource/homeAggregation.ts'
import type { DataSource, DataSourceConfig, DataSourceType, HomeSection, MediaDetail, MediaItem } from '../src/services/datasource/types.ts'

const embyHeroItem: MediaItem = {
  id: 'emby-movie-1',
  sourceId: 'emby-home',
  name: 'Emby Movie',
  type: 'movie',
  path: 'emby-movie-1',
  posterUrl: 'https://example.test/poster.jpg',
}

const embySource = createFakeSource({
  id: 'emby-home',
  name: '家庭 Emby',
  type: 'emby',
  getHomeSections: async () => [
    {
      id: 'hero-emby-home',
      sourceId: 'emby-home',
      title: '家庭 Emby 精选',
      type: 'hero',
      items: [embyHeroItem],
    },
    {
      id: 'continue-emby-home',
      sourceId: 'emby-home',
      title: '家庭 Emby 继续观看',
      type: 'continueWatching',
      items: [{ ...embyHeroItem, id: 'emby-resume-1', resumePosition: 120 }],
    },
    {
      id: 'recent-emby-home',
      sourceId: 'emby-home',
      title: '家庭 Emby 最新入库',
      type: 'recentlyAdded',
      items: [{ ...embyHeroItem, id: 'emby-recent-1' }],
    },
  ],
})

const failingOpenListSource = createFakeSource({
  id: 'alist-broken',
  name: 'OpenList/Alist',
  type: 'alist',
  getHomeSections: async () => {
    throw new Error('TMDB unavailable or source scan failed')
  },
})

const sections = await collectHomeSectionsFromSources([failingOpenListSource, embySource])

assert.equal(sections.some(section => section.id.startsWith('error-')), false)
assert.equal(sections.find(section => section.type === 'hero')?.items[0]?.sourceId, 'emby-home')
assert.equal(sections.find(section => section.type === 'continueWatching')?.items[0]?.sourceId, 'emby-home')
assert.equal(sections.find(section => section.type === 'recentlyAdded')?.items[0]?.sourceId, 'emby-home')

console.log(JSON.stringify({
  sectionCount: sections.length,
  heroSourceId: sections.find(section => section.type === 'hero')?.sourceId,
  continueSourceId: sections.find(section => section.type === 'continueWatching')?.sourceId,
  latestSourceId: sections.find(section => section.type === 'recentlyAdded')?.sourceId,
}, null, 2))

function createFakeSource(input: {
  id: string
  name: string
  type: DataSourceType
  getHomeSections?: () => Promise<HomeSection[]>
}): DataSource {
  const config: DataSourceConfig = {
    id: input.id,
    type: input.type,
    name: input.name,
    order: 0,
    url: 'https://example.test',
    enabled: true,
  }

  return {
    id: input.id,
    name: input.name,
    type: input.type,
    isConnected: true,
    init: async () => {},
    test: async () => true,
    destroy: () => {},
    list: async () => [],
    listLibraries: async () => [],
    getHomeSections: input.getHomeSections,
    search: async () => [],
    getDetail: async (id: string): Promise<MediaDetail> => ({
      id,
      sourceId: input.id,
      name: input.name,
      type: 'movie',
      path: id,
    }),
    getStreamURL: async (id: string) => id,
    exportConfig: () => config,
  }
}
