// @vitest-environment happy-dom

import { mount, type VueWrapper } from '@vue/test-utils'
import { flushPromises } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import DirectoryPickerDialog from '@/components/DirectoryPickerDialog.vue'
import type { DirectoryListing } from '@/types/api'

const apiMock = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ api: apiMock }))

function listing(token: string, location: string, children: Array<{ name: string; token: string }> = []): DirectoryListing {
  return {
    platform: 'pan115', location, current_token: token, current_selection_token: `select-${token}`,
    breadcrumbs: [], truncated: false,
    items: children.map(item => ({ name: item.name, location: `${location}/${item.name}`, token: item.token, selectable: false, enterable: true, kind: 'cloud-directory' })),
  }
}

function button(label: string) {
  return [...document.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.trim().startsWith(label))!
}

describe('directory picker session navigation', () => {
  let wrapper: VueWrapper | null = null

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    apiMock.mockReset()
    document.body.innerHTML = ''
  })

  it('reuses a visited browse token and lets explicit refresh bypass the session cache', async () => {
    const root = listing('root', '/媒体', [{ name: '电视剧', token: 'tv' }])
    const tv = listing('tv', '/媒体/电视剧')
    apiMock.mockImplementation((path: string) => {
      if (path === '/api/v1/storages/7/directory') return Promise.resolve(root)
      if (path === '/api/v1/storages/7/directory?token=tv') return Promise.resolve(tv)
      if (path === '/api/v1/storages/7/directory?token=root') return Promise.resolve(root)
      throw new Error(`unexpected path ${path}`)
    })
    wrapper = mount(DirectoryPickerDialog, { attachTo: document.body, props: { open: true, storageId: 7, restrictToStorage: true } })
    await flushPromises()

    expect(document.body.textContent).toContain('电视剧')
    button('电视剧').click()
    await flushPromises()
    expect(document.body.textContent).toContain('/媒体/电视剧')
    expect(apiMock).toHaveBeenCalledTimes(2)

    button('返回上级').click()
    await flushPromises()
    expect(document.body.textContent).toContain('/媒体')
    expect(apiMock).toHaveBeenCalledTimes(2)

    button('刷新').click()
    await flushPromises()
    expect(apiMock).toHaveBeenCalledTimes(3)
    expect(apiMock).toHaveBeenLastCalledWith('/api/v1/storages/7/directory?token=root', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })

  it('does not let a canceled response from an earlier dialog session overwrite the reopened location', async () => {
    let resolveFirst!: (value: DirectoryListing) => void
    const first = new Promise<DirectoryListing>(resolve => { resolveFirst = resolve })
    const latest = listing('latest', '/最新目录')
    apiMock.mockReturnValueOnce(first).mockResolvedValueOnce(latest)
    wrapper = mount(DirectoryPickerDialog, { attachTo: document.body, props: { open: true, storageId: 7, restrictToStorage: true } })
    await flushPromises()
    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(document.body.textContent).toContain('/最新目录')

    resolveFirst(listing('stale', '/旧目录'))
    await flushPromises()
    expect(document.body.textContent).toContain('/最新目录')
    expect(document.body.textContent).not.toContain('/旧目录')
  })
})
