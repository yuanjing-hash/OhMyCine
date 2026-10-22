// @vitest-environment happy-dom
import type { ServerResourceItem } from '../src/services/serverDiscovery'
import type { ShareContentAnalysis } from '../src/services/serverShareContent'
import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { computed, defineComponent, ref, shallowRef } from 'vue'
import Workspace from '../src/components/server/DiscoverySearchWorkspace.vue'
import SharePreview from '../src/components/server/DiscoverySharePreview.vue'
import { useServerResourceRecognition } from '../src/composables/useServerResourceRecognition'
import { ServerDataSource } from '../src/services/datasource/server'
import { emptyResourceSearch } from '../src/services/serverDiscoverySearch'
import { parseServerResourceRecognition } from '../src/services/serverResourceRecognition'
import { parseShareContentAnalysis } from '../src/services/serverShareContent'
import { parseServerSharePreview } from '../src/services/serverSharePreview'

const cleanup: Array<() => void> = []
afterEach(() => { cleanup.splice(0).forEach(stop => stop()); document.body.innerHTML = ''; vi.useRealTimers() })
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const analysis = (episodes = [1, 3, 10, 11, 12]): ShareContentAnalysis => ({ status: 'complete', complete: true, groups: [{ title: 'Actual show', media_type: 'tv', season: 1, episodes, status: 'matched', video_count: episodes.length + 1 }], video_count: episodes.length + 1, ignored_count: 1, unresolved_count: 0, message: '按真实文件名分析', checked_at: new Date().toISOString(), expires_at: new Date(Date.now() + 60000).toISOString() })
const cloud = (token: string): ServerResourceItem => ({ token, title: `${token} advertised 1–99`, sourceKind: 'quark_share', resultType: 'quark', expiresAt: new Date(Date.now() + 300000).toISOString() })
const recognition = (value?: ShareContentAnalysis) => ({ engine_version: 'nextgen-domain-v13', status: 'matched', title: 'Advertised work', media_type: 'tv', episodes: { season: 1, episode_min: 1, episode_max: 99 }, ...(value ? { share_analysis: value } : {}) })

async function fixtureSource(reply: (token: string) => Promise<unknown> | unknown) {
  const calls: Array<{ path: string, body?: unknown }> = []
  const source = new ServerDataSource({ readCredential: async () => ({ accessToken: `omc_player_${'a'.repeat(43)}` }), bridge: { request: async (request) => {
    calls.push({ path: request.path, body: request.body })
    const data = await reply((request.body as { result_token: string }).result_token)
    return { status: 200, body: { code: 0, message: 'success', data } }
  } } })
  await source.init({ id: 'fixture', name: 'Fixture Server', type: 'server', enabled: true, order: 0, url: 'http://127.0.0.1:3000', extra: { credentialRef: 'fixture-only' } })
  cleanup.push(() => source.destroy())
  return { source, calls }
}

function render(sourceValue: ServerDataSource, initial: ServerResourceItem[]) {
  const source = shallowRef<ServerDataSource | null>(sourceValue)
  const enabled = ref(true)
  const owner = ref('query-one')
  const items = ref(initial)
  let queue!: ReturnType<typeof useServerResourceRecognition>
  const host = defineComponent({
    components: { Workspace },
    setup() {
      queue = useServerResourceRecognition(items, source, owner, enabled)
      const state = computed(() => ({ ...emptyResourceSearch(), outcome: 'complete' as const, groups: [{ siteId: 1, siteName: 'Custom source', siteType: 'cloud_share', status: 'success', page: 1, hasNext: false, skipped: 0, items: items.value }] }))
      return { state, enabled, ...queue }
    },
    template: '<Workspace :open="enabled" title="Fixture" :state="state" :can-acquire="true" :recognitions="recognitions" :recognizing-tokens="loadingTokens" :recognition-errors="errors" @recognize="retry" />',
  })
  const wrapper = mount(host, { global: { stubs: { teleport: true, transition: false } } })
  cleanup.push(() => wrapper.unmount())
  return { wrapper, queue, source, enabled, owner, items }
}

it('automatically requests actual share files before preview with two requests at a time', async () => {
  const first = deferred<unknown>(), second = deferred<unknown>(), third = deferred<unknown>()
  const fixture = await fixtureSource(token => ({ one: first, two: second, three: third }[token]!.promise))
  const { wrapper } = render(fixture.source, [cloud('one'), cloud('two'), cloud('three')])
  await flushPromises()
  expect(fixture.calls).toHaveLength(2)
  expect(wrapper.text()).toContain('正在读取分享文件')
  expect(fixture.calls.every(call => call.path === '/api/v1/player/discovery/torrent-results/recognize')).toBe(true)
  expect(fixture.calls[0].body).toEqual({ result_token: 'one' })
  first.resolve(recognition(analysis(Array.from({ length: 14 }, (_, i) => i + 1))))
  await flushPromises()
  expect(fixture.calls).toHaveLength(3)
  const content = wrapper.findAll('[aria-label="分享文件分析"]')[0].text()
  expect(content).toContain('已读取分享内容')
  expect(content).toContain('第 1–14 集（共 14 集） · 15 个视频')
  expect(content).not.toContain('1–99')
  second.resolve(recognition(analysis())); third.resolve(recognition(analysis()))
  await flushPromises()
})

it('keeps sparse holes, distinct works and seasons, and video encodings separate from episode counts', async () => {
  const value = analysis()
  value.groups.push({ title: 'Another show', media_type: 'tv', season: 2, episodes: [7, 8], video_count: 2, status: 'unrecognized' })
  value.unresolved_count = 2
  const fixture = await fixtureSource(() => recognition(value))
  const { wrapper } = render(fixture.source, [cloud('sparse')])
  await flushPromises()
  const content = wrapper.get('[aria-label="分享文件分析"]').text()
  expect(content).toContain('第 1、3、10–12 集（共 5 集） · 6 个视频')
  expect(content).toContain('Another show · 第 2 季 · 第 7–8 集')
  expect(content).not.toContain('第 1–12 集')
  expect(content).toContain('作品待确认')
})

it('does not use advertised title episodes on failure or older responses; BT needs no share receiver', async () => {
  const fixture = await fixtureSource(token => token === 'unavailable' ? recognition({ ...analysis(), status: 'unavailable', complete: false, groups: [], message: '没有可用的同来源分享接收器' }) : recognition())
  const { wrapper } = render(fixture.source, [cloud('unavailable'), cloud('legacy'), { token: 'bt', title: 'BT show S01E11', sourceKind: 'bt' }])
  await flushPromises()
  const content = wrapper.findAll('[aria-label="分享文件分析"]')
  expect(content[0].text()).toContain('分享内容读取未完成')
  expect(content[0].text()).toContain('同来源分享接收器')
  expect(content[0].text()).not.toContain('第 1–99 集')
  expect(content[1].text()).toContain('标题仅供参考')
  expect(content[1].text()).not.toContain('第 1–99 集')
  expect(wrapper.get('[aria-label="BT 标题识别"]').text()).toContain('第 1–99 集')
  expect(fixture.calls).toHaveLength(3)
})

it('fences close, query, source and token replacement, including late failures and finally handlers', async () => {
  const old = deferred<unknown>(), newSourcePending = deferred<unknown>()
  const first = await fixtureSource(() => old.promise)
  const second = await fixtureSource(() => newSourcePending.promise)
  const view = render(first.source, [cloud('old')])
  await flushPromises()
  view.enabled.value = false
  view.items.value = [cloud('next')]
  view.owner.value = 'query-two'
  view.source.value = second.source
  view.enabled.value = true
  await flushPromises()
  expect(second.calls.map(call => (call.body as { result_token: string }).result_token)).toEqual(['next'])
  old.reject(new Error('old request failed'))
  await flushPromises()
  expect(view.queue.loadingTokens.value).toEqual(['next'])
  expect(view.wrapper.text()).not.toContain('old request failed')
  newSourcePending.resolve(recognition(analysis([20, 21])))
  await flushPromises()
  expect(view.wrapper.get('[aria-label="分享文件分析"]').text()).toContain('第 20–21 集')
  expect(view.queue.recognitions.value.old).toBeUndefined()
})

it('retains a newer preview summary when an older automatic result finishes', async () => {
  const pending = deferred<unknown>()
  const fixture = await fixtureSource(() => pending.promise)
  const view = render(fixture.source, [cloud('same')])
  await flushPromises()
  view.queue.acceptAnalysis('same', analysis([20, 21]))
  pending.resolve(recognition(analysis([1, 2])))
  await flushPromises()
  expect(view.wrapper.get('[aria-label="分享文件分析"]').text()).toContain('第 20–21 集')
  expect(view.wrapper.get('[aria-label="分享文件分析"]').text()).not.toContain('第 1–2 集')
})

it('does not release an actual in-flight native request just because the UI deadline elapsed', async () => {
  vi.useFakeTimers()
  const pending = deferred<unknown>()
  const fixture = await fixtureSource(() => pending.promise)
  const view = render(fixture.source, [cloud('one'), cloud('two'), cloud('three')])
  await flushPromises()
  expect(fixture.calls).toHaveLength(2)
  await vi.advanceTimersByTimeAsync(65000)
  await flushPromises()
  expect(view.queue.errors.value.one).toContain('分享文件读取超时')
  expect(view.queue.loadingTokens.value).toEqual([])
  expect(fixture.calls).toHaveLength(2)
  pending.reject(new Error('Native request ended'))
  await flushPromises()
  expect(fixture.calls).toHaveLength(3)
})

it('keeps native in-flight requests bounded through rapid query changes and retries', async () => {
  const pending: Array<ReturnType<typeof deferred<unknown>>> = []
  let current = 0, maximum = 0
  const fixture = await fixtureSource(async () => {
    const request = deferred<unknown>()
    pending.push(request)
    current++
    maximum = Math.max(maximum, current)
    try { return await request.promise }
    finally { current-- }
  })
  const view = render(fixture.source, [cloud('old-one'), cloud('old-two')])
  await flushPromises()
  for (let iteration = 0; iteration < 8; iteration++) {
    view.items.value = [cloud(`next-${iteration}`)]
    view.owner.value = `query-${iteration}`
    view.queue.retry(`next-${iteration}`)
    await flushPromises()
  }
  expect(fixture.calls).toHaveLength(2)
  expect(maximum).toBe(2)
  pending[0].reject(new Error('Old native request timed out'))
  await flushPromises()
  expect(fixture.calls).toHaveLength(3)
  expect(view.queue.loadingTokens.value).toEqual(['next-7'])
  expect(view.wrapper.text()).not.toContain('Old native request timed out')
  pending[1].resolve(recognition(analysis([1])))
  pending[2].resolve(recognition(analysis([20])))
  await flushPromises()
  expect(maximum).toBe(2)
  expect(view.wrapper.text()).toContain('第 20 集')
  expect(view.queue.recognitions.value['old-one']).toBeUndefined()
})

it('retains the same-source native budget across unmount and lets the new owner continue after native timeout', async () => {
  vi.useFakeTimers()
  const pending: Array<ReturnType<typeof deferred<unknown>>> = []
  const fixture = await fixtureSource(() => {
    const request = deferred<unknown>()
    pending.push(request)
    return request.promise
  })
  const oldView = render(fixture.source, [cloud('old-one'), cloud('old-two')])
  await flushPromises()
  oldView.wrapper.unmount()
  const nextView = render(fixture.source, [cloud('next-one'), cloud('next-two')])
  await flushPromises()
  expect(fixture.calls).toHaveLength(2)
  setTimeout(() => pending[0].reject(new Error('Native timeout')), 60000)
  await vi.advanceTimersByTimeAsync(60000)
  await flushPromises()
  expect(fixture.calls).toHaveLength(3)
  expect(nextView.queue.loadingTokens.value).toEqual(['next-one'])
  pending[1].resolve(recognition(analysis([1])))
  await flushPromises()
  expect(fixture.calls).toHaveLength(4)
  expect(nextView.queue.loadingTokens.value).toEqual(['next-one', 'next-two'])
  pending[2].resolve(recognition(analysis([20])))
  pending[3].resolve(recognition(analysis([21])))
  await flushPromises()
  expect(nextView.wrapper.text()).not.toContain('Native timeout')
  expect(nextView.wrapper.text()).toContain('第 20 集')
})

it('emits only the current manual preview summary after receiver/result replacement', async () => {
  const old = deferred<ReturnType<typeof parseServerSharePreview>>()
  const next = parseServerSharePreview({ token: 'preview-next', entries: [], file_count: 0, total_size: 0, expires_at: new Date(Date.now() + 60000).toISOString(), share_analysis: analysis([30]) })
  let reads = 0
  const wrapper = mount(SharePreview, { props: { resultToken: 'first', downloaderId: 'one', providerLabel: 'Quark', load: () => ++reads === 1 ? old.promise : Promise.resolve(next) } })
  cleanup.push(() => wrapper.unmount())
  await flushPromises()
  await wrapper.setProps({ resultToken: 'next', downloaderId: 'two' })
  await flushPromises()
  old.resolve({ ...next, shareAnalysis: analysis([1]) })
  await flushPromises()
  expect(wrapper.emitted('analysis')).toHaveLength(1)
  expect(wrapper.emitted('analysis')?.[0]).toEqual(['next', next.shareAnalysis])
})

it('parses additive facts defensively, strips private fields, and accepts old recognition responses', () => {
  expect(parseServerResourceRecognition(recognition()).shareAnalysis).toBeUndefined()
  expect(parseShareContentAnalysis({ ...analysis(), groups: Array.from({ length: 33 }, () => analysis().groups[0]) })).toBeUndefined()
  expect(parseShareContentAnalysis({ ...analysis(), groups: [{ ...analysis().groups[0], episodes: [1, '2'] }] })).toBeUndefined()
  expect(parseShareContentAnalysis({ ...analysis(), expires_at: new Date(Date.now() - 1).toISOString() })).toBeUndefined()
  const parsed = parseShareContentAnalysis({ ...analysis(), provider_id: 'private-marker', groups: [{ ...analysis().groups[0], episodes: [12, 1, 1, 10], poster_url: 'https://example.invalid/private-marker' }] })!
  expect(parsed.groups[0].episodes).toEqual([1, 10, 12])
  expect(JSON.stringify(parsed)).not.toContain('private-marker')
})
