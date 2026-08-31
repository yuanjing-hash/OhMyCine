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
assert.match(chrome, /const appWindow = isTauriRuntime\(\) \? getCurrentWindow\(\) : null/)
assert.match(chrome, /function beginDrag\(event: MouseEvent\) \{[\s\S]*?event\.button !== 0[\s\S]*?event\.preventDefault\(\)[\s\S]*?event\.stopPropagation\(\)[\s\S]*?void appWindow\.startDragging\(\)\.catch\(\(\) => undefined\)/)
assert.equal(chrome.match(/appWindow\.startDragging\(/g)?.length, 1)
assert.doesNotMatch(chrome, /dragStart|dragIfMoved|isDragStarting|deltaX|deltaY/)
assert.doesNotMatch(chrome, /isMaximized\(\)[\s\S]{0,120}unmaximize\(\)[\s\S]{0,120}startDragging\(\)/)
assert.doesNotMatch(chrome, /@mousemove="dragIfMoved"|@mouseup="endDrag"|@mouseleave="endDrag"/)
assert.doesNotMatch(chrome, /data-tauri-drag-region/)
assert.match(chrome, /class="desktop-window-drag pointer-events-auto[^"\n]*z-0[^"\n]*"[\s\S]{0,160}@dblclick="toggleMaximize"[\s\S]{0,80}@mousedown="beginDrag"/)
assert.match(chrome, /class="window-chrome pointer-events-none fixed inset-x-0 top-0 h-16"/)
assert.match(chrome, /class="glass-panel player-window-back pointer-events-auto[^"\n]*z-20/)
assert.match(chrome, /class="desktop-window-nav glass-panel pointer-events-auto[^"\n]*z-10/)
assert.match(chrome, /class="desktop-window-controls glass-panel pointer-events-auto[^"\n]*z-10/)
assert.match(chrome, /function isTauriRuntime\(\): boolean \{[\s\S]*?__TAURI_INTERNALS__[\s\S]*?\}/)

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
const moveSyncStart = windowsBackend.indexOf('fn sync_position_from_cached_bounds(&self)')
const moveSyncEnd = windowsBackend.indexOf('    // -----------------------------------------------------------------------', moveSyncStart)
const ownerEventStart = windowsBackend.indexOf('pub fn on_owner_window_event(&mut self, event: OwnerWindowEvent)')
const ownerEventEnd = windowsBackend.indexOf('    fn note_waiting_for_webview_bounds', ownerEventStart)
const geometrySyncStart = windowsBackend.indexOf('fn sync_geometry_from_owner(&mut self)')
const geometrySyncEnd = windowsBackend.indexOf('    fn hide_mpv_hwnd', geometrySyncStart)
const setBoundsStart = windowsBackend.indexOf('pub fn set_bounds(&mut self, bounds: RenderSurfaceBounds)')
const setBoundsEnd = windowsBackend.indexOf('    /// Accept the strategy', setBoundsStart)
assert.notEqual(moveSyncStart, -1)
assert.notEqual(moveSyncEnd, -1)
assert.notEqual(ownerEventStart, -1)
assert.notEqual(ownerEventEnd, -1)
assert.notEqual(geometrySyncStart, -1)
assert.notEqual(geometrySyncEnd, -1)
assert.notEqual(setBoundsStart, -1)
assert.notEqual(setBoundsEnd, -1)
const moveSync = windowsBackend.slice(moveSyncStart, moveSyncEnd)
const ownerEvents = windowsBackend.slice(ownerEventStart, ownerEventEnd)
const geometrySync = windowsBackend.slice(geometrySyncStart, geometrySyncEnd)
const setBounds = windowsBackend.slice(setBoundsStart, setBoundsEnd)
assert.match(ownerEvents, /OwnerWindowEvent::Resized => \{\s*self\.sync_visibility_from_owner\(\);\s*self\.note_waiting_for_webview_bounds\("owner resized"\);\s*return;/)
assert.match(ownerEvents, /OwnerWindowEvent::Moved => \{\s*self\.sync_position_from_cached_bounds\(\);\s*return;/)
assert.match(ownerEvents, /OwnerWindowEvent::ScaleFactorChanged => \{\s*self\.note_waiting_for_webview_bounds\("owner scale changed"\);\s*return;/)
assert.match(moveSync, /let Some\(bounds\) = self\.bounds else[\s\S]*?ClientToScreen\(owner, &mut client_origin\)[\s\S]*?SetWindowPos\(\s*hwnd,\s*owner,\s*x,\s*y,\s*0,\s*0,\s*SWP_NOSIZE \| SWP_NOACTIVATE \| SWP_SHOWWINDOW/)
assert.doesNotMatch(ownerEvents, /OwnerWindowEvent::Moved => \{[^}]*(?:note_waiting_for_webview_bounds|sync_geometry_from_owner)/)
assert.doesNotMatch(moveSync, /GetClientRect\(|apply_window_region\(|run_on_main_thread\(/)
assert.match(ownerEvents, /OwnerWindowEvent::WindowStateChanged[\s\S]*?let changed =\s*self\.owner_fullscreen != fullscreen[\s\S]*?self\.owner_fullscreen = fullscreen;\s*self\.owner_maximized = maximized;\s*if !changed \{\s*return;\s*\}[\s\S]*?schedule_owner_corner_preference[\s\S]*?self\.sync_geometry_from_owner\(\)/)
assert.match(geometrySync, /let Some\(current_bounds\) = self\.bounds else[\s\S]*?waiting_for_webview_bounds/)
assert.doesNotMatch(geometrySync, /GetClientRect\(|client_w|client_h/)
assert.match(geometrySync, /let state_str = if fullscreen \{\s*"fullscreen"/)
assert.match(setBounds, /SetWindowPos\([\s\S]*?SWP_NOACTIVATE \| SWP_SHOWWINDOW[\s\S]*?d\.mpv_hwnd_shown = true/)
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
  immediateNativeWindowDrag: true,
  cachedBoundsMoveSync: true,
  webviewBoundsOwnResizeTiming: true,
  chromeHidesAfterWindowExit: true,
  boundedMpvEventDrain: true,
  nativeRoundedWindowShape: true,
  fullscreenEdgeOverscan: true,
}, null, 2))
