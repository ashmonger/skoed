<template>
  <div class="space-y-4">
    <!-- Global error banner -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <!-- Toolbar -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">Profiles</h1>
      <button class="btn-primary" @click="openCreate">
        <PlusIcon class="h-4 w-4" /> New profile
      </button>
    </div>

    <!-- Table card -->
    <div class="card p-4">
      <p v-if="loading" class="text-sm text-fg-muted py-6 text-center">Loading…</p>

      <div v-else-if="nonDefaultCount === 0 && !hasDefault" class="py-12 text-center space-y-3">
        <p class="text-sm text-fg-muted">No profiles yet.</p>
        <button class="btn-primary" @click="openCreate">
          <PlusIcon class="h-4 w-4" /> Create your first profile
        </button>
      </div>

      <table v-else class="table">
        <thead>
          <tr>
            <th>ID</th>
            <th>Name</th>
            <th>Blocklists</th>
            <th>Clients</th>
            <th>SafeSearch</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="p in profiles" :key="p.id">
            <tr class="cursor-pointer" @click="openEdit(p)">
              <td class="font-mono text-xs text-fg-muted">{{ p.id }}</td>
              <td class="font-medium text-fg-strong">{{ p.name }}</td>
              <td>
                <div class="flex items-center gap-1 flex-wrap">
                  <span class="text-xs text-fg-muted">{{ (p.blocklists?.length ?? 0) }}</span>
                  <template v-for="(blId, idx) in displayedBlocklists(p)" :key="blId">
                    <span class="badge-accent font-mono text-[10px]">{{ blId }}</span>
                    <span v-if="idx === displayedBlocklists(p).length - 1
                              && (p.blocklists?.length ?? 0) > BLOCKLIST_BADGE_LIMIT"
                          class="text-xs text-fg-muted">
                      +{{ (p.blocklists?.length ?? 0) - BLOCKLIST_BADGE_LIMIT }}
                    </span>
                  </template>
                </div>
              </td>
              <td class="text-xs text-fg-muted whitespace-nowrap">
                {{ (p.client_ips?.length ?? 0) }} IP{{ (p.client_ips?.length ?? 0) === 1 ? '' : 's' }}
                · {{ (p.client_cidrs?.length ?? 0) }} CIDR{{ (p.client_cidrs?.length ?? 0) === 1 ? '' : 's' }}
              </td>
              <td>
                <div v-if="(p.safesearch?.length ?? 0) > 0" class="flex flex-wrap gap-1">
                  <span v-for="prov in p.safesearch" :key="prov" class="badge-success">{{ prov }}</span>
                </div>
                <span v-else class="text-xs text-fg-subtle">off</span>
              </td>
              <td class="text-right whitespace-nowrap" @click.stop>
                <button class="btn-ghost"
                        :title="`Copy DoH-gap rules for ${p.id}`"
                        :data-testid="`copy-doh-gap-rules-profile-${p.id}`"
                        @click="openFwRules({ kind: 'profile', profileId: p.id })">
                  <ClipboardDocumentListIcon class="h-4 w-4" />
                </button>
                <button class="btn-ghost"
                        title="Edit profile"
                        @click="openEdit(p)">
                  <PencilSquareIcon class="h-4 w-4" />
                </button>
                <button v-if="p.id !== 'default'"
                        class="btn-ghost text-danger"
                        title="Delete profile"
                        @click="askDelete(p)">
                  <TrashIcon class="h-4 w-4" />
                </button>
                <button v-else
                        class="btn-ghost text-fg-subtle"
                        disabled
                        title="Cannot delete the default profile">
                  <TrashIcon class="h-4 w-4" />
                </button>
              </td>
            </tr>
            <tr v-if="rowErrors[p.id]">
              <td colspan="6" class="!py-1 text-xs text-danger">{{ rowErrors[p.id] }}</td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>

    <!-- Edit / Create modal -->
    <div v-if="showModal"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="closeModal">
      <form class="card max-w-2xl w-full p-6 max-h-[90vh] overflow-y-auto space-y-4"
            @submit.prevent="submitModal">
        <h2 class="text-base font-semibold text-fg-strong">
          {{ mode === 'create' ? 'New profile' : `Edit ${original?.name ?? ''}` }}
        </h2>

        <p v-if="formError" class="text-sm text-danger">{{ formError }}</p>

        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="label" for="pf-id">
              ID
              <span v-if="mode === 'edit'" class="text-fg-subtle font-normal">(read-only)</span>
            </label>
            <input id="pf-id" v-model="form.id"
                   class="input font-mono text-xs"
                   :disabled="mode === 'edit'"
                   :required="mode === 'create'"
                   placeholder="e.g. kids" />
          </div>
          <div>
            <label class="label" for="pf-name">Name</label>
            <input id="pf-name" v-model="form.name" class="input" required
                   placeholder="e.g. Kids" />
          </div>
        </div>

        <div>
          <span class="label">Blocklists</span>
          <p v-if="!blocklistOptions.length" class="text-xs text-fg-subtle">
            No blocklists available. Create one first.
          </p>
          <div v-else
               class="max-h-48 overflow-y-auto border border-border rounded p-2 space-y-1">
            <label v-for="bl in blocklistOptions" :key="bl.id"
                   class="flex items-center gap-2 text-sm">
              <input type="checkbox"
                     :value="bl.id"
                     v-model="form.blocklists" />
              <span class="font-mono text-xs text-fg-muted">{{ bl.id }}</span>
              <span class="text-fg">{{ bl.name }}</span>
              <span v-if="!bl.enabled" class="badge text-fg-subtle">disabled</span>
            </label>
          </div>
        </div>

        <div>
          <label class="label" for="pf-allowlist">
            Allowlist
            <span class="text-fg-subtle font-normal">(one domain per line)</span>
          </label>
          <textarea id="pf-allowlist"
                    v-model="form.allowlistText"
                    rows="3"
                    class="input font-mono text-xs"
                    placeholder="example.com&#10;trusted.example.net" />
        </div>

        <div>
          <span class="label">SafeSearch providers</span>
          <div class="flex flex-wrap gap-4 text-sm">
            <label v-for="prov in SAFESEARCH_PROVIDERS" :key="prov"
                   class="inline-flex items-center gap-2">
              <input type="checkbox" :value="prov" v-model="form.safesearch" />
              {{ prov }}
            </label>
          </div>
        </div>

        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="label" for="pf-ips">
              Client IPs
              <span class="text-fg-subtle font-normal">(one per line)</span>
            </label>
            <textarea id="pf-ips"
                      v-model="form.clientIpsText"
                      rows="4"
                      class="input font-mono text-xs"
                      placeholder="192.168.1.10&#10;10.0.0.5" />
          </div>
          <div>
            <label class="label" for="pf-cidrs">
              Client CIDRs
              <span class="text-fg-subtle font-normal">(one per line)</span>
            </label>
            <textarea id="pf-cidrs"
                      v-model="form.clientCidrsText"
                      rows="4"
                      class="input font-mono text-xs"
                      placeholder="192.168.1.0/24&#10;10.0.0.0/8" />
          </div>
        </div>

        <!-- M3.6 — stable identity match keys.
             Priority on lookup: Client-ID > MAC > hostname > IP/CIDR.
             Operators sourcing these from DHCP get device-stable matching
             that survives lease renewals. -->
        <details class="space-y-2">
          <summary class="cursor-pointer text-sm text-fg-muted hover:text-fg-strong">
            DHCP-stable identity (Client-ID / MAC / hostname)
          </summary>
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-4 pt-2">
            <div>
              <label class="label" for="pf-ids">
                Client-IDs
                <span class="text-fg-subtle font-normal">(one per line)</span>
              </label>
              <textarea id="pf-ids"
                        v-model="form.clientIdsText"
                        rows="3"
                        class="input font-mono text-xs"
                        placeholder="id:tablet42&#10;01:aa:bb:cc:dd:ee:ff" />
            </div>
            <div>
              <label class="label" for="pf-macs">
                MACs
                <span class="text-fg-subtle font-normal">(one per line)</span>
              </label>
              <textarea id="pf-macs"
                        v-model="form.clientMacsText"
                        rows="3"
                        class="input font-mono text-xs"
                        placeholder="aa:bb:cc:dd:ee:ff" />
            </div>
            <div>
              <label class="label" for="pf-hostnames">
                Hostnames
                <span class="text-fg-subtle font-normal">(one per line)</span>
              </label>
              <textarea id="pf-hostnames"
                        v-model="form.clientHostnamesText"
                        rows="3"
                        class="input font-mono text-xs"
                        placeholder="kid-tablet&#10;home-laptop" />
            </div>
          </div>
        </details>

        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn-secondary" @click="closeModal">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="submitting">
            {{ submitting
                ? (mode === 'create' ? 'Creating…' : 'Saving…')
                : (mode === 'create' ? 'Create profile' : 'Save changes') }}
          </button>
        </div>
      </form>
    </div>

    <!-- M6 — Copy DoH-gap rules modal (per-profile scope). -->
    <FirewallRulesModal
      v-if="fwRuleScope"
      :scope="fwRuleScope"
      @close="fwRuleScope = null" />

    <!-- Delete confirmation modal -->
    <div v-if="pendingDelete"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="pendingDelete = null">
      <div class="card max-w-lg w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Delete profile?</h2>
        <p class="text-sm text-fg">
          Delete profile <span class="font-semibold">{{ pendingDelete.name }}</span>?
          <span class="text-fg-muted">
            Matches {{ matchedClientCount(pendingDelete) }} client{{ matchedClientCount(pendingDelete) === 1 ? '' : 's' }}
            ({{ (pendingDelete.client_ips?.length ?? 0) }} IP{{ (pendingDelete.client_ips?.length ?? 0) === 1 ? '' : 's' }},
            {{ (pendingDelete.client_cidrs?.length ?? 0) }} CIDR{{ (pendingDelete.client_cidrs?.length ?? 0) === 1 ? '' : 's' }}).
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
  ClipboardDocumentListIcon, PencilSquareIcon, PlusIcon, TrashIcon,
} from '@heroicons/vue/24/outline'
import {
  createProfile, deleteProfile, listBlocklists, listProfiles, updateProfile,
} from '@/api/endpoints'
import type { FwRuleScope } from '@/api/endpoints'
import type { Blocklist, Profile } from '@/api/types'
import FirewallRulesModal from '@/components/FirewallRulesModal.vue'

// ─── Constants ───────────────────────────────────────────────────────────

const SAFESEARCH_PROVIDERS = ['google', 'bing', 'youtube', 'duckduckgo'] as const
const BLOCKLIST_BADGE_LIMIT = 3

// ─── State ───────────────────────────────────────────────────────────────

const profiles = ref<Profile[]>([])
const blocklistOptions = ref<Blocklist[]>([])
const loading = ref(true)
const lastError = ref('')
const rowErrors = reactive<Record<string, string>>({})

const showModal = ref(false)
const mode = ref<'create' | 'edit'>('create')
const submitting = ref(false)
const formError = ref('')
const original = ref<Profile | null>(null)

interface FormState {
  id: string
  name: string
  blocklists: string[]
  allowlistText: string
  safesearch: string[]
  clientIpsText: string
  clientCidrsText: string
  clientIdsText: string
  clientMacsText: string
  clientHostnamesText: string
}

const emptyForm = (): FormState => ({
  id: '',
  name: '',
  blocklists: [],
  allowlistText: '',
  safesearch: [],
  clientIpsText: '',
  clientCidrsText: '',
  clientIdsText: '',
  clientMacsText: '',
  clientHostnamesText: '',
})
const form = reactive<FormState>(emptyForm())

const pendingDelete = ref<Profile | null>(null)
const deleting = ref(false)

// M6 — "Copy DoH-gap rules" modal scoped to a profile.
const fwRuleScope = ref<FwRuleScope | null>(null)
function openFwRules(scope: FwRuleScope) {
  fwRuleScope.value = scope
}

// ─── Derived ─────────────────────────────────────────────────────────────

const hasDefault = computed(() => profiles.value.some(p => p.id === 'default'))
const nonDefaultCount = computed(() => profiles.value.filter(p => p.id !== 'default').length)

function displayedBlocklists(p: Profile): string[] {
  return (p.blocklists ?? []).slice(0, BLOCKLIST_BADGE_LIMIT)
}

function matchedClientCount(p: Profile): number {
  return (p.client_ips?.length ?? 0) + (p.client_cidrs?.length ?? 0)
}

// ─── Data loading ────────────────────────────────────────────────────────

async function refresh() {
  loading.value = true
  try {
    const [profs, bls] = await Promise.all([listProfiles(), listBlocklists()])
    profiles.value = profs
    blocklistOptions.value = bls
    lastError.value = ''
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load profiles')
  } finally {
    loading.value = false
  }
}

// ─── Modal: create / edit ────────────────────────────────────────────────

function openCreate() {
  mode.value = 'create'
  original.value = null
  Object.assign(form, emptyForm())
  formError.value = ''
  showModal.value = true
}

function openEdit(p: Profile) {
  mode.value = 'edit'
  original.value = p
  form.id = p.id
  form.name = p.name
  form.blocklists = [...(p.blocklists ?? [])]
  form.allowlistText = (p.allowlist ?? []).join('\n')
  form.safesearch = [...(p.safesearch ?? [])]
  form.clientIpsText = (p.client_ips ?? []).join('\n')
  form.clientCidrsText = (p.client_cidrs ?? []).join('\n')
  form.clientIdsText = (p.client_ids ?? []).join('\n')
  form.clientMacsText = (p.client_macs ?? []).join('\n')
  form.clientHostnamesText = (p.client_hostnames ?? []).join('\n')
  formError.value = ''
  showModal.value = true
}

function closeModal() {
  if (submitting.value) return
  showModal.value = false
}

function parseLines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map(l => l.trim())
    .filter(Boolean)
}

function arraysEqual(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false
  return true
}

async function submitModal() {
  formError.value = ''
  if (!form.name.trim()) {
    formError.value = 'Name is required.'
    return
  }
  if (mode.value === 'create' && !form.id.trim()) {
    formError.value = 'ID is required.'
    return
  }

  const allowlist = parseLines(form.allowlistText)
  const clientIps = parseLines(form.clientIpsText)
  const clientCidrs = parseLines(form.clientCidrsText)
  const clientIds = parseLines(form.clientIdsText)
  const clientMacs = parseLines(form.clientMacsText).map(s => s.toLowerCase())
  const clientHostnames = parseLines(form.clientHostnamesText)
  const blocklists = [...form.blocklists]
  const safesearch = [...form.safesearch]

  submitting.value = true
  try {
    if (mode.value === 'create') {
      const payload: Profile = {
        id: form.id.trim(),
        name: form.name.trim(),
        blocklists,
        allowlist,
        safesearch,
        client_ips: clientIps,
        client_cidrs: clientCidrs,
        client_ids: clientIds,
        client_macs: clientMacs,
        client_hostnames: clientHostnames,
      }
      const created = await createProfile(payload)
      profiles.value = [...profiles.value, created]
    } else if (original.value) {
      // Build a PATCH with only dirty fields.
      const patch: Partial<Profile> = {}
      const o = original.value
      if (form.name.trim() !== o.name) patch.name = form.name.trim()
      if (!arraysEqual(blocklists, o.blocklists ?? [])) patch.blocklists = blocklists
      if (!arraysEqual(allowlist, o.allowlist ?? [])) patch.allowlist = allowlist
      if (!arraysEqual(safesearch, o.safesearch ?? [])) patch.safesearch = safesearch
      if (!arraysEqual(clientIps, o.client_ips ?? [])) patch.client_ips = clientIps
      if (!arraysEqual(clientCidrs, o.client_cidrs ?? [])) patch.client_cidrs = clientCidrs
      if (!arraysEqual(clientIds, o.client_ids ?? [])) patch.client_ids = clientIds
      if (!arraysEqual(clientMacs, o.client_macs ?? [])) patch.client_macs = clientMacs
      if (!arraysEqual(clientHostnames, o.client_hostnames ?? [])) patch.client_hostnames = clientHostnames

      if (Object.keys(patch).length === 0) {
        showModal.value = false
        return
      }
      const updated = await updateProfile(o.id, patch)
      const idx = profiles.value.findIndex(p => p.id === o.id)
      if (idx >= 0) profiles.value.splice(idx, 1, updated)
    }
    showModal.value = false
  } catch (err) {
    formError.value = errMsg(err,
      mode.value === 'create' ? 'Failed to create profile' : 'Failed to save profile')
  } finally {
    submitting.value = false
  }
}

// ─── Delete ──────────────────────────────────────────────────────────────

function askDelete(p: Profile) {
  if (p.id === 'default') return
  pendingDelete.value = p
}

async function confirmDelete() {
  const p = pendingDelete.value
  if (!p) return
  deleting.value = true
  rowErrors[p.id] = ''
  try {
    await deleteProfile(p.id)
    profiles.value = profiles.value.filter(x => x.id !== p.id)
    pendingDelete.value = null
  } catch (err) {
    const msg = errMsg(err, 'Failed to delete profile')
    rowErrors[p.id] = msg
    lastError.value = msg
  } finally {
    deleting.value = false
  }
}

// ─── Helpers ─────────────────────────────────────────────────────────────

function errMsg(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }, message?: string }
  return e?.response?.data?.error || e?.message || fallback
}

// ─── Keyboard: close modals on Escape ────────────────────────────────────

function onKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (pendingDelete.value) { pendingDelete.value = null; return }
  if (showModal.value && !submitting.value) { showModal.value = false }
}

onMounted(() => {
  refresh()
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
})
</script>
