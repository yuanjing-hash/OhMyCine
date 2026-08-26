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
const downloadBridge = readFileSync(resolve(root, 'src/services/downloads.ts'), 'utf8')
const downloadStore = readFileSync(resolve(root, 'src/stores/downloads.ts'), 'utf8')
const downloadCenter = readFileSync(resolve(root, 'src/views/DownloadsView.vue'), 'utf8')
const settings = readFileSync(resolve(root, 'src/views/SettingsView.vue'), 'utf8')

function extractInterface(source: string, name: string): string {
  const match = source.match(new RegExp(`export interface ${name}(?: extends [^\\{]+)? \\{[\\s\\S]*?\\n\\}`))
  assert.ok(match, `missing ${name} interface`)
  return match[0]
}

assert.match(rust, /player_download_pick_directory/)
assert.match(rust, /execute_android_task/)
assert.match(rust, /is_android_tree_uri/)
assert.match(rust, /resolve_alist/)
assert.doesNotMatch(rust, /Android downloads require a persistent writable SAF folder/)
assert.match(bridge, /run_mobile_plugin_async/)
assert.match(plugin, /ACTION_OPEN_DOCUMENT_TREE/)
assert.match(plugin, /FLAG_GRANT_PERSISTABLE_URI_PERMISSION/)
assert.match(plugin, /takePersistableUriPermission/)
assert.match(plugin, /persistedUriPermissions\.find \{ it\.uri == root \}/)
assert.match(plugin, /grant\?\.isReadPermission == true && grant\.isWritePermission/)
assert.match(plugin, /FLAG_GRANT_WRITE_URI_PERMISSION/)
assert.match(plugin, /FLAG_DIR_SUPPORTS_CREATE/)
assert.match(plugin, /ohmycine-part/)
assert.match(plugin, /resolveCompletedDocument/)
assert.match(plugin, /"entityHash" to document\?\.let \{ documentEntityHash/)
assert.match(plugin, /deleteCompletedDocument/)
assert.match(plugin, /deletePartialDocument/)
assert.match(plugin, /DocumentsContract\.deleteDocument/)
assert.match(plugin, /openOutputStream\(uri, if \(args\.truncate\) "wt" else "wa"\)/)
assert.match(plugin, /entityHash.*documentEntityHash/)
assert.match(plugin, /COLUMN_LAST_MODIFIED/)
assert.match(plugin, /授权已失效，请重新选择目录/)
assert.match(rust, /stored_entity.*entity_hash/s)
assert.doesNotMatch(rust, /android-saf-local-v1/)
assert.match(rust, /let mut resume_pending = offset > 0/)
assert.match(rust, /resume_pending = false/)
assert.match(rust, /resolve_completed_document/)
assert.match(rust, /delete_completed_document/)
assert.match(rust, /cleanup_cancelled_android_task/)
assert.match(rust, /cleanup_cancelled_android_final/)
assert.match(rust, /retry_android_cleanup/)
assert.match(rust, /"android_saf_partial"[\s\S]*delete_partial_document/)
assert.match(rust, /"android_saf_final"[\s\S]*delete_completed_document/)
assert.match(rust, /entity_hash[\s\S]*expected != entity_hash/)
assert.match(service, /startForeground\(id, notification\)/)
assert.match(service, /activeNotifications = linkedMapOf/)
assert.match(service, /activeNotifications\.remove\(id\)/)
assert.match(service, /if \(foregroundId == null\)[\s\S]*startForeground\(id, notification\)/)
assert.match(service, /startForeground\(replacement\.key, replacement\.value\)/)
assert.match(manifest, /FOREGROUND_SERVICE_DATA_SYNC/)
assert.match(manifest, /foregroundServiceType="dataSync"/)
assert.match(adapter, /pickAndroidDownloadDirectory\(false\)/)
assert.doesNotMatch(adapter, /isNativeAndroidRuntime\(\)[\s\S]{0,160}availability: 'disabled'/)
assert.match(downloadBridge, /invoke<string \| null>\('player_download_pick_directory', \{ persistent \}\)/)
assert.match(downloadCenter, /const selected = isNativeAndroidRuntime\(\)[\s\S]{0,120}\? await pickAndroidDownloadDirectory\(false\)[\s\S]{0,180}typeof selected === 'string'[\s\S]{0,80}await store\.saveDirectory\(selected\)/)
assert.match(downloadStore, /async function saveDirectory\(directory: string\)[\s\S]{0,120}defaultDirectory\.value = await setDefaultDownloadDirectory\(directory\)/)
assert.doesNotMatch(settings, /pickAndroidDownloadDirectory|getDefaultDownloadDirectory|setDefaultDownloadDirectory|chooseDefaultDownloadDirectory|默认下载目录/)
assert.match(rust, /validate_redirect\(&url, &next\)\?/)
assert.match(rust, /if origin_key\(&next\) != original_origin \{\s*headers\.clear\(\);\s*\}/)
const persistedBridgeContracts = [
  'DownloadTask',
  'DownloadEnqueueOptions',
  'OfflineItemSummary',
  'OfflineDetailRecord',
].map(name => extractInterface(downloadBridge, name)).join('\n')
assert.doesNotMatch(persistedBridgeContracts, /\b(?:url|headers?|cookie|authorization|apiKey|deviceToken|signature)\s*\??:/i)
assert.match(downloadBridge, /syncOfflineAttachments[\s\S]*invoke<OfflineAttachmentSyncResult>\('player_download_sync_attachments'/)

console.log(JSON.stringify({
  androidSafPersistentGrant: true,
  androidDownloadToDoesNotReplaceDefault: true,
  androidDefaultDirectoryPersistsViaDownloadStore: true,
  settingsViewDoesNotOwnDownloadDirectory: true,
  androidForegroundNotification: true,
  revokedGrantRecovery: true,
  completedOfflineResolveAndDelete: true,
  activeCancellationCleansOwnedPartial: true,
  finalizedCancellationCleansOwnedDocument: true,
  deferredSafCleanupIsRetried: true,
  completedSafIdentityIsValidated: true,
  persistedDescriptorsExcludeSensitiveTransport: true,
  transientAttachmentTransportUsesDedicatedCommand: true,
}, null, 2))
