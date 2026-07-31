import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import {
  shortcutFromKeyboardEvent,
  validateUniqueNavigationShortcuts,
} from '../src/services/navigationShortcuts.ts'
import {
  loadPlayerShortcutBindings,
  playerShortcutTargetForEvent,
  validateUniquePlayerShortcuts,
} from '../src/services/playerShortcuts.ts'

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
assert.match(playerView, /event\.code === 'ArrowUp'/)
assert.match(playerView, /adjustVolumeFromKeyboard/)
assert.match(playerView, /playerShortcutTargetForEvent/)
assert.match(playerView, /hideChromeFromKeyboard/)
assert.match(playerView, /showKeyboardOsd/)
assert.match(playerView, /runKeyboardAction/)
assert.match(playerView, /cycleSubtitleFromKeyboard/)
assert.match(playerView, /cycleAudioFromKeyboard/)
assert.match(playerView, /revealChromeFromPointer/)
assert.match(playerView, /<Transition name="keyboard-osd">/)
assert.match(playerView, /ARROW_HOLD_DELAY/)
assert.match(playerView, /releaseHeldArrow\(false\)/)
assert.match(playerView, /@click="handlePlayerAreaClick"/)
assert.match(playerView, /ref="bottomChromeRef"[\s\S]*data-player-click-ignore/)
assert.match(playerView, /addExternalSubtitle\(track\.url, track\.title \?\? result\.title, track\.language, 'provider'\)/)
assert.match(playerView, /addExternalSubtitle\(downloaded\.path, downloaded\.title, downloaded\.language, 'downloaded'\)/)
assert.match(playerView, /function cancelPendingTrackPreferenceRestore\(\)/)
assert.match(playerView, /async function handleSetSubtitle[\s\S]*cancelPendingTrackPreferenceRestore\(\)/)

const playerControls = await source('../src/components/player/PlayerControls.vue')
const toggleMenuBody = playerControls.match(/function toggleMenu\(menu: ControlMenu\) \{([\s\S]*?)\n\}/)?.[1] ?? ''
assert.doesNotMatch(toggleMenuBody, /refreshTracks/, 'opening subtitle/audio menus must not synchronously query mpv track-list')
assert.match(playerControls, /downloadedSubtitleTracks/)
assert.match(playerControls, /mediaSubtitleTracks/)
assert.match(playerControls, /subtitle-group-divider/)
assert.match(playerControls, /defineExpose\(\{ dismissTransientUi, toggleFullscreenFromShortcut \}\)/)
assert.match(playerControls, /async function toggleFullscreenFromShortcut/)

const volumeControl = await source('../src/components/player/VolumeControl.vue')
assert.match(volumeControl, /const displayVolume = computed\(\(\) => props\.volume\)/)

const appLayout = await source('../src/components/layout/AppLayout.vue')
assert.match(appLayout, /isPlayerRoute\.value && playerShortcutTargetForEvent/)

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
assert.doesNotMatch(nativePlayer, /mpv_command_async/)
assert.match(nativePlayer, /"sid" \| "aid" => self\.command\(&\["set", property_name, value\]\)/)
assert.match(nativePlayer, /self\.command\(&\["sub-add", path, "select", title, language\]\)/)
assert.doesNotMatch(nativePlayer, /mpv_set_property_async/)
assert.match(nativePlayer, /pub fn drain_events/)

const playerCommands = await source('../src-tauri/src/commands/player.rs')
assert.match(playerCommands, /prepare_external_subtitle/)
assert.match(playerCommands, /layout\.cache_dir\.join\("mpv-subtitles"\)/)
assert.match(playerCommands, /MAX_PREPARED_SUBTITLE_BYTES/)

const dataSourceStore = await source('../src/stores/datasource.ts')
assert.match(dataSourceStore, /deletePlaybackHistoryForSource\(id\)/)
assert.match(dataSourceStore, /deleteMediaPlaybackPreferencesForSource\(id\)/)
assert.match(dataSourceStore, /removeNavigationShortcutBinding\(`source:\$\{id\}`\)/)

const settingsView = await source('../src/views/SettingsView.vue')
assert.match(settingsView, /不会删除数据源、登录凭据、播放记录或全局软件设置/)
assert.match(settingsView, /saveNavigationShortcutBindings/)
assert.match(settingsView, /savePlayerShortcutBindings/)
assert.match(settingsView, /playerShortcutEntries/)
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

const playerBindings = loadPlayerShortcutBindings()
assert.equal(playerBindings.hideControls, 'KeyH')
assert.deepEqual(
  [
    playerBindings.playPrevious,
    playerBindings.seekBackward,
    playerBindings.togglePause,
    playerBindings.seekForward,
    playerBindings.playNext,
    playerBindings.toggleMute,
    playerBindings.toggleSpeedMenu,
    playerBindings.toggleSubtitleMenu,
    playerBindings.toggleAudioMenu,
    playerBindings.toggleQueueMenu,
  ],
  ['KeyQ', 'KeyW', 'KeyE', 'KeyR', 'KeyT', 'KeyY', 'KeyU', 'KeyI', 'KeyO', 'KeyP'],
)
assert.equal(playerBindings.toggleSettings, undefined)
assert.equal(playerBindings.toggleFullscreen, undefined)
assert.equal(playerShortcutTargetForEvent({
  code: 'KeyH',
  altKey: false,
  ctrlKey: false,
  shiftKey: false,
  metaKey: false,
  isComposing: false,
} as KeyboardEvent, playerBindings), 'hideControls')
assert.throws(() => validateUniquePlayerShortcuts({
  hideControls: 'KeyH',
  togglePause: 'KeyH',
}), /已被其他播放动作占用/)
assert.equal(shortcutFromKeyboardEvent({
  code: 'ArrowUp',
  altKey: false,
  ctrlKey: false,
  shiftKey: false,
  metaKey: false,
  isComposing: false,
} as KeyboardEvent), null)

console.log(JSON.stringify({
  mediaPreferenceSqlite: true,
  sourceScopedSubtitleCache: true,
  playbackTapAndHoldKeys: true,
  controlChromeIgnoresVideoClickToPause: true,
  subtitleMenuGroupsDownloadedAndMediaTracks: true,
  externalSubtitleCommandsReturnActualResult: true,
  externalSubtitlesUseShortRuntimeCache: true,
  subtitleControlsAvoidSynchronousTrackRefresh: true,
  trackRestoreWaitsForMetadata: true,
  cacheClearPreservesGlobalState: true,
  customizableNavigationShortcuts: true,
  customizablePlayerShortcuts: true,
  fixedVolumeArrowShortcuts: true,
  keyboardActionsUseCompactOsd: true,
  keyboardActionsDoNotRevealChrome: true,
}, null, 2))
