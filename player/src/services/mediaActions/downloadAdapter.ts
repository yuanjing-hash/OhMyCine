import type { MediaActionAdapter, MediaActionCapability, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import type { DataSource } from '@/services/datasource/types'
import { open } from '@tauri-apps/plugin-dialog'
import { planMediaDownload } from '@/services/downloadPlanning'
import { enqueueDownload, enqueueDownloadGroup, pickAndroidDownloadDirectory } from '@/services/downloads'
import { isNativeAndroidRuntime, isTauriRuntime } from '@/services/runtimePlatform'

const SUPPORTED_TYPES = new Set(['local', 'alist', 'clouddrive2', 'webdav', '123', 'quark', 'emby', 'jellyfin'])

export function createDownloadMediaActionAdapter(resolveSource: (id: string) => DataSource | null): MediaActionAdapter {
  return {
    id: 'downloads',
    priority: 85,
    supports: target => target.kind === 'media',
    resolve: target => capabilities(target),
    execute: (target, action) => execute(resolveSource, target, action),
  }
}

function capabilities(target: MediaActionTarget): MediaActionCapability[] {
  if (target.kind !== 'media' || !target.sourceType)
    return []
  if (!SUPPORTED_TYPES.has(target.sourceType))
    return []
  if (isNativeAndroidRuntime()) {
    return [{ action: 'download', availability: 'available' }, { action: 'downloadTo', availability: 'available' }]
  }
  if (!isTauriRuntime()) {
    const reason = '下载队列仅在桌面应用中可用。'
    return [{ action: 'download', availability: 'disabled', disabledReason: reason }, { action: 'downloadTo', availability: 'disabled', disabledReason: reason }]
  }
  return [{ action: 'download', availability: 'available' }, { action: 'downloadTo', availability: 'available' }]
}

async function execute(resolveSource: (id: string) => DataSource | null, target: MediaActionTarget, action: MediaActionId): Promise<MediaActionExecutionResult> {
  if (target.kind !== 'media' || (action !== 'download' && action !== 'downloadTo'))
    throw new Error('此对象不支持下载操作。')
  let directory: string | undefined
  if (action === 'downloadTo') {
    const selected = isNativeAndroidRuntime()
      ? await pickAndroidDownloadDirectory(false)
      : await open({ directory: true, multiple: false })
    if (typeof selected !== 'string')
      return { message: '已取消选择下载目录' }
    directory = selected
  }
  const source = resolveSource(target.sourceId)
  if (!source)
    throw new Error('媒体来源不可用，请刷新后重试。')
  const plan = await planMediaDownload(source, target)
  if (plan.aggregate)
    await enqueueDownloadGroup(plan.files, plan.displayName, directory)
  else
    await enqueueDownload(plan.files[0], { destinationDirectory: directory, mediaSourceId: plan.files[0].mediaSourceId, expectedBytes: plan.files[0].expectedBytes })
  const operation = target.sourceType === 'local' ? '复制' : '下载'
  return { message: plan.aggregate ? `已将 ${plan.files.length} 个文件加入${operation}队列` : `已加入${operation}队列` }
}
