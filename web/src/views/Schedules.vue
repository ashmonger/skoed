<!--
  Schedules.vue — M3 schedule management page.

  Known M3.1 gap: the management API exposes addScheduleBinding /
  deleteScheduleBinding but no "list bindings" endpoint. Bindings live in
  the cluster snapshot (reachable only via /api/v1/config/export as YAML).
  Rather than ship a browser-side YAML parser, this page keeps a
  per-schedule in-memory list of bindings the operator has added/removed
  during the current session. On reload the list starts empty until the
  operator adds new ones. This is documented in ROADMAP.md as an M3.1 gap
  and will be fixed when GET /api/v1/schedules/{id}/bindings lands.
-->
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
      <h1 class="text-lg font-semibold text-fg-strong">Schedules</h1>
      <button class="btn-primary" @click="openCreate">
        <PlusIcon class="h-4 w-4" /> New schedule
      </button>
    </div>

    <!-- Table card -->
    <div class="card p-4">
      <p v-if="loading" class="text-sm text-fg-muted py-6 text-center">Loading…</p>

      <div v-else-if="schedules.length === 0" class="py-12 text-center space-y-3">
        <p class="text-sm text-fg-muted">No schedules yet.</p>
        <button class="btn-primary" @click="openCreate">
          <PlusIcon class="h-4 w-4" /> Create your first schedule
        </button>
      </div>

      <table v-else class="table">
        <thead>
          <tr>
            <th>ID</th>
            <th>Name</th>
            <th>Mode</th>
            <th>Windows</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="s in schedules" :key="s.id">
            <tr class="cursor-pointer" @click="toggleBindings(s.id)">
              <td class="font-mono text-xs text-fg-muted">{{ s.id }}</td>
              <td class="font-medium text-fg-strong">{{ s.name }}</td>
              <td>
                <span :class="s.mode === 'block_only_inside' ? 'badge-danger' : 'badge-success'">
                  {{ modeLabel(s.mode) }}
                </span>
              </td>
              <td class="text-xs text-fg-muted">
                <span v-if="(s.windows?.length ?? 0) === 0" class="text-fg-subtle">no windows</span>
                <span v-else>{{ summarizeWindows(s.windows) }}</span>
              </td>
              <td class="text-right whitespace-nowrap" @click.stop>
                <button class="btn-ghost"
                        title="Bindings"
                        @click="toggleBindings(s.id)">
                  <ClockIcon class="h-4 w-4" />
                </button>
                <button class="btn-ghost"
                        title="Edit schedule"
                        @click="openEdit(s)">
                  <PencilSquareIcon class="h-4 w-4" />
                </button>
                <button class="btn-ghost text-danger"
                        title="Delete schedule"
                        @click="askDelete(s)">
                  <TrashIcon class="h-4 w-4" />
                </button>
              </td>
            </tr>
            <tr v-if="rowErrors[s.id]">
              <td colspan="5" class="!py-1 text-xs text-danger">{{ rowErrors[s.id] }}</td>
            </tr>
            <!-- Bindings panel (expanded inline) -->
            <tr v-if="expandedId === s.id">
              <td colspan="5" class="bg-bg-hover">
                <div class="space-y-3 p-2">
                  <p class="text-xs text-fg-muted">
                    Bindings attach this schedule to a (profile, blocklist) pair.
                    Existing bindings are not listed (no server-side enumeration endpoint
                    yet — known M3.1 gap); only ones added in this session are shown.
                  </p>

                  <ul v-if="(bindings[s.id]?.length ?? 0) > 0" class="space-y-1">
                    <li v-for="b in bindings[s.id]"
                        :key="`${b.profile_id}|${b.blocklist_id}`"
                        class="flex items-center gap-2 text-xs">
                      <span class="badge-accent font-mono">{{ b.profile_id }}</span>
                      <span class="text-fg-subtle">→</span>
                      <span class="badge-accent font-mono">{{ b.blocklist_id }}</span>
                      <button class="btn-ghost text-danger ml-auto !py-0 !px-1"
                              title="Remove binding"
                              :disabled="!!bindingRemoving[`${s.id}|${b.profile_id}|${b.blocklist_id}`]"
                              @click="removeBinding(s.id, b.profile_id, b.blocklist_id)">
                        <XMarkIcon class="h-3 w-3" />
                      </button>
                    </li>
                  </ul>
                  <p v-else class="text-xs text-fg-subtle">No bindings tracked in this session.</p>

                  <form class="flex flex-wrap items-end gap-2"
                        @submit.prevent="submitBinding(s.id)">
                    <div class="flex-1 min-w-[10rem]">
                      <label class="label text-xs" :for="`bind-pf-${s.id}`">Profile</label>
                      <select :id="`bind-pf-${s.id}`"
                              v-model="bindingDraft[s.id].profile_id"
                              class="input">
                        <option value="" disabled>Select profile…</option>
                        <option v-for="p in profileOptions" :key="p.id" :value="p.id">
                          {{ p.name }} ({{ p.id }})
                        </option>
                      </select>
                    </div>
                    <div class="flex-1 min-w-[10rem]">
                      <label class="label text-xs" :for="`bind-bl-${s.id}`">Blocklist</label>
                      <select :id="`bind-bl-${s.id}`"
                              v-model="bindingDraft[s.id].blocklist_id"
                              class="input">
                        <option value="" disabled>Select blocklist…</option>
                        <option v-for="bl in blocklistOptions" :key="bl.id" :value="bl.id">
                          {{ bl.name }} ({{ bl.id }})
                        </option>
                      </select>
                    </div>
                    <button type="submit" class="btn-primary"
                            :disabled="!bindingDraft[s.id].profile_id
                                       || !bindingDraft[s.id].blocklist_id
                                       || bindingSubmitting[s.id]">
                      <PlusIcon class="h-4 w-4" />
                      {{ bindingSubmitting[s.id] ? 'Saving…' : 'Add binding' }}
                    </button>
                  </form>
                  <p v-if="bindingErrors[s.id]" class="text-xs text-danger">
                    {{ bindingErrors[s.id] }}
                  </p>
                </div>
              </td>
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
          {{ modalMode === 'create' ? 'New schedule' : `Edit ${original?.name ?? ''}` }}
        </h2>

        <p v-if="formError" class="text-sm text-danger">{{ formError }}</p>

        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="label" for="sc-id">
              ID
              <span v-if="modalMode === 'edit'" class="text-fg-subtle font-normal">(read-only)</span>
            </label>
            <input id="sc-id" v-model="form.id"
                   class="input font-mono text-xs"
                   :disabled="modalMode === 'edit'"
                   :required="modalMode === 'create'"
                   placeholder="e.g. evening-clamp" />
          </div>
          <div>
            <label class="label" for="sc-name">Name</label>
            <input id="sc-name" v-model="form.name" class="input" required
                   placeholder="e.g. Evening clamp" />
          </div>
        </div>

        <div>
          <span class="label">Mode</span>
          <div class="flex flex-col gap-1 text-sm">
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="block_only_inside" v-model="form.mode" />
              <span>Block only inside window</span>
              <span class="text-fg-subtle text-xs">(filter applies during the window)</span>
            </label>
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="allow_only_inside" v-model="form.mode" />
              <span>Allow only inside window</span>
              <span class="text-fg-subtle text-xs">(filter applies outside the window)</span>
            </label>
          </div>
        </div>

        <div>
          <div class="flex items-center justify-between mb-1">
            <span class="label !mb-0">Windows</span>
            <button type="button" class="btn-secondary !py-1" @click="addWindow">
              <PlusIcon class="h-4 w-4" /> Add window
            </button>
          </div>
          <p v-if="form.windows.length === 0"
             class="text-xs text-fg-subtle py-2">
            No windows. Add at least one to define when this schedule applies.
          </p>
          <div v-else class="space-y-3">
            <div v-for="(w, idx) in form.windows" :key="idx"
                 class="border border-border rounded p-3 space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-fg-muted">Window #{{ idx + 1 }}</span>
                <button type="button"
                        class="btn-ghost text-danger !py-0 !px-1"
                        title="Remove window"
                        @click="removeWindow(idx)">
                  <XMarkIcon class="h-4 w-4" />
                </button>
              </div>
              <div class="flex flex-wrap gap-3 text-sm">
                <label v-for="d in DAYS" :key="d"
                       class="inline-flex items-center gap-1">
                  <input type="checkbox" :value="d" v-model="w.days" />
                  {{ d }}
                </label>
              </div>
              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label class="label text-xs" :for="`win-start-${idx}`">Start</label>
                  <input :id="`win-start-${idx}`"
                         type="time"
                         v-model="w.start"
                         class="input" />
                </div>
                <div>
                  <label class="label text-xs" :for="`win-end-${idx}`">End</label>
                  <input :id="`win-end-${idx}`"
                         type="time"
                         v-model="w.end"
                         class="input" />
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn-secondary" @click="closeModal">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="submitting">
            {{ submitting
                ? (modalMode === 'create' ? 'Creating…' : 'Saving…')
                : (modalMode === 'create' ? 'Create schedule' : 'Save changes') }}
          </button>
        </div>
      </form>
    </div>

    <!-- Delete confirmation modal -->
    <div v-if="pendingDelete"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="pendingDelete = null">
      <div class="card max-w-lg w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Delete schedule?</h2>
        <p class="text-sm text-fg">
          Delete schedule <span class="font-semibold">{{ pendingDelete.name }}</span>?
          <span class="text-fg-muted">Any bindings attached to it will be removed server-side.</span>
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
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ClockIcon, PencilSquareIcon, PlusIcon, TrashIcon, XMarkIcon,
} from '@heroicons/vue/24/outline'
import {
  addScheduleBinding, createSchedule, deleteSchedule, deleteScheduleBinding,
  listBlocklists, listProfiles, listSchedules, updateSchedule,
} from '@/api/endpoints'
import type {
  Blocklist, Profile, Schedule, ScheduleBinding, ScheduleMode, TimeWindow,
} from '@/api/types'

// ─── Constants ───────────────────────────────────────────────────────────

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'] as const
const DAY_ORDER: Record<string, number> = {
  Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6,
}

// ─── State ───────────────────────────────────────────────────────────────

const schedules = ref<Schedule[]>([])
const profileOptions = ref<Profile[]>([])
const blocklistOptions = ref<Blocklist[]>([])
const loading = ref(true)
const lastError = ref('')
const rowErrors = reactive<Record<string, string>>({})

const showModal = ref(false)
const modalMode = ref<'create' | 'edit'>('create')
const submitting = ref(false)
const formError = ref('')
const original = ref<Schedule | null>(null)

interface FormState {
  id: string
  name: string
  mode: ScheduleMode
  windows: TimeWindow[]
}

const emptyForm = (): FormState => ({
  id: '',
  name: '',
  mode: 'block_only_inside',
  windows: [],
})
const form = reactive<FormState>(emptyForm())

const pendingDelete = ref<Schedule | null>(null)
const deleting = ref(false)

// Bindings: tracked locally per schedule because no list endpoint exists.
const expandedId = ref<string | null>(null)
const bindings = reactive<Record<string, ScheduleBinding[]>>({})
const bindingDraft = reactive<Record<string, { profile_id: string; blocklist_id: string }>>({})
const bindingSubmitting = reactive<Record<string, boolean>>({})
const bindingErrors = reactive<Record<string, string>>({})
const bindingRemoving = reactive<Record<string, boolean>>({})

// ─── Helpers ─────────────────────────────────────────────────────────────

function modeLabel(m: ScheduleMode): string {
  return m === 'block_only_inside' ? 'block in window' : 'allow in window'
}

function newWindow(): TimeWindow {
  return { days: [], start: '00:00', end: '00:00' }
}

function summarizeWindows(windows: TimeWindow[]): string {
  return windows.map(formatWindow).join('; ')
}

function formatWindow(w: TimeWindow): string {
  const days = [...(w.days ?? [])].sort((a, b) => (DAY_ORDER[a] ?? 99) - (DAY_ORDER[b] ?? 99))
  return `${compressDays(days)} ${w.start}-${w.end}`
}

// Compress consecutive day ranges, e.g. Mon,Tue,Wed,Thu,Fri → "Mon-Fri".
function compressDays(days: string[]): string {
  if (days.length === 0) return '(no days)'
  const indexed = days.map(d => ({ d, i: DAY_ORDER[d] ?? -1 })).filter(x => x.i >= 0)
  if (indexed.length === 0) return days.join(',')
  const parts: string[] = []
  let runStart = indexed[0]
  let runEnd = indexed[0]
  for (let k = 1; k < indexed.length; k++) {
    const cur = indexed[k]
    if (cur.i === runEnd.i + 1) {
      runEnd = cur
    } else {
      parts.push(runStart.d === runEnd.d ? runStart.d : `${runStart.d}-${runEnd.d}`)
      runStart = cur
      runEnd = cur
    }
  }
  parts.push(runStart.d === runEnd.d ? runStart.d : `${runStart.d}-${runEnd.d}`)
  return parts.join(',')
}

function ensureBindingDraft(id: string) {
  if (!bindingDraft[id]) {
    bindingDraft[id] = { profile_id: '', blocklist_id: '' }
  }
}

// ─── Data loading ────────────────────────────────────────────────────────

async function refresh() {
  loading.value = true
  try {
    const [scheds, profs, bls] = await Promise.all([
      listSchedules(), listProfiles(), listBlocklists(),
    ])
    schedules.value = scheds
    profileOptions.value = profs
    blocklistOptions.value = bls
    for (const s of scheds) {
      if (!bindings[s.id]) bindings[s.id] = []
      ensureBindingDraft(s.id)
    }
    lastError.value = ''
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load schedules')
  } finally {
    loading.value = false
  }
}

// ─── Modal: create / edit ────────────────────────────────────────────────

function openCreate() {
  modalMode.value = 'create'
  original.value = null
  Object.assign(form, emptyForm())
  form.windows = [newWindow()]
  formError.value = ''
  showModal.value = true
}

function openEdit(s: Schedule) {
  modalMode.value = 'edit'
  original.value = s
  form.id = s.id
  form.name = s.name
  form.mode = s.mode
  form.windows = (s.windows ?? []).map(w => ({
    days: [...(w.days ?? [])],
    start: w.start,
    end: w.end,
  }))
  formError.value = ''
  showModal.value = true
}

function closeModal() {
  if (submitting.value) return
  showModal.value = false
}

function addWindow() {
  form.windows.push(newWindow())
}

function removeWindow(idx: number) {
  form.windows.splice(idx, 1)
}

async function submitModal() {
  formError.value = ''
  if (!form.name.trim()) {
    formError.value = 'Name is required.'
    return
  }
  if (modalMode.value === 'create' && !form.id.trim()) {
    formError.value = 'ID is required.'
    return
  }
  for (let i = 0; i < form.windows.length; i++) {
    const w = form.windows[i]
    if (w.days.length === 0) {
      formError.value = `Window #${i + 1}: pick at least one day.`
      return
    }
    if (!w.start || !w.end) {
      formError.value = `Window #${i + 1}: start and end times are required.`
      return
    }
  }

  const payload: Schedule = {
    id: form.id.trim(),
    name: form.name.trim(),
    mode: form.mode,
    windows: form.windows.map(w => ({
      days: [...w.days],
      start: w.start,
      end: w.end,
    })),
  }

  submitting.value = true
  try {
    if (modalMode.value === 'create') {
      const created = await createSchedule(payload)
      schedules.value = [...schedules.value, created]
      if (!bindings[created.id]) bindings[created.id] = []
      ensureBindingDraft(created.id)
    } else if (original.value) {
      const o = original.value
      const patch: Partial<Schedule> = {}
      if (payload.name !== o.name) patch.name = payload.name
      if (payload.mode !== o.mode) patch.mode = payload.mode
      // Windows: send if changed (deep-ish compare via JSON).
      if (JSON.stringify(payload.windows) !== JSON.stringify(o.windows ?? [])) {
        patch.windows = payload.windows
      }
      if (Object.keys(patch).length === 0) {
        showModal.value = false
        return
      }
      const updated = await updateSchedule(o.id, patch)
      const idx = schedules.value.findIndex(s => s.id === o.id)
      if (idx >= 0) schedules.value.splice(idx, 1, updated)
    }
    showModal.value = false
  } catch (err) {
    formError.value = errMsg(err,
      modalMode.value === 'create' ? 'Failed to create schedule' : 'Failed to save schedule')
  } finally {
    submitting.value = false
  }
}

// ─── Delete ──────────────────────────────────────────────────────────────

function askDelete(s: Schedule) {
  pendingDelete.value = s
}

async function confirmDelete() {
  const s = pendingDelete.value
  if (!s) return
  deleting.value = true
  rowErrors[s.id] = ''
  try {
    await deleteSchedule(s.id)
    schedules.value = schedules.value.filter(x => x.id !== s.id)
    delete bindings[s.id]
    delete bindingDraft[s.id]
    if (expandedId.value === s.id) expandedId.value = null
    pendingDelete.value = null
  } catch (err) {
    const msg = errMsg(err, 'Failed to delete schedule')
    rowErrors[s.id] = msg
    lastError.value = msg
  } finally {
    deleting.value = false
  }
}

// ─── Bindings ────────────────────────────────────────────────────────────

function toggleBindings(id: string) {
  ensureBindingDraft(id)
  if (!bindings[id]) bindings[id] = []
  expandedId.value = expandedId.value === id ? null : id
}

async function submitBinding(id: string) {
  const draft = bindingDraft[id]
  if (!draft?.profile_id || !draft?.blocklist_id) return
  bindingErrors[id] = ''
  bindingSubmitting[id] = true
  try {
    const created = await addScheduleBinding(id, draft.profile_id, draft.blocklist_id)
    if (!bindings[id]) bindings[id] = []
    // Skip duplicates if the same pair was already tracked locally.
    const dup = bindings[id].some(
      b => b.profile_id === created.profile_id && b.blocklist_id === created.blocklist_id,
    )
    if (!dup) bindings[id].push(created)
    bindingDraft[id] = { profile_id: '', blocklist_id: '' }
  } catch (err) {
    bindingErrors[id] = errMsg(err, 'Failed to add binding')
  } finally {
    bindingSubmitting[id] = false
  }
}

async function removeBinding(id: string, profile_id: string, blocklist_id: string) {
  const key = `${id}|${profile_id}|${blocklist_id}`
  if (bindingRemoving[key]) return
  bindingErrors[id] = ''
  bindingRemoving[key] = true
  try {
    await deleteScheduleBinding(id, profile_id, blocklist_id)
    bindings[id] = (bindings[id] ?? []).filter(
      b => !(b.profile_id === profile_id && b.blocklist_id === blocklist_id),
    )
  } catch (err) {
    bindingErrors[id] = errMsg(err, 'Failed to remove binding')
  } finally {
    delete bindingRemoving[key]
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
