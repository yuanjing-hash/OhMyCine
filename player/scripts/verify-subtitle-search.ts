import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const read = (path: string) => readFile(fileURLToPath(new URL(path, root)), 'utf8')

const controls = await read('src/components/player/PlayerControls.vue')
assert.match(controls, /搜索字幕/)
assert.match(controls, /emit\('searchSubtitles'\)/)

const playerView = await read('src/views/PlayerView.vue')
assert.match(playerView, /subtitleSearchRequiresSourceChoice\.value = sourceType === 'emby'/)
assert.match(playerView, /subtitleSearchOrigin\.value = sourceType === 'emby' \? null : 'local'/)
assert.match(playerView, /if \(origin === 'emby'\)[\s\S]*source\.searchSubtitles/)
assert.match(playerView, /else \{[\s\S]*searchLocalSubtitles/)

const emby = await read('src/services/datasource/emby.ts')
assert.match(emby, /RemoteSearch\/Subtitles\/\$\{encodeURIComponent\(language\)\}/)
assert.match(emby, /postPlaybackJson\([\s\S]*RemoteSearch\/Subtitles/)
assert.match(emby, /this\.cache\.clear\(\)/)

const settings = await read('src/services/subtitle/settings.ts')
assert.match(settings, /saveOpenSubtitlesCredential/)
assert.doesNotMatch(settings, /setAppSetting\([^)]*apiKey/)

const rust = await read('src-tauri/src/commands/subtitle.rs')
assert.match(rust, /host == "opensubtitles\.com" \|\| host\.ends_with\("\.opensubtitles\.com"\)/)
assert.match(rust, /layout\.cache_dir\.join\("subtitles"\)/)
assert.match(rust, /MAX_DOWNLOAD_RESPONSE_BYTES/)

console.log(JSON.stringify({
  embyRequiresExplicitOriginChoice: true,
  nonEmbyUsesLocalSearchDirectly: true,
  credentialsUseSecureStore: true,
  downloadsAreConstrainedToSubtitleCache: true,
}, null, 2))
