// @vitest-environment happy-dom
import type { DataSource, MediaDetail, MediaItem } from '../src/services/datasource/types'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { markRaw, reactive, shallowReactive } from 'vue'
import { useRoute } from 'vue-router'
import Detail from '../src/views/MediaDetailView.vue'
import { ServerDataSource, ServerRequestError } from '../src/services/datasource/server'
import { savePlaybackMediaContext } from '../src/services/playbackContext'
import { PLAYED_STATE_CHANGED_EVENT } from '../src/services/playbackHistory'
import { dispatchServerLibraryRefresh } from '../src/services/serverMediaChanges'

const state = vi.hoisted(() => ({ configs: [] as Array<Record<string, any>>, sources: new Map<string, DataSource>() }))
const offlineRead = vi.hoisted(() => vi.fn())
const offlineConvert = vi.hoisted(() => vi.fn())
const progressRead = vi.hoisted(() => vi.fn())
vi.mock('vue-router', async () => {
  const { reactive } = await import('vue')
  const route = reactive({ params: { sourceId: 'fixture', itemId: 'server-acquisition|9|tv|100' }, query: {}, fullPath: '/legacy' })
  return { useRoute: () => route, useRouter: () => ({ push: vi.fn() }) }
})
vi.mock('@/stores/datasource', () => ({ useDataSourceStore: () => ({ get configs() { return state.configs }, loadConfigs: vi.fn(), syncManager: vi.fn(), getSource: (id: string) => state.sources.get(id) }) }))
vi.mock('@/services/downloads', () => ({ getOfflineDetail: offlineRead }))
vi.mock('@/services/datasource/offline', () => ({ toOfflineMediaDetail: offlineConvert }))
vi.mock('@/services/playbackHistory', async original => ({ ...await original<object>(), getPlaybackProgress: progressRead }))
vi.mock('@/services/runtimePlatform', () => ({ isNativeAndroidRuntime: () => false }))
vi.mock('@/services/playerInteractionSettings', () => ({ loadPlayerInteractionSettings: () => ({ mobileEpisodeLayout: 'rail' }) }))

const cleanups: Array<() => void> = []
const token = btoa('series:tmdb:100').replace(/=+$/, '')
const workID = `work|9|${token}`
const movieToken = btoa('movie:tmdb:200').replace(/=+$/, '')
const movieID = `work|9|${movieToken}`
const legacyID = 'server-acquisition|9|tv|100'
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
function mediaItem(movie = false) {
  const id = movie ? movieToken : token
  return { id, library_id: 9, item_token: `work|9|${id}`, history_identity: movie ? `server:v1:movie:9:${id}` : undefined, title: movie ? 'Current movie' : 'Published show', kind: movie ? 'movie' : 'series', work_identity: { scheme: 'tmdb', media_type: movie ? 'movie' : 'series', value: movie ? '200' : '100' }, tmdb_id: movie ? 200 : 100, added_at: '2026-10-23T00:00:00Z', file_count: 2, season_count: 1, episode_count: 2 }
}
function version(episode: number, movie = false, season = 1) {
  const id = movie ? 200 : season === 1 ? episode : season * 100 + episode
  return { id, title: movie ? 'Current movie version' : `Published episode ${episode}`, season: movie ? undefined : season, episode: movie ? undefined : episode, playable: true, item_token: `entry|9|${movie ? movieToken : token}|${id}`, history_identity: movie ? `server:v1:movie:9:${movieToken}` : `server:v1:episode:9:${token}:${season}:${episode}`, exact_identity: `server:entry:${id}`, size: 100, delivery_kind: 'server_redirect' }
}
async function fixture() {
  const api = { published: false, phase: 'queued', episodes: 1, seasons: 1, catalogError: 0, pending: undefined as ReturnType<typeof deferred<unknown>> | undefined }
  const paths: string[] = []
  const source = markRaw(new ServerDataSource({ readCredential: async () => ({ accessToken: `omc_player_${'a'.repeat(43)}` }), bridge: { request: async (request) => {
    paths.push(request.path)
    let data: unknown
    if (request.path.endsWith('/acquisition')) {
      data = api.pending ? await api.pending.promise : { media_type: 'tv', tmdb_id: 100, stage: api.phase === 'completed' ? 'library' : 'download', status: api.phase, processed_files: 0, total_files: 0, revision: 1, ...(api.published ? { catalog_target: { library_id: 9, work_token: token } } : {}) }
    }
    else if (request.path.includes('/discovery/details/')) { data = { work: { provider: 'tmdb', provider_id: '100', media_type: 'tv', title: 'Queued shell', tmdb_id: 100 }, genres: [], directors: [], cast: [] } }
    else if (request.path.includes('/catalog/')) {
      if (api.catalogError)
        return { status: api.catalogError, body: { code: 'permission_denied', message: 'Catalog access denied', data: {} } }
      const movie = request.path.endsWith(movieToken)
      data = { item: mediaItem(movie), versions: movie ? [version(1, true)] : Array.from({ length: api.seasons }, (_, season) => Array.from({ length: api.episodes }, (_, index) => version(index + 1, false, season + 1))).flat() }
    }
    else { throw new Error(`Unexpected fixture path: ${request.path}`) }
    return { status: 200, body: { code: 0, message: 'success', data } }
  } } }))
  const config = { id: 'fixture', name: 'Fixture Server', type: 'server' as const, enabled: true, order: 0, url: 'http://127.0.0.1:3000', extra: { credentialRef: 'fixture', credentialVersion: 1, deviceId: 'fixture-device' } }
  await source.init(config)
  state.sources.set('fixture', source)
  state.configs.push(config)
  cleanups.push(() => source.destroy())
  return { source, api, paths }
}
function render() {
  const wrapper = mount(Detail, { global: { stubs: { MediaDetailHero: { props: ['title'], template: '<div><h1>{{ title }}</h1><slot name="actions" /></div>' }, CachedImage: true, ImmersiveMediaRail: true, MediaGrid: true, teleport: true } } })
  cleanups.push(() => wrapper.unmount())
  return wrapper
}
beforeEach(() => {
  state.configs = reactive([])
  state.sources = shallowReactive(new Map())
  offlineRead.mockReset().mockResolvedValue(undefined)
  offlineConvert.mockReset()
  progressRead.mockReset().mockResolvedValue(undefined)
  Object.assign(useRoute().params, { sourceId: 'fixture', itemId: legacyID })
  Object.keys(useRoute().query).forEach(key => delete useRoute().query[key])
  Object.defineProperty(window, 'matchMedia', { configurable: true, value: () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }) })
  HTMLElement.prototype.scrollIntoView = vi.fn()
  HTMLElement.prototype.scrollTo = vi.fn()
})
afterEach(() => { cleanups.splice(0).reverse().forEach(stop => stop()); document.body.innerHTML = ''; vi.restoreAllMocks() })

it('rehydrates an open legacy shell via actual API parsing and DataSource after publication and focus', async () => {
  const { api, paths } = await fixture()
  const wrapper = render()
  await flushPromises()
  expect(wrapper.text()).toContain('Queued shell')
  expect(wrapper.text()).toContain('等待执行')
  expect(wrapper.findAll('.episode-card')).toHaveLength(0)
  api.published = true
  dispatchServerLibraryRefresh({ sourceId: 'fixture', libraryIds: ['9'], libraryRevisions: { 9: 2 }, resyncRequired: false, version: 1 })
  await flushPromises()
  expect(wrapper.text()).toContain('Published show')
  expect(wrapper.text()).toContain('等待执行')
  expect(wrapper.findAll('.episode-card')).toHaveLength(1)
  expect(paths.filter(path => path.endsWith(`/catalog/${token}`)).length).toBeGreaterThanOrEqual(3)
  api.episodes = 2
  api.phase = 'completed'
  window.dispatchEvent(new Event('focus'))
  await flushPromises()
  expect(wrapper.findAll('.episode-card')).toHaveLength(2)
  expect(wrapper.text()).toContain('已入库')
  expect(wrapper.text()).not.toContain('queued')
  expect(wrapper.text()).not.toContain('completed')
})

it.each(['queued', 'running', 'failed'])('keeps real versions playable while the latest task remains %s', async (phase) => {
  const { api } = await fixture()
  api.published = true
  api.phase = phase
  const wrapper = render()
  await flushPromises()
  expect(wrapper.text()).toContain('Published show')
  expect(wrapper.findAll('.episode-card')).toHaveLength(1)
  expect(wrapper.findAll('button').some(button => /播放/.test(button.text()))).toBe(true)
  expect(wrapper.text()).not.toContain('已入库')
})

it('rejects late acquisition status on route/source configuration changes before any catalog follow-up', async () => {
  const { api, paths } = await fixture()
  const pending = deferred<unknown>()
  api.pending = pending
  const wrapper = render()
  await flushPromises()
  const next = markRaw({ id: 'next', type: 'server', getDetail: vi.fn().mockResolvedValue({ id: 'next-movie', sourceId: 'next', type: 'movie', name: 'New account movie', path: '', mediaSources: [] }) } as unknown as DataSource)
  state.sources.set('next', next)
  state.configs.push({ id: 'next', type: 'server', enabled: true })
  Object.assign(useRoute().params, { sourceId: 'next', itemId: 'next-movie' })
  await flushPromises()
  pending.resolve({ media_type: 'tv', tmdb_id: 100, stage: 'library', status: 'completed', catalog_target: { library_id: 9, work_token: token } })
  await flushPromises()
  expect(wrapper.text()).toContain('New account movie')
  expect(wrapper.text()).not.toContain('Published show')
  // The old native call can finish, but the view cannot follow it or restore old media.
  expect(wrapper.findAll('.episode-card')).toHaveLength(0)
  expect(paths.filter(path => path.endsWith('/acquisition'))).toHaveLength(1)
  expect(paths.some(path => path.includes('/catalog/'))).toBe(false)
})

it('fences late seasons, progress, errors and finally after credential replacement and new loading state', async () => {
  const { source, api } = await fixture()
  api.published = true
  const pendingSeason = deferred<MediaItem[]>()
  const list = vi.spyOn(source, 'list').mockImplementationOnce(() => pendingSeason.promise)
  const wrapper = render()
  await flushPromises()
  expect(wrapper.text()).toContain('Published show')
  const replacementDetail = deferred<MediaDetail>()
  const replacement = markRaw({ id: 'fixture', type: 'server', getDetail: vi.fn().mockImplementation(() => replacementDetail.promise) } as unknown as DataSource)
  state.sources.set('fixture', replacement)
  state.configs[0]!.extra.credentialVersion++
  await flushPromises()
  expect(wrapper.get('[aria-live="polite"]').text()).toContain('正在加载')
  pendingSeason.reject(new Error('obsolete season error'))
  await flushPromises()
  expect(wrapper.get('[aria-live="polite"]').text()).toContain('正在加载')
  expect(wrapper.text()).not.toContain('obsolete season error')
  replacementDetail.resolve({ id: movieID, sourceId: 'fixture', name: 'Replacement movie', type: 'movie', path: '', mediaSources: [] })
  await flushPromises()
  expect(wrapper.text()).toContain('Replacement movie')
  expect(wrapper.findAll('.episode-card')).toHaveLength(0)
  expect(list).toHaveBeenCalledTimes(1)
})

it('ignores old progress and its finally while the next route is still loading', async () => {
  const { source, api } = await fixture()
  api.published = true
  const pendingProgress = deferred<undefined>()
  progressRead.mockImplementationOnce(() => pendingProgress.promise)
  const wrapper = render()
  await flushPromises()
  const pendingMovie = deferred<MediaDetail>()
  vi.spyOn(source, 'getDetail').mockImplementationOnce(() => pendingMovie.promise)
  useRoute().params.itemId = movieID
  await flushPromises()
  pendingProgress.resolve(undefined)
  await flushPromises()
  expect(wrapper.get('[aria-live="polite"]').text()).toContain('正在加载')
  pendingMovie.resolve({ id: movieID, sourceId: 'fixture', name: 'New route movie', type: 'movie', path: '', mediaSources: [] })
  await flushPromises()
  expect(wrapper.text()).toContain('New route movie')
  expect(wrapper.findAll('.episode-card')).toHaveLength(0)
})

it('never revives an offline shell for a denied catalog or legacy acquisition', async () => {
  const { source } = await fixture()
  offlineRead.mockResolvedValue({ fixture: true })
  offlineConvert.mockResolvedValue({ id: movieID, sourceId: 'fixture', name: 'Forbidden cached movie', type: 'movie', path: '', mediaSources: [] })
  vi.spyOn(source, 'getDetail').mockRejectedValue(new ServerRequestError('Catalog access denied', 403, 'permission_denied'))
  useRoute().params.itemId = movieID
  const contextId = savePlaybackMediaContext({ sourceId: 'fixture', itemId: movieID, detail: { id: movieID, sourceId: 'fixture', name: 'Forbidden cached movie', type: 'movie', path: '', mediaSources: [] } })
  Object.assign(useRoute().query, { contextId })
  const wrapper = render()
  await flushPromises()
  expect(wrapper.text()).toContain('Catalog access denied')
  expect(wrapper.text()).not.toContain('Forbidden cached movie')
  expect(offlineRead).not.toHaveBeenCalled()
  useRoute().params.itemId = legacyID
  await flushPromises()
  expect(offlineRead).not.toHaveBeenCalled()
})

it('still opens a real downloaded movie on a network failure', async () => {
  const { source } = await fixture()
  vi.spyOn(source, 'getDetail').mockRejectedValue(new Error('Failed to fetch'))
  offlineRead.mockResolvedValue({ fixture: true })
  offlineConvert.mockResolvedValue({ id: movieID, sourceId: 'fixture', name: 'Downloaded offline movie', type: 'movie', path: '', mediaSources: [] })
  useRoute().params.itemId = movieID
  const wrapper = render()
  await flushPromises()
  expect(wrapper.text()).toContain('Downloaded offline movie')
  expect(offlineRead).toHaveBeenCalledWith('fixture', movieID)
})

it.each(['removed', 'disabled'])('does not restore old catalog context after the owning Server is %s', async (retirement) => {
  const { api } = await fixture()
  api.published = true
  useRoute().params.itemId = movieID
  const contextId = savePlaybackMediaContext({ sourceId: 'fixture', itemId: movieID, detail: { id: movieID, sourceId: 'fixture', originType: 'server', name: 'Retired Server cached movie', type: 'movie', path: '', mediaSources: [] } })
  Object.assign(useRoute().query, { contextId })
  const wrapper = render()
  await flushPromises()
  expect(wrapper.text()).toContain('Current movie')
  offlineRead.mockResolvedValue({ fixture: true })
  offlineConvert.mockResolvedValue({ id: movieID, sourceId: 'fixture', name: 'Retired offline shell', type: 'movie', path: '', mediaSources: [] })
  state.sources.delete('fixture')
  if (retirement === 'removed')
    state.configs.splice(0)
  else
    state.configs[0]!.enabled = false
  await flushPromises()
  expect(wrapper.text()).not.toContain('Retired Server cached movie')
  expect(wrapper.text()).not.toContain('Retired offline shell')
  expect(wrapper.text()).toContain('数据源不可用')
  expect(offlineRead).not.toHaveBeenCalled()
})

it('retains the independent offline owner and other enabled sources playback context', async () => {
  const source = markRaw({ id: 'fixture', type: 'offline', getDetail: vi.fn() } as unknown as DataSource)
  state.sources.set('fixture', source)
  useRoute().params.itemId = movieID
  const contextId = savePlaybackMediaContext({ sourceId: 'fixture', itemId: movieID, detail: { id: movieID, sourceId: 'fixture', name: 'Independent owned movie', type: 'movie', path: '', mediaSources: [] } })
  Object.assign(useRoute().query, { contextId })
  const wrapper = render()
  await flushPromises()
  expect(wrapper.text()).toContain('Independent owned movie')
  expect(source.getDetail).not.toHaveBeenCalled()
  const emby = markRaw({ id: 'fixture', type: 'emby', getDetail: vi.fn() } as unknown as DataSource)
  state.sources.set('fixture', emby)
  state.configs.push({ id: 'fixture', type: 'emby', enabled: true })
  await flushPromises()
  expect(wrapper.text()).toContain('Independent owned movie')
  expect(emby.getDetail).not.toHaveBeenCalled()
})

it('preserves the selected season and episode on focus, and falls back only when the season disappears', async () => {
  const { api } = await fixture()
  api.published = true
  api.seasons = 2
  api.episodes = 2
  const wrapper = render()
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === '第 2 季')!.trigger('click')
  await flushPromises()
  await wrapper.get('[aria-label="选择下一集"]').trigger('click')
  expect(wrapper.find('.episode-card.is-selected').text()).toContain('Published episode 2')
  window.dispatchEvent(new Event('focus'))
  await flushPromises()
  expect(wrapper.text()).toContain('第 2 季 · 2 集')
  expect(wrapper.find('.episode-card.is-selected').text()).toContain('Published episode 2')
  window.dispatchEvent(new Event(PLAYED_STATE_CHANGED_EVENT))
  await flushPromises()
  expect(wrapper.text()).toContain('第 2 季 · 2 集')
  expect(wrapper.find('.episode-card.is-selected').text()).toContain('Published episode 2')
  api.seasons = 1
  window.dispatchEvent(new Event('focus'))
  await flushPromises()
  expect(wrapper.text()).toContain('第 1 季 · 2 集')
  expect(wrapper.text()).not.toContain('第 2 季 · 2 集')
})

it('preserves a selected version on same-scope refresh and resets it for a different route', async () => {
  const { source } = await fixture()
  let includeHigh = true
  vi.spyOn(source, 'getDetail').mockImplementation(async () => ({
    id: movieID, sourceId: 'fixture', name: 'Published movie', type: 'movie', path: '',
    mediaSources: [{ id: 'low', name: 'Low', size: 100 }, ...(includeHigh ? [{ id: 'high', name: 'High', size: 200 }] : [])],
    acquisition: { stage: 'download', status: 'queued', revision: 1 },
  }))
  useRoute().params.itemId = 'server-acquisition|9|movie|200'
  const wrapper = render()
  await flushPromises()
  const selectedVersion = () => wrapper.findAll('button').find(button => button.text().startsWith('版本') && button.classes().includes('bg-white/14'))?.text()
  await wrapper.findAll('button').find(button => button.text().startsWith('版本 2'))!.trigger('click')
  expect(selectedVersion()).toContain('版本 2')
  window.dispatchEvent(new Event('focus'))
  await flushPromises()
  expect(selectedVersion()).toContain('版本 2')
  useRoute().params.itemId = 'server-acquisition|9|movie|201'
  await flushPromises()
  expect(selectedVersion()).toContain('版本 1')
  includeHigh = false
  window.dispatchEvent(new Event('focus'))
  await flushPromises()
  expect(wrapper.text()).not.toContain('200 B')
})

it('stops late status from starting a catalog read after unmount', async () => {
  const { api, paths } = await fixture()
  const pending = deferred<unknown>()
  api.pending = pending
  const wrapper = render()
  await flushPromises()
  wrapper.unmount()
  pending.resolve({ media_type: 'tv', tmdb_id: 100, stage: 'library', status: 'completed', catalog_target: { library_id: 9, work_token: token } })
  await flushPromises()
  expect(paths).toHaveLength(1)
})
