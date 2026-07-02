<!-- Schedules.vue — M3 schedule management page. -->
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
                  </p>

                  <p v-if="bindingsLoading[s.id]" class="text-xs text-fg-subtle">Loading…</p>
                  <template v-else>
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
                    <p v-else class="text-xs text-fg-subtle">No bindings.</p>
                  </template>

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

        <!-- M37: Template presets -->
        <div>
          <span class="label">Presets</span>
          <div class="flex flex-wrap gap-2 text-xs">
            <button type="button" class="btn-secondary !py-1 !text-xs"
                    @click="applyPreset('weekdays')">Weekdays 00:00–24:00</button>
            <button type="button" class="btn-secondary !py-1 !text-xs"
                    @click="applyPreset('weekends')">Weekends 00:00–24:00</button>
            <button type="button" class="btn-secondary !py-1 !text-xs"
                    @click="applyPreset('bedtime')">Bedtime 21:00–07:00</button>
            <button type="button" class="btn-secondary !py-1 !text-xs"
                    @click="applyPreset('school')">School Hours 08:00–15:00</button>
            <button type="button" class="btn-secondary !py-1 !text-xs"
                    @click="applyPreset('allweek')">All week 00:00–24:00</button>
          </div>
        </div>

        <!-- M37: Visual 7×24 grid editor -->
        <div>
          <div class="flex items-center justify-between mb-1">
            <span class="label !mb-0">Visual editor</span>
            <span class="text-xs text-fg-muted">Click or drag to toggle hour slots</span>
          </div>
          <div class="overflow-x-auto">
            <table class="text-[9px] w-full border-collapse"
                   @mouseup="stopDrag()" @mouseleave="stopDrag()">
              <thead>
                <tr>
                  <th class="pr-1 text-right text-fg-muted w-5">h</th>
                  <th v-for="d in DAYS" :key="d"
                      class="text-center text-fg-muted font-medium pb-0.5" style="min-width:24px">
                    {{ d.slice(0,2) }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="h in 24" :key="h - 1">
                  <td class="pr-1 text-right text-fg-muted">{{ String(h - 1).padStart(2,'0') }}</td>
                  <td v-for="d in DAYS" :key="d"
                      :class="[
                        'border border-border/30 select-none',
                        isDragging ? 'cursor-crosshair' : 'cursor-pointer',
                        gridCellActive(d, h - 1) ? 'bg-accent/60' : 'bg-bg hover:bg-accent/20'
                      ]"
                      style="height:10px"
                      @mousedown.prevent="startDrag(d, h - 1)"
                      @mouseover="doDrag(d, h - 1)" />
                </tr>
              </tbody>
            </table>
          </div>
          <p class="text-xs text-fg-muted mt-1">
            Blue = active hours. Changes sync to the windows list below.
          </p>
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
  listBlocklists, listProfiles, listScheduleBindings, listSchedules, updateSchedule,
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

const expandedId = ref<string | null>(null)
const bindings = reactive<Record<string, ScheduleBinding[]>>({})
const bindingsLoading = reactive<Record<string, boolean>>({})
const bindingDraft = reactive<Record<string, { profile_id: string; blocklist_id: string }>>({})
const bindingSubmitting = reactive<Record<string, boolean>>({})
const bindingErrors = reactive<Record<string, string>>({})
const bindingRemoving = reactive<Record<string, boolean>>({})

// Grid drag state
const isDragging = ref(false)
const dragMode = ref(true) // true = activating cells, false = deactivating

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
    for (const s of scheds) ensureBindingDraft(s.id)
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

// ─── M37: Template presets ────────────────────────────────────────────────────

const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri'] as const
const WEEKEND_DAYS = ['Sat', 'Sun'] as const

const PRESETS: Record<string, { windows: Array<{ days: string[]; start: string; end: string }> }> = {
  weekdays:  { windows: [{ days: [...WEEKDAYS],      start: '00:00', end: '23:59' }] },
  weekends:  { windows: [{ days: [...WEEKEND_DAYS],  start: '00:00', end: '23:59' }] },
  bedtime:   { windows: [{ days: [...DAYS],          start: '21:00', end: '07:00' }] },
  school:    { windows: [{ days: [...WEEKDAYS],      start: '08:00', end: '15:00' }] },
  allweek:   { windows: [{ days: [...DAYS],          start: '00:00', end: '23:59' }] },
}

function applyPreset(name: string) {
  const preset = PRESETS[name]
  if (!preset) return
  form.windows = preset.windows.map(w => ({ days: [...w.days], start: w.start, end: w.end }))
}

// ─── M37: Visual 7×24 grid ────────────────────────────────────────────────────

function parseHHMM(s: string): number {
  const [h, m] = s.split(':').map(Number)
  return (h || 0) * 60 + (m || 0)
}

function gridCellActive(day: string, hour: number): boolean {
  // Check if the given day+hour falls inside any window.
  const curMin = hour * 60
  for (const w of form.windows) {
    if (!w.days.includes(day)) continue
    const s = parseHHMM(w.start)
    const e = parseHHMM(w.end)
    if (s === e) continue
    if (s < e) {
      if (curMin >= s && curMin < e) return true
    } else {
      // Wraps midnight
      if (curMin >= s || curMin < e) return true
    }
  }
  return false
}

function toggleGridCell(day: string, hour: number) {
  const wasActive = gridCellActive(day, hour)
  if (wasActive) {
    // Remove this hour from all windows that cover it for this day.
    const newWindows: typeof form.windows = []
    for (const w of form.windows) {
      if (!w.days.includes(day)) { newWindows.push(w); continue }
      const s = parseHHMM(w.start)
      const e = parseHHMM(w.end)
      // If this single-day window covers the hour, split or remove.
      const windowStart = s
      const windowEnd = e === 0 ? 24 * 60 : e
      if (hour * 60 >= windowStart && hour * 60 < windowEnd) {
        // Remove this day from the window; create sub-windows if needed.
        const otherDays = w.days.filter(d => d !== day)
        if (otherDays.length > 0) {
          newWindows.push({ days: otherDays, start: w.start, end: w.end })
        }
        // Add back the same window for this day without the toggled hour.
        if (hour * 60 > windowStart) {
          newWindows.push({ days: [day], start: w.start, end: `${String(hour).padStart(2,'0')}:00` })
        }
        const afterHour = hour + 1
        if (afterHour * 60 < windowEnd) {
          newWindows.push({ days: [day], start: `${String(afterHour).padStart(2,'0')}:00`, end: w.end })
        }
      } else {
        newWindows.push(w)
      }
    }
    form.windows = newWindows
  } else {
    // Activate: add or extend a window for this day+hour.
    const start = `${String(hour).padStart(2,'0')}:00`
    const end = hour === 23 ? '23:59' : `${String(hour + 1).padStart(2,'0')}:00`
    // Try merging with an adjacent window for the same day.
    let merged = false
    for (const w of form.windows) {
      if (!w.days.includes(day) || w.days.length > 1) continue
      if (w.end === start) { w.end = end; merged = true; break }
      if (w.start === end) { w.start = start; merged = true; break }
    }
    if (!merged) form.windows.push({ days: [day], start, end })
  }
}

function setGridCell(day: string, hour: number, active: boolean) {
  if (gridCellActive(day, hour) !== active) toggleGridCell(day, hour)
}

// After a drag, rebuild form.windows from the active-hour set so that
// any fragmentation caused by fast mouse movement (skipped mouseover events)
// is resolved into the minimal set of contiguous windows.
function consolidateWindows() {
  const activeHours: Record<string, Set<number>> = {}
  for (const w of form.windows) {
    const startMin = parseHHMM(w.start)
    const endMinNorm = w.end === '23:59' ? 24 * 60 : parseHHMM(w.end)
    for (const d of w.days) {
      if (!activeHours[d]) activeHours[d] = new Set()
      for (let min = startMin; min < endMinNorm; min += 60) activeHours[d].add(min / 60)
    }
  }
  const result: TimeWindow[] = []
  for (const [d, hours] of Object.entries(activeHours)) {
    const sorted = [...hours].sort((a, b) => a - b)
    let i = 0
    while (i < sorted.length) {
      const startH = sorted[i]
      let endH = startH + 1
      while (i + 1 < sorted.length && sorted[i + 1] === endH) { i++; endH++ }
      result.push({
        days: [d],
        start: `${String(startH).padStart(2, '0')}:00`,
        end: endH > 23 ? '23:59' : `${String(endH).padStart(2, '0')}:00`,
      })
      i++
    }
  }
  form.windows = result
}

function startDrag(day: string, hour: number) {
  isDragging.value = true
  dragMode.value = !gridCellActive(day, hour)
  setGridCell(day, hour, dragMode.value)
}

function doDrag(day: string, hour: number) {
  if (!isDragging.value) return
  setGridCell(day, hour, dragMode.value)
}

function stopDrag() {
  if (isDragging.value) {
    isDragging.value = false
    consolidateWindows()
  }
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

async function toggleBindings(id: string) {
  ensureBindingDraft(id)
  if (expandedId.value === id) {
    expandedId.value = null
    return
  }
  expandedId.value = id
  if (bindings[id]) return  // already loaded
  bindingsLoading[id] = true
  try {
    bindings[id] = await listScheduleBindings(id)
  } catch {
    bindings[id] = []
  } finally {
    bindingsLoading[id] = false
  }
}

async function submitBinding(id: string) {
  const draft = bindingDraft[id]
  if (!draft?.profile_id || !draft?.blocklist_id) return
  bindingErrors[id] = ''

  // M37: conflict validation — check if this (profile, blocklist) pair is already
  // bound to *another* schedule. Multiple bindings for the same pair create confusing
  // overlap behaviour; warn the operator before allowing it.
  const conflictScheduleId = checkBindingConflict(id, draft.profile_id, draft.blocklist_id)
  if (conflictScheduleId) {
    const cs = schedules.value.find(s => s.id === conflictScheduleId)
    bindingErrors[id] = `Conflict: this profile+blocklist pair is already bound to schedule "${cs?.name ?? conflictScheduleId}". Multiple bindings can cause unexpected interactions.`
    return
  }

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

// M37: check if (profileId, blocklistId) is already bound to a different schedule.
// Returns the conflicting schedule ID, or null if clean.
function checkBindingConflict(currentScheduleId: string, profileId: string, blocklistId: string): string | null {
  for (const [sid, bs] of Object.entries(bindings)) {
    if (sid === currentScheduleId) continue
    for (const b of (bs || [])) {
      if (b.profile_id === profileId && b.blocklist_id === blocklistId) {
        return sid
      }
    }
  }
  return null
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
  window.addEventListener('mouseup', stopDrag)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('mouseup', stopDrag)
})
</script>
