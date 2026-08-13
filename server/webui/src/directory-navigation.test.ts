import { describe, expect, it } from 'vitest'
import { directoryRootLabel, FILESYSTEM_ROOTS_ENDPOINT } from './directory-navigation'

describe('directory root navigation', () => {
  it('uses a familiar Windows root label', () => { expect(directoryRootLabel('windows')).toBe('此电脑') })
  it('uses a filesystem label for Unix and unknown adapters', () => {
    expect(directoryRootLabel('linux')).toBe('文件系统')
    expect(directoryRootLabel('fake')).toBe('文件系统')
  })
  it('targets the existing roots endpoint', () => { expect(FILESYSTEM_ROOTS_ENDPOINT).toBe('/api/v1/filesystem/roots') })
})
