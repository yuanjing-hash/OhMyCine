import { describe, expect, it } from 'vitest'
import { findNavigationItem } from '@/navigation'
import { downloadsRouteContract, mediaLibrariesRouteContract, organizationRouteContract, playersRouteContract, strmRouteContract } from '@/router/contracts'

describe('media library route contract', () => {
  it('binds the system URL to the permission-filtered navigation item', () => {
    expect(mediaLibrariesRouteContract).toEqual({ path: 'system/media-libraries', name: 'media-libraries', navigationID: 'media-libraries' })
    expect(findNavigationItem(mediaLibrariesRouteContract.navigationID).to).toBe('/system/media-libraries')
  })
})

describe('STRM route contract', () => {
	it('binds the real STRM workspace to its navigation item', () => {
		expect(strmRouteContract).toEqual({ path: 'automation/strm', name: 'strm', navigationID: 'strm' })
		expect(findNavigationItem(strmRouteContract.navigationID).to).toBe('/automation/strm')
	})
})

describe('player management route contract', () => {
  it('binds the system URL to the connection read navigation item', () => {
    expect(playersRouteContract).toEqual({ path: 'system/players', name: 'players', navigationID: 'players' })
    expect(findNavigationItem(playersRouteContract.navigationID).to).toBe('/system/players')
  })
})

describe('media organization route contract', () => {
  it('binds the automation URL to the transfer permission navigation item', () => {
    expect(organizationRouteContract).toEqual({ path: 'automation/organization', name: 'organization', navigationID: 'organization' })
    expect(findNavigationItem(organizationRouteContract.navigationID).to).toBe('/automation/organization')
  })
})

describe('download route contract', () => {
  it('binds the automation URL to the permission-filtered navigation item', () => {
    expect(downloadsRouteContract).toEqual({ path: 'automation/downloads', name: 'downloads', navigationID: 'downloads' })
    expect(findNavigationItem(downloadsRouteContract.navigationID).to).toBe('/automation/downloads')
  })
})
