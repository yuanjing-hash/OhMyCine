import { defineStore } from 'pinia'
import { ref } from 'vue'

export const usePlayerChromeStore = defineStore('playerChrome', () => {
  const visible = ref(true)

  function setVisible(nextVisible: boolean) {
    visible.value = nextVisible
  }

  return { visible, setVisible }
})
