import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { isNearbyDoubleTap, resolveTouchGestureAxis, touchSeekTarget, touchVerticalLevel } from '../src/services/playerTouchGestures'

const root = new URL('../', import.meta.url)

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

const appLayout = await source('src/components/layout/AppLayout.vue')
assert.match(appLayout, /import MobileNavigation from '\.\/MobileNavigation\.vue'/)
assert.match(appLayout, /<MobileNavigation v-if="!isPlayerRoute"/)
assert.match(appLayout, /isNativeAndroidRuntime/)
assert.match(appLayout, /<WindowChrome v-if="!isNativeAndroid"/)
assert.match(appLayout, /<FloatingControls v-if="!isNativeAndroid"/)
assert.match(appLayout, /handleMainTouchStart/)
assert.match(appLayout, /distance < 72/)
assert.match(appLayout, /searchWorkspace\.show\(\)/)
assert.match(appLayout, /<GlobalSearchWorkspace v-if="!isPlayerRoute"/)

const mobileNavigation = await source('src/components/layout/MobileNavigation.vue')
assert.match(mobileNavigation, /首页/)
assert.match(mobileNavigation, /媒体库/)
assert.match(mobileNavigation, /快捷操作/)
assert.match(mobileNavigation, /设置/)
assert.match(mobileNavigation, /activeSheet = ref<MobileSheet \| null>/)
assert.match(mobileNavigation, /mobile-sheet-layer/)
assert.match(mobileNavigation, /env\(safe-area-inset-bottom\)/)
assert.match(mobileNavigation, /pickAndroidLocalVideo/)
assert.match(mobileNavigation, /savePlaybackMediaContext/)
assert.match(mobileNavigation, /locator: \{\s+kind: 'localPath',\s+path: selected\.uri,/)
assert.doesNotMatch(mobileNavigation, /query: \{\s+path: selected\.uri/)
assert.doesNotMatch(mobileNavigation, /mobile-nav-quick/)
assert.match(mobileNavigation, /class="mobile-nav-item" :class="\{ 'is-active': activeSheet === 'quick' \}"/)

const settingsView = await source('src/views/SettingsView.vue')
assert.match(settingsView, /pickAndroidLocalDirectory/)
assert.match(settingsView, /已授权本地媒体目录/)
assert.match(settingsView, /form\.rootLabel = selected\.name/)

const sidebar = await source('src/components/layout/DataSourceSidebar.vue')
assert.doesNotMatch(sidebar, /mobile-source-nav/)

const floatingControls = await source('src/components/layout/FloatingControls.vue')
assert.match(floatingControls, /@media \(max-width: 767px\), \(hover: none\) and \(pointer: coarse\)[\s\S]*?\.floating-controls \{\s+display: none;/)

const homeView = await source('src/views/HomeView.vue')
assert.match(homeView, /recent-play-overlay/)
assert.match(homeView, /\.recent-play-overlay \{[\s\S]*?opacity: 1;/)

const heroCarousel = await source('src/components/media/HeroCarousel.vue')
assert.match(heroCarousel, /@pointerdown="handlePointerDown"/)
assert.match(heroCarousel, /@pointermove="handlePointerMove"/)
assert.match(heroCarousel, /Math\.abs\(deltaX\) > Math\.abs\(deltaY\) \* 1\.2/)
assert.match(heroCarousel, /touch-action: pan-y;/)
assert.match(heroCarousel, /emit\('detail', item\)/)
assert.match(heroCarousel, /isInteractiveTarget\(event\.target\)/)

const globalStyles = await source('src/styles/global.css')
assert.doesNotMatch(globalStyles, /\.cinema-scrollbar \{\s+overscroll-behavior-y: contain;/)

const mediaCard = await source('src/components/media/MediaCard.vue')
assert.match(mediaCard, /media-card-play/)
assert.match(mediaCard, /\.media-card-play \{[\s\S]*?opacity: 1;/)

const sourceLibrary = await source('src/views/SourceLibraryView.vue')
assert.match(sourceLibrary, /\.source-bottom-controls \{[\s\S]*?opacity: 1;/)
assert.match(sourceLibrary, /bottom: calc\(5\.25rem \+ env\(safe-area-inset-bottom\)\)/)

const playerControls = await source('src/components/player/PlayerControls.vue')
assert.match(playerControls, /grid-template-columns: auto minmax\(0, 1fr\) auto/)
assert.match(playerControls, /\.control-popover \{[\s\S]*?position: fixed;/)
assert.match(playerControls, /orientationSupported: boolean/)
assert.match(playerControls, /自动横屏/)
assert.match(playerControls, /锁定横屏/)
assert.match(playerControls, /锁定竖屏/)

const mobilePlayerControls = await source('src/components/player/MobilePlayerControls.vue')
assert.match(mobilePlayerControls, /mobile-control-layer/)
assert.match(mobilePlayerControls, /mobile-player-top/)
assert.match(mobilePlayerControls, /mobile-transport/)
assert.match(mobilePlayerControls, /mobile-player-bottom/)
assert.match(mobilePlayerControls, /mobile-bottom-row/)
assert.match(mobilePlayerControls, /grid-template-columns: auto 3\.2rem minmax\(6rem, 1fr\) 3\.2rem auto/)
assert.doesNotMatch(mobilePlayerControls, /class="mobile-timeline"/)
assert.doesNotMatch(mobilePlayerControls, /class="mobile-bottom-tools"/)
assert.match(mobilePlayerControls, /mobile-player-sheet/)
assert.match(mobilePlayerControls, /自动横屏/)
assert.match(mobilePlayerControls, /锁定横屏/)
assert.match(mobilePlayerControls, /锁定竖屏/)
assert.match(mobilePlayerControls, /搜索字幕/)
assert.match(mobilePlayerControls, /载入本地字幕/)
assert.match(mobilePlayerControls, /@media \(orientation: portrait\)/)
assert.equal((mobilePlayerControls.match(/aria-label="字幕"/g) ?? []).length, 1)
assert.match(mobilePlayerControls, /\.transport-skip \{[\s\S]*?width: 3\.25rem;/)
assert.match(mobilePlayerControls, /\.transport-primary \{[\s\S]*?width: 3\.9rem;/)
assert.match(mobilePlayerControls, /linear-gradient\(145deg, rgba\(35, 38, 45, 0\.7\)/)
assert.match(mobilePlayerControls, /播放器亮度/)
assert.match(mobilePlayerControls, /setVideoBrightness/)

const mediaDetail = await source('src/views/MediaDetailView.vue')
assert.match(mediaDetail, /loadPlayerInteractionSettings\(\)\.mobileEpisodeLayout/)
assert.match(mediaDetail, /is-mobile-episode-surface/)
assert.match(mediaDetail, /renderedEpisodes = computed\(\(\) => isMobileEpisodeViewport\.value \? episodes\.value : visibleEpisodes\.value\)/)
assert.match(mediaDetail, /is-mobile-episode-surface\.is-horizontal \.episode-card-strip \{[\s\S]*?touch-action: pan-x;/)
assert.match(mediaDetail, /is-mobile-episode-surface\.is-vertical \.episode-card-strip \{[\s\S]*?touch-action: pan-y;/)
assert.match(mediaDetail, /is-mobile-episode-surface \.episode-position-row \{[\s\S]*?display: none;/)
assert.match(mediaDetail, /is-mobile-episode-surface \.episode-edge-fade/)
assert.match(mediaDetail, /min-width: calc\(100vw - 2rem\)/)

const subtitleSearch = await source('src/components/player/SubtitleSearchDialog.vue')
assert.match(subtitleSearch, /resultSummary/)
assert.match(subtitleSearch, /找到 \$\{props\.results\.length\} 条字幕/)
assert.match(subtitleSearch, /subtitle-search-loading/)
assert.match(subtitleSearch, /mobileLayout\?: boolean/)
assert.match(subtitleSearch, /'is-mobile': mobileLayout/)
assert.match(subtitleSearch, /grid-template-columns: minmax\(18rem, 34vw\) minmax\(0, 1fr\)/)

const progressBar = await source('src/components/player/ProgressBar.vue')
assert.match(progressBar, /@pointerdown\.prevent="handlePointerDown"/)
assert.match(progressBar, /setPointerCapture\(event\.pointerId\)/)
assert.match(progressBar, /touch-action: none/)
assert.match(progressBar, /pendingTouch/)
assert.match(progressBar, /Math\.abs\(deltaX\) >= 12/)
assert.match(progressBar, /Math\.abs\(deltaY\) > Math\.abs\(deltaX\)/)

assert.equal(resolveTouchGestureAxis(8, 4), 'pending')
assert.equal(resolveTouchGestureAxis(12, 12), 'horizontal')
assert.equal(resolveTouchGestureAxis(13, 18), 'vertical')
assert.equal(resolveTouchGestureAxis(30, 28, 24, 1.25), 'pending')
assert.equal(resolveTouchGestureAxis(36, 20, 24, 1.25), 'horizontal')
assert.equal(resolveTouchGestureAxis(20, 36, 24, 1.25), 'vertical')
assert.equal(touchSeekTarget(50, -500, 500, 100), 0)
assert.equal(touchSeekTarget(50, 500, 500, 100), 100)
assert.equal(touchVerticalLevel(50, -500, 500), 100)
assert.equal(touchVerticalLevel(50, 500, 500), 0)
assert.equal(isNearbyDoubleTap(null, { x: 10, y: 10, at: 100 }), false)
assert.equal(isNearbyDoubleTap({ x: 10, y: 10, at: 100 }, { x: 20, y: 20, at: 350 }), true)
assert.equal(isNearbyDoubleTap({ x: 10, y: 10, at: 100 }, { x: 100, y: 100, at: 350 }), false)

const playerView = await source('src/views/PlayerView.vue')
assert.match(playerView, /event\.pointerType === 'mouse' && event\.altKey && event\.button === 0/)
assert.match(playerView, /event\.pointerType !== 'touch' && !simulatedWithMouse/)
assert.match(playerView, /function revealChromeFromPointer\(\) \{\s+if \(touchGestureSession\?\.simulatedWithMouse\)\s+return/)
assert.match(playerView, /session\.leftSide \? 'brightness' : 'volume'/)
assert.match(playerView, /isNearbyDoubleTap\(lastTouchTap, currentTap\)/)
assert.match(playerView, /void handleTogglePause\(\)\.catch/)
assert.match(playerView, /suppressPlayerClickUntil = Date\.now\(\) \+ TOUCH_CLICK_SUPPRESSION_MS/)
assert.match(playerView, /@media \(any-pointer: coarse\) \{[\s\S]*?touch-action: none;/)
assert.doesNotMatch(playerView, /触摸测试/)
assert.match(playerView, /beginHeldArrow\(touchGestureSession\.holdArrowKey, 'touch'\)/)
assert.match(playerView, /heldArrowOwner === 'touch' && arrowHoldActivated/)
assert.match(playerView, /releaseHeldArrow\(false, 'touch'\)/)
assert.match(playerView, /Date\.now\(\) - session\.startedAt <= 700/)
assert.doesNotMatch(playerView, /playbackDiagnostics\?\.state !== 'error'/)
assert.match(playerView, /android-touch-capture/)
assert.match(playerView, /loadPlayerInteractionSettings\(\)\.longPressPlaybackSpeed/)
assert.match(playerView, /const isNativeAndroidPlayer = isNativeAndroidRuntime\(\)/)
assert.match(playerView, /player-view--native-mobile/)
assert.match(playerView, /<MobilePlayerControls/)
assert.match(playerView, /<BufferingIndicator/)
assert.match(playerView, /v-if="hasMedia && isNativeAndroidPlayer"/)
assert.match(playerView, /:mobile-layout="false"/)
assert.match(playerView, /:mobile-layout="isNativeAndroidPlayer"/)
assert.match(playerView, /if \(isNativeAndroidPlayer\) \{\s+toggleChromeFromTouch\(\)/)
assert.match(playerView, /showKeyboardOsd\(`屏幕方向 · \$\{label\}`\)/)
assert.match(playerView, /const TOUCH_MOVEMENT_THRESHOLD = 24/)
assert.match(playerView, /const TOUCH_AXIS_DOMINANCE = 1\.25/)
assert.match(playerView, /isProtectedSystemGestureStart/)
assert.match(playerView, /topGuard = Math\.max\(48/)
assert.match(playerView, /setDisplayBrightness\(update\.value\)/)
assert.match(playerView, /屏幕亮度/)
assert.match(playerView, /displayBrightnessSupported/)
assert.doesNotMatch(playerView, /else\s+await setVideoBrightness\(update\.value\)/)
assert.match(playerView, /document\.addEventListener\('visibilitychange', handleVisibilityChange\)/)

const desktopBrightness = await source('src-tauri/src/commands/display_brightness.rs')
assert.match(desktopBrightness, /GetPhysicalMonitorsFromHMONITOR/)
assert.match(desktopBrightness, /GetMonitorBrightness/)
assert.match(desktopBrightness, /SetMonitorBrightness/)
assert.match(desktopBrightness, /WmiMonitorBrightness/)
assert.match(desktopBrightness, /WmiMonitorBrightnessMethods/)
assert.match(desktopBrightness, /WmiSetBrightness/)
assert.match(desktopBrightness, /desktop_window_handle/)

const cargoToml = await source('src-tauri/Cargo.toml')
assert.match(cargoToml, /wmi = \{ version = "0\.17\.2"/)
assert.match(cargoToml, /"Win32_Devices_Display"/)

const videoPlayer = await source('src/components/player/VideoPlayer.vue')
assert.match(videoPlayer, /等待播放中/)
assert.doesNotMatch(videoPlayer, /拖拽文件到此处播放/)
assert.match(videoPlayer, /playback-backdrop/)
assert.match(videoPlayer, /!props\.videoReady/)

const mpvPlayer = await source('src-tauri/src/mpv/player.rs')
assert.match(mpvPlayer, /"video-zoom"[\s\S]*?\| "brightness" =>/)

const windowChrome = await source('src/components/layout/WindowChrome.vue')
assert.match(windowChrome, /const appWindow = isTauriRuntime\(\) \? getCurrentWindow\(\) : null/)
assert.match(windowChrome, /@media \(max-width: 767px\) \{[\s\S]*?\.desktop-window-controls/)

const mainActivity = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/MainActivity.kt')
assert.match(mainActivity, /SystemBarStyle\.dark\(Color\.TRANSPARENT\)/)
assert.match(mainActivity, /registerForActivityResult\(/)
assert.match(mainActivity, /fun launchLocalMediaPicker\(/)
assert.match(mainActivity, /localMediaPickerCallback == null/)

const localMediaPlugin = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/localmedia/LocalMediaPlugin.kt')
assert.match(localMediaPlugin, /host\.launchLocalMediaPicker\(intent\)/)
assert.doesNotMatch(localMediaPlugin, /startActivityForResult\(/)

console.log(JSON.stringify({
  mobileBottomNavigation: true,
  libraryAndQuickSheets: true,
  hoverOnlyGlobalControlsRemoved: true,
  touchMediaActionsVisible: true,
  sourceQuickControlsPersistent: true,
  mobilePlayerControlLayout: true,
  touchProgressSeeking: true,
  touchPlaybackGestures: true,
  touchDesktopInputIsolation: true,
  desktopTouchGestureSimulation: true,
  browserResponsivePreviewSupported: true,
  verticalScrollThroughHomeMediaRows: true,
  nativeAndroidPlayerLayout: true,
  nativeAndroidDesktopChromeRemoved: true,
  nativeAndroidDedicatedControls: true,
  nativeAndroidTapChromeFallback: true,
  mobilePullToSearch: true,
  mobileHeroSwipeNavigation: true,
  heroSingleTapOpensDetail: true,
  mobileFullscreenSubtitleSearch: true,
  conciseWaitingPlaybackState: true,
  nativeOrientationLockControl: true,
  androidSystemMediaPicker: true,
  androidDocumentTreePicker: true,
  androidPickerLifecycleOwnedByActivity: true,
  darkSystemBarForeground: true,
  configurableMobileEpisodeLayout: true,
  touchGestureEdgeProtection: true,
  intentionalTouchProgressSeeking: true,
  separateDisplayAndVideoBrightness: true,
  windowsSystemDisplayBrightness: true,
}, null, 2))
