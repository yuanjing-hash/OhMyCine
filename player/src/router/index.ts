import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'home',
      component: () => import('@/views/HomeView.vue'),
    },
    {
      path: '/player',
      name: 'player',
      component: () => import('@/views/PlayerView.vue'),
    },
    {
      path: '/source/:sourceId',
      name: 'source',
      component: () => import('@/views/SourceLibraryView.vue'),
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/views/SettingsView.vue'),
    },
  ],
})

export default router
