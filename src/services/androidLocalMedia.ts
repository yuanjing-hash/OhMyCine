import { invoke } from '@tauri-apps/api/core'

export interface AndroidPickedLocalMedia {
  readonly cancelled: boolean
  readonly uri?: string
  readonly name?: string
  readonly size?: number
  readonly modifiedMs?: number
}

export async function pickAndroidLocalVideo(): Promise<AndroidPickedLocalMedia> {
  return invoke<AndroidPickedLocalMedia>('local_file_pick_video')
}

export async function pickAndroidLocalDirectory(): Promise<AndroidPickedLocalMedia> {
  return invoke<AndroidPickedLocalMedia>('local_file_pick_directory')
}
