import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

const variables = await source('src/styles/variables.css')
for (const token of [
  '--control-bg',
  '--control-bg-hover',
  '--control-option-bg',
  '--control-border',
  '--control-text',
  '--control-placeholder',
  '--control-focus-ring',
  '--chrome-scrim',
  '--chrome-surface',
  '--chrome-surface-translucent',
  '--navigation-surface',
]) {
  assert.match(variables, new RegExp(`${token}:`))
  assert.match(variables, new RegExp(`\\[data-theme="light"\\][\\s\\S]*?${token}:`))
}

const globalStyles = await source('src/styles/global.css')
assert.match(globalStyles, /html\[data-theme="light"\] \{\s+color-scheme: light;/)
assert.match(globalStyles, /html, body \{[\s\S]*?background: var\(--color-bg\);/)
assert.match(globalStyles, /#app \{[\s\S]*?background: var\(--color-bg\);/)
assert.match(globalStyles, /html\.theme-switching[\s\S]*?transition: none !important;/)
assert.match(globalStyles, /select \{[\s\S]*?appearance: none;/)
assert.match(globalStyles, /select option,[\s\S]*?background: var\(--control-option-bg\);/)
assert.match(globalStyles, /\.theme-adaptive input:not/)
assert.match(globalStyles, /\.theme-adaptive \.text-white\\\/82/)
assert.match(globalStyles, /\.theme-immersive-dark \{/)

const settings = await source('src/views/SettingsView.vue')
assert.match(settings, /class="settings-view theme-adaptive /)
assert.match(settings, /<select[\s\S]*?<option/)

const updateDialog = await source('src/components/layout/UpdateDialog.vue')
assert.match(updateDialog, /class="theme-adaptive glass-panel/)
assert.doesNotMatch(updateDialog, /theme-adaptive[^>]*bg-black\/78/)

const mediaDetail = await source('src/views/MediaDetailView.vue')
assert.match(mediaDetail, /class="detail-view theme-adaptive/)
assert.match(mediaDetail, /class="detail-hero theme-immersive-dark/)

const sourceLibrary = await source('src/views/SourceLibraryView.vue')
assert.match(sourceLibrary, /class="source-view theme-adaptive/)
assert.match(sourceLibrary, /identification-dialog theme-adaptive/)
assert.doesNotMatch(sourceLibrary, /identification-dialog theme-immersive-dark/)

const heroCarousel = await source('src/components/media/HeroCarousel.vue')
assert.match(heroCarousel, /class="hero-carousel theme-immersive-dark/)

const subtitleSearch = await source('src/components/player/SubtitleSearchDialog.vue')
assert.match(subtitleSearch, /theme-immersive-dark/)

const playerView = await source('src/views/PlayerView.vue')
assert.match(playerView, /class="player-view theme-adaptive/)
assert.match(playerView, /background: var\(--player-chrome-top-gradient\);/)
assert.match(playerView, /background: var\(--player-chrome-bottom-gradient\);/)

const playerControls = await source('src/components/player/PlayerControls.vue')
assert.match(playerControls, /background: var\(--player-chrome-surface\);/)
assert.match(playerControls, /background: var\(--player-chrome-surface-strong\);/)

const mobilePlayerControls = await source('src/components/player/MobilePlayerControls.vue')
assert.match(mobilePlayerControls, /color: var\(--color-text\);/)
assert.match(mobilePlayerControls, /background: var\(--player-chrome-surface\);/)
assert.match(mobilePlayerControls, /background: var\(--player-chrome-surface-strong\);/)

const videoPlayer = await source('src/components/player/VideoPlayer.vue')
assert.match(videoPlayer, /\.playback-status-panel \{[\s\S]*?border: 1px solid var\(--control-border\);/)
assert.match(videoPlayer, /\.playback-status-panel \{[\s\S]*?background: var\(--player-chrome-surface-strong\);/)
assert.match(videoPlayer, /\.playback-status-panel \{[\s\S]*?box-shadow: var\(--player-chrome-shadow\);/)
assert.match(videoPlayer, /\.playback-status-description \{[\s\S]*?color: var\(--color-text-secondary\);/)
assert.match(videoPlayer, /\.playback-status-action \{[\s\S]*?background: var\(--control-bg\);/)
assert.doesNotMatch(videoPlayer, /\.playback-status-panel \{[\s\S]*?background: rgba\(8, 10, 15, 0\.82\);/)

const mobileNavigation = await source('src/components/layout/MobileNavigation.vue')
assert.match(mobileNavigation, /class="mobile-sheet theme-adaptive"/)
assert.match(mobileNavigation, /class="mobile-bottom-nav theme-adaptive"/)
assert.match(mobileNavigation, /background: var\(--chrome-surface\);/)
assert.match(mobileNavigation, /background: var\(--navigation-surface\);/)

const globalSearch = await source('src/components/media/GlobalSearchWorkspace.vue')
assert.match(globalSearch, /class="search-workspace theme-adaptive/)
assert.doesNotMatch(globalSearch, /search-workspace theme-immersive-dark/)
assert.match(globalSearch, /background: var\(--chrome-surface-translucent\);/)

const home = await source('src/views/HomeView.vue')
assert.match(home, /first-run-home theme-adaptive/)
assert.match(home, /theme-adaptive grid grid-cols-1/)

const themeComposable = await source('src/composables/useTheme.ts')
assert.match(themeComposable, /root\.classList\.add\('theme-switching'\)/)
assert.match(themeComposable, /void root\.offsetWidth/)
assert.match(themeComposable, /root\.setAttribute\('data-theme', nextTheme\)/)
assert.match(themeComposable, /window\.requestAnimationFrame/)
assert.doesNotMatch(themeComposable, /watchEffect/)

console.log(JSON.stringify({
  adaptiveControlTokens: true,
  themedNativeSelects: true,
  readableLightSettings: true,
  themedUpdateDialog: true,
  adaptiveSearchAndLibrarySurfaces: true,
  immersiveArtworkExceptions: true,
  adaptiveDesktopAndAndroidPlayerChrome: true,
  adaptivePlaybackStartupStatus: true,
  readableLightMobileNavigation: true,
  atomicAndroidThemeSwitch: true,
  opaqueNonPlayerRoot: true,
}, null, 2))
