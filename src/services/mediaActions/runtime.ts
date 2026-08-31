import type { MediaActionConfirmation, MediaActionConfirmationResult, MediaActionFeedback, MediaActionTarget } from './types'
import { readonly, shallowRef } from 'vue'
import { MediaActionController } from './controller'

export interface MediaActionMenuAnchor {
  readonly x: number
  readonly y: number
}

export type MediaActionMenuPresentation = 'auto' | 'popover' | 'sheet'

export interface MediaActionMenuRequest {
  readonly target: MediaActionTarget
  readonly anchor?: MediaActionMenuAnchor
  readonly presentation?: MediaActionMenuPresentation
}

interface PendingConfirmation {
  readonly confirmation: MediaActionConfirmation
  readonly resolve: (result: MediaActionConfirmationResult) => void
}

const menuRequest = shallowRef<MediaActionMenuRequest | null>(null)
const pendingConfirmation = shallowRef<PendingConfirmation | null>(null)
const feedback = shallowRef<MediaActionFeedback | null>(null)
let controller = new MediaActionController({ confirm: requestMediaActionConfirmation, onFeedback: publishFeedback })

export function configureMediaActionController(nextController: MediaActionController) {
  controller = nextController
}

export function getMediaActionController(): MediaActionController {
  return controller
}

export function openMediaActionMenu(request: MediaActionMenuRequest) {
  menuRequest.value = request
}

export function closeMediaActionMenu() {
  menuRequest.value = null
}

export function useMediaActionRuntime() {
  return {
    menuRequest: readonly(menuRequest),
    pendingConfirmation: readonly(pendingConfirmation),
    feedback: readonly(feedback),
  }
}

export function requestMediaActionConfirmation(confirmation: MediaActionConfirmation): Promise<MediaActionConfirmationResult> {
  pendingConfirmation.value?.resolve({ confirmed: false, deleteSourceFiles: false })
  return new Promise<MediaActionConfirmationResult>((resolve) => {
    pendingConfirmation.value = { confirmation, resolve }
  })
}

export function resolveMediaActionConfirmation(result: MediaActionConfirmationResult) {
  const pending = pendingConfirmation.value
  if (!pending)
    return
  pendingConfirmation.value = null
  pending.resolve(result)
}

export function publishFeedback(nextFeedback: MediaActionFeedback) {
  feedback.value = nextFeedback
}
