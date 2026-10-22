<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useAttrs, watch } from 'vue'
import { cacheImage, getCachedImage, isTauriImageCacheAvailable, peekCachedImage, serverArtworkCredentialGeneration, subscribeServerArtworkSources } from '@/services/imageCache'
import { isProtectedServerArtworkURL } from '@/services/serverArtwork'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  cacheKey: string
  src?: string
  alt?: string
}>(), {
  src: '',
  alt: '',
})

const emit = defineEmits<{ error: [url: string] }>()

const attrs = useAttrs()
const root = ref<HTMLElement | null>(null)
const initialDisplaySource = () => isProtectedServerArtworkURL(props.src) ? peekCachedImage(props.cacheKey, props.src) ?? '' : props.src
const displaySource = ref(initialDisplaySource())
const ready = ref(Boolean(displaySource.value))
const retryDelays = [600, 1800] as const
let observer: IntersectionObserver | null = null
let generation = 0
let visible = false
let disposed = false
let attempts = 0
let inFlightGeneration: number | null = null
let retryTimer: ReturnType<typeof setTimeout> | undefined
let credentialGeneration = serverArtworkCredentialGeneration(props.cacheKey, props.src)

function clearRetry() {
  clearTimeout(retryTimer)
  retryTimer = undefined
}

function resetImage() {
  generation += 1
  attempts = 0
  clearRetry()
  credentialGeneration = serverArtworkCredentialGeneration(props.cacheKey, props.src)
  displaySource.value = initialDisplaySource()
  ready.value = Boolean(displaySource.value)
  if (visible)
    requestImage()
}

const unsubscribeArtworkSources = subscribeServerArtworkSources(() => {
  if (credentialGeneration !== serverArtworkCredentialGeneration(props.cacheKey, props.src))
    resetImage()
})

watch(() => [props.cacheKey, props.src], () => {
  resetImage()
})

function requestImage() {
  if (disposed || !visible || inFlightGeneration === generation || attempts > retryDelays.length)
    return
  if (isProtectedServerArtworkURL(props.src) && credentialGeneration === null)
    return
  clearRetry()
  const currentGeneration = generation
  inFlightGeneration = currentGeneration
  attempts += 1
  void resolveImage(currentGeneration).finally(() => {
    if (inFlightGeneration === currentGeneration)
      inFlightGeneration = null
  })
}

function retryImage(currentGeneration: number) {
  if (disposed || currentGeneration !== generation)
    return
  const delay = retryDelays[attempts - 1]
  if (delay === undefined) {
    emit('error', props.src)
    return
  }
  if (!visible || retryTimer !== undefined)
    return
  retryTimer = setTimeout(() => {
    retryTimer = undefined
    requestImage()
  }, delay)
}

async function resolveImage(currentGeneration: number) {
  const protectedArtwork = isProtectedServerArtworkURL(props.src)
  if (!isTauriImageCacheAvailable()) {
    displaySource.value = protectedArtwork ? '' : props.src
    ready.value = Boolean(displaySource.value)
    return
  }

  const cached = await getCachedImage(props.cacheKey, props.src)
  if (currentGeneration !== generation)
    return
  if (cached) {
    displaySource.value = cached
    ready.value = true
    return
  }
  else if (!props.src) {
    displaySource.value = ''
    ready.value = false
  }

  if (!props.src)
    return
  const resolved = await cacheImage(props.cacheKey, props.src)
  if (currentGeneration !== generation)
    return
  displaySource.value = resolved
  ready.value = Boolean(resolved)
  if (!resolved)
    retryImage(currentGeneration)
}

function handleImageError(event: Event) {
  const image = event.target as HTMLImageElement
  if (disposed || !image.isConnected || image.getAttribute('src') !== displaySource.value)
    return
  if (!isProtectedServerArtworkURL(props.src) && displaySource.value !== props.src && props.src) {
    displaySource.value = props.src
    ready.value = true
    return
  }
  displaySource.value = ''
  ready.value = false
  // A browser URL can fail before the lazy native-cache lookup starts.
  attempts = Math.max(1, attempts)
  retryImage(generation)
}

function handleOnline() {
  if (!ready.value)
    requestImage()
}

onMounted(() => {
  globalThis.addEventListener('online', handleOnline)
  if (!('IntersectionObserver' in globalThis)) {
    visible = true
    requestImage()
    return
  }
  observer = new IntersectionObserver((entries) => {
    const wasVisible = visible
    visible = entries.some(entry => entry.isIntersecting)
    if (!visible) {
      clearRetry()
      return
    }
    if (!wasVisible && (!ready.value || attempts === 0))
      requestImage()
  }, { rootMargin: '320px' })
  if (root.value)
    observer.observe(root.value)
})

onBeforeUnmount(() => {
  disposed = true
  generation += 1
  clearRetry()
  observer?.disconnect()
  unsubscribeArtworkSources()
  globalThis.removeEventListener('online', handleOnline)
})
</script>

<template>
  <span ref="root" class="cached-image-host block h-full w-full">
    <img
      v-if="ready && displaySource"
      v-bind="attrs"
      :src="displaySource"
      :alt="alt"
      @error="handleImageError"
    >
    <slot v-else name="fallback" />
  </span>
</template>
