import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { transitionWindowFullscreen } from '../src/services/windowFullscreen.ts'

const calls: string[] = []
let fullscreen = false
let maximized = true
const windowApi = {
  async isFullscreen() {
    calls.push('isFullscreen')
    return fullscreen
  },
  async isMaximized() {
    calls.push('isMaximized')
    return maximized
  },
  async setFullscreen(next: boolean) {
    calls.push(`setFullscreen:${next}`)
    fullscreen = next
  },
  async maximize() {
    calls.push('maximize')
    maximized = true
  },
  async unmaximize() {
    calls.push('unmaximize')
    maximized = false
  },
}

const entered = await transitionWindowFullscreen(windowApi, true, false, { delayMs: 0 })
assert.equal(entered.fullscreen, true)
assert.equal(entered.restoreMaximizedOnExit, true)
assert.deepEqual(calls.slice(0, 5), [
  'isFullscreen',
  'isMaximized',
  'unmaximize',
  'isMaximized',
  'setFullscreen:true',
])
assert.equal(calls[5], 'isFullscreen')

calls.length = 0
const exited = await transitionWindowFullscreen(windowApi, false, entered.restoreMaximizedOnExit, { delayMs: 0 })
assert.equal(exited.fullscreen, false)
assert.equal(exited.restoreMaximizedOnExit, false)
assert.deepEqual(calls, ['isFullscreen', 'setFullscreen:false', 'isFullscreen', 'maximize'])

fullscreen = false
maximized = true
calls.length = 0
const noOpWindowApi = {
  ...windowApi,
  async setFullscreen(next: boolean) {
    calls.push(`setFullscreen-noop:${next}`)
  },
}
await assert.rejects(
  transitionWindowFullscreen(noOpWindowApi, true, false, { attempts: 1, delayMs: 0 }),
  /窗口没有进入全屏状态/,
)
assert.equal(maximized, true)
assert.equal(calls.at(-1), 'maximize')

const root = new URL('../', import.meta.url)
const chrome = await readFile(fileURLToPath(new URL('src/components/layout/WindowChrome.vue', root)), 'utf8')
assert.match(chrome, /isMaximized \? '还原窗口' : '最大化窗口'/)
assert.match(chrome, /<svg v-if="isMaximized"/)

const videoPlayer = await readFile(fileURLToPath(new URL('src/components/player/VideoPlayer.vue', root)), 'utf8')
assert.match(videoPlayer, /appWindow\.onResized\(reportBounds\)/)
assert.match(videoPlayer, /appWindow\.onMoved\(reportBounds\)/)
assert.match(videoPlayer, /appWindow\.onScaleChanged\(reportBounds\)/)

const playerView = await readFile(fileURLToPath(new URL('src/views/PlayerView.vue', root)), 'utf8')
assert.match(playerView, /const AUTO_HIDE_DELAY = 3000/)
assert.doesNotMatch(playerView, /canAutoHideChrome\(\)[\s\S]{0,300}isWindowFocused/)
assert.match(playerView, /function handleWindowBlur\(\) \{\s+playerControlsRef\.value\?\.dismissTransientUi\(\)\s+controlsInteracting\.value = false\s+closePlaybackContextMenu\(false\)\s+void releaseHeldArrow\(false\)\s+scheduleChromeHide\(\)/)
assert.match(playerView, /function handleApplicationPointerLeave\(\)[\s\S]*dismissTransientUi\(\)[\s\S]*controlsInteracting\.value = false[\s\S]*closePlaybackContextMenu\(false\)[\s\S]*scheduleChromeHide\(\)/)
assert.match(playerView, /document\.documentElement\.addEventListener\('mouseleave', handleApplicationPointerLeave\)/)
assert.match(playerView, /appWindow\.onFocusChanged\(\(\{ payload: focused \}\) =>/)

const windowsBackend = await readFile(fileURLToPath(new URL('src-tauri/src/mpv/platform/windows.rs', root)), 'utf8')
assert.match(windowsBackend, /OwnerWindowEvent::Resized => \{[\s\S]*?waiting for WebView ResizeObserver bounds/)
assert.match(windowsBackend, /SWP_NOMOVE \| SWP_NOSIZE \| SWP_NOACTIVATE \| SWP_SHOWWINDOW/)
assert.match(windowsBackend, /MAX_EVENTS_PER_TICK|WINDOW_CORNER_RADIUS_LOGICAL/)
assert.match(windowsBackend, /DwmSetWindowAttribute/)
assert.match(windowsBackend, /CreateRoundRectRgn/)
assert.match(windowsBackend, /if fullscreen \{ 1 \} else \{ 0 \}/)

const mpvEvents = await readFile(fileURLToPath(new URL('src-tauri/src/mpv/events.rs', root)), 'utf8')
assert.match(mpvEvents, /const MAX_EVENTS_PER_TICK: usize = 64/)
assert.match(mpvEvents, /drain_events\(MAX_EVENTS_PER_TICK\)/)
assert.match(mpvEvents, /mpv:video-ready/)

const glass = await readFile(fileURLToPath(new URL('src/styles/glass.css', root)), 'utf8')
assert.match(glass, /border-radius: 12px/)
assert.match(glass, /native-window-fullscreen[\s\S]*border-radius: 0/)

console.log(JSON.stringify({
  maximizedFullscreenRoundTrip: true,
  maximizeRestoreIcon: true,
  webviewBoundsOwnResizeTiming: true,
  chromeHidesAfterWindowExit: true,
  boundedMpvEventDrain: true,
  nativeRoundedWindowShape: true,
  fullscreenEdgeOverscan: true,
}, null, 2))
