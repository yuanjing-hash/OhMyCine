import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useSearchWorkspaceStore = defineStore('search-workspace', () => {
  const open = ref(false)

  function show() {
    open.value = true
  }

  function hide() {
    open.value = false
  }

  function toggle() {
    open.value = !open.value
  }

  return { open, show, hide, toggle }
})
