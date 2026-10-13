export interface MpvColorDiagnostics {
  sourceGamma: string | null
  sourcePrimaries: string | null
  sourcePixelFormat: string | null
  sourceHardwarePixelFormat: string | null
  dolbyVisionProfile: number | null
  targetGamma: string | null
  targetPrimaries: string | null
  targetPixelFormat: string | null
  outputMode: 'hdr' | 'sdr' | 'unknown'
  gpuContext: string | null
  displayHdrTypes: string[] | null
  reason: string | null
}

export interface VideoDynamicRangeState {
  label: string
  details: string
}

export type VideoSourceColorMetadata = Pick<MpvColorDiagnostics, 'sourceGamma' | 'sourcePrimaries' | 'sourcePixelFormat' | 'sourceHardwarePixelFormat' | 'dolbyVisionProfile'>

type VideoTransfer = 'sdr' | 'pq' | 'hlg' | 'unknown'

const SDR_TRANSFERS = new Set(['bt.1886', 'srgb', 'gamma1.8', 'gamma2.0', 'gamma2.2', 'gamma2.4', 'gamma2.6', 'gamma2.8'])

export function normalizeVideoColorMetadata(value: unknown): string | null {
  if (typeof value !== 'string')
    return null
  const normalized = value.trim()
  return normalized && !/^(?:unknown|auto|undefined|null)$/i.test(normalized) ? normalized : null
}

export function normalizeDolbyVisionProfile(value: unknown): number | null {
  if (typeof value !== 'number' && (typeof value !== 'string' || !/^\d+(?:\.0+)?$/.test(value.trim())))
    return null
  const profile = Number(value)
  return Number.isInteger(profile) && profile > 0 && profile <= 255 ? profile : null
}

function videoTransfer(gamma: string | null | undefined): VideoTransfer {
  const normalized = normalizeVideoColorMetadata(gamma)?.toLowerCase()
  if (normalized === 'pq')
    return 'pq'
  if (normalized === 'hlg')
    return 'hlg'
  return normalized && SDR_TRANSFERS.has(normalized) ? 'sdr' : 'unknown'
}

function sourceDetails(color: VideoSourceColorMetadata | null | undefined, profile: number | null): string {
  const parts = [
    color?.sourceGamma ? `gamma ${color.sourceGamma}` : null,
    color?.sourcePrimaries ? `primaries ${color.sourcePrimaries}` : null,
    color?.sourcePixelFormat ? `format ${color.sourcePixelFormat}` : null,
    color?.sourceHardwarePixelFormat ? `hw-format ${color.sourceHardwarePixelFormat}` : null,
    profile ? `DV profile ${profile}` : null,
  ].filter((part): part is string => Boolean(part))
  return parts.join(' · ') || '当前片源的色彩信息尚不可用'
}

export function describeVideoSourceColor(color: VideoSourceColorMetadata | null | undefined): VideoDynamicRangeState {
  const profile = normalizeDolbyVisionProfile(color?.dolbyVisionProfile)
  const details = sourceDetails(color, profile)
  if (profile)
    return { label: 'Dolby Vision', details }

  switch (videoTransfer(color?.sourceGamma)) {
    case 'pq':
      return { label: 'PQ HDR', details }
    case 'hlg':
      return { label: 'HLG HDR', details }
    case 'sdr':
      return { label: 'SDR', details }
    default:
      return { label: '未知', details }
  }
}

export function describeVideoTargetColor(color: MpvColorDiagnostics | null | undefined): VideoDynamicRangeState {
  const transfer = videoTransfer(color?.targetGamma)
  let label = '未知'
  if (color?.outputMode === 'hdr' && (transfer === 'pq' || transfer === 'hlg'))
    label = transfer === 'pq' ? 'PQ HDR 目标' : 'HLG HDR 目标'
  else if (color?.outputMode === 'sdr' && transfer === 'sdr')
    label = 'SDR 目标'

  const parts = [
    color?.targetGamma ? `gamma ${color.targetGamma}` : null,
    color?.targetPrimaries ? `primaries ${color.targetPrimaries}` : null,
    color?.targetPixelFormat ? `GPU format ${color.targetPixelFormat}` : null,
  ].filter((part): part is string => Boolean(part))
  return { label, details: parts.join(' · ') || '当前视频的渲染目标信息尚不可用' }
}
