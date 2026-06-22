<template>
  <div class="space-y-6">
    <!-- Page header with enable/disable toggle -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">DHCP Server</h1>
      <label class="inline-flex items-center gap-2 text-sm">
        <span class="text-fg-muted">{{ dhcpForm.enabled ? 'Enabled' : 'Disabled' }}</span>
        <button
          role="switch"
          :aria-checked="dhcpForm.enabled"
          class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors"
          :class="dhcpForm.enabled ? 'bg-accent' : 'bg-border'"
          :disabled="dhcpToggling"
          @click="toggleDhcp">
          <span
            class="inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform"
            :class="dhcpForm.enabled ? 'translate-x-4' : 'translate-x-1'" />
        </button>
      </label>
    </div>

    <!-- Global error / saved banners -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>
    <p v-if="savedAt" class="text-sm text-success">Saved.</p>

    <!-- ─── Pool configuration ──────────────────────────────────────────── -->
    <section class="card p-5 space-y-4">
      <header class="flex items-center gap-2">
        <ServerIcon class="h-5 w-5 text-accent" />
        <h2 class="text-base font-semibold text-fg-strong">Pool configuration</h2>
      </header>

      <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label class="label" for="dhcp-pool-start">Pool start</label>
          <input id="dhcp-pool-start" v-model="dhcpForm.pool_start" type="text"
                 class="input font-mono text-sm" placeholder="10.0.0.100"
                 @input="dhcpDirty = true" />
        </div>
        <div>
          <label class="label" for="dhcp-pool-end">Pool end</label>
          <input id="dhcp-pool-end" v-model="dhcpForm.pool_end" type="text"
                 class="input font-mono text-sm" placeholder="10.0.0.200"
                 @input="dhcpDirty = true" />
        </div>
        <div>
          <label class="label" for="dhcp-gateway">Gateway</label>
          <input id="dhcp-gateway" v-model="dhcpForm.gateway" type="text"
                 class="input font-mono text-sm" placeholder="10.0.0.1"
                 @input="dhcpDirty = true" />
        </div>
        <div>
          <label class="label" for="dhcp-lease-time">Lease time (seconds)</label>
          <input id="dhcp-lease-time" v-model.number="dhcpForm.lease_time_seconds"
                 type="number" min="60" class="input"
                 @input="dhcpDirty = true" />
        </div>
        <div>
          <label class="label" for="dhcp-domain">Domain <span class="text-fg-subtle font-normal">(optional)</span></label>
          <input id="dhcp-domain" v-model="dhcpForm.domain" type="text"
                 class="input font-mono text-sm" placeholder="home.arpa"
                 @input="dhcpDirty = true" />
        </div>
        <div>
          <label class="label" for="dhcp-dns-server">DNS server override <span class="text-fg-subtle font-normal">(optional)</span></label>
          <input id="dhcp-dns-server" v-model="dhcpForm.dns_server" type="text"
                 class="input font-mono text-sm" placeholder="leave blank to use this node"
                 @input="dhcpDirty = true" />
        </div>
      </div>

      <div class="flex items-center justify-end pt-1">
        <button class="btn-primary"
                :disabled="!dhcpDirty || dhcpSaving"
                @click="saveDhcpPool">
          {{ dhcpSaving ? 'Saving…' : 'Save DHCP settings' }}
        </button>
      </div>
    </section>

    <!-- ─── Pool utilisation ────────────────────────────────────────────── -->
    <section v-if="dhcpStatus && dhcpStatus.pool_total > 0" class="card p-5 space-y-2">
      <span class="label !mb-0">Pool utilisation</span>
      <div class="flex items-center gap-3">
        <div class="flex-1 h-2 rounded-full bg-border overflow-hidden">
          <div class="h-full rounded-full bg-accent transition-all"
               :style="{ width: dhcpUtilPct + '%' }" />
        </div>
        <span class="text-xs text-fg-muted whitespace-nowrap">
          {{ dhcpStatus.leases_active }} / {{ dhcpStatus.pool_total }}
        </span>
      </div>
    </section>

    <!-- ─── Static assignments ──────────────────────────────────────────── -->
    <section class="card p-5 space-y-3">
      <div class="flex items-center justify-between">
        <header class="flex items-center gap-2">
          <h2 class="text-base font-semibold text-fg-strong">Static assignments</h2>
        </header>
        <button class="btn-secondary text-xs" @click="dhcpAddRow = !dhcpAddRow">
          {{ dhcpAddRow ? 'Cancel' : '+ Add' }}
        </button>
      </div>

      <!-- Add-row inline form -->
      <div v-if="dhcpAddRow" class="grid grid-cols-1 sm:grid-cols-3 gap-2 items-end">
        <div>
          <label class="label text-xs" for="sa-mac">MAC address</label>
          <input id="sa-mac" v-model="newStatic.mac" type="text"
                 class="input font-mono text-xs" placeholder="aa:bb:cc:dd:ee:ff" />
        </div>
        <div>
          <label class="label text-xs" for="sa-ip">IP address</label>
          <input id="sa-ip" v-model="newStatic.ip" type="text"
                 class="input font-mono text-xs" placeholder="10.0.0.50" />
        </div>
        <div>
          <label class="label text-xs" for="sa-hostname">Hostname <span class="text-fg-subtle font-normal">(optional)</span></label>
          <input id="sa-hostname" v-model="newStatic.hostname" type="text"
                 class="input text-xs" placeholder="mydevice" />
        </div>
        <p v-if="staticAddError" class="sm:col-span-3 text-xs text-danger">{{ staticAddError }}</p>
        <div class="sm:col-span-3 flex justify-end">
          <button class="btn-primary text-xs" :disabled="staticAdding" @click="addStaticEntry">
            {{ staticAdding ? 'Saving…' : 'Save' }}
          </button>
        </div>
      </div>

      <p v-if="staticEntries.length === 0 && !dhcpAddRow" class="text-sm text-fg-muted">
        No static assignments.
      </p>
      <table v-else-if="staticEntries.length > 0" class="w-full text-sm border-collapse">
        <thead>
          <tr class="text-left text-xs text-fg-muted border-b border-border">
            <th class="pb-1 pr-3 font-medium">MAC</th>
            <th class="pb-1 pr-3 font-medium">IP</th>
            <th class="pb-1 pr-3 font-medium">Hostname</th>
            <th class="pb-1 font-medium"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="e in staticEntries" :key="e.mac"
              class="border-b border-border last:border-0">
            <td class="py-1.5 pr-3 font-mono text-xs text-fg">{{ e.mac }}</td>
            <td class="py-1.5 pr-3 font-mono text-xs text-fg">{{ e.ip }}</td>
            <td class="py-1.5 pr-3 text-xs text-fg">{{ e.hostname || '—' }}</td>
            <td class="py-1.5 text-right">
              <button class="btn-ghost !py-0 !px-1 text-danger text-xs"
                      :disabled="deletingMAC === e.mac"
                      @click="confirmDeleteStatic(e.mac)">
                {{ deletingMAC === e.mac ? '…' : '✕' }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <!-- ─── Active leases ───────────────────────────────────────────────── -->
    <section class="card p-5 space-y-3">
      <div class="flex items-center justify-between">
        <header class="flex items-center gap-2">
          <h2 class="text-base font-semibold text-fg-strong">Active leases</h2>
        </header>
        <button class="btn-ghost text-xs" :disabled="leasesLoading" @click="refreshLeases">
          {{ leasesLoading ? 'Loading…' : '↺ Refresh' }}
        </button>
      </div>
      <p v-if="leases.length === 0" class="text-sm text-fg-muted">No active leases.</p>
      <table v-else class="w-full text-sm border-collapse">
        <thead>
          <tr class="text-left text-xs text-fg-muted border-b border-border">
            <th class="pb-1 pr-3 font-medium">IP</th>
            <th class="pb-1 pr-3 font-medium">MAC</th>
            <th class="pb-1 pr-3 font-medium">Hostname</th>
            <th class="pb-1 pr-3 font-medium">Expires</th>
            <th class="pb-1 font-medium">Origin</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in leases" :key="l.ip + l.mac"
              class="border-b border-border last:border-0">
            <td class="py-1.5 pr-3 font-mono text-xs">{{ l.ip }}</td>
            <td class="py-1.5 pr-3 font-mono text-xs">{{ l.mac }}</td>
            <td class="py-1.5 pr-3 text-xs">{{ l.hostname || '—' }}</td>
            <td class="py-1.5 pr-3 text-xs text-fg-muted">{{ formatExpiry(l.expires_at) }}</td>
            <td class="py-1.5">
              <span class="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium"
                    :class="l.origin === 'static' ? 'bg-accent/10 text-accent' : 'bg-fg-subtle/20 text-fg-muted'">
                {{ l.origin }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ServerIcon } from '@heroicons/vue/24/outline'
import {
  createDhcpStaticAssignment, deleteDhcpStaticAssignment,
  getDhcpServerStatus, listDhcpLeases, listDhcpStaticAssignments,
  putDhcpServerSettings,
} from '@/api/endpoints'
import type { DhcpLease, DhcpServerStatus, DhcpStaticAssignment } from '@/api/types'

// ─── State ─────────────────────────────────────────────────────────────────

const lastError = ref('')
const savedAt = ref(0)

interface DhcpForm {
  enabled: boolean
  pool_start: string
  pool_end: string
  gateway: string
  lease_time_seconds: number
  domain: string
  dns_server: string
}

const dhcpStatus = ref<DhcpServerStatus | null>(null)
const dhcpForm = reactive<DhcpForm>({
  enabled: false, pool_start: '', pool_end: '', gateway: '',
  lease_time_seconds: 86400, domain: '', dns_server: '',
})
const dhcpDirty = ref(false)
const dhcpSaving = ref(false)
const dhcpToggling = ref(false)

const staticEntries = ref<DhcpStaticAssignment[]>([])
const dhcpAddRow = ref(false)
const newStatic = reactive({ mac: '', ip: '', hostname: '' })
const staticAdding = ref(false)
const staticAddError = ref('')
const deletingMAC = ref('')

const leases = ref<DhcpLease[]>([])
const leasesLoading = ref(false)

let dhcpPollTimer: ReturnType<typeof setInterval> | null = null

const dhcpUtilPct = computed(() => {
  if (!dhcpStatus.value || dhcpStatus.value.pool_total === 0) return 0
  return Math.round((dhcpStatus.value.leases_active / dhcpStatus.value.pool_total) * 100)
})

// ─── Lifecycle ─────────────────────────────────────────────────────────────

onMounted(async () => {
  await Promise.all([loadDhcp(), refreshStaticEntries(), refreshLeases()])
  dhcpPollTimer = setInterval(async () => {
    await loadDhcp()
    await refreshLeases()
  }, 10000)
})

onUnmounted(() => {
  if (dhcpPollTimer !== null) clearInterval(dhcpPollTimer)
})

// ─── Data loading ──────────────────────────────────────────────────────────

async function loadDhcp() {
  try {
    const s = await getDhcpServerStatus()
    dhcpStatus.value = s
    dhcpForm.enabled = s.enabled
    dhcpForm.pool_start = s.pool_start
    dhcpForm.pool_end = s.pool_end
    dhcpForm.gateway = s.gateway
    dhcpForm.lease_time_seconds = s.lease_time_seconds || 86400
    dhcpForm.domain = s.domain
    dhcpForm.dns_server = s.dns_server
    dhcpDirty.value = false
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load DHCP status')
  }
}

async function refreshLeases() {
  leasesLoading.value = true
  try {
    leases.value = (await listDhcpLeases()) ?? []
  } catch { /* ignore */ } finally {
    leasesLoading.value = false
  }
}

async function refreshStaticEntries() {
  try {
    staticEntries.value = (await listDhcpStaticAssignments()) ?? []
  } catch { /* ignore */ }
}

// ─── Actions ───────────────────────────────────────────────────────────────

async function toggleDhcp() {
  lastError.value = ''
  dhcpToggling.value = true
  const next = !dhcpForm.enabled
  try {
    const s = await putDhcpServerSettings({ enabled: next })
    dhcpStatus.value = s
    dhcpForm.enabled = s.enabled
    flash()
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to toggle DHCP server')
  } finally {
    dhcpToggling.value = false
  }
}

async function saveDhcpPool() {
  lastError.value = ''
  dhcpSaving.value = true
  try {
    const s = await putDhcpServerSettings({
      pool_start: dhcpForm.pool_start || undefined,
      pool_end: dhcpForm.pool_end || undefined,
      gateway: dhcpForm.gateway || undefined,
      lease_time_seconds: dhcpForm.lease_time_seconds || undefined,
      domain: dhcpForm.domain || undefined,
      dns_server: dhcpForm.dns_server || undefined,
    })
    dhcpStatus.value = s
    dhcpDirty.value = false
    flash()
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to save DHCP settings')
  } finally {
    dhcpSaving.value = false
  }
}

async function addStaticEntry() {
  staticAddError.value = ''
  if (!newStatic.mac || !newStatic.ip) {
    staticAddError.value = 'MAC and IP are required'
    return
  }
  staticAdding.value = true
  try {
    await createDhcpStaticAssignment({ mac: newStatic.mac, ip: newStatic.ip, hostname: newStatic.hostname })
    newStatic.mac = ''
    newStatic.ip = ''
    newStatic.hostname = ''
    dhcpAddRow.value = false
    await refreshStaticEntries()
  } catch (err) {
    staticAddError.value = errMsg(err, 'Failed to add static assignment')
  } finally {
    staticAdding.value = false
  }
}

async function confirmDeleteStatic(mac: string) {
  if (!window.confirm(`Delete static assignment for ${mac}?`)) return
  deletingMAC.value = mac
  try {
    await deleteDhcpStaticAssignment(mac)
    await refreshStaticEntries()
  } catch (err) {
    lastError.value = errMsg(err, `Failed to delete assignment for ${mac}`)
  } finally {
    deletingMAC.value = ''
  }
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function formatExpiry(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const diffMs = d.getTime() - Date.now()
  if (diffMs <= 0) return 'expired'
  const mins = Math.round(diffMs / 60000)
  if (mins < 60) return `${mins}m`
  const hrs = Math.round(mins / 60)
  if (hrs < 24) return `${hrs}h`
  return `${Math.round(hrs / 24)}d`
}

function flash() {
  const token = Date.now()
  savedAt.value = token
  window.setTimeout(() => { if (savedAt.value === token) savedAt.value = 0 }, 2000)
}

function errMsg(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }, message?: string }
  return e?.response?.data?.error || e?.message || fallback
}
</script>
