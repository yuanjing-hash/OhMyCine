import type { ServerPlaybackHistoryChange } from '@/services/datasource/server'
import type { DataSourceConfig } from '@/services/datasource/types'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import type { useDataSourceStore } from '@/stores/datasource'
import { invoke } from '@tauri-apps/api/core'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { redactSensitiveText } from '@/services/datasource/errors'
import { ServerDataSource } from '@/services/datasource/server'
import { listPlaybackHistoryPage, PLAYED_STATE_CHANGED_EVENT } from '@/services/playbackHistory'

type DataSourceStore = ReturnType<typeof useDataSourceStore>
const CURSOR_PREFIX = 'ohmycine:server-history-cursor:'
const DIAGNOSTIC_PREFIX = 'ohmycine:server-history-sync-diagnostic:'
const SYNC_INTERVAL_MS = 60_000

export function startPlaybackHistorySync(store: DataSourceStore): () => void {
  let timer: number | undefined
  let running = false
  let rerun = false

  const run = async () => {
    if (running) {
      rerun = true
      return
    }
    running = true
    try {
      do {
        rerun = false
        await syncPlaybackHistory(store)
      } while (rerun)
    }
    finally {
      running = false
    }
  }
  const schedule = () => {
    if (timer)
      window.clearTimeout(timer)
    timer = window.setTimeout(() => {
      timer = undefined
      void run()
    }, 2_000)
  }
  window.addEventListener(PLAYED_STATE_CHANGED_EVENT, schedule)
  timer = window.setTimeout(() => {
    timer = undefined
    void run()
  }, 3_000)
  const interval = window.setInterval(() => void run(), SYNC_INTERVAL_MS)
  return () => {
    window.removeEventListener(PLAYED_STATE_CHANGED_EVENT, schedule)
    if (timer)
      window.clearTimeout(timer)
    if (interval)
      window.clearInterval(interval)
  }
}

export async function syncPlaybackHistory(store: DataSourceStore): Promise<void> {
  store.loadConfigs()
  await store.syncManager().catch(() => undefined)
  const configs = store.orderedConfigs.filter(config => config.enabled !== false)
  const servers = configs.flatMap((config) => {
    const source = store.getSource(config.id)
    return config.type === 'server' && source instanceof ServerDataSource ? [{ config, source }] : []
  })
  if (!servers.length)
    return
  const history = await readLocalHistory(500)
  const outgoing = await Promise.all(history.flatMap((entry) => {
    const sourceConfig = configs.find(config => config.id === entry.sourceId)
    return sourceConfig ? [{ entry, sourceConfig }] : []
  }).map(({ entry, sourceConfig }) => toServerChange(entry, sourceConfig)))

  let merged = 0
  for (const target of servers) {
    const cursorKey = `${CURSOR_PREFIX}${target.config.id}:${encodeURIComponent(safeOrigin(target.config.url) ?? target.config.url)}`
    let cursor = safeCursor(getAppSetting(cursorKey))
    try {
      let received = 0
      for (let page = 0; page < 20; page++) {
        const response = await target.source.syncPlaybackHistory({ cursor, changes: page === 0 ? outgoing : [] })
        received += response.changes.length
        const incoming = response.changes.flatMap(change => mapIncomingChange(change, configs))
        if (incoming.length)
          merged += await invoke<number>('player_merge_playback_history', { entries: incoming })
        if (response.cursor < cursor)
          throw new Error('Server 播放历史游标发生回退。')
        cursor = response.cursor
        await setAppSetting(cursorKey, String(cursor))
        if (response.changes.length < 500)
          break
      }
      await saveSyncDiagnostic(target.config.id, { ok: true, cursor, outgoing: outgoing.length, received })
    }
    catch (error) {
      // Local playback history remains authoritative while a Server is offline.
      await saveSyncDiagnostic(target.config.id, { ok: false, cursor, outgoing: outgoing.length, message: redactSensitiveText(error).slice(0, 256) })
    }
  }
  if (merged > 0)
    window.dispatchEvent(new CustomEvent(PLAYED_STATE_CHANGED_EVENT, { detail: { source: 'server-history-sync' } }))
}

async function saveSyncDiagnostic(sourceId: string, value: { ok: boolean, cursor: number, outgoing: number, received?: number, message?: string }): Promise<void> {
  try {
    await setAppSetting(`${DIAGNOSTIC_PREFIX}${sourceId}`, JSON.stringify({ timestamp: new Date().toISOString(), ...value }))
  }
  catch {
    // Diagnostics must never make playback history synchronization fail.
  }
}

async function readLocalHistory(limit: number): Promise<PlaybackHistoryEntry[]> {
  const result: PlaybackHistoryEntry[] = []
  for (let page = 1; result.length < limit; page++) {
    const current = await listPlaybackHistoryPage(page, Math.min(100, limit - result.length))
    result.push(...current.list)
    if (!current.hasMore)
      break
  }
  return result
}

async function toServerChange(entry: PlaybackHistoryEntry, config: DataSourceConfig): Promise<ServerPlaybackHistoryChange> {
  const locator = safeOrigin(config.url) ?? ''
  const stableSource = locator || config.id
  return {
    sync_key: await sha256(`${config.type}\0${stableSource}\0${entry.mediaIdentity}`),
    source_kind: config.type,
    source_locator: locator || undefined,
    source_id: config.id,
    library_id: entry.libraryId ?? undefined,
    item_id: entry.itemId ?? undefined,
    media_identity: entry.mediaIdentity,
    title: entry.title,
    stream_identity: entry.streamIdentity ?? undefined,
    media_type: entry.mediaType ?? undefined,
    poster_url: entry.posterUrl ?? undefined,
    backdrop_url: entry.backdropUrl ?? undefined,
    title_logo_url: entry.titleLogoUrl ?? undefined,
    position: entry.position,
    duration: entry.duration ?? undefined,
    completed: entry.completed,
    updated_at: normalizeTimestamp(entry.updatedAt),
  }
}

function mapIncomingChange(change: ServerPlaybackHistoryChange, configs: readonly DataSourceConfig[]) {
  const locator = change.source_locator ? safeOrigin(change.source_locator) : undefined
  const config = configs.find((candidate) => {
    if (candidate.type !== change.source_kind || candidate.enabled === false)
      return false
    return locator ? safeOrigin(candidate.url) === locator : candidate.id === change.source_id
  })
  if (!config)
    return []
  return [{
    sourceId: config.id,
    libraryId: change.library_id,
    itemId: change.item_id,
    mediaIdentity: change.media_identity,
    title: change.title,
    streamIdentity: change.stream_identity,
    mediaType: change.media_type,
    posterUrl: change.poster_url,
    backdropUrl: change.backdrop_url,
    titleLogoUrl: change.title_logo_url,
    position: change.position,
    duration: change.duration,
    completed: change.completed,
    deleted: change.deleted === true,
    updatedAt: normalizeTimestamp(change.updated_at),
  }]
}

function safeOrigin(value: string): string | undefined {
  try {
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.origin : undefined
  }
  catch {
    return undefined
  }
}

function safeCursor(value: string | null): number {
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : 0
}

function normalizeTimestamp(value: number): number {
  if (!Number.isSafeInteger(value) || value <= 0)
    return Date.now()
  return value < 10_000_000_000 ? value * 1_000 : value
}

async function sha256(value: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value))
  return [...new Uint8Array(digest)].map(byte => byte.toString(16).padStart(2, '0')).join('')
}
