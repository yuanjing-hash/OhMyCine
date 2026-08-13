import { describe, expect, it } from 'vitest'
import { findNavigationItem } from '@/navigation'
import { mediaLibrariesRouteContract } from '@/router/contracts'

describe('media library route contract', () => {
  it('binds the system URL to the permission-filtered navigation item', () => {
    expect(mediaLibrariesRouteContract).toEqual({ path: 'system/media-libraries', name: 'media-libraries', navigationID: 'media-libraries' })
    expect(findNavigationItem(mediaLibrariesRouteContract.navigationID).to).toBe('/system/media-libraries')
  })
})
