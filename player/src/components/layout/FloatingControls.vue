<script setup lang="ts">
import { ref } from 'vue'
import { useTheme } from '@/composables/useTheme'

const { theme, toggle: toggleTheme } = useTheme()
const isHovered = ref(false)
</script>

<template>
  <div
    class="fixed bottom-4 right-4 z-50"
    @mouseenter="isHovered = true"
    @mouseleave="isHovered = false"
  >
    <div class="absolute -inset-6" />

    <Transition name="fade-up">
      <div
        v-show="isHovered"
        class="glass-panel relative flex items-center gap-1 rounded-2xl p-1.5"
      >
        <!-- Player -->
        <button
          class="gp-btn flex h-10 w-10 items-center justify-center rounded-xl transition-all duration-200"
          title="Player"
        >
          <svg width="18" height="18" viewBox="0 0 20 20" fill="none">
            <path d="M5 3l12 7-12 7V3z" fill="currentColor" />
          </svg>
        </button>

        <div class="gp-divider h-5 w-px" />

        <!-- Theme toggle -->
        <button
          class="gp-btn theme-toggle relative flex h-10 w-10 items-center justify-center overflow-hidden rounded-xl transition-all duration-200"
          :class="{ 'is-light': theme === 'light' }"
          title="Toggle theme"
          @click="toggleTheme"
        >
          <svg
            class="icon-sun absolute"
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <circle cx="12" cy="12" r="5" />
            <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
          </svg>
          <svg
            class="icon-moon absolute"
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z" />
          </svg>
        </button>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.gp-btn {
  color: var(--gp-text);
}
.gp-btn:hover {
  color: var(--gp-text-full);
  background: var(--gp-hover);
}
.gp-divider {
  background: var(--gp-divider);
}

.fade-up-enter-active,
.fade-up-leave-active {
  transition: opacity 0.3s cubic-bezier(0.16, 1, 0.3, 1), transform 0.3s cubic-bezier(0.16, 1, 0.3, 1);
}

.fade-up-enter-from,
.fade-up-leave-to {
  opacity: 0;
  transform: translateY(8px);
}

.icon-sun,
.icon-moon {
  transition: transform 0.5s cubic-bezier(0.16, 1, 0.3, 1), opacity 0.3s ease;
}

.icon-sun {
  opacity: 0;
  transform: rotate(90deg) scale(0);
}

.icon-moon {
  opacity: 1;
  transform: rotate(0) scale(1);
}

.is-light .icon-sun {
  opacity: 1;
  transform: rotate(0) scale(1);
}

.is-light .icon-moon {
  opacity: 0;
  transform: rotate(-90deg) scale(0);
}
</style>
