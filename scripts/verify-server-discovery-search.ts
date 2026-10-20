import assert from 'node:assert/strict'
import type { ServerBridge, ServerDiscoveryStreamEvent, ServerDiscoveryStreamHandle } from '../src/services/datasource/server.ts'
import type { ServerResourceGroup, ServerResourceStreamEvent, ServerSearchProgress, ServerSearchSite } from '../src/services/serverDiscovery.ts'
import { ServerDataSource } from '../src/services/datasource/server.ts'
import { streamServerResources } from '../src/services/serverDiscovery.ts'
import { beginResourceSearch, emptyResourceSearch, finishResourceSearch, reduceResourceSearch, resourceGroupIsPrevious, resourceGroupPageIsChanging, resourceGroupRetryPage, ResourceSearchController, searchIncompleteLabel, searchPhaseLabel } from '../src/services/serverDiscoverySearch.ts'

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

const sites: ServerSearchSite[] = [2, 1].map(id => ({ id, name: '站点 ' + id, searchable: true, siteType: 'bt' }))
function group(siteId = 1, tokens = ['a'], extra: Partial<ServerResourceGroup> = {}): ServerResourceGroup {
  return { siteId, siteName: '站点 ' + siteId, siteType: 'bt', status: 'success', page: 1, hasNext: true, skipped: 0, items: tokens.map(token => ({ token, title: '资源 ' + token })), ...extra }
}
function progress(extra: Partial<ServerSearchProgress> = {}): ServerSearchProgress {
  return { total: 1, pending: 0, running: 1, completed: 0, succeeded: 0, failed: 0, resultCount: 0, ...extra }
}
function update(state: ReturnType<typeof emptyResourceSearch>, value: ServerResourceGroup) {
  return reduceResourceSearch(state, { type: 'site', group: value }, sites, 2000)
}
function begin(previous = emptyResourceSearch(), siteIds = [1], replaceAll = true, page = 1) {
  return beginResourceSearch(previous, { sites, siteIds, replaceAll, page }, 1000)
}

// One upstream batch can be slow while actual phase, alias and counts remain visible.
let state = begin()
state = reduceResourceSearch(state, { type: 'progress', progress: progress({ siteDetail: { siteId: 1, siteName: '站点 1', phase: 'waiting_rate_limit', queryName: '名称一', queryIndex: 1, queriesCompleted: 0, queriesTotal: 3, resultCount: 0, elapsedMs: 500, revision: 1 } }) }, sites, 1500)
assert.equal(state.progress.completed, 0)
assert.equal(state.activities[0].detail?.phase, 'waiting_rate_limit')
assert.equal(state.groups.length, 0)
assert.equal(searchPhaseLabel(state.activities[0].detail?.phase), '正在等待站点限速放行')
assert.equal(searchPhaseLabel('unknown'), '正在等待 Server 搜索进度')
state = update(state, group(1, ['a'], { complete: false, revision: 1, queriesCompleted: 1, queriesTotal: 3 }))
const selected = state.groups[0].items[0]
state = update(state, group(1, ['a', 'a', 'b'], { complete: false, revision: 2, queriesCompleted: 2, queriesTotal: 3 }))
assert.deepEqual(state.groups[0].items.map(item => item.token), ['a', 'b'])
assert.equal(state.groups[0].items[0], selected, 'An unchanged item keeps its object and keyed card identity')
assert.equal(state.progress.completed, 0, 'Cumulative batches do not count as completed sites')
const latest = state
assert.equal(update(state, group(1, [], { revision: 1 })), latest)
assert.equal(update(state, group(1, [], { revision: 2 })), latest)
assert.equal(update(state, group(2, ['foreign'], { revision: 10 })), latest)
assert.equal(update(state, group(1, ['wrong-page'], { page: 2, revision: 3 })), latest)
state = update(state, group(1, ['a', 'b'], { complete: true, revision: 3, partial: true, incompleteReason: 'request_timeout', queriesCompleted: 2, queriesFailed: 1, queriesTotal: 3 }))
state = reduceResourceSearch(state, { type: 'done', progress: progress({ running: 0, completed: 1, succeeded: 1, resultCount: 2 }) }, sites, 3000)
assert.equal(state.outcome, 'partial')
assert.equal(state.groups[0].items.length, 2)
assert.equal(state.groups[0].queriesFailed, 1)
assert.equal(state.progress.completed, 1)
assert.equal(selected.token, 'a', 'The current acquisition selection is not rebound to another snapshot')
assert.equal(searchIncompleteLabel('request_timeout'), '站点请求超时')
assert.equal(searchIncompleteLabel('unknown-provider-error'), '部分查询未完成')
assert.equal(searchIncompleteLabel('resource_browser_login_pending'), '管理员正在完成站点登录，请稍后重试')
assert.equal(searchIncompleteLabel('resource_browser_restore_timeout'), '恢复登录会话超时，尚未确认登录失效')
assert.equal(searchIncompleteLabel('resource_rate_limited'), '站点正在限速')
assert.equal(searchIncompleteLabel('resource_browser_verification_required'), '站点需要重新完成网页验证')

// Partial transport failure and an empty failure snapshot preserve published results.
let interrupted = update(begin(), group(1, ['a'], { complete: false, revision: 1 }))
interrupted = update(interrupted, group(1, [], { status: 'error', complete: true, revision: 2 }))
assert.equal(interrupted.groups[0].items[0].token, 'a')
assert.equal(interrupted.groups[0].partial, true)
interrupted = finishResourceSearch(interrupted, 'failed', '网络中断', 2500)
assert.equal(interrupted.outcome, 'partial')
assert.equal(interrupted.error, '网络中断')
const withoutFinal = finishResourceSearch(update(begin(), group(1, ['a'], { complete: false, revision: 1 })), 'complete', '', 2500)
assert.equal(withoutFinal.outcome, 'partial')
assert.equal(withoutFinal.groups[0].incompleteReason, 'stream_interrupted')
assert.equal(finishResourceSearch(begin(), 'failed').outcome, 'failed')
assert.equal(finishResourceSearch(begin(), 'cancelled').outcome, 'cancelled')

// Legacy groups remain terminal; result order follows configured sites rather than arrival.
let legacy = begin(emptyResourceSearch(), [1, 2])
legacy = update(legacy, group(1, ['a']))
legacy = update(legacy, group(2, ['b']))
legacy = finishResourceSearch(legacy, 'complete')
assert.deepEqual(legacy.groups.map(item => item.siteId), [2, 1])
assert.equal(legacy.outcome, 'complete')
const empty = finishResourceSearch(update(begin(), group(1, [])), 'complete')
assert.equal(empty.outcome, 'complete', 'Only a fully successful empty result is authoritative no-match')
const unfinishedEmpty = finishResourceSearch(update(begin(), group(1, [], { partial: true, complete: true, queriesCompleted: 1, queriesTotal: 2 })), 'complete')
assert.equal(unfinishedEmpty.outcome, 'partial')

// Retry retains previous results until a useful/authoritative replacement; other sites stay intact.
let retry = begin(legacy, [1], false)
assert.equal(resourceGroupIsPrevious(retry, 1), true)
assert.equal(retry.groups[0], legacy.groups[0])
retry = update(retry, group(1, [], { complete: false, revision: 1 }))
assert.equal(resourceGroupIsPrevious(retry, 1), true)
retry = update(retry, group(1, [], { status: 'error', complete: true, revision: 2 }))
retry = finishResourceSearch(retry, 'complete')
assert.equal(retry.groups.find(item => item.siteId === 1)?.items[0].token, 'a')
assert.equal(resourceGroupIsPrevious(retry, 1), true)
assert.equal(retry.outcome, 'partial')
let successfulRetry = begin(retry, [1], false)
successfulRetry = update(successfulRetry, group(1, ['replacement'], { complete: false, revision: 1 }))
assert.equal(resourceGroupIsPrevious(successfulRetry, 1), false)
assert.deepEqual(successfulRetry.groups.find(item => item.siteId === 1)?.items.map(item => item.token), ['replacement'])
successfulRetry = update(successfulRetry, group(1, [], { complete: true, revision: 2 }))
assert.equal(successfulRetry.groups.find(item => item.siteId === 1)?.items.length, 0)
assert.equal(successfulRetry.groups[0], legacy.groups[0], 'Retry does not replace an unrelated site')
let nextPage = begin(legacy, [1], false, 2)
const oldPage = nextPage.groups.find(item => item.siteId === 1)!
assert.equal(resourceGroupPageIsChanging(nextPage, oldPage), true)
const failedNextPage = finishResourceSearch(update(nextPage, group(1, [], { page: 2, status: 'error', complete: true, revision: 1, errorCode: 'request_timeout' })), 'complete')
assert.equal(failedNextPage.groups.find(item => item.siteId === 1)?.page, 1, 'Failed paging retains the old page without mixing its rows into the new page')
assert.equal(resourceGroupRetryPage(failedNextPage, 1), 2, 'Retry repeats the requested failed page rather than the retained old page')
let retriedNextPage = begin(failedNextPage, [1], false, resourceGroupRetryPage(failedNextPage, 1))
retriedNextPage = update(retriedNextPage, group(1, ['retried-page2'], { page: 2, complete: true, revision: 1 }))
assert.equal(retriedNextPage.groups.find(item => item.siteId === 1)?.page, 2)
assert.deepEqual(retriedNextPage.groups.find(item => item.siteId === 1)?.items.map(item => item.token), ['retried-page2'])
assert.equal(retriedNextPage.groups[0], legacy.groups[0], 'Retrying the failed page leaves other sites untouched')
nextPage = update(nextPage, group(1, ['page2'], { page: 2, complete: false, revision: 1 }))
assert.equal(resourceGroupPageIsChanging(nextPage, nextPage.groups.find(item => item.siteId === 1)!), false)
assert.deepEqual(nextPage.groups.find(item => item.siteId === 1)?.items.map(item => item.token), ['page2'])

// Out-of-order stage revisions cannot replace newer detail or regress overall terminal counts.
let stages = begin()
for (const revision of [3, 2, 3]) {
  stages = reduceResourceSearch(stages, { type: 'progress', progress: progress({ siteDetail: { siteId: 1, siteName: '站点 1', phase: revision === 3 ? 'querying' : 'queued', revision } }) }, sites)
}
assert.equal(stages.activities[0].detail?.phase, 'querying')
let preparing = begin()
preparing = reduceResourceSearch(preparing, { type: 'progress', progress: progress({ total: 0, running: 0, phase: 'preparing_media' }) }, sites)
assert.equal(preparing.progress.total, 1, 'Keep the selected site count while Server prepares media before scoping sites')
assert.equal(preparing.progress.phase, 'preparing_media')
preparing = reduceResourceSearch(preparing, { type: 'progress', progress: progress({ eventSequence: 2 }) }, sites)
const sequenced = preparing
assert.equal(reduceResourceSearch(preparing, { type: 'progress', progress: progress({ eventSequence: 1, phase: 'preparing_media' }) }, sites), sequenced)
preparing = update(preparing, group(1, ['a'], { complete: true, revision: 1 }))
preparing = reduceResourceSearch(preparing, { type: 'done', progress: progress({ eventSequence: 2, completed: 1, succeeded: 1, running: 0 }) }, sites)
assert.equal(preparing.outcome, 'complete', 'Done reuses the final progress sequence and still finalizes the search')

// Runtime progress must belong to this exact scope and preserve the count identities.
const scopedProgress = begin()
for (const invalid of [
  progress({ total: 2, pending: 1 }),
  progress({ siteId: 2 }),
  progress({ siteDetail: { siteId: 2, siteName: '外部站点', phase: 'querying' } }),
  progress({ completed: 1 }),
  progress({ resultCount: -1 }),
  progress({ resultCount: 1.5 }),
  progress({ total: 0, running: 0, phase: 'querying' }),
]) {
  assert.equal(reduceResourceSearch(scopedProgress, { type: 'progress', progress: invalid }, sites), scopedProgress)
}
const invalidDone = reduceResourceSearch(scopedProgress, { type: 'done', progress: progress({ total: 0, running: 0 }) }, sites)
assert.equal(invalidDone.outcome, 'failed', 'Malformed done cannot become an authoritative empty search')
const prematureDone = reduceResourceSearch(update(begin(), group(1, ['a'], { complete: true })), { type: 'done', progress: progress() }, sites)
assert.equal(prematureDone.outcome, 'partial', 'A terminal envelope must confirm every selected site even when some rows arrived')
assert.equal(prematureDone.groups[0].items[0].token, 'a')

// Actual typed service and DataSource bridge: one v2 native request, no JSON fallback.
const opened: Array<{ requestId: string, path: string, token?: string, emit: (event: ServerDiscoveryStreamEvent) => void, done: ReturnType<typeof deferred<void>> }> = []
const cancelled: string[] = []
let jsonCalls = 0
const bridge: ServerBridge = {
  request: async () => { jsonCalls++; throw new Error('Unexpected JSON request') },
  stream: async (request, requestId, emit) => {
    const done = deferred<void>()
    opened.push({ requestId, path: request.path, token: request.accessToken, emit, done })
    await done.promise
  },
  cancelStream: async (requestId) => {
    cancelled.push(requestId)
    opened.find(item => item.requestId === requestId)?.done.resolve()
  },
}
const token = 'omc_player_' + 'a'.repeat(43)
const sourceConfig = { id: 'server-test', name: 'Server', type: 'server' as const, order: 0, url: 'http://server.example.test:3000', extra: { deviceId: 'device', credentialRef: 'source-secret-ref' } }
const source = new ServerDataSource({ bridge, readCredential: async () => ({ accessToken: token }) })
await source.init(sourceConfig)
const events: ServerResourceStreamEvent[] = []
for (const input of [
  { mediaType: 'movie' as const, tmdbId: 346, siteIds: [1, 2] },
  { mediaType: 'movie' as const, title: 'Seven Samurai', direct: true, siteIds: [1], page: 1 },
  { mediaType: 'tv' as const, tmdbId: 100, direct: true, siteIds: [2], page: 2 },
]) {
  const handle = await streamServerResources(source, input, event => events.push(event))
  const current = opened.at(-1)!
  const url = new URL('http://server.example.test' + current.path)
  assert.equal(url.searchParams.get('progress_version'), '2')
  assert.equal(url.searchParams.get('page'), String(input.page ?? 1))
  assert.deepEqual(url.searchParams.getAll('site_ids'), input.siteIds.map(String))
  assert.equal(current.token, token)
  current.emit({ event: 'progress', data: { total: 1, running: 1, site_detail: { site_id: 1, site_name: '站点 1', phase: 'querying', query_name: '名称', query_index: 1, queries_completed: 0, queries_failed: 0, queries_total: 3, result_count: 0, elapsed_ms: 1000, revision: 1 } } })
  current.emit({ event: 'site', data: { site_id: 1, site_name: '站点 1', status: 'success', page: 1, complete: false, revision: 1, partial: false, queries_completed: 1, queries_total: 3, items: [{ token: 'claim', title: '资源' }] } })
  current.emit({ event: 'progress', data: { site_detail: { site_id: 1, site_name: '站点 1', phase: 'private-provider-stage', elapsed_ms: -1, revision: 1.5 } } })
  current.emit({ event: 'progress', data: { phase: 'preparing_media', event_sequence: 3 } })
  const rootProgress = events.at(-1)
  assert.equal(rootProgress?.type === 'progress' && rootProgress.progress.phase, 'preparing_media')
  assert.equal(rootProgress?.type === 'progress' && rootProgress.progress.eventSequence, 3)
  const beforeMalformed = events.length
  for (const invalid of [{ complete: 'false' }, { revision: -1 }, { queries_completed: 1.5 }, { partial: 'true' }])
    current.emit({ event: 'site', data: { site_id: 1, site_name: '站点 1', status: 'success', page: 1, items: [], ...invalid } })
  assert.equal(events.length, beforeMalformed, 'Malformed v2 fields cannot be reinterpreted as a legacy complete-empty snapshot')
  await handle.cancel()
  await handle.done
  const count = events.length
  current.emit({ event: 'site', data: { site_id: 1, site_name: 'late', items: [] } })
  assert.equal(events.length, count, 'Cancelled source streams suppress late native events')
}
assert.equal(jsonCalls, 0)
assert.equal(opened.length, 3)
const detailed = events[0]
assert.equal(detailed.type === 'progress' && detailed.progress.siteDetail?.queriesTotal, 3)
const snapshot = events[1]
assert.equal(snapshot.type === 'site' && snapshot.group.complete, false)
const unknownPhase = events[2]
assert.equal(unknownPhase.type === 'progress' && unknownPhase.progress.siteDetail?.phase, 'unknown')
assert.equal(unknownPhase.type === 'progress' && unknownPhase.progress.siteDetail?.elapsedMs, undefined)
assert.equal(unknownPhase.type === 'progress' && unknownPhase.progress.siteDetail?.revision, undefined)
const live = await streamServerResources(source, { mediaType: 'movie', tmdbId: 346, siteIds: [1] }, event => events.push(event))
const liveRequest = opened.at(-1)!
source.destroy()
assert.equal(cancelled.includes(liveRequest.requestId), true)
const countAfterDestroy = events.length
liveRequest.emit({ event: 'done', data: {} })
assert.equal(events.length, countAfterDestroy)
await live.done

// A source destroyed while credentials are loading never opens or resurrects a stream.
const credential = deferred<{ accessToken: string } | null>()
let credentialReads = 0
const pendingSource = new ServerDataSource({ bridge, readCredential: async () => ++credentialReads === 1 ? null : credential.promise })
await pendingSource.init(sourceConfig)
const pendingStream = streamServerResources(pendingSource, { mediaType: 'movie', tmdbId: 346, siteIds: [1] }, () => {})
const beforePending = opened.length
pendingSource.destroy()
credential.resolve({ accessToken: token })
await assert.rejects(pendingStream, /已取消/)
assert.equal(opened.length, beforePending)
assert.equal(pendingSource.isConnected, false)

// Deferred handles and old events cannot win over cancellation/new runs.
function fakeHandle(id: string) {
  const done = deferred<void>()
  let cancels = 0
  return { handle: { requestId: id, done: done.promise, cancel: async () => { cancels++; done.resolve() } } satisfies ServerDiscoveryStreamHandle, done, get cancels() { return cancels } }
}
const controller = new ResourceSearchController()
const oldHandle = fakeHandle('old')
const newHandle = fakeHandle('new')
const opening = deferred<ServerDiscoveryStreamHandle>()
let oldEmit: ((event: ServerResourceStreamEvent) => void) | undefined
let oldCompleted = 0
let newCompleted = 0
const observed: string[] = []
const oldRun = controller.run(async (emit) => { oldEmit = emit; return opening.promise }, { start() {}, event: () => observed.push('old'), complete: () => { oldCompleted++ }, error: () => observed.push('old-error') })
await Promise.resolve()
await controller.cancel()
opening.resolve(oldHandle.handle)
await oldRun
assert.equal(oldHandle.cancels, 1)
assert.equal(oldCompleted, 0)
oldEmit?.({ type: 'site', group: group() })
assert.deepEqual(observed, [])

const delayedOld = deferred<ServerDiscoveryStreamHandle>()
const oldAgain = fakeHandle('old-again')
const first = controller.run(async () => delayedOld.promise, { start() {}, event() {}, complete: () => observed.push('wrong-complete'), error: () => observed.push('wrong-error') })
await Promise.resolve()
let newEmit: ((event: ServerResourceStreamEvent) => void) | undefined
const second = controller.run(async (emit) => { newEmit = emit; return newHandle.handle }, { start() {}, event: () => observed.push('new'), complete: () => { newCompleted++ }, error: () => observed.push('new-error') })
await Promise.resolve()
await Promise.resolve()
delayedOld.resolve(oldAgain.handle)
await first
assert.equal(oldAgain.cancels, 1)
assert.equal(newHandle.cancels, 0, 'An obsolete handle cannot overwrite the new active handle')
newEmit?.({ type: 'done', progress: progress({ completed: 1, succeeded: 1, running: 0 }) })
await second
assert.equal(newCompleted, 1)
assert.deepEqual(observed, ['new'])
assert.equal(newHandle.cancels, 1)

const truncated = fakeHandle('truncated')
let eofError = ''
const truncatedRun = controller.run(async () => truncated.handle, { start() {}, event() {}, complete: () => assert.fail('EOF without done is not success'), error: reason => { eofError = String(reason) } })
await Promise.resolve()
truncated.done.resolve()
await truncatedRun
assert.match(eofError, /未收到完成确认/)

console.log('Server acquisition search: v2 DTOs, cumulative/legacy snapshots, partial retention, paging, native request ownership and cancellation passed.')
