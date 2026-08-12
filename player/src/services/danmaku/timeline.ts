import type { DanmakuComment } from './types'

export interface DanmakuTimelineWindow {
  start: number
  end: number
}

export function findDanmakuTimelineWindow(
  comments: readonly DanmakuComment[],
  currentTime: number,
  lookBehindSeconds: number,
): DanmakuTimelineWindow {
  if (!Number.isFinite(currentTime) || !Number.isFinite(lookBehindSeconds) || comments.length === 0)
    return { start: 0, end: 0 }
  const earliest = Math.max(0, currentTime - Math.max(0, lookBehindSeconds))
  return {
    start: lowerBound(comments, earliest),
    end: upperBound(comments, currentTime),
  }
}

function lowerBound(comments: readonly DanmakuComment[], time: number): number {
  let low = 0
  let high = comments.length
  while (low < high) {
    const middle = low + Math.floor((high - low) / 2)
    if (comments[middle].time < time)
      low = middle + 1
    else
      high = middle
  }
  return low
}

function upperBound(comments: readonly DanmakuComment[], time: number): number {
  let low = 0
  let high = comments.length
  while (low < high) {
    const middle = low + Math.floor((high - low) / 2)
    if (comments[middle].time <= time)
      low = middle + 1
    else
      high = middle
  }
  return low
}
