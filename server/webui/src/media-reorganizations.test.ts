import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { mediaReorganizationPhaseLabel } from '@/media-reorganizations'

describe('media reorganization presentation', () => {
  it('keeps the destructive workflow explicit and reports every persisted phase', () => {
    expect(mediaReorganizationPhaseLabel('queued')).toBe('等待执行')
    expect(mediaReorganizationPhaseLabel('executing')).toContain('移动')
    expect(mediaReorganizationPhaseLabel('reconciling')).toContain('投影')
    expect(mediaReorganizationPhaseLabel('completed')).toBe('重新整理完成')
    expect(mediaReorganizationPhaseLabel('failed')).toBe('重新整理失败')
  })

  it('uses one managed-only preview flow from download, organization and media details', () => {
    const dialog = readFileSync(new URL('./components/MediaReorganizationDialog.vue', import.meta.url), 'utf8')
    const downloads = readFileSync(new URL('./views/DownloadsView.vue', import.meta.url), 'utf8')
    const organization = readFileSync(new URL('./views/OrganizationView.vue', import.meta.url), 'utf8')
    const libraries = readFileSync(new URL('./views/MediaLibrariesView.vue', import.meta.url), 'utf8')

    expect(dialog).toContain('非托管文件不会被扫描、猜测或删除')
    expect(dialog).toContain('生成安全预览')
    expect(dialog).toContain('确认并开始重新整理')
    expect(downloads).toContain('<MediaReorganizationDialog')
    expect(organization).toContain('<MediaReorganizationDialog')
    expect(libraries).toContain('reorganizable_transfers')
    expect(libraries).toContain('<MediaReorganizationDialog')
  })
})
