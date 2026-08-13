<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import AuthShell from '@/components/AuthShell.vue'

const auth = useAuthStore(); const route = useRoute(); const router = useRouter()
const username = ref(''); const password = ref(''); const loading = ref(false); const error = ref('')
async function submit() {
  error.value = ''; loading.value = true
  try { await auth.login(username.value, password.value); await router.replace(typeof route.query.redirect === 'string' ? route.query.redirect : '/') }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '登录失败' }
  finally { loading.value = false }
}
</script>

<template>
  <AuthShell>
    <div class="mb-7 flex items-center gap-3"><span class="brand-badge">O</span><div><h1 class="m-0 text-2xl font-800">Server 管理端</h1><p class="text-subtle m-0 text-xs">OhMyCine</p></div></div>
    <form class="space-y-4" @submit.prevent="submit">
      <div><label class="label" for="login-user">用户名</label><input id="login-user" v-model="username" class="input" autocomplete="username" required autofocus /></div>
      <div><label class="label" for="login-password">密码</label><input id="login-password" v-model="password" class="input" type="password" autocomplete="current-password" required /></div>
      <p v-if="error" class="semantic-error px-3 py-2 text-sm" role="alert">{{ error }}</p>
      <button class="btn-primary w-full" :disabled="loading">{{ loading ? '正在验证…' : '登录' }}</button>
    </form>
    <p class="text-subtle mt-5 text-center text-xs">会话保存在 HttpOnly Cookie 中，浏览器不会把令牌写入 localStorage。</p>
  </AuthShell>
</template>
