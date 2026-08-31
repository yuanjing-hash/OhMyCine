import type { MediaDetail, SubtitleTrack } from './types'
import { isVideoFileName, providerBasename, providerParentPath, stripFileExtension } from '@/services/scraper/pathUtils'

const SUBTITLE_EXTENSIONS = new Set(['srt', 'ass', 'ssa', 'vtt', 'sub'])

export interface SiblingSubtitleEntry {
  readonly name: string
  readonly path: string
  readonly isDir: boolean
}

export async function withSiblingSubtitles(
  detail: MediaDetail,
  listEntries: (parentPath: string) => Promise<readonly SiblingSubtitleEntry[]>,
  resolveUrl: (path: string) => Promise<string>,
): Promise<MediaDetail> {
  if (detail.type === 'folder' || detail.type === 'series' || detail.type === 'season' || !isVideoFileName(detail.path))
    return detail

  try {
    const entries = await listEntries(providerParentPath(detail.path))
    const subtitles = await discoverSiblingSubtitles(detail.path, entries, resolveUrl)
    return subtitles.length > 0 ? { ...detail, subtitles } : detail
  }
  catch {
    return detail
  }
}

export async function discoverSiblingSubtitles(
  videoPath: string,
  entries: readonly SiblingSubtitleEntry[],
  resolveUrl: (path: string) => Promise<string>,
): Promise<SubtitleTrack[]> {
  const videoName = providerBasename(videoPath)
  if (!videoName || !isVideoFileName(videoName))
    return []

  const videoStem = stripFileExtension(videoName)
  const parentPath = providerParentPath(videoPath)
  const candidates = entries
    .filter(entry => !entry.isDir && providerParentPath(entry.path) === parentPath)
    .map(entry => ({ entry, extension: subtitleExtension(entry.name) }))
    .filter((candidate): candidate is { entry: SiblingSubtitleEntry, extension: string } => Boolean(candidate.extension))
    .filter(candidate => subtitleStemMatchesVideo(stripFileExtension(candidate.entry.name), videoStem))
    .sort((left, right) => subtitleMatchRank(left.entry.name, videoStem) - subtitleMatchRank(right.entry.name, videoStem)
      || left.entry.name.localeCompare(right.entry.name))

  const tracks: SubtitleTrack[] = []
  for (const candidate of candidates) {
    try {
      const url = await resolveUrl(candidate.entry.path)
      if (!url)
        continue
      tracks.push({
        index: stableSubtitleIndex(candidate.entry.path),
        language: inferSubtitleLanguage(stripFileExtension(candidate.entry.name).slice(videoStem.length)),
        title: candidate.entry.name,
        codec: candidate.extension,
        isDefault: false,
        source: 'external',
        url,
      })
    }
    catch {
      // One inaccessible sibling subtitle must not hide the video detail or other valid tracks.
    }
  }
  return tracks
}

function subtitleExtension(name: string): string | null {
  const match = /\.([a-z0-9]{2,5})$/i.exec(name)
  if (!match)
    return null
  const extension = match[1].toLowerCase()
  return SUBTITLE_EXTENSIONS.has(extension) ? extension : null
}

function subtitleStemMatchesVideo(subtitleStem: string, videoStem: string): boolean {
  const subtitle = subtitleStem.toLocaleLowerCase()
  const video = videoStem.toLocaleLowerCase()
  if (subtitle === video)
    return true
  if (!subtitle.startsWith(video))
    return false
  return /^[._\-\s[(]/.test(subtitle.slice(video.length))
}

function subtitleMatchRank(name: string, videoStem: string): number {
  return stripFileExtension(name).toLocaleLowerCase() === videoStem.toLocaleLowerCase() ? 0 : 1
}

function inferSubtitleLanguage(rawSuffix: string): string {
  const suffix = rawSuffix.toLocaleLowerCase().replace(/^[._\-\s[(]+|[\])]+$/g, '')
  if (!suffix)
    return 'Unknown'
  if (/(?:^|[._\-\s])(?:zh[-_]?tw|zh[-_]?hk|cht|tc|traditional)(?:$|[._\-\s])/.test(suffix))
    return 'zh-TW'
  if (/(?:^|[._\-\s])(?:zh|zh[-_]?cn|zh[-_]?hans|chs|sc|chi|zho|cn|simplified)(?:$|[._\-\s])/.test(suffix))
    return 'zh-CN'
  if (/(?:^|[._\-\s])(?:en|eng|english)(?:$|[._\-\s])/.test(suffix))
    return 'en'
  if (/(?:^|[._\-\s])(?:ja|jpn|jp|japanese)(?:$|[._\-\s])/.test(suffix))
    return 'ja'
  if (/(?:^|[._\-\s])(?:ko|kor|kr|korean)(?:$|[._\-\s])/.test(suffix))
    return 'ko'
  return 'Unknown'
}

function stableSubtitleIndex(path: string): number {
  let hash = 2166136261
  for (const char of path) {
    hash ^= char.codePointAt(0) ?? 0
    hash = Math.imul(hash, 16777619)
  }
  return 1_000_000 + (hash >>> 0) % 1_000_000_000
}
