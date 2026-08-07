<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { ListResponse } from '@/types/api'

interface AuditEntry { id: number; actor_id: number | null; action: string; target_type: string; target_id: string; outcome: string; metadata: Record<string, unknown>; request_id: string; ip_hint: string; created_at: string }
const entries = ref<AuditEntry[]>([]); const loading = ref(true); const error = ref('')
async function load() { loading.value = true; try { entries.value = (await api<ListResponse<AuditEntry>>('/api/v1/audit?limit=100')).list } catch (reason) { error.value = reason instanceof Error ? reason.message : '加载失败' } finally { loading.value = false } }
onMounted(load)
</script>

<template>
  <section><p class="mb-2 text-xs font-700 uppercase tracking-[.22em] text-cyan-300">Observability</p><h1 class="m-0 text-3xl font-800">审计日志</h1><p class="mt-2 text-slate-400">仅记录动作、目标和安全元数据，不记录密码、Cookie 或会话令牌。</p><p v-if="error" class="mt-5 rounded-3 bg-red-400/10 p-3 text-red-200">{{ error }}</p><div v-if="loading" class="mt-8 text-slate-500">正在加载审计记录…</div><div v-else-if="entries.length === 0" class="panel mt-8 text-slate-500">尚无审计事件。</div><div v-else class="panel mt-7 overflow-x-auto p-0"><table class="w-full min-w-200 border-collapse text-left text-sm"><thead class="text-xs uppercase tracking-wider text-slate-500"><tr><th class="p-4">时间</th><th class="p-4">动作</th><th class="p-4">目标</th><th class="p-4">操作者</th><th class="p-4">结果</th><th class="p-4">元数据</th></tr></thead><tbody><tr v-for="entry in entries" :key="entry.id" class="border-t border-white/7"><td class="p-4 text-slate-400">{{ new Date(entry.created_at).toLocaleString() }}</td><td class="p-4 font-600">{{ entry.action }}</td><td class="p-4 text-slate-400">{{ entry.target_type }}:{{ entry.target_id }}</td><td class="p-4 text-slate-400">{{ entry.actor_id ?? 'anonymous' }}</td><td class="p-4" :class="entry.outcome === 'success' ? 'text-emerald-300' : 'text-amber-300'">{{ entry.outcome }}</td><td class="max-w-80 truncate p-4 font-mono text-xs text-slate-500" :title="JSON.stringify(entry.metadata)">{{ JSON.stringify(entry.metadata) }}</td></tr></tbody></table></div></section>
</template>
