import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  { path: '/login', name: 'login', component: () => import('./views/Login.vue'), meta: { layout: 'blank' } },
  { path: '/setup', name: 'setup', component: () => import('./views/Setup.vue'), meta: { layout: 'blank' } },
  {
    path: '/',
    component: () => import('./layouts/Shell.vue'),
    meta: { requiresAuth: true },
    children: [
      { path: '', name: 'dashboard', component: () => import('./views/Dashboard.vue') },
      { path: 'blocklists', name: 'blocklists', component: () => import('./views/Blocklists.vue') },
      { path: 'allowlist', name: 'allowlist', component: () => import('./views/Allowlist.vue') },
      { path: 'local-dns', name: 'local-dns', component: () => import('./views/LocalDNS.vue') },
      { path: 'profiles', name: 'profiles', component: () => import('./views/Profiles.vue') },
      { path: 'schedules', name: 'schedules', component: () => import('./views/Schedules.vue') },
      { path: 'categories', name: 'categories', component: () => import('./views/Categories.vue') },
      { path: 'query-log', name: 'query-log', component: () => import('./views/QueryLog.vue') },
      { path: 'stats', name: 'stats', component: () => import('./views/Stats.vue') },
      { path: 'cluster', name: 'cluster', component: () => import('./views/Cluster.vue') },
      { path: 'settings', name: 'settings', component: () => import('./views/Settings.vue') },
      { path: 'account', name: 'account', component: () => import('./views/Account.vue') },
    ],
  },
  { path: '/:catchAll(.*)*', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.ready) await auth.probe()
  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return { name: auth.isSetup ? 'login' : 'setup', query: { redirect: to.fullPath } }
  }
  if (to.name === 'setup' && auth.isSetup) {
    return { name: 'login' }
  }
})
