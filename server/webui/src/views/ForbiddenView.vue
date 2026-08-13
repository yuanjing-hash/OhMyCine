<script setup lang="ts">
import ThemeToggle from '@/components/ThemeToggle.vue'
</script>

<template>
  <section class="panel mx-auto mt-12 max-w-xl text-center">
    <div class="flex justify-end"><ThemeToggle /></div>
    <div class="mt-4 text-5xl font-700">403</div>
    <h1>没有访问权限</h1>
    <p class="page-description">当前账户缺少此页面使用的 permission code。后端 API 会执行同样的权限校验。</p>
    <RouterLink class="btn-primary mt-3" to="/">返回仪表盘</RouterLink>
  </section>
</template>
