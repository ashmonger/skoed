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
            <td class="font-mono text-xs text-fg-muted">{{ n.api_address }}</td>
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
            <td colspan="8" class="text-fg-muted text-center py-6">No nodes reported.</td>
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
import { computed, onBeforeUnmount, onMounted, ref, h } from 'vue'
import {
  ArrowPathIcon, ArrowsRightLeftIcon, ClipboardIcon, PlusIcon, TrashIcon,
} from '@heroicons/vue/24/outline'
import {
  clusterHealth, clusterStatus, createJoinToken,
  removeNode, transferLeadership,
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

function canTransferTo(n: ClusterNode): boolean {
  return localIsLeader.value && n.role === 'follower'
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
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  window.removeEventListener('keydown', onKey)
})
</script>
