<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useAttrs, watch } from 'vue'
import { cacheImage, getCachedImage, isTauriImageCacheAvailable } from '@/services/imageCache'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  cacheKey: string
  src?: string
  alt?: string
}>(), {
  src: '',
  alt: '',
})

const attrs = useAttrs()
const root = ref<HTMLElement | null>(null)
const displaySource = ref(props.src)
const ready = ref(Boolean(props.src))
let observer: IntersectionObserver | null = null
let generation = 0
let visible = false

watch(() => [props.cacheKey, props.src], () => {
  generation += 1
  displaySource.value = props.src
  ready.value = Boolean(props.src)
  if (visible)
    void resolveImage(generation)
})

async function resolveImage(currentGeneration: number) {
  if (!isTauriImageCacheAvailable()) {
    ready.value = Boolean(props.src)
    return
  }

  const cached = await getCachedImage(props.cacheKey)
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
}

function handleImageError() {
  if (displaySource.value !== props.src && props.src) {
    displaySource.value = props.src
    ready.value = true
    return
  }
  displaySource.value = ''
  ready.value = false
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
