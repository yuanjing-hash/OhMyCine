import type { Router } from 'vue-router'

type LayoutBackHandler = () => boolean | Promise<boolean>

let activeOwner: symbol | null = null
let activeHandler: LayoutBackHandler | null = null
let navigating = false

export function registerLayoutBackHandler(owner: symbol, handler: LayoutBackHandler): () => void {
  activeOwner = owner
  activeHandler = handler
  return () => {
    if (activeOwner !== owner)
      return
    activeOwner = null
    activeHandler = null
  }
}

export async function navigateLayoutBack(router: Router): Promise<void> {
  if (navigating)
    return
  navigating = true
  try {
    if (activeHandler && await activeHandler())
      return
    if (window.history.state?.back)
      router.back()
    else
      await router.push('/')
  }
  finally {
    navigating = false
  }
}
