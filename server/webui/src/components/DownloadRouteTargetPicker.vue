<script setup lang="ts">
import { computed } from 'vue'
import { formatRouteBytes, routeTargetByID, type DownloadRoutePreview } from '@/download-routes'

const props = withDefaults(defineProps<{
  modelValue: number
  preview: DownloadRoutePreview | null
  loading?: boolean
  disabled?: boolean
  emptyMessage?: string
}>(), { loading: false, disabled: false, emptyMessage: '当前下载器没有可执行的入库路线。' })

const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
const selected = computed(() => routeTargetByID(props.preview, props.modelValue))
</script>

<template>
  <div>
    <label class="label">目标媒体库</label>
    <div v-if="loading" class="semantic-inset p-3 text-sm text-muted" aria-live="polite">正在计算可执行的入库路线…</div>
    <div v-else-if="!preview?.options.length" class="semantic-warning p-3 text-sm">{{ emptyMessage }}</div>
    <div v-else class="grid gap-2" role="radiogroup" aria-label="目标媒体库">
      <label v-for="option in preview.options" :key="option.media_library_id" class="semantic-list-item flex items-start gap-3 p-3" :class="{ 'semantic-list-item--selected': modelValue === option.media_library_id, 'opacity-60': !option.enabled }">
        <input :checked="modelValue === option.media_library_id" type="radio" name="download-route-target" :disabled="disabled || !option.enabled" :value="option.media_library_id" @change="emit('update:modelValue', option.media_library_id)" />
        <span class="min-w-0 flex-1">
          <span class="flex flex-wrap items-center gap-2"><strong>{{ option.library_name }}</strong><span class="status-chip">{{ option.route_label }}</span></span>
          <small class="text-subtle mt-1 block">{{ option.storage_name }}<template v-if="option.requires_managed_staging">· 需 Server 本地暂存</template></small>
          <small v-if="option.required_bytes != null" class="text-subtle mt-1 block">预计占用 {{ formatRouteBytes(option.required_bytes) }}<template v-if="option.available_bytes != null">· 可用 {{ formatRouteBytes(option.available_bytes) }}</template></small>
          <small v-if="!option.enabled" class="semantic-danger-text mt-1 block">{{ option.reason_message || option.reason_code || '当前路线不可用' }}</small>
        </span>
      </label>
    </div>
    <p v-if="selected?.requires_managed_staging" class="semantic-warning mb-0 mt-2 p-3 text-xs">这是跨数据源入库：来源会先完整进入 Server 受管暂存区，校验和刮削后再写入目标库。
    </p>
  </div>
</template>
