<template>
  <div class="space-y-4">
    <!-- Global error banner -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <!-- Header -->
    <div>
      <h1 class="text-lg font-semibold text-fg-strong">Allowlist</h1>
      <p class="text-sm text-fg-muted">
        Domains here are never blocked, even if a blocklist matches.
      </p>
    </div>

    <!-- Add form -->
    <div class="card p-4 space-y-3">
      <form class="flex items-start gap-2" @submit.prevent="submitAdd">
        <div class="flex-1">
          <label class="label" for="al-add">Add domain</label>
          <input id="al-add"
                 v-model="addInput"
                 class="input font-mono text-sm"
                 placeholder="example.com or *.example.com"
                 :disabled="adding"
                 @input="addError = ''" />
          <p v-if="addError" class="mt-1 text-xs text-danger">{{ addError }}</p>
        </div>
        <button type="submit" class="btn-primary mt-6" :disabled="adding">
          <PlusIcon class="h-4 w-4" /> {{ adding ? 'Adding…' : 'Add' }}
        </button>
        <button type="button"
                class="btn-secondary mt-6"
                :disabled="adding || bulkBusy"
                @click="bulkOpen = !bulkOpen">
          <QueueListIcon class="h-4 w-4" /> Bulk add
        </button>
      </form>

      <!-- Bulk add expander -->
      <div v-if="bulkOpen" class="border-t border-border pt-3 space-y-2">
        <label class="label" for="al-bulk">Bulk add <span class="text-fg-subtle font-normal">(one per line)</span></label>
        <textarea id="al-bulk"
                  v-model="bulkText"
                  rows="6"
                  class="input font-mono text-xs"
                  placeholder="example.com&#10;*.cdn.example.net&#10;trusted.local"
                  :disabled="bulkBusy" />
        <p v-if="bulkStatus" class="text-xs text-fg-muted">{{ bulkStatus }}</p>
        <ul v-if="bulkFailures.length" class="text-xs text-danger space-y-0.5 max-h-24 overflow-auto">
          <li v-for="f in bulkFailures" :key="f.domain">
            <span class="font-mono">{{ f.domain }}</span> — {{ f.message }}
          </li>
        </ul>
        <div class="flex justify-end gap-2">
          <button type="button"
                  class="btn-ghost"
                  :disabled="bulkBusy"
                  @click="closeBulk">Cancel</button>
          <button type="button"
                  class="btn-primary"
                  :disabled="bulkBusy || !bulkText.trim()"
                  @click="submitBulk">
            {{ bulkBusy ? `Adding ${bulkProgress.done}/${bulkProgress.total}…` : 'Add all' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Filter -->
    <div>
      <label class="label sr-only" for="al-filter">Filter</label>
      <div class="relative">
        <MagnifyingGlassIcon class="h-4 w-4 absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle" />
        <input id="al-filter"
               v-model="filter"
               class="input pl-9"
               placeholder="Filter domains…" />
      </div>
    </div>

    <!-- List -->
    <p v-if="loading" class="card p-6 text-sm text-fg-muted text-center">Loading…</p>

    <div v-else-if="!entries.length" class="card p-8 text-center space-y-2">
      <p class="text-sm font-medium text-fg-strong">No allowlisted domains yet.</p>
      <p class="text-sm text-fg-muted">
        Add a domain above to exempt it from every blocklist. Wildcards like
        <code class="font-mono text-xs text-fg">*.example.com</code> are supported.
      </p>
    </div>

    <div v-else-if="!filtered.length" class="card p-8 text-center">
      <p class="text-sm text-fg-muted">
        No domains match <span class="font-mono text-fg">{{ filter }}</span>.
      </p>
    </div>

    <div v-else class="card p-4">
      <p class="text-xs text-fg-muted mb-2">
        Showing {{ filtered.length }} of {{ entries.length }} domain{{ entries.length === 1 ? '' : 's' }}.
      </p>
      <table class="table">
        <thead>
          <tr>
            <th>Domain</th>
            <th class="text-right w-32">Actions</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="domain in filtered" :key="domain">
            <tr>
              <td class="font-mono text-sm">{{ domain }}</td>
              <td class="text-right whitespace-nowrap">
                <button class="btn-ghost text-danger"
                        :disabled="busyRows[domain]"
                        title="Remove from allowlist"
                        @click="askRemove(domain)">
                  <TrashIcon class="h-4 w-4" />
                  Remove
                </button>
              </td>
            </tr>
            <tr v-if="rowErrors[domain]">
              <td colspan="2" class="!py-1 text-xs text-danger">{{ rowErrors[domain] }}</td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>

    <!-- Remove confirmation modal -->
    <div v-if="pendingRemove"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="pendingRemove = null">
      <div class="card max-w-md w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Remove from allowlist?</h2>
        <p class="text-sm text-fg">
          Remove <span class="font-mono font-semibold">{{ pendingRemove }}</span> from allowlist?
        </p>
        <p class="text-xs text-fg-muted">
          Blocklist matches for this domain will resume taking effect.
        </p>
        <div class="flex justify-end gap-2">
          <button class="btn-secondary" :disabled="removing" @click="pendingRemove = null">Cancel</button>
          <button class="btn-danger" :disabled="removing" @click="confirmRemove">
            {{ removing ? 'Removing…' : 'Remove' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  MagnifyingGlassIcon, PlusIcon, QueueListIcon, TrashIcon,
} from '@heroicons/vue/24/outline'
import { addAllowlist, listAllowlist, removeAllowlist } from '@/api/endpoints'

// ─── State ───────────────────────────────────────────────────────────────

const entries = ref<string[]>([])
const loading = ref(true)
const lastError = ref('')
const rowErrors = reactive<Record<string, string>>({})
const busyRows = reactive<Record<string, boolean>>({})

const addInput = ref('')
const addError = ref('')
const adding = ref(false)

const filter = ref('')

const bulkOpen = ref(false)
const bulkText = ref('')
const bulkBusy = ref(false)
const bulkStatus = ref('')
const bulkProgress = reactive({ done: 0, total: 0 })
const bulkFailures = ref<{ domain: string; message: string }[]>([])

const pendingRemove = ref<string | null>(null)
const removing = ref(false)

// Domain or wildcard validation: labels of letters/digits/hyphens separated
// by dots, optional leading `*.`. Not a full RFC check — just guards against
// obvious garbage like spaces, slashes or empty strings.
const DOMAIN_RE = /^(\*\.)?([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$/

// ─── Computed ────────────────────────────────────────────────────────────

const filtered = computed(() => {
  const q = filter.value.trim().toLowerCase()
  const sorted = [...entries.value].sort((a, b) => a.localeCompare(b))
  if (!q) return sorted
  return sorted.filter(d => d.toLowerCase().includes(q))
})

// ─── Data loading ────────────────────────────────────────────────────────

async function refresh() {
  try {
    entries.value = await listAllowlist()
    lastError.value = ''
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load allowlist')
  } finally {
    loading.value = false
  }
}

// ─── Add ─────────────────────────────────────────────────────────────────

function validateDomain(raw: string): string | null {
  const d = raw.trim()
  if (!d) return 'Domain is required.'
  if (/\s/.test(d)) return 'Domain must not contain whitespace.'
  if (!DOMAIN_RE.test(d)) return 'Enter a valid domain (e.g. example.com or *.example.com).'
  return null
}

async function submitAdd() {
  const domain = addInput.value.trim()
  const err = validateDomain(domain)
  if (err) { addError.value = err; return }
  if (entries.value.includes(domain)) {
    addError.value = 'Domain is already in the allowlist.'
    return
  }
  adding.value = true
  addError.value = ''
  try {
    await addAllowlist(domain)
    entries.value = [...entries.value, domain]
    addInput.value = ''
  } catch (e) {
    addError.value = errMsg(e, 'Failed to add domain')
  } finally {
    adding.value = false
  }
}

// ─── Bulk add ────────────────────────────────────────────────────────────

function closeBulk() {
  if (bulkBusy.value) return
  bulkOpen.value = false
  bulkText.value = ''
  bulkStatus.value = ''
  bulkFailures.value = []
  bulkProgress.done = 0
  bulkProgress.total = 0
}

async function submitBulk() {
  // Split on any whitespace; dedup while preserving order; skip existing.
  const seen = new Set<string>()
  const candidates: string[] = []
  for (const tok of bulkText.value.split(/\s+/)) {
    const d = tok.trim()
    if (!d || seen.has(d)) continue
    seen.add(d)
    candidates.push(d)
  }

  bulkFailures.value = []
  bulkProgress.done = 0
  bulkProgress.total = candidates.length
  bulkStatus.value = ''

  if (!candidates.length) {
    bulkStatus.value = 'Nothing to add.'
    return
  }

  bulkBusy.value = true
  let added = 0
  let skipped = 0
  try {
    for (const domain of candidates) {
      const v = validateDomain(domain)
      if (v) {
        bulkFailures.value.push({ domain, message: v })
        bulkProgress.done++
        continue
      }
      if (entries.value.includes(domain)) {
        skipped++
        bulkProgress.done++
        continue
      }
      try {
        await addAllowlist(domain)
        entries.value = [...entries.value, domain]
        added++
      } catch (e) {
        bulkFailures.value.push({ domain, message: errMsg(e, 'Failed') })
      } finally {
        bulkProgress.done++
      }
    }
    const parts = [`Added ${added}`]
    if (skipped) parts.push(`${skipped} already present`)
    if (bulkFailures.value.length) parts.push(`${bulkFailures.value.length} failed`)
    bulkStatus.value = parts.join(', ') + '.'
    if (!bulkFailures.value.length) {
      bulkText.value = ''
    }
  } finally {
    bulkBusy.value = false
  }
}

// ─── Remove ──────────────────────────────────────────────────────────────

function askRemove(domain: string) {
  rowErrors[domain] = ''
  pendingRemove.value = domain
}

async function confirmRemove() {
  const domain = pendingRemove.value
  if (!domain) return
  removing.value = true
  busyRows[domain] = true
  try {
    await removeAllowlist(domain)
    entries.value = entries.value.filter(d => d !== domain)
    pendingRemove.value = null
  } catch (e) {
    rowErrors[domain] = errMsg(e, 'Failed to remove domain')
    lastError.value = rowErrors[domain]
    pendingRemove.value = null
  } finally {
    busyRows[domain] = false
    removing.value = false
  }
}

// ─── Helpers ─────────────────────────────────────────────────────────────

function errMsg(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }, message?: string }
  return e?.response?.data?.error || e?.message || fallback
}

function onKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (pendingRemove.value && !removing.value) { pendingRemove.value = null; return }
  if (bulkOpen.value && !bulkBusy.value) { closeBulk() }
}

onMounted(() => {
  refresh()
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
})
</script>
