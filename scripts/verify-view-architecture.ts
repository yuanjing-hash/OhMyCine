import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const [settingsView, playerView, sourceLibraryView, settingsOptions, playerPresentation, scannedMedia] = await Promise.all([
  source('src/views/SettingsView.vue'),
  source('src/views/PlayerView.vue'),
  source('src/views/SourceLibraryView.vue'),
  source('src/services/settingsSourceOptions.ts'),
  source('src/services/playerPresentation.ts'),
  source('src/services/sourceLibraryScannedMedia.ts'),
])

assert.ok(lineCount(settingsView) < 4_000)
assert.ok(lineCount(playerView) < 3_300)
assert.ok(lineCount(sourceLibraryView) < 2_500)
assert.match(settingsView, /@\/services\/settingsSourceOptions/)
assert.match(playerView, /@\/services\/playerPresentation/)
assert.match(sourceLibraryView, /@\/services\/sourceLibraryScannedMedia/)
assert.doesNotMatch(playerView, /function containsUnsafeDisplayToken/)
assert.doesNotMatch(sourceLibraryView, /function createScannedCategory/)
assert.doesNotMatch(settingsView, /const sourceTypeOptions: Array/)
assert.match(settingsOptions, /type: '115'/)
assert.match(settingsOptions, /available: false/)
assert.match(settingsOptions, /即将推出/)
assert.match(playerPresentation, /safePlayerMenuText/)
assert.match(scannedMedia, /createScannedCategory/)

console.log(JSON.stringify({
  settingsViewLines: lineCount(settingsView),
  playerViewLines: lineCount(playerView),
  sourceLibraryViewLines: lineCount(sourceLibraryView),
  reusableSettingsLogicExtracted: true,
  reusablePlayerPresentationExtracted: true,
  scannedLibraryDomainExtracted: true,
  planned115CardVisible: true,
}, null, 2))

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

function lineCount(value: string): number {
  return value.split('\n').length
}
