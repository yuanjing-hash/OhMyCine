import type { ServerDataSource } from '@/services/datasource/server'

export type DiscoveryRequestLane = 'title' | 'content' | 'coverage'
interface NativeLane {
  pending: Set<object>
  listeners: Set<() => void>
}
// A physical invoke outlives its Vue owner and even a replaced DataSource.
// Source ids are local connection identities, never credentials. Only settled
// invokes release slots; UI deadlines/cancellation do not reset this registry.
const sources = new Map<string, Map<DiscoveryRequestLane, NativeLane>>()
const anonymous = new WeakMap<ServerDataSource, Map<DiscoveryRequestLane, NativeLane>>()
export function discoveryRequestLane(source: ServerDataSource, lane: DiscoveryRequestLane): NativeLane {
  let lanes = source.id ? sources.get(source.id) : anonymous.get(source)
  if (!lanes) {
    lanes = new Map()
    if (source.id)
      sources.set(source.id, lanes)
    else
      anonymous.set(source, lanes)
  }
  let requests = lanes.get(lane)
  if (!requests) {
    requests = { pending: new Set(), listeners: new Set() }
    lanes.set(lane, requests)
  }
  return requests
}
export async function runDiscoveryRequest<T>(source: ServerDataSource, lane: DiscoveryRequestLane, work: () => Promise<T>, current: () => boolean = () => true): Promise<T> {
  const requests = discoveryRequestLane(source, lane)
  while (requests.pending.size >= 2) {
    await new Promise<void>((resolve) => {
      const ready = () => {
        requests.listeners.delete(ready)
        resolve()
      }
      requests.listeners.add(ready)
    })
  }
  if (!current())
    throw new Error('Server 请求所属页面已变化。')
  const lease = {}
  requests.pending.add(lease)
  try {
    return await work()
  }
  finally {
    requests.pending.delete(lease)
    for (const listener of [...requests.listeners])
      listener()
  }
}
