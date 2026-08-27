import type { MediaActionAdapter, MediaActionCapability, MediaActionConfirmation, MediaActionConfirmationResult, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import type { DataSource } from '@/services/datasource/types'
import { open } from '@tauri-apps/plugin-dialog'
import { planMediaDownload, summarizeDownloadPlan } from '@/services/downloadPlanning'
import { enqueueDownload, enqueueDownloadGroup, pickAndroidDownloadDirectory } from '@/services/downloads'
import { isNativeAndroidRuntime, isTauriRuntime } from '@/services/runtimePlatform'

const SUPPORTED_TYPES = new Set(['local', 'alist', 'clouddrive2', 'webdav', '123', 'quark', 'emby', 'jellyfin', 'server'])

export function createDownloadMediaActionAdapter(
  resolveSource: (id: string) => DataSource | null,
  confirm: (confirmation: MediaActionConfirmation) => Promise<MediaActionConfirmationResult>,
): MediaActionAdapter {
  return {
    id: 'downloads',
    priority: 85,
    supports: target => target.kind === 'media',
    resolve: target => capabilities(target),
    execute: (target, action) => execute(resolveSource, confirm, target, action),
  }
}

function capabilities(target: MediaActionTarget): MediaActionCapability[] {
  if (target.kind !== 'media' || !target.sourceType)
    return []
  if (!SUPPORTED_TYPES.has(target.sourceType))
    return []
  if (target.sourceType === 'server' && target.itemId.startsWith('online-')) {
    const reason = '该在线插件尚未提供可安全续传的离线文件流。'
    return [{ action: 'download', availability: 'disabled', disabledReason: reason }, { action: 'downloadTo', availability: 'disabled', disabledReason: reason }]
  }
  if (isNativeAndroidRuntime()) {
    return [{ action: 'download', availability: 'available' }, { action: 'downloadTo', availability: 'available' }]
  }
  if (!isTauriRuntime()) {
    const reason = '下载队列仅在桌面应用中可用。'
    return [{ action: 'download', availability: 'disabled', disabledReason: reason }, { action: 'downloadTo', availability: 'disabled', disabledReason: reason }]
  }
  return [{ action: 'download', availability: 'available' }, { action: 'downloadTo', availability: 'available' }]
}

async function execute(
  resolveSource: (id: string) => DataSource | null,
  confirm: (confirmation: MediaActionConfirmation) => Promise<MediaActionConfirmationResult>,
  target: MediaActionTarget,
  action: MediaActionId,
): Promise<MediaActionExecutionResult> {
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
  const confirmation = await confirm({
    title: '确认下载',
    message: downloadConfirmation(plan),
    confirmLabel: '确认下载',
    cancelLabel: '取消',
    danger: 'caution',
  })
  if (!confirmation.confirmed) {
    return { message: '已取消下载' }
  }
  if (plan.aggregate) {
    await enqueueDownloadGroup(plan.files, plan.displayName, directory)
  }
  else {
    await enqueueDownload(plan.files[0], {
      destinationDirectory: directory,
      mediaSourceId: plan.files[0].mediaSourceId,
      variantId: plan.files[0].variantId,
      libraryId: plan.files[0].libraryId,
      expectedBytes: plan.files[0].expectedBytes,
      detailSnapshot: plan.files[0].detailSnapshot,
    })
  }
  const operation = target.sourceType === 'local' ? '复制' : '下载'
  return { message: plan.aggregate ? `已将 ${plan.files.length} 个文件加入${operation}队列` : `已加入${operation}队列` }
}

function downloadConfirmation(plan: Awaited<ReturnType<typeof planMediaDownload>>): string {
  const summary = summarizeDownloadPlan(plan)
  const size = summary.knownBytes > 0 ? formatBytes(summary.knownBytes) : '未知'
  const unknown = summary.unknownSizeFiles > 0 ? `，其中 ${summary.unknownSizeFiles} 个文件大小未知` : ''
  const selection = summary.usesExplicitSelection
    ? '将使用当前选定或来源首选的媒体版本/静态清晰度，不会自动下载同一媒体的全部版本。'
    : '来源未提供可预选的版本信息，将下载其原始文件。'
  return `确认下载“${plan.displayName}”？\n\n文件：${summary.fileCount} 个\n预计大小：${size}${unknown}\n\n${selection}`
}

function formatBytes(value: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024)))
  return `${(value / 1024 ** index).toFixed(index > 0 ? 1 : 0)} ${units[index]}`
}
