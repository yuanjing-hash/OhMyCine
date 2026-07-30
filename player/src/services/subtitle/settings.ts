import type { SubtitleLanguage } from './types'
import type { OpenSubtitlesCredentialValue } from '@/services/datasource/credentialStore'
import { invoke } from '@tauri-apps/api/core'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { readOpenSubtitlesCredential, removeCredential, saveOpenSubtitlesCredential } from '@/services/datasource/credentialStore'

const SETTINGS_KEY = 'ohmycine-subtitle-search-settings-v1'
export const OPENSUBTITLES_CREDENTIAL_REF = 'player:subtitle:opensubtitles-api-key'

export interface SubtitleSearchSettings {
  defaultLanguage: SubtitleLanguage
  openSubtitlesEnabled: boolean
  shooterEnabled: boolean
  xunleiEnabled: boolean
}

const DEFAULT_SETTINGS: SubtitleSearchSettings = {
  defaultLanguage: 'zh-CN',
  openSubtitlesEnabled: true,
  shooterEnabled: true,
  xunleiEnabled: false,
}

export function loadSubtitleSearchSettings(): SubtitleSearchSettings {
  const raw = getAppSetting(SETTINGS_KEY)
  if (!raw)
    return { ...DEFAULT_SETTINGS }
  try {
    const value = JSON.parse(raw) as Partial<SubtitleSearchSettings>
    return {
      defaultLanguage: isSubtitleLanguage(value.defaultLanguage) ? value.defaultLanguage : DEFAULT_SETTINGS.defaultLanguage,
      openSubtitlesEnabled: value.openSubtitlesEnabled !== false,
      shooterEnabled: value.shooterEnabled !== false,
      xunleiEnabled: value.xunleiEnabled === true,
    }
  }
  catch {
    return { ...DEFAULT_SETTINGS }
  }
}

export async function saveSubtitleSearchSettings(settings: SubtitleSearchSettings): Promise<void> {
  await setAppSetting(SETTINGS_KEY, JSON.stringify({
    defaultLanguage: isSubtitleLanguage(settings.defaultLanguage) ? settings.defaultLanguage : DEFAULT_SETTINGS.defaultLanguage,
    openSubtitlesEnabled: settings.openSubtitlesEnabled,
    shooterEnabled: settings.shooterEnabled,
    xunleiEnabled: settings.xunleiEnabled,
  }))
}

export async function saveOpenSubtitlesCredentials(value: OpenSubtitlesCredentialValue): Promise<void> {
  await saveOpenSubtitlesCredential(OPENSUBTITLES_CREDENTIAL_REF, value)
}

export async function readOpenSubtitlesCredentials(): Promise<OpenSubtitlesCredentialValue | null> {
  return readOpenSubtitlesCredential(OPENSUBTITLES_CREDENTIAL_REF)
}

export async function clearOpenSubtitlesCredentials(): Promise<void> {
  await removeCredential(OPENSUBTITLES_CREDENTIAL_REF)
}

export interface OpenSubtitlesLoginStatus {
  authenticated: boolean
}

export async function testOpenSubtitlesLogin(value: OpenSubtitlesCredentialValue): Promise<OpenSubtitlesLoginStatus> {
  if (value.authMode !== 'account' || !value.username || !value.password)
    throw new Error('请选择账号密码模式并填写完整凭据。')
  return invoke<OpenSubtitlesLoginStatus>('subtitle_login_opensubtitles', {
    request: {
      authMode: value.authMode,
      username: value.username,
      password: value.password,
    },
  })
}

export function isSubtitleLanguage(value: unknown): value is SubtitleLanguage {
  return value === 'zh-CN' || value === 'zh-TW' || value === 'en' || value === 'ja' || value === 'ko'
}
