import assert from 'node:assert/strict'
import { createRawSourceAutoIndexTargets, createRawSourceIndexScheduler, DEFAULT_RAW_SOURCE_INCREMENTAL_INDEX_INTERVAL_MS, DEFAULT_RAW_SOURCE_INDEX_INTERVAL_MS } from '../src/services/scraper/rawSourceIndexScheduler.ts'
import { readRawSourceScanScheduleConfig } from '../src/services/scraper/rawSourceScanSchedule.ts'
import type { DataSource, DataSourceConfig } from '../src/services/datasource/types.ts'
import type { RawFileSourceType, RawScanPreview } from '../src/services/scraper/types.ts'
import type { RawLocalScanCache, RunRawSourceScanInput } from '../src/services/scraper/localScanCache.ts'

class MemoryStorage {
  private readonly values = new Map<string, string>()

  getItem(key: string): string | null {
    return this.values.get(key) ?? null
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value)
  }
}

const storage = new MemoryStorage()
let now = Date.parse('2026-05-25T00:00:00.000Z')
const scans: string[] = []
const source = { list: async () => [] } satisfies Pick<DataSource, 'list'>
const scheduler = createRawSourceIndexScheduler({
  intervalMs: DEFAULT_RAW_SOURCE_INDEX_INTERVAL_MS,
  storage,
  now: () => now,
  scanRunner: async (input) => {
    scans.push(`${input.sourceType}:${input.sourceId}:${input.rootPath}`)
    return createScanCache(input)
  },
})

const first = await scheduler.triggerAutoIndexForTargets([
  {
    sourceId: 'alist-one',
    sourceType: 'alist',
    rootPath: '/影视库',
    source,
  },
])

const repeated = await scheduler.triggerAutoIndexForTargets([
  {
    sourceId: 'alist-one',
    sourceType: 'alist',
    rootPath: '/影视库',
    source,
  },
])

const independent = await scheduler.triggerAutoIndexForTargets([
  {
    sourceId: 'alist-two',
    sourceType: 'alist',
    rootPath: '/影视库',
    source,
  },
])

now += DEFAULT_RAW_SOURCE_INDEX_INTERVAL_MS + 1
const afterCooldown = await scheduler.triggerAutoIndexForTargets([
  {
    sourceId: 'alist-one',
    sourceType: 'alist',
    rootPath: '/影视库',
    source,
  },
])

assert.equal(first[0]?.state, 'completed')
assert.equal(first[0]?.skipped, false)
assert.equal(repeated[0]?.state, 'cooldown')
assert.equal(repeated[0]?.skipped, true)
assert.equal(independent[0]?.state, 'completed')
assert.equal(independent[0]?.skipped, false)
assert.equal(afterCooldown[0]?.state, 'completed')
assert.deepEqual(scans, [
  'alist:alist-one:/影视库',
  'alist:alist-two:/影视库',
  'alist:alist-one:/影视库',
])

const foregroundStatuses: string[] = []
let finishForegroundScan: ((cache: RawLocalScanCache) => void) | undefined
const foregroundScheduler = createRawSourceIndexScheduler({
  storage: new MemoryStorage(),
  now: () => now,
  scanRunner: async input => new Promise<RawLocalScanCache>((resolve) => {
    scans.push(`${input.sourceType}:${input.sourceId}:${input.rootPath}:foreground`)
    finishForegroundScan = resolve
  }),
})
foregroundScheduler.subscribe(status => foregroundStatuses.push(status.state))
const foregroundTarget = {
  sourceId: 'local-first-open',
  sourceType: 'local' as const,
  rootPath: '/新媒体库',
  source,
}
const foregroundScan = foregroundScheduler.forceScan(foregroundTarget)
const runningStatus = await foregroundScheduler.getStatus({
  sourceId: foregroundTarget.sourceId,
  sourceType: foregroundTarget.sourceType,
  rootPath: foregroundTarget.rootPath,
})
const runningTrigger = await foregroundScheduler.triggerAutoIndexForTargets([foregroundTarget])
assert.equal(foregroundStatuses.at(-1), 'running')
assert.equal(runningStatus.state, 'running')
assert.equal(runningTrigger[0]?.state, 'running')
assert.equal(runningTrigger[0]?.skipped, true)
assert.ok(finishForegroundScan)
finishForegroundScan(createScanCache({
  source,
  sourceId: foregroundTarget.sourceId,
  sourceType: foregroundTarget.sourceType,
  rootPath: foregroundTarget.rootPath,
}))
const foregroundCache = await foregroundScan
const completedStatus = await foregroundScheduler.getStatus({
  sourceId: foregroundTarget.sourceId,
  sourceType: foregroundTarget.sourceType,
  rootPath: foregroundTarget.rootPath,
})
assert.equal(foregroundCache.sourceId, foregroundTarget.sourceId)
assert.equal(foregroundStatuses.at(-1), 'completed')
assert.equal(completedStatus.state, 'completed')

const targetConfigs: DataSourceConfig[] = [
  createConfig('alist-target', 'alist', {
    rootPath: '/影视库',
    rawSourceScanSchedule: {
      full: { enabled: true, intervalMs: DEFAULT_RAW_SOURCE_INDEX_INTERVAL_MS },
      incremental: { enabled: true, intervalMs: DEFAULT_RAW_SOURCE_INCREMENTAL_INDEX_INTERVAL_MS },
    },
  }),
  createConfig('local-target', 'local', {
    rootPath: '/mnt/media',
  }),
  createConfig('emby-target', 'emby'),
]
const autoTargets = createRawSourceAutoIndexTargets(targetConfigs, () => source)
assert.deepEqual(autoTargets.map(target => target.sourceId), ['alist-target', 'local-target'])
assert.equal(readRawSourceScanScheduleConfig(targetConfigs[0]).incremental.intervalMs, DEFAULT_RAW_SOURCE_INCREMENTAL_INDEX_INTERVAL_MS)

const dualScans: string[] = []
const dualScheduler = createRawSourceIndexScheduler({
  storage: new MemoryStorage(),
  now: () => now,
  scanRunner: async (input) => {
    dualScans.push(input.scanKind ?? 'full')
    return createScanCache(input)
  },
  incrementalScanRunner: async (input) => {
    dualScans.push(input.scanKind ?? 'incremental')
    return createScanCache(input)
  },
})
const dualTarget = autoTargets[0]!
const dualFull = await dualScheduler.triggerAutoIndexForTargets([dualTarget], { scanKind: 'full' })
const dualIncrementalFirst = await dualScheduler.triggerAutoIndexForTargets([dualTarget], { scanKind: 'incremental' })
const dualIncrementalCooldown = await dualScheduler.triggerAutoIndexForTargets([dualTarget], { scanKind: 'incremental' })
now += DEFAULT_RAW_SOURCE_INCREMENTAL_INDEX_INTERVAL_MS + 1
const dualIncremental = await dualScheduler.triggerAutoIndexForTargets([dualTarget], { scanKind: 'incremental' })
assert.equal(dualFull[0]?.state, 'completed')
assert.equal(dualIncrementalFirst[0]?.state, 'completed')
assert.equal(dualIncrementalCooldown[0]?.state, 'cooldown')
assert.equal(dualIncremental[0]?.state, 'completed')
assert.deepEqual(dualScans, ['full', 'incremental', 'incremental'])

const disabledIncrementalTarget = {
  ...dualTarget,
  sourceId: 'incremental-disabled',
  schedule: {
    ...dualTarget.schedule!,
    incremental: { enabled: false, intervalMs: DEFAULT_RAW_SOURCE_INCREMENTAL_INDEX_INTERVAL_MS },
  },
}
const disabledIncremental = await dualScheduler.triggerAutoIndexForTargets([disabledIncrementalTarget], {
  scanKind: 'incremental',
  respectSchedule: true,
})
assert.equal(disabledIncremental[0]?.state, 'disabled')
assert.equal(disabledIncremental[0]?.skipped, true)

const dirtyScans: string[] = []
const dirtyScheduler = createRawSourceIndexScheduler({
  storage: new MemoryStorage(),
  now: () => now,
  dirtyDebounceMs: 0,
  incrementalScanRunner: async (input) => {
    dirtyScans.push(`${input.scanKind}:${input.sourceId}`)
    return createScanCache(input)
  },
})
dirtyScheduler.markIncrementalDirty(dualTarget)
await new Promise(resolve => setTimeout(resolve, 10))
const dirtyStatus = await dirtyScheduler.getStatus({
  sourceId: dualTarget.sourceId,
  sourceType: dualTarget.sourceType,
  rootPath: dualTarget.rootPath,
  scanKind: 'incremental',
})
assert.equal(dirtyStatus.state, 'completed')
assert.deepEqual(dirtyScans, ['incremental:alist-target'])

console.log(JSON.stringify({
  firstState: first[0]?.state,
  repeatedState: repeated[0]?.state,
  repeatedSkipped: repeated[0]?.skipped,
  independentState: independent[0]?.state,
  afterCooldownState: afterCooldown[0]?.state,
  foregroundStatuses,
  runningStatus: runningStatus.state,
  runningTriggerSkipped: runningTrigger[0]?.skipped,
  completedStatus: completedStatus.state,
  autoTargetIds: autoTargets.map(target => target.sourceId),
  dualScans,
  disabledIncrementalState: disabledIncremental[0]?.state,
  dirtyScans,
  scans,
}, null, 2))

function createScanCache(input: RunRawSourceScanInput): RawLocalScanCache {
  const startedAt = new Date(now).toISOString()
  const finishedAt = new Date(now + 1000).toISOString()
  const preview: RawScanPreview = {
    records: [],
    candidates: [],
    detection: {
      mode: 'nonStandard',
      confidence: 0.8,
      reasons: ['scheduler verify'],
      samplePaths: [],
      scores: {
        videoCount: 0,
        sampledCount: 0,
        titleYearFolder: 0,
        titleYearFile: 0,
        seasonFolder: 0,
        episodePattern: 0,
        chineseEpisodePattern: 0,
        categoryTitleSeasonHierarchy: 0,
        sameSeriesEpisodeGroups: 0,
        rootLevelVideos: 0,
        mixedFolderAmbiguity: 0,
        standardScore: 0,
        nonStandardScore: 1,
      },
    },
  }

  return {
    version: 1,
    scanId: `${input.sourceId}-${scans.length}`,
    sourceId: input.sourceId,
    sourceType: input.sourceType as RawFileSourceType,
    rootPath: input.rootPath ?? '/',
    status: 'completed',
    startedAt,
    finishedAt,
    folderCount: 0,
    fileCount: 0,
    skippedFileCount: 0,
    errorCount: 0,
    logs: [],
    ...preview,
  }
}

function createConfig(id: string, type: DataSourceConfig['type'], extra: Record<string, unknown> = {}): DataSourceConfig {
  return {
    id,
    type,
    name: id,
    displayName: id,
    order: 0,
    url: type === 'local' ? 'local://filesystem' : 'https://example.test',
    enabled: true,
    extra,
  }
}
