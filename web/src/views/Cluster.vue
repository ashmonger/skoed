<template>
  <div class="space-y-6">
    <!-- Global error banner -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <!-- ─── Header strip ────────────────────────────────────────────────── -->
    <div class="card p-4">
      <div class="flex items-center justify-between mb-3">
        <h1 class="text-lg font-semibold text-fg-strong">Cluster</h1>
        <button class="btn-ghost" :disabled="refreshing"
                title="Refresh now" @click="refresh">
          <ArrowPathIcon class="h-4 w-4" :class="{ 'animate-spin': refreshing }" />
        </button>
      </div>

      <div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-4">
        <HeaderField label="Mode" :value="health?.mode ?? '—'" />
        <HeaderField label="Status">
          <span v-if="health?.status === 'ok'" class="badge-success">ok</span>
          <span v-else-if="health?.status === 'degraded'" class="badge-warning">degraded</span>
          <span v-else class="badge">—</span>
        </HeaderField>
        <HeaderField label="Leader">
          <div class="flex items-center gap-1">
            <span class="font-mono text-xs text-fg-strong truncate" :title="status?.leader_id">
              {{ status?.leader_id || '—' }}
            </span>
            <button v-if="status?.leader_id"
                    class="btn-ghost !p-1" title="Copy leader id"
                    @click="copyText(status.leader_id)">
              <ClipboardIcon class="h-3.5 w-3.5" />
            </button>
          </div>
        </HeaderField>
        <HeaderField label="Raft term" :value="String(status?.raft_term ?? '—')" />
        <HeaderField label="Members" :value="memberStr" />
      </div>
    </div>

    <!-- ─── Nodes table ─────────────────────────────────────────────────── -->
    <div class="card p-4">
      <h2 class="text-sm font-semibold text-fg-strong mb-3">Nodes</h2>

      <p v-if="loading" class="text-sm text-fg-muted py-6 text-center">Loading…</p>

      <table v-else class="table">
        <thead>
          <tr>
            <th>Node</th>
            <th>Role</th>
            <th>Sync state</th>
            <th>Raft address</th>
            <th>API address</th>
            <th>Version</th>
            <th class="text-right">Commit</th>
            <th>Last contact</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="n in status?.nodes ?? []" :key="n.node_id">
            <td class="font-mono text-xs text-fg-strong">{{ n.node_id }}</td>
            <td>
              <span :class="roleBadgeClass(n.role)">{{ n.role }}</span>
            </td>
            <td>
              <span v-if="n.sync_state === 'in_sync'" class="badge-success">in sync</span>
              <span v-else-if="n.sync_state === 'behind'" class="badge-warning">behind</span>
              <span v-else class="badge-danger">unreachable</span>
            </td>
            <td class="font-mono text-xs text-fg-muted">{{ n.raft_address }}</td>
            <td class="font-mono text-xs text-fg-muted">{{ nodeApiAddress(n) }}</td>
            <td class="font-mono text-xs text-fg-muted">{{ n.version ?? '—' }}</td>
            <td class="text-right font-mono text-xs">{{ n.commit_index }}</td>
            <td class="text-fg-muted text-xs" :title="formatAbsolute(n.last_contact)">
              {{ formatRelative(n.last_contact) }}
            </td>
            <td class="text-right whitespace-nowrap">
              <button v-if="canTransferTo(n)"
                      class="btn-ghost" title="Transfer leadership to this node"
                      @click="askTransfer(n)">
                <ArrowsRightLeftIcon class="h-4 w-4" />
              </button>
              <button v-if="canRemove(n)"
                      class="btn-ghost text-danger" title="Remove node"
                      @click="askRemove(n)">
                <TrashIcon class="h-4 w-4" />
              </button>
            </td>
          </tr>
          <tr v-if="!loading && !(status?.nodes ?? []).length">
            <td colspan="9" class="text-fg-muted text-center py-6">No nodes reported.</td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- ─── Join token panel ────────────────────────────────────────────── -->
    <div class="card p-4 space-y-3">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-semibold text-fg-strong">Add a node</h2>
        <button class="btn-primary" :disabled="generatingToken" @click="generateToken">
          <PlusIcon class="h-4 w-4" />
          {{ generatingToken ? 'Generating…' : 'Generate token' }}
        </button>
      </div>
      <p class="text-sm text-fg-muted">
        Generate a single-use token. Paste it into the new node's bootstrap section
        in <code class="font-mono text-xs">config.yaml</code>.
      </p>

      <p v-if="tokenError" class="text-sm text-danger">{{ tokenError }}</p>

      <div v-if="joinToken" class="card bg-bg-hover p-3 space-y-2">
        <div class="flex items-center justify-between">
          <span class="text-xs font-semibold text-fg-strong uppercase tracking-wide">
            Join payload
          </span>
          <button class="btn-secondary !py-1" @click="copyJoinPayload">
            <ClipboardIcon class="h-3.5 w-3.5" />
            {{ copied ? 'Copied!' : 'Copy to clipboard' }}
          </button>
        </div>
        <pre class="font-mono text-xs text-fg-strong whitespace-pre-wrap break-all">{{ joinPayloadText }}</pre>
        <p class="text-xs text-warning">This token will only be shown once.</p>
      </div>
    </div>

    <!-- ─── Join an existing cluster (single-node only) ─────────────────── -->
    <div v-if="health?.mode === 'single-node'" class="card p-4 space-y-3">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-semibold text-fg-strong">Join an existing cluster</h2>
      </div>
      <p class="text-sm text-fg-muted">
        Paste the join payload from the leader's Cluster page, then click
        <strong>Join</strong>. The join payload contains the token, leader address,
        and expiry — copy it in one click on the leader.
      </p>

      <p v-if="joinClusterError" class="text-sm text-danger">{{ joinClusterError }}</p>
      <p v-if="joinClusterSuccess" class="text-sm text-success">
        Joined cluster {{ joinClusterSuccess }}. This page will refresh shortly.
      </p>

      <textarea
        v-model="joinPayloadInput"
        rows="5"
        class="input font-mono text-xs"
        placeholder="token: xxxxxxxx&#10;leader_address: http://192.168.1.10:8080&#10;expires_at: 2026-01-01T00:00:00Z"
        :disabled="joining" />

      <div class="flex justify-end">
        <button class="btn-primary" :disabled="joining || !joinPayloadInput.trim()" @click="doJoin">
          {{ joining ? 'Joining…' : 'Join' }}
        </button>
      </div>
    </div>

    <!-- ─── Rolling upgrade (M18, cluster mode only) ────────────────────── -->
    <div v-if="health?.mode !== 'single-node'"
         class="card p-4 space-y-3">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-semibold text-fg-strong">Software update</h2>
        <span v-if="upgradeStatusData?.in_progress" class="badge-warning text-xs">upgrading…</span>
      </div>

      <!-- Up-to-date / unchecked state — always show check button -->
      <template v-if="!upgradeCheck?.upgrade_available && !upgradeStatusData?.in_progress && !upgradeStatusData?.completed_nodes.length && !upgradeStatusData?.failed_node">
        <div class="flex items-center gap-3">
          <button class="btn-secondary" :disabled="checkingUpgrade" @click="checkUpgradeManually">
            {{ checkingUpgrade ? 'Checking…' : 'Check for updates' }}
          </button>
          <span v-if="upgradeCheck !== null && !upgradeCheck.upgrade_available"
                class="text-xs text-success">
            Up to date
          </span>
        </div>
      </template>

      <!-- Upgrade prompt — shown when no upgrade is running yet -->
      <template v-if="upgradeCheck?.upgrade_available && !upgradeStatusData?.in_progress && !upgradeStatusData?.completed_nodes.length && !upgradeStatusData?.failed_node">
        <p class="text-sm text-fg-muted">
          skoed <span class="font-mono text-accent font-semibold">{{ upgradeCheck.available_version }}</span>
          is available. All nodes will be upgraded sequentially — followers first, then the leader — without losing quorum.
        </p>
        <div class="flex items-center gap-3">
          <button
            class="btn-primary"
            :disabled="rollingUpgrading"
            @click="startRollingUpgrade">
            {{ rollingUpgrading ? 'Starting…' : 'Upgrade cluster' }}
          </button>
          <a v-if="upgradeCheck.release_notes_url"
             :href="upgradeCheck.release_notes_url"
             target="_blank" rel="noopener"
             class="text-xs text-accent hover:underline">
            Release notes ↗
          </a>
          <button class="btn-ghost text-xs" :disabled="checkingUpgrade" @click="checkUpgradeManually">
            {{ checkingUpgrade ? 'Checking…' : 'Re-check' }}
          </button>
          <span class="text-xs text-fg-subtle ml-auto">
            Only the leader can start — followers forward automatically.
          </span>
        </div>
      </template>

      <!-- Live upgrade log — shown when upgrade is running or just completed -->
      <div v-if="upgradeLogOpen" class="space-y-2">
        <div class="flex items-center justify-between">
          <span class="text-xs text-fg-muted font-mono">
            <span v-if="upgradeStatusData?.in_progress" class="text-warning">● upgrading…</span>
            <span v-else-if="upgradeStatusData?.failed_node" class="text-danger">● failed on {{ upgradeStatusData.failed_node }}</span>
            <span v-else-if="upgradeLogDone" class="text-success">● upgrade complete</span>
            <span v-else>● waiting for log…</span>
          </span>
          <button class="btn-ghost !py-0.5 !px-2 text-xs"
                  @click="upgradeLogOpen = false; if (closeUpgradeLog) { closeUpgradeLog(); closeUpgradeLog = undefined }">
            close
          </button>
        </div>
        <div id="upgrade-log-box"
             class="rounded bg-bg-canvas border border-border p-3 font-mono text-xs text-fg-muted overflow-y-auto max-h-52 space-y-0.5">
          <div v-if="!upgradeLog.length" class="text-fg-subtle">Connecting…</div>
          <div v-for="(line, i) in upgradeLog" :key="i"
               :class="line.includes('FAIL') ? 'text-danger' : line.includes('OK') ? 'text-success' : ''">
            {{ line }}
          </div>
        </div>
      </div>

      <p v-if="rollingUpgradeError" class="text-sm text-danger">{{ rollingUpgradeError }}</p>
    </div>

    <!-- ─── Transfer leadership modal ───────────────────────────────────── -->
    <div v-if="pendingTransfer"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="pendingTransfer = null">
      <div class="card max-w-lg w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Transfer leadership?</h2>
        <p class="text-sm text-fg">
          Transfer leadership to
          <span class="font-mono text-xs text-fg-strong">{{ pendingTransfer.node_id }}</span>?
          <span class="text-fg-muted">
            The cluster will briefly elect during the handoff.
          </span>
        </p>
        <p v-if="actionError" class="text-sm text-danger">{{ actionError }}</p>
        <div class="flex justify-end gap-2">
          <button class="btn-secondary" :disabled="actionBusy"
                  @click="pendingTransfer = null">Cancel</button>
          <button class="btn-primary" :disabled="actionBusy" @click="confirmTransfer">
            {{ actionBusy ? 'Transferring…' : 'Transfer' }}
          </button>
        </div>
      </div>
    </div>

    <!-- ─── Remove node modal ───────────────────────────────────────────── -->
    <div v-if="pendingRemove"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="pendingRemove = null">
      <div class="card max-w-lg w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Remove node?</h2>
        <p class="text-sm text-fg">
          Remove
          <span class="font-mono text-xs text-fg-strong">{{ pendingRemove.node_id }}</span>
          from the cluster?
          <span class="text-fg-muted">
            Its data directory must be wiped before it can re-enrol.
          </span>
        </p>
        <p v-if="actionError" class="text-sm text-danger">{{ actionError }}</p>
        <div class="flex justify-end gap-2">
          <button class="btn-secondary" :disabled="actionBusy"
                  @click="pendingRemove = null">Cancel</button>
          <button class="btn-danger" :disabled="actionBusy" @click="confirmRemove">
            {{ actionBusy ? 'Removing…' : 'Remove' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, h } from 'vue'
import {
  ArrowPathIcon, ArrowsRightLeftIcon, ClipboardIcon, PlusIcon, TrashIcon,
} from '@heroicons/vue/24/outline'
import {
  clusterHealth, clusterStatus, createJoinToken,
  nodeSelfJoin, removeNode, transferLeadership,
  rollingUpgradeApply, rollingUpgradeStatus, checkUpgrade, upgradeLogStream,
  type RollingUpgradeStatus, type UpgradeCheck,
} from '@/api/endpoints'
import type {
  ClusterHealth, ClusterNode, ClusterStatus, JoinTokenResponse,
} from '@/api/types'

// ─── Tiny header field component (kept local; no other-file edits) ──────

const HeaderField = (props: { label: string; value?: string }, { slots }: { slots: { default?: () => any } }) =>
  h('div', { class: 'min-w-0' }, [
    h('div', { class: 'text-xs uppercase tracking-wide text-fg-muted font-semibold mb-1' }, props.label),
    h('div', { class: 'text-sm text-fg-strong truncate' },
      (slots.default ? slots.default() : (props.value ?? '—')) as any),
  ])

// ─── State ───────────────────────────────────────────────────────────────

const status = ref<ClusterStatus | null>(null)
const health = ref<ClusterHealth | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const lastError = ref('')

const joinToken = ref<JoinTokenResponse | null>(null)
const generatingToken = ref(false)
const tokenError = ref('')
const copied = ref(false)

const pendingTransfer = ref<ClusterNode | null>(null)
const pendingRemove = ref<ClusterNode | null>(null)
const actionBusy = ref(false)
const actionError = ref('')

// ─── Follower join ────────────────────────────────────────────────────────────
const joinPayloadInput = ref('')
const joining = ref(false)
const joinClusterError = ref('')
const joinClusterSuccess = ref('')

// ─── Rolling upgrade (M18) ────────────────────────────────────────────────────
const upgradeCheck = ref<UpgradeCheck | null>(null)
const checkingUpgrade = ref(false)
const rollingUpgrading = ref(false)
const rollingUpgradeError = ref('')
const rollingUpgradeMsg = ref('')
const upgradeStatusData = ref<RollingUpgradeStatus | null>(null)
let upgradeStatusTimer: number | undefined
const upgradeLog = ref<string[]>([])
const upgradeLogOpen = ref(false)
const upgradeLogDone = ref(false)
let closeUpgradeLog: (() => void) | undefined

// ─── Derived ─────────────────────────────────────────────────────────────

const memberStr = computed(() => {
  if (!health.value) return '—'
  return `${health.value.reachable_members} / ${health.value.members}`
})

// "Is THIS node the leader?" — resolved via clusterHealth(), which the
// backend evaluates against the receiving node's own raft state. We hit
// the same origin for the UI, so role==='leader' here means the local
// process serving the page is the cluster leader, which is what gates
// the Transfer action below.
const localIsLeader = computed(() => health.value?.role === 'leader')
const leaderId = computed(() => status.value?.leader_id ?? '')

const joinPayloadText = computed(() => {
  if (!joinToken.value) return ''
  const { token, leader_address, expires_at } = joinToken.value
  return `token: ${token}\nleader_address: ${leader_address}\nexpires_at: ${expires_at}`
})

// ─── Action gating ───────────────────────────────────────────────────────

function nodeApiAddress(n: ClusterNode): string {
  const ip = n.raft_address.replace(/:\d+$/, '')
  const port = n.api_address.replace(/^.*:/, '')
  return `${ip}:${port}`
}

function canTransferTo(n: ClusterNode): boolean {
  return n.role === 'follower'
}

function canRemove(n: ClusterNode): boolean {
  // Never offer to remove the current leader: that is what the transfer
  // flow is for.
  return n.role !== 'leader' && n.node_id !== leaderId.value
}

function roleBadgeClass(role: ClusterNode['role']): string {
  switch (role) {
    case 'leader':   return 'badge-accent'
    case 'learner':  return 'badge-warning'
    case 'removed':  return 'badge-danger'
    default:         return 'badge'
  }
}

// ─── Data loading ────────────────────────────────────────────────────────

async function refresh() {
  refreshing.value = true
  try {
    const [s, h] = await Promise.allSettled([clusterStatus(), clusterHealth()])
    if (s.status === 'fulfilled') status.value = s.value
    else lastError.value = errMsg(s.reason, 'Failed to load cluster status')
    if (h.status === 'fulfilled') health.value = h.value
  } finally {
    refreshing.value = false
    loading.value = false
  }
}

// ─── Join token ──────────────────────────────────────────────────────────

async function generateToken() {
  generatingToken.value = true
  tokenError.value = ''
  copied.value = false
  try {
    joinToken.value = await createJoinToken()
  } catch (err) {
    tokenError.value = errMsg(err, 'Failed to create join token')
  } finally {
    generatingToken.value = false
  }
}

async function copyJoinPayload() {
  if (!joinPayloadText.value) return
  await copyText(joinPayloadText.value)
  copied.value = true
  window.setTimeout(() => { copied.value = false }, 1500)
}

async function copyText(text: string) {
  try { await navigator.clipboard.writeText(text) } catch { /* ignore */ }
}

// ─── Follower join ────────────────────────────────────────────────────────

// Parse a plain-text join payload of the form:
//   token: <value>
//   leader_address: <value>
//   expires_at: <value>
function parseJoinPayload(raw: string): { token: string; leader_address: string } | null {
  const get = (key: string) => {
    const m = raw.match(new RegExp(`^${key}:\\s*(.+)$`, 'm'))
    return m ? m[1].trim() : ''
  }
  const token = get('token')
  const leader_address = get('leader_address')
  if (!token || !leader_address) return null
  return { token, leader_address }
}

async function doJoin() {
  joinClusterError.value = ''
  joinClusterSuccess.value = ''
  const parsed = parseJoinPayload(joinPayloadInput.value)
  if (!parsed) {
    joinClusterError.value = 'Invalid payload — expected lines: token, leader_address, expires_at'
    return
  }
  joining.value = true
  try {
    const result = await nodeSelfJoin(parsed.token, parsed.leader_address)
    joinClusterSuccess.value = result.cluster_id || 'ok'
    joinPayloadInput.value = ''
    // Refresh cluster state after a short delay to let Raft converge.
    window.setTimeout(refresh, 3000)
  } catch (err) {
    joinClusterError.value = errMsg(err, 'Failed to join cluster')
  } finally {
    joining.value = false
  }
}

// ─── Modals ──────────────────────────────────────────────────────────────

function askTransfer(n: ClusterNode) {
  actionError.value = ''
  pendingTransfer.value = n
}

function askRemove(n: ClusterNode) {
  actionError.value = ''
  pendingRemove.value = n
}

async function confirmTransfer() {
  const n = pendingTransfer.value
  if (!n) return
  actionBusy.value = true
  actionError.value = ''
  try {
    await transferLeadership(n.node_id)
    pendingTransfer.value = null
    await refresh()
  } catch (err) {
    actionError.value = errMsg(err, 'Failed to transfer leadership')
  } finally {
    actionBusy.value = false
  }
}

async function confirmRemove() {
  const n = pendingRemove.value
  if (!n) return
  actionBusy.value = true
  actionError.value = ''
  try {
    await removeNode(n.node_id)
    pendingRemove.value = null
    await refresh()
  } catch (err) {
    actionError.value = errMsg(err, 'Failed to remove node')
  } finally {
    actionBusy.value = false
  }
}

// ─── Rolling upgrade ─────────────────────────────────────────────────────

async function checkUpgradeManually() {
  checkingUpgrade.value = true
  try { upgradeCheck.value = await checkUpgrade() } catch { /* ignore */ }
  finally { checkingUpgrade.value = false }
}

async function startRollingUpgrade() {
  rollingUpgradeError.value = ''
  rollingUpgradeMsg.value = ''
  // Resolve the tar.gz URL for the current platform from the upgrade check.
  const assets = upgradeCheck.value?.assets ?? {}
  const arch = navigator.userAgent.includes('arm') || navigator.userAgent.includes('aarch64') ? 'arm64' : 'amd64'
  const assetURL = assets[`linux_${arch}`] ?? assets['linux_amd64'] ?? ''
  if (!assetURL) {
    rollingUpgradeError.value = 'No download URL found for this platform — check GitHub releases manually.'
    return
  }
  rollingUpgrading.value = true
  upgradeLog.value = []
  upgradeLogDone.value = false
  upgradeLogOpen.value = true

  // Start SSE log stream before kicking off upgrade so we don't miss early lines.
  if (closeUpgradeLog) closeUpgradeLog()
  closeUpgradeLog = upgradeLogStream(
    (line) => {
      upgradeLog.value.push(line)
      nextTick(() => {
        const el = document.getElementById('upgrade-log-box')
        if (el) el.scrollTop = el.scrollHeight
      })
    },
    () => { upgradeLogDone.value = true },
  )

  try {
    const result = await rollingUpgradeApply(assetURL)
    rollingUpgradeMsg.value = result.message
    pollUpgradeStatus()
  } catch (err) {
    rollingUpgradeError.value = errMsg(err, 'Failed to start rolling upgrade')
    if (closeUpgradeLog) { closeUpgradeLog(); closeUpgradeLog = undefined }
    upgradeLogOpen.value = false
  } finally {
    rollingUpgrading.value = false
  }
}

async function pollUpgradeStatus() {
  if (upgradeStatusTimer) window.clearInterval(upgradeStatusTimer)
  const tick = async () => {
    try {
      upgradeStatusData.value = await rollingUpgradeStatus()
      if (!upgradeStatusData.value.in_progress) {
        window.clearInterval(upgradeStatusTimer)
        upgradeStatusTimer = undefined
      }
    } catch { /* ignore polling errors */ }
  }
  await tick()
  upgradeStatusTimer = window.setInterval(tick, 3000)
}

// ─── Helpers ─────────────────────────────────────────────────────────────

function errMsg(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }, message?: string }
  return e?.response?.data?.error || e?.message || fallback
}

function formatRelative(iso?: string): string {
  if (!iso) return 'never'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return iso
  const diffSec = Math.round((Date.now() - then) / 1000)
  if (diffSec < 5) return 'just now'
  if (diffSec < 60) return `${diffSec}s ago`
  const mins = Math.round(diffSec / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.round(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.round(hours / 24)
  if (days < 30) return `${days}d ago`
  return new Date(iso).toLocaleDateString()
}

function formatAbsolute(iso?: string): string {
  if (!iso) return ''
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString()
}

// ─── Keyboard: close modals on Escape ────────────────────────────────────

function onKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (pendingTransfer.value && !actionBusy.value) { pendingTransfer.value = null; return }
  if (pendingRemove.value   && !actionBusy.value) { pendingRemove.value = null }
}

// ─── Lifecycle ───────────────────────────────────────────────────────────

let timer: number | undefined
onMounted(async () => {
  await refresh()
  timer = window.setInterval(refresh, 5_000)
  window.addEventListener('keydown', onKey)
  // Check for available upgrade from GitHub.
  try { upgradeCheck.value = await checkUpgrade() } catch { /* ignore */ }
  // Restore upgrade status if a rolling upgrade was running before page load.
  try {
    const s = await rollingUpgradeStatus()
    upgradeStatusData.value = s
    if (s.in_progress) pollUpgradeStatus()
  } catch { /* ignore */ }
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  if (upgradeStatusTimer) window.clearInterval(upgradeStatusTimer)
  if (closeUpgradeLog) { closeUpgradeLog(); closeUpgradeLog = undefined }
  window.removeEventListener('keydown', onKey)
})
</script>
