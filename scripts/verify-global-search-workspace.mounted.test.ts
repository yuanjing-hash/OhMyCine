import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { useRoute } from 'vue-router'
import Workspace from '../src/components/media/GlobalSearchWorkspace.vue'
import { useSearchWorkspaceStore } from '../src/stores/searchWorkspace'

const search = vi.hoisted(() => vi.fn())
const discovery = vi.hoisted(() => vi.fn())
const refresh = vi.hoisted(() => vi.fn())
const sync = vi.hoisted(() => vi.fn())
vi.mock('vue-router', async () => {
  const { reactive } = await import('vue')
  const route = reactive({ name: 'home', fullPath: '/' })
  return { useRoute: () => route, useRouter: () => ({ push: vi.fn() }) }
})
vi.mock('@/services/datasource/server', () => ({ ServerDataSource: class {
  constructor(readonly id: string) {}
  hasCapability() { return true }
  refreshCapabilities() { return refresh(this.id) }
  async listLibraries() { return [] }
} }))
vi.mock('@/stores/datasource', async () => {
  const { ServerDataSource } = await import('@/services/datasource/server')
  const sources = { a: new ServerDataSource('a'), b: new ServerDataSource('b') }
  return { useDataSourceStore: () => ({
    configs: [], orderedConfigs: [{ id: 'a', type: 'server', name: 'Server A' }, { id: 'b', type: 'server', name: 'Server B' }], homeSections: [],
    loadConfigs: vi.fn(), loadHomeSections: vi.fn(), syncManager: sync, searchAllSources: search,
    getSourceRootSnapshot: () => undefined, getSource: (id: keyof typeof sources) => sources[id],
  }) }
})
vi.mock('@/stores/searchWorkspace', async () => {
  const { reactive } = await import('vue')
  const state = reactive({ open: false, show() { state.open = true }, hide() { state.open = false } })
  return { useSearchWorkspaceStore: () => state }
})
vi.mock('@/services/serverDiscovery', () => ({ searchServerDiscovery: discovery }))
vi.mock('@/services/playbackHistory', () => ({
  getPlaybackCompletionBatch: async () => [], playbackCompletionKey: vi.fn(), playbackCompletionKeyForMediaItem: vi.fn(), playbackProgressIdentityForMediaItem: vi.fn(), PLAYED_STATE_CHANGED_EVENT: 'test:played',
}))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function works(title: string) { return [{ title, provider: 'tmdb', providerId: title, mediaType: 'tv' }] }
function render() {
  return mount(Workspace, { global: { stubs: { teleport: true, transition: false, CachedImage: true } } })
}
type Wrapper = ReturnType<typeof render>
function more(wrapper: Wrapper) { return wrapper.find('button.server-more') }
async function query(wrapper: Wrapper, value: string) {
  await wrapper.get('input').setValue(value)
  await vi.advanceTimersByTimeAsync(300)
  await flushPromises()
}
async function open(wrapper: Wrapper) {
  useSearchWorkspaceStore().show()
  await flushPromises()
  expect(wrapper.get('[role="dialog"]').exists()).toBe(true)
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  useSearchWorkspaceStore().hide()
  useRoute().fullPath = '/'
  search.mockReset().mockResolvedValue([])
  discovery.mockReset().mockImplementation(async (_source, keyword) => works(keyword))
  refresh.mockReset().mockResolvedValue(['discovery_search'])
  sync.mockReset().mockResolvedValue(undefined)
})
afterEach(() => { vi.useRealTimers(); document.body.innerHTML = '' })

it('clears completed Server results when a nonempty query changes and requires a new explicit discovery click', async () => {
  const wrapper = render()
  try {
    await open(wrapper)
    await query(wrapper, '闪电奇侠刚达拉')
    expect(discovery).not.toHaveBeenCalled()
    await more(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('闪电奇侠刚达拉')
    expect(more(wrapper).exists()).toBe(false)
    await query(wrapper, '奥特曼')
    expect(wrapper.text()).not.toContain('闪电奇侠刚达拉')
    expect(more(wrapper).exists()).toBe(true)
    expect(discovery).toHaveBeenCalledTimes(2)
    await more(wrapper).trigger('click')
    await flushPromises()
    expect(discovery.mock.calls.slice(-2).every(call => call[1] === '奥特曼')).toBe(true)
    expect(wrapper.findAll('.result-card').map(card => card.text()).join()).toContain('奥特曼')
  } finally { wrapper.unmount() }
})

it('keeps same-query completed results on return, but rejects a pending response after close and reopen', async () => {
  const wrapper = render()
  try {
    await open(wrapper)
    await query(wrapper, '第一部')
    await more(wrapper).trigger('click')
    await flushPromises()
    useSearchWorkspaceStore().hide()
    await flushPromises()
    await open(wrapper)
    expect(wrapper.findAll('.result-card').length).toBe(2)
    await query(wrapper, '第二部')
    const old = deferred<ReturnType<typeof works>>()
    discovery.mockImplementation(() => old.promise)
    await more(wrapper).trigger('click')
    await flushPromises()
    useSearchWorkspaceStore().hide()
    await flushPromises()
    await open(wrapper)
    expect(more(wrapper).attributes('disabled')).toBeUndefined()
    old.resolve(works('旧会话回包'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('旧会话回包')
    expect(more(wrapper).exists()).toBe(true)
  } finally { wrapper.unmount() }
})

it('invalidates discovery on type and source changes and ignores old failures without stopping the new request', async () => {
  const wrapper = render()
  try {
    await open(wrapper)
    await query(wrapper, '同名作品')
    const old = deferred<ReturnType<typeof works>>()
    const current = deferred<ReturnType<typeof works>>()
    discovery.mockImplementation(() => old.promise)
    await more(wrapper).trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '剧集')!.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'Server B')!.trigger('click')
    await flushPromises()
    expect(more(wrapper).attributes('disabled')).toBeUndefined()
    discovery.mockImplementation(() => current.promise)
    await more(wrapper).trigger('click')
    await flushPromises()
    expect(discovery.mock.calls.at(-1)?.slice(1)).toEqual(['同名作品', 'tv'])
    old.reject(new Error('旧查询失败'))
    await flushPromises()
    expect(more(wrapper).text()).toContain('搜索中')
    expect(wrapper.text()).not.toContain('旧查询失败')
    current.resolve(works('当前结果'))
    await flushPromises()
    expect(wrapper.findAll('.result-card')).toHaveLength(1)
    expect(wrapper.text()).toContain('Server B')
  } finally { wrapper.unmount() }
})

it('does not dispatch discovery after a retired capability wait, and cancels catalog debounce on close', async () => {
  const wrapper = render()
  try {
    await open(wrapper)
    await query(wrapper, '等待权限')
    const capabilities = deferred<string[]>()
    refresh.mockImplementation(() => capabilities.promise)
    await more(wrapper).trigger('click')
    await flushPromises()
    useRoute().fullPath = '/different'
    await flushPromises()
    capabilities.resolve(['discovery_search'])
    await flushPromises()
    expect(discovery).not.toHaveBeenCalled()
    refresh.mockResolvedValue(['discovery_search'])
    await open(wrapper)
    await wrapper.get('input').setValue('未发出的新查询')
    await wrapper.get('button[aria-label="取消搜索"]').trigger('click')
    await vi.advanceTimersByTimeAsync(300)
    expect(search.mock.calls.some(call => call[0] === '未发出的新查询')).toBe(false)
  } finally { wrapper.unmount() }
})

it('rejects a movie response after switching only the media type and starts a new explicit TV query', async () => {
  const wrapper = render()
  try {
    await open(wrapper)
    await query(wrapper, '同名电影与剧集')
    await wrapper.findAll('button').find(button => button.text() === '电影')!.trigger('click')
    const movies = deferred<ReturnType<typeof works>>()
    discovery.mockImplementation(() => movies.promise)
    await more(wrapper).trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '剧集')!.trigger('click')
    movies.resolve(works('旧类型的电影'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('旧类型的电影')
    expect(more(wrapper).attributes('disabled')).toBeUndefined()
    discovery.mockResolvedValue(works('当前剧集'))
    await more(wrapper).trigger('click')
    await flushPromises()
    expect(discovery.mock.calls.slice(-2).every(call => call[2] === 'tv')).toBe(true)
    expect(wrapper.text()).toContain('当前剧集')
  } finally { wrapper.unmount() }
})

it('clears completed discovery when selecting another source and never queries an unselected source', async () => {
  const wrapper = render()
  try {
    await open(wrapper)
    await query(wrapper, '按来源搜索')
    await more(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.result-card')).toHaveLength(2)
    await wrapper.findAll('button').find(button => button.text() === 'Server B')!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.result-card')).toHaveLength(0)
    expect(more(wrapper).exists()).toBe(true)
    discovery.mockClear()
    await more(wrapper).trigger('click')
    await flushPromises()
    expect(discovery).toHaveBeenCalledTimes(1)
    expect(discovery.mock.calls[0][0].id).toBe('b')
    expect(search.mock.calls.at(-1)?.[2]).toEqual(['b'])
  } finally { wrapper.unmount() }
})

it('discards a previous catalog reply and keeps ordinary catalog search independent from discovery', async () => {
  const wrapper = render()
  try {
    await open(wrapper)
    const old = deferred<unknown[]>()
    search.mockReturnValueOnce(old.promise)
    await query(wrapper, '旧馆藏')
    search.mockResolvedValue([{ id: 'new', sourceId: 'a', name: '新馆藏', type: 'movie', path: '' }])
    await query(wrapper, '新馆藏')
    old.resolve([{ id: 'old', sourceId: 'a', name: '旧馆藏', type: 'movie', path: '' }])
    await flushPromises()
    expect(wrapper.text()).toContain('新馆藏')
    expect(wrapper.text()).not.toContain('旧馆藏')
    expect(discovery).not.toHaveBeenCalled()
  } finally { wrapper.unmount() }
})
