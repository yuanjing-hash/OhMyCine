import type { MediaActionAdapter, MediaActionCapability, MediaActionConfirmationResult, MediaActionExecutionResult, MediaActionId, MediaActionTarget } from './types'
import type { DataSource, DataSourceConfig } from '@/services/datasource/types'
import type { MediaDeletePlan } from '@/services/mediaDelete'
import { executeMediaDelete, resolveMediaDeletePlan } from '@/services/mediaDelete'

export interface DeleteMediaActionAdapterOptions {
  resolveSource: (sourceId: string) => DataSource | null
  resolveConfig: (sourceId: string) => DataSourceConfig | undefined
}

const plans = new Map<string, MediaDeletePlan>()

export function createDeleteMediaActionAdapter(options: DeleteMediaActionAdapterOptions): MediaActionAdapter {
  return {
    id: 'media-delete',
    priority: 100,
    supports: target => target.kind === 'media',
    resolve: target => resolveCapabilities(options, target),
    execute: (target, action, confirmation) => executeDelete(options, target, action, confirmation),
  }
}

async function resolveCapabilities(options: DeleteMediaActionAdapterOptions, target: MediaActionTarget): Promise<MediaActionCapability[]> {
  if (target.kind !== 'media')
    return []
  const source = options.resolveSource(target.sourceId)
  let plan = await resolveMediaDeletePlan(target, options.resolveConfig(target.sourceId))
  if ((target.sourceType === 'emby' || target.sourceType === 'jellyfin') && plan.sourceDeleteAvailable) {
    const permitted = await source?.canDeleteMedia?.(target.itemId).catch(() => false) ?? false
    if (!permitted) {
      plan = {
        ...plan,
        sourceDeleteAvailable: false,
        sourceDeleteDisabledReason: '当前媒体服务用户没有删除此对象的权限。',
      }
    }
  }
  plans.set(planKey(target), plan)
  const typedScope = target.mediaType === 'series' || target.mediaType === 'season'
  return [{
    action: 'deleteMedia',
    availability: 'available',
    danger: 'destructive',
    confirmation: {
      title: '从 Player 媒体库移除',
      message: '默认只隐藏当前媒体库条目，不会修改来源文件。只有主动勾选下方选项才会执行真实来源删除。',
      confirmLabel: '确认移除',
      cancelLabel: '取消',
      danger: 'destructive',
      requiredText: typedScope ? target.display.name : undefined,
      sourceDelete: {
        label: target.sourceType === 'local' ? '同时删除本地源文件' : '同时从媒体服务删除源媒体',
        available: plan.sourceDeleteAvailable && plan.providerItemIds.length > 0,
        disabledReason: plan.providerItemIds.length ? plan.sourceDeleteDisabledReason : '没有可由扫描记录明确归属的源文件。',
        itemCount: plan.providerItemIds.length,
        pathSummaries: plan.pathSummaries,
      },
    },
  }]
}

async function executeDelete(options: DeleteMediaActionAdapterOptions, target: MediaActionTarget, action: MediaActionId, confirmation?: MediaActionConfirmationResult): Promise<MediaActionExecutionResult> {
  if (target.kind !== 'media' || action !== 'deleteMedia' || !confirmation?.confirmed)
    throw new Error('删除确认已失效。')
  const plan = plans.get(planKey(target)) ?? await resolveMediaDeletePlan(target, options.resolveConfig(target.sourceId))
  plans.delete(planKey(target))
  const outcome = await executeMediaDelete(plan, options.resolveSource(target.sourceId), options.resolveConfig(target.sourceId), confirmation.deleteSourceFiles)
  if (outcome.failed.length) {
    const successLabel = outcome.succeeded.length ? `成功 ${outcome.succeeded.length} 项，` : ''
    return {
      message: `${successLabel}失败 ${outcome.failed.length} 项；失败项已保留在索引中。`,
      feedbackKind: 'error',
      invalidations: [{ sourceId: target.sourceId, itemIds: outcome.succeeded, scopes: ['home', 'source', 'detail', 'search', 'history'] }],
    }
  }
  return {
    message: confirmation.deleteSourceFiles ? `已删除 ${outcome.succeeded.length} 个来源文件` : '已从 Player 媒体库移除',
    invalidations: [{ sourceId: target.sourceId, itemIds: [target.itemId], scopes: ['home', 'source', 'detail', 'search', 'history'] }],
  }
}

function planKey(target: MediaActionTarget): string {
  return `${target.sourceId}\0${target.kind === 'media' ? target.itemId : target.libraryId}`
}
