import assert from 'node:assert/strict'
import fs from 'node:fs'
import { navigateLayoutBack, registerLayoutBackHandler } from '../src/services/layoutBackNavigation.ts'

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
const backButton = fs.readFileSync(new URL('../src/components/layout/BackButton.vue', import.meta.url), 'utf8')
const windowChrome = fs.readFileSync(new URL('../src/components/layout/WindowChrome.vue', import.meta.url), 'utf8')
assert.match(backButton, /navigateLayoutBack\(router\)/)
assert.match(windowChrome, /navigateLayoutBack\(router\)/)

console.log(JSON.stringify({ internalHierarchyFirst: true, rootRouterFallback: true, sharedBackHandler: true }, null, 2))
