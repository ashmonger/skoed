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
            <button v-for="f in (['dnsmasq','kea','json'] as const)"
               :key="f"
               class="block w-full text-left px-3 py-1.5 text-sm hover:bg-bg-hover rounded text-fg"
               @click="doExport(f)">
              {{ f }}
            </button>
          </div>
          <p v-if="exportError" class="absolute right-0 top-full mt-1 text-xs text-danger whitespace-nowrap bg-bg border border-border rounded px-2 py-1 z-10">
            {{ exportError }}
          </p>
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
    <div v-else-if="leases.length === 0" class="card p-6 space-y-3">
      <p class="text-sm text-fg-muted text-center">No DHCP leases found.</p>
      <div class="border border-border rounded p-4 text-sm space-y-2">
        <p class="font-medium text-fg-strong">How to populate this view</p>
        <p class="text-fg-muted text-xs">
          skoed can read leases from an upstream DHCP server or serve its own.
          Choose one:
        </p>
        <ul class="text-xs text-fg-muted space-y-1 list-disc list-inside">
          <li>
            <span class="font-medium text-fg">Built-in DHCP server</span> — enable and configure it on the
            <router-link class="text-accent hover:underline" :to="{ name: 'dhcp' }">DHCP page</router-link>.
            Leases from the built-in server appear here automatically.
          </li>
          <li>
            <span class="font-medium text-fg">External DHCP source</span> (dnsmasq, Kea, HTTP) — configure
            <code class="font-mono bg-bg-input px-1 rounded">dhcp_integration.source</code> in
            <code class="font-mono bg-bg-input px-1 rounded">node.yaml</code> on each node and restart.
          </li>
        </ul>
      </div>
    </div>

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
            <th class="text-right">Actions</th>
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
            <td class="text-right whitespace-nowrap">
              <div class="relative inline-block text-left">
                <button class="btn-ghost"
                        type="button"
                        aria-haspopup="menu"
                        :aria-expanded="openMenuIP === l.ip"
                        :data-testid="`client-row-actions-${l.ip}`"
                        :title="`Actions for ${l.ip}`"
                        @click.stop="toggleMenu(l.ip)">
                  <EllipsisHorizontalIcon class="h-4 w-4" />
                </button>
                <div v-if="openMenuIP === l.ip"
                     role="menu"
                     class="absolute right-0 mt-1 z-10 card p-1 min-w-[14rem]"
                     @click.stop>
                  <button type="button"
                          role="menuitem"
                          class="flex items-center gap-2 w-full px-3 py-1.5 text-sm text-fg
                                 hover:bg-bg-hover rounded text-left"
                          :data-testid="`copy-doh-gap-rules-${l.ip}`"
                          @click="openFwRules({ kind: 'client', ip: l.ip })">
                    <ClipboardDocumentListIcon class="h-4 w-4" />
                    <span>Copy DoH-gap rules (this IP)</span>
                  </button>
                  <button v-if="subnetFor(l.ip)"
                          type="button"
                          role="menuitem"
                          class="flex items-center gap-2 w-full px-3 py-1.5 text-sm text-fg
                                 hover:bg-bg-hover rounded text-left"
                          @click="openFwRules({ kind: 'subnet', cidr: subnetFor(l.ip)! })">
                    <ClipboardDocumentListIcon class="h-4 w-4" />
                    <span>Copy DoH-gap rules (this /24)</span>
                  </button>
                  <button type="button"
                          role="menuitem"
                          class="flex items-center gap-2 w-full px-3 py-1.5 text-sm text-fg
                                 hover:bg-bg-hover rounded text-left"
                          @click="openDetail(l.ip)">
                    <InformationCircleIcon class="h-4 w-4" />
                    <span>View details</span>
                  </button>
                </div>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
      <p class="px-4 py-2 text-xs text-fg-muted border-t border-border">
        {{ filtered.length }} of {{ leases.length }} clients
      </p>
    </div>

    <!-- M6 — Copy DoH-gap rules modal (per-row action / per-subnet action). -->
    <FirewallRulesModal
      v-if="fwRuleScope"
      :scope="fwRuleScope"
      @close="fwRuleScope = null" />

    <!-- Client detail drawer -->
    <Transition name="slide-right">
      <div v-if="detailIP"
           class="fixed inset-y-0 right-0 w-96 bg-bg border-l border-border shadow-xl z-40 flex flex-col">
        <div class="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
          <h2 class="font-semibold text-fg-strong">Client details</h2>
          <button class="btn-ghost p-1" @click="detailIP = null">
            <XMarkIcon class="h-4 w-4" />
          </button>
        </div>
        <div class="flex-1 overflow-y-auto p-4 space-y-4">
          <p v-if="detailLoading" class="text-sm text-fg-muted text-center py-8">Loading…</p>
          <p v-else-if="detailError" class="text-sm text-danger">{{ detailError }}</p>
          <template v-else-if="detail">
            <div class="space-y-1">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-fg-muted">Identity</h3>
              <div class="card p-3 space-y-2 text-sm">
                <div class="flex justify-between">
                  <span class="text-fg-muted">IP</span>
                  <span class="font-mono text-xs">{{ detail.ip }}</span>
                </div>
                <div class="flex justify-between">
                  <span class="text-fg-muted">Hostname</span>
                  <span>{{ detail.hostname || '—' }}</span>
                </div>
                <div class="flex justify-between">
                  <span class="text-fg-muted">MAC</span>
                  <span class="font-mono text-xs">{{ detail.mac || '—' }}</span>
                </div>
                <div class="flex justify-between">
                  <span class="text-fg-muted">Client-ID</span>
                  <span class="font-mono text-xs break-all">{{ detail.client_id || '—' }}</span>
                </div>
                <div class="flex justify-between">
                  <span class="text-fg-muted">Source</span>
                  <span class="badge bg-accent-subtle text-accent">{{ detail.source }}</span>
                </div>
                <div v-if="detail.last_seen" class="flex justify-between">
                  <span class="text-fg-muted">Last seen</span>
                  <span class="text-xs">{{ fmtTime(detail.last_seen) }}</span>
                </div>
                <div v-if="detail.duid" class="flex justify-between">
                  <span class="text-fg-muted">DUID</span>
                  <span class="font-mono text-xs break-all">{{ detail.duid }}</span>
                </div>
              </div>
            </div>

            <div v-if="detail.origin" class="space-y-1">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-fg-muted">Device origin</h3>
              <div class="card p-3 space-y-2 text-sm">
                <div class="flex justify-between">
                  <span class="text-fg-muted">Vendor</span>
                  <span>{{ detail.origin }}</span>
                </div>
                <div v-if="detail.origin_confidence" class="flex justify-between">
                  <span class="text-fg-muted">Confidence</span>
                  <span class="badge bg-accent-subtle text-accent">{{ detail.origin_confidence }}</span>
                </div>
              </div>
            </div>

            <div v-if="detail.profile_ids && detail.profile_ids.length > 0" class="space-y-1">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-fg-muted">Profiles</h3>
              <div class="card p-3 flex flex-wrap gap-1">
                <span v-for="pid in detail.profile_ids" :key="pid"
                      class="badge bg-accent-subtle text-accent text-xs font-mono">{{ pid }}</span>
              </div>
            </div>

            <div v-if="detail.ipv6_addresses && detail.ipv6_addresses.length > 0" class="space-y-1">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-fg-muted">IPv6 addresses</h3>
              <div class="card p-3 space-y-1">
                <p v-for="addr in detail.ipv6_addresses" :key="addr"
                   class="font-mono text-xs text-fg-muted">{{ addr }}</p>
              </div>
            </div>

            <div v-if="detailDoh" class="space-y-1">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-fg-muted">DoH status</h3>
              <div class="card p-3 space-y-2 text-sm">
                <div class="flex justify-between items-center">
                  <span class="text-fg-muted">Using DoH</span>
                  <span v-if="detailDoh.using_doh"
                        class="badge bg-warning/15 text-warning border border-warning/30">Yes</span>
                  <span v-else
                        class="badge bg-success/15 text-success border border-success/30">No</span>
                </div>
                <div class="flex justify-between">
                  <span class="text-fg-muted">Probes (1h)</span>
                  <span>{{ detailDoh.doh_probes_1h }}</span>
                </div>
                <div v-if="detailDoh.last_doh_query" class="flex justify-between">
                  <span class="text-fg-muted">Last DoH query</span>
                  <span class="text-xs">{{ fmtTime(detailDoh.last_doh_query) }}</span>
                </div>
                <div v-if="detailDoh.suspected_provider" class="flex justify-between">
                  <span class="text-fg-muted">Suspected provider</span>
                  <span>{{ detailDoh.suspected_provider }}</span>
                </div>
              </div>
            </div>
          </template>
        </div>
      </div>
    </Transition>

    <!-- Backdrop for detail drawer -->
    <div v-if="detailIP"
         class="fixed inset-0 z-30 bg-black/20"
         @click="detailIP = null" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ArrowDownTrayIcon, ArrowPathIcon, ClipboardDocumentListIcon,
  EllipsisHorizontalIcon, ExclamationTriangleIcon, InformationCircleIcon,
  MagnifyingGlassIcon, XMarkIcon,
} from '@heroicons/vue/24/outline'
import {
  acknowledgeAnomaly, exportReservationsURL, getClientDetail,
  getClientDohStatusDetail, listAnomalies, listLeases,
} from '@/api/endpoints'
import { getToken } from '@/api/client'
import type { FwRuleScope } from '@/api/endpoints'
import type { Anomaly, AnomalyKind, ClientDetail, ClientDohStatusDetail, Lease } from '@/api/types'
import FirewallRulesModal from '@/components/FirewallRulesModal.vue'

const leases = ref<Lease[]>([])
const anomalies = ref<Anomaly[]>([])
const loading = ref(false)
const search = ref('')
const sortKey = ref<'ip' | 'hostname' | 'source'>('ip')
const exportOpen = ref(false)
const exportError = ref('')

// M6 — per-row "Copy DoH-gap rules" overflow menu + modal scope.
const openMenuIP = ref<string | null>(null)
const fwRuleScope = ref<FwRuleScope | null>(null)

// Client detail drawer
const detailIP = ref<string | null>(null)
const detail = ref<ClientDetail | null>(null)
const detailDoh = ref<ClientDohStatusDetail | null>(null)
const detailLoading = ref(false)
const detailError = ref('')

function toggleMenu(ip: string) {
  openMenuIP.value = openMenuIP.value === ip ? null : ip
}
function openFwRules(scope: FwRuleScope) {
  fwRuleScope.value = scope
  openMenuIP.value = null
}

async function openDetail(ip: string) {
  openMenuIP.value = null
  detailIP.value = ip
  detail.value = null
  detailDoh.value = null
  detailError.value = ''
  detailLoading.value = true
  try {
    const [d, doh] = await Promise.all([
      getClientDetail(ip),
      getClientDohStatusDetail(ip),
    ])
    detail.value = d
    detailDoh.value = doh
  } catch (e: unknown) {
    detailError.value = (e as Error).message ?? 'Failed to load client details'
  } finally {
    detailLoading.value = false
  }
}
// Derive the /24 CIDR for a v4 IP — pure client-side, sent verbatim to
// the server which re-validates per FS-FwRuleRejectsInvalidSubnet.
function subnetFor(ip: string): string | null {
  const parts = ip.split('.')
  if (parts.length !== 4) return null
  if (parts.some(p => !/^\d{1,3}$/.test(p))) return null
  return `${parts[0]}.${parts[1]}.${parts[2]}.0/24`
}

function onDocClick() {
  openMenuIP.value = null
}

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

async function doExport(f: 'dnsmasq' | 'kea' | 'json') {
  exportOpen.value = false
  exportError.value = ''
  const token = getToken()
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = `Bearer ${token}`
  try {
    const resp = await fetch(exportReservationsURL(f), { headers })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}))
      throw new Error(body.error || `Nothing to export (HTTP ${resp.status})`)
    }
    const blob = await resp.blob()
    const ext = f === 'json' ? 'json' : f === 'kea' ? 'json' : 'conf'
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `skoed-reservations.${ext}`
    a.style.cssText = 'position:fixed;top:-100px;left:-100px'
    document.body.appendChild(a)
    a.click()
    setTimeout(() => { URL.revokeObjectURL(url); a.remove() }, 1000)
  } catch (err) {
    exportError.value = (err as Error).message ?? 'Export failed'
    setTimeout(() => { exportError.value = '' }, 4000)
  }
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
  window.addEventListener('click', onDocClick)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  window.removeEventListener('click', onDocClick)
})
</script>

<style scoped>
.slide-right-enter-active,
.slide-right-leave-active {
  transition: transform 0.2s ease;
}
.slide-right-enter-from,
.slide-right-leave-to {
  transform: translateX(100%);
}
</style>
