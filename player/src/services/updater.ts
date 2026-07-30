import { getVersion } from '@tauri-apps/api/app'
import { Channel, invoke } from '@tauri-apps/api/core'
import { getAppSetting, setAppSetting } from '@/services/appSettings'

const SETTINGS_KEY = 'ohmycine-updater-settings-v1'

export type UpdateChannel = 'beta' | 'stable'
export type UpdateStatus = 'idle' | 'checking' | 'latest' | 'available' | 'downloading' | 'installing' | 'error'

export interface UpdaterSettings {
  autoCheck: boolean
  channel: UpdateChannel
}

export interface UpdateCheckResult {
  available: boolean
  currentVersion: string
  version?: string
  date?: string
  body?: string
  channel: UpdateChannel
}

export type UpdateProgressEvent
  = | { event: 'Started', data: { content_length?: number } }
    | { event: 'Progress', data: { chunk_length: number } }
    | { event: 'Finished' }

const DEFAULT_SETTINGS: UpdaterSettings = {
  autoCheck: true,
  channel: 'beta',
}

export function loadUpdaterSettings(): UpdaterSettings {
  const raw = getAppSetting(SETTINGS_KEY)
  if (!raw)
    return { ...DEFAULT_SETTINGS }
  try {
    const value = JSON.parse(raw) as Partial<UpdaterSettings>
    return {
      autoCheck: value.autoCheck !== false,
      channel: value.channel === 'stable' ? 'stable' : 'beta',
    }
  }
  catch {
    return { ...DEFAULT_SETTINGS }
  }
}

export async function saveUpdaterSettings(settings: UpdaterSettings): Promise<void> {
  await setAppSetting(SETTINGS_KEY, JSON.stringify({
    autoCheck: settings.autoCheck,
    channel: settings.channel === 'stable' ? 'stable' : 'beta',
  }))
}

export async function checkPlayerUpdate(channel: UpdateChannel): Promise<UpdateCheckResult> {
  return invoke<UpdateCheckResult>('player_check_for_updates', { channel })
}

export async function installPlayerUpdate(onProgress: (event: UpdateProgressEvent) => void): Promise<void> {
  const onEvent = new Channel<UpdateProgressEvent>()
  onEvent.onmessage = onProgress
  await invoke('player_install_update', { onEvent })
}

export async function currentPlayerVersion(): Promise<string> {
  try {
    return await getVersion()
  }
  catch {
    return '开发构建'
  }
}
