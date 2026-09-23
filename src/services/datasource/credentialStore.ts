import { invoke } from '@tauri-apps/api/core'
import { getAppSetting, removeAppSetting, setAppSetting } from '@/services/appSettings'

const PERSISTENT_UNAVAILABLE_KEY = 'ohmycine:persistent-credentials-unavailable'
const memoryCredentials = new Map<string, string>()

export interface EmbyCredentialValue {
  readonly accessToken: string
  readonly username: string
  readonly password: string
}

export interface ServerCredentialValue {
  readonly accessToken: string
}

export type OpenSubtitlesAuthMode = 'apiKey' | 'account'

export interface OpenSubtitlesCredentialValue {
  readonly authMode: OpenSubtitlesAuthMode
  readonly apiKey?: string
  readonly username?: string
  readonly password?: string
}

type CredentialProvider = 'emby' | 'jellyfin' | 'server' | 'opensubtitles'

interface StoredEmbyCredentialEnvelope {
  readonly version: 1
  readonly provider: 'emby' | 'jellyfin'
  readonly accessToken: string
  readonly username: string
  readonly password: string
}

interface StoredServerCredentialEnvelope {
  readonly version: 1
  readonly provider: 'server'
  readonly accessToken: string
}

interface StoredOpenSubtitlesCredentialEnvelope {
  readonly version: 3
  readonly provider: 'opensubtitles'
  readonly authMode: OpenSubtitlesAuthMode
  readonly apiKey?: string
  readonly username?: string
  readonly password?: string
}

export function createCredentialRef(sourceId: string, provider: CredentialProvider = 'emby'): string {
  return `datasource:${sourceId}:${provider}-credential`
}

export async function saveCredential(ref: string, token: string): Promise<void> {
  await saveRawCredential(ref, token)
}

export async function saveRawCredentialBackup(ref: string, value: string): Promise<void> {
  await saveRawCredential(ref, value)
}

export async function readRawCredentialBackup(ref: string): Promise<string | null> {
  return readRawCredential(ref)
}

export async function readCredential(ref: string): Promise<string | null> {
  const raw = await readRawCredential(ref)
  const parsed = parseEmbyCredential(raw)
  return parsed?.accessToken ?? raw
}

export async function saveEmbyCredential(ref: string, value: EmbyCredentialValue, provider: 'emby' | 'jellyfin' = 'emby'): Promise<void> {
  if (!value.accessToken || !value.username || !value.password)
    throw new Error('Credential value is incomplete.')

  await saveRawCredential(ref, JSON.stringify({
    version: 1,
    provider,
    accessToken: value.accessToken,
    username: value.username,
    password: value.password,
  } satisfies StoredEmbyCredentialEnvelope))
}

export async function readEmbyCredential(ref: string): Promise<EmbyCredentialValue | null> {
  return parseEmbyCredential(await readRawCredential(ref))
}

export async function saveServerCredential(ref: string, value: ServerCredentialValue): Promise<void> {
  const accessToken = value.accessToken.trim()
  if (!accessToken.startsWith('omc_player_'))
    throw new Error('Server credential value is invalid.')
  await saveRawCredential(ref, JSON.stringify({
    version: 1,
    provider: 'server',
    accessToken,
  } satisfies StoredServerCredentialEnvelope))
}

export async function readServerCredential(ref: string): Promise<ServerCredentialValue | null> {
  return parseServerCredential(await readRawCredential(ref))
}

export async function saveOpenSubtitlesCredential(ref: string, value: OpenSubtitlesCredentialValue): Promise<void> {
  const apiKey = value.apiKey?.trim() || undefined
  const username = value.username?.trim() || undefined
  const password = value.password || undefined
  if (value.authMode === 'apiKey' && !apiKey)
    throw new Error('OpenSubtitles API Key is incomplete.')
  if (value.authMode === 'account' && (!username || !password))
    throw new Error('OpenSubtitles account credential is incomplete.')

  await saveRawCredential(ref, JSON.stringify({
    version: 3,
    provider: 'opensubtitles',
    authMode: value.authMode,
    apiKey: value.authMode === 'apiKey' ? apiKey : undefined,
    username: value.authMode === 'account' ? username : undefined,
    password: value.authMode === 'account' ? password : undefined,
  } satisfies StoredOpenSubtitlesCredentialEnvelope))
}

export async function readOpenSubtitlesCredential(ref: string): Promise<OpenSubtitlesCredentialValue | null> {
  return parseOpenSubtitlesCredential(await readRawCredential(ref))
}

export async function removeCredential(ref: string): Promise<void> {
  if (!ref)
    return

  try {
    await invoke('credential_delete', { refName: ref })
  }
  finally {
    memoryCredentials.delete(ref)
  }
}

export function hasPersistentCredentialStorageWarning(): boolean {
  return getAppSetting(PERSISTENT_UNAVAILABLE_KEY) === 'true'
}

export async function probePersistentCredentialStorage(): Promise<boolean> {
  try {
    await invoke<string | null>('credential_get', { refName: 'player:credential-health-check' })
    await removeAppSetting(PERSISTENT_UNAVAILABLE_KEY)
    return true
  }
  catch {
    await setAppSetting(PERSISTENT_UNAVAILABLE_KEY, 'true')
    return false
  }
}

async function saveRawCredential(ref: string, value: string): Promise<void> {
  if (!ref || !value)
    throw new Error('Credential reference or value is empty.')

  try {
    await invoke('credential_set', { refName: ref, token: value })
    memoryCredentials.delete(ref)
    void removeAppSetting(PERSISTENT_UNAVAILABLE_KEY)
  }
  catch {
    void setAppSetting(PERSISTENT_UNAVAILABLE_KEY, 'true')
    memoryCredentials.set(ref, value)
  }
}

async function readRawCredential(ref: string): Promise<string | null> {
  if (!ref)
    return null

  try {
    return await invoke<string | null>('credential_get', { refName: ref })
  }
  catch {
    return memoryCredentials.get(ref) ?? null
  }
}

function parseEmbyCredential(raw: string | null): EmbyCredentialValue | null {
  if (!raw)
    return null

  try {
    const value = JSON.parse(raw) as unknown
    if (!isObject(value))
      return null
    if ((value.provider !== 'emby' && value.provider !== 'jellyfin') || value.version !== 1)
      return null
    if (typeof value.accessToken !== 'string' || typeof value.username !== 'string' || typeof value.password !== 'string')
      return null
    if (!value.accessToken || !value.username || !value.password)
      return null
    return {
      accessToken: value.accessToken,
      username: value.username,
      password: value.password,
    }
  }
  catch {
    return null
  }
}

function parseOpenSubtitlesCredential(raw: string | null): OpenSubtitlesCredentialValue | null {
  if (!raw)
    return null

  try {
    const value = JSON.parse(raw) as unknown
    if (!isObject(value))
      return null
    if (value.provider !== 'opensubtitles' || (value.version !== 1 && value.version !== 2 && value.version !== 3))
      return null
    if (value.version === 1) {
      return typeof value.apiKey === 'string' && value.apiKey.trim()
        ? { authMode: 'apiKey', apiKey: value.apiKey.trim() }
        : null
    }

    const apiKey = typeof value.apiKey === 'string' ? value.apiKey.trim() : ''
    const username = typeof value.username === 'string' ? value.username.trim() : ''
    const password = typeof value.password === 'string' ? value.password : ''
    if (value.version === 2) {
      if (username && password)
        return { authMode: 'account', username, password }
      return apiKey ? { authMode: 'apiKey', apiKey } : null
    }
    if (value.authMode === 'apiKey' && apiKey)
      return { authMode: 'apiKey', apiKey }
    if (value.authMode === 'account' && username && password)
      return { authMode: 'account', username, password }
    return null
  }
  catch {
    return null
  }
}

function parseServerCredential(raw: string | null): ServerCredentialValue | null {
  if (!raw)
    return null
  try {
    const value = JSON.parse(raw) as unknown
    if (!isObject(value) || value.provider !== 'server' || value.version !== 1)
      return null
    if (typeof value.accessToken !== 'string' || !value.accessToken.trim().startsWith('omc_player_'))
      return null
    return { accessToken: value.accessToken.trim() }
  }
  catch {
    return null
  }
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
