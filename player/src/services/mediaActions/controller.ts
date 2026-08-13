import type {
  MediaActionAdapter,
  MediaActionCapability,
  MediaActionConfirmation,
  MediaActionConfirmationResult,
  MediaActionExecutionOutcome,
  MediaActionFeedback,
  MediaActionId,
  MediaActionInvalidation,
  MediaActionTarget,
  ResolvedMediaAction,
} from './types'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { getMediaActionDefinition } from './catalog'
import { MEDIA_ACTION_IDS, mediaActionTargetKey } from './types'

export interface MediaActionControllerOptions {
  readonly adapters?: readonly MediaActionAdapter[]
  readonly confirm?: (confirmation: MediaActionConfirmation) => MediaActionConfirmationResult | Promise<MediaActionConfirmationResult>
  readonly invalidate?: (invalidation: MediaActionInvalidation) => void | Promise<void>
  readonly onFeedback?: (feedback: MediaActionFeedback) => void
}

interface AdapterCapability extends MediaActionCapability {
  readonly adapter: MediaActionAdapter
}

export class MediaActionController {
  private readonly adapters = new Map<string, MediaActionAdapter>()
  private readonly inFlight = new Map<string, Promise<MediaActionExecutionOutcome>>()
  private feedbackId = 0

  constructor(private readonly options: MediaActionControllerOptions = {}) {
    for (const adapter of options.adapters ?? [])
      this.registerAdapter(adapter)
  }

  registerAdapter(adapter: MediaActionAdapter): () => void {
    if (this.adapters.has(adapter.id))
      throw new Error(`Media action adapter already registered: ${adapter.id}`)
    this.adapters.set(adapter.id, adapter)
    return () => this.adapters.delete(adapter.id)
  }

  async resolve(target: MediaActionTarget): Promise<ResolvedMediaAction[]> {
    const capabilities = await this.resolveCapabilities(target)
    return MEDIA_ACTION_IDS.map((action) => {
      const capability = capabilities.get(action)
      const definition = getMediaActionDefinition(action)
      return {
        action,
        label: definition.label,
        description: definition.description,
        group: definition.group,
        order: definition.order,
        availability: capability?.availability ?? 'hidden',
        disabledReason: capability?.disabledReason,
        danger: capability?.danger ?? 'none',
        confirmation: capability?.confirmation,
        busy: this.inFlight.has(this.executionKey(target, action)),
      }
    }).filter(action => action.availability !== 'hidden').sort((left, right) => left.order - right.order)
  }

  execute(target: MediaActionTarget, action: MediaActionId): Promise<MediaActionExecutionOutcome> {
    const key = this.executionKey(target, action)
    const current = this.inFlight.get(key)
    if (current)
      return current

    const execution = this.executeOnce(target, action).finally(() => this.inFlight.delete(key))
    this.inFlight.set(key, execution)
    return execution
  }

  isBusy(target: MediaActionTarget, action: MediaActionId): boolean {
    return this.inFlight.has(this.executionKey(target, action))
  }

  private async executeOnce(target: MediaActionTarget, action: MediaActionId): Promise<MediaActionExecutionOutcome> {
    try {
      const capability = (await this.resolveCapabilities(target)).get(action)
      if (!capability || capability.availability !== 'available')
        return { status: 'ignored', message: capability?.disabledReason }

      let confirmation: MediaActionConfirmationResult | undefined
      if (capability.confirmation) {
        confirmation = await this.options.confirm?.(capability.confirmation) ?? { confirmed: false, deleteSourceFiles: false }
        if (!confirmation.confirmed)
          return { status: 'cancelled' }
      }

      const result = await capability.adapter.execute(target, action, confirmation)
      for (const invalidation of result?.invalidations ?? [])
        await this.options.invalidate?.(invalidation)
      const message = result?.message ?? `${getMediaActionDefinition(action).label}完成`
      this.feedback(result?.feedbackKind ?? 'success', message)
      return { status: 'completed', message }
    }
    catch (error) {
      const message = toSafeErrorMessage(error, `${getMediaActionDefinition(action).label}失败，请稍后重试。`)
      this.feedback('error', message)
      return { status: 'failed', message }
    }
  }

  private async resolveCapabilities(target: MediaActionTarget): Promise<Map<MediaActionId, AdapterCapability>> {
    const adapters = [...this.adapters.values()]
      .filter(adapter => adapter.supports(target))
      .sort((left, right) => (right.priority ?? 0) - (left.priority ?? 0))
    const resolved = new Map<MediaActionId, AdapterCapability>()

    for (const adapter of adapters) {
      const capabilities = await adapter.resolve(target)
      for (const capability of capabilities) {
        if (resolved.has(capability.action))
          continue
        if (capability.availability === 'disabled' && !capability.disabledReason)
          throw new Error(`Disabled media action requires a reason: ${capability.action}`)
        resolved.set(capability.action, { ...capability, adapter })
      }
    }
    return resolved
  }

  private executionKey(target: MediaActionTarget, action: MediaActionId): string {
    return `${mediaActionTargetKey(target)}:${action}`
  }

  private feedback(kind: MediaActionFeedback['kind'], message: string) {
    this.options.onFeedback?.({ id: ++this.feedbackId, kind, message })
  }
}
