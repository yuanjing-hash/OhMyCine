import type { RawFileSourceType } from './types'
import type { DataSourceConfig } from '@/services/datasource/types'

export type RawSourceScanKind = 'full' | 'incremental'

export interface RawSourceScanKindSchedule {
  readonly enabled: boolean
  readonly intervalMs: number
}

export interface RawSourceScanScheduleConfig {
  readonly full: RawSourceScanKindSchedule
  readonly incremental: RawSourceScanKindSchedule
}

export const DEFAULT_RAW_SOURCE_FULL_SCAN_INTERVAL_MS = 6 * 60 * 60 * 1000
export const DEFAULT_RAW_SOURCE_INCREMENTAL_SCAN_INTERVAL_MS = 60 * 1000
export const MIN_RAW_SOURCE_SCAN_INTERVAL_MS = 60 * 1000
export const RAW_SOURCE_SCAN_SCHEDULE_EXTRA_KEY = 'rawSourceScanSchedule'

export function isAutoIndexableRawSourceType(type: DataSourceConfig['type']): type is Extract<RawFileSourceType, 'alist' | 'local'> {
  return type === 'alist' || type === 'local'
}

export function readRawSourceScanScheduleConfig(config: DataSourceConfig | null | undefined): RawSourceScanScheduleConfig {
  const raw = config?.extra?.[RAW_SOURCE_SCAN_SCHEDULE_EXTRA_KEY]
  const record = isRecord(raw) ? raw : {}

  return {
    full: readKindSchedule(record.full, DEFAULT_RAW_SOURCE_FULL_SCAN_INTERVAL_MS),
    incremental: readKindSchedule(record.incremental, DEFAULT_RAW_SOURCE_INCREMENTAL_SCAN_INTERVAL_MS),
  }
}

export function updateRawSourceScanScheduleExtra(
  extra: Record<string, unknown> | undefined,
  scanKind: RawSourceScanKind,
  patch: Partial<RawSourceScanKindSchedule>,
): Record<string, unknown> {
  const currentConfig = readRawSourceScanScheduleConfig({
    id: '',
    type: 'local',
    name: '',
    order: 0,
    url: '',
    extra,
  })
  const nextConfig: RawSourceScanScheduleConfig = {
    ...currentConfig,
    [scanKind]: normalizeKindSchedule({
      ...currentConfig[scanKind],
      ...patch,
    }, defaultIntervalForKind(scanKind)),
  }

  return {
    ...(extra ?? {}),
    [RAW_SOURCE_SCAN_SCHEDULE_EXTRA_KEY]: nextConfig,
  }
}

export function intervalMinutesToMs(value: number): number {
  return clampIntervalMs(Math.round(value * 60 * 1000), MIN_RAW_SOURCE_SCAN_INTERVAL_MS)
}

export function intervalMsToMinutes(value: number): number {
  return Math.max(1, Math.round(value / 60_000))
}

export function defaultIntervalForKind(scanKind: RawSourceScanKind): number {
  return scanKind === 'incremental'
    ? DEFAULT_RAW_SOURCE_INCREMENTAL_SCAN_INTERVAL_MS
    : DEFAULT_RAW_SOURCE_FULL_SCAN_INTERVAL_MS
}

function readKindSchedule(value: unknown, defaultIntervalMs: number): RawSourceScanKindSchedule {
  if (!isRecord(value)) {
    return {
      enabled: true,
      intervalMs: defaultIntervalMs,
    }
  }

  return normalizeKindSchedule({
    enabled: typeof value.enabled === 'boolean' ? value.enabled : true,
    intervalMs: typeof value.intervalMs === 'number' ? value.intervalMs : defaultIntervalMs,
  }, defaultIntervalMs)
}

function normalizeKindSchedule(value: Partial<RawSourceScanKindSchedule>, defaultIntervalMs: number): RawSourceScanKindSchedule {
  return {
    enabled: value.enabled ?? true,
    intervalMs: clampIntervalMs(value.intervalMs, defaultIntervalMs),
  }
}

function clampIntervalMs(value: number | undefined, fallback: number): number {
  if (typeof value !== 'number' || !Number.isFinite(value))
    return fallback
  return Math.max(MIN_RAW_SOURCE_SCAN_INTERVAL_MS, Math.round(value))
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
