<template>
  <div class="min-h-screen flex bg-bg-canvas">
    <!-- Sidebar (Pi-hole-inspired: persistent left nav with grouped sections) -->
    <aside
      class="w-60 shrink-0 border-r border-border bg-bg-card flex flex-col"
      :class="{ '-ml-60 md:ml-0': !sidebarOpen }"
    >
      <div class="px-5 h-12 border-b border-border flex items-center gap-2">
        <img src="/favicon.svg" alt="" class="w-6 h-6" />
        <span class="font-semibold text-fg-strong tracking-tight">dblock</span>
        <span
          v-if="health"
          class="ml-auto"
          :class="health.status === 'ok' ? 'badge-success' : 'badge-warning'"
        >
          {{ health.status }}
        </span>
      </div>

      <nav class="flex-1 px-2 py-3 space-y-0.5 text-sm overflow-y-auto">
        <RouterLink
          v-for="item in nav"
          :key="item.name"
          :to="{ name: item.name }"
          class="flex items-center gap-2 px-3 py-1.5 rounded text-fg
                 hover:bg-bg-hover hover:text-fg-strong transition-colors"
          active-class="bg-accent-subtle text-accent font-medium"
        >
          <component :is="item.icon" class="w-4 h-4" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div class="px-3 py-3 border-t border-border text-xs text-fg-muted">
        <div v-if="health" class="space-y-0.5">
          <div>
            <span class="text-fg-subtle">node</span>
            <span class="font-mono ml-1">{{ health.node_id }}</span>
          </div>
          <div>
            <span class="text-fg-subtle">role</span>
            <span class="ml-1">{{ health.role }}</span>
          </div>
          <div>
            <span class="text-fg-subtle">mode</span>
            <span class="ml-1">{{ health.mode }}</span>
          </div>
          <div>
            <span class="text-fg-subtle">term</span>
            <span class="font-mono ml-1">{{ health.raft_term }}</span>
            <span class="text-fg-subtle ml-2">commit</span>
            <span class="font-mono ml-1">{{ health.commit_index }}</span>
          </div>
        </div>
      </div>
    </aside>

    <!-- Main column -->
    <div class="flex-1 flex flex-col min-w-0">
      <!-- Header (AdGuard-inspired: thin top bar with theme toggle + account) -->
      <header class="h-12 border-b border-border bg-bg-card flex items-center px-4 gap-2">
        <button
          class="md:hidden btn-ghost p-1.5"
          @click="sidebarOpen = !sidebarOpen"
          aria-label="Toggle navigation"
        >
          <Bars3Icon class="w-5 h-5" />
        </button>
        <h1 class="text-sm font-semibold text-fg-strong">{{ pageTitle }}</h1>
        <div class="ml-auto flex items-center gap-2">
          <button
            class="btn-ghost p-1.5"
            :title="theme.mode === 'dark' ? 'Switch to light' : 'Switch to dark'"
            @click="theme.toggleMode()"
          >
            <SunIcon v-if="theme.mode === 'dark'" class="w-5 h-5" />
            <MoonIcon v-else class="w-5 h-5" />
          </button>
          <select
            class="input py-1 w-44"
            :value="theme.palette"
            @change="onPaletteChange"
            title="Palette"
          >
            <option value="monokai-solarized">Monokai Solarized</option>
            <option value="monokai">Monokai (vivid)</option>
            <option value="monokai-blue">Monokai Blue</option>
            <option value="monokai-pro">Monokai Pro</option>
            <option value="lipgloss">Lipgloss</option>
          </select>
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
import { computed, onMounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import {
  HomeIcon, NoSymbolIcon, CheckBadgeIcon, ServerStackIcon,
  QueueListIcon, ChartBarIcon, CpuChipIcon, Cog6ToothIcon,
  UserCircleIcon, Bars3Icon, SunIcon, MoonIcon,
  ArrowRightStartOnRectangleIcon, UsersIcon, ClockIcon, TagIcon,
  DevicePhoneMobileIcon,
} from '@heroicons/vue/24/outline'
import { useThemeStore } from '@/stores/theme'
import { useAuthStore } from '@/stores/auth'
import { clusterHealth } from '@/api/endpoints'
import type { ClusterHealth } from '@/api/types'

const theme = useThemeStore()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const sidebarOpen = ref(true)
const health = ref<ClusterHealth | null>(null)

const nav = [
  { name: 'dashboard',  label: 'Dashboard',  icon: HomeIcon },
  { name: 'blocklists', label: 'Blocklists', icon: NoSymbolIcon },
  { name: 'allowlist',  label: 'Allowlist',  icon: CheckBadgeIcon },
  { name: 'local-dns',  label: 'Local DNS',  icon: ServerStackIcon },
  { name: 'clients',    label: 'Clients',    icon: DevicePhoneMobileIcon },
  { name: 'profiles',   label: 'Profiles',   icon: UsersIcon },
  { name: 'schedules',  label: 'Schedules',  icon: ClockIcon },
  { name: 'categories', label: 'Categories', icon: TagIcon },
  { name: 'query-log',  label: 'Query log',  icon: QueueListIcon },
  { name: 'stats',      label: 'Stats',      icon: ChartBarIcon },
  { name: 'cluster',    label: 'Cluster',    icon: CpuChipIcon },
  { name: 'settings',   label: 'Settings',   icon: Cog6ToothIcon },
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
  settings: 'Settings',
  account: 'Account',
}
const pageTitle = computed(() => titles[String(route.name ?? '')] ?? 'dblock')

function onPaletteChange(e: Event) {
  const v = (e.target as HTMLSelectElement).value as
    'monokai' | 'monokai-solarized' | 'monokai-blue' | 'monokai-pro' | 'lipgloss'
  theme.setPalette(v)
}

function onLogout() {
  auth.logout()
  router.replace({ name: 'login' })
}

async function refreshHealth() {
  try { health.value = await clusterHealth() } catch { /* ignore */ }
}
onMounted(async () => {
  await refreshHealth()
  setInterval(refreshHealth, 10_000)
})
</script>
