import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useAcquisitionWorkspaceStore = defineStore('acquisition-workspace', () => {
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
