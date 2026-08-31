import type { MediaActionId, MediaActionTarget } from './types'

export interface MaintenanceHandler {
  canHandle?: (target: MediaActionTarget, action: MediaActionId) => boolean | Promise<boolean>
  execute: (target: MediaActionTarget, action: MediaActionId) => void | Promise<void>
}

const handlers = new Map<string, MaintenanceHandler>()
const waiters = new Map<string, Array<(handler: MaintenanceHandler) => void>>()

export function registerMaintenanceHandler(sourceId: string, handler: MaintenanceHandler): () => void {
  handlers.set(sourceId, handler)
  for (const resolve of waiters.get(sourceId) ?? [])
    resolve(handler)
  waiters.delete(sourceId)
  return () => {
    if (handlers.get(sourceId) === handler)
      handlers.delete(sourceId)
  }
}

export function getMaintenanceHandler(sourceId: string): MaintenanceHandler | null {
  return handlers.get(sourceId) ?? null
}

export function waitForMaintenanceHandler(sourceId: string, timeoutMs = 5000): Promise<MaintenanceHandler> {
  const current = getMaintenanceHandler(sourceId)
  if (current)
    return Promise.resolve(current)
  return new Promise((resolve, reject) => {
    const timeout = window.setTimeout(() => reject(new Error('媒体来源页未能及时打开，请重试。')), timeoutMs)
    const finish = (handler: MaintenanceHandler) => {
      window.clearTimeout(timeout)
      resolve(handler)
    }
    waiters.set(sourceId, [...(waiters.get(sourceId) ?? []), finish])
  })
}
