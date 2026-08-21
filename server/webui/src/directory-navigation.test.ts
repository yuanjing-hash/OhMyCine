import { describe, expect, it } from 'vitest'
import { directoryPickerBrowseEndpoint, directoryPickerFallbackEndpoint, directoryPickerInitialEndpoint, directoryRootLabel, displayedStagingPath, DOWNLOAD_STAGING_DIRECTORY_ENDPOINT, FILESYSTEM_ROOTS_ENDPOINT, storageDirectoryEndpoint } from './directory-navigation'

describe('directory root navigation', () => {
  it('uses a familiar Windows root label', () => { expect(directoryRootLabel('windows')).toBe('此电脑') })
  it('uses a filesystem label for Unix and unknown adapters', () => {
    expect(directoryRootLabel('linux')).toBe('文件系统')
    expect(directoryRootLabel('fake')).toBe('文件系统')
  })
  it('targets the existing roots endpoint', () => { expect(FILESYSTEM_ROOTS_ENDPOINT).toBe('/api/v1/filesystem/roots') })
  it('opens the global staging picker through its Server-owned current directory endpoint', () => {
    expect(DOWNLOAD_STAGING_DIRECTORY_ENDPOINT).toBe('/api/v1/settings/downloads/directory')
    expect(directoryPickerInitialEndpoint(DOWNLOAD_STAGING_DIRECTORY_ENDPOINT)).toBe(DOWNLOAD_STAGING_DIRECTORY_ENDPOINT)
    expect(directoryPickerInitialEndpoint()).toBe(FILESYSTEM_ROOTS_ENDPOINT)
    expect(directoryPickerFallbackEndpoint(DOWNLOAD_STAGING_DIRECTORY_ENDPOINT)).toBe(FILESYSTEM_ROOTS_ENDPOINT)
    expect(directoryPickerFallbackEndpoint(FILESYSTEM_ROOTS_ENDPOINT)).toBeNull()
  })
  it('keeps the configured absolute path visible until a new directory is selected', () => {
    expect(displayedStagingPath('D:\\Downloads\\staging', '')).toBe('D:\\Downloads\\staging')
    expect(displayedStagingPath('D:\\Downloads\\staging', 'E:\\incoming')).toBe('E:\\incoming')
  })
  it('keeps all Storage-scoped navigation on the Storage endpoint', () => {
    expect(storageDirectoryEndpoint(42)).toBe('/api/v1/storages/42/directory')
    expect(directoryPickerBrowseEndpoint(42, 9)).toBe('/api/v1/storages/42/directory')
    expect(directoryPickerBrowseEndpoint(null, 9)).toBe('/api/v1/connections/9/directories')
    expect(directoryPickerBrowseEndpoint()).toBe('/api/v1/filesystem/directories')
  })
})
