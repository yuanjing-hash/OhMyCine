import { invoke } from '@tauri-apps/api/core'

export interface AndroidPickedLocalMedia {
  readonly cancelled: boolean
  readonly uri?: string
  readonly name?: string
  readonly size?: number
  readonly modifiedMs?: number
}

export interface AndroidSelectedLocalMedia {
  readonly uri: string
  readonly name?: string
  readonly size?: number
  readonly modifiedMs?: number
}

export interface AndroidPickedLocalMediaSelection {
  readonly cancelled: boolean
  readonly items: readonly AndroidSelectedLocalMedia[]
}

export async function pickAndroidLocalVideos(): Promise<AndroidPickedLocalMediaSelection> {
  return invoke<AndroidPickedLocalMediaSelection>('local_file_pick_videos')
}

export async function pickAndroidLocalVideo(): Promise<AndroidPickedLocalMedia> {
  return invoke<AndroidPickedLocalMedia>('local_file_pick_video')
}

export async function pickAndroidLocalDirectory(): Promise<AndroidPickedLocalMedia> {
  return invoke<AndroidPickedLocalMedia>('local_file_pick_directory')
}
