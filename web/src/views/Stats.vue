<template>
  <div class="space-y-6">
    <!-- Header row with refresh -->
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-lg font-semibold text-fg-strong">Cluster statistics</h1>
        <p class="text-xs text-fg-muted" v-if="stats?.window_from && stats?.window_to">
          Window: {{ fmtHour(stats.window_from) }} → {{ fmtHour(stats.window_to) }}
        </p>
        <p class="text-xs text-fg-muted" v-else-if="stats">
          Window: waiting for first hourly flush…
        </p>
      </div>
      <button class="btn-ghost" :disabled="loading" @click="refresh" aria-label="Refresh">
        <ArrowPathIcon class="h-4 w-4" :class="{ 'animate-spin': loading }" />
        <span>Refresh</span>
      </button>
    </div>

    <!-- Loading state -->
    <div v-if="!stats && loading" class="card p-8 text-center text-sm text-fg-muted">
      Loading cluster stats…
    </div>

    <!-- Empty state -->
    <div v-else-if="stats && stats.per_node.length === 0"
         class="card p-8 text-center text-sm text-fg-muted">
      No aggregate data yet — generate some DNS traffic and wait one minute for the first flush.
    </div>

    <template v-else-if="stats">
      <!-- Header row: 5 StatTiles -->
      <div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-4">
        <StatTile label="Total queries" :value="fmtNum(stats.cluster_totals.total)" tone="accent" />
        <StatTile label="Blocked"
                  :value="`${fmtNum(stats.cluster_totals.blocked)} (${pct(stats.cluster_totals.blocked, stats.cluster_totals.total)}%)`"
                  tone="danger" />
        <StatTile label="Forwarded" :value="fmtNum(stats.cluster_totals.forwarded)" tone="success" />
        <StatTile label="Cached" :value="fmtNum(stats.cluster_totals.cached)" tone="accent" />
        <StatTile label="Local" :value="fmtNum(stats.cluster_totals.local)" tone="warning" />
      </div>

      <!-- Two-column grid: top domains + top clients -->
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div class="card p-4">
          <h2 class="text-sm font-semibold text-fg-strong mb-3">Top blocked domains</h2>
          <table class="table">
            <thead>
              <tr><th>Domain</th><th class="text-right w-32">Count</th></tr>
            </thead>
            <tbody>
              <tr v-for="d in topDomains" :key="d.domain">
                <td class="font-mono text-xs">{{ d.domain }}</td>
                <td class="text-right">
                  <div class="flex items-center justify-end gap-2">
                    <span class="font-mono text-xs">{{ fmtNum(d.count) }}</span>
                    <div class="h-1.5 w-20 bg-bg-hover rounded overflow-hidden">
                      <div class="h-full bg-danger rounded"
                           :style="{ width: barPct(d.count, maxDomainCount) + '%' }" />
                    </div>
                  </div>
                </td>
              </tr>
              <tr v-if="!topDomains.length">
                <td colspan="2" class="text-fg-muted text-center py-3">No data yet.</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="card p-4">
          <h2 class="text-sm font-semibold text-fg-strong mb-3">Top clients</h2>
          <table class="table">
            <thead>
              <tr><th>Client</th><th class="text-right w-32">Count</th></tr>
            </thead>
            <tbody>
              <tr v-for="c in topClients" :key="c.client">
                <td class="font-mono text-xs">{{ c.client }}</td>
                <td class="text-right">
                  <div class="flex items-center justify-end gap-2">
                    <span class="font-mono text-xs">{{ fmtNum(c.count) }}</span>
                    <div class="h-1.5 w-20 bg-bg-hover rounded overflow-hidden">
                      <div class="h-full bg-accent rounded"
                           :style="{ width: barPct(c.count, maxClientCount) + '%' }" />
                    </div>
                  </div>
                </td>
              </tr>
              <tr v-if="!topClients.length">
                <td colspan="2" class="text-fg-muted text-center py-3">No data yet.</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- Per-node breakdown -->
      <div class="card p-4">
        <h2 class="text-sm font-semibold text-fg-strong mb-3">Per-node breakdown</h2>
        <table class="table">
          <thead>
            <tr>
              <th>Node</th>
              <th>Hour</th>
              <th class="text-right">Total</th>
              <th class="text-right">Blocked</th>
              <th class="text-right">Forwarded</th>
              <th class="text-right">Cached</th>
              <th class="text-right">Local</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in sortedPerNode" :key="row.node_id + '|' + row.hour_start">
              <td class="font-mono text-xs">{{ row.node_id }}</td>
              <td class="text-xs">{{ fmtHour(row.hour_start) }}</td>
              <td class="text-right font-mono text-xs">{{ fmtNum(row.total) }}</td>
              <td class="text-right font-mono text-xs text-danger">{{ fmtNum(row.blocked) }}</td>
              <td class="text-right font-mono text-xs text-success">{{ fmtNum(row.forwarded) }}</td>
              <td class="text-right font-mono text-xs text-accent">{{ fmtNum(row.cached) }}</td>
              <td class="text-right font-mono text-xs text-warning">{{ fmtNum(row.local) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <!-- DoH attempts today — FSID FS-WebUiDohWidget. Rendered independently
         of the aggregate stats block so it surfaces even before the first
         hourly flush has produced any aggregate data. -->
    <div v-if="dohRows.length > 0 || dohError" class="card p-4 space-y-3">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-semibold text-fg-strong flex items-center gap-2">
          <LockClosedIcon class="h-4 w-4" />
          <span>DoH attempts today</span>
        </h2>
      </div>
      <div v-if="dohError" class="text-xs text-danger flex items-center gap-3">
        <span>Could not load DoH attempts.</span>
        <button class="btn-ghost" :disabled="dohLoading" @click="refreshDoH">Retry</button>
      </div>
      <table v-else class="table">
        <thead>
          <tr>
            <th>Client</th>
            <th class="text-right w-32">Probes</th>
            <th class="w-32"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in dohRows" :key="row.client">
            <td class="font-mono text-xs">{{ row.client }}</td>
            <td class="text-right font-mono text-xs">{{ fmtNum(row.count) }}</td>
            <td class="text-right">
              <router-link
                class="inline-flex items-center gap-1 text-xs text-accent hover:underline"
                :to="{ name: 'query-log', query: { client: row.client, category: 'doh-probe' } }"
              >
                <span>View log</span>
                <ArrowRightIcon class="h-3 w-3" />
              </router-link>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="card p-4">
      <h2 class="text-sm font-semibold text-fg-strong flex items-center gap-2 mb-2">
        <LockClosedIcon class="h-4 w-4" />
        <span>DoH attempts today</span>
      </h2>
      <p class="text-xs text-fg-muted">No DoH probes detected today.</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ArrowPathIcon, ArrowRightIcon, LockClosedIcon } from '@heroicons/vue/24/outline'
import StatTile from '@/components/StatTile.vue'
import { clusterStats, getClusterQueryLog } from '@/api/endpoints'
import type { ClusterStats, HourAggregate, QueryLogEntry } from '@/api/types'

const stats = ref<ClusterStats | null>(null)
const loading = ref(false)

const topDomains = computed(() => stats.value?.top_domains?.slice(0, 20) ?? [])
const topClients = computed(() => stats.value?.top_clients?.slice(0, 20) ?? [])

const maxDomainCount = computed(() =>
  topDomains.value.reduce((m, d) => Math.max(m, d.count), 0))
const maxClientCount = computed(() =>
  topClients.value.reduce((m, c) => Math.max(m, c.count), 0))

// Sort per_node: most recent hour first, ties broken by node_id ascending.
const sortedPerNode = computed<HourAggregate[]>(() => {
  const rows = stats.value?.per_node ? [...stats.value.per_node] : []
  rows.sort((a, b) => {
    const ta = Date.parse(a.hour_start)
    const tb = Date.parse(b.hour_start)
    if (tb !== ta) return tb - ta
    return a.node_id.localeCompare(b.node_id)
  })
  return rows
})

function pct(value: number, total: number): string {
  if (!total) return '0.0'
  return ((value / total) * 100).toFixed(1)
}

function barPct(value: number, max: number): number {
  if (!max) return 0
  return Math.round((value / max) * 100)
}

function fmtNum(n: number): string {
  return new Intl.NumberFormat().format(n)
}

const hourFmt = new Intl.DateTimeFormat(undefined, {
  month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit',
})
function fmtHour(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return hourFmt.format(d)
}

async function refresh() {
  loading.value = true
  try {
    stats.value = await clusterStats()
  } catch {
    /* ignore — keep previous snapshot visible */
  } finally {
    loading.value = false
  }
}

// ─── DoH attempts today (FS-WebUiDohWidget) ─────────────────────────────
interface DohRow { client: string; count: number }

const dohRows = ref<DohRow[]>([])
const dohError = ref(false)
const dohLoading = ref(false)

function todayISODate(): string {
  return new Date().toISOString().slice(0, 10)
}

async function refreshDoH() {
  dohLoading.value = true
  dohError.value = false
  try {
    const page = await getClusterQueryLog({ outcome: 'blocked', limit: 500 })
    const today = todayISODate()
    const counts = new Map<string, number>()
    const entries: QueryLogEntry[] = page.entries ?? []
    for (const e of entries) {
      if (e.blocklist_id !== 'cat:doh') continue
      if (!e.timestamp || e.timestamp.slice(0, 10) !== today) continue
      counts.set(e.client, (counts.get(e.client) ?? 0) + 1)
    }
    dohRows.value = Array.from(counts, ([client, count]) => ({ client, count }))
      .sort((a, b) => b.count - a.count)
      .slice(0, 10)
  } catch {
    dohError.value = true
    dohRows.value = []
  } finally {
    dohLoading.value = false
  }
}

let timer: number | undefined
let dohTimer: number | undefined
onMounted(async () => {
  await Promise.all([refresh(), refreshDoH()])
  timer = window.setInterval(refresh, 30_000)
  dohTimer = window.setInterval(refreshDoH, 60_000)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  if (dohTimer) window.clearInterval(dohTimer)
})
</script>
