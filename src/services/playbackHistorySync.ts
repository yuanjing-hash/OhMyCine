import type { ServerPlaybackHistoryChange, ServerPlaybackHistorySyncResponse } from '@/services/datasource/server'
import type { DataSourceConfig, DataSourceType } from '@/services/datasource/types'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import type { useDataSourceStore } from '@/stores/datasource'
import { invoke } from '@tauri-apps/api/core'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { redactSensitiveText } from '@/services/datasource/errors'
import { mapServerHistoryItem, ServerDataSource, ServerRequestError } from '@/services/datasource/server'
import { listPlaybackHistorySyncPage, PLAYED_STATE_CHANGED_EVENT } from '@/services/playbackHistory'

type DataSourceStore = ReturnType<typeof useDataSourceStore>
interface HistorySourceDescriptor {
  id: string
  type: DataSourceType | 'local-file'
  name: string
  url: string
}
const CURSOR_PREFIX = 'ohmycine:server-history-cursor:'
const DIAGNOSTIC_PREFIX = 'ohmycine:server-history-sync-diagnostic:'
const SYNC_INTERVAL_MS = 60_000
const HISTORY_RECORD_REJECTION_CODES = new Set(['INVALID_REQUEST', 'NOT_FOUND', 'PERMISSION_DENIED', 'history_clock_ahead'])
export interface HistorySyncFocus { sourceId: string, mediaIdentity: string, updatedAt: number }
export interface HistorySyncOutcome { status: 'confirmed' | 'pending' | 'rejected', reason?: string }

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

export async function syncPlaybackHistory(store: DataSourceStore, focus?: HistorySyncFocus): Promise<HistorySyncOutcome> {
  store.loadConfigs()
  await store.syncManager().catch(() => undefined)
  const allConfigs = store.orderedConfigs
  const enabledConfigs = allConfigs.filter(config => config.enabled !== false)
  const servers = enabledConfigs.flatMap((config) => {
    const source = store.getSource(config.id)
    return config.type === 'server' && source instanceof ServerDataSource ? [{ config, source }] : []
  })
  if (!servers.length)
    return { status: 'pending' }
  const history = await readLocalHistory()
  let merged = 0
  let focusTargets = 0
  let confirmedTargets = 0
  let focusRejection: string | undefined
  for (const target of servers) {
    const outgoing = await createServerHistoryUploadChanges(history, allConfigs, target.config.id)
    const focusedChange = focus && outgoing.find(change => change.deleted === true
      && change.source_id === focus.sourceId && change.media_identity === focus.mediaIdentity
      && change.updated_at === focus.updatedAt)
    if (focusedChange)
      focusTargets++
    let focusedRejected = false
    let focusedConfirmed = false
    const cursorKey = `${CURSOR_PREFIX}${target.config.id}`
    let cursor = safeCursor(getAppSetting(cursorKey))
    try {
      let received = 0
      let rejected = 0
      const batches = chunkServerHistoryChanges(outgoing)
      let shouldPullMore = false
      let rejectedUpload = false
      for (const changes of batches) {
        let response: ServerPlaybackHistorySyncResponse
        try {
          response = await target.source.syncPlaybackHistory({ cursor, changes })
        }
        catch (error) {
          if (!(error instanceof ServerRequestError) || !error.code || !HISTORY_RECORD_REJECTION_CODES.has(error.code))
            throw error
          rejectedUpload = true
          rejected += changes.length
          if (focusedChange && changes.some(change => change.sync_key === focusedChange.sync_key)) {
            focusedRejected = true
            focusRejection = error.code
          }
          continue
        }
        received += response.changes.length
        rejected += response.rejected.length
        const focusError = response.rejected.find(item => item.sync_key === focusedChange?.sync_key)
        if (focusError) {
          focusedRejected = true
          focusRejection = focusError.code
        }
        if (focus && focusedChange && serverConfirmsHistoryDeletion(focus, focusedChange, response.changes))
          focusedConfirmed = true
        const incoming = response.changes.flatMap(change => mapServerHistoryChangeToLocalEntry(change, enabledConfigs, target.config))
        if (incoming.length)
          merged += await invoke<number>('player_merge_playback_history', { entries: incoming })
        if (response.cursor < cursor)
          throw new Error('Server 播放历史游标发生回退。')
        cursor = response.cursor
        await setAppSetting(cursorKey, String(cursor))
        shouldPullMore = response.changes.length === 500
      }
      if (rejectedUpload)
        shouldPullMore = true
      for (let page = 0; shouldPullMore && page < 100; page++) {
        const response = await target.source.syncPlaybackHistory({ cursor, changes: [] })
        received += response.changes.length
        rejected += response.rejected.length
        const focusError = response.rejected.find(item => item.sync_key === focusedChange?.sync_key)
        if (focusError) {
          focusedRejected = true
          focusRejection = focusError.code
        }
        if (focus && focusedChange && serverConfirmsHistoryDeletion(focus, focusedChange, response.changes))
          focusedConfirmed = true
        const incoming = response.changes.flatMap(change => mapServerHistoryChangeToLocalEntry(change, enabledConfigs, target.config))
        if (incoming.length)
          merged += await invoke<number>('player_merge_playback_history', { entries: incoming })
        if (response.cursor < cursor)
          throw new Error('Server 播放历史游标发生回退。')
        cursor = response.cursor
        await setAppSetting(cursorKey, String(cursor))
        shouldPullMore = response.changes.length === 500
        if (!shouldPullMore)
          break
      }
      if (focusedChange && !focusedRejected && focusedConfirmed)
        confirmedTargets++
      await saveSyncDiagnostic(target.config.id, { ok: !rejectedUpload, cursor, outgoing: outgoing.length, received, rejected })
    }
    catch (error) {
      // Keep the tombstone for a later retry. A Server response is an explicit rejection.
      if (focusedChange && error instanceof ServerRequestError)
        focusRejection = error.code ?? error.message
      await saveSyncDiagnostic(target.config.id, { ok: false, cursor, outgoing: outgoing.length, message: redactSensitiveText(error).slice(0, 256) })
    }
  }
  if (merged > 0)
    window.dispatchEvent(new CustomEvent(PLAYED_STATE_CHANGED_EVENT, { detail: { source: 'server-history-sync' } }))
  if (focusRejection)
    return { status: 'rejected', reason: focusRejection }
  return focusTargets > 0 && confirmedTargets === focusTargets ? { status: 'confirmed' } : { status: 'pending' }
}

export function serverConfirmsHistoryDeletion(focus: HistorySyncFocus, uploaded: ServerPlaybackHistoryChange, returned: readonly ServerPlaybackHistoryChange[]): boolean {
  return returned.some(change => change.sync_key === uploaded.sync_key && change.deleted === true
    && change.updated_at >= focus.updatedAt)
}

export async function createServerHistoryUploadChanges(history: readonly PlaybackHistoryEntry[], configs: readonly DataSourceConfig[], targetServerId: string): Promise<ServerPlaybackHistoryChange[]> {
  const configsById = new Map(configs.map(config => [config.id, config]))
  return Promise.all(history.flatMap((entry) => {
    const sourceConfig = historySourceDescriptor(entry, configsById)
    if (!sourceConfig || (sourceConfig.type === 'server' && sourceConfig.id !== targetServerId))
      return []
    return [toServerChange(entry, sourceConfig)]
  }))
}

async function saveSyncDiagnostic(sourceId: string, value: { ok: boolean, cursor: number, outgoing: number, received?: number, rejected?: number, message?: string }): Promise<void> {
  try {
    await setAppSetting(`${DIAGNOSTIC_PREFIX}${sourceId}`, JSON.stringify({ timestamp: new Date().toISOString(), ...value }))
  }
  catch {
    // Diagnostics must never make playback history synchronization fail.
  }
}

export function chunkServerHistoryChanges(changes: readonly ServerPlaybackHistoryChange[]): ServerPlaybackHistoryChange[][] {
  if (!changes.length)
    return [[]]
  const result: ServerPlaybackHistoryChange[][] = []
  for (let offset = 0; offset < changes.length; offset += 500)
    result.push(changes.slice(offset, offset + 500))
  return result
}

async function readLocalHistory(): Promise<PlaybackHistoryEntry[]> {
  const result: PlaybackHistoryEntry[] = []
  for (let page = 1; page <= 100_000; page++) {
    const current = await listPlaybackHistorySyncPage(page, 100)
    result.push(...current.list)
    if (!current.hasMore)
      break
  }
  return result
}

function historySourceDescriptor(entry: PlaybackHistoryEntry, configsById: ReadonlyMap<string, DataSourceConfig>): HistorySourceDescriptor | undefined {
  const config = configsById.get(entry.sourceId)
  if (config) {
    return {
      id: config.id,
      type: config.type,
      name: config.displayName ?? config.name,
      url: config.url,
    }
  }
  if (entry.sourceId === 'local-file')
    return { id: 'local-file', type: 'local-file', name: '本机文件', url: '' }
  return undefined
}

async function toServerChange(entry: PlaybackHistoryEntry, config: HistorySourceDescriptor): Promise<ServerPlaybackHistoryChange> {
  const locator = safeOrigin(config.url) ?? ''
  const stableSource = locator || config.id
  const historyIdentity = config.type === 'server' && isServerHistoryIdentity(entry.mediaIdentity)
    ? entry.mediaIdentity
    : undefined
  const itemToken = historyIdentity ? canonicalMovieWorkToken(historyIdentity) ?? entry.itemId : entry.itemId
  return {
    sync_key: await sha256(historyIdentity ? `${config.type}\0${historyIdentity}` : `${config.type}\0${stableSource}\0${entry.mediaIdentity}`),
    source_kind: config.type,
    source_name: config.name,
    source_locator: locator || undefined,
    source_id: config.id,
    library_id: entry.libraryId ?? undefined,
    item_id: itemToken ?? undefined,
    item_token: itemToken ?? undefined,
    media_identity: entry.mediaIdentity,
    history_identity: historyIdentity,
    title: entry.title,
    stream_identity: entry.streamIdentity ?? undefined,
    media_type: entry.mediaType ?? undefined,
    poster_url: entry.posterUrl ?? undefined,
    backdrop_url: entry.backdropUrl ?? undefined,
    title_logo_url: entry.titleLogoUrl ?? undefined,
    display_subtitle: entry.displaySubtitle ?? undefined,
    episode_still_url: entry.episodeStillUrl ?? undefined,
    position: entry.position,
    duration: entry.duration ?? undefined,
    completed: entry.completed,
    deleted: entry.deleted,
    updated_at: normalizeTimestamp(entry.updatedAt),
  }
}

export function mapServerHistoryChangeToLocalEntry(change: ServerPlaybackHistoryChange, configs: readonly DataSourceConfig[], currentServer: DataSourceConfig) {
  const locator = change.source_locator ? safeOrigin(change.source_locator) : undefined
  const config = change.source_kind === 'server'
    ? currentServer
    : configs.find((candidate) => {
        if (candidate.type !== change.source_kind || candidate.enabled === false)
          return false
        return locator ? safeOrigin(candidate.url) === locator : candidate.id === change.source_id
      })
  if (!config)
    return []
  const presentation = change.source_kind === 'server' ? mapServerHistoryItem(config.id, change, config.url)[0] : undefined
  return [{
    sourceId: config.id,
    libraryId: change.library_id,
    itemId: presentation?.id ?? change.item_token ?? change.item_id,
    mediaIdentity: change.deleted === true ? change.media_identity : presentation?.historyIdentity ?? change.history_identity ?? change.media_identity,
    title: presentation?.name ?? change.display_title ?? change.series_title ?? change.title,
    streamIdentity: change.stream_identity,
    mediaType: presentation?.type ?? change.media_type,
    posterUrl: presentation?.posterUrl ?? change.poster_url,
    backdropUrl: presentation?.backdropUrl ?? change.backdrop_url,
    titleLogoUrl: presentation?.titleLogoUrl ?? change.title_logo_url,
    displaySubtitle: presentation?.displaySubtitle ?? change.display_subtitle,
    episodeStillUrl: presentation?.episodeStillUrl ?? change.episode_still_url,
    position: change.position,
    duration: change.duration,
    completed: change.completed,
    deleted: change.deleted === true,
    updatedAt: normalizeTimestamp(change.updated_at),
  }]
}

function isServerHistoryIdentity(value: string): boolean {
  return /^server:v1:(?:movie|episode):\S{1,1024}$/.test(value)
}

function canonicalMovieWorkToken(identity: string): string | undefined {
  const match = /^server:v1:movie:([1-9]\d*):([\w-]{1,512})$/.exec(identity)
  return match ? `work|${match[1]}|${match[2]}` : undefined
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
