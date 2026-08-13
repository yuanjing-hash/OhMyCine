import type { MediaActionId, MediaItemActionTarget } from './types'
import { readonly, shallowRef } from 'vue'

export type MediaEditorKind = Extract<MediaActionId, 'editMetadata' | 'editArtwork' | 'editSubtitles'>

export interface MediaEditorRequest {
  readonly target: MediaItemActionTarget
  readonly kind: MediaEditorKind
}

const request = shallowRef<MediaEditorRequest | null>(null)

export function openMediaEditor(target: MediaItemActionTarget, kind: MediaEditorKind): void {
  request.value = { target, kind }
}

export function closeMediaEditor(): void {
  request.value = null
}

export function useMediaEditorRuntime() {
  return { request: readonly(request) }
}
