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
]) {
  assert.match(variables, new RegExp(`${token}:`))
  assert.match(variables, new RegExp(`\\[data-theme="light"\\][\\s\\S]*?${token}:`))
}

const globalStyles = await source('src/styles/global.css')
assert.match(globalStyles, /html\[data-theme="light"\] \{\s+color-scheme: light;/)
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

const immersiveSurfaces = await Promise.all([
  source('src/views/MediaDetailView.vue'),
  source('src/views/SourceLibraryView.vue'),
  source('src/components/player/SubtitleSearchDialog.vue'),
])
for (const surface of immersiveSurfaces)
  assert.match(surface, /theme-immersive-dark/)

const mobileNavigation = await source('src/components/layout/MobileNavigation.vue')
assert.match(mobileNavigation, /html\[data-theme="light"\][\s\S]*?\.mobile-bottom-nav/)
assert.match(mobileNavigation, /html\[data-theme="light"\][\s\S]*?\.mobile-nav-item\.is-active/)

console.log(JSON.stringify({
  adaptiveControlTokens: true,
  themedNativeSelects: true,
  readableLightSettings: true,
  themedUpdateDialog: true,
  immersiveDarkControlExceptions: true,
  readableLightMobileNavigation: true,
}, null, 2))
