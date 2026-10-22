import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { useRoute } from 'vue-router'
import Detail from '../src/views/ServerDiscoveryDetailView.vue'
import Picker from '../src/components/server/DiscoverySitePickerDialog.vue'
import Workspace from '../src/components/server/DiscoverySearchWorkspace.vue'
import { beginResourceSearch, emptyResourceSearch, reduceResourceSearch } from '../src/services/serverDiscoverySearch'

const calls = vi.hoisted(() => [] as Array<Record<string, unknown>>)
const details = vi.hoisted(() => vi.fn())
const siteLists = vi.hoisted(() => vi.fn())
const coverage = vi.hoisted(() => vi.fn())
const acquisition = vi.hoisted(() => vi.fn())
const followDefaults = vi.hoisted(() => vi.fn())
const followPreview = vi.hoisted(() => vi.fn())
const createFollow = vi.hoisted(() => vi.fn())
vi.mock('vue-router', async () => {
  const { reactive } = await import('vue')
  const route = reactive({ params: { sourceId: 'fixture', provider: 'tmdb', mediaType: 'movie', providerId: '100' }, query: {}, fullPath: '/fixture' })
  return { useRoute: () => route, useRouter: () => ({ replace: vi.fn() }) }
})
vi.mock('@/services/datasource/server', () => ({ ServerDataSource: class {
  async refreshCapabilities() { return ['discovery_search', 'mixed_discovery_v1', 'subscription_create'] }
  createDiscoveryFollow(input: unknown) { return createFollow(input) }
} }))
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
  getServerFollowDefaults: followDefaults, getServerFollowRoutePreview: followPreview, getServerResourceLink: vi.fn(), getServerRouteRecommendation: vi.fn(),
  streamServerResources: async (_source: unknown, input: Record<string, unknown>, event: (value: unknown) => void) => {
    calls.push(input)
    event({ type: 'site', group: { siteId: 8, siteName: 'PanSou', status: 'success', page: input.page, hasNext: input.page === 1, skipped: 0, snapshotId: 'owned-snapshot', items: [{ token: 'fixture-token', title: 'Fixture result', resultType: 'magnet' }] } })
    event({ type: 'done', progress: { total: 1, pending: 0, running: 0, completed: 1, succeeded: 1, failed: 0, resultCount: 1 } })
    return { done: Promise.resolve(), cancel: vi.fn() }
  },
}))
beforeEach(() => {
  details.mockReset().mockResolvedValue({ work: { title: 'Fixture', tmdbId: 100 }, genres: [] })
  siteLists.mockReset().mockResolvedValue([{ id: 8, name: 'PanSou', kind: 'pansou_tg', siteType: 'cloud_share', searchable: true }])
  coverage.mockReset().mockResolvedValue(null)
  acquisition.mockReset().mockResolvedValue(null)
  followDefaults.mockReset()
  followPreview.mockReset().mockImplementation(async (_source, siteIds: number[], libraryId: number) => ({ available: true, routes: siteIds.map(siteId => ({ siteId, siteName: `站点 ${siteId}`, sourceKind: 'bt', recommended: { downloaderId: 'qbit', libraryId } })) }))
  createFollow.mockReset().mockResolvedValue({})
  const route = useRoute()
  route.params.sourceId = 'fixture'
  route.params.mediaType = 'movie'
  route.params.providerId = '100'
  route.fullPath = '/fixture'
})
afterEach(() => { calls.splice(0); document.body.innerHTML = '' })

function render() {
  return mount(Detail, { global: { stubs: { teleport: true, transition: false, MediaDetailHero: { props: ['title'], template: '<div><h1>{{ title }}</h1><slot name="actions" /></div>' }, DiscoveryAcquisitionDialog: true } } })
}

async function subscriptionDefaults() {
  // The released Server supplies available seasons in coverage, while the
  // execution snapshot intentionally has no selected seasons yet.
  const { getServerFollowDefaults } = await vi.importActual<typeof import('../src/services/serverDiscovery')>('../src/services/serverDiscovery')
  return getServerFollowDefaults({ getDiscoveryFollowDefaults: async () => ({
    snapshot: { version: 2, routing_policy: 'source_priority', seasons: null, site_ids: [8], media_library_id: 1, schedule: { kind: 'interval', minutes: 360 }, max_resources_per_run: 3, download_priority: 0 },
    coverage: { tv: { seasons: [
      { season_number: 0, name: '特别篇', counts: { missing: 2 } },
      { season_number: 1, name: '第一季', counts: { missing: 8 } },
      { season_number: 3, name: '第三季', counts: { missing: 5 } },
    ] } },
    subscribed_seasons: [1], sites: [{ id: 8, name: '站点 8', site_type: 'bt' }], downloaders: [{ id: 'qbit', name: 'qBittorrent' }], media_libraries: [{ id: 1, name: '媒体库一' }, { id: 2, name: '媒体库二' }],
  }) } as Parameters<typeof getServerFollowDefaults>[0], 100)
}

async function openSubscription(wrapper: ReturnType<typeof render>) {
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === '订阅')!.trigger('click')
  await flushPromises()
}
function confirmSubscription(wrapper: ReturnType<typeof render>) { return wrapper.findAll('button').find(button => button.text() === '确认订阅')! }

it('uses actual unsubscribed seasons from Server defaults and can submit a valid first-time subscription', async () => {
  useRoute().params.mediaType = 'tv'
  details.mockResolvedValue({ work: { title: 'Fixture show', tmdbId: 100 }, seasonCount: 3, genres: [] })
  followDefaults.mockResolvedValue(await subscriptionDefaults())
  const wrapper = render()
  try {
    await openSubscription(wrapper)
    expect(wrapper.text()).not.toContain('第 2 季')
    expect(wrapper.text()).toContain('第三季')
    expect(wrapper.findAll('button').find(button => button.text().includes('第一季'))!.attributes('disabled')).toBeDefined()
    expect(confirmSubscription(wrapper).attributes('disabled')).toBeUndefined()
    await confirmSubscription(wrapper).trigger('click')
    await flushPromises()
    expect(createFollow).toHaveBeenCalledWith(expect.objectContaining({ tmdb_id: 100, snapshot: expect.objectContaining({ seasons: [3], site_ids: [8], media_library_id: 1, routing_policy: 'source_priority' }) }))
  } finally { wrapper.unmount() }
})

it('explains a missing season at the confirmation button and includes specials only after explicit selection', async () => {
  useRoute().params.mediaType = 'tv'
  followDefaults.mockResolvedValue(await subscriptionDefaults())
  const wrapper = render()
  try {
    await openSubscription(wrapper)
    await wrapper.findAll('button').find(button => button.text().includes('第三季'))!.trigger('click')
    expect(confirmSubscription(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.get('#follow-submit-status').text()).toContain('请选择至少一季')
    await wrapper.findAll('button').find(button => button.text() === '特别篇')!.trigger('click')
    expect(confirmSubscription(wrapper).attributes('disabled')).toBeUndefined()
    await confirmSubscription(wrapper).trigger('click')
    await flushPromises()
    expect(createFollow.mock.calls.at(-1)?.[0].snapshot.seasons).toEqual([0])
  } finally { wrapper.unmount() }
})

it('keeps numeric library selection and the newest preview while waiting, without overwriting the chosen season', async () => {
  useRoute().params.mediaType = 'tv'
  followDefaults.mockResolvedValue(await subscriptionDefaults())
  let finishOld!: (value: unknown) => void
  followPreview.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve }))
  const wrapper = render()
  try {
    await openSubscription(wrapper)
    expect(confirmSubscription(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.get('#follow-submit-status').text()).toContain('路由')
    await wrapper.findAll('button').find(button => button.text().includes('第三季'))!.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === '特别篇')!.trigger('click')
    await wrapper.get('.follow-card select').setValue('2')
    await flushPromises()
    expect(followPreview.mock.calls.at(-1)?.[2]).toBe(2)
    finishOld({ available: false, routes: [] })
    await flushPromises()
    expect(confirmSubscription(wrapper).attributes('disabled')).toBeUndefined()
    await confirmSubscription(wrapper).trigger('click')
    await flushPromises()
    expect(createFollow.mock.calls.at(-1)?.[0].snapshot).toMatchObject({ seasons: [0], media_library_id: 2 })
  } finally { wrapper.unmount() }
})

it('does not restore follow defaults or issue its preview after the detail route changes', async () => {
  useRoute().params.mediaType = 'tv'
  let finish!: (value: unknown) => void
  followDefaults.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const wrapper = render()
  try {
    await openSubscription(wrapper)
    useRoute().params.sourceId = 'next'
    useRoute().fullPath = '/next'
    await flushPromises()
    finish(await subscriptionDefaults())
    await flushPromises()
    expect(wrapper.find('.follow-card').exists()).toBe(false)
    expect(followPreview).not.toHaveBeenCalled()
  } finally { wrapper.unmount() }
})

it('reports unavailable seasons, missing sites, invalid limits, and unavailable routes without sending a follow', async () => {
  useRoute().params.mediaType = 'tv'
  followDefaults.mockResolvedValue(await subscriptionDefaults())
  const wrapper = render()
  try {
    await openSubscription(wrapper)
    await wrapper.findAll('button').find(button => button.text() === '取消全选')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('#follow-submit-status').text()).toContain('搜索站点')
    expect(confirmSubscription(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.findAll('button').find(button => button.text() === '快速全选')!.trigger('click')
    await flushPromises()
    const limits = wrapper.findAll('.follow-card input[type="number"]')
    await limits[0].setValue('1')
    expect(wrapper.get('#follow-submit-status').text()).toContain('检查间隔')
    await limits[0].setValue('360')
    await limits[1].setValue('21')
    expect(wrapper.get('#follow-submit-status').text()).toContain('下载资源数')
    await limits[1].setValue('3')
    followPreview.mockResolvedValue({ available: false, routes: [{ siteId: 8, siteName: '站点 8', recommended: null, reasonMessage: '目标库不可达' }] })
    await wrapper.get('.follow-card select').setValue('2')
    await flushPromises()
    expect(wrapper.text()).toContain('目标库不可达')
    expect(wrapper.get('#follow-submit-status').text()).toContain('下载路由')
    expect(confirmSubscription(wrapper).attributes('disabled')).toBeDefined()
    expect(createFollow).not.toHaveBeenCalled()
  } finally { wrapper.unmount() }

  const defaults = await subscriptionDefaults()
  followDefaults.mockResolvedValue({ ...defaults, seasons: [], snapshot: { ...defaults.snapshot, seasons: [] } })
  const empty = render()
  try {
    await openSubscription(empty)
    expect(empty.get('#follow-submit-status').text()).toContain('尚未返回可订阅季')
    expect(empty.text()).not.toContain('第 1 季')
    expect(confirmSubscription(empty).attributes('disabled')).toBeDefined()
  } finally { empty.unmount() }
})

it('does not replace a newly opened follow or its explicit season choice with the closed dialog defaults', async () => {
  useRoute().params.mediaType = 'tv'
  let finish!: (value: unknown) => void
  followDefaults.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  followDefaults.mockResolvedValue(await subscriptionDefaults())
  const wrapper = render()
  try {
    await openSubscription(wrapper)
    await wrapper.findAll('.follow-card button').find(button => button.text() === '取消')!.trigger('click')
    await openSubscription(wrapper)
    await wrapper.findAll('button').find(button => button.text() === '第三季')!.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === '特别篇')!.trigger('click')
    const stale = await subscriptionDefaults()
    finish({ ...stale, snapshot: { ...stale.snapshot, seasons: [3], media_library_id: 2 } })
    await flushPromises()
    expect(followPreview).toHaveBeenCalledTimes(1)
    await confirmSubscription(wrapper).trigger('click')
    await flushPromises()
    expect(createFollow.mock.calls.at(-1)?.[0].snapshot).toMatchObject({ seasons: [0], media_library_id: 1 })
  } finally { wrapper.unmount() }
})

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

it('shows a mixed partial batch immediately and retains its claims after a later upstream failure', async () => {
  const sites = [{ id: 8, name: 'PanSou', kind: 'pansou_tg', siteType: 'cloud_share', searchable: true }]
  const firstBatch = {
    siteId: 8, siteName: 'PanSou', siteType: 'cloud_share', status: 'success', page: 1, skipped: 0,
    hasNext: true, snapshotId: 'owned-partial-snapshot', complete: false, revision: 1, completion: 'received',
    items: [
      { token: 'quark-partial', title: 'Already received episode', sourceKind: 'quark_share', resultType: 'quark', acquisitionAvailable: true, origins: [{ kind: 'plugin' as const, name: 'Fixture plugin' }] },
      { token: 'view-only-partial', title: 'View-only result', resultType: 'aliyun', acquisitionAvailable: false, acquisitionReason: '尚未支持此类型的自动入库。' },
    ],
  }
  let state = beginResourceSearch(emptyResourceSearch(), { sites, siteIds: [8], replaceAll: true, page: 1 })
  state = reduceResourceSearch(state, { type: 'site', group: firstBatch }, sites)
  const wrapper = mount(Workspace, {
    props: { open: true, title: 'Fixture', state, canAcquire: true, mixedSupported: true },
    global: { stubs: { teleport: true, transition: false } },
  })
  try {
    expect(wrapper.text()).toContain('正在搜索 1 个站点 · 已显示 2 条结果')
    expect(wrapper.text()).toContain('Already received episode')
    expect(wrapper.text()).toContain('PanSou · 插件 · Fixture plugin')
    expect(wrapper.findAll('button').find(button => button.text() === '下一页')!.attributes('disabled')).toBeDefined()
    const viewOnly = wrapper.findAll('article').find(article => article.text().includes('View-only result'))!
    expect(viewOnly.get('.select-button').attributes('disabled')).toBeDefined()
    expect(viewOnly.findAll('button').find(button => button.text() === '复制链接')!.attributes('disabled')).toBeUndefined()

    state = reduceResourceSearch(state, { type: 'site', group: { ...firstBatch, status: 'error', complete: true, revision: 2, partial: true, errorCode: 'site_unavailable', incompleteReason: 'site_unavailable', items: [] } }, sites)
    state = reduceResourceSearch(state, { type: 'done', progress: { total: 1, pending: 0, running: 0, completed: 1, succeeded: 0, failed: 1, resultCount: 2 } }, sites)
    await wrapper.setProps({ state })
    expect(wrapper.text()).toContain('搜索尚未完整完成 · 已保留 2 条结果')
    expect(wrapper.text()).toContain('站点暂时不可用')
    expect(wrapper.text()).toContain('上游尚未确认全部来源完成')
    expect(wrapper.text()).toContain('Already received episode')
    expect(wrapper.text()).toContain('PanSou · 插件 · Fixture plugin')
    await wrapper.findAll('button').find(button => button.text() === '转存入库')!.trigger('click')
    expect(wrapper.emitted('selectResource')?.[0]?.[0]).toMatchObject({ token: 'quark-partial', sourceKind: 'quark_share' })
    expect(state.groups[0].snapshotId).toBe('owned-partial-snapshot')
  }
  finally { wrapper.unmount() }
})
