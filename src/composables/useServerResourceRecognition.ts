import type { Ref } from 'vue'
import type { ServerDataSource } from '@/services/datasource/server'
import type { ServerCoverageSummary, ServerResourceItem } from '@/services/serverDiscovery'
import type { DiscoveryRequestLane } from '@/services/serverDiscoveryRequests'
import type { ServerContentPreview } from '@/services/serverResourceContent'
import type { ServerResourceRecognition } from '@/services/serverResourceRecognition'
import type { ShareContentAnalysis } from '@/services/serverShareContent'
import { onScopeDispose, ref, watch } from 'vue'
import { getServerCoverage } from '@/services/serverDiscovery'
import { discoveryRequestLane } from '@/services/serverDiscoveryRequests'
import { previewServerResourceContent } from '@/services/serverResourceContent'
import { isServerCloudResource, recognizeServerResource } from '@/services/serverResourceRecognition'
import { parseShareContentAnalysis } from '@/services/serverShareContent'

interface Attempt {
  generation: number
  source: ServerDataSource
  revision: number
  cancel: () => void
}
// Independent queues share only source identity and physical leases. Retiring a
// scope cannot release an invoke; only its settled bridge promise can do that.
export function useServerResourceRecognition(items: Readonly<Ref<ServerResourceItem[]>>, source: Readonly<Ref<ServerDataSource | null>>, owner: Readonly<Ref<string>>, enabled: Readonly<Ref<boolean>>) {
  let generation = 0
  let disposed = false
  function lane<T>(name: DiscoveryRequestLane, eligible: (item: ServerResourceItem) => boolean, load: (source: ServerDataSource, item: ServerResourceItem) => Promise<T>, timeoutMs: number, timeoutMessage: (item: ServerResourceItem) => string) {
    const values = ref<Record<string, T>>({}) as Ref<Record<string, T>>
    const errors = ref<Record<string, string>>({})
    const loading = ref<string[]>([])
    const active = new Map<string, Attempt>()
    const completed = new Set<string>()
    const revisions = new Map<string, number>()
    let listening: ReturnType<typeof discoveryRequestLane> | undefined
    function current(token: string, attempt: Attempt) {
      return !disposed && enabled.value && generation === attempt.generation && source.value === attempt.source && active.get(token) === attempt && revisions.get(token) === attempt.revision && items.value.some(item => item.token === token)
    }
    function invalidate(token: string) {
      const attempt = active.get(token)
      active.delete(token)
      attempt?.cancel()
      loading.value = [...active.keys()]
    }
    function pump() {
      if (disposed || !enabled.value || !source.value)
        return
      const requests = discoveryRequestLane(source.value, name)
      for (const item of items.value) {
        if (active.size >= 2 || requests.pending.size >= 2)
          break
        if (!eligible(item) || completed.has(item.token) || active.has(item.token))
          continue
        const token = item.token
        completed.add(token)
        if (item.expiresAt && !(Date.parse(item.expiresAt) > Date.now())) {
          errors.value[token] = '搜索结果已过期，请重新搜索。'
          continue
        }
        const revision = revisions.get(token) ?? 0
        revisions.set(token, revision)
        let cancel!: () => void
        const cancelled = new Promise<void>((resolve) => {
          cancel = resolve
        })
        const attempt: Attempt = { generation, source: source.value, revision, cancel }
        active.set(token, attempt)
        requests.pending.add(attempt)
        loading.value = [...active.keys()]
        let timeout: ReturnType<typeof setTimeout>
        const deadline = new Promise<void>((resolve) => {
          timeout = setTimeout(() => {
            if (current(token, attempt))
              errors.value[token] = timeoutMessage(item)
            resolve()
          }, timeoutMs)
        })
        const work = load(attempt.source, item).then((result) => {
          if (current(token, attempt))
            values.value[token] = result
        }).catch((reason) => {
          if (current(token, attempt))
            errors.value[token] = reason instanceof Error ? reason.message : 'Server 检查失败，请重新检测。'
        }).finally(() => {
          requests.pending.delete(attempt)
          for (const listener of [...requests.listeners])
            listener()
        })
        void Promise.race([work, cancelled, deadline]).finally(() => {
          clearTimeout(timeout)
          if (active.get(token) === attempt) {
            active.delete(token)
            loading.value = [...active.keys()]
            pump()
          }
        })
      }
    }
    function reset() {
      for (const token of [...active.keys()])
        invalidate(token)
      completed.clear()
      revisions.clear()
      values.value = {}
      errors.value = {}
    }
    function retry(token: string) {
      if (!items.value.some(item => item.token === token))
        return
      revisions.set(token, (revisions.get(token) ?? 0) + 1)
      invalidate(token)
      delete errors.value[token]
      delete values.value[token]
      completed.delete(token)
      pump()
    }
    function accept(token: string, value: T) {
      if (disposed || !enabled.value || !items.value.some(item => item.token === token))
        return
      revisions.set(token, (revisions.get(token) ?? 0) + 1)
      invalidate(token)
      completed.add(token)
      delete errors.value[token]
      values.value[token] = value
      pump()
    }
    function subscribe() {
      listening?.listeners.delete(pump)
      listening = source.value ? discoveryRequestLane(source.value, name) : undefined
      listening?.listeners.add(pump)
    }
    function trim() {
      const tokens = new Set(items.value.map(item => item.token))
      for (const token of [...active.keys()]) {
        if (!tokens.has(token))
          invalidate(token)
      }
      for (const map of [values, errors]) {
        for (const token of Object.keys(map.value)) {
          if (!tokens.has(token))
            delete map.value[token]
        }
      }
      for (const token of completed) {
        if (!tokens.has(token))
          completed.delete(token)
      }
      for (const token of revisions.keys()) {
        if (!tokens.has(token))
          revisions.delete(token)
      }
      pump()
    }
    function dispose() {
      listening?.listeners.delete(pump)
      reset()
    }
    return { values, errors, loading, pump, subscribe, trim, reset, retry, accept, dispose }
  }
  const title = lane<ServerResourceRecognition>('title', () => true, (current, item) => recognizeServerResource(current, item.token, item.contentInspection === true), 65000, item => isServerCloudResource(item) && !item.contentInspection ? '分享文件读取超时，尚未确认实际集数；可重新检测。' : '标题识别超时，可重新检测。')
  const content = lane<ServerContentPreview>('content', item => item.contentInspection === true, (current, item) => previewServerResourceContent(current, { result_token: item.token }), 75000, () => '实际文件读取超时，尚未确认包内集数；可独立重新读取。')
  const coverage = lane<ServerCoverageSummary>('coverage', item => !!title.values.value[item.token]?.tmdbId && !!title.values.value[item.token]?.mediaType, (current, item) => {
    const facts = title.values.value[item.token]!
    return getServerCoverage(current, facts.mediaType!, facts.tmdbId!)
  }, 25000, () => '作品库内覆盖检查超时，不影响文件读取。')
  const lanes = [title, content, coverage]
  function reset() {
    generation++
    for (const lane of lanes)
      lane.reset()
  }
  const stopOwner = watch([source, owner, enabled], () => {
    reset()
    for (const lane of lanes) {
      lane.subscribe()
      lane.pump()
    }
  }, { immediate: true, flush: 'sync' })
  const stopItems = watch(items, () => {
    for (const lane of lanes)
      lane.trim()
  }, { flush: 'sync' })
  const stopTitle = watch(title.values, () => coverage.pump(), { deep: true, flush: 'sync' })
  function retry(token: string) {
    title.retry(token)
    coverage.retry(token)
  }
  function acceptAnalysis(token: string, value: ShareContentAnalysis) {
    const item = items.value.find(item => item.token === token)
    const analysis = parseShareContentAnalysis(value)
    if (!item || !isServerCloudResource(item) || !analysis)
      return
    title.accept(token, { ...(title.values.value[token] ?? { status: 'unrecognized', title: item.title }), shareAnalysis: analysis })
  }
  function dispose() {
    disposed = true
    stopOwner()
    stopItems()
    stopTitle()
    generation++
    for (const lane of lanes)
      lane.dispose()
  }
  onScopeDispose(dispose)
  return { recognitions: title.values, errors: title.errors, loadingTokens: title.loading, contentPreviews: content.values, contentErrors: content.errors, inspectingTokens: content.loading, coverages: coverage.values, coverageErrors: coverage.errors, retry, retryContent: content.retry, acceptContent: content.accept, acceptAnalysis, reset, dispose }
}
