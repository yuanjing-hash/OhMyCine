import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { isReservedPlayerShortcutCode, shortcutDisplayLabel, shortcutFromKeyboardEvent } from '@/services/navigationShortcuts'

export type PlayerShortcutTarget = 'hideControls'
  | 'playPrevious'
  | 'seekBackward'
  | 'togglePause'
  | 'seekForward'
  | 'playNext'
  | 'toggleMute'
  | 'toggleSpeedMenu'
  | 'toggleSubtitleMenu'
  | 'toggleAudioMenu'
  | 'toggleQueueMenu'
  | 'toggleSettings'
  | 'toggleFullscreen'

export type PlayerShortcutBindings = Partial<Record<PlayerShortcutTarget, string>>

export const PLAYER_SHORTCUTS_CHANGED_EVENT = 'ohmycine:player-shortcuts-changed'

const STORAGE_KEY = 'ohmycine-player-shortcuts-v1'
const PLAYER_SHORTCUT_TARGETS = new Set<PlayerShortcutTarget>([
  'hideControls',
  'playPrevious',
  'seekBackward',
  'togglePause',
  'seekForward',
  'playNext',
  'toggleMute',
  'toggleSpeedMenu',
  'toggleSubtitleMenu',
  'toggleAudioMenu',
  'toggleQueueMenu',
  'toggleSettings',
  'toggleFullscreen',
])
const DEFAULT_BINDINGS: PlayerShortcutBindings = {
  hideControls: 'KeyH',
  playPrevious: 'KeyQ',
  seekBackward: 'KeyW',
  togglePause: 'KeyE',
  seekForward: 'KeyR',
  playNext: 'KeyT',
  toggleMute: 'KeyY',
  toggleSpeedMenu: 'KeyU',
  toggleSubtitleMenu: 'KeyI',
  toggleAudioMenu: 'KeyO',
  toggleQueueMenu: 'KeyP',
  toggleSettings: 'BracketLeft',
  toggleFullscreen: 'BracketRight',
}

export function loadPlayerShortcutBindings(): PlayerShortcutBindings {
  const raw = getAppSetting(STORAGE_KEY)
  if (!raw)
    return { ...DEFAULT_BINDINGS }

  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
      return { ...DEFAULT_BINDINGS }
    const bindings = sanitizePlayerShortcutBindings(parsed as Record<string, unknown>)
    validateUniquePlayerShortcuts(bindings)
    return bindings
  }
  catch {
    return { ...DEFAULT_BINDINGS }
  }
}

export async function savePlayerShortcutBindings(bindings: PlayerShortcutBindings): Promise<void> {
  const sanitized = sanitizePlayerShortcutBindings(bindings)
  validateUniquePlayerShortcuts(sanitized)
  await setAppSetting(STORAGE_KEY, JSON.stringify(sanitized))
  window.dispatchEvent(new CustomEvent(PLAYER_SHORTCUTS_CHANGED_EVENT))
}

export function resetPlayerShortcutBindings(): PlayerShortcutBindings {
  return { ...DEFAULT_BINDINGS }
}

export function playerShortcutTargetForEvent(
  event: KeyboardEvent,
  bindings: PlayerShortcutBindings,
): PlayerShortcutTarget | null {
  const shortcut = shortcutFromKeyboardEvent(event)
  if (!shortcut)
    return null
  const target = Object.entries(bindings).find(([, binding]) => binding === shortcut)?.[0]
  return target && isPlayerShortcutTarget(target) ? target : null
}

export function validateUniquePlayerShortcuts(bindings: PlayerShortcutBindings): void {
  const used = new Map<string, PlayerShortcutTarget>()
  for (const [rawTarget, shortcut] of Object.entries(bindings)) {
    if (!shortcut || !isPlayerShortcutTarget(rawTarget))
      continue
    const previous = used.get(shortcut)
    if (previous)
      throw new Error(`播放器快捷键 ${shortcutDisplayLabel(shortcut)} 已被其他播放动作占用。`)
    used.set(shortcut, rawTarget)
  }
}

function sanitizePlayerShortcutBindings(bindings: Record<string, unknown>): PlayerShortcutBindings {
  return Object.fromEntries(
    Object.entries(bindings)
      .filter(([target, shortcut]) => isPlayerShortcutTarget(target) && typeof shortcut === 'string')
      .map(([target, shortcut]) => [target, normalizeStoredShortcut(shortcut as string)])
      .filter((entry): entry is [PlayerShortcutTarget, string] => Boolean(entry[1])),
  )
}

function isPlayerShortcutTarget(value: string): value is PlayerShortcutTarget {
  return PLAYER_SHORTCUT_TARGETS.has(value as PlayerShortcutTarget)
}

function normalizeStoredShortcut(value: string): string {
  const parts = value.split('+').map(part => part.trim()).filter(Boolean)
  if (parts.length === 0)
    return ''
  const code = parts.at(-1) ?? ''
  if (!code || isModifierCode(code) || isReservedPlayerShortcutCode(code))
    return ''
  const modifiers = ['Ctrl', 'Alt', 'Shift', 'Meta'].filter(modifier => parts.includes(modifier))
  return [...modifiers, code].join('+')
}

function isModifierCode(code: string): boolean {
  return ['ControlLeft', 'ControlRight', 'AltLeft', 'AltRight', 'ShiftLeft', 'ShiftRight', 'MetaLeft', 'MetaRight'].includes(code)
}
