import type { DataSource } from '@/services/datasource/types'
import type { DownloadTask, OfflineAttachmentInput, OfflineAttachmentSyncResult } from '@/services/downloads'
import { syncOfflineAttachments } from '@/services/downloads'
import { artworkCacheKey, cacheImage } from '@/services/imageCache'

const SUBTITLE_EXTENSIONS = new Set(['srt', 'ass', 'ssa', 'vtt', 'sub'])

export async function synchronizeOfflineAttachments(task: DownloadTask, source: DataSource): Promise<OfflineAttachmentSyncResult> {
  const detail = await source.getDetail(task.itemId)
  const attachments: OfflineAttachmentInput[] = []
  const failedKinds: string[] = []

  await collectArtwork(detail.posterUrl, 'poster', task, attachments, failedKinds)
  await collectArtwork(detail.backdropUrl, 'backdrop', task, attachments, failedKinds)
  await collectArtwork(detail.stills?.[0], 'still', task, attachments, failedKinds)

  try {
    const stream = await source.getStreamRequest?.({
      itemId: task.itemId,
      mediaSourceId: task.mediaSourceId,
      variantId: task.variantId,
    })
    for (const subtitle of stream?.subtitles ?? []) {
      if (!subtitle.url)
        continue
      const extension = subtitleExtension(subtitle.codec, subtitle.url)
      if (!extension) {
        failedKinds.push('subtitle')
        continue
      }
      attachments.push({
        kind: 'subtitle',
        remoteUrl: subtitle.url,
        headers: subtitle.headers ? { ...subtitle.headers } : undefined,
        extension,
      })
    }

    const danmakuTrack = stream?.danmaku?.[0]
    if (danmakuTrack && source.getDanmakuComments) {
      try {
        const comments = await source.getDanmakuComments(danmakuTrack)
        if (comments.length > 0)
          attachments.push({ kind: 'danmaku', dataUrl: jsonDataUrl(comments), extension: 'json' })
      }
      catch {
        failedKinds.push('danmaku')
      }
    }
  }
  catch {
    if ((detail.subtitles ?? []).some(track => track.source === 'external' || track.url))
      failedKinds.push('subtitle')
  }

  return syncOfflineAttachments(task.id, attachments, [...new Set(failedKinds)])
}

async function collectArtwork(
  url: string | undefined,
  kind: 'poster' | 'backdrop' | 'still',
  task: DownloadTask,
  output: OfflineAttachmentInput[],
  failedKinds: string[],
) {
  if (!url)
    return
  const cached = url.startsWith('data:image/')
    ? url
    : await cacheImage(artworkCacheKey(task.sourceId, task.itemId, kind === 'still' ? 'thumbnail' : kind), url)
  if (cached.startsWith('data:image/'))
    output.push({ kind, dataUrl: cached })
  else
    failedKinds.push(kind)
}

function subtitleExtension(codec: string | undefined, url: string): string | undefined {
  const fromCodec = codec?.trim().toLocaleLowerCase().replace(/^web/, '')
  if (fromCodec && SUBTITLE_EXTENSIONS.has(fromCodec))
    return fromCodec
  try {
    const suffix = new URL(url).pathname.split('.').pop()?.toLocaleLowerCase()
    return suffix && SUBTITLE_EXTENSIONS.has(suffix) ? suffix : undefined
  }
  catch {
    return undefined
  }
}

function jsonDataUrl(value: unknown): string {
  const bytes = new TextEncoder().encode(JSON.stringify(value))
  let binary = ''
  for (let offset = 0; offset < bytes.length; offset += 0x8000)
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000))
  return `data:application/json;base64,${btoa(binary)}`
}
