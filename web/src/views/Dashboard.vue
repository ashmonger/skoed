<template>
  <div class="space-y-6">
    <!-- M5.9.4 — Getting Started card. Shows only on a fresh cluster
         (0 blocklists, 0 profiles) and only until the operator either
         adds something or clicks [x]. Sits above all alert cards so a
         new admin sees it first; existing alerts (spoof/upgrade/stale/DoH)
         stay in their established order beneath. -->
    <div v-if="showGettingStarted"
         class="card p-4 border-l-4 border-accent space-y-3 relative">
      <button class="absolute top-2 right-2 text-fg-muted hover:text-fg-strong"
              aria-label="Dismiss Getting Started card"
              @click="dismissGettingStarted">
        <XMarkIcon class="w-4 h-4" />
      </button>
      <div class="flex items-center gap-2">
        <RocketLaunchIcon class="w-5 h-5 text-accent" />
        <h2 class="text-sm font-semibold text-fg-strong">Getting started</h2>
      </div>
      <p class="text-xs text-fg-muted">
        A few minutes to a working skoed. Each step links to the right
        page or doc — finish step 1 and this card hides itself.
      </p>
      <ol class="space-y-2 text-sm">
        <li class="flex items-start gap-3">
          <span class="font-mono text-xs text-accent w-5 flex-shrink-0 mt-0.5">①</span>
          <div class="flex-1">
            <router-link :to="{ name: 'blocklists' }"
                         class="text-accent hover:underline font-medium">
              Add a blocklist
            </router-link>
            <p class="text-xs text-fg-muted">
              Paste a hosts-format URL (e.g. Hagezi Pro) on the Blocklists page.
              skoed will fetch, parse, and start blocking on next DNS query.
            </p>
          </div>
        </li>
        <li class="flex items-start gap-3">
          <span class="font-mono text-xs text-accent w-5 flex-shrink-0 mt-0.5">②</span>
          <div class="flex-1">
            <a href="/docs/cluster/bootstrap.html"
               target="_blank" rel="noopener"
               class="text-accent hover:underline font-medium">
              Bootstrap a cluster
            </a>
            <span class="text-xs text-fg-muted">(optional)</span>
            <p class="text-xs text-fg-muted">
              Single-node is fine. Issue a join token from
              <router-link :to="{ name: 'cluster' }" class="text-accent hover:underline">Cluster</router-link>
              when you're ready to add HA.
            </p>
          </div>
        </li>
        <li class="flex items-start gap-3">
          <span class="font-mono text-xs text-accent w-5 flex-shrink-0 mt-0.5">③</span>
          <div class="flex-1">
            <span class="text-fg-strong font-medium">Point a client at skoed</span>
            <p class="text-xs text-fg-muted">
              Set your router's DHCP "DNS server" option to this node, or test
              one query first:
            </p>
            <details class="mt-1">
              <summary class="text-xs text-accent hover:underline cursor-pointer">
                Show the dig command
              </summary>
              <pre class="mt-1 text-xs font-mono bg-bg-hover text-fg-strong p-2 rounded overflow-x-auto"><code>dig @{{ skoedHost }} example.com</code></pre>
            </details>
          </div>
        </li>
      </ol>
      <p class="text-xs text-fg-muted">
        See the full walk-through:
        <a href="/docs/first-run/getting-started.html"
           target="_blank" rel="noopener"
           class="text-accent hover:underline">Getting started →</a>
      </p>
    </div>

    <!-- M3.6 — spoof alert (DHCP lease lookalikes). Listed before the DoH
         alert because identity-spoofing is the more serious signal. -->
    <div v-if="spoofAlert.length > 0" class="card p-4 border-l-4 border-danger space-y-2">
      <div class="flex items-center gap-2">
        <ExclamationTriangleIcon class="w-5 h-5 text-danger" />
        <h2 class="text-sm font-semibold text-fg-strong">Possible identity spoofing</h2>
      </div>
      <p class="text-xs text-fg-muted">
        DHCP lease changes that look like spoofing. Investigate then acknowledge from the
        <router-link :to="{ name: 'clients' }" class="text-accent hover:underline">Clients</router-link>
        page.
      </p>
      <ul class="text-sm space-y-0.5">
        <li v-for="a in spoofAlert" :key="a.id" class="flex items-center gap-2">
          <span class="badge-warning">{{ kindLabel(a.kind) }}</span>
          <span class="font-mono text-xs">{{ a.ip }}</span>
          <span v-if="a.mac" class="text-fg-muted text-xs">mac {{ a.mac }}</span>
          <router-link
            class="ml-auto text-xs text-accent hover:underline"
            :to="{ name: 'clients' }"
          >Review →</router-link>
        </li>
      </ul>
    </div>

    <!-- M5.6 — upgrade-available banner. Only the leader's feed cache
         is authoritative, but every node serves /upgrade/check. -->
    <div v-if="upgrade && upgrade.upgrade_available" class="card p-4 border-l-4 border-accent space-y-2">
      <div class="flex items-center gap-2">
        <ArrowUpCircleIcon class="w-5 h-5 text-accent" />
        <h2 class="text-sm font-semibold text-fg-strong">Upgrade available</h2>
      </div>
      <p class="text-sm">
        skoed <span class="font-mono text-accent">{{ upgrade.available_version }}</span> is
        out. You're on <span class="font-mono text-fg-muted">{{ upgrade.current_version || 'dev' }}</span>.
      </p>
      <div class="flex items-center gap-3 text-sm">
        <a v-if="upgrade.release_notes_url"
           :href="upgrade.release_notes_url"
           target="_blank" rel="noopener"
           class="text-accent hover:underline">Release notes</a>
        <button class="btn-primary ml-auto"
                :disabled="upgradeStarting"
                @click="onUpgradeStart">
          {{ upgradeStarting ? 'Starting…' : 'Upgrade now' }}
        </button>
      </div>
      <p v-if="upgradeStatus" class="text-xs text-fg-muted">{{ upgradeStatus }}</p>
    </div>

    <!-- M5.4 — stale blocklist alert. Blocklist hasn't refreshed in 2× its interval. -->
    <div v-if="staleBlocklists.length > 0" class="card p-4 border-l-4 border-warning space-y-2">
      <div class="flex items-center gap-2">
        <ExclamationTriangleIcon class="w-5 h-5 text-warning" />
        <h2 class="text-sm font-semibold text-fg-strong">Stale blocklists</h2>
      </div>
      <p class="text-xs text-fg-muted">
        These blocklists haven't refreshed in more than 2× their interval.
        Open the
        <router-link :to="{ name: 'blocklists' }" class="text-accent hover:underline">Blocklists</router-link>
        page to inspect or trigger a manual refresh.
      </p>
      <ul class="text-sm space-y-0.5">
        <li v-for="bl in staleBlocklists" :key="bl.id" class="flex items-center gap-2">
          <span class="font-mono text-xs text-fg-muted">{{ bl.id }}</span>
          <span class="font-medium">{{ bl.name }}</span>
          <span v-if="bl.last_refresh_status === 'error'" class="chip chip-danger">error</span>
          <span class="ml-auto text-xs text-fg-subtle">
            last refresh {{ bl.last_refresh_at ? formatRelative(bl.last_refresh_at) : 'never' }}
          </span>
        </li>
      </ul>
    </div>

    <!-- M3.5 DoH alert — surfaces clients with N+ DoH probes in the last hour -->
    <div v-if="dohAlert.length > 0" class="card p-4 border-l-4 border-warning space-y-2">
      <div class="flex items-center gap-2">
        <ExclamationTriangleIcon class="w-5 h-5 text-warning" />
        <h2 class="text-sm font-semibold text-fg-strong">Suspected DoH/DoT use</h2>
      </div>
      <p class="text-xs text-fg-muted">
        Clients with blocked DoH probes in the last hour. skoed catches the
        hostname-based path; clients using hardcoded resolver IPs need a
        firewall rule (see roadmap M3.5).
      </p>
      <ul class="text-sm space-y-0.5">
        <li v-for="row in dohAlert" :key="row.client" class="flex items-center gap-2">
          <span class="font-mono text-xs">{{ row.client }}</span>
          <span class="text-fg-muted">·</span>
          <span class="text-xs">{{ row.count }} probe{{ row.count > 1 ? 's' : '' }}</span>
          <span v-if="row.provider" class="badge-warning">{{ row.provider }}</span>
          <router-link
            class="ml-auto text-xs text-accent hover:underline"
            :to="{ name: 'query-log', query: { client: row.client } }"
          >View log →</router-link>
        </li>
      </ul>
    </div>

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
import {
  ArrowUpCircleIcon, ExclamationTriangleIcon, RocketLaunchIcon, XMarkIcon,
} from '@heroicons/vue/24/outline'
import StatTile from '@/components/StatTile.vue'
import Breakdown from '@/components/Breakdown.vue'
import {
  checkUpgrade, clusterHealth, clusterStats, clusterStatus,
  getClientDohStatus, getClusterQueryLog, listAnomalies, listBlocklists,
  listProfiles, startUpgrade, type UpgradeCheck,
} from '@/api/endpoints'
import type {
  Anomaly, AnomalyKind, Blocklist, ClusterHealth, ClusterStats, ClusterStatus, QueryLogEntry,
} from '@/api/types'

// M5.9.4 — Getting Started card. Visible only while the cluster has
// neither blocklists nor profiles AND the operator hasn't dismissed.
// Auto-hides on first blocklist; dismissal sticks via localStorage.
const GETTING_STARTED_KEY = 'skoed.gettingStarted.dismissed'
const blocklistsCount = ref<number | null>(null)
const profilesCount = ref<number | null>(null)
const gettingStartedDismissed = ref<boolean>(
  typeof window !== 'undefined' &&
    window.localStorage.getItem(GETTING_STARTED_KEY) === 'true',
)
const showGettingStarted = computed(() =>
  !gettingStartedDismissed.value &&
  blocklistsCount.value === 0 &&
  profilesCount.value === 0,
)
const skoedHost = computed(() =>
  typeof window !== 'undefined' ? window.location.hostname || '<skoed-host>' : '<skoed-host>',
)
function dismissGettingStarted() {
  gettingStartedDismissed.value = true
  try {
    window.localStorage.setItem(GETTING_STARTED_KEY, 'true')
  } catch {
    /* private mode / storage disabled — runtime hide still applies */
  }
}
async function refreshGettingStarted() {
  if (gettingStartedDismissed.value) return
  try {
    const [bl, pr] = await Promise.all([listBlocklists(), listProfiles()])
    // Count only operator-created entities. A fresh node already ships
    // with the bundled "cat:doh" category and a "default" profile —
    // those don't count as "the operator has done something."
    blocklistsCount.value = bl.filter((b) => !b.id.startsWith('cat:')).length
    profilesCount.value   = pr.filter((p) => p.id !== 'default').length
  } catch {
    // On error, leave counts null so the card stays hidden — we never
    // want to flash a fresh-install card at an established operator.
    blocklistsCount.value = blocklistsCount.value ?? -1
    profilesCount.value = profilesCount.value ?? -1
  }
}

const health = ref<ClusterHealth | null>(null)
const stats  = ref<ClusterStats | null>(null)
const status = ref<ClusterStatus | null>(null)

const memberStr = computed(() => {
  if (!health.value) return '—'
  return `${health.value.reachable_members} / ${health.value.members}`
})

// M3.5 — surface clients suspected of DoH/DoT use.
interface DohAlertRow { client: string; count: number; provider: string | null }
const dohAlert = ref<DohAlertRow[]>([])

// M3.6 — surface unacknowledged anti-spoof anomalies. Same shape as
// the Clients page card; this is the top-of-Dashboard preview.
const spoofAlert = ref<Anomaly[]>([])
function kindLabel(k: AnomalyKind): string {
  switch (k) {
    case 'mac_changed_for_client_id': return 'MAC changed'
    case 'client_id_changed_for_mac': return 'Client-ID changed'
    case 'new_device_steals_hostname': return 'Hostname clash'
  }
}
async function refreshSpoofAlert() {
  try {
    const all = await listAnomalies()
    spoofAlert.value = all.filter(a => !a.acknowledged_at).slice(0, 5)
  } catch {
    spoofAlert.value = []
  }
}

// M5.4 — surface auto-refresh blocklists that have gone stale
// (no successful refresh in > 2× their configured interval).
const staleBlocklists = ref<Blocklist[]>([])
async function refreshStaleBlocklists() {
  try {
    const all = await listBlocklists()
    const now = Date.now()
    staleBlocklists.value = all.filter((bl) => {
      if (bl.source.type !== 'url') return false
      if (!bl.refresh_interval_seconds) return false
      const intervalMs = bl.refresh_interval_seconds * 1000
      if (!bl.last_refresh_at) return true // never refreshed and auto-refresh enabled
      const last = new Date(bl.last_refresh_at).getTime()
      return now - last > 2 * intervalMs
    }).slice(0, 5)
  } catch {
    staleBlocklists.value = []
  }
}

// M5.6 — release-feed banner state.
const upgrade = ref<UpgradeCheck | null>(null)
const upgradeStarting = ref(false)
const upgradeStatus = ref('')
async function refreshUpgrade() {
  try {
    upgrade.value = await checkUpgrade()
  } catch {
    upgrade.value = null
  }
}
async function onUpgradeStart() {
  upgradeStarting.value = true
  upgradeStatus.value = ''
  try {
    await startUpgrade()
    upgradeStatus.value = 'Upgrade triggered. Watch the audit log for the binary-swap result.'
  } catch (err) {
    const e = err as { message?: string }
    upgradeStatus.value = e?.message || 'Upgrade failed; see audit log.'
  } finally {
    upgradeStarting.value = false
  }
}

function formatRelative(iso?: string): string {
  if (!iso) return 'never'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return iso
  const diffSec = Math.round((Date.now() - then) / 1000)
  if (diffSec < 60) return `${diffSec}s ago`
  const mins = Math.round(diffSec / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.round(mins / 60)
  if (hours < 48) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}

async function refreshDohAlert() {
  try {
    const page = await getClusterQueryLog({ outcome: 'blocked', limit: 500 })
    const entries: QueryLogEntry[] = page.entries ?? []
    const clients = new Set<string>()
    for (const e of entries) {
      if (e.blocklist_id === 'cat:doh') clients.add(e.client)
    }
    const statuses = await Promise.all(
      Array.from(clients).map(async (c) => {
        try { return await getClientDohStatus(c) }
        catch { return null }
      })
    )
    dohAlert.value = statuses
      .filter((s): s is NonNullable<typeof s> => s !== null && s.using_doh && s.doh_probes_1h > 0)
      .map((s) => ({ client: s.client, count: s.doh_probes_1h, provider: s.suspected_provider }))
      .sort((a, b) => b.count - a.count)
      .slice(0, 5)
  } catch {
    dohAlert.value = []
  }
}

async function refresh() {
  try {
    const [h, s, st] = await Promise.allSettled([clusterHealth(), clusterStats(), clusterStatus()])
    if (h.status === 'fulfilled') health.value = h.value
    if (s.status === 'fulfilled') stats.value  = s.value
    if (st.status === 'fulfilled') status.value = st.value
  } catch { /* ignore */ }
}

let timer: number | undefined
let dohTimer: number | undefined
let spoofTimer: number | undefined
let staleTimer: number | undefined
let upgradeTimer: number | undefined
onMounted(async () => {
  await Promise.all([
    refresh(), refreshDohAlert(), refreshSpoofAlert(),
    refreshStaleBlocklists(), refreshUpgrade(), refreshGettingStarted(),
  ])
  timer = window.setInterval(refresh, 10_000)
  dohTimer = window.setInterval(refreshDohAlert, 60_000)
  spoofTimer = window.setInterval(refreshSpoofAlert, 30_000)
  staleTimer = window.setInterval(refreshStaleBlocklists, 60_000)
  upgradeTimer = window.setInterval(refreshUpgrade, 5 * 60_000)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  if (dohTimer) window.clearInterval(dohTimer)
  if (spoofTimer) window.clearInterval(spoofTimer)
  if (staleTimer) window.clearInterval(staleTimer)
  if (upgradeTimer) window.clearInterval(upgradeTimer)
})
</script>
