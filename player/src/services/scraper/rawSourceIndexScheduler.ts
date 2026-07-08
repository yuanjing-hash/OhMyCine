import type { RawLocalScanCache, RawLocalScanLogEntry, RunRawSourceScanInput } from './localScanCache'
import type { RawFileSourceType } from './types'
import type { DataSource, DataSourceConfig } from '@/services/datasource/types'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { loadRawSourceScanCache, runRawSourceLocalScan } from './localScanCache'
import { normalizeProviderPath } from './pathUtils'

export const DEFAULT_RAW_SOURCE_INDEX_INTERVAL_MS = 6 * 60 * 60 * 1000

export type RawSourceIndexStatusKind = 'idle' | 'cooldown' | 'queued' | 'running' | 'completed' | 'failed'

export interface RawSourceIndexTarget {
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
  readonly source: Pick<DataSource, 'list'>
}

export interface RawSourceIndexStatus {
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
  readonly state: RawSourceIndexStatusKind
  readonly lastAttemptAt?: string
  readonly lastSuccessAt?: string
  readonly lastFailureAt?: string
  readonly nextAllowedAt?: string
  readonly errorMessage?: string
}

export interface RawSourceIndexTriggerResult extends RawSourceIndexStatus {
  readonly skipped: boolean
  readonly cache?: RawLocalScanCache
}

export interface RawSourceIndexManualScanOptions {
  readonly onLog?: (entry: RawLocalScanLogEntry) => void
}

export interface RawSourceAutoIndexingInput {
  readonly getTargets: () => RawSourceIndexTarget[] | Promise<RawSourceIndexTarget[]>
  readonly intervalMs?: number
}

export interface RawSourceIndexSchedulerOptions {
  readonly intervalMs?: number
  readonly now?: () => number
  readonly storage?: RawSourceIndexStorage | null
  readonly scanRunner?: (input: RunRawSourceScanInput) => Promise<RawLocalScanCache>
}

interface RawSourceIndexScheduleRecord {
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

const RAW_SOURCE_INDEX_SCHEDULE_KEY_PREFIX = 'ohmycine-raw-source-index-schedule-v1'

export class RawSourceIndexScheduler {
  private readonly statuses = new Map<string, RawSourceIndexStatus>()
  private readonly inFlight = new Map<string, Promise<RawLocalScanCache>>()
  private readonly listeners = new Set<RawSourceIndexListener>()
  private readonly now: () => number
  private readonly storage: RawSourceIndexStorage | null
  private readonly scanRunner: (input: RunRawSourceScanInput) => Promise<RawLocalScanCache>
  private readonly defaultIntervalMs: number
  private autoIndexInput: RawSourceAutoIndexingInput | null = null
  private autoIndexTimer: ReturnType<typeof setInterval> | null = null
  private autoIndexCycleRunning = false

  constructor(options: RawSourceIndexSchedulerOptions = {}) {
    this.defaultIntervalMs = options.intervalMs ?? DEFAULT_RAW_SOURCE_INDEX_INTERVAL_MS
    this.now = options.now ?? (() => Date.now())
    this.storage = options.storage === undefined ? resolveDefaultStorage() : options.storage
    this.scanRunner = options.scanRunner ?? runRawSourceLocalScan
  }

  subscribe(listener: RawSourceIndexListener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  startAutoIndexing(input: RawSourceAutoIndexingInput): void {
    this.autoIndexInput = input
    const intervalMs = input.intervalMs ?? this.defaultIntervalMs

    if (!this.autoIndexTimer) {
      this.autoIndexTimer = setInterval(() => {
        void this.runAutoIndexCycle()
      }, intervalMs)
    }

    void this.runAutoIndexCycle()
  }

  stopAutoIndexing(): void {
    if (this.autoIndexTimer)
      clearInterval(this.autoIndexTimer)
    this.autoIndexTimer = null
    this.autoIndexInput = null
  }

  async triggerAutoIndexForTargets(
    targets: readonly RawSourceIndexTarget[],
    options: { intervalMs?: number } = {},
  ): Promise<RawSourceIndexTriggerResult[]> {
    const normalizedTargets = uniqueTargets(targets.map(normalizeTarget))
    const intervalMs = options.intervalMs ?? this.defaultIntervalMs
    const settled = await Promise.allSettled(
      normalizedTargets.map(target => this.triggerAutoIndexForTarget(target, intervalMs)),
    )

    return settled.map((result, index) => {
      if (result.status === 'fulfilled')
        return result.value

      const target = normalizedTargets[index]
      const status = this.failedStatus(target, result.reason)
      this.setStatus(status)
      return {
        ...status,
        skipped: false,
      }
    })
  }

  async forceScan(target: RawSourceIndexTarget, options: RawSourceIndexManualScanOptions = {}): Promise<RawLocalScanCache> {
    return this.runScan(normalizeTarget(target), {
      onLog: options.onLog,
      throwOnFailure: true,
    })
  }

  async getStatus(input: {
    readonly sourceId: string
    readonly sourceType: RawFileSourceType
    readonly rootPath?: string
  }): Promise<RawSourceIndexStatus> {
    const rootPath = normalizeProviderPath(input.rootPath)
    const key = sourceIndexKey(input.sourceId, input.sourceType, rootPath)
    const current = this.statuses.get(key)
    if (current)
      return current

    const record = this.readScheduleRecord(input.sourceId, input.sourceType, rootPath)
    const cache = await loadRawSourceScanCache(input.sourceId, input.sourceType, rootPath)
    const lastAttemptAt = latestIso(record?.lastAttemptAt, cache?.finishedAt)
    const lastSuccessAt = latestIso(record?.lastSuccessAt, cache?.finishedAt)
    if (lastSuccessAt) {
      return {
        sourceId: input.sourceId,
        sourceType: input.sourceType,
        rootPath,
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
      state: 'idle',
      lastAttemptAt,
    }
  }

  private async runAutoIndexCycle(): Promise<void> {
    if (!this.autoIndexInput || this.autoIndexCycleRunning)
      return

    this.autoIndexCycleRunning = true
    try {
      const targets = await this.autoIndexInput.getTargets()
      await this.triggerAutoIndexForTargets(targets, {
        intervalMs: this.autoIndexInput.intervalMs,
      })
    }
    catch {
      // Auto indexing is a background enhancement and must never surface through app startup.
    }
    finally {
      this.autoIndexCycleRunning = false
    }
  }

  private async triggerAutoIndexForTarget(target: RawSourceIndexTarget, intervalMs: number): Promise<RawSourceIndexTriggerResult> {
    const key = sourceIndexTargetKey(target)
    if (this.inFlight.has(key)) {
      const status: RawSourceIndexStatus = {
        ...this.baseStatus(target),
        state: 'running',
      }
      this.setStatus(status)
      return {
        ...status,
        skipped: true,
      }
    }

    const cooldown = await this.cooldownStatus(target, intervalMs)
    if (cooldown) {
      this.setStatus(cooldown)
      return {
        ...cooldown,
        skipped: true,
      }
    }

    try {
      const cache = await this.runScan(target, { throwOnFailure: false })
      const status = await this.getStatus(target)
      return {
        ...status,
        skipped: false,
        cache,
      }
    }
    catch (error) {
      const status = this.failedStatus(target, error)
      this.setStatus(status)
      return {
        ...status,
        skipped: false,
      }
    }
  }

  private async runScan(
    target: RawSourceIndexTarget,
    options: { onLog?: (entry: RawLocalScanLogEntry) => void, throwOnFailure: boolean },
  ): Promise<RawLocalScanCache> {
    const key = sourceIndexTargetKey(target)
    const active = this.inFlight.get(key)
    if (active)
      return active

    const startedAt = this.isoNow()
    this.writeScheduleRecord({
      ...this.readScheduleRecord(target.sourceId, target.sourceType, target.rootPath),
      version: 1,
      sourceId: target.sourceId,
      sourceType: target.sourceType,
      rootPath: target.rootPath,
      lastAttemptAt: startedAt,
    })
    this.setStatus({
      ...this.baseStatus(target),
      state: 'running',
      lastAttemptAt: startedAt,
    })

    const scan = Promise.resolve().then(() => this.scanRunner({
      source: target.source,
      sourceId: target.sourceId,
      sourceType: target.sourceType,
      rootPath: target.rootPath,
      onLog: options.onLog,
    }))

    this.inFlight.set(key, scan)

    try {
      const cache = await scan
      const finishedAt = cache.finishedAt || this.isoNow()
      this.writeScheduleRecord({
        version: 1,
        sourceId: target.sourceId,
        sourceType: target.sourceType,
        rootPath: target.rootPath,
        lastAttemptAt: startedAt,
        lastSuccessAt: finishedAt,
      })
      this.setStatus({
        ...this.baseStatus(target),
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
        version: 1,
        sourceId: target.sourceId,
        sourceType: target.sourceType,
        rootPath: target.rootPath,
        lastAttemptAt: startedAt,
        lastFailureAt: failedAt,
        errorMessage,
      })
      this.setStatus({
        ...this.baseStatus(target),
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
      if (this.inFlight.get(key) === scan)
        this.inFlight.delete(key)
    }
  }

  private async cooldownStatus(target: RawSourceIndexTarget, intervalMs: number): Promise<RawSourceIndexStatus | null> {
    if (intervalMs <= 0)
      return null

    const record = this.readScheduleRecord(target.sourceId, target.sourceType, target.rootPath)
    const cache = await loadRawSourceScanCache(target.sourceId, target.sourceType, target.rootPath)
    const lastAttemptMs = latestTimestamp(record?.lastAttemptAt, cache?.finishedAt)
    if (lastAttemptMs == null)
      return null

    const nextAllowedMs = lastAttemptMs + intervalMs
    if (this.now() >= nextAllowedMs)
      return null

    return {
      ...this.baseStatus(target),
      state: 'cooldown',
      lastAttemptAt: latestIso(record?.lastAttemptAt, cache?.finishedAt),
      lastSuccessAt: latestIso(record?.lastSuccessAt, cache?.finishedAt),
      lastFailureAt: record?.lastFailureAt,
      nextAllowedAt: new Date(nextAllowedMs).toISOString(),
      errorMessage: record?.errorMessage,
    }
  }

  private baseStatus(target: Pick<RawSourceIndexTarget, 'sourceId' | 'sourceType' | 'rootPath'>): Pick<RawSourceIndexStatus, 'sourceId' | 'sourceType' | 'rootPath'> {
    return {
      sourceId: target.sourceId,
      sourceType: target.sourceType,
      rootPath: target.rootPath,
    }
  }

  private failedStatus(target: Pick<RawSourceIndexTarget, 'sourceId' | 'sourceType' | 'rootPath'>, error: unknown): RawSourceIndexStatus {
    return {
      ...this.baseStatus(target),
      state: 'failed',
      lastFailureAt: this.isoNow(),
      errorMessage: toSafeErrorMessage(error, '后台索引失败，文件夹浏览和其他数据源不受影响。'),
    }
  }

  private setStatus(status: RawSourceIndexStatus): void {
    this.statuses.set(sourceIndexKey(status.sourceId, status.sourceType, status.rootPath), status)
    for (const listener of this.listeners)
      listener(status)
  }

  private readScheduleRecord(sourceId: string, sourceType: RawFileSourceType, rootPath: string): RawSourceIndexScheduleRecord | null {
    if (!this.storage)
      return null

    try {
      const raw = this.storage.getItem(scheduleKey(sourceId, sourceType, rootPath))
      if (!raw)
        return null
      const value = JSON.parse(raw) as unknown
      return isScheduleRecord(value, sourceId, sourceType, rootPath) ? value : null
    }
    catch {
      return null
    }
  }

  private writeScheduleRecord(record: RawSourceIndexScheduleRecord): void {
    if (!this.storage)
      return

    try {
      this.storage.setItem(scheduleKey(record.sourceId, record.sourceType, record.rootPath), JSON.stringify(record))
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
    .filter(config => config.enabled !== false && config.type === 'alist')
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
  if (config.enabled === false || config.type !== 'alist')
    return null

  return normalizeTarget({
    sourceId: config.id,
    sourceType: 'alist',
    rootPath: readRawSourceRootPath(config),
    source,
  })
}

export function readRawSourceRootPath(config: DataSourceConfig): string {
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
  }
}

function uniqueTargets(targets: readonly RawSourceIndexTarget[]): RawSourceIndexTarget[] {
  const map = new Map<string, RawSourceIndexTarget>()
  for (const target of targets)
    map.set(sourceIndexTargetKey(target), target)
  return [...map.values()]
}

function sourceIndexTargetKey(target: Pick<RawSourceIndexTarget, 'sourceId' | 'sourceType' | 'rootPath'>): string {
  return sourceIndexKey(target.sourceId, target.sourceType, target.rootPath)
}

function sourceIndexKey(sourceId: string, sourceType: RawFileSourceType, rootPath: string): string {
  return `${sourceType}:${sourceId}:${rootPath}`
}

function scheduleKey(sourceId: string, sourceType: RawFileSourceType, rootPath: string): string {
  return `${RAW_SOURCE_INDEX_SCHEDULE_KEY_PREFIX}:${encodeURIComponent(sourceType)}:${encodeURIComponent(sourceId)}:${encodeURIComponent(rootPath)}`
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
): value is RawSourceIndexScheduleRecord {
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
