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
  cacheClearPreservesGlobalState: true,
  customizableNavigationShortcuts: true,
}, null, 2))
