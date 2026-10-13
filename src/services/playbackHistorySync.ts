import type { ServerPlaybackHistoryChange, ServerPlaybackHistorySyncResponse } from '@/services/datasource/server'
import type { DataSourceConfig } from '@/services/datasource/types'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import type { useDataSourceStore } from '@/stores/datasource'
import { invoke } from '@tauri-apps/api/core'
import { watch } from 'vue'
import { setAppSetting } from '@/services/appSettings'
import { readServerCredential } from '@/services/datasource/credentialStore'
import { redactSensitiveText } from '@/services/datasource/errors'
import { mapServerHistoryItem, ServerDataSource, ServerRequestError } from '@/services/datasource/server'
import { parseOnlineItemID } from '@/services/datasource/serverOnline'
import { configurePlaybackHistoryScopeResolver, invalidatePendingPlaybackHistoryMutations, listPlaybackHistorySyncPage, PLAYED_STATE_CHANGED_EVENT } from '@/services/playbackHistory'

type DataSourceStore = ReturnType<typeof useDataSourceStore>
interface HistorySyncState { cursor: number, generation: number }
export interface HistorySyncFocus { sourceId: string, mediaIdentity: string, updatedAt: number }
export interface HistorySyncOutcome { status: 'confirmed' | 'pending' | 'rejected', reason?: string }
const HISTORY_RECORD_REJECTION_CODES = new Set(['INVALID_REQUEST', 'NOT_FOUND', 'PERMISSION_DENIED', 'history_clock_ahead'])

// Automatic, manual, deletion and reset callers share this queue.
let syncQueue: Promise<unknown> = Promise.resolve()
let syncEpoch = 0
let bindingEpoch = 0
function serialize<T>(operation: () => Promise<T>): Promise<T> {
  const result = syncQueue.then(operation)
  syncQueue = result.catch(() => undefined)
  return result
}

async function scopeForServer(source: ServerDataSource): Promise<string> {
  const account = await source.getPlaybackHistoryAccount()
  return sha256(`${account.endpoint}\0${account.accountId}\0${source.id}`)
}

async function mappingKey(configs: readonly DataSourceConfig[]): Promise<string> {
  return sha256(JSON.stringify(configs.map(config => ({
    id: config.id,
    type: config.type,
    endpoint: safeEndpoint(config.url),
    userId: providerUserID(config),
  })).sort((left, right) => left.id.localeCompare(right.id))))
}

async function validationKey(config: DataSourceConfig): Promise<string> {
  return sha256(JSON.stringify({ id: config.id, type: config.type, endpoint: safeEndpoint(config.url), credentialRef: config.extra?.credentialRef, credentialVersion: config.extra?.credentialVersion, userId: providerUserID(config) }))
}

async function providerScope(config: DataSourceConfig): Promise<string | null> {
  return safeEndpoint(config.url) && providerUserID(config)
    ? sha256(`${config.type}\0${safeEndpoint(config.url)}\0${providerUserID(config)}\0${config.id}`)
    : null
}

async function bindHistorySource(store: DataSourceStore, sourceId: string): Promise<string | null> {
  const epoch = bindingEpoch
  const config = store.orderedConfigs.find(config => config.id === sourceId)
  if (!config)
    return null
  const source = store.getSource(sourceId)
  const validation = await validationKey(config)
  const context = await mappingKey(store.orderedConfigs)
  if (epoch !== bindingEpoch)
    return null
  if (config.type === 'emby' || config.type === 'jellyfin') {
    const scope = await providerScope(config)
    if (epoch !== bindingEpoch)
      return null
    if (scope) {
      await invoke<HistorySyncState>('player_get_playback_history_sync_state', { sourceId, scope, mappingKey: context, validationKey: validation })
    }
    return scope
  }
  if (config.type !== 'server')
    return null
  try {
    if (!(source instanceof ServerDataSource))
      throw new Error('Server 暂时不可连接。')
    const scope = await scopeForServer(source)
    if (epoch !== bindingEpoch || store.getSource(sourceId) !== source)
      throw new Error('Server 连接已经改变。')
    await invoke<HistorySyncState>('player_get_playback_history_sync_state', {
      sourceId,
      scope,
      mappingKey: context,
      validationKey: validation,
    })
    return scope
  }
  catch {
    if (epoch !== bindingEpoch)
      return null
    const ref = config.extra?.credentialRef
    if (typeof ref !== 'string' || !await readServerCredential(ref))
      return null
    if (epoch !== bindingEpoch)
      return null
    // Offline writes can use the last validated account only with the identical credential context.
    return invoke<string | null>('player_restore_playback_history_scope', { sourceId, validationKey: validation })
  }
}

export function startPlaybackHistorySync(store: DataSourceStore): () => void {
  let timer: number | undefined
  let running = false
  let rerun = false
  let disposed = false
  const disposeScopeResolver = configurePlaybackHistoryScopeResolver(sourceId => bindHistorySource(store, sourceId))
  const run = async () => {
    if (disposed)
      return
    if (running) {
      rerun = true
      return
    }
    running = true
    try {
      do {
        rerun = false
        await syncPlaybackHistory(store)
        if (disposed)
          break
      } while (rerun)
    }
    finally {
      running = false
    }
  }
  const schedule = () => {
    if (timer !== undefined)
      window.clearTimeout(timer)
    timer = window.setTimeout(() => {
      timer = undefined
      void run()
    }, 250)
  }
  const changed = (event: Event) => {
    if ((event as CustomEvent<{ source?: string }>).detail?.source === 'player-history')
      schedule()
  }
  const visible = () => {
    if (!document.hidden)
      schedule()
  }
  window.addEventListener(PLAYED_STATE_CHANGED_EVENT, changed)
  window.addEventListener('online', schedule)
  window.addEventListener('focus', schedule)
  document.addEventListener('visibilitychange', visible)
  let configuredSourceIds = store.orderedConfigs.map(config => config.id)
  const disposeConfigWatch = watch(() => JSON.stringify(store.orderedConfigs.map(config => ({
    id: config.id,
    type: config.type,
    url: config.url,
    extra: config.extra,
  }))), () => {
    bindingEpoch++
    store.clearPlaybackHistoryDisplayCache()
    const currentIds = store.orderedConfigs.map(config => config.id)
    const sourceIds = [...new Set([...configuredSourceIds, ...currentIds])]
    configuredSourceIds = currentIds
    void invoke('player_invalidate_playback_history_sources', { sourceIds }).then(schedule, schedule)
  }, { flush: 'sync' })
  void run()
  const interval = window.setInterval(() => void run(), 60_000)
  return () => {
    disposed = true
    disposeScopeResolver()
    disposeConfigWatch()
    window.removeEventListener(PLAYED_STATE_CHANGED_EVENT, changed)
    window.removeEventListener('online', schedule)
    window.removeEventListener('focus', schedule)
    document.removeEventListener('visibilitychange', visible)
    if (timer !== undefined)
      window.clearTimeout(timer)
    window.clearInterval(interval)
  }
}

export function syncPlaybackHistory(store: DataSourceStore, focus?: HistorySyncFocus): Promise<HistorySyncOutcome> {
  const epoch = syncEpoch
  return serialize(() => epoch === syncEpoch ? performSync(store, epoch, focus) : Promise.resolve<HistorySyncOutcome>({ status: 'pending' }))
    .catch((): HistorySyncOutcome => ({ status: 'pending' }))
}

export function clearLocalPlaybackHistoryAndResync(store: DataSourceStore): Promise<HistorySyncOutcome> {
  const epoch = ++syncEpoch
  invalidatePendingPlaybackHistoryMutations()
  // Drain any issued upload first: invalidating a local result cannot undo a cloud write.
  return serialize(async () => {
    await invoke<number>('player_clear_local_playback_history')
    store.clearPlaybackHistoryDisplayCache()
    window.dispatchEvent(new CustomEvent(PLAYED_STATE_CHANGED_EVENT, { detail: { source: 'local-history-reset' } }))
    return performSync(store, epoch)
  })
}

async function performSync(store: DataSourceStore, epoch: number, focus?: HistorySyncFocus): Promise<HistorySyncOutcome> {
  const currentBindingEpoch = bindingEpoch
  store.loadConfigs()
  await store.syncManager()
  const configs = [...store.orderedConfigs]
  const context = await mappingKey(configs)
  await Promise.allSettled(configs.map(config => bindHistorySource(store, config.id)))
  const providerScopes = new Map((await Promise.all(configs.map(async config => [config.id, config.type === 'emby' || config.type === 'jellyfin' ? await providerScope(config) : null] as const))).filter((entry): entry is readonly [string, string] => entry[1] !== null))
  const targets = configs.flatMap((config) => {
    const source = store.getSource(config.id)
    return config.enabled !== false && source instanceof ServerDataSource ? [{ config, source }] : []
  })
  if (!targets.length || epoch !== syncEpoch)
    return { status: 'pending' }
  let merged = 0
  let completeTargets = 0
  let focusTargets = 0
  let confirmedTargets = 0
  let focusRejection: string | undefined
  for (const target of targets) {
    let outgoingCount = 0
    let cursor = 0
    try {
      const scope = await scopeForServer(target.source)
      const state = await invoke<HistorySyncState>('player_get_playback_history_sync_state', {
        sourceId: target.config.id,
        scope,
        mappingKey: context,
        validationKey: await validationKey(target.config),
      })
      cursor = state.cursor
      const history = await readLocalHistory(scope)
      const uploads = await createServerHistoryUploads(history, configs, target.config.id, identity => target.source.canSyncOnlineHistory(identity), scope, providerScopes)
      const outgoing = uploads.map(upload => upload.change)
      outgoingCount = outgoing.length
      const snapshots = new Map(uploads.map(upload => [upload.change, upload.entry] as const))
      const focusedChange = focus && outgoing.find((change) => {
        const entry = snapshots.get(change)
        return change.deleted && entry?.sourceId === focus.sourceId && entry.mediaIdentity === focus.mediaIdentity && entry.updatedAt === focus.updatedAt
      })
      if (focusedChange)
        focusTargets++
      let focusedConfirmed = false
      let focusedRejected = false
      let received = 0
      let rejected = 0
      let failedUpload = false
      const active = () => epoch === syncEpoch && currentBindingEpoch === bindingEpoch && store.getSource(target.config.id) === target.source
      const apply = async (response: ServerPlaybackHistorySyncResponse, changes: readonly ServerPlaybackHistoryChange[]) => {
        if (!active())
          return false
        if (response.cursor < cursor)
          throw new Error('Server 播放历史游标发生回退。')
        const rejects = new Set(response.rejected.map(item => item.sync_key))
        const focusError = response.rejected.find(item => !item.sync_key || item.sync_key === focusedChange?.sync_key)
        if (focusError && focusedChange) {
          focusedRejected = true
          focusRejection = focusError.code
        }
        if (focus && focusedChange && serverConfirmsHistoryDeletion(focus, focusedChange, response.changes))
          focusedConfirmed = true
        const acknowledgments = changes.filter(change => !rejects.has(undefined) && !rejects.has(change.sync_key))
          .flatMap((change) => {
            const entry = snapshots.get(change)
            return entry ? [{ sourceId: entry.sourceId, mediaIdentity: entry.mediaIdentity, updatedAt: entry.updatedAt }] : []
          })
        // Import disabled providers too. Adding a configured provider changes the mapping key and
        // resets the pull checkpoint, so previously unconfigured records can be replayed.
        const entries = response.changes.flatMap(change => mapServerHistoryChangeToLocalEntry(change, configs, target.config, scope, providerScopes))
        merged += await invoke<number>('player_apply_playback_history_sync', {
          request: {
            sourceId: target.config.id,
            scope,
            mappingKey: context,
            generation: state.generation,
            cursor: response.cursor,
            entries,
            acknowledgments,
          },
        })
        cursor = response.cursor
        received += response.changes.length
        rejected += response.rejected.length
        return true
      }
      for (const changes of chunkServerHistoryChanges(outgoing)) {
        if (!changes.length)
          continue
        if (!active())
          return { status: 'pending' }
        try {
          const response = await target.source.syncPlaybackHistory({ cursor, changes })
          if (!await apply(response, changes))
            return { status: 'pending' }
        }
        catch (error) {
          if (!(error instanceof ServerRequestError) || !error.code || !HISTORY_RECORD_REJECTION_CODES.has(error.code))
            throw error
          failedUpload = true
          rejected += changes.length
          if (focusedChange && changes.some(change => change.sync_key === focusedChange.sync_key)) {
            focusedRejected = true
            focusRejection = error.code
          }
        }
      }
      let fullyPulled = false
      // A short response can still advance the cursor through filtered Server rows. Drain by
      // revision movement, not by the visible result count.
      for (let page = 0; page < 100 && active(); page++) {
        const response = await target.source.syncPlaybackHistory({ cursor, changes: [] })
        const previous = cursor
        if (!await apply(response, []))
          return { status: 'pending' }
        if (cursor === previous) {
          fullyPulled = true
          break
        }
      }
      if (fullyPulled && !failedUpload && rejected === 0)
        completeTargets++
      if (focusedChange && focusedConfirmed && !focusedRejected)
        confirmedTargets++
      await saveSyncDiagnostic(target.config.id, { ok: fullyPulled && !failedUpload, cursor, outgoing: outgoingCount, received, rejected })
    }
    catch (error) {
      if (focus && error instanceof ServerRequestError)
        focusRejection = error.code ?? error.message
      await saveSyncDiagnostic(target.config.id, { ok: false, cursor, outgoing: outgoingCount, message: redactSensitiveText(error).slice(0, 256) })
    }
  }
  if (merged > 0 && epoch === syncEpoch) {
    store.invalidateHomeCache()
    window.dispatchEvent(new CustomEvent(PLAYED_STATE_CHANGED_EVENT, { detail: { source: 'server-history-sync' } }))
  }
  if (focusRejection)
    return { status: 'rejected', reason: focusRejection }
  return (focus ? focusTargets > 0 && confirmedTargets === focusTargets : completeTargets === targets.length)
    ? { status: 'confirmed' }
    : { status: 'pending' }
}

export function serverConfirmsHistoryDeletion(focus: HistorySyncFocus, uploaded: ServerPlaybackHistoryChange, returned: readonly ServerPlaybackHistoryChange[]): boolean {
  return returned.some(change => change.sync_key === uploaded.sync_key && change.deleted === true && change.updated_at >= focus.updatedAt)
}

export async function createServerHistoryUploadChanges(history: readonly PlaybackHistoryEntry[], configs: readonly DataSourceConfig[], targetServerId: string, acceptOnlineHistory?: (identity: string) => boolean, targetScope?: string, providerScopes?: ReadonlyMap<string, string>): Promise<ServerPlaybackHistoryChange[]> {
  return (await createServerHistoryUploads(history, configs, targetServerId, acceptOnlineHistory, targetScope, providerScopes)).map(upload => upload.change)
}

async function createServerHistoryUploads(history: readonly PlaybackHistoryEntry[], configs: readonly DataSourceConfig[], targetServerId: string, acceptOnlineHistory?: (identity: string) => boolean, targetScope?: string, providerScopes?: ReadonlyMap<string, string>): Promise<{ change: ServerPlaybackHistoryChange, entry: PlaybackHistoryEntry }[]> {
  const configsById = new Map(configs.map(config => [config.id, config]))
  return Promise.all(history.flatMap((entry) => {
    const config = configsById.get(entry.sourceId)
    if (!config || entry.progressSource !== 'local' || !['server', 'emby', 'jellyfin'].includes(config.type)
      || !safeEndpoint(config.url) || !Number.isSafeInteger(entry.updatedAt) || entry.updatedAt < 10_000_000_000) {
      return []
    }
    if (config.type === 'server') {
      if (config.id !== targetServerId || !targetScope || entry.ownerScope !== targetScope || !isServerHistoryIdentity(entry.mediaIdentity))
        return []
      if (parseOnlineItemID(entry.mediaIdentity)?.kind === 'version' && acceptOnlineHistory && !acceptOnlineHistory(entry.mediaIdentity))
        return []
    }
    else if (!providerUserID(config) || !entry.ownerScope || entry.ownerScope !== providerScopes?.get(config.id)) {
      return []
    }
    return [toServerChange(entry, config).then(change => ({ change, entry }))]
  }))
}

function providerUserID(config: DataSourceConfig): string | undefined {
  return typeof config.extra?.userId === 'string' && config.extra.userId ? config.extra.userId : undefined
}

function providerAccountID(config: DataSourceConfig): string {
  return `${config.type}:v1:${providerUserID(config) ?? ''}`
}

async function toServerChange(entry: PlaybackHistoryEntry, config: DataSourceConfig): Promise<ServerPlaybackHistoryChange> {
  const locator = safeEndpoint(config.url)
  if (!locator || !Number.isSafeInteger(entry.updatedAt) || entry.updatedAt < 10_000_000_000)
    throw new Error('播放历史的来源或时间无效。')
  const historyIdentity = config.type === 'server' ? entry.mediaIdentity : undefined
  const itemToken = historyIdentity ? canonicalMovieWorkToken(historyIdentity) ?? entry.itemId : entry.itemId
  return {
    sync_key: await sha256(historyIdentity ?? `${config.type}\0${locator}\0${providerUserID(config)}\0${entry.mediaIdentity}`),
    source_kind: config.type,
    source_name: config.displayName ?? config.name,
    source_locator: locator,
    source_id: config.type === 'server' ? config.id : providerAccountID(config),
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
    updated_at: entry.updatedAt,
  }
}

export function mapServerHistoryChangeToLocalEntry(change: ServerPlaybackHistoryChange, configs: readonly DataSourceConfig[], currentServer: DataSourceConfig, scope?: string, providerScopes?: ReadonlyMap<string, string>) {
  const config = change.source_kind === 'server'
    ? currentServer
    : configs.find(candidate => candidate.type === change.source_kind && safeEndpoint(candidate.url) === safeEndpoint(change.source_locator ?? '')
      && providerAccountID(candidate) === change.source_id)
  if (!config || (config.type === 'server' && (!scope || !change.history_identity || !isServerHistoryIdentity(change.history_identity) || !change.item_token))
    || (config.type !== 'server' && !providerScopes?.has(config.id))) {
    return []
  }
  const presentation = config.type === 'server' ? mapServerHistoryItem(config.id, change, config.url)[0] : undefined
  return [{
    sourceId: config.id,
    ownerScope: config.type === 'server' ? scope : providerScopes?.get(config.id),
    libraryId: change.library_id,
    itemId: presentation?.id ?? change.item_token ?? change.item_id,
    mediaIdentity: config.type === 'server' ? change.history_identity! : change.media_identity,
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
    updatedAt: change.updated_at,
  }]
}

export function chunkServerHistoryChanges(changes: readonly ServerPlaybackHistoryChange[]): ServerPlaybackHistoryChange[][] {
  if (!changes.length)
    return [[]]
  const result: ServerPlaybackHistoryChange[][] = []
  for (let offset = 0; offset < changes.length; offset += 500)
    result.push(changes.slice(offset, offset + 500))
  return result
}

async function readLocalHistory(scope: string): Promise<PlaybackHistoryEntry[]> {
  const result: PlaybackHistoryEntry[] = []
  for (let page = 1; page <= 100_000; page++) {
    const current = await listPlaybackHistorySyncPage(page, 100, scope)
    result.push(...current.list)
    if (!current.hasMore)
      break
  }
  return result
}

async function saveSyncDiagnostic(sourceId: string, value: { ok: boolean, cursor: number, outgoing: number, received?: number, rejected?: number, message?: string }): Promise<void> {
  try {
    await setAppSetting(`ohmycine:server-history-sync-diagnostic:${sourceId}`, JSON.stringify({ timestamp: new Date().toISOString(), ...value }))
  }
  catch {
    // Diagnostic persistence must not make synchronization fail.
  }
}

function isServerHistoryIdentity(value: string): boolean {
  return parseOnlineItemID(value)?.kind === 'version' || /^server:v1:(?:movie|episode):\S{1,1024}$/.test(value)
}

function canonicalMovieWorkToken(identity: string): string | undefined {
  const match = /^server:v1:movie:([1-9]\d*):([\w-]{1,512})$/.exec(identity)
  return match ? `work|${match[1]}|${match[2]}` : undefined
}

function safeEndpoint(value: string): string | undefined {
  try {
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password && !url.search && !url.hash
      ? `${url.origin}${url.pathname.replace(/\/+$/, '')}`
      : undefined
  }
  catch {
    return undefined
  }
}

async function sha256(value: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value))
  return [...new Uint8Array(digest)].map(byte => byte.toString(16).padStart(2, '0')).join('')
}
