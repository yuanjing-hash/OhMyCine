<script setup lang="ts">
import type { CollectionSelectionOption } from '@/services/mediaActions'
import { computed, ref, watch } from 'vue'
import { resolveCollectionSelection, useCollectionSelectionRuntime } from '@/services/mediaActions'

const runtime = useCollectionSelectionRuntime()
const collections = ref<CollectionSelectionOption[]>([])
const name = ref('')
const loading = ref(false)
const candidates = computed(() => collections.value)
watch(() => runtime.request.value, (request) => {
  if (request)
    void load()
})
async function load() {
  loading.value = true
  try {
    collections.value = await runtime.request.value?.load() ?? []
  }
  finally {
    loading.value = false
  }
}
async function createAndChoose() {
  const request = runtime.request.value
  const value = name.value.trim()
  if (!request || !value)
    return
  const id = await request.create(value)
  name.value = ''
  resolveCollectionSelection(id)
}
</script>

<template>
  <Teleport to="body">
    <div v-if="runtime.request.value" class="collection-dialog-layer">
      <button class="collection-dialog-scrim" aria-label="取消" @click="resolveCollectionSelection(null)" /><section role="dialog" aria-modal="true" class="collection-dialog theme-adaptive">
        <header>
          <div><small>{{ runtime.request.value.kind === 'playlist' ? '播放列表' : '合集' }}</small><strong>添加“{{ runtime.request.value.target.display.name }}”</strong></div><button @click="resolveCollectionSelection(null)">
            ×
          </button>
          <small>{{ runtime.request.value.ownerLabel }}</small>
        </header><div class="collection-list">
          <button v-for="item in candidates" :key="item.id" @click="resolveCollectionSelection(item.id)">
            <span>{{ item.name }}</span><small>{{ item.itemCount ?? 0 }} 项</small>
          </button><p v-if="!loading && !candidates.length">
            还没有可选项目，可在下方新建。
          </p>
        </div><form @submit.prevent="createAndChoose">
          <input v-model="name" :placeholder="runtime.request.value.kind === 'playlist' ? '新播放列表名称' : '新合集名称'" maxlength="512"><button :disabled="!name.trim()">
            新建并添加
          </button>
        </form>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.collection-dialog-layer{position:fixed;inset:0;z-index:1350;display:grid;place-items:center;padding:1rem}.collection-dialog-scrim{position:absolute;inset:0;background:var(--chrome-scrim);backdrop-filter:blur(9px)}.collection-dialog{position:relative;width:min(30rem,100%);max-height:min(80vh,38rem);overflow:auto;border:1px solid var(--chrome-border);border-radius:12px;padding:1rem;color:var(--color-text);background:var(--chrome-surface);box-shadow:var(--chrome-shadow)}header{display:flex;justify-content:space-between;gap:1rem}header small,header strong{display:block}header small{color:var(--color-text-tertiary);font-size:.68rem}header strong{margin-top:.2rem}header>button{width:2rem;height:2rem;border-radius:50%;background:var(--surface-soft)}.collection-list{display:grid;gap:.4rem;margin-top:1rem}.collection-list button{display:flex;min-height:3rem;align-items:center;justify-content:space-between;border-radius:8px;padding:0 .8rem;background:var(--surface-soft);text-align:left}.collection-list small,.collection-list p{color:var(--color-text-tertiary);font-size:.7rem}.collection-list p{padding:1rem;text-align:center}form{display:grid;grid-template-columns:1fr auto;gap:.5rem;margin-top:1rem;border-top:1px solid var(--color-divider);padding-top:1rem}input{min-width:0;border:1px solid var(--control-border);border-radius:8px;padding:.7rem;color:var(--control-text);background:var(--control-bg)}form button{border-radius:8px;padding:0 1rem;color:var(--color-text-inverse);background:var(--color-text);font-size:.75rem;font-weight:800}form button:disabled{opacity:.4}@media(max-width:767px){.collection-dialog-layer{align-items:end;padding:0}.collection-dialog{width:100%;max-height:78vh;border-width:1px 0 0;border-radius:16px 16px 0 0;padding-bottom:max(1rem,env(safe-area-inset-bottom))}}
</style>
