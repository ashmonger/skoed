<template>
  <div class="space-y-4">
    <!-- Global error banner -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <!-- Toolbar -->
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-lg font-semibold text-fg-strong">Local DNS</h1>
        <p class="text-sm text-fg-muted">Resolve hostnames to specific addresses on this cluster.</p>
      </div>
      <button class="btn-primary" @click="openCreate">
        <PlusIcon class="h-4 w-4" /> New entry
      </button>
    </div>

    <!-- Table card -->
    <div class="card p-4 space-y-3">
      <!-- Filter -->
      <div class="flex items-center gap-2">
        <MagnifyingGlassIcon class="h-4 w-4 text-fg-muted" />
        <input v-model="filter" type="search" class="input max-w-sm"
               placeholder="Filter by hostname or value…" />
        <span v-if="filter && filtered.length !== entries.length"
              class="text-xs text-fg-muted">
          {{ filtered.length }} / {{ entries.length }} shown
        </span>
      </div>

      <p v-if="loading" class="text-sm text-fg-muted py-6 text-center">Loading…</p>

      <div v-else-if="!entries.length" class="py-12 text-center space-y-3">
        <p class="text-sm text-fg-muted">No local DNS entries yet.</p>
        <button class="btn-primary" @click="openCreate">
          <PlusIcon class="h-4 w-4" /> Create your first entry
        </button>
      </div>

      <div v-else-if="!filtered.length" class="py-12 text-center">
        <p class="text-sm text-fg-muted">No entries match "{{ filter }}".</p>
      </div>

      <table v-else class="table">
        <thead>
          <tr>
            <th>Hostname</th>
            <th class="w-24">Type</th>
            <th>Value</th>
            <th class="w-24 text-right">TTL</th>
            <th class="w-40 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="entry in filtered" :key="entry.id">
            <tr>
              <!-- Hostname -->
              <td class="font-mono text-xs text-fg-strong">
                <template v-if="editingId === entry.id">
                  <input v-model="editDraft.hostname" class="input" />
                </template>
                <template v-else>{{ entry.hostname }}</template>
              </td>

              <!-- Type -->
              <td>
                <template v-if="editingId === entry.id">
                  <select v-model="editDraft.type" class="input">
                    <option value="A">A</option>
                    <option value="AAAA">AAAA</option>
                    <option value="CNAME">CNAME</option>
                  </select>
                </template>
                <template v-else>
                  <span :class="typeBadgeClass(entry.type)">{{ entry.type }}</span>
                </template>
              </td>

              <!-- Value -->
              <td class="font-mono text-xs">
                <template v-if="editingId === entry.id">
                  <input v-model="editDraft.value" class="input"
                         :placeholder="valuePlaceholder(editDraft.type)" />
                </template>
                <template v-else>{{ entry.value }}</template>
              </td>

              <!-- TTL -->
              <td class="text-right font-mono text-xs">
                <template v-if="editingId === entry.id">
                  <input v-model.number="editDraft.ttl" type="number"
                         min="1" max="86400" class="input text-right" />
                </template>
                <template v-else>{{ entry.ttl }}s</template>
              </td>

              <!-- Actions -->
              <td class="text-right whitespace-nowrap">
                <template v-if="editingId === entry.id">
                  <button class="btn-ghost text-success"
                          :disabled="busyRows[entry.id]"
                          title="Save"
                          @click="saveEdit(entry)">
                    <CheckIcon class="h-4 w-4" />
                  </button>
                  <button class="btn-ghost"
                          :disabled="busyRows[entry.id]"
                          title="Cancel"
                          @click="cancelEdit()">
                    <XMarkIcon class="h-4 w-4" />
                  </button>
                </template>
                <template v-else>
                  <button class="btn-ghost"
                          :disabled="busyRows[entry.id] || editingId !== null"
                          title="Edit entry"
                          @click="startEdit(entry)">
                    <PencilSquareIcon class="h-4 w-4" />
                  </button>
                  <button class="btn-ghost text-danger"
                          :disabled="busyRows[entry.id] || editingId !== null"
                          title="Delete entry"
                          @click="askDelete(entry)">
                    <TrashIcon class="h-4 w-4" />
                  </button>
                </template>
              </td>
            </tr>
            <tr v-if="rowErrors[entry.id]">
              <td colspan="5" class="!py-1 text-xs text-danger">{{ rowErrors[entry.id] }}</td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>

    <!-- Create modal -->
    <div v-if="showCreate"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="closeCreate">
      <form class="card max-w-lg w-full p-6 space-y-4" @submit.prevent="submitCreate">
        <h2 class="text-base font-semibold text-fg-strong">New local DNS entry</h2>

        <p v-if="formError" class="text-sm text-danger">{{ formError }}</p>

        <div>
          <label class="label" for="dns-hostname">Hostname</label>
          <input id="dns-hostname" v-model="form.hostname" class="input"
                 required placeholder="nas.home.lan" />
        </div>

        <div>
          <span class="label">Type</span>
          <div class="flex gap-4 text-sm">
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="A" v-model="form.type" /> A
            </label>
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="AAAA" v-model="form.type" /> AAAA
            </label>
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="CNAME" v-model="form.type" /> CNAME
            </label>
          </div>
        </div>

        <div class="grid grid-cols-3 gap-3">
          <div class="col-span-2">
            <label class="label" for="dns-value">Value</label>
            <input id="dns-value" v-model="form.value" class="input"
                   required :placeholder="valuePlaceholder(form.type)" />
          </div>
          <div>
            <label class="label" for="dns-ttl">TTL (seconds)</label>
            <input id="dns-ttl" v-model.number="form.ttl" type="number"
                   min="1" max="86400" class="input" />
          </div>
        </div>

        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn-secondary" @click="closeCreate">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="submitting">
            {{ submitting ? 'Creating…' : 'Create entry' }}
          </button>
        </div>
      </form>
    </div>

    <!-- Delete confirmation modal -->
    <div v-if="pendingDelete"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="pendingDelete = null">
      <div class="card max-w-lg w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Delete entry?</h2>
        <p class="text-sm text-fg">
          Delete <span class="font-mono font-semibold">{{ pendingDelete.hostname }}</span>
          (<span class="font-mono">{{ pendingDelete.type }}</span>)?
        </p>
        <div class="flex justify-end gap-2">
          <button class="btn-secondary" @click="pendingDelete = null">Cancel</button>
          <button class="btn-danger" :disabled="deleting" @click="confirmDelete">
            {{ deleting ? 'Deleting…' : 'Delete' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  CheckIcon, MagnifyingGlassIcon, PencilSquareIcon, PlusIcon,
  TrashIcon, XMarkIcon,
} from '@heroicons/vue/24/outline'
import {
  createLocalDNS, deleteLocalDNS, listLocalDNS, updateLocalDNS,
} from '@/api/endpoints'
import type { LocalDNSEntry } from '@/api/types'

type DNSType = 'A' | 'AAAA' | 'CNAME'

// ─── State ───────────────────────────────────────────────────────────────

const entries = ref<LocalDNSEntry[]>([])
const loading = ref(true)
const lastError = ref('')
const rowErrors = reactive<Record<string, string>>({})
const busyRows = reactive<Record<string, boolean>>({})

const filter = ref('')

const showCreate = ref(false)
const submitting = ref(false)
const formError = ref('')

interface FormState {
  hostname: string
  type: DNSType
  value: string
  ttl: number
}

const emptyForm = (): FormState => ({
  hostname: '', type: 'A', value: '', ttl: 300,
})
const form = reactive<FormState>(emptyForm())

const pendingDelete = ref<LocalDNSEntry | null>(null)
const deleting = ref(false)

// Inline edit state — single row at a time
const editingId = ref<string | null>(null)
const editDraft = reactive<FormState>(emptyForm())
let editSnapshot: LocalDNSEntry | null = null

// ─── Derived ─────────────────────────────────────────────────────────────

const filtered = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (!q) return entries.value
  return entries.value.filter(e =>
    e.hostname.toLowerCase().includes(q) || e.value.toLowerCase().includes(q),
  )
})

// ─── Data loading ────────────────────────────────────────────────────────

async function refresh() {
  try {
    entries.value = await listLocalDNS()
    lastError.value = ''
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load local DNS entries')
  } finally {
    loading.value = false
  }
}

// ─── Inline edit ─────────────────────────────────────────────────────────

function startEdit(entry: LocalDNSEntry) {
  editingId.value = entry.id
  editSnapshot = { ...entry }
  Object.assign(editDraft, {
    hostname: entry.hostname,
    type: entry.type,
    value: entry.value,
    ttl: entry.ttl,
  })
  rowErrors[entry.id] = ''
}

function cancelEdit() {
  editingId.value = null
  editSnapshot = null
}

async function saveEdit(entry: LocalDNSEntry) {
  const hostname = editDraft.hostname.trim()
  const value = editDraft.value.trim()
  const type = editDraft.type
  const ttl = Number(editDraft.ttl)

  if (!hostname) {
    rowErrors[entry.id] = 'Hostname is required.'
    return
  }
  if (!Number.isFinite(ttl) || ttl < 1 || ttl > 86400) {
    rowErrors[entry.id] = 'TTL must be between 1 and 86400 seconds.'
    return
  }
  const valueErr = validateValue(type, value)
  if (valueErr) {
    rowErrors[entry.id] = valueErr
    return
  }

  const patch = { hostname, type, value, ttl }
  // Optimistic apply
  Object.assign(entry, patch)
  busyRows[entry.id] = true
  rowErrors[entry.id] = ''
  try {
    const updated = await updateLocalDNS(entry.id, patch)
    Object.assign(entry, updated)
    editingId.value = null
    editSnapshot = null
  } catch (err) {
    if (editSnapshot) Object.assign(entry, editSnapshot) // rollback
    rowErrors[entry.id] = errMsg(err, 'Failed to update entry')
  } finally {
    busyRows[entry.id] = false
  }
}

// ─── Delete ──────────────────────────────────────────────────────────────

function askDelete(entry: LocalDNSEntry) { pendingDelete.value = entry }

async function confirmDelete() {
  const entry = pendingDelete.value
  if (!entry) return
  deleting.value = true
  try {
    await deleteLocalDNS(entry.id)
    entries.value = entries.value.filter(e => e.id !== entry.id)
    pendingDelete.value = null
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to delete entry')
  } finally {
    deleting.value = false
  }
}

// ─── Create modal ────────────────────────────────────────────────────────

function openCreate() {
  Object.assign(form, emptyForm())
  formError.value = ''
  showCreate.value = true
}

function closeCreate() {
  if (submitting.value) return
  showCreate.value = false
}

async function submitCreate() {
  formError.value = ''
  const hostname = form.hostname.trim()
  const value = form.value.trim()
  const ttl = Number(form.ttl)

  if (!hostname) {
    formError.value = 'Hostname is required.'
    return
  }
  if (!Number.isFinite(ttl) || ttl < 1 || ttl > 86400) {
    formError.value = 'TTL must be between 1 and 86400 seconds.'
    return
  }
  const valueErr = validateValue(form.type, value)
  if (valueErr) {
    formError.value = valueErr
    return
  }

  submitting.value = true
  try {
    const created = await createLocalDNS({ hostname, type: form.type, value, ttl })
    entries.value = [created, ...entries.value]
    showCreate.value = false
  } catch (err) {
    formError.value = errMsg(err, 'Failed to create entry')
  } finally {
    submitting.value = false
  }
}

// ─── Validation ──────────────────────────────────────────────────────────

function validateValue(type: DNSType, value: string): string {
  if (!value) return 'Value is required.'
  switch (type) {
    case 'A':
      return isIPv4(value) ? '' : 'Value must be a valid IPv4 address (e.g. 192.0.2.1).'
    case 'AAAA':
      return isIPv6(value) ? '' : 'Value must be a valid IPv6 address (e.g. 2001:db8::1).'
    case 'CNAME':
      return isDomain(value) ? '' : 'Value must be a valid domain (e.g. target.example.com).'
  }
}

function isIPv4(v: string): boolean {
  const m = v.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/)
  if (!m) return false
  return m.slice(1).every(o => {
    const n = Number(o)
    return n >= 0 && n <= 255 && String(n) === o
  })
}

function isIPv6(v: string): boolean {
  if (!v.includes(':')) return false
  try { new URL('http://[' + v + ']'); return true } catch { return false }
}

function isDomain(v: string): boolean {
  if (v.startsWith('.') || v.endsWith('.')) return false
  if (v.length > 253) return false
  return /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$/.test(v)
}

// ─── Presentation helpers ────────────────────────────────────────────────

function typeBadgeClass(t: DNSType): string {
  switch (t) {
    case 'A':     return 'badge-success'
    case 'AAAA':  return 'badge-accent'
    case 'CNAME': return 'badge-warning'
  }
}

function valuePlaceholder(t: DNSType): string {
  switch (t) {
    case 'A':     return '192.0.2.1'
    case 'AAAA':  return '2001:db8::1'
    case 'CNAME': return 'target.example.com'
  }
}

function errMsg(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }, message?: string }
  return e?.response?.data?.error || e?.message || fallback
}

// ─── Keyboard: close modals / cancel edit on Escape ──────────────────────

function onKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (pendingDelete.value) { pendingDelete.value = null; return }
  if (showCreate.value && !submitting.value) { showCreate.value = false; return }
  if (editingId.value) { cancelEdit() }
}

onMounted(() => {
  refresh()
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
})
</script>
