import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

function zIndex(source: string, selector: string) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const match = source.match(new RegExp(`${escaped}\\s*\\{[^}]*z-index:\\s*(\\d+)`, 's'))
  if (!match) throw new Error(`Missing z-index for ${selector}`)
  return Number(match[1])
}

describe('global tool panel layering', () => {
  it('keeps the click-blocking scrim below the topbar stacking context', () => {
    const source = readFileSync(new URL('./layouts/AppLayout.vue', import.meta.url), 'utf8')
    const scrim = zIndex(source, '.shell-scrim')
    const topbar = zIndex(source, '.shell-topbar')
    const panel = zIndex(source, '.tool-panel')
    expect(scrim).toBeLessThan(topbar)
    expect(panel).toBeGreaterThan(scrim)
  })
})
