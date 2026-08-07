<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { navigationItems } from '@/navigation'

const route = useRoute()
const page = computed(() => navigationItems.find(item => item.to === route.path))
</script>

<template>
  <section class="mx-auto max-w-5xl">
    <p class="mb-2 text-xs font-700 uppercase tracking-[.22em] text-cyan-300">{{ page?.eyebrow ?? 'Planned' }}</p>
    <h1 class="m-0 text-3xl font-800">{{ page?.label ?? '规划页面' }}</h1>
    <p class="mt-2 max-w-3xl text-slate-400">{{ page?.description ?? '该页面尚未接入业务能力。' }}</p>
    <article class="panel mt-8 overflow-hidden p-0">
      <div class="border-b border-white/8 bg-white/3 px-5 py-4">
        <span class="status-chip status-chip--planned">规划中</span>
      </div>
      <div class="p-6 sm:p-8">
        <h2 class="m-0 text-xl">真实业务边界尚未配置</h2>
        <p class="mb-0 mt-3 max-w-2xl text-sm leading-7 text-slate-400">
          当前版本没有请求或伪造这个业务域的数据。对应的 permission catalog、服务端 API 与 service policy
          完成后，本页才会替换为真实状态和操作。
        </p>
      </div>
    </article>
  </section>
</template>
