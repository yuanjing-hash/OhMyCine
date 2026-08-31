import type { DataSourceType, MediaItem, MediaLibrary } from '@/services/datasource/types'

export const MEDIA_ACTION_IDS = [
  'play',
  'viewDetails',
  'markPlayed',
  'markUnplayed',
  'removeFromContinueWatching',
  'favorite',
  'unfavorite',
  'addToPlaylist',
  'addToCollection',
  'download',
  'downloadTo',
  'editMetadata',
  'editArtwork',
  'editSubtitles',
  'deleteMedia',
  'identify',
  'refreshMetadata',
  'openLibrary',
  'rescanLibrary',
] as const

export type MediaActionId = typeof MEDIA_ACTION_IDS[number]
export type MediaActionAvailability = 'available' | 'disabled' | 'hidden'
export type MediaActionDanger = 'none' | 'caution' | 'destructive'
export type MediaActionGroup = 'primary' | 'state' | 'organize' | 'download' | 'manage' | 'danger'

export interface MediaActionTargetDisplay {
  readonly name: string
  readonly sourceName?: string
}

export interface MediaItemActionTarget {
  readonly kind: 'media'
  readonly sourceId: string
  readonly sourceType?: DataSourceType
  readonly itemId: string
  /** Stable version selected by the user in the detail/player UI. */
  readonly mediaSourceId?: string
  /** Stable quality variant selected inside mediaSourceId. */
  readonly variantId?: string
  readonly libraryId?: string
  readonly mediaType: MediaItem['type']
  readonly played?: boolean
  readonly favorite?: boolean
  readonly context?: 'continueWatching'
  readonly display: MediaActionTargetDisplay
}

export interface MediaLibraryActionTarget {
  readonly kind: 'library'
  readonly sourceId: string
  readonly sourceType?: DataSourceType
  readonly libraryId: string
  readonly libraryType: MediaLibrary['type']
  readonly display: MediaActionTargetDisplay
}

/** Stable UI identity only. Paths, stream URLs, headers and credentials never belong here. */
export type MediaActionTarget = MediaItemActionTarget | MediaLibraryActionTarget

export interface MediaActionConfirmation {
  readonly title: string
  readonly message: string
  readonly confirmLabel: string
  readonly cancelLabel?: string
  readonly danger: Exclude<MediaActionDanger, 'none'>
  readonly requiredText?: string
  readonly sourceDelete?: {
    readonly label: string
    readonly available: boolean
    readonly disabledReason?: string
    readonly itemCount: number
    readonly pathSummaries: readonly string[]
  }
}

export interface MediaActionConfirmationResult {
  readonly confirmed: boolean
  readonly deleteSourceFiles: boolean
}

export interface MediaActionCapability {
  readonly action: MediaActionId
  readonly availability: MediaActionAvailability
  readonly disabledReason?: string
  readonly danger?: MediaActionDanger
  readonly confirmation?: MediaActionConfirmation
}

export interface ResolvedMediaAction extends MediaActionCapability {
  readonly label: string
  readonly description?: string
  readonly group: MediaActionGroup
  readonly order: number
  readonly busy: boolean
}

export interface MediaActionInvalidation {
  readonly sourceId: string
  readonly itemIds?: readonly string[]
  readonly libraryIds?: readonly string[]
  readonly scopes: readonly ('home' | 'source' | 'detail' | 'search' | 'history' | 'collections')[]
}

export interface MediaActionExecutionResult {
  readonly message?: string
  readonly feedbackKind?: MediaActionFeedback['kind']
  readonly invalidations?: readonly MediaActionInvalidation[]
}

export interface MediaActionAdapter {
  readonly id: string
  readonly priority?: number
  supports: (target: MediaActionTarget) => boolean
  resolve: (target: MediaActionTarget) => readonly MediaActionCapability[] | Promise<readonly MediaActionCapability[]>
  execute: (target: MediaActionTarget, action: MediaActionId, confirmation?: MediaActionConfirmationResult) => MediaActionExecutionResult | void | Promise<MediaActionExecutionResult | void>
}

export interface MediaActionFeedback {
  readonly id: number
  readonly kind: 'success' | 'error'
  readonly message: string
}

export interface MediaActionExecutionOutcome {
  readonly status: 'completed' | 'cancelled' | 'ignored' | 'failed'
  readonly message?: string
}

export function createMediaActionTarget(
  item: MediaItem | MediaLibrary,
  sourceType?: DataSourceType,
  sourceName?: string,
  context?: MediaItemActionTarget['context'],
): MediaActionTarget {
  if ('path' in item) {
    const target: MediaItemActionTarget = {
      kind: 'media',
      sourceId: item.sourceId,
      sourceType,
      itemId: item.id,
      libraryId: item.libraryId,
      mediaType: item.type,
      display: { name: item.name, sourceName },
    }
    if (item.played != null)
      Object.assign(target, { played: item.played })
    if (item.favorite != null)
      Object.assign(target, { favorite: item.favorite })
    if (context)
      Object.assign(target, { context })
    return target
  }

  return {
    kind: 'library',
    sourceId: item.sourceId,
    sourceType,
    libraryId: item.id,
    libraryType: item.type,
    display: { name: item.name, sourceName },
  }
}

export function mediaActionTargetKey(target: MediaActionTarget): string {
  const objectId = target.kind === 'media' ? target.itemId : target.libraryId
  return `${target.sourceId}:${target.kind}:${objectId}`
}
