import type { MediaActionTarget } from './types'
import { openMediaActionMenu } from './runtime'

const LONG_PRESS_DELAY_MS = 520
const LONG_PRESS_MOVEMENT_PX = 12
const SYNTHETIC_EVENT_SUPPRESSION_MS = 900

interface LongPressSession {
  readonly pointerId: number
  readonly element: HTMLElement
  readonly target: MediaActionTarget
  readonly startX: number
  readonly startY: number
  timer: number | undefined
  triggered: boolean
}

const sessions = new Map<number, LongPressSession>()
const suppressedElements = new WeakMap<HTMLElement, number>()
let scrollListenerInstalled = false

export function beginMediaActionLongPress(target: MediaActionTarget, event: PointerEvent) {
  if (event.pointerType !== 'touch' || !event.isPrimary || event.button !== 0)
    return
  const element = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  if (!element || isInteractiveMediaActionDescendant(event.target, element))
    return

  cancelMediaActionLongPress(event.pointerId)
  const session: LongPressSession = {
    pointerId: event.pointerId,
    element,
    target,
    startX: event.clientX,
    startY: event.clientY,
    timer: undefined,
    triggered: false,
  }
  session.timer = window.setTimeout(() => triggerLongPress(session), LONG_PRESS_DELAY_MS)
  sessions.set(event.pointerId, session)
  ensureScrollCancellation()
}

export function moveMediaActionLongPress(event: PointerEvent) {
  const session = sessions.get(event.pointerId)
  if (!session || session.triggered)
    return
  if (Math.hypot(event.clientX - session.startX, event.clientY - session.startY) > LONG_PRESS_MOVEMENT_PX)
    cancelMediaActionLongPress(event.pointerId)
}

export function endMediaActionLongPress(event: PointerEvent) {
  const session = sessions.get(event.pointerId)
  if (!session)
    return
  if (session.triggered) {
    event.preventDefault()
    event.stopPropagation()
    suppressSyntheticEvents(session.element)
  }
  clearSession(session)
}

export function cancelMediaActionLongPress(pointerId: number) {
  const session = sessions.get(pointerId)
  if (session)
    clearSession(session)
}

export function suppressMediaActionClick(event: MouseEvent): boolean {
  const element = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  if (!element || !isElementSuppressed(element))
    return false
  event.preventDefault()
  event.stopPropagation()
  return true
}

export function openMediaActionContextMenu(target: MediaActionTarget, event: MouseEvent): boolean {
  event.preventDefault()
  const element = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  if (element && isElementSuppressed(element)) {
    event.stopPropagation()
    return false
  }

  if (isTouchContextMenuEvent(event)) {
    event.stopPropagation()
    return false
  }

  openMediaActionMenu({
    target,
    anchor: { x: event.clientX, y: event.clientY },
    presentation: 'popover',
  })
  event.stopPropagation()
  return true
}

export function handleMediaActionKeyboard(
  target: MediaActionTarget,
  event: KeyboardEvent,
  activate: () => void,
): boolean {
  if (event.target !== event.currentTarget)
    return false
  if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
    event.preventDefault()
    const element = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
    const bounds = element?.getBoundingClientRect()
    openMediaActionMenu({
      target,
      anchor: bounds ? { x: bounds.left + Math.min(bounds.width / 2, 48), y: bounds.top + Math.min(bounds.height / 2, 48) } : undefined,
      presentation: 'popover',
    })
    return true
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    activate()
    return true
  }
  return false
}

function triggerLongPress(session: LongPressSession) {
  if (sessions.get(session.pointerId) !== session)
    return
  session.timer = undefined
  session.triggered = true
  suppressSyntheticEvents(session.element)
  openMediaActionMenu({ target: session.target, presentation: 'sheet' })
}

function clearSession(session: LongPressSession) {
  if (session.timer)
    window.clearTimeout(session.timer)
  sessions.delete(session.pointerId)
  removeScrollCancellationWhenIdle()
}

function suppressSyntheticEvents(element: HTMLElement) {
  suppressedElements.set(element, Date.now() + SYNTHETIC_EVENT_SUPPRESSION_MS)
}

function isElementSuppressed(element: HTMLElement): boolean {
  const until = suppressedElements.get(element) ?? 0
  if (Date.now() < until)
    return true
  suppressedElements.delete(element)
  return false
}

function isTouchContextMenuEvent(event: MouseEvent): boolean {
  return typeof PointerEvent !== 'undefined' && event instanceof PointerEvent && event.pointerType !== 'mouse'
}

function isInteractiveMediaActionDescendant(target: EventTarget | null, host: HTMLElement): boolean {
  return target instanceof Element && target !== host && Boolean(target.closest('button, a, input, select, textarea, [role="button"], [role="menuitem"]'))
}

function ensureScrollCancellation() {
  if (scrollListenerInstalled)
    return
  scrollListenerInstalled = true
  window.addEventListener('scroll', cancelAllPendingLongPresses, true)
}

function removeScrollCancellationWhenIdle() {
  if (!scrollListenerInstalled || sessions.size > 0)
    return
  scrollListenerInstalled = false
  window.removeEventListener('scroll', cancelAllPendingLongPresses, true)
}

function cancelAllPendingLongPresses() {
  const pending = [...sessions.values()]
  for (const session of pending)
    clearSession(session)
}
