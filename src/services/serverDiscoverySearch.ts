import type { ServerDiscoveryStreamHandle } from './datasource/server'
import type { ServerResourceGroup, ServerResourceItem, ServerResourceStreamEvent, ServerSearchPhase, ServerSearchProgress, ServerSearchSite, ServerSearchSiteDetail } from './serverDiscovery'

export type ResourceSearchOutcome = 'idle' | 'running' | 'complete' | 'partial' | 'cancelled' | 'failed'
export interface ResourceSearchActivity {
  siteId: number
  siteName: string
  requestedPage: number
  pending: boolean
  previousResults: boolean
  receivedSnapshot: boolean
  detail?: ServerSearchSiteDetail
  detailReceivedAt: number
  startedAt: number
  endedAt?: number
  groupRevision?: number
}
export interface ResourceSearchState {
  groups: ServerResourceGroup[]
  activities: ResourceSearchActivity[]
  targetSiteIds: number[]
  progress: ServerSearchProgress
  outcome: ResourceSearchOutcome
  error: string
  startedAt: number
  endedAt?: number
}

export function emptyResourceSearch(): ResourceSearchState {
  return { groups: [], activities: [], targetSiteIds: [], progress: emptyProgress(0), outcome: 'idle', error: '', startedAt: 0 }
}

export function beginResourceSearch(previous: ResourceSearchState, input: { sites: ServerSearchSite[], siteIds: number[], replaceAll: boolean, page: number }, now = Date.now()): ResourceSearchState {
  const siteIds = [...new Set(input.siteIds)]
  const groups = input.replaceAll ? [] : previous.groups
  const retained = input.replaceAll ? [] : previous.activities.filter(activity => !siteIds.includes(activity.siteId))
  const activities = siteIds.map((siteId): ResourceSearchActivity => ({
    siteId,
    siteName: input.sites.find(site => site.id === siteId)?.name ?? '站点',
    requestedPage: input.page,
    pending: true,
    previousResults: groups.some(group => group.siteId === siteId),
    receivedSnapshot: false,
    detailReceivedAt: now,
    startedAt: now,
  }))
  return {
    groups,
    activities: orderBySites([...retained, ...activities], input.sites),
    targetSiteIds: siteIds,
    progress: emptyProgress(siteIds.length),
    outcome: 'running',
    error: '',
    startedAt: now,
  }
}

export function reduceResourceSearch(state: ResourceSearchState, event: ServerResourceStreamEvent, sites: ServerSearchSite[], now = Date.now()): ResourceSearchState {
  if (state.outcome !== 'running')
    return state
  if (event.type === 'error')
    return finishResourceSearch(state, 'failed', event.message, now)
  if (event.type === 'progress' || event.type === 'done') {
    if (!validProgress(event.progress, state.targetSiteIds) || (event.type === 'done' && event.progress.completed !== state.targetSiteIds.length))
      return event.type === 'done' ? finishResourceSearch(state, 'failed', 'Server 返回的搜索进度无效，已保留收到的结果。', now) : state
    const currentSequence = state.progress.eventSequence
    const incomingSequence = event.progress.eventSequence
    const currentOrNewer = newerRevision(currentSequence, incomingSequence) || (event.type === 'done' && currentSequence === incomingSequence)
    if (!currentOrNewer)
      return state
    const detail = event.progress.siteDetail
    let activities = state.activities
    if (detail && state.targetSiteIds.includes(detail.siteId)) {
      activities = activities.map((activity) => {
        if (activity.siteId !== detail.siteId || !activity.pending || !newerRevision(activity.detail?.revision, detail.revision))
          return activity
        return { ...activity, detail, detailReceivedAt: now }
      })
    }
    const incoming = event.progress.total === 0 && event.progress.phase === 'preparing_media'
      ? { ...event.progress, total: state.progress.total, pending: state.progress.pending }
      : event.progress
    const next = { ...state, activities, progress: incoming.completed >= state.progress.completed ? incoming : state.progress }
    return event.type === 'done' ? finishResourceSearch(next, 'complete', '', now) : next
  }

  const incoming = event.group
  const activity = state.activities.find(item => item.siteId === incoming.siteId)
  if (!activity || !state.targetSiteIds.includes(incoming.siteId) || !activity.pending || incoming.page !== activity.requestedPage || !newerRevision(activity.groupRevision, incoming.revision))
    return state
  const current = state.groups.find(group => group.siteId === incoming.siteId)
  const terminal = incoming.complete !== false
  const incomingItems = reuseResourceItems(current?.items ?? [], incoming.items)
  const keepPrevious = activity.previousResults && incomingItems.length === 0 && (incoming.status !== 'success' || incoming.partial === true || !terminal)
  // A broken/late terminal response must not turn an already published batch into an empty list.
  const preserveArrived = !activity.previousResults && current?.items.length && incomingItems.length === 0 && (incoming.status !== 'success' || incoming.partial === true)
  const group: ServerResourceGroup = preserveArrived
    ? { ...incoming, items: current.items, partial: true, incompleteReason: incoming.incompleteReason ?? incoming.errorCode ?? 'stream_interrupted' }
    : { ...incoming, items: incomingItems }
  const groups = keepPrevious && current ? state.groups : orderBySites([...state.groups.filter(item => item.siteId !== incoming.siteId), group], sites)
  const activities = state.activities.map(item => item.siteId === incoming.siteId
    ? { ...item, siteName: incoming.siteName, pending: !terminal, previousResults: Boolean(keepPrevious), receivedSnapshot: !keepPrevious || item.receivedSnapshot, groupRevision: incoming.revision, endedAt: terminal ? now : undefined }
    : item)
  return { ...state, groups, activities }
}

export function finishResourceSearch(state: ResourceSearchState, outcome: 'complete' | 'cancelled' | 'failed', error = '', now = Date.now()): ResourceSearchState {
  if (state.outcome !== 'running')
    return state
  let incomplete = false
  const activities = state.activities.map((activity) => {
    if (!state.targetSiteIds.includes(activity.siteId))
      return activity
    const group = state.groups.find(item => item.siteId === activity.siteId)
    if (activity.pending || activity.previousResults || group?.partial || group?.status !== 'success')
      incomplete = true
    return { ...activity, pending: false, endedAt: activity.endedAt ?? now }
  })
  const groups = state.groups.map((group) => {
    const activity = state.activities.find(item => item.siteId === group.siteId)
    if (!activity?.pending || activity.previousResults)
      return group
    return { ...group, complete: true, partial: true, incompleteReason: group.incompleteReason ?? (outcome === 'cancelled' ? 'cancelled' : 'stream_interrupted') }
  })
  const hasResults = groups.some(group => group.items.length > 0)
  const allFailed = state.targetSiteIds.every(siteId => !activities.find(item => item.siteId === siteId)?.receivedSnapshot || groups.find(group => group.siteId === siteId)?.status !== 'success')
  return {
    ...state,
    groups,
    activities,
    outcome: outcome === 'cancelled' ? 'cancelled' : outcome === 'failed' || allFailed ? hasResults ? 'partial' : 'failed' : incomplete ? 'partial' : 'complete',
    error,
    endedAt: now,
  }
}

export function resourceGroupIsPrevious(state: ResourceSearchState, siteId: number): boolean {
  return state.activities.find(activity => activity.siteId === siteId)?.previousResults === true
}

export function resourceGroupPageIsChanging(state: ResourceSearchState, group: ServerResourceGroup): boolean {
  const activity = state.activities.find(item => item.siteId === group.siteId)
  return activity?.pending === true && activity.requestedPage !== group.page
}

export function resourceGroupRetryPage(state: ResourceSearchState, siteId: number): number {
  return state.activities.find(activity => activity.siteId === siteId)?.requestedPage
    ?? state.groups.find(group => group.siteId === siteId)?.page ?? 1
}

export function searchPhaseLabel(phase?: ServerSearchPhase): string {
  switch (phase) {
    case 'preparing_media': return '正在准备作品信息'
    case 'queued': return '正在等待搜索名额'
    case 'preparing_site': return '正在准备站点'
    case 'restoring_session': return '正在恢复登录会话'
    case 'checking_session': return '正在检查登录状态'
    case 'waiting_rate_limit': return '正在等待站点限速放行'
    case 'querying': return '正在请求站点，等待返回'
    case 'processing_results': return '正在处理已返回的结果'
    case 'finished': return '查询已结束'
    default: return '正在等待 Server 搜索进度'
  }
}

export function searchIncompleteLabel(reason?: string): string {
  switch (reason) {
    case 'request_timeout': return '站点请求超时'
    case 'site_budget_exceeded': return '站点搜索时间已用尽'
    case 'rate_limit_wait_exceeded': return '等待限速放行超时'
    case 'search_budget_exceeded': return '本轮搜索时间已用尽'
    case 'cancelled': return '搜索已取消'
    case 'result_limit': return '已达到本轮结果上限'
    case 'site_authentication_failed':
    case 'authentication_required':
    case 'auth_required':
    case 'resource_auth_required': return '需要重新登录站点'
    case 'rate_limited':
    case 'resource_rate_limited':
    case 'site_rate_limited': return '站点正在限速'
    case 'site_unavailable': return '站点暂时不可用'
    case 'resource_result_invalid':
    case 'site_invalid_response': return '站点返回的结果格式异常'
    case 'verification_required':
    case 'resource_verification_required':
    case 'resource_browser_verification_required':
    case 'site_verification_required': return '站点需要重新完成网页验证'
    case 'resource_browser_login_pending': return '管理员正在完成站点登录，请稍后重试'
    case 'resource_browser_restore_timeout': return '恢复登录会话超时，尚未确认登录失效'
    case 'permission_denied': return '当前账号没有此站点的搜索权限'
    case 'stream_interrupted': return '搜索连接已中断'
    default: return '部分查询未完成'
  }
}

function emptyProgress(total: number): ServerSearchProgress {
  return { total, pending: total, running: 0, completed: 0, succeeded: 0, failed: 0, resultCount: 0 }
}

function validProgress(progress: ServerSearchProgress, siteIds: number[]): boolean {
  const counts = [progress.total, progress.pending, progress.running, progress.completed, progress.succeeded, progress.failed, progress.resultCount]
  if (counts.some(value => !Number.isSafeInteger(value) || value < 0))
    return false
  const preparing = progress.phase === 'preparing_media' && counts.every(value => value === 0) && progress.siteId == null && progress.siteDetail == null
  return (preparing || progress.total === siteIds.length)
    && progress.total === progress.pending + progress.running + progress.completed
    && progress.completed === progress.succeeded + progress.failed
    && (progress.siteId == null || siteIds.includes(progress.siteId))
    && (progress.siteDetail == null || siteIds.includes(progress.siteDetail.siteId))
}

function newerRevision(current?: number, incoming?: number): boolean {
  return current == null || (incoming != null && incoming > current)
}

function orderBySites<T extends { siteId: number }>(items: T[], sites: ServerSearchSite[]): T[] {
  const order = new Map(sites.map((site, index) => [site.id, index]))
  return items.sort((left, right) => (order.get(left.siteId) ?? Number.MAX_SAFE_INTEGER) - (order.get(right.siteId) ?? Number.MAX_SAFE_INTEGER) || left.siteId - right.siteId)
}

function reuseResourceItems(current: ServerResourceItem[], incoming: ServerResourceItem[]): ServerResourceItem[] {
  const known = new Map(current.map(item => [item.token, item]))
  const seen = new Set<string>()
  return incoming.flatMap((item) => {
    if (seen.has(item.token))
      return []
    seen.add(item.token)
    const old = known.get(item.token)
    return [old && Object.keys(item).every(key => old[key as keyof ServerResourceItem] === item[key as keyof ServerResourceItem]) ? old : item]
  })
}

interface SearchObserver {
  start: () => void
  event: (event: ServerResourceStreamEvent) => void
  complete: () => void
  error: (reason: unknown) => void
}

// Owns handles even when a previous open() is still awaiting credentials or native setup.
// Hiding the workspace does not cancel this controller; explicit cancel/navigation does.
export class ResourceSearchController {
  private generation = 0
  private active: ServerDiscoveryStreamHandle | null = null

  async run(open: (onEvent: SearchObserver['event'], isCurrent: () => boolean) => Promise<ServerDiscoveryStreamHandle | undefined>, observer: SearchObserver): Promise<void> {
    const generation = ++this.generation
    const isCurrent = () => generation === this.generation
    const previous = this.active
    this.active = null
    let terminal = false
    observer.start()
    try {
      await previous?.cancel()
      if (!isCurrent())
        return
      const handle = await open((event) => {
        if (!isCurrent() || terminal)
          return
        observer.event(event)
        if (event.type === 'done' || event.type === 'error') {
          terminal = true
          void this.active?.cancel()
        }
      }, isCurrent)
      if (!handle)
        return
      // Attach a rejection handler before awaiting cancellation of an obsolete handle.
      void handle.done.catch(() => {})
      if (!isCurrent()) {
        await handle.cancel()
        return
      }
      this.active = handle
      if (terminal)
        await handle.cancel()
      await handle.done
      if (isCurrent()) {
        if (terminal)
          observer.complete()
        else
          observer.error(new Error('Server 搜索连接已结束，但未收到完成确认；已保留收到的结果。'))
      }
    }
    catch (reason) {
      if (isCurrent() && !terminal)
        observer.error(reason)
    }
    finally {
      if (isCurrent())
        this.active = null
    }
  }

  async cancel(): Promise<void> {
    this.generation++
    const handle = this.active
    this.active = null
    await handle?.cancel()
  }
}
