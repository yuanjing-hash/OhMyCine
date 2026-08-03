import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

const appLayout = await source('src/components/layout/AppLayout.vue')
assert.match(appLayout, /import MobileNavigation from '\.\/MobileNavigation\.vue'/)
assert.match(appLayout, /<MobileNavigation v-if="!isPlayerRoute"/)

const mobileNavigation = await source('src/components/layout/MobileNavigation.vue')
assert.match(mobileNavigation, /首页/)
assert.match(mobileNavigation, /媒体库/)
assert.match(mobileNavigation, /快捷操作/)
assert.match(mobileNavigation, /设置/)
assert.match(mobileNavigation, /activeSheet = ref<MobileSheet \| null>/)
assert.match(mobileNavigation, /mobile-sheet-layer/)
assert.match(mobileNavigation, /env\(safe-area-inset-bottom\)/)

const sidebar = await source('src/components/layout/DataSourceSidebar.vue')
assert.doesNotMatch(sidebar, /mobile-source-nav/)

const floatingControls = await source('src/components/layout/FloatingControls.vue')
assert.match(floatingControls, /@media \(max-width: 767px\), \(hover: none\) and \(pointer: coarse\)[\s\S]*?\.floating-controls \{\s+display: none;/)

const homeView = await source('src/views/HomeView.vue')
assert.match(homeView, /recent-play-overlay/)
assert.match(homeView, /\.recent-play-overlay \{[\s\S]*?opacity: 1;/)

const mediaCard = await source('src/components/media/MediaCard.vue')
assert.match(mediaCard, /media-card-play/)
assert.match(mediaCard, /\.media-card-play \{[\s\S]*?opacity: 1;/)

const sourceLibrary = await source('src/views/SourceLibraryView.vue')
assert.match(sourceLibrary, /\.source-bottom-controls \{[\s\S]*?opacity: 1;/)
assert.match(sourceLibrary, /bottom: calc\(5\.25rem \+ env\(safe-area-inset-bottom\)\)/)

const playerControls = await source('src/components/player/PlayerControls.vue')
assert.match(playerControls, /grid-template-columns: auto minmax\(0, 1fr\) auto/)
assert.match(playerControls, /\.control-popover \{[\s\S]*?position: fixed;/)

const progressBar = await source('src/components/player/ProgressBar.vue')
assert.match(progressBar, /@pointerdown\.prevent="handlePointerDown"/)
assert.match(progressBar, /setPointerCapture\(event\.pointerId\)/)
assert.match(progressBar, /touch-action: none/)

const windowChrome = await source('src/components/layout/WindowChrome.vue')
assert.match(windowChrome, /const appWindow = isTauriRuntime\(\) \? getCurrentWindow\(\) : null/)
assert.match(windowChrome, /@media \(max-width: 767px\) \{[\s\S]*?\.desktop-window-controls/)

console.log(JSON.stringify({
  mobileBottomNavigation: true,
  libraryAndQuickSheets: true,
  hoverOnlyGlobalControlsRemoved: true,
  touchMediaActionsVisible: true,
  sourceQuickControlsPersistent: true,
  mobilePlayerControlLayout: true,
  touchProgressSeeking: true,
  browserResponsivePreviewSupported: true,
}, null, 2))
