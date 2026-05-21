import { invoke } from '@tauri-apps/api/core'

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

type CredentialProvider = 'emby' | 'alist'

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
  return localStorage.getItem(PERSISTENT_UNAVAILABLE_KEY) === 'true'
}

async function saveRawCredential(ref: string, value: string): Promise<void> {
  if (!ref || !value)
    throw new Error('Credential reference or value is empty.')

  try {
    await invoke('credential_set', { refName: ref, token: value })
    memoryCredentials.delete(ref)
    localStorage.removeItem(PERSISTENT_UNAVAILABLE_KEY)
  }
  catch {
    localStorage.setItem(PERSISTENT_UNAVAILABLE_KEY, 'true')
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

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
