import { createRouter, createWebHistory } from 'vue-router'
import { playbackRouteQueryNeedsSanitization, sanitizePlaybackRouteQuery } from '@/services/playbackRoute'

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
      path: '/source/:sourceId/item/:itemId',
      name: 'media-detail',
      component: () => import('@/views/MediaDetailView.vue'),
    },
    {
      path: '/favorites',
      name: 'favorites',
      component: () => import('@/views/FavoritesView.vue'),
    },
    {
      path: '/history',
      name: 'history',
      component: () => import('@/views/HistoryView.vue'),
    },
    {
      path: '/downloads',
      name: 'downloads',
      component: () => import('@/views/DownloadsView.vue'),
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/views/SettingsView.vue'),
    },
  ],
})

router.beforeEach((to) => {
  if (to.name !== 'player' || !playbackRouteQueryNeedsSanitization(to.query))
    return true

  return {
    name: 'player',
    query: sanitizePlaybackRouteQuery(to.query),
    replace: true,
  }
})

export default router
