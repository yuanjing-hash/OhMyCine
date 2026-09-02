<script setup lang="ts">
defineProps<{
  title: string
  originalTitle?: string
  posterUrl?: string
  backdropUrl?: string
  titleLogoUrl?: string
  eyebrow?: string
  overview?: string
}>()

defineEmits<{ titleLogoError: [url: string] }>()
</script>

<template>
  <section
    class="detail-hero theme-immersive-dark relative min-h-[68vh] overflow-hidden bg-cover bg-center"
    :style="backdropUrl ? { backgroundImage: `url(${backdropUrl})` } : undefined"
  >
    <div class="pointer-events-none absolute inset-0 bg-gradient-to-r from-black/94 via-black/62 to-black/20" />
    <div class="pointer-events-none absolute inset-0 bg-gradient-to-t from-[var(--color-bg)] via-transparent to-black/40" />
    <div class="detail-hero-content relative flex min-h-[68vh] items-end gap-8 px-4 pb-10 pt-20 md:px-6 md:pb-12 md:pl-24 md:pt-24 lg:px-12 lg:pl-28">
      <div class="hidden w-56 flex-shrink-0 overflow-hidden rounded-[1.8rem] border border-white/12 bg-white/6 shadow-2xl md:block">
        <img v-if="posterUrl" :src="posterUrl" :alt="title" class="aspect-[2/3] w-full object-cover" loading="eager" decoding="async">
        <div v-else class="flex aspect-[2/3] items-center justify-center p-6 text-center text-sm text-white/45">
          {{ title }}
        </div>
      </div>

      <div class="max-w-4xl">
        <p v-if="!titleLogoUrl" class="text-xs uppercase tracking-[0.28em] text-white/42">
          {{ eyebrow || 'OhMyCine Detail' }}
        </p>
        <img
          v-if="titleLogoUrl"
          :src="titleLogoUrl"
          :alt="title"
          class="max-h-28 max-w-[min(30rem,78vw)] object-contain object-left drop-shadow-2xl"
          loading="eager"
          decoding="async"
          @error="$emit('titleLogoError', titleLogoUrl)"
        >
        <h1 :class="titleLogoUrl ? 'sr-only' : 'mt-3 text-3xl font-bold leading-tight drop-shadow-2xl sm:text-4xl lg:text-6xl'">
          {{ title }}
        </h1>
        <p v-if="originalTitle && originalTitle !== title" class="mt-2 text-sm text-white/48">
          {{ originalTitle }}
        </p>
        <div class="mt-4 flex flex-wrap items-center gap-3 text-sm text-white/68">
          <slot name="meta" />
        </div>
        <p v-if="overview" class="mt-5 max-w-3xl text-base leading-8 text-white/68 line-clamp-5">
          {{ overview }}
        </p>
        <div class="mt-7 flex flex-wrap items-center gap-3">
          <slot name="actions" />
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .detail-hero,
  .detail-hero-content {
    min-height: min(68svh, 38rem);
  }

  .detail-hero-content {
    padding-top: max(5rem, calc(env(safe-area-inset-top) + 4rem));
    padding-bottom: 2rem;
  }

  .detail-hero-content h1 {
    font-size: 2rem;
  }

  .detail-hero-content p.line-clamp-5 {
    -webkit-line-clamp: 3;
    font-size: 0.88rem;
    line-height: 1.65;
  }

  .detail-hero-content :deep(button),
  .detail-hero-content :deep(span.rounded-full) {
    min-height: 2.9rem;
    border-radius: 8px;
  }
}
</style>
