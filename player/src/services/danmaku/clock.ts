export interface DanmakuClockAnchor {
  mediaTime: number
  wallTime: number
}

export function interpolatedDanmakuTime(
  anchor: DanmakuClockAnchor,
  wallTime: number,
  playbackSpeed: number,
  playing: boolean,
): number {
  if (!playing)
    return anchor.mediaTime
  const elapsedSeconds = Math.max(0, wallTime - anchor.wallTime) / 1_000
  const speed = Number.isFinite(playbackSpeed) ? Math.max(0, playbackSpeed) : 1
  return anchor.mediaTime + elapsedSeconds * speed
}
