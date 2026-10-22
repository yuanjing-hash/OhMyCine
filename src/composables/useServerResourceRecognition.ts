import type { Ref } from 'vue'
import type { ServerDataSource } from '@/services/datasource/server'
import type { ServerResourceItem } from '@/services/serverDiscovery'
import type { ServerResourceRecognition } from '@/services/serverResourceRecognition'
import type { ShareContentAnalysis } from '@/services/serverShareContent'
import { onScopeDispose, ref, watch } from 'vue'
import { isServerCloudResource, recognizeServerResource } from '@/services/serverResourceRecognition'
import { parseShareContentAnalysis } from '@/services/serverShareContent'

interface Attempt { generation: number, source: ServerDataSource, revision: number, cancel: () => void }
interface NativeRequests { pending: Set<Attempt>, listeners: Set<() => void> }

// Retiring Vue state does not cancel an invoke already running in Rust. Keep
// its lease until that actual bridge request settles, across remounted owners
// of the same Server source. The native method retains its own hard timeout.
const nativeRequests = new WeakMap<ServerDataSource, NativeRequests>()
function requestsFor(source: ServerDataSource): NativeRequests {
  let requests = nativeRequests.get(source)
  if (!requests) {
    requests = { pending: new Set(), listeners: new Set() }
    nativeRequests.set(source, requests)
  }
  return requests
}

// One visible result page owns at most two recognition requests. All facts stay
// in this scope; a new query, Server instance or closed workspace retires it.
export function useServerResourceRecognition(items: Readonly<Ref<ServerResourceItem[]>>, source: Readonly<Ref<ServerDataSource | null>>, owner: Readonly<Ref<string>>, enabled: Readonly<Ref<boolean>>) {
  const recognitions = ref<Record<string, ServerResourceRecognition>>({})
  const errors = ref<Record<string, string>>({})
  const loadingTokens = ref<string[]>([])
  const active = new Map<string, Attempt>()
  let completed = new Set<string>()
  let generation = 0
  let disposed = false
  const revisions = new Map<string, number>()
  let listening: NativeRequests | undefined

  function current(token: string, attempt: Attempt) {
    return !disposed && enabled.value && generation === attempt.generation && source.value === attempt.source && active.get(token) === attempt
      && revisions.get(token) === attempt.revision && items.value.some(item => item.token === token)
  }
  function invalidate(token: string) {
    const attempt = active.get(token)
    active.delete(token)
    attempt?.cancel()
    loadingTokens.value = [...active.keys()]
  }
  function pump() {
    if (disposed || !enabled.value || !source.value)
      return
    const requests = requestsFor(source.value)
    for (const item of items.value) {
      if (active.size >= 2 || requests.pending.size >= 2)
        break
      if (completed.has(item.token) || active.has(item.token))
        continue
      completed.add(item.token)
      if (item.expiresAt && !(Date.parse(item.expiresAt) > Date.now())) {
        errors.value[item.token] = '搜索结果已过期，请重新搜索。'
        continue
      }
      const token = item.token
      const revision = revisions.get(token) ?? 0
      revisions.set(token, revision)
      let cancel!: () => void
      const cancelled = new Promise<void>((resolve) => {
        cancel = resolve
      })
      const attempt: Attempt = { generation, source: source.value, revision, cancel }
      active.set(token, attempt)
      requests.pending.add(attempt)
      loadingTokens.value = [...active.keys()]
      let timeout: ReturnType<typeof setTimeout>
      const deadline = new Promise<void>((resolve) => {
        timeout = setTimeout(() => {
          if (current(token, attempt))
            errors.value[token] = isServerCloudResource(item) ? '分享文件读取超时，尚未确认实际集数；可重新检测。' : 'BT 标题识别超时，可重新检测。'
          resolve()
        }, 65000)
      })
      const work = recognizeServerResource(attempt.source, token).then((result) => {
        if (current(token, attempt))
          recognitions.value[token] = result
      }).catch((reason) => {
        if (current(token, attempt))
          errors.value[token] = reason instanceof Error ? reason.message : 'Server 资源分析失败，请重新检测。'
      }).finally(() => {
        requests.pending.delete(attempt)
        for (const listener of requests.listeners)
          listener()
      })
      void Promise.race([work, cancelled, deadline]).finally(() => {
        clearTimeout(timeout)
        if (active.get(token) === attempt) {
          active.delete(token)
          loadingTokens.value = [...active.keys()]
          pump()
        }
      })
    }
  }
  function reset() {
    generation++
    for (const token of [...active.keys()]) invalidate(token)
    completed = new Set()
    revisions.clear()
    recognitions.value = {}
    errors.value = {}
  }
  function retry(token: string) {
    if (!items.value.some(item => item.token === token))
      return
    revisions.set(token, (revisions.get(token) ?? 0) + 1)
    invalidate(token)
    delete errors.value[token]
    delete recognitions.value[token]
    completed.delete(token)
    pump()
  }
  function acceptAnalysis(token: string, value: ShareContentAnalysis) {
    const item = items.value.find(item => item.token === token)
    const analysis = parseShareContentAnalysis(value)
    if (disposed || !enabled.value || !item || !isServerCloudResource(item) || !analysis)
      return
    revisions.set(token, (revisions.get(token) ?? 0) + 1)
    invalidate(token)
    completed.add(token)
    delete errors.value[token]
    recognitions.value[token] = { ...(recognitions.value[token] ?? { status: 'unrecognized', title: item.title }), shareAnalysis: analysis }
    pump()
  }
  const stopOwner = watch([source, owner, enabled], () => {
    listening?.listeners.delete(pump)
    listening = source.value ? requestsFor(source.value) : undefined
    listening?.listeners.add(pump)
    reset()
    pump()
  }, { immediate: true, flush: 'sync' })
  const stopItems = watch(items, () => {
    const tokens = new Set(items.value.map(item => item.token))
    for (const token of [...active.keys()]) {
      if (!tokens.has(token))
        invalidate(token)
    }
    for (const map of [recognitions, errors]) {
      for (const token of Object.keys(map.value)) {
        if (!tokens.has(token))
          delete map.value[token]
      }
    }
    completed = new Set([...completed].filter(token => tokens.has(token)))
    for (const token of revisions.keys()) {
      if (!tokens.has(token))
        revisions.delete(token)
    }
    pump()
  }, { flush: 'sync' })
  function dispose() {
    disposed = true
    stopOwner()
    stopItems()
    listening?.listeners.delete(pump)
    listening = undefined
    reset()
  }
  onScopeDispose(dispose)
  return { recognitions, errors, loadingTokens, retry, acceptAnalysis, reset, dispose }
}
