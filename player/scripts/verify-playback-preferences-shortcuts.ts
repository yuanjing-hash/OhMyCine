import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import {
  shortcutFromKeyboardEvent,
  validateUniqueNavigationShortcuts,
} from '../src/services/navigationShortcuts.ts'

async function source(relativePath: string) {
  return readFile(fileURLToPath(new URL(relativePath, import.meta.url)), 'utf8')
}

const preferenceCommand = await source('../src-tauri/src/commands/preference.rs')
assert.match(preferenceCommand, /CREATE TABLE IF NOT EXISTS media_playback_preferences/)
assert.match(preferenceCommand, /DELETE FROM media_playback_preferences WHERE source_id = \?1/)
assert.match(preferenceCommand, /canonical_path\.starts_with\(&canonical_root\)/)
assert.match(preferenceCommand, /DELETE FROM raw_scan_cache/)

const subtitleCommand = await source('../src-tauri/src/commands/subtitle.rs')
assert.match(subtitleCommand, /scoped_cache_key\(\s*"subtitle-source"/)
assert.match(subtitleCommand, /scoped_cache_key\(\s*"subtitle-media"/)

const playerView = await source('../src/views/PlayerView.vue')
assert.match(playerView, /restoreMediaPlaybackPreference/)
assert.match(playerView, /event\.code === 'Space'/)
assert.match(playerView, /ARROW_HOLD_DELAY/)
assert.match(playerView, /releaseHeldArrow\(false\)/)
assert.match(playerView, /@click="handlePlayerAreaClick"/)

const playerControls = await source('../src/components/player/PlayerControls.vue')
const toggleMenuBody = playerControls.match(/function toggleMenu\(menu: ControlMenu\) \{([\s\S]*?)\n\}/)?.[1] ?? ''
assert.doesNotMatch(toggleMenuBody, /refreshTracks/, 'opening subtitle/audio menus must not synchronously query mpv track-list')

const mpvComposable = await source('../src/composables/useMpv.ts')
for (const functionName of ['setSubtitle', 'setAudio']) {
  const functionBody = mpvComposable.match(new RegExp(`async function ${functionName}\\([^)]*\\) \\{([\\s\\S]*?)\\n  \\}`))?.[1] ?? ''
  assert.doesNotMatch(functionBody, /await refreshTrackState\(\)/, `${functionName} must not immediately re-read track-list during a track switch`)
}
assert.doesNotMatch(
  mpvComposable.match(/async function load\([^)]*\) \{([\s\S]*?)\n  \}/)?.[1] ?? '',
  /await refreshTrackState\(\)/,
  'media load must not synchronously query track-list before duration metadata is ready',
)

const nativePlayer = await source('../src-tauri/src/mpv/player.rs')
assert.match(nativePlayer, /mpv_set_property_async/)
assert.match(nativePlayer, /mpv_command_async/)
assert.match(nativePlayer, /pub fn drain_events/)

const dataSourceStore = await source('../src/stores/datasource.ts')
assert.match(dataSourceStore, /deletePlaybackHistoryForSource\(id\)/)
assert.match(dataSourceStore, /deleteMediaPlaybackPreferencesForSource\(id\)/)
assert.match(dataSourceStore, /removeNavigationShortcutBinding\(`source:\$\{id\}`\)/)

const settingsView = await source('../src/views/SettingsView.vue')
assert.match(settingsView, /不会删除数据源、登录凭据、播放记录或全局软件设置/)
assert.match(settingsView, /saveNavigationShortcutBindings/)
assert.match(settingsView, /longPressPlaybackSpeed/)

const shortcut = shortcutFromKeyboardEvent({
  code: 'KeyH',
  altKey: true,
  ctrlKey: false,
  shiftKey: false,
  metaKey: false,
  isComposing: false,
} as KeyboardEvent)
assert.equal(shortcut, 'Alt+KeyH')
assert.throws(() => validateUniqueNavigationShortcuts({
  home: 'Alt+KeyH',
  settings: 'Alt+KeyH',
}), /已被其他导航入口占用/)
assert.throws(() => validateUniqueNavigationShortcuts({ home: 'Space' }), /已保留给播放器/)

console.log(JSON.stringify({
  mediaPreferenceSqlite: true,
  sourceScopedSubtitleCache: true,
  playbackTapAndHoldKeys: true,
  subtitleControlsAvoidSynchronousTrackRefresh: true,
  trackRestoreUsesAsyncNativeCommands: true,
  cacheClearPreservesGlobalState: true,
  customizableNavigationShortcuts: true,
}, null, 2))
