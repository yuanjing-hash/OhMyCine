<script setup lang="ts">
import { computed } from 'vue'
import RuleConditionSelector from '@/components/RuleConditionSelector.vue'
import { countryOptions, languageOptions, movieGenreOptions, moveItem, newCategory, tvGenreOptions } from '@/media-rules'
import type { ClassificationGroup, RuleSetCondition } from '@/types/api'

const props = defineProps<{ modelValue: ClassificationGroup; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: ClassificationGroup] }>()
const genres = computed(() => props.modelValue.media_type === 'movie' ? movieGenreOptions : tvGenreOptions)
function update(value: Partial<ClassificationGroup>) { emit('update:modelValue', { ...props.modelValue, ...value }) }
function updateCategory(index: number, value: Partial<ClassificationGroup['categories'][number]>) { const categories = [...props.modelValue.categories]; categories[index] = { ...categories[index]!, ...value }; update({ categories }) }
function updateCondition(index: number, key: 'genre_ids' | 'original_languages' | 'production_countries' | 'origin_countries', value: RuleSetCondition<string | number>) { const category = props.modelValue.categories[index]!; updateCategory(index, { conditions: { ...category.conditions, [key]: value } }) }
function add() { update({ categories: [...props.modelValue.categories, newCategory(props.modelValue.media_type)] }) }
function remove(index: number) { update({ categories: props.modelValue.categories.filter((_, itemIndex) => itemIndex !== index) }) }
</script>

<template>
  <section class="space-y-4">
    <div><label class="label" :for="`fallback-${modelValue.media_type}`">兜底分类名称</label><input :id="`fallback-${modelValue.media_type}`" class="input" :value="modelValue.fallback_category_name" :disabled="disabled" @input="update({ fallback_category_name: ($event.target as HTMLInputElement).value })" /></div>
    <article v-for="(category, index) in modelValue.categories" :key="category.id" class="semantic-inset p-4">
      <div class="flex flex-wrap items-center gap-2"><input class="input min-w-48 flex-1" :value="category.name" :disabled="disabled" aria-label="分类名称" @input="updateCategory(index, { name: ($event.target as HTMLInputElement).value })" /><button type="button" class="btn-secondary" :disabled="disabled || index === 0" aria-label="上移分类" @click="update({ categories: moveItem(modelValue.categories, index, -1) })">↑</button><button type="button" class="btn-secondary" :disabled="disabled || index === modelValue.categories.length - 1" aria-label="下移分类" @click="update({ categories: moveItem(modelValue.categories, index, 1) })">↓</button><button type="button" class="btn-danger" :disabled="disabled" @click="remove(index)">删除</button></div>
      <div class="mt-4 space-y-4"><RuleConditionSelector label="TMDB Genre" :options="genres" :model-value="category.conditions.genre_ids" :disabled="disabled" @update:model-value="updateCondition(index, 'genre_ids', $event)" /><RuleConditionSelector label="原始语言" :options="languageOptions" :model-value="category.conditions.original_languages" :disabled="disabled" @update:model-value="updateCondition(index, 'original_languages', $event)" /><RuleConditionSelector v-if="modelValue.media_type === 'movie' && category.conditions.production_countries" label="制片国家/地区" :options="countryOptions" :model-value="category.conditions.production_countries" :disabled="disabled" @update:model-value="updateCondition(index, 'production_countries', $event)" /><RuleConditionSelector v-if="modelValue.media_type === 'tv' && category.conditions.origin_countries" label="原产国家/地区" :options="countryOptions" :model-value="category.conditions.origin_countries" :disabled="disabled" @update:model-value="updateCondition(index, 'origin_countries', $event)" />
        <div class="grid gap-3 md:grid-cols-2"><div><label class="label" :for="`year-from-${category.id}`">年份从</label><input :id="`year-from-${category.id}`" class="input" type="number" min="1888" max="2200" :value="category.conditions.release_year?.from" :disabled="disabled" @input="updateCategory(index, { conditions: { ...category.conditions, release_year: { ...category.conditions.release_year, from: Number(($event.target as HTMLInputElement).value) || undefined } } })" /></div><div><label class="label" :for="`year-to-${category.id}`">年份至</label><input :id="`year-to-${category.id}`" class="input" type="number" min="1888" max="2200" :value="category.conditions.release_year?.to" :disabled="disabled" @input="updateCategory(index, { conditions: { ...category.conditions, release_year: { ...category.conditions.release_year, to: Number(($event.target as HTMLInputElement).value) || undefined } } })" /></div></div>
      </div>
    </article>
    <button v-if="!disabled" type="button" class="btn-secondary" @click="add">添加分类</button>
  </section>
</template>
