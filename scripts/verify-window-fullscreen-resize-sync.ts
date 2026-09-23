import assert from 'node:assert/strict'
import { transitionWindowFullscreen } from '../src/services/windowFullscreen.ts'

const calls: string[] = []
let fullscreen = false
let maximized = true
const windowApi = {
  async isFullscreen() {
    calls.push('isFullscreen')
    return fullscreen
  },
  async isMaximized() {
    calls.push('isMaximized')
    return maximized
  },
  async setFullscreen(next: boolean) {
    calls.push(`setFullscreen:${next}`)
    fullscreen = next
  },
  async maximize() {
    calls.push('maximize')
    maximized = true
  },
  async unmaximize() {
    calls.push('unmaximize')
    maximized = false
  },
}

const entered = await transitionWindowFullscreen(windowApi, true, false, { delayMs: 0 })
assert.equal(entered.fullscreen, true)
assert.equal(entered.restoreMaximizedOnExit, true)
assert.deepEqual(calls.slice(0, 5), [
  'isFullscreen',
  'isMaximized',
  'unmaximize',
  'isMaximized',
  'setFullscreen:true',
])
assert.equal(calls[5], 'isFullscreen')

calls.length = 0
const exited = await transitionWindowFullscreen(windowApi, false, entered.restoreMaximizedOnExit, { delayMs: 0 })
assert.equal(exited.fullscreen, false)
assert.equal(exited.restoreMaximizedOnExit, false)
assert.deepEqual(calls, ['isFullscreen', 'setFullscreen:false', 'isFullscreen', 'maximize'])

fullscreen = false
maximized = true
calls.length = 0
const noOpWindowApi = {
  ...windowApi,
  async setFullscreen(next: boolean) {
    calls.push(`setFullscreen-noop:${next}`)
  },
}
await assert.rejects(
  transitionWindowFullscreen(noOpWindowApi, true, false, { attempts: 1, delayMs: 0 }),
  /窗口没有进入全屏状态/,
)
assert.equal(maximized, true)
assert.equal(calls.at(-1), 'maximize')

console.log('window fullscreen transition behavior verification passed')
