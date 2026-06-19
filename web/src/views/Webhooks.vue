<template>
  <div class="space-y-4">
    <!-- Toolbar -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">Webhooks</h1>
      <div class="flex items-center gap-2">
        <button class="btn-ghost" :disabled="loading" @click="refresh">
          <ArrowPathIcon class="h-4 w-4" :class="{ 'animate-spin': loading }" />
          <span>Refresh</span>
        </button>
        <button class="btn-primary" @click="openCreate">
          <PlusIcon class="h-4 w-4" />
          <span>Add endpoint</span>
        </button>
      </div>
    </div>

    <!-- Info banner -->
    <div class="card p-4 flex gap-3 text-sm text-fg-muted">
      <InformationCircleIcon class="h-5 w-5 text-accent shrink-0 mt-0.5" />
      <div>
        skoed delivers JSON events to registered URLs via HTTP POST.
        Each delivery is signed with <code class="font-mono text-accent2">X-Skoed-Signature: sha256=&lt;hex&gt;</code> when a secret is set.
        Retries: 0 s · 1 s · 4 s · 16 s.
      </div>
    </div>

    <!-- Error -->
    <p v-if="error" class="card p-3 text-sm text-danger border border-danger/30">{{ error }}</p>

    <!-- Empty -->
    <p v-if="!loading && endpoints.length === 0 && !error"
       class="card p-8 text-sm text-fg-muted text-center">
      No webhook endpoints yet. Add one to receive real-time push alerts.
    </p>

    <!-- Endpoint list -->
    <div v-if="endpoints.length > 0" class="card overflow-hidden">
      <table class="table">
        <thead>
          <tr>
            <th>URL</th>
            <th>Events</th>
            <th>Status</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="ep in endpoints" :key="ep.id">
            <td class="font-mono text-xs max-w-[280px] truncate" :title="ep.url">{{ ep.url }}</td>
            <td>
              <div class="flex flex-wrap gap-1">
                <span v-for="ev in ep.events" :key="ev"
                      class="badge bg-accent-subtle text-accent text-xs">{{ ev }}</span>
              </div>
            </td>
            <td>
              <span v-if="ep.enabled" class="badge bg-success/15 text-success border border-success/30">enabled</span>
              <span v-else class="badge bg-bg-hover text-fg-muted border border-border">disabled</span>
            </td>
            <td class="text-right whitespace-nowrap">
              <div class="flex items-center justify-end gap-1">
                <button class="btn-ghost text-xs"
                        :disabled="testing === ep.id"
                        :title="`Test fire ${ep.url}`"
                        @click="testFire(ep.id)">
                  <BoltIcon class="h-4 w-4" :class="{ 'animate-pulse': testing === ep.id }" />
                  <span>Test</span>
                </button>
                <button class="btn-ghost text-xs text-danger"
                        :disabled="deleting === ep.id"
                        @click="confirmDelete(ep)">
                  <TrashIcon class="h-4 w-4" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Test result toast -->
    <div v-if="testResult"
         class="fixed bottom-4 right-4 z-50 card p-3 text-sm max-w-xs border"
         :class="testResult.ok ? 'border-success/40 text-success' : 'border-danger/40 text-danger'">
      <div class="flex items-start gap-2">
        <CheckCircleIcon v-if="testResult.ok" class="h-4 w-4 shrink-0 mt-0.5" />
        <XCircleIcon v-else class="h-4 w-4 shrink-0 mt-0.5" />
        <span>{{ testResult.message }}</span>
      </div>
    </div>

    <!-- Create modal -->
    <div v-if="showCreate"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="closeCreate">
      <form class="card max-w-lg w-full p-6 space-y-4" @submit.prevent="submitCreate">
        <h2 class="text-base font-semibold text-fg-strong">Add webhook endpoint</h2>

        <div class="space-y-1">
          <label class="text-sm font-medium text-fg">URL <span class="text-danger">*</span></label>
          <input v-model="form.url" type="url" required placeholder="https://example.com/hook"
                 class="input w-full" />
        </div>

        <div class="space-y-1">
          <label class="text-sm font-medium text-fg">Secret <span class="text-xs text-fg-muted">(optional — enables HMAC signing)</span></label>
          <input v-model="form.secret" type="text" placeholder="my-secret-key"
                 class="input w-full font-mono" autocomplete="off" />
        </div>

        <div class="space-y-2">
          <label class="text-sm font-medium text-fg">Events <span class="text-danger">*</span></label>
          <div class="grid grid-cols-2 gap-1.5">
            <label v-for="ev in AVAILABLE_EVENTS" :key="ev"
                   class="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox" :value="ev" v-model="form.events"
                     class="rounded border-border" />
              <span class="font-mono text-xs">{{ ev }}</span>
            </label>
          </div>
          <p v-if="form.events.length === 0" class="text-xs text-danger">Select at least one event.</p>
        </div>

        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" v-model="form.enabled" class="rounded border-border" />
          <span>Enabled</span>
        </label>

        <p v-if="createError" class="text-sm text-danger">{{ createError }}</p>

        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn-secondary" @click="closeCreate">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="creating || form.events.length === 0">
            {{ creating ? 'Adding…' : 'Add endpoint' }}
          </button>
        </div>
      </form>
    </div>

    <!-- Delete confirm modal -->
    <div v-if="deleteTarget"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="deleteTarget = null">
      <div class="card max-w-md w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Remove webhook endpoint?</h2>
        <p class="text-sm text-fg-muted">
          This will permanently remove
          <span class="font-mono text-xs text-fg">{{ deleteTarget.url }}</span>.
          In-flight deliveries may still complete.
        </p>
        <div class="flex justify-end gap-2">
          <button class="btn-secondary" @click="deleteTarget = null">Cancel</button>
          <button class="btn-danger" :disabled="!!deleting" @click="doDelete">
            {{ deleting ? 'Removing…' : 'Remove' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ArrowPathIcon, BoltIcon, CheckCircleIcon, InformationCircleIcon,
  PlusIcon, TrashIcon, XCircleIcon,
} from '@heroicons/vue/24/outline'
import { createWebhook, deleteWebhook, listWebhooks, testWebhook } from '@/api/endpoints'
import type { WebhookEndpoint } from '@/api/types'

const AVAILABLE_EVENTS = [
  'device.new',
  'cluster.node_down',
  'cluster.node_rejoined',
  'blocklist.download_failed',
  'filter.pause_started',
  'filter.pause_expired',
]

const endpoints = ref<WebhookEndpoint[]>([])
const loading = ref(false)
const error = ref('')

const showCreate = ref(false)
const creating = ref(false)
const createError = ref('')
const form = ref({ url: '', secret: '', events: [] as string[], enabled: true })

const deleteTarget = ref<WebhookEndpoint | null>(null)
const deleting = ref<string | null>(null)

const testing = ref<string | null>(null)
const testResult = ref<{ ok: boolean; message: string } | null>(null)

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    endpoints.value = await listWebhooks()
  } catch (e: unknown) {
    error.value = (e as Error).message ?? 'Failed to load webhooks'
  } finally {
    loading.value = false
  }
}

function openCreate() {
  form.value = { url: '', secret: '', events: [], enabled: true }
  createError.value = ''
  showCreate.value = true
}

function closeCreate() {
  showCreate.value = false
}

async function submitCreate() {
  if (form.value.events.length === 0) return
  creating.value = true
  createError.value = ''
  try {
    const ep = await createWebhook({
      url: form.value.url,
      secret: form.value.secret,
      events: form.value.events,
      enabled: form.value.enabled,
    })
    endpoints.value.push(ep)
    closeCreate()
  } catch (e: unknown) {
    createError.value = (e as Error).message ?? 'Failed to create endpoint'
  } finally {
    creating.value = false
  }
}

function confirmDelete(ep: WebhookEndpoint) {
  deleteTarget.value = ep
}

async function doDelete() {
  if (!deleteTarget.value) return
  const id = deleteTarget.value.id
  deleting.value = id
  try {
    await deleteWebhook(id)
    endpoints.value = endpoints.value.filter(e => e.id !== id)
    deleteTarget.value = null
  } catch (e: unknown) {
    error.value = (e as Error).message ?? 'Failed to delete'
  } finally {
    deleting.value = null
  }
}

async function testFire(id: string) {
  testing.value = id
  testResult.value = null
  try {
    await testWebhook(id)
    testResult.value = { ok: true, message: 'webhook.test event fired — check your endpoint.' }
  } catch (e: unknown) {
    testResult.value = { ok: false, message: (e as Error).message ?? 'Test fire failed' }
  } finally {
    testing.value = null
    setTimeout(() => { testResult.value = null }, 5000)
  }
}

onMounted(refresh)
</script>
