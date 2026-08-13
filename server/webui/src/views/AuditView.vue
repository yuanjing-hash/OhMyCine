<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { ListResponse } from '@/types/api'

interface AuditEntry { id: number; actor_id: number | null; action: string; target_type: string; target_id: string; outcome: string; metadata: Record<string, unknown>; request_id: string; ip_hint: string; created_at: string }
const entries = ref<AuditEntry[]>([]); const loading = ref(true); const error = ref('')
async function load() { loading.value = true; error.value = ''; try { entries.value = (await api<ListResponse<AuditEntry>>('/api/v1/audit?limit=100')).list } catch (reason) { error.value = reason instanceof Error ? reason.message : '加载失败' } finally { loading.value = false } }
onMounted(load)
</script>

<template>
  <section>
    <h1 class="m-0 text-3xl font-800">审计日志</h1>
    <p class="page-description mt-2">仅记录动作、目标和安全元数据，不记录密码、Cookie 或会话令牌。</p>
    <p v-if="error" class="semantic-error mt-5 p-3" role="alert">{{ error }}</p>
    <div v-if="loading" class="text-subtle mt-8">正在加载审计记录…</div>
    <div v-else-if="entries.length === 0" class="text-subtle panel mt-8">尚无审计事件。</div>
    <div v-else class="panel mt-7 overflow-x-auto p-0">
      <table class="w-full min-w-200 border-collapse text-left text-sm">
        <thead class="text-subtle text-xs"><tr><th class="p-4">时间</th><th class="p-4">动作</th><th class="p-4">目标</th><th class="p-4">操作者</th><th class="p-4">结果</th><th class="p-4">元数据</th></tr></thead>
        <tbody><tr v-for="entry in entries" :key="entry.id" class="semantic-divider border-t"><td class="text-muted p-4">{{ new Date(entry.created_at).toLocaleString() }}</td><td class="p-4 font-600">{{ entry.action }}</td><td class="text-muted p-4">{{ entry.target_type }}:{{ entry.target_id }}</td><td class="text-muted p-4">{{ entry.actor_id ?? 'anonymous' }}</td><td class="p-4" :class="entry.outcome === 'success' ? 'semantic-success-text' : 'semantic-warning-text'">{{ entry.outcome }}</td><td class="text-subtle max-w-80 truncate p-4 font-mono text-xs" :title="JSON.stringify(entry.metadata)">{{ JSON.stringify(entry.metadata) }}</td></tr></tbody>
      </table>
    </div>
  </section>
</template>
