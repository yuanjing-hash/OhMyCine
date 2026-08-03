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
]) {
  assert.match(mobileCommands, new RegExp(`"${command}"`))
}
assert.match(mobileCommands, /sanitize_http_headers/)
assert.match(mobileCommands, /RenderBackendKind::AndroidSurface/)
assert.match(mobileCommands, /wait_for_android_surface/)
assert.match(mobileCommands, /Android 播放器表面准备超时/)
assert.match(mobileCommands, /stream_proxy\.prepare\(path, headers\)/)
assert.match(mobileCommands, /stream_proxy\.clear\(\)/)

const streamProxy = await source('src-tauri/src/mpv/mobile_proxy.rs')
assert.match(streamProxy, /TcpListener::bind\(\("127\.0\.0\.1", 0\)\)/)
assert.match(streamProxy, /URL_SAFE_NO_PAD/)
assert.match(streamProxy, /constant_time_eq/)
assert.match(streamProxy, /Body::from_stream\(response\.bytes_stream\(\)\)/)
assert.match(streamProxy, /RANGE, IF_RANGE, IF_NONE_MATCH, IF_MODIFIED_SINCE/)
assert.doesNotMatch(streamProxy, /danger_accept_invalid_certs/)

const kotlinPlugin = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvPlugin.kt')
assert.match(kotlinPlugin, /@TauriPlugin/)
assert.match(kotlinPlugin, /MpvSurfaceHost\.install/)
assert.match(kotlinPlugin, /fun surfaceStatus/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_SENSOR_LANDSCAPE/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_LANDSCAPE/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_PORTRAIT/)
assert.match(kotlinPlugin, /fun setOrientation/)
assert.match(kotlinPlugin, /fun applyEngineSettings/)
assert.match(kotlinPlugin, /WindowInsetsCompat\.Type\.systemBars/)
assert.match(kotlinPlugin, /FLAG_KEEP_SCREEN_ON/)

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

const setup = await source('scripts/setup-libmpv-android.mjs')
assert.match(setup, /releaseTag = '2026-04-25'/)
assert.match(setup, /4400bcba6be9cec1128e24d1eba153d8727384926b0639fa7fe44d4e36b04f81/)
assert.match(setup, /libmpv\.so/)
assert.match(setup, /libplayer\.so/)

const packageJson = await source('package.json')
assert.match(packageJson, /CARGO_PROFILE_DEV_DEBUG=0/)
assert.match(packageJson, /CARGO_PROFILE_DEV_STRIP=debuginfo/)
assert.match(packageJson, /gradlew -p src-tauri\/gen\/android clean/)
assert.doesNotMatch(packageJson, /assembleDebug --rerun-tasks/)

const gradle = await source('src-tauri/gen/android/app/build.gradle.kts')
assert.doesNotMatch(gradle, /keepDebugSymbols/)

const manifest = await source('src-tauri/gen/android/app/src/main/AndroidManifest.xml')
assert.match(manifest, /android:icon="@mipmap\/ic_launcher"/)
assert.match(manifest, /android:roundIcon="@mipmap\/ic_launcher_round"/)
const adaptiveIcon = await source('src-tauri/gen/android/app/src/main/res/mipmap-anydpi-v26/ic_launcher.xml')
assert.match(adaptiveIcon, /@mipmap\/ic_launcher_foreground/)

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
}, null, 2))
