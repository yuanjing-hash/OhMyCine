<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import AuthShell from '@/components/AuthShell.vue'
import SecretInput from '@/components/SecretInput.vue'

const auth = useAuthStore()
const router = useRouter()
const username = ref('')
const displayName = ref('')
const password = ref('')
const confirmPassword = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  error.value = ''
  if (password.value !== confirmPassword.value) { error.value = '两次输入的密码不一致'; return }
  loading.value = true
  try { await auth.setup(username.value, displayName.value, password.value); await router.replace('/') }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '首次设置失败' }
  finally { loading.value = false }
}
</script>

<template>
  <AuthShell width="large">
    <h1 class="m-0 text-3xl font-800">创建实例 Owner</h1>
    <p class="page-description mt-3 text-sm leading-6">Owner 是唯一的实例所有者，首版不支持隐式转移、停用或删除。请使用至少 12 位的独立密码。</p>
    <div v-if="auth.recoveryRequired" class="semantic-error mt-5 p-4 text-sm">数据库已有用户但缺少 owner。Server 已进入安全恢复状态，不会自动创建默认管理员。请通过受控数据库恢复流程处理。</div>
    <form v-else class="mt-7 space-y-4" @submit.prevent="submit">
      <div><label class="label" for="username">用户名</label><input id="username" v-model="username" class="input" autocomplete="username" required minlength="3" maxlength="64" placeholder="owner" /></div>
      <div><label class="label" for="display">显示名称</label><input id="display" v-model="displayName" class="input" maxlength="128" placeholder="家庭影院管理员" /></div>
      <div><label class="label" for="password">密码</label><SecretInput id="password" v-model="password" class="input" autocomplete="new-password" required minlength="12" maxlength="128" /></div>
      <div><label class="label" for="confirm">确认密码</label><SecretInput id="confirm" v-model="confirmPassword" class="input" autocomplete="new-password" required minlength="12" maxlength="128" /></div>
      <p v-if="error" class="semantic-error px-3 py-2 text-sm" role="alert">{{ error }}</p>
      <button class="btn-primary w-full" :disabled="loading">{{ loading ? '正在初始化…' : '创建 Owner 并进入管理端' }}</button>
    </form>
  </AuthShell>
</template>
