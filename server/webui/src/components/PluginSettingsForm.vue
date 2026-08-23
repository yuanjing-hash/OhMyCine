<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { PluginQRCodeAuthState, PluginSettingsField, PluginSettingsPage } from '@/plugins'

const props = defineProps<{
  page: PluginSettingsPage
  modelValue: Record<string, unknown>
  disabled?: boolean
  credentialConfigured?: boolean
  healthStatus?: string
  qrAuthState?: PluginQRCodeAuthState
  qrAuthActionVisible?: boolean
  qrAuthActionDisabled?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, unknown>]
  'start-auth': []
}>()
const activeTab = ref(props.page.tabs[0]?.id ?? '')

watch(() => props.page, page => {
  if (!page.tabs.some(tab => tab.id === activeTab.value)) activeTab.value = page.tabs[0]?.id ?? ''
})

const selectedTab = computed(() => props.page.tabs.find(tab => tab.id === activeTab.value) ?? props.page.tabs[0])
const qrCodeVisible = computed(() => props.qrAuthState?.state === 'pending' || props.qrAuthState?.state === 'scanned')

function value(field: PluginSettingsField) {
  return field.key ? props.modelValue[field.key] : undefined
}

function update(field: PluginSettingsField, next: unknown) {
  if (!field.key) return
  emit('update:modelValue', { ...props.modelValue, [field.key]: next })
}

function credentialLabel() {
  if (props.qrAuthState?.state === 'scanned') return '已扫码，请在客户端确认'
  if (props.qrAuthState?.state === 'confirmed') return props.qrAuthState.accountName ? `已登录：${props.qrAuthState.accountName}` : '账号已登录'
  if (props.qrAuthState?.state === 'expired') return '二维码已过期，请重新生成'
  if (props.qrAuthState?.state === 'pending') return '等待扫码确认'
  if (props.healthStatus === 'healthy') return '账号已登录，连接正常'
  if (props.healthStatus === 'auth_pending') return '等待扫码确认'
  if (props.healthStatus === 'auth_expired') return '登录已过期，请重新登录'
  if (props.healthStatus === 'error') return '账号连接异常'
  return props.credentialConfigured ? '登录凭据已安全保存' : '尚未登录'
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN')
}
</script>

<template>
  <div class="grid gap-4">
    <nav v-if="page.tabs.length > 1" class="management-tabs" aria-label="插件设置分类">
      <button v-for="tab in page.tabs" :key="tab.id" type="button" class="management-tab" :class="{ 'management-tab--active': activeTab === tab.id }" @click="activeTab = tab.id">{{ tab.title }}</button>
    </nav>

    <template v-if="selectedTab">
      <section v-for="section in selectedTab.sections" :key="section.id" class="semantic-inset grid gap-3 p-4">
        <div>
          <h4 class="m-0 text-sm">{{ section.title }}</h4>
          <p v-if="section.description" class="text-subtle mb-0 mt-1 text-xs">{{ section.description }}</p>
        </div>

        <template v-for="field in section.fields" :key="field.key || `${section.id}:${field.type}:${field.label}`">
          <div v-if="field.type === 'notice'" class="semantic-warning p-3 text-xs">
            <strong>{{ field.label }}</strong>
            <p v-if="field.description" class="mb-0 mt-1">{{ field.description }}</p>
          </div>
          <div v-else-if="field.type === 'credential-status'" class="grid gap-3 rounded border border-[var(--line)] bg-[var(--surface)] p-3 text-sm">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div><strong>{{ field.label }}</strong><p v-if="field.description" class="text-subtle m-0 mt-1 text-xs">{{ field.description }}</p></div>
              <span :class="healthStatus === 'healthy' || qrAuthState?.state === 'confirmed' ? 'status-chip status-chip--ready' : credentialConfigured || qrAuthState ? 'status-chip status-chip--warning' : 'status-chip'">{{ credentialLabel() }}</span>
            </div>
            <div v-if="qrCodeVisible && qrAuthState" class="semantic-inset grid justify-items-center gap-2 p-3 text-center text-xs">
              <img :src="qrAuthState.qrDataURL" width="220" height="220" alt="插件登录二维码" class="rounded bg-white p-2" />
              <strong>{{ credentialLabel() }}</strong>
              <span class="text-subtle">二维码有效期至 {{ formatTime(qrAuthState.expiresAt) }}</span>
            </div>
            <div v-if="qrAuthActionVisible" class="flex justify-end">
              <button type="button" class="btn-primary" :disabled="qrAuthActionDisabled" @click="emit('start-auth')">{{ credentialConfigured || qrAuthState?.state === 'confirmed' ? '重新扫码登录' : qrAuthState?.state === 'expired' ? '重新生成二维码' : '扫码登录' }}</button>
            </div>
          </div>
          <label v-else-if="field.type === 'switch'" class="flex items-start gap-3 text-sm">
            <input type="checkbox" :checked="Boolean(value(field))" :disabled="disabled" class="mt-1" @change="update(field, ($event.target as HTMLInputElement).checked)" />
            <span><strong>{{ field.label }}</strong><span v-if="field.description" class="text-subtle mt-1 block text-xs">{{ field.description }}</span></span>
          </label>
          <div v-else>
            <label class="label">{{ field.label }}</label>
            <select v-if="field.type === 'select'" class="input" :value="String(value(field) ?? '')" :disabled="disabled" @change="update(field, ($event.target as HTMLSelectElement).value)">
              <option v-for="option in field.options ?? []" :key="option.value" :value="option.value">{{ option.label }}</option>
            </select>
            <input v-else-if="field.type === 'number'" class="input" type="number" :value="Number(value(field) ?? 0)" :min="field.minimum" :max="field.maximum" :disabled="disabled" @input="update(field, Number(($event.target as HTMLInputElement).value))" />
            <input v-else class="input" type="text" :value="String(value(field) ?? '')" :placeholder="field.placeholder" :disabled="disabled" @input="update(field, ($event.target as HTMLInputElement).value)" />
            <p v-if="field.description" class="text-subtle mb-0 mt-1 text-xs">{{ field.description }}</p>
          </div>
        </template>
      </section>
    </template>
  </div>
</template>
