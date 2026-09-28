import { defineStore } from 'pinia'
import { ref } from 'vue'

export const usePlayerChromeStore = defineStore('playerChrome', () => {
  const visible = ref(true)
  const fullscreen = ref(false)
  const fullscreenTransitioning = ref(false)

  function setVisible(nextVisible: boolean) {
    visible.value = nextVisible
  }

  function setFullscreen(nextFullscreen: boolean) {
    fullscreen.value = nextFullscreen
  }

  function setFullscreenTransitioning(nextTransitioning: boolean) {
    fullscreenTransitioning.value = nextTransitioning
  }

  return { visible, fullscreen, fullscreenTransitioning, setVisible, setFullscreen, setFullscreenTransitioning }
})
