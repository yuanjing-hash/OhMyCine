import type { SubtitleLanguage } from './types'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { readOpenSubtitlesCredential, removeCredential, saveOpenSubtitlesCredential } from '@/services/datasource/credentialStore'

const SETTINGS_KEY = 'ohmycine-subtitle-search-settings-v1'
export const OPENSUBTITLES_CREDENTIAL_REF = 'player:subtitle:opensubtitles-api-key'

export interface SubtitleSearchSettings {
  defaultLanguage: SubtitleLanguage
  openSubtitlesEnabled: boolean
}

const DEFAULT_SETTINGS: SubtitleSearchSettings = {
  defaultLanguage: 'zh-CN',
  openSubtitlesEnabled: true,
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
  }))
}

export async function saveOpenSubtitlesApiKey(apiKey: string): Promise<void> {
  await saveOpenSubtitlesCredential(OPENSUBTITLES_CREDENTIAL_REF, { apiKey })
}

export async function readOpenSubtitlesApiKey(): Promise<string | null> {
  return (await readOpenSubtitlesCredential(OPENSUBTITLES_CREDENTIAL_REF))?.apiKey ?? null
}

export async function clearOpenSubtitlesApiKey(): Promise<void> {
  await removeCredential(OPENSUBTITLES_CREDENTIAL_REF)
}

export function isSubtitleLanguage(value: unknown): value is SubtitleLanguage {
  return value === 'zh-CN' || value === 'zh-TW' || value === 'en' || value === 'ja' || value === 'ko'
}
