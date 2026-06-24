<template>
  <div class="space-y-4">
    <!-- Global error banner -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <!-- Filtering pause card -->
    <div class="card p-4 flex items-center justify-between gap-4">
      <div class="flex items-center gap-3 min-w-0">
        <ClockIcon class="h-5 w-5 text-fg-muted flex-shrink-0" />
        <div class="min-w-0">
          <p class="text-sm font-medium text-fg-strong">Filtering</p>
          <p v-if="pauseActive" class="text-xs text-fg-muted truncate">
            Paused
            <span v-if="globalPause?.profile_ids?.length" class="text-fg-subtle">
              ({{ globalPause.profile_ids.map(profileName).join(', ') }})
            </span>
            &mdash; resumes in
            <span class="font-mono text-fg">{{ formatRemaining(pauseRemainingMs) }}</span>
            <span v-if="globalPause?.reason"> &mdash; {{ globalPause.reason }}</span>
          </p>
          <p v-else class="text-xs text-fg-muted">Active &mdash; all blocklists enforced</p>
        </div>
      </div>
      <div class="flex items-center gap-2 flex-shrink-0">
        <span v-if="pauseActive" class="badge-warning">paused</span>
        <button v-if="pauseActive" class="btn-secondary" :disabled="resumingGlobal" @click="resumeFiltering">
          <PlayCircleIcon class="h-4 w-4" /> Resume now
        </button>
        <button v-else class="btn-secondary" @click="openPauseModal">
          <ClockIcon class="h-4 w-4" /> Pause filtering
        </button>
      </div>
    </div>

    <!-- Toolbar -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">Blocklists</h1>
      <button class="btn-primary" @click="openCreate">
        <PlusIcon class="h-4 w-4" /> New blocklist
      </button>
    </div>

    <!-- Table card -->
    <div class="card p-4">
      <p v-if="loading" class="text-sm text-fg-muted py-6 text-center">Loading…</p>

      <div v-else-if="!blocklists.length" class="py-12 text-center space-y-3">
        <p class="text-sm text-fg-muted">No blocklists yet.</p>
        <button class="btn-primary" @click="openCreate">
          <PlusIcon class="h-4 w-4" /> Create your first blocklist
        </button>
      </div>

      <table v-else class="table">
        <thead>
          <tr>
            <th class="w-16">Enabled</th>
            <th>ID</th>
            <th>Name</th>
            <th>Source</th>
            <th class="text-right">Domains</th>
            <th>Last updated</th>
            <th>Auto-refresh</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="bl in blocklists" :key="bl.id">
            <tr>
              <td>
                <button type="button"
                        :disabled="busyRows[bl.id]"
                        class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors"
                        :class="bl.enabled ? 'bg-success' : 'bg-bg-hover border border-border'"
                        :aria-pressed="bl.enabled"
                        :aria-label="`Toggle ${bl.name}`"
                        @click="toggleEnabled(bl)">
                  <span class="inline-block h-4 w-4 rounded-full bg-bg-input shadow transform transition-transform"
                        :class="bl.enabled ? 'translate-x-4' : 'translate-x-1'" />
                </button>
              </td>
              <td class="font-mono text-xs text-fg-muted">{{ bl.id }}</td>
              <td class="font-medium text-fg-strong">{{ bl.name }}</td>
              <td>
                <span v-if="bl.source.type === 'url'" class="badge-accent" :title="bl.source.url">
                  URL{{ bl.source.format ? ` (${bl.source.format})` : '' }}
                </span>
                <span v-else class="badge">Inline</span>
              </td>
              <td class="text-right font-mono text-xs">{{ bl.domain_count.toLocaleString() }}</td>
              <td class="text-fg-muted text-xs">{{ formatRelative(bl.last_updated) }}</td>
              <td class="text-xs whitespace-nowrap">
                <span v-if="bl.source.type !== 'url'" class="text-fg-subtle">—</span>
                <div v-else class="flex items-center gap-2">
                  <!-- auto-refresh toggle -->
                  <button type="button"
                          :disabled="busyRows[bl.id]"
                          class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors flex-shrink-0"
                          :class="bl.refresh_interval_seconds ? 'bg-success' : 'bg-bg-hover border border-border'"
                          :title="bl.refresh_interval_seconds ? 'Disable auto-refresh' : 'Enable auto-refresh (24 h)'"
                          @click="toggleAutoRefresh(bl)">
                    <span class="inline-block h-4 w-4 rounded-full bg-bg-input shadow transform transition-transform"
                          :class="bl.refresh_interval_seconds ? 'translate-x-4' : 'translate-x-1'" />
                  </button>
                  <!-- interval selector (visible when on) -->
                  <template v-if="bl.refresh_interval_seconds">
                    <select class="input !py-0 !px-1 text-xs h-6 w-20"
                            :value="bl.refresh_interval_seconds"
                            @change="setRefreshInterval(bl, +($event.target as HTMLSelectElement).value)">
                      <option :value="3600">1 h</option>
                      <option :value="21600">6 h</option>
                      <option :value="43200">12 h</option>
                      <option :value="86400">24 h</option>
                      <option :value="604800">7 d</option>
                    </select>
                    <span v-if="bl.last_refresh_status"
                          class="chip"
                          :class="refreshChipClass(bl)"
                          :title="bl.last_refresh_error || ''">
                      {{ bl.last_refresh_status }}
                    </span>
                  </template>
                  <span v-else class="text-fg-subtle">off</span>
                </div>
              </td>
              <td class="text-right whitespace-nowrap">
                <button v-if="bl.source.type === 'url'"
                        class="btn-ghost"
                        :disabled="busyRows[bl.id]"
                        title="Refresh from source"
                        @click="refreshRow(bl)">
                  <ArrowPathIcon class="h-4 w-4" :class="{ 'animate-spin': busyRows[bl.id] }" />
                </button>
                <button class="btn-ghost text-danger"
                        :disabled="busyRows[bl.id]"
                        title="Delete blocklist"
                        @click="askDelete(bl)">
                  <TrashIcon class="h-4 w-4" />
                </button>
              </td>
            </tr>
            <tr v-if="rowErrors[bl.id]">
              <td colspan="8" class="!py-1 text-xs text-danger">{{ rowErrors[bl.id] }}</td>
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
        <h2 class="text-base font-semibold text-fg-strong">New blocklist</h2>

        <p v-if="formError" class="text-sm text-danger">{{ formError }}</p>

        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="label" for="bl-id">ID <span class="text-fg-subtle font-normal">(optional)</span></label>
            <input id="bl-id" v-model="form.id" class="input" placeholder="auto-generated" />
          </div>
          <div>
            <label class="label" for="bl-name">Name</label>
            <input id="bl-name" v-model="form.name" class="input" required placeholder="e.g. StevenBlack hosts" />
          </div>
        </div>

        <div>
          <span class="label">Source</span>
          <div class="flex gap-4 text-sm">
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="url" v-model="form.sourceType" /> From URL
            </label>
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="inline" v-model="form.sourceType" /> Manual entries
            </label>
          </div>
        </div>

        <div v-if="form.sourceType === 'url'" class="grid grid-cols-3 gap-3">
          <div class="col-span-2">
            <label class="label" for="bl-url">URL</label>
            <input id="bl-url" v-model="form.url" class="input"
                   placeholder="https://example.com/hosts.txt" required type="url" />
          </div>
          <div>
            <label class="label" for="bl-format">Format</label>
            <select id="bl-format" v-model="form.format" class="input">
              <option value="auto">auto</option>
              <option value="hosts">hosts</option>
              <option value="domainlist">domainlist</option>
              <option value="askoed">askoed</option>
            </select>
          </div>
        </div>

        <div v-if="form.sourceType === 'url'">
          <label class="label" for="bl-refresh">Auto-refresh interval (seconds)</label>
          <input id="bl-refresh" v-model.number="form.refreshIntervalSeconds" type="number" min="0"
                 class="input w-32" placeholder="0" />
          <p class="text-xs text-fg-muted mt-1">
            0 = manual only. Typical: 86400 (24 h). Leader-only fetches.
          </p>
        </div>

        <div v-else>
          <label class="label" for="bl-domains">Domains <span class="text-fg-subtle font-normal">(one per line)</span></label>
          <textarea id="bl-domains" v-model="form.domainsText" rows="6"
                    class="input font-mono text-xs"
                    placeholder="ads.example.com&#10;tracker.example.net" />
        </div>

        <div class="grid grid-cols-2 gap-3 items-end">
          <div>
            <label class="label" for="bl-policy">Block policy override</label>
            <select id="bl-policy" v-model="form.blockPolicy" class="input">
              <option value="">inherit</option>
              <option value="nxdomain">nxdomain</option>
              <option value="null">null</option>
              <option value="nodata">nodata</option>
            </select>
          </div>
          <label class="inline-flex items-center gap-2 text-sm pb-2">
            <input type="checkbox" v-model="form.enabled" /> Enabled
          </label>
        </div>

        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn-secondary" @click="closeCreate">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="submitting">
            {{ submitting ? 'Creating…' : 'Create blocklist' }}
          </button>
        </div>
      </form>
    </div>

    <!-- Pause filtering modal -->
    <div v-if="showPauseModal"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="showPauseModal = false">
      <div class="card max-w-sm w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Pause filtering</h2>

        <p v-if="pauseError" class="text-sm text-danger">{{ pauseError }}</p>

        <div>
          <span class="label">Duration</span>
          <div class="grid grid-cols-4 gap-2">
            <button v-for="p in PAUSE_PRESETS" :key="p.seconds"
                    type="button"
                    class="btn-secondary text-xs"
                    :class="pauseCustomMinutes === null && pauseSelectedPreset === p.seconds
                            ? 'border-accent !text-accent' : ''"
                    @click="pauseSelectedPreset = p.seconds; pauseCustomMinutes = null">
              {{ p.label }}
            </button>
          </div>
          <div class="flex items-center gap-2 mt-2">
            <input type="number" min="1" v-model.number="pauseCustomMinutes"
                   class="input w-24" placeholder="Custom"
                   :class="pauseCustomMinutes !== null ? 'border-accent ring-1 ring-accent' : ''"
                   @input="pauseCustomMinutes = ($event.target as HTMLInputElement).valueAsNumber || null" />
            <span class="text-sm text-fg-muted">minutes</span>
          </div>
        </div>

        <div>
          <label class="label" for="pause-reason">Reason <span class="text-fg-subtle font-normal">(optional)</span></label>
          <input id="pause-reason" v-model="pauseReason" class="input"
                 placeholder="e.g. Updating software" />
        </div>

        <!-- Profile scope selector -->
        <div v-if="pauseProfiles.length > 0">
          <span class="label">Apply to</span>
          <div class="space-y-1 mt-1">
            <label class="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox"
                     :checked="pauseSelectedProfileIds.length === 0"
                     @change="pauseSelectedProfileIds = []" />
              <span class="font-medium">All profiles</span>
            </label>
            <label v-for="prof in pauseProfiles" :key="prof.id"
                   class="flex items-center gap-2 text-sm cursor-pointer pl-1">
              <input type="checkbox"
                     :value="prof.id"
                     v-model="pauseSelectedProfileIds" />
              <span>{{ prof.name }}</span>
              <span class="text-fg-subtle text-xs font-mono">{{ prof.id }}</span>
            </label>
          </div>
        </div>

        <div class="flex justify-end gap-2 pt-2">
          <button class="btn-secondary" @click="showPauseModal = false">Cancel</button>
          <button class="btn-primary" :disabled="pausingGlobal" @click="activatePause">
            {{ pausingGlobal ? 'Pausing…' : 'Pause filtering' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Delete confirmation modal -->
    <div v-if="pendingDelete"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="pendingDelete = null">
      <div class="card max-w-lg w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Delete blocklist?</h2>
        <p class="text-sm text-fg">
          Delete blocklist <span class="font-semibold">{{ pendingDelete.name }}</span>?
          <span class="text-fg-muted">
            {{ pendingDelete.domain_count.toLocaleString() }} domains will no longer be blocked.
          </span>
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
  ArrowPathIcon, ClockIcon, PlayCircleIcon, PlusIcon, TrashIcon,
} from '@heroicons/vue/24/outline'
import {
  clearGlobalPause, createBlocklist, deleteBlocklist, getGlobalPause,
  listBlocklists, listProfiles, refreshBlocklist, setGlobalPause, updateBlocklist,
  type CreateBlocklistInput,
} from '@/api/endpoints'
import type { Blocklist, PauseState, Profile } from '@/api/types'

// ─── State ───────────────────────────────────────────────────────────────

const blocklists = ref<Blocklist[]>([])
const loading = ref(true)
const lastError = ref('')
const rowErrors = reactive<Record<string, string>>({})
const busyRows = reactive<Record<string, boolean>>({})

const showCreate = ref(false)
const submitting = ref(false)
const formError = ref('')

interface FormState {
  id: string
  name: string
  enabled: boolean
  sourceType: 'url' | 'inline'
  url: string
  format: string
  domainsText: string
  blockPolicy: string
  refreshIntervalSeconds: number
}

const emptyForm = (): FormState => ({
  id: '', name: '', enabled: true,
  sourceType: 'url', url: '', format: 'auto',
  domainsText: '', blockPolicy: '',
  refreshIntervalSeconds: 0,
})
const form = reactive<FormState>(emptyForm())

const pendingDelete = ref<Blocklist | null>(null)
const deleting = ref(false)

// ─── Filtering pause ─────────────────────────────────────────────────────

const PAUSE_PRESETS = [
  { label: '15 min', seconds: 15 * 60 },
  { label: '30 min', seconds: 30 * 60 },
  { label: '1 hour', seconds: 3600 },
  { label: '2 hours', seconds: 7200 },
]

const globalPause = ref<PauseState | null>(null)
const showPauseModal = ref(false)
const pauseSelectedPreset = ref(3600)
const pauseCustomMinutes = ref<number | null>(null)
const pauseReason = ref('')
const pauseSelectedProfileIds = ref<string[]>([])
const pauseProfiles = ref<Profile[]>([])
const pausingGlobal = ref(false)
const resumingGlobal = ref(false)
const pauseError = ref('')
const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null

const pauseActive = computed(() => {
  const p = globalPause.value
  if (!p?.active || !p.resumes_at) return false
  return new Date(p.resumes_at).getTime() > now.value
})

const pauseRemainingMs = computed(() => {
  if (!globalPause.value?.resumes_at) return 0
  return Math.max(0, new Date(globalPause.value.resumes_at).getTime() - now.value)
})

const pauseDurationSeconds = computed(() =>
  pauseCustomMinutes.value != null && pauseCustomMinutes.value > 0
    ? Math.round(pauseCustomMinutes.value * 60)
    : pauseSelectedPreset.value,
)

function formatRemaining(ms: number): string {
  const secs = Math.ceil(ms / 1000)
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  const s = secs % 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

function openPauseModal() {
  pauseSelectedPreset.value = 3600
  pauseCustomMinutes.value = null
  pauseReason.value = ''
  pauseError.value = ''
  pauseSelectedProfileIds.value = []
  showPauseModal.value = true
}

async function activatePause() {
  pauseError.value = ''
  const secs = pauseDurationSeconds.value
  if (!secs || secs < 1) {
    pauseError.value = 'Enter a valid duration.'
    return
  }
  pausingGlobal.value = true
  try {
    globalPause.value = await setGlobalPause(
      secs,
      pauseReason.value || undefined,
      pauseSelectedProfileIds.value.length ? pauseSelectedProfileIds.value : undefined,
    )
    showPauseModal.value = false
  } catch (err) {
    pauseError.value = errMsg(err, 'Failed to pause filtering')
  } finally {
    pausingGlobal.value = false
  }
}

async function resumeFiltering() {
  resumingGlobal.value = true
  try {
    await clearGlobalPause()
    globalPause.value = await getGlobalPause()
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to resume filtering')
  } finally {
    resumingGlobal.value = false
  }
}

async function loadGlobalPause() {
  try {
    globalPause.value = await getGlobalPause()
  } catch {
    // non-critical — degrade gracefully
  }
}

async function loadProfiles() {
  try {
    pauseProfiles.value = await listProfiles()
  } catch {
    // non-critical
  }
}

function profileName(id: string): string {
  return pauseProfiles.value.find(p => p.id === id)?.name ?? id
}

// ─── Data loading ────────────────────────────────────────────────────────

async function refresh() {
  try {
    blocklists.value = await listBlocklists()
    lastError.value = ''
    // Clear stale row errors — on a successful list load they are no longer relevant
    // (e.g. errors left over from a service restart or transient failure).
    Object.keys(rowErrors).forEach(k => delete rowErrors[k])
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load blocklists')
  } finally {
    loading.value = false
  }
}

// ─── Row actions ─────────────────────────────────────────────────────────

async function toggleEnabled(bl: Blocklist) {
  const prev = bl.enabled
  bl.enabled = !prev // optimistic
  busyRows[bl.id] = true
  rowErrors[bl.id] = ''
  try {
    const updated = await updateBlocklist(bl.id, { enabled: bl.enabled })
    Object.assign(bl, updated)
  } catch (err) {
    bl.enabled = prev // rollback
    rowErrors[bl.id] = errMsg(err, 'Failed to update')
  } finally {
    busyRows[bl.id] = false
  }
}

async function toggleAutoRefresh(bl: Blocklist) {
  const next = bl.refresh_interval_seconds ? 0 : 86400
  await setRefreshInterval(bl, next)
}

async function setRefreshInterval(bl: Blocklist, seconds: number) {
  busyRows[bl.id] = true
  rowErrors[bl.id] = ''
  try {
    const updated = await updateBlocklist(bl.id, { refresh_interval_seconds: seconds })
    Object.assign(bl, updated)
  } catch (err) {
    rowErrors[bl.id] = errMsg(err, 'Failed to update')
  } finally {
    busyRows[bl.id] = false
  }
}

async function refreshRow(bl: Blocklist) {
  busyRows[bl.id] = true
  rowErrors[bl.id] = ''
  try {
    const updated = await refreshBlocklist(bl.id)
    Object.assign(bl, updated)
  } catch (err) {
    rowErrors[bl.id] = errMsg(err, 'Refresh failed')
  } finally {
    busyRows[bl.id] = false
  }
}

function askDelete(bl: Blocklist) { pendingDelete.value = bl }

async function confirmDelete() {
  const bl = pendingDelete.value
  if (!bl) return
  deleting.value = true
  try {
    await deleteBlocklist(bl.id)
    blocklists.value = blocklists.value.filter(b => b.id !== bl.id)
    pendingDelete.value = null
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to delete blocklist')
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
  if (!form.name.trim()) {
    formError.value = 'Name is required.'
    return
  }

  const input: CreateBlocklistInput = {
    name: form.name.trim(),
    enabled: form.enabled,
    source: form.sourceType === 'url'
      ? { type: 'url', url: form.url.trim(), format: form.format }
      : { type: 'inline' },
  }
  if (form.id.trim()) input.id = form.id.trim()
  if (form.blockPolicy) input.block_policy = form.blockPolicy
  if (form.sourceType === 'url' && form.refreshIntervalSeconds > 0) {
    input.refresh_interval_seconds = form.refreshIntervalSeconds
  }

  if (form.sourceType === 'inline') {
    const domains = form.domainsText
      .split(/\s+/)
      .map(d => d.trim())
      .filter(Boolean)
    input.domains = domains
  }

  submitting.value = true
  try {
    const created = await createBlocklist(input)
    blocklists.value = [created, ...blocklists.value]
    showCreate.value = false
  } catch (err) {
    formError.value = errMsg(err, 'Failed to create blocklist')
  } finally {
    submitting.value = false
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

function formatInterval(secs: number): string {
  if (!secs) return ''
  if (secs < 60) return `${secs}s`
  const mins = Math.round(secs / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.round(mins / 60)
  if (hours < 48) return `${hours}h`
  return `${Math.round(hours / 24)}d`
}

function refreshChipClass(bl: { last_refresh_status?: string }): string {
  switch (bl.last_refresh_status) {
    case 'ok':        return 'chip-success'
    case 'unchanged': return 'chip-neutral'
    case 'error':     return 'chip-danger'
    default:          return 'chip-neutral'
  }
}

// ─── Keyboard: close modals on Escape ────────────────────────────────────

function onKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (showPauseModal.value) { showPauseModal.value = false; return }
  if (pendingDelete.value) { pendingDelete.value = null; return }
  if (showCreate.value && !submitting.value) { showCreate.value = false }
}

onMounted(() => {
  refresh()
  loadGlobalPause()
  loadProfiles()
  ticker = setInterval(() => { now.value = Date.now() }, 1000)
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  if (ticker) clearInterval(ticker)
  window.removeEventListener('keydown', onKey)
})
</script>
