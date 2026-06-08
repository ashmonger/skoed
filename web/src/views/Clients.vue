<template>
  <div class="space-y-4">
    <!-- Toolbar -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">Clients</h1>
      <div class="flex items-center gap-2">
        <button class="btn-ghost" :disabled="loading" @click="refresh">
          <ArrowPathIcon class="h-4 w-4" :class="{ 'animate-spin': loading }" />
          <span>Refresh</span>
        </button>
        <div class="relative">
          <button class="btn-secondary" @click="exportOpen = !exportOpen">
            <ArrowDownTrayIcon class="h-4 w-4" />
            <span>Export reservations</span>
          </button>
          <div v-if="exportOpen"
               class="absolute right-0 top-full mt-1 z-10 card p-1 min-w-[160px]">
            <a v-for="f in (['dnsmasq','kea','json'] as const)"
               :key="f"
               class="block px-3 py-1.5 text-sm hover:bg-bg-hover rounded cursor-pointer text-fg"
               :href="exportURL(f)"
               :download="`dblock-reservations.${f === 'json' ? 'json' : f === 'kea' ? 'json' : 'conf'}`"
               @click="exportOpen = false">
              {{ f }}
            </a>
          </div>
        </div>
      </div>
    </div>

    <!-- Spoof anomalies (top-of-page if any) -->
    <div v-if="activeAnomalies.length > 0" class="card p-4 border-l-4 border-warning space-y-2">
      <div class="flex items-center gap-2">
        <ExclamationTriangleIcon class="h-5 w-5 text-warning" />
        <h2 class="text-sm font-semibold text-fg-strong">Possible identity spoofing</h2>
      </div>
      <p class="text-xs text-fg-muted">
        DHCP lease changes that look like spoofing. Acknowledge after investigation.
      </p>
      <ul class="text-sm space-y-1">
        <li v-for="a in activeAnomalies" :key="a.id"
            class="flex items-center gap-2 px-2 py-1 rounded hover:bg-bg-hover">
          <span class="badge-warning">{{ kindLabel(a.kind) }}</span>
          <span class="font-mono text-xs">{{ a.ip }}</span>
          <span class="text-xs text-fg-muted">
            mac {{ a.mac || '—' }} (was {{ a.prior_mac || '—' }})
          </span>
          <span class="ml-auto text-xs text-fg-subtle">{{ fmtTime(a.detected_at) }}</span>
          <button class="btn-ghost text-xs" @click="acknowledge(a.id)">Acknowledge</button>
        </li>
      </ul>
    </div>

    <!-- Filter row -->
    <div class="flex items-center gap-2">
      <div class="flex-1 relative">
        <MagnifyingGlassIcon class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-fg-subtle" />
        <input v-model="search"
               type="text"
               placeholder="Filter by IP, MAC, hostname, or client-id…"
               class="input pl-9" />
      </div>
      <select v-model="sortKey" class="input w-44">
        <option value="ip">Sort by IP</option>
        <option value="hostname">Sort by hostname</option>
        <option value="source">Sort by source</option>
      </select>
    </div>

    <!-- Empty / loading state -->
    <p v-if="loading && leases.length === 0" class="card p-6 text-sm text-fg-muted text-center">
      Loading lease snapshot…
    </p>
    <p v-else-if="leases.length === 0"
       class="card p-6 text-sm text-fg-muted text-center">
      No leases in the cache. Either DHCP integration isn't configured on this node,
      or the upstream source hasn't returned any leases yet.
    </p>

    <!-- Lease table -->
    <div v-else class="card overflow-hidden">
      <table class="table">
        <thead>
          <tr>
            <th>IP</th>
            <th>Hostname</th>
            <th>MAC</th>
            <th>Client-ID</th>
            <th>Source</th>
            <th class="text-right">Expires</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in filtered" :key="l.ip">
            <td class="font-mono text-xs">{{ l.ip }}</td>
            <td>{{ l.hostname || '—' }}</td>
            <td class="font-mono text-xs text-fg-muted">{{ l.mac || '—' }}</td>
            <td class="font-mono text-xs text-fg-muted">{{ l.client_id || '—' }}</td>
            <td><span class="badge bg-accent-subtle text-accent">{{ l.source }}</span></td>
            <td class="text-right text-xs text-fg-muted">{{ fmtExpiry(l.expires_at) }}</td>
          </tr>
        </tbody>
      </table>
      <p class="px-4 py-2 text-xs text-fg-muted border-t border-border">
        {{ filtered.length }} of {{ leases.length }} clients
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ArrowDownTrayIcon, ArrowPathIcon, ExclamationTriangleIcon, MagnifyingGlassIcon,
} from '@heroicons/vue/24/outline'
import {
  acknowledgeAnomaly, exportReservationsURL, listAnomalies, listLeases,
} from '@/api/endpoints'
import type { Anomaly, AnomalyKind, Lease } from '@/api/types'

const leases = ref<Lease[]>([])
const anomalies = ref<Anomaly[]>([])
const loading = ref(false)
const search = ref('')
const sortKey = ref<'ip' | 'hostname' | 'source'>('ip')
const exportOpen = ref(false)

const activeAnomalies = computed(() =>
  anomalies.value.filter(a => !a.acknowledged_at),
)

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  const filteredLs = q
    ? leases.value.filter(l =>
        l.ip.toLowerCase().includes(q) ||
        l.mac.toLowerCase().includes(q) ||
        l.hostname.toLowerCase().includes(q) ||
        l.client_id.toLowerCase().includes(q))
    : leases.value
  const sorted = [...filteredLs]
  sorted.sort((a, b) => {
    if (sortKey.value === 'ip') {
      return ipCompare(a.ip, b.ip)
    }
    if (sortKey.value === 'hostname') {
      return a.hostname.localeCompare(b.hostname)
    }
    return a.source.localeCompare(b.source)
  })
  return sorted
})

function ipCompare(a: string, b: string): number {
  const ap = a.split('.').map(n => parseInt(n, 10))
  const bp = b.split('.').map(n => parseInt(n, 10))
  for (let i = 0; i < Math.max(ap.length, bp.length); i++) {
    const diff = (ap[i] ?? 0) - (bp[i] ?? 0)
    if (diff !== 0) return diff
  }
  return 0
}

function kindLabel(k: AnomalyKind): string {
  switch (k) {
    case 'mac_changed_for_client_id': return 'MAC changed'
    case 'client_id_changed_for_mac': return 'Client-ID changed'
    case 'new_device_steals_hostname': return 'Hostname clash'
  }
}

function fmtTime(s: string): string {
  return new Date(s).toLocaleString()
}
function fmtExpiry(s: string): string {
  const t = new Date(s)
  if (isNaN(t.getTime())) return '—'
  const diff = t.getTime() - Date.now()
  if (diff <= 0) return 'expired'
  const m = Math.round(diff / 60_000)
  if (m < 60) return `${m}m`
  const h = Math.round(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.round(h / 24)}d`
}

function exportURL(f: 'dnsmasq' | 'kea' | 'json'): string {
  return exportReservationsURL(f)
}

async function refresh() {
  loading.value = true
  try {
    const [ls, ans] = await Promise.all([listLeases(), listAnomalies()])
    leases.value = ls
    anomalies.value = ans
  } catch { /* keep prior snapshot */ }
  finally {
    loading.value = false
  }
}

async function acknowledge(id: string) {
  try {
    await acknowledgeAnomaly(id)
    await refresh()
  } catch { /* surfaced by per-row UI later */ }
}

let timer: number | undefined
onMounted(async () => {
  await refresh()
  timer = window.setInterval(refresh, 30_000)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
})
</script>
