<template>
  <div class="space-y-4">
    <div>
      <h1 class="text-lg font-semibold text-fg-strong">Audit log</h1>
      <p class="text-sm text-fg-muted">
        Every state-changing API call is recorded here with actor, action, target, and result.
        Replicated through Raft &mdash; identical on every node.
      </p>
    </div>

    <!-- Filter bar -->
    <div class="card p-3">
      <div class="flex flex-wrap items-end gap-3">
        <div>
          <label class="label">Actor</label>
          <input v-model="actorDraft" type="text" placeholder="user:admin"
                 class="input w-48" @keyup.enter="apply" />
        </div>
        <div>
          <label class="label">Action prefix</label>
          <input v-model="actionDraft" type="text" placeholder="blocklist."
                 class="input w-48" @keyup.enter="apply" />
        </div>
        <div>
          <label class="label">Result</label>
          <select v-model="resultDraft" class="input w-32">
            <option value="">All</option>
            <option value="ok">OK</option>
            <option value="error">Error</option>
          </select>
        </div>
        <div>
          <label class="label">Page size</label>
          <select v-model.number="limitDraft" class="input w-24">
            <option :value="25">25</option>
            <option :value="50">50</option>
            <option :value="100">100</option>
          </select>
        </div>
        <div>
          <label class="label invisible">Apply</label>
          <button type="button" class="btn-primary" :disabled="!dirty" @click="apply">
            Apply
          </button>
        </div>
        <div class="ml-auto self-end">
          <button type="button" class="btn-secondary" @click="load">Refresh</button>
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
              <th>Actor</th>
              <th>Action</th>
              <th>Target</th>
              <th>Result</th>
              <th>Node</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading">
              <td colspan="6" class="text-center text-fg-muted py-6">Loading&hellip;</td>
            </tr>
            <tr v-else-if="entries.length === 0">
              <td colspan="6" class="text-center text-fg-muted py-6">
                No audit entries match the current filters.
              </td>
            </tr>
            <template v-else>
              <template v-for="(e, idx) in entries" :key="e.id">
                <tr
                  class="cursor-pointer hover:bg-bg-hover"
                  :class="{ 'border-l-2 border-l-danger': e.result === 'error' }"
                  @click="toggle(idx)"
                >
                  <td class="font-mono text-xs">{{ formatTime(e.timestamp) }}</td>
                  <td>{{ e.actor }}</td>
                  <td class="font-mono text-xs">{{ e.action }}</td>
                  <td class="text-fg-muted">{{ e.target || '—' }}</td>
                  <td>
                    <span
                      class="chip"
                      :class="e.result === 'ok' ? 'chip-success' : 'chip-danger'"
                    >{{ e.result }}</span>
                  </td>
                  <td class="text-fg-muted">{{ e.node_id || '—' }}</td>
                </tr>
                <tr v-if="expanded === idx" class="bg-bg-subtle">
                  <td colspan="6" class="px-4 py-3">
                    <dl class="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-1 text-sm">
                      <dt class="text-fg-muted">id</dt>
                      <dd class="font-mono text-xs break-all">{{ e.id }}</dd>
                      <dt class="text-fg-muted">seq</dt>
                      <dd class="font-mono text-xs">{{ e.seq }}</dd>
                      <template v-if="e.diff">
                        <dt class="text-fg-muted">diff</dt>
                        <dd class="font-mono text-xs">{{ e.diff }}</dd>
                      </template>
                      <template v-if="e.error">
                        <dt class="text-fg-muted">error</dt>
                        <dd class="font-mono text-xs text-danger">{{ e.error }}</dd>
                      </template>
                      <template v-if="e.request_id">
                        <dt class="text-fg-muted">request_id</dt>
                        <dd class="font-mono text-xs break-all">{{ e.request_id }}</dd>
                      </template>
                    </dl>
                  </td>
                </tr>
              </template>
            </template>
          </tbody>
        </table>
      </div>
      <div class="flex items-center justify-between border-t border-border px-3 py-2 text-sm text-fg-muted">
        <div>
          {{ entries.length }} of {{ total }} entries
        </div>
        <div class="space-x-2">
          <button type="button" class="btn-secondary" :disabled="offset === 0" @click="prev">Prev</button>
          <button type="button" class="btn-secondary" :disabled="offset + limit >= total" @click="next">Next</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { listAudit } from '@/api/endpoints'
import type { AuditEntry } from '@/api/types'

const actorDraft = ref('')
const actionDraft = ref('')
const resultDraft = ref<'' | 'ok' | 'error'>('')
const limitDraft = ref(50)

const actor = ref('')
const action = ref('')
const result = ref<'' | 'ok' | 'error'>('')
const limit = ref(50)
const offset = ref(0)

const entries = ref<AuditEntry[]>([])
const total = ref(0)
const loading = ref(false)
const expanded = ref<number | null>(null)

const dirty = computed(
  () =>
    actorDraft.value !== actor.value ||
    actionDraft.value !== action.value ||
    resultDraft.value !== result.value ||
    limitDraft.value !== limit.value,
)

async function load() {
  loading.value = true
  try {
    const page = await listAudit({
      actor: actor.value || undefined,
      action: action.value || undefined,
      result: result.value || undefined,
      limit: limit.value,
      offset: offset.value,
    })
    entries.value = page.entries
    total.value = page.total
  } finally {
    loading.value = false
  }
}

function apply() {
  actor.value = actorDraft.value
  action.value = actionDraft.value
  result.value = resultDraft.value
  limit.value = limitDraft.value
  offset.value = 0
  load()
}

function prev() {
  offset.value = Math.max(0, offset.value - limit.value)
  load()
}

function next() {
  offset.value += limit.value
  load()
}

function toggle(idx: number) {
  expanded.value = expanded.value === idx ? null : idx
}

function formatTime(ts: string) {
  const d = new Date(ts)
  return d.toLocaleString()
}

onMounted(load)
</script>
