// @vitest-environment happy-dom

import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { afterEach, describe, expect, it, vi } from 'vitest'
import DiscoveryDetailView from '@/views/DiscoveryDetailView.vue'

const { apiMock, routeMock, routerMock } = vi.hoisted(() => ({
  apiMock: vi.fn(),
  routeMock: { fullPath: '/discovery/details/tmdb/tv/100', params: { provider: 'tmdb', mediaType: 'tv', providerID: '100' } },
  routerMock: { back: vi.fn(), push: vi.fn() },
}))

vi.mock('@/api/client', () => ({ api: apiMock }))
vi.mock('vue-router', () => ({ useRoute: () => routeMock, useRouter: () => routerMock }))

const detail = {
  work: { provider: 'tmdb', provider_id: '100', media_type: 'tv', title: '测试剧', tmdb_id: 100 },
  genres: [],
  countries: [],
  spoken_languages: [],
  studios: [],
  directors: [],
  writers: [],
  cast: [],
  backdrop_urls: [],
  recommendations: [],
  similar: [],
  resolved_from_tmdb: true,
}

const nullableCoverage = {
  media_type: 'tv',
  tmdb_id: 100,
  title: '测试剧',
  status: 'missing',
  libraries: [],
  freshness: { checked_at: '2026-08-28T00:00:00Z', library_scan_state: 'complete', tmdb_state: 'complete' },
  tv: {
    counts: { total: 8, present: 0, missing: 8, future: 0, unknown: 0 },
    seasons: [
      {
        season_number: 1,
        name: '第 1 季',
        special: false,
        status: 'missing',
        counts: { total: 8, present: 0, missing: 8, future: 0, unknown: 0 },
        episodes: [
          { episode_number: 1, name: null, air_date: null, status: null, library_ids: null },
          { episode_number: 2, name: '旧缓存引用', air_date: '2026-01-02', status: 'present', library_ids: [999] },
        ],
      },
      {
        season_number: 2,
        name: '第 2 季',
        special: false,
        status: 'unknown',
        counts: null,
        episodes: null,
      },
    ],
  },
}

afterEach(() => {
  apiMock.mockReset()
  routerMock.back.mockReset()
  routerMock.push.mockReset()
})

describe('discovery detail coverage expansion', () => {
  it('keeps the detail route rendered while nullable seasons repeatedly expand and collapse', async () => {
    apiMock.mockImplementation((path: string) => Promise.resolve(path.includes('/coverage') ? nullableCoverage : detail))
    const wrapper = mount(DiscoveryDetailView, { global: { plugins: [createPinia()] } })
    await vi.waitFor(() => expect(wrapper.text()).toContain('第 1 季'))

    const seasonButtons = wrapper.findAll('section.semantic-list-item > button')
    expect(seasonButtons).toHaveLength(2)

    await seasonButtons[0].trigger('click')
    expect(wrapper.text()).toContain('名称未知')
    expect(wrapper.text()).toContain('播出日期未知')
    expect(wrapper.text()).toContain('未知 / 待扫描')
    expect(wrapper.text()).toContain('媒体库信息暂不可用')
    expect(seasonButtons[0].attributes('aria-expanded')).toBe('true')

    await seasonButtons[0].trigger('click')
    expect(seasonButtons[0].attributes('aria-expanded')).toBe('false')
    expect(wrapper.text()).not.toContain('名称未知')

    await seasonButtons[0].trigger('click')
    expect(wrapper.text()).toContain('名称未知')
    expect(wrapper.find('h1').text()).toBe('测试剧')

    await seasonButtons[1].trigger('click')
    expect(wrapper.text()).toContain('该季暂无可显示的集信息，因此不会推断缺集。')
    expect(wrapper.find('h1').text()).toBe('测试剧')
    wrapper.unmount()
  })
})
