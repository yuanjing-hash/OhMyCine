// @vitest-environment happy-dom
import type { ServerResourceItem } from '../src/services/serverDiscovery'
import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref, shallowRef } from 'vue'
import Content from '../src/components/server/DiscoveryResourceContent.vue'
import Acquisition from '../src/components/server/DiscoveryAcquisitionDialog.vue'
import { useServerResourceRecognition } from '../src/composables/useServerResourceRecognition'
import { ServerDataSource } from '../src/services/datasource/server'
import { parseServerResourceContent } from '../src/services/serverResourceContent'
import { parseServerSubtitlePolicy } from '../src/services/serverSubtitlePolicy'

const cleanup: Array<() => void> = []
afterEach(() => { cleanup.splice(0).forEach(stop => stop()); document.body.innerHTML = ''; vi.useRealTimers() })
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const preview = (name = 'Actual.Show.S01E03.zh-Hans.mkv', token = 'preview-one') => ({ version: 1, token, status: 'complete', complete: true, files: [{ token: `file-${token}`, path: `Show/${name}`, name, size: 12345, is_dir: false, file_kind: 'video', episodes: [3], season: 1, work_status: 'matched', subtitles: [{ language: 'zh', script: 'Hans', source: 'release_claim' }] }], total_count: 1, displayed_count: 1, total_size: 12345, checked_at: new Date().toISOString(), expires_at: new Date(Date.now() + 120000).toISOString(), message: '检查没有创建下载任务' })
const item = (token: string): ServerResourceItem => ({ token, title: 'Advertised 1–99', sourceKind: 'bt', resultType: 'magnet', contentInspection: true })
let serial = 0
async function fixture(reply: (path: string, body: Record<string, unknown>) => Promise<unknown> | unknown, id = `content-${++serial}`) {
  const calls: Array<{ path: string, body: Record<string, unknown> }> = []
  const source = new ServerDataSource({ readCredential: async () => ({ accessToken: `omc_player_${'a'.repeat(43)}` }), bridge: { request: async (request) => {
    const body = (request.body ?? {}) as Record<string, unknown>
    calls.push({ path: request.path, body })
    return { status: 200, body: { code: 0, message: 'success', data: await reply(request.path, body) } }
  } } })
  await source.init({ id, name: 'Fixture', type: 'server', enabled: true, order: 0, url: 'http://127.0.0.1:3000', extra: { credentialRef: 'fixture-only' } })
  cleanup.push(() => source.destroy())
  return { source, calls }
}
function render(sourceValue: ServerDataSource, initial = [item('one')]) {
  const source = shallowRef<ServerDataSource | null>(sourceValue), items = ref(initial), owner = ref('query-one'), enabled = ref(true)
  let queue!: ReturnType<typeof useServerResourceRecognition>
  const host = defineComponent({ setup() { queue = useServerResourceRecognition(items, source, owner, enabled); return { source, ...queue } }, template: '<Content result-token="one" :source="source" :preview="contentPreviews.one" :loading="inspectingTokens.includes(\'one\')" :error="contentErrors.one" />', components: { Content } })
  const wrapper = mount(host); cleanup.push(() => wrapper.unmount())
  return { wrapper, source, items, owner, enabled, queue }
}
it('uses title-only recognition and independent actual-file/coverage lanes, without downloader creation', async () => {
  const slow = deferred<unknown>()
  const server = await fixture((path) => path.endsWith('content-preview') ? slow.promise : path.endsWith('recognize') ? { status: 'matched', title: 'Actual show', media_type: 'tv', tmdb_id: 42 } : { status: 'complete', tv: { counts: { present: 2, missing: 1, total: 3 } } })
  const view = render(server.source, [item('one'), item('two'), item('three')])
  await flushPromises()
  expect(server.calls.filter(call => call.path.endsWith('recognize'))).toHaveLength(3)
  expect(server.calls.filter(call => call.path.endsWith('recognize')).every(call => call.body.title_only === true)).toBe(true)
  expect(server.calls.filter(call => call.path.endsWith('content-preview'))).toHaveLength(2)
  expect(view.queue.coverages.value.one.present).toBe(2)
  expect(view.wrapper.text()).toContain('正在读取实际文件')
  expect(server.calls.some(call => call.path.endsWith('/downloads') || call.path.includes('share-preview'))).toBe(false)
  slow.resolve(preview()); await flushPromises()
  expect(view.wrapper.get('[aria-label="实际文件名"]').text()).toContain('Actual.Show.S01E03.zh-Hans.mkv')
  expect(view.wrapper.text()).toContain('第 3 集')
  expect(view.wrapper.text()).not.toContain('1–99')
})
it('retains physical content slots across same-id DataSource teardown/remount and rejects old account facts', async () => {
  const old = deferred<unknown>(), next = deferred<unknown>()
  const id = `same-${++serial}`
  const first = await fixture(path => path.endsWith('content-preview') ? old.promise : { status: 'unrecognized', title: 'title' }, id)
  const view = render(first.source, [item('one'), item('two')]); await flushPromises()
  expect(first.calls.filter(call => call.path.endsWith('content-preview'))).toHaveLength(2)
  view.wrapper.unmount(); first.source.destroy()
  const second = await fixture(path => path.endsWith('content-preview') ? next.promise : { status: 'unrecognized', title: 'title' }, id)
  const fresh = render(second.source); await flushPromises()
  expect(second.calls.filter(call => call.path.endsWith('content-preview'))).toHaveLength(0)
  old.resolve(preview('Old.Account.S01E01.mkv')); await flushPromises()
  expect(second.calls.filter(call => call.path.endsWith('content-preview'))).toHaveLength(1)
  expect(fresh.queue.contentPreviews.value.one).toBeUndefined()
  next.resolve(preview('New.Account.S01E03.mkv')); await flushPromises()
  expect(fresh.wrapper.text()).toContain('New.Account.S01E03.mkv')
  expect(fresh.wrapper.text()).not.toContain('Old.Account')
})
it('does not release content leases on UI timeout or let late errors/finally overwrite a new query', async () => {
  vi.useFakeTimers()
  const old = deferred<unknown>(), next = deferred<unknown>()
  let count = 0
  const server = await fixture(path => path.endsWith('content-preview') ? ++count <= 2 ? old.promise : next.promise : { status: 'unrecognized', title: 'title' })
  const view = render(server.source, [item('one'), item('two')]); await flushPromises()
  await vi.advanceTimersByTimeAsync(75000); await flushPromises()
  expect(count).toBe(2)
  view.owner.value = 'query-two'; view.items.value = [item('one')]; view.queue.retryContent('one'); await flushPromises()
  expect(count).toBe(2)
  old.reject(new Error('retired error')); await flushPromises()
  expect(count).toBe(3)
  expect(view.queue.inspectingTokens.value).toEqual(['one'])
  expect(view.wrapper.text()).not.toContain('retired error')
  next.resolve(preview()); await flushPromises()
  expect(view.queue.contentPreviews.value.one.files[0].episodes).toEqual([3])
})
it('keeps manual content revision newer than older automatic completion', async () => {
  const old = deferred<unknown>()
  const server = await fixture(path => path.endsWith('content-preview') ? old.promise : { status: 'unrecognized', title: 'title' })
  const view = render(server.source); await flushPromises()
  view.queue.acceptContent('one', parseServerResourceContent(preview('Manual.S01E03.mkv', 'manual')))
  old.resolve(preview('Automatic.S01E01.mkv')); await flushPromises()
  expect(view.wrapper.text()).toContain('Manual.S01E03.mkv'); expect(view.wrapper.text()).not.toContain('Automatic')
})
it('freezes only checked opaque file tokens and gates acquisition until that selection is confirmed', async () => {
  const server = await fixture((path, body) => path.endsWith('content-preview') ? preview() : { selection_token: 'selection-opaque', preview_token: body.preview_token, selected_entry_tokens: body.selected_entry_tokens, file_count: 1, video_count: 1, selected_bytes: 12345, total_size: 12345 })
  const recommendation = { sourceKind: 'bt' as const, recommended: { downloaderId: 'qbit', libraryId: 1 }, choices: [{ downloaderId: 'qbit', downloaderName: 'qB', downloaderType: 'qbittorrent', libraryId: 1, libraryName: 'Library', enabled: true }] }
  const wrapper = mount(Acquisition, { props: { open: true, resource: item('one'), source: server.source, libraries: [{ id: 1, name: 'Library' }], recommendation, routeError: '', loading: false, submitting: false, loadSharePreview: vi.fn() }, global: { stubs: { teleport: true, transition: false } } }); cleanup.push(() => wrapper.unmount())
  const button = (text: string) => wrapper.findAll('button').find(button => button.text() === text)!
  await button('下一步').trigger('click'); await button('下一步').trigger('click'); await flushPromises()
  expect(button('确认入库').attributes('disabled')).toBeDefined()
  await wrapper.get('[aria-label="实际文件内容"] input[type="checkbox"]').setValue(true)
  await wrapper.findAll('button').find(button => button.text().includes('确认选择'))!.trigger('click'); await flushPromises()
  expect(server.calls.at(-1)).toEqual({ path: '/api/v1/player/discovery/content-selection', body: { result_token: 'one', preview_token: 'preview-one', selected_entry_tokens: ['file-preview-one'] } })
  expect(button('确认入库').attributes('disabled')).toBeUndefined()
  await button('确认入库').trigger('click')
  expect(wrapper.emitted('confirm')?.[0]?.[0]).toEqual(expect.objectContaining({ contentSelection: expect.objectContaining({ selectionToken: 'selection-opaque', selectedBytes: 12345 }) }))
  await wrapper.get('[aria-label="实际文件内容"] input[type="checkbox"]').setValue(false)
  expect(button('确认入库').attributes('disabled')).toBeDefined()
})
it('rejects expired/unsafe content, strips private metadata, and validates subtitle policy without inventing legacy defaults', () => {
  const parsed = parseServerResourceContent({ ...preview(), native_index: 15, info_hash: 'private', files: [{ ...preview().files[0], provider_id: 'private' }] })
  expect(JSON.stringify(parsed)).not.toContain('private')
  for (const path of ['/etc/video.mkv', '../video.mkv', 'C:/video.mkv']) expect(() => parseServerResourceContent({ ...preview(), files: [{ ...preview().files[0], path }] })).toThrow()
  expect(() => parseServerResourceContent({ ...preview(), expires_at: new Date(0).toISOString() })).toThrow()
  expect(parseServerSubtitlePolicy(undefined)).toBeUndefined()
  expect(parseServerSubtitlePolicy({ mode: 'prefer', languages: ['zh-Hans', 'zh-Hant'] })).toEqual({ mode: 'prefer', languages: ['zh-Hans', 'zh-Hant'] })
  expect(parseServerSubtitlePolicy({ mode: 'require', languages: [] })).toBeUndefined()
})

it('sends the frozen opaque selection through the final download field and never native file indices', async () => {
  const server = await fixture(() => ({}))
  await server.source.createDiscoveryDownload({ result_token: 'result', downloader_id: 'qbit', priority: 0, content_selection_token: 'opaque-selected-files' })
  expect(server.calls.at(-1)).toEqual({ path: '/api/v1/player/discovery/downloads', body: { result_token: 'result', downloader_id: 'qbit', priority: 0, content_selection_token: 'opaque-selected-files' } })
})

it('keeps files mode blocked after metadata failure, and requires explicit whole-resource opt-in for trusted BT', async () => {
  const server = await fixture(() => { throw new Error('metadata unavailable') })
  const recommendation = { sourceKind: 'bt' as const, recommended: { downloaderId: 'qbit', libraryId: 1 }, choices: [{ downloaderId: 'qbit', downloaderName: 'qB', downloaderType: 'qbittorrent', libraryId: 1, libraryName: 'Library', enabled: true }] }
  const wrapper = mount(Acquisition, { props: { open: true, resource: item('whole'), source: server.source, libraries: [{ id: 1, name: 'Library' }], recommendation, routeError: '', loading: false, submitting: false, loadSharePreview: vi.fn() }, global: { stubs: { teleport: true, transition: false } } }); cleanup.push(() => wrapper.unmount())
  const button = (text: string) => wrapper.findAll('button').find(button => button.text() === text)!
  await button('下一步').trigger('click'); await button('下一步').trigger('click'); await flushPromises()
  expect(wrapper.text()).toContain('metadata unavailable')
  expect(button('确认入库').attributes('disabled')).toBeDefined()
  expect(wrapper.emitted('confirm')).toBeUndefined()
  await wrapper.get('input[type="checkbox"]').setValue(true)
  expect(wrapper.text()).toContain('内部文件未经本次选择核实')
  expect(button('确认入库').attributes('disabled')).toBeUndefined()
  await button('确认入库').trigger('click')
  expect(wrapper.emitted('confirm')?.[0]?.[0]).toEqual({ downloaderId: 'qbit', libraryId: 1, contentMode: 'all' })
  await wrapper.get('input[type="checkbox"]').setValue(false)
  expect(button('确认入库').attributes('disabled')).toBeDefined()
})
it('never offers whole-resource fallback for a cloud share after content inspection fails', async () => {
  const server = await fixture(() => { throw new Error('share unavailable') })
  const resource = { ...item('cloud'), sourceKind: '115_share', cloudProvider: '115' }
  const recommendation = { sourceKind: '115_share' as const, recommended: { downloaderId: 'receiver', libraryId: 1 }, choices: [{ downloaderId: 'receiver', downloaderName: 'Receiver', downloaderType: 'pan115_offline', libraryId: 1, libraryName: 'Library', enabled: true }] }
  const wrapper = mount(Acquisition, { props: { open: true, resource, source: server.source, libraries: [{ id: 1, name: 'Library' }], recommendation, routeError: '', loading: false, submitting: false, loadSharePreview: vi.fn() }, global: { stubs: { teleport: true, transition: false } } }); cleanup.push(() => wrapper.unmount())
  const button = (text: string) => wrapper.findAll('button').find(button => button.text() === text)!
  await button('下一步').trigger('click'); await button('下一步').trigger('click'); await flushPromises()
  expect(wrapper.text()).toContain('share unavailable')
  expect(wrapper.text()).not.toContain('明确获取整个资源')
  expect(button('确认入库').attributes('disabled')).toBeDefined()
})
