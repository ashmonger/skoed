<template>
  <div class="space-y-4">
    <!-- Toolbar -->
    <div class="card p-3">
      <div class="flex flex-wrap items-end gap-3">
        <!-- Scope toggle -->
        <div>
          <label class="label">Scope</label>
          <div class="inline-flex rounded border border-border overflow-hidden">
            <button
              type="button"
              class="px-3 py-1.5 text-sm font-medium transition-colors"
              :class="scope === 'local' ? 'bg-accent-subtle text-accent' : 'bg-bg-card text-fg hover:bg-bg-hover'"
              @click="onScopeChange('local')"
            >Local</button>
            <button
              type="button"
              class="px-3 py-1.5 text-sm font-medium transition-colors border-l border-border"
              :class="scope === 'cluster' ? 'bg-accent-subtle text-accent' : 'bg-bg-card text-fg hover:bg-bg-hover'"
              @click="onScopeChange('cluster')"
            >Cluster</button>
          </div>
        </div>

        <!-- Outcome -->
        <div>
          <label class="label flex items-center gap-1">
            <FunnelIcon class="h-4 w-4" />
            Outcome
          </label>
          <select v-model="outcomeDraft" class="input w-40">
            <option value="">All</option>
            <option value="blocked">Blocked</option>
            <option value="forwarded">Forwarded</option>
            <option value="cached">Cached</option>
            <option value="local">Local</option>
          </select>
        </div>

        <!-- Client -->
        <div>
          <label class="label">Client IP</label>
          <input
            v-model="clientDraft"
            type="text"
            placeholder="e.g. 10.0.0.5"
            class="input w-48"
          />
        </div>

        <!-- Page size -->
        <div>
          <label class="label">Page size</label>
          <select v-model.number="limitDraft" class="input w-24">
            <option :value="50">50</option>
            <option :value="100">100</option>
            <option :value="250">250</option>
          </select>
        </div>

        <!-- Live tail -->
        <div>
          <label class="label">Live tail</label>
          <button
            type="button"
            class="btn-secondary"
            @click="toggleLiveTail"
          >
            <component :is="liveTail ? PauseIcon : PlayIcon" class="h-4 w-4" />
            {{ liveTail ? 'Pause' : 'Play' }}
          </button>
        </div>

        <!-- Apply -->
        <div>
          <label class="label invisible">Apply</label>
          <button
            type="button"
            class="btn-primary"
            :disabled="!filtersDirty"
            @click="applyFilters"
          >Apply</button>
        </div>
      </div>
    </div>

    <!-- Table -->
    <div class="card">
      <div class="overflow-x-auto">
        <table class="table">
          <thead>
            <tr>
              <th>Time</th>
              <th v-if="scope === 'cluster'">Node</th>
              <th>Client</th>
              <th>Domain</th>
              <th>Type</th>
              <th>Outcome</th>
              <th>Blocklist</th>
              <th v-if="hasDnssec">DNSSEC</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="entries === null">
              <td :colspan="colCount" class="text-center text-fg-muted py-6">Loading…</td>
            </tr>
            <tr v-else-if="entries.length === 0">
              <td :colspan="colCount" class="text-center text-fg-muted py-6">
                No entries match the current filter.
              </td>
            </tr>
            <tr v-for="e in entries ?? []" :key="e.id">
              <td class="font-mono text-xs whitespace-nowrap" :title="relativeTime(e.timestamp)">
                {{ formatTime(e.timestamp) }}
              </td>
              <td v-if="scope === 'cluster'" class="font-mono text-xs">{{ e.node_id ?? '—' }}</td>
              <td class="font-mono text-xs">{{ e.client }}</td>
              <td>
                <span class="font-mono text-xs truncate max-w-xs block" :title="e.domain">{{ e.domain }}</span>
              </td>
              <td class="font-mono text-xs">{{ e.query_type }}</td>
              <td>
                <span :class="outcomeBadge(e.outcome)">{{ e.outcome }}</span>
              </td>
              <td class="font-mono text-xs text-fg-muted">{{ e.blocklist_id ?? '—' }}</td>
              <td v-if="hasDnssec" :title="e.dnssec_error ?? undefined">
                <span :class="dnssecBadge(e.dnssec_status)">{{ dnssecLabel(e.dnssec_status) }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Pagination -->
      <div class="flex flex-wrap items-center justify-between gap-2 p-3 border-t border-border">
        <div class="text-sm text-fg-muted">
          <span v-if="entries && entries.length > 0">
            Showing {{ offset + 1 }}-{{ offset + entries.length }} of {{ total }}
          </span>
          <span v-else>Showing 0 of {{ total }}</span>
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="btn-secondary"
            :disabled="offset === 0"
            @click="prevPage"
          >Prev</button>
          <button
            type="button"
            class="btn-secondary"
            :disabled="!hasNext"
            @click="nextPage"
          >Next</button>
        </div>
      </div>

      <!-- Per-node fan-out summary -->
      <div
        v-if="scope === 'cluster' && perNode && perNode.length > 0"
        class="px-3 pb-3 text-xs text-fg-muted"
      >
        {{ perNodeSummary }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { FunnelIcon, PauseIcon, PlayIcon } from '@heroicons/vue/24/outline'
import { clusterHealth, getClusterQueryLog, getQueryLog } from '@/api/endpoints'
import type { PerNodeStatus, QueryLogEntry } from '@/api/types'

type Scope = 'local' | 'cluster'
type Outcome = '' | 'forwarded' | 'blocked' | 'local' | 'cached'

// Applied filter state — drives API calls.
const scope = ref<Scope>('local')
const outcome = ref<Outcome>('')
const client = ref('')
const limit = ref(100)
const offset = ref(0)

// Draft (toolbar) state — committed on Apply or debounced auto-apply.
const outcomeDraft = ref<Outcome>('')
const clientDraft = ref('')
const limitDraft = ref(100)

// Data
const entries = ref<QueryLogEntry[] | null>(null)
const total = ref(0)
const perNode = ref<PerNodeStatus[] | undefined>(undefined)

// Live tail
const liveTail = ref(true)
let pollTimer: number | undefined
let debounceTimer: number | undefined

const filtersDirty = computed(() =>
  outcomeDraft.value !== outcome.value ||
  clientDraft.value !== client.value ||
  limitDraft.value !== limit.value,
)

const hasNext = computed(() => offset.value + (entries.value?.length ?? 0) < total.value)

const perNodeSummary = computed(() => {
  if (!perNode.value) return ''
  return perNode.value
    .map(n => {
      const count = n.entry_count !== undefined ? ` (${n.entry_count})` : ''
      return `${n.node_id}: ${n.status}${count}`
    })
    .join(', ')
})

function outcomeBadge(o: QueryLogEntry['outcome']): string {
  switch (o) {
    case 'blocked':   return 'badge-danger'
    case 'forwarded': return 'badge-success'
    case 'cached':    return 'badge-accent'
    case 'local':     return 'badge-warning'
  }
}

// M43 — DNSSEC column is shown when any loaded entry has a dnssec_status.
const hasDnssec = computed(() =>
  (entries.value ?? []).some(e => e.dnssec_status != null),
)

const colCount = computed(() => {
  let n = 6 // Time, Client, Domain, Type, Outcome, Blocklist
  if (scope.value === 'cluster') n++
  if (hasDnssec.value) n++
  return n
})

function dnssecBadge(status: QueryLogEntry['dnssec_status']): string {
  switch (status) {
    case 'secure':        return 'badge-success'
    case 'insecure':      return 'badge-warning'
    case 'bogus':         return 'badge-danger'
    case 'indeterminate': return 'badge-accent'
    default:              return 'badge-accent'
  }
}

function dnssecLabel(status: QueryLogEntry['dnssec_status']): string {
  if (!status) return '—'
  return status
}

const timeFmt = new Intl.DateTimeFormat(undefined, {
  hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
})

function formatTime(ts: string): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return timeFmt.format(d)
}

function relativeTime(ts: string): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const diffSec = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000))
  if (diffSec < 60) return `${diffSec}s ago`
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`
  return `${Math.floor(diffSec / 86400)}d ago`
}

async function fetchPage(prepend: boolean) {
  const params = {
    client: client.value || undefined,
    outcome: outcome.value || undefined,
    limit: limit.value,
    offset: offset.value,
  }
  try {
    const page = scope.value === 'cluster'
      ? await getClusterQueryLog(params)
      : await getQueryLog(params)
    total.value = page.total
    perNode.value = page.per_node

    if (prepend && entries.value && entries.value.length > 0) {
      // Live-tail prepend: merge incoming with current, dedupe by id,
      // preserving the most recent ordering returned by the API and
      // keeping our existing tail entries (trimmed to page limit).
      const seen = new Set<string>()
      const merged: QueryLogEntry[] = []
      for (const e of page.entries) {
        if (!seen.has(e.id)) { seen.add(e.id); merged.push(e) }
      }
      for (const e of entries.value) {
        if (!seen.has(e.id)) { seen.add(e.id); merged.push(e) }
      }
      entries.value = merged.slice(0, limit.value)
    } else {
      entries.value = page.entries
    }
  } catch {
    if (entries.value === null) entries.value = []
  }
}

function canLiveTailPrepend(): boolean {
  return offset.value === 0 && !client.value && !outcome.value
}

function startPolling() {
  stopPolling()
  if (!liveTail.value) return
  pollTimer = window.setInterval(() => {
    fetchPage(canLiveTailPrepend())
  }, 3000)
}

function stopPolling() {
  if (pollTimer) { window.clearInterval(pollTimer); pollTimer = undefined }
}

function toggleLiveTail() {
  liveTail.value = !liveTail.value
  if (liveTail.value) startPolling()
  else stopPolling()
}

function applyFilters() {
  outcome.value = outcomeDraft.value
  client.value = clientDraft.value.trim()
  limit.value = limitDraft.value
  offset.value = 0
  entries.value = null
  fetchPage(false).then(startPolling)
}

function onScopeChange(s: Scope) {
  if (s === scope.value) return
  scope.value = s
  offset.value = 0
  entries.value = null
  fetchPage(false).then(startPolling)
}

function prevPage() {
  if (offset.value === 0) return
  offset.value = Math.max(0, offset.value - limit.value)
  entries.value = null
  fetchPage(false).then(startPolling)
}

function nextPage() {
  if (!hasNext.value) return
  offset.value = offset.value + limit.value
  entries.value = null
  fetchPage(false).then(startPolling)
}

// Debounced auto-apply on draft changes (400ms).
watch([outcomeDraft, clientDraft, limitDraft], () => {
  if (debounceTimer) window.clearTimeout(debounceTimer)
  debounceTimer = window.setTimeout(() => {
    if (filtersDirty.value) applyFilters()
  }, 400)
})

onMounted(async () => {
  try {
    const h = await clusterHealth()
    if (h.members > 1) scope.value = 'cluster'
  } catch { /* leave scope as local */ }
  await fetchPage(false)
  startPolling()
})

onBeforeUnmount(() => {
  stopPolling()
  if (debounceTimer) window.clearTimeout(debounceTimer)
})
</script>
