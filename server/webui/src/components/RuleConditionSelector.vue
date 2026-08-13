<script setup lang="ts">
import type { RuleSetCondition } from '@/types/api'

const props = defineProps<{ label: string; options: readonly (string | number)[]; modelValue: RuleSetCondition<string | number>; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: RuleSetCondition<string | number>] }>()
function state(option: string | number) { return props.modelValue.include.includes(option) ? 'include' : props.modelValue.exclude.includes(option) ? 'exclude' : 'none' }
function setState(option: string | number, value: string) {
  emit('update:modelValue', {
    include: props.modelValue.include.filter(item => item !== option).concat(value === 'include' ? [option] : []),
    exclude: props.modelValue.exclude.filter(item => item !== option).concat(value === 'exclude' ? [option] : []),
  })
}
</script>

<template>
  <fieldset class="m-0 border-0 p-0"><legend class="label">{{ label }}</legend>
    <div class="condition-grid"><label v-for="option in options" :key="option" class="condition-option"><span>{{ option }}</span><select class="input condition-select" :value="state(option)" :disabled="disabled" :aria-label="`${label} ${option}`" @change="setState(option, ($event.target as HTMLSelectElement).value)"><option value="none">不限</option><option value="include">包含</option><option value="exclude">排除</option></select></label></div>
  </fieldset>
</template>
