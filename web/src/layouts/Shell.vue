<template>
  <div class="fixed inset-0 flex bg-bg-canvas">
    <!-- Sidebar (Pi-hole-inspired: persistent left nav with grouped sections) -->
    <aside
      class="w-60 shrink-0 border-r border-border bg-bg-card flex flex-col h-full"
      :class="{ '-ml-60 md:ml-0': !sidebarOpen }"
    >
      <div class="px-5 h-12 border-b border-border flex items-center gap-2">
        <img src="/favicon.svg" alt="" class="w-6 h-6" />
        <span class="font-semibold text-fg-strong tracking-tight">skoed</span>
        <span
          v-if="health"
          class="ml-auto"
          :class="health.status === 'ok' ? 'badge-success' : 'badge-warning'"
        >
          {{ health.status }}
        </span>
      </div>

      <nav class="flex-1 px-2 py-3 text-sm overflow-y-auto">
        <div v-for="(section, idx) in nav" :key="section.label"
             :class="idx > 0 ? 'mt-3 pt-3 border-t border-border' : ''">
          <div class="px-3 mb-1 text-[10px] uppercase tracking-wider text-fg-subtle font-semibold">
            {{ section.label }}
          </div>
          <div class="space-y-0.5">
            <RouterLink
              v-for="item in section.items"
              :key="item.name"
              :to="{ name: item.name }"
              class="flex items-center gap-2 px-3 py-1.5 rounded text-fg
                     hover:bg-bg-hover hover:text-fg-strong transition-colors"
              active-class="bg-accent-subtle text-accent font-medium"
            >
              <component :is="item.icon" class="w-4 h-4" />
              <span>{{ item.label }}</span>
            </RouterLink>
            <!-- M4.5: API docs live outside the SPA router (server-rendered
                 static HTML). Use a plain anchor so the browser does a real
                 navigation rather than vue-router pattern matching. -->
            <a v-if="section.label === 'System'"
               href="/api/docs/"
               target="_blank"
               class="flex items-center gap-2 px-3 py-1.5 rounded text-fg
                      hover:bg-bg-hover hover:text-fg-strong transition-colors">
              <CodeBracketIcon class="w-4 h-4" />
              <span>API</span>
            </a>
          </div>
        </div>
      </nav>

    </aside>

    <!-- Main column -->
    <div class="flex-1 flex flex-col min-w-0 overflow-hidden">
      <!-- Header (AdGuard-inspired: thin top bar with theme toggle + account) -->
      <header class="h-12 shrink-0 border-b border-border bg-bg-card flex items-center px-4 gap-2">
        <button
          class="md:hidden btn-ghost p-1.5"
          @click="sidebarOpen = !sidebarOpen"
          aria-label="Toggle navigation"
        >
          <Bars3Icon class="w-5 h-5" />
        </button>
        <h1 class="text-sm font-semibold text-fg-strong">{{ pageTitle }}</h1>
        <div class="ml-auto flex items-center gap-2">
          <!-- Node info chips (moved from sidebar footer) -->
          <template v-if="health">
            <button
              class="text-xs text-fg-muted hidden lg:block hover:text-fg transition-colors"
              title="Change palette"
              @click="cyclePalette"
            >{{ paletteLabel }}</button>
            <span class="hidden lg:inline-flex items-center gap-1 border border-border bg-bg-canvas rounded px-2 py-0.5 font-mono text-xs text-fg-muted">
              <span>{{ health.node_id }}</span>
              <span :class="health.role === 'leader' ? 'text-accent font-semibold' : 'text-fg-muted'">{{ health.role }}</span>
              <template v-if="leaderId">
                <span class="text-fg-subtle mx-0.5">·</span>
                <span class="text-fg-subtle">leader</span>
                <span class="text-accent font-semibold">{{ leaderId }}</span>
              </template>
            </span>
            <span v-if="health.version" class="hidden lg:inline-flex border border-border bg-bg-canvas rounded px-2 py-0.5 font-mono text-xs text-fg-muted">
              {{ health.version }}
            </span>
            <div class="hidden lg:block w-px h-4 bg-border mx-1" />
          </template>
          <button
            class="btn-ghost p-1.5"
            :title="theme.mode === 'dark' ? 'Switch to light' : 'Switch to dark'"
            @click="theme.toggleMode()"
          >
            <SunIcon v-if="theme.mode === 'dark'" class="w-5 h-5" />
            <MoonIcon v-else class="w-5 h-5" />
          </button>
          <div class="text-xs text-fg-muted hidden sm:block">{{ auth.user }}</div>
          <button class="btn-ghost p-1.5" title="Account" @click="$router.push({ name: 'account' })">
            <UserCircleIcon class="w-5 h-5" />
          </button>
          <button class="btn-ghost p-1.5" title="Log out" @click="onLogout">
            <ArrowRightStartOnRectangleIcon class="w-5 h-5" />
          </button>
        </div>
      </header>

      <main class="flex-1 overflow-y-auto p-4 md:p-6">
        <RouterView />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import {
  HomeIcon, NoSymbolIcon, CheckBadgeIcon, ServerStackIcon,
  QueueListIcon, ChartBarIcon, CpuChipIcon, Cog6ToothIcon,
  UserCircleIcon, Bars3Icon, SunIcon, MoonIcon,
  ArrowRightStartOnRectangleIcon, UsersIcon, ClockIcon, TagIcon,
  DevicePhoneMobileIcon, CodeBracketIcon, BeakerIcon,
  BellAlertIcon, KeyIcon, ServerIcon,
} from '@heroicons/vue/24/outline'
import { useThemeStore } from '@/stores/theme'
import { useAuthStore } from '@/stores/auth'
import { clusterHealth, clusterStatus } from '@/api/endpoints'
import type { ClusterHealth } from '@/api/types'

const theme = useThemeStore()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const sidebarOpen = ref(true)
const health = ref<ClusterHealth | null>(null)
const leaderId = ref<string | null>(null)

// Sidebar nav is grouped into thematic sections:
//   Overview — the dashboard
//   Filtering — every resource that decides "block / allow / rewrite"
//   Observability — read-only signals (query log, stats)
//   Tools — operator utilities that don't change cluster state
//   System — node/cluster admin
const nav: Array<{
  label: string
  items: Array<{ name: string; label: string; icon: unknown }>
}> = [
  {
    label: 'Overview',
    items: [{ name: 'dashboard', label: 'Dashboard', icon: HomeIcon }],
  },
  {
    label: 'Filtering',
    items: [
      { name: 'blocklists', label: 'Blocklists', icon: NoSymbolIcon },
      { name: 'allowlist',  label: 'Allowlist',  icon: CheckBadgeIcon },
      { name: 'local-dns',  label: 'Local DNS',  icon: ServerStackIcon },
      { name: 'clients',    label: 'Clients',    icon: DevicePhoneMobileIcon },
      { name: 'profiles',   label: 'Profiles',   icon: UsersIcon },
      { name: 'schedules',  label: 'Schedules',  icon: ClockIcon },
      { name: 'categories', label: 'Categories', icon: TagIcon },
    ],
  },
  {
    label: 'Observability',
    items: [
      { name: 'query-log', label: 'Query log', icon: QueueListIcon },
      { name: 'stats',     label: 'Stats',     icon: ChartBarIcon },
    ],
  },
  {
    label: 'Tools',
    items: [
      { name: 'test-domain', label: 'Test a domain', icon: BeakerIcon },
    ],
  },
  {
    label: 'Integrations',
    items: [
      { name: 'webhooks', label: 'Webhooks',   icon: BellAlertIcon },
      { name: 'tokens',   label: 'API Tokens', icon: KeyIcon },
    ],
  },
  {
    label: 'System',
    items: [
      { name: 'cluster',  label: 'Cluster',      icon: CpuChipIcon },
      { name: 'dhcp',     label: 'DHCP Server',  icon: ServerIcon },
      { name: 'settings', label: 'Settings',     icon: Cog6ToothIcon },
    ],
  },
]

const titles: Record<string, string> = {
  dashboard: 'Dashboard',
  blocklists: 'Blocklists',
  allowlist: 'Allowlist',
  'local-dns': 'Local DNS',
  clients: 'Clients',
  profiles: 'Profiles',
  schedules: 'Schedules',
  categories: 'Categories',
  'query-log': 'Query log',
  stats: 'Stats',
  cluster: 'Cluster',
  dhcp: 'DHCP Server',
  'test-domain': 'Test a domain',
  settings: 'Settings',
  account: 'Account',
  webhooks: 'Webhooks',
  tokens: 'API Tokens',
}
const pageTitle = computed(() => titles[String(route.name ?? '')] ?? 'skoed')

const paletteLabel = computed(() => ({
  'lipgloss': 'Lipgloss',
  'monokai': 'Monokai',
  'monokai-solarized': 'Monokai Solarized',
  'monokai-blue': 'Monokai Blue',
  'monokai-pro': 'Monokai Pro',
}[theme.palette] ?? theme.palette))

const PALETTES = ['lipgloss', 'monokai', 'monokai-solarized', 'monokai-blue', 'monokai-pro'] as const

function cyclePalette() {
  const idx = PALETTES.indexOf(theme.palette as typeof PALETTES[number])
  theme.setPalette(PALETTES[(idx + 1) % PALETTES.length])
}

async function onLogout() {
  await auth.logout()
  router.replace({ name: 'login' })
}

async function refreshHealth() {
  try {
    health.value = await clusterHealth()
    if (health.value.mode === 'cluster' && health.value.role !== 'leader') {
      const s = await clusterStatus()
      leaderId.value = s.leader_id || null
    } else {
      leaderId.value = null
    }
  } catch { /* ignore */ }
}
let healthTimer: ReturnType<typeof setInterval> | null = null
onMounted(async () => {
  await refreshHealth()
  healthTimer = setInterval(refreshHealth, 10_000)
})
onUnmounted(() => {
  if (healthTimer !== null) clearInterval(healthTimer)
})
</script>
