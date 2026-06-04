<template>
  <div class="space-y-6">
    <!-- Stat tiles (AdGuard-Home-inspired big numbers) -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
      <StatTile label="Cluster status" :value="health?.status ?? '—'"
                :tone="health?.status === 'ok' ? 'success' : 'warning'" />
      <StatTile label="Mode" :value="health?.mode ?? '—'" tone="accent" />
      <StatTile label="Members" :value="memberStr" tone="accent" />
      <StatTile label="Total queries (window)" :value="String(stats?.cluster_totals.total ?? 0)" tone="accent" />
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- Blocked / forwarded breakdown -->
      <div class="card p-4">
        <h2 class="text-sm font-semibold text-fg-strong mb-3">Query breakdown</h2>
        <div v-if="stats" class="space-y-2 text-sm">
          <Breakdown label="Blocked"   :value="stats.cluster_totals.blocked"   tone="danger"  :total="stats.cluster_totals.total" />
          <Breakdown label="Forwarded" :value="stats.cluster_totals.forwarded" tone="success" :total="stats.cluster_totals.total" />
          <Breakdown label="Cached"    :value="stats.cluster_totals.cached"    tone="accent"  :total="stats.cluster_totals.total" />
          <Breakdown label="Local"     :value="stats.cluster_totals.local"     tone="warning" :total="stats.cluster_totals.total" />
        </div>
        <p v-else class="text-sm text-fg-muted">Loading…</p>
      </div>

      <!-- Top blocked domains -->
      <div class="card p-4">
        <h2 class="text-sm font-semibold text-fg-strong mb-3">Top blocked domains</h2>
        <table class="table">
          <thead><tr><th>Domain</th><th class="text-right">Count</th></tr></thead>
          <tbody>
            <tr v-for="d in stats?.top_domains?.slice(0, 8) ?? []" :key="d.domain">
              <td class="font-mono text-xs">{{ d.domain }}</td>
              <td class="text-right">{{ d.count }}</td>
            </tr>
            <tr v-if="!stats?.top_domains?.length"><td colspan="2" class="text-fg-muted text-center py-3">No data yet.</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Per-node summary table -->
    <div class="card p-4">
      <h2 class="text-sm font-semibold text-fg-strong mb-3">Cluster nodes</h2>
      <table class="table">
        <thead>
          <tr><th>Node</th><th>Role</th><th>Sync</th><th class="text-right">Commit</th></tr>
        </thead>
        <tbody>
          <tr v-for="n in status?.nodes ?? []" :key="n.node_id">
            <td class="font-mono text-xs">{{ n.node_id }}</td>
            <td><span :class="n.role === 'leader' ? 'badge-accent' : 'badge'">{{ n.role }}</span></td>
            <td>
              <span v-if="n.sync_state === 'in_sync'" class="badge-success">in sync</span>
              <span v-else-if="n.sync_state === 'behind'" class="badge-warning">behind</span>
              <span v-else class="badge-danger">unreachable</span>
            </td>
            <td class="text-right font-mono text-xs">{{ n.commit_index }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import StatTile from '@/components/StatTile.vue'
import Breakdown from '@/components/Breakdown.vue'
import { clusterHealth, clusterStats, clusterStatus } from '@/api/endpoints'
import type { ClusterHealth, ClusterStats, ClusterStatus } from '@/api/types'

const health = ref<ClusterHealth | null>(null)
const stats  = ref<ClusterStats | null>(null)
const status = ref<ClusterStatus | null>(null)

const memberStr = computed(() => {
  if (!health.value) return '—'
  return `${health.value.reachable_members} / ${health.value.members}`
})

async function refresh() {
  try {
    const [h, s, st] = await Promise.allSettled([clusterHealth(), clusterStats(), clusterStatus()])
    if (h.status === 'fulfilled') health.value = h.value
    if (s.status === 'fulfilled') stats.value  = s.value
    if (st.status === 'fulfilled') status.value = st.value
  } catch { /* ignore */ }
}

let timer: number | undefined
onMounted(async () => { await refresh(); timer = window.setInterval(refresh, 10_000) })
onBeforeUnmount(() => { if (timer) window.clearInterval(timer) })
</script>
