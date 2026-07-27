import type { RawLocalScanCache, RawLocalScanLogEntry, RunRawSourceScanInput } from './localScanCache'
import type { RawSourceScanKind, RawSourceScanScheduleConfig } from './rawSourceScanSchedule'
import type { RawFileSourceType } from './types'
import type { DataSource, DataSourceConfig } from '@/services/datasource/types'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { readLocalProviderRootPath } from '@/services/datasource/local'
import { loadRawSourceScanCache, runRawSourceIncrementalScan, runRawSourceLocalScan } from './localScanCache'
import { normalizeProviderPath } from './pathUtils'
import {
  DEFAULT_RAW_SOURCE_FULL_SCAN_INTERVAL_MS,
  DEFAULT_RAW_SOURCE_INCREMENTAL_SCAN_INTERVAL_MS,
  defaultIntervalForKind,
  isAutoIndexableRawSourceType,
  readRawSourceScanScheduleConfig,
} from './rawSourceScanSchedule'

export const DEFAULT_RAW_SOURCE_INDEX_INTERVAL_MS = DEFAULT_RAW_SOURCE_FULL_SCAN_INTERVAL_MS
export const DEFAULT_RAW_SOURCE_INCREMENTAL_INDEX_INTERVAL_MS = DEFAULT_RAW_SOURCE_INCREMENTAL_SCAN_INTERVAL_MS
export const DEFAULT_RAW_SOURCE_AUTO_INDEX_TICK_MS = DEFAULT_RAW_SOURCE_INCREMENTAL_SCAN_INTERVAL_MS
export const DEFAULT_RAW_SOURCE_DIRTY_DEBOUNCE_MS = 2_500

export type RawSourceIndexStatusKind = 'idle' | 'disabled' | 'cooldown' | 'queued' | 'running' | 'completed' | 'failed'

export interface RawSourceIndexTarget {
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
  readonly source: Pick<DataSource, 'list'>
  readonly schedule?: RawSourceScanScheduleConfig
}

export interface RawSourceIndexStatus {
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
  readonly scanKind: RawSourceScanKind
  readonly state: RawSourceIndexStatusKind
  readonly lastAttemptAt?: string
  readonly lastSuccessAt?: string
  readonly lastFailureAt?: string
  readonly nextAllowedAt?: string
  readonly errorMessage?: string
  readonly dirty?: boolean
}

export interface RawSourceIndexTriggerResult extends RawSourceIndexStatus {
  readonly skipped: boolean
  readonly cache?: RawLocalScanCache
}

export interface RawSourceIndexManualScanOptions {
  readonly scanKind?: RawSourceScanKind
  readonly onLog?: (entry: RawLocalScanLogEntry) => void
}

export interface RawSourceAutoIndexingInput {
  readonly getTargets: () => RawSourceIndexTarget[] | Promise<RawSourceIndexTarget[]>
  readonly intervalMs?: number
  readonly fullIntervalMs?: number
  readonly incrementalIntervalMs?: number
  readonly tickIntervalMs?: number
}

export interface RawSourceIndexSchedulerOptions {
  readonly intervalMs?: number
  readonly incrementalIntervalMs?: number
  readonly tickIntervalMs?: number
  readonly dirtyDebounceMs?: number
  readonly now?: () => number
  readonly storage?: RawSourceIndexStorage | null
  readonly scanRunner?: (input: RunRawSourceScanInput) => Promise<RawLocalScanCache>
  readonly incrementalScanRunner?: (input: RunRawSourceScanInput) => Promise<RawLocalScanCache>
}

interface RawSourceIndexScheduleRecord {
  readonly version: 2
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
  readonly scanKind: RawSourceScanKind
  readonly lastAttemptAt?: string
  readonly lastSuccessAt?: string
  readonly lastFailureAt?: string
  readonly errorMessage?: string
}

interface RawSourceIndexLegacyScheduleRecord {
  readonly version: 1
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
  readonly lastAttemptAt?: string
  readonly lastSuccessAt?: string
  readonly lastFailureAt?: string
  readonly errorMessage?: string
}

interface RawSourceIndexStorage {
  getItem: (key: string) => string | null
  setItem: (key: string, value: string) => void
}

type RawSourceIndexListener = (status: RawSourceIndexStatus) => void

const RAW_SOURCE_INDEX_SCHEDULE_KEY_PREFIX = 'ohmycine-raw-source-index-schedule-v2'
const LEGACY_RAW_SOURCE_INDEX_SCHEDULE_KEY_PREFIX = 'ohmycine-raw-source-index-schedule-v1'

export class RawSourceIndexScheduler {
  private readonly statuses = new Map<string, RawSourceIndexStatus>()
  private readonly inFlight = new Map<string, Promise<RawLocalScanCache>>()
  private readonly dirtyTimers = new Map<string, ReturnType<typeof setTimeout>>()
  private readonly listeners = new Set<RawSourceIndexListener>()
  private readonly now: () => number
  private readonly storage: RawSourceIndexStorage | null
  private readonly scanRunner: (input: RunRawSourceScanInput) => Promise<RawLocalScanCache>
  private readonly incrementalScanRunner: (input: RunRawSourceScanInput) => Promise<RawLocalScanCache>
  private readonly defaultFullIntervalMs: number
  private readonly defaultIncrementalIntervalMs: number
  private readonly defaultTickIntervalMs: number
  private readonly dirtyDebounceMs: number
  private autoIndexInput: RawSourceAutoIndexingInput | null = null
  private autoIndexTimer: ReturnType<typeof setInterval> | null = null
  private autoIndexCycleRunning = false

  constructor(options: RawSourceIndexSchedulerOptions = {}) {
    this.defaultFullIntervalMs = options.intervalMs ?? DEFAULT_RAW_SOURCE_FULL_SCAN_INTERVAL_MS
    this.defaultIncrementalIntervalMs = options.incrementalIntervalMs ?? DEFAULT_RAW_SOURCE_INCREMENTAL_SCAN_INTERVAL_MS
    this.defaultTickIntervalMs = options.tickIntervalMs ?? Math.min(this.defaultIncrementalIntervalMs, DEFAULT_RAW_SOURCE_AUTO_INDEX_TICK_MS)
    this.dirtyDebounceMs = options.dirtyDebounceMs ?? DEFAULT_RAW_SOURCE_DIRTY_DEBOUNCE_MS
    this.now = options.now ?? (() => Date.now())
    this.storage = options.storage === undefined ? resolveDefaultStorage() : options.storage
    this.scanRunner = options.scanRunner ?? runRawSourceLocalScan
    this.incrementalScanRunner = options.incrementalScanRunner ?? runRawSourceIncrementalScan
  }

  subscribe(listener: RawSourceIndexListener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  startAutoIndexing(input: RawSourceAutoIndexingInput): void {
    this.autoIndexInput = input
    if (this.autoIndexTimer)
      clearInterval(this.autoIndexTimer)

    this.autoIndexTimer = setInterval(() => {
      void this.runAutoIndexCycle()
    }, input.tickIntervalMs ?? this.defaultTickIntervalMs)

    void this.runAutoIndexCycle()
  }

  stopAutoIndexing(): void {
    if (this.autoIndexTimer)
      clearInterval(this.autoIndexTimer)
    this.autoIndexTimer = null
    this.autoIndexInput = null
    for (const timer of this.dirtyTimers.values())
      clearTimeout(timer)
    this.dirtyTimers.clear()
  }

  async triggerAutoIndexForTargets(
    targets: readonly RawSourceIndexTarget[],
    options: { intervalMs?: number, scanKind?: RawSourceScanKind, ignoreCooldown?: boolean, respectSchedule?: boolean } = {},
  ): Promise<RawSourceIndexTriggerResult[]> {
    const scanKind = options.scanKind ?? 'full'
    const normalizedTargets = uniqueTargets(targets.map(normalizeTarget))
    const settled = await Promise.allSettled(
      normalizedTargets.map(target => this.triggerAutoIndexForTarget(target, scanKind, options)),
    )

    return settled.map((result, index) => {
      if (result.status === 'fulfilled')
        return result.value

      const target = normalizedTargets[index]
      const status = this.failedStatus(target, scanKind, result.reason)
      this.setStatus(status)
      return {
        ...status,
        skipped: false,
      }
    })
  }

  async forceScan(target: RawSourceIndexTarget, options: RawSourceIndexManualScanOptions = {}): Promise<RawLocalScanCache> {
    return this.runScan(normalizeTarget(target), options.scanKind ?? 'full', {
      onLog: options.onLog,
      throwOnFailure: true,
    })
  }

  markIncrementalDirty(target: RawSourceIndexTarget, options: { debounceMs?: number } = {}): void {
    const normalizedTarget = normalizeTarget(target)
    if (readScheduleForTarget(normalizedTarget).incremental.enabled === false)
      return

    const key = sourceIndexTargetKey(normalizedTarget)
    const currentTimer = this.dirtyTimers.get(key)
    if (currentTimer)
      clearTimeout(currentTimer)

    this.setStatus({
      ...this.baseStatus(normalizedTarget, 'incremental'),
      state: 'queued',
      dirty: true,
    })

    const timer = setTimeout(() => {
      this.dirtyTimers.delete(key)
      void this.triggerAutoIndexForTarget(normalizedTarget, 'incremental', {
        ignoreCooldown: true,
        respectSchedule: true,
      })
    }, options.debounceMs ?? this.dirtyDebounceMs)
    this.dirtyTimers.set(key, timer)
  }

  async getStatus(input: {
    readonly sourceId: string
    readonly sourceType: RawFileSourceType
    readonly rootPath?: string
    readonly scanKind?: RawSourceScanKind
  }): Promise<RawSourceIndexStatus> {
    const scanKind = input.scanKind ?? 'full'
    const rootPath = normalizeProviderPath(input.rootPath)
    const key = sourceIndexKey(scanKind, input.sourceId, input.sourceType, rootPath)
    const current = this.statuses.get(key)
    if (current)
      return current

    const record = this.readScheduleRecord(input.sourceId, input.sourceType, rootPath, scanKind)
    const cache = await loadRawSourceScanCache(input.sourceId, input.sourceType, rootPath)
    const lastAttemptAt = latestIso(record?.lastAttemptAt, cache?.finishedAt)
    const lastSuccessAt = latestIso(record?.lastSuccessAt, cache?.finishedAt)
    if (lastSuccessAt) {
      return {
        sourceId: input.sourceId,
        sourceType: input.sourceType,
        rootPath,
        scanKind,
        state: 'completed',
        lastAttemptAt,
        lastSuccessAt,
      }
    }

    if (record?.lastFailureAt) {
      return {
        sourceId: input.sourceId,
        sourceType: input.sourceType,
        rootPath,
        scanKind,
        state: 'failed',
        lastAttemptAt,
        lastFailureAt: record.lastFailureAt,
        errorMessage: record.errorMessage,
      }
    }

    return {
      sourceId: input.sourceId,
      sourceType: input.sourceType,
      rootPath,
      scanKind,
      state: 'idle',
      lastAttemptAt,
    }
  }

  async getStatuses(input: {
    readonly sourceId: string
    readonly sourceType: RawFileSourceType
    readonly rootPath?: string
  }): Promise<Record<RawSourceScanKind, RawSourceIndexStatus>> {
    const [full, incremental] = await Promise.all([
      this.getStatus({ ...input, scanKind: 'full' }),
      this.getStatus({ ...input, scanKind: 'incremental' }),
    ])
    return { full, incremental }
  }

  private async runAutoIndexCycle(): Promise<void> {
    if (!this.autoIndexInput || this.autoIndexCycleRunning)
      return

    this.autoIndexCycleRunning = true
    try {
      const targets = await this.autoIndexInput.getTargets()
      await Promise.all([
        this.triggerAutoIndexForTargets(targets, {
          scanKind: 'full',
          intervalMs: this.autoIndexInput.fullIntervalMs ?? this.autoIndexInput.intervalMs,
          respectSchedule: true,
        }),
        this.triggerAutoIndexForTargets(targets, {
          scanKind: 'incremental',
          intervalMs: this.autoIndexInput.incrementalIntervalMs,
          respectSchedule: true,
        }),
      ])
    }
    catch {
      // Auto indexing is a background enhancement and must never surface through app startup.
    }
    finally {
      this.autoIndexCycleRunning = false
    }
  }

  private async triggerAutoIndexForTarget(
    target: RawSourceIndexTarget,
    scanKind: RawSourceScanKind,
    options: { intervalMs?: number, ignoreCooldown?: boolean, respectSchedule?: boolean } = {},
  ): Promise<RawSourceIndexTriggerResult> {
    if (options.respectSchedule !== false && readScheduleForTarget(target)[scanKind].enabled === false) {
      const status: RawSourceIndexStatus = {
        ...this.baseStatus(target, scanKind),
        state: 'disabled',
      }
      this.setStatus(status)
      return {
        ...status,
        skipped: true,
      }
    }

    const targetKey = sourceIndexTargetKey(target)
    if (this.inFlight.has(targetKey)) {
      const status: RawSourceIndexStatus = {
        ...this.baseStatus(target, scanKind),
        state: 'running',
      }
      this.setStatus(status)
      return {
        ...status,
        skipped: true,
      }
    }

    const intervalMs = options.intervalMs ?? intervalForTarget(target, scanKind, this.defaultIntervalForKind(scanKind))
    const cooldown = options.ignoreCooldown ? null : await this.cooldownStatus(target, scanKind, intervalMs)
    if (cooldown) {
      this.setStatus(cooldown)
      return {
        ...cooldown,
        skipped: true,
      }
    }

    try {
      const cache = await this.runScan(target, scanKind, { throwOnFailure: false })
      const status = await this.getStatus({ ...target, scanKind })
      return {
        ...status,
        skipped: false,
        cache,
      }
    }
    catch (error) {
      const status = this.failedStatus(target, scanKind, error)
      this.setStatus(status)
      return {
        ...status,
        skipped: false,
      }
    }
  }

  private async runScan(
    target: RawSourceIndexTarget,
    scanKind: RawSourceScanKind,
    options: { onLog?: (entry: RawLocalScanLogEntry) => void, throwOnFailure: boolean },
  ): Promise<RawLocalScanCache> {
    const targetKey = sourceIndexTargetKey(target)
    const active = this.inFlight.get(targetKey)
    if (active)
      return active

    const startedAt = this.isoNow()
    this.writeScheduleRecord({
      ...this.readScheduleRecord(target.sourceId, target.sourceType, target.rootPath, scanKind),
      version: 2,
      sourceId: target.sourceId,
      sourceType: target.sourceType,
      rootPath: target.rootPath,
      scanKind,
      lastAttemptAt: startedAt,
    })
    this.setStatus({
      ...this.baseStatus(target, scanKind),
      state: 'running',
      lastAttemptAt: startedAt,
    })

    const runner = scanKind === 'incremental' ? this.incrementalScanRunner : this.scanRunner
    const scan = Promise.resolve().then(() => runner({
      source: target.source,
      sourceId: target.sourceId,
      sourceType: target.sourceType,
      rootPath: target.rootPath,
      scanKind,
      onLog: options.onLog,
    }))

    this.inFlight.set(targetKey, scan)

    try {
      const cache = await scan
      const finishedAt = this.isoNow()
      this.writeScheduleRecord({
        version: 2,
        sourceId: target.sourceId,
        sourceType: target.sourceType,
        rootPath: target.rootPath,
        scanKind,
        lastAttemptAt: startedAt,
        lastSuccessAt: finishedAt,
      })
      this.setStatus({
        ...this.baseStatus(target, scanKind),
        state: 'completed',
        lastAttemptAt: startedAt,
        lastSuccessAt: finishedAt,
      })
      return cache
    }
    catch (error) {
      const failedAt = this.isoNow()
      const errorMessage = toSafeErrorMessage(error, '后台索引失败，文件夹浏览和其他数据源不受影响。')
      this.writeScheduleRecord({
        version: 2,
        sourceId: target.sourceId,
        sourceType: target.sourceType,
        rootPath: target.rootPath,
        scanKind,
        lastAttemptAt: startedAt,
        lastFailureAt: failedAt,
        errorMessage,
      })
      this.setStatus({
        ...this.baseStatus(target, scanKind),
        state: 'failed',
        lastAttemptAt: startedAt,
        lastFailureAt: failedAt,
        errorMessage,
      })
      if (options.throwOnFailure)
        throw new Error(errorMessage)
      throw error
    }
    finally {
      if (this.inFlight.get(targetKey) === scan)
        this.inFlight.delete(targetKey)
    }
  }

  private async cooldownStatus(target: RawSourceIndexTarget, scanKind: RawSourceScanKind, intervalMs: number): Promise<RawSourceIndexStatus | null> {
    if (intervalMs <= 0)
      return null

    const record = this.readScheduleRecord(target.sourceId, target.sourceType, target.rootPath, scanKind)
    const cache = await loadRawSourceScanCache(target.sourceId, target.sourceType, target.rootPath)
    const lastAttemptMs = latestTimestamp(record?.lastAttemptAt, cache?.finishedAt)
    if (lastAttemptMs == null)
      return null

    const nextAllowedMs = lastAttemptMs + intervalMs
    if (this.now() >= nextAllowedMs)
      return null

    return {
      ...this.baseStatus(target, scanKind),
      state: 'cooldown',
      lastAttemptAt: latestIso(record?.lastAttemptAt, cache?.finishedAt),
      lastSuccessAt: latestIso(record?.lastSuccessAt, cache?.finishedAt),
      lastFailureAt: record?.lastFailureAt,
      nextAllowedAt: new Date(nextAllowedMs).toISOString(),
      errorMessage: record?.errorMessage,
    }
  }

  private defaultIntervalForKind(scanKind: RawSourceScanKind): number {
    return scanKind === 'incremental' ? this.defaultIncrementalIntervalMs : this.defaultFullIntervalMs
  }

  private baseStatus(
    target: Pick<RawSourceIndexTarget, 'sourceId' | 'sourceType' | 'rootPath'>,
    scanKind: RawSourceScanKind,
  ): Pick<RawSourceIndexStatus, 'sourceId' | 'sourceType' | 'rootPath' | 'scanKind'> {
    return {
      sourceId: target.sourceId,
      sourceType: target.sourceType,
      rootPath: target.rootPath,
      scanKind,
    }
  }

  private failedStatus(
    target: Pick<RawSourceIndexTarget, 'sourceId' | 'sourceType' | 'rootPath'>,
    scanKind: RawSourceScanKind,
    error: unknown,
  ): RawSourceIndexStatus {
    return {
      ...this.baseStatus(target, scanKind),
      state: 'failed',
      lastFailureAt: this.isoNow(),
      errorMessage: toSafeErrorMessage(error, '后台索引失败，文件夹浏览和其他数据源不受影响。'),
    }
  }

  private setStatus(status: RawSourceIndexStatus): void {
    this.statuses.set(sourceIndexKey(status.scanKind, status.sourceId, status.sourceType, status.rootPath), status)
    for (const listener of this.listeners)
      listener(status)
  }

  private readScheduleRecord(
    sourceId: string,
    sourceType: RawFileSourceType,
    rootPath: string,
    scanKind: RawSourceScanKind,
  ): RawSourceIndexScheduleRecord | null {
    if (!this.storage)
      return null

    try {
      const raw = this.storage.getItem(scheduleKey(sourceId, sourceType, rootPath, scanKind))
      if (raw) {
        const value = JSON.parse(raw) as unknown
        if (isScheduleRecord(value, sourceId, sourceType, rootPath, scanKind))
          return value
      }

      if (scanKind !== 'full')
        return null

      const legacyRaw = this.storage.getItem(legacyScheduleKey(sourceId, sourceType, rootPath))
      if (!legacyRaw)
        return null
      const legacyValue = JSON.parse(legacyRaw) as unknown
      if (!isLegacyScheduleRecord(legacyValue, sourceId, sourceType, rootPath))
        return null
      return {
        version: 2,
        sourceId,
        sourceType,
        rootPath,
        scanKind,
        lastAttemptAt: legacyValue.lastAttemptAt,
        lastSuccessAt: legacyValue.lastSuccessAt,
        lastFailureAt: legacyValue.lastFailureAt,
        errorMessage: legacyValue.errorMessage,
      }
    }
    catch {
      return null
    }
  }

  private writeScheduleRecord(record: RawSourceIndexScheduleRecord): void {
    if (!this.storage)
      return

    try {
      this.storage.setItem(scheduleKey(record.sourceId, record.sourceType, record.rootPath, record.scanKind), JSON.stringify(record))
    }
    catch {
      // Schedule metadata is best-effort; failure must not affect scanning or browsing.
    }
  }

  private isoNow(): string {
    return new Date(this.now()).toISOString()
  }
}

export function createRawSourceAutoIndexTargets(
  configs: readonly DataSourceConfig[],
  resolveSource: (sourceId: string) => Pick<DataSource, 'list'> | null,
): RawSourceIndexTarget[] {
  return configs
    .filter(config => config.enabled !== false && isAutoIndexableRawSourceType(config.type))
    .map((config) => {
      const source = resolveSource(config.id)
      return source ? createRawSourceIndexTarget(config, source) : null
    })
    .filter((target): target is RawSourceIndexTarget => target != null)
}

export function createRawSourceIndexTarget(
  config: DataSourceConfig,
  source: Pick<DataSource, 'list'>,
): RawSourceIndexTarget | null {
  if (config.enabled === false || !isAutoIndexableRawSourceType(config.type))
    return null

  return normalizeTarget({
    sourceId: config.id,
    sourceType: config.type,
    rootPath: readRawSourceRootPath(config),
    source,
    schedule: readRawSourceScanScheduleConfig(config),
  })
}

export function readRawSourceRootPath(config: DataSourceConfig): string {
  if (config.type === 'local')
    return readLocalProviderRootPath(config)

  const rawRootPath = config.extra?.rootPath
  if (typeof rawRootPath !== 'string')
    return '/'

  try {
    return normalizeProviderPath(rawRootPath)
  }
  catch {
    return '/'
  }
}

export function createRawSourceIndexScheduler(options: RawSourceIndexSchedulerOptions = {}): RawSourceIndexScheduler {
  return new RawSourceIndexScheduler(options)
}

export const rawSourceIndexScheduler = createRawSourceIndexScheduler()

function normalizeTarget(target: RawSourceIndexTarget): RawSourceIndexTarget {
  return {
    ...target,
    rootPath: normalizeProviderPath(target.rootPath),
    schedule: target.schedule ?? {
      full: {
        enabled: true,
        intervalMs: defaultIntervalForKind('full'),
      },
      incremental: {
        enabled: true,
        intervalMs: defaultIntervalForKind('incremental'),
      },
    },
  }
}

function uniqueTargets(targets: readonly RawSourceIndexTarget[]): RawSourceIndexTarget[] {
  const map = new Map<string, RawSourceIndexTarget>()
  for (const target of targets)
    map.set(sourceIndexTargetKey(target), target)
  return [...map.values()]
}

function readScheduleForTarget(target: RawSourceIndexTarget): RawSourceScanScheduleConfig {
  return target.schedule ?? readRawSourceScanScheduleConfig(null)
}

function intervalForTarget(target: RawSourceIndexTarget, scanKind: RawSourceScanKind, fallback: number): number {
  return target.schedule?.[scanKind].intervalMs ?? fallback
}

function sourceIndexTargetKey(target: Pick<RawSourceIndexTarget, 'sourceId' | 'sourceType' | 'rootPath'>): string {
  return `${target.sourceType}:${target.sourceId}:${target.rootPath}`
}

function sourceIndexKey(scanKind: RawSourceScanKind, sourceId: string, sourceType: RawFileSourceType, rootPath: string): string {
  return `${scanKind}:${sourceType}:${sourceId}:${rootPath}`
}

function scheduleKey(sourceId: string, sourceType: RawFileSourceType, rootPath: string, scanKind: RawSourceScanKind): string {
  return `${RAW_SOURCE_INDEX_SCHEDULE_KEY_PREFIX}:${encodeURIComponent(scanKind)}:${encodeURIComponent(sourceType)}:${encodeURIComponent(sourceId)}:${encodeURIComponent(rootPath)}`
}

function legacyScheduleKey(sourceId: string, sourceType: RawFileSourceType, rootPath: string): string {
  return `${LEGACY_RAW_SOURCE_INDEX_SCHEDULE_KEY_PREFIX}:${encodeURIComponent(sourceType)}:${encodeURIComponent(sourceId)}:${encodeURIComponent(rootPath)}`
}

function resolveDefaultStorage(): RawSourceIndexStorage | null {
  try {
    return globalThis.localStorage ?? null
  }
  catch {
    return null
  }
}

function isScheduleRecord(
  value: unknown,
  sourceId: string,
  sourceType: RawFileSourceType,
  rootPath: string,
  scanKind: RawSourceScanKind,
): value is RawSourceIndexScheduleRecord {
  if (!value || typeof value !== 'object')
    return false
  const record = value as Record<string, unknown>
  return record.version === 2
    && record.sourceId === sourceId
    && record.sourceType === sourceType
    && record.rootPath === rootPath
    && record.scanKind === scanKind
}

function isLegacyScheduleRecord(
  value: unknown,
  sourceId: string,
  sourceType: RawFileSourceType,
  rootPath: string,
): value is RawSourceIndexLegacyScheduleRecord {
  if (!value || typeof value !== 'object')
    return false
  const record = value as Record<string, unknown>
  return record.version === 1
    && record.sourceId === sourceId
    && record.sourceType === sourceType
    && record.rootPath === rootPath
}

function latestTimestamp(...values: Array<string | undefined>): number | null {
  const timestamps = values
    .map(value => value ? Date.parse(value) : Number.NaN)
    .filter(value => Number.isFinite(value))
  if (timestamps.length === 0)
    return null
  return Math.max(...timestamps)
}

function latestIso(...values: Array<string | undefined>): string | undefined {
  const timestamp = latestTimestamp(...values)
  return timestamp == null ? undefined : new Date(timestamp).toISOString()
}
