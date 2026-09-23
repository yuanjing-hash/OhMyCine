import assert from 'node:assert/strict'
import type { DataSourceConfig } from '../src/services/datasource/types'

const values = new Map<string, string>()
const storage: Storage = {
  get length() { return values.size },
  clear() { values.clear() },
  getItem: key => values.get(key) ?? null,
  key: index => [...values.keys()][index] ?? null,
  removeItem: key => { values.delete(key) },
  setItem: (key, value) => { values.set(key, value) },
}
Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
Object.defineProperty(globalThis, 'window', { configurable: true, value: { dispatchEvent: () => true } })
const { initializeAppSettings, getAppSetting, setAppSetting, flushAppSettings } = await import('../src/services/appSettings.ts')
await initializeAppSettings()

const legacy = [
  { id: 'old-webdav', type: 'webdav', name: 'Old WebDAV', url: 'https://old.example.test', order: 0, extra: { credentialRef: 'custom:old-webdav' } },
  { id: 'old-local', type: 'local', name: 'Old folder', url: 'D:/Movies', order: 1 },
] as DataSourceConfig[]
await setAppSetting('ohmycine-navigation-shortcuts-v1', JSON.stringify({ home: 'Alt+KeyH', 'source:old-webdav': 'Alt+KeyW', 'source:server-live': 'Alt+KeyS' }))
await setAppSetting('ohmycine-home-contribution-preferences-v1', JSON.stringify({
  'old-webdav': { enabled: true, order: 0, placement: 'content' },
  'old-webdav\u0000row': { enabled: true, order: 1, placement: 'hero' },
  'server-live': { enabled: true, order: 2, placement: 'hero' },
}))
await setAppSetting('ohmycine-media-tombstones-v1', JSON.stringify(['old-webdav\u0000movie', 'server-live\u0000film']))
await setAppSetting('ohmycine-tmdb-settings-v1', '{"enabled":true}')
await setAppSetting('ohmycine-scrape-classification-rules', '{"version":1}')
await setAppSetting('ohmycine-raw-source-index-schedule-v2:old-webdav', '{"version":2}')
await setAppSetting('ohmycine-media-display-cache-v1', '{"old":true}')
await setAppSetting('ohmycine-offline-completed-fixture', 'preserve')
storage.setItem('ohmycine-raw-source-scan-cache-v1:old-webdav', 'old-cache')

const calls: Array<{ command: string, args: Record<string, unknown> }> = []
const members = [
  { sourceId: 'old-webdav', itemId: 'obsolete' },
  { sourceId: 'server-live', itemId: 'kept' },
]
let failHistoryOnce = true
Object.assign(globalThis.window, {
  __TAURI_INTERNALS__: {
    invoke: async (command: string, args: Record<string, unknown> = {}) => {
      calls.push({ command, args })
      if (command === 'player_delete_playback_history_for_source') {
        if (failHistoryOnce) {
          failHistoryOnce = false
          throw new Error('fixture transient native failure')
        }
        return 1
      }
      if (command === 'player_delete_media_playback_preferences_for_source')
        return 1
      if (command === 'player_list_media_collections')
        return [{ id: 'collection', members: [...members] }]
      if (command === 'player_remove_media_collection_member') {
        const index = members.findIndex(member => member.sourceId === args.sourceId && member.itemId === args.itemId)
        if (index >= 0)
          members.splice(index, 1)
        return true
      }
      if (command === 'credential_delete')
        return null
      throw new Error('unexpected native command: ' + command)
    },
  },
})

const { queueLegacySourceCleanup, runLegacySourceCleanup } = await import('../src/services/legacySourceMigration.ts')
await queueLegacySourceCleanup(legacy)
await assert.rejects(runLegacySourceCleanup(), /下次启动将重试/)
assert.ok(getAppSetting('ohmycine-player-legacy-source-cleanup-v1'), 'failed native cleanup must retain a retry marker')
await runLegacySourceCleanup()
await runLegacySourceCleanup()
await flushAppSettings()

assert.equal(getAppSetting('ohmycine-player-legacy-source-cleanup-v1'), null)
assert.equal(getAppSetting('ohmycine-tmdb-settings-v1'), null)
assert.equal(getAppSetting('ohmycine-scrape-classification-rules'), null)
assert.equal(getAppSetting('ohmycine-raw-source-index-schedule-v2:old-webdav'), null)
assert.equal(getAppSetting('ohmycine-media-display-cache-v1'), null)
assert.equal(storage.getItem('ohmycine-raw-source-scan-cache-v1:old-webdav'), null)
assert.equal(getAppSetting('ohmycine-offline-completed-fixture'), 'preserve')
assert.deepEqual(members, [{ sourceId: 'server-live', itemId: 'kept' }])
assert.deepEqual(JSON.parse(getAppSetting('ohmycine-media-tombstones-v1') ?? '[]'), ['server-live\u0000film'])
assert.deepEqual(Object.keys(JSON.parse(getAppSetting('ohmycine-home-contribution-preferences-v1') ?? '{}')), ['server-live'])
assert.deepEqual(Object.keys(JSON.parse(getAppSetting('ohmycine-navigation-shortcuts-v1') ?? '{}')), ['home', 'source:server-live'])
assert.deepEqual(
  calls.filter(call => call.command === 'player_delete_playback_history_for_source').map(call => call.args.sourceId),
  ['old-webdav', 'old-webdav', 'old-local'],
)
assert.deepEqual(
  calls.filter(call => call.command === 'credential_delete').map(call => call.args.refName),
  ['custom:old-webdav', 'datasource:old-webdav:webdav-credential', 'datasource:old-local:local-credential', 'settings:tmdb-credential'],
)
assert.equal(calls.some(call => String(call.command).includes('download')), false, 'migration must leave completed offline packages alone')
storage.setItem('ohmycine-raw-source-scan-cache-v1:orphan', 'orphan-cache')
await queueLegacySourceCleanup([])
assert.ok(getAppSetting('ohmycine-player-legacy-source-cleanup-v1'))
await runLegacySourceCleanup()
assert.equal(storage.getItem('ohmycine-raw-source-scan-cache-v1:orphan'), null)
console.log('legacy source cleanup retry, isolation, credentials, caches, and idempotency verified')
