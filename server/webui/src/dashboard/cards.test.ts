import { describe, expect, it } from 'vitest'
import { Permissions } from '@/auth/generated-permissions'
import { dashboardCards, dashboardSectionPriority, getVisibleDashboardCards } from '@/dashboard/cards'

describe('mixed dashboard card contract', () => {
  it('keeps operational sections ahead of discovery content', () => {
    const priority = new Map(dashboardSectionPriority.map((section, index) => [section, index]))
    const positions = dashboardCards.map(card => priority.get(card.section) ?? -1)

    expect(positions).toEqual([...positions].sort((left, right) => left - right))
    expect(dashboardCards.at(-1)?.id).toBe('discovery-hero')
  })

  it('has one real baseline owner and explicit planned states for unimplemented domains', () => {
    expect(dashboardCards.filter(card => card.state === 'live').map(card => card.id)).toEqual(['server-status'])
    expect(dashboardCards.filter(card => card.id !== 'server-status').every(card => card.state === 'planned')).toBe(true)
  })

  it('omits protected domain cards instead of leaking their counts', () => {
    const visible = getVisibleDashboardCards([Permissions.DashboardRead])
    expect(visible.map(card => card.id)).toEqual(['server-status', 'media-summary'])
  })

  it('preserves the canonical order after permission filtering', () => {
    const allPermissions = Object.values(Permissions)
    expect(getVisibleDashboardCards(allPermissions).map(card => card.id)).toEqual(dashboardCards.map(card => card.id))
  })
})
