import type { StreamVariant } from '@/services/datasource/types'

const MAX_VARIANTS = 32

export function usableStreamVariants(variants: readonly StreamVariant[] | undefined): StreamVariant[] {
  const unique = new Map<string, StreamVariant>()
  for (const variant of variants ?? []) {
    const id = variant.id.trim()
    if (!id || !variant.available || unique.has(id))
      continue
    unique.set(id, { ...variant, id })
    if (unique.size >= MAX_VARIANTS)
      break
  }
  return [...unique.values()]
}

export function streamVariantLabel(variant: StreamVariant | undefined): string {
  if (!variant)
    return '清晰度'

  const explicit = variant.label.trim()
  if (explicit)
    return explicit
  if (variant.height && variant.height > 0)
    return `${variant.height}p`
  return '清晰度'
}

export function streamVariantDescription(variant: StreamVariant): string {
  const resolution = variant.width && variant.height ? `${variant.width}×${variant.height}` : undefined
  const bitrate = variant.bitrate && variant.bitrate > 0 ? `${formatBitrate(variant.bitrate)}` : undefined
  return [resolution, bitrate, variant.videoCodec, variant.dynamicRange].filter(Boolean).join(' · ')
}

export function formatBitrate(bitsPerSecond: number | null | undefined): string {
  if (bitsPerSecond == null || !Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0)
    return '暂不可用'
  if (bitsPerSecond >= 1_000_000)
    return `${(bitsPerSecond / 1_000_000).toFixed(1)} Mbps`
  if (bitsPerSecond >= 1000)
    return `${Math.round(bitsPerSecond / 1000)} kbps`
  return `${Math.round(bitsPerSecond)} bps`
}
