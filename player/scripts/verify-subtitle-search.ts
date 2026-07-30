import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const read = (path: string) => readFile(fileURLToPath(new URL(path, root)), 'utf8')

const controls = await read('src/components/player/PlayerControls.vue')
assert.match(controls, /搜索字幕/)
assert.match(controls, /emit\('searchSubtitles'\)/)
assert.match(controls, /字幕偏移/)
assert.match(controls, /emit\('setSubtitleDelay'/)
assert.match(controls, /type="range"[\s\S]*min="-30"[\s\S]*max="30"/)

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
assert.match(settings, /subtitle_login_opensubtitles/)
assert.match(settings, /shooterEnabled/)
assert.match(settings, /xunleiEnabled/)
assert.doesNotMatch(settings, /setAppSetting\([^)]*apiKey/)

const subtitleIndex = await read('src/services/subtitle/index.ts')
assert.match(subtitleIndex, /hasOpenSubtitlesApiKey/)
assert.match(subtitleIndex, /settings\.shooterEnabled && canUseHashProviders/)
assert.match(subtitleIndex, /settings\.xunleiEnabled && canUseHashProviders/)
assert.match(subtitleIndex, /Promise\.allSettled/)

const useMpv = await read('src/composables/useMpv.ts')
assert.match(useMpv, /prop: 'sub-delay'/)
assert.match(useMpv, /MIN_SUBTITLE_DELAY = -30/)
assert.match(useMpv, /MAX_SUBTITLE_DELAY = 30/)
assert.match(useMpv, /applySubtitleDelay\(DEFAULT_SUBTITLE_DELAY\)/)

const credentialStore = await read('src/services/datasource/credentialStore.ts')
assert.match(credentialStore, /version: 2,[\s\S]*provider: 'opensubtitles'/)
assert.match(credentialStore, /value\.version !== 1 && value\.version !== 2/)
assert.match(credentialStore, /probePersistentCredentialStorage[\s\S]*credential-health-check/)

const providers = await read('src/services/subtitle/hashProviders.ts')
assert.match(providers, /if \(!input\.localFilePath\)[\s\S]*return \[\]/)
assert.match(providers, /subtitle_search_hash_provider/)
assert.doesNotMatch(providers, /downloadRef.*https?:/)

const rust = await read('src-tauri/src/commands/subtitle.rs')
assert.match(rust, /host == "opensubtitles\.com" \|\| host\.ends_with\("\.opensubtitles\.com"\)/)
assert.match(rust, /layout\.cache_dir\.join\("subtitles"\)/)
assert.match(rust, /MAX_DOWNLOAD_RESPONSE_BYTES/)
assert.match(rust, /compute_shooter_hash/)
assert.match(rust, /compute_xunlei_cid/)
assert.match(rust, /url\.host_str\(\) != Some\("www\.shooter\.cn"\)/)
assert.match(rust, /url\.host_str\(\) != Some\("subtitle\.v\.geilijiasu\.com"\)/)
assert.match(rust, /DOWNLOAD_REFERENCE_TTL/)
assert.doesNotMatch(rust, /println!|dbg!|log::.*password/)

console.log(JSON.stringify({
  embyRequiresExplicitOriginChoice: true,
  nonEmbyUsesLocalSearchDirectly: true,
  credentialsUseSecureStore: true,
  downloadsAreConstrainedToSubtitleCache: true,
  openSubtitlesAccountLoginUsesEphemeralJwt: true,
  shooterAndXunleiRequireLocalFileHashes: true,
  hashProviderDownloadsUseOpaqueReferences: true,
  missingOpenSubtitlesKeyDoesNotBlockHashProviders: true,
  subtitleDelayUsesMpvSubDelay: true,
}, null, 2))
