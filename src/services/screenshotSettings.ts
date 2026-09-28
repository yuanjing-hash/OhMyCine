import { invoke } from '@tauri-apps/api/core'
import { open } from '@tauri-apps/plugin-dialog'
import { isPermissionGranted, requestPermission, sendNotification } from '@tauri-apps/plugin-notification'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { isNativeAndroidRuntime, isTauriRuntime } from '@/services/runtimePlatform'

export type ScreenshotFormat = 'png' | 'jpg'

export interface ScreenshotSettings {
  directory: string
  format: ScreenshotFormat
}

export interface ScreenshotResult {
  name: string
  path: string
}

const SETTINGS_KEY = 'ohmycine-screenshot-settings-v1'

export function loadScreenshotSettings(): ScreenshotSettings {
  try {
    const saved = JSON.parse(getAppSetting(SETTINGS_KEY) ?? '') as Partial<ScreenshotSettings>
    return {
      directory: typeof saved.directory === 'string' ? saved.directory : '',
      format: saved.format === 'jpg' ? 'jpg' : 'png',
    }
  }
  catch {
    return { directory: '', format: 'png' }
  }
}

export async function saveScreenshotSettings(settings: ScreenshotSettings): Promise<void> {
  if (settings.directory)
    await invoke('player_screenshot_directory_label', { directory: settings.directory })
  await setAppSetting(SETTINGS_KEY, JSON.stringify(settings))
}

export async function screenshotDirectoryLabel(directory: string): Promise<string> {
  if (!isTauriRuntime())
    return directory || '程序旁的“截图”目录'
  return invoke<string>('player_screenshot_directory_label', { directory })
}

export async function pickScreenshotDirectory(): Promise<string | null> {
  if (isNativeAndroidRuntime())
    return invoke<string | null>('player_download_pick_directory', { persistent: false })
  const selected = await open({ directory: true, multiple: false, title: '选择截图保存目录' })
  return typeof selected === 'string' ? selected : null
}

export async function captureVideoFrame(title: string, seasonNumber?: number, episodeNumber?: number): Promise<ScreenshotResult> {
  if (!isTauriRuntime())
    throw new Error('内置截图仅支持已安装的 Player。')
  const settings = loadScreenshotSettings()
  return invoke<ScreenshotResult>('mpv_capture_screenshot', {
    title,
    seasonNumber,
    episodeNumber,
    directory: settings.directory || null,
    format: settings.format,
  })
}

export async function notifyScreenshotSaved(result: ScreenshotResult): Promise<boolean> {
  try {
    let allowed = await isPermissionGranted()
    if (!allowed)
      allowed = await requestPermission() === 'granted'
    if (!allowed)
      return false
    sendNotification({ title: '截图已保存', body: result.name })
    return true
  }
  catch {
    return false
  }
}
