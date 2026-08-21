<script setup lang="ts">
import { dismissToast, toasts } from '@/toast'
</script>

<template>
  <Teleport to="body">
    <div class="toast-viewport" aria-live="polite" aria-atomic="false">
      <TransitionGroup name="toast">
        <article v-for="item in toasts" :key="item.id" class="toast-item" :class="`toast-item--${item.kind}`" :role="item.kind === 'error' ? 'alert' : 'status'">
          <span class="toast-item__mark" aria-hidden="true">{{ item.kind === 'success' ? '✓' : item.kind === 'error' ? '!' : item.kind === 'warning' ? '!' : 'i' }}</span>
          <p>{{ item.message }}</p>
          <button type="button" aria-label="关闭通知" @click="dismissToast(item.id)">×</button>
        </article>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
