import assert from 'node:assert/strict'
import fs from 'node:fs'
import { navigateLayoutBack, registerLayoutBackHandler } from '../src/services/layoutBackNavigation.ts'
import { loadSourceBrowseContext, saveSourceBrowseContext, sourceBrowseContextIdFromQuery } from '../src/services/sourceBrowseContext.ts'

const sensitiveFolderId = 'D:\\Media\\动画电影'
const browseContextId = saveSourceBrowseContext({
  sourceId: 'server-home',
  viewMode: 'folders',
  selectedLibrary: {
    id: '115-library',
    sourceId: 'server-home',
    name: '115测试盘',
    type: 'movies',
  },
  navigationStack: [
    { id: '115-library', name: '115测试盘', type: 'movies' },
    { id: sensitiveFolderId, name: '动画电影', type: 'folder' },
  ],
  selectedScannedCategoryId: null,
  searchKeyword: '',
  scrollTop: 720,
})
assert.ok(!browseContextId.includes(sensitiveFolderId))
assert.equal(sourceBrowseContextIdFromQuery([null, browseContextId]), browseContextId)
assert.equal(loadSourceBrowseContext(browseContextId, 'another-source'), null)
const restoredBrowseContext = loadSourceBrowseContext(browseContextId, 'server-home')
assert.equal(restoredBrowseContext?.navigationStack.at(-1)?.name, '动画电影')
assert.equal(restoredBrowseContext?.scrollTop, 720)
if (restoredBrowseContext)
  (restoredBrowseContext.navigationStack as Array<{ name: string }>)[1]!.name = 'mutated'
assert.equal(loadSourceBrowseContext(browseContextId, 'server-home')?.navigationStack.at(-1)?.name, '动画电影')

const calls: string[] = []
const router = {
  back: () => calls.push('router.back'),
  push: async (path: string) => { calls.push(`router.push:${path}`) },
}
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: { history: { state: { back: '/source/server-home' } } },
})

const owner = Symbol('server-library-navigation-test')
const unregister = registerLayoutBackHandler(owner, async () => {
  calls.push('internal.back')
  return true
})
await navigateLayoutBack(router as never)
assert.deepEqual(calls, ['internal.back'])

unregister()
await navigateLayoutBack(router as never)
assert.deepEqual(calls, ['internal.back', 'router.back'])

window.history.state.back = null
await navigateLayoutBack(router as never)
assert.deepEqual(calls, ['internal.back', 'router.back', 'router.push:/'])

const sourceView = fs.readFileSync(new URL('../src/views/SourceLibraryView.vue', import.meta.url), 'utf8')
assert.match(sourceView, /registerLayoutBackHandler\(layoutContextOwner, handleInPageBack\)/)
assert.match(sourceView, /navigationStack\.value\.length > 1[\s\S]*navigateToCrumb\(navigationStack\.value\.length - 2\)/)
assert.match(sourceView, /selectedLibrary\.value[\s\S]*backToLibraries\(\)/)
assert.match(sourceView, /await persistSourceBrowseContext\(\{ captureScroll: true \}\)[\s\S]*name: 'media-detail'/)
assert.match(sourceView, /await restoreSourceBrowseContext\(\)/)
assert.match(sourceView, /browseContextId/)
const backButton = fs.readFileSync(new URL('../src/components/layout/BackButton.vue', import.meta.url), 'utf8')
const windowChrome = fs.readFileSync(new URL('../src/components/layout/WindowChrome.vue', import.meta.url), 'utf8')
assert.match(backButton, /navigateLayoutBack\(router\)/)
assert.match(windowChrome, /navigateLayoutBack\(router\)/)

console.log(JSON.stringify({
  internalHierarchyFirst: true,
  rootRouterFallback: true,
  sharedBackHandler: true,
  detailBackContextRestored: true,
  providerPathKeptOutOfRoute: true,
}, null, 2))
