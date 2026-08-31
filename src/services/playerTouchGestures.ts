export type TouchGestureAxis = 'pending' | 'horizontal' | 'vertical'

const DEFAULT_MOVEMENT_THRESHOLD = 12
const DEFAULT_SEEK_SPAN_SECONDS = 120
const MIN_SEEK_SPAN_SECONDS = 60
const MAX_SEEK_SPAN_SECONDS = 180

export function resolveTouchGestureAxis(
  deltaX: number,
  deltaY: number,
  movementThreshold = DEFAULT_MOVEMENT_THRESHOLD,
  dominanceRatio = 1,
): TouchGestureAxis {
  const horizontal = Math.abs(deltaX)
  const vertical = Math.abs(deltaY)
  if (Math.max(horizontal, vertical) < movementThreshold)
    return 'pending'
  if (horizontal >= vertical * dominanceRatio)
    return 'horizontal'
  if (vertical >= horizontal * dominanceRatio)
    return 'vertical'
  return 'pending'
}

export function touchSeekTarget(
  startPosition: number,
  deltaX: number,
  viewportWidth: number,
  duration: number,
): number {
  const safeWidth = Math.max(1, viewportWidth)
  const seekSpan = duration > 0
    ? clamp(duration * 0.08, MIN_SEEK_SPAN_SECONDS, MAX_SEEK_SPAN_SECONDS)
    : DEFAULT_SEEK_SPAN_SECONDS
  const target = startPosition + (deltaX / safeWidth) * seekSpan
  return clamp(target, 0, duration > 0 ? duration : Number.MAX_SAFE_INTEGER)
}

export function touchVerticalLevel(
  startLevel: number,
  deltaY: number,
  viewportHeight: number,
): number {
  const safeHeight = Math.max(1, viewportHeight)
  return clamp(startLevel - (deltaY / safeHeight) * 100, 0, 100)
}

export function isNearbyDoubleTap(
  previous: { x: number, y: number, at: number } | null,
  current: { x: number, y: number, at: number },
  maxDelayMs = 320,
  maxDistancePx = 72,
): boolean {
  if (!previous)
    return false
  const elapsed = current.at - previous.at
  if (elapsed <= 0 || elapsed > maxDelayMs)
    return false
  return Math.hypot(current.x - previous.x, current.y - previous.y) <= maxDistancePx
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}
