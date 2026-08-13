<script setup lang="ts">
import type { MediaLibraryDraft } from '@/media-libraries'

defineProps<{ disabled?: boolean }>()
const model = defineModel<MediaLibraryDraft>({ required: true })
</script>

<template>
  <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
    <div><label class="label">周期全量间隔（小时）</label><input v-model.number="model.full_scan_interval_hours" class="input" type="number" min="1" max="720" :disabled="disabled" /></div>
    <div><label class="label">定时增量间隔（分钟）</label><input v-model.number="model.incremental_minutes" class="input" type="number" min="1" max="1440" :disabled="disabled" /></div>
    <div><label class="label">Provider 请求 / 秒</label><input v-model.number="model.provider_rate_per_second" class="input" type="number" min="1" max="1000" :disabled="disabled" /></div>
    <div><label class="label">Provider 并发</label><input v-model.number="model.provider_concurrency" class="input" type="number" min="1" max="32" :disabled="disabled" /></div>
    <div><label class="label">Metadata 请求 / 秒</label><input v-model.number="model.metadata_rate_per_second" class="input" type="number" min="1" max="100" :disabled="disabled" /></div>
    <div><label class="label">Metadata 并发</label><input v-model.number="model.metadata_concurrency" class="input" type="number" min="1" max="16" :disabled="disabled" /></div>
    <div><label class="label">TMDB 语言</label><input v-model="model.metadata_language" class="input" maxlength="16" :disabled="disabled" /></div>
    <div><label class="label">TMDB 地区</label><input v-model="model.metadata_region" class="input" maxlength="8" :disabled="disabled" /></div>
    <div><label class="label">匹配策略</label><select v-model="model.match_strategy" class="input" :disabled="disabled"><option value="balanced">平衡</option><option value="strict">严格</option><option value="lenient">宽松</option></select></div>
    <div class="md:col-span-2"><label class="label">视频扩展名（逗号分隔）</label><textarea v-model="model.video_extensions_text" class="input" rows="3" :disabled="disabled"></textarea></div>
    <div class="md:col-span-2"><label class="label">忽略规则（每行一个）</label><textarea v-model="model.ignore_patterns_text" class="input" rows="3" :disabled="disabled"></textarea></div>
  </div>
</template>
