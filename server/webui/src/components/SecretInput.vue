<script setup lang="ts">
import { computed, ref, useAttrs, watch } from 'vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  modelValue: string
  configured?: boolean
  multiline?: boolean
  configuredLabel?: string
}>(), {
  configured: false,
  multiline: false,
  configuredLabel: '••••••••（已配置）',
})

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const attrs = useAttrs()
const revealed = ref(false)
const placeholder = computed(() => props.configured && !props.modelValue
  ? props.configuredLabel
  : typeof attrs.placeholder === 'string' ? attrs.placeholder : undefined)
const revealLabel = computed(() => revealed.value ? '隐藏当前输入内容' : '显示当前输入内容')
const disabled = computed(() => attrs.disabled !== undefined && attrs.disabled !== false)

watch(() => props.modelValue, (value) => {
  if (!value)
    revealed.value = false
})

function update(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement | HTMLTextAreaElement).value)
}
</script>

<template>
  <div class="relative w-full">
    <textarea
      v-if="multiline"
      v-bind="$attrs"
      :value="modelValue"
      :placeholder="placeholder"
      :class="{ 'secret-input--masked': !revealed }"
      class="pr-12"
      @input="update"
    />
    <input
      v-else
      v-bind="$attrs"
      :value="modelValue"
      :placeholder="placeholder"
      :type="revealed ? 'text' : 'password'"
      class="pr-12"
      @input="update"
    />
    <button
      type="button"
      class="secret-input__toggle absolute right-2 top-2 flex h-8 w-8 items-center justify-center rounded-md text-[var(--text-muted)] transition hover:bg-[var(--surface-muted)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-45"
      :aria-label="revealLabel"
      :title="configured && !modelValue ? '已保存凭据不会回传；输入新值后可查看' : revealLabel"
      :disabled="!modelValue || disabled"
      @click="revealed = !revealed"
    >
      <svg v-if="revealed" aria-hidden="true" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="m3 3 18 18" />
        <path d="M10.6 10.7a2 2 0 0 0 2.7 2.7" />
        <path d="M9.9 4.2A10.5 10.5 0 0 1 12 4c5 0 8.5 4 9.5 6.2a4.4 4.4 0 0 1 0 3.6 11.4 11.4 0 0 1-2 3" />
        <path d="M6.6 6.6a13.2 13.2 0 0 0-4.1 5.2 4.4 4.4 0 0 0 0 3.6C3.5 17.7 7 21 12 21a10.8 10.8 0 0 0 4-.8" />
      </svg>
      <svg v-else aria-hidden="true" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="M2.5 12s3.5-7 9.5-7 9.5 7 9.5 7-3.5 7-9.5 7-9.5-7-9.5-7Z" />
        <circle cx="12" cy="12" r="3" />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.secret-input--masked:not(:placeholder-shown) {
  -webkit-text-security: disc;
}
</style>
