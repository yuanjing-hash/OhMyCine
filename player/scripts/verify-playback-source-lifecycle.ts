import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { getPlaybackMediaContext, savePlaybackMediaContext } from '../src/services/playbackContext.ts'

const viewFiles = [
  '../src/views/HomeView.vue',
  '../src/views/MediaDetailView.vue',
  '../src/views/SourceLibraryView.vue',
  '../src/views/PlayerView.vue',
]

for (const relativePath of viewFiles) {
  const absolutePath = fileURLToPath(new URL(relativePath, import.meta.url))
  const source = await readFile(absolutePath, 'utf8')
  const playerNavigations = [...source.matchAll(/name:\s*'player',\s*query:\s*\{([\s\S]*?)\n\s*\},\n\s*\}\)/g)]

  for (const navigation of playerNavigations)
    assert.doesNotMatch(navigation[1], /\bpath\s*:/, `${relativePath} must not persist playback paths in route query`)
}

const localPath = 'C:\\Media\\Private\\Movie.mkv'
const localContextId = savePlaybackMediaContext({
  sourceId: 'local-file',
  itemId: 'local-movie',
  locator: { kind: 'localPath', path: localPath },
})
const localContext = getPlaybackMediaContext(localContextId)
assert.deepEqual(localContext?.locator, { kind: 'localPath', path: localPath })

const embyContextId = savePlaybackMediaContext({
  sourceId: 'emby-home',
  itemId: 'movie-1',
  mediaSourceId: 'version-4k',
})
const embyContext = getPlaybackMediaContext(embyContextId)
assert.deepEqual(embyContext?.locator, {
  kind: 'dataSource',
  sourceId: 'emby-home',
  itemId: 'movie-1',
  mediaSourceId: 'version-4k',
})

const embySource = await readFile(fileURLToPath(new URL('../src/services/datasource/emby.ts', import.meta.url)), 'utf8')
assert.match(embySource, /mediaSources\.filter\(source => source\.Id === requestedMediaSourceId\)/)
assert.match(embySource, /所选 Emby 媒体版本已不可用/)

const historyCommand = await readFile(fileURLToPath(new URL('../src-tauri/src/commands/history.rs', import.meta.url)), 'utf8')
assert.match(historyCommand, /DELETE FROM playback_history WHERE source_id = \?1/)

console.log(JSON.stringify({
  checkedViews: viewFiles.length,
  localPathStoredOnlyInMemory: true,
  embyMediaSourceId: embyContext?.mediaSourceId,
  sourceScopedHistoryDelete: true,
}, null, 2))
