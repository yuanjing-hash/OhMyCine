import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const rust = readFileSync(resolve(root, 'src-tauri/src/commands/downloads.rs'), 'utf8')
const bridge = readFileSync(resolve(root, 'src-tauri/src/commands/download_android.rs'), 'utf8')
const plugin = readFileSync(resolve(root, 'src-tauri/gen/android/app/src/main/java/com/ohmycine/player/downloads/DownloadPlugin.kt'), 'utf8')
const service = readFileSync(resolve(root, 'src-tauri/gen/android/app/src/main/java/com/ohmycine/player/downloads/DownloadService.kt'), 'utf8')
const manifest = readFileSync(resolve(root, 'src-tauri/gen/android/app/src/main/AndroidManifest.xml'), 'utf8')
const adapter = readFileSync(resolve(root, 'src/services/mediaActions/downloadAdapter.ts'), 'utf8')
const settings = readFileSync(resolve(root, 'src/views/SettingsView.vue'), 'utf8')

assert.match(rust, /player_download_pick_directory/)
assert.match(rust, /execute_android_task/)
assert.match(rust, /is_android_tree_uri/)
assert.match(rust, /resolve_alist/)
assert.doesNotMatch(rust, /Android downloads require a persistent writable SAF folder/)
assert.match(bridge, /run_mobile_plugin_async/)
assert.match(plugin, /ACTION_OPEN_DOCUMENT_TREE/)
assert.match(plugin, /takePersistableUriPermission/)
assert.match(plugin, /FLAG_GRANT_WRITE_URI_PERMISSION/)
assert.match(plugin, /FLAG_DIR_SUPPORTS_CREATE/)
assert.match(plugin, /ohmycine-part/)
assert.match(plugin, /openOutputStream\(uri, if \(args\.truncate\) "wt" else "wa"\)/)
assert.match(plugin, /entityHash.*documentEntityHash/)
assert.match(plugin, /COLUMN_LAST_MODIFIED/)
assert.match(plugin, /授权已失效，请重新选择目录/)
assert.match(rust, /stored_entity.*entity_hash/s)
assert.doesNotMatch(rust, /android-saf-local-v1/)
assert.match(rust, /let mut resume_pending = offset > 0/)
assert.match(rust, /resume_pending = false/)
assert.match(service, /startForeground\(id, notification\)/)
assert.match(service, /activeNotifications = linkedMapOf/)
assert.match(service, /activeNotifications\.remove\(id\)/)
assert.match(service, /if \(foregroundId == null\)[\s\S]*startForeground\(id, notification\)/)
assert.match(service, /startForeground\(replacement\.key, replacement\.value\)/)
assert.match(manifest, /FOREGROUND_SERVICE_DATA_SYNC/)
assert.match(manifest, /foregroundServiceType="dataSync"/)
assert.match(adapter, /pickAndroidDownloadDirectory\(false\)/)
assert.doesNotMatch(adapter, /isNativeAndroidRuntime\(\)[\s\S]{0,160}availability: 'disabled'/)
assert.match(settings, /pickAndroidDownloadDirectory\(true\)/)

console.log(JSON.stringify({
  androidSafPersistentGrant: true,
  androidDownloadToOneShotGrant: true,
  androidForegroundNotification: true,
  revokedGrantRecovery: true,
  sensitiveTransportRemainsNative: true,
}, null, 2))
