<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

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
  <main class="grid min-h-screen place-items-center p-5">
    <section class="panel w-full max-w-md p-8">
      <div class="mb-7 flex items-center gap-3"><span class="grid h-11 w-11 place-items-center rounded-3 bg-cyan-300 text-xl font-900 text-slate-950">O</span><div><h1 class="m-0 text-2xl font-800">Server 管理端</h1><p class="m-0 text-xs tracking-wider text-slate-500">OHMYCINE</p></div></div>
      <form class="space-y-4" @submit.prevent="submit">
        <div><label class="label" for="login-user">用户名</label><input id="login-user" v-model="username" class="input" autocomplete="username" required autofocus /></div>
        <div><label class="label" for="login-password">密码</label><input id="login-password" v-model="password" class="input" type="password" autocomplete="current-password" required /></div>
        <p v-if="error" class="rounded-3 bg-red-400/10 px-3 py-2 text-sm text-red-200">{{ error }}</p>
        <button class="btn-primary w-full" :disabled="loading">{{ loading ? '正在验证…' : '登录' }}</button>
      </form>
      <p class="mt-5 text-center text-xs text-slate-600">会话保存在 HttpOnly Cookie 中，浏览器不会把令牌写入 localStorage。</p>
    </section>
  </main>
</template>
