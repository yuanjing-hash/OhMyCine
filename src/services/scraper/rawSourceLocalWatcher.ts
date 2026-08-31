import type { RawSourceIndexTarget } from './rawSourceIndexScheduler'
import type { DataSource, DataSourceConfig } from '@/services/datasource/types'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'
import { readLocalRootPath } from '@/services/datasource/local'
import { createRawSourceIndexTarget } from './rawSourceIndexScheduler'
import { readRawSourceScanScheduleConfig } from './rawSourceScanSchedule'

interface LocalFileWatchEvent {
  readonly sourceId: string
  readonly rootPath: string
  readonly changedPath?: string
  readonly kind: string
}

interface ActiveLocalWatcher {
  readonly sourceId: string
  readonly localRootPath: string
  readonly target: RawSourceIndexTarget
}

export interface RawSourceLocalWatcherController {
  sync: (configs: readonly DataSourceConfig[]) => Promise<void>
  dispose: () => Promise<void>
}

export function createRawSourceLocalWatcherController(options: {
  readonly resolveSource: (sourceId: string) => Pick<DataSource, 'list'> | null
  readonly markDirty: (target: RawSourceIndexTarget) => void
}): RawSourceLocalWatcherController {
  const activeWatchers = new Map<string, ActiveLocalWatcher>()
  let unlistenPromise: Promise<() => void> | null = null

  async function ensureEventListener(): Promise<void> {
    if (unlistenPromise || !isTauriRuntime())
      return

    unlistenPromise = listen<LocalFileWatchEvent>('local-file:changed', (event) => {
      const watcher = activeWatchers.get(event.payload.sourceId)
      if (!watcher)
        return
      options.markDirty(watcher.target)
    })
  }

  async function sync(configs: readonly DataSourceConfig[]): Promise<void> {
    if (!isTauriRuntime())
      return

    await ensureEventListener()
    const nextWatchers = new Map<string, ActiveLocalWatcher>()

    for (const config of configs) {
      const watcher = createLocalWatcher(config)
      if (watcher)
        nextWatchers.set(watcher.sourceId, watcher)
    }

    for (const [sourceId, active] of activeWatchers) {
      const next = nextWatchers.get(sourceId)
      if (!next || next.localRootPath !== active.localRootPath) {
        await invoke('local_file_watch_stop', { sourceId }).catch(() => undefined)
        activeWatchers.delete(sourceId)
      }
    }

    for (const [sourceId, next] of nextWatchers) {
      const active = activeWatchers.get(sourceId)
      if (active?.localRootPath === next.localRootPath)
        continue

      await invoke('local_file_watch_start', {
        sourceId,
        rootPath: next.localRootPath,
      }).then(
        () => activeWatchers.set(sourceId, next),
        () => undefined,
      )
    }
  }

  function createLocalWatcher(config: DataSourceConfig): ActiveLocalWatcher | null {
    if (config.enabled === false || config.type !== 'local')
      return null
    if (!readRawSourceScanScheduleConfig(config).incremental.enabled)
      return null

    const localRootPath = readLocalRootPath(config)
    if (!localRootPath)
      return null

    const source = options.resolveSource(config.id)
    if (!source)
      return null

    const target = createRawSourceIndexTarget(config, source)
    return target
      ? {
          sourceId: config.id,
          localRootPath,
          target,
        }
      : null
  }

  async function dispose(): Promise<void> {
    const sourceIds = [...activeWatchers.keys()]
    activeWatchers.clear()
    await Promise.all(sourceIds.map(sourceId => invoke('local_file_watch_stop', { sourceId }).catch(() => undefined)))
    const unlisten = await unlistenPromise?.catch(() => null)
    unlisten?.()
    unlistenPromise = null
  }

  return { sync, dispose }
}

function isTauriRuntime(): boolean {
  const root = globalThis as {
    readonly __TAURI_INTERNALS__?: unknown
    readonly window?: { readonly __TAURI_INTERNALS__?: unknown }
  }
  return root.__TAURI_INTERNALS__ != null || root.window?.__TAURI_INTERNALS__ != null
}
