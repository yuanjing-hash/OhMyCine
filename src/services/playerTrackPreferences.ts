import type { MediaTrackPreference } from '@/services/mediaPlaybackPreferences'

export interface PlaybackTrackCandidate {
  id: string | number
  mpvId?: number
  language?: string | null
  title?: string | null
  codec?: string | null
  channels?: number | null
}

export function matchPlaybackTrackPreference<T extends PlaybackTrackCandidate>(
  tracks: readonly T[],
  preference: MediaTrackPreference | null | undefined,
): T | null {
  if (!preference)
    return null

  const hasStableFingerprint = Boolean(
    normalizedTrackText(preference.title)
    || normalizedTrackText(preference.language)
    || normalizedTrackText(preference.codec)
    || preference.channels != null,
  )
  if (hasStableFingerprint) {
    let best: { track: T, score: number } | null = null
    for (const track of tracks) {
      let score = 0
      if (sameTrackText(preference.title, track.title))
        score += 6
      if (sameTrackText(preference.language, track.language))
        score += 4
      if (sameTrackText(preference.codec, track.codec))
        score += 2
      if (preference.channels != null && track.channels === preference.channels)
        score += 2
      if (!best || score > best.score)
        best = { track, score }
    }

    const minimumScore = preference.title || preference.language
      ? 4
      : preference.codec || preference.channels != null
        ? 2
        : Number.POSITIVE_INFINITY
    return best && best.score >= minimumScore ? best.track : null
  }

  if (preference.trackId == null)
    return null
  return tracks.find(track => numericTrackId(track) === preference.trackId) ?? null
}

function numericTrackId(track: PlaybackTrackCandidate): number | null {
  if (typeof track.mpvId === 'number')
    return track.mpvId
  return typeof track.id === 'number' ? track.id : null
}

function sameTrackText(expected: string | null | undefined, actual: string | null | undefined): boolean {
  const normalizedExpected = normalizedTrackText(expected)
  return Boolean(normalizedExpected && normalizedExpected === normalizedTrackText(actual))
}

function normalizedTrackText(value: string | null | undefined): string {
  return value?.trim().toLocaleLowerCase() ?? ''
}
