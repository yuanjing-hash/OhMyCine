import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const repositoryRoot = new URL('../../', import.meta.url)
const readPlayer = (path: string) => readFile(fileURLToPath(new URL(path, root)), 'utf8')
const readRepository = (path: string) => readFile(fileURLToPath(new URL(path, repositoryRoot)), 'utf8')

const tauriConfig = JSON.parse(await readPlayer('src-tauri/tauri.conf.json')) as Record<string, any>
const updaterConfig = JSON.parse(await readPlayer('src-tauri/tauri.updater.conf.json')) as Record<string, any>
assert.equal(typeof tauriConfig.plugins?.updater?.pubkey, 'string')
assert.equal(tauriConfig.plugins.updater.pubkey.length > 100, true)
assert.equal(updaterConfig.bundle?.createUpdaterArtifacts, true)

const rust = await readPlayer('src-tauri/src/commands/updater.rs')
assert.match(rust, /api\.github\.com\/repos\/yuanjing-hash\/OhMyCine\/releases/)
assert.match(rust, /UpdateChannel::Beta => !release\.draft/)
assert.match(rust, /UpdateChannel::Stable => !release\.draft && !release\.prerelease/)
assert.match(rust, /installer_arg\(format!\("\/D=/)
assert.match(rust, /download_and_install/)

const store = await readPlayer('src/stores/updater.ts')
assert.match(store, /let activeCheck: Promise<UpdateCheckResult> \| null = null/)
assert.match(store, /scheduleStartupCheck/)
assert.match(store, /promptOpen\.value = true/)

const settings = await readPlayer('src/views/SettingsView.vue')
assert.match(settings, /保存更新设置/)
assert.match(settings, /立即检测更新/)
assert.match(settings, /Beta/)
assert.match(settings, /正式版/)

const workflow = await readRepository('.github/workflows/player-beta-release.yml')
assert.match(workflow, /TAURI_SIGNING_PRIVATE_KEY: \$\{\{ secrets\.TAURI_SIGNING_PRIVATE_KEY \}\}/)
assert.match(workflow, /tauri\.updater\.conf\.json/)
assert.match(workflow, /setup_signature_asset/)
assert.match(workflow, /dist\/player-beta\/latest\.json/)
assert.match(workflow, /release_flags=\(\)/)
assert.match(workflow, /release-android-arm64:/)
assert.match(workflow, /npm run tauri:build:android:preview/)
assert.match(workflow, /OhMyCine-Player-v\$\{app_version\}-android-arm64\.apk/)
assert.doesNotMatch(workflow, /ohmycine-updater\.key/)

console.log(JSON.stringify({
  signedArtifactsRequired: true,
  githubRepositoryPinned: true,
  betaStableChannelsSeparated: true,
  startupAndManualChecksShareStore: true,
  portableInstallDirectoryPreserved: true,
  androidReleaseArtifactAutomated: true,
}, null, 2))
