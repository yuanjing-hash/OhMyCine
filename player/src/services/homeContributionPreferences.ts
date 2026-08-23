import { getAppSetting, setAppSetting } from '@/services/appSettings'

const STORAGE_KEY = 'ohmycine-home-contribution-preferences-v1'

export type HomeContributionPlacement = 'hero' | 'content'

export interface HomeContributionPreference {
  enabled: boolean
  order: number
  placement: HomeContributionPlacement
}

export type HomeContributionPreferences = Record<string, HomeContributionPreference>

export function loadHomeContributionPreferences(): HomeContributionPreferences {
  const raw = getAppSetting(STORAGE_KEY)
  if (!raw)
    return {}
  try {
    const value = JSON.parse(raw) as unknown
    if (!isRecord(value))
      return {}
    return Object.fromEntries(Object.entries(value).slice(0, 200).flatMap(([key, entry]) => {
      if (!isSafeKey(key) || !isRecord(entry))
        return []
      return [[key, {
        enabled: entry.enabled !== false,
        order: boundedOrder(entry.order),
        placement: entry.placement === 'hero' ? 'hero' : 'content',
      } satisfies HomeContributionPreference]]
    }))
  }
  catch {
    return {}
  }
}

export async function saveHomeContributionPreferences(value: HomeContributionPreferences): Promise<void> {
  const safe = Object.fromEntries(Object.entries(value).slice(0, 200).flatMap(([key, entry]) => {
    if (!isSafeKey(key))
      return []
    return [[key, {
      enabled: entry.enabled !== false,
      order: boundedOrder(entry.order),
      placement: entry.placement === 'hero' ? 'hero' : 'content',
    }]]
  }))
  await setAppSetting(STORAGE_KEY, JSON.stringify(safe))
}

export function contributionPreferenceKey(providerIdentity: string, sectionId: string): string {
  return `${providerIdentity}\u0000${sectionId}`
}

function boundedOrder(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value)
    ? Math.max(0, Math.min(10_000, value))
    : 10_000
}

function isSafeKey(value: string): boolean {
  return value.length > 0 && value.length <= 512 && !/[\r\n]/.test(value)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}
