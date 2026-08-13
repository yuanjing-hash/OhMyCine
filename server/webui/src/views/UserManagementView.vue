<script setup lang="ts">
import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { getVisibleUserManagementTabs } from '@/navigation'

const auth = useAuthStore()
const tabs = computed(() => getVisibleUserManagementTabs(auth.user?.permissions ?? []))
</script>

<template>
  <section>
    <header class="mb-6">
      <h1 class="m-0 text-3xl font-800">用户管理</h1>
      <p class="page-description mt-2">账户、角色与权限按当前用户的读取权限分别开放；后端授权仍是安全边界。</p>
      <nav class="management-tabs mt-6" aria-label="用户管理">
        <RouterLink v-for="tab in tabs" :key="tab.id" :to="tab.to" class="management-tab" active-class="management-tab--active">
          {{ tab.label }}
        </RouterLink>
      </nav>
    </header>
    <RouterView />
  </section>
</template>
