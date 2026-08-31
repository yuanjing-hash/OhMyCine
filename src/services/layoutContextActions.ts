import { readonly, shallowRef } from 'vue'

export type LayoutContextActionIcon = 'rescrape' | 'scan' | 'folder'

export interface LayoutContextAction {
  readonly id: string
  readonly label: string
  readonly description: string
  readonly icon: LayoutContextActionIcon
  readonly disabled?: boolean
  readonly active?: boolean
  readonly execute: () => void | Promise<void>
}

const actions = shallowRef<readonly LayoutContextAction[]>([])
let activeOwner: symbol | null = null

export function setLayoutContextActions(owner: symbol, nextActions: readonly LayoutContextAction[]) {
  activeOwner = owner
  actions.value = nextActions
}

export function clearLayoutContextActions(owner: symbol) {
  if (activeOwner !== owner)
    return

  activeOwner = null
  actions.value = []
}

export function useLayoutContextActions() {
  return {
    actions: readonly(actions),
  }
}
