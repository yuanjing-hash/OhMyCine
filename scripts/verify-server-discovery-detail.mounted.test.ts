import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { useRoute } from 'vue-router'
import Detail from '../src/views/ServerDiscoveryDetailView.vue'
import Picker from '../src/components/server/DiscoverySitePickerDialog.vue'
import Workspace from '../src/components/server/DiscoverySearchWorkspace.vue'

const calls = vi.hoisted(() => [] as Array<Record<string, unknown>>)
const details = vi.hoisted(() => vi.fn())
const siteLists = vi.hoisted(() => vi.fn())
const coverage = vi.hoisted(() => vi.fn())
const acquisition = vi.hoisted(() => vi.fn())
vi.mock('vue-router', async () => {
  const { reactive } = await import('vue')
  const route = reactive({ params: { sourceId: 'fixture', provider: 'tmdb', mediaType: 'movie', providerId: '100' }, query: {}, fullPath: '/fixture' })
  return { useRoute: () => route, useRouter: () => ({ replace: vi.fn() }) }
})
vi.mock('@/services/datasource/server', () => ({ ServerDataSource: class { async refreshCapabilities() { return ['discovery_search', 'mixed_discovery_v1'] } } }))
vi.mock('@/stores/datasource', async () => {
  const { ServerDataSource } = await import('@/services/datasource/server')
  const sources = { fixture: new ServerDataSource(), next: new ServerDataSource() }
  return { useDataSourceStore: () => ({ loadConfigs: vi.fn(), syncManager: vi.fn(), orderedConfigs: Object.keys(sources).map(id => ({ id, type: 'server', enabled: true })), getSource: (id: keyof typeof sources) => sources[id] }) }
})
vi.mock('@/stores/searchWorkspace', () => ({ useSearchWorkspaceStore: () => ({ show: vi.fn() }) }))
vi.mock('@/stores/acquisitionWorkspace', () => ({ useAcquisitionWorkspaceStore: () => ({ show: vi.fn() }) }))
vi.mock('@/services/mediaActions', () => ({ publishFeedback: vi.fn() }))
vi.mock('@/services/layoutBackNavigation', () => ({ registerLayoutBackHandler: () => vi.fn() }))
vi.mock('@/services/serverSharePreview', () => ({ previewServerShare: vi.fn() }))
vi.mock('@/services/serverDiscovery', async original => ({
  ...await original<object>(),
  getServerDiscoveryDetail: details,
  getServerCoverage: coverage, getServerAcquisition: acquisition,
  getServerSearchSites: siteLists,
  getServerFollowDefaults: vi.fn(), getServerFollowRoutePreview: vi.fn(), getServerResourceLink: vi.fn(), getServerRouteRecommendation: vi.fn(),
  streamServerResources: async (_source: unknown, input: Record<string, unknown>, event: (value: unknown) => void) => {
    calls.push(input)
    event({ type: 'site', group: { siteId: 8, siteName: 'PanSou', status: 'success', page: input.page, hasNext: input.page === 1, skipped: 0, snapshotId: 'owned-snapshot', items: [{ token: 'fixture-token', title: 'Fixture result', resultType: 'magnet' }] } })
    return { done: Promise.resolve(), cancel: vi.fn() }
  },
}))
beforeEach(() => {
  details.mockReset().mockResolvedValue({ work: { title: 'Fixture', tmdbId: 100 }, genres: [] })
  siteLists.mockReset().mockResolvedValue([{ id: 8, name: 'PanSou', kind: 'pansou_tg', siteType: 'cloud_share', searchable: true }])
  coverage.mockReset().mockResolvedValue(null)
  acquisition.mockReset().mockResolvedValue(null)
  const route = useRoute()
  route.params.sourceId = 'fixture'
  route.fullPath = '/fixture'
})
afterEach(() => { calls.splice(0); document.body.innerHTML = '' })

function render() {
  return mount(Detail, { global: { stubs: { teleport: true, transition: false, MediaDetailHero: { props: ['title'], template: '<div><h1>{{ title }}</h1><slot name="actions" /></div>' }, DiscoveryAcquisitionDialog: true } } })
}

it('keeps the owned snapshot on next and previous page and refreshes only on retry or applied type changes', async () => {
  const wrapper = render()
  try {
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '搜索')!.trigger('click')
    await flushPromises()
    wrapper.findComponent(Picker).vm.$emit('search')
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ page: 1, refresh: true, snapshotId: undefined })
    const workspace = wrapper.findComponent(Workspace)
    await workspace.get('select').setValue('all')
    await workspace.findAll('button').find(button => button.text() === '下一页')!.trigger('click')
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ page: 2, refresh: false, snapshotId: 'owned-snapshot', typePolicy: undefined })
    await workspace.findAll('button').find(button => button.text() === '上一页')!.trigger('click')
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ page: 1, refresh: false, snapshotId: 'owned-snapshot', typePolicy: undefined })
    workspace.vm.$emit('retry', 8)
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ page: 1, refresh: true, snapshotId: undefined, typePolicy: undefined })
    await workspace.findAll('button').find(button => button.text() === '按此类型重新搜索')!.trigger('click')
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ page: 1, refresh: true, snapshotId: undefined, typePolicy: 'all' })
  } finally { wrapper.unmount() }
})

it('ignores a previous Server detail response after the route changes', async () => {
  let finish!: (value: unknown) => void
  details.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  details.mockResolvedValue({ work: { title: 'Current Server', tmdbId: 200 }, genres: [] })
  const wrapper = render()
  try {
    await flushPromises()
    const route = useRoute()
    route.params.sourceId = 'next'
    route.fullPath = '/next'
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Current Server')
    finish({ work: { title: 'Retired Server', tmdbId: 100 }, genres: [] })
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Current Server')
    expect(coverage).toHaveBeenCalledTimes(1)
    expect(acquisition).toHaveBeenCalledTimes(1)
  } finally { wrapper.unmount() }
})

it('does not let a previous failure clear the new route loading state', async () => {
  let rejectOld!: (reason: unknown) => void
  let finishCurrent!: (value: unknown) => void
  details.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectOld = reject }))
  details.mockImplementationOnce(() => new Promise(resolve => { finishCurrent = resolve }))
  const wrapper = render()
  try {
    await flushPromises()
    const route = useRoute()
    route.params.sourceId = 'next'
    route.fullPath = '/next'
    await flushPromises()
    rejectOld(new Error('Retired Server failed'))
    await flushPromises()
    expect(wrapper.text()).toContain('正在通过 Server 读取 TMDB 详情')
    expect(wrapper.text()).not.toContain('Retired Server failed')
    finishCurrent({ work: { title: 'Current Server', tmdbId: 200 }, genres: [] })
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Current Server')
  } finally { wrapper.unmount() }
})

it('does not restore the old Server site selection after a route change', async () => {
  let finish!: (value: unknown) => void
  siteLists.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  siteLists.mockResolvedValue([{ id: 9, name: 'Current site', kind: 'pansou_tg', siteType: 'cloud_share', searchable: true }])
  const wrapper = render()
  try {
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '搜索')!.trigger('click')
    await flushPromises()
    const route = useRoute()
    route.params.sourceId = 'next'
    route.fullPath = '/next'
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '搜索')!.trigger('click')
    await flushPromises()
    expect(wrapper.findComponent(Picker).props('selectedSiteIds')).toEqual([9])
    finish([{ id: 8, name: 'Retired site', kind: 'pansou_tg', siteType: 'cloud_share', searchable: true }])
    await flushPromises()
    expect(wrapper.findComponent(Picker).props('selectedSiteIds')).toEqual([9])
    expect(wrapper.findComponent(Picker).props('sites')[0].name).toBe('Current site')
  } finally { wrapper.unmount() }
})

it('does not begin coverage or acquisition requests after unmount', async () => {
  let finish!: (value: unknown) => void
  details.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const wrapper = render()
  await flushPromises()
  wrapper.unmount()
  finish({ work: { title: 'Retired Server', tmdbId: 100 }, genres: [] })
  await flushPromises()
  expect(coverage).not.toHaveBeenCalled()
  expect(acquisition).not.toHaveBeenCalled()
})
