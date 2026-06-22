import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

// M5.9.5: the root path renders the unauthenticated Landing view.
// The legacy admin Shell + Dashboard moved to /dashboard. The
// `requiresAuth` route-meta still kicks visitors back to /login,
// preserving every admin route's existing protection. When the
// operator disabled the public landing page (node.api.public_landing.enabled=false),
// the server redirects GET / to /login before this router ever sees it.
const routes: RouteRecordRaw[] = [
  { path: '/', name: 'landing', component: () => import('./views/Landing.vue'), meta: { layout: 'blank' } },
  { path: '/about', name: 'about', component: () => import('./views/About.vue'), meta: { layout: 'blank' } },
  { path: '/login', name: 'login', component: () => import('./views/Login.vue'), meta: { layout: 'blank' } },
  { path: '/setup', name: 'setup', component: () => import('./views/Setup.vue'), meta: { layout: 'blank' } },
  {
    path: '/dashboard',
    component: () => import('./layouts/Shell.vue'),
    meta: { requiresAuth: true },
    children: [
      { path: '', name: 'dashboard', component: () => import('./views/Dashboard.vue') },
      { path: 'blocklists', name: 'blocklists', component: () => import('./views/Blocklists.vue') },
      { path: 'allowlist', name: 'allowlist', component: () => import('./views/Allowlist.vue') },
      { path: 'local-dns', name: 'local-dns', component: () => import('./views/LocalDNS.vue') },
      { path: 'clients', name: 'clients', component: () => import('./views/Clients.vue') },
      { path: 'profiles', name: 'profiles', component: () => import('./views/Profiles.vue') },
      { path: 'schedules', name: 'schedules', component: () => import('./views/Schedules.vue') },
      { path: 'categories', name: 'categories', component: () => import('./views/Categories.vue') },
      { path: 'query-log', name: 'query-log', component: () => import('./views/QueryLog.vue') },
      { path: 'stats', name: 'stats', component: () => import('./views/Stats.vue') },
      { path: 'cluster', name: 'cluster', component: () => import('./views/Cluster.vue') },
      { path: 'dhcp', name: 'dhcp', component: () => import('./views/Dhcp.vue') },
      { path: 'settings', name: 'settings', component: () => import('./views/Settings.vue') },
      { path: 'settings/audit', name: 'audit', component: () => import('./views/Audit.vue') },
      { path: 'tools/test-domain', name: 'test-domain', component: () => import('./views/TestDomain.vue') },
      { path: 'account', name: 'account', component: () => import('./views/Account.vue') },
      { path: 'webhooks', name: 'webhooks', component: () => import('./views/Webhooks.vue') },
      { path: 'tokens', name: 'tokens', component: () => import('./views/Tokens.vue') },
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
  // Once an admin is logged in, the landing page is essentially a
  // marketing surface — they want the dashboard. Bounce them through.
  if (to.name === 'landing' && auth.isAuthenticated) {
    return { name: 'dashboard' }
  }
})
