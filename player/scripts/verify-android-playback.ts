import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

const rustPlugin = await source('src-tauri/src/mpv/mobile.rs')
assert.match(rustPlugin, /com\.ohmycine\.player\.mpv/)
assert.match(rustPlugin, /MpvPlugin/)
assert.match(rustPlugin, /run_mobile_plugin_async/)
assert.match(rustPlugin, /mpv:time-update/)
assert.match(rustPlugin, /mpv:duration-change/)
assert.match(rustPlugin, /mpv:paused/)
assert.match(rustPlugin, /mpv:resumed/)

const mobileCommands = await source('src-tauri/src/commands/player_mobile.rs')
assert.doesNotMatch(mobileCommands, /MOBILE_PLAYBACK_UNAVAILABLE|暂不支持播放/)
for (const command of [
  'load',
  'applyEngineSettings',
  'addSubtitle',
  'pause',
  'resume',
  'stop',
  'seek',
  'getProperty',
  'setProperty',
  'trackState',
  'playbackDiagnostics',
  'surfaceStatus',
  'orientationState',
  'setOrientation',
  'displayBrightnessState',
  'setDisplayBrightness',
]) {
  assert.match(mobileCommands, new RegExp(`"${command}"`))
}
assert.match(mobileCommands, /sanitize_http_headers/)
assert.match(mobileCommands, /RenderBackendKind::AndroidSurface/)
assert.match(mobileCommands, /wait_for_android_surface/)
assert.match(mobileCommands, /Android 播放器表面准备超时/)
assert.match(mobileCommands, /stream_proxy\.prepare\(path, headers\)/)
assert.match(mobileCommands, /stream_proxy\.clear\(\)/)
assert.match(mobileCommands, /matches!\(url\.scheme\(\), "http" \| "https"\)/)

const streamProxy = await source('src-tauri/src/mpv/mobile_proxy.rs')
assert.match(streamProxy, /TcpListener::bind\(\("127\.0\.0\.1", 0\)\)/)
assert.match(streamProxy, /const LOOPBACK_PATH: &str = "\/media\/:token"/)
assert.match(streamProxy, /URL_SAFE_NO_PAD/)
assert.match(streamProxy, /constant_time_eq/)
assert.match(streamProxy, /Body::from_stream\(response\.bytes_stream\(\)\)/)
assert.match(streamProxy, /RANGE, IF_RANGE, IF_NONE_MATCH, IF_MODIFIED_SINCE/)
assert.match(streamProxy, /Policy::none\(\)/)
assert.match(streamProxy, /MAX_UPSTREAM_REDIRECTS/)
assert.match(streamProxy, /same_origin\(&current_url, &next_url\)/)
assert.doesNotMatch(streamProxy, /danger_accept_invalid_certs/)

const playerView = await source('src/views/PlayerView.vue')
assert.match(playerView, /v-if="hasMedia && isNativeAndroidPlayer"[\s\S]*?android-touch-capture/)
assert.doesNotMatch(playerView, /playbackDiagnostics\?\.state !== 'error'/)
assert.match(playerView, /@pointerdown\.stop="handlePlayerTouchPointerDown"/)
assert.match(playerView, /@pointercancel\.stop="handlePlayerTouchPointerEnd\(\$event, true\)"/)
assert.match(playerView, /\.android-touch-capture \{[\s\S]*?touch-action: none;/)

const useMpv = await source('src/composables/useMpv.ts')
assert.match(useMpv, /diagnostics\.state === 'error' \|\| diagnostics\.state === 'ended'/)
assert.match(useMpv, /isPlaying\.value = false/)

const kotlinPlugin = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvPlugin.kt')
assert.match(kotlinPlugin, /@TauriPlugin/)
assert.match(kotlinPlugin, /MpvSurfaceHost\.install/)
assert.match(kotlinPlugin, /fun surfaceStatus/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_SENSOR_LANDSCAPE/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_LANDSCAPE/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_PORTRAIT/)
assert.match(kotlinPlugin, /fun setOrientation/)
assert.match(kotlinPlugin, /fun applyEngineSettings/)
assert.match(kotlinPlugin, /backgroundPlaybackEnabled/)
assert.match(kotlinPlugin, /override fun onPause\(\)/)
assert.match(kotlinPlugin, /!backgroundPlaybackEnabled[\s\S]*?MpvSurfaceHost\.pause\(true\)/)
assert.match(kotlinPlugin, /Manifest\.permission\.POST_NOTIFICATIONS/)
assert.match(kotlinPlugin, /fun displayBrightnessState/)
assert.match(kotlinPlugin, /fun setDisplayBrightness/)
assert.match(kotlinPlugin, /attributes\.screenBrightness = \(level \/ 100\.0\)/)
assert.match(kotlinPlugin, /originalWindowBrightness\?\.let/)
assert.match(kotlinPlugin, /PlaybackService\.start/)
assert.match(kotlinPlugin, /PlaybackService\.stop/)
assert.match(kotlinPlugin, /WindowInsetsCompat\.Type\.systemBars/)
assert.match(kotlinPlugin, /FLAG_KEEP_SCREEN_ON/)
assert.match(kotlinPlugin, /contentResolver\.openFileDescriptor/)
assert.match(kotlinPlugin, /descriptor\.detachFd\(\)/)
assert.match(kotlinPlugin, /fdclose:\/\//)
assert.match(kotlinPlugin, /MpvSurfaceHost\.addSubtitle\(preparePlayablePath\(args\.url\)/)

const localMediaPlugin = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/localmedia/LocalMediaPlugin.kt')
assert.match(localMediaPlugin, /Intent\.ACTION_OPEN_DOCUMENT/)
assert.match(localMediaPlugin, /Intent\.ACTION_OPEN_DOCUMENT_TREE/)
assert.match(localMediaPlugin, /takePersistableUriPermission/)
assert.match(localMediaPlugin, /FLAG_GRANT_WRITE_URI_PERMISSION/)
assert.match(localMediaPlugin, /DocumentsContract\.deleteDocument/)
assert.match(localMediaPlugin, /requireWritableTree/)
assert.match(localMediaPlugin, /DocumentsContract\.buildChildDocumentsUriUsingTree/)
assert.match(localMediaPlugin, /persistedUriPermissions/)
assert.match(localMediaPlugin, /Android 本地媒体目录授权已失效/)

const localMediaCommands = await source('src-tauri/src/commands/local_file_android.rs')
assert.match(localMediaCommands, /com\.ohmycine\.player\.localmedia/)
assert.match(localMediaCommands, /LocalMediaPlugin/)
assert.match(localMediaCommands, /local_file_pick_video/)
assert.match(localMediaCommands, /local_file_pick_directory/)
assert.match(localMediaCommands, /local_file_delete_owned/)
assert.match(localMediaCommands, /\.run::<Value>\(\s*"delete"/)
assert.match(localMediaCommands, /\.run\(\s+"streamPath"/)

const surfaceHost = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvSurfaceHost.kt')
assert.match(surfaceHost, /SurfaceView/)
assert.match(surfaceHost, /MPVLib\.attachSurface/)
assert.match(surfaceHost, /MPVLib\.setOptionString\("gpu-context", "android"\)/)
assert.match(surfaceHost, /PRIMARY_ANDROID_VIDEO_OUTPUT = "gpu-next"/)
assert.match(surfaceHost, /FALLBACK_ANDROID_VIDEO_OUTPUT = "gpu"/)
assert.match(surfaceHost, /video-output-fallback/)
assert.match(surfaceHost, /hardwareDecoder = "mediacodec,mediacodec-copy"/)
assert.match(surfaceHost, /fun applyEngineSettings\(settings: MpvEngineSettings\)/)
assert.match(surfaceHost, /MPVLib\.setPropertyString\("hwdec", hardwareDecoder\)/)
assert.match(surfaceHost, /MPVLib\.setPropertyString\("video-sync", videoSync\)/)
assert.match(surfaceHost, /webView\.setBackgroundColor\(Color\.TRANSPARENT\)/)
assert.match(surfaceHost, /parent\.addView\(container, index, layoutParams\)/)
assert.doesNotMatch(surfaceHost, /activity\.setContentView/)
assert.match(surfaceHost, /PendingLoad/)
assert.match(surfaceHost, /pendingLoad\?\.let \{ play\(it\) \}/)
assert.match(surfaceHost, /initializationError/)
assert.match(surfaceHost, /MPVLib\.MpvEvent\.FILE_LOADED/)
assert.match(surfaceHost, /MPVLib\.MpvEvent\.END_FILE/)
assert.match(surfaceHost, /sanitizeDiagnosticLine/)
assert.match(surfaceHost, /MpvPlaybackDiagnostics/)
assert.match(surfaceHost, /playbackTransport = if \(request\.path\.startsWith\("http:\/\/127\.0\.0\.1:"\)\) "rust-loopback" else "direct"/)
assert.match(surfaceHost, /"pause", "paused-for-cache"/)
assert.match(surfaceHost, /"brightness", "sub-delay", "cache-speed"/)
assert.match(surfaceHost, /fun hasActivePlayback\(\): Boolean/)

const playbackService = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/PlaybackService.kt')
assert.match(playbackService, /class PlaybackService : Service\(\)/)
assert.match(playbackService, /MediaSessionCompat/)
assert.match(playbackService, /startForeground\(NOTIFICATION_ID, notification\)/)
assert.match(playbackService, /PlaybackStateCompat\.ACTION_SEEK_TO/)
assert.match(playbackService, /ACTION_FAST_FORWARD/)
assert.match(playbackService, /ACTION_REWIND/)
assert.match(playbackService, /MpvSurfaceHost\.hasActivePlayback\(\)/)
assert.match(playbackService, /stopSelf\(\)/)
assert.match(playbackService, /START_NOT_STICKY/)

assert.match(rustPlugin, /playback_transport: String/)
assert.match(useMpv, /playbackTransport: string/)
assert.match(await source('src/components/player/VideoPlayer.vue'), /\['playbackTransport', diagnostics\.playbackTransport\]/)

const setup = await source('scripts/setup-libmpv-android.mjs')
assert.match(setup, /releaseTag = '2026-04-25'/)
assert.match(setup, /4400bcba6be9cec1128e24d1eba153d8727384926b0639fa7fe44d4e36b04f81/)
assert.match(setup, /libmpv\.so/)
assert.match(setup, /libplayer\.so/)
assert.match(setup, /maxDownloadAttempts = 3/)
assert.match(setup, /AbortSignal\.timeout\(120_000\)/)
assert.match(setup, /rmSync\(destPath, \{ force: true \}\)/)
assert.match(setup, /retryBaseDelayMs \* \(2 \*\* \(attempt - 1\)\)/)

const packageJson = await source('package.json')
const androidBuildScript = await source('scripts/build-android-preview.mjs')
assert.match(packageJson, /"tauri:build:android:preview": "node scripts\/build-android-preview\.mjs"/)
assert.doesNotMatch(packageJson, /tauri:build:android:preview[^\n]+(?:rm -f|env -u|\$HOME|wsl)/)
assert.match(androidBuildScript, /rmSync\(apkPath, \{ force: true \}\)/)
assert.match(androidBuildScript, /CARGO_PROFILE_DEV_DEBUG/)
assert.match(androidBuildScript, /CARGO_PROFILE_DEV_STRIP/)
assert.match(androidBuildScript, /D:\\\\Software\\\\Android\\\\Sdk/)
assert.match(androidBuildScript, /GRADLE_USER_HOME/)
assert.doesNotMatch(packageJson, /gradlew -p src-tauri\/gen\/android clean/)
assert.doesNotMatch(packageJson, /assembleDebug --rerun-tasks/)

const gradle = await source('src-tauri/gen/android/app/build.gradle.kts')
assert.doesNotMatch(gradle, /keepDebugSymbols/)
assert.match(gradle, /compileSdk = 36/)
assert.match(gradle, /targetSdk = 36/)

const manifest = await source('src-tauri/gen/android/app/src/main/AndroidManifest.xml')
assert.match(manifest, /android:icon="@mipmap\/ic_launcher"/)
assert.match(manifest, /android:roundIcon="@mipmap\/ic_launcher_round"/)
assert.match(manifest, /REQUEST_INSTALL_PACKAGES/)
assert.match(manifest, /POST_NOTIFICATIONS/)
assert.match(manifest, /FOREGROUND_SERVICE_MEDIA_PLAYBACK/)
assert.match(manifest, /android:name="\.mpv\.PlaybackService"/)
assert.match(manifest, /android:foregroundServiceType="mediaPlayback"/)
assert.doesNotMatch(manifest, /READ_EXTERNAL_STORAGE|WRITE_EXTERNAL_STORAGE|MANAGE_EXTERNAL_STORAGE/)
const adaptiveIcon = await source('src-tauri/gen/android/app/src/main/res/mipmap-anydpi-v26/ic_launcher.xml')
assert.match(adaptiveIcon, /@mipmap\/ic_launcher_foreground/)

const updaterPlugin = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/updater/UpdaterPlugin.kt')
assert.match(updaterPlugin, /FileProvider\.getUriForFile/)
assert.match(updaterPlugin, /canRequestPackageInstalls/)

assert.match(gradle, /OHMYCINE_ANDROID_KEYSTORE/)
assert.match(gradle, /signingConfigs\.getByName\("preview"\)/)
assert.match(gradle, /androidx\.media:media:1\.7\.0/)

console.log(JSON.stringify({
  androidSurfaceViewBackend: true,
  officialMpvAndroidRuntimePinned: true,
  playbackCommandsBridged: true,
  playbackEventsForwarded: true,
  playbackHeadersValidated: true,
  nativeDebugSymbolsStrippedFromApk: true,
  delayedSurfacePlaybackSafe: true,
  nativeLandscapePlaybackMode: true,
  nativeInitializationErrorsSurfaced: true,
  nativeOrientationModes: true,
  gpuNextDefaultWithRuntimeFallback: true,
  configurablePlayerEngine: true,
  nativePlaybackDiagnostics: true,
  rustlsLoopbackMediaBridge: true,
  adaptiveAndroidLauncherIcon: true,
  androidSafFileAndDirectoryAccess: true,
  androidContentUriFileDescriptorPlayback: true,
  androidWindowBrightnessGesture: true,
  androidBackgroundPlaybackSetting: true,
  androidMediaSessionNotification: true,
}, null, 2))
