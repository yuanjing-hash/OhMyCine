import { getVersion } from '@tauri-apps/api/app'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'
import { getAppSetting, setAppSetting } from '@/services/appSettings'

const SETTINGS_KEY = 'ohmycine-updater-settings-v1'

export type UpdateChannel = 'beta' | 'stable'
export type UpdateDownloadStatus = 'idle' | 'available' | 'downloading' | 'paused' | 'verifying' | 'ready' | 'installing' | 'error'
export type UpdateStatus = UpdateDownloadStatus | 'checking' | 'latest'

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
  platform: 'desktop' | 'android'
}

export interface UpdateDownloadState {
  update: UpdateCheckResult | null
  status: UpdateDownloadStatus
  downloadedBytes: number
  contentLength: number | null
  error: string | null
}

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

export async function readPlayerUpdateState(): Promise<UpdateDownloadState> {
  return invoke<UpdateDownloadState>('player_update_state')
}

export async function listenPlayerUpdateState(onState: (state: UpdateDownloadState) => void): Promise<() => void> {
  return listen<UpdateDownloadState>('updater:state', event => onState(event.payload))
}

export async function downloadPlayerUpdate(): Promise<UpdateDownloadState> {
  return invoke<UpdateDownloadState>('player_download_update')
}

export async function installPlayerUpdate(): Promise<void> {
  await invoke('player_install_update')
}

export async function currentPlayerVersion(): Promise<string> {
  try {
    return await getVersion()
  }
  catch {
    return '开发构建'
  }
}
