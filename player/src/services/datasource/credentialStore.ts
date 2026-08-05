import { invoke } from '@tauri-apps/api/core'
import { getAppSetting, removeAppSetting, setAppSetting } from '@/services/appSettings'

const PERSISTENT_UNAVAILABLE_KEY = 'ohmycine:persistent-credentials-unavailable'
const memoryCredentials = new Map<string, string>()

export interface EmbyCredentialValue {
  readonly accessToken: string
  readonly username: string
  readonly password: string
}

export interface AlistCredentialValue {
  readonly token: string
  readonly username: string
  readonly password: string
}

export interface CloudDrive2CredentialValue {
  readonly apiToken: string
}

export interface WebDavCredentialValue {
  readonly username: string
  readonly password: string
}

export interface QuarkCredentialValue {
  readonly cookie: string
}

export interface TmdbCredentialValue {
  readonly authType: 'apiKey' | 'readAccessToken'
  readonly value: string
}

export type OpenSubtitlesAuthMode = 'apiKey' | 'account'

export interface OpenSubtitlesCredentialValue {
  readonly authMode: OpenSubtitlesAuthMode
  readonly apiKey?: string
  readonly username?: string
  readonly password?: string
}

type CredentialProvider = 'emby' | 'alist' | 'clouddrive2' | 'webdav' | 'quark' | 'tmdb' | 'opensubtitles'

interface StoredEmbyCredentialEnvelope {
  readonly version: 1
  readonly provider: 'emby'
  readonly accessToken: string
  readonly username: string
  readonly password: string
}

interface StoredAlistCredentialEnvelope {
  readonly version: 1
  readonly provider: 'alist'
  readonly token: string
  readonly username: string
  readonly password: string
}

interface StoredCloudDrive2CredentialEnvelope {
  readonly version: 2
  readonly provider: 'clouddrive2'
  readonly apiToken: string
}

interface StoredWebDavCredentialEnvelope {
  readonly version: 1
  readonly provider: 'webdav'
  readonly username: string
  readonly password: string
}

interface StoredQuarkCredentialEnvelope {
  readonly version: 1
  readonly provider: 'quark'
  readonly cookie: string
}

interface StoredTmdbCredentialEnvelope {
  readonly version: 1
  readonly provider: 'tmdb'
  readonly authType: 'apiKey' | 'readAccessToken'
  readonly value: string
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

export async function saveEmbyCredential(ref: string, value: EmbyCredentialValue): Promise<void> {
  if (!value.accessToken || !value.username || !value.password)
    throw new Error('Credential value is incomplete.')

  await saveRawCredential(ref, JSON.stringify({
    version: 1,
    provider: 'emby',
    accessToken: value.accessToken,
    username: value.username,
    password: value.password,
  } satisfies StoredEmbyCredentialEnvelope))
}

export async function readEmbyCredential(ref: string): Promise<EmbyCredentialValue | null> {
  return parseEmbyCredential(await readRawCredential(ref))
}

export async function saveAlistCredential(ref: string, value: AlistCredentialValue): Promise<void> {
  if (!value.token || !value.username || !value.password)
    throw new Error('Credential value is incomplete.')

  await saveRawCredential(ref, JSON.stringify({
    version: 1,
    provider: 'alist',
    token: value.token,
    username: value.username,
    password: value.password,
  } satisfies StoredAlistCredentialEnvelope))
}

export async function readAlistCredential(ref: string): Promise<AlistCredentialValue | null> {
  return parseAlistCredential(await readRawCredential(ref))
}

export async function saveCloudDrive2Credential(ref: string, value: CloudDrive2CredentialValue): Promise<void> {
  if (!value.apiToken.trim())
    throw new Error('Credential value is incomplete.')

  await saveRawCredential(ref, JSON.stringify({
    version: 2,
    provider: 'clouddrive2',
    apiToken: value.apiToken.trim(),
  } satisfies StoredCloudDrive2CredentialEnvelope))
}

export async function readCloudDrive2Credential(ref: string): Promise<CloudDrive2CredentialValue | null> {
  return parseCloudDrive2Credential(await readRawCredential(ref))
}

export async function saveWebDavCredential(ref: string, value: WebDavCredentialValue): Promise<void> {
  if (!value.username || !value.password)
    throw new Error('Credential value is incomplete.')

  await saveRawCredential(ref, JSON.stringify({
    version: 1,
    provider: 'webdav',
    username: value.username,
    password: value.password,
  } satisfies StoredWebDavCredentialEnvelope))
}

export async function readWebDavCredential(ref: string): Promise<WebDavCredentialValue | null> {
  return parseWebDavCredential(await readRawCredential(ref))
}

export async function saveQuarkCredential(ref: string, value: QuarkCredentialValue): Promise<void> {
  const cookie = value.cookie.trim()
  if (!cookie)
    throw new Error('Credential value is incomplete.')

  await saveRawCredential(ref, JSON.stringify({
    version: 1,
    provider: 'quark',
    cookie,
  } satisfies StoredQuarkCredentialEnvelope))
}

export async function readQuarkCredential(ref: string): Promise<QuarkCredentialValue | null> {
  return parseQuarkCredential(await readRawCredential(ref))
}

export async function saveTmdbCredential(ref: string, value: TmdbCredentialValue): Promise<void> {
  if (!isTmdbAuthType(value.authType) || !value.value.trim())
    throw new Error('Credential value is incomplete.')

  await saveRawCredential(ref, JSON.stringify({
    version: 1,
    provider: 'tmdb',
    authType: value.authType,
    value: value.value.trim(),
  } satisfies StoredTmdbCredentialEnvelope))
}

export async function readTmdbCredential(ref: string): Promise<TmdbCredentialValue | null> {
  return parseTmdbCredential(await readRawCredential(ref))
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
    if (value.provider !== 'emby' || value.version !== 1)
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

function parseAlistCredential(raw: string | null): AlistCredentialValue | null {
  if (!raw)
    return null

  try {
    const value = JSON.parse(raw) as unknown
    if (!isObject(value))
      return null
    if (value.provider !== 'alist' || value.version !== 1)
      return null
    if (typeof value.token !== 'string' || typeof value.username !== 'string' || typeof value.password !== 'string')
      return null
    if (!value.token || !value.username || !value.password)
      return null
    return {
      token: value.token,
      username: value.username,
      password: value.password,
    }
  }
  catch {
    return null
  }
}

function parseCloudDrive2Credential(raw: string | null): CloudDrive2CredentialValue | null {
  if (!raw)
    return null

  try {
    const value = JSON.parse(raw) as unknown
    if (!isObject(value))
      return null
    if (value.provider !== 'clouddrive2' || value.version !== 2)
      return null
    if (typeof value.apiToken !== 'string' || !value.apiToken.trim())
      return null
    return {
      apiToken: value.apiToken.trim(),
    }
  }
  catch {
    return null
  }
}

function parseWebDavCredential(raw: string | null): WebDavCredentialValue | null {
  if (!raw)
    return null

  try {
    const value = JSON.parse(raw) as unknown
    if (!isObject(value))
      return null
    if (value.provider !== 'webdav' || value.version !== 1)
      return null
    if (typeof value.username !== 'string' || typeof value.password !== 'string')
      return null
    if (!value.username || !value.password)
      return null
    return {
      username: value.username,
      password: value.password,
    }
  }
  catch {
    return null
  }
}

function parseQuarkCredential(raw: string | null): QuarkCredentialValue | null {
  if (!raw)
    return null

  try {
    const value = JSON.parse(raw) as unknown
    if (!isObject(value) || value.provider !== 'quark' || value.version !== 1)
      return null
    if (typeof value.cookie !== 'string' || !value.cookie.trim())
      return null
    return { cookie: value.cookie.trim() }
  }
  catch {
    return null
  }
}

function parseTmdbCredential(raw: string | null): TmdbCredentialValue | null {
  if (!raw)
    return null

  try {
    const value = JSON.parse(raw) as unknown
    if (!isObject(value))
      return null
    if (value.provider !== 'tmdb' || value.version !== 1)
      return null
    if (!isTmdbAuthType(value.authType) || typeof value.value !== 'string')
      return null
    if (!value.value.trim())
      return null
    return {
      authType: value.authType,
      value: value.value.trim(),
    }
  }
  catch {
    return null
  }
}

function isTmdbAuthType(value: unknown): value is TmdbCredentialValue['authType'] {
  return value === 'apiKey' || value === 'readAccessToken'
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
