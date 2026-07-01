<template>
  <div class="space-y-4">

    <!-- Toolbar -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">Devices</h1>
      <div class="flex items-center gap-2">
        <button class="btn-ghost" :disabled="loading" @click="refresh">
          <ArrowPathIcon class="h-4 w-4" :class="{ 'animate-spin': loading }" />
          <span>Refresh</span>
        </button>
        <button class="btn-primary" @click="openCreate">
          <PlusIcon class="h-4 w-4" />
          <span>Register device</span>
        </button>
      </div>
    </div>

    <!-- Search -->
    <div class="flex-1 relative">
      <MagnifyingGlassIcon class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-fg-subtle" />
      <input v-model="search"
             type="text"
             placeholder="Filter by name, IP, MAC, or hostname…"
             class="input pl-9 w-full" />
    </div>

    <!-- Empty state -->
    <p v-if="loading && devices.length === 0" class="card p-6 text-sm text-fg-muted text-center">
      Loading…
    </p>
    <div v-else-if="filtered.length === 0" class="card p-6 text-sm text-fg-muted text-center">
      No registered devices yet. Click <strong>Register device</strong> to add one.
    </div>

    <!-- Device table -->
    <div v-else class="card overflow-hidden">
      <table class="table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Profile</th>
            <th>IPs</th>
            <th>MACs</th>
            <th>Hostnames</th>
            <th>Client-IDs</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in filtered" :key="d.id">
            <td class="font-medium">{{ d.name }}</td>
            <td><span class="badge bg-accent-subtle text-accent">{{ d.profile_id }}</span></td>
            <td class="font-mono text-xs text-fg-muted">{{ (d.ips ?? []).join(', ') || '—' }}</td>
            <td class="font-mono text-xs text-fg-muted">{{ (d.macs ?? []).join(', ') || '—' }}</td>
            <td class="text-xs text-fg-muted">{{ (d.hostnames ?? []).join(', ') || '—' }}</td>
            <td class="font-mono text-xs text-fg-muted">{{ (d.client_ids ?? []).join(', ') || '—' }}</td>
            <td class="text-right whitespace-nowrap">
              <button class="btn-ghost" title="Edit" @click="openEdit(d)">
                <PencilIcon class="h-4 w-4" />
              </button>
              <button class="btn-ghost text-danger" title="Delete" @click="confirmDelete(d)">
                <TrashIcon class="h-4 w-4" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create / Edit modal -->
    <Teleport to="body">
      <div v-if="panelOpen"
           class="fixed inset-0 z-40 flex items-center justify-center p-4"
           @click.self="closePanel">
        <div class="fixed inset-0 bg-black/60" @click="closePanel" />
        <div class="relative w-full max-w-lg bg-bg-card border border-border rounded-xl shadow-2xl flex flex-col z-50 max-h-[90vh]">
          <div class="flex items-center justify-between px-5 py-4 border-b border-border">
            <h2 class="font-semibold text-fg-strong">{{ editingDevice ? 'Edit device' : 'Register device' }}</h2>
            <button class="btn-ghost" @click="closePanel">
              <XMarkIcon class="h-5 w-5" />
            </button>
          </div>

          <div class="flex-1 overflow-y-auto px-5 py-4 space-y-4">

            <div>
              <label class="label">Name <span class="text-danger">*</span></label>
              <input v-model="form.name" class="input w-full" placeholder="workstation-01" :disabled="!!editingDevice" />
            </div>

            <div>
              <label class="label">Profile <span class="text-danger">*</span></label>
              <select v-model="form.profile_id" class="input w-full">
                <option value="">— select a profile —</option>
                <option v-for="p in profiles" :key="p.id" :value="p.id">{{ p.name || p.id }}</option>
              </select>
            </div>

            <div>
              <label class="label">MAC addresses</label>
              <textarea v-model="form.macsText" class="input w-full font-mono text-xs" rows="2"
                        placeholder="aa:bb:cc:dd:ee:01&#10;aa:bb:cc:dd:ee:02" />
              <p class="text-xs text-fg-muted mt-1">One per line</p>
            </div>

            <div>
              <label class="label">IP addresses</label>
              <textarea v-model="form.ipsText" class="input w-full font-mono text-xs" rows="2"
                        placeholder="192.168.1.10" />
              <p class="text-xs text-fg-muted mt-1">One per line</p>
            </div>

            <div>
              <label class="label">Hostnames</label>
              <textarea v-model="form.hostnamesText" class="input w-full font-mono text-xs" rows="2"
                        placeholder="workstation-01.lan" />
              <p class="text-xs text-fg-muted mt-1">One per line</p>
            </div>

            <div>
              <label class="label">Client-IDs</label>
              <textarea v-model="form.clientIDsText" class="input w-full font-mono text-xs" rows="2"
                        placeholder="" />
              <p class="text-xs text-fg-muted mt-1">One per line</p>
            </div>

            <p v-if="formError" class="text-sm text-danger">{{ formError }}</p>
          </div>

          <div class="px-5 py-4 border-t border-border flex justify-end gap-2">
            <button class="btn-secondary" @click="closePanel">Cancel</button>
            <button class="btn-primary" :disabled="saving" @click="save">
              {{ saving ? 'Saving…' : editingDevice ? 'Save changes' : 'Register' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Delete confirmation dialog -->
    <Teleport to="body">
      <div v-if="deleteTarget"
           class="fixed inset-0 z-50 flex items-center justify-center"
           @click.self="deleteTarget = null">
        <div class="fixed inset-0 bg-black/50" @click="deleteTarget = null" />
        <div class="relative bg-bg-elevated rounded-lg shadow-xl p-6 max-w-sm w-full mx-4 z-10 space-y-4">
          <h2 class="font-semibold text-fg-strong">Delete {{ deleteTarget.name }}?</h2>
          <p class="text-sm text-fg-muted">
            This will remove the device registration. Profile assignments and filtering rules are unaffected.
          </p>
          <p v-if="deleteError" class="text-sm text-danger">{{ deleteError }}</p>
          <div class="flex justify-end gap-2">
            <button class="btn-secondary" @click="deleteTarget = null">Cancel</button>
            <button class="btn-danger" :disabled="deleting" @click="doDelete">
              {{ deleting ? 'Deleting…' : 'Delete' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>

  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import {
  ArrowPathIcon, PlusIcon, MagnifyingGlassIcon,
  PencilIcon, TrashIcon, XMarkIcon,
} from '@heroicons/vue/24/outline'
import { listDevices, createDevice, updateDevice, deleteDevice, listProfiles } from '@/api/endpoints'
import type { Device, Profile } from '@/api/types'

const devices  = ref<Device[]>([])
const profiles = ref<Profile[]>([])
const loading  = ref(false)
const search   = ref('')

const panelOpen     = ref(false)
const editingDevice = ref<Device | null>(null)
const saving        = ref(false)
const formError     = ref('')

const deleteTarget = ref<Device | null>(null)
const deleting     = ref(false)
const deleteError  = ref('')

interface DeviceForm {
  name: string
  profile_id: string
  macsText: string
  ipsText: string
  hostnamesText: string
  clientIDsText: string
}

const blankForm = (): DeviceForm => ({
  name: '', profile_id: '', macsText: '', ipsText: '', hostnamesText: '', clientIDsText: '',
})
const form = ref<DeviceForm>(blankForm())

const filtered = computed(() => {
  const q = search.value.toLowerCase().trim()
  if (!q) return devices.value
  return devices.value.filter(d =>
    d.name.toLowerCase().includes(q) ||
    (d.ips ?? []).some(ip => ip.includes(q)) ||
    (d.macs ?? []).some(m => m.toLowerCase().includes(q)) ||
    (d.hostnames ?? []).some(h => h.toLowerCase().includes(q)) ||
    (d.client_ids ?? []).some(c => c.toLowerCase().includes(q)),
  )
})

async function refresh() {
  loading.value = true
  try {
    const [devs, profs] = await Promise.all([listDevices(), listProfiles()])
    devices.value  = devs ?? []
    profiles.value = profs ?? []
  } finally {
    loading.value = false
  }
}

onMounted(refresh)

function openCreate() {
  editingDevice.value = null
  form.value = blankForm()
  formError.value = ''
  panelOpen.value = true
}

function openEdit(d: Device) {
  editingDevice.value = d
  form.value = {
    name: d.name,
    profile_id: d.profile_id,
    macsText: (d.macs ?? []).join('\n'),
    ipsText: (d.ips ?? []).join('\n'),
    hostnamesText: (d.hostnames ?? []).join('\n'),
    clientIDsText: (d.client_ids ?? []).join('\n'),
  }
  formError.value = ''
  panelOpen.value = true
}

function closePanel() {
  panelOpen.value = false
  editingDevice.value = null
}

function parseLines(text: string): string[] {
  return text.split('\n').map(s => s.trim()).filter(Boolean)
}

async function save() {
  formError.value = ''
  if (!form.value.name.trim()) {
    formError.value = 'Name is required.'
    return
  }
  if (!form.value.profile_id) {
    formError.value = 'Profile is required.'
    return
  }
  saving.value = true
  try {
    const payload = {
      name:       form.value.name.trim(),
      profile_id: form.value.profile_id,
      macs:       parseLines(form.value.macsText),
      ips:        parseLines(form.value.ipsText),
      hostnames:  parseLines(form.value.hostnamesText),
      client_ids: parseLines(form.value.clientIDsText),
    }
    if (editingDevice.value) {
      await updateDevice(editingDevice.value.id, payload)
    } else {
      await createDevice(payload)
    }
    closePanel()
    await refresh()
  } catch (e: any) {
    formError.value = e?.message ?? 'Save failed.'
  } finally {
    saving.value = false
  }
}

function confirmDelete(d: Device) {
  deleteTarget.value = d
  deleteError.value = ''
}

async function doDelete() {
  if (!deleteTarget.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    await deleteDevice(deleteTarget.value.id)
    deleteTarget.value = null
    await refresh()
  } catch (e: any) {
    deleteError.value = e?.message ?? 'Delete failed.'
  } finally {
    deleting.value = false
  }
}
</script>
