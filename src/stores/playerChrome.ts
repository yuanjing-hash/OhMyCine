import { defineStore } from 'pinia'
import { ref } from 'vue'

export const usePlayerChromeStore = defineStore('playerChrome', () => {
  const visible = ref(true)
  const fullscreen = ref(false)
  const fullscreenTransitioning = ref(false)
  const screenshotRequest = ref(0)

  function setVisible(nextVisible: boolean) {
    visible.value = nextVisible
  }

  function setFullscreen(nextFullscreen: boolean) {
    fullscreen.value = nextFullscreen
  }

  function setFullscreenTransitioning(nextTransitioning: boolean) {
    fullscreenTransitioning.value = nextTransitioning
  }

  function requestScreenshot() {
    screenshotRequest.value += 1
  }

  return { visible, fullscreen, fullscreenTransitioning, screenshotRequest, setVisible, setFullscreen, setFullscreenTransitioning, requestScreenshot }
})
