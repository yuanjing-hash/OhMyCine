import type { DataSourceConfig } from '@/services/datasource/types'
import { getAppSetting, listAppSettingKeys, removeAppSetting, setAppSetting } from '@/services/appSettings'
import { removeCredential } from '@/services/datasource/credentialStore'
import { loadHomeContributionPreferences, saveHomeContributionPreferences } from '@/services/homeContributionPreferences'
import { listLocalMediaCollections, removeLocalCollectionMember } from '@/services/mediaCollections'
import { pruneMediaTombstonesForSources } from '@/services/mediaDelete'
import { deleteMediaPlaybackPreferencesForSource } from '@/services/mediaPlaybackPreferences'
import { removeNavigationShortcutBinding } from '@/services/navigationShortcuts'
import { deletePlaybackHistoryForSource } from '@/services/playbackHistory'

const PENDING_KEY = 'ohmycine-player-legacy-source-cleanup-v1'
const DISPLAY_CACHE_KEY = 'ohmycine-media-display-cache-v1'
const GLOBAL_SETTINGS = [
  'ohmycine-tmdb-settings-v1',
  'ohmycine-scrape-classification-rules',
]
const SCHEDULE_PREFIXES = [
  'ohmycine-raw-source-index-schedule-v1:',
  'ohmycine-raw-source-index-schedule-v2:',
]
const RAW_CACHE_PREFIX = 'ohmycine-raw-source-scan-cache-v1:'

interface PendingSource {
  id: string
  type: string
  credentialRefs: string[]
}

interface PendingCleanup {
  sources: PendingSource[]
  globalPending: boolean
}

/** Queue the cleanup before saving the filtered data-source list, so a failed step retries. */
export async function queueLegacySourceCleanup(configs: readonly DataSourceConfig[]): Promise<void> {
  const hasLegacySettings = GLOBAL_SETTINGS.some(key => getAppSetting(key) != null)
    || listAppSettingKeys().some(key => SCHEDULE_PREFIXES.some(prefix => key.startsWith(prefix)))
    || hasBrowserRawScanCache()
  if (configs.length === 0 && !hasLegacySettings)
    return
  const pending = readPending()
  const byId = new Map(pending.sources.map(source => [source.id, source]))
  for (const config of configs) {
    const refs = new Set<string>()
    const configuredRef = config.extra?.credentialRef
    if (typeof configuredRef === 'string' && configuredRef.trim())
      refs.add(configuredRef)
    refs.add(`datasource:${config.id}:${config.type}-credential`)
    byId.set(config.id, {
      id: config.id,
      type: config.type,
      credentialRefs: [...refs],
    })
  }
  await savePending({ sources: [...byId.values()], globalPending: pending.globalPending || configs.length > 0 || hasLegacySettings })
}

export async function runLegacySourceCleanup(): Promise<void> {
  let pending = readPending()
  for (const source of pending.sources) {
    if (!await deletePlaybackHistoryForSource(source.id))
      throw new Error('旧媒体源播放记录清理失败，下次启动将重试。')
    await deleteMediaPlaybackPreferencesForSource(source.id)
    await removeNavigationShortcutBinding(`source:${source.id}`)
    await removeSourceFromCollections(source.id)
    await removeSourceFromHomePreferences(source.id)
    await pruneMediaTombstonesForSources([source.id])
    for (const ref of source.credentialRefs)
      await removeCredential(ref)
    pending = {
      ...pending,
      sources: pending.sources.filter(item => item.id !== source.id),
    }
    await savePending(pending)
  }
  if (!pending.globalPending)
    return

  for (const key of GLOBAL_SETTINGS)
    await removeAppSetting(key)
  for (const key of listAppSettingKeys()) {
    if (SCHEDULE_PREFIXES.some(prefix => key.startsWith(prefix)))
      await removeAppSetting(key)
  }
  await removeCredential('settings:tmdb-credential')
  await removeAppSetting(DISPLAY_CACHE_KEY)
  removeBrowserRawScanCache()
  await savePending({ sources: [], globalPending: false })
}

async function removeSourceFromCollections(sourceId: string): Promise<void> {
  const collections = await listLocalMediaCollections()
  for (const collection of collections) {
    for (const member of collection.members) {
      if (member.sourceId === sourceId)
        await removeLocalCollectionMember(collection.id, member.sourceId, member.itemId)
    }
  }
}

async function removeSourceFromHomePreferences(sourceId: string): Promise<void> {
  const preferences = loadHomeContributionPreferences()
  const entries = Object.entries(preferences)
    .filter(([key]) => key !== sourceId && !key.startsWith(`${sourceId}\0`))
  if (entries.length !== Object.keys(preferences).length)
    await saveHomeContributionPreferences(Object.fromEntries(entries))
}

function browserStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null
  }
  catch {
    return null
  }
}

function hasBrowserRawScanCache(): boolean {
  const storage = browserStorage()
  if (!storage)
    return false
  for (let index = 0; index < storage.length; index++) {
    if (storage.key(index)?.startsWith(RAW_CACHE_PREFIX))
      return true
  }
  return false
}

function removeBrowserRawScanCache(): void {
  const storage = browserStorage()
  if (!storage)
    return
  for (let index = storage.length - 1; index >= 0; index--) {
    const key = storage.key(index)
    if (key?.startsWith(RAW_CACHE_PREFIX))
      storage.removeItem(key)
  }
}

function readPending(): PendingCleanup {
  try {
    const value = JSON.parse(getAppSetting(PENDING_KEY) ?? 'null') as Partial<PendingCleanup> | null
    return {
      sources: Array.isArray(value?.sources)
        ? value.sources.filter((source): source is PendingSource => typeof source?.id === 'string'
          && typeof source?.type === 'string'
          && Array.isArray(source.credentialRefs))
        : [],
      globalPending: value?.globalPending === true,
    }
  }
  catch {
    return { sources: [], globalPending: false }
  }
}

async function savePending(value: PendingCleanup): Promise<void> {
  if (value.sources.length === 0 && !value.globalPending)
    await removeAppSetting(PENDING_KEY)
  else
    await setAppSetting(PENDING_KEY, JSON.stringify(value))
}
