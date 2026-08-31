import { getAppSetting, setAppSetting } from '@/services/appSettings'

export type NavigationShortcutTarget = 'home' | 'settings' | 'datasources' | `source:${string}`
export type NavigationShortcutBindings = Record<string, string>

export const NAVIGATION_SHORTCUTS_CHANGED_EVENT = 'ohmycine:navigation-shortcuts-changed'

const STORAGE_KEY = 'ohmycine-navigation-shortcuts-v1'
const DEFAULT_BINDINGS: NavigationShortcutBindings = {
  home: 'Alt+KeyH',
  settings: 'Alt+Comma',
  datasources: 'Alt+KeyD',
}
const RESERVED_PLAYER_SHORTCUTS = new Set(['Space', 'ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Escape'])

export function loadNavigationShortcutBindings(): NavigationShortcutBindings {
  const raw = getAppSetting(STORAGE_KEY)
  if (!raw)
    return { ...DEFAULT_BINDINGS }

  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
      return { ...DEFAULT_BINDINGS }
    return Object.fromEntries(
      Object.entries(parsed)
        .filter(([target, shortcut]) => isNavigationShortcutTarget(target) && typeof shortcut === 'string')
        .map(([target, shortcut]) => [target, normalizeStoredShortcut(shortcut as string)])
        .filter((entry): entry is [string, string] => Boolean(entry[1])),
    )
  }
  catch {
    return { ...DEFAULT_BINDINGS }
  }
}

export async function saveNavigationShortcutBindings(bindings: NavigationShortcutBindings): Promise<void> {
  const sanitized = Object.fromEntries(
    Object.entries(bindings)
      .filter(([target]) => isNavigationShortcutTarget(target))
      .map(([target, shortcut]) => [target, normalizeStoredShortcut(shortcut)])
      .filter((entry): entry is [string, string] => Boolean(entry[1])),
  )
  validateUniqueNavigationShortcuts(sanitized)
  await setAppSetting(STORAGE_KEY, JSON.stringify(sanitized))
  window.dispatchEvent(new CustomEvent(NAVIGATION_SHORTCUTS_CHANGED_EVENT))
}

export async function removeNavigationShortcutBinding(target: NavigationShortcutTarget): Promise<void> {
  const bindings = loadNavigationShortcutBindings()
  if (!bindings[target])
    return
  delete bindings[target]
  await saveNavigationShortcutBindings(bindings)
}

export function resetNavigationShortcutBindings(): NavigationShortcutBindings {
  return { ...DEFAULT_BINDINGS }
}

export function shortcutFromKeyboardEvent(event: KeyboardEvent): string | null {
  if (event.isComposing || event.code === 'Tab' || event.code === 'CapsLock' || isModifierCode(event.code))
    return null
  const parts: string[] = []
  if (event.ctrlKey)
    parts.push('Ctrl')
  if (event.altKey)
    parts.push('Alt')
  if (event.shiftKey)
    parts.push('Shift')
  if (event.metaKey)
    parts.push('Meta')
  parts.push(event.code)
  const shortcut = parts.join('+')
  return isReservedPlayerShortcutCode(event.code) ? null : shortcut
}

export function shortcutDisplayLabel(shortcut: string | undefined): string {
  if (!shortcut)
    return '未设置'
  return shortcut
    .replace(/Key([A-Z])/g, '$1')
    .replace(/Digit(\d)/g, '$1')
    .replace('Comma', ',')
    .replace('Period', '.')
    .replace('Slash', '/')
    .replace('Semicolon', ';')
    .replace('Quote', '\'')
    .replace('BracketLeft', '[')
    .replace('BracketRight', ']')
    .replace('Backslash', '\\')
    .replace('Minus', '-')
    .replace('Equal', '=')
}

export function navigationShortcutTargetForEvent(event: KeyboardEvent, bindings: NavigationShortcutBindings): NavigationShortcutTarget | null {
  const shortcut = shortcutFromKeyboardEvent(event)
  if (!shortcut)
    return null
  const target = Object.entries(bindings).find(([, binding]) => binding === shortcut)?.[0]
  return target && isNavigationShortcutTarget(target) ? target : null
}

export function shouldIgnoreNavigationShortcut(event: KeyboardEvent): boolean {
  if (event.defaultPrevented || event.isComposing)
    return true
  const target = event.target
  if (!(target instanceof HTMLElement))
    return false
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
}

export function validateUniqueNavigationShortcuts(bindings: NavigationShortcutBindings): void {
  const used = new Map<string, string>()
  for (const [target, shortcut] of Object.entries(bindings)) {
    if (!shortcut)
      continue
    if (isReservedPlayerShortcut(shortcut))
      throw new Error('空格、方向键和 Esc 已保留给播放器，不能绑定为导航快捷键。')
    const previous = used.get(shortcut)
    if (previous)
      throw new Error(`快捷键 ${shortcutDisplayLabel(shortcut)} 已被其他导航入口占用。`)
    used.set(shortcut, target)
  }
}

function isNavigationShortcutTarget(value: string): value is NavigationShortcutTarget {
  return value === 'home' || value === 'settings' || value === 'datasources' || value.startsWith('source:')
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

function isReservedPlayerShortcut(shortcut: string): boolean {
  const code = shortcut.split('+').at(-1)?.trim() ?? ''
  return isReservedPlayerShortcutCode(code)
}

export function isReservedPlayerShortcutCode(code: string): boolean {
  return RESERVED_PLAYER_SHORTCUTS.has(code)
}
