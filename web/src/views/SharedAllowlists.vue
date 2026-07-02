<template>
  <div class="space-y-4">
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <!-- Header -->
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 class="text-lg font-semibold text-fg-strong">Shared Allowlists</h1>
        <p class="text-sm text-fg-muted">
          Named allowlists that can be applied to multiple profiles at once.
        </p>
      </div>
      <button class="btn-primary" @click="openCreate">
        <PlusIcon class="h-4 w-4" />
        New shared allowlist
      </button>
    </div>

    <!-- Loading state -->
    <div v-if="loading" class="text-sm text-fg-muted py-8 text-center">Loading…</div>

    <!-- Empty state -->
    <div v-else-if="sharedAllowlists.length === 0"
         class="card py-12 text-center space-y-2">
      <p class="text-fg-muted text-sm">No shared allowlists yet.</p>
      <p class="text-fg-muted text-xs">
        Create one to share a set of always-allowed domains across multiple profiles.
      </p>
    </div>

    <!-- List -->
    <ul v-else class="space-y-3">
      <li v-for="sal in sharedAllowlists" :key="sal.id" class="card p-4 space-y-2">
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <p class="font-medium text-fg-strong truncate">{{ sal.name }}</p>
            <p class="text-xs text-fg-muted font-mono">{{ sal.id }}</p>
          </div>
          <div class="flex items-center gap-1.5 shrink-0">
            <button class="btn-ghost" title="Edit" @click="openEdit(sal)">
              <PencilSquareIcon class="h-4 w-4" />
            </button>
            <button class="btn-ghost text-danger" title="Delete" @click="confirmDelete(sal)">
              <TrashIcon class="h-4 w-4" />
            </button>
          </div>
        </div>
        <div class="flex flex-wrap gap-1.5 text-xs">
          <span class="badge-neutral">{{ (sal.entries || []).length }} entries</span>
          <span v-for="pid in (sal.profiles || [])" :key="pid"
                class="badge-accent">{{ profileName(pid) }}</span>
          <span v-if="!(sal.profiles || []).length" class="text-fg-muted italic">No profiles assigned</span>
        </div>
      </li>
    </ul>

    <!-- Create / Edit modal -->
    <div v-if="modal" class="fixed inset-0 z-50 flex items-center justify-center bg-overlay/60 p-4"
         @click.self="modal = null">
      <div class="card w-full max-w-lg space-y-4 p-5 max-h-[90vh] overflow-y-auto">
        <h2 class="font-semibold text-fg-strong">
          {{ modal.mode === 'create' ? 'New Shared Allowlist' : 'Edit Shared Allowlist' }}
        </h2>

        <div class="space-y-3">
          <div v-if="modal.mode === 'create'">
            <label class="label" for="sal-id">ID <span class="text-danger">*</span></label>
            <input id="sal-id" v-model="modal.id" type="text" class="input font-mono text-sm"
                   placeholder="e.g. family-safe" />
          </div>
          <div>
            <label class="label" for="sal-name">Name <span class="text-danger">*</span></label>
            <input id="sal-name" v-model="modal.name" type="text" class="input"
                   placeholder="e.g. Family Safe Sites" />
          </div>

          <!-- Profile assignments -->
          <div>
            <label class="label">Assign to profiles</label>
            <div class="flex flex-wrap gap-2">
              <label v-for="p in profiles" :key="p.id"
                     class="flex items-center gap-1.5 text-sm cursor-pointer">
                <input type="checkbox"
                       :value="p.id"
                       v-model="modal.profiles"
                       class="rounded border-border" />
                {{ p.name || p.id }}
              </label>
            </div>
          </div>

          <!-- Entries -->
          <div>
            <label class="label" for="sal-entries">Domains (one per line)</label>
            <textarea id="sal-entries"
                      v-model="modal.entriesText"
                      class="input font-mono text-sm min-h-[120px]"
                      placeholder="example.com&#10;*.trusted.net" />
          </div>
        </div>

        <p v-if="modalError" class="text-sm text-danger">{{ modalError }}</p>

        <div class="flex justify-end gap-2 pt-2">
          <button class="btn-secondary" @click="modal = null">Cancel</button>
          <button class="btn-primary" :disabled="saving" @click="saveModal">
            {{ saving ? 'Saving…' : 'Save' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Delete confirm modal -->
    <div v-if="deleteTarget" class="fixed inset-0 z-50 flex items-center justify-center bg-overlay/60 p-4"
         @click.self="deleteTarget = null">
      <div class="card w-full max-w-sm p-5 space-y-4">
        <h2 class="font-semibold text-fg-strong">Delete "{{ deleteTarget.name }}"?</h2>
        <p class="text-sm text-fg-muted">This will remove the shared allowlist and all its entries. Profiles will no longer benefit from it.</p>
        <div class="flex justify-end gap-2">
          <button class="btn-secondary" @click="deleteTarget = null">Cancel</button>
          <button class="btn-danger" :disabled="deleting" @click="doDelete">
            {{ deleting ? 'Deleting…' : 'Delete' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { PlusIcon, PencilSquareIcon, TrashIcon } from '@heroicons/vue/24/outline'
import {
  listSharedAllowlists, createSharedAllowlist, updateSharedAllowlist, deleteSharedAllowlist,
  listProfiles,
} from '@/api/endpoints'
import type { Profile, SharedAllowlist } from '@/api/types'

const loading = ref(true)
const lastError = ref('')
const sharedAllowlists = ref<SharedAllowlist[]>([])
const profiles = ref<Profile[]>([])

function profileName(id: string): string {
  const p = profiles.value.find(p => p.id === id)
  return p ? (p.name || p.id) : id
}

async function load() {
  loading.value = true
  try {
    const [sals, profs] = await Promise.all([listSharedAllowlists(), listProfiles()])
    sharedAllowlists.value = sals
    profiles.value = profs
  } catch (err) {
    lastError.value = String(err)
  } finally {
    loading.value = false
  }
}

onMounted(load)

// ─── Modal state ─────────────────────────────────────────────────────────────

interface ModalState {
  mode: 'create' | 'edit'
  id: string
  name: string
  profiles: string[]
  entriesText: string
}

const modal = ref<ModalState | null>(null)
const modalError = ref('')
const saving = ref(false)

function openCreate() {
  modal.value = { mode: 'create', id: '', name: '', profiles: [], entriesText: '' }
  modalError.value = ''
}

function openEdit(sal: SharedAllowlist) {
  const lines = (sal.entries || []).map(e => e.domain).join('\n')
  modal.value = {
    mode: 'edit',
    id: sal.id,
    name: sal.name,
    profiles: [...(sal.profiles || [])],
    entriesText: lines,
  }
  modalError.value = ''
}

function entriesFromText(text: string) {
  return text.split('\n')
    .map(l => l.trim())
    .filter(l => l.length > 0)
    .map(domain => ({ domain }))
}

async function saveModal() {
  if (!modal.value) return
  const m = modal.value
  if (!m.id.trim()) { modalError.value = 'ID is required'; return }
  if (!m.name.trim()) { modalError.value = 'Name is required'; return }

  saving.value = true
  modalError.value = ''
  try {
    const sal: SharedAllowlist = {
      id: m.id.trim(),
      name: m.name.trim(),
      profiles: m.profiles,
      entries: entriesFromText(m.entriesText),
    }
    if (m.mode === 'create') {
      await createSharedAllowlist(sal)
    } else {
      await updateSharedAllowlist(sal.id, sal)
    }
    modal.value = null
    await load()
  } catch (err) {
    modalError.value = String(err)
  } finally {
    saving.value = false
  }
}

// ─── Delete ───────────────────────────────────────────────────────────────────

const deleteTarget = ref<SharedAllowlist | null>(null)
const deleting = ref(false)

function confirmDelete(sal: SharedAllowlist) {
  deleteTarget.value = sal
}

async function doDelete() {
  if (!deleteTarget.value) return
  deleting.value = true
  try {
    await deleteSharedAllowlist(deleteTarget.value.id)
    deleteTarget.value = null
    await load()
  } catch (err) {
    lastError.value = String(err)
    deleteTarget.value = null
  } finally {
    deleting.value = false
  }
}
</script>
