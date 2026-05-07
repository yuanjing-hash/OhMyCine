<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()

const sourceId = computed(() => route.params.sourceId as string)
const sourceConfig = computed(() =>
  store.configs.find(c => c.id === sourceId.value),
)
</script>

<template>
  <div class="relative min-h-full">
    <div class="space-y-8 p-6 pl-20 pt-16">
      <!-- Source not found -->
      <div v-if="!sourceConfig" class="flex flex-col items-center justify-center py-24">
        <p class="text-lg text-white/40">
          Data source not found
        </p>
        <button
          class="mt-4 rounded-full bg-white/10 px-6 py-2 text-sm text-white/70 transition-colors hover:bg-white/20"
          @click="router.push('/')"
        >
          Back to Home
        </button>
      </div>

      <template v-else>
        <!-- Source header -->
        <div class="flex items-center gap-4">
          <div class="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/18 text-lg font-bold text-primary">
            <img v-if="sourceConfig.iconUrl" :src="sourceConfig.iconUrl" class="h-8 w-8 rounded" :alt="sourceConfig.name">
            <span v-else>{{ sourceConfig.type[0].toUpperCase() }}</span>
          </div>
          <div>
            <h1 class="text-2xl font-bold text-white">
              {{ sourceConfig.displayName ?? sourceConfig.name }}
            </h1>
            <p class="text-sm text-white/40">
              {{ sourceConfig.type }}
            </p>
          </div>
        </div>

        <!-- Featured items hero (placeholder) -->
        <section>
          <div class="glass rounded-2xl p-8 text-center">
            <p class="text-lg text-white/40">
              Library browsing coming soon
            </p>
            <p class="mt-2 text-sm text-white/25">
              Connect a data source and browse its libraries, collections, and media
            </p>
          </div>
        </section>

        <!-- Libraries placeholder -->
        <section>
          <h2 class="mb-4 text-xl font-bold text-white">
            Libraries
          </h2>
          <div class="grid grid-cols-3 gap-4">
            <div
              v-for="i in 3"
              :key="i"
              class="glass-card cursor-pointer rounded-2xl p-6 text-center"
            >
              <div class="mx-auto mb-3 flex h-14 w-14 items-center justify-center rounded-2xl bg-white/5">
                <span class="text-2xl opacity-30">
                  {{ i === 1 ? '🎬' : i === 2 ? '📺' : '📹' }}
                </span>
              </div>
              <p class="text-sm font-medium text-white/50">
                {{ i === 1 ? 'Movies' : i === 2 ? 'TV Shows' : 'Other' }}
              </p>
              <p class="mt-1 text-xs text-white/25">
                -- items
              </p>
            </div>
          </div>
        </section>
      </template>
    </div>
  </div>
</template>
