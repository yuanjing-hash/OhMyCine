import { invoke } from '@tauri-apps/api/core'

export type PlayerStorageMode = 'standard' | 'portable'
export type PlayerCredentialProtection = 'windowsDpapi' | 'portableFileKey' | 'localFileKey'

export interface PlayerStorageInfo {
  mode: PlayerStorageMode
  baseDir: string
  dataDir: string
  cacheDir: string
  logDir: string
  portableMarkerPath: string
  credentialProtection: PlayerCredentialProtection
}

interface SettingStorage {
  getItem: (key: string) => string | null
  setItem: (key: string, value: string) => void
  removeItem: (key: string) => void
}

const MIGRATABLE_EXACT_KEYS = new Set([
  'ohmycine-datasources',
  'ohmycine-theme',
  'ohmycine-tmdb-settings-v1',
  'ohmycine-scrape-classification-rules',
  'ohmycine:persistent-credentials-unavailable',
])
const MIGRATABLE_PREFIXES = [
  'ohmycine-raw-source-index-schedule-v1:',
  'ohmycine-raw-source-index-schedule-v2:',
]

const settings = new Map<string, string>()
let initialized = false
let desktopStorage = false
let writeQueue: Promise<void> = Promise.resolve()
let pendingWriteError: unknown = null

export async function initializeAppSettings(): Promise<void> {
  if (initialized)
    return

  const legacyEntries = readLegacyEntries()
  if (isTauriRuntime()) {
    try {
      const persisted = await invoke<Record<string, string>>('player_settings_get_all')
      for (const [key, value] of Object.entries(persisted))
        settings.set(key, value)

      for (const [key, value] of legacyEntries) {
        if (!settings.has(key))
          await invoke('player_settings_set', { key, value })
        settings.set(key, settings.get(key) ?? value)
        removeLegacyEntry(key)
      }
      desktopStorage = true
      initialized = true
      return
    }
    catch {
      // The browser fallback below keeps development and recovery mode usable.
    }
  }

  for (const [key, value] of legacyEntries)
    settings.set(key, value)
  initialized = true
}

export function getAppSetting(key: string): string | null {
  const value = settings.get(key)
  if (value != null)
    return value

  return initialized ? null : browserStorage()?.getItem(key) ?? null
}

export function setAppSetting(key: string, value: string): Promise<void> {
  settings.set(key, value)
  return enqueueWrite(async () => {
    if (desktopStorage)
      await invoke('player_settings_set', { key, value })
    else
      browserStorage()?.setItem(key, value)
  })
}

export function removeAppSetting(key: string): Promise<void> {
  settings.delete(key)
  return enqueueWrite(async () => {
    if (desktopStorage)
      await invoke('player_settings_delete', { key })
    else
      browserStorage()?.removeItem(key)
  })
}

export async function flushAppSettings(): Promise<void> {
  await writeQueue
  if (pendingWriteError != null) {
    const error = pendingWriteError
    pendingWriteError = null
    throw error
  }
}

export async function getPlayerStorageInfo(): Promise<PlayerStorageInfo | null> {
  if (!isTauriRuntime())
    return null

  try {
    return await invoke<PlayerStorageInfo>('player_get_storage_info')
  }
  catch {
    return null
  }
}

export const appSettingsStorage: SettingStorage = {
  getItem: getAppSetting,
  setItem(key, value) {
    void setAppSetting(key, value)
  },
  removeItem(key) {
    void removeAppSetting(key)
  },
}

function enqueueWrite(operation: () => Promise<void>): Promise<void> {
  const result = writeQueue.then(operation)
  result.catch((error) => {
    pendingWriteError = error
  })
  writeQueue = result.catch(() => undefined)
  return result
}

function readLegacyEntries(): Array<[string, string]> {
  const storage = browserStorage()
  if (!storage)
    return []

  const entries: Array<[string, string]> = []
  for (let index = 0; index < storage.length; index++) {
    const key = storage.key(index)
    if (!key || !isMigratableSettingKey(key))
      continue
    const value = storage.getItem(key)
    const safeValue = value == null ? null : sanitizeLegacyValue(key, value)
    if (safeValue != null)
      entries.push([key, safeValue])
  }
  return entries
}

function removeLegacyEntry(key: string): void {
  try {
    browserStorage()?.removeItem(key)
  }
  catch {
    // A successful SQLite import remains authoritative even if WebView cleanup fails.
  }
}

function isMigratableSettingKey(key: string): boolean {
  return MIGRATABLE_EXACT_KEYS.has(key) || MIGRATABLE_PREFIXES.some(prefix => key.startsWith(prefix))
}

function sanitizeLegacyValue(key: string, value: string): string | null {
  if (key === 'ohmycine-theme' || key === 'ohmycine:persistent-credentials-unavailable')
    return value

  try {
    return JSON.stringify(stripSensitiveFields(JSON.parse(value) as unknown))
  }
  catch {
    return null
  }
}

function stripSensitiveFields(value: unknown): unknown {
  if (Array.isArray(value))
    return value.map(stripSensitiveFields)
  if (!value || typeof value !== 'object')
    return value

  return Object.fromEntries(
    Object.entries(value)
      .filter(([key]) => !isSensitiveSettingField(key))
      .map(([key, child]) => [key, stripSensitiveFields(child)]),
  )
}

function isSensitiveSettingField(key: string): boolean {
  const normalized = key.toLowerCase()
  return ['apikey', 'api_key', 'access_token', 'passwd', 'pwd'].includes(normalized)
    || normalized.includes('token')
    || normalized.includes('password')
    || normalized.includes('username')
    || normalized.includes('authorization')
    || normalized.includes('cookie')
    || normalized.includes('passkey')
}

function isTauriRuntime(): boolean {
  const root = globalThis as {
    readonly __TAURI_INTERNALS__?: unknown
    readonly window?: { readonly __TAURI_INTERNALS__?: unknown }
  }
  return root.__TAURI_INTERNALS__ != null || root.window?.__TAURI_INTERNALS__ != null
}

function browserStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null
  }
  catch {
    return null
  }
}
