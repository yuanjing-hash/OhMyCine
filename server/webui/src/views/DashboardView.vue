<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'

interface Summary { initialized: boolean; recovery_required: boolean; users: number; active_users: number; roles: number; audit_events: number }
const summary = ref<Summary | null>(null); const loading = ref(true); const error = ref('')
const planned = [
  { title: 'OpenList/Alist 连接', detail: '下一切片将接入真实连接测试与加密凭据。' },
  { title: '存储目标', detail: '承载远端路径、STRM 输出与播放策略。' },
  { title: 'STRM → 302 → Emby', detail: '按纵向闭环交付，不返回伪成功。' },
]
async function load() { loading.value = true; error.value = ''; try { summary.value = await api('/api/v1/dashboard') } catch (reason) { error.value = reason instanceof Error ? reason.message : '加载失败' } finally { loading.value = false } }
onMounted(load)
</script>

<template>
  <section>
    <p class="mb-2 text-xs font-700 uppercase tracking-[.22em] text-cyan-300">Foundation online</p>
    <h1 class="m-0 text-3xl font-800">Server 仪表盘</h1>
    <p class="mt-2 text-slate-400">认证、会话、权限目录和管理审计已经形成第一条真实闭环。</p>
    <div v-if="error" class="mt-6 rounded-3 bg-red-400/10 p-4 text-red-200">{{ error }} <button class="ml-3 underline" @click="load">重试</button></div>
    <div v-else-if="loading" class="mt-8 text-slate-500">正在读取 Server 状态…</div>
    <template v-else-if="summary">
      <div class="mt-8 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <article v-for="item in [{label:'用户',value:summary.users},{label:'有效用户',value:summary.active_users},{label:'角色',value:summary.roles},{label:'审计事件',value:summary.audit_events}]" :key="item.label" class="panel"><div class="text-sm text-slate-500">{{ item.label }}</div><strong class="mt-2 block text-3xl">{{ item.value }}</strong></article>
      </div>
      <div class="mt-8 grid gap-5 xl:grid-cols-[1.2fr_.8fr]">
        <section class="panel"><h2 class="mt-0 text-lg">安全基线</h2><ul class="m-0 space-y-3 p-0 text-sm text-slate-300 list-none"><li>✓ Opaque 可撤销会话 + HttpOnly Cookie</li><li>✓ Session-bound CSRF + Origin / Fetch Metadata</li><li>✓ 多角色权限并集 + API 强制授权</li><li>✓ Owner / 最后管理员 / 防权限提升事务不变量</li><li>✓ 登录、用户和角色变更审计</li></ul></section>
        <section class="panel"><h2 class="mt-0 text-lg">运行状态</h2><div class="rounded-3 bg-emerald-400/10 p-4 text-sm text-emerald-200">数据库已初始化，管理基础可用。</div><p class="mb-0 mt-4 text-xs leading-5 text-slate-500">媒体业务尚未实现；界面不会把规划项伪装成成功。</p></section>
      </div>
      <h2 class="mb-4 mt-10 text-xl">下一纵向切片</h2>
      <div class="grid gap-4 md:grid-cols-3"><article v-for="item in planned" :key="item.title" class="panel opacity-75"><span class="rounded-full bg-white/8 px-2 py-1 text-[10px] uppercase tracking-wider text-slate-500">Planned</span><h3 class="mb-2 mt-4">{{ item.title }}</h3><p class="m-0 text-sm leading-6 text-slate-500">{{ item.detail }}</p></article></div>
    </template>
  </section>
</template>
