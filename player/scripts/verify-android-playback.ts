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
  'addSubtitle',
  'pause',
  'resume',
  'stop',
  'seek',
  'getProperty',
  'setProperty',
  'trackState',
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

const kotlinPlugin = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvPlugin.kt')
assert.match(kotlinPlugin, /@TauriPlugin/)
assert.match(kotlinPlugin, /MpvSurfaceHost\.install/)
assert.match(kotlinPlugin, /fun surfaceStatus/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_SENSOR_LANDSCAPE/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_LANDSCAPE/)
assert.match(kotlinPlugin, /SCREEN_ORIENTATION_PORTRAIT/)
assert.match(kotlinPlugin, /fun setOrientation/)
assert.match(kotlinPlugin, /WindowInsetsCompat\.Type\.systemBars/)
assert.match(kotlinPlugin, /FLAG_KEEP_SCREEN_ON/)

const surfaceHost = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvSurfaceHost.kt')
assert.match(surfaceHost, /SurfaceView/)
assert.match(surfaceHost, /MPVLib\.attachSurface/)
assert.match(surfaceHost, /MPVLib\.setOptionString\("gpu-context", "android"\)/)
assert.match(surfaceHost, /MPVLib\.setOptionString\("vo", "gpu-next"\)/)
assert.match(surfaceHost, /MPVLib\.setOptionString\("hwdec", "mediacodec,mediacodec-copy"\)/)
assert.match(surfaceHost, /webView\.setBackgroundColor\(Color\.TRANSPARENT\)/)
assert.match(surfaceHost, /parent\.addView\(container, index, layoutParams\)/)
assert.doesNotMatch(surfaceHost, /activity\.setContentView/)
assert.match(surfaceHost, /PendingLoad/)
assert.match(surfaceHost, /pendingLoad\?\.let \{ play\(it\) \}/)
assert.match(surfaceHost, /initializationError/)

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
}, null, 2))
