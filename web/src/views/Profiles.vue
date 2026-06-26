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
            <th>Pause</th>
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
              <td @click.stop>
                <button v-if="profilePauses[p.id]?.active"
                        class="badge-warning cursor-pointer border-0 hover:opacity-80"
                        :title="`Paused — ${formatRemaining(profileRemainingMs(p.id))}`"
                        @click="openProfilePause(p.id)">
                  paused
                </button>
                <button v-else
                        class="btn-ghost text-xs text-fg-muted"
                        title="Pause filtering for this profile"
                        @click="openProfilePause(p.id)">
                  <ClockIcon class="h-4 w-4" />
                </button>
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
              <td colspan="7" class="!py-1 text-xs text-danger">{{ rowErrors[p.id] }}</td>
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
        <div class="flex items-start justify-between gap-3">
          <h2 class="text-base font-semibold text-fg-strong">
            {{ mode === 'create' ? 'New profile' : `Edit ${original?.name ?? ''}` }}
          </h2>
          <!-- Tab bar — only shown in edit mode -->
          <div v-if="mode === 'edit'" class="flex items-center gap-1 text-sm border border-border rounded overflow-hidden">
            <button type="button"
                    class="px-3 py-1.5 transition-colors"
                    :class="modalTab === 'settings' ? 'bg-accent text-white font-medium' : 'text-fg-muted hover:text-fg-strong'"
                    @click="modalTab = 'settings'">
              Settings
            </button>
            <button type="button"
                    class="px-3 py-1.5 transition-colors flex items-center gap-1"
                    :class="modalTab === 'allowlist' ? 'bg-accent text-white font-medium' : 'text-fg-muted hover:text-fg-strong'"
                    @click="openAllowlistTab">
              Allowlist
              <span v-if="allowlistEntries.length > 0"
                    class="inline-flex items-center justify-center h-4 min-w-[1rem] rounded-full text-[10px] font-bold px-1"
                    :class="modalTab === 'allowlist' ? 'bg-white/20 text-white' : 'bg-accent/20 text-accent'">
                {{ allowlistEntries.length }}
              </span>
            </button>
            <button type="button"
                    class="px-3 py-1.5 transition-colors"
                    :class="modalTab === 'blockpage' ? 'bg-accent text-white font-medium' : 'text-fg-muted hover:text-fg-strong'"
                    @click="modalTab = 'blockpage'">
              Block page
            </button>
          </div>
        </div>

        <p v-if="formError" class="text-sm text-danger">{{ formError }}</p>

        <!-- ── Allowlist tab (edit mode only) ─────────────────────────────── -->
        <div v-if="mode === 'edit' && modalTab === 'allowlist'" class="space-y-3">
          <p v-if="allowlistError" class="text-sm text-danger">{{ allowlistError }}</p>

          <!-- Add domain input -->
          <div class="flex items-end gap-2">
            <div class="flex-1">
              <label class="label" for="pf-al-add">Add domain</label>
              <input id="pf-al-add"
                     v-model="allowlistAddInput"
                     class="input font-mono text-sm"
                     placeholder="example.com or *.example.com"
                     :disabled="allowlistAdding"
                     @keydown.enter.prevent="submitAllowlistAdd" />
            </div>
            <button type="button"
                    class="btn-primary"
                    :disabled="allowlistAdding || !allowlistAddInput.trim()"
                    @click="submitAllowlistAdd">
              <PlusIcon class="h-4 w-4" />
              {{ allowlistAdding ? 'Adding…' : 'Add' }}
            </button>
          </div>

          <!-- Entry list -->
          <div v-if="allowlistLoading" class="text-sm text-fg-muted py-4 text-center">Loading…</div>
          <div v-else-if="allowlistEntries.length === 0"
               class="text-sm text-fg-muted text-center py-4 border border-dashed border-border rounded">
            No allowlist entries for this profile.
          </div>
          <ul v-else class="divide-y divide-border border border-border rounded max-h-64 overflow-y-auto">
            <li v-for="entry in allowlistEntries"
                :key="entry"
                class="flex items-center justify-between px-3 py-2 text-sm gap-2">
              <span class="flex items-center gap-1.5 min-w-0">
                <span v-if="isWildcard(entry)"
                      class="badge-accent text-[10px] font-mono shrink-0">*</span>
                <span class="font-mono text-xs text-fg truncate">{{ entry }}</span>
              </span>
              <button type="button"
                      class="btn-ghost text-danger shrink-0"
                      :disabled="allowlistDeleting === entry"
                      :title="`Remove ${entry}`"
                      @click="deleteAllowlistEntry(entry)">
                <TrashIcon class="h-4 w-4" />
              </button>
            </li>
          </ul>
        </div>

        <!-- ── Block page tab (edit mode only) ────────────────────────────── -->
        <div v-if="mode === 'edit' && modalTab === 'blockpage'" class="space-y-3">
          <p class="text-xs text-fg-muted">
            Override the global block page content for clients in this profile.
            Leave fields empty to use the global default.
          </p>
          <p v-if="blockPageTabError" class="text-sm text-danger">{{ blockPageTabError }}</p>
          <p v-if="blockPageTabSaved" class="text-sm text-success">Saved.</p>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label class="label" for="bp-tab-title">Page title</label>
              <input id="bp-tab-title" v-model="blockPageTabForm.title"
                     type="text" placeholder="Access Blocked" class="input" />
            </div>
            <div>
              <label class="label" for="bp-tab-email">Contact email</label>
              <input id="bp-tab-email" v-model="blockPageTabForm.contact_email"
                     type="email" placeholder="admin@example.com" class="input" />
            </div>
            <div class="sm:col-span-2">
              <label class="label" for="bp-tab-message">Message</label>
              <textarea id="bp-tab-message" v-model="blockPageTabForm.message"
                        rows="2" placeholder="This site has been blocked." class="input resize-none" />
            </div>
            <div class="sm:col-span-2">
              <label class="label" for="bp-tab-passcode">Bypass passcode</label>
              <input id="bp-tab-passcode" v-model="blockPageTabForm.bypass_passcode"
                     type="text" placeholder="e.g. homework1234" class="input font-mono" />
              <p class="text-xs text-fg-muted mt-1">
                Users on the block page can enter this code to pause filtering for a set duration.
              </p>
            </div>
          </div>
          <div class="flex justify-end gap-2 pt-2">
            <button type="button" class="btn-secondary" @click="closeModal">Close</button>
            <button type="button" class="btn-primary" :disabled="blockPageTabSaving" @click="saveBlockPageTab">
              {{ blockPageTabSaving ? 'Saving…' : 'Save block page' }}
            </button>
          </div>
        </div>

        <!-- ── Settings tab (always shown in create mode; toggled in edit) ── -->
        <template v-if="mode === 'create' || modalTab === 'settings'">

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

        </template><!-- end settings/create template -->

        <!-- Close button for allowlist tab (no form submit needed) -->
        <div v-if="mode === 'edit' && modalTab === 'allowlist'" class="flex justify-end pt-2">
          <button type="button" class="btn-secondary" @click="closeModal">Close</button>
        </div>
      </form>
    </div>

    <!-- M13 — Per-profile pause modal -->
    <div v-if="profilePauseTarget !== null"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="profilePauseTarget = null">
      <div class="card max-w-sm w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">
          Pause filtering &mdash;
          <span class="font-mono text-sm text-fg-muted">{{ profilePauseTarget }}</span>
        </h2>

        <p v-if="profilePauseError" class="text-sm text-danger">{{ profilePauseError }}</p>

        <template v-if="profilePauseLoading">
          <p class="text-sm text-fg-muted">Loading…</p>
        </template>
        <template v-else-if="profilePauses[profilePauseTarget]?.active">
          <!-- Active pause: show status and resume option -->
          <div class="rounded border border-warning bg-warning-subtle px-4 py-3 space-y-1">
            <p class="text-sm font-medium text-warning">Filtering is paused</p>
            <p class="text-xs text-fg-muted">
              Resumes in
              <span class="font-mono font-medium text-fg">{{ formatRemaining(profileRemainingMs(profilePauseTarget)) }}</span>
              <span v-if="profilePauses[profilePauseTarget]?.reason">
                &nbsp;&mdash; {{ profilePauses[profilePauseTarget]?.reason }}
              </span>
            </p>
          </div>
          <div class="flex justify-end gap-2 pt-2">
            <button class="btn-secondary" @click="profilePauseTarget = null">Close</button>
            <button class="btn-primary" :disabled="profilePauseSubmitting" @click="resumeProfileFiltering">
              {{ profilePauseSubmitting ? 'Resuming…' : 'Resume now' }}
            </button>
          </div>
        </template>
        <template v-else>
          <!-- No active pause: show duration selector -->
          <div>
            <span class="label">Duration</span>
            <div class="grid grid-cols-4 gap-2">
              <button v-for="p in PAUSE_PRESETS" :key="p.seconds"
                      type="button"
                      class="btn-secondary text-xs"
                      :class="profilePauseCustomMinutes === null && profilePauseSelectedPreset === p.seconds
                              ? 'border-accent !text-accent' : ''"
                      @click="profilePauseSelectedPreset = p.seconds; profilePauseCustomMinutes = null">
                {{ p.label }}
              </button>
            </div>
            <div class="flex items-center gap-2 mt-2">
              <input type="number" min="1" v-model.number="profilePauseCustomMinutes"
                     class="input w-24" placeholder="Custom"
                     :class="profilePauseCustomMinutes !== null ? 'border-accent ring-1 ring-accent' : ''"
                     @input="profilePauseCustomMinutes = ($event.target as HTMLInputElement).valueAsNumber || null" />
              <span class="text-sm text-fg-muted">minutes</span>
            </div>
          </div>

          <div>
            <label class="label" for="pp-reason">Reason <span class="text-fg-subtle font-normal">(optional)</span></label>
            <input id="pp-reason" v-model="profilePauseReason" class="input"
                   placeholder="e.g. Temporary access" />
          </div>

          <div class="flex justify-end gap-2 pt-2">
            <button class="btn-secondary" @click="profilePauseTarget = null">Cancel</button>
            <button class="btn-primary" :disabled="profilePauseSubmitting" @click="activateProfilePause">
              {{ profilePauseSubmitting ? 'Pausing…' : 'Pause filtering' }}
            </button>
          </div>
        </template>
      </div>
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
  ClipboardDocumentListIcon, ClockIcon, PencilSquareIcon, PlusIcon, TrashIcon,
} from '@heroicons/vue/24/outline'
import {
  addProfileAllowlist, clearProfilePause, createProfile, deleteProfile,
  getProfilePause, listBlocklists, listProfileAllowlist, listProfiles,
  patchProfileBlockPage, removeProfileAllowlist, setProfilePause, updateProfile,
} from '@/api/endpoints'
import type { FwRuleScope } from '@/api/endpoints'
import type { Blocklist, PauseState, Profile, ProfileBlockPage } from '@/api/types'
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

// ─── Modal tab (edit mode only) ──────────────────────────────────────────

type ModalTab = 'settings' | 'allowlist' | 'blockpage'
const modalTab = ref<ModalTab>('settings')

// Allowlist tab state
const allowlistEntries = ref<string[]>([])
const allowlistLoading = ref(false)
const allowlistError = ref('')
const allowlistAddInput = ref('')
const allowlistAdding = ref(false)
const allowlistDeleting = ref<string | null>(null)

// Block page tab state (M33)
const blockPageTabForm = reactive<ProfileBlockPage>({
  title: '', message: '', contact_email: '', bypass_passcode: '',
})
const blockPageTabSaving = ref(false)
const blockPageTabSaved = ref(false)
const blockPageTabError = ref('')

async function saveBlockPageTab() {
  if (!original.value) return
  blockPageTabSaving.value = true
  blockPageTabError.value = ''
  blockPageTabSaved.value = false
  try {
    const patch: ProfileBlockPage = {}
    if (blockPageTabForm.title) patch.title = blockPageTabForm.title
    if (blockPageTabForm.message) patch.message = blockPageTabForm.message
    if (blockPageTabForm.contact_email) patch.contact_email = blockPageTabForm.contact_email
    if (blockPageTabForm.bypass_passcode) patch.bypass_passcode = blockPageTabForm.bypass_passcode
    await patchProfileBlockPage(original.value.id, patch)
    blockPageTabSaved.value = true
    setTimeout(() => { blockPageTabSaved.value = false }, 2000)
  } catch (err) {
    blockPageTabError.value = String(err)
  } finally {
    blockPageTabSaving.value = false
  }
}

function isWildcard(entry: string): boolean {
  // Wildcards are stored as "example.com" after normalisation, but displayed
  // as entered. We detect them if the user entered "*.example.com" — in that
  // case the stored form strips the prefix. For display we check original.
  // Since the API stores the normalised form, we cannot reliably detect them
  // post-round-trip unless we track the originals. Instead we check the raw
  // entry from the API: if it starts with "*." it is a wildcard.
  return entry.startsWith('*.')
}

async function openAllowlistTab() {
  modalTab.value = 'allowlist'
  if (!original.value) return
  allowlistError.value = ''
  allowlistLoading.value = true
  allowlistAddInput.value = ''
  try {
    allowlistEntries.value = await listProfileAllowlist(original.value.id)
  } catch (err) {
    allowlistError.value = errMsg(err, 'Failed to load allowlist')
  } finally {
    allowlistLoading.value = false
  }
}

async function submitAllowlistAdd() {
  const domain = allowlistAddInput.value.trim()
  if (!domain || !original.value) return
  allowlistError.value = ''
  allowlistAdding.value = true
  try {
    await addProfileAllowlist(original.value.id, domain)
    allowlistEntries.value = await listProfileAllowlist(original.value.id)
    allowlistAddInput.value = ''
    // Keep the profile list and Settings-tab textarea in sync.
    const idx = profiles.value.findIndex(p => p.id === original.value!.id)
    if (idx >= 0) profiles.value[idx] = { ...profiles.value[idx], allowlist: [...allowlistEntries.value] }
    form.allowlistText = allowlistEntries.value.join('\n')
  } catch (err) {
    allowlistError.value = errMsg(err, 'Failed to add domain')
  } finally {
    allowlistAdding.value = false
  }
}

async function deleteAllowlistEntry(entry: string) {
  if (!original.value) return
  allowlistError.value = ''
  allowlistDeleting.value = entry
  try {
    await removeProfileAllowlist(original.value.id, entry)
    allowlistEntries.value = await listProfileAllowlist(original.value.id)
    // Keep the profile list and Settings-tab textarea in sync.
    const idx = profiles.value.findIndex(p => p.id === original.value!.id)
    if (idx >= 0) profiles.value[idx] = { ...profiles.value[idx], allowlist: [...allowlistEntries.value] }
    form.allowlistText = allowlistEntries.value.join('\n')
  } catch (err) {
    allowlistError.value = errMsg(err, 'Failed to remove domain')
  } finally {
    allowlistDeleting.value = null
  }
}

// M6 — "Copy DoH-gap rules" modal scoped to a profile.
const fwRuleScope = ref<FwRuleScope | null>(null)
function openFwRules(scope: FwRuleScope) {
  fwRuleScope.value = scope
}

// ─── M13 — Per-profile pause ─────────────────────────────────────────────

const PAUSE_PRESETS = [
  { label: '15 min', seconds: 15 * 60 },
  { label: '30 min', seconds: 30 * 60 },
  { label: '1 hour', seconds: 3600 },
  { label: '2 hours', seconds: 7200 },
]

const profilePauses = reactive<Record<string, PauseState>>({})
const profilePauseTarget = ref<string | null>(null)
const profilePauseLoading = ref(false)
const profilePauseSubmitting = ref(false)
const profilePauseError = ref('')
const profilePauseSelectedPreset = ref(3600)
const profilePauseCustomMinutes = ref<number | null>(null)
const profilePauseReason = ref('')
const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null

const profilePauseDurationSeconds = computed(() =>
  profilePauseCustomMinutes.value != null && profilePauseCustomMinutes.value > 0
    ? Math.round(profilePauseCustomMinutes.value * 60)
    : profilePauseSelectedPreset.value,
)

function profileRemainingMs(id: string): number {
  const p = profilePauses[id]
  if (!p?.active || !p.resumes_at) return 0
  return Math.max(0, new Date(p.resumes_at).getTime() - now.value)
}

function formatRemaining(ms: number): string {
  const secs = Math.ceil(ms / 1000)
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  const s = secs % 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

async function openProfilePause(id: string) {
  profilePauseTarget.value = id
  profilePauseLoading.value = true
  profilePauseError.value = ''
  profilePauseSelectedPreset.value = 3600
  profilePauseCustomMinutes.value = null
  profilePauseReason.value = ''
  try {
    profilePauses[id] = await getProfilePause(id)
  } catch {
    // keep existing state if already loaded; otherwise show empty form
    if (!profilePauses[id]) profilePauses[id] = { active: false }
  } finally {
    profilePauseLoading.value = false
  }
}

async function activateProfilePause() {
  const id = profilePauseTarget.value
  if (!id) return
  profilePauseError.value = ''
  const secs = profilePauseDurationSeconds.value
  if (!secs || secs < 1) {
    profilePauseError.value = 'Enter a valid duration.'
    return
  }
  profilePauseSubmitting.value = true
  try {
    profilePauses[id] = await setProfilePause(id, secs, profilePauseReason.value || undefined)
    profilePauseTarget.value = null
  } catch (err) {
    profilePauseError.value = errMsg(err, 'Failed to pause filtering')
  } finally {
    profilePauseSubmitting.value = false
  }
}

async function resumeProfileFiltering() {
  const id = profilePauseTarget.value
  if (!id) return
  profilePauseSubmitting.value = true
  try {
    await clearProfilePause(id)
    profilePauses[id] = { active: false }
    profilePauseTarget.value = null
  } catch (err) {
    profilePauseError.value = errMsg(err, 'Failed to resume filtering')
  } finally {
    profilePauseSubmitting.value = false
  }
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
  modalTab.value = 'settings'
  Object.assign(form, emptyForm())
  formError.value = ''
  showModal.value = true
}

function openEdit(p: Profile) {
  mode.value = 'edit'
  modalTab.value = 'settings'
  allowlistEntries.value = p.allowlist ?? []
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
  // M33 — block page overrides
  blockPageTabForm.title = p.block_page?.title ?? ''
  blockPageTabForm.message = p.block_page?.message ?? ''
  blockPageTabForm.contact_email = p.block_page?.contact_email ?? ''
  blockPageTabForm.bypass_passcode = p.block_page?.bypass_passcode ?? ''
  blockPageTabError.value = ''
  blockPageTabSaved.value = false
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
  if (profilePauseTarget.value) { profilePauseTarget.value = null; return }
  if (pendingDelete.value) { pendingDelete.value = null; return }
  if (showModal.value && !submitting.value) { showModal.value = false }
}

onMounted(async () => {
  await refresh()
  ticker = setInterval(() => { now.value = Date.now() }, 1000)
  // Pre-load pause states for all profiles so the table column reflects reality.
  for (const p of profiles.value) {
    getProfilePause(p.id).then(s => { profilePauses[p.id] = s }).catch(() => {})
  }
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  if (ticker) clearInterval(ticker)
  window.removeEventListener('keydown', onKey)
})
</script>
