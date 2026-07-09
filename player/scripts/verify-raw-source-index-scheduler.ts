import assert from 'node:assert/strict'
import { createRawSourceIndexScheduler, DEFAULT_RAW_SOURCE_INDEX_INTERVAL_MS } from '../src/services/scraper/rawSourceIndexScheduler.ts'
import type { DataSource } from '../src/services/datasource/types.ts'
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
