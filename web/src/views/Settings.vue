<template>
  <div class="space-y-6">
    <!-- Page header -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">Settings</h1>
    </div>

    <!-- Global error banner -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <p v-if="loading" class="card p-5 text-sm text-fg-muted text-center">Loading settings…</p>

    <template v-else-if="settings">
      <!-- ─── DNS section ──────────────────────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <GlobeAltIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">DNS</h2>
        </header>

        <p v-if="dnsError" class="text-sm text-danger">{{ dnsError }}</p>

        <div>
          <span class="label">Mode</span>
          <div class="flex gap-4 text-sm">
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="forwarding" v-model="dnsForm.mode" @change="markDnsDirty" />
              Forwarding
            </label>
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="recursive" v-model="dnsForm.mode" @change="markDnsDirty" />
              Recursive
            </label>
          </div>
          <p class="text-xs text-fg-muted mt-1">
            <span v-if="dnsForm.mode === 'forwarding'">Queries are forwarded to upstream resolvers.</span>
            <span v-else>Queries are resolved recursively from the root servers.</span>
          </p>
        </div>

        <div v-if="dnsForm.mode === 'forwarding'">
          <label class="label" for="dns-upstreams">
            Upstream resolvers
            <span class="text-fg-subtle font-normal">(one per line, host:port)</span>
          </label>
          <textarea id="dns-upstreams"
                    v-model="dnsForm.upstreamsText"
                    rows="4"
                    class="input font-mono text-xs"
                    placeholder="9.9.9.9:53&#10;1.1.1.1:53"
                    @input="markDnsDirty" />
        </div>

        <div v-else>
          <label class="label" for="dns-trusted">
            Trusted subnets
            <span class="text-fg-subtle font-normal">(one CIDR per line)</span>
          </label>
          <textarea id="dns-trusted"
                    v-model="dnsForm.trustedText"
                    rows="4"
                    class="input font-mono text-xs"
                    placeholder="192.168.0.0/16&#10;10.0.0.0/8"
                    @input="markDnsDirty" />
        </div>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label class="label" for="dns-timeout">Upstream timeout (seconds)</label>
            <input id="dns-timeout"
                   v-model.number="dnsForm.timeout"
                   type="number" min="1" max="30"
                   class="input"
                   @input="markDnsDirty" />
          </div>
          <div>
            <div class="flex items-center justify-between">
              <label class="label !mb-0" for="dns-cache-max">Cache max entries</label>
              <label class="inline-flex items-center gap-1.5 text-xs text-fg-muted">
                <input type="checkbox"
                       v-model="dnsForm.cacheEnabled"
                       @change="markDnsDirty" />
                Enable DNS cache
              </label>
            </div>
            <input id="dns-cache-max"
                   v-model.number="dnsForm.cacheMax"
                   type="number" min="0"
                   class="input mt-1"
                   :disabled="!dnsForm.cacheEnabled"
                   @input="markDnsDirty" />
          </div>
        </div>

        <!-- M4.7 — DNS cache stats + purge button -->
        <div class="border-t border-border pt-3 space-y-2" v-if="dnsForm.cacheEnabled">
          <div class="flex items-center justify-between">
            <span class="label !mb-0">DNS cache</span>
            <button class="btn-secondary text-xs"
                    :disabled="cachePurging"
                    @click="onPurgeCache">
              {{ cachePurging ? 'Purging…' : 'Clear DNS cache' }}
            </button>
          </div>
          <div v-if="cacheStats" class="grid grid-cols-2 sm:grid-cols-5 gap-2 text-xs text-fg-muted">
            <span>size <b class="text-fg-strong">{{ cacheStats.size }}</b> / {{ cacheStats.max_entries }}</span>
            <span>hits <b class="text-fg-strong">{{ cacheStats.hits }}</b></span>
            <span>misses <b class="text-fg-strong">{{ cacheStats.misses }}</b></span>
            <span>evictions <b class="text-fg-strong">{{ cacheStats.evictions }}</b></span>
            <span v-if="cachePurgedAt" class="text-success">Purged {{ cachePurgedCount }}.</span>
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-1">
          <span v-if="dnsSavedAt" class="text-xs text-success">Saved.</span>
          <button class="btn-primary"
                  :disabled="!dnsDirty || dnsSaving"
                  @click="saveDns">
            {{ dnsSaving ? 'Saving…' : 'Save DNS settings' }}
          </button>
        </div>
      </section>

      <!-- ─── Filtering section ────────────────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <ShieldCheckIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Filtering</h2>
        </header>

        <p v-if="filteringError" class="text-sm text-danger">{{ filteringError }}</p>

        <div>
          <span class="label">Default block policy</span>
          <div class="space-y-2 text-sm">
            <label class="flex items-start gap-2">
              <input type="radio" value="nxdomain"
                     v-model="filteringForm.policy"
                     class="mt-1"
                     @change="markFilteringDirty" />
              <span>
                <span class="font-medium text-fg-strong">NXDOMAIN</span>
                <span class="block text-xs text-fg-muted">
                  Pretend the domain doesn't exist (recommended)
                </span>
              </span>
            </label>
            <label class="flex items-start gap-2">
              <input type="radio" value="null"
                     v-model="filteringForm.policy"
                     class="mt-1"
                     @change="markFilteringDirty" />
              <span>
                <span class="font-medium text-fg-strong">NULL</span>
                <span class="block text-xs text-fg-muted">Return 0.0.0.0 / ::</span>
              </span>
            </label>
            <label class="flex items-start gap-2">
              <input type="radio" value="nodata"
                     v-model="filteringForm.policy"
                     class="mt-1"
                     @change="markFilteringDirty" />
              <span>
                <span class="font-medium text-fg-strong">NODATA</span>
                <span class="block text-xs text-fg-muted">Return empty success</span>
              </span>
            </label>
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-1">
          <span v-if="filteringSavedAt" class="text-xs text-success">Saved.</span>
          <button class="btn-primary"
                  :disabled="!filteringDirty || filteringSaving"
                  @click="saveFiltering">
            {{ filteringSaving ? 'Saving…' : 'Save filtering' }}
          </button>
        </div>
      </section>

      <!-- ─── Query log section ────────────────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <ClipboardDocumentListIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Query log</h2>
        </header>

        <p v-if="queryLogError" class="text-sm text-danger">{{ queryLogError }}</p>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label class="label" for="ql-max">Max entries per node</label>
            <input id="ql-max"
                   v-model.number="queryLogForm.maxEntries"
                   type="number" min="100"
                   class="input"
                   @input="markQueryLogDirty" />
            <p class="text-xs text-fg-muted mt-1">
              Older entries are evicted on each node when this limit is reached.
            </p>
          </div>
          <div>
            <label class="label" for="ql-retention">Aggregate retention (days)</label>
            <input id="ql-retention"
                   v-model.number="queryLogForm.retentionDays"
                   type="number" min="1" max="365"
                   class="input"
                   @input="markQueryLogDirty" />
            <p class="text-xs text-fg-muted mt-1">
              Hourly aggregates used by the Dashboard are kept for this long.
            </p>
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-1">
          <span v-if="queryLogSavedAt" class="text-xs text-success">Saved.</span>
          <button class="btn-primary"
                  :disabled="!queryLogDirty || queryLogSaving"
                  @click="saveQueryLog">
            {{ queryLogSaving ? 'Saving…' : 'Save query log' }}
          </button>
        </div>
      </section>

      <!-- ─── Audit log link (M5.2) ─────────────────────────────────────── -->
      <section class="card p-5">
        <header class="flex items-center gap-2">
          <DocumentTextIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Audit log</h2>
        </header>
        <p class="text-sm text-fg-muted mt-1">
          Every state-changing API call is recorded with actor, action, target, and result.
          Replicated through Raft &mdash; identical on every node.
        </p>
        <div class="mt-3">
          <router-link :to="{ name: 'audit' }" class="btn-secondary">
            Open audit log
          </router-link>
        </div>
      </section>

      <!-- ─── Configuration backup ──────────────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <ArchiveBoxIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Configuration backup</h2>
        </header>

        <p class="text-sm text-fg-muted">
          Download the full configuration as a portable archive, or restore a previously
          downloaded backup. Admin credentials are <strong>not</strong> included in the
          backup and are never changed by a restore.
        </p>

        <!-- Download -->
        <div class="flex flex-wrap items-center gap-3 pt-1">
          <button class="btn-secondary" :disabled="exporting" @click="downloadBackup">
            {{ exporting ? 'Preparing…' : 'Download backup' }}
          </button>
          <span v-if="exportError" class="text-xs text-danger">{{ exportError }}</span>
          <span v-else class="text-xs text-fg-muted">
            Downloads <code class="font-mono">skoed-config.tar.gz</code>
          </span>
        </div>

        <!-- Restore -->
        <div class="border-t border-border pt-4 space-y-3">
          <h3 class="text-sm font-semibold text-fg-strong">Restore backup</h3>

          <p v-if="backupError" class="text-sm text-danger">{{ backupError }}</p>
          <p v-if="backupSuccess" class="text-sm text-success">{{ backupSuccess }}</p>

          <div class="flex flex-wrap items-center gap-3">
            <label class="btn-secondary cursor-pointer">
              <input type="file" accept=".tar.gz,.tgz" class="sr-only"
                     :disabled="restoring"
                     @change="onBackupFileSelected" />
              {{ selectedFile ? selectedFile.name : 'Choose archive…' }}
            </label>
            <button class="btn-danger"
                    :disabled="!selectedFile || restoring"
                    @click="restoreConfirm = true">
              {{ restoring ? 'Restoring…' : 'Restore' }}
            </button>
          </div>
        </div>
      </section>
    </template>
  </div>

  <!-- ─── Restore confirmation modal ──────────────────────────────────────── -->
  <div v-if="restoreConfirm"
       class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
       @click.self="restoreConfirm = false">
    <div class="card max-w-lg w-full p-6 space-y-4">
      <h2 class="text-base font-semibold text-fg-strong">Restore configuration?</h2>
      <p class="text-sm text-fg">
        This will replace the current configuration with the contents of
        <span class="font-mono text-xs text-fg-strong">{{ selectedFile?.name }}</span>.
        <span class="text-fg-muted">Your admin credentials will not be changed.</span>
      </p>
      <p v-if="backupError" class="text-sm text-danger">{{ backupError }}</p>
      <div class="flex justify-end gap-2">
        <button class="btn-secondary" :disabled="restoring" @click="restoreConfirm = false">
          Cancel
        </button>
        <button class="btn-danger" :disabled="restoring" @click="doRestore">
          {{ restoring ? 'Restoring…' : 'Yes, restore' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import {
  ArchiveBoxIcon, ClipboardDocumentListIcon, DocumentTextIcon, GlobeAltIcon, ShieldCheckIcon,
} from '@heroicons/vue/24/outline'
import {
  getDNSCacheStats, getSettings, patchSettings, purgeDNSCache,
} from '@/api/endpoints'
import { api, getToken } from '@/api/client'
import type { DNSCacheStats, DNSConfig, Settings } from '@/api/types'

// ─── State ─────────────────────────────────────────────────────────────────

const settings = ref<Settings | null>(null)
const loading = ref(true)
const lastError = ref('')

// Per-section forms mirror the loaded Settings; dirty flags enable Save buttons.
interface DnsForm {
  mode: 'forwarding' | 'recursive'
  upstreamsText: string
  trustedText: string
  timeout: number
  cacheEnabled: boolean
  cacheMax: number
}

interface FilteringForm {
  policy: 'nxdomain' | 'null' | 'nodata'
}

interface QueryLogForm {
  maxEntries: number
  retentionDays: number
}

const dnsForm = reactive<DnsForm>({
  mode: 'forwarding', upstreamsText: '', trustedText: '',
  timeout: 5, cacheEnabled: true, cacheMax: 10000,
})
const filteringForm = reactive<FilteringForm>({ policy: 'nxdomain' })
const queryLogForm = reactive<QueryLogForm>({ maxEntries: 10000, retentionDays: 30 })

const dnsDirty = ref(false)
const filteringDirty = ref(false)
const queryLogDirty = ref(false)

const dnsSaving = ref(false)
const filteringSaving = ref(false)
const queryLogSaving = ref(false)

const dnsError = ref('')
const filteringError = ref('')
const queryLogError = ref('')

const dnsSavedAt = ref(0)
const filteringSavedAt = ref(0)
const queryLogSavedAt = ref(0)

// M4.7 — DNS cache controls
const cacheStats = ref<DNSCacheStats | null>(null)
const cachePurging = ref(false)
const cachePurgedAt = ref(0)
const cachePurgedCount = ref(0)

async function refreshCacheStats() {
  try {
    cacheStats.value = await getDNSCacheStats()
  } catch { /* leave previous snapshot in place */ }
}

async function onPurgeCache() {
  cachePurging.value = true
  try {
    const out = await purgeDNSCache()
    cachePurgedCount.value = out.purged
    cachePurgedAt.value = Date.now()
    await refreshCacheStats()
    window.setTimeout(() => { cachePurgedAt.value = 0 }, 4000)
  } catch (err) {
    dnsError.value = errMsg(err, 'Failed to purge DNS cache')
  } finally {
    cachePurging.value = false
  }
}

// ─── Loading ───────────────────────────────────────────────────────────────

onMounted(async () => {
  try {
    const s = await getSettings()
    applySettings(s)
    await refreshCacheStats()
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load settings')
  } finally {
    loading.value = false
  }
})

function applySettings(s: Settings) {
  settings.value = s

  // DNS form
  dnsForm.mode = s.dns.mode
  dnsForm.upstreamsText = (s.dns.upstream_resolvers ?? []).join('\n')
  dnsForm.trustedText = (s.dns.trusted_subnets ?? []).join('\n')
  dnsForm.timeout = s.dns.upstream_timeout_seconds
  dnsForm.cacheEnabled = s.dns.cache.enabled
  dnsForm.cacheMax = s.dns.cache.max_entries
  dnsDirty.value = false

  // Filtering form
  filteringForm.policy = s.filtering.block_policy
  filteringDirty.value = false

  // Query log form
  queryLogForm.maxEntries = s.query_log.max_entries
  queryLogForm.retentionDays = s.query_log.aggregate_retention_days
  queryLogDirty.value = false
}

// ─── Dirty tracking ────────────────────────────────────────────────────────

function markDnsDirty() { dnsDirty.value = true }
function markFilteringDirty() { filteringDirty.value = true }
function markQueryLogDirty() { queryLogDirty.value = true }

// ─── Saves ─────────────────────────────────────────────────────────────────

async function saveDns() {
  if (!settings.value) return
  dnsError.value = ''
  dnsSaving.value = true
  try {
    const current = settings.value.dns
    const dns: DNSConfig = {
      // listen.* is node-local (node.yaml) and not editable via the API,
      // but the PATCH contract still expects a full DNSConfig — preserve it.
      listen: { ...current.listen },
      mode: dnsForm.mode,
      upstream_timeout_seconds: dnsForm.timeout,
      cache: {
        enabled: dnsForm.cacheEnabled,
        max_entries: dnsForm.cacheMax,
      },
    }
    if (dnsForm.mode === 'forwarding') {
      dns.upstream_resolvers = parseLines(dnsForm.upstreamsText)
    } else {
      dns.trusted_subnets = parseLines(dnsForm.trustedText)
    }
    const updated = await patchSettings({ dns })
    applySettings(updated)
    flashSaved(dnsSavedAt)
  } catch (err) {
    dnsError.value = errMsg(err, 'Failed to save DNS settings')
  } finally {
    dnsSaving.value = false
  }
}

async function saveFiltering() {
  filteringError.value = ''
  filteringSaving.value = true
  try {
    const updated = await patchSettings({
      filtering: { block_policy: filteringForm.policy },
    })
    applySettings(updated)
    flashSaved(filteringSavedAt)
  } catch (err) {
    filteringError.value = errMsg(err, 'Failed to save filtering settings')
  } finally {
    filteringSaving.value = false
  }
}

async function saveQueryLog() {
  queryLogError.value = ''
  queryLogSaving.value = true
  try {
    const updated = await patchSettings({
      query_log: {
        max_entries: queryLogForm.maxEntries,
        aggregate_retention_days: queryLogForm.retentionDays,
      },
    })
    applySettings(updated)
    flashSaved(queryLogSavedAt)
  } catch (err) {
    queryLogError.value = errMsg(err, 'Failed to save query log settings')
  } finally {
    queryLogSaving.value = false
  }
}

// ─── Configuration backup ──────────────────────────────────────────────────

const exporting = ref(false)
const exportError = ref('')

async function downloadBackup() {
  exporting.value = true
  exportError.value = ''
  try {
    const res = await api.get('/api/v1/config/export', { responseType: 'blob' })
    const blob = new Blob([res.data as BlobPart], { type: 'application/gzip' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.style.cssText = 'position:fixed;top:-100px;left:-100px'
    a.href = url
    a.download = 'skoed-config.tar.gz'
    document.body.appendChild(a)
    a.click()
    setTimeout(() => { URL.revokeObjectURL(url); a.remove() }, 1000)
  } catch (err) {
    exportError.value = errMsg(err, 'Export failed — check your session and try again.')
  } finally {
    exporting.value = false
  }
}

const selectedFile = ref<File | null>(null)
const restoring = ref(false)
const restoreConfirm = ref(false)
const backupError = ref('')
const backupSuccess = ref('')

function onBackupFileSelected(e: Event) {
  const input = e.target as HTMLInputElement
  selectedFile.value = input.files?.[0] ?? null
  backupError.value = ''
  backupSuccess.value = ''
}

async function doRestore() {
  if (!selectedFile.value) return
  backupError.value = ''
  backupSuccess.value = ''
  restoring.value = true
  restoreConfirm.value = false
  try {
    const token = getToken()
    const headers: Record<string, string> = {}
    if (token) headers.Authorization = `Bearer ${token}`
    const form = new FormData()
    form.append('archive', selectedFile.value, selectedFile.value.name)
    const resp = await fetch('/api/v1/config/import', { method: 'POST', headers, body: form })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${resp.status}`)
    }
    backupSuccess.value = 'Configuration restored. Refresh the page to see the updated settings.'
    selectedFile.value = null
  } catch (err) {
    backupError.value = errMsg(err, 'Restore failed')
  } finally {
    restoring.value = false
  }
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function parseLines(text: string): string[] {
  return text.split(/\r?\n/).map(l => l.trim()).filter(Boolean)
}

function flashSaved(slot: { value: number }) {
  const token = Date.now()
  slot.value = token
  window.setTimeout(() => {
    if (slot.value === token) slot.value = 0
  }, 2000)
}

function errMsg(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }, message?: string }
  return e?.response?.data?.error || e?.message || fallback
}
</script>
