import type { MediaActionAdapter, MediaActionCapability, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import { open } from '@tauri-apps/plugin-dialog'
import { enqueueDownload } from '@/services/downloads'
import { isNativeAndroidRuntime, isTauriRuntime } from '@/services/runtimePlatform'

const SUPPORTED_TYPES = new Set(['local', 'alist'])

export function createDownloadMediaActionAdapter(): MediaActionAdapter {
  return {
    id: 'downloads',
    priority: 85,
    supports: target => target.kind === 'media',
    resolve: target => capabilities(target),
    execute,
  }
}

function capabilities(target: MediaActionTarget): MediaActionCapability[] {
  if (target.kind !== 'media' || !target.sourceType || matchesAggregate(target.mediaType))
    return []
  if (!SUPPORTED_TYPES.has(target.sourceType))
    return []
  if (isNativeAndroidRuntime()) {
    const reason = 'Android 下载需要持久可写的 SAF 目录授权和前台服务，当前版本尚未启用。'
    return [{ action: 'download', availability: 'disabled', disabledReason: reason }, { action: 'downloadTo', availability: 'disabled', disabledReason: reason }]
  }
  if (!isTauriRuntime()) {
    const reason = '下载队列仅在桌面应用中可用。'
    return [{ action: 'download', availability: 'disabled', disabledReason: reason }, { action: 'downloadTo', availability: 'disabled', disabledReason: reason }]
  }
  return [{ action: 'download', availability: 'available' }, { action: 'downloadTo', availability: 'available' }]
}

async function execute(target: MediaActionTarget, action: MediaActionId): Promise<MediaActionExecutionResult> {
  if (target.kind !== 'media' || (action !== 'download' && action !== 'downloadTo'))
    throw new Error('此对象不支持下载操作。')
  let directory: string | undefined
  if (action === 'downloadTo') {
    const selected = await open({ directory: true, multiple: false })
    if (typeof selected !== 'string')
      return { message: '已取消选择下载目录' }
    directory = selected
  }
  await enqueueDownload(target, directory)
  const operation = target.sourceType === 'local' ? '复制' : '下载'
  return { message: `已加入${operation}队列` }
}

function matchesAggregate(mediaType: string): boolean {
  return mediaType === 'folder' || mediaType === 'series' || mediaType === 'season'
}
