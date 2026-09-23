<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useAttrs, watch } from 'vue'
import { cacheImage, getCachedImage, isTauriImageCacheAvailable } from '@/services/imageCache'
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
const displaySource = ref(isProtectedServerArtworkURL(props.src) ? '' : props.src)
const ready = ref(Boolean(displaySource.value))
let observer: IntersectionObserver | null = null
let generation = 0
let visible = false

watch(() => [props.cacheKey, props.src], () => {
  generation += 1
  displaySource.value = isProtectedServerArtworkURL(props.src) ? '' : props.src
  ready.value = Boolean(displaySource.value)
  if (visible)
    void resolveImage(generation)
})

async function resolveImage(currentGeneration: number) {
  const protectedArtwork = isProtectedServerArtworkURL(props.src)
  if (!isTauriImageCacheAvailable()) {
    displaySource.value = protectedArtwork ? '' : props.src
    ready.value = Boolean(displaySource.value)
    return
  }

  const cached = protectedArtwork ? null : await getCachedImage(props.cacheKey)
  if (currentGeneration !== generation)
    return
  if (cached) {
    displaySource.value = cached
    ready.value = true
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
    emit('error', props.src)
}

function handleImageError() {
  if (!isProtectedServerArtworkURL(props.src) && displaySource.value !== props.src && props.src) {
    displaySource.value = props.src
    ready.value = true
    return
  }
  displaySource.value = ''
  ready.value = false
  emit('error', props.src)
}

onMounted(() => {
  if (!('IntersectionObserver' in globalThis)) {
    visible = true
    void resolveImage(generation)
    return
  }
  observer = new IntersectionObserver((entries) => {
    if (!entries.some(entry => entry.isIntersecting))
      return
    visible = true
    observer?.disconnect()
    observer = null
    void resolveImage(generation)
  }, { rootMargin: '320px' })
  if (root.value)
    observer.observe(root.value)
})

onBeforeUnmount(() => observer?.disconnect())
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
