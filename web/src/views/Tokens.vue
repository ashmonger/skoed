<template>
  <div class="space-y-4">
    <!-- Toolbar -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">API Tokens</h1>
      <div class="flex items-center gap-2">
        <button class="btn-ghost" :disabled="loading" @click="refresh">
          <ArrowPathIcon class="h-4 w-4" :class="{ 'animate-spin': loading }" />
          <span>Refresh</span>
        </button>
        <button class="btn-primary" @click="openCreate">
          <PlusIcon class="h-4 w-4" />
          <span>Issue token</span>
        </button>
      </div>
    </div>

    <!-- Info banner -->
    <div class="card p-4 flex gap-3 text-sm text-fg-muted">
      <InformationCircleIcon class="h-5 w-5 text-accent shrink-0 mt-0.5" />
      <div>
        API tokens let scripts and integrations call the skoed API without your session password.
        The raw token value is shown <strong class="text-fg">once</strong> at creation — store it securely.
        Pass it as <code class="font-mono text-accent2">Authorization: Bearer &lt;token&gt;</code>.
      </div>
    </div>

    <!-- Error -->
    <p v-if="error" class="card p-3 text-sm text-danger border border-danger/30">{{ error }}</p>

    <!-- Empty -->
    <p v-if="!loading && tokens.length === 0 && !error"
       class="card p-8 text-sm text-fg-muted text-center">
      No API tokens yet. Issue one to allow programmatic access.
    </p>

    <!-- Token list -->
    <div v-if="tokens.length > 0" class="card overflow-hidden">
      <table class="table">
        <thead>
          <tr>
            <th>Label</th>
            <th>Scopes</th>
            <th>Created</th>
            <th>Last used</th>
            <th>Expires</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in tokens" :key="t.id">
            <td class="font-medium text-fg-strong">{{ t.label }}</td>
            <td>
              <div class="flex gap-1 flex-wrap">
                <span v-for="s in t.scopes" :key="s"
                      class="badge bg-accent-subtle text-accent text-xs">{{ s }}</span>
              </div>
            </td>
            <td class="text-xs text-fg-muted">{{ fmtDate(t.created_at) }}</td>
            <td class="text-xs text-fg-muted">{{ t.last_used_at ? fmtDate(t.last_used_at) : '—' }}</td>
            <td class="text-xs" :class="isExpired(t.expires_at) ? 'text-danger' : 'text-fg-muted'">
              {{ t.expires_at ? fmtDate(t.expires_at) : 'never' }}
              <span v-if="isExpired(t.expires_at)" class="ml-1 text-danger">(expired)</span>
            </td>
            <td class="text-right whitespace-nowrap">
              <div class="flex items-center justify-end gap-1">
                <button class="btn-ghost text-xs" @click="openRename(t)">
                  <PencilIcon class="h-4 w-4" />
                </button>
                <button class="btn-ghost text-xs text-danger" @click="confirmDelete(t)">
                  <TrashIcon class="h-4 w-4" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Minted token display (once) -->
    <div v-if="minted"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="minted = null">
      <div class="card max-w-lg w-full p-6 space-y-4">
        <div class="flex items-center gap-2">
          <KeyIcon class="h-5 w-5 text-success" />
          <h2 class="text-base font-semibold text-fg-strong">Token created — copy it now</h2>
        </div>
        <p class="text-sm text-fg-muted">
          This is the only time the raw token is shown. Store it in a secrets manager or environment variable.
        </p>
        <div class="relative">
          <code class="block font-mono text-xs bg-bg-hover border border-border rounded p-3 break-all pr-10">{{ minted.token }}</code>
          <button class="absolute top-2 right-2 btn-ghost p-1" @click="copyToken" :title="copied ? 'Copied!' : 'Copy'">
            <CheckIcon v-if="copied" class="h-4 w-4 text-success" />
            <ClipboardDocumentIcon v-else class="h-4 w-4" />
          </button>
        </div>
        <div class="text-xs text-fg-muted space-y-1">
          <div><span class="font-medium text-fg">Label:</span> {{ minted.label }}</div>
          <div><span class="font-medium text-fg">Scopes:</span> {{ minted.scopes.join(', ') }}</div>
          <div v-if="minted.expires_at"><span class="font-medium text-fg">Expires:</span> {{ fmtDate(minted.expires_at) }}</div>
        </div>
        <div class="flex justify-end">
          <button class="btn-primary" @click="minted = null">Done</button>
        </div>
      </div>
    </div>

    <!-- Create modal -->
    <div v-if="showCreate"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="closeCreate">
      <form class="card max-w-md w-full p-6 space-y-4" @submit.prevent="submitCreate">
        <h2 class="text-base font-semibold text-fg-strong">Issue API token</h2>

        <div class="space-y-1">
          <label class="text-sm font-medium text-fg">Label <span class="text-danger">*</span></label>
          <input v-model="createForm.label" type="text" required maxlength="64"
                 placeholder="e.g. grafana-integration" class="input w-full" />
        </div>

        <div class="space-y-2">
          <label class="text-sm font-medium text-fg">Scopes</label>
          <div class="space-y-1">
            <label v-for="s in SCOPES" :key="s.value" class="flex items-start gap-2 text-sm cursor-pointer">
              <input type="checkbox" :value="s.value" v-model="createForm.scopes" class="mt-0.5 rounded border-border" />
              <span>
                <span class="font-mono text-xs text-accent">{{ s.value }}</span>
                <span class="text-fg-muted ml-1">— {{ s.desc }}</span>
              </span>
            </label>
          </div>
        </div>

        <div class="space-y-1">
          <label class="text-sm font-medium text-fg">Expires <span class="text-xs text-fg-muted">(optional)</span></label>
          <input v-model="createForm.expires_at" type="datetime-local" class="input w-full" />
        </div>

        <p v-if="createError" class="text-sm text-danger">{{ createError }}</p>

        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn-secondary" @click="closeCreate">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="creating">
            {{ creating ? 'Issuing…' : 'Issue token' }}
          </button>
        </div>
      </form>
    </div>

    <!-- Rename modal -->
    <div v-if="renameTarget"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="renameTarget = null">
      <form class="card max-w-md w-full p-6 space-y-4" @submit.prevent="submitRename">
        <h2 class="text-base font-semibold text-fg-strong">Rename token</h2>
        <div class="space-y-1">
          <label class="text-sm font-medium text-fg">Label</label>
          <input v-model="renameLabel" type="text" required maxlength="64" class="input w-full" />
        </div>
        <p v-if="renameError" class="text-sm text-danger">{{ renameError }}</p>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn-secondary" @click="renameTarget = null">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="renaming">
            {{ renaming ? 'Saving…' : 'Save' }}
          </button>
        </div>
      </form>
    </div>

    <!-- Delete confirm -->
    <div v-if="deleteTarget"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="deleteTarget = null">
      <div class="card max-w-md w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Revoke token?</h2>
        <p class="text-sm text-fg-muted">
          <strong class="text-fg">{{ deleteTarget.label }}</strong> will be immediately invalidated.
          Any script using it will start getting 401 errors.
        </p>
        <div class="flex justify-end gap-2">
          <button class="btn-secondary" @click="deleteTarget = null">Cancel</button>
          <button class="btn-danger" :disabled="deleting" @click="doDelete">
            {{ deleting ? 'Revoking…' : 'Revoke' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ArrowPathIcon, CheckIcon, ClipboardDocumentIcon, InformationCircleIcon,
  KeyIcon, PencilIcon, PlusIcon, TrashIcon,
} from '@heroicons/vue/24/outline'
import { createToken, deleteToken, listTokens, renameToken } from '@/api/endpoints'
import type { APIToken, APITokenMinted } from '@/api/types'

const SCOPES = [
  { value: 'read',          desc: 'read-only access to all resources' },
  { value: 'write',         desc: 'create / update / delete resources' },
  { value: 'cluster:admin', desc: 'cluster management (node ops, cert rotation)' },
]

const tokens = ref<APIToken[]>([])
const loading = ref(false)
const error = ref('')

const showCreate = ref(false)
const creating = ref(false)
const createError = ref('')
const createForm = ref({ label: '', scopes: ['read', 'write'] as string[], expires_at: '' })

const minted = ref<APITokenMinted | null>(null)
const copied = ref(false)

const renameTarget = ref<APIToken | null>(null)
const renameLabel = ref('')
const renameError = ref('')
const renaming = ref(false)

const deleteTarget = ref<APIToken | null>(null)
const deleting = ref(false)

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    tokens.value = await listTokens()
  } catch (e: unknown) {
    error.value = (e as Error).message ?? 'Failed to load tokens'
  } finally {
    loading.value = false
  }
}

function openCreate() {
  createForm.value = { label: '', scopes: ['read', 'write'], expires_at: '' }
  createError.value = ''
  showCreate.value = true
}

function closeCreate() {
  showCreate.value = false
}

async function submitCreate() {
  creating.value = true
  createError.value = ''
  try {
    const result = await createToken(
      createForm.value.label,
      createForm.value.scopes,
      createForm.value.expires_at || undefined,
    )
    tokens.value.unshift(result)
    closeCreate()
    minted.value = result
  } catch (e: unknown) {
    createError.value = (e as Error).message ?? 'Failed to create token'
  } finally {
    creating.value = false
  }
}

async function copyToken() {
  if (!minted.value) return
  await navigator.clipboard.writeText(minted.value.token)
  copied.value = true
  setTimeout(() => { copied.value = false }, 2000)
}

function openRename(t: APIToken) {
  renameTarget.value = t
  renameLabel.value = t.label
  renameError.value = ''
}

async function submitRename() {
  if (!renameTarget.value) return
  renaming.value = true
  renameError.value = ''
  try {
    const updated = await renameToken(renameTarget.value.id, renameLabel.value)
    const idx = tokens.value.findIndex(t => t.id === updated.id)
    if (idx >= 0) tokens.value[idx] = updated
    renameTarget.value = null
  } catch (e: unknown) {
    renameError.value = (e as Error).message ?? 'Failed to rename'
  } finally {
    renaming.value = false
  }
}

function confirmDelete(t: APIToken) {
  deleteTarget.value = t
}

async function doDelete() {
  if (!deleteTarget.value) return
  deleting.value = true
  try {
    await deleteToken(deleteTarget.value.id)
    tokens.value = tokens.value.filter(t => t.id !== deleteTarget.value!.id)
    deleteTarget.value = null
  } catch (e: unknown) {
    error.value = (e as Error).message ?? 'Failed to revoke token'
  } finally {
    deleting.value = false
  }
}

function fmtDate(s: string | null | undefined): string {
  if (!s) return '—'
  return new Date(s).toLocaleDateString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  })
}

function isExpired(s: string | null | undefined): boolean {
  if (!s) return false
  return new Date(s) < new Date()
}

onMounted(refresh)
</script>
