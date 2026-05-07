import { createPinia } from 'pinia'
import { createApp } from 'vue'
import { createI18n } from 'vue-i18n'
import App from './App.vue'
import { useTheme } from './composables/useTheme'
import en from './i18n/en.json'
import zhCN from './i18n/zh-CN.json'
import router from './router'
import '@unocss/reset/tailwind.css'
import 'virtual:uno.css'
import './styles/variables.css'
import './styles/glass.css'
import './styles/global.css'

useTheme().load()

const i18n = createI18n({
  legacy: false,
  locale: 'zh-CN',
  fallbackLocale: 'en',
  messages: {
    'zh-CN': zhCN,
    en,
  },
})

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(i18n)
app.mount('#app')
