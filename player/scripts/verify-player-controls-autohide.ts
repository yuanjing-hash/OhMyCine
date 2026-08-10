import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const [playerView, playerControls] = await Promise.all([
  source('src/views/PlayerView.vue'),
  source('src/components/player/PlayerControls.vue'),
])

const dismissBody = functionBody(playerControls, 'dismissTransientUi')
for (const state of [
  'pointerInside.value = false',
  'focusInside.value = false',
  'childInteracting.value = false',
  'activeMenu.value = null',
  'settingsPanelOpen.value = false',
  'settingsPanelInteracting.value = false',
]) {
  assert.ok(dismissBody.includes(state), `dismissTransientUi must clear stale state: ${state}`)
}
assert.match(dismissBody, /activeElement\.blur\(\)/)
assert.match(dismissBody, /emitInteractionState\(\)/)

const pointerLeaveBody = functionBody(playerControls, 'handlePointerLeave')
assert.match(pointerLeaveBody, /window\.setTimeout/)
assert.match(pointerLeaveBody, /if \(childInteracting\.value\)\s+return/)
assert.match(pointerLeaveBody, /activeMenu\.value = null/)
assert.match(pointerLeaveBody, /settingsPanelOpen\.value = false/)
assert.match(pointerLeaveBody, /if \(pointerOwnsFocus\)/)
assert.match(playerControls, /@mouseenter="handlePointerEnter"/)
assert.match(playerControls, /@mouseleave="handlePointerLeave"/)
assert.match(playerControls, /@pointerdown\.capture="markPointerInteraction"/)
assert.match(playerControls, /function handleKeydown\(event: KeyboardEvent\) \{\s+pointerOwnsFocus = false/)

for (const handler of ['handleWindowBlur', 'handleApplicationPointerLeave']) {
  const body = functionBody(playerView, handler)
  const dismissIndex = body.indexOf('playerControlsRef.value?.dismissTransientUi()')
  const resetIndex = body.indexOf('controlsInteracting.value = false')
  const scheduleIndex = body.indexOf('scheduleChromeHide()')
  assert.ok(dismissIndex >= 0 && resetIndex > dismissIndex, `${handler} must override child interaction after dismissal`)
  assert.ok(scheduleIndex > resetIndex, `${handler} must schedule hiding after state reset`)
  assert.match(body, /closePlaybackContextMenu\(false\)/)
}

assert.match(playerView, /appWindow\.onFocusChanged\(\(\{ payload: focused \}\) =>/)
assert.match(playerView, /if \(focused\)\s+handleWindowFocus\(\)\s+else\s+handleWindowBlur\(\)/)
assert.match(playerView, /nativeWindowFocusUnlisten\?\.\(\)/)
assert.match(functionBody(playerView, 'handleVisibilityChange'), /dismissTransientUi\(\)[\s\S]*controlsInteracting\.value = false[\s\S]*scheduleChromeHide\(\)/)

console.log(JSON.stringify({
  pointerClickFocusReleasedAfterControlExit: true,
  transientMenusReleasedAfterControlExit: true,
  staleChildInteractionCleared: true,
  browserAndNativeWindowBlurHandled: true,
  hiddenDocumentSchedulesAutoHide: true,
}, null, 2))

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

function functionBody(sourceText: string, name: string): string {
  const start = sourceText.indexOf(`function ${name}(`)
  assert.ok(start >= 0, `missing function ${name}`)
  const bodyStart = sourceText.indexOf('{', start)
  let depth = 0
  for (let index = bodyStart; index < sourceText.length; index++) {
    if (sourceText[index] === '{')
      depth += 1
    else if (sourceText[index] === '}')
      depth -= 1
    if (depth === 0)
      return sourceText.slice(bodyStart + 1, index)
  }
  throw new Error(`unterminated function ${name}`)
}
