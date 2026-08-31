export interface WindowFullscreenApi {
  isFullscreen: () => Promise<boolean>
  isMaximized: () => Promise<boolean>
  setFullscreen: (fullscreen: boolean) => Promise<void>
  maximize: () => Promise<void>
  unmaximize: () => Promise<void>
}

export interface WindowFullscreenTransitionResult {
  fullscreen: boolean
  restoreMaximizedOnExit: boolean
}

interface WindowFullscreenTransitionOptions {
  attempts?: number
  delayMs?: number
}

export async function transitionWindowFullscreen(
  windowApi: WindowFullscreenApi,
  nextFullscreen: boolean,
  restoreMaximizedOnExit: boolean,
  options: WindowFullscreenTransitionOptions = {},
): Promise<WindowFullscreenTransitionResult> {
  const currentFullscreen = await windowApi.isFullscreen()
  if (currentFullscreen === nextFullscreen) {
    if (!nextFullscreen && restoreMaximizedOnExit)
      await windowApi.maximize()
    return { fullscreen: currentFullscreen, restoreMaximizedOnExit: false }
  }

  let shouldRestoreMaximized = restoreMaximizedOnExit
  try {
    if (nextFullscreen) {
      shouldRestoreMaximized = await windowApi.isMaximized()
      if (shouldRestoreMaximized) {
        await windowApi.unmaximize()
        const unmaximized = await waitForMaximizedState(windowApi, false, options)
        if (!unmaximized)
          throw new Error('窗口没有退出最大化状态')
      }
    }

    await windowApi.setFullscreen(nextFullscreen)
    const applied = await waitForFullscreenState(windowApi, nextFullscreen, options)
    if (!applied)
      throw new Error(nextFullscreen ? '窗口没有进入全屏状态' : '窗口没有退出全屏状态')
  }
  catch (error) {
    if (nextFullscreen && shouldRestoreMaximized)
      await windowApi.maximize().catch(() => undefined)
    throw error
  }

  if (!nextFullscreen && shouldRestoreMaximized) {
    await windowApi.maximize()
    shouldRestoreMaximized = false
  }

  return {
    fullscreen: nextFullscreen,
    restoreMaximizedOnExit: shouldRestoreMaximized,
  }
}

async function waitForFullscreenState(
  windowApi: Pick<WindowFullscreenApi, 'isFullscreen'>,
  expected: boolean,
  options: WindowFullscreenTransitionOptions,
): Promise<boolean> {
  const attempts = Math.max(1, options.attempts ?? 8)
  const delayMs = Math.max(0, options.delayMs ?? 32)

  for (let attempt = 0; attempt < attempts; attempt++) {
    if (await windowApi.isFullscreen() === expected)
      return true
    if (attempt < attempts - 1 && delayMs > 0)
      await new Promise(resolve => globalThis.setTimeout(resolve, delayMs))
  }
  return false
}

async function waitForMaximizedState(
  windowApi: Pick<WindowFullscreenApi, 'isMaximized'>,
  expected: boolean,
  options: WindowFullscreenTransitionOptions,
): Promise<boolean> {
  const attempts = Math.max(1, options.attempts ?? 8)
  const delayMs = Math.max(0, options.delayMs ?? 32)

  for (let attempt = 0; attempt < attempts; attempt++) {
    if (await windowApi.isMaximized() === expected)
      return true
    if (attempt < attempts - 1 && delayMs > 0)
      await new Promise(resolve => globalThis.setTimeout(resolve, delayMs))
  }
  return false
}
